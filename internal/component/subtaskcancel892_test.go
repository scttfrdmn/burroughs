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

// TestStopCompletesWhileASyncSubtaskCancelIsWaiting is §5 **H-1** for the sync `subtask.cancel` wait: a
// stop-the-world request issued while that wait is in progress must complete **without waiting for the
// cancellation to resolve**.
//
// # The defect this exists for
//
// The wait landed as a bare `select` in a canon function whose caller parameter was `_`. A canon function
// runs with the calling agent **inside guest execution**, so for up to `subtaskCancelBound` (30s) that
// agent was running host code *without being marked blocked*: it reaches no safepoint and is not excused
// as one. Phase 4 clause 3 drives garbage collection's stop-the-world through cooperative safepoints, so
// a GC beginning during a cancellation would stall for the whole wait.
//
// `waitableSetWait` already did this correctly, inside `c.Blocking(...)`. The ignored parameter is what
// made the omission invisible — there was nothing to misuse, so nothing looked wrong.
//
// # Why this assertion is timing-free in the way that matters
//
// It does **not** compare two measured durations. `Stop(deadline)` returning `nil` *means* it completed
// within its deadline, and the deadline here (`stwDeadline`) is far shorter than the resolution the
// cancel is waiting on (`lateResolve`). So the claim is carried by `Stop`'s own contract rather than by a
// stopwatch — which is the repair grave #891 taught: *a witness whose subject depends on scheduling is a
// sample, not an assertion.* The elapsed time is logged, not asserted.
//
// A `Stop` issued a moment *before* the agent reaches `enterBlocked` is still sound: `Stop` installs its
// round and then waits on a channel the release sites close, so an agent that blocks microseconds later
// takes the lock, sees the round, parks at the safepoint and releases it. There is no window in which an
// early `Stop` would spuriously expire.
func TestStopCompletesWhileASyncSubtaskCancelIsWaiting(t *testing.T) {
	const (
		// The resolution the cancel waits on. Must be comfortably longer than stwDeadline, because the
		// whole point is that Stop does not wait for it.
		lateResolve = 3 * time.Second
		// Stop's own bound. Returning nil within this is the assertion.
		stwDeadline = 500 * time.Millisecond
	)
	t.Setenv(asyncGateEnv, "1")
	b, err := os.ReadFile("testdata/asynclift/cancel-wat-parent/composed.wasm")
	if err != nil {
		t.Fatalf("the committed composed artefact is missing: %v", err)
	}

	cancelBegun := make(chan struct{}, 1)
	h := NewHost(io.Discard, io.Discard, nil)
	h.asyncImpls = map[string]asyncLowerImpl{
		"tick": func(_ *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
			onStart()
			return func() {
				// Signalled from onCancel, which the engine invokes immediately before the sync wait.
				select {
				case cancelBegun <- struct{}{}:
				default:
				}
				go func() {
					time.Sleep(lateResolve)
					onResolve(canon.U32(0))
				}()
			}, nil
		},
	}
	h.syncImpls = map[string]interp.CanonFunc{
		"note":   func(_ *interp.CanonCaller, _ []interp.Value) ([]interp.Value, error) { return nil, nil },
		"report": func(_ *interp.CanonCaller, _ []interp.Value) ([]interp.Value, error) { return nil, nil },
	}

	in, err := InstantiateWithHost(b, h)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()
	ci := coreInstanceWithExport(in, "go")
	if ci == nil {
		t.Fatal("no core instance exports go")
	}

	done := make(chan error, 1)
	go func() {
		_, cErr := in.CallValues("go")
		done <- cErr
	}()

	select {
	case <-cancelBegun:
	case <-time.After(5 * time.Second):
		t.Fatal("the guest never reached its cancellation within 5s, so there is no wait to stop against")
	}

	start := time.Now()
	stopErr := ci.Stop(stwDeadline)
	elapsed := time.Since(start)
	t.Logf("Stop returned after %s (deadline %s, the cancel is waiting on a %s resolution)",
		elapsed, stwDeadline, lateResolve)
	if stopErr != nil {
		t.Fatalf("Stop did not complete within %s while a sync subtask.cancel was waiting: %v\n"+
			"The waiting agent is in host code and must be marked BLOCKED for the duration "+
			"(§5 H-1, c.Blocking), or it reaches no safepoint and a stop-the-world stalls for the "+
			"whole wait — up to subtaskCancelBound. Phase 4 clause 3's GC drives STW through "+
			"cooperative safepoints, so this is a GC that stalls on a cancellation.",
			stwDeadline, stopErr)
	}

	// Resume and let the cancellation finish, so the test leaves nothing stopped and the cancel is shown
	// to still work after the round — a Stop that completed by breaking the wait would fail here.
	ci.Resume()
	select {
	case cErr := <-done:
		if cErr != nil {
			t.Errorf("after Stop/Resume the cancelling call failed: %v", cErr)
		}
	case <-time.After(lateResolve + 5*time.Second):
		t.Error("the cancelling call never completed after Stop/Resume")
	}
}
