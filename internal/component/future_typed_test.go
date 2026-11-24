// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import "testing"

// futureElemAt is the element type of the future end at index fi, for a test that calls a future built-in directly on
// a host-minted future: the type the guest's built-in would name, which the link checked is the host's payload type
// (checkConsumerPayload).
func futureElemAt(h *asyncHandles, fi int32) *elemType {
	h.mu.Lock()
	defer h.mu.Unlock()
	if fe, ok := handleAt[*readableFutureEnd](h, uint32(fi)); ok {
		return fe.elem
	}
	return nil
}

// TestTypedFutureEndsMatchTheOracles is #1282 item 1's witness, and #1364 item 4's: a future end carries its element
// type, and each of the six future built-ins traps an end of another type (def:copy, def:cancel_copy and def:drop:
// `trap_if(end.t != stream_or_future_t.t)`), as ADR 0124 made the stream built-ins do. Every row of
// future-typed-traps.reading was read on wasmtime 49.0.3 (10 of 10) and on the pin 2e7895c (refread.py's
// future-typed-traps rows, 16 of 16) before the code, and the two agree on every row. A second type index naming the
// same future<u8> is the same type, as the pin's structural ValType equality reads it. Burroughs's future ends carried
// no type, so every mismatched built-in went ahead.
//
// Both gate:cm-threads states, gate:async on.
//
// **Neuters, run:** named at checkFutureEndType and at the sites it is called from.
func TestTypedFutureEndsMatchTheOracles(t *testing.T) {
	const needle = "of a different type"
	runKindRows(t, "future-typed-traps", 11, map[string]string{
		"nested-mismatch-read":   "future.read: end 1 is a future " + needle,
		"mismatch-read":          "future.read: end 1 is a future " + needle,
		"mismatch-write":         "future.write: end 2 is a future " + needle,
		"mismatch-cancel-read":   "future.cancel-read: end 1 is a future " + needle,
		"mismatch-cancel-write":  "future.cancel-write: end 2 is a future " + needle,
		"mismatch-drop-readable": "future.drop-readable: end 1 is a future " + needle,
		"mismatch-drop-writable": "future.drop-writable: end 2 is a future " + needle,
		"mismatch-new32-read8":   "future.read: end 1 is a future " + needle,
	})
}
