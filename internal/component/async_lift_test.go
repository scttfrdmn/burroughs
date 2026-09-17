// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
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
