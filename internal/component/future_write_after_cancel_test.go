// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import "testing"

// TestAFutureWriteAfterACancelledOneMatchesTheOracles is #1353's witness: a future.write after a future.cancel-write
// on the same end. Every row of future-write-after-cancel.reading was read on wasmtime 49.0.3 (10 of 10) and on the
// pin 2e7895c (refread.py's future-write-after-cancel rows, 16 of 16) before the code, and the two agree on every row.
// A cancelled write leaves its writable end IDLE (def:FutureEnd.notify.future_event+'s CANCELLING_COPY arm), so a
// second write is admitted: it parks with no reader, a read then completes it, and its event is FUTURE_WRITE
// COMPLETED. Burroughs trapped every second write "not writable". The IDLE writer still may not drop, not being DONE.
//
// Both gate:cm-threads states, gate:async on, through the public Instantiate and CallValues.
//
// **Neuters, run:** named at their site in futureCancelWrite.
func TestAFutureWriteAfterACancelledOneMatchesTheOracles(t *testing.T) {
	runKindRows(t, "future-write-after-cancel", 6, map[string]string{
		"drop-writable-after-cancel-write": "future.drop-writable: end 2 has not written its value",
	})
}
