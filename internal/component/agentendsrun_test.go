// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/interp"
)

// #1357's witnesses (ADR 0132): an exit or a trap on a spawned agent ends a run parked in `memory.atomic.wait32`, as
// an exit from run's own agent does (contract §2 T-2: no main-thread special case). The fixtures are
// `testdata/agent-ends-run-{exit,trap,control}.wat`, each a `wasi:cli/run` component whose agent is started through
// the spawn door (ADR 0131). Their memories declare max = min, so they are Go-heap backed and `-race` sees them.

// agentEndsRuns is how many runs each row must EXERCISE: runs whose futex waiter was seen parked before the run ended.
// A row runs fresh instances until that many have, or agentEndsAttempts have run.
//
// **Counted by the place, not by the attempt** (#1361's Windows run). The fixture's agent gives run 100ms to reach
// its wait before it exits or traps. That is a bound, and a slow runner outran it: in 1 of 10 runs the trap came first,
// the run still ended with the trap, and the waiter was never seen parked, so that run witnessed nothing. The guest
// cannot see another agent parked, but the test can, so the count is of runs the test saw parked. A run that never
// parked is still checked for its outcome; it just does not count. The guard keeps its teeth: a fixture that never
// parks reaches agentEndsAttempts with 0 exercised and fails. This is grave #1360's shape, a timing witness that
// needs a place.
const agentEndsRuns = 10

// agentEndsAttempts caps a row's fresh instances, at three per run it must exercise.
const agentEndsAttempts = 3 * agentEndsRuns

// agentEndsBound is how long a run may take to return once its futex waiter has been seen parked. The fixture's
// agent ends the run 100ms after run publishes that it is about to wait, so only a run nothing ends waits it out.
const agentEndsBound = 3 * time.Second

// agentEndsRows are the three rows. wantCode is CallRun's code; wantTrap means CallRun returns the agent's trap.
var agentEndsRows = []struct {
	fixture  string
	what     string
	wantCode int
	wantTrap bool
}{
	{"agent-ends-run-exit.wasm", "(a) a spawned agent calls exit-with-code(7) while run is parked in wait32", 7, false},
	{"agent-ends-run-trap.wasm", "(b) a spawned agent executes unreachable while run is parked in wait32", 0, true},
	{"agent-ends-run-control.wasm", "(c) run calls exit-with-code(7) while the agent is parked in wait32", 7, false},
}

// engineCreated is every goroutine an engine file started, keyed on its `created by` function line, which carries the
// module path, and not on its file line, which carries the checkout's.
func engineCreated(stacks string) []string {
	var out []string
	for _, g := range strings.Split(stacks, "\n\n") {
		_, created, ok := strings.Cut(g, "\ncreated by ")
		if !ok {
			continue
		}
		lines := strings.SplitN(created, "\n", 3)
		if len(lines) >= 2 && strings.HasPrefix(lines[0], "github.com/scttfrdmn/burroughs/internal/") &&
			!strings.Contains(lines[1], "_test.go") {
			out = append(out, g)
		}
	}
	return out
}

// futexPlace names every goroutine parked in `memory.atomic.wait32`, whoever started it (run's is the test's own
// goroutine), and then every engine goroutine, so a run that did not return names where it is.
func futexPlace(stacks string) string {
	var b strings.Builder
	parked := parkedIn(stacks, parkFrames[1])
	fmt.Fprintf(&b, "%d goroutine(s) parked in memory.atomic.wait32:", len(parked))
	for _, g := range parked {
		describeGoroutine(&b, g)
	}
	b.WriteString("\n" + agentsPlace(stacks))
	return b.String()
}

// TestAnAgentsExitOrTrapEndsARunParkedInAFutexWait is #1357's witness (ADR 0132). Each row runs fresh instances through
// CallRun, as `burroughs run` does, until agentEndsRuns have been exercised (see agentEndsRuns), and asserts, per run:
//   - **the futex waiter was seen parked** in `interp.(*memory).wait` before the run ended: run in rows (a) and (b),
//     the agent in row (c). A run that never parked did not exercise its row;
//   - **the run returned within agentEndsBound**, with exit code 7 for (a) and (c), and with the agent's trap for
//     (b). A run that does not return is a verdict naming where it is parked, and Close then ends it, so the test
//     never hangs;
//   - **Close returns within closeEndsBound, and no engine goroutine survives it.**
//
// Read before the repair, at b71b22d: (a) and (b) hung in 10 of 10 runs each, with run parked in
// `interp.(*memory).wait` and no engine goroutine left, and (c) exited 7 in 10 of 10.
func TestAnAgentsExitOrTrapEndsARunParkedInAFutexWait(t *testing.T) {
	t.Setenv(cmSharedMemoryGateEnv, "1")
	for _, row := range agentEndsRows {
		t.Run(strings.TrimSuffix(row.fixture, ".wasm"), func(t *testing.T) {
			var ended, hung, unparked, exercised, attempts int
			firstHang := ""
			for i := 0; exercised < agentEndsRuns && i < agentEndsAttempts; i++ {
				attempts++
				baseline := len(engineCreated(goroutineStacks()))
				in := instantiateFixtureWithHost(t, row.fixture, NewHost(io.Discard, io.Discard, nil))
				type out struct {
					code int
					err  error
				}
				done := make(chan out, 1)
				go func() {
					code, err := in.CallRun()
					done <- out{code, err}
				}()
				// NEUTER, run: with this naming no frame, row (b) reads 30 of 30 attempts never seen parked and 0
				// exercised, and fails at agentEndsAttempts.
				parked := awaitParked(parkFrames[1], 1, wakeProbeBound)
				if !parked {
					unparked++
				}
				var o out
				returned := false
				select {
				case o = <-done:
					returned = true
				case <-time.After(agentEndsBound):
					hung++
					if firstHang == "" {
						firstHang = futexPlace(goroutineStacks())
					}
				}
				closed := make(chan struct{})
				go func() {
					in.Close()
					close(closed)
				}()
				select {
				case <-closed:
				case <-time.After(closeEndsBound):
					t.Fatalf("run %d: Close did not return within %v. %s", i, closeEndsBound, futexPlace(goroutineStacks()))
				}
				if !returned {
					select {
					case o = <-done: // Close ended the parked run, by T-5.4
					case <-time.After(10 * time.Second):
						t.Fatalf("run %d: the run did not return even after Close. %s", i, futexPlace(goroutineStacks()))
					}
				} else {
					ended++
					checkAgentEndsOutcome(t, i, row.what, row.wantCode, row.wantTrap, o.code, o.err)
					if parked {
						exercised++
					}
				}
				deadline := time.Now().Add(5 * time.Second)
				for len(engineCreated(goroutineStacks())) > baseline && time.Now().Before(deadline) {
					time.Sleep(10 * time.Millisecond)
				}
				if left := engineCreated(goroutineStacks()); len(left) > baseline {
					t.Fatalf("run %d: %d engine goroutines survived Close (%d before the fixture):\n%s", i, len(left),
						baseline, strings.Join(left, "\n\n"))
				}
			}
			t.Logf("%s: %d of %d runs ended within %v, %d hung, %d never seen parked, %d exercised", row.what, ended,
				attempts, agentEndsBound, hung, unparked, exercised)
			if hung > 0 {
				t.Errorf("%s: %d of %d runs did not return within %v. The first: %s", row.what, hung, attempts,
					agentEndsBound, firstHang)
			}
			if exercised < agentEndsRuns {
				t.Errorf("%s: only %d of %d attempts exercised the row (the futex waiter seen parked, then the run "+
					"ended); %d never parked", row.what, exercised, attempts, unparked)
			}
		})
	}
}

// checkAgentEndsOutcome checks one run's CallRun result against its row.
func checkAgentEndsOutcome(t *testing.T, i int, what string, wantCode int, wantTrap bool, code int, err error) {
	t.Helper()
	if !wantTrap {
		if err != nil || code != wantCode {
			t.Errorf("run %d: %s: CallRun = %d, %v; want %d, nil", i, what, code, err, wantCode)
		}
		return
	}
	var trap *interp.Trap
	var ee exitError
	if !errors.As(err, &trap) || errors.As(err, &ee) || !strings.Contains(err.Error(), "unreachable") {
		t.Errorf("run %d: %s: CallRun = %d, %v; want the agent's unreachable trap", i, what, code, err)
		return
	}
	// The agent is a thread of run's task (ADR 0130), and routeFault records its trap on run as its owner, so run
	// reports the trap as its own, as an owner parked in a component-model wait does, and not as the lock refusal a
	// call that merely overlapped another task's trap gets.
	if errors.Is(err, ErrInstanceLocked) {
		t.Errorf("run %d: %s: CallRun = %v; want the agent's trap as run's own, not ErrInstanceLocked", i, what, err)
	}
	if i == 0 {
		t.Logf("%s: the run returned %v", what, err)
	}
}
