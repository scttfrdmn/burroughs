// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"fmt"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// gate:async slice-1 increment 2a-i-A: the async `canon lower`'s SYNC-RESOLVING arm.
//
// An async-lowered import's flat ABI is the sync lower's flat args (params + a retptr) plus a PACKED i32
// return (definitions.py `canon_lower`, def:2187–2251): `[RETURNED]` (= 2) when the callee resolves
// inline, or `[state|(subtaski<<4)]` when it blocks. This increment executes the inline-resolving arm
// only; the blocking arm needs the waitable-set loop (2b) and refuses by name here (`ErrAsyncNotImplemented`,
// increment 1). The oracle for these bytes is `canon/testdata/fixtures.json`'s `async_lowers`, pinned
// model-faithfully (a sync outer export lifted through the model's own `Store.lift`/`invoke` establishes
// the Task/Thread context, never a stub — #728).

// ErrAsyncWouldBlock is the signal an async-lower impl raises when it cannot resolve inline — the blocking
// arm, which 2a-i-A does not execute. It is NOT an error to surface: the wrapper turns it into the
// gate:async blocking-NYI refusal, by name, **before writing any guest memory** (the ordering that keeps
// a future blocking arm from leaving a half-lowered result — a refusal, not silent corruption).
var ErrAsyncWouldBlock = errors.New("component: async lower did not resolve inline (blocking arm is 2b)")

// asyncLowerImpl resolves an async-lowered import inline: given the lifted params, it returns the result
// value to lower, or ErrAsyncWouldBlock if it would block. It does NOT write guest memory itself — the
// wrapper owns the retptr write, so the write happens only after inline resolution is confirmed.
type asyncLowerImpl func(c *interp.CanonCaller, params []interp.Value) (canon.Value, bool, error)

// asyncLowerFunc adapts an inline-resolving async impl to the async-lower flat ABI. Flat args are the
// params followed by the retptr; the returned core value is the packed status. The order is the
// load-bearing part (Scott's caution): the impl is consulted for inline resolution FIRST; only on inline
// resolution does the wrapper lower the result to the retptr; a block-signal refuses by name with the
// retptr untouched.
func asyncLowerFunc(impl asyncLowerImpl, hasResult bool) interp.CanonFunc {
	return func(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		// A result-returning async lower's flat args end in a retptr; an empty-result one has none (the
		// fixture's `async-lower-empty-resolves-inline`: flat_params []). `hasResult` gates the split.
		params := args
		var retptr uint64
		if hasResult {
			if len(args) < 1 {
				return nil, fmt.Errorf("component: async lower: got %d core args, want params + retptr", len(args))
			}
			params = args[:len(args)-1]
			retptr = uint64(uint32(args[len(args)-1].Bits))
		}

		// 1. Consult the impl for inline resolution — BEFORE any guest-memory write.
		v, resolved, err := impl(c, params)
		if err != nil {
			if errors.Is(err, ErrAsyncWouldBlock) {
				// The blocking arm (2b): refuse by name, retptr untouched — not a half-lowered result.
				return nil, fmt.Errorf("%w: an async lower blocked (its callee did not resolve inline); "+
					"the waitable-set loop is increment 2b, not yet implemented", ErrAsyncNotImplemented)
			}
			return nil, err
		}
		if !resolved {
			return nil, fmt.Errorf("%w: an async lower did not resolve inline", ErrAsyncNotImplemented)
		}

		// 2. Inline resolution confirmed: NOW lower the result to the retptr (if any) and return RETURNED.
		if hasResult {
			if err := canon.StoreVia(guestHeap{c}, v, int(uint32(retptr))); err != nil {
				return nil, fmt.Errorf("component: async lower: lowering result to retptr: %w", err)
			}
		}
		return []interp.Value{interp.I32(int32(asyncSubtaskReturned))}, nil
	}
}

// asyncSubtaskReturned is Subtask.State.RETURNED (definitions.py:804) — the packed status an async lower
// returns when it resolved inline (the low 4 bits, subtask index zero since no handle is tabled).
const asyncSubtaskReturned = 2
