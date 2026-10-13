package component

import (
	"io"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// TestAResultStoresReallocRunsWithNoEngineLockHeld is the guest-code half of ADR 0101's result-store
// amendment: when lowering a result calls the caller's `cabi_realloc`, that guest code runs with no engine
// mutex held.
//
// # Why it can happen at all
//
// A string or list result is lowered by `canon.StoreString` / `canon.StoreList`, which call `Heap.Realloc`.
// `guestHeap.Realloc` routes that to `interp.CanonCaller.Realloc`, which runs the lower's `(realloc $f)`:
// guest code. The store used to make that call inside one `asyncHandles.mu` hold (the old `applyLocked`), which is
// guest execution under an engine lock: the shape the §4 B-MM rules exist to prevent. A guest that stops
// at a safepoint there would hold `h.mu` across the stop.
//
// # The witness
//
// `result-store-realloc.wasm`'s import returns a string, and its impl resolves inline, so the store is
// QUEUED (the holder is in host code) and applied as the holder leaves host code. Its `realloc` is a pure
// bump allocator, legal under the reference's `may_leave = False`, with a long spin. A poller on another
// goroutine calls `h.mu.TryLock` throughout `run` and records the longest unbroken stretch it failed.
//
// **Under the fix that stretch is microseconds**, the lock's own short critical sections. **Under the
// defect it is the whole realloc**, tens of milliseconds and more under `-race`. The threshold, 20 ms, is
// far below the spin and far above any critical section. This is a duration, not an ordering of two clocks.
//
// **Neuter, run:** making the store hold `h.mu` across its guest-memory work again makes the poller see it
// held for the realloc's duration, and this fails on the stretch.
func TestAResultStoresReallocRunsWithNoEngineLockHeld(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	b, err := os.ReadFile("testdata/result-store-realloc.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	h := NewHost(io.Discard, io.Discard, nil)
	h.asyncImpls = map[string]asyncLowerImpl{
		"test:async/ops::op": func(_ *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
			onStart()
			onResolve(canon.Str("hello")) // inline: queued, applied as the holder leaves host code
			return func() {}, nil
		},
	}
	in, err := InstantiateWithHost(b, h)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()

	var stop atomic.Bool
	longest := make(chan time.Duration, 1)
	go func() {
		var best, cur time.Duration
		var since time.Time
		for !stop.Load() {
			if in.w.async.mu.TryLock() {
				in.w.async.mu.Unlock()
				if !since.IsZero() {
					cur = time.Since(since)
					best = max(best, cur)
					since = time.Time{}
				}
			} else if since.IsZero() {
				since = time.Now()
			}
		}
		if !since.IsZero() {
			best = max(best, time.Since(since))
		}
		longest <- best
	}()

	res, err := in.CallValues("run")
	stop.Store(true)
	held := <-longest
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if n, _ := res[0].U32(); n != 5 {
		t.Fatalf("run returned %d; want 5, the length of the lowered \"hello\"", n)
	}
	if held > 20*time.Millisecond {
		t.Fatalf("asyncHandles.mu was held for an unbroken %v during run, which is the realloc's guest code "+
			"running under the engine lock — the store must do its guest-memory work with no engine mutex "+
			"held", held)
	}
	t.Logf("longest unbroken hold of asyncHandles.mu during run: %v", held)
}
