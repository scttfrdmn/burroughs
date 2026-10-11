// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package burroughs

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/interp"
)

// TestCloseReachesItsBoundWithANamedOutcome is `Close`'s second stated behaviour: a guest that does not
// finish cancelling is torn down at the bound, with a **named** outcome rather than a hang.
//
// # The fixture is an UNCOOPERATIVE guest, because the old arithmetic was a race
//
// This used to take the ordinary yielding guest and shorten `componentCloseBound` below the cancellation
// round trip, so the bound was reached *by arithmetic*. That made the test depend on timing, and it
// failed on `make ci`'s `test-endtable` gate on a tree whose Go files were byte-identical to one the
// same gate had just passed.
//
// **The mechanism, established by forcing the order rather than by counting failures.** `Close(0)` calls
// `CancelAll` and then samples `active` exactly once. Drive the call to completion before that sample —
// call `CancelAll` directly, block on the call's result, then `Close(0)` — and the result is a
// deterministic **nil, 20/20**, with the call carrying the cancellation sentinel. The unmodified
// ordering gives `ErrCloseIncomplete` **60/60** on an idle host. So `Close` was right both times: a
// cooperating guest that finishes inside that window leaves nothing in flight, and a clean close is the
// correct answer the test was calling a failure.
//
// `lift-uncooperative-synth` removes the dependence. Its callback ignores its event triple — including
// TASK_CANCELLED — and returns YIELD, never calling `task.return`. So it never resolves, never returns
// EXIT, and `active` stays non-zero across the sample **by construction**. The old comment here named
// this fixture as the better specimen and said none existed; it exists now.
//
// # What ends the call, and why 5 seconds is a bound rather than a guess
//
// A guest that never stops has to be reclaimed by something, and it is not the park. Measured:
//
//	Close(0) after 14.25µs -> ErrCloseIncomplete
//	the call ENDED 48.958µs after Close returned
//
// `Close` tears down on **both** paths, so `c.in.Close()` closes the world either way, and **two
// mechanisms can then end the call** — see the assertion below for both sentinels and for why the test
// accepts either. What is ruled out is the park: the loop requests cancellation **once**
// (`!ctxRequested`, *"because a refused request would otherwise be retried every iteration for the rest
// of a spinning guest's life"*), and the `callbackYield` arm re-enters at once with EVENT_NONE without
// parking, so `internal/component/async_lift.go:liftParkBound`'s 30 seconds is never consulted. The
// reclamation is therefore microseconds, and the 5-second bound below is four orders of margin on a
// measured figure rather than a round number chosen to be safe.
//
// # What this does NOT cover
//
// Reclamation here works because the uncooperative guest **comes back** through `invokeIndex` to
// continue. A goroutine that never re-enters — a component-model Thread parked on a handoff, under
// ADR 0100 — waits on a channel `Close` does not touch, so the closed-instance refusal will not reach
// it. That case needs ADR 0071's terminal unwind through `exitReq` and an explicit wake, and it is owed
// by the first `gate:cm-threads` slice that creates a second Thread. This test says nothing about it.
func TestCloseReachesItsBoundWithANamedOutcome(t *testing.T) {
	t.Setenv("BURROUGHS_ASYNC", "1")
	wasm, err := os.ReadFile("internal/component/testdata/lift-uncooperative-synth.wasm")
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
	//
	// Zero is now a *statement* rather than a lever: the guest cannot finish in any bound, so the value
	// only decides how long the test waits to be told so.
	saved := componentCloseBound
	componentCloseBound = 0
	defer func() { componentCloseBound = saved }()

	done := make(chan error, 1)
	go func() {
		_, cErr := c.Call(context.Background(), "run")
		done <- cErr
	}()

	// **Wait for the call to be in flight, not for a duration.** `active` is the signal: `Call`
	// increments it before any guest work. With the uncooperative fixture it also cannot fall back to
	// zero on its own, which is what the old fixture could do between this loop and `Close`.
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
		// **The call's own outcome goes in the message.** Without it, "returned <nil>" cannot be told
		// apart from "the call had already ended", which is exactly the ambiguity that made the previous
		// failure unexplained. Read without blocking: the call may legitimately still be running.
		var callOutcome any = "still running"
		select {
		case e := <-done:
			callOutcome = e
		default:
		}
		t.Fatalf("Close with a zero bound returned %v; want ErrCloseIncomplete. The bound must end the "+
			"wait with a NAMED outcome — a Close that hung would not fail this test, it would never "+
			"finish it, and one that tore down silently would report a clean close over a guest that "+
			"had not stopped.\nthe call's outcome at this point: %v", closeErr, callOutcome)
	}
	// The outcome must be distinguishable from a clean close, which is the whole reason it is its own
	// value: "closed" and "closed over a guest that would not stop" say different things about the module.
	if errors.Is(closeErr, ErrComponentClosed) {
		t.Error("the incomplete-close outcome also matches ErrComponentClosed; the two must not collapse")
	}

	// **The uncooperative call is reclaimed, by one of TWO named sentinels, and the choice is a race
	// this test must not pick a side in.** Measured both ways on this fixture:
	//
	//	interp.ErrClosed      `instance is closed: "cb" was not called (contract §5 H-3)`
	//	                      — the guest was BETWEEN re-entries, so the next `invokeIndex` refused it
	//	interp.ErrTerminated  `thread terminated by shutdown: thread 1 ended at a safepoint after
	//	                      Close, so "cb" did not complete (contract §2 T-5.4)`
	//	                      — the guest was INSIDE guest execution, so it met a safepoint with
	//	                      `exitReq` set: ADR 0071's terminal unwind
	//
	// A standalone probe saw the first; this test in its package sees the second. **Both are correct
	// reclamations of the same guest**, and which one wins depends on where `Close` lands relative to
	// the callback loop's re-entry. Asserting either one alone would re-introduce a timing dependence in
	// a new place, which is the defect this fixture exists to remove — so the assertion is the
	// disjunction, and what it excludes is a nil error, an unrelated error, or no return at all.
	//
	// Asserted with `errors.Is` against the sentinels; the message texts above are documentation, not
	// the predicate.
	select {
	case callErr := <-done:
		if !errors.Is(callErr, interp.ErrClosed) && !errors.Is(callErr, interp.ErrTerminated) {
			t.Errorf("the reclaimed call ended with %v; want interp.ErrClosed (refused at the next "+
				"callback re-entry) or interp.ErrTerminated (ended at a safepoint by the terminal "+
				"unwind). Those are the two paths that reclaim a guest which ignores cancellation; "+
				"anything else means something unaccounted for ended it", callErr)
		}
	case <-time.After(5 * time.Second):
		t.Error("the in-flight call never returned after an incomplete Close; teardown must run on both " +
			"paths, so a reached bound reports itself rather than abandoning the instance. Measured at " +
			"~49µs, so five seconds is four orders of margin")
	}
}
