// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
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
// So an entry **waits** for the slot. The semaphore is held across the whole entry — set the slot, invoke,
// clear, release — which makes the slot provably the entered task's, and a second caller blocks only for
// the duration of one guest execution: the loop parks *after* `inst.Invoke` returns, so the holder always
// releases before it waits for an event.
//
// # Why the wait is bounded, and what the bound catches
//
// A plain mutex would turn one case into a **deadlock** rather than a trap: a guest re-entering an
// async-lifted export from inside its own entry would wait for a semaphore it holds itself. Thread
// identity cannot distinguish that case here — the re-entrant call is on the same engine thread as every
// other — so the bound is what separates "another caller is executing" from "this caller is waiting for
// itself". Expiry is a named trap, the same principle as the park's bound.
func (h *asyncHandles) enterTask(t *liftTask) (prev *liftTask, err error) {
	select {
	case h.entrySem <- struct{}{}:
	case <-time.After(liftEntryBound):
		return nil, &interp.Trap{Reason: fmt.Sprintf(
			"async canon lift could not begin an entry within %s: another entry has held the component "+
				"instance's execution slot for longer than any single guest call should take, which is "+
				"what a lift re-entering itself looks like (one engine thread per instance, so thread "+
				"identity cannot tell that apart from ordinary contention)", liftEntryBound)}
	}
	h.mu.Lock()
	prev = h.lift
	h.lift = t
	h.mu.Unlock()
	return prev, nil
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

// joinTo records the set this subtask belongs to, so onResolve can wake its waiters.
func (st *subtask) joinTo(s *waitableSet) { st.set = s }

// currentSet reports the set this subtask is joined to, so `waitable.join` can remove it before re-joining.
func (st *subtask) currentSet() *waitableSet { return st.set }

func newAsyncHandles() *asyncHandles {
	return &asyncHandles{entries: []any{nil}, entrySem: make(chan struct{}, 1)}
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
// The model yields here (thread.yield_) to let the single-threaded callee run and observe the request;
// Burroughs has no yield — a callee runs on its own goroutine — so an unresolved cancel simply returns
// BLOCKED and the guest awaits the SUBTASK event, as it does for any unresolved subtask (a substrate
// mapping like per-caller context, ADR 0050, not a transliteration). onCancel is invoked WITHOUT the table
// lock held: it may call onResolve, which takes the lock, so holding it here would deadlock — the same
// discipline the blocking arm uses for the impl and its resolver.
func subtaskCancel(h *asyncHandles) interp.CanonFunc {
	return func(_ *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("component: subtask.cancel: got %d args, want (i)", len(args))
		}
		i := uint32(args[0].Bits)
		h.mu.Lock()
		st, ok := handleAt[*subtask](h, i)
		if !ok {
			h.mu.Unlock()
			return nil, fmt.Errorf("component: subtask.cancel: handle %d is not a subtask", i)
		}
		switch {
		case st.delivered:
			h.mu.Unlock()
			return nil, &interp.Trap{Reason: fmt.Sprintf("subtask.cancel: subtask %d already resolve-delivered", i)}
		case st.cancellationRequested:
			h.mu.Unlock()
			return nil, &interp.Trap{Reason: fmt.Sprintf("subtask.cancel: subtask %d already has a cancellation requested", i)}
		case st.set != nil:
			h.mu.Unlock()
			return nil, &interp.Trap{Reason: fmt.Sprintf("subtask.cancel: subtask %d is joined to a waitable set", i)}
		}
		if st.resolved {
			state := st.state // already resolved before the cancel — deliver its state (get_pending_event)
			st.delivered = true
			h.mu.Unlock()
			return []interp.Value{interp.I32(int32(state))}, nil
		}
		st.cancellationRequested = true
		onCancel := st.onCancel
		h.mu.Unlock()

		if onCancel != nil {
			onCancel() // may call onResolve (which locks); must run without the table lock held
		}

		h.mu.Lock()
		defer h.mu.Unlock()
		if !st.resolved {
			bits := uint32(asyncBlocked) // the callee did not resolve inline; the guest awaits the event
			return []interp.Value{interp.I32(int32(bits))}, nil
		}
		st.delivered = true
		return []interp.Value{interp.I32(int32(st.state))}, nil
	}
}

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
