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
	return asyncLowerFuncFor(func(*subtask) asyncLowerImpl { return impl }, hasResult, h)
}

// asyncLowerFuncFor is asyncLowerFunc with the impl made per call, from the call's subtask, so that the
// cross-component adapter can attach its cancellation hand-back (`subtask.cancelTransfer`, #726).
func asyncLowerFuncFor(implFor func(*subtask) asyncLowerImpl, hasResult bool, h *asyncHandles) interp.CanonFunc {
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
		// apply is the store itself — lower the result, then resolve — run under the run slot, or on the slot
		// holder's goroutine as it leaves host code (see `onResolve` below). It takes `h.mu` only for its
		// bookkeeping, and **never across the guest-memory work**.
		//
		// **Why not one `h.mu` hold, as this was.** Lowering a string or list result calls `Heap.Realloc`
		// (`canon.StoreString`, `canon.StoreList`), which `guestHeap` routes to `CanonCaller.Realloc`: the
		// caller's `cabi_realloc`, which is guest code. Inside one `h.mu` hold that is guest execution under
		// an engine lock, the shape the §4 B-MM rules exist to prevent: a guest that stops at a safepoint there
		// holds `h.mu` across the stop. `TestAResultStoresReallocRunsWithNoEngineLockHeld` measured the lock
		// held for the whole realloc. So the store is three steps, and only the middle one touches guest
		// memory, with no engine lock held:
		//
		//  1. under `h.mu`: the cancellation check, and minting a future or stream handle (`addLocked`);
		//  2. no lock: the retptr write or the codec's store, realloc included;
		//  3. under `h.mu`: record an error, or mark RETURNED and wake any set the subtask has joined.
		//
		// **The B-MM-1 edge is unchanged.** The retptr write in step 2 precedes step 3's acquisition, and
		// `resolved` is published inside it, so a waiter that observes the resolution under `h.mu` sees the
		// lowered result. The mutex is one of the TWO edges carrying §4 B-MM-1's acquire guarantee to a parked
		// waiter; the other is signalLocked's wake-channel close→receive (see async_waitset.go's header,
		// corrected after the litmus witness). A blocked callee that never calls onResolve leaves the retptr
		// untouched.
		//
		// NEUTER THIS by holding `h.mu` across step 2 again, and the realloc witness sees the lock held for the
		// realloc's duration.
		apply := func(v canon.Value) {
			h.mu.Lock()
			// **The cancel arm is chosen by the resolution, not by the request** (#1009). The reference's
			// `on_resolve` branches on `result is None`, and `cancellation_requested` chooses nothing on the
			// value arm, so a callee that ignores a delivered cancellation and returns a value is RETURNED.
			// This branched on `st.cancellationRequested` alone until #1009, which resolved such a callee
			// CANCELLED with its value dropped.
			if st.cancellationRequested && v.IsNoResolution() {
				// A cancel resolution (the model's on_resolve(None), def:2214–2220): no result is lowered,
				// and the terminal state is chosen by whether the subtask had started — STARTING ->
				// CANCELLED_BEFORE_STARTED, STARTED -> CANCELLED_BEFORE_RETURNED. This is the cancel-resolution
				// path the re-check found missing: before increment 4 onResolve only ever produced RETURNED,
				// so a cancelled subtask would have resolved with the wrong terminal state (#739).
				if st.state == subtaskStarting {
					st.resolveLocked(subtaskCancelledBeforeStarted)
				} else {
					st.resolveLocked(subtaskCancelledBeforeReturned)
				}
				h.mu.Unlock()
				return
			}
			// A future<T> or stream<T> result: mint its end in the async handle table now, under the lock, and
			// write the handle (an i32) in step 2. The codec cannot lower these — their handles live in the
			// component-layer table, not the codec's — so the wrapper writes them (gate:async increment 3:
			// a readable end for a future, a writable end, which the guest writes to, for a stream).
			var handle int
			var kind canon.Kind
			// A NoResolution with no cancel request (a child that failed while nobody asked to cancel it)
			// resolves RETURNED with nothing lowered, as before #1009: there is no value to store.
			store := hasResult && !v.IsNoResolution()
			if store {
				kind = v.Type.Kind
				switch kind {
				case canon.KindFuture:
					fe := &readableFutureEnd{value: v.FutureValue()}
					fe.index = h.addLocked(fe)
					handle = fe.index
				case canon.KindStream:
					se := &writableStreamEnd{}
					se.index = h.addLocked(se)
					handle = se.index
				default:
					// Every other kind mints nothing: the codec lowers it in step 2.
				}
			}
			h.mu.Unlock()

			var err error
			if store {
				switch kind {
				case canon.KindFuture, canon.KindStream:
					var buf [4]byte
					binary.LittleEndian.PutUint32(buf[:], uint32(handle))
					if werr := c.Write(uint64(uint32(retptr)), buf[:]); werr != nil {
						err = fmt.Errorf("component: async lower: writing %s handle to retptr: %w", kind, werr)
					}
				default:
					if serr := canon.StoreVia(guestHeap{c}, v, int(uint32(retptr))); serr != nil {
						err = fmt.Errorf("component: async lower: lowering result to retptr: %w", serr)
					}
				}
			}

			h.mu.Lock()
			defer h.mu.Unlock()
			if err != nil {
				resolveErr = err
				return
			}
			st.resolveLocked(subtaskReturned)
		}

		// onResolve is the resolution the impl is handed. It applies the store under the right ordering.
		//
		// **Under the caller instance's run slot** (ADR 0101's result-store amendment, #983). Lowering a
		// result writes guest memory and may call the caller's `realloc`, which is guest code. Without the
		// slot, a host resolver or a child component's goroutine (`crossComponentResolve`) wrote the result
		// while another task of the instance executed: the Windows race job's report on `9f11079`, rebuilt
		// heap-backed on darwin.
		//
		// **Or, while the slot's holder is in HOST code (`asyncHandles.inHost`), queued for the holder to
		// apply as it leaves host code** (`asyncHandles.leaveHost`), on its own goroutine, before its guest
		// resumes. Waiting for the slot there can deadlock, because the holder may be the one resolving. And
		// writing at once was #983's first fix, which `TestAResultStoreDoesNotLandWhileTheHolderIsInHostCode`
		// showed wrong: the holder's host code reaches guest memory through `Caller`, ADR 0064's plain
		// region, and a store from another goroutine landed underneath it with nothing ordering the two. The
		// reference orders them: the resolver's work is another thread's execution, which cannot run during
		// this thread's host call, but does run before this thread executes guest code again. The queue is
		// that ordering. It is not deferral to the waiter, which the chair ruled out on #983: the store
		// still lands before any guest code of the instance runs.
		//
		// NEUTER THIS (either arm) to bring a race back. Skipping the acquisition fails
		// `TestAResultStoreWaitsForTheExecutingTask`. Applying at once instead of queueing fails
		// `TestAResultStoreDoesNotLandWhileTheHolderIsInHostCode`.
		onResolve := func(v canon.Value) {
			h.mu.Lock()
			if h.inHost > 0 {
				h.hostStores = append(h.hostStores, func() { apply(v) })
				h.mu.Unlock()
				return
			}
			h.mu.Unlock()
			ch := &runChain{}
			h.acquireRun(ch, nil, nil)
			defer h.releaseRunFor(ch)
			apply(v) // takes h.mu for its bookkeeping only, never across its guest-memory work
		}

		// The impl is host code on this agent's goroutine, under the run slot it holds: while it runs, a
		// resolution of ANY subtask writes without waiting for the slot (`asyncHandles.inHost`). Lowered
		// under `h.mu` before the guest resumes (see `onResolve`).
		h.enterHost()
		onCancel, err := implFor(st)(c, onStart, onResolve)
		h.leaveHost()
		if err != nil {
			return nil, err
		}
		// Store the impl's request_cancellation so subtask.cancel (0x06) can invoke it. Carried inert since
		// 2a-i-B-1; wired in increment 4. Guarded by the table mutex like the rest of the subtask's fields.
		h.mu.Lock()
		st.onCancel = onCancel
		h.mu.Unlock()

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
