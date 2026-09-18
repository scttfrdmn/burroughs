// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"io"
	"os"
	"testing"

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
	if err := cd.fn.invoke(); err != nil {
		t.Fatalf("invoke run (the async-lift loop skeleton): %v", err)
	}
	if len(cd.fn.result) != 1 || cd.fn.result[0].Bits != 42 {
		t.Errorf("run resolved to %v, want [42] via task.return (matching the wasmtime reading run()->42)", cd.fn.result)
	}
	// The current-task pointer is cleared after invoke (teardown keys on the task), so a second lift is admissible.
	if in.w.async.lift != nil {
		t.Error("the current lift task was not cleared after resolution")
	}
}

// TestAsyncLiftAtMostOneTaskPerAgentTraps witnesses the at-most-one-lift-task assertion firing through the
// real invoke path (invokeAsyncLift on the real fixture's compFunc): with a lift task already in flight on
// the agent, entering an async lift traps by name rather than resolving the wrong task. The nested state is
// induced here by pre-setting the current-task pointer; the ORGANIC byte-driven witness — a host call into
// an async-lifted export while another lift's loop is genuinely PARKED on the agent — lands with step 2,
// which is where that re-entrant window first exists (the EXIT-only step-1 path never parks, so there is no
// organic overlap to synthesize yet). #785, #732.
func TestAsyncLiftAtMostOneTaskPerAgentTraps(t *testing.T) {
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
	// A lift task already in flight on this agent (stands in for a re-entrant caller).
	in.w.async.lift = &liftTask{}
	err = cd.fn.invoke()
	if err == nil {
		t.Fatal("entering an async lift with one already in flight did not trap — the at-most-one assertion is silent")
	}
	var trap *interp.Trap
	if !errors.As(err, &trap) {
		t.Fatalf("want a Trap naming the in-flight lift, got %v", err)
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
