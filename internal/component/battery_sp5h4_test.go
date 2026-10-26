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
// It reads the fixture's counters only **under a stop** of the core instance the Threads run in, as
// calleegap_test.go reads its child. `interp.ReadBoundaryMemory` is a read from outside any guest call. Taken
// while a sibling Thread stores, it is a data race: the first draft read that way, and `-race` reported it on the
// runner's counter. The stop is the edge that orders the guest's stores before the read.
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

// u32 reads the word at off. The caller must hold a stop of pr.body (see `stopped`).
func (pr *parkedRunner) u32(off uint64) uint32 {
	pr.t.Helper()
	b, err := interp.ReadBoundaryMemory(&pr.mem, off, 4)
	if err != nil {
		pr.t.Fatalf("reading parked-runner's memory at %d: %v", off, err)
	}
	return binary.LittleEndian.Uint32(b)
}

// prState is one reading of the fixture's three counters, taken under a stop.
type prState struct{ parked, woke, counter uint32 }

// stopped stops pr.body, reads the counters, and resumes it. The stop's bound is generous because only the case's
// own `Stop` is the subject: a stop taken here only for a read has no deadline claim to make.
func (pr *parkedRunner) stopped() prState {
	pr.t.Helper()
	if err := pr.body.Stop(30 * time.Second); err != nil {
		pr.t.Fatalf("a stop taken to read the counters failed: %v", err)
	}
	s := prState{pr.u32(prParked), pr.u32(prWoke), pr.u32(prCounter)}
	pr.body.Resume()
	return s
}

// waitFor polls, under a stop each time, until cond holds, or fails naming what it waited for. It is a floor, not
// a timing claim: it waits for a state the guest reaches, and the bound only turns a state never reached into a
// failure.
func (pr *parkedRunner) waitFor(what string, bound time.Duration, cond func(prState) bool) prState {
	pr.t.Helper()
	deadline := time.Now().Add(bound)
	for {
		s := pr.stopped()
		if cond(s) {
			return s
		}
		if time.Now().After(deadline) {
			pr.t.Fatalf("parked-runner never reached %q within %s (parked=%d woke=%d counter=%d)", what, bound,
				s.parked, s.woke, s.counter)
		}
		time.Sleep(5 * time.Millisecond)
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

// prRunnerK is the runner's count: about 1.2s of counting in either build on darwin/arm64 (16M at 13.6M counts per
// second, or 800k at 632k under `-race`; see `raceCountScale`), and slower on janus. Each case's observations take
// a few tens of milliseconds of guest time, so the margin is wide. Every case also checks that the runner had not
// finished when it looked, so a machine fast enough to finish first fails loudly; it cannot pass.
const prRunnerK = 16_000_000 / raceCountScale

// TestSP5StopCompletesWithAgentsSuspendedInBuiltins is the litmus battery case
// `sp5-stop-completes-with-agents-suspended-in-builtins` (contract §3 SP-5).
//
// **Allowed:** `Stop(deadline)` returns within the deadline with all N = 4 agents still suspended in a
// guest-called built-in (`waitable-set.wait`), while a fifth agent, the runner, executes. **Forbidden:** missing the
// deadline, or any suspended agent being resumed for the stop to complete.
//
// **Witness:** the four parked Threads and the runner are all in one task, parked-runner's shape. The harness
// confirms all four parked and the runner counting before it calls `Stop` on the core instance their bodies run
// in. **Floor:** every run confirms all four parked first, and that the runner had not finished at the stop.
// **Arbiter:** neither, a scheduling claim.
//
// **Neuter, run:** `waitable-set.wait`'s park without its `c.Blocking` excursion (`h.suspending(…)()` called plainly
// in async_waitset.go). The case fails in 2 of 2 runs under `-race`, at the first stop taken after a Thread parks:
// *"thread 1 has 1 of 5 callers at a safepoint (1 parked, 0 blocked)"*. That is a read-stop, not the case's own
// `Stop(time.Second)`. Under this neuter every stop with an agent suspended in the wait is the forbidden outcome,
// so the first one taken is the one that fails.
func TestSP5StopCompletesWithAgentsSuspendedInBuiltins(t *testing.T) {
	const k = prRunnerK
	pr := startParkedRunner(t, 4, k)
	s := pr.waitFor("all four Threads parked", 10*time.Second, func(s prState) bool { return s.parked == 4 })
	c0 := s.counter
	pr.waitFor("the runner counting", 10*time.Second, func(s prState) bool { return s.counter > c0 })

	if err := pr.body.Stop(time.Second); err != nil {
		t.Fatalf("Stop did not complete with four agents suspended in waitable-set.wait and the runner executing: %v", err)
	}
	stopped, woke := pr.u32(prCounter), pr.u32(prWoke)
	time.Sleep(10 * time.Millisecond)
	again := pr.u32(prCounter)
	pr.body.Resume()
	if stopped >= k {
		t.Fatalf("the runner had finished (%d of %d) when Stop returned, so the stop was not taken while it executed; "+
			"raise prRunnerK", stopped, k)
	}
	if woke != 0 {
		t.Errorf("after Stop, %d parked Threads had woken: a suspended agent was resumed for the stop to complete", woke)
	}
	if again != stopped {
		t.Errorf("the runner's counter moved from %d to %d while stopped", stopped, again)
	}
	if got := pr.result(60 * time.Second); got != k {
		t.Errorf("run = %d after Stop/Resume, want %d", got, k)
	}
}

// TestH4ASuspendedAgentDoesNotStarveItsSiblings is the litmus battery case
// `h4-a-suspended-agent-does-not-starve-its-siblings` (contract §5 H-4's no-starvation half).
//
// **Allowed:** while A is suspended in `waitable-set.wait` for T = 100ms, sibling B's counter advances by any
// nonzero amount. **Forbidden:** zero advance. **Witness:** parked-runner with N = 1: A is the one parked Thread and
// B is the runner, readied only once A has parked. **Floor:** A is confirmed suspended before the window opens, and
// still suspended, with B unfinished, when it closes. **Arbiter:** neither.
//
// **Neuter, run:** the park keeps the run slot (`suspending`'s `held` forced false in runslot.go, so the wait takes
// the no-slot path). The case fails in 2 of 2 runs under `-race` on its own assertion: *"B's counter did not
// advance in 100ms while A was suspended in waitable-set.wait (0): starvation"*.
func TestH4ASuspendedAgentDoesNotStarveItsSiblings(t *testing.T) {
	const k = prRunnerK
	pr := startParkedRunner(t, 1, k)
	s0 := pr.waitFor("A parked", 10*time.Second, func(s prState) bool { return s.parked == 1 })
	time.Sleep(100 * time.Millisecond)
	s1 := pr.stopped()
	if s1.woke != 0 || s1.counter >= k {
		t.Fatalf("A woke, or B finished, during the window (woke=%d, counter %d of %d), so B's advance was not "+
			"measured while A was suspended; raise prRunnerK", s1.woke, s1.counter, k)
	}
	if s1.counter == s0.counter {
		t.Errorf("B's counter did not advance in 100ms while A was suspended in waitable-set.wait (%d): starvation",
			s0.counter)
	}
	t.Logf("B advanced %d in 100ms while A was suspended (k=%d)", s1.counter-s0.counter, k)
	if got := pr.result(60 * time.Second); got != k {
		t.Errorf("run = %d, want %d", got, k)
	}
}
