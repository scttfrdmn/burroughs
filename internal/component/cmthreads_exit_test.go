// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"bytes"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/interp"
)

// TestAThreadsExitEndsARunThatYields is #1265's witness: a created component-model Thread calls `wasi:cli/exit`
// while the run is still going, and `CallRun` returns the Thread's code, as wasmtime 49.0.3 exits with it
// (`cmthreads/exit-from-thread.reading`, 10 of 10 each).
//
// Each fixture's run async-lowers a composed child's `f`, whose Thread T calls `exit-with-code(9)`. Then:
//   - **exit-from-thread**: `f` has resolved, so T's exit is an orphan trap, and the run YIELDs forever;
//   - **exit-from-thread-live**: `f` YIELDs forever unresolved, so T's exit ends a live task, and the run
//     YIELDs forever;
//   - **exit-from-thread-wait**, the control: as exit-from-thread, with the run WAITing on a future nobody
//     writes.
//
// The exit reached the store lock in all three at 2c4371d, and the control exited 9 there: the WAIT arm selects
// on the lock. The two YIELD runs hung, because the YIELD arm had no lock case (#1265, ADR 0105). The issue named
// the cause as the component's `exitError` not being an `interp.InstanceEnder`. That interface is read only by
// `world.retire`, which a created Thread never reaches, since a Thread runs on its instance's host agent.
//
// Bounded, because the defect is a hang.
//
// **Neuter, run:** `yieldPoint` returning `yieldWait`'s result alone. exit-from-thread and exit-from-thread-live
// fail on the bound, and the control still exits 9.
func TestAThreadsExitEndsARunThatYields(t *testing.T) {
	t.Setenv(cmThreadsGateEnv, "1")
	t.Setenv(asyncGateEnv, "1")
	for _, name := range []string{"exit-from-thread", "exit-from-thread-live", "exit-from-thread-wait"} {
		t.Run(name, func(t *testing.T) {
			reading := readingLine(t, "testdata/cmthreads/exit-from-thread.reading", "wasmtime 49.0.3 on "+name+", 10 of 10")
			want, err := strconv.Atoi(reading)
			if err != nil {
				t.Fatalf("exit-from-thread.reading's line for %s: %v", name, err)
			}
			wasm, err := os.ReadFile("testdata/cmthreads/" + name + ".wasm")
			if err != nil {
				t.Fatalf("the committed fixture is missing: %v", err)
			}
			var stdout, stderr bytes.Buffer
			in, err := InstantiateWithHost(wasm, NewHost(&stdout, &stderr, nil))
			if err != nil {
				t.Fatalf("instantiate: %v", err)
			}
			defer in.Close()

			type outcome struct {
				code int
				err  error
			}
			done := make(chan outcome, 1)
			go func() {
				code, err := in.CallRun()
				done <- outcome{code, err}
			}()
			var o outcome
			select {
			case o = <-done:
			case <-time.After(10 * time.Second):
				t.Fatalf("run did not return within 10s, where wasmtime 49.0.3 exits %d: the created Thread's exit "+
					"did not end a run that is still going", want)
			}
			if o.err != nil {
				t.Fatalf("run: %v; wasmtime 49.0.3 exits %d", o.err, want)
			}
			if o.code != want {
				t.Errorf("run exited %d, want wasmtime 49.0.3's %d, the code the Thread passed to exit-with-code", o.code, want)
			}
			if stdout.Len() != 0 || stderr.Len() != 0 {
				t.Errorf("run wrote stdout %q and stderr %q; wasmtime 49.0.3 writes nothing to either", stdout.String(), stderr.String())
			}
		})
	}
}

// TestAThreadsTrapAtItsTasksYieldIsTheCallsOwn is the YIELD arm's lock case seen from the trapping call (#1265).
// `cmthreads/yield-own-trap.wasm`'s `run` YIELDs forever while its own Thread T traps, which wasmtime 49.0.3
// reports as T's `unreachable` (build.sh asserts it). The routing that hands `run` the trap also locks the store,
// so the arm's check would find the lock. ADR 0105 has the trapping call return its own trap unchanged, so the
// arm leaves this task's own fault to the loop.
//
// **Neuter, run:** `yieldPoint` without its `faultOf` test. `run` returns ErrInstanceLocked wrapping the trap.
func TestAThreadsTrapAtItsTasksYieldIsTheCallsOwn(t *testing.T) {
	t.Setenv(cmThreadsGateEnv, "1")
	t.Setenv(asyncGateEnv, "1")
	wasm, err := os.ReadFile("testdata/cmthreads/yield-own-trap.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	in, err := Instantiate(wasm)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()
	done := make(chan error, 1)
	go func() {
		_, cerr := in.CallValues("run")
		done <- cerr
	}()
	select {
	case err = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return within 10s; wasmtime 49.0.3 traps in T")
	}
	var trap *interp.Trap
	if !errors.As(err, &trap) || !strings.Contains(trap.Reason, "unreachable") {
		t.Fatalf("run: got %v; want T's unreachable trap, as wasmtime 49.0.3 traps", err)
	}
	if errors.Is(err, ErrInstanceLocked) {
		t.Errorf("run: got %v; want its own trap, not a lockdown refusal (ADR 0105: the trapping call returns its "+
			"own trap unchanged)", err)
	}
}
