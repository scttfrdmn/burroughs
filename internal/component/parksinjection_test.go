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
// DEFERRING helper. So the parks assertion had never been run against an impl that does not park, and
// *a control isn't born until it's watched die*.
//
// # The assertion here is inverted
//
// A red run of THIS test means the parks assertion would have MISSED an inline-resolving impl.
//
// # Measured rates (#863), both directions — and what overtook them
//
//	injected, this test     200 runs + 50 under -race   0 misses, 0 races
//	original, unmodified    200 runs + 50 under -race   0 failures, 0 races
//
// The conclusion drawn from those rows was *"the parks assertion has neither false passes nor false
// failures"*, on the separate-claims reasoning that "catches the bad case every time" and "never fires
// when nothing is wrong" need their own measurements. **The reasoning stands and the conclusion was
// falsified**: both rows were taken on `darwin/arm64`, and `ubuntu-24.04` later produced the miss (see
// the final check below). So the rows are kept as what was measured and the sentence they supported is
// struck — *a determinism premise is platform-scoped*, and 250 arm64 runs are not a claim about a
// control, only about a control on one machine.
//
// Both assertions are now **bounded negatives rather than samples**, so neither row's successor is a
// rate: the question "how often does it miss" no longer has a subject.
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

	// The original's parks check, read as a detector — and it is now the **strengthened** check, which is
	// why this arm is deterministic.
	//
	// # What moved, and why the old form could not stay
	//
	// This used to mirror the original's non-blocking `select` on A's channel and report a MISS when
	// `default:` was taken. That made the control's own verdict depend on whether A had delivered yet —
	// the same race it existed to expose. **CI on `ubuntu-24.04` took that branch** and reported a MISS
	// on a tree whose only change was a decoder repair.
	//
	// # The reproduction was sought and not found, which is why the repair is structural
	//
	//	darwin/arm64, native           180 runs, -cpu=1/2    0 misses
	//	amd64 under QEMU (xcheck)      180 runs, -cpu=1/2/4  0 misses
	//	x86-64 native (janus.local)    NOT RUN — hostname unresolvable
	//
	// So the miss is **not** explained by the architecture, which was the first hypothesis: the other
	// memory model, exercised by the instrument that exists for exactly this question, does not produce
	// it either. What is left is CI's machine under CI's load, which is not an instrument available here.
	// The native x86-64 row is a mechanism failure and not a verdict — CI's own x86-64 runner answers it
	// one push later.
	//
	// A repair whose failure cannot be reproduced cannot be watched die *as a timing repair*, so the
	// subject moved instead: both checks became assertions scheduling cannot perturb, and the
	// falsifications below are structural (remove the deferral / add one) rather than temporal.
	//
	// The detection claim, as the complement of the strengthened assertion: with an inline-resolving impl
	// **A completes without anything resolving its subtask**, so the original's bounded wait would see a
	// delivery and fail. That is the miss the original form could not reliably catch.
	//
	// Asserted by waiting for A **generously**, which is sound in this direction for the mirror of the
	// original's reason: A has nothing left to wait for, so it will deliver. The failure mode is a loud
	// timeout rather than a silent pass.
	oA := recvWithin(t, chA, 5*time.Second, "the inline-resolving agent")
	if oA.err != nil {
		t.Fatalf("A returned an error (%v); expected a completed inline round trip", oA.err)
	}
	if atomic.LoadInt32(entered) != 1 {
		t.Errorf("op impl entered %d times, want 1", atomic.LoadInt32(entered))
	}
	// **"A delivered without a resolve" is guaranteed by construction, not asserted here.**
	// `waitsetSynthHostInline` returns no resolver channel at all — it calls `onResolve` in place and has
	// nothing to hand out — so there is no state in which this control could be accidentally measuring a
	// deferring impl. I tried to assert it and found there was nothing to assert against, which is the
	// better arrangement: the distinction the detection rests on is in the helper's type rather than in a
	// check that could be forgotten.
}
