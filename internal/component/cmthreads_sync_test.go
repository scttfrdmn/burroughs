package component

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// # #970: the sync lift's scheduling branch
//
// The reference's `canon_lift` ends a sync lift's thread with
//
//	if not ft.async_:
//	  while task.state != Task.State.RESOLVED:
//	    candidates = { t for t in inst.threads if t.ready() }
//	    trap_if(not candidates)
//	    random.choice(list(candidates)).resume()
//
// so a sync callee that suspends lets ready Threads run, and traps if none is ready. Witnessed against
// `sync-spawn.reading` and `sync-trap.reading`, taken with wasmtime 49.0.2 before the engine change.

// TestASyncLiftRunsASpawnedThreadBeforeItReturns is `sync-spawn.reading`'s differential: a sync callee
// creates a Thread, readies it, and suspends. The body sets bit 1 and readies the caller, which resumes and
// returns `mem[0]`. wasmtime returns 3 on 10 of 10 runs.
//
// **Neuter, run:** making `threadSwitch` refuse a sync caller again (`self.task == nil`) fails this on the
// refusal.
func TestASyncLiftRunsASpawnedThreadBeforeItReturns(t *testing.T) {
	t.Setenv(cmThreadsGateEnv, "1")
	t.Setenv(asyncGateEnv, "1")
	want := readCmthreadsResult(t, "sync-spawn.reading")
	wasm, err := os.ReadFile("testdata/cmthreads/sync-spawn.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	in, err := Instantiate(wasm)
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	defer in.Close()
	if got := callRunBounded(t, in); got != want {
		t.Fatalf("run returned %#b; sync-spawn.reading records %#b (bit 0: the entry, bit 1: the body ran "+
			"before the sync lift returned)", got, want)
	}
	if n := in.w.threads.live(); n != 0 {
		t.Errorf("%d Thread(s) still registered; the body finished and the sync lift returned", n)
	}
}

// TestASyncLiftThatSuspendsWithNothingReadyTraps is `sync-trap.reading`'s differential: a sync callee
// creates a Thread it never readies, then suspends. Nothing is runnable and the task is unresolved, so the
// reference traps, and wasmtime traps with *"cannot block a synchronous task before returning"* on 10 of 10.
//
// **Bounded**, because the defect this guards is a HANG: an async callee in the same position simply does
// not progress (ADR 0100), and a sync branch that treated its origin the same way would leave `run` waiting
// forever.
//
// **Neuter, run:** deleting the sync arm in `cmScheduler.loop` makes `run` hang, and this fails on its
// bound.
func TestASyncLiftThatSuspendsWithNothingReadyTraps(t *testing.T) {
	t.Setenv(cmThreadsGateEnv, "1")
	t.Setenv(asyncGateEnv, "1")
	wasm, err := os.ReadFile("testdata/cmthreads/sync-trap.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	in, err := Instantiate(wasm)
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	defer in.Close()
	errc := make(chan error, 1)
	go func() { _, e := in.CallValues("run", canon.U32(0)); errc <- e }()
	var got error
	select {
	case got = <-errc:
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return within 10s: a sync callee suspended with nothing ready is parked " +
			"forever, where the reference's sync branch traps (trap_if(not candidates))")
	}
	var trap *interp.Trap
	if !errors.As(got, &trap) {
		t.Fatalf("not a trap: %v — sync-trap.reading records wasmtime trapping", got)
	}
	if !strings.Contains(trap.Reason, "cannot block a synchronous task before returning") {
		t.Errorf("the trap does not say what wasmtime's says: %q", trap.Reason)
	}
}
