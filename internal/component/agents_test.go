// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// E2's witnesses (#1336, ADR 0130): several agents of one instance whose canonical memory is shared, executing
// guest code at once as threads of the run task. No guest can start an agent yet (the door is E3's, #1337), so
// each fixture's exports call `test:e2/ops.spawn`, which this file serves with `asyncHandles.spawnAgent`.
//
// The fixtures are `testdata/agents-shared.wat`, a free instance, and `testdata/agents-unshared-canon.wat`, its
// peak half in an instance that keeps ADR 0101. Their memories declare max = min, so they are Go-heap backed and
// `-race` sees guest memory (ADR 0101's follow-up 3, #979).

// agentFixture is one instance of an agents fixture and the test host that serves its imports.
type agentFixture struct {
	in   *Instantiated
	core *interp.Instance

	peak   int    // the most executing agents `probe` read; written on run's goroutine, read after the call
	stacks string // every goroutine's stack, taken by `probe` when it did not see the agents at once
	report []byte // memory[1024..4096), copied by `report` once run has joined its agents

	holding chan struct{}          // `hold`'s impl has entered (agent 4 is in host code)
	release chan struct{}          // closed to let `hold`'s impl return
	woke    chan struct{}          // `woke` was called
	late    chan func(canon.Value) // `late`'s resolver, handed out by its impl
}

// agentsProbeBound is how long `probe` waits to see run's agent and both spawned agents executing at once. It is a
// bound on a true negative: an agent parked at the run slot cannot arrive, so only the failing arm waits it out.
const agentsProbeBound = 3 * time.Second

func newAgentFixture(t *testing.T, name string) *agentFixture {
	t.Helper()
	f := &agentFixture{
		holding: make(chan struct{}, 1),
		release: make(chan struct{}),
		woke:    make(chan struct{}, 1),
		late:    make(chan func(canon.Value), 1),
	}
	h := NewHost(io.Discard, io.Discard, nil)
	h.syncImpls = map[string]interp.CanonFunc{
		"test:e2/ops::spawn": func(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
			_, err := f.in.w.async.spawnAgent(c, f.core, "mstart", int32(uint32(args[0].Bits)))
			return nil, err
		},
		"test:e2/ops::probe": func(*interp.CanonCaller, []interp.Value) ([]interp.Value, error) {
			deadline := time.Now().Add(agentsProbeBound)
			for f.peak < 3 && time.Now().Before(deadline) {
				f.peak = max(f.peak, f.core.ExecutingAgents())
				time.Sleep(time.Millisecond)
			}
			if f.peak < 3 {
				f.stacks = allGoroutineStacks()
				return []interp.Value{interp.I32(0)}, nil
			}
			return []interp.Value{interp.I32(1)}, nil
		},
		"test:e2/ops::report": func(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
			b, err := c.Read(uint64(uint32(args[0].Bits)), uint64(uint32(args[1].Bits)))
			f.report = b
			return nil, err
		},
		"test:e2/ops::await-hold": func(*interp.CanonCaller, []interp.Value) ([]interp.Value, error) {
			select {
			case <-f.holding:
				return nil, nil
			case <-time.After(agentsProbeBound):
				return nil, errors.New("agent 4 never entered hold's impl")
			}
		},
		"test:e2/ops::woke": func(*interp.CanonCaller, []interp.Value) ([]interp.Value, error) {
			f.woke <- struct{}{}
			return nil, nil
		},
	}
	h.asyncImpls = map[string]asyncLowerImpl{
		"test:e2/ops::hold": func(_ *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
			onStart()
			f.holding <- struct{}{}
			<-f.release
			onResolve(canon.Value{})
			return func() {}, nil
		},
		"test:e2/ops::late": func(_ *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
			onStart()
			f.late <- onResolve
			return func() {}, nil
		},
	}
	f.in = instantiateFixtureWithHost(t, name, h)
	f.core = coreInstanceWithExport(f.in, "mstart")
	if f.core == nil {
		f.in.Close()
		t.Fatal("no core instance exports mstart")
	}
	return f
}

// call runs one export on its own goroutine and returns its result, or fails the test naming where the engine's
// goroutines are parked if it has not returned within bound. The deferred Close ends what is left.
func (f *agentFixture) call(t *testing.T, name string, bound time.Duration) (uint32, error) {
	t.Helper()
	type out struct {
		res []canon.Value
		err error
	}
	ch := make(chan out, 1)
	go func() {
		res, err := f.in.CallValues(name)
		ch <- out{res, err}
	}()
	select {
	case o := <-ch:
		if o.err != nil {
			return 0, o.err
		}
		if len(o.res) != 1 {
			t.Fatalf("%s returned %d values, want 1", name, len(o.res))
		}
		v, ok := o.res[0].U32()
		if !ok {
			t.Fatalf("%s returned %v, want a u32", name, o.res[0])
		}
		return v, nil
	case <-time.After(bound):
		st := allGoroutineStacks()
		if p := os.Getenv("E2_STACKS"); p != "" {
			_ = os.WriteFile(p, []byte(st), 0o644)
		}
		// An agent that trapped never reaches run's join, so its trap (T-5.3's retained fault) is the place.
		t.Fatalf("%s did not return within %v. Parked at the run slot: %s. A spawned agent's trap: %v", name, bound,
			strings.Join(slotWaiters(st), "; "), f.core.Fault())
		return 0, nil
	}
}

// allGoroutineStacks is every goroutine's stack, as `runtime.Stack` writes it.
func allGoroutineStacks() string {
	buf := make([]byte, 8<<20)
	return string(buf[:runtime.Stack(buf, true)])
}

// slotWaiters names each goroutine parked waiting for a component instance's run slot, by what it is: a spawned
// agent at its entry, or a result store. It is how a failure names its place rather than a bare timeout.
func slotWaiters(stacks string) []string {
	var out []string
	for _, g := range strings.Split(stacks, "\n\n") {
		if !strings.Contains(g, "(*asyncHandles).acquireRun") {
			continue
		}
		switch {
		case strings.Contains(g, "spawnAgent.func"):
			out = append(out, "a spawned agent at its entry (spawnAgent's Enter → acquireRun)")
		case strings.Contains(g, "asyncLowerFuncWith.func"):
			out = append(out, "an async lower's result store (onResolve → acquireRun)")
		default:
			out = append(out, "another acquireRun")
		}
	}
	if len(out) == 0 {
		out = append(out, "nothing (no goroutine is in acquireRun)")
	}
	return out
}

// TestAgentsOfASharedMemoryInstanceExecuteAtOnce is E2's main witness (#1336, ADR 0130). In a free instance, run
// spawns two agents, and `probe` reads the engine's executing count (`interp.Instance.ExecutingAgents`) while both
// spin: run's agent, inside probe, and both spawned agents, so 3. Then both agents call the async ABI at once, and
// run joins them through memory.atomic.wait32, holding the run slot all the while, which is the shape a Go M
// parked in `notesleep` has.
//
// **What it asserts, from the memory run reports:**
//   - every agent's guest checks passed (its fail bits are zero): an empty set polls NONE, wait-for's subtask
//     event is (SUBTASK, RETURNED) after at least its 1ms, a stream<u8> written by agent 1 is read byte for byte
//     by agent 2, and a future<u32> written by agent 2 is read by agent 1;
//   - **T-6:** each agent read back its own global (1 and 2) after both had set it, and run read 0x77;
//   - **context:** each agent read back its own slot (107 and 207), and run read 0x55;
//   - **its own Thread:** the three thread.index values are distinct and nonzero;
//   - **linearizable handles:** the 64 waitable sets the two agents churned concurrently are 64 distinct indices.
//
// **Run under -race**, over a Go-heap memory, it is also the witness that no engine path races.
//
// **NEUTER, run (ADR 0130's rollback):** with spawnAgent's Enter set for a free instance too, the probe reads 1
// and names both agents parked at the slot (`spawnAgent's Enter → acquireRun`). **NEUTER, run:** with
// `onResolve`'s free arm dropped, so a store takes the slot, the peak is 3 and run then never returns: its
// wait-for stores are parked in acquireRun behind run's agent, which waits in memory.atomic.wait32 for agents
// that wait for those stores, and the failure names them. **NEUTER, run:** with interp's spawn leaving a spawned
// thread's `globalsOf` unset, so agents use the instance's globals, agent 2 reads 1 and run reads 1 (T-6).
// **NEUTER, run:** with spawnAgent carrying index 0, the agents trap at thread.index, and the failure names that
// trap (T-5.3's retained fault).
func TestAgentsOfASharedMemoryInstanceExecuteAtOnce(t *testing.T) {
	t.Setenv(cmSharedMemoryGateEnv, "1")
	f := newAgentFixture(t, "agents-shared.wasm")
	defer f.in.Close()
	if !f.in.w.async.agentsRunFree {
		t.Fatal("agents-shared's canonical memory is shared and the gate is on, but the instance is not free")
	}
	got, err := f.call(t, "run", 60*time.Second)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	t.Logf("peak executing agents: %d", f.peak)
	if f.peak < 3 {
		t.Fatalf("probe read at most %d executing agents, want 3 (run's and both spawned agents'). Parked at the "+
			"run slot: %s", f.peak, strings.Join(slotWaiters(f.stacks), "; "))
	}
	if got != 0 {
		t.Fatalf("run returned %#x, want 0", got)
	}
	if len(f.report) != 3072 {
		t.Fatalf("run reported %d bytes, want 3072", len(f.report))
	}
	word := func(k, off int) uint32 { return binary.LittleEndian.Uint32(f.report[k*1024+off:]) }
	if g, c := word(0, 12), word(0, 8); g != 0x77 || c != 0x55 {
		t.Errorf("run read its global %#x and its context %#x, want 0x77 and 0x55", g, c)
	}
	indices := map[uint32]int{word(0, 4): 0}
	seen := map[uint32]string{}
	for k := 1; k <= 2; k++ {
		if bits := word(k, 0); bits != 0 {
			t.Errorf("agent %d's guest checks failed: bits %#x (1 poll, 2 wait-for's event, 4 the clock, 8 the "+
				"stream copy's result, 16 the future copy's, 32 the future's value, 64 an event's code, 128 the "+
				"stream's bytes)", k, bits)
		}
		if g := word(k, 12); g != uint32(k) {
			t.Errorf("agent %d read its global as %d, want %d: the agents' globals are not their own (T-6)", k, g, k)
		}
		if c := word(k, 8); c != uint32(k*100+7) {
			t.Errorf("agent %d read its context slot as %d, want %d", k, c, k*100+7)
		}
		idx := word(k, 4)
		if prev, dup := indices[idx]; dup || idx == 0 {
			t.Errorf("agent %d's thread.index is %d, which is zero or agent %d's too", k, idx, prev)
		}
		indices[idx] = k
		for i := range 32 {
			hd := word(k, 256+4*i)
			if who, dup := seen[hd]; dup || hd == 0 {
				t.Errorf("waitable set handle %d, agent %d's #%d, is zero or was also %s's: the handle table's "+
					"operations are not linearizable", hd, k, i, who)
			}
			seen[hd] = fmt.Sprintf("agent %d #%d", k, i)
		}
	}
}

// TestAgentsOfAnUnsharedCanonMemoryInstanceKeepTheRunSlot is ADR 0130's "every other instance keeps ADR 0101
// exactly", and the free witness's twin. agents-unshared-canon's canonical memory is unshared, so its instance is
// not free, and its spawned agents take the run slot at entry: run holds it inside `probe`, so probe reads one
// executing agent and finds both spawned agents parked at the slot. That is `TestTwoCallsIntoOneInstanceNeverExecuteAtOnce`'s
// property for an agent rather than a second call, and the free witness shows the opposite.
func TestAgentsOfAnUnsharedCanonMemoryInstanceKeepTheRunSlot(t *testing.T) {
	t.Setenv(cmSharedMemoryGateEnv, "1") // the gate is on, and the instance is still not free
	f := newAgentFixture(t, "agents-unshared-canon.wasm")
	defer f.in.Close()
	if f.in.w.async.agentsRunFree {
		t.Fatal("an instance whose canonical memory is unshared is free")
	}
	got, err := f.call(t, "run", 30*time.Second)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got != 0xdead || f.peak != 1 {
		t.Fatalf("run returned %#x with a peak of %d executing agents, want 0xdead and 1: two agents of an ADR 0101 "+
			"instance executed at once", got, f.peak)
	}
	parked := 0
	for _, w := range slotWaiters(f.stacks) {
		if strings.HasPrefix(w, "a spawned agent at its entry") {
			parked++
		}
	}
	if parked != 2 {
		t.Errorf("%d spawned agents were parked at the run slot, want 2: %s", parked,
			strings.Join(slotWaiters(f.stacks), "; "))
	}
}

// TestASyncWaitOnAnExecutingAgentDoesNotTrap is ADR 0130's §5, row 5 (#1336): the sync tail's candidates include an
// agent that is executing. sync-tail is SYNC-typed, so the tail judges its wait (grave #1267). It reads a future
// that agent 3 writes, and waits; agent 3 is executing a loop before its write when the wait is judged, so the
// reference's one-runner test would find no candidate and trap. Here it waits, and reads 0x7a11.
//
// **NEUTER, run:** with the `agentRunning` term dropped from `syncTail`, the call traps "cannot block a
// synchronous task before returning".
func TestASyncWaitOnAnExecutingAgentDoesNotTrap(t *testing.T) {
	t.Setenv(cmSharedMemoryGateEnv, "1")
	f := newAgentFixture(t, "agents-shared.wasm")
	defer f.in.Close()
	got, err := f.call(t, "sync-tail", 60*time.Second)
	if err != nil {
		t.Fatalf("sync-tail: %v", err)
	}
	if got != 0x7a11 {
		t.Fatalf("sync-tail returned %#x, want 0x7a11, the value agent 3 wrote", got)
	}
}

// TestAStoreForOneAgentIsNotHeldBehindAnothersHostCode is ADR 0130's §4 (#1336): the in-host window is per agent.
// Agent 4 calls `hold`, whose impl this test keeps open, so agent 4 is in host code. Meanwhile window's own agent
// starts a wait-for(1ms) and waits for it. Its store is addressed to run's agent, whose window is closed, so it is
// applied at once and window calls `woke` while agent 4 is still held.
//
// **NEUTER, run:** with `asyncLowerFuncWith` opening the instance's window (`enterHost(nil)`) from a free agent
// too, the store is queued behind agent 4's host code, and `woke` does not arrive while agent 4 is held.
func TestAStoreForOneAgentIsNotHeldBehindAnothersHostCode(t *testing.T) {
	t.Setenv(cmSharedMemoryGateEnv, "1")
	f := newAgentFixture(t, "agents-shared.wasm")
	defer f.in.Close()
	type out struct {
		v   uint32
		err error
	}
	done := make(chan out, 1)
	go func() {
		res, err := f.in.CallValues("window")
		var v uint32
		if err == nil && len(res) == 1 {
			v, _ = res[0].U32()
		}
		done <- out{v, err}
	}()
	select {
	case <-f.woke:
	case <-time.After(10 * time.Second):
		close(f.release)
		t.Fatal("window's wait-for store did not reach it while agent 4 was in hold's impl: it is held behind " +
			"another agent's in-host window")
	}
	close(f.release)
	select {
	case o := <-done:
		if o.err != nil || o.v != 0 {
			t.Fatalf("window returned %#x, %v; want 0, nil", o.v, o.err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("window did not return after hold was released")
	}
}

// TestALateReallocStoreInAFreeInstanceIsRefusedByName is ADR 0130's §3 limit (#1336): a store applied off its
// agent's goroutine cannot call cabi_realloc, because in a free instance that agent's stack may be executing. The
// test resolves `late`'s string after the lower has returned (late-string calls `woke` first), from the test's own
// goroutine, so the store is off-agent. It is refused by name, as the owner's trap.
//
// **NEUTER, run:** with `offAgentHeap.Realloc` returning a pointer (0) instead of the refusal, the string is
// stored and the call returns its length, 4.
func TestALateReallocStoreInAFreeInstanceIsRefusedByName(t *testing.T) {
	t.Setenv(cmSharedMemoryGateEnv, "1")
	f := newAgentFixture(t, "agents-shared.wasm")
	defer f.in.Close()
	type out struct {
		res []canon.Value
		err error
	}
	done := make(chan out, 1)
	go func() {
		res, err := f.in.CallValues("late-string")
		done <- out{res, err}
	}()
	var resolve func(canon.Value)
	select {
	case resolve = <-f.late:
	case <-time.After(10 * time.Second):
		t.Fatal("late's impl was never called")
	}
	select {
	case <-f.woke:
	case <-time.After(10 * time.Second):
		t.Fatal("late-string did not return from its lower")
	}
	resolve(canon.Str("late"))
	select {
	case o := <-done:
		if o.err == nil {
			t.Fatalf("late-string returned %v: a late store that needs cabi_realloc was applied off its agent", o.res)
		}
		if !strings.Contains(o.err.Error(), "cabi_realloc") || !strings.Contains(o.err.Error(), "ADR 0130") {
			t.Fatalf("late-string failed, but not by the refusal's name: %v", o.err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("late-string did not return after its store")
	}
}
