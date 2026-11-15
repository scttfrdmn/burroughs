// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// TestADroppedWaitableLeavesItsSet is grave #1296's witness: a dropped readable or writable stream end, a dropped
// readable future end and a dropped subtask each leave their waitable set, as def:Waitable.drop's `self.join(None)`
// does, so the set can then be dropped. Every row of waitable-drop.reading is wasmtime 49.0.3's, 10 of 10. The
// `still-joined` row drops a set whose member is still joined and traps on both engines, which shows the set's own
// check is live and the four rows above it pass because the member left, not because nothing is checked.
//
// Neutered, each run: the `joinWaitableLocked(…, nil)` line removed from streamDropReadable, streamDropWritable,
// futureDrop or subtaskDrop, and that one row traps "still has 1 joined waitable(s)" at the set's drop, while the
// other three still pass. Found by the fork's F3a runtime, which joins its stdio ends to one set and drops that set
// after flushing at exit (#1238).
func TestADroppedWaitableLeavesItsSet(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	b, err := os.ReadFile("testdata/waitable-drop.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	raw, err := os.ReadFile("testdata/waitable-drop.reading")
	if err != nil {
		t.Fatalf("the committed reading is missing, so this witness has no oracle: %v", err)
	}
	rows := 0
	for _, ln := range strings.Split(string(raw), "\n") {
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		export, reading, ok := strings.Cut(ln, ": ")
		if !ok {
			t.Fatalf("waitable-drop.reading: row %q has no `: `", ln)
		}
		rows++
		t.Run(export, func(t *testing.T) {
			res, err := runWaitableDropRow(t, b, export)
			if strings.HasPrefix(reading, "trap") {
				var trap *interp.Trap
				if !errors.As(err, &trap) || !strings.Contains(err.Error(), "joined waitable") {
					t.Fatalf("%s() = %v, %v; want the set's still-joined trap (the oracle: %s)", export, res, err, reading)
				}
				return
			}
			want, perr := strconv.ParseUint(reading, 10, 32)
			if perr != nil {
				t.Fatalf("%s: reading %q is neither a trap nor a u32", export, reading)
			}
			if err != nil {
				t.Fatalf("%s(): %v; the oracle reads %d", export, err, want)
			}
			if v, ok := res[0].U32(); !ok || uint64(v) != want {
				t.Fatalf("%s() = %v; the oracle reads %d", export, res, want)
			}
		})
	}
	if rows != 5 {
		t.Fatalf("waitable-drop.reading has %d rows, want 5", rows)
	}
}

// runWaitableDropRow calls one export on a fresh instance with the real host, which serves the `subtask` row's
// wait-for (ADR 0121), with a 10s harness hang guard.
func runWaitableDropRow(t *testing.T, b []byte, export string) ([]canon.Value, error) {
	t.Helper()
	in, err := InstantiateWithHost(b, NewHost(nil, nil, nil))
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	t.Cleanup(in.Close)
	type out struct {
		res []canon.Value
		err error
	}
	ch := make(chan out, 1)
	go func() { r, e := in.CallValues(export); ch <- out{r, e} }()
	select {
	case o := <-ch:
		return o.res, o.err
	case <-time.After(10 * time.Second): // a harness hang guard, not a measurement
		t.Fatalf("%s() did not return within 10s", export)
		return nil, nil
	}
}
