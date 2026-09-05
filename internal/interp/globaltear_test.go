// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"fmt"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// tearAgentIters is how many write/read rounds each agent runs. Large enough that the two goroutines
// overlap for most of their lives on any scheduler, and both controls below *assert* that overlap
// rather than assuming it — see each one's vacuity arm.
const tearAgentIters = 200_000

// buildTearModule compiles src and instantiates it, failing the test at whichever stage goes wrong.
// Shared by the two controls because the only thing that differs between them is the wat.
func buildTearModule(t *testing.T, src string) *Instance {
	t.Helper()
	img, err := text.EncodeModule([]byte(src))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	m, err := (&binary.Decoder{Features: binary.DefaultFeatures()}).DecodeModule(img)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	in, trap := Instantiate(m)
	if trap != nil {
		t.Fatalf("instantiate: %v", trap)
	}
	if derr := in.Deferred(); derr != nil {
		t.Fatalf("instantiate fell short: %v", derr)
	}
	return in
}

// TestAV128GlobalIsNeverAssembledFromTwoWrites is the v128 arm's witness, and it needs no race
// detector: the defect is observable **as a value**.
//
// # The defect
//
// A `v128` global is two `uint64` fields (decision 0024, grave #239's storage half), and `set` wrote
// them as two separate assignments. A `global.get` interleaved between those two assignments returns a
// vector built from the *low* half of one write and the *high* half of another — a value no `global.set`
// in the module ever wrote. Decision 0063's mutex is what makes the pair one unit.
//
// # Why this one has teeth without `-race`
//
// The writer only ever stores vectors whose two lanes are **equal** — all-zero or all-one — so
// `lane0 != lane1` is a torn read and nothing else. That is a value-level oracle, which the numeric
// arm cannot have (a single aligned 8-byte store does not tear on either architecture this runs on, so
// its witness is a race *report*; see the sibling control below). The tear detection runs inside the
// guest so the read loop is tight: pulling each observation out through `Invoke` would pay a call
// setup per sample and take orders of magnitude fewer of them.
//
// # The vacuity arm, which is the third return value
//
// Zero torn reads is also what a test whose goroutines never overlapped reports, and what a test whose
// reader ran entirely before the writer started reports. So the reader counts how many times it saw
// each of the two written vectors, and **both counts must be non-zero**: that is what establishes the
// loops were concurrent and the window was open. Without it this control would go green the day a
// scheduling change serialised the two agents, and would go green for the wrong reason.
func TestAV128GlobalIsNeverAssembledFromTwoWrites(t *testing.T) {
	src := fmt.Sprintf(`(module
	  (global $g (mut v128) (v128.const i64x2 0 0))
	  (func (export "write") (local $i i32)
	    (block $done (loop $l
	      (br_if $done (i32.eq (local.get $i) (i32.const %d)))
	      (global.set $g (v128.const i64x2 0 0))
	      (global.set $g (v128.const i64x2 -1 -1))
	      (local.set $i (i32.add (local.get $i) (i32.const 1)))
	      (br $l))))
	  (func (export "read") (result i32 i32 i32)
	    (local $i i32) (local $v v128) (local $torn i32) (local $zeros i32) (local $ones i32)
	    (block $done (loop $l
	      (br_if $done (i32.eq (local.get $i) (i32.const %d)))
	      (local.set $v (global.get $g))
	      (if (i64.ne (i64x2.extract_lane 0 (local.get $v))
	                  (i64x2.extract_lane 1 (local.get $v)))
	        (then (local.set $torn (i32.add (local.get $torn) (i32.const 1))))
	        (else (if (i64.eqz (i64x2.extract_lane 0 (local.get $v)))
	          (then (local.set $zeros (i32.add (local.get $zeros) (i32.const 1))))
	          (else (local.set $ones (i32.add (local.get $ones) (i32.const 1)))))))
	      (local.set $i (i32.add (local.get $i) (i32.const 1)))
	      (br $l)))
	    (local.get $torn) (local.get $zeros) (local.get $ones)))`,
		tearAgentIters, tearAgentIters)

	in := buildTearModule(t, src)

	werr := make(chan error, 1)
	go func() {
		_, err := in.Invoke("write")
		werr <- err
	}()
	got, rerr := in.Invoke("read")
	if err := <-werr; err != nil {
		t.Fatalf("write agent: %v", err)
	}
	if rerr != nil {
		t.Fatalf("read agent: %v", rerr)
	}
	if len(got) != 3 {
		t.Fatalf("read returned %d values, want 3", len(got))
	}
	torn, zeros, ones := got[0].Int32(), got[1].Int32(), got[2].Int32()

	// Vacuity first, because a torn count of zero means nothing until the window is known to have been
	// open. Reported as a failure rather than a skip: *a skip is not a verdict*, and a control that
	// declines to run is indistinguishable here from one that ran and found nothing.
	if zeros == 0 || ones == 0 {
		t.Fatalf("the reader saw %d all-zero and %d all-one vectors in %d reads, so it never observed "+
			"both of the values the writer stores — the two agents did not overlap and a torn count of "+
			"%d is not evidence of anything. This control measures nothing in this state",
			zeros, ones, tearAgentIters, torn)
	}
	if torn != 0 {
		t.Errorf("%d of %d `global.get`s on a v128 global returned a vector whose two i64 lanes "+
			"differ, and every `global.set` in the module writes both lanes equal — so those reads "+
			"assembled a value out of two different writes.\n"+
			"A v128 global is two `uint64` fields (decision 0024) and the pair has to be published as "+
			"one unit: decision 0063 holds the global's own mutex across both stores in `set` and both "+
			"loads in `get`. No spec vector can witness this — the reference has one thread — so this "+
			"test is the oracle (#573)",
			torn, tearAgentIters)
	}
}

// TestANumericGlobalIsNotWrittenAndReadWithoutSynchronisation is the numeric arm's witness, and unlike
// its v128 sibling it is a **race-detector** control: it has teeth under `go test -race` and is close
// to toothless without it.
//
// # Why the oracle has to be the detector here
//
// A numeric global is one `uint64`. On both architectures this engine is built for, an aligned 8-byte
// store does not tear, so no value the reader observes can distinguish a plain field from an atomic
// one — a value-level assertion would pass on the defect. What is wrong with the plain field is that it
// is an unsynchronised read/write pair in the Go memory model, where the result is undefined rather
// than merely stale, and the instrument that answers *that* question is `-race`.
//
// **So this control's verdict lives in CI's `race` step, not in `make check`.** `make check` does not
// pass `-race`; `make race` does, and CI reaches it from a step named `race` inside the `build` job
// (`.github/workflows/ci.yml`). **It is not a job of its own, which is what this comment said until
// the sentence was read against a run's own job list** — that list is fuzz-smoke, lint, conformance,
// citations, build twice and vuln, so a reader sent to find `race` in it finds nothing and cannot
// tell a skipped verdict from a misnamed one. Being a step rather than a job has two consequences
// worth having: a green local `check` still says nothing about this test's subject, and because
// `build` is a two-architecture matrix the step runs twice, so the verdict is two readings and not
// one. Stated because the alternative is a reader assuming the gate they ran covers it — *a green
// from a gate that did not run is unavailable, not implied* — and because **a verdict channel named
// wrongly is worse than one left unnamed**: the wrong name is checkable, so it reads as though
// somebody had checked it.
//
// # The vacuity arm
//
// A detector reports nothing if the two goroutines never overlap, and a value assertion would pass
// anyway. So the reader counts distinct observations and **must see both** of the writer's two values.
// That is the same guard the v128 control uses and it is here for a sharper reason: without it, this
// test's only failure mode is one that a non-overlapping schedule silently removes.
func TestANumericGlobalIsNotWrittenAndReadWithoutSynchronisation(t *testing.T) {
	src := fmt.Sprintf(`(module
	  (global $g (mut i64) (i64.const 0))
	  (func (export "write") (local $i i32)
	    (block $done (loop $l
	      (br_if $done (i32.eq (local.get $i) (i32.const %d)))
	      (global.set $g (i64.const 0))
	      (global.set $g (i64.const -1))
	      (local.set $i (i32.add (local.get $i) (i32.const 1)))
	      (br $l))))
	  (func (export "read") (result i32 i32 i32)
	    (local $i i32) (local $v i64) (local $other i32) (local $zeros i32) (local $ones i32)
	    (block $done (loop $l
	      (br_if $done (i32.eq (local.get $i) (i32.const %d)))
	      (local.set $v (global.get $g))
	      (if (i64.eqz (local.get $v))
	        (then (local.set $zeros (i32.add (local.get $zeros) (i32.const 1))))
	        (else (if (i64.eq (local.get $v) (i64.const -1))
	          (then (local.set $ones (i32.add (local.get $ones) (i32.const 1))))
	          (else (local.set $other (i32.add (local.get $other) (i32.const 1)))))))
	      (local.set $i (i32.add (local.get $i) (i32.const 1)))
	      (br $l)))
	    (local.get $other) (local.get $zeros) (local.get $ones)))`,
		tearAgentIters, tearAgentIters)

	in := buildTearModule(t, src)

	werr := make(chan error, 1)
	go func() {
		_, err := in.Invoke("write")
		werr <- err
	}()
	got, rerr := in.Invoke("read")
	if err := <-werr; err != nil {
		t.Fatalf("write agent: %v", err)
	}
	if rerr != nil {
		t.Fatalf("read agent: %v", rerr)
	}
	if len(got) != 3 {
		t.Fatalf("read returned %d values, want 3", len(got))
	}
	other, zeros, ones := got[0].Int32(), got[1].Int32(), got[2].Int32()

	if zeros == 0 || ones == 0 {
		t.Fatalf("the reader saw %d zeros and %d all-ones in %d reads of an i64 global, so it never "+
			"observed both values the writer stores — the agents did not overlap, and under `-race` "+
			"this run exercised no concurrent access at all. This control measures nothing in this state",
			zeros, ones, tearAgentIters)
	}
	// Weak by construction and kept anyway: on amd64 and arm64 an aligned 8-byte store is indivisible,
	// so this arm cannot fail on the defect and is not what makes this control work. It is here because
	// it costs one comparison and it is the arm that would fire on a platform where the premise fails.
	if other != 0 {
		t.Errorf("%d of %d `global.get`s on an i64 global returned neither 0 nor -1, and those are the "+
			"only two values written — an 8-byte global was observed torn, which this engine's target "+
			"architectures were assumed not to permit (#573, decision 0063)",
			other, tearAgentIters)
	}
}

// TestARefGlobalIsNotWrittenAndReadWithoutSynchronisation is the reference arm's witness — #573's third
// arm, the one decision 0063 left open and decision 0066 settles with an atomic pointer.
//
// # The defect, and why it is worse than the numeric arm's
//
// A `ref` is **40 bytes**: two discriminator bools, two `uint32`s and three pointers
// (`TestRefWidthIsMeasuredNotAssumed` pins the width). `set` wrote it as one plain struct assignment,
// which the compiler lowers to five word stores, so a concurrent `global.get` can pair one write's
// discriminator with another write's payload — a value no `global.set` in the module ever wrote. That is
// the v128 arm's defect at 2.5× the width, and it is what falsified the *"only case above word width,
// and rare in practice"* premise the earlier ruling rested on.
//
// # The oracle is `-race`, and the reason is not the one the numeric arm gives
//
// The numeric arm cannot tear on either architecture this engine targets, so its wrongness is
// definitional — an unsynchronised read/write pair, undefined by the Go memory model — and only a
// detector can see it. Here a tear is genuinely possible, and the oracle is still `-race`, because a
// **guest-visible** value-level oracle is not available: the only thing wasm MVP lets a guest ask about
// a reference it holds is `ref.is_null`, so distinguishing two *non-null* references from inside the
// module needs `ref.eq` or `call_ref` — the function-references and GC proposals, both gated off here.
// A host-side comparison through `value()` would be a different measurement (one boundary crossing per
// observation) and would price the read loop rather than the field. So the value arm below is the weak
// one and is kept for the same reason its numeric sibling keeps its own: it costs one comparison and it
// is what would fire if the assumption behind the strong arm's absence were wrong.
//
// **So this control's verdict lives in CI's `race` step, not in `make check`** — inside the `build` job,
// which is a two-architecture matrix, so the verdict is two readings. The numeric sibling above states
// the same thing and states why naming that channel wrongly would be worse than not naming it.
//
// # Watched die
//
// With `ref` a plain field and `storeRef`/`loadRef` a plain assignment and a plain read — the shape
// `main` carried before decision 0066 — `go test -race -run
// TestARefGlobalIsNotWrittenAndReadWithoutSynchronisation ./internal/interp/` reports `WARNING: DATA
// RACE`, the write at `interp.(*global).storeRef` inside `set` against the previous read at
// `interp.(*global).get` — the read side is named for `get` rather than `loadRef` because the accessor
// inlines, which is worth recording so a reader matching the report against this paragraph is not
// looking for a frame the compiler removed. The mutation is the pre-0066 code, so the arm needs no
// invention.
//
// # The vacuity arm
//
// A detector reports nothing if the agents never overlap. The reader therefore counts null and non-null
// observations and **both must be non-zero**, which is what establishes that the window was open. The
// writer alternates `ref.null func` and `ref.func $f`, so those two counts partition its reads.
func TestARefGlobalIsNotWrittenAndReadWithoutSynchronisation(t *testing.T) {
	src := fmt.Sprintf(`(module
	  (func $f)
	  (elem declare func $f)
	  (global $g (mut funcref) (ref.null func))
	  (func (export "write") (local $i i32)
	    (block $done (loop $l
	      (br_if $done (i32.eq (local.get $i) (i32.const %d)))
	      (global.set $g (ref.null func))
	      (global.set $g (ref.func $f))
	      (local.set $i (i32.add (local.get $i) (i32.const 1)))
	      (br $l))))
	  (func (export "read") (result i32 i32)
	    (local $i i32) (local $nulls i32) (local $funcs i32)
	    (block $done (loop $l
	      (br_if $done (i32.eq (local.get $i) (i32.const %d)))
	      (if (ref.is_null (global.get $g))
	        (then (local.set $nulls (i32.add (local.get $nulls) (i32.const 1))))
	        (else (local.set $funcs (i32.add (local.get $funcs) (i32.const 1)))))
	      (local.set $i (i32.add (local.get $i) (i32.const 1)))
	      (br $l)))
	    (local.get $nulls) (local.get $funcs)))`,
		tearAgentIters, tearAgentIters)

	in := buildTearModule(t, src)

	werr := make(chan error, 1)
	go func() {
		_, err := in.Invoke("write")
		werr <- err
	}()
	got, rerr := in.Invoke("read")
	if err := <-werr; err != nil {
		t.Fatalf("write agent: %v", err)
	}
	if rerr != nil {
		t.Fatalf("read agent: %v", rerr)
	}
	if len(got) != 2 {
		t.Fatalf("read returned %d values, want 2", len(got))
	}
	nulls, funcs := got[0].Int32(), got[1].Int32()

	if nulls == 0 || funcs == 0 {
		t.Fatalf("the reader saw %d nulls and %d function references in %d reads of a funcref global, "+
			"so it never observed both values the writer stores — the agents did not overlap, and under "+
			"`-race` this run exercised no concurrent access at all. This control measures nothing in "+
			"this state", nulls, funcs, tearAgentIters)
	}
	// The two counts must also account for every read, which is the arm that fires if a torn reference
	// were ever *reported* as neither — it cannot be, since `ref.is_null` is one bit and the `if` above
	// is total. Written as an accounting check rather than dropped, because that totality is the reason
	// the value-level oracle is unavailable here and a reader who does not know that will look for it.
	if int(nulls)+int(funcs) != tearAgentIters {
		t.Errorf("the reader accounted for %d + %d = %d of %d reads. `ref.is_null` partitions every "+
			"observation, so any other total means the read loop did not run the advertised number of "+
			"times and the vacuity arm above is checking a different population",
			nulls, funcs, int(nulls)+int(funcs), tearAgentIters)
	}
}
