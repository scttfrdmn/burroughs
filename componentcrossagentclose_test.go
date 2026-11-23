// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package burroughs

import (
	"context"
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/interp"
)

// TestComponentCloseEndsAgentsBlockedInAWaitAndInAFutexWait is contract §6 R-1's shutdown clause, as amended on
// #1334, and §2 T-5.4, at the public boundary (#1338). `cross-agent-shutdown.wasm` starts two agents through the
// component spawn door: one blocks in `waitable-set.wait` on an empty set, one in `memory.atomic.wait32` that nothing
// notifies. Each is ADR 0128's wait that blocks for ever, so only shutdown can end it.
//
//   - **run has returned** (`shutdown`): no call is in flight, so `Close` returns nil, and both agents are gone.
//   - **run is still in flight** (`shutdown-inflight`), its own agent in an unbounded `memory.atomic.wait32`, as a Go M
//     idle in notesleep is: the cancellation `Close` requests first is not observed by a wait the pin does not let
//     a cancellation end, so `Close` returns ErrCloseIncomplete at its bound, as ADR 0128 measured for one agent, and
//     then tears down. The call returns T-5.4's termination (`interp.ErrTerminated` in its chain), and every agent is
//     gone.
func TestComponentCloseEndsAgentsBlockedInAWaitAndInAFutexWait(t *testing.T) {
	t.Setenv("BURROUGHS_CM_SHARED_MEMORY", "1")
	wasm, err := os.ReadFile("internal/component/testdata/cross-agent-shutdown.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	saved := componentCloseBound
	componentCloseBound = 200 * time.Millisecond
	defer func() { componentCloseBound = saved }()
	for _, c := range []struct {
		export   string
		futexes  int
		inFlight bool
	}{
		{"shutdown", 1, false},
		{"shutdown-inflight", 2, true},
	} {
		t.Run(c.export, func(t *testing.T) {
			baseline := len(engineGoroutineStacks())
			comp, err := LoadComponent(wasm)
			if err != nil {
				t.Fatalf("LoadComponent: %v", err)
			}
			type out struct {
				res []ComponentValue
				err error
			}
			done := make(chan out, 1)
			go func() {
				res, err := comp.Call(context.Background(), c.export)
				done <- out{res, err}
			}()
			if !c.inFlight {
				o := <-done
				if v, ok := firstU32(o.res); o.err != nil || !ok || v != 1 {
					_ = comp.Close()
					t.Fatalf("%s: %v, %v; want 1, nil (both agents published)", c.export, o.res, o.err)
				}
			}
			if !parkedAtLeast("component.waitableSetWait", 1) || !parkedAtLeast("interp.(*memory).wait", c.futexes) {
				_ = comp.Close()
				t.Fatalf("the agents never parked in waitable-set.wait (1) and memory.atomic.wait32 (%d)", c.futexes)
			}
			start := time.Now()
			closeErr := comp.Close()
			took := time.Since(start)
			// NEUTER, run: with waitableSetWait's context and abort arms dropped, Close takes 10s in both rows, interp's
			// closeQuiesceInterval, and fails here.
			if limit := componentCloseBound + 5*time.Second; took > limit {
				t.Errorf("Close took %v, more than %v: it outwaited a blocked agent rather than ending it", took, limit)
			}
			if c.inFlight {
				if !errors.Is(closeErr, ErrCloseIncomplete) {
					t.Errorf("Close with %s in flight returned %v, want ErrCloseIncomplete", c.export, closeErr)
				}
				select {
				case o := <-done:
					if !errors.Is(o.err, interp.ErrTerminated) {
						t.Errorf("the in-flight %s returned %v, %v after Close, want T-5.4's termination", c.export, o.res,
							o.err)
					}
					t.Logf("Close returned %v after %v; the call returned %v", closeErr, took.Round(time.Millisecond), o.err)
				case <-time.After(10 * time.Second):
					t.Fatalf("the in-flight %s did not return after Close", c.export)
				}
			} else {
				if closeErr != nil {
					t.Errorf("Close with no call in flight returned %v, want nil", closeErr)
				}
				t.Logf("Close returned nil after %v", took.Round(time.Microsecond))
			}
			deadline := time.Now().Add(5 * time.Second)
			for len(engineGoroutineStacks()) > baseline && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if left := engineGoroutineStacks(); len(left) > baseline {
				t.Fatalf("%d engine goroutines survived Close (%d before):\n%s", len(left), baseline,
					strings.Join(left, "\n\n"))
			}
		})
	}
}

func firstU32(res []ComponentValue) (uint32, bool) {
	if len(res) != 1 {
		return 0, false
	}
	return res[0].U32()
}

// allStacks is every goroutine's stack, in a buffer grown until it fits.
func allStacks() string {
	for n := 1 << 20; ; n *= 2 {
		buf := make([]byte, n)
		if k := runtime.Stack(buf, true); k < n {
			return string(buf[:k])
		}
	}
}

// parkedAtLeast waits up to 5s for n goroutines parked in a select inside frame.
func parkedAtLeast(frame string, n int) bool {
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := 0
		for _, g := range strings.Split(allStacks(), "\n\n") {
			head, _, _ := strings.Cut(g, "\n")
			if strings.Contains(head, "[select") && strings.Contains(g, frame) {
				got++
			}
		}
		if got >= n {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(time.Millisecond)
	}
}

// engineGoroutineStacks is every goroutine an engine file started: its `created by` frame is in internal/, and not
// in a test file.
func engineGoroutineStacks() []string {
	var out []string
	for _, g := range strings.Split(allStacks(), "\n\n") {
		_, created, ok := strings.Cut(g, "\ncreated by ")
		if !ok {
			continue
		}
		lines := strings.SplitN(created, "\n", 3)
		if len(lines) >= 2 && strings.Contains(lines[1], "/burroughs/internal/") && !strings.Contains(lines[1], "_test.go") {
			out = append(out, g)
		}
	}
	return out
}
