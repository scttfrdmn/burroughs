// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package burroughs

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

// TestCloseReachesItsBoundWithANamedOutcome is `Close`'s second stated behaviour: a guest that does not
// finish cancelling is torn down at the bound, with a **named** outcome rather than a hang.
//
// # Why this needs the bound shortened and nothing else faked
//
// The fixture is the ordinary yielding guest; what makes it not finish is the bound being shorter than
// the cancellation takes. `componentCloseBound` is reduced to a value below the round trip, so the bound
// is reached by arithmetic rather than by a guest written to misbehave — which keeps the subject the
// *bound* and not a special fixture.
//
// A real uncooperative guest would be a better specimen and there is no committed one: a guest that
// ignores `TASK_CANCELLED` entirely would have to be authored, and the fixtures that exist all cooperate.
// Said rather than left implicit.
func TestCloseReachesItsBoundWithANamedOutcome(t *testing.T) {
	t.Setenv("BURROUGHS_ASYNC", "1")
	wasm, err := os.ReadFile("internal/component/testdata/lift-cancel-yield-synth.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	c, err := LoadComponent(wasm)
	if err != nil {
		t.Fatalf("LoadComponent: %v", err)
	}

	// Set directly, which is why this witness is in the INTERNAL test package: the bound is an
	// implementation detail, and exporting a hook so an external test could reach it would be new public
	// surface for a test's convenience — exactly what the stamp's limit reserves.
	saved := componentCloseBound
	componentCloseBound = 0
	defer func() { componentCloseBound = saved }()

	done := make(chan error, 1)
	go func() {
		_, cErr := c.Call(context.Background(), "run")
		done <- cErr
	}()

	// **Wait for the call to be in flight, not for 20ms.**
	//
	// This was `time.Sleep(20 * time.Millisecond)`, and it was a timer standing in for a signal — this
	// tree's own first operational rule, applied to a test instead of to CI. It held on an idle machine
	// and failed under `make strict`, which runs every package at once: 20ms was not enough for the call
	// to enter, `Close` found nothing active, and returned cleanly. The test then reported that the bound
	// had not produced `ErrCloseIncomplete` — **a true statement about a run that never set up the
	// condition.** A load-sensitive false failure is the mild outcome; the same sleep could have passed
	// over a broken bound on a fast machine.
	//
	// `active` is the signal: `Call` increments it before doing any guest work, and the yielding fixture
	// never finishes, so once it is non-zero it stays non-zero. The deadline makes a call that never
	// enters a failure rather than a hang.
	deadline := time.Now().Add(5 * time.Second)
	for {
		c.mu.Lock()
		inFlight := c.active
		c.mu.Unlock()
		if inFlight > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the call never entered: Close's bound cannot be reached with nothing in flight, so " +
				"this test would have asserted the bound against an idle component")
		}
		time.Sleep(time.Millisecond)
	}

	closeErr := c.Close()
	if !errors.Is(closeErr, ErrCloseIncomplete) {
		t.Fatalf("Close with a zero bound returned %v; want ErrCloseIncomplete. The bound must end the "+
			"wait with a NAMED outcome — a Close that hung would not fail this test, it would never "+
			"finish it, and one that tore down silently would report a clean close over a guest that "+
			"had not stopped", closeErr)
	}
	// The outcome must be distinguishable from a clean close, which is the whole reason it is its own
	// value: "closed" and "closed over a guest that would not stop" say different things about the module.
	if errors.Is(closeErr, ErrComponentClosed) {
		t.Error("the incomplete-close outcome also matches ErrComponentClosed; the two must not collapse")
	}

	// The call still ends — teardown runs on both paths, so a reached bound is not a leak.
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Error("the in-flight call never returned after an incomplete Close; teardown must run on both " +
			"paths, so a reached bound reports itself rather than abandoning the instance")
	}
}
