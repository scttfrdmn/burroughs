// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package burroughs

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// TestComponentConfigRunEndsWhenAnAgentExitsOrTraps is #1357's witness at the public boundary (ADR 0132):
// `ComponentConfig.Run` over the three `internal/component/testdata/agent-ends-run-*.wasm` fixtures, each a
// `wasi:cli/run` component whose agent is started through the spawn door.
//
//   - **(a)** a spawned agent calls `exit-with-code(7)` while run is parked in an unbounded `memory.atomic.wait32`:
//     Run returns 7, nil;
//   - **(b)** a spawned agent executes `unreachable` at the same point: Run returns this package's *Trap;
//   - **(c)** run's own agent exits 7 while the agent is parked: Run returns 7, nil, and its Close ends the agent.
//
// In every row Run returns within its bound and no engine goroutine survives it. A Run that does not return is a
// verdict naming where every engine goroutine is, not a hang of the test; its goroutine cannot be ended from here,
// because Run owns the instance.
//
// Read before the repair, at b71b22d: (a) and (b) did not return in 5 of 5 runs each, and (c) returned 7 in 5 of 5.
func TestComponentConfigRunEndsWhenAnAgentExitsOrTraps(t *testing.T) {
	t.Setenv("BURROUGHS_CM_SHARED_MEMORY", "1")
	for _, c := range []struct {
		fixture  string
		wantCode int
		wantTrap bool
	}{
		{"agent-ends-run-exit.wasm", 7, false},
		{"agent-ends-run-trap.wasm", 0, true},
		{"agent-ends-run-control.wasm", 7, false},
	} {
		t.Run(strings.TrimSuffix(c.fixture, ".wasm"), func(t *testing.T) {
			wasm, err := os.ReadFile("internal/component/testdata/" + c.fixture)
			if err != nil {
				t.Fatalf("the committed fixture is missing: %v", err)
			}
			const runs = 5
			hung := 0
			for i := range runs {
				baseline := len(engineGoroutineStacks())
				type out struct {
					code int
					err  error
				}
				done := make(chan out, 1)
				go func() {
					code, err := ComponentConfig{}.Run(wasm)
					done <- out{code, err}
				}()
				var o out
				select {
				case o = <-done:
				case <-time.After(5 * time.Second):
					hung++
					if hung == 1 {
						t.Errorf("run %d: Run did not return within 5s. %d goroutine(s) parked in memory.atomic.wait32 "+
							"(interp.(*memory).wait); %s", i, futexParked(), enginePlaces())
					}
					continue
				}
				if c.wantTrap {
					var trap *Trap
					// run's own routed trap (ADR 0132), not ErrInstanceLocked's refusal of a call that overlapped one
					if !errors.As(o.err, &trap) || !strings.Contains(trap.Reason, "unreachable") ||
						errors.Is(o.err, ErrInstanceLocked) {
						t.Errorf("run %d: Run = %d, %v; want the agent's unreachable as a *Trap", i, o.code, o.err)
					} else if i == 0 {
						t.Logf("Run returned %v", o.err)
					}
				} else if o.err != nil || o.code != c.wantCode {
					t.Errorf("run %d: Run = %d, %v; want %d, nil", i, o.code, o.err, c.wantCode)
				}
				deadline := time.Now().Add(5 * time.Second)
				for len(engineGoroutineStacks()) > baseline && time.Now().Before(deadline) {
					time.Sleep(10 * time.Millisecond)
				}
				if left := engineGoroutineStacks(); len(left) > baseline {
					t.Fatalf("run %d: %d engine goroutines survived Run (%d before):\n%s", i, len(left), baseline,
						strings.Join(left, "\n\n"))
				}
			}
			t.Logf("%d of %d runs returned, %d did not", runs-hung, runs, hung)
			if hung > 0 {
				t.Errorf("%d of %d runs did not return within 5s", hung, runs)
			}
		})
	}
}

// futexParked counts the goroutines parked in a select inside `interp.(*memory).wait`, whoever started them: a parked
// run is on the goroutine that called Run, which enginePlaces does not list. The frame name is bare of its module
// path, as parkedAtLeast's are.
func futexParked() int {
	n := 0
	for _, g := range strings.Split(allStacks(), "\n\n") {
		head, _, _ := strings.Cut(g, "\n")
		if strings.Contains(head, "[select") && strings.Contains(g, "interp.(*memory).wait") {
			n++
		}
	}
	return n
}
