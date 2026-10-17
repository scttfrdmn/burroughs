// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"fmt"

	"github.com/scttfrdmn/burroughs/internal/interp"
)

// gate:async increment 4: context.get/set — the first op the real guest (p3async-hello) hits. Async
// component-model context is per-agent (i32) storage of a fixed number of slots. The model stores it
// per-thread; in Burroughs the per-agent unit is the caller's stack (two concurrent agents are two stacks
// but one shared thread object), so the storage lives there (interp.CanonCaller.ContextGet/Set), keyed by
// nothing — per-agent by construction. An unset slot reads 0 (the stack's zero value, matching the model's
// [0,0]); a slot index past the fixed count traps. The slot index is a STATIC operand of the built-in,
// captured at decode; these wrappers close over it. (Decision 0050: per-invocation and per-thread coincide
// under v1 and split at §7 growable continuations — the placement's live expiry, #739.)

// DATED DISPOSITION (2026-09-17, #785 step 1 — the async-lift context correction): the placement above is
// corrected for ONE case. When an async (callback) lift is in flight (h.lift != nil), context lives on the
// durable lift TASK, not the caller's stack — because that loop spans multiple stack-creating calls (the
// callee, then each callback re-entry) and the model runs the whole loop on one thread with one storage, so
// per-call stack storage would lose context between re-entries and fail #788's context-survival invariant.
// The SYNC path is unchanged: a sync caller IS a stack, so its context stays on the stack (h.lift == nil).
// This is the case ADR 0050 / #739 named as the placement's expiry — not §7 growable continuations as first
// guessed, but the callback lift, which captures no continuation stack yet carries state that outlives any
// single call. Recorded as a dated disposition (the asyncLowerImpl / stream.write-consumer form on #739),
// not an unremarked change. The storage split lives in async_lift.go's liftTask.

// callerStorage is the calling Thread's context storage, the reference's `current_thread().storage` (#1013):
//   - a created Thread's own;
//   - an implicit Thread's task's, under an async lift;
//   - nil for a sync lift, whose context is the stack's.
//
// **It is the CALLER's, found through the Thread record its stack carries**, not `asyncHandles.lift`. That
// field is the instance's "current task", and it was not the caller's on the default path. A sync entry made
// while an async task waited read the waiting task's context (`context-clobber.wasm`: 5 where both oracles
// read 0). Only a caller with no Thread record (a core-level call bypassing the component) falls back to
// `h.lift`, which is all it ever had.
func callerStorage(h *asyncHandles, c *interp.CanonCaller) *[2]uint32 {
	if h.threads != nil {
		if rec := h.threads.current(c); rec != nil {
			switch {
			case !rec.implicit:
				return &rec.storage
			case rec.task != nil:
				return &rec.task.storage
			default:
				return nil // a sync lift: the stack's
			}
		}
	}
	h.mu.Lock()
	lift := h.lift
	h.mu.Unlock()
	if lift != nil {
		return &lift.storage
	}
	return nil
}

// contextGet implements `canon context.get` (0xa, definitions.py:canon_context_get) for a static slot: read the calling
// Thread's context slot, returning it as an i32 (slice-1 scope is i32 context). See callerStorage for whose
// storage that is.
func contextGet(h *asyncHandles, slot uint32) interp.CanonFunc {
	return func(c *interp.CanonCaller, _ []interp.Value) ([]interp.Value, error) {
		if st := callerStorage(h, c); st != nil {
			if int(slot) >= len(st) {
				return nil, &interp.Trap{Reason: fmt.Sprintf("context.get: slot %d out of range (%d task slots)", slot, len(st))}
			}
			return []interp.Value{interp.I32(int32(st[slot]))}, nil
		}
		v, err := c.ContextGet(slot)
		if err != nil {
			return nil, err
		}
		return []interp.Value{interp.I32(int32(v))}, nil
	}
}

// contextSet implements `canon context.set` (0xb, definitions.py:canon_context_set) for a static slot: write the value
// (a runtime i32 arg) into the calling agent's context slot — the lift task's under an async lift, else the
// caller's stack's.
func contextSet(h *asyncHandles, slot uint32) interp.CanonFunc {
	return func(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("component: context.set: got %d args, want (v)", len(args))
		}
		if st := callerStorage(h, c); st != nil {
			if int(slot) >= len(st) {
				return nil, &interp.Trap{Reason: fmt.Sprintf("context.set: slot %d out of range (%d task slots)", slot, len(st))}
			}
			st[slot] = uint32(args[0].Bits)
			return nil, nil
		}
		if err := c.ContextSet(slot, uint32(args[0].Bits)); err != nil {
			return nil, err
		}
		return nil, nil
	}
}
