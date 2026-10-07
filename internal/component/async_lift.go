// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// liftTask is the durable task an async (callback) lift runs on — the analog of the model's Task/Thread
// (canon_lift def:2096-2153). Its lifetime is the TASK's, not any single core-func call: the callback loop
// spans multiple stack-creating invocations (the callee, then each callback re-entry), and the task, its
// resolution, and its context storage persist across all of them. Teardown keys on RESOLUTION (task.return
// or cancellation), never on the loop exiting normally — a cancelled task resolves without a normal EXIT
// (#785 step 1, Scott's caution; the cancellation guest is the case a loop-exit teardown would break).
// liftState mirrors definitions.py `Task.State` (def:389-394) one-for-one, and it is an enum rather than
// the two booleans it replaces (`resolved`, `cancelled`) because the model's five states include two that
// a boolean pair can represent twice over. PENDING_CANCEL and CANCEL_DELIVERED are *distinct* — the first
// is a request nobody has told the guest about, the second is a request the guest has been handed and may
// now act on — and `task.cancel`'s precondition is the second specifically (def:494).
//
// INITIAL is kept separate from STARTED even though Burroughs enters the callee immediately, with no
// backpressure wait to sit in. The window is small and it is not empty, and the model's two
// `request_cancellation` arms (def:463-470) diverge on exactly this distinction: INITIAL is the
// before-started path, the route to status 3, which this slice REFUSES BY NAME because it has no
// reference reading (#884). A state that cannot be observed cannot be refused by name.
type liftState uint8

const (
	liftInitial         liftState = iota // Task.State.INITIAL — created, callee not yet entered
	liftStarted                          // Task.State.STARTED — running
	liftPendingCancel                    // Task.State.PENDING_CANCEL — requested, not yet delivered to the guest
	liftCancelDelivered                  // Task.State.CANCEL_DELIVERED — guest has had TASK_CANCELLED
	liftResolved                         // Task.State.RESOLVED — by task.return or by task.cancel
)

func (s liftState) String() string {
	switch s {
	case liftInitial:
		return "initial"
	case liftStarted:
		return "started"
	case liftPendingCancel:
		return "pending-cancel"
	case liftCancelDelivered:
		return "cancel-delivered"
	case liftResolved:
		return "resolved"
	}
	return fmt.Sprintf("liftState(%d)", uint8(s))
}

type liftTask struct {
	state   liftState      // mirrors Task.State; resolution (however reached) is the teardown key
	result  []interp.Value // the flat result task.return received; read at resolution
	storage [2]uint32      // the task's context (context.get/set), NOT the per-call stack's — see below

	// lifted is the result `task.return` lifted **eagerly**, when its canonopts gave it a result type to
	// lift against. nil when the export declares no result, or when the type is one the flat lift cannot
	// carry (in which case `task.return` has already refused).
	//
	// # Why eagerly, and why this field rather than lifting at resolution
	//
	// The model lifts inside `canon_task_return` (definitions.py:2336) and hands `Task.return_` an
	// already-lifted value (def:487-492). Burroughs stored the flat words and lifted them after the
	// callback loop exited, which for a `u32` is identical — the value *is* the word — and wrong for
	// anything whose payload lives in guest memory: between `task.return` and the loop's exit the guest
	// **runs again**, and it is free to reuse the buffer its `(ptr, len)` named. The late lift then reads
	// whatever the guest put there second.
	//
	// So this is not an optimisation and not a tidying. It is the difference between reading the guest's
	// result and reading the guest's next scratch write.
	lifted *canon.Value

	// cancelledResolution distinguishes the two ways a task reaches `liftResolved`, which the model
	// expresses as the *payload* of `on_resolve`: `return_` passes a result (def:490), `cancel` passes
	// `None` (def:497). Burroughs cannot read that distinction off `result`, because a guest whose export
	// returns nothing resolves with an empty result legitimately — `export go: async func()` is exactly
	// grave #885's artefact. So the discriminant is explicit rather than inferred from an ambiguous nil.
	cancelledResolution bool

	// cancelled WAS a field here, with a comment saying "**Nothing sets it today**: Burroughs has no way
	// to cancel a task". That was true when #864 wrote it and is false now — ADR 0094 is what changed it,
	// and the sentence is repaired rather than left standing, because a comment telling the next reader
	// the tree is in a state it is not is the foreclosing-words shape. The state it recorded is now
	// `liftCancelDelivered`.

	// # The re-entry state (#871), which exists because the guest's frame does not
	//
	// When the callee or a callback returns WAIT or YIELD, the guest's core frame is **gone** — that is
	// what returning a dispatch code instead of blocking means. So everything the next entry needs lives
	// here, on the task, rather than on the Go stack of the call that parked.
	//
	// This is the field set that makes the stackless model different in kind from the engine's other
	// park: `waitableSetWait` keeps the guest's frame alive on the Go stack inside a `c.Blocking`
	// excursion, so it needs no durable state at all. The two cannot share a mechanism.
	//
	// `cb` lives on the TASK and not only on the compFunc because #869 gives one instance several
	// concurrent lift tasks, from different exports, each with its own callback. Keyed per task now, it
	// needs no second move then.
	cb coreDef // the callback core func (cn.Opts.Callback), threaded through at walk time

	// waitSet is the waitable-set index the guest named in its WAIT return, kept for the diagnostic when
	// the park's bound expires: the index is what tells a reader WHICH set never produced an event, and
	// by that point the packed return it came from is several entries behind.
	waitSet uint32

	// ctx is the call's context (#880, #858). It is the embedder's cancellation channel, and it reaches
	// the two places this task can wait: `awaitEvent`'s park and `enterTask`'s entry semaphore.
	//
	// # Why a field on the task rather than a parameter everywhere
	//
	// The task is already threaded to both waits — `awaitEvent` takes it, and `enterTask` is called with
	// it — so carrying the context here reaches them without a signature change at every intermediate
	// hop. It is per-call by construction, which a field on the shared `compFunc` would not be (#869's
	// lesson: a `compFunc` is shared by every concurrent caller of its export).
	//
	// **Read through `context()`, never directly.** `newLiftTask` substitutes `context.Background()`,
	// but a struct literal — which several tests legitimately use to put a task in a specific state —
	// leaves this nil, and a nil `context.Context` panics on `Done()`.
	//
	// That is not hypothetical: it cost a 60-second hang. The panic fired *inside* a critical section, so
	// the table mutex was never released and the next goroutine to want it deadlocked — the test binary
	// reported a timeout rather than the nil deref, and the stack had to be read to find the real cause.
	// **An invariant maintained by every constructor is an invariant one literal breaks**; maintained at
	// the single read site, it cannot be.
	ctx context.Context

	// cancelWake wakes a park that is waiting on this task's cancellation (ADR 0094). It is CLOSED, not
	// sent on, so every parked selector sees it and a request that arrives before any park is not lost —
	// the park re-checks the state on entry and finds the cancel already pending.
	//
	// It is separate from the waitable set's `wake` because the two have different subjects and different
	// lifetimes: `signalLocked` closes and REPLACES the set's channel on every event, so a cancellation
	// routed through it would be indistinguishable from an event and would have to be re-armed. A
	// cancellation happens at most once per task, so a one-shot close is the whole mechanism.
	//
	// Set to nil once closed. A closed channel fires forever, so a park that re-selected on it would spin
	// instead of waiting; nil blocks, which is the correct behaviour for "there is no second
	// cancellation". Reaching that select at all requires the cancel to have been delivered already,
	// since the pass before it checks `deliverPendingCancelLocked` first.
	cancelWake chan struct{}

	// everEntered records whether this task has entered the guest at least once, and it exists for
	// exactly one decision: **whether a cancelled context may ABORT an entry.**
	//
	// Before the first entry, no guest code has run — there are no destructors to skip and nothing to
	// tell — so abandoning the task is harmless and is the model's own INITIAL arm in spirit. After it,
	// every entry must proceed, because **the cancellation is delivered THROUGH an entry**: refusing one
	// abandons a task mid-flight, leaving its destructors unrun and its resources held.
	//
	// Measured, not reasoned: with `enterTask` refusing on context for any started task, the yielding
	// guest's witness passed while the guest never received TASK_CANCELLED at all — every YIELD
	// re-entry was refused, so the call ended with `ErrCancelled` from `enterAndInvoke` and the guest's
	// own cancellation path never ran. The neuter of the top-of-loop check then did not fail the test,
	// which is how it surfaced: *a witness that passes under its own neuter is passing for another
	// reason.*
	//
	// `liftStarted` cannot substitute: `runLiftTask` sets it immediately *before* the first entry, so a
	// task queued for its first entry is already STARTED.
	everEntered bool
}

// markEntered records that this task has entered the guest at least once. Caller holds h.mu.
func (t *liftTask) markEntered() { t.everEntered = true }

// context returns the call's context, substituting `context.Background()` for a task built by a struct
// literal. See the `ctx` field for why this is the only read path.
func (t *liftTask) context() context.Context {
	if t.ctx == nil {
		return context.Background()
	}
	return t.ctx
}

// taskReturn implements `canon task.return` (0x09, definitions.py canon_task_return def:2329): it resolves
// the calling agent's CURRENT lift task with the flat result the guest passes. Bound per-instance over the
// async handle table, which holds the current lift task (set by the callback loop before it invokes the
// callee). It traps if there is no current lift task — task.return outside an async lift is a guest error,
// not a silent no-op.

// ErrCancelled is what a cancelled lift's caller gets, and it is ONE sentinel for both cancelled statuses
// by #862's ruling 1 (chat-Claude, relayed by Scott, inside what ADR 0085 amendment 1 already approved —
// so not new public surface). The mapping, committed in testdata/asynclift/CANCELLATION.md: status 3
// CANCELLED_BEFORE_STARTED and status 4 CANCELLED_BEFORE_RETURNED both return this value; status 2
// RETURNED returns its result normally, because the work completed. Statuses with no Go equivalent: none,
// recorded as a claim so a later ABI status is visibly outside the mapping rather than silently absorbed.
//
// The 3-versus-4 distinction is **deferred, not discarded** (#858): it matters to an embedder deciding
// whether a retry is safe, Burroughs can see it where a Rust guest cannot, and no consumer has asked.
// It stays addable without a break as an error TYPE wrapping this value, so `errors.Is` keeps answering.
var ErrCancelled = errors.New("component: the async task was cancelled")

// `ErrTaskCancelUnbuilt` **was here and is deleted**, across two slices that each removed one of its two
// producers.
//
// It began as `task.cancel`'s cancelled-branch refusal, while nothing in the engine could set the
// precondition. ADR 0094 built that branch, and re-pointed the value at the one refusal left: the model's
// `request_cancellation` INITIAL arm, declined because it was believed to produce `subtask.cancel` status
// 3. ADR 0094 amendment 2 (see `requestCancelLocked`) found that reason false for Burroughs and honoured
// the arm, which left the value with **no producer at all**.
//
// Deleted rather than kept, because a sentinel nothing returns is the same defect as a field nothing
// writes — which is what #864 left behind and what ADR 0094 was cleaning up. Keeping it "in case" would
// make the next reader hunt for a branch that does not exist. It was never public surface: this is
// `internal/component`, so no embedder could reference it.
//
// `ErrCancelNotRunning` is the refusal that survives, for a request against a task already past running.

// ErrCancelNotRunning refuses a host cancellation request aimed at a task that is not STARTED.
//
// **The model specifies nothing here, so this is Burroughs' decision** (ADR 0094), and it is a refusal
// rather than either of the two tempting alternatives. `request_cancellation`'s else arm *asserts*
// `state == STARTED` (def:469), so a redundant or late request is undefined for an embedder, not allowed.
//
//   - **Not a trap**, because a trap blames the guest. A host that requests cancellation while the task
//     resolves underneath it has raced a legitimate race, and nothing the guest did is wrong.
//   - **Not silently ignored**, because then "I cancelled it" and "it finished first" become the same
//     observation and the host cannot tell which happened.
//
// So it is refused with the state in the message, and it is **not idempotent**: a second request is
// refused the same way, which matches the model's assertion rather than inventing a laxer contract.
var ErrCancelNotRunning = errors.New("component: no running async task to cancel")

// requestCancelLocked is definitions.py `Task.request_cancellation` (def:463-470). Caller holds h.mu.
//
// The model's two arms diverge on INITIAL versus STARTED. **Both are honoured here, and both set
// PENDING_CANCEL** — which is what the model's own code does too (def:463-470): its INITIAL arm then
// resumes the thread so the entry path delivers the cancel, while Burroughs' delivery happens at the
// first check the lift loop or its park reaches. Same state, different carrier.
//
// # The INITIAL arm is honoured, not refused — ADR 0094 amendment 2
//
// ADR 0094 refused it with `ErrTaskCancelUnbuilt`, on the reason that it *"would produce subtask.cancel
// status 3, which has no reference reading"*. **That reason is true of the model and false of Burroughs**,
// and the difference is one ADR 0095 introduced deliberately:
//
//   - In the model, `canon_lift`'s `thread_func` calls `task.start()` — and therefore the lower's
//     `on_start` — INSIDE the lift (def:2097-2102). So a lift task still INITIAL means `on_start` has not
//     run, means the parent's subtask is still STARTING, means `on_resolve(None)` picks
//     CANCELLED_BEFORE_STARTED = 3. The reasoning holds there.
//   - In Burroughs the cross-component adapter calls `onStart()` **itself, synchronously, before the
//     child's lift task exists**, because `on_start` reads the caller's flat args and those belong to the
//     caller's frame. So the parent's subtask is already STARTED, and the terminal state is
//     CANCELLED_BEFORE_RETURNED = 4 whatever the child's lift task state is. Status 3 is unreachable on
//     this path, so honouring the request claims nothing unmeasured.
//
// And refusing had a cost that ADR 0094 mispriced: it said the parent would learn of the refusal through
// BLOCKED. Grave #892 removed BLOCKED from the sync path, so the refusal became a **30s hang** instead.
// Measured as a flaky one — 150 runs of the composed witness could not all complete — which is worse than
// a consistent failure. *A gap whose consequence has changed needs re-deciding, not re-citing.*
func (t *liftTask) requestCancelLocked() error {
	switch t.state {
	case liftStarted, liftInitial:
		t.state = liftPendingCancel
		// Wake a park, if there is one. **This is what makes the capability reachable in its main case**:
		// the task a host wants to cancel is typically parked on a set nothing will ever resolve, so a
		// state change nobody is woken by would be a cancellation that takes effect only if something
		// else happens first. Closed rather than sent on, so an unparked task loses nothing — see the
		// field's comment.
		if t.cancelWake != nil {
			close(t.cancelWake)
			t.cancelWake = nil
		}
		return nil
	default:
		return fmt.Errorf("%w: task is %s", ErrCancelNotRunning, t.state)
	}
}

// deliverPendingCancelLocked is definitions.py `Task.deliver_pending_cancel` (def:475-480): it converts a
// PENDING_CANCEL into CANCEL_DELIVERED and reports whether it did. Caller holds h.mu.
//
// The report is the whole value: the lift loop delivers TASK_CANCELLED **in place of waiting** when this
// returns true, so a false is what lets the loop park. One-shot by construction — the state moves, so a
// second call on the same request returns false.
func (t *liftTask) deliverPendingCancelLocked() bool {
	if t.state != liftPendingCancel {
		return false
	}
	t.state = liftCancelDelivered
	return true
}

// cancelLocked is definitions.py `Task.cancel` (def:494-498): it resolves a task whose cancellation has
// been DELIVERED, with no result. Caller holds h.mu.
//
// The precondition is CANCEL_DELIVERED specifically, not "cancelled somehow" — a guest that calls
// `task.cancel` on a request it has not been handed yet is as wrong as one that calls it with no
// cancellation at all, and the model traps on both (`trap_if(state != CANCEL_DELIVERED)`). That is why
// liftState keeps the two cancel states apart.
//
// # The trap has two messages for the model's one condition, and the test caught me collapsing them
//
// `trap_if(state != CANCEL_DELIVERED)` is one predicate, but it covers two situations a reader needs to
// tell apart: a task **never cancelled** (the common case, and the Canonical ABI's own named rule, which
// wasmtime spells `TaskCancelNotCancelled` — *"task.cancel called by task which has not been
// cancelled"*), and a task with a cancel **requested but not yet delivered** (reachable only in the
// window this slice opened). A first draft used the second wording for both and
// `TestTaskCancelTrapsOnAnUncancelledTask` failed, because it asserts the ABI's rule by name. It was
// right to: a shared message would have reported the rare case's cause for the common case.
func (t *liftTask) cancelLocked() error {
	switch t.state {
	case liftCancelDelivered:
		// fall through to the resolution below
	case liftPendingCancel:
		return &interp.Trap{Reason: "task.cancel called by a task whose cancellation has not been " +
			"delivered yet (requested, not handed to the guest)"}
	default:
		return &interp.Trap{Reason: fmt.Sprintf(
			"task.cancel called by a task that has not been cancelled (task is %s)", t.state)}
	}
	t.result = nil
	t.cancelledResolution = true
	t.state = liftResolved
	return nil
}

// taskCancel implements `canon task.cancel` (0x05, definitions.py canon_task_cancel def:2342).
//
// # What changed, and why the previous comment could not stay
//
// This used to be a trap and a named refusal, and its comment said so as a *general* claim: *"Burroughs
// cannot cancel anything, so **every reachable call lands on that branch**, and implementing it is
// implementing the spec rather than deferring it."* The first clause was true and ADR 0094 falsified it;
// the trap is now one of two outcomes rather than the whole implementation. Repaired rather than
// annotated — a comment that names a constraint the code no longer embodies misdirects the next reader.
//
// The trap arm is unchanged and still the spec's, which is why only its reach narrowed: wasmtime spells
// the same rule `TaskCancelNotCancelled` — *"`task.cancel` called by task which has not been cancelled"*.
//
// # Why it had to land before the parity witness
//
// `wit-bindgen` emits a `TaskCancelOnDrop` guard for EVERY async export, so both committed guests import
// `[task-cancel]` — including the `single` guest, whose `compute` has nothing to do with cancellation.
// With 0x05 unbuilt, `isBuiltAsyncBuiltin` refused it and **neither guest could instantiate**, which
// blocked #864's value-carrying call and #857's parity readings. The guests never *call* it absent a
// cancellation; they only need it bound.
func taskCancel(h *asyncHandles) interp.CanonFunc {
	return func(_ *interp.CanonCaller, _ []interp.Value) ([]interp.Value, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.lift == nil {
			// Same discipline as task.return's: outside an async lift this is a guest error, not a no-op.
			return nil, &interp.Trap{Reason: "task.cancel called with no async lift task in flight"}
		}
		return nil, h.lift.cancelLocked()
	}
}

func taskReturn(h *asyncHandles, resultT *ValType) interp.CanonFunc {
	return func(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		// **The lift happens here, before the lock and before `Task.return_`'s equivalent** — the order the
		// model uses (definitions.py:2336 lifts, then def:2337 calls `task.return_`). It reads guest memory,
		// so it must not hold `h.mu`: a read can fail, and §4 B-MM-3's rule is that a critical section
		// holds no operation that can block or reach out of the engine.
		//
		// `c` was `_` until #903, which is the same smell it is everywhere else in this package: the caller
		// is the memory, so a canon function that ignores it is a canon function that cannot touch the
		// guest's heap. Here it could not lift its own argument.
		var lifted *canon.Value
		if resultT != nil {
			v, err := liftTaskReturnValue(c, *resultT, args)
			if err != nil {
				return nil, err
			}
			lifted = &v
		}

		h.mu.Lock()
		defer h.mu.Unlock()
		if h.lift == nil {
			return nil, &interp.Trap{Reason: "task.return called with no async lift task in flight"}
		}
		// definitions.py `Task.return_` traps on an already-resolved task (def:487). Burroughs had no such
		// check, because with no cancellation a task reached RESOLVED exactly once — through here. A
		// cancelled task is resolved by `task.cancel`, so a `task.return` after it is now reachable and is
		// a guest error: the guest was told its task was cancelled and returned a value anyway.
		if h.lift.state == liftResolved {
			return nil, &interp.Trap{Reason: "task.return called on a task that is already resolved"}
		}
		h.lift.result = append([]interp.Value(nil), args...)
		h.lift.lifted = lifted
		h.lift.cancelledResolution = false
		h.lift.state = liftResolved
		return nil, nil
	}
}

// liftTaskReturnValue lifts `task.return`'s flat arguments into a component value.
//
// # Scope: what a FLAT lift can carry without a per-element load
//
// Every one-word kind — `bool`, the eight integers, `f32`/`f64`, `char` — is lifted by the codec's one
// scalar flat lift, and a `string` is a `(ptr, len)` pair whose bytes are read through the codec's one
// string lift. Everything compound — a list, a variant, a record — needs a per-element load the way
// `canon.StoreList` needs a per-element store, and no such lift exists yet (`canon.LoadListU8` is the one
// reduced case, and it reduces only because a u8 is one byte at a one-byte stride). So those refuse
// **by name** here rather than being approximated, which is the same discipline the bridge applies one
// level up.
//
// # Why every scalar, and not just the one a fixture drives
//
// This function handled `u32` alone when it landed, because `u32` is what every committed guest returns.
// That was a **regression the moment it shipped**, and guest-driven scope does not excuse it: before the
// eager lift, a non-`u32` scalar from an async *cross-component* child resolved through
// `crossComponentValue`, which has arms for bool, the eight integers and the two floats. An eagerly
// lifted value takes precedence over the flat words, so those arms became unreachable for the async path
// and a conforming child returning `bool` would have **trapped** where it used to work.
//
// No committed fixture returns one, so nothing caught it — the same blindness that let the late lift
// survive, one layer along. The arms are added because the set is *closed and small*, not because a guest
// asked: a partial switch over a complete enum is a defect waiting for its first caller.
//
// It deliberately does not build a second lift path: every indirect read goes through
// `canon.LoadStringFromRange`, which is the function the codec's own two arms call and the differential's
// string fixtures verify.
func liftTaskReturnValue(c *interp.CanonCaller, vt ValType, args []interp.Value) (canon.Value, error) {
	ct, err := canonTypeOf(vt)
	if err != nil {
		return canon.Value{}, &interp.Trap{Reason: fmt.Sprintf(
			"task.return declares a result this engine's Canonical ABI cannot lift: %v", err)}
	}

	want := func(n int) error {
		if len(args) != n {
			return &interp.Trap{Reason: fmt.Sprintf(
				"task.return for a %s result got %d flat value(s), want %d", ct.Kind, len(args), n)}
		}
		return nil
	}

	switch ct.Kind {
	// Every one-word kind — bool, the eight integers, the two floats, char — goes through the codec's
	// **one** scalar flat lift. Delegated rather than restated: `canon.LiftFlatScalar` is the function
	// `liftFlat` reaches for every scalar, so the differential against `definitions.py` over
	// `gen/cases.json` is its oracle, and these arms are verified against the model rather than against a
	// table. Restating them here is what produced the `u32`-only regression in the first place.
	case canon.KindBool, canon.KindU8, canon.KindU16, canon.KindU32, canon.KindU64,
		canon.KindS8, canon.KindS16, canon.KindS32, canon.KindS64,
		canon.KindF32, canon.KindF64, canon.KindChar:
		if err := want(1); err != nil {
			return canon.Value{}, err
		}
		// `Bits` and not `Int32()`: the lift is defined on the unsigned word (definitions.py's iterator
		// yields one, and `char`'s trap depends on it), and a 64-bit kind needs all 64 bits. Narrowing is
		// the codec's business, per kind, where `flattenType` says how wide the word is.
		v, lerr := canon.LiftFlatScalar(ct, args[0].Bits)
		if lerr != nil {
			// A `char` outside the Unicode scalar range is the reachable case, and it is a **guest fault**:
			// the model traps there (def:1343-1347), so this does too.
			return canon.Value{}, &interp.Trap{Reason: fmt.Sprintf(
				"task.return's %s result: %v", ct.Kind, lerr)}
		}
		return v, nil

	case canon.KindString:
		// Two flat words, `(ptr, byte-length)`. **Read now**, which is the whole point: the guest resumes
		// after this call and may reuse the buffer.
		if err := want(2); err != nil {
			return canon.Value{}, err
		}
		ptr := int(uint32(args[0].Int32()))
		n := int(uint32(args[1].Int32()))
		v, lerr := canon.LoadStringFromRange(guestHeap{c}, ptr, n)
		if lerr != nil {
			// A guest's own (ptr, len) failing the model's traps is a guest fault, not an engine error —
			// it traps, as `load_string_from_range` does (def:1383-1389).
			return canon.Value{}, &interp.Trap{Reason: fmt.Sprintf("task.return's string result: %v", lerr)}
		}
		return v, nil

	default:
		// Everything left is compound: list, variant (and so result/option/enum, which despecialize to
		// one), record, tuple, own/borrow. The message said "this engine lifts scalars and string" while
		// only `u32` was handled, which made it **false for every other scalar** — a refusal that
		// misdescribes its own scope sends the next reader looking for a bug that is not there. It is now
		// accurate about what remains.
		return canon.Value{}, &interp.Trap{Reason: fmt.Sprintf(
			"task.return declares a %s result; a compound value needs a per-element load the codec does "+
				"not yet expose — the mirror of the per-element store canon.StoreList takes — so this "+
				"engine lifts bool, the integers, the floats, char and string from a task.return's flat "+
				"values and refuses the rest by name", ct.Kind)}
	}
}

// task.return context storage lives on the liftTask, not the caller's stack. This corrects the slice-1
// placement (ADR 0050 / #739's per-caller judgment, recorded in async_context.go) for the async-lift case
// ONLY: a sync caller IS a stack, so its placement stays on the stack; the async-lift loop is the case that
// separates caller from stack, because it spans multiple stack-creating calls, so its storage must outlive
// any one of them or #788's context-survival invariant fails. Dated disposition, not an unremarked change
// (same form as the asyncLowerImpl and stream.write-consumer notes on #739). This is also the state the S-1
// correction was circling: a callback lift captures no continuation stack, but it carries state that
// outlives any single call, and the task is where that state lives.

// The stackless (callback) async-lift ABI — the second async guest's tier (ADR 0086's deferred
// stackless-vs-stackful choice, settled guest-driven: the Rust p3 toolchain emits
// `(canon lift ... async (callback ...))`, so that is what the engine implements; stackful stays deferred
// to a guest that stackfully lifts one). This file starts with the piece the whole dispatch turns on: the
// decode of the packed i32 the callback returns.

// callbackCode is the async-lift callback's dispatch code — the low nibble of the packed i32 the callback
// returns (definitions.py `CallbackCode`, def:2165-2169). EXIT means the task resolved; YIELD is a
// cooperative yield with no set; WAIT means the task is blocked on the waitable set whose index is packed
// in the high bits. The code is exactly what distinguishes "done" from "waiting on set si" from "yield" —
// so it is decoded and range-checked, never masked-and-assumed.
type callbackCode uint32

const (
	callbackExit    callbackCode = 0 // task resolved (the guest's `run` happy path)
	callbackYield   callbackCode = 1 // cooperative yield
	callbackWait    callbackCode = 2 // blocked on waitable set `si`
	callbackCodeMax callbackCode = callbackWait
)

// unpackCallbackResult decodes the callback's packed i32 return into its dispatch code and waitable-set
// index (definitions.py `unpack_callback_result`, def:2171-2177): `code = packed & 0xf`, and the
// waitable-set index is `packed >> 4`. A code above MAX traps — the guard a dispatch that masks instead of
// range-checks would skip, and the reason the pin covers out-of-range codes and not only the happy path.
// Pinned byte-exact against the model: fixtures.json `callback_result`.
func unpackCallbackResult(packed uint32) (callbackCode, uint32, error) {
	code := callbackCode(packed & 0xf)
	if code > callbackCodeMax {
		return 0, 0, &interp.Trap{Reason: fmt.Sprintf(
			"async-lift callback returned code %d, above max %d (EXIT/YIELD/WAIT)", uint32(code), uint32(callbackCodeMax))}
	}
	return code, packed >> 4, nil
}

// liftParkBound bounds one park (#871). **Its expiry is a named outcome, not a hang** — the same
// principle as `ciwatch.sh`'s `unfinished` verdict: a wait that can end in silence reports "still
// waiting" as though it were "nothing to report".
//
// It is a `var` solely so a witness can shorten it; nothing in the engine reassigns it. The value is a
// backstop for a guest or host that never resolves, not a scheduling parameter, so it is deliberately far
// above any legitimate wait — [[an-unasserted-distance-is-the-vacuum]] cuts the other way here, since a
// bound close to real waits would fire on load rather than on a defect.
var liftParkBound = 30 * time.Second

// `liftEntryBound` lived here and is DELETED. It bounded how long an async-lift entry waits for the
// instance's execution slot, trapping on expiry to turn a self-re-entering lift's deadlock into a named
// refusal. Removed on the chair's review of #882: **its only subject is unreachable** (see enterTask —
// `CanonCaller` has no guest entry but a depth-budgeted `Realloc`, and no host impl holds the instance),
// and it cost a false trap, since ordinary contention expired it and the message blamed self-re-entry.
// Entry contention now waits, as the model's backpressure does.

// ErrLiftParkExpired is a park that reached its bound with no event. Its own error rather than a generic
// trap, because the two readings a caller needs to separate are *"the guest is wrong"* and *"nothing ever
// resolved the thing it waited for"*, and only the second is this.
var ErrLiftParkExpired = errors.New("component: async-lift park expired with no event")

// awaitEvent parks until a member of waitable set `si` has a pending event, then delivers it (#871).
//
// # This is the stackless park, and it is not waitableSetWait
//
// The guest's frame is gone: it returned WAIT rather than blocking. So **no agent is blocked here** — the
// wait is on the Go goroutine that called the async-lifted export, which is the caller waiting for its own
// call to resolve. `waitableSetWait` is the other park, where the guest's frame stays alive inside a
// `c.Blocking` excursion and the *agent* is marked blocked. Same observable behaviour, different mechanism,
// and the reason they cannot share an implementation (#871's recon).
//
// The readiness test is the SAME `set.pendingEventLocked()` the other park and `waitable-set.poll` use, so
// an event delivered through a callback re-entry is byte-for-byte the one a parked `wait` would have got.
// A second readiness notion is the defect this shares its predicate to avoid.
//
// # The park is CANCEL-AWARE, and this is where the model puts that (def:781-792)
//
// `wait_from_callback`'s readiness is *"has_pending_event() **or** task.has_pending_cancel()"*, and on
// waking it prefers the cancel: `if deliver_pending_cancel(): return (TASK_CANCELLED, 0, 0)`. So the WAIT
// path's cancellation check lives **inside the wait**, not in the lift loop.
//
// **ADR 0094's first draft got this wrong**, and the error is worth stating because it is a reading
// method and not a slip: I read `canon_lift`, found one `deliver_pending_cancel` at its top and a second
// in its YIELD arm, and concluded from their absence in the WAIT arm that the WAIT path had no check —
// *"a cancel arriving while the guest is parked on a waitable set is not delivered by this loop at all"*.
// That inference required `wait_from_callback` to be cancel-blind, which I never checked. **The model
// distributes the mechanism across the two functions**, and the consequence of believing otherwise is not
// a latency difference but a hang: a task parked on a set nothing will ever resolve is exactly the task a
// host cancels, so a cancel-blind park makes cancellation unreachable in its main case. Measured as that
// hang on #887's first run. *Don't derive a follow-up from your own reasoning* — read the site.
//
// The task is a parameter for this reason alone: the readiness predicate is about the TASK's state as
// much as the set's, and the previous signature could not express that.
func (h *asyncHandles) awaitEvent(task *liftTask, si uint32, bound time.Duration) (event, error) {
	deadline := time.NewTimer(bound)
	defer deadline.Stop()

	// num_waiting, so waitable-set.drop traps on a set this loop is parked on. The model counts in
	// `wait_from_callback` (def:782-786) exactly as it does in `wait`, so the stackless park is a waiter
	// too — counting only the blocking excursion would make drop's trap depend on which park the guest
	// used. Resolved once, outside the loop, so a re-check pass does not double-count.
	h.mu.Lock()
	set0, ok0 := handleAt[*waitableSet](h, si)
	if !ok0 {
		h.mu.Unlock()
		return event{}, &interp.Trap{Reason: fmt.Sprintf(
			"async-lift WAIT named handle %d, which is not a waitable set", si)}
	}
	set0.waiting++
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		set0.waiting--
		h.mu.Unlock()
	}()

	// **One context request per park, enforced by taking the channel out of the select afterwards.** A
	// `Done()` channel stays closed, so a case that re-selected on it would spin: the ordinary path exits
	// the loop on the next pass (the delivery), but a *refused* request — the task is already past
	// running — leaves the loop running with a permanently ready case. A flag rather than nilling
	// `task.ctx` because the context is the caller's and this function does not own it.
	requested := false

	for {
		h.mu.Lock()
		set, ok := handleAt[*waitableSet](h, si)
		if !ok {
			h.mu.Unlock()
			return event{}, &interp.Trap{Reason: fmt.Sprintf(
				"async-lift WAIT named handle %d, which is not a waitable set", si)}
		}
		// **Cancel first, event second** — the model's own order (def:789-792): on waking it calls
		// `deliver_pending_cancel()` and returns TASK_CANCELLED if it fires, consulting the set only
		// otherwise. The order is observable whenever both are ready at once, and preferring the event
		// would strand a delivered cancellation behind a queue of events.
		if task.deliverPendingCancelLocked() {
			h.mu.Unlock()
			return event{code: eventTaskCancelled}, nil
		}
		if e, ready := set.pendingEventLocked(); ready {
			h.mu.Unlock()
			return e, nil
		}
		// The wake channel is re-read under the lock each pass: signalLocked CLOSES and REPLACES it, so a
		// channel captured once would be the stale, already-closed one and this would spin.
		wake := set.wake
		cancelWake := task.cancelWake
		var ctxDone <-chan struct{}
		if !requested {
			ctxDone = task.context().Done()
		}
		h.mu.Unlock()
		select {
		case <-wake: // a member resolved (or another waiter's cycle) — re-check
		case <-cancelWake: // a host cancellation was requested — re-check, which will deliver it
		case <-ctxDone:
			// **The embedder's context (#880, #858). It REQUESTS a cancellation; it does not abort the
			// park.** The difference is the whole of it: aborting would return an error from a task the
			// guest still believes is running, leaving its destructors unrun and its resources held.
			// Requesting takes ADR 0094's path — PENDING_CANCEL, then TASK_CANCELLED delivered at the
			// top of this loop — so the guest runs its own cancellation and resolves, and the caller
			// gets `ErrCancelled` from a task that actually ended.
			//
			// The request is made once and then this case goes inert, because `requestCancelLocked`
			// moves the state out of `liftStarted` and the next pass's `deliverPendingCancelLocked`
			// returns the event. A still-closed `Done()` channel would otherwise re-fire forever.
			requested = true
			h.mu.Lock()
			// The only refusal `requestCancelLocked` can give here is `ErrCancelNotRunning` — the task
			// is already past running, which means it has resolved or is resolving, which means the next
			// pass of this loop either delivers its event or the EXIT arm reports it. There is nothing a
			// caller could do with the error that the loop is not already doing, and this function's
			// return is an `(event, error)` about the *wait*, not about the request.
			//
			//nolint:errcheck // ErrCancelNotRunning here means the task already resolved; the loop's next
			// pass reports that outcome, so the refusal has no consumer.
			_ = task.requestCancelLocked()
			h.mu.Unlock()
		case <-deadline.C:
			return event{}, fmt.Errorf("%w: waitable set %d produced nothing in %s — the host impl or "+
				"guest that would resolve it never did", ErrLiftParkExpired, si, bound)
		}
	}
}
