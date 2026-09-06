// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"strings"
	"testing"
	"time"
)

// This file is **[#656][656]**'s regression battery: four shapes, one sentence — *an arrival is a caller,
// and the protocol this replaces named neither the caller that must arrive nor the caller that did.*
// Decision [0074][0074] is what they hold, and each test names the release site that repairs its shape,
// because [0074][0074]'s soundness argument *is* the enumeration of those sites and a battery that shared
// one is a battery that checks one.
//
//	| shape | what goes wrong | the release site that repairs it |
//	|-------|-----------------|----------------------------------|
//	| A     | false success is impossible; a thread the round waits for **exits** and the round hangs to its deadline | `leaveCall`, then `world.retire` |
//	| B     | a caller counted at a safepoint by SP-2 **wakes**, parks, and satisfies another *thread's* caller | none — there is nothing to satisfy |
//	| C1    | the same, where the two callers are **siblings on one `thread`** | none, and this is the shape that refuted #656's own two proposals |
//	| C2    | a caller **returns** from the guest and the round hangs to its deadline | `leaveCall` |
//
// # The window is written, not raced, and that is the whole reason these are witnesses
//
// `poll` is reached from a back-edge in `jumpTo`, from `enterFrame`, and from `tailcall.go`. A guest body
// with **no branch, no call and no tail call** therefore cannot reach a safepoint at any point in its
// length, so `unpollableTail` buys an unpolled stretch whose extent is a property of the engine's dispatch
// cost rather than of the machine's load. The first draft of these tests used a short counted loop and
// passed on the broken engine — the guest had already parked by the time the host could look — which is
// [ADR 0067][0067]'s stillbirth lesson arriving a second time. *A control isn't born until it's watched
// die*, and each of these was watched to die against the arrival protocol before the repair landed; the
// measured lines are in the ADR's context table.
//
// **The two false-expiry shapes (A, C2) assert `Stop`'s verdict; the two false-success shapes (B, C1)
// cannot**, because a broken engine's `Stop` returns nil and so does a correct one. Those two read the
// guest's own last-instruction word instead: a nil returned while that word is still zero is a stopped
// world with guest code left to run in it, which is the breach stated in the terms §3 SP-1 promises.
//
// # The falsification battery, and the mutation that killed nothing
//
// Each shape was then watched to die against a mutation of the *landed* mechanism, because the arrival
// protocol these were first measured on is gone and *a re-pointed control has not been watched die*. Five
// mutations, each applied alone to `internal/interp/safepoint.go` and reverted, all four tests run under
// `-race`:
//
//	| mutation                                                            | A    | B    | C1   | C2   |
//	|---------------------------------------------------------------------|------|------|------|------|
//	| M1 `atSafepointLocked` returns on the *first* satisfied thread      | fail | FAIL | pass | pass |
//	| M2 thread-granular: `callers > 0 && parked+blocked == 0` is not one  | pass | pass | FAIL | pass |
//	| M3 `leaveCall` is not a release site                                 | pass | pass | fail | FAIL |
//	| M4 `retire` is not a release site                                    | pass | pass | pass | pass |
//	| M5 neither departure site releases                                   | FAIL | pass | —    | —    |
//
// Capitals are the shape the mutation is *for*; a lower-case fail is collateral and is reported because a
// battery that only prints its intended kills cannot show which arms overlap. Every shape has a mutation
// that kills it, and no two shapes are killed by the same set — which is what makes these four tests and
// not one test with four fixtures.
//
// **M4 killed nothing, and that is a finding rather than a gap.** `retire` is one of decision 0074's four
// release sites, and this battery cannot observe it: [ADR 0068][0068] wraps `runEntry` in
// `enterCall`/`leaveCall`, so a spawned thread's `leaveCall` **always** precedes its `retire`, and the
// predicate is already satisfied by the time the reaper runs. M3 and M5 together say the two sites cover
// shape A redundantly — either alone suffices — so `retire`'s necessity is *argued* (a thread could in
// principle leave `live` without a matching `leaveCall`, and 0074 keeps the site so the predicate does not
// depend on that never happening) and is **not measured here**. Stated so a later reader does not take
// this file's green as evidence for a site it never exercised.
//
// [656]: https://github.com/scttfrdmn/burroughs/issues/656
// [0074]: ../../docs/decisions/0074-stop-waits-on-sp-1s-own-predicate-over-the-caller-marks-because-an-arrival-is-a-caller-and-the-protocol-named-neither-end-of-it.md
// [0067]: ../../docs/decisions/0067-a-caller-count-joins-the-blocked-mark-because-sp-2s-predicate-is-about-callers-and-a-thread-is-not-one.md
// [0068]: ../../docs/decisions/0068-spawn-drops-0056s-walk-and-refuses-the-two-cases-a-per-instance-world-cannot-express-because-a-thread-belongs-to-exactly-one-stop.md
const (
	// The layout, all below the region `unpollableTail` fills so no store here is clobbered by one.
	// Naturally aligned and four bytes apart, so every access is a plain `i32` at a fixed offset.
	stopSpawnBase = 0  // the spawned entry's sentinel; its last-instruction word is at +4
	stopTailWord  = 16 // a host-side `tail` caller's sentinel; its last-instruction word is at +4
	stopWaitAddr  = 32 // the futex cell

	// The value the last instruction of a tail body stores. Distinct from 1 and from 0, so the word
	// tells "not started", "started" and "finished" apart in one read.
	stopFinished = 24301

	// Sized from the same measurement `unpollableTail` documents: enough fills that the stretch is
	// hundreds of milliseconds wide against deadlines and wait intervals measured in whole seconds.
	// Larger buys no confidence and is paid by every run of the suite.
	stopTailFills = 60
)

// unpollableTail is `stopTailFills` copies of one `memory.fill` over most of a 64-page memory: straight
// line, no branch, no call, no tail call, so `poll` is unreachable from the first byte of it to the last.
//
// **It is a written window and not a timed one, which is the property the whole file rests on.** A loop
// long enough to take the same wall-clock time would reach a back-edge every iteration, so the guest would
// park within nanoseconds of the request and every assertion below would be a race against the scheduler.
// The measured extent on the dev box is a few hundred milliseconds per witness; nothing asserts that
// figure, and nothing needs to — what is asserted is that a poll cannot happen, which is structural.
func unpollableTail() string {
	return strings.Repeat(
		"(memory.fill (i32.const 65536) (i32.const 7) (i32.const 4128768))\n", stopTailFills)
}

// stopPlainTailModule is C2's guest and needs **no gate at all**: unshared memory, no atomics, no spawn.
// That is what moves #656's blast radius past `gate:threads` — the issue's own *"it blocks the
// `gate:threads` flip"* is true and is not the whole bound — so this builder deliberately does not reach
// for `instantiateThreads1`.
func stopPlainTailModule(t *testing.T) *Instance {
	t.Helper()
	in, trap := instantiate1(t, `(module
		(memory 64 64)
		(func (export "tail") (param $sentinel i32) (param $done i32)
			(i32.store (local.get $sentinel) (i32.const 1))
			`+unpollableTail()+`
			(i32.store (local.get $done) (i32.const `+itoa(stopFinished)+`))))`)
	if trap != nil {
		t.Fatalf("instantiating the plain tail module: %v", trap)
	}
	return in
}

// stopSharedTailModule is the guest for A, B and C1. Shared memory and atomic accesses because every word
// here is written by one agent and read by another, and `-race` is an authority this package answers to —
// a plain store in the guest against a plain read from the host is a data race on guest memory whatever
// the stop protocol does. `entry` is `Spawn`-shaped (one `i32`, no results, T-1); `tail` is the same body
// for a host-side `Invoke`; `waiter` suspends; `read` is the host's window onto a word.
func stopSharedTailModule(t *testing.T, waitNanos int64) *Instance {
	t.Helper()
	return instantiateThreads1(t, `(module
		(memory 64 64 shared)
		(func (export "entry") (param $base i32)
			(i32.atomic.store (local.get $base) (i32.const 1))
			`+unpollableTail()+`
			(i32.atomic.store (i32.add (local.get $base) (i32.const 4))
				(i32.const `+itoa(stopFinished)+`)))
		(func (export "tail") (param $sentinel i32) (param $done i32)
			(i32.atomic.store (local.get $sentinel) (i32.const 1))
			`+unpollableTail()+`
			(i32.atomic.store (local.get $done) (i32.const `+itoa(stopFinished)+`)))
		(func (export "waiter") (param $addr i32) (result i32)
			(memory.atomic.wait32 (local.get $addr) (i32.const 0)
				(i64.const `+itoa(uint64(waitNanos))+`)))
		(func (export "read") (param $addr i32) (result i32)
			(i32.atomic.load (local.get $addr))))`)
}

// stopWord reads a guest word from the host side, atomically. `atomicLoadWord` rather than a plain slice
// read for the reason `stopSharedTailModule` uses atomic stores: the guest is writing these words on
// another goroutine, and the read has to be the same kind of access the write is.
//
// `!ok` is a `Fatal` and not a skip: the address is a compile-time constant and naturally aligned, so a
// decline is the accessor answering about something other than this call.
func stopWord(t *testing.T, in *Instance, addr int) int32 {
	t.Helper()
	v, ok := atomicLoadWord(in.mems[0].view()[addr : addr+4])
	if !ok {
		t.Fatalf("atomicLoadWord declined a 4-byte read at %d, which is naturally aligned and in "+
			"bounds — so this is a finding about the accessor, not about safepoints", addr)
	}
	return int32(uint32(v))
}

// awaitTailStarted waits for the store that is the *first* instruction of a tail body. It is the premise
// every witness here rests on and it is waited for rather than assumed: a `Stop` issued before the caller
// has entered the guest measures an idle world, which is a green over nothing. *A test's premise is not its
// assertion, and a premise that fails silently means nothing was measured.*
func awaitTailStarted(t *testing.T, in *Instance, addr int) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for stopWord(t, in, addr) != 1 {
		if !time.Now().Before(deadline) {
			t.Fatalf("no caller reached the sentinel store at %d within 30s, so the tail never "+
				"started and this witness has no subject", addr)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestAStopCompletesWhenTheThreadItWaitsForExits is **#656's shape A**: the round waits for a thread, and
// that thread *leaves* rather than parking.
//
// A spawned thread runs an unpollable tail, so it cannot reach a safepoint between the sentinel store and
// its return. `Stop` therefore has one caller to account for and the only transition available is a
// departure — `leaveCall` when `runEntry` returns, then `world.retire` when the goroutine ends. Under the
// arrival protocol neither of those was a message, so the round counted a sender that no longer existed
// and burned its whole deadline: measured as *"1 of 2 arrived within 2s"* about a thread that had already
// exited (#656).
//
// **The verdict is the assertion here, and it can be**, because the broken engine and the correct one
// disagree on it: A is a false *expiry*, so `err == nil` is the discriminator and no elapsed time is
// measured.
//
// `parked == 0` is asserted beside it, and it is what distinguishes the repair from a coincidence: a nil
// with a positive `parked` would mean the thread reached a safepoint after all, which would make this a
// test of the ordinary park path wearing shape A's name.
func TestAStopCompletesWhenTheThreadItWaitsForExits(t *testing.T) {
	in := stopSharedTailModule(t, int64(time.Second))
	if _, err := in.Spawn(exportedFuncIndex(t, in, "entry"), stopSpawnBase, 0); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	sp := onlySpawnedThread(t, in)
	awaitTailStarted(t, in, stopSpawnBase)

	if err := in.Stop(30 * time.Second); err != nil {
		t.Fatalf("Stop with one spawned caller inside an unpollable tail returned %v, want nil.\n"+
			"That caller cannot reach a poll, so the only transitions left to it are `leaveCall` on "+
			"return and `world.retire` on exit. An expiry here means neither is a release site for "+
			"SP-1's predicate, which is #656's shape A: a round waiting out its deadline for an "+
			"agent that has left (decision 0074)", err)
	}

	in.world.mu.Lock()
	parked := sp.parked
	in.world.mu.Unlock()
	if parked != 0 {
		t.Errorf("the spawned thread's parked is %d, want 0 — the tail has no poll site in it, so a "+
			"park means this stop was completed by the ordinary path and shape A was never "+
			"exercised", parked)
	}
	if got := stopWord(t, in, stopSpawnBase+4); got != stopFinished {
		t.Errorf("the spawned tail's last-instruction word is %d, want %d — the release under test is "+
			"a *departure*, so this witness requires that the caller ran to the end of its body",
			got, stopFinished)
	}

	in.Resume()
	select {
	case <-sp.done:
	case <-time.After(30 * time.Second):
		t.Fatal("the spawned thread did not end 30s after Resume, so it is parked rather than exited " +
			"and shape A's premise was wrong about which transition released the round")
	}
	if sp.err != nil {
		t.Errorf("the spawned thread ended with %v, want nil", sp.err)
	}
}

// TestAWakingBlockedCallerDoesNotCompleteAnotherThreadsRound is **#656's shape B**: a caller that SP-2
// counts as already at a safepoint wakes inside the round, parks — and must not thereby complete a round
// that is still waiting for a *different* thread's caller.
//
// Two threads. The host thread's only caller suspends in `memory.atomic.wait32` on a short interval, so
// SP-2 makes it at a safepoint before `Stop` runs. A spawned thread is inside an unpollable tail, so it is
// the one the round is actually waiting for. The wait then expires mid-round, its caller wakes, clears
// `blocked` and parks — and under the arrival protocol that park *sent*, filling the single slot the walk
// had opened for the spawned thread: `Stop` returned nil while guest code ran, measured at 911µs (#656).
//
// **The verdict cannot be the assertion**, because a correct `Stop` also returns nil. What discriminates is
// the spawned caller's own last-instruction word, read the moment `Stop` comes back: a stopped world in
// which that word is still zero *and* nothing parked is a world with guest code left to run in it.
//
// Under decision 0074 there is no site to repair, which is the point: `parked+blocked >= callers` is
// per-thread, so no transition on the host thread can make the spawned thread's term true. The test asserts
// a property of the predicate's shape rather than of a guard.
func TestAWakingBlockedCallerDoesNotCompleteAnotherThreadsRound(t *testing.T) {
	// Long enough that the waiter is still queued when `Stop` runs, short enough that it wakes well
	// inside the spawned tail's remaining extent. Both halves are checked: the premise below asserts
	// the first, and the tail's word asserts the second by being zero.
	const waitInterval = 200 * time.Millisecond

	in := stopSharedTailModule(t, int64(waitInterval))
	if _, err := in.Spawn(exportedFuncIndex(t, in, "entry"), stopSpawnBase, 0); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	sp := onlySpawnedThread(t, in)
	awaitTailStarted(t, in, stopSpawnBase)

	waited := make(chan outcome, 1)
	go func() { waited <- callOffGoroutine(in, "waiter", I32(stopWaitAddr)) }()
	awaitQueued(t, in, stopWaitAddr, 1)

	in.world.mu.Lock()
	hostBlocked, hostCallers := in.host.blocked, in.host.callers
	spCallers := sp.callers
	in.world.mu.Unlock()
	if hostBlocked != 1 || hostCallers != 1 || spCallers != 1 {
		t.Fatalf("premise: the host thread is blocked=%d callers=%d and the spawned thread has "+
			"callers=%d; want 1, 1 and 1. The subject is a round in which one thread is already at "+
			"a safepoint and another is not, so any other state measures something else",
			hostBlocked, hostCallers, spCallers)
	}

	err := in.Stop(30 * time.Second)

	in.world.mu.Lock()
	parked := sp.parked
	in.world.mu.Unlock()
	finished := stopWord(t, in, stopSpawnBase+4)

	if err == nil && parked == 0 && finished == 0 {
		t.Errorf("Stop reported the world stopped while the spawned caller was still inside its "+
			"unpollable tail: parked=0 and its last-instruction word is 0.\n"+
			"The only thing that changed state during the round is the host thread's waiter waking "+
			"out of its %s interval, so this is #656's shape B — a caller SP-2 had already counted "+
			"at a safepoint completing a round that was waiting for a different thread. Contract §3 "+
			"SP-1 promises *every* guest thread at a safepoint, and `parked+blocked >= callers` is "+
			"per thread precisely so that no transition on one can answer for another (decision "+
			"0074)", waitInterval)
	}
	if err != nil {
		t.Errorf("Stop: %v — both callers reach a safepoint inside 30s (the waiter by waking and "+
			"parking, the spawned one by finishing its tail and leaving), so an expiry here is a "+
			"release site missing rather than shape B", err)
	}

	in.Resume()
	select {
	case o := <-waited:
		if got := o.get(t); got != waitTimedOut {
			t.Errorf("the waiter returned %d, want %d (\"timed-out\") — its interval expired during "+
				"the stop, and SP-4 forbids the stop having woken it for any other reason",
				got, waitTimedOut)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the waiter did not return 30s after Resume, so it was parked and not released")
	}
	select {
	case <-sp.done:
	case <-time.After(30 * time.Second):
		t.Fatal("the spawned thread did not end 30s after Resume")
	}
}

// TestASiblingCallerOnOneThreadDoesNotCompleteTheRunnersRound is **#656's shape C1**, and it is the witness
// that refuted both repairs the issue itself proposed — *by identity rather than by degree*.
//
// One `thread`, two callers: `link.go` registers exactly one `thread` per instance and the public API lets
// an embedder drive N concurrent `Invoke` calls through it. Caller A suspends in `memory.atomic.wait32` on
// a short interval; caller B is inside an unpollable tail. `blocked=1 callers=2`, so the walk knows this
// thread is not at a safepoint. A's interval then expires, A wakes and parks, and under the arrival
// protocol its park filled the slot opened for **B** — its sibling, on the same row of `live`, behind the
// same `ThreadID` and the same `reported` flag.
//
// **That identity is what refutes #656's two proposals.** Waiting on the *thread identities* awaited cannot
// tell A's arrival from B's, because it is the same id; an `awaited` mark set per thread is true for both
// callers at once, so gating the send on it changes nothing. The unit that has to be accounted for is a
// **caller**, and neither a token nor a `ThreadID` names one — which is why decision 0074 replaced the
// count with a per-caller predicate rather than filtering the channel.
//
// This is the mixed state [ADR 0067][0067] was written for, one level further in: 0067 fixed *which threads
// to wait for* and left *what completes the wait* alone.
// `TestAThreadIsAtASafepointOnlyWhenEveryCallerOnItIsSuspended` holds 0067's half, with a wait long enough
// to outlive the round; this one is the same two callers with the wait expiring inside it.
//
// [0067]: ../../docs/decisions/0067-a-caller-count-joins-the-blocked-mark-because-sp-2s-predicate-is-about-callers-and-a-thread-is-not-one.md
func TestASiblingCallerOnOneThreadDoesNotCompleteTheRunnersRound(t *testing.T) {
	const waitInterval = 200 * time.Millisecond

	in := stopSharedTailModule(t, int64(waitInterval))

	// Caller B first, so its tail has its full extent left when A's interval expires.
	ran := make(chan error, 1)
	go func() {
		_, err := in.Invoke("tail", I32(stopTailWord), I32(stopTailWord+4))
		ran <- err
	}()
	awaitTailStarted(t, in, stopTailWord)

	waited := make(chan outcome, 1)
	go func() { waited <- callOffGoroutine(in, "waiter", I32(stopWaitAddr)) }()
	awaitQueued(t, in, stopWaitAddr, 1)

	in.world.mu.Lock()
	blocked, callers := in.host.blocked, in.host.callers
	in.world.mu.Unlock()
	if blocked != 1 || callers != 2 {
		t.Fatalf("premise: blocked=%d callers=%d, want 1 and 2 — two callers on one `thread`, one "+
			"suspended and one running, is the whole subject. Any other state and the sibling "+
			"identity this witness turns on does not exist", blocked, callers)
	}

	err := in.Stop(30 * time.Second)
	finished := stopWord(t, in, stopTailWord+4)

	if err == nil && finished == 0 {
		t.Errorf("Stop reported the world stopped while a sibling caller on the same `thread` was " +
			"still inside its unpollable tail: its last-instruction word is 0.\n" +
			"Both callers share one `thread`, one `ThreadID` and one row in `live`, so nothing that " +
			"names a *thread* can tell the waiter's transition from the runner's — which is why " +
			"#656's two proposed repairs (wait on the awaited ids; gate the send on a per-thread " +
			"`awaited` mark) are both refuted by this state and why decision 0074 counts callers " +
			"instead.")
	}
	if err != nil {
		t.Errorf("Stop: %v — the waiter parks when its %s interval expires and the runner leaves "+
			"when its tail ends, both well inside 30s, so an expiry here is a release site missing",
			err, waitInterval)
	}

	in.Resume()
	for range 2 {
		select {
		case rerr := <-ran:
			if rerr != nil {
				t.Errorf("the tail caller failed: %v", rerr)
			}
		case o := <-waited:
			if got := o.get(t); got != waitTimedOut {
				t.Errorf("the waiter returned %d, want %d (\"timed-out\")", got, waitTimedOut)
			}
		case <-time.After(30 * time.Second):
			t.Fatal("a caller did not return 30s after Resume, so it was parked and not released")
		}
	}
}

// TestACallerThatReturnsFromTheGuestCompletesTheRound is **#656's shape C2**, and it is the one that moves
// the issue's blast radius: *no gate at all.* One `Invoke`, one `Stop`, an unshared memory, no atomics and
// no `Spawn` — so this false expiry is reachable on the engine as it ships today.
//
// The caller runs to the end of an unpollable body, and `leaveCall` decrements `callers`. Under the arrival
// protocol that was not a message, so the round waited out its whole interval for an agent that had left:
// measured as *"0 of 1 arrived within 3s"* with the caller's last instruction **already run** — a report
// whose subject did not exist. `leaveCall` is the release site, and `Stop`'s verdict is the assertion,
// because a false expiry is a direction the two engines disagree on.
//
// The last-instruction word is asserted too, and in the *opposite* direction from B and C1: here it must be
// set, because the release under test is a departure and a zero would mean the caller was still inside the
// body when `Stop` returned — which would make a nil verdict a different bug entirely.
func TestACallerThatReturnsFromTheGuestCompletesTheRound(t *testing.T) {
	in := stopPlainTailModule(t)

	ran := make(chan error, 1)
	go func() {
		_, err := in.Invoke("tail", I32(stopTailWord), I32(stopTailWord+4))
		ran <- err
	}()
	awaitTailStarted(t, in, stopTailWord)

	if err := in.Stop(30 * time.Second); err != nil {
		t.Fatalf("Stop with one caller inside an unpollable tail returned %v, want nil.\n"+
			"That caller cannot reach a poll, so its only route to a safepoint is to finish and "+
			"leave. An expiry here means `leaveCall` is not a release site for SP-1's predicate, "+
			"which is #656's shape C2 — reachable with no gate at all: unshared memory, no atomics, "+
			"no Spawn (decision 0074)", err)
	}
	if got := stopWord(t, in, stopTailWord+4); got != stopFinished {
		t.Errorf("the tail's last-instruction word is %d, want %d — the release under test is the "+
			"caller *leaving*, so a zero means the stop completed for some other reason and this "+
			"witness measured something else", got, stopFinished)
	}

	in.Resume()
	select {
	case err := <-ran:
		if err != nil {
			t.Errorf("the tail caller failed: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the caller did not return 30s after Resume")
	}
}
