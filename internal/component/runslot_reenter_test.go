package component

import (
	"os"
	"testing"
	"time"
)

// TestAChainReentersAnInstanceItAlreadyHolds is the re-entry witness: parent → child → parent, all
// synchronous, through a lift the parent passed its child.
//
// `reenter.wasm`'s parent has two core instances. P1 exports `f`, whose lift the parent passes to a nested
// child; P2's `run` calls the child's `g`, and `g` calls `f` and adds 1. P1 and P2 share the parent's run
// slot. The reference has no reentrance rule at this pin (`canon_lower` checks only `may_leave`), and the
// whole chain is one thread there, so it returns 7 + 1. **wasmtime 49.0.2 returns 8, on 3 of 3 runs.**
//
// **#976 broke this (#984), and the deadlock analysis for #983 found it.** #976 recorded the run slot's holder by agent
// index, so the inner entry into the parent waited for a slot its own call chain held, on its own goroutine:
// a self-deadlock. Burroughs returned 8 at `d2eccdf`, the commit before #976, and hung on `7dddfea`. The
// slot is now held by a CALL CHAIN, which the synchronous cross-component arm passes on, so the inner entry
// re-enters it.
//
// **Bounded**, because the defect is a hang.
//
// **Neuter, run:** dropping the chain from the synchronous arm (passing a bare `context.Background()`, as
// before) makes `run` hang, and this fails on its bound.
func TestAChainReentersAnInstanceItAlreadyHolds(t *testing.T) {
	b, err := os.ReadFile("testdata/reenter.wasm")
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
		t.Fatal("run did not return within 10s: the call back into the parent is waiting for a run slot " +
			"its own synchronous call chain holds — a self-deadlock the reference cannot have")
	}
}
