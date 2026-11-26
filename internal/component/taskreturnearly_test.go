// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// readTaskReturnEarly calls task-return-early.wasm's `go(row)` n times in one gate state, a fresh instance each
// time, and counts the outcomes: "value N", "trap: <reason>" for a call that trapped, "error: <text>" for any
// other failure, and "hang" for a call that did not return within the bound.
func readTaskReturnEarly(t *testing.T, row uint32, gate string, n int) map[string]int {
	t.Helper()
	t.Setenv(asyncGateEnv, "1")
	t.Setenv(cmThreadsGateEnv, gate)
	b, err := os.ReadFile("testdata/task-return-early.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	counts := map[string]int{}
	for range n {
		in, err := Instantiate(b)
		if err != nil {
			t.Fatalf("Instantiate: %v", err)
		}
		ch := make(chan string, 1)
		go func() {
			res, e := in.CallValues("go", canon.U32(row))
			var tr *interp.Trap
			switch {
			case errors.As(e, &tr):
				ch <- "trap: " + tr.Reason
			case e != nil:
				ch <- "error: " + e.Error()
			default:
				v, _ := res[0].U32()
				ch <- fmt.Sprintf("value %d", v)
			}
		}()
		select {
		case o := <-ch:
			counts[o]++
		case <-time.After(optionABound):
			counts["hang"]++
			in.Close()
			<-ch // Close ends the call; waited for, so no call outlives its instance
		}
		in.Close()
		in.Close() // a second Close probes that the first closed everything
	}
	return counts
}

// TestACrossComponentCallerIsResolvedAtTaskReturn is E2-in-a's cross-component witness (ADR 0136): a parent is
// resolved at its child's `task.return`, while the child is still running, and the child runs on to its exit after
// it. Each row's value is wasmtime 49.0.3's, 10 of 10, and is in the pin's set (`task-return-early.reading`):
//   - early (66055): the child returns in its first segment and YIELDs until released, so the parent's lower
//     reads RETURNED (2) and the child's `exited` reads 0 when the parent has the result;
//   - later (65799): the child returns in its first callback, so the result arrives by the parent's WAIT
//     (STARTED, 1), with `exited` still 0;
//   - at-exit (69895): the child returns and EXITs in one callback, the case main already read right.
//
// Bit 16 of each is the child running on to its EXIT after the parent released it. **The place is the child's own
// state, not a duration**: it cannot reach EXIT before the parent's `release`, and the parent reads `exited` before
// it releases.
//
// On main, early and later hung 3 of 3 per gate state: the child waits for the parent's release and the parent for
// a result the adapter delivered only at the child's exit. **NEUTER, run:** taskReturn skips the hook, and both
// rows hang again in both gate states, while at-exit still reads 69895 (the exit's resolution is all it needs).
func TestACrossComponentCallerIsResolvedAtTaskReturn(t *testing.T) {
	rows := []struct {
		row  uint32
		name string
		want string
	}{
		{0, "early", "value 66055"},
		{1, "later", "value 65799"},
		{5, "at-exit", "value 69895"},
	}
	for _, gate := range []string{"0", "1"} {
		for _, r := range rows {
			got := readTaskReturnEarly(t, r.row, gate, 20)
			if got[r.want] != 20 {
				t.Errorf("gate=%q %s: outcomes %v, want %s every time", gate, r.name, got, r.want)
			}
		}
	}
}

// TestAReturnInsideTheCancelHandBackIsResolvedAtTheExit reads task-return-early's cancel-poll row: the parent
// cancels g's subtask with the async subtask.cancel and, without suspending, polls g's sync `exited`; g's callback
// is handed TASK_CANCELLED, calls task.return(7) and EXITs. The pin reads 4615, resuming g inside the cancel, and so
// did main. wasmtime 49.0.3 runs g only when the canceller suspends, so its canceller spins: no result in 10s, 3 of
// 3. Burroughs runs that callback beside its canceller, inside upstream #726's hand-back window (#999), which is the
// one place the on-return hook leaves the resolution to the exit (ADR 0136).
//
// **NEUTER, run:** resolve inside the window too (drop liftAsAsyncImpl's hand-back test), and the first run, gate off,
// deadlocks, and Close did not end it, so the test binary timed out. Its goroutine dump is the cycle: g's taskReturn
// in onResolve's acquireRun, holding g's run slot and waiting for the parent's, and the parent's invokeSync of
// `exited` in acquireRun, holding the parent's and waiting for g's. **Since ADR 0139 (#1398) Close ends it**: the same
// neuter, through the seam `answerInsideHandBack`, reads hang in 20 of 20 runs in both gate states, each ended by the
// read's Close. TestCloseEndsACrossInstanceRunSlotDeadlock is that witness.
func TestAReturnInsideTheCancelHandBackIsResolvedAtTheExit(t *testing.T) {
	for _, gate := range []string{"0", "1"} {
		got := readTaskReturnEarly(t, 6, gate, 20)
		if got["value 4615"] != 20 {
			t.Errorf("gate=%q cancel-poll: outcomes %v, want value 4615 every time", gate, got)
		}
	}
}

// TestACrossComponentCalleesTrapEndsTheCall reads task-return-early's three trap rows: a child's trap locks the
// store (def:trap's lockdown, ADR 0105) whether or not it had returned, so the parent's call ends with that trap,
// as it ends on both oracles:
//   - trap-after: the child returns, YIELDs, and traps in its callback while the parent, holding the result, WAITs
//     on an empty set. wasmtime 49.0.3: "wasm `unreachable` instruction executed";
//   - twice: the child's second `task.return` traps. wasmtime 49.0.3: "`task.return` or `task.cancel` called more
//     than once for current task";
//   - no-return: the child EXITs without returning. wasmtime 49.0.3: "async-lifted export failed to produce a
//     result".
//
// Each message here is Burroughs' own for the same rule. On main the child's trap resolved the parent RETURNED with
// nothing, so trap-after and twice hung and no-return read 70144, a result of 0. **NEUTER, run:** skip the adapter's
// isTrap arm, so no lock is set, and the three rows read hang, hang and 70144 in both gate states.
func TestACrossComponentCalleesTrapEndsTheCall(t *testing.T) {
	rows := []struct {
		row  uint32
		name string
		want string
	}{
		{2, "trap-after", "trap: unreachable"},
		{3, "twice", "trap: task.return called on a task that is already resolved"},
		{4, "no-return", "trap: async lift returned EXIT without resolving its task via task.return"},
	}
	for _, gate := range []string{"0", "1"} {
		for _, r := range rows {
			got := readTaskReturnEarly(t, r.row, gate, 10)
			if got[r.want] != 10 {
				t.Errorf("gate=%q %s: outcomes %v, want %q every time", gate, r.name, got, r.want)
			}
		}
	}
}
