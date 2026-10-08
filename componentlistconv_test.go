// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package burroughs

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
)

// White-box unit witnesses for the list **read** conversion (#902, ADR 0097 item 4's public half).
//
// # These are supplementary, and this comment used to say they were the only witness
//
// They were written when no guest could return a list: the eager lift refused every compound
// `task.return` result, so `fromCanon`'s list arm was unreachable through `Component.Call` and these
// unit tests were all the coverage it could have. That header is now **false** — the lift arm was wired
// through `canon.LoadList` in the same PR, on the chair's ruling that a read accessor must not ship as
// a name an embedder can call but cannot use for its purpose. Repaired rather than left standing,
// because a test file's own account of why it exists is the first thing its next reader believes.
//
// The real witness is `TestAnEmbedderReadsAListResult` in `burroughs_test`: a guest returns a
// `list<u32>` and clobbers its own backing immediately afterwards, so the embedder's elements prove the
// lift was **eager** and not merely correct. What is left here is what a guest cannot conveniently
// reach:
//
//   - the **empty** list's element type surviving the conversion, which needs a list with no element to
//     infer from;
//   - **nesting**, so the recursion in both the value and the type conversion is exercised rather than
//     extrapolated from one level;
//   - an **unspellable element type** refused by name, which needs a type the codec models and the
//     public surface does not — a condition no guest can be built to produce.
//
// Those are the cases that justify a white-box test. The rest moved out.

func TestAListConvertsBackFromTheCodecsValue(t *testing.T) {
	u32 := canon.Type{Kind: canon.KindU32}
	cv, err := canon.List(u32, canon.U32(1), canon.U32(2), canon.U32(3))
	if err != nil {
		t.Fatalf("canon.List: %v", err)
	}

	got, err := fromCanon(cv)
	if err != nil {
		t.Fatalf("fromCanon(list<u32>): %v", err)
	}
	if got.Kind() != KindComponentList {
		t.Fatalf("a canon list converted to a %s", got.Kind())
	}
	if s := got.String(); !strings.Contains(s, "list<u32>") {
		t.Errorf("the converted list renders as %q, losing its element type", s)
	}
	xs, ok := got.List()
	if !ok {
		t.Fatal("the converted value does not read as a list")
	}
	if len(xs) != 3 {
		t.Fatalf("the converted list has %d element(s), want 3", len(xs))
	}
	for i, want := range []uint32{1, 2, 3} {
		u, uok := xs[i].U32()
		if !uok {
			t.Errorf("element %d is %v, not a u32", i, xs[i])
			continue
		}
		if u != want {
			t.Errorf("element %d = %d, want %d", i, u, want)
		}
	}

	// **An empty list keeps its element type**, which is the case with nothing to infer from and the
	// reason the conversion reads the type descriptor rather than `elems[0]`.
	empty, err := canon.List(canon.Type{Kind: canon.KindString})
	if err != nil {
		t.Fatalf("canon.List(empty): %v", err)
	}
	ge, err := fromCanon(empty)
	if err != nil {
		t.Fatalf("fromCanon(empty list<string>): %v", err)
	}
	if s := ge.String(); !strings.Contains(s, "list<string>") {
		t.Errorf("an empty list converted to %q — the element type did not survive, so an empty "+
			"list<string> is indistinguishable from an empty list<u32>", s)
	}

	// Nesting, so the recursion in both the value and the type conversion is exercised rather than
	// assumed from the single-level case.
	inner, err := canon.List(u32, canon.U32(7))
	if err != nil {
		t.Fatalf("canon.List(inner): %v", err)
	}
	outer, err := canon.List(canon.Type{Kind: canon.KindList, Elem: &u32}, inner)
	if err != nil {
		t.Fatalf("canon.List(outer): %v", err)
	}
	gn, err := fromCanon(outer)
	if err != nil {
		t.Fatalf("fromCanon(list<list<u32>>): %v", err)
	}
	if s := gn.String(); !strings.Contains(s, "list<list<u32>>") {
		t.Errorf("a nested list converted to %q", s)
	}
}

// TestAnUnspellableElementRefusesNamingTheKind is the read direction's refusal, which ADR 0097 calls the
// direction nobody was looking.
//
// A `list<T>` whose `T` the public surface cannot spell must fail **naming T**, once — not succeed as a
// list whose elements then each fail, which would report the same gap per element and bury the one fact
// that matters.
func TestAnUnspellableElementRefusesNamingTheKind(t *testing.T) {
	// **`char` is the specimen, and it used to be `tuple`.** The swap is itself a finding worth
	// recording rather than a silent test edit: this asserted that a `list<tuple<u32>>` is refused
	// "naming tuple", and it broke the moment `canon.TupleType` started despecializing — the refusal
	// now reads `record{0: u32}`, because at the ABI layer that *is* what a tuple is
	// (definitions.py:1133). The assertion was about a name the type no longer has.
	//
	// `char` is the stable choice: the codec models it fully and the public value surface spells only
	// `u32`, `string` and lists of those, so it is unspellable for a reason that has nothing to do with
	// which compound forms have landed. A specimen whose name can change under a refactor was the wrong
	// one to hold a naming assertion against.
	ch := canon.Type{Kind: canon.KindChar}
	lt := canon.Type{Kind: canon.KindList, Elem: &ch}

	_, err := componentTypeFromCanon(lt)
	if err == nil {
		t.Fatal("list<char> was spelled across the public boundary, which has no char")
	}
	if !errors.Is(err, ErrUnsupported) {
		t.Errorf("the refusal is not ErrUnsupported: %v — this one IS an engine gap, unlike a caller's "+
			"own malformed input, so the sentinel is right here", err)
	}
	// It must name the element, not just "list": a refusal saying "cannot spell list" would be false,
	// since lists are spellable.
	if !strings.Contains(err.Error(), "char") {
		t.Errorf("the refusal %q does not name the element type it could not spell", err)
	}

	// And a record element, now that the codec models one: the refusal names the record structurally,
	// so an embedder sees which record rather than "a record". This is also where a **tuple** lands,
	// since it despecializes — so `tuple<u32>` is refused as `record{0: u32}`, which is the ABI's own
	// name for it and the one price of having a single field-carrying arm.
	rec := canon.RecordType(canon.Field{Name: "x", Type: canon.Type{Kind: canon.KindU32}})
	rlt := canon.Type{Kind: canon.KindList, Elem: &rec}
	rerr2 := func() error { _, e := componentTypeFromCanon(rlt); return e }()
	if rerr2 == nil {
		t.Fatal("list<record{x: u32}> was spelled across the public boundary, which has no record yet")
	}
	if !strings.Contains(rerr2.Error(), "record{x: u32}") {
		t.Errorf("the refusal %q does not name the record structurally; \"record\" alone would not say "+
			"which one, and the fields are part of the type", rerr2)
	}

	// Empty is not an escape hatch. An empty `list<char>` has no element to fail on, so a conversion
	// that checked only the elements would let its type through and hand back a list of nothing.
	emptyCh, cerr := canon.List(ch)
	if cerr != nil {
		t.Fatalf("canon.List(empty list<char>): %v", cerr)
	}
	if _, verr := fromCanon(emptyCh); verr == nil {
		t.Error("an empty list<char> crossed the public boundary; with no element to refuse, only the " +
			"type check can catch it")
	}
}

// TestTheListReadPathHasAnEndToEndWitness is this file's own anti-vacuity guard, and it exists because
// the header above was wrong once.
//
// A white-box test over a path with no end-to-end witness is defensible; one that *claims* to be
// supplementary while the real witness has quietly stopped existing is not. So the claim is checked:
// the guest fixture the end-to-end test drives must be present, and the conversion these tests exercise
// must be the one that test reaches.
//
// If the fixture is deleted, this fails here with the reason — rather than leaving a file whose header
// says "the real witness is elsewhere" pointing at nothing.
func TestTheListReadPathHasAnEndToEndWitness(t *testing.T) {
	const fixture = "internal/component/testdata/task-return-list-clobber-synth.wasm"
	if _, err := os.Stat(fixture); err != nil {
		t.Fatalf("the end-to-end witness's fixture is gone (%v), so the tests in this file are the only "+
			"coverage of the list read path again — and this file's header says they are not. Either "+
			"restore %s and TestAnEmbedderReadsAListResult, or rewrite the header to say what is true",
			err, fixture)
	}

	// And the conversion is reachable for a list of a spellable element, which is what makes the
	// end-to-end path work at all. A failure here and a pass there would mean the guest route bypasses
	// this conversion, which is worth knowing.
	u32 := canon.Type{Kind: canon.KindU32}
	cv, err := canon.List(u32, canon.U32(9))
	if err != nil {
		t.Fatalf("canon.List: %v", err)
	}
	if _, err := fromCanon(cv); err != nil {
		t.Fatalf("a list<u32> does not convert outward: %v", err)
	}
}
