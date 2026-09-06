// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// The §2 litmus cases whose vehicle is `Instance.Spawn`, and that is why they are not in
// `battery_test.go`.
//
// That file's vehicle is [ADR 0062]'s two instances sharing an imported memory, and its own header
// records the prohibition: *"What this vehicle must not be used for is T-1."* Two instances driven by
// two goroutines report N agents parked for any N, because Go parks N goroutines happily — so the case
// whose forbidden set exists to exclude an M:N mapping would score a pass *on* an M:N mapping. The rows
// here therefore ride the real primitive: `Spawn`, one goroutine per agent, each `runtime.LockOSThread`ed
// and never unlocked. **The split is the vehicle**, so a future case lands in whichever file names the
// vehicle it actually used, and a reader can tell which one ran without reading the body.
//
// Both cases are registered in [the pre-registration](../../docs/litmus-battery-preregistration.md) and
// neither invents its own allowed set. What is *not* in the registration — a round count, a poll
// interval, an encoding — is the implementation's and is said to be, in the trailing paragraphs that
// document names *"What the implementation measured"*.
//
// [ADR 0062]: ../../docs/decisions/0062-the-litmus-batterys-two-agents-are-two-instances-sharing-an-imported-memory-because-a-shared-memory-spans-instances-and-spawn-does-not-gate-that.md

const (
	// litmusParkBase is where the agents' words start, and it is not 0 so that an address arriving
	// somewhere unexpected is recognisable as an address rather than as an unset field.
	litmusParkBase int32 = 64
	// litmusParkStride gives each agent a futex word at `base` and a result word at `base+4`. Both are
	// 4-aligned, which is what `memory.atomic.wait32` requires and what T-1's *"distinct naturally-aligned
	// word"* asks for. It is not a claim about cache lines: nothing here needs the words to be in
	// different ones, and 8 would not deliver that if something did.
	litmusParkStride int32 = 8
	// litmusResultBias is added to every wait result before the guest stores it, and it is the difference
	// between an assertion and an analytic zero. `waitWoken` is 0 and a fresh word holds 0, so an
	// unencoded result word would be satisfied by a word **nobody wrote** — the reading would pass on an
	// engine whose agent never ran at all. Biased, 0 means "no store", 1 woken, 2 not-equal, 3 timed out.
	litmusResultBias int32 = 1
)

// litmusParkSrc is T-1's module: park on a word with an infinite timeout, then record what the wait
// answered.
//
// The timeout is `-1` because the registration's forbidden set names *"a return of 2 (timed out) under an
// infinite timeout"*, and an operand of 5s would test a different sentence. `memory.wait` renders a
// negative timeout as a nil expiry channel, so the only way this returns 2 is an engine that clamps —
// which is the defect the row is for.
const litmusParkSrc = `(module
  (memory 1 1 shared)
  (func (export "park") (param $base i32)
    (i32.atomic.store
      (i32.add (local.get $base) (i32.const 4))
      (i32.add
        (memory.atomic.wait32 (local.get $base) (i32.const 0) (i64.const -1))
        (i32.const 1))))
  (func (export "notify") (param $addr i32) (param $count i32) (result i32)
    (memory.atomic.notify (local.get $addr) (local.get $count)))
  (func (export "load") (param $addr i32) (result i32)
    (i32.atomic.load (local.get $addr))))`

// queuedAcross reports how many waiters sit at each of `addrs`, and the total.
//
// **One `waitMu` acquisition for all N addresses, which is why this is not a loop over `queuedAt`.** T-1's
// claim is about N agents parked *at one instant*; N separate acquisitions would sum N readings taken at N
// instants, and a sum like that can reach N without N ever being simultaneous. Nothing in this case leaves
// the queue before the notify, so the two forms would agree here — but agreeing by circumstance is not the
// property the row is registered on.
//
// This is the case's **parked counter**, and the queue map is it rather than `thread.blocked` because the
// registration names where the agents must be: *"parked in `memory.atomic.wait32` on a distinct
// naturally-aligned word"*. `blocked` counts callers that are suspended for any reason — a host call is
// one (ADR 0069) — and `memory.wait` increments it *before* `enqueueIfEqual`, so it reaches N while an
// agent may still be short of the queue. The map names the instruction and the word; the mark names
// neither. `blockedCallers` is kept as a second mechanism for the same event, checked once the map has
// already answered.
//
// Read under `waitMu`, which is the lock the engine's own enqueue takes, and never nested inside `w.mu`:
// nothing in the engine holds both at once and this must not be the first thing that does.
func queuedAcross(in *Instance, addrs []uint64) (per []int, total int) {
	mem := in.mems[0]
	per = make([]int, len(addrs))
	mem.waitMu.Lock()
	defer mem.waitMu.Unlock()
	for i, ea := range addrs {
		per[i] = len(mem.waiters[ea])
		total += per[i]
	}
	return per, total
}

// blockedCallers sums the blocked-caller marks over every live thread of `in`.
//
// The instantiating thread is in `live` and is counted like any other, deliberately: a run that observes
// the agents from the test's own goroutine has nobody inside an `Invoke`, so `in.host` contributes 0 and
// a nonzero contribution from it would itself be worth seeing.
func blockedCallers(in *Instance) int {
	w := &in.world
	w.mu.Lock()
	defer w.mu.Unlock()
	total := 0
	for _, t := range w.live {
		total += t.blocked
	}
	return total
}

// closeAtEnd registers the backstop every case in this file needs: an agent left in guest code past a
// test's return is grave [#666][666] — the boundary counter is package-level ([ADR 0052]), so a leaked
// crossing lands in *another* test's delta and fails a test that did nothing wrong.
//
// `Close` is the sanctioned route rather than a hand-rolled release: it sets the terminal marks, cancels
// each thread's context so a suspended `wait` dequeues and terminates, and waits on quiescence. It is
// idempotent, so the success paths below — which release and join every agent themselves — leave it
// nothing to do and it returns nil.
//
// [ADR 0052]: ../../docs/decisions/0052-the-boundary-counter-is-package-level-because-a-per-instance-counter-cannot-see-the-crossing-that-has-no-instance.md
// [666]: https://github.com/scttfrdmn/burroughs/issues/666
func closeAtEnd(t *testing.T, in *Instance) {
	t.Helper()
	t.Cleanup(func() {
		if err := in.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
}

// TestNAgentsAreAllParkedInAWaitAtOneInstant is case `t1-n-agents-block-simultaneously`, which discharges
// contract §2 T-1 — *"a wasm thread backed 1:1 by an OS thread"*.
//
// Registered allowed set: `blocked == N` for `N = 8`. Forbidden: `blocked < N`; `blocked > N` is an
// instrument fault and is reported in that word. **The deadline expiring is the forbidden outcome, not a
// discard** — which is the one place in this battery where the premise and the assertion coincide, so the
// poll below fails as a verdict rather than as an unmet premise, unlike `awaitQueued` next door.
//
// # What the forbidden set can and cannot discriminate, measured rather than argued
//
// It catches a **bounded pool** in both its shapes and an **event loop**, which is the pool at capacity 1.
// It does *not* catch an **M:N mapping** in Go, because Go's own scheduler is M:N and parks 8 goroutines
// without complaint — 1:1-ness comes from `spawn`'s `runtime.LockOSThread` and is **structural** here, not
// witnessed. So of the registration's three readings, two are covered by this case and the third is not.
//
// It also does not catch a wait path that **serializes after the enqueue**: a package-level mutex held
// across `memory.wait`'s `select` survives this case, because the parked counter is the futex queue and an
// agent joins the queue before it parks. That injection is not a T-1 violation either — eight agents each
// holding an OS thread inside the instruction is what the clause asks for, whatever the engine serializes
// internally — which is why the survivor is recorded as a bound on the reading rather than repaired.
//
// The four injections and their results are in the pre-registration's trailing paragraph for this row.
func TestNAgentsAreAllParkedInAWaitAtOneInstant(t *testing.T) {
	const (
		n          = 8
		deadline   = 10 * time.Second
		spawnBound = 30 * time.Second
	)

	in := hostLink(t, litmusParkSrc, binary.Features{Threads: true}, hostImports(nil))
	closeAtEnd(t, in)

	entry := exportedFuncIndex(t, in, "park")
	addrs := make([]uint64, n)
	tids := make([]ThreadID, n)
	for i := range addrs {
		addrs[i] = uint64(litmusParkBase + int32(i)*litmusParkStride)
	}

	// **The spawn loop is bounded, because a pool has two shapes and only one of them counts down.** An
	// engine that queues work behind a fixed worker set lets every `Spawn` return and then parks fewer
	// than N agents, which the poll below reports as T-1's forbidden outcome. An engine that blocks its
	// *spawner* past the capacity never returns from the fifth `Spawn` — measured, at a capacity of 4: the
	// unbounded form of this loop wedged and the verdict arrived as the test binary's own 120s panic with
	// a goroutine dump. That is grave [#608][608]'s rule one clause over — *no answer is spelled FAIL* —
	// so the loop runs off a goroutine and the wedge is a message naming how many `Spawn`s returned.
	//
	// The three arms are disjoint and none of them touches `tids` unless the loop finished: a failing arm
	// leaves the spawner goroutine running, and reading a slice it may still write would make the repair
	// its own data race.
	//
	// [608]: https://github.com/scttfrdmn/burroughs/issues/608
	type spawnFailure struct {
		i   int
		err error
	}
	var returned atomic.Int32
	spawned := make(chan struct{})
	failed := make(chan spawnFailure, 1)
	go func() {
		for i := range tids {
			tid, err := in.Spawn(entry, int32(addrs[i]), 0)
			if err != nil {
				failed <- spawnFailure{i: i, err: err}
				return
			}
			tids[i] = tid
			returned.Add(1)
		}
		close(spawned)
	}()
	select {
	case <-spawned:
	case f := <-failed:
		t.Fatalf("Spawn of agent %d of %d: %v — this is the premise and not the assertion: with "+
			"fewer than %d agents started, nothing was measured", f.i+1, n, f.err, n)
	case <-time.After(spawnBound):
		t.Fatalf("only %d of %d `Spawn` calls returned within %v — T-1's forbidden outcome arriving one "+
			"step earlier than the poll below: an engine that blocks its spawner past a capacity holds "+
			"fewer than %d agents because it never started them", returned.Load(), n, spawnBound, n)
	}

	// Poll rather than sleep — *a duration is not a completion signal* — and poll the queue rather than a
	// guest-visible channel, because no guest operation reports "a waiter has reached its select": a
	// notify of count 0 returns 0 whether the queue is empty or full.
	started := time.Now()
	expiry := started.Add(deadline)
	var per []int
	total := 0
	for total < n && time.Now().Before(expiry) {
		per, total = queuedAcross(in, addrs)
		if total < n {
			time.Sleep(time.Millisecond)
		}
	}
	elapsed := time.Since(started)

	switch {
	case total > n:
		t.Fatalf("%d waiters queued across %d addresses with %d agents spawned — this is an instrument "+
			"fault and not a verdict: the queue holds a waiter this test did not create (per address: %v)",
			total, len(addrs), n, per)
	case total < n:
		// The message names the outcome and offers the readings as readings. A pool, an event loop, and
		// fewer OS threads than agents are the registration's three reasons; an engine whose wait returns
		// without parking at all reaches the same count and is none of them — measured, by the
		// negative-timeout clamp that lands here at 0 of 8.
		t.Fatalf("only %d of %d agents were parked in `memory.atomic.wait32` after %v — T-1's forbidden "+
			"outcome: fewer agents are inside the instruction than were spawned. The registration's "+
			"readings are a pool, an event loop, or fewer OS threads than agents; a wait that returns "+
			"without parking reaches this count too (per address: %v)", total, n, elapsed, per)
	}
	for i, q := range per {
		if q != 1 {
			t.Errorf("address %d holds %d waiters, want 1 — the agents are not on distinct words, so "+
				"`total == %d` was reached without %d distinct parks", addrs[i], q, n, n)
		}
	}
	// The same event counted by a second mechanism. A disagreement here is an instrument fault in one of
	// the two counters, not a T-1 verdict, and it is asserted rather than logged because the two counts
	// are derived from different fields written by different functions.
	if got := blockedCallers(in); got != n {
		t.Errorf("%d blocked callers over the live set, want %d — the queue and the marks disagree about "+
			"the same %d parks", got, n, n)
	}
	t.Logf("%d agents parked in `memory.atomic.wait32` on %d distinct words, observed %v after the last "+
		"Spawn returned", total, len(addrs), elapsed)

	// Release, join, and only then read each result word. Joining before the read is what makes the read
	// an assertion about a *finished* agent rather than a poll that could catch a store in flight.
	for i, ea := range addrs {
		if woke := call(t, in, "notify", I32(int32(ea)), I32(1)); woke != 1 {
			t.Errorf("notify at %d woke %d, want 1 — agent %d was parked one line above", ea, woke, i+1)
		}
	}
	for i, tid := range tids {
		if err := in.Join(tid); err != nil {
			t.Errorf("Join(agent %d, tid %d): %v", i+1, tid, err)
		}
	}
	for i, ea := range addrs {
		got := call(t, in, "load", I32(int32(ea)+4))
		if got != waitWoken+litmusResultBias {
			t.Errorf("agent %d recorded %d at %d, want %d (`waitWoken` biased by %d; %d means the agent "+
				"stored nothing)", i+1, got, ea+4, waitWoken+litmusResultBias, litmusResultBias, 0)
		}
	}
}

// litmusFirstWaiterSrc is T-2's module. The instantiating agent waits; a spawned child spins on `armed`,
// stores the word, and notifies.
//
// `armed` is **host** state, which the registration requires in those terms — *"that flag is host state,
// not guest memory, so setting it cannot itself supply the edge under test"*. A guest flag would make the
// child's spin a second synchronising access in the memory whose wake edge is the subject.
//
// The notify's wake count is stored biased, for `litmusResultBias`'s reason: a count of 0 is the
// interesting one, and an unbiased 0 is indistinguishable from a child that never reached the store.
const litmusFirstWaiterSrc = `(module
  (import "h" "armed" (func $armed (result i32)))
  (memory 1 1 shared)
  (func (export "await") (param $addr i32) (result i32)
    (memory.atomic.wait32 (local.get $addr) (i32.const 0) (i64.const -1)))
  (func (export "wake") (param $base i32)
    (block $ready
      (loop $spin
        (br_if $ready (call $armed))
        (br $spin)))
    (i32.atomic.store (local.get $base) (i32.const 1))
    (i32.atomic.store
      (i32.add (local.get $base) (i32.const 4))
      (i32.add (memory.atomic.notify (local.get $base) (i32.const 1)) (i32.const 1))))
  (func (export "load") (param $addr i32) (result i32)
    (i32.atomic.load (local.get $addr))))`

// TestTheInstantiatingAgentWaitsAndASpawnedChildWakesIt is case
// `t2-the-first-agent-waits-and-a-child-wakes-it`, which discharges contract §2 T-2 — **no main-thread
// special case**.
//
// Registered allowed set: the instantiating agent's `memory.atomic.wait32` returns 0 (woken) after a
// spawned child stores the expected word and notifies. Forbidden: a trap; `ErrUnsupportedOp`; a return
// of 2 under an infinite timeout; a return of 0 with no notify issued. Floor: at least 90% of rounds
// report 0, because a round whose wait returned 1 (not-equal) never tested the clause.
//
// # The waiting agent is `in.host`, and driving it from a goroutine does not change that
//
// Every `Invoke` runs on `in.host` whichever goroutine calls it, so the `go func` below changes which
// *goroutine* calls in and not which *agent* waits — which is what lets the harness bound a wait whose
// operand is an infinite timeout. Without the bound, an engine that never wakes the first agent would
// hang until the test binary's own panic, and a `t.Fatalf` naming the round is a better verdict than a
// goroutine dump. `t.Fatalf` from the spawned goroutine would be undefined, which is why the result
// travels back as an `outcome`.
//
// # The witness releases the child on the *observed* enqueue, and the registered flag is why
//
// The registration's witness has a requirement and a means: *"the first agent must enter the wait before
// the child's store"*, arranged *"by having the child spin on a host-side flag the first agent sets
// immediately before waiting"*. **The means does not deliver the requirement, and the number is in the
// pre-registration's trailing paragraph for this row**: with the flag set by a host call executed
// immediately before the wait instruction, 99–100% of rounds were woken on the default path and only
// 81–87.5% under `-race`, which breaches the registered 90% floor on the instrument CI actually runs.
// *"Immediately before"* is not a bound — between the host call's return and the enqueue there are a
// return, a dispatch, and an `enterBlocked`, and race instrumentation stretches all three while the child
// is already spinning.
//
// So the requirement is kept and the means is strengthened: `armed` answers 1 once the harness has
// **observed a waiter queued at this round's word**. That is still host state — the engine's futex queue,
// read host-side — so it still cannot supply the guest-visible edge under test, and it makes the ordering
// the registration asks for a fact rather than a hope. The consequence is recorded rather than enjoyed:
// the not-equal discard becomes structurally unreachable, so the registered floor is satisfied by
// construction and `TestAWaitWhoseCellChangedDoesNotQueue` is where that arm is covered.
//
// # The child is released unconditionally once the wait has returned, so a T-2 violation is a FAIL
//
// `armed` also answers 1 when the round is over. Without that arm, an engine *with* a main-thread special
// case — the defect this row exists for — would return from the wait without ever queuing, the child would
// spin forever on an enqueue that will not happen, and `Join` would hang: the violation would arrive as a
// wedged test rather than as a verdict. With it, the child stores and notifies into an empty queue, the
// wake count comes back 0, and the round fails naming both readings.
func TestTheInstantiatingAgentWaitsAndASpawnedChildWakesIt(t *testing.T) {
	const (
		rounds   = 200
		perRound = 10 * time.Second
		floorPct = 90
	)

	var (
		in       *Instance
		word     atomic.Uint64 // the round's futex word, read by `armed` on the child's thread
		released atomic.Bool   // the round's wait has returned; the child may finish either way
	)
	in = hostLink(t, litmusFirstWaiterSrc, binary.Features{Threads: true}, hostImports(map[string]Extern{
		"armed": HostExtern(ft(nil, []binary.ValType{binary.I32}), func(*Caller, []Value) ([]Value, error) {
			if released.Load() {
				return []Value{I32(1)}, nil
			}
			if _, queued := queuedAcross(in, []uint64{word.Load()}); queued > 0 {
				return []Value{I32(1)}, nil
			}
			// Yield rather than return straight into the guest's next spin iteration: this loop and the
			// first agent's enqueue contend for the same `waitMu`, and `sawQueued` next door yields for the
			// same reason on the same lock.
			runtime.Gosched()
			return []Value{I32(0)}, nil
		}),
	}))
	closeAtEnd(t, in)

	child := exportedFuncIndex(t, in, "wake")
	woken := 0
	for round := range rounds {
		// A fresh word per round rather than a reset one: a fresh word holds 0, which is what the wait
		// expects, so no round inherits the previous round's store. `rounds*litmusParkStride` past the base
		// stays inside the single declared page.
		base := litmusParkBase + int32(round)*litmusParkStride
		word.Store(uint64(base))
		released.Store(false)

		tid, err := in.Spawn(child, base, 0)
		if err != nil {
			t.Fatalf("round %d: Spawn of the waking child: %v — premise, not assertion", round, err)
		}

		res := make(chan outcome, 1)
		go func() { res <- callOffGoroutine(in, "await", I32(base)) }()

		var got int32
		select {
		case o := <-res:
			if o.err != nil {
				t.Fatalf("round %d: the first agent's wait answered no value: %v — a trap and "+
					"`ErrUnsupportedOp` are both in T-2's forbidden set, and a main-thread refusal would "+
					"arrive here", round, o.err)
			}
			got = o.res
		case <-time.After(perRound):
			t.Fatalf("round %d: the first agent was still inside `memory.atomic.wait32` after %v with a "+
				"child spawned to wake it — T-2's forbidden set has no hang in it because the engine has "+
				"no path to one: this is a waiter the notify did not claim", round, perRound)
		}

		// Release the child before joining it, whichever way the wait went.
		released.Store(true)
		if err := in.Join(tid); err != nil {
			t.Fatalf("round %d: Join(child %d): %v", round, tid, err)
		}
		wake := call(t, in, "load", I32(base+4)) - litmusResultBias

		switch {
		case got == waitTimedOut:
			t.Fatalf("round %d: the wait returned %d (timed out) under an infinite timeout — T-2's "+
				"forbidden outcome, and the operand was -1", round, got)
		case wake == -litmusResultBias:
			t.Fatalf("round %d: the child stored nothing at %d, so it never reached its notify — premise, "+
				"not assertion", round, base+4)
		case wake > 1:
			t.Fatalf("round %d: the notify woke %d waiters with one agent waiting — instrument fault, not "+
				"a verdict", round, wake)
		case wake == 1:
			if got != waitWoken {
				t.Errorf("round %d: the notify claimed a waiter but the first agent's wait returned %d, "+
					"want %d (woken)", round, got, waitWoken)
			}
			woken++
		default:
			// wake == 0: the notify found an empty queue, so the first agent's wait returned without ever
			// queuing. Under this witness that is not the registration's discard — the child was held until
			// the enqueue was observed — so both readings are engine defects and both are named.
			t.Errorf("round %d: the first agent's wait returned %d having queued nothing, so the notify woke "+
				"0: either the instantiating thread is refused the wait (T-2's forbidden main-thread special "+
				"case) or a wait on a word holding 0 answered not-equal", round, got)
		}
	}

	// Reported on every run, failing or not: a floor that prints only when it is breached cannot show a
	// drift toward itself.
	t.Logf("%d of %d rounds woke the instantiating agent, floor %d%%", woken, rounds, floorPct)
	// **Structurally satisfied, and kept for what would falsify that.** The child is released on the
	// observed enqueue, so every round that does not fail above is a woken round and this cannot fire —
	// which is a property of the witness above, not of the engine. It stays because it is the registered
	// floor and because weakening that gate is exactly the edit it would catch.
	if woken*100 < floorPct*rounds {
		t.Errorf("%d of %d rounds reported 0 (woken), below the registered %d%% floor — the case fails as "+
			"un-witnessed rather than as a T-2 violation: a round whose wait returned not-equal never "+
			"tested the clause", woken, rounds, floorPct)
	}
}
