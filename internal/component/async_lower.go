// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"encoding/binary"
	"fmt"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// gate:async slice-1 increment 2a-i-A/B: the async `canon lower`'s execution.
//
// An async-lowered import's flat ABI is the sync lower's flat args (params + a retptr) plus a PACKED i32
// return (definitions.py `canon_lower`, def:2187–2251): `[RETURNED]` (= 2) when the callee resolves inline
// (2a-i-A, the sync-resolving arm), or `[state | (subtaski<<4)]` when it blocks (2a-i-B, the blocking arm —
// the callee starts but defers resolution, so the subtask is registered and its index returned). The
// waitable-set loop that observes a blocked subtask's resolution is 2a-i-B-2, refused by name until then.
// The oracle for these bytes is `canon/testdata/fixtures.json` (`async_lowers` for the inline arm,
// `async_lower_blocking` for the blocking return), pinned model-faithfully — a sync outer export lifted
// through the model's own `Store.lift`/`invoke` establishes the Task/Thread context, never a stub (#728).

// asyncLowerImpl performs an async-lowered import call, mirroring the reference model's callee shape
// `callee(on_start, on_resolve) -> on_cancel` (definitions.py FuncInst, def:383–386). It lifts the flat
// params by calling onStart (which returns the lifted param Values), then RESOLVES by calling onResolve
// with the result value — synchronously for the sync-resolving arm, or later (from its own async progress)
// for the blocking arm, in which case it returns without having called onResolve. onCancel is invoked if
// the subtask is cancelled before it resolves (unused until a later increment).
//
// This REPLACES the provisional one-shot `func(c, params) (canon.Value, bool, error)` of 2a-i-A: a
// would-block-then-refuse signal is a single exchange, but the blocking arm needs an impl that stays
// resolvable and a guest that can observe the resolution — a different interface, not an extension (#739).
type asyncLowerImpl func(c *interp.CanonCaller, onStart func() []interp.Value, onResolve func(v canon.Value)) (onCancel func(), err error)

// asyncLowerFunc adapts an async-lower impl to the async-lower flat ABI. Flat args are the params followed
// by the retptr (when the func returns a result); the returned core value is the packed status. The order
// is load-bearing (Scott's caution, carried from 2a-i-A): onResolve is the only writer of the retptr, and
// the impl calls it only on resolution — so a blocked callee (which never calls onResolve) leaves the
// retptr untouched, a registered subtask rather than a half-lowered result.
func asyncLowerFunc(impl asyncLowerImpl, hasResult bool, h *asyncHandles) interp.CanonFunc {
	return func(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		// A result-returning async lower's flat args end in a retptr; an empty-result one has none (the
		// fixture's empty case: flat_params []). `hasResult` gates the split.
		params := args
		var retptr uint64
		if hasResult {
			if len(args) < 1 {
				return nil, fmt.Errorf("component: async lower: got %d core args, want params + retptr", len(args))
			}
			params = args[:len(args)-1]
			retptr = uint64(uint32(args[len(args)-1].Bits))
		}

		st := &subtask{state: subtaskStarting}
		onStart := func() []interp.Value {
			h.mu.Lock()
			st.state = subtaskStarted // lift the params (model-produced STARTING -> STARTED)
			h.mu.Unlock()
			return params
		}
		var resolveErr error
		onResolve := func(v canon.Value) {
			// Lower the result to the retptr, then mark RETURNED and wake any set the subtask has joined.
			// For the blocking arm this can fire on the impl's OWN goroutine while a guest agent is parked
			// in waitable-set.wait, so it runs under the table mutex; the retptr write precedes `resolved`
			// so a waiter that observes the resolution sees the lowered result. A blocked callee that never
			// calls onResolve leaves the retptr untouched.
			h.mu.Lock()
			defer h.mu.Unlock()
			if hasResult {
				if v.Type.Kind == canon.KindFuture {
					// A future<T> result: mint a readable end in the async handle table and write its handle
					// (an i32). The codec cannot lower it — future handles live in the component-layer table,
					// not the codec's — so the wrapper writes the handle here (gate:async increment 3).
					fe := &readableFutureEnd{value: v.FutureValue()}
					fe.index = h.addLocked(fe)
					var buf [4]byte
					binary.LittleEndian.PutUint32(buf[:], uint32(fe.index))
					if err := c.Write(uint64(uint32(retptr)), buf[:]); err != nil {
						resolveErr = fmt.Errorf("component: async lower: writing future handle to retptr: %w", err)
						return
					}
				} else if v.Type.Kind == canon.KindStream {
					// A stream<T> result: mint a writable end (the guest writes to it) and write its handle,
					// the same component-layer minting as a future (gate:async increment 3, write side).
					se := &writableStreamEnd{}
					se.index = h.addLocked(se)
					var buf [4]byte
					binary.LittleEndian.PutUint32(buf[:], uint32(se.index))
					if err := c.Write(uint64(uint32(retptr)), buf[:]); err != nil {
						resolveErr = fmt.Errorf("component: async lower: writing stream handle to retptr: %w", err)
						return
					}
				} else if err := canon.StoreVia(guestHeap{c}, v, int(uint32(retptr))); err != nil {
					resolveErr = fmt.Errorf("component: async lower: lowering result to retptr: %w", err)
					return
				}
			}
			st.state = subtaskReturned
			st.resolved = true
			if st.set != nil { // wake any agent parked on the set this subtask was joined to
				st.set.signalLocked()
			}
		}

		onCancel, err := impl(c, onStart, onResolve)
		if err != nil {
			return nil, err
		}
		_ = onCancel // cancellation is a later increment; the blocking arm holds the resolver, not the cancel.

		h.mu.Lock()
		rerr := resolveErr
		resolved := st.resolved
		var packed int32
		if !resolved {
			// Blocking arm (2a-i-B): the callee started but did not resolve inline. Register the subtask
			// (assigning its index) and return [state | (subtaski<<4)]; the guest observes the resolution
			// via the waitable-set loop. Captured under the lock so a resolution racing on another
			// goroutine cannot interleave with the index assignment.
			st.index = h.addLocked(st)
			packed = packSubtaskWait(st.state, st.index)
		}
		h.mu.Unlock()

		if rerr != nil {
			return nil, rerr
		}
		if resolved {
			// Sync-resolving arm (2a-i-A), or an async callee that resolved before we checked: the result
			// is already at the retptr; return [RETURNED].
			return []interp.Value{interp.I32(int32(subtaskReturned))}, nil
		}
		return []interp.Value{interp.I32(packed)}, nil
	}
}
