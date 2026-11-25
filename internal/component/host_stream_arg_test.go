// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"io"
	"testing"
)

// TestAHostStreamArgumentLiftsAsThePinDoes is #1367's witness: wasi:cli/stdout's write-via-stream, a host import whose
// parameter is a stream<u8>, given a handle it may not lift. Every row of host-stream-arg-kind.reading was read on
// wasmtime 49.0.3 (10 of 10) and on the pin 2e7895c (refread.py's host-stream-arg-kind rows, 16 of 16) before the
// code, and the two agree on every row: def:lift_async_value traps a handle that is not a readable end (a writable
// stream end, a future end, an index with no entry), a stream of another element, an end joined to a set, and an end
// not IDLE. The control, a fresh readable stream<u8> end, is taken. Burroughs returned ErrLinkRefused for a wrong
// kind, where the pin traps.
//
// Both gate:cm-threads states, gate:async on.
//
// **Neuters, run:** named at liftStreamArgLocked.
func TestAHostStreamArgumentLiftsAsThePinDoes(t *testing.T) {
	host := func() *Host { return NewHost(io.Discard, io.Discard, nil) }
	runKindRowsWith(t, "host-stream-arg-kind", 7, map[string]string{
		"wvs-writable-end": "write-via-stream: handle 2 is not a readable stream end",
		"wvs-future-end":   "write-via-stream: handle 1 is not a readable stream end",
		"wvs-no-handle":    "write-via-stream: handle 99 is not a readable stream end",
		"wvs-u32-stream":   "write-via-stream: end 1 is a stream of a different type",
		"wvs-joined-end":   "write-via-stream: end 1 is joined to a waitable set",
		"wvs-reading-end":  "write-via-stream: end 1 is not IDLE",
	}, host)
}
