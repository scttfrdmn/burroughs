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

// TestASyncCancelOfAJoinedEndTraps is grave #1301's witness: a sync stream.cancel-read or stream.cancel-write on an
// end joined to a waitable set traps, as def:cancel_copy's `trap_if(end.in_waitable_set() and not async_)` does.
// Every row of cancel-joined.reading is wasmtime 49.0.3's, 10 of 10: the joined rows trap "waitable cannot be used
// synchronously while added to a waitable set", and the unjoined rows, the control, cancel and return 2. Found by
// the fork's F5 slice (#1240), whose netpoller unjoins before it cancels.
//
// Neutered, run: syncCancelGuard returning fn unwrapped, and both joined rows return 2 where the oracle traps, while
// the unjoined rows still pass.
func TestASyncCancelOfAJoinedEndTraps(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	b, err := os.ReadFile("testdata/cancel-joined.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	raw, err := os.ReadFile("testdata/cancel-joined.reading")
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
			t.Fatalf("cancel-joined.reading: row %q has no `: `", ln)
		}
		rows++
		t.Run(export, func(t *testing.T) {
			res, err := runWaitableDropRow(t, b, export)
			if strings.HasPrefix(reading, "trap") {
				var trap *interp.Trap
				if !errors.As(err, &trap) || !strings.Contains(err.Error(), "joined to a waitable set") {
					t.Fatalf("%s() = %v, %v; want the joined-end trap (the oracle: %s)", export, res, err, reading)
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
	if rows != 4 {
		t.Fatalf("cancel-joined.reading has %d rows, want 4", rows)
	}
}
