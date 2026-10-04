// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"fmt"
	"time"

	"github.com/scttfrdmn/burroughs/internal/interp"
)

// liftTask is the durable task an async (callback) lift runs on — the analog of the model's Task/Thread
// (canon_lift def:2096-2153). Its lifetime is the TASK's, not any single core-func call: the callback loop
// spans multiple stack-creating invocations (the callee, then each callback re-entry), and the task, its
// resolution, and its context storage persist across all of them. Teardown keys on RESOLUTION (task.return
// or cancellation), never on the loop exiting normally — a cancelled task resolves without a normal EXIT
// (#785 step 1, Scott's caution; the cancellation guest is the case a loop-exit teardown would break).
type liftTask struct {
	resolved bool           // set by task.return or by cancellation — the teardown key
	result   []interp.Value // the flat result task.return received; read at resolution
	storage  [2]uint32      // the task's context (context.get/set), NOT the per-call stack's — see below

	// cancelled records that this task has been cancelled, which is the precondition `task.cancel`
	// (0x05) checks. **Nothing sets it today**: Burroughs has no way to cancel a task, and the engine it
	// is measured against has none either for a task that has already started — dropping a host call
	// future ABANDONS it (see testdata/asynclift/ABANDONMENT.md). Cancellation reaches a task only
	// through a caller's `subtask.cancel`, which is #862's composed artifact.
	//
	// So the field exists for `task.cancel` to check and for #862 to set, and the cancelled branch is
	// REFUSED by name rather than implemented against semantics nobody has measured yet.
	cancelled bool

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
}

// taskReturn implements `canon task.return` (0x09, definitions.py canon_task_return def:2329): it resolves
// the calling agent's CURRENT lift task with the flat result the guest passes. Bound per-instance over the
// async handle table, which holds the current lift task (set by the callback loop before it invokes the
// callee). It traps if there is no current lift task — task.return outside an async lift is a guest error,
// not a silent no-op.
// ErrTaskCancelUnbuilt is the cancelled branch of `task.cancel` (0x05): the task HAS been cancelled, and
// what a task does on resolving a cancellation is #862's subject rather than this slice's.
//
// It is a named refusal rather than an implementation because nothing can reach it yet — no path in this
// engine sets `liftTask.cancelled` — so an implementation here would be semantics written against no
// measurement, which is the shape this campaign keeps correcting. When #862's composed artifact can
// actually cancel a task, the reading it produces is what the branch gets built from.
var ErrTaskCancelUnbuilt = errors.New(
	"component: task.cancel on a cancelled task is not implemented (gate:async 2b, #862)")

// taskCancel implements `canon task.cancel` (0x05, definitions.py canon_task_cancel def:2342).
//
// # Why the trap is the whole implementation, and is not a stub
//
// The Canonical ABI says what `task.cancel` does on a task that has NOT been cancelled: it traps.
// wasmtime spells the same rule `TaskCancelNotCancelled` — *"`task.cancel` called by task which has not
// been cancelled"*. Burroughs cannot cancel anything, so **every reachable call lands on that branch**,
// and implementing it is implementing the spec rather than deferring it.
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
		task := h.lift
		h.mu.Unlock()
		if task == nil {
			// Same discipline as task.return's: outside an async lift this is a guest error, not a no-op.
			return nil, &interp.Trap{Reason: "task.cancel called with no async lift task in flight"}
		}
		if task.cancelled {
			return nil, ErrTaskCancelUnbuilt
		}
		return nil, &interp.Trap{
			Reason: "task.cancel called by a task that has not been cancelled",
		}
	}
}

func taskReturn(h *asyncHandles) interp.CanonFunc {
	return func(_ *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.lift == nil {
			return nil, &interp.Trap{Reason: "task.return called with no async lift task in flight"}
		}
		h.lift.result = append([]interp.Value(nil), args...)
		h.lift.resolved = true
		return nil, nil
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
func (h *asyncHandles) awaitEvent(si uint32, bound time.Duration) (event, error) {
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

	for {
		h.mu.Lock()
		set, ok := handleAt[*waitableSet](h, si)
		if !ok {
			h.mu.Unlock()
			return event{}, &interp.Trap{Reason: fmt.Sprintf(
				"async-lift WAIT named handle %d, which is not a waitable set", si)}
		}
		if e, ready := set.pendingEventLocked(); ready {
			h.mu.Unlock()
			return e, nil
		}
		// The wake channel is re-read under the lock each pass: signalLocked CLOSES and REPLACES it, so a
		// channel captured once would be the stale, already-closed one and this would spin.
		wake := set.wake
		h.mu.Unlock()
		select {
		case <-wake: // a member resolved (or another waiter's cycle) — re-check
		case <-deadline.C:
			return event{}, fmt.Errorf("%w: waitable set %d produced nothing in %s — the host impl or "+
				"guest that would resolve it never did", ErrLiftParkExpired, si, bound)
		}
	}
}
