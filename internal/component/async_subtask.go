// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/scttfrdmn/burroughs/internal/interp"
)

// gate:async slice-1 increment 2a-i-B: the async `canon lower`'s BLOCKING arm — the subtask substrate and
// the per-instance async handle table.
//
// When an async-lowered import's callee does not resolve inline, `canon_lower` registers a subtask in the
// instance handle table and returns `[state | (subtaski<<4)]` (definitions.py:2235–2251). The guest then
// creates a waitable set (`waitable-set.new`), joins the subtask to it (`waitable.join`), and blocks on
// `waitable-set.wait` until the subtask resolves and delivers a `(SUBTASK, subtaski, state)` event
// (async_waitset.go, increment 2a-i-B-2). Subtasks and waitable-sets share ONE index space per instance —
// the model's `inst.handles` — so a `subtaski` and an `si` never collide.

// subtaskState mirrors definitions.py Subtask.State (def:801–806). STARTING/STARTED/RETURNED are the
// non-cancel lifecycle; the two CANCELLED states (3, 4) land with subtask.cancel (increment 4). Which
// CANCELLED state a cancel produces is chosen by the subtask's state at the time it resolves: STARTING ->
// CANCELLED_BEFORE_STARTED, STARTED -> CANCELLED_BEFORE_RETURNED (async_lower.go's onResolve).
type subtaskState uint32

const (
	subtaskStarting                subtaskState = 0
	subtaskStarted                 subtaskState = 1
	subtaskReturned                subtaskState = 2
	subtaskCancelledBeforeStarted  subtaskState = 3
	subtaskCancelledBeforeReturned subtaskState = 4
)

// subtask is a pending async-lowered call. The blocking arm registers it in the instance table (which
// assigns `index`); the impl holds the resolver (onResolve, in async_lower.go) that later flips it to
// RETURNED, marks `resolved`, and — if the subtask has been joined to a waitable set — wakes the set's
// waiters. `delivered` guards against a resolved subtask's event being taken twice. All fields are guarded
// by the owning asyncHandles' mutex (resolution can fire on the impl's goroutine while a guest agent is
// parked in waitable-set.wait).
type subtask struct {
	state                 subtaskState
	index                 int          // this subtask's handle index (the `subtaski` the event carries)
	resolved              bool         // set by onResolve
	delivered             bool         // set when the SUBTASK event has been taken by a waitable-set.wait
	set                   *waitableSet // the set it is joined to, if any (nil until waitable.join)
	cancellationRequested bool         // set by subtask.cancel before it invokes onCancel
	onCancel              func()       // the impl's request_cancellation, captured at lower (async_lower.go)
	// resolveWake is closed by `onResolve` when this subtask resolves, so a **sync** `subtask.cancel`
	// can wait for the resolution the model makes it wait for (grave #892, `definitions.py` def:2428:
	// `thread.wait_until(subtask.resolved)`).
	//
	// # Why the subtask needs its own channel rather than the set's
	//
	// The waitable set's `wake` is the obvious candidate and is unavailable here by construction:
	// `subtask.cancel` **traps** if the subtask is joined to a set (def:2421, and the trap is already
	// implemented), so on this path there is no set to signal through. A subtask being cancelled is
	// exactly a subtask with no set.
	//
	// Closed rather than sent on, and nil-once-closed, for `liftTask.cancelWake`'s reasons: a close is
	// seen by every waiter and cannot be missed by one that arrives late, and nilling it stops a
	// re-select from spinning on an already-closed channel.
	resolveWake chan struct{}
}

// asyncHandles is a component instance's async handle table — subtasks and waitable-sets in one index
// space, index 0 a reserved nil sentinel so the first add returns 1 (matching the reference model's Table
// and the oracle's `subtaski=1`). The mutex guards the whole table AND the subtask/waitable-set state it
// holds: resolution runs on the impl's goroutine while a sibling agent is parked in waitable-set.wait, so
// the arming write and the parked read are synchronized here rather than per-object.
type asyncHandles struct {
	mu      sync.Mutex
	entries []any // entries[0] is the reserved nil sentinel; each is *subtask or *waitableSet
	// lift is the CURRENT async (callback) lift task — the slot a built-in like `task.return` acts
	// through. Set on every entry to a callee or a callback and restored to the previous value on exit
	// (#871), so a built-in always acts on the task whose code is running.
	//
	// **It is nil while a task is parked**, which is correct: between a WAIT return and the next
	// re-entry, none of that task's code is running and there is nothing for a built-in to act on.
	//
	// This said "per agent" until #857's recon measured otherwise: `asyncHandles` is built at a single
	// call site (`walkComponent`), so one slot serves the whole instance. The prose claimed something
	// NARROWER than the code enforced, which would have told a reader two agents could each hold a lift.
	// Per-agent is what #869 makes true, and it is the scope the Canonical ABI wants: several async-lifted
	// tasks may run concurrently in one instance, each built-in acting on the task calling it.
	lift *liftTask

	// liftsInFlight counts the lift tasks this instance hosts — **a count, not a flag** (#869).
	//
	// # Why a count, and why it is not the thing that bounds entry
	//
	// It was a bool until #869, asserting at most one lift per instance. The Canonical ABI allows several
	// async-lifted tasks to be in flight in one instance concurrently, which is what #771 exists to
	// unlock, so the bound moved: **several may be in flight, one may be entered.** The count is kept so
	// teardown is symmetric and so a leak is visible, not to refuse anything.
	//
	// It was a bool and `lift != nil` before that (#871 split them), and the lesson repeats one level up:
	// each time a new state becomes expressible, a field that meant two things has to give one of them up.
	// Here "in flight" and "entered" come apart, exactly as "exists" and "running" did at the first park.
	liftsInFlight int

	// entrySem is a capacity-1 semaphore serializing async-lift ENTRIES — one guest execution at a time
	// in this instance, which is what makes the single `lift` slot provably the entered task's. See
	// enterTask for why entries wait rather than refuse.
	entrySem chan struct{}

	// cancellable holds every lift task this instance currently hosts, which is what a cancellation
	// request is aimed at (ADR 0094). It is a SET and not the `lift` slot, because those answer
	// different questions: `lift` is the *entered* task, and it is **nil while a task is parked** —
	// which is precisely when a host cancellation is interesting. Cancelling only the entered task
	// would be able to cancel a task that is running and unable to cancel one that is waiting, the exact
	// inverse of what the capability is for.
	//
	// Keyed by pointer because a lift task has no index: it is not in the handle table. Added where
	// `liftsInFlight` is incremented and removed in the same deferred teardown, so the set and the count
	// have one lifetime and a leak in either is visible as a disagreement between them.
	cancellable map[*liftTask]struct{}
}

// requestCancelAll requests cancellation of every lift task this instance currently hosts, and is the
// internal trigger ADR 0094 builds the mechanism behind.
//
// # Why this is unexported, and what it implements
//
// The model's host entry point is a per-call `OnCancel` handed back by the lift — `Store.invoke` returns
// it (def:510-518). **Burroughs cannot copy that shape**: `Invoke` blocks until the task resolves, so a
// trigger it returned would arrive when there is nothing left to cancel. Go's inward form is a
// `context.Context` passed in.
//
// **That surface is DECIDED, not open**: ADR 0085 amendment 1, stamped by Scott on 2026-10-02, sets
// `Component.Call(ctx, name, args...)` and makes cancelling the context cancel the in-flight task. This
// comment said it was *"#880's subject and public API surface, so it is Scott's and not this slice's"* —
// which escalated a settled question, because I checked the model and the engine and not the project's
// own decision record. **Decided and unimplemented is a different state from undecided**, and this
// trigger is the mechanism that decision's implementation drives rather than a stand-in for a missing
// one. It lands with [ADR 0085]'s surface, amendment 1's own slice 3, now [#858].
//
// # Why "all" rather than one
//
// A lift task has no embedder-facing identity **yet** — `Component.Call(ctx, …)` gives each call its own
// context, so the per-call subject arrives with the surface. Until then the trigger takes the only
// subject available: the instance. A per-call trigger is then a narrowing of this, not a rewrite.
//
// [ADR 0085]: ../../docs/decisions/0085-the-public-component-api-surface-a-new-component-value-type-resource-handles-first-class-and-wit-typed-constructors.md
// [#858]: https://github.com/scttfrdmn/burroughs/issues/858
//
// Returns the first refusal, so a request against an instance with nothing running is observable rather
// than silently successful — see ErrCancelNotRunning for why that is a refusal and not a no-op.
func (h *asyncHandles) requestCancelAll() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.cancellable) == 0 {
		return fmt.Errorf("%w: this instance hosts no lift task", ErrCancelNotRunning)
	}
	var first error
	for t := range h.cancellable {
		if err := t.requestCancelLocked(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// enterTask makes t the current lift task and returns the previous one, which the caller MUST restore.
// Paired with leaveTask around every entry to a callee or a callback (#871).
//
// A built-in must act on the task whose code is running, and in the stackless model that changes several
// times within one `invokeAsyncLiftWith`. The pair is two methods rather than one deferred closure because
// the loop restores at points that are not function exits.
//
// # Entries SERIALIZE on a semaphore; they are not refused (#869)
//
// Measured (`TestConcurrentHostCallsShareOneThreadID`): every `Instance.Invoke` runs on `&in.host`, one
// engine thread per instance, so **two concurrent host callers present the same `Thread()`**. That ruled
// out the per-agent map #869 was registered to build — it would have collapsed two tasks into one entry
// and crossed their results.
//
// **A first attempt refused an occupied slot, and running it showed why that is wrong.** With one engine
// thread, two concurrent callers *necessarily* contend: the first is inside `inst.Invoke` with the guest
// running, and the second arrives while that entry is live. Refusing there turned the concurrency this
// slice exists to permit into a trap — the acceptance arm came back with one caller trapped and the other
// parked alone, arrivals=1, so the rendezvous could never close. Contention is the normal state, not the
// defect.
//
// So an entry **waits** for the slot, unconditionally and with no bound. The semaphore is held across the
// whole entry — set the slot, invoke, clear, release — and it is released around the **park**, which is
// the model's own `wait_from_callback` (def:781-792, where readiness requires `exclusive_thread is None`)
// and what makes two tasks able to interleave at all.
//
// # It is held across a blocking host call too, and that is a GUEST-INVARIANT guarantee
//
// An earlier version released it for the duration of a §5 excursion, on the ground that the engine thread
// is free there so a sibling may as well run. **Reverted** (chair's review of #882): the model holds
// `exclusive_thread` across a callback-lifted task's whole entry (`needs_exclusive`, def:417; taken in
// `enter_implicit_thread`, def:434-437; released only in `exit_implicit_thread` or around the park), and
// that is not merely a scheduling choice — **it is the guarantee that no other task's guest code runs in
// the instance while this task's guest frame is live.**
//
// A correct guest may depend on it. A Rust guest holding a `RefCell` borrow, or its executor's state,
// across a blocking import must not have another task's callback run in between. Releasing during the
// excursion permits exactly that, so a correct guest could panic here while running fine against the
// reference. The committed guests do not exercise it, which is why no witness caught it.
//
// # Why there is no bound, and what replaces the one there was
//
// The bound existed to catch a lift re-entering itself, which a plain semaphore would deadlock on rather
// than trap. **That case is unreachable**, searched rather than assumed: `CanonCaller`'s only guest-entry
// method is the depth-budgeted `Realloc` — no `Invoke`, no instance accessor, which is §5 H-2's
// "enforced by absence" — and no host impl in this engine captures an `*Instantiated` or
// `*interp.Instance`, so nothing can call back into `invokeAsyncLiftWith`. A bound whose only subject
// cannot occur is a mechanism with no consumer, and it cost a false trap: ordinary contention expired it
// and the message blamed self-re-entry.
//
// So contention waits, as the model's backpressure does (def:424-430: no trap, no bound). The wait
// terminates because it is bounded by the holder's own progress, and the holder's park is bounded. The
// one case it does not cover is a holder blocked forever in a host import — which is the limitation
// `interp` already documents for H-3 (*"a source that never returns holds Close until it does"*), so this
// inherits a property the engine has rather than adding one. [#880] is where the call's context ends both.
//
// [#880]: https://github.com/scttfrdmn/burroughs/issues/880
func (h *asyncHandles) enterTask(t *liftTask) (prev *liftTask) {
	h.entrySem <- struct{}{}
	h.mu.Lock()
	prev = h.lift
	h.lift = t
	h.mu.Unlock()
	return prev
}

// leaveTask restores the current lift task to prev — the value enterTask returned — and releases the
// entry semaphore, admitting whichever caller is waiting.
//
// Called only on the paths where enterTask SUCCEEDED: `enterAndInvoke` returns early without deferring
// this when the entry was refused, so the release is never unpaired.
func (h *asyncHandles) leaveTask(prev *liftTask) {
	h.mu.Lock()
	h.lift = prev
	h.mu.Unlock()
	<-h.entrySem
}

// pendingEventLocked delivers a resolved subtask's (SUBTASK, subtaski, state) event once, mirroring the
// model's subtask_event closure (def:2243–2246) — the event carries the subtask's live index and state.
// subtask satisfies the waitable interface (async_waitset.go). The caller holds asyncHandles.mu.
func (st *subtask) pendingEventLocked() (event, bool) {
	if st.resolved && !st.delivered {
		st.delivered = true
		return event{code: eventSubtask, p1: uint32(st.index), p2: uint32(st.state)}, true
	}
	return event{}, false
}

// resolveLocked moves this subtask to a terminal state and wakes everything that could be waiting on it:
// a waitable-set waiter through the set's channel, and a **sync `subtask.cancel`** through the subtask's
// own. Caller holds h.mu.
//
// # Why this is one method and not two call sites
//
// `onResolve` has two arms — the cancel arm picking CANCELLED_BEFORE_{STARTED,RETURNED}, and the result
// arm picking RETURNED — and both did the same three things inline. Adding the sync-cancel wake to both
// would have made two mirrored copies of one operation, which is **grave #885's shape**: the duplication
// is the defect and a missing case in one copy is its symptom. So the shared part moved here first and
// the arms keep only what differs between them, which is the state.
func (st *subtask) resolveLocked(state subtaskState) {
	st.state = state
	st.resolved = true
	if st.set != nil { // wake any agent parked on the set this subtask was joined to
		st.set.signalLocked()
	}
	if st.resolveWake != nil { // wake a sync subtask.cancel waiting for exactly this (grave #892)
		close(st.resolveWake)
		st.resolveWake = nil
	}
}

// joinTo records the set this subtask belongs to, so onResolve can wake its waiters.
func (st *subtask) joinTo(s *waitableSet) { st.set = s }

// currentSet reports the set this subtask is joined to, so `waitable.join` can remove it before re-joining.
func (st *subtask) currentSet() *waitableSet { return st.set }

func newAsyncHandles() *asyncHandles {
	return &asyncHandles{
		entries:     []any{nil},
		entrySem:    make(chan struct{}, 1),
		cancellable: make(map[*liftTask]struct{}),
	}
}

// addLocked registers v and returns its index (>= 1). The caller holds mu.
//
// Receiver renamed `t` -> `h` for consistency with every other method on this type (revive
// receiver-naming). It was the only `t`, and it passed lint until #871 added `enterTask`/`leaveTask`
// above it — which made `h` the established name and this the outlier.
func (h *asyncHandles) addLocked(v any) int {
	h.entries = append(h.entries, v)
	return len(h.entries) - 1
}

// removeLocked clears the entry at index i, mirroring the model's `HandleTable.remove` (def:664-668). The
// caller holds mu.
//
// The slot is **nil'd in place and not spliced out**, because every live handle the guest holds is an index
// into this slice: removing an element would silently renumber every handle above it. The model's own
// `remove` does the same (`self.array[i] = None`) and keeps a free list; this engine does not reuse slots
// yet, so the nil is the whole of it — a reused index would hand a guest a stale handle's identity, which
// is a decision to take when something needs the compaction and not before.
func (h *asyncHandles) removeLocked(i uint32) {
	if i == 0 || int(i) >= len(h.entries) {
		return
	}
	h.entries[i] = nil
}

// packSubtaskWait encodes `canon_lower`'s blocking return `[state | (subtaski<<4)]` (def:2251). The model
// asserts 0 <= state < 2^4 and 0 < subtaski < 2^28, so the two fields never overlap.
func packSubtaskWait(state subtaskState, subtaski int) int32 {
	return int32(uint32(state) | uint32(subtaski)<<4)
}

// subtaskCancel implements `canon subtask.cancel` (0x06, definitions.py:2414). It requests cancellation of a
// not-yet-resolved subtask and invokes the impl's cancel handler (the onCancel captured at lower, inert
// since 2a-i-B-1 until this increment). If the callee resolves during the cancel, the subtask reaches a
// CANCELLED state — CANCELLED_BEFORE_STARTED if it had not started, CANCELLED_BEFORE_RETURNED if it had —
// and the state is returned; otherwise BLOCKED.
//
// # The two forms are different functions, and treating them as one crashed guests (grave #892)
//
// This comment used to say: *"The model yields here (thread.yield_) to let the single-threaded callee run
// and observe the request; Burroughs has no yield — a callee runs on its own goroutine — so an unresolved
// cancel simply returns BLOCKED and the guest awaits the SUBTASK event, as it does for any unresolved
// subtask (a substrate mapping like per-caller context, ADR 0050, not a transliteration)."*
//
// **It described the model's ASYNC arm and applied it to both forms.** `canon_subtask_cancel(async_, i)`
// takes the flag, and def:2426-2433 branches on it: the sync form **waits**
// (`thread.wait_until(subtask.resolved)`) and only the async form yields and may return BLOCKED. The
// substrate-mapping argument was sound for the arm it described and was never checked against the other,
// which existed in the bytes the whole time — `async?` was decoded and thrown away.
//
// The consequence was not cosmetic. `wit-bindgen`'s drop glue is **synchronous** (it runs in `Drop`,
// which cannot await), so it uses the sync form, never expects BLOCKED, and meets it in
// `in_progress_update`'s `other => panic!("unknown code {other:#x}")` — `unreachable` in wasm. A
// conforming guest crashed.
//
// `asyncForm` now selects: wait (bounded, see `subtaskCancelBound`) or return BLOCKED.
//
// onCancel is invoked WITHOUT the table lock held: it may call onResolve, which takes the lock, so
// holding it here would deadlock — the same discipline the blocking arm uses for the impl and its
// resolver.
// beginSubtaskCancel is `subtask.cancel`'s guarded prologue: it validates the handle, applies the model's
// three traps, records the cancellation request, and arms the resolution wake — **all under one balanced
// `Lock`/`Unlock` pair in a single block.**
//
// # Why it is a separate function
//
// It was inline, with the table lock taken once at the top and released on **five** different paths. That
// is the shape that breeds a missed unlock, and it also defeated
// `TestNoEngineLockIsHeldAcrossAChannelOperation`: with conditional unlocks nested in `if`s and a
// `switch`, the control cannot pair them, so it reported the sync wait's channel receive as being inside
// a critical section that was in fact already released. Rather than exempt the function — *"an exemption
// inherits none of this control's lessons"* — the lock was made balanced and local, which is the shape
// `awaitEvent` already uses and the one the control can actually verify.
//
// The second return is the **inline** answer: non-nil when the subtask had already resolved before the
// cancel, in which case its state is the result and there is nothing to wait for (`get_pending_event`).
func (h *asyncHandles) beginSubtaskCancel(i uint32, asyncForm bool) (
	st *subtask, inlineState *subtaskState, wake chan struct{}, onCancel func(), err error,
) {
	h.mu.Lock()
	defer h.mu.Unlock()

	st, ok := handleAt[*subtask](h, i)
	if !ok {
		return nil, nil, nil, nil, fmt.Errorf("component: subtask.cancel: handle %d is not a subtask", i)
	}
	// The model's three traps, in its order (def:2419-2421).
	switch {
	case st.delivered:
		return nil, nil, nil, nil, &interp.Trap{
			Reason: fmt.Sprintf("subtask.cancel: subtask %d already resolve-delivered", i),
		}
	case st.cancellationRequested:
		return nil, nil, nil, nil, &interp.Trap{
			Reason: fmt.Sprintf("subtask.cancel: subtask %d already has a cancellation requested", i),
		}
	case st.set != nil:
		return nil, nil, nil, nil, &interp.Trap{
			Reason: fmt.Sprintf("subtask.cancel: subtask %d is joined to a waitable set", i),
		}
	}
	if st.resolved {
		state := st.state
		st.delivered = true
		return st, &state, nil, nil, nil
	}
	st.cancellationRequested = true
	// Arm the resolution wake BEFORE the lock is released and before `onCancel` runs. An impl that
	// resolves on another goroutine the instant it is asked would otherwise close a channel that did not
	// exist yet, and the wait would then wait for a resolution that had already happened — the
	// lost-wakeup shape. Only the sync form waits, so only it needs the channel.
	if !asyncForm {
		st.resolveWake = make(chan struct{})
	}
	return st, nil, st.resolveWake, st.onCancel, nil
}

func subtaskCancel(h *asyncHandles, asyncForm bool) interp.CanonFunc {
	return func(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("component: subtask.cancel: got %d args, want (i)", len(args))
		}
		i := uint32(args[0].Bits)
		st, inlineState, wake, onCancel, err := h.beginSubtaskCancel(i, asyncForm)
		if err != nil {
			return nil, err
		}
		if inlineState != nil {
			// Already resolved before the cancel — deliver its state (`get_pending_event`).
			return []interp.Value{interp.I32(int32(*inlineState))}, nil
		}
		if onCancel != nil {
			onCancel() // may call onResolve (which locks); must run without the table lock held
		}

		h.mu.Lock()
		resolvedNow := st.resolved
		h.mu.Unlock()

		// **The wait happens with NO lock held, and the shape is not a style choice.**
		//
		// A first version read the flag, unlocked, waited and re-locked all inside one
		// `h.mu.Lock()`-opened block. It was correct — the unlock preceded the receive — and
		// `TestNoEngineLockIsHeldAcrossAChannelOperation` refused it anyway, reporting a channel receive
		// inside the critical section opened by that `Lock`. The control reads the structure rather than
		// tracing a conditional unlock, and **that is the right trade**: §4 B-MM-3 forbids holding an
		// engine lock across a guest resume, a `close` on a release channel *is* a resume, and a rule that
		// could be satisfied by an argument about control flow is a rule the next edit falsifies silently.
		//
		// Its own message says what to do: *"If the lock is genuinely outside the hazard, narrow the rule
		// … do not add a name to a list, because an exemption inherits none of this control's lessons."*
		// So the code was restructured to make the premise visibly hold, rather than exempted. It is also
		// simpler — one read, one wait, one re-lock, instead of a lock/unlock/lock dance.
		if !resolvedNow && !asyncForm {
			// **The SYNC form waits** (grave #892, `definitions.py` def:2426-2428):
			//
			//	if not subtask.resolved():
			//	  if not async_:
			//	    thread.wait_until(subtask.resolved)
			//	  else:
			//	    thread.yield_()
			//
			// Burroughs returned BLOCKED here for **both** forms, because the `async?` operand was
			// decoded and discarded. That is a shipped divergence that **crashes conforming guests**:
			// `wit-bindgen`'s drop glue is synchronous — it runs in `Drop`, which cannot await — so it
			// uses this form, never expects BLOCKED, and meets it in `in_progress_update`'s
			// `other => panic!("unknown code {other:#x}")`, which is `unreachable` in wasm.
			//
			// The wait is **bounded**, for `liftParkBound`'s reason one level out: an impl that never
			// resolves must end in a verdict rather than a hang. The model has no bound because it has no
			// real time; this engine does, and a wait with no bound is a hang wearing a spec citation.
			// **Inside `c.Blocking`, which is §5 H-1 and not a wrapper for tidiness.**
			//
			// A first version waited in a bare `select`. It was correct about *what* it waited for and
			// wrong about *who* was waiting: this is a canon function, so the calling agent is in guest
			// execution, and for up to `subtaskCancelBound` that agent would be running host code
			// **without being marked blocked**. It therefore reaches no safepoint and is not excused as
			// blocked, so a stop-the-world that begins during a cancellation stalls for the whole wait —
			// Phase 4 clause 3's GC drives STW through cooperative safepoints.
			//
			// `Blocking` marks the agent blocked for `fn`'s duration, so a concurrent `Stop` sees it at a
			// safepoint and **only this agent waits** (H-1: siblings run). `waitableSetWait` already did
			// exactly this; the defect was that this function ignored its caller — its parameter was `_`,
			// which is the shape that made the omission invisible.
			//
			// The return value carries the expiry out of the excursion rather than being handled inside,
			// because `Blocking`'s own contract is that realloc and lowering must not run inside `fn`.
			if werr := c.Blocking(func() error {
				select {
				case <-wake:
					return nil
				case <-time.After(subtaskCancelBound):
					return fmt.Errorf("%w: subtask %d did not resolve within %s of its cancellation — "+
						"the host impl's cancel handler never resolved it", ErrSubtaskCancelExpired, i,
						subtaskCancelBound)
				}
			}); werr != nil {
				return nil, werr
			}
		}
		// **No `defer` in this function, and that is the control's requirement rather than a preference.**
		// `TestNoEngineLockIsHeldAcrossAChannelOperation` says so in its own words: *"A deferred `Unlock`
		// anywhere in the function keeps the whole-function interval, unnarrowed"* — because `defer`
		// cannot be ordered lexically against a channel operation, so a function with one is treated as
		// locked throughout. With a `defer` here, the sync wait above sat inside that interval no matter
		// how the rest was arranged, which is why two earlier restructurings did not satisfy it.
		//
		// So the epilogue reads and mutates under one balanced `Lock`/`Unlock` in this statement list and
		// branches after releasing. Taking the decision out from under the lock is also the better shape
		// on its own terms: nothing between the unlock and the return touches shared state.
		h.mu.Lock()
		blocked := !st.resolved
		state := st.state
		if !blocked {
			st.delivered = true
		}
		h.mu.Unlock()

		if blocked {
			// Reachable on the **async** form only: the model's async arm yields once and then returns
			// BLOCKED if the subtask still has not resolved (def:2430-2433). The guest awaits the event.
			// On the sync form the wait above either saw the resolution or returned the named expiry.
			bits := uint32(asyncBlocked)
			return []interp.Value{interp.I32(int32(bits))}, nil
		}
		return []interp.Value{interp.I32(int32(state))}, nil
	}
}

// subtaskCancelBound bounds the SYNC `subtask.cancel` wait (grave #892).
//
// The model waits unconditionally (`thread.wait_until(subtask.resolved)`) because it has no real time and
// a non-resolving impl is outside what it describes. This engine has both, so the wait is bounded for the
// same reason `liftParkBound` is: *a wait that cannot be satisfied must end in a verdict.* An unbounded
// wait here would turn a misbehaving host impl into a hung guest with no diagnostic, which is strictly
// worse than the BLOCKED this replaces.
//
// A `var` rather than a `const` so a witness can shorten it — the expiry has to be watched firing, and a
// test that waited the real bound to see it would be a test nobody runs.
var subtaskCancelBound = 30 * time.Second

// ErrSubtaskCancelExpired is the sync `subtask.cancel` wait's bound expiring: the impl's cancel handler
// was invoked and never resolved the subtask. Named rather than returned as BLOCKED, because BLOCKED is
// the ASYNC form's answer and reusing it is exactly the conflation grave #892 is about.
var ErrSubtaskCancelExpired = errors.New("component: subtask.cancel (sync) timed out waiting for the cancellation to resolve")

// subtaskDrop implements `canon subtask.drop` (0x0d, definitions.py:2441 -> Subtask.drop def:854). It traps
// unless the subtask is resolve-delivered (Burroughs: `delivered`, set when a wait consumed the SUBTASK
// event or subtask.cancel returned the state inline), then removes it from the table. The trap is the
// scheduler-side mirror of the stream drop-mid-copy trap: dropping an undelivered resolution would leave the
// guest's completion silently unaccounted rather than erroring. The two CANCELLED terminal states behave
// exactly as RETURNED here — once delivered, drop is legal; while undelivered, it traps — so cancellation
// added new inputs to this path but no new rule.
func subtaskDrop(h *asyncHandles) interp.CanonFunc {
	return func(_ *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("component: subtask.drop: got %d args, want (i)", len(args))
		}
		i := uint32(args[0].Bits)
		h.mu.Lock()
		defer h.mu.Unlock()
		st, ok := handleAt[*subtask](h, i)
		if !ok {
			return nil, fmt.Errorf("component: subtask.drop: handle %d is not a subtask", i)
		}
		if !st.delivered {
			return nil, &interp.Trap{Reason: fmt.Sprintf("subtask.drop: subtask %d is not resolve-delivered; dropping it would silently discard its resolution", i)}
		}
		h.entries[i] = nil
		return nil, nil
	}
}
