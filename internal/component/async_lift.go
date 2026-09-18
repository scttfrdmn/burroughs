// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"fmt"

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
}

// taskReturn implements `canon task.return` (0x09, definitions.py canon_task_return def:2329): it resolves
// the calling agent's CURRENT lift task with the flat result the guest passes. Bound per-instance over the
// async handle table, which holds the current lift task (set by the callback loop before it invokes the
// callee). It traps if there is no current lift task — task.return outside an async lift is a guest error,
// not a silent no-op.
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
