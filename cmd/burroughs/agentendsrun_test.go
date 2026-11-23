// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestRunEndsWhenAComponentsAgentExitsOrTraps is `burroughs run`'s arm of #1357 (ADR 0132): a component whose spawned
// agent exits or traps while run is parked in an unbounded `memory.atomic.wait32` ends the process with the agent's
// exit code, or with the CLI's trap code, exactly as an exit from run's own agent does. The fixtures are
// internal/component's `agent-ends-run-*.wasm`. A run that does not end within its bound is a verdict, not a hang of
// the test.
//
// Read before the repair, at b71b22d: (a) and (b) did not end within 10s, and (c) exited 7.
func TestRunEndsWhenAComponentsAgentExitsOrTraps(t *testing.T) {
	t.Setenv("BURROUGHS_CM_SHARED_MEMORY", "1")
	for _, c := range []struct {
		fixture string
		code    int
		stderr  string // what stderr must contain; "" for a guest's own exit, which writes nothing
	}{
		{"agent-ends-run-exit.wasm", 7, ""},
		{"agent-ends-run-trap.wasm", exitTrap, "unreachable"},
		{"agent-ends-run-control.wasm", 7, ""},
	} {
		t.Run(strings.TrimSuffix(c.fixture, ".wasm"), func(t *testing.T) {
			var out, errOut bytes.Buffer
			done := make(chan int, 1)
			go func() {
				done <- dispatch(&out, &errOut, []string{"run", "../../internal/component/testdata/" + c.fixture})
			}()
			select {
			case code := <-done:
				if code != c.code {
					t.Fatalf("burroughs run %s exited %d, want %d (stderr: %q)", c.fixture, code, c.code, errOut.String())
				}
				if c.stderr == "" && errOut.Len() != 0 {
					t.Errorf("stderr = %q, want nothing: a guest's exit is its verdict, not a failure", errOut.String())
				}
				if !strings.Contains(errOut.String(), c.stderr) {
					t.Errorf("stderr = %q, want it to name %q", errOut.String(), c.stderr)
				}
			case <-time.After(10 * time.Second):
				t.Fatalf("burroughs run %s did not end within 10s: %d goroutine(s) parked in memory.atomic.wait32 "+
					"(interp.(*memory).wait)", c.fixture, futexParked())
			}
		})
	}
}

// futexParked counts the goroutines parked in a select inside `interp.(*memory).wait`. The frame name is bare of its
// module path, because go1.26.9 and go1.27.4 inline differently and a qualified name is the compiler's choice.
func futexParked() int {
	buf := make([]byte, 8<<20)
	n := 0
	for _, g := range strings.Split(string(buf[:runtime.Stack(buf, true)]), "\n\n") {
		head, _, _ := strings.Cut(g, "\n")
		if strings.Contains(head, "[select") && strings.Contains(g, "interp.(*memory).wait") {
			n++
		}
	}
	return n
}
