// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"errors"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// SP-1's witnesses (contract §3, ADR 0104's ruling 1). The bound registered on #515 is the longest
// straight-line stretch plus one bulk chunk plus scheduling noise, so arrival no longer scales with a bulk
// instruction's operand. Each witness's neuter must MISS its bound and FAIL, not hang: `Stop`'s own deadline
// turns a miss into `ErrStopDeadline`, and each guest's work is finite or pollable, so `Close` ends.

// TestATightLoopReachesASafepointWithinTheBound: a loop that makes no calls reaches a safepoint at a back-edge.
// The forecast is a p99 of at most 10µs (#515). The deadline here is far wider, so CI's scheduling noise cannot
// fail it, and the neuter never arrives at all, so the width does not blunt it.
//
// **Neuter, run:** removing the back-edge poll from `jumpTo`. The stop misses its deadline and the test fails.
// The loop's finite trip count, several seconds of work, lets `Close` finish afterwards.
func TestATightLoopReachesASafepointWithinTheBound(t *testing.T) {
	const trips = 100_000_000
	in := spinModule(t)
	goInvoke(t, in, "spin", I32(trips)) // and waits for it, after Close (#1193)
	// A first stop, so the guest is known to be inside its loop: a stop issued before it enters is answered
	// at its first frame entry, which is not the poll under test.
	if err := in.Stop(30 * time.Second); err != nil {
		t.Fatalf("the first Stop: %v", err)
	}
	in.Resume()

	const bound = 100 * time.Millisecond
	rng := rand.New(rand.NewPCG(1, 1))
	for i := range 20 {
		// **Let the guest run before each request.** Without this the next `Stop` found the guest still
		// parked from the last, which satisfies the predicate at once. This witness's first draft passed under
		// its own neuter for exactly that reason, in 0.00s. The host's clock, spun.
		until := time.Now().Add(time.Millisecond + time.Duration(rng.Int64N(int64(2*time.Millisecond))))
		for time.Now().Before(until) {
		}
		t0 := time.Now()
		err := in.Stop(bound)
		d := time.Since(t0)
		in.Resume()
		if err != nil {
			t.Fatalf("trial %d: Stop(%s) on a tight loop with no calls returned %v after %s; the loop's every "+
				"trip crosses a back-edge, so a miss means the back-edge poll is not on that path (SP-1, ADR 0059)",
				i, bound, err, d)
		}
	}
}

// TestAStopLandsInsideABulkFill: a `Stop` can park an agent **inside** one large `memory.fill`, which only the
// per-chunk poll can do (SP-1, ADR 0104). The baseline on #515 was the whole instruction: 17ms for 64 MiB on
// janus. The forecast, a p99 of at most 100µs, is carried by janus's reading (`BenchmarkSP1Arrival`).
//
// # Why it asserts a place and not a time
//
// This witness asserted a 10ms arrival bound until a `make ci` run under parallel load missed it on trial 0,
// with the poll in place. It is the defect #1062's adapter witness showed on CI amd64: a wall-clock bound a
// neuter can miss sits inside a loaded machine's noise. So this witness asserts a fact noise cannot produce.
//
// The guest fills 128 MiB per instruction, in a loop, alternating the fill byte (7, then 8) between passes.
// A fill runs forward, chunk by chunk. So a stop that parks the agent mid-fill leaves the region's first
// byte from this pass and its last byte from the previous one. Each `Stop` has a long deadline, so it always
// returns with the agent parked. **With the per-chunk poll, some of 40 trials park mid-fill. Without it,
// none can**, because the loop's only other safepoint is its back-edge, between two whole fills.
//
// **Neuter, run:** skipping the per-chunk poll in `execMemoryFill`, and no trial parks mid-fill. `Close` ends
// the guest at its next back-edge.
func TestAStopLandsInsideABulkFill(t *testing.T) {
	const size = 134217728
	const src = `(module
		(memory 2048 2048)
		(func (export "fills") (param i32) (result i32)
			(loop (memory.fill (i32.const 0) (i32.add (i32.const 7) (i32.and (local.get 0) (i32.const 1)))
					(i32.const 134217728))
				(local.set 0 (i32.sub (local.get 0) (i32.const 1))) (br_if 0 (local.get 0)))
			(i32.const 0)))`
	img, err := text.EncodeModule([]byte(src))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	m, err := binary.DecodeModule(img)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	in, trap := mustInst(t, m)
	if trap != nil {
		t.Fatalf("instantiate: %v", trap)
	}
	goInvoke(t, in, "fills", I32(1_000_000)) // and waits for it, after Close (#1193)
	// Two passes before any trial, so both ends of the region hold a fill byte rather than the zero memory
	// started with.
	for primed := false; !primed; {
		if err := in.Stop(30 * time.Second); err != nil {
			t.Fatalf("waiting for the first fills: %v", err)
		}
		primed = stopWord(t, in, size-4) != 0 // read while parked, as every read here is
		in.Resume()
	}

	const trials = 40
	rng := rand.New(rand.NewPCG(1, 2))
	inside := 0
	var points []uint64
	for range trials {
		// Let the guest run to an arbitrary point of a fill before the request: the host's clock, spun.
		until := time.Now().Add(time.Duration(rng.Int64N(int64(40 * time.Millisecond))))
		for time.Now().Before(until) {
		}
		if err := in.Stop(30 * time.Second); err != nil {
			t.Fatalf("Stop: %v", err)
		}
		// Read only while the agent is parked: `Stop` returning orders its writes before these reads (SP-6).
		if stopWord(t, in, 0) != stopWord(t, in, size-4) {
			inside++
			points = append(points, wordStopPoint(t, in, 0, size))
		}
		in.Resume()
	}
	t.Logf("%d of %d stops parked the agent inside a memory.fill", inside, trials)
	if inside == 0 {
		t.Fatalf("no stop of %d parked the agent inside a 128 MiB memory.fill: the region's two ends always "+
			"agreed. A bulk instruction polls between %d-byte chunks, and only that poll can park the agent "+
			"mid-fill, so the fill ran unpolled", trials, bulkChunk)
	}
	// **And where each stop landed** (the chair's ruling of 2026-10-19, item 1): on the chunk lattice, and not
	// only on a coarser one. Neuter, run: the fill's step ×64, and stops still land mid-fill, but every point is
	// a multiple of the coarser lattice, so this fails.
	checkBulkPollLattice(t, points, bulkChunk, "a memory.fill")
}

// TestAStopLandsInsideABulkCopy is `TestAStopLandsInsideABulkFill`'s twin for `memory.copy`: a stop can park
// the agent inside one 32 MiB copy, at a chunk boundary, at every chunk. The guest fills two 32 MiB sources with
// 7 and 8 once, then copies them alternately over a 32 MiB destination below both. That is a non-overlapping,
// forward copy, so a mid-copy park leaves the destination with a prefix of the new source and a suffix of the
// old.
//
// **Neuters, run:** the copy's per-chunk poll removed, and no stop lands mid-copy; the copy's step ×64, and the
// lattice check fails.
func TestAStopLandsInsideABulkCopy(t *testing.T) {
	const size = 33554432 // 32 MiB
	const src = `(module
		(memory 2048 2048)
		(func (export "copies") (param i32) (result i32)
			(memory.fill (i32.const 33554432) (i32.const 7) (i32.const 33554432))
			(memory.fill (i32.const 67108864) (i32.const 8) (i32.const 33554432))
			(loop
				(memory.copy (i32.const 0)
					(i32.add (i32.const 33554432) (i32.mul (i32.and (local.get 0) (i32.const 1)) (i32.const 33554432)))
					(i32.const 33554432))
				(local.set 0 (i32.sub (local.get 0) (i32.const 1))) (br_if 0 (local.get 0)))
			(i32.const 0)))`
	img, err := text.EncodeModule([]byte(src))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	m, err := binary.DecodeModule(img)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	in, trap := mustInst(t, m)
	if trap != nil {
		t.Fatalf("instantiate: %v", trap)
	}
	goInvoke(t, in, "copies", I32(1_000_000)) // and waits for it, after Close (#1193)
	for primed := false; !primed; {           // both ends of the destination hold a copied byte
		if err := in.Stop(30 * time.Second); err != nil {
			t.Fatalf("waiting for the first copies: %v", err)
		}
		primed = stopWord(t, in, size-4) != 0
		in.Resume()
	}

	const trials = 80
	rng := rand.New(rand.NewPCG(1, 7))
	var points []uint64
	for range trials {
		until := time.Now().Add(time.Duration(rng.Int64N(int64(20 * time.Millisecond))))
		for time.Now().Before(until) {
		}
		if err := in.Stop(30 * time.Second); err != nil {
			t.Fatalf("Stop: %v", err)
		}
		if stopWord(t, in, 0) != stopWord(t, in, size-4) {
			points = append(points, wordStopPoint(t, in, 0, size))
		}
		in.Resume()
	}
	t.Logf("%d of %d stops parked the agent inside a memory.copy", len(points), trials)
	if len(points) == 0 {
		t.Fatalf("no stop of %d parked the agent inside a 32 MiB memory.copy: the destination's two ends always "+
			"agreed, so the copy ran unpolled", trials)
	}
	checkBulkPollLattice(t, points, bulkChunk, "a memory.copy")
}

// TestSP1StopBringsEveryAgentToASafepointByTheDeadline is the litmus case
// `sp1-stop-brings-every-agent-to-a-safepoint-by-the-deadline` (`docs/litmus-battery-preregistration.md`),
// built standalone as ADR 0104's ruling 2 allows. Building it does not schedule the §4 battery, which stays
// parked under #10.
//
// **Allowed:** `Stop(deadline)` returns within the deadline, with every one of N = 8 agents running an
// unbounded loop reported at a safepoint. **Forbidden:** a return after the deadline, or any agent's progress
// counter advancing after `Stop` returned, read twice 10ms apart. **The witness:** each agent is inside its loop
// when the request is made, arranged by waiting until every counter has advanced. **The loop body is
// arithmetic only and contains no host call**, so the agents are stopped by an epoch check and not at a
// boundary. **The floor:** a run that does not observe all N counters advancing before the request is an
// instrument fault.
func TestSP1StopBringsEveryAgentToASafepointByTheDeadline(t *testing.T) {
	const n = 8
	in := instantiateThreads1(t, `(module
		(memory 1 1 shared)
		(func (export "entry") (param $i i32)
			(local $a i32)
			(local.set $a (i32.shl (local.get $i) (i32.const 2)))
			(loop
				(i32.atomic.store (local.get $a) (i32.add (i32.atomic.load (local.get $a)) (i32.const 1)))
				(br 0))))`)
	defer func() { _ = in.Close() }()
	entry := exportedFuncIndex(t, in, "entry")
	for i := range n {
		if _, err := in.Spawn(entry, int32(i), 0); err != nil {
			t.Fatalf("Spawn of agent %d: %v (a premise, not the assertion)", i, err)
		}
	}
	counters := func() [n]int32 {
		var c [n]int32
		for i := range n {
			c[i] = stopWord(t, in, 4*i)
		}
		return c
	}
	// The floor: every agent inside its loop before the request.
	waitUntil := time.Now().Add(30 * time.Second)
	for {
		c := counters()
		all := true
		for _, v := range c {
			all = all && v > 0
		}
		if all {
			break
		}
		if time.Now().After(waitUntil) {
			t.Fatalf("instrument fault: not every agent advanced its counter within 30s (%v)", c)
		}
	}

	const deadline = 2 * time.Second
	t0 := time.Now()
	err := in.Stop(deadline)
	d := time.Since(t0)
	if err != nil {
		var detail string
		if errors.Is(err, ErrStopDeadline) {
			detail = "the deadline expired: some agent in an arithmetic-only loop reached no safepoint"
		}
		t.Fatalf("Stop(%s) with %d agents in unbounded loops returned %v after %s. %s", deadline, n, err, d, detail)
	}
	if d > deadline {
		t.Fatalf("Stop returned after %s, past its deadline of %s (forbidden)", d, deadline)
	}
	first := counters()
	time.Sleep(10 * time.Millisecond) // the registered case's interval between the two reads
	second := counters()
	if first != second {
		t.Fatalf("a counter advanced after Stop returned: %v then %v, 10ms apart (forbidden). Every agent "+
			"must be at a safepoint", first, second)
	}
	in.Resume()
	t.Logf("Stop arrived in %s for %d agents", d, n)
}
