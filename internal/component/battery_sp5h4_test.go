// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"encoding/binary"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// parkedRunner is the shared harness for the litmus cases `sp5-…` and `h4-…` (#771; the plan accepted by the chair
// on 2026-10-26). It drives `cmthreads/parked-runner.wasm`:
//   - one async-callback task, `run(n, k)`;
//   - n created Threads, each parked in `waitable-set.wait` on its own future;
//   - a runner Thread, readied by the n-th parked one, that counts to k and then releases them.
//
// It reads the fixture's counters through `interp.ReadBoundaryMemory`. That read takes B-MM-1's edge and the
// growth lock, so reading while the guest runs is race-free: the guest's aligned stores are atomic (ADR 0054).
type parkedRunner struct {
	t    *testing.T
	in   *Instantiated
	body *interp.Instance // the core instance whose table holds the Thread bodies: the parked waits and the runner run here
	mem  interp.Extern
	done chan parkedRunnerOutcome
}

type parkedRunnerOutcome struct {
	res []canon.Value
	err error
}

// The fixture's memory layout (parked-runner.wat's header).
const (
	prParked  = 0
	prWoke    = 4
	prCounter = 8
)

func startParkedRunner(t *testing.T, n, k uint32) *parkedRunner {
	t.Helper()
	t.Setenv(cmThreadsGateEnv, "1")
	t.Setenv(asyncGateEnv, "1")
	b, err := os.ReadFile("testdata/cmthreads/parked-runner.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	in, err := Instantiate(b)
	if err != nil {
		t.Fatalf("Instantiate parked-runner: %v", err)
	}
	t.Cleanup(in.Close)
	body := coreInstanceWithExport(in, "t")
	memInst := coreInstanceWithExport(in, "m")
	if body == nil || memInst == nil {
		t.Fatal("parked-runner's core instances were not found by their exports (t, m)")
	}
	mem, ok := memInst.Export("m")
	if !ok {
		t.Fatal("parked-runner's memory instance exports no memory m")
	}
	pr := &parkedRunner{t: t, in: in, body: body, mem: mem, done: make(chan parkedRunnerOutcome, 1)}
	go func() {
		r, e := in.CallValues("run", canon.U32(n), canon.U32(k))
		pr.done <- parkedRunnerOutcome{r, e}
	}()
	return pr
}

func (pr *parkedRunner) u32(off uint64) uint32 {
	pr.t.Helper()
	b, err := interp.ReadBoundaryMemory(&pr.mem, off, 4)
	if err != nil {
		pr.t.Fatalf("reading parked-runner's memory at %d: %v", off, err)
	}
	return binary.LittleEndian.Uint32(b)
}

// waitFor polls until cond holds, or fails naming what it waited for. It is a floor, not a timing claim: it
// waits for a state the guest reaches, and the bound only turns a state never reached into a failure.
func (pr *parkedRunner) waitFor(what string, bound time.Duration, cond func() bool) {
	pr.t.Helper()
	deadline := time.Now().Add(bound)
	for !cond() {
		if time.Now().After(deadline) {
			pr.t.Fatalf("parked-runner never reached %q within %s (parked=%d woke=%d counter=%d)", what, bound,
				pr.u32(prParked), pr.u32(prWoke), pr.u32(prCounter))
		}
		time.Sleep(time.Millisecond)
	}
}

func (pr *parkedRunner) result(bound time.Duration) uint32 {
	pr.t.Helper()
	select {
	case o := <-pr.done:
		if o.err != nil {
			pr.t.Fatalf("run: %v", o.err)
		}
		v, _ := o.res[0].U32()
		return v
	case <-time.After(bound):
		buf := make([]byte, 1<<22)
		buf = buf[:runtime.Stack(buf, true)]
		pr.t.Logf("every goroutine at the bound (%d bytes):\n%s", len(buf), buf)
		pr.t.Fatalf("run did not return within %s", bound)
		return 0
	}
}

// sp5K and h4K are the runner's count. Each must keep the runner counting through its case's observations, and
// not much longer. Both are set from a measured runtime, recorded in the commit that lands these cases.
const (
	sp5K = 20_000_000
	h4K  = 20_000_000
)

// TestSP5StopCompletesWithAgentsSuspendedInBuiltins is the litmus battery case
// `sp5-stop-completes-with-agents-suspended-in-builtins` (contract §3 SP-5).
//
// **Allowed:** `Stop(deadline)` returns within the deadline with all N = 4 agents still suspended in a
// guest-called built-in (`waitable-set.wait`), while a fifth agent, the runner, executes. **Forbidden:** missing the
// deadline, or any suspended agent being resumed for the stop to complete.
//
// **Witness:** the four parked Threads and the runner are all in one task, parked-runner's shape. The harness
// confirms all four parked and the runner counting before it calls `Stop` on the core instance their bodies run
// in. **Floor:** every run confirms all four parked first. **Arbiter:** neither, a scheduling claim.
func TestSP5StopCompletesWithAgentsSuspendedInBuiltins(t *testing.T) {
	pr := startParkedRunner(t, 4, sp5K)
	pr.waitFor("all four Threads parked", 10*time.Second, func() bool { return pr.u32(prParked) == 4 })
	c0 := pr.u32(prCounter)
	pr.waitFor("the runner counting", 10*time.Second, func() bool { return pr.u32(prCounter) > c0 })

	if err := pr.body.Stop(time.Second); err != nil {
		t.Fatalf("Stop did not complete with four agents suspended in waitable-set.wait and the runner executing: %v", err)
	}
	stopped := pr.u32(prCounter)
	if woke := pr.u32(prWoke); woke != 0 {
		t.Errorf("after Stop, %d parked Threads had woken: a suspended agent was resumed for the stop to complete", woke)
	}
	time.Sleep(10 * time.Millisecond)
	if again := pr.u32(prCounter); again != stopped {
		t.Errorf("the runner's counter moved from %d to %d after Stop returned: it was not stopped", stopped, again)
	}
	pr.body.Resume()
	if got := pr.result(60 * time.Second); got != sp5K {
		t.Errorf("run = %d after Stop/Resume, want %d", got, sp5K)
	}
}

// TestH4ASuspendedAgentDoesNotStarveItsSiblings is the litmus battery case
// `h4-a-suspended-agent-does-not-starve-its-siblings` (contract §5 H-4's no-starvation half).
//
// **Allowed:** while A is suspended in `waitable-set.wait` for T = 100ms, sibling B's counter advances by any
// nonzero amount. **Forbidden:** zero advance. **Witness:** parked-runner with N = 1: A is the one parked Thread and
// B is the runner, readied only once A has parked. **Floor:** A is confirmed suspended before the window opens.
// **Arbiter:** neither.
func TestH4ASuspendedAgentDoesNotStarveItsSiblings(t *testing.T) {
	pr := startParkedRunner(t, 1, h4K)
	pr.waitFor("A parked", 10*time.Second, func() bool { return pr.u32(prParked) == 1 })
	c0 := pr.u32(prCounter)
	time.Sleep(100 * time.Millisecond)
	c1 := pr.u32(prCounter)
	if woke := pr.u32(prWoke); woke != 0 {
		t.Fatalf("A woke during the window, so B's advance was not measured while A was suspended; raise h4K")
	}
	if c1 == c0 {
		t.Errorf("B's counter did not advance in 100ms while A was suspended in waitable-set.wait (%d): starvation", c0)
	}
	t.Logf("B advanced %d in 100ms while A was suspended", c1-c0)
	if got := pr.result(60 * time.Second); got != h4K {
		t.Errorf("run = %d, want %d", got, h4K)
	}
}
