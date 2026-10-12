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

// TestAResultStoreWaitsForTheExecutingTask is B's witness (ADR 0101's result-store amendment): a subtask's
// result store, made from a goroutine outside the instance, waits until the task executing there has
// released the run slot.
//
// # What it constructs
//
// `result-store-heap.wasm`'s `run` async-lowers an import, waits on the subtask, then runs a long load
// loop over its retptr. Two callers X and Y park in the wait. X alone is resolved, and the test waits
// until X has left the wait AND the slot is held again: X is executing its loop. Only then is Y resolved,
// from this goroutine. Y's result store writes Y's retptr into the guest memory X is reading.
//
// **Under the fix Y's resolve cannot return until X has returned**, because the store takes the run slot,
// and X holds it until it returns. That ordering is the assertion, and it is deterministic: the slot is
// the only thing that can make Y's resolve wait.
//
// **And the memory is heap-backed** (`(memory 1 1)`, #979), so under `-race` the detector can see the
// store and X's loads. Under the neuter the race job reports the pair the Windows job reported on
// `9f11079`.
//
// **Neuter, run:** skipping the acquisition in `asyncLowerFunc`'s `onResolve` lets Y's resolve return
// while X is still executing, and this fails on the ordering assertion. Under `-race` it also reports the
// race.
func TestAResultStoreWaitsForTheExecutingTask(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	b, err := os.ReadFile("testdata/result-store-heap.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	h, resolvers, _ := waitsetSynthHost()
	in, err := InstantiateWithHost(b, h)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()

	var xDone atomic.Int64 // when the first caller to return did so
	done := make(chan error, 2)
	for range 2 {
		go func() {
			_, e := in.CallValues("run")
			xDone.CompareAndSwap(0, time.Now().UnixNano())
			done <- e
		}()
	}
	var rs []func(canon.Value)
	for len(rs) < 2 {
		select {
		case r := <-resolvers:
			rs = append(rs, r)
		case <-time.After(10 * time.Second):
			t.Fatalf("only %d of 2 callers reached the import", len(rs))
		}
	}
	waitFor(t, "both callers parked", func() bool { return parkedWaiters(in.w.async) == 2 })

	rs[0](canon.U32(1)) // X
	waitFor(t, "X executing (left the wait, slot held)", func() bool {
		in.w.async.mu.Lock()
		held := in.w.async.runChain != nil
		in.w.async.mu.Unlock()
		return parkedWaiters(in.w.async) == 1 && held
	})

	rs[1](canon.U32(2)) // Y's store, from this goroutine, while X executes
	yReturned := time.Now().UnixNano()

	for range 2 {
		select {
		case e := <-done:
			if e != nil {
				t.Fatalf("run: %v", e)
			}
		case <-time.After(30 * time.Second):
			t.Fatal("a caller never returned")
		}
	}
	if x := xDone.Load(); x == 0 || yReturned < x {
		t.Fatalf("Y's result store returned before X finished executing (Y at %d, X at %d): the store did "+
			"not wait for the run slot, so it wrote guest memory while another task of the instance "+
			"executed — the reference stores results while no other thread runs", yReturned, x)
	}
}

// waitFor polls a condition the engine sets, up to a bound — the wait is on a real signal, the poll
// interval is not the wait.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for: %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestAChildsResultStoreWaitsForTheParentsExecutingTask is B on the production path: a NESTED child
// component resolves a parent's subtask from the child's own goroutine (`crossComponentResolve` →
// `asyncLowerFunc`'s `onResolve`), and the store into the parent's memory must wait for the parent's
// executing task.
//
// `xc-gated.wasm`'s parent `run(id)` awaits the child's `work(id)`, and the child's `work` awaits the host's
// `op(id)`, which this test controls:
//
//  1. Caller 2 starts. Its child call reaches `op(2)`, which is held. The parent task returns WAIT, parked
//     with no slot.
//  2. Caller 1 starts. `op(1)` resolves at once, the child returns 101, and caller 1 SPINS over caller 2's
//     retptr while holding the parent's run slot, recording whether it changes.
//  3. Once caller 1 has held the slot for a sustained interval (the spin; everything before it is brief),
//     the test releases `op(2)`. The child returns 102 on its goroutine, and that store targets caller 2's
//     retptr, the word caller 1 is reading.
//
// **Under the fix the store waits for the slot**, so caller 1 never sees the word change and returns
// exactly 101. Caller 2 then returns 102. Both values are wasmtime 49.0.2's for `xc.wasm`, the ungated
// twin (3 of 3 each). wasmtime's CLI runs one call at a time, so the ordering's oracle is the reference's
// model: one thread runs at a time.
//
// **Neuter, run:** skipping the acquisition in `onResolve` lets the store land mid-spin. Caller 1 returns
// `1<<16 | 101`, and this fails on the value.
func TestAChildsResultStoreWaitsForTheParentsExecutingTask(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	b, err := os.ReadFile("testdata/xc-gated.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	held := make(chan func(canon.Value), 1)
	h := NewHost(io.Discard, io.Discard, nil)
	h.asyncImpls = map[string]asyncLowerImpl{
		"test:async/ops::op": func(_ *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
			ps := onStart()
			if len(ps) > 0 && ps[0].Int32() == 2 {
				held <- onResolve // caller 2's: the test decides when
				return func() {}, nil
			}
			onResolve(canon.U32(1)) // caller 1's: at once
			return func() {}, nil
		},
	}
	in, err := InstantiateWithHost(b, h)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()
	parent := in.w.async
	slotHeld := func() bool {
		parent.mu.Lock()
		defer parent.mu.Unlock()
		return parent.runChain != nil
	}

	type out struct {
		v   uint32
		err error
	}
	call := func(id uint32, res chan<- out) {
		r, e := in.CallValues("run", canon.U32(id))
		if e != nil {
			res <- out{err: e}
			return
		}
		v, _ := r[0].U32()
		res <- out{v: v}
	}
	r2, r1 := make(chan out, 1), make(chan out, 1)
	go call(2, r2)
	var release func(canon.Value)
	select {
	case release = <-held:
	case <-time.After(10 * time.Second):
		t.Fatal("caller 2's child never reached op(2)")
	}
	waitFor(t, "caller 2 parked (the parent's slot free)", func() bool { return !slotHeld() })

	go call(1, r1)
	// Caller 1 is spinning once it has held the slot continuously for a sustained interval: everything it
	// does before the spin is a handful of calls.
	waitFor(t, "caller 1 spinning under the parent's slot", func() bool {
		for range 20 {
			if !slotHeld() {
				return false
			}
			time.Sleep(250 * time.Microsecond)
		}
		return true
	})
	release(canon.U32(2))

	for _, c := range []struct {
		id   uint32
		ch   chan out
		want uint32
	}{{1, r1, 101}, {2, r2, 102}} {
		select {
		case o := <-c.ch:
			if o.err != nil {
				t.Fatalf("run(%d): %v", c.id, o.err)
			}
			if o.v != c.want {
				t.Errorf("run(%d) returned %#x; want %d. A set bit 16 means caller 1 saw caller 2's result "+
					"store land while it executed: the child's store did not wait for the parent's run slot",
					c.id, o.v, c.want)
			}
		case <-time.After(30 * time.Second):
			t.Fatalf("run(%d) never returned", c.id)
		}
	}
}
