// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"strings"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// The three cells this file's module uses, all whole-word aligned and in three *different* words, so
// that an atomic access to one cannot be an access to another. The futex address is 0 because
// `awaitQueued` and `notify` both name it, and a shared word would make a wake look like progress.
const (
	mixedFutexAddr = 0
	mixedGateAddr  = 8
	mixedCtrAddr   = 16
)

// mixedBodyWrites is the number of guest writes in one **unpolled straight-line stretch**, and it is
// this file's whole measuring apparatus. See `TestAThreadIsAtASafepointOnlyWhenEveryCallerOnItIsSuspended`
// for why the stretch has to be long; the number is set from the three poll sites, not from a duration:
// `poll` is called from `jumpTo` (a back-edge), from `enterFrame`, and from `tailcall.go` — so a body
// with no branch, no call and no tail call cannot reach a safepoint however long it is, and unrolling
// the writes is the only lever that widens the window without depending on what else the machine is
// doing.
const mixedBodyWrites = 40000

// mixedStopRounds is how many `Stop`/`Resume` cycles the assertion is repeated over. One round is a
// sound test of the *fixed* engine and a weak test of the broken one — see the test's own note on what
// this instrument can and cannot prove.
const mixedStopRounds = 5

// mixedSuspendAndSpinModule is `futexModule` and `gatedSpinModule` in one instance, which is the whole
// point: #592's case needs one `thread` carrying two callers in *different* states at the same moment,
// and neither existing builder can produce that alone.
//
// The loop body is `mixedBodyWrites` unrolled atomic increments of one counter cell, and that counter is
// the only thing in this file that can witness "still executing" from the host side. It has to be a
// *guest* write to *shared* memory read atomically, because the host reads it while the guest may be
// writing it: a plain load against a guest store is a data race on guest memory, and `-race` is an
// authority this package answers to.
//
// The body is generated rather than written out for the obvious reason, and the gate is kept from
// `gatedSpinModule` for its reason — it makes the loop's lifetime the test's to decide rather than a
// trip count guessed against a machine's speed, which matters more here because this test holds the
// loop running across several whole `Stop`/`Resume` cycles.
func mixedSuspendAndSpinModule(t *testing.T) *Instance {
	t.Helper()

	var body strings.Builder
	for range mixedBodyWrites {
		body.WriteString("(drop (i32.atomic.rmw.add (i32.const ")
		body.WriteString("16")
		body.WriteString(") (i32.const 1)))\n")
	}

	src := `(module
	  (memory 1 1 shared)
	  (func (export "wait32") (param $addr i32) (param $expect i32) (param $timeout i64) (result i32)
	    (memory.atomic.wait32 (local.get $addr) (local.get $expect) (local.get $timeout)))
	  (func (export "notify") (param $addr i32) (param $count i32) (result i32)
	    (memory.atomic.notify (local.get $addr) (local.get $count)))
	  (func (export "spin") (result i32) (local $n i32)
	    (loop
	      (local.set $n (i32.add (local.get $n) (i32.const 1)))
	      ` + body.String() + `
	      (br_if 0 (i32.eqz (i32.atomic.load (i32.const 8)))))
	    (local.get $n))
	  (func (export "release")
	    (i32.atomic.store (i32.const 8) (i32.const 1))))`

	img, err := text.EncodeModule([]byte(src))
	if err != nil {
		t.Fatalf("encoding the mixed suspend-and-spin module: %v", err)
	}
	m, err := (&binary.Decoder{Features: binary.Features{Threads: true}}).DecodeModule(img)
	if err != nil {
		t.Fatalf("decoding the mixed suspend-and-spin module: %v", err)
	}
	in, trap := Instantiate(m)
	if trap != nil {
		t.Fatalf("instantiating the mixed suspend-and-spin module: %v", trap)
	}
	if derr := in.Deferred(); derr != nil {
		t.Fatalf("instantiating the mixed suspend-and-spin module fell short: %v", derr)
	}
	return in
}

// mixedCounter reaches the trip counter as an `atomicCell` so the host's reads use the same word
// operation the guest's writes do.
//
// **Not through `Invoke`, and that is a constraint rather than a preference.** Reading the counter by
// calling an exported function would create a stack, poll, and — while a stop is in progress — park the
// *test's* goroutine at the safepoint. The test would then be waiting for a `Resume` it is itself
// responsible for calling. So every host-side observation in this file goes through the memory directly,
// which is `awaitQueued`'s precedent one file over.
func mixedCounter(t *testing.T, in *Instance) atomicCell {
	t.Helper()

	c, err := in.mems[0].cell(mixedCtrAddr, 0, 4)
	if err != nil {
		t.Fatalf("reaching the trip counter at %d: %v", mixedCtrAddr, err)
	}
	return c
}

// awaitSpinning blocks until the guest loop has written at least once, and reports a *premise* rather
// than a failure if it never does — `awaitQueued`'s form, for its reason: nothing has been measured yet
// at this point, so a timeout here is the fixture failing to arrive at the state the assertion is about.
func awaitSpinning(t *testing.T, ctr atomicCell) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if ctr.load() > 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("the guest loop wrote nothing within 10s — this is the test's premise and not its "+
		"assertion: the counter at %d never moved, so no caller was executing guest code and the "+
		"mixed state this test is about was never reached", mixedCtrAddr)
}

// releaseEverything unwinds every way this file's fixture can hold a goroutine, and is registered as a
// cleanup rather than written at the end of the test **because the interesting runs are the failing
// ones**. A `t.Fatalf` mid-test returns without opening the gate, which would leave a guest loop
// spinning on a core — and `go test` keeps the process alive for the rest of the package, so one failed
// assertion here would be paid for by every benchmark and timing-sensitive test scheduled after it. That
// is the *stateful instrument* shape: a test that changes the machine the later tests are measured on.
//
// It asserts nothing. A cleanup that fails is a second verdict channel competing with the assertions
// above it, and everything worth concluding has already been concluded by the time this runs. `Resume`
// is a no-op when no stop is in progress, and a notify with nothing queued wakes zero, so all three
// calls are safe on the passing path too.
func releaseEverything(in *Instance) func() {
	return func() {
		in.Resume()
		_, _ = in.Invoke("release")
		_, _ = in.Invoke("notify", I32(mixedFutexAddr), I32(1))
	}
}

// TestAThreadIsAtASafepointOnlyWhenEveryCallerOnItIsSuspended is contract §3 SP-2 read against the
// engine's own thread identity, and it is **#592**: *"a `thread` is per instance and a caller is per
// goroutine, so SP-2 can report a safepoint while guest code runs."*
//
// SP-2 makes a thread suspended in `memory.atomic.wait` count as *at a safepoint*, and requires that
// such a thread *"cannot touch guest memory until it re-enters through a boundary that observes the
// stop."* `link.go` registers exactly one `thread` per instance while the public API lets an embedder
// drive N concurrent `Invoke` calls through it, so *"this thread is blocked"* and *"no guest code is
// running on this thread"* stopped being the same proposition the moment the second caller became
// possible. The engine's own `TestAtomicRmwIsNotObservablyTornAcrossThreads` drives two.
//
// **The mixed state is the one the mark cannot represent.** Caller A suspends in a wait, so `blocked` is
// 1; caller B executes a loop on the same `thread`. `Stop`'s arrival loop asks `blocked > 0`, answers
// *"at a safepoint"*, adds nothing to `want`, and returns `nil` without waiting for anybody — while B is
// running. The host called `Stop` in order to look at guest memory, and the guest is changing it.
//
// # What this instrument can and cannot prove, and why the body is 40000 instructions long
//
// **The violation is a race window, not a steady state, and the first version of this test was
// stillborn because of it.** With a three-instruction loop body, `Stop` returns early exactly as
// described — and B then reaches its next back-edge within nanoseconds, polls, sees the request it was
// never waited for, and parks. By the time the host has returned from `Stop` and read the counter once,
// the guest has already stopped. The test passed on the broken engine, which is the *stillbirth* shape:
// a check that cannot fail, watched not failing, and mistaken for a check that holds.
//
// So the window has to be widened, and the lever is structural rather than temporal. `poll` has three
// call sites — a back-edge in `jumpTo`, `enterFrame`, and `tailcall.go` — so **a body with no branch, no
// call and no tail call cannot reach a safepoint at all**, however many instructions it contains.
// Unrolling `mixedBodyWrites` writes into one straight-line stretch therefore buys an unpolled interval
// whose length depends on the engine's own dispatch cost and not on what else the machine is doing.
//
// What that gets, stated exactly, because the two directions are not symmetric:
//
//   - **On a fixed engine the assertion is deterministic.** `Stop` waits for B's arrival, B arrives at
//     the back-edge, and the counter is quiescent from before `Stop` returns until `Resume`. No timing
//     relation is involved, so a failure here is a real regression and never a slow machine.
//   - **On a broken engine it is probabilistic**, and the probability is what the widening buys: the
//     host has to read the counter while B is somewhere inside a stretch it takes B many microseconds
//     to finish, against a read that costs nanoseconds. `mixedStopRounds` rounds compound it. The
//     residual miss — B happening to be at the very end of its stretch in every round — is a **false
//     pass**, which is the direction a widened window fails in and the reason the margin is left large
//     rather than tuned.
//
// # The five clauses
//
//  1. `Stop` returns nil. Both callers are accounted for, so arrival happened.
//  2. **The counter does not advance across the stop.** The assertion the defect fails. An advance is a
//     guest write inside a stopped world, which no schedule makes legitimate.
//  3. B has not returned. A stop that let the loop finish would satisfy clauses 1 and 2 by accident.
//  4. A is still suspended — SP-4's half, composed here rather than re-derived: this stop must not have
//     woken the waiter in order to collect its arrival.
//  5. After the last `Resume`, B runs again, the gate releases it, and it returns a positive trip count;
//     and a notify still finds A queued. A safepoint that corrupted either caller's frame, value stack
//     or `pc` would pass everything above.
//
// `release` and `notify` are called **after** the final `Resume` and not before, for `mixedCounter`'s
// reason: they are `Invoke`s, and an `Invoke` into a stopped world parks the caller — here, the test.
func TestAThreadIsAtASafepointOnlyWhenEveryCallerOnItIsSuspended(t *testing.T) {
	in := mixedSuspendAndSpinModule(t)
	t.Cleanup(releaseEverything(in))

	// Caller A: suspends in a wait, which is what makes `blocked` positive. The timeout is long
	// enough to outlive every round below, because a wait that timed out mid-test would clear the
	// mark and quietly turn this into a test of the ordinary all-callers-running case.
	waitRes := make(chan outcome, 1)
	go func() {
		waitRes <- callOffGoroutine(in, "wait32",
			I32(mixedFutexAddr), I32(0), I64(int64(120*time.Second)))
	}()
	awaitQueued(t, in, mixedFutexAddr, 1)

	// Caller B: executes guest code on the same `thread`, and keeps executing until the gate opens.
	spinDone := make(chan []Value, 1)
	spinErr := make(chan error, 1)
	go func() {
		out, err := in.Invoke("spin")
		if err != nil {
			spinErr <- err
			return
		}
		spinDone <- out
	}()
	ctr := mixedCounter(t, in)
	awaitSpinning(t, ctr)

	for round := range mixedStopRounds {
		if err := in.Stop(5 * time.Second); err != nil {
			t.Fatalf("round %d: Stop with one caller suspended in a wait and one executing a loop: "+
				"%v.\nBoth callers share one `thread`, so this stop has one arrival to collect — "+
				"from the loop, at a back-edge. A deadline expiry means the suspended caller is "+
				"being waited for as well, which SP-4 forbids (contract §3, decision 0060)",
				round, err)
		}

		// Clause 2, the one the defect fails.
		before := ctr.load()
		time.Sleep(50 * time.Millisecond)
		if after := ctr.load(); after != before {
			t.Fatalf("round %d: the guest loop advanced its counter from %d to %d *after* Stop "+
				"returned nil — contract §3 SP-2 requires that a thread reported at a safepoint "+
				"cannot touch guest memory until it re-enters through a boundary that observes the "+
				"stop, and these are guest writes inside a stopped world.\n"+
				"One `thread` carries both callers (link.go registers one per instance), so a stop "+
				"that reads only the suspended-caller count concludes the whole thread is parked on "+
				"the strength of the caller that is (#592)", round, before, after)
		}

		// Clause 3.
		select {
		case out := <-spinDone:
			t.Fatalf("round %d: the loop returned %v during the stop, want it still inside the "+
				"loop — the gate is not open yet, so a return here means the loop exited for some "+
				"reason other than the one the test controls", round, out)
		case err := <-spinErr:
			t.Fatalf("round %d: the loop failed: %v", round, err)
		default:
		}

		// Clause 4 — SP-4's half, composed.
		select {
		case r := <-waitRes:
			t.Fatalf("round %d: the suspended caller returned %d during the stop, want it still "+
				"suspended — contract §3 SP-4 requires a stop with a thread parked in a wait to "+
				"complete without waking it, and this stop woke it", round, r.res)
		default:
		}

		in.Resume()
	}

	// Clause 5. The gate opens only now: `release` is an `Invoke`, and one issued before the final
	// `Resume` would park this goroutine at the safepoint it is responsible for lifting.
	if _, err := in.Invoke("release"); err != nil {
		t.Fatalf("release: %v", err)
	}
	select {
	case out := <-spinDone:
		if len(out) != 1 || out[0].Bits == 0 {
			t.Errorf("after the last Resume the loop returned %v, want one positive trip count — "+
				"the loop counts its own trips in a local, so a zero means parking and releasing "+
				"at a back-edge perturbed `pc`, the value stack or the frame", out)
		}
	case err := <-spinErr:
		t.Fatalf("after the last Resume the loop failed: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("the loop did not return after Resume and the gate opened — it is parked at a " +
			"safepoint the Resume should have released")
	}
	if woke := call(t, in, "notify", I32(mixedFutexAddr), I32(1)); woke != 1 {
		t.Fatalf("after the stops and Resumes, a notify woke %d of the one waiter that was queued "+
			"before them, want 1 — a stop disturbed the queue", woke)
	}
	select {
	case r := <-waitRes:
		if got := r.get(t); got != waitWoken {
			t.Errorf("the suspended caller returned %d, want %d (\"ok\")", got, waitWoken)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the suspended caller did not return after the notify claimed it")
	}
}
