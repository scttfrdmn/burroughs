// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/interp"
)

// TestACancelTakesACompletedCopysEvent is #1282's item 2: a stream.cancel-write or stream.cancel-read on a copy that
// completed before its event was taken. Every row of stream-cancel-pending.reading is wasmtime 49.0.3's (10 of 10),
// and the pin's End state machine reads the same on each. The end is COPYING until its event is taken, so the cancel
// does not trap (def:cancel_copy:"trap_if(end.state != End.State.COPYING)"); it takes the pending event, as CANCELLED
// with the copy's progress (34), or DROPPED (33) when the other end has gone. A poll of the cancelled end afterwards
// sees no second event, and its control without the cancel sees the first. Both gate states.
//
// **Neuters, run** (each named at its site):
//   - streamCancelWrite trapping on `e.resolved` again: cancel-write-after-copy, cancel-write-after-copy-reader-dropped,
//     poll-after-cancel-write and write-after-cancel-write trap "no write in flight";
//   - takeCancelledCopyLocked leaving the result COMPLETED: cancel-write-after-copy and cancel-read-after-copy read 32
//     where the oracle reads 34, and the two dropped rows still pass.
func TestACancelTakesACompletedCopysEvent(t *testing.T) {
	runKindRows(t, "stream-cancel-pending", 12, map[string]string{"cancel-write-idle": "no write in flight"})
}

// wrongKindNeedles names, for each row of wrong-kind-traps.reading, the phrase Burroughs' trap carries: the built-in
// and the kind it takes, so each row is held to its own site rather than to any trap.
var wrongKindNeedles = map[string]string{
	"stream-read-writable":          "stream.read: handle 2 is not a readable stream end",
	"stream-write-readable":         "stream.write: handle 1 is not a writable stream end",
	"stream-cancel-read-writable":   "stream.cancel-read: handle 2 is not a readable stream end",
	"stream-cancel-write-readable":  "stream.cancel-write: handle 1 is not a writable stream end",
	"stream-drop-readable-writable": "stream.drop-readable: handle 2 is not a readable stream end",
	"stream-drop-writable-readable": "stream.drop-writable: handle 1 is not a writable stream end",
	"stream-write-no-handle":        "stream.write: handle 99 is not a writable stream end",
	"future-read-writable":          "future.read: handle 2 is not a readable future end",
	"future-read-stream":            "future.read: handle 1 is not a readable future end",
	"future-write-readable":         "future.write: handle 1 is not a writable future end",
	"future-cancel-write-readable":  "future.cancel-write: handle 1 is not a writable future end",
	"future-drop-readable-writable": "future.drop-readable: handle 2 is not a readable future end",
	"future-cancel-read-writable":   "future.cancel-read: handle 2 is not a readable future end",
	"future-drop-writable-readable": "future.drop-writable: handle 1 is not a writable future end",
	"join-set-as-waitable":          "waitable.join: handle 1 is not a waitable",
	"join-end-as-set":               "waitable.join: handle 2 is not a waitable set",
	"wait-end":                      "waitable-set.wait: handle 1 is not a waitable set",
	"poll-end":                      "waitable-set.poll: handle 1 is not a waitable set",
	"set-drop-end":                  "waitable-set.drop: handle 1 is not a waitable set",
	"subtask-cancel-end":            "subtask.cancel: handle 1 is not a subtask",
	"subtask-drop-end":              "subtask.drop: handle 1 is not a subtask",
}

// TestAWrongKindHandleTraps is #1282's item 3: every stream, future, waitable-set, waitable and subtask built-in
// Burroughs builds, given a handle of another kind, traps, as the pin's `trap_if(not isinstance(...))` does at each
// (def:copy, def:cancel_copy, def:drop, def:canon_waitable_join, def:canon_waitable_set_wait,
// def:canon_waitable_set_poll, def:canon_waitable_set_drop, def:canon_subtask_cancel, def:canon_subtask_drop), and an
// index with no entry traps as def:Table.get does. Every row of wrong-kind-traps.reading is wasmtime 49.0.3's, 10 of
// 10, a trap. They were Go errors. Both gate states. future.cancel-read and future.drop-writable joined the registry
// when #1362 bound them, so it covers def:cancel_copy and def:drop on both future ends.
//
// **Neuters, run:** any one site's wrongKindTrap replaced by the Go error it was, and that site's rows fail on the
// error not being an *interp.Trap, while the others pass. Each site names its rows.
func TestAWrongKindHandleTraps(t *testing.T) {
	runKindRows(t, "wrong-kind-traps", 21, wrongKindNeedles)
}

// runKindRows runs every row of testdata/<fixture>.reading in both gate states: a trap row must trap with its needle,
// and a u32 row must return it.
func runKindRows(t *testing.T, fixture string, wantRows int, needles map[string]string) {
	t.Helper()
	b, err := os.ReadFile("testdata/" + fixture + ".wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	order, want := typedStreamRows(t, fixture)
	if len(order) != wantRows {
		t.Fatalf("%s.reading has %d rows, want %d", fixture, len(order), wantRows)
	}
	for _, gate := range []string{"0", "1"} {
		for _, export := range order {
			reading := want[export]
			t.Run(export+"/gate="+gate, func(t *testing.T) {
				t.Setenv(asyncGateEnv, "1")
				t.Setenv(cmThreadsGateEnv, gate)
				res, err := runTypedStreamRow(t, b, export)
				if strings.HasPrefix(reading, "trap") {
					needle, ok := needles[export]
					if !ok {
						t.Fatalf("%s is a trap row with no needle", export)
					}
					var trap *interp.Trap
					if !errors.As(err, &trap) || !strings.Contains(err.Error(), needle) {
						t.Fatalf("%s() = %v, %v; want a trap containing %q (the oracle: %s)", export, res, err, needle,
							reading)
					}
					return
				}
				wantV, perr := strconv.ParseUint(reading, 10, 32)
				if perr != nil {
					t.Fatalf("%s: reading %q is neither a trap nor a u32", export, reading)
				}
				if err != nil {
					t.Fatalf("%s(): %v; the oracle reads %d", export, err, wantV)
				}
				if v, ok := res[0].U32(); !ok || uint64(v) != wantV {
					t.Fatalf("%s() = %v; the oracle reads %d", export, res, wantV)
				}
			})
		}
	}
}
