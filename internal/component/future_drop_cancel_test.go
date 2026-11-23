// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import "testing"

// TestFutureDropWritableAndCancelReadMatchTheOracles is #1362's witness: future.drop-writable (0x1b) and
// future.cancel-read (0x18), which Burroughs did not bind, so a component importing either was refused at
// instantiate by gateAsync ("an async canon built-in (0x18)"). Every row of future-drop-cancel.reading and
// future-cancel-read-async.reading was read on wasmtime 49.0.3 (10 of 10) and on the pin 2e7895c (refread.py's
// future_drop_cancel rows, 16 of 16) before the code, and the two oracles agree on every row:
//
//   - **A writable future end drops only when DONE** (def:drop:"trap_if(isinstance(end, WritableFutureEnd) and
//     end.state !="), so a writer that never wrote traps, with or without a read parked, and a parked reader is
//     never handed a DROPPED by its writer's drop. A writer whose write is pending, or whose completed write's event
//     is untaken, is COPYING, and traps first (def:drop:"trap_if(end.copying_or_cancelling())").
//   - **A writer whose reader dropped first is notified DROPPED** (def:End.drop), so its write takes the DROPPED
//     inline and leaves it DONE, and its drop then succeeds.
//   - **A cancel of a parked read returns CANCELLED inline and leaves the reader IDLE**, so the next write parks and
//     the read after it completes; a cancel of a read a write completed takes the COMPLETED. A reader not COPYING
//     traps (def:cancel_copy:"trap_if(end.state != End.State.COPYING)"), as does a sync cancel of a joined reader.
//
// Both gate:cm-threads states, gate:async on, through the public Instantiate and CallValues. The fixtures' memory is
// 1 1, so the copies are heap-backed for -race.
//
// **Neuters, run:** each named at its site in async_future.go, with the rows it fails.
func TestFutureDropWritableAndCancelReadMatchTheOracles(t *testing.T) {
	runKindRows(t, "future-drop-cancel", 16, map[string]string{
		"drop-writable-event-untaken":      "future.drop-writable: end 2 is mid-copy",
		"drop-writable-write-pending":      "future.drop-writable: end 2 is mid-copy",
		"drop-writable-idle":               "future.drop-writable: end 2 has not written its value",
		"drop-writable-idle-reader-parked": "future.drop-writable: end 2 has not written its value",
		"cancel-read-idle":                 "future.cancel-read: end 1 has no read in flight",
		"cancel-read-after-event-taken":    "future.cancel-read: end 1 has no read in flight",
		"cancel-read-sync-joined":          "future.cancel-read: end 1 is joined to a waitable set",
	})
	runKindRows(t, "future-cancel-read-async", 2, nil)
}
