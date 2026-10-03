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

// waitsetSynthHostInline is `waitsetSynthHost`'s falsifying twin: the `op` impl resolves INLINE, so the
// guest never parks. It hands out no resolver, because an inline impl by definition has none — the one
// structural difference between the arms, and an unavoidable one, since the deferral is what is removed.
func waitsetSynthHostInline() (*Host, *int32) {
	h := NewHost(io.Discard, io.Discard, nil)
	var entered int32
	h.asyncImpls = map[string]asyncLowerImpl{
		"test:async/ops::op": func(_ *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
			onStart()
			atomic.AddInt32(&entered, 1)
			onResolve(canon.U32(107)) // INLINE — there is no park to observe
			return func() {}, nil
		},
	}
	return h, &entered
}

// TestWaitsetParksCheckDetectsAnInlineResolvingImpl is the falsification of
// `TestWaitableSetWaitParksOnlyTheCallingAgentSiblingRuns`'s parks assertion (#863).
//
// # Why that control needed falsifying
//
// Every one of the six tests driving `async-waitset-synth.wasm` goes through `waitsetSynthHost`, the
// DEFERRING helper. So the parks assertion — a non-blocking check that agent A has not returned — had
// never been run against an impl that does not park, and *a control isn't born until it's watched die*.
//
// # The assertion here is inverted
//
// A red run of THIS test means the parks assertion would have MISSED an inline-resolving impl. The
// original's `default:` arm passes whenever A has not yet delivered to its channel, so a miss is possible
// in principle and had to be measured rather than argued.
//
// # Measured rates (#863), both directions
//
//	injected, this test     200 runs + 50 under -race   0 misses, 0 races
//	original, unmodified    200 runs + 50 under -race   0 failures, 0 races
//
// So the parks assertion has neither false passes nor false failures. The two are **separate claims**:
// "catches the bad case every time" and "never fires when nothing is wrong" need their own measurements,
// and a control sound in one direction and flaky in the other still gets deleted.
//
// # The `entered` wait, and why it must not be simplified away
//
// **The first version of this injection omitted it and missed 199 of 200 runs.** That was not the parks
// assertion failing: `startInvoke` uses a BUFFERED channel, so A's result would sit ready, and a miss
// therefore meant A's goroutine had never run at all.
//
// The original test gets that synchronisation for free from `resolve := <-resolvers` — receiving the
// resolver proves A reached the `op` import before the sibling starts. An inline impl hands out no
// resolver, so deleting that line deletes the guarantee with it, and the test then measures Go's
// scheduler instead of the control. *A cheaper parameter can destroy a discrimination* — here the removed
// parameter was not a tuning knob but the arm's synchronisation.
//
// 199 of 200 is also the reason it was caught: too uniform for a race. One red run and 199 red runs read
// identically as "the control is broken", but only the second looks wrong enough to check.
func TestWaitsetParksCheckDetectsAnInlineResolvingImpl(t *testing.T) {
	t.Setenv("BURROUGHS_ASYNC", "1")
	b, err := os.ReadFile("testdata/async-waitset-synth.wasm")
	if err != nil {
		t.Fatalf("synth fixture: %v", err)
	}
	h, entered := waitsetSynthHostInline()
	in, err := InstantiateWithHost(b, h)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()
	ci := coreInstanceWithExport(in, "run")
	if ci == nil {
		t.Fatal("no core instance exports run")
	}

	chA := startInvoke(ci, "run")

	// Wait until A has demonstrably reached the import — the guarantee the original gets from
	// `<-resolvers`. See the doc comment: without this the test measures the scheduler.
	deadline := time.Now().Add(5 * time.Second)
	for atomic.LoadInt32(entered) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("A never reached the `op` import within 5s; the injection has no subject")
		}
		time.Sleep(time.Millisecond)
	}

	// Mirror the original's shape: a sibling runs to completion first, which is what gives the original's
	// non-blocking check its meaning — work demonstrably happened while A stayed parked.
	chB := startInvoke(ci, "sibling")
	oB := recvWithin(t, chB, 5*time.Second, "the sibling agent")
	if oB.err != nil {
		t.Fatalf("sibling failed (%v); the fixture, not the control, is the subject here", oB.err)
	}

	// The original's parks check, read as a detector. With an inline impl A has already returned, so
	// `default:` being taken is the control missing its failure case.
	select {
	case o := <-chA:
		if o.err != nil {
			t.Fatalf("A returned an error (%v); expected a completed inline round trip", o.err)
		}
		if atomic.LoadInt32(entered) != 1 {
			t.Errorf("op impl entered %d times, want 1", atomic.LoadInt32(entered))
		}
	default:
		t.Fatal("MISS: the parks assertion would have passed against an inline-resolving impl — " +
			"A had not delivered to its channel when the non-blocking check ran, so `default:` was " +
			"taken and the absence of a park went undetected")
	}
}
