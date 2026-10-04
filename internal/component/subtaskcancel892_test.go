// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"io"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// TestBothCancellingParentsSurviveALaterResolvingHost is grave #892's end-to-end witness, on the two
// composed artefacts that crashed.
//
// # What it looked like before the repair
//
// Measured during #888, driving these same artefacts with a cancel handler that did not resolve
// synchronously:
//
//	cancel-wat-parent   report kind 1 = 1 (STARTED), kind 2 = 4294967295 (BLOCKED),
//	                    then trap: "subtask.drop: subtask 1 is not resolve-delivered"
//	cancel-rust-parent  trap: unreachable
//
// Both are this defect. `subtask.cancel` returned BLOCKED where the model **waits** on the sync-lowered
// form (`definitions.py` def:2426-2433), and `wit-bindgen`'s drop glue is synchronous — it runs in `Drop`,
// which cannot await — so it meets BLOCKED in `in_progress_update`'s
// `other => panic!("unknown code {other:#x}")`, which is `unreachable` in wasm.
//
// # Why the host resolves LATER, and why that is the ordinary case
//
// A cancel handler that resolves *inside* `onCancel` sidesteps the defect entirely, which is why #887's
// witness did not trip it and why this survived. A real host dropping a pending call does work before it
// can honestly say the call is gone. **And a guest child is worse than a Go host**: its cancellation
// unwinds through the guest — dropping the async body, running destructors, calling `task.cancel` — so it
// is never instantaneous. That is why #888's recon predicted this path would meet this defect first, and
// why the unit arms in `async_lower_test.go` use the same later-resolving shape.
//
// # The WAT parent reproduces #862's committed reading, on the real path
//
// `cancel-wat.reading` records `STATUS 4` (CANCELLED_BEFORE_RETURNED) from wasmtime. The parent reports
// the same 4 here — read out of the committed file rather than retyped, so a reading that moves upstream
// cannot leave this asserting a stale expectation it also claims to check.
func TestBothCancellingParentsSurviveALaterResolvingHost(t *testing.T) {
	want := readCancelReading(t, "testdata/asynclift/cancel-wat.reading")

	for _, tc := range []struct {
		name       string
		path       string
		wantStatus bool // whether this parent has a report channel for the status
	}{
		{"wat_parent", "testdata/asynclift/cancel-wat-parent/composed.wasm", true},
		// The Rust parent cannot report the status — four facts in wit-bindgen-rt 0.44.0 put it out of a
		// Rust guest's reach (see CANCELLATION.md). What it witnesses here is that it does not CRASH,
		// which is the whole of this grave on that side, and it is the arm that showed `unreachable`.
		{"rust_parent", "testdata/asynclift/cancel-rust-parent/composed.wasm", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(asyncGateEnv, "1")
			b, err := os.ReadFile(tc.path)
			if err != nil {
				t.Fatalf("the committed composed artefact is missing: %v", err)
			}
			r := &crossCallReports{}
			var receipts int32
			h := NewHost(io.Discard, io.Discard, nil)
			h.asyncImpls = map[string]asyncLowerImpl{
				"tick": func(_ *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
					onStart()
					return func() {
						// The discriminating shape: resolve after returning, from another goroutine.
						go func() {
							time.Sleep(5 * time.Millisecond)
							onResolve(canon.U32(0))
						}()
					}, nil
				},
			}
			h.syncImpls = map[string]interp.CanonFunc{
				"note": func(_ *interp.CanonCaller, _ []interp.Value) ([]interp.Value, error) {
					atomic.AddInt32(&receipts, 1)
					return nil, nil
				},
				"report": func(_ *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
					if len(args) >= 2 {
						r.report(uint32(args[0].Bits), uint32(args[1].Bits))
					}
					return nil, nil
				},
			}
			in, err := InstantiateWithHost(b, h)
			if err != nil {
				t.Fatalf("instantiate: %v", err)
			}
			defer in.Close()

			_, cErr := in.CallValues("go")
			reports, _ := r.snapshot()
			t.Logf("reports=%v receipts=%d", reports, atomic.LoadInt32(&receipts))

			if cErr != nil {
				t.Fatalf("go() trapped (%v). Before the repair this was `unreachable` (Rust) or a "+
					"subtask.drop trap after BLOCKED (WAT) — a conforming guest crashing because "+
					"subtask.cancel answered with the ASYNC form's BLOCKED on a SYNC call", cErr)
			}

			// The receipt is the guest's cancellation path having run at all. Its absence would mean the
			// guest never got far enough to be cancelled, which would make the status below meaningless
			// — a green on a test that exercised nothing.
			if atomic.LoadInt32(&receipts) == 0 {
				t.Error("no receipt arrived, so the child's cancellation path did not run and this " +
					"reading has no subject")
			}

			if !tc.wantStatus {
				return
			}
			status, ok := r.valueFor(2)
			if !ok {
				t.Fatal("the WAT parent reported no cancel status (kind 2)")
			}
			if status == uint32(asyncBlocked) {
				t.Fatalf("subtask.cancel returned BLOCKED (%#x) on the sync form — grave #892 returning. "+
					"The model waits here; only the async form may answer BLOCKED", status)
			}
			if status != want.status {
				t.Errorf("cancel status = %d, want %d from the committed cancel-wat.reading", status, want.status)
			}
		})
	}
}
