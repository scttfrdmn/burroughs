package component

import (
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// TestTwoCallsIntoOneInstanceNeverExecuteAtOnce is #975's witness: two concurrent calls into one component
// instance never execute guest code at the same time.
//
// # What the reference does
//
// One thread runs at a time. `Store.tick` resumes ONE ready thread, and `Thread.resume` runs it until it
// suspends — so two tasks of one instance interleave only at suspension points (`waitable-set.wait`'s
// `wait_until`, a switch), never in parallel. Both tasks may be in flight, and both may be parked at once;
// what cannot happen is both executing.
//
// # What Burroughs did
//
// The sync-lift path (`compFunc.invokeWith`) took no per-instance slot at all — `asyncHandles.entrySem` is
// the async path's, and it is the reference's `exclusive_thread`, which a sync-typed task does not take
// either. So two `CallValues` on one instance's sync export, both woken from `waitable-set.wait`, ran guest
// code on two goroutines over one unshared memory. Measured before the fix: two callers took the wall time
// of one (2.2 ms each way, scaling 10× with the work). #975's Windows race report is the same population
// seen through the detector: both callers' `waitable-set.wait` storing its event at the same `ptr`.
//
// # The witness
//
// `run-slot.wasm`'s `run` waits on a subtask, then calls the same import again as a PROBE, which the impl
// below resolves before returning — so the probe executes inline in the caller's canon call, while that
// caller is executing. The first caller to reach the probe is held there for `parksGrace`, watching for
// the second. Under the reference's model the second cannot arrive: it is woken, but it cannot execute
// until the first suspends or returns. The bound is a TRUE NEGATIVE's bound — under the fix nothing can
// make the second arrive — so the test does not depend on the machine's speed in its passing direction.
//
// **No wasmtime reading**: wasmtime cannot supply `test:async/ops`. The oracle is the reference's execution
// model, cited above by symbol.
//
// **Two neuters, both run, one per half of the design:**
//
//   - removing the slot's acquisition from `invokeWith` lets the second caller reach the probe while the
//     first is inside it — this fails on the overlap assertion, 3 of 3;
//   - making `asyncHandles.suspending` keep the slot across the wait (returning `fn` as is) serializes too much: the
//     second caller cannot even park, and this fails on "only 1 of 2 callers parked". The reference lets
//     both be parked at once, so a slot held across a suspension is the wrong fix, not a stricter one.
func TestTwoCallsIntoOneInstanceNeverExecuteAtOnce(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	b, err := os.ReadFile("testdata/run-slot.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}

	resolvers := make(chan func(canon.Value), 2)
	var mu sync.Mutex
	calls := 0
	secondProbe := make(chan struct{})
	overlap := false

	h := NewHost(io.Discard, io.Discard, nil)
	h.asyncImpls = map[string]asyncLowerImpl{
		"test:async/ops::op": func(_ *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
			onStart()
			mu.Lock()
			calls++
			n := calls
			mu.Unlock()
			switch {
			case n <= 2: // each caller's first call: deferred, so it waits on the set
				resolvers <- onResolve
			case n == 3: // the first probe: hold this caller here and watch for the second
				select {
				case <-secondProbe:
					mu.Lock()
					overlap = true
					mu.Unlock()
				case <-time.After(parksGrace):
				}
				onResolve(canon.U32(3))
			default: // the second probe
				close(secondProbe)
				onResolve(canon.U32(4))
			}
			return func() {}, nil
		},
	}
	in, err := InstantiateWithHost(b, h)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()

	done := make(chan error, 2)
	for range 2 {
		go func() { _, e := in.CallValues("run"); done <- e }()
	}
	// Both callers must be parked in `waitable-set.wait` before either is woken, or the pair is not
	// concurrent in the sense that matters. Two parked at once is the reference's own interleaving.
	var rs []func(canon.Value)
	for len(rs) < 2 {
		select {
		case r := <-resolvers:
			rs = append(rs, r)
		case <-time.After(10 * time.Second):
			t.Fatalf("only %d of 2 callers parked in waitable-set.wait — two calls into one instance "+
				"must both be able to be in flight; a slot held across the wait would serialize them here", len(rs))
		}
	}
	// **And both must actually be PARKED, not merely past handing their resolver over.** The impl sends its
	// resolver before the lowered call has computed its status, so resolving at once can land in that window:
	// the lower then returns RETURNED, the guest takes its legal early return (`packed == 2`), and neither
	// waits nor probes. Measured: 1 run in 100 read "3 calls reached the import" before this wait existed.
	// Polled on the engine's own `set.waiting` count, under the lock that guards it.
	parkedBy := time.Now().Add(10 * time.Second)
	for parkedWaiters(in.w.async) < 2 {
		if time.Now().After(parkedBy) {
			t.Fatalf("%d of 2 callers parked in waitable-set.wait within 10s", parkedWaiters(in.w.async))
		}
		time.Sleep(time.Millisecond)
	}
	for _, r := range rs {
		r(canon.U32(1))
	}
	for range 2 {
		select {
		case e := <-done:
			if e != nil {
				t.Fatalf("run: %v", e)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("a caller never returned")
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 4 {
		t.Fatalf("%d calls reached the import; want 4 (two waits, two probes), so the probe never ran", calls)
	}
	if overlap {
		t.Fatal("the second caller reached the probe while the first was executing inside it: two calls " +
			"into one instance executed guest code at once. The reference runs one thread at a time " +
			"(Store.tick resumes one; Thread.resume runs it until it suspends)")
	}
}

// parkedWaiters counts the agents parked in `waitable-set.wait` across the instance's sets — each set's
// `waiting`, read under `asyncHandles.mu` as the engine writes it.
func parkedWaiters(h *asyncHandles) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, e := range h.entries {
		if s, ok := e.(*waitableSet); ok {
			n += s.waiting
		}
	}
	return n
}
