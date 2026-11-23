// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"encoding/binary"
	"fmt"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// The blocking sync lower of an async host import (#1268, ADR 0126): the pin's def:canon_lower sync arm for a
// callee whose function type is async,
//
//	subtask.on_cancel = callee(on_start, on_resolve)
//	...
//	if not opts.async_:
//	  if not subtask.resolved():
//	    thread.wait_until(subtask.resolved)
//	  subtask.deliver_resolve()
//	  return subtask.flat_results
//
// wasi-libc's getaddrinfo lowers `wasi:sockets/ip-name-lookup.resolve-addresses`, an `async func`, this way, with
// no `async` option, so a std guest that resolves a name reaches it. ADR 0121 refused the form at instantiate.
//
// # The impl is the async lower's, and the wait is the sync cancel's
//
// The impl is started exactly as the async lower starts it, `callee(on_start, on_resolve)` as host code, so one
// impl serves both lowers. Its `onResolve` here records the value and resolves the subtask under
// `asyncHandles.mu`, which closes the subtask's wake. **It writes no guest memory**, so it takes no run slot,
// whichever goroutine calls it: the result is lowered after the wait, by the caller.
//
// Unresolved when the impl returns, the caller waits as the sync `subtask.cancel` waits (async_subtask.go):
// inside `c.Blocking`, so a stop-the-world sees the agent blocked (§5 H-1), and inside `h.suspending`, so the
// run slot is released and another task of the instance may execute. **The wait is judged** (`judged` true): it
// is `wait_until(subtask.resolved)` from inside the lifted function, so the sync tail traps it when the function
// is sync-typed and no candidate is ready, and lets it wait when the function is async-typed (grave #1267's rule).
//
// # The result is lowered by the caller, with its own options
//
// After the wait, on the caller's goroutine, with the run slot held again and no engine lock held: a result past
// MAX_FLAT_RESULTS is stored at the return pointer, the lower's last core argument, through the lower's memory and
// realloc (`canon.StoreVia`), as a sync impl stores; a one-word result is returned as a core value. That is
// `lower_flat_values(cx, MAX_FLAT_RESULTS, …)`, run where `subtask.flat_results` is returned rather than inside
// `on_resolve`, which is unobservable: the caller is the only Thread that could read the store, and it is
// waiting.
//
// # Nothing cancels it, and Close ends it
//
// The subtask is not in the handle table, so no guest holds an index to cancel, and its `onCancel` is never
// called. Close ends a pending call two ways: `killAllParked` aborts its Thread's wait, and `stopHostWaits`
// closes its wake, which ends a wait the kill did not reach. Either way the call returns errThreadKilled.

// syncLowerForm is a blocking sync lower's core shape, read from the guest's signature by the walk.
type syncLowerForm struct {
	name      string      // the import's identity, for messages
	hasResult bool        // the function returns a value
	retptr    bool        // the result is stored at a return pointer, the last core argument
	spill     *canon.Type // the parameter tuple, when the parameters crossed as one pointer to it
}

// syncLowerCall is one pending blocking sync lower, in its instance's registry (`asyncHandles.syncLowerCalls`)
// from the impl's start to the lower's return. Its fields are guarded by `asyncHandles.mu`, except wake.
//
// **wake is the subtask's resolution wake, kept here as well as on the subtask.** Whoever closes it, the
// resolution (`resolveLocked`) or Close's stop, takes it off the subtask under the lock, so it is closed once; the
// waiter reads this copy, which is never taken. The waiter read the subtask's field in a first build, and a stop
// that ran before the waiter reached its wait left it selecting on nil: Close then hung, 1 run of 12 under -race,
// waiting for a host call that waited for nothing.
type syncLowerCall struct {
	st   *subtask
	wake chan struct{} // closed by the resolution or by Close's stop, set once at start
	v    canon.Value   // the value onResolve was handed
}

// syncLowerOfAsync adapts an async host impl to the sync lower's flat ABI, blocking until the impl resolves.
func syncLowerOfAsync(impl asyncLowerImpl, form syncLowerForm, h *asyncHandles) interp.CanonFunc {
	return func(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		params := args
		var retptr uint32
		if form.retptr {
			if len(args) < 1 {
				return nil, fmt.Errorf("component: sync lower of %s: got %d core args, want params + retptr", form.name, len(args))
			}
			params = args[:len(args)-1]
			retptr = uint32(args[len(args)-1].Bits)
		}
		if form.spill != nil {
			loaded, err := loadSpilledParams(c, *form.spill, params)
			if err != nil {
				return nil, err
			}
			params = loaded
		}
		call, err := h.startSyncLower()
		if err != nil {
			return nil, err
		}
		// Deferred, because the call can end by unwinding: an agent ended at a safepoint after Close (T-5's
		// sentinel, ADR 0071) leaves through the wait's Blocking, past every statement after it. A first build
		// removed the call in line, and TestCloseEndsAPendingSyncLoweredCall found it still registered after Close.
		defer h.endSyncLower(call)
		onStart := func() []interp.Value {
			h.mu.Lock()
			call.st.state = subtaskStarted
			h.mu.Unlock()
			return params
		}
		onResolve := func(v canon.Value) {
			h.mu.Lock()
			if !call.st.resolved {
				call.v = v
				call.st.resolveLocked(subtaskReturned)
			}
			h.mu.Unlock()
		}
		agent := h.freeAgentOf(c) // a free agent's own window (#1336, ADR 0130), nil for a slot holder
		h.enterHost(agent)
		_, err = impl(c, onStart, onResolve)
		h.leaveHost(agent)
		if err != nil {
			return nil, err
		}
		v, err := h.awaitSyncLower(c, call)
		if err != nil {
			return nil, err
		}
		return lowerSyncResult(c, form, retptr, v)
	}
}

// startSyncLower registers a pending call, whose subtask's wake is armed before the impl can resolve it. After
// Close's stop none starts: no guest of the instance runs again, and an impl the stop refused to start would
// leave the call waiting for a resolution nothing makes.
func (h *asyncHandles) startSyncLower() (*syncLowerCall, error) {
	wake := make(chan struct{})
	call := &syncLowerCall{st: &subtask{state: subtaskStarting, resolveWake: wake}, wake: wake}
	h.mu.Lock()
	stopped := h.hostWaitsStopped
	if !stopped {
		h.syncLowerCalls[call] = struct{}{}
	}
	h.mu.Unlock()
	if stopped {
		return nil, errThreadKilled
	}
	return call, nil
}

// endSyncLower removes a call from the registry: it is no longer a waker, and Close has nothing of it to end.
func (h *asyncHandles) endSyncLower(call *syncLowerCall) {
	h.mu.Lock()
	delete(h.syncLowerCalls, call)
	h.mu.Unlock()
}

// awaitSyncLower is `wait_until(subtask.resolved)`, and returns the value the impl resolved with. A wait Close
// ended without a resolution is errThreadKilled.
//
// NEUTER: pass `judged` false, and TestASyncLowerOfAnAsyncImportBlocksOrTrapsByType's sync-typed `go` returns 7
// where wasmtime traps, and name-lookup's held `run-sync(0)` waits until the hang guard.
func (h *asyncHandles) awaitSyncLower(c *interp.CanonCaller, call *syncLowerCall) (canon.Value, error) {
	h.mu.Lock()
	resolved := call.st.resolved
	h.mu.Unlock()
	if !resolved {
		// `ready` is wait_until's predicate, read under h.mu by the sync tail (#1005).
		ready := func() bool { return call.st.resolved }
		if err := c.Blocking(h.suspending(c, ready, true, func(abort <-chan struct{}) error {
			select {
			case <-call.wake: // resolved, or closed by Close's stop, before this wait or during it
				return nil
			case <-abort: // a trap in this Thread's task, or Close, ended it (ADR 0101's Option A, point 7)
				return nil // `suspending` returns the abort's error
			case <-h.lock.wait(): // another call's trap locked the store (#1040): end at this suspension
				return h.lock.locked()
			}
		})); err != nil {
			return canon.Value{}, err
		}
	}
	h.mu.Lock()
	resolved = call.st.resolved
	v := call.v
	h.mu.Unlock()
	if !resolved {
		return canon.Value{}, errThreadKilled // Close's stop woke it
	}
	return v, nil
}

// stopSyncLowersLocked is Close's stop for pending blocking sync lowers: each one's wake is taken, and returned
// for the caller to close once it has released h.mu, which wakes its waiter with no resolution. The caller holds
// h.mu.
func (h *asyncHandles) stopSyncLowersLocked() []chan struct{} {
	var wakes []chan struct{}
	for call := range h.syncLowerCalls {
		if w := call.st.resolveWake; w != nil {
			call.st.resolveWake = nil
			wakes = append(wakes, w)
		}
	}
	return wakes
}

// lowerSyncResult lowers the resolved value through the sync lower's own options: at the return pointer, or as
// one flat word. A future or stream result would need its end minted in the handle table, which no host import
// returning one through this path needs, so it is refused by name.
func lowerSyncResult(c *interp.CanonCaller, form syncLowerForm, retptr uint32, v canon.Value) ([]interp.Value, error) {
	if !form.hasResult {
		return nil, nil
	}
	if v.IsNoResolution() {
		return nil, fmt.Errorf("component: sync lower of %s: its host impl resolved with no value", form.name)
	}
	if k := v.Type.Kind; k == canon.KindFuture || k == canon.KindStream {
		return nil, fmt.Errorf("%w: a sync lower of %s, whose result is a %s: the blocking sync lower of an async "+
			"import lowers no future or stream (ADR 0126)", ErrUnsupportedForm, form.name, k)
	}
	if form.retptr {
		if err := canon.StoreVia(guestHeap{c}, v, int(retptr)); err != nil {
			return nil, fmt.Errorf("component: sync lower of %s: lowering the result to its return pointer: %w", form.name, err)
		}
		return nil, nil
	}
	// One flat word: the value is stored into a scratch buffer and loaded flat, the codec's own layout both ways,
	// so no per-kind conversion lives here.
	var s flatScratch
	if err := canon.StoreVia(&s, v, 0); err != nil {
		return nil, fmt.Errorf("component: sync lower of %s: flattening the result: %w", form.name, err)
	}
	bits, types, err := canon.LoadFlat(&s, v.Type, 0)
	if err != nil {
		return nil, fmt.Errorf("component: sync lower of %s: flattening the result: %w", form.name, err)
	}
	if len(bits) > maxFlatResults {
		return nil, fmt.Errorf("component: sync lower of %s: the result flattens to %d words, past the %d a core type returns", form.name, len(bits), maxFlatResults)
	}
	return flatValues(bits, types), nil
}

// flatScratch is a heap over a Go slice, for flattening one small value: it grows to fit what is written, and
// has no allocator, since a value that flattens to one word holds no string or list.
type flatScratch struct{ b []byte }

func (s *flatScratch) grow(end int) {
	if end > len(s.b) {
		s.b = append(s.b, make([]byte, end-len(s.b))...)
	}
}

func (s *flatScratch) Realloc(_, _, _, _ int) (int, error) {
	return 0, fmt.Errorf("component: a one-word result has nothing to allocate")
}

func (s *flatScratch) WriteBytes(ptr int, data []byte) error {
	s.grow(ptr + len(data))
	copy(s.b[ptr:], data)
	return nil
}

func (s *flatScratch) StoreInt(v uint64, ptr, nbytes int) error {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], v)
	return s.WriteBytes(ptr, buf[:nbytes])
}

func (s *flatScratch) ReadBytes(ptr, n int) ([]byte, error) {
	if err := s.CheckRange(ptr, n); err != nil {
		return nil, err
	}
	return s.b[ptr : ptr+n], nil
}

func (s *flatScratch) CheckRange(ptr, n int) error {
	if ptr < 0 || n < 0 || ptr+n > len(s.b) {
		return fmt.Errorf("component: %d byte(s) at %d are past the scratch value's %d", n, ptr, len(s.b))
	}
	return nil
}
