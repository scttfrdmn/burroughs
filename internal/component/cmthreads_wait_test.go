package component

import (
	"os"
	"testing"
	"time"
)

// TestACreatedThreadsWaitReleasesTheRunSlot is #977's witness: a created Thread parked in
// `waitable-set.wait` gives up its chain's run slot, as the reference's `wait_until` → `block()` gives up
// the thread.
//
// `cmthreads/thread-wait-callback.wasm`:
//   - the parent's async `run` spawns Thread T and YIELDs until T is done;
//   - T async-lowers a nested child's `g` and parks in `waitable-set.wait` for it;
//   - the child's `g` calls back into a lift of the parent's own `f`, which the parent passed it
//     (`walker.compArgRef`), and returns `f() + 1`.
//
// **wasmtime 49.0.2 returns 8, on 10 of 10 runs.** Before #977 Burroughs hung. T's wait released nothing,
// so the child's call into the parent waited for the slot T's chain held, and T waited for the child.
//
// **Bounded**, because the defect is a hang.
//
// **Neuter, run:** making the wait keep the run slot, with `suspending` calling `fn` without giving the
// slot up, brings the hang back, and this fails on its bound.
//
// The neuter first named here was restricting `suspending` to implicit Threads (the `rec.implicit`
// condition). ADR 0101's Option A removed that branch, because under A every waiting Thread gives the slot
// up, so the neuter was restated as the amendment predicted.
func TestACreatedThreadsWaitReleasesTheRunSlot(t *testing.T) {
	t.Setenv(cmThreadsGateEnv, "1")
	t.Setenv(asyncGateEnv, "1")
	b, err := os.ReadFile("testdata/cmthreads/thread-wait-callback.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	in, err := Instantiate(b)
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	type out struct {
		v   uint32
		err error
	}
	ch := make(chan out, 1)
	go func() {
		r, e := in.CallValues("run")
		if e != nil {
			ch <- out{err: e}
			return
		}
		v, _ := r[0].U32()
		ch <- out{v: v}
	}()
	select {
	case o := <-ch:
		in.Close()
		if o.err != nil {
			t.Fatalf("run: %v", o.err)
		}
		if o.v != 8 {
			t.Fatalf("run returned %d; wasmtime returns 8 (7 from the parent's f, +1 in the child's g)", o.v)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return within 10s: the child's call back into the parent is waiting for a run " +
			"slot that the waiting Thread's chain holds, and the Thread is waiting for the child")
	}
}
