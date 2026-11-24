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

// futureSyncWaitFrame is the frame a sync future copy's wait parks in (awaitFutureCopy), named bare, as parkFrames'
// are, since the compiler chooses whether to inline it.
const futureSyncWaitFrame = "awaitFutureCopy"

// futureSyncThreadRows are future-sync.reading's rows that cmthreads/sync-future-own-thread.wasm exports: each needs a
// Thread, so each runs with gate:cm-threads on only, as every cmthreads fixture does.
var futureSyncThreadRows = map[string]bool{
	"sync-read-own-thread":          true,
	"sync-write-own-thread":         true,
	"sync-read-own-thread-control":  true,
	"sync-read-cancelled-by-thread": true,
	"sync-read-joined-by-thread":    true,
}

// futureSyncNeedles names, for each trap row of future-sync.reading, the phrase Burroughs's trap carries.
var futureSyncNeedles = map[string]string{
	"sync-write-joined":               "future.write: end 2 is joined to a waitable set",
	"sync-read-joined":                "future.read: end 1 is joined to a waitable set",
	"sync-write-no-reader-sync-typed": "trap_if(not candidates)",
	"sync-read-own-thread-control":    "trap_if(not candidates)",
	"sync-read-cancelled-by-thread":   "future.cancel-read: end 1 has a sync copy waiting on it",
	"sync-read-joined-by-thread":      "waitable.join: end 1 has a sync copy waiting on it",
}

// TestASyncFutureCopyBlocksAsThePinDoes is #1352's witness: future.read and future.write in their SYNC form, which the
// decoder bound as the async form, so a sync copy that did not complete inline returned BLOCKED to a caller that
// cannot take it. Every row of future-sync.reading was read on wasmtime 49.0.3 (10 of 10, the hang rows 3 of 3) and on
// the pin 2e7895c (refread.py's future-sync and sync-future-own-thread rows, 16 of 16) before the code, and the two
// agree on every row:
//
//   - **A sync copy that finds the other end's copy parked completes inline**, and returns its payload.
//   - **One that does not waits for its own event** (def:copy:"end.wait_for_pending_event()"). With nothing to wake it,
//     an async-typed export hangs on both oracles, and this asserts the place: the call is parked in the copy's wait,
//     and Close then ends it. A sync-typed export traps at the sync tail. A Thread of the instance that makes the
//     other copy wakes it.
//   - **A sync copy on a joined end traps**, and so does another Thread's cancel or join of an end whose sync copy
//     is waiting (def:cancel_copy's and def:canon_waitable_join's `has_sync_waiter`).
//
// future-sync.wasm's rows run in both gate:cm-threads states; the thread rows with the gate on, as cmthreads fixtures
// do. gate:async on.
//
// **Neuters, run:** named at their sites.
func TestASyncFutureCopyBlocksAsThePinDoes(t *testing.T) {
	order, want := typedStreamRows(t, "future-sync")
	if len(order) != 13 {
		t.Fatalf("future-sync.reading has %d rows, want 13", len(order))
	}
	b, err := os.ReadFile("testdata/future-sync.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	for _, export := range order {
		reading := want[export]
		if futureSyncThreadRows[export] {
			t.Run(export+"/gate=1", func(t *testing.T) {
				_, res, err := callBounded(t, "sync-future-own-thread", export)
				checkFutureSyncRow(t, export, reading, res, err)
			})
			continue
		}
		for _, gate := range []string{"0", "1"} {
			t.Run(export+"/gate="+gate, func(t *testing.T) {
				t.Setenv(asyncGateEnv, "1")
				t.Setenv(cmThreadsGateEnv, gate)
				if reading == "hang" {
					requireParkedInFutureCopy(t, b, export)
					return
				}
				res, err := runTypedStreamRow(t, b, export)
				checkFutureSyncRow(t, export, reading, res, err)
			})
		}
	}
}

// checkFutureSyncRow holds one row's result to its reading: a trap with the row's needle, or the u32.
func checkFutureSyncRow(t *testing.T, export, reading string, res []canon.Value, err error) {
	t.Helper()
	if strings.HasPrefix(reading, "trap") {
		needle, ok := futureSyncNeedles[export]
		if !ok {
			t.Fatalf("%s is a trap row with no needle", export)
		}
		var trap *interp.Trap
		if !errors.As(err, &trap) || !strings.Contains(err.Error(), needle) {
			t.Fatalf("%s() = %v, %v; want a trap containing %q (the oracle: %s)", export, res, err, needle, reading)
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
}

// requireParkedInFutureCopy is a hang row: the call must park in the sync copy's wait and stay parked, as both
// oracles hang, and Close must then end it. The place is asserted, not a duration: the call is seen parked in
// awaitFutureCopy's select, has not returned when it is, and returns once Close ends it.
func requireParkedInFutureCopy(t *testing.T, b []byte, export string) {
	t.Helper()
	in, err := Instantiate(b)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	type out struct {
		res []canon.Value
		err error
	}
	done := make(chan out, 1)
	go func() { r, e := in.CallValues(export); done <- out{r, e} }()
	if !awaitParked(futureSyncWaitFrame, 1, wakeProbeBound) {
		select {
		case o := <-done:
			in.Close()
			t.Fatalf("%s() = %v, %v without parking in %s; both oracles hang", export, o.res, o.err, futureSyncWaitFrame)
		default:
		}
		in.Close()
		t.Fatalf("%s() did not park in %s within %v: %s", export, futureSyncWaitFrame, wakeProbeBound,
			agentsPlace(goroutineStacks()))
	}
	select {
	case o := <-done:
		in.Close()
		t.Fatalf("%s() returned %v, %v while parked; both oracles hang", export, o.res, o.err)
	default:
	}
	closed := make(chan struct{})
	go func() { in.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(closeEndsBound):
		t.Fatalf("Close did not return within %v: %s", closeEndsBound, agentsPlace(goroutineStacks()))
	}
	select {
	case o := <-done:
		if o.err == nil {
			t.Fatalf("%s() = %v after Close; want the error Close ends a parked call with", export, o.res)
		}
	case <-time.After(closeEndsBound):
		t.Fatalf("%s() did not return after Close: %s", export, agentsPlace(goroutineStacks()))
	}
}
