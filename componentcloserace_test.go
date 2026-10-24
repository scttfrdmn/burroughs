// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package burroughs

import (
	"context"
	"errors"
	"os"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/interp"
)

// TestCloseCountsEveryCallItAdmits is #945's witness. It was built under the chair's second ruling of 2026-10-24,
// and committed under the third, as a guard on an ordering anyone could break later. It
// drives `Close` while calls are still starting, with both a zero bound and a non-zero one, and checks whether
// `Close` reports a clean close while a call it should have counted is still running.
//
// **What "should have counted" means here.** The fixture is `lift-uncooperative-synth`. Its guest ignores
// cancellation and never finishes on its own, so an admitted call ends only through the teardown inside
// `Close`. That teardown runs *after* `Close` has sampled `active`, and it ends the call with
// `interp.ErrClosed` or `interp.ErrTerminated` (see `TestCloseReachesItsBoundWithANamedOutcome` for why it
// can be either). So a call ending with either sentinel was in flight when `Close` sampled. If `Close`
// returned nil in that trial, it reported complete while a call it had admitted was still running. That is
// #945's non-zero-bound question, which the issue says would be a real defect rather than a naming one.
//
// A refused call ends with ErrComponentClosed and is not counted. Any other outcome is reported but is not a
// violation: the guest cannot produce one, so it means something else ended the call. Asserting on it would
// be guessing which.
//
// **The floor.** A trial in which every call was admitted, or every call refused, never opened the window
// between the closed check and the increment. Each bound must see at least one trial with both outcomes,
// or the run is an instrument fault rather than a pass.
//
// **The measured run, recorded on #945.** On macOS arm64 and on janus x86-64 there were 0 violations in 27,000
// trials per bound, with about 80–87% of trials straddling admission. `BURROUGHS_CLOSE_TRIALS` sets the count
// for such a run. The default of 500 per bound keeps the test under the chair's 5-second budget under `-race`.
//
// NEUTER, run: `Call`'s `active++` moved to its own `c.mu` acquisition after the closed check, with a
// `runtime.Gosched` between the two. Both arms then fail. At the committed 500 trials per bound there were 61
// violations at the zero bound and 59 at 2ms. At 40 trials there were 8 and 4.
func TestCloseCountsEveryCallItAdmits(t *testing.T) {
	t.Setenv("BURROUGHS_ASYNC", "1")
	wasm, err := os.ReadFile("internal/component/testdata/lift-uncooperative-synth.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	trials := 500
	if s := os.Getenv("BURROUGHS_CLOSE_TRIALS"); s != "" {
		if trials, err = strconv.Atoi(s); err != nil || trials < 1 {
			t.Fatalf("BURROUGHS_CLOSE_TRIALS=%q is not a positive count", s)
		}
	}
	const callers = 4

	saved := componentCloseBound
	defer func() { componentCloseBound = saved }()

	for _, bound := range []time.Duration{0, 2 * time.Millisecond} {
		t.Run("bound="+bound.String(), func(t *testing.T) {
			componentCloseBound = bound
			var straddled, violations, other int
			for trial := range trials {
				c, lerr := LoadComponent(wasm)
				if lerr != nil {
					t.Fatalf("LoadComponent: %v", lerr)
				}
				var start, ready sync.WaitGroup
				start.Add(1)
				ready.Add(callers)
				errs := make([]error, callers)
				var ended sync.WaitGroup
				ended.Add(callers)
				for i := range callers {
					go func() {
						defer ended.Done()
						ready.Done()
						start.Wait()
						_, errs[i] = c.Call(context.Background(), "run")
					}()
				}
				ready.Wait()
				start.Done()
				// Stagger Close across the calls' admission: trial k yields k mod 8 times first, so the
				// sample lands at different points of the callers' starts.
				for range trial % 8 {
					runtime.Gosched()
				}
				closeErr := c.Close()

				waited := make(chan struct{})
				go func() { ended.Wait(); close(waited) }()
				select {
				case <-waited:
				case <-time.After(5 * time.Second):
					t.Fatalf("trial %d: a call never returned after Close; teardown runs on both paths", trial)
				}

				var reclaimed, refused int
				for _, e := range errs {
					switch {
					case errors.Is(e, ErrComponentClosed):
						refused++
					case errors.Is(e, interp.ErrClosed), errors.Is(e, interp.ErrTerminated):
						reclaimed++
					default:
						other++
						t.Logf("trial %d: a call ended with %v, which this guest cannot produce", trial, e)
					}
				}
				if reclaimed > 0 && refused > 0 {
					straddled++
				}
				if reclaimed > 0 && closeErr == nil {
					violations++
					t.Errorf("trial %d: Close returned nil while %d admitted call(s) were still running; "+
						"they ended only at the teardown after Close's sample (%d refused)",
						trial, reclaimed, refused)
				}
				if reclaimed > 0 && closeErr != nil && !errors.Is(closeErr, ErrCloseIncomplete) {
					t.Errorf("trial %d: Close returned %v; want ErrCloseIncomplete with calls in flight",
						trial, closeErr)
				}
			}
			t.Logf("bound %s: %d trials, %d straddled admission, %d violations, %d other outcomes",
				bound, trials, straddled, violations, other)
			if straddled == 0 {
				t.Fatalf("no trial of %d had both an admitted and a refused call: Close never landed inside "+
					"the callers' admission, so this run witnessed nothing", trials)
			}
		})
	}
}
