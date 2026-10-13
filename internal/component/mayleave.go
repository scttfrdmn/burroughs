// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// mayLeaveGuard wraps a canon built-in or lowered import with the reference's `trap_if(not
// task.inst.may_leave)` (#990).
//
// **Where the reference clears may_leave, found by search of the pin's `definitions.py`**: two sites.
// `LiftLowerContext.reallocate` sets `inst.may_leave = False` around the realloc call, and `canon_lift`
// sets it around `post_return`. Burroughs refuses `post-return` at load (callvalues.go), so the realloc is
// the one site, and `interp.CanonCaller.Realloc` / `interp.Instance.InvokeMayNotLeave` are where it is
// cleared.
//
// **Where the reference traps on it**: `canon_lower`'s adapter, and every built-in except five —
// `resource.rep`, `context.get`, `context.set`, `backpressure.inc` and `backpressure.dec`, which carry no
// check. Of those five, the two `context.*` forms are the ones bound here; the walker does not wrap them
// (asyncBuiltinMayLeaveExempt).
//
// The check reads the CALLING stack's mark, not an instance flag: the realloc runs on the stack that
// called it, so a built-in it calls sees the mark, and a different agent's call into the same instance —
// which the run slot already serialises — does not.
func mayLeaveGuard(name string, fn interp.CanonFunc) interp.CanonFunc {
	return func(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		if !c.MayLeave() {
			return nil, &interp.Trap{Reason: name + ": called while its instance may not leave (inside " +
				"cabi_realloc); the reference traps here (trap_if(not may_leave))"}
		}
		return fn(c, args)
	}
}

// asyncBuiltinMayLeaveExempt reports whether an async built-in opcode is one of the reference's
// unchecked five that this walker binds: `context.get` (0x0a) and `context.set` (0x0b).
// `backpressure.inc`/`.dec` (0x24/0x25) and `resource.rep` (0x04) are exempt too but are not bound here.
func asyncBuiltinMayLeaveExempt(op byte) bool { return op == 0x0a || op == 0x0b }
