// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import "testing"

// TestFutureAndStreamChecksMatchThePin is #1364's witness for items 1, 2, 3 and 5 (item 4 is
// TestTypedFutureEndsMatchTheOracles'). Every row of future-stream-checks.reading and future-cancel-write-async.reading
// was read on wasmtime 49.0.3 (10 of 10) and on the pin 2e7895c (refread.py's future-stream-checks rows, 16 of 16)
// before the code, and the two agree on every row:
//
//   - **Item 1: a reader mid-copy does not drop** (def:drop:"trap_if(end.copying_or_cancelling())"): a read pending,
//     or one a write completed whose event is untaken. Burroughs dropped both.
//   - **Item 2: the sync future.cancel-write traps a joined writer** (def:cancel_copy:"trap_if(end.in_waitable_set()
//     and not async_)"), after the state check, so a joined writer with no write traps as having none. The async form
//     cancels a joined writer. Burroughs bound one form for both, unguarded.
//   - **Item 3: the sync stream cancels check the state before the join**, as def:cancel_copy orders them. Burroughs's
//     guard trapped a joined end with no copy as joined.
//   - **Item 5: a future.read on a reader not IDLE traps** (def:copy:"trap_if(end.state != End.State.IDLE)").
//     Burroughs returned a Go error.
//
// Both gate:cm-threads states, gate:async on. Each trap row's needle is the check the pin's traceback names.
//
// **Neuters, run:** each named at its site.
func TestFutureAndStreamChecksMatchThePin(t *testing.T) {
	runKindRows(t, "future-stream-checks", 11, map[string]string{
		"drop-readable-read-pending":      "future.drop-readable: end 1 is mid-copy",
		"drop-readable-event-untaken":     "future.drop-readable: end 1 is mid-copy",
		"cancel-write-sync-joined":        "future.cancel-write: end 2 is joined to a waitable set",
		"cancel-write-idle-joined":        "future.cancel-write: end 2 has no write in flight",
		"stream-cancel-read-idle-joined":  "stream.cancel-read: end 1 has no copy in flight",
		"stream-cancel-write-idle-joined": "stream.cancel-write: end 2 has no write in flight",
		"read-after-read-pending":         "future.read: end 1 is not IDLE",
		"read-after-read-event-untaken":   "future.read: end 1 is not IDLE",
		"read-after-read-done":            "future.read: end 1 is not IDLE",
	})
	runKindRows(t, "future-cancel-write-async", 2, nil)
}
