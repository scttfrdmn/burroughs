// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"errors"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// spawnModule is the guest for T-1's behavioural arms: one entry function of exactly T-1's shape,
// gated on a word the host writes, plus the accessors the host needs to sequence against it.
//
// # The layout is three words from the argument, and that is what makes the argument checked
//
// The entry takes `$base` and uses it for everything: it bumps an arrival counter at `$base`, spins
// until the gate at `$base+4` is non-zero, then stores a sentinel at `$base+8`. So a spawn that
// dropped the argument — or widened it wrongly, or read the frame's local 0 as uninitialised — would
// bump a counter the host is not watching and the sequencing below would time out with the premise
// unmet rather than pass. `$base` is 64 rather than 0 for exactly that reason: at 0 a dropped
// argument is indistinguishable from a delivered one.
//
// # Gated rather than counted, on grave #598's ruling
//
// A trip count makes the *duration* of the guest's work the thing a deadline is measured against, and
// then a deadline gets used as a hang detector — three concurrent callers came within 2.5s of a 30s
// bound on an idle box and exceeded it on CI, and the arm reported a wedge over callers that were
// blocked on nothing. A gate moves both properties out of the scheduler's hands: the thread cannot
// leave the loop until the host says so, so *"the thread is inside guest code"* is a fact the host
// established rather than one it is hoping for, and the post-`Resume` wait bounds wedge-freedom only.
//
// Every access is atomic and the memory is `shared` because two agents touch these words. A plain
// load in the loop against a plain store from the host is a data race on guest memory, and `-race` is
// an authority this package answers to. That makes the threads gate a decode requirement, which is
// `instantiateThreads1`'s reason for existing.
//
// `notEntry` is the wrong-shape function the refusal table below needs, and it is in this module
// rather than in its own so that the refusal is about the *type* and not about the instance: the
// shared memory is present, so `hasSharedMemory` has already passed when `ErrThreadEntry` is reached.
func spawnModule(t *testing.T) *Instance {
	t.Helper()
	const src = `(module
		(memory 1 1 shared)
		(func (export "entry") (param $base i32)
			(drop (i32.atomic.rmw.add (local.get $base) (i32.const 1)))
			(loop
				(br_if 0 (i32.eqz (i32.atomic.load (i32.add (local.get $base) (i32.const 4))))))
			(i32.atomic.store (i32.add (local.get $base) (i32.const 8)) (i32.const 24301)))
		(func (export "notEntry") (param $a i32) (result i32)
			(local.get $a))
		(func (export "read") (param $addr i32) (result i32)
			(i32.atomic.load (local.get $addr)))
		(func (export "store") (param $addr i32) (param $v i32)
			(i32.atomic.store (local.get $addr) (local.get $v))))`
	return instantiateThreads1(t, src)
}

// The three words `spawnModule`'s entry uses, and the sentinel it finally stores. Named constants
// rather than literals because two of the three are read by the host at an offset the *guest*
// computed, so a disagreement between the two sides would show up as a timeout rather than as a
// mismatch.
const (
	spawnBase     int32 = 64
	spawnArrived  int32 = spawnBase
	spawnGate     int32 = spawnBase + 4
	spawnSentinel int32 = spawnBase + 8
	spawnMark     int32 = 24301
)

// exportedFuncIndex is the test-side answer to `Spawn` taking a function *index*, which is the shape
// contract §2 T-1 fixes (`spawn(entry_func, arg, stack_hint)`) and not a convenience this package
// chose. A `t.Fatalf` here reports a missing premise, in those words.
func exportedFuncIndex(t *testing.T, in *Instance, name string) uint32 {
	t.Helper()
	ext, ok := in.Export(name)
	if !ok {
		t.Fatalf("the module exports no %q, so this test's premise is absent rather than its "+
			"assertion failing", name)
	}
	if ext.Kind != binary.ExternFunc {
		t.Fatalf("%q exports a %v rather than a function", name, ext.Kind)
	}
	return ext.fnIdx
}

// awaitGuestWord blocks until an atomically-stored guest word reaches `want`, and it is **sequencing
// and never an assertion** — `awaitQueued`'s rule one file over, for its reason: a test's premise is
// not its assertion, and a premise that fails silently means nothing was measured.
//
// Read through the guest's own `read` export rather than out of `in.mems[0]` directly. A plain host
// read of the backing array while a spawned thread stores into it atomically is a data race, and the
// point of these arms is that `-race` has nothing to say about them.
//
// Polled rather than slept for a plausible interval: *a duration is not a completion signal*. The
// deadline exists so a wedge is a FAIL rather than a hung run, and it is generous because what it
// bounds is scheduler latency for one goroutine start — orders of magnitude away from the bound,
// which is the direction that makes a red mean *blocked* rather than *slow*.
func awaitGuestWord(t *testing.T, in *Instance, addr, want int32) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if got := call(t, in, "read", I32(addr)); got == want {
			return
		} else if !time.Now().Before(deadline) {
			t.Fatalf("guest word at %d never reached %d (last read %d).\n"+
				"This is a premise and not an assertion: the spawned thread was expected to "+
				"reach its entry function and announce itself, so a timeout here says the "+
				"thread did not run rather than that it ran wrongly.", addr, want, got)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestSpawnRunsItsEntryFunctionConcurrentlyWithItsArgument is contract §2 T-1's own sentence, run:
// *"a thread-spawn host primitive of the shape `spawn(entry_func, arg, stack_hint) → tid`, creating a
// wasm thread backed 1:1 by an OS thread, sharing the module's shared linear memory."*
//
// **Four claims, and the second is the one a weaker test would skip.** That `Spawn` returns a
// non-zero tid distinct from the host thread's. That the entry function runs **concurrently** — the
// host waits for the guest's arrival word *before* it releases the gate, so a `Spawn` that ran the
// entry inline on the calling goroutine could not reach this line at all, it would wedge in `Spawn`
// forever. That the argument arrived, which the layout asserts by construction (see `spawnModule`).
// And that the thread terminated with no error, read from `thread.err` after `done` closes — the
// channel is the happens-before edge that makes that read legal.
//
// The tid is checked against `in.host.id` rather than against `1`, because *"monotonic from 1"* is
// `ThreadID`'s promise and *which* number the host consumed is `InstantiateLinked`'s business. What
// matters here is that a spawned thread is not the host thread wearing its id.
func TestSpawnRunsItsEntryFunctionConcurrentlyWithItsArgument(t *testing.T) {
	in := spawnModule(t)

	tid, err := in.Spawn(exportedFuncIndex(t, in, "entry"), spawnBase, 0)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if tid == 0 {
		t.Errorf("Spawn returned tid 0, which is the value reserved for `alongside an error` — "+
			"see ThreadID, where 0 is deliberately not a valid id so that a zero cannot mean "+
			"`the first thread`. Error was %v.", err)
	}
	if tid == in.host.id {
		t.Errorf("Spawn returned tid %d, the same id as the host thread — a spawned thread that "+
			"shares the host's id shares its `stopReq` and its caller count, so §3's whole "+
			"per-thread mechanism would be reading one thread's state as two", tid)
	}

	// The concurrency claim. Nothing below this line could run if `Spawn` had executed the entry
	// itself: the gate is still zero, so the guest is in its loop, and the host is what releases it.
	awaitGuestWord(t, in, spawnArrived, 1)

	// The handle is taken **before** the gate is released, and the ordering is load-bearing after
	// ADR 0071: `world.live` is live-only, so a thread reaped between the release and this
	// lookup would leave the world holding only the host and `onlySpawnedThread` would fail with a
	// count of 0. The guest is provably still in its loop here, because it announced itself.
	sp := onlySpawnedThread(t, in)
	callVoid(t, in, "store", I32(spawnGate), I32(1))

	// The wait is on `done` rather than on `Join` so the failure has a bounded message: `Join` blocks
	// until the thread ends and a wedge there is the test binary's own timeout, ten minutes later and
	// with nothing said about which announcement had been made.
	select {
	case <-sp.done:
	case <-time.After(30 * time.Second):
		t.Fatalf("the spawned thread never terminated after the gate was released — it announced "+
			"itself at %d, so it reached guest code and then failed to leave it", spawnArrived)
	}

	// The *outcome* is read through `Join`, which is T-5.2's surface and is answerable after exit by
	// construction — the goroutine retires into `world.exited` before it closes `done`, so this join
	// resolves from the record rather than by waiting again.
	if err := in.Join(tid); err != nil {
		t.Errorf("Join(%d) returned %v, want nil.\n"+
			"Called after `done` closed, so this is the record `world.retire` wrote and not a "+
			"second wait; a trap here is a finding about `runEntry`'s frame rather than about "+
			"spawning.", tid, err)
	}
	if got := call(t, in, "read", I32(spawnSentinel)); got != spawnMark {
		t.Errorf("sentinel at %d is %d, want %d — the entry function ran its loop and then did not "+
			"finish its body, so `invoke` returned to `runEntry` early or the argument that "+
			"addressed all three words was not the one the host passed",
			spawnSentinel, got, spawnMark)
	}
}

// onlySpawnedThread hands back the one member of the world that is not the host thread, failing when
// there is not exactly one.
//
// **It is only usable while that thread is live, and the reason changed under it.** This doc used to
// say `Spawn` returned an id and no handle because T-5 was open (§10.3, #12), so the package's own
// tests reached the object rather than an exported surface. T-5 is decided ([ADR 0071][0071]) and
// `Instance.Join` exists, so the surface argument is gone; what survives is that `Join` answers with
// an *error* and the assertions below want `callers`, `blocked` and `reported`, which no exported
// surface reports and none should. **Anything about a thread's outcome goes through `Join`**; this
// helper is for its safepoint state, taken while it has one.
//
// `world.live` is live-only membership after 0071, so a caller must hold this thread in its loop when
// it asks. A lookup after the thread could have been reaped finds only the host and fails with a count
// of 0 — a real failure of this helper's contract rather than a flake to retry, since the caller asked
// a question about a thread that no longer exists.
//
// The exactness is the point rather than convenience: "not exactly one" covers both a spawn that
// registered nothing — which would make every assertion below it a claim about the host thread — and
// one that registered twice.
//
// [0071]: ../../docs/decisions/0071-t-5-is-live-only-membership-a-bounded-status-record-a-fault-in-two-channels-and-a-sentinel-panic-for-the-terminal-unwind.md
func onlySpawnedThread(t *testing.T, in *Instance) *thread {
	t.Helper()
	w := &in.world
	w.mu.Lock()
	defer w.mu.Unlock()

	var found []*thread
	for _, m := range w.live {
		if m != &in.host {
			found = append(found, m)
		}
	}
	if len(found) != 1 {
		t.Fatalf("the world holds %d members and %d of them are not the host thread, want exactly "+
			"1.\nRegistration happens inside `newThread`, before the `go` statement, so a count "+
			"of 0 means the spawn was refused or admitted nowhere — and a thread in no world "+
			"opts out of stopping entirely, since `poll` and `enterCall` are both nil-legal.",
			len(w.live), len(found))
	}
	return found[0]
}

// TestSpawnRefusesTheCasesItCannotAnswer is decision 0068's four named limits, each read back through
// `errors.Is` rather than through a message match, because the identity is the part a caller can
// branch on and the wording is not.
//
// **Every arm also asserts that the refusal left no trace**, which is `spawn`'s stated placement
// rule: creation is after every refusal and admission is inside creation, so a refused spawn consumes
// no `ThreadID` and adds no member to a world whose `Stop` would then wait for it. That second half is
// the one with teeth — a leaked member is not a wrong answer at the call site, it is a `Stop` that
// waits out its whole deadline for a thread that never existed, arriving later and somewhere else.
//
// `ErrStopInProgress` is the arm where the two halves come apart: it is refused by `world.admit`,
// *inside* `newThread`, so it is the only case where the trace-free claim is about code that ran
// rather than about code that was skipped. `newThread` bumps the id counter after `admit` returns for
// exactly that reason.
func TestSpawnRefusesTheCasesItCannotAnswer(t *testing.T) {
	// The foreign-entry pair is built here rather than in a case body so that both instances hold a
	// shared memory: `hasSharedMemory` is checked before `resolveCall`, so an importer with an
	// unshared memory would be refused by the wrong arm and the test would pass for the wrong reason.
	foreignPair := func(t *testing.T) *Instance {
		t.Helper()
		supp := instantiateThreads1(t, `(module
			(memory 1 1 shared)
			(func (export "entry") (param $base i32)))`)
		// The import precedes the memory because the binary format's section order does, and
		// `text.EncodeModule` reports it rather than sorting for the author.
		imp, trap, err := link1Threads(t, `(module
			(import "s" "entry" (func $e (param i32)))
			(memory 1 1 shared)
			(export "entry" (func $e)))`, exportsOf(supp))
		if err != nil || trap != nil {
			t.Fatalf("linking the importer: err=%v trap=%v", err, trap)
		}
		return imp
	}

	cases := []struct {
		name  string
		build func(*testing.T) *Instance
		// export is the entry function's name in the instance `build` returns, and `before` runs
		// after the index is resolved so that a case can put the instance into the state its
		// refusal is about.
		export string
		before func(*testing.T, *Instance)
		want   error
	}{{
		name: "no shared memory to share",
		// `instantiate1`, not `instantiateThreads1`: an unshared memory decodes on the default
		// board, and using the gated decoder here would leave the arm silent about the case an
		// embedder actually reaches — a module built without the threads proposal in mind.
		build: func(t *testing.T) *Instance {
			t.Helper()
			in, trap := instantiate1(t, `(module
				(memory 1 1)
				(func (export "entry") (param $base i32)))`)
			if trap != nil {
				t.Fatalf("instantiate: %v", trap)
			}
			return in
		},
		export: "entry",
		want:   ErrNotShared,
	}, {
		name:   "entry function returns a value",
		build:  spawnModule,
		export: "notEntry",
		want:   ErrThreadEntry,
	}, {
		name:   "entry function is defined in another instance",
		build:  foreignPair,
		export: "entry",
		want:   ErrForeignEntry,
	}, {
		name:   "a stop is in flight",
		build:  spawnModule,
		export: "entry",
		before: func(t *testing.T, in *Instance) {
			t.Helper()
			// No guest thread is running, so every member is at a safepoint by `blocked ==
			// callers` and this returns immediately. What it establishes is `world.resume != nil`,
			// which is the state `admit` refuses on — and `Resume` is deliberately *not* called,
			// because the case is a spawn attempted mid-round.
			if err := in.Stop(30 * time.Second); err != nil {
				t.Fatalf("Stop on an idle instance: %v — this arm needs a round in flight, so a "+
					"failure here is a missing premise", err)
			}
			t.Cleanup(in.Resume)
		},
		want: ErrStopInProgress,
	}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := c.build(t)
			entry := exportedFuncIndex(t, in, c.export)
			if c.before != nil {
				c.before(t, in)
			}

			idsBefore := in.nextTID.Load()
			in.world.mu.Lock()
			liveBefore := len(in.world.live)
			in.world.mu.Unlock()

			tid, err := in.Spawn(entry, spawnBase, 0)
			if !errors.Is(err, c.want) {
				t.Fatalf("Spawn returned %v, want an error matching %v.\nMatched with `errors.Is` "+
					"because the identity is what a caller branches on; a refusal that is right "+
					"in its wording and wrong in its wrapping is a refusal no embedder can act "+
					"on.", err, c.want)
			}
			if tid != 0 {
				t.Errorf("a refused Spawn returned tid %d, want 0 — ThreadID reserves 0 for "+
					"exactly the value that travels beside an error", tid)
			}
			if got := in.nextTID.Load(); got != idsBefore {
				t.Errorf("the id counter moved from %d to %d across a refused spawn.\n"+
					"`newThread` bumps it only after `world.admit` has succeeded, so a refusal "+
					"that consumes an id makes the ids a record of attempts rather than of "+
					"threads.", idsBefore, got)
			}
			in.world.mu.Lock()
			liveAfter := len(in.world.live)
			in.world.mu.Unlock()
			if liveAfter != liveBefore {
				t.Errorf("the world's membership moved from %d to %d across a refused spawn.\n"+
					"This is the half with teeth: a leaked member is not a wrong answer here, "+
					"it is a later `Stop` waiting out its whole deadline for a thread that was "+
					"never started.", liveBefore, liveAfter)
			}
		})
	}
}

// TestStopReachesASpawnedThread is decision 0068's central obligation, and it is a **witness rather
// than a forecast**: without the `enterCall`/`leaveCall` pair in `runEntry` the spawned thread runs
// guest code with `blocked == callers == 0`, which is exactly what `Stop` reads as *at a safepoint*.
// That is **#592** — the grave [ADR 0067][0067] was written to close — arriving through a new site
// instead of through a changed predicate.
//
// # What discriminates, and why it is not `Stop`'s return value
//
// `Stop` returns nil in both the working and the broken engine, so its verdict cannot be the
// assertion. In the working one it returns nil because an arrival was received; in the broken one
// because there was nothing to wait for. The two are told apart by two reads of the mechanism `Stop`
// consults, both under `world.mu`:
//
//   - **Before the stop, `callers` is 1 and `blocked` is 0**, so `blocked == callers` is false and this
//     thread is a *sender* rather than a member counted as already arrived. Delete the pair from
//     `runEntry` and this is the assertion that fires, immediately and with the count in the message.
//   - **After the stop returns nil, `reported` is true.** `parkAtSafepoint` is the only writer, so
//     this is a positive signal that the thread genuinely parked at a back-edge — not an inference
//     from elapsed time, which is what a *"did it not finish?"* arm would be reduced to.
//
// The third claim is that the thread is still *usable* afterwards: `Resume`, then the gate, then a
// terminated thread with no error and its sentinel stored. A safepoint that corrupted the frame, the
// value stack or `pc` would satisfy both reads above.
//
// **Nothing is invoked between `Stop` and `Resume`, deliberately.** `Stop` sets `stopReq` on every
// member including `in.host`, so a host `Invoke` in that window parks the test's own goroutine at
// `enterFrame` — which is the mechanism working, and would read as a wedged test.
//
// [0067]: ../../docs/decisions/0067-a-caller-count-joins-the-blocked-mark-because-sp-2s-predicate-is-about-callers-and-a-thread-is-not-one.md
func TestStopReachesASpawnedThread(t *testing.T) {
	in := spawnModule(t)
	if _, err := in.Spawn(exportedFuncIndex(t, in, "entry"), spawnBase, 0); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	sp := onlySpawnedThread(t, in)

	// The premise this whole test rests on: the thread is inside its gated loop, established by the
	// guest's own announcement rather than by waiting a while.
	awaitGuestWord(t, in, spawnArrived, 1)

	in.world.mu.Lock()
	callers, blocked := sp.callers, sp.blocked
	in.world.mu.Unlock()
	if callers != 1 || blocked != 0 {
		t.Fatalf("the spawned thread is in its loop with callers=%d blocked=%d, want 1 and 0.\n"+
			"`Stop` asks `blocked == callers` (decision 0067), so callers=0 here makes a thread "+
			"executing guest instructions read as *at a safepoint* — #592's failure with "+
			"`runEntry` as the new site. `enterCall`/`leaveCall` around `in.invoke` is the "+
			"denominator that makes the predicate false while this thread runs.", callers, blocked)
	}

	if err := in.Stop(30 * time.Second); err != nil {
		t.Fatalf("Stop: %v — the thread was in a gated loop whose back-edge polls every "+
			"iteration, so a deadline expiry here means the poll did not reach it", err)
	}
	in.world.mu.Lock()
	reported := sp.reported
	in.world.mu.Unlock()
	if !reported {
		t.Errorf("Stop returned nil and the spawned thread never reported an arrival.\n" +
			"`parkAtSafepoint` is the only writer of `reported`, so this is the reading that " +
			"tells a stop that *waited* from one that counted this thread as already arrived — " +
			"and the latter is a `Stop` returning nil while guest code runs.")
	}
	in.Resume()

	callVoid(t, in, "store", I32(spawnGate), I32(1))
	select {
	case <-sp.done:
	case <-time.After(30 * time.Second):
		t.Fatalf("the spawned thread never terminated after Resume and the gate.\n" +
			"It arrived at a safepoint, so it was parked; a thread that does not come back from " +
			"one is `Resume` failing to release it rather than a stop failing to reach it.")
	}
	if sp.err != nil {
		t.Errorf("the resumed thread terminated with %v, want nil", sp.err)
	}
	if got := call(t, in, "read", I32(spawnSentinel)); got != spawnMark {
		t.Errorf("sentinel at %d is %d, want %d — the thread was stopped and resumed and then did "+
			"not finish its body, which is a safepoint that damaged the frame, the value stack "+
			"or `pc` rather than one that held it", spawnSentinel, got, spawnMark)
	}
}
