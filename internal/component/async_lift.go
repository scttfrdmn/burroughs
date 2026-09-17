// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"fmt"

	"github.com/scttfrdmn/burroughs/internal/interp"
)

// The stackless (callback) async-lift ABI — the second async guest's tier (ADR 0086's deferred
// stackless-vs-stackful choice, settled guest-driven: the Rust p3 toolchain emits
// `(canon lift ... async (callback ...))`, so that is what the engine implements; stackful stays deferred
// to a guest that stackfully lifts one). This file starts with the piece the whole dispatch turns on: the
// decode of the packed i32 the callback returns.

// callbackCode is the async-lift callback's dispatch code — the low nibble of the packed i32 the callback
// returns (definitions.py `CallbackCode`, def:2165-2169). EXIT means the task resolved; YIELD is a
// cooperative yield with no set; WAIT means the task is blocked on the waitable set whose index is packed
// in the high bits. The code is exactly what distinguishes "done" from "waiting on set si" from "yield" —
// so it is decoded and range-checked, never masked-and-assumed.
type callbackCode uint32

const (
	callbackExit    callbackCode = 0 // task resolved (the guest's `run` happy path)
	callbackYield   callbackCode = 1 // cooperative yield
	callbackWait    callbackCode = 2 // blocked on waitable set `si`
	callbackCodeMax callbackCode = callbackWait
)

// unpackCallbackResult decodes the callback's packed i32 return into its dispatch code and waitable-set
// index (definitions.py `unpack_callback_result`, def:2171-2177): `code = packed & 0xf`, and the
// waitable-set index is `packed >> 4`. A code above MAX traps — the guard a dispatch that masks instead of
// range-checks would skip, and the reason the pin covers out-of-range codes and not only the happy path.
// Pinned byte-exact against the model: fixtures.json `callback_result`.
func unpackCallbackResult(packed uint32) (callbackCode, uint32, error) {
	code := callbackCode(packed & 0xf)
	if code > callbackCodeMax {
		return 0, 0, &interp.Trap{Reason: fmt.Sprintf(
			"async-lift callback returned code %d, above max %d (EXIT/YIELD/WAIT)", uint32(code), uint32(callbackCodeMax))}
	}
	return code, packed >> 4, nil
}
