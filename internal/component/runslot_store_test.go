package component

import (
	"io"
	"os"
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
// `result-store-heap-cb.wasm`'s `run` async-lowers an import into retptr 0, waits on the subtask, then runs a
// long loop watching retptr 0 for a change, and returns `(changed << 16) | mem[0]`. It is an async callback
// lift, which parks by returning WAIT. Until 2026-10-16 this drove `result-store-heap.wasm`, a sync lift
// parked in `waitable-set.wait`, which both oracles trap (`TestASyncWaitOnAnotherInstancesEventTraps`).
// The neuter below was re-run on the callback shape. Two callers X and Y
// park in the wait, and both lowered into the same word. X alone is resolved, with 1. The test then waits
// until X has left the wait AND the slot is held again, i.e. X is executing its loop, and only then
// resolves Y, with 2, from this goroutine. Y's result store writes the word X is watching.
//
// **The assertion is the guest's observation, not a clock.** Under the fix, Y's store takes the run slot
// and X holds it until it returns, so X never sees the word change. X returns 1, and Y, which runs after,
// returns 2. Under the neuter the store lands mid-loop, and X returns `0x10002`.
//
// **It used to compare two wall-clock points and was wrong to.** It asserted that Y's resolve returned after
// X's `CallValues` did. But X releases the slot inside `invokeWith`, BEFORE `CallValues` returns, and Y's
// store may legitimately complete in that gap. CI's ubuntu amd64 runner caught it at 2 ms, on #986, a PR
// that touched none of this.
//
// **And the memory is heap-backed** (`(memory 1 1)`, #979), so under `-race` the detector can see the
// store and X's loads. Under the neuter the race job reports the pair the Windows job reported on
// `9f11079`.
//
// **Neuter, run:** skipping the acquisition in `asyncLowerFunc`'s `onResolve` lets the store land while X
// executes, and this fails on X's value. Under `-race` it also reports the race. On the callback shape it
// failed 5 of 5 with `0x10002`, and the race was reported under `-race`.
func TestAResultStoreWaitsForTheExecutingTask(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	b, err := os.ReadFile("testdata/result-store-heap-cb.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	h, resolvers, _ := waitsetSynthHost()
	in, err := InstantiateWithHost(b, h)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()

	type out struct {
		v   uint32
		err error
	}
	results := make(chan out, 2)
	for range 2 {
		go func() {
			r, e := in.CallValues("run")
			if e != nil {
				results <- out{err: e}
				return
			}
			v, _ := r[0].U32()
			results <- out{v: v}
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
	rs[1](canon.U32(2)) // Y's store, from this goroutine, into the word X is watching

	got := map[uint32]int{}
	for range 2 {
		select {
		case o := <-results:
			if o.err != nil {
				t.Fatalf("run: %v", o.err)
			}
			got[o.v]++
		case <-time.After(30 * time.Second):
			t.Fatal("a caller never returned")
		}
	}
	if got[1] != 1 || got[2] != 1 {
		t.Fatalf("the two callers returned %v; want exactly one 1 (X: the word never changed while it "+
			"executed) and one 2 (Y). A value with bit 16 set (0x10002) is X seeing Y's result store land "+
			"while it executed: the store did not wait for the run slot", got)
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
