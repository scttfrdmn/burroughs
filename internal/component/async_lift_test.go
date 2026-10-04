// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// TestUnpackCallbackResultMatchesTheOracle is the differential pin for the stackless async-lift callback
// ABI's dispatch decode (definitions.py unpack_callback_result, def:2171-2177), fixtures.json
// `callback_result`. The callback returns a packed i32 the scheduler dispatches on — the return value that
// tells "done" from "waiting on set si" from "yield". EVERY code is asserted, not just the happy-path EXIT:
// a success-only pin is green against a dispatch that cannot tell WAIT from EXIT, which is the mis-route
// shape this tier has caught three times (Scott's caution on the second-guest slice). The out-of-range
// codes are asserted to TRAP — the guard a decode that masks (& 0xf) instead of range-checking would skip.
func TestUnpackCallbackResultMatchesTheOracle(t *testing.T) {
	var doc struct {
		CallbackResult struct {
			Codes struct {
				EXIT, YIELD, WAIT, MAX uint32
			} `json:"codes"`
			Unpacks []struct {
				Packed uint32 `json:"packed"`
				Code   uint32 `json:"code"`
				SI     uint32 `json:"si"`
				Traps  bool   `json:"traps"`
			} `json:"unpacks"`
		} `json:"callback_result"`
	}
	loadFixtures(t, &doc)
	fx := doc.CallbackResult

	// The code constants are the oracle's, not hand-copied. A drift in either direction fails here.
	if uint32(callbackExit) != fx.Codes.EXIT || uint32(callbackYield) != fx.Codes.YIELD ||
		uint32(callbackWait) != fx.Codes.WAIT || uint32(callbackCodeMax) != fx.Codes.MAX {
		t.Fatalf("callback code constants (EXIT %d, YIELD %d, WAIT %d, MAX %d) != oracle (%d, %d, %d, %d)",
			callbackExit, callbackYield, callbackWait, callbackCodeMax,
			fx.Codes.EXIT, fx.Codes.YIELD, fx.Codes.WAIT, fx.Codes.MAX)
	}
	if len(fx.Unpacks) == 0 {
		t.Fatal("no callback_result.unpacks in fixtures.json — the pin is empty")
	}

	// Every case: a trap entry must trap; a value entry must decode to exactly the oracle's (code, si).
	sawTrap, sawWait := false, false
	for _, u := range fx.Unpacks {
		code, si, err := unpackCallbackResult(u.Packed)
		if u.Traps {
			sawTrap = true
			if err == nil {
				t.Errorf("packed %#x: decoded (code %d, si %d) but the oracle traps (code above MAX) — "+
					"a decode that masks instead of range-checking would pass here", u.Packed, code, si)
			}
			var trap *interp.Trap
			if err != nil && !errors.As(err, &trap) {
				t.Errorf("packed %#x: want a Trap, got %v", u.Packed, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("packed %#x: unexpected error %v, want (code %d, si %d)", u.Packed, err, u.Code, u.SI)
			continue
		}
		if uint32(code) != u.Code || si != u.SI {
			t.Errorf("packed %#x: decoded (code %d, si %d), want oracle (code %d, si %d) — the code and the "+
				"set index must not swap", u.Packed, code, si, u.Code, u.SI)
		}
		if callbackCode(u.Code) == callbackWait && u.SI != 0 {
			sawWait = true // a WAIT with a non-zero set index — the discriminating case
		}
	}
	// The pin is only meaningful if it exercises the non-happy-path codes, not just EXIT.
	if !sawTrap {
		t.Error("the pin covers no trapping (out-of-range) code — it cannot catch a decode that skips the range check")
	}
	if !sawWait {
		t.Error("the pin covers no WAIT with a non-zero set index — it cannot catch a code/set-index swap")
	}
}

// TestAsyncLiftLoopPinExercisesReentryState guards the loop-behavior differential pin (fixtures.json
// async_lift_loop), produced by driving the model's real canon_lift callback loop (def:2096-2153) through a
// MULTI-CYCLE case: WAIT -> event -> WAIT -> event -> EXIT. The pin's job is to catch a Go loop that loses
// state between re-entries yet recovers to the right final answer, so this guards that the pin actually
// exercises re-entry state (Scott's caution): >= 2 cycles, context set in cycle 1 read back in cycle 2, the
// waitable set surviving every re-entry, both armed events delivered, and the resolution. The invariants are
// SCHEDULE-INDEPENDENT — event order over a two-member set is non-deterministic in both the model
// (random.shuffle) and a goroutine-scheduled engine, so the pin is a multiset, not an ordering. The engine's
// execution loop (next increment) asserts its behavior against this pin; this test keeps the pin honest.
func TestAsyncLiftLoopPinExercisesReentryState(t *testing.T) {
	var doc struct {
		AsyncLiftLoop struct {
			Cycles           int     `json:"cycles"`
			EventsMultiset   [][]int `json:"events_multiset"`
			CtxReadback      int     `json:"ctx_readback"`
			CtxWritten       int     `json:"ctx_written"`
			SameSetEachCycle bool    `json:"same_set_each_cycle"`
			Resolved         []int   `json:"resolved"`
			ResultExpected   int     `json:"result_expected"`
		} `json:"async_lift_loop"`
	}
	loadFixtures(t, &doc)
	lp := doc.AsyncLiftLoop

	// Multi-cycle, not a single park-and-finish — the whole point of a re-entry-state pin.
	if lp.Cycles < 2 {
		t.Errorf("async_lift_loop pins %d callback cycles; a re-entry-state pin needs >= 2 (WAIT, event, "+
			"WAIT again, EXIT), or it cannot catch a loop that loses state between re-entries", lp.Cycles)
	}
	// Context survived re-entry: written in cycle 1, read in cycle 2.
	if lp.CtxReadback != lp.CtxWritten || lp.CtxWritten == 0 {
		t.Errorf("context did not survive re-entry: wrote %#x, read %#x back a cycle later", lp.CtxWritten, lp.CtxReadback)
	}
	// The waitable set survived every re-entry (same handle each cycle).
	if !lp.SameSetEachCycle {
		t.Error("the waitable set did not survive across the WAIT->event->WAIT cycle")
	}
	// Both armed events delivered, and distinct (a loop that dropped or duplicated one fails here).
	if len(lp.EventsMultiset) != 2 {
		t.Fatalf("delivered %d events across the cycles, want 2 (both armed)", len(lp.EventsMultiset))
	}
	if lp.EventsMultiset[0][1] == lp.EventsMultiset[1][1] && lp.EventsMultiset[0][2] == lp.EventsMultiset[1][2] {
		t.Errorf("the two delivered events are identical %v — one was dropped or duplicated", lp.EventsMultiset)
	}
	// The task resolved to the value it returned via task.return.
	if len(lp.Resolved) != 1 || lp.Resolved[0] != lp.ResultExpected {
		t.Errorf("resolved %v, want [%d] — the loop did not carry the returned value to resolution", lp.Resolved, lp.ResultExpected)
	}
}

// TestAsyncLiftExitOnlyResolvesViaTaskReturn is step 1 of the async-lift execution: the loop skeleton run
// end-to-end on the minimal EXIT-only guest (async-lift-exit-synth.wasm — callee calls task.return(42) then
// returns EXIT, no park). The durable lift task is created, the callee runs on it, task.return resolves it,
// and EXIT confirms resolution. The resolved value (42) matches the committed wasmtime reading (run()->42);
// a lift that returned EXIT without task.return traps. gate:async on (the mechanism is off by default).
func TestAsyncLiftExitOnlyResolvesViaTaskReturn(t *testing.T) {
	t.Setenv("BURROUGHS_ASYNC", "1")
	b, err := os.ReadFile("testdata/async-lift-exit-synth.wasm")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	in, err := InstantiateWithHost(b, NewHost(io.Discard, io.Discard, nil))
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()
	cd, ok := in.export.exports["run"]
	if !ok || cd.fn == nil {
		t.Fatal("component exports no run function")
	}
	if !cd.fn.async {
		t.Fatal("run was not bound as an async (callback) lift")
	}
	// `invokeWith(nil)` rather than `invoke()`, because the resolution is now a RETURN value. It used to
	// be read off `cd.fn.result`, a field on the shared compFunc that raced between concurrent callers
	// (#869); `invoke()` is the discarding wrapper, so a test that needs the result asks for it.
	resolved, err := cd.fn.invokeWith(nil)
	if err != nil {
		t.Fatalf("invoke run (the async-lift loop skeleton): %v", err)
	}
	if len(resolved) != 1 || resolved[0].Bits != 42 {
		t.Errorf("run resolved to %v, want [42] via task.return (matching the wasmtime reading run()->42)", resolved)
	}
	// The current-task pointer is cleared after invoke (teardown keys on the task), so a second lift is admissible.
	if in.w.async.lift != nil {
		t.Error("the current lift task was not cleared after resolution")
	}
}

// TestAnEntryWaitsForTheExecutionSlot is this arm's FOURTH subject, and the churn is the record.
//
//	#785/#732: a lift task already in flight on the agent           -> trap
//	#871:      `liftInFlight = true`, a lift EXISTS in the instance -> trap
//	#869 (a):  an occupied current-task slot                        -> trap
//	#869 (b):  an entry that cannot BEGIN within its bound          -> trap
//	#869 (c):  an entry WAITS for the slot and then succeeds        -> no trap at all
//
// The first two went because several lifts per instance is the deliverable, so "one already exists" is no
// longer an error. (a) went because the acceptance arm destroyed it: with one engine thread two
// concurrent callers *necessarily* contend, so refusing an occupied slot refused the concurrency.
//
// **(b) went on the chair's review of #882, and its subject turned out to be unreachable.** The bound
// existed to catch a lift re-entering itself, which a plain semaphore deadlocks on. Searched: a host impl
// cannot call back in — `CanonCaller`'s only guest-entry method is the depth-budgeted `Realloc`, with no
// `Invoke` and no instance accessor (§5 H-2 "enforced by absence"), and no impl in this engine captures
// an `*Instantiated`. A bound whose only subject cannot occur is a mechanism with no consumer, and it
// cost a false trap: ordinary contention expired it and the message blamed self-re-entry.
//
// So what is asserted now is the behaviour that remains: contention **waits**, as the model's
// backpressure does (def:424-430 — no trap, no bound).
func TestAnEntryWaitsForTheExecutionSlot(t *testing.T) {
	t.Setenv("BURROUGHS_ASYNC", "1")
	b, err := os.ReadFile("testdata/async-lift-exit-synth.wasm")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	in, err := InstantiateWithHost(b, NewHost(io.Discard, io.Discard, nil))
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()
	cd := in.export.exports["run"]

	// Occupy the execution slot, then release it from another goroutine after a delay. The entry must
	// **wait and then succeed** — it must not refuse, and it must not expire, because there is no bound.
	in.w.async.entrySem <- struct{}{}
	const held = 300 * time.Millisecond
	go func() {
		time.Sleep(held)
		<-in.w.async.entrySem
	}()

	start := time.Now()
	err = cd.fn.invoke()
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("an entry that merely had to WAIT for the slot failed: %v\n\nContention is the normal "+
			"state of two concurrent callers, so it must not refuse.", err)
	}
	// It must have actually waited, or the slot is not guarding entry at all and the arm is vacuous.
	if elapsed < held {
		t.Errorf("the entry completed in %s, less than the %s the slot was held — it did not wait for "+
			"the slot, so nothing here is being guarded", elapsed, held)
	}
}

// TestASecondLiftIsPermittedWhileTheFirstIsParked is this arm with its claim REVERSED by #869.
//
// # What it asserted, and why the reversal is the deliverable
//
// It was `TestAsyncLiftAtMostOneTaskTrapsWhileGenuinelyParked`, and it required a second lift entered
// during a park to **trap**. That was correct while a component instance could host one lift task, and
// it was the falsification for splitting `lift` from `liftInFlight` in #871.
//
// **#869's whole purpose is to make that trap wrong.** The Canonical ABI allows several async-lifted
// tasks in flight in one instance, and #771 exists to unlock exactly that. So the arm is not deleted and
// not weakened — its claim is inverted, and the inversion *is* the slice's headline property: a second
// lift entered while the first is parked now **succeeds**.
//
// # What still constrains it, so this is not a permissiveness change
//
// Entries still **serialize**: only one guest execution runs in the instance at a time, and a contending
// entry **waits** — `TestAnEntryWaitsForTheExecutionSlot` above pins that, and
// `TestASiblingWaitsForABlockingImportAndDoesNotTrap` pins that the wait spans a blocking host call, which
// is the guest-invariant guarantee. The pair is the point: *in flight* became permitted in the same change
// that kept *entered* exclusive, which is why neither arm alone would show the restriction moved rather
// than vanished.
//
// **This sentence has gone stale three times inside two slices**, and `TestEveryCitedTestNameResolves`
// caught it every time. It cited, in order: an overlapping-entry *refusal* (an intermediate design the
// acceptance arm destroyed, since concurrent callers necessarily contend); then that control by a name I
// had invented for it; then an entry *bound* that was reverted on review because its only subject is
// unreachable. The churn is left visible because a comment naming a control is a citation, and this one
// kept pointing at mechanisms that no longer existed.
//
// # Why it must still reach a genuine park first
//
// The polling below is kept verbatim in purpose from the version that trapped. Measured then: without
// it, the arm ran its second call while the guest was still unwinding toward its WAIT return, so it was
// testing a window it had not actually entered. A permissiveness claim measured outside the window is
// worth even less than a refusal claim was.
func TestASecondLiftIsPermittedWhileTheFirstIsParked(t *testing.T) {
	t.Setenv("BURROUGHS_ASYNC", "1")
	b, err := os.ReadFile("testdata/asynclift/suspending/component.wasm")
	if err != nil {
		t.Fatalf("the committed suspending guest is missing: %v", err)
	}
	// `tick` defers forever, so the first lift reaches its park and stays there for the whole test.
	h := NewHost(io.Discard, io.Discard, nil)
	parked := make(chan struct{})
	var once sync.Once
	// `hs` is assigned after instantiate and read from inside the import, which only ever runs during a
	// call made after that — so there is no ordering hazard, and the alternative (threading the handles
	// in some other way) would mean the impl could not see the engine's own count at all.
	var hs *asyncHandles
	var peakInFlight int32
	h.asyncImpls = map[string]asyncLowerImpl{
		"tick": func(_ *interp.CanonCaller, onStart func() []interp.Value, _ func(canon.Value)) (func(), error) {
			onStart()
			// **Sample the engine's own in-flight count from inside a live entry.** This is the only
			// point at which both lifts are demonstrably coexisting, and reading it here rather than
			// polling from the test means the concurrency is measured where it happens instead of
			// inferred from two calls overlapping in wall-clock time.
			if hs != nil {
				hs.mu.Lock()
				n := int32(hs.liftsInFlight)
				hs.mu.Unlock()
				for {
					old := atomic.LoadInt32(&peakInFlight)
					if n <= old || atomic.CompareAndSwapInt32(&peakInFlight, old, n) {
						break
					}
				}
			}
			// Signalled from inside the import, so the second entry happens when the guest has
			// DEMONSTRABLY suspended rather than after a duration — the rendezvous discipline the
			// committed harness uses, for the same reason.
			once.Do(func() { close(parked) })
			return func() {}, nil
		},
	}
	in, err := InstantiateWithHost(b, h)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()
	hs = in.w.async

	// Shorten the bound: this test WANTS the park to expire, since nothing will ever resolve `tick`, and
	// the engine's 30s backstop is not a test's timescale.
	restore := liftParkBound
	liftParkBound = 300 * time.Millisecond
	defer func() { liftParkBound = restore }()

	first := make(chan error, 1)
	go func() { _, e := in.CallValues("run", canon.U32(1)); first <- e }()

	select {
	case <-parked:
	case e := <-first:
		t.Fatalf("the first lift returned before reaching its park, so there is no overlap to witness: %v", e)
	case <-time.After(5 * time.Second):
		t.Fatal("the first lift never entered `tick`, so it never parked")
	}

	// **Wait for the task to be GENUINELY parked, not merely for `tick` to have been entered.**
	//
	// Measured: without this the arm passed against the pre-#871 single-field assertion, because the
	// import's signal fires while the guest is still unwinding toward its WAIT return — so `h.lift` was
	// still set when the second call made its check, and a `lift != nil` assertion trapped for the wrong
	// reason. The arm was protected by timing rather than by the property it claimed to test, which is
	// only visible by running the falsification.
	//
	// The parked state is exactly `lift == nil && liftsInFlight == 1`: no task's code is running, and one
	// lift exists. Polling for it is what puts the second call inside the window.
	deadline := time.Now().Add(5 * time.Second)
	for {
		in.w.async.mu.Lock()
		cur, n := in.w.async.lift, in.w.async.liftsInFlight
		in.w.async.mu.Unlock()
		if cur == nil && n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the first lift never reached a park (current task %v, in flight %d)", cur, n)
		}
		time.Sleep(2 * time.Millisecond)
	}

	// The window: the first lift is parked, so `h.lift` is nil and the count says one lift exists.
	//
	// The second lift must be ADMITTED. It then parks on its own set and expires at the shortened bound,
	// because nothing resolves `tick` — so `ErrLiftParkExpired` is the *success* reading here: it proves
	// the lift ran far enough to park, which a refused entry never would.
	_, second := in.CallValues("run", canon.U32(2))
	if second == nil {
		t.Fatal("the second lift returned a result, which cannot happen with a `tick` that never " +
			"resolves — so it did not actually park, and this arm measured the wrong thing")
	}
	var trap *interp.Trap
	if errors.As(second, &trap) && strings.Contains(trap.Reason, "overlapping entry") {
		t.Fatalf("a second lift was REFUSED while the first was merely parked: %q.\n\nSeveral lift tasks "+
			"per instance is what #869 built; only an overlapping ENTRY may trap, and a parked task has "+
			"no live entry.", trap.Reason)
	}
	if !errors.Is(second, ErrLiftParkExpired) {
		t.Errorf("the second lift was admitted but did not end at its own park's bound: %v", second)
	}
	// Both lifts were in flight at once, which is the property. Checked from the engine's own count
	// rather than inferred from the two calls having overlapped in wall-clock time.
	if peak := atomic.LoadInt32(&peakInFlight); peak < 2 {
		t.Errorf("peak lifts in flight was %d, want 2 — the two lifts never coexisted, so this arm did "+
			"not witness concurrency even though both calls were admitted", peak)
	}

	// Drain the first call so the test does not leak a goroutine past its own end; it expires at the
	// shortened bound, which is the named outcome rather than a hang.
	if e := <-first; !errors.Is(e, ErrLiftParkExpired) {
		t.Errorf("the parked first lift ended as %v, want ErrLiftParkExpired", e)
	}
}

// TestAsyncLiftContextLivesOnTheTask is the dated-disposition correction (#785 / ADR 0050 / #739): with an
// async lift in flight (h.lift != nil), context.get/set use the durable TASK's storage, not the caller's
// stack — so context survives across the loop's re-entries. With no lift (the sync path), it stays on the
// stack, unchanged.
func TestAsyncLiftContextLivesOnTheTask(t *testing.T) {
	cc, err := interp.NewCanonCallerForTest(1)
	if err != nil {
		t.Fatalf("harness caller: %v", err)
	}
	h := newAsyncHandles()
	h.lift = &liftTask{}
	// Under a lift: set slot 0 to 0x5151, read it back from the TASK.
	if _, serr := contextSet(h, 0)(cc, []interp.Value{interp.I32(0x5151)}); serr != nil {
		t.Fatalf("context.set under lift: %v", serr)
	}
	if h.lift.storage[0] != 0x5151 {
		t.Errorf("context.set under a lift wrote %#x to the task, want 0x5151 (it went to the stack instead)", h.lift.storage[0])
	}
	got, err := contextGet(h, 0)(cc, nil)
	if err != nil {
		t.Fatalf("context.get under lift: %v", err)
	}
	if len(got) != 1 || got[0].Bits != 0x5151 {
		t.Errorf("context.get under a lift read %v, want [0x5151] from the task", got)
	}
	// The sync path (h.lift == nil, storage on the caller's stack) is unchanged and covered by the existing
	// context tests (TestContextStorageIsPerCallerStack); it is not re-exercised here, where the harness
	// caller has no stack.
}

// TestFutureCancelWritePinIsTheWriteArmRunningCancelled guards the future-cancellation differential pin
// (fixtures.json future_cancel_write), the oracle for the future write-side built-ins the WAT cancellation
// guest will drive (2nd async guest, #785). Produced by driving the model's real canon_future_cancel_write
// through the SAME cancel_copy substrate as the stream arm. ARM PRECISION (#752/#785): this is the future
// *write* arm's running CANCELLED — the write parks (no reader) then cancels inline to CANCELLED, the end
// left IDLE (open). The future *read* arm's CANCELLED stays synthetic (no running producer), so this pin
// does not make "future CANCELLED" true on the read side. Differential-first: the pin lands before the Go
// built-ins it oracles.
func TestFutureCancelWritePinIsTheWriteArmRunningCancelled(t *testing.T) {
	var doc struct {
		FutureCancelWrite struct {
			WriteRet     []int64 `json:"write_ret"`
			CancelRet    []int64 `json:"cancel_ret"`
			Result       int     `json:"result"`
			Progress     int     `json:"progress"`
			WiStateAfter string  `json:"wi_state_after"`
		} `json:"future_cancel_write"`
	}
	loadFixtures(t, &doc)
	f := doc.FutureCancelWrite
	if len(f.WriteRet) != 1 || uint32(f.WriteRet[0]) != 0xFFFFFFFF {
		t.Errorf("write_ret = %v, want [BLOCKED] (0xFFFFFFFF) — the write parks with no reader", f.WriteRet)
	}
	if f.Result != 2 {
		t.Errorf("result = %d, want 2 (CopyResult.CANCELLED) — the future WRITE arm's running production", f.Result)
	}
	if f.Progress != 0 {
		t.Errorf("progress = %d, want 0 — nothing copied before the cancel", f.Progress)
	}
	if f.WiStateAfter != "IDLE" {
		t.Errorf("wi_state_after = %q, want IDLE — CANCELLED leaves the end open (only DROPPED is DONE)", f.WiStateAfter)
	}
}

// TestAsyncFutureCancelWriteRunningProducer is the running CANCELLED producer (2nd async guest, #785, step 2
// of the execution): the WAT cancellation guest (async-future-cancel-synth.wasm) runs on Burroughs —
// future.new mints the end pair, future.write parks the write (no reader, BLOCKED), future.cancel-write
// resolves it to CANCELLED, task.return carries CANCELLED, EXIT. The lift loop is still first-call → EXIT
// (the future.write park is at the future-end level, not a lift WAIT). The THREE-WAY assertion (#765): the
// running production, the codec constant, and the synthetic pin all agree on CANCELLED, now with a real
// producer at both ends. Matches the committed wasmtime reading (run()→2).
func TestAsyncFutureCancelWriteRunningProducer(t *testing.T) {
	t.Setenv("BURROUGHS_ASYNC", "1")
	b, err := os.ReadFile("testdata/async-future-cancel-synth.wasm")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	in, err := InstantiateWithHost(b, NewHost(io.Discard, io.Discard, nil))
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()
	cd := in.export.exports["run"]
	resolved, err := cd.fn.invokeWith(nil) // the resolution is a return value now (#869)
	if err != nil {
		t.Fatalf("invoke run (the cancellation guest): %v", err)
	}
	// (1) the running production: run resolved to CANCELLED.
	if len(resolved) != 1 || resolved[0].Bits != uint64(copyCancelled) {
		t.Fatalf("run resolved to %v, want [%d] (CANCELLED) — the running future-write cancel producer", resolved, copyCancelled)
	}
	// (2) the codec constant, and (3) the synthetic pin — the three-way (#765) agreement.
	if copyCancelled != 2 {
		t.Errorf("codec CopyResult.CANCELLED = %d, want 2", copyCancelled)
	}
	var doc struct {
		FutureCancelWrite struct {
			Result int `json:"result"`
		} `json:"future_cancel_write"`
	}
	loadFixtures(t, &doc)
	if doc.FutureCancelWrite.Result != int(copyCancelled) {
		t.Errorf("synthetic pin result %d != codec constant %d — the three-way disagrees", doc.FutureCancelWrite.Result, copyCancelled)
	}
}

// TestFutureWriteDroppedStaysFixtureOnly is the explicit DROPPED check (#785, Scott's caution): future.write
// is now built, so DROPPED becomes REACHABLE — but whether THIS guest produces it is separate from whether
// the built-in exists. The cancellation guest produces CANCELLED, not DROPPED (nothing drops the paired read
// end while the write pends), so future.write's DROPPED stays category-3 (fixture-only), audited here rather
// than changed by implication. Its running-producer expiry updates to a guest that drops a future read end
// with a write in flight.
func TestFutureWriteDroppedStaysFixtureOnly(t *testing.T) {
	t.Setenv("BURROUGHS_ASYNC", "1")
	b, err := os.ReadFile("testdata/async-future-cancel-synth.wasm")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	in, err := InstantiateWithHost(b, NewHost(io.Discard, io.Discard, nil))
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()
	cd := in.export.exports["run"]
	resolved, err := cd.fn.invokeWith(nil) // the resolution is a return value now (#869)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	// The guest produced CANCELLED, explicitly NOT DROPPED — so DROPPED has no running producer here.
	if len(resolved) == 1 && resolved[0].Bits == uint64(copyDropped) {
		t.Fatalf("the cancellation guest produced DROPPED — unexpected; it should cancel, not drop")
	}
	if len(resolved) != 1 {
		t.Fatalf("run resolved to %d values, want 1", len(resolved))
	}
	if resolved[0].Bits != uint64(copyCancelled) {
		t.Fatalf("guest produced %v, want CANCELLED — DROPPED's running producer awaits a guest that drops", resolved)
	}
}

// TestP3AsyncCancelStillRefusedByName witnesses the boundary honestly (#785, #732): p3async-cancel — the Rust
// guest whose wit-bindgen surface DRAGS future built-ins its run() never calls — still refuses at instantiate
// by name, because the unexercised ones (future.cancel-read 0x18, future.drop-writable 0x1b, task.cancel
// 0x05) stay unbuilt. The refusal fires on its own bytes: the boundary is a named refusal, not a gap. It
// stays the standing end-to-end candidate (blocker = those unbuilt built-ins); when a later guest drives
// them, the Rust end-to-end lands.
func TestP3AsyncCancelStillRefusedByName(t *testing.T) {
	t.Setenv("BURROUGHS_ASYNC", "1")
	b, err := os.ReadFile("testdata/p3async-cancel.wasm")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	_, err = InstantiateWithHost(b, NewHost(io.Discard, io.Discard, nil))
	if err == nil {
		t.Fatal("p3async-cancel instantiated — its unexercised future built-ins should still refuse by name")
	}
	if !errors.Is(err, ErrAsyncNotImplemented) {
		t.Fatalf("refusal is not ErrAsyncNotImplemented (a named gate-on-unbuilt refusal): %v", err)
	}
}

// TestWaitableSetDropRunsOnRealBytes is the re-pointed 0x22 witness (#792's expiry, reached in #871).
//
// # What it used to assert, and why that is gone
//
// It asserted that executing `waitable-set.drop` **refused by name** — the #792 tightening, which deleted
// an impl that removed the table entry without the model's two traps. Its own doc comment named the
// expiry: *"a guest that drops a waitable set. Then 0x22 is rebuilt with the model's two traps, its oracle
// pin, and its firing witness."*
//
// That expiry arrived from an unexpected direction: not a new guest, but **this slice's park**. The
// committed suspending guest drops its set after resuming, which it could never reach while every WAIT
// refused. So the refusal is gone and the arm is re-pointed at the behaviour that replaced it.
//
// The instantiate half is kept verbatim in spirit: a guest that binds 0x22 must instantiate, which was
// true under the refusal and must stay true under the implementation.
func TestWaitableSetDropRunsOnRealBytes(t *testing.T) {
	t.Setenv("BURROUGHS_ASYNC", "1")
	b, err := os.ReadFile("testdata/async-waitset-drop-synth.wasm")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	in, err := InstantiateWithHost(b, NewHost(io.Discard, io.Discard, nil))
	if err != nil {
		t.Fatalf("a guest that binds waitable-set.drop must instantiate: %v", err)
	}
	defer in.Close()
	cd, ok := in.export.exports["run"]
	if !ok || cd.fn == nil {
		t.Fatal("no run export")
	}
	// The fixture creates a set and drops it — empty, no waiters, so both of the model's traps are
	// inapplicable and the drop must simply succeed.
	if err = cd.fn.invoke(); err != nil {
		t.Fatalf("a guest dropping an empty, unwaited waitable set must succeed: %v", err)
	}
}

// TestWaitableSetDropTrapsOnMembersAndOnWaiters pins the two traps #792 deleted an impl for lacking
// (`WaitableSet.drop`, def:794-796).
//
// # Three arms, and the third is the vacuity check
//
// The traps are asserted over the impl rather than over bytes because **no committed guest can construct
// either state**: a guest that drops a set it still has members in, or that another agent is parked on, is
// precisely the malformed case the traps exist for, and synthesizing one would be writing a guest to be
// wrong. The real-bytes path is covered by the arm above, so this is the helper-vs-path split made on
// purpose rather than by omission.
//
// The success arm is what keeps the other two honest: without it, an impl that trapped unconditionally
// would pass both trap arms.
func TestWaitableSetDropTrapsOnMembersAndOnWaiters(t *testing.T) {
	// newSet installs an empty set at a fresh handle and returns both.
	newSet := func() (*asyncHandles, uint32, *waitableSet) {
		h := newAsyncHandles()
		s := &waitableSet{wake: make(chan struct{})}
		h.mu.Lock()
		si := uint32(h.addLocked(s))
		h.mu.Unlock()
		return h, si, s
	}

	t.Run("an_empty_unwaited_set_drops", func(t *testing.T) {
		h, si, _ := newSet()
		if _, err := waitableSetDrop(h)(nil, []interp.Value{interp.I32(int32(si))}); err != nil {
			t.Fatalf("dropping an empty, unwaited set must succeed: %v", err)
		}
		// The handle is gone: a second drop must not find a set. This is what makes it a removal rather
		// than a no-op that happened to return nil.
		if _, err := waitableSetDrop(h)(nil, []interp.Value{interp.I32(int32(si))}); err == nil {
			t.Error("the handle survived its own drop — the entry was never removed")
		}
	})

	t.Run("a_set_with_a_joined_member_traps", func(t *testing.T) {
		h, si, s := newSet()
		st := &subtask{}
		h.mu.Lock()
		joinWaitableLocked(st, s)
		h.mu.Unlock()
		_, err := waitableSetDrop(h)(nil, []interp.Value{interp.I32(int32(si))})
		if err == nil {
			t.Fatal("dropping a set with a joined waitable did not trap (WaitableSet.drop traps on a " +
				"non-empty set)")
		}
		var trap *interp.Trap
		if !errors.As(err, &trap) {
			t.Fatalf("want a Trap, got %v", err)
		}
		if !strings.Contains(trap.Reason, "joined waitable") {
			t.Errorf("the trap does not say the set was non-empty: %q", trap.Reason)
		}
	})

	t.Run("a_set_with_a_parked_waiter_traps", func(t *testing.T) {
		h, si, s := newSet()
		h.mu.Lock()
		s.waiting++ // what both parks do for the duration of the park
		h.mu.Unlock()
		_, err := waitableSetDrop(h)(nil, []interp.Value{interp.I32(int32(si))})
		if err == nil {
			t.Fatal("dropping a set with a parked waiter did not trap (WaitableSet.drop traps on " +
				"num_waiting > 0)")
		}
		var trap *interp.Trap
		if !errors.As(err, &trap) {
			t.Fatalf("want a Trap, got %v", err)
		}
		if !strings.Contains(trap.Reason, "parked on it") {
			t.Errorf("the trap does not say the set had waiters: %q", trap.Reason)
		}
	})
}

// TestWaitableJoinLeavesThePreviousSet pins the step of `Waitable.join` this engine never had
// (def:737-743: `if self.wset: self.wset.elems.remove(self)`).
//
// Without it a re-joined waitable stayed a member of every set it had ever been in, so its resolution
// would deliver an event to waiters on a set it had supposedly left — and `waitable-set.drop` would trap
// on a stale membership the guest believed it had removed. Not needed by any witness in this slice; pinned
// because it is the same model line, and an unexercised half of a three-step operation is the kind of gap
// the #792 audit was about.
func TestWaitableJoinLeavesThePreviousSet(t *testing.T) {
	h := newAsyncHandles()
	a := &waitableSet{wake: make(chan struct{})}
	bSet := &waitableSet{wake: make(chan struct{})}
	st := &subtask{}

	h.mu.Lock()
	joinWaitableLocked(st, a)
	h.mu.Unlock()
	if len(a.members) != 1 {
		t.Fatalf("after joining set A it has %d members, want 1", len(a.members))
	}

	h.mu.Lock()
	joinWaitableLocked(st, bSet)
	h.mu.Unlock()
	if len(bSet.members) != 1 {
		t.Errorf("after re-joining to set B it has %d members, want 1", len(bSet.members))
	}
	if len(a.members) != 0 {
		t.Errorf("set A still has %d member(s) after the waitable left it — a resolution would wake A's "+
			"waiters about a waitable that is no longer in A", len(a.members))
	}

	// And the unjoin (si == 0 → join(nil)), which is the step the park actually drove.
	h.mu.Lock()
	joinWaitableLocked(st, nil)
	h.mu.Unlock()
	if len(bSet.members) != 0 {
		t.Errorf("after unjoining, set B still has %d member(s)", len(bSet.members))
	}
	if st.currentSet() != nil {
		t.Error("after unjoining, the waitable still points at a set")
	}
}
