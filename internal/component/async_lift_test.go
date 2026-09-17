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
