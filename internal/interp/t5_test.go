// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// This file is contract §2 T-5's own clauses, run: [ADR 0071]'s five rows and the quiescence wait, each
// asserted through the exported surface where one exists and through the package where the question is
// about a safepoint mark.
//
// **What each test is a witness *for* is stated at it, because three of these arms would pass on the code
// that was here before.** A `Join` that answered from `thread.err` rather than from the record would pass
// every join arm and leak the record; a `Close` that returned before its threads unwound passed for the
// whole of #602's life (fact 4 of 0071's table — **`Close` returned in 39µs while a spawned counter ran on
// to 23008**); live-only membership is invisible to any test that does not count. So the counting arms
// pin numbers and the shutdown arms read what the *thread* recorded, never what the caller was told.
//
// [ADR 0071]: ../../docs/decisions/0071-t-5-is-live-only-membership-a-bounded-status-record-a-fault-in-two-channels-and-a-sentinel-panic-for-the-terminal-unwind.md

// t5OpenGateModule is `spawnModule` with its gate already open, so a spawned entry runs to completion
// without the host sequencing it.
//
// Its own helper rather than a flag on `spawnModule`, because the two are used for opposite purposes:
// every arm there needs the thread *held* inside guest code, and every arm here needs it **finished**.
// The gate is opened before any spawn, so a thread that reaches the loop leaves it on its first test.
func t5OpenGateModule(t *testing.T) *Instance {
	t.Helper()

	in := spawnModule(t)
	callVoid(t, in, "store", I32(spawnGate), I32(1))
	return in
}

// t5TrapModule's entry runs T-1's shape and then traps. The trap is `unreachable` rather than an
// out-of-bounds access, so the error a test reads names the instruction and not an address that would
// have to agree with a layout constant.
func t5TrapModule(t *testing.T) *Instance {
	t.Helper()

	return instantiateThreads1(t, `(module
		(memory 1 1 shared)
		(func (export "entry") (param $base i32)
			(drop (i32.atomic.rmw.add (local.get $base) (i32.const 1)))
			(unreachable))
		(func (export "read") (param $addr i32) (result i32)
			(i32.atomic.load (local.get $addr)))
		(func (export "store") (param $addr i32) (param $v i32)
			(i32.atomic.store (local.get $addr) (local.get $v))))`)
}

// withCloseInterval shortens `closeQuiesceInterval` for one test and restores it.
//
// The restore is a `t.Cleanup` rather than a `defer` in the caller so that a `t.Fatalf` between the two
// cannot leave the whole rest of the package running against a 50ms shutdown bound — which would not fail
// anything, it would make every later `Close` on a busy machine an `ErrCloseDeadline` and the cause would
// be three tests away.
func withCloseInterval(t *testing.T, d time.Duration) {
	t.Helper()

	was := closeQuiesceInterval
	closeQuiesceInterval = d
	t.Cleanup(func() { closeQuiesceInterval = was })
}

// liveAndExited reads the two sizes T-5's boundedness is about, under the mutex that owns them.
func liveAndExited(in *Instance) (live, exited int) {
	w := &in.world
	w.mu.Lock()
	defer w.mu.Unlock()

	return len(w.live), len(w.exited)
}

// awaitRetired blocks until `world.retire` has recorded every one of these threads — the state a
// *join-after-exit* needs, established rather than assumed.
//
// **It exists because `Join` has two paths and the arrival counter cannot say which one a test takes.** A
// join reaching a thread that is still in `live` waits on `done` and consumes the record on the far side;
// a join reaching one already retired answers straight from the record. Both are correct, and T-5.2's
// required case is the *second*, so a test that only waits for the guest's arrival word is asserting the
// clause on whichever path the scheduler happened to give it. Measured: with the fast path's `delete`
// removed, `TestJoinAnswersAfterExitAndConsumesTheRecord` still passed — it had been taking the live path.
//
// A bounded state poll, which is `awaitGuestWord`'s idiom and not a `sleep` standing in for a signal: the
// state is `world.exited`'s contents and there is no channel an embedder can wait on for it, by design —
// T-5.2's surface is `Join`, and `Join` is the thing under test.
func awaitRetired(t *testing.T, in *Instance, tids []ThreadID) {
	t.Helper()

	w := &in.world
	deadline := time.Now().Add(30 * time.Second)
	for {
		w.mu.Lock()
		missing := 0
		for _, tid := range tids {
			if _, ok := w.exited[tid]; !ok {
				missing++
			}
		}
		live := len(w.live)
		w.mu.Unlock()

		if missing == 0 {
			return
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("%d of %d spawned threads never reached `world.retire` (%d still live).\n"+
				"A premise and not an assertion: these threads run through an open gate, so a timeout "+
				"here says they did not finish rather than that the record is wrong.",
				missing, len(tids), live)
		}
		time.Sleep(time.Millisecond)
	}
}

// quiescent asks the engine its own T-5.4 question — `world.quiescentLocked`, under the mutex that owns it
// — and returns the detail a failure needs beside the answer.
//
// **Derived rather than restated.** A test that spelled the three conditions out again would agree with a
// `quiescentLocked` that had dropped one of them: *a literal duplicating a type's property is correct once*.
// The detail is built here rather than taken from `Instance.unquiesced`, because that report names every
// live thread — including the instantiation thread, which is live by construction — and a failure needs to
// know which of them was still executing.
func quiescent(in *Instance) (ok bool, detail string) {
	w := &in.world

	w.mu.Lock()
	defer w.mu.Unlock()

	parts := make([]string, 0, len(w.live))
	for _, t := range w.live {
		parts = append(parts, fmt.Sprintf("%s(callers=%d unretired=%t)", t, t.callers, t.done != nil))
	}
	return w.quiescentLocked(), fmt.Sprintf("hostCalls=%d, live: %s", w.hostCalls, strings.Join(parts, " "))
}

// TestMembershipAndStatusRecordsAreBothBounded is [ADR 0071]'s rows 1 and 2 against the measurement that
// motivated them: **51 members after 50 completed spawns**, fact 3 of that ADR's table.
//
// **Two records with two different bounds, and the test asserts both because they are bounded by different
// things.** Membership is bounded by *liveness* — `world.retire` splices a finished thread out, so the set
// returns to the one thread the instance was built on with nothing asked of the embedder. The status record
// is bounded by *consumption* — it survives on purpose, because T-5.2 requires join-after-exit to be
// answerable, and the embedder's `Join` is what takes it out. So a passing engine shows `live` falling back
// to 1 by itself and `exited` falling to 0 only as the joins happen.
//
// **The numbers are pinned exactly rather than floored**, because a floor is what the old code satisfied: 51
// members is `>= 1`, and every arm a floor could state was true of the leak. `spawnCount` is 50 to be the
// same population the measurement was taken over.
//
// The intermediate reading — `exited` at 50 with `live` back at 1 — is the one that distinguishes retirement
// from consumption. Taken before any join, so a `retire` that deleted the record instead of writing it (or a
// `Join` that read `thread.err` and left the map alone) fails here rather than at the end.
func TestMembershipAndStatusRecordsAreBothBounded(t *testing.T) {
	const spawnCount = 50

	in := t5OpenGateModule(t)
	entry := exportedFuncIndex(t, in, "entry")

	tids := make([]ThreadID, 0, spawnCount)
	for i := 0; i < spawnCount; i++ {
		tid, err := in.Spawn(entry, spawnBase, 0)
		if err != nil {
			t.Fatalf("Spawn %d: %v", i, err)
		}
		tids = append(tids, tid)
	}

	// Every thread has ended once it has bumped the arrival counter and left the open gate: the store
	// after it is the last instruction of the body. Sequencing, not an assertion — the counter says the
	// bodies ran, and `awaitRetired` says the engine has finished with them, which is what makes every
	// join below the join-after-exit path rather than whichever path the scheduler supplied.
	awaitGuestWord(t, in, spawnArrived, spawnCount)
	awaitRetired(t, in, tids)

	// Before any join: membership has already returned to the host alone and every record is waiting to be
	// asked for. This is the reading that separates the two bounds, and it is `retire`'s half of it.
	if live, exited := liveAndExited(in); live != 1 || exited != spawnCount {
		t.Fatalf("after %d completed spawns and no join, live=%d exited=%d, want 1 and %d.\n"+
			"`live` is bounded by `world.retire` and needs nothing from the embedder. A live count of "+
			"%d is fact 3 of ADR 0071 — the set that only grew.",
			spawnCount, live, exited, spawnCount, spawnCount+1)
	}

	for i, tid := range tids {
		if err := in.Join(tid); err != nil {
			t.Fatalf("Join(%d) (spawn %d) returned %v, want nil — the entry runs to completion "+
				"through an open gate, so a non-nil status here is a finding about `runEntry` "+
				"rather than about the record", tid, i, err)
		}
		if i == 0 {
			// And after exactly one consumption: one record fewer, nothing else moved. The pair of
			// readings is what says `exited` is bounded by the embedder's joins and by nothing else.
			if live, exited := liveAndExited(in); live != 1 || exited != spawnCount-1 {
				t.Errorf("after %d completed spawns and one join, live=%d exited=%d, want 1 and %d.\n"+
					"`exited` is bounded by consumption and needs a `Join` each, so a count that "+
					"did not move is a `Join` answering from `thread.err` and leaving the map "+
					"alone — the leak one field over (contract §2 T-5.2).",
					spawnCount, live, exited, spawnCount-1)
			}
		}
	}

	live, exited := liveAndExited(in)
	if live != 1 || exited != 0 {
		t.Errorf("after %d completed and joined spawns, live=%d exited=%d, want 1 and 0.\n"+
			"1 is the instantiation thread, which does not terminate; 0 is every status record "+
			"having been consumed by its join (contract §2 T-5.2).", spawnCount, live, exited)
	}
}

// TestATidNeverComesBackAfterItsThreadRetires is contract §2 T-5.5, and it is a live risk *because* of row
// 1 rather than in spite of it.
//
// **Before live-only membership the clause was true by accident.** `world.members` only ever grew, so an id
// handed out was permanently spoken for by an entry in the set and no plausible implementation could reissue
// it. Retirement removes that accident: a set with holes in it is exactly the shape that invites a reaper to
// fill them, and the reuse would be invisible to every other arm in this file — a joined `tid` answering for
// a second thread is a *correct-looking* answer to the wrong question.
//
// So the test spawns a generation, drives it to completion, joins it (which also drops the records, leaving
// nothing anywhere that remembers the ids), and then spawns a second generation and asks for the
// intersection. `nextTID` being monotonic is what makes this pass; the point is that nothing but this arm
// says it has to be.
func TestATidNeverComesBackAfterItsThreadRetires(t *testing.T) {
	const perGeneration = 8

	in := t5OpenGateModule(t)
	entry := exportedFuncIndex(t, in, "entry")

	seen := make(map[ThreadID]int, 2*perGeneration)
	for gen := 0; gen < 2; gen++ {
		tids := make([]ThreadID, 0, perGeneration)
		for i := 0; i < perGeneration; i++ {
			tid, err := in.Spawn(entry, spawnBase, 0)
			if err != nil {
				t.Fatalf("Spawn (generation %d, %d): %v", gen, i, err)
			}
			if was, dup := seen[tid]; dup {
				t.Fatalf("Spawn in generation %d handed out tid %d, already issued in generation %d — "+
					"T-5.5 is *\"a tid is never reused within an instance\"*, and after row 1 the "+
					"live set has holes for a reaper to fill", gen, tid, was)
			}
			seen[tid] = gen
			tids = append(tids, tid)
		}
		awaitGuestWord(t, in, spawnArrived, int32((gen+1)*perGeneration))
		for _, tid := range tids {
			if err := in.Join(tid); err != nil {
				t.Fatalf("Join(%d) in generation %d: %v", tid, gen, err)
			}
		}
	}

	if len(seen) != 2*perGeneration {
		t.Errorf("%d distinct tids over %d spawns — the duplicate check above should have named which, so "+
			"this arm firing alone means the map lost an entry rather than the engine reusing an id",
			len(seen), 2*perGeneration)
	}
}

// TestJoinAnswersAfterExitAndConsumesTheRecord is T-5.2's required case and its stamped bound in one
// sequence, in that order because the second is only interesting if the first works.
//
// *"Answerable after exit"* is the clause (Scott's Q2-a), and it is what a naive implementation gets wrong:
// a `Join` that waited on a live thread and refused everything else would fail exactly here, on the
// ordinary spawn-then-finish-then-join path. It works because `world.retire` writes the record **before**
// `thread.done` closes, so there is no instant at which a finished thread has no status.
//
// *"Consumed by a join"* is the bound (*"Bound the record: consumed by a join, or dropped at `Close`"*), and
// a second join is therefore `ErrUnknownThread`. Asserted through `errors.Is` because the identity is what
// an embedder branches on, and the wording of a three-cause refusal is not.
func TestJoinAnswersAfterExitAndConsumesTheRecord(t *testing.T) {
	in := t5OpenGateModule(t)
	entry := exportedFuncIndex(t, in, "entry")

	tid, err := in.Spawn(entry, spawnBase, 0)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	// **The exit is established first, and the paragraph here argued the opposite.** It said *"nothing
	// sequences ahead of it deliberately … establishing that it *has* exited first would test the other half
	// of the same method"* — which has it backwards: the *live* path is `TestJoinBlocksUntilTheThreadEnds`'s
	// subject, and leaving this one unsequenced means the scheduler picks which path this test takes.
	// Measured, by the injection that drops the fast path's `delete`: this test passed with it gone, because
	// it had been reaching a thread still in `live` and consuming the record on the far side of `done`.
	awaitRetired(t, in, []ThreadID{tid})

	if jerr := in.Join(tid); jerr != nil {
		t.Fatalf("Join(%d) returned %v, want nil", tid, jerr)
	}
	if jerr := in.Join(tid); !errors.Is(jerr, ErrUnknownThread) {
		t.Errorf("a second Join(%d) returned %v, want ErrUnknownThread — the record is consumed by "+
			"the first join, which is the bound Scott's ruling put on retention (contract §2 T-5.2)",
			tid, jerr)
	}
	if _, exited := liveAndExited(in); exited != 0 {
		t.Errorf("the status map holds %d record(s) after the only spawned thread was joined, want 0 "+
			"— a `Join` answering from `thread.err` rather than from the record passes the arm "+
			"above and leaks here", exited)
	}
}

// TestJoinBlocksUntilTheThreadEnds is the other half of T-5.2: a join on a thread that has *not* finished
// waits for it, rather than refusing or answering early.
//
// **The premise is the gate and the assertion is the ordering.** The thread announces itself, so it is
// provably inside guest code; the join is started on a second goroutine and must still be blocked; the host
// then opens the gate and the join returns nil. An early answer — a `Join` that read a zero `thread.err`
// before the thread had written one — fails on the *first* read, which is the one that has no timing in it:
// the gate is closed, so there is no interval in which returning is legal.
func TestJoinBlocksUntilTheThreadEnds(t *testing.T) {
	in := spawnModule(t)

	tid, err := in.Spawn(exportedFuncIndex(t, in, "entry"), spawnBase, 0)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	awaitGuestWord(t, in, spawnArrived, 1)

	joined := make(chan error, 1)
	go func() { joined <- in.Join(tid) }()

	select {
	case jerr := <-joined:
		t.Fatalf("Join(%d) returned %v while the thread was still in its gated loop — the gate is "+
			"closed, so the thread has not terminated and there is nothing to report", tid, jerr)
	case <-time.After(50 * time.Millisecond):
	}

	callVoid(t, in, "store", I32(spawnGate), I32(1))
	select {
	case jerr := <-joined:
		if jerr != nil {
			t.Errorf("Join(%d) returned %v after the gate opened, want nil", tid, jerr)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("Join(%d) did not return 30s after the gate opened — `world.retire` closes `done` "+
			"after writing the record, so a wedge here is the receive never being released", tid)
	}

	// **The consumption arm belongs here too, because `Join` has two `delete` sites on two different
	// paths.** `TestJoinAnswersAfterExitAndConsumesTheRecord` is sequenced onto the record fast path by
	// `awaitRetired`, so it reaches the `delete` before the live scan; the join above blocks on `done` and
	// reaches the one after it. Measured rather than reasoned: with the after-exit test sequenced, the
	// injection that drops the post-`done` `delete` survived the whole package — this arm is the witness it
	// was missing, and *one property asserted once* is not the same as *once per path that can hold it*.
	if _, exited := liveAndExited(in); exited != 0 {
		t.Errorf("the status map holds %d record(s) after the joined thread's record was consumed, "+
			"want 0 — a join that answers and leaves the entry behind is fact 3's leak one field over "+
			"(contract §2 T-5.2 — a record is consumed by a join)", exited)
	}
}

// TestJoinNamesWhatTheCallerGotWrong is `Join`'s two refusals, and they are two rather than one because
// they are about different mistakes.
//
// `ErrNotSpawned` for the instantiation thread: it is live and never terminates, so a join on it would block
// until `Close` — *a hang wearing a wait's shape*, which is the thing a refusal buys.
//
// `ErrUnknownThread` for an id this instance never issued. The id is taken past the counter rather than
// guessed, so the arm cannot pass by naming a thread that happens not to exist yet for an unrelated reason.
func TestJoinNamesWhatTheCallerGotWrong(t *testing.T) {
	in := t5OpenGateModule(t)

	if err := in.Join(in.host.id); !errors.Is(err, ErrNotSpawned) {
		t.Errorf("Join on the instantiation thread returned %v, want ErrNotSpawned — it does not "+
			"terminate, so a join on it is a wait with no end (contract §2 T-5.2)", err)
	}
	unissued := ThreadID(in.nextTID.Load() + 1000)
	if err := in.Join(unissued); !errors.Is(err, ErrUnknownThread) {
		t.Errorf("Join(%d) on an id this instance never issued returned %v, want ErrUnknownThread",
			unissued, err)
	}
}

// TestASpawnedThreadsTrapReachesBothChannelsAndTheJoin is contract §2 T-5.3, and the point of the test is
// that the **three readers are three, with three different bounds.**
//
// T-5.3 is *"reported to the embedder at the next host entry into the instance"* plus Scott's addition —
// *"the retained trap must also be retrievable by the embedder independently of any host entry"*, on the
// ground that *"a guest that spawns, traps, then blocks forever in a futex never makes a host entry."* So:
//
//   - **`Join` answers the per-thread status, once.** The record is consumed, so the trap arrives here first
//     and is gone from the map afterwards.
//   - **`Invoke` reports the fault once and then stops.** An instance whose thread trapped must not fail
//     every subsequent call with the same news, so the *report* is consumed while the value is not.
//   - **`Fault` answers any number of times and consumes nothing**, which is why it is asserted last: it
//     still answers after both consumptions above.
//
// **The order is the assertion.** A single retained value with one consumer would pass whichever of these
// three ran first and fail the other two, so running all three in a fixed order over one trap is what makes
// the two-views-of-one-value claim checkable rather than asserted.
func TestASpawnedThreadsTrapReachesBothChannelsAndTheJoin(t *testing.T) {
	in := t5TrapModule(t)

	tid, err := in.Spawn(exportedFuncIndex(t, in, "entry"), spawnBase, 0)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	jerr := in.Join(tid)
	var jtrap *Trap
	if !errors.As(jerr, &jtrap) || jtrap.Reason != "unreachable" {
		t.Fatalf("Join(%d) returned %v, want the `unreachable` trap — the entry bumps its arrival "+
			"word and then traps, so a nil here means the trap never reached `runEntry`'s return "+
			"and the thread's status is a clean exit for a body that did not finish", tid, jerr)
	}

	// The host entry. Any export would do; `read` is used because its own answer is checkable, which is
	// what makes the *second* call below an assertion that the instance still works rather than one that
	// it merely returns.
	_, ierr := in.Invoke("read", I32(spawnArrived))
	if !errors.Is(ierr, ErrThreadFault) {
		t.Errorf("the first host entry after a spawned thread trapped returned %v, want ErrThreadFault "+
			"— T-5.3 makes this entry the report, and it pre-empts the call rather than running it",
			ierr)
	}
	var itrap *Trap
	if !errors.As(ierr, &itrap) || itrap.Reason != "unreachable" {
		t.Errorf("the reported fault %v does not carry the trap that caused it — an embedder told "+
			"*that* its guest broke and not *how* has to go looking for `Join`, which may already "+
			"have been consumed", ierr)
	}

	if got := call(t, in, "read", I32(spawnArrived)); got != 1 {
		t.Errorf("the second host entry read %d at the arrival word, want 1 — the report is consumed "+
			"by the first entry, so this call must run; a still-reporting instance is one an "+
			"embedder can never use again", got)
	}

	if ferr := in.Fault(); !errors.Is(ferr, ErrThreadFault) {
		t.Errorf("Fault() returned %v after the report was consumed, want the retained fault — the "+
			"value is never consumed, which is Scott's *"+
			"\"retrievable independently of any host entry\"* half of T-5.3", ferr)
	}
}

// TestCloseEndsARunningThreadAndWaitsForTheUnwind is contract §2 T-5.4's first case and the repair for fact
// 4 of [ADR 0071]'s table: **`Close` returned in 39µs while a spawned counter ran on to 23008.**
//
// **The assertion is read off the engine and not off `Close`, and not off the thread's goroutine either.**
// `Close` returning nil is what the broken engine also did; what it could not do is leave the instance
// *quiescent* at the moment `Close` returned. That is the property T-5.4 names — *"no guest instruction of
// the instance executes after shutdown returns"* — and it is the one a spinning thread falsifies.
//
// **It is deliberately not a non-blocking read of `done`.** That was this arm's first form and it is a race
// in the test rather than an assertion about the engine: `retire` releases quiescence and `close(done)`
// happens after it in the same `defer`, so a correct `Close` may return in the window between the two and
// the receive would find `done` open. The bounded receive below is only the happens-before edge for reading
// `sp.err`; quiescence is what is being asserted, and it is true the instant `Close` returns.
//
// The gate is never opened. The thread can only leave its loop by being terminated, so a `Close` that did
// not reach it leaves this test with a thread still spinning and a `done` that is open.
func TestCloseEndsARunningThreadAndWaitsForTheUnwind(t *testing.T) {
	in := spawnModule(t)

	if _, err := in.Spawn(exportedFuncIndex(t, in, "entry"), spawnBase, 0); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	awaitGuestWord(t, in, spawnArrived, 1)
	sp := onlySpawnedThread(t, in)

	if err := in.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if ok, detail := quiescent(in); !ok {
		t.Fatalf("Close returned on an instance that is not quiescent (%s), so the unwind T-5.4 requires "+
			"it to wait for has not happened. This is ADR 0071's fact 4 exactly — a terminal "+
			"operation returning while guest code executes.", detail)
	}

	// Bounded, and only for the read edge on `sp.err`: quiescence above already means the thread retired,
	// so this receive is on an already-closed channel in every passing run. A timeout here is therefore a
	// finding about the `retire`-then-`close` ordering rather than about how long the unwind took.
	select {
	case <-sp.done:
	case <-time.After(30 * time.Second):
		t.Fatalf("the instance is quiescent but %s never closed `done` — `retire` and the close are one "+
			"`defer` in that order, so quiescence without the close is that ordering broken", sp)
	}
	if !errors.Is(sp.err, ErrTerminated) {
		t.Errorf("the terminated thread recorded %v, want ErrTerminated — its gate was never opened, "+
			"so the only way out of its loop is the terminal safepoint, and any other status means "+
			"it left by a route this test did not arrange", sp.err)
	}
	if in.Fault() != nil {
		t.Errorf("Fault() is %v after a shutdown-induced termination, want nil — a thread the engine "+
			"itself ended did not *fault*, and reporting one would tell an embedder that called "+
			"`Close` that its guest had trapped", in.Fault())
	}
	if live, exited := liveAndExited(in); live != 1 || exited != 0 {
		t.Errorf("after Close, live=%d exited=%d, want 1 and 0 — the terminated thread retires out of "+
			"`live`, and `Close` drops the status records on its way out (contract §2 T-5.2)",
			live, exited)
	}
}

// TestCloseTrapsAWaiterOutOfItsWait is T-5.4's second case, in the clause's own words: a suspended thread is
// ended *"by trapping it out of the wait rather than by returning one of that instruction's defined
// results."*
//
// **The three results are the reason this is not a fourth result.** `memory.atomic.wait` is defined to push
// 0, 1 or 2, and `notify`'s wake count is guest-visible — so *woken* in particular is a number another
// thread may already have read. A shutdown spelled as any of the three tells the guest that something
// happened which did not. So the arm asserts an **error** and, specifically, that the invocation did not
// return a value at all.
//
// The timeout is long enough that expiry is not an available answer within the test: a run in which the
// waiter timed out would report `waitTimedOut` and this arm would fail with the value in the message rather
// than pass for the wrong reason.
func TestCloseTrapsAWaiterOutOfItsWait(t *testing.T) {
	in := futexModule(t)

	res := make(chan outcome, 1)
	go func() {
		res <- callOffGoroutine(in, "wait32", I32(0), I32(0), I64(int64(5*time.Minute)))
	}()
	awaitQueued(t, in, 0, 1)

	if err := in.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// The wait was `Close`'s to end, so the instance is quiescent when `Close` returns — same assertion as
	// the running-thread arm, and for the same reason it is not a non-blocking receive on `res`:
	// `invokeIndex`'s `defer` releases quiescence through `leaveCall` and the goroutine's send happens
	// after `Invoke` returns to it, so a correct `Close` can return before the send.
	if ok, detail := quiescent(in); !ok {
		t.Fatalf("Close returned while the waiter was still counted (%s) — the quiescence wait is what "+
			"makes T-5.4's *\"MUST wait for the unwinds\"* true, and a `Close` that returns first "+
			"leaves an embedder tearing down an instance that still has a thread in it", detail)
	}

	select {
	case r := <-res:
		if r.err == nil {
			t.Fatalf("the wait returned result %d rather than an error — T-5.4 ends a suspended thread "+
				"by trapping it out, because all three of the instruction's defined results are "+
				"claims about something that did not happen (0 in particular is a wake another "+
				"thread's `notify` count may already have reported)", r.res)
		}
		if !errors.Is(r.err, ErrTerminated) {
			t.Errorf("the wait ended with %v, want ErrTerminated", r.err)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("the instance is quiescent but the wait has not returned — the invocation's caller " +
			"uncount is in the same `defer` that returns the error, so this is unreachable unless " +
			"quiescence is released by something other than the unwind")
	}

	if queued := queuedAt(in, 0); queued != 0 {
		t.Errorf("%d waiter(s) are still queued at address 0 after the terminating thread left, want 0 "+
			"— a departed waiter still in the queue spends part of a later `notify`'s count on a "+
			"thread that has gone, which is `resolveExpiry`'s own reason for dequeuing", queued)
	}
}

// queuedAt reads the futex queue's depth at one address. `awaitQueued`'s read without the wait, because this
// caller is asserting a *final* state rather than sequencing against a transient one.
func queuedAt(in *Instance, ea uint64) int {
	mem := in.mems[0]
	mem.waitMu.Lock()
	defer mem.waitMu.Unlock()

	return len(mem.waiters[ea])
}

// TestStopAfterCloseIsRefused is the pause-versus-teardown distinction as a refusal: *"A pause must not
// disturb a blocked host call; a teardown must interrupt it"* (Scott, on the #646 review), and a pause
// *after* a teardown has nothing to pause.
//
// **The refusal is `ErrClosed` rather than nil, because a nil would be a stop nobody can end.** `Close` nils
// `world.resume` so that no later `Resume` can revive a terminated thread; a `Stop` that installed a fresh
// round on a closed world would therefore be a round with no releaser — and it would clear nothing, since
// the terminal `stopReq` must stay set. Reporting is the only answer that leaves the instance in the state
// `Close` put it in.
func TestStopAfterCloseIsRefused(t *testing.T) {
	in := t5OpenGateModule(t)

	if err := in.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := in.Stop(time.Second); !errors.Is(err, ErrClosed) {
		t.Errorf("Stop on a closed instance returned %v, want ErrClosed — a stop installs a round that "+
			"only `Resume` ends, and `Close` has already made `Resume` a no-op", err)
	}
}

// TestAClosedInstanceInvokesNothing is the refusal that moved: it used to live at the host-call boundary,
// where only a module with a host import could reach it.
//
// **Before [ADR 0071] this call ran the guest.** `beginHostCall` was the only site that knew the instance was
// closed, so a module without a host import ran to completion on a torn-down instance, and one with a host
// import ran until it reached the import. Row 4 makes the difference observable — `Close` marks `in.host`
// terminal like anything else, so the call is now ended at `enterFrame`'s poll — and `ErrTerminated` is the
// wrong word for it: nothing was terminated, the call never started.
//
// The guest word is read afterwards to say that nothing *ran*, which is the half a refusal can pass without:
// an engine that executed the body and then reported `ErrClosed` satisfies the first assertion.
func TestAClosedInstanceInvokesNothing(t *testing.T) {
	in := t5OpenGateModule(t)

	if err := in.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := in.Invoke("store", I32(spawnSentinel), I32(spawnMark)); !errors.Is(err, ErrClosed) {
		t.Errorf("Invoke on a closed instance returned %v, want ErrClosed — `ErrTerminated` would name "+
			"a termination that did not happen, since the call never began", err)
	}
	// Read through the engine's own image rather than through the `read` export, necessarily: the
	// instance is closed, so there is no invocation left to ask with. Safe as a plain load — every thread
	// that could have stored here has been ended and waited for, which is what `Close` returning nil
	// means, so this is the one reader.
	if got := in.mems[0].view()[spawnSentinel]; got != 0 {
		t.Errorf("the sentinel word's low byte is %d after a refused Invoke, want 0 — the body ran "+
			"and the "+
			"refusal was reported after it rather than instead of it", got)
	}
}

// TestCloseReportsAnInstanceItCouldNotQuiesce is T-5.4's third clause, which is Scott's own correction to
// the ruling: *"If a case remains that provably can't be ended, name it and have `Close` return an error
// rather than return silently."*
//
// **The un-endable case here is case 1 of the two named on `Close`: a host function that ignores its
// cancelled context.** Pure Go cannot take a running goroutine away, so this is not a defect to fix but a
// limit to report — and the report is what this test is about. The embedder's function blocks on a channel
// the test never closes, so the instance genuinely never falls quiet.
//
// **The interval is shortened rather than waited out**, which is `closeQuiesceInterval`'s own stated reason
// for being a `var`: at ten seconds the only test that can witness this arm costs the bound, and the
// alternative — calling `in.unquiesced()` directly — proves the message renders while nothing reaches it.
//
// The expiry error deliberately does **not** claim the case was un-endable, and the assertion mirrors that:
// it reads `ErrCloseDeadline` and the still-live thread's name, not a diagnosis. From out here, slow and
// impossible look the same.
func TestCloseReportsAnInstanceItCouldNotQuiesce(t *testing.T) {
	withCloseInterval(t, 50*time.Millisecond)

	entered := make(chan struct{})
	wedged := make(chan struct{})
	// Never closed, deliberately: this goroutine's whole job is to be the case that cannot be ended. It
	// leaks for the rest of the run, which is the honest cost of witnessing a limit whose definition is
	// that nothing can end it.
	in := hostLink(t, `(module
		(import "h" "wedge" (func $wedge))
		(func (export "call") (call $wedge)))`,
		binary.Features{}, hostImports(map[string]Extern{
			"wedge": HostExtern(ft(nil, nil), func(*Caller, []Value) ([]Value, error) {
				close(entered)
				<-wedged
				return nil, nil
			}),
		}))

	go func() { _, _ = in.Invoke("call") }()
	select {
	case <-entered:
	case <-time.After(30 * time.Second):
		t.Fatal("the embedder's function was never entered, so this test's premise is absent rather " +
			"than its assertion failing")
	}

	err := in.Close()
	if !errors.Is(err, ErrCloseDeadline) {
		t.Fatalf("Close returned %v, want ErrCloseDeadline — a host function that ignores its "+
			"cancelled context is case 1 of the two `Close` names, and T-5.4 requires it be "+
			"reported rather than returned silently", err)
	}
	if !errorMentions(err, in.host.String()) {
		t.Errorf("the expiry error %q does not name the thread that is still live (%s) — the error "+
			"reports which threads did not end, because from outside the engine slow and impossible "+
			"are the same observation", err, &in.host)
	}
}

// errorMentions is a substring test over an error's rendering, and it is used **once**, for the one claim in
// this file that is about a message rather than about an identity.
//
// Every other arm here reads `errors.Is`, because *the identity is what a caller branches on and the wording
// is not*. The exception is `ErrCloseDeadline`, whose whole content is the report: T-5.4 asks the un-endable
// case to be *named*, so an error that carried the right identity and named no thread would satisfy every
// `errors.Is` and discharge nothing.
func errorMentions(err error, want string) bool {
	return err != nil && want != "" && strings.Contains(err.Error(), want)
}
