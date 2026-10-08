// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package burroughs

import (
	"errors"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
)

// White-box witnesses for the list **read** direction (#902, ADR 0097 item 4's public half).
//
// # Why these are in package `burroughs` and not `burroughs_test`
//
// Because they have to be: `fromCanon` and `componentTypeFromCanon` are unexported, and **no guest
// returns a list today.** The eager lift refuses a compound `task.return` result by name — the codec
// exposes `canon.LoadList` now, but that arm does not use it yet, which is its own slice. So the
// conversion cannot be reached through `Component.Call`, and a path with no test is the thing this
// tree is least willing to ship.
//
// The alternative was to leave the arm out until a guest drove it. That is worse: `fromCanon`'s default
// arm would then refuse a list with "this release cannot carry a list", which will be **false** the
// moment the lift arm lands, and the gap between the two slices is exactly when someone would read it.
// Stated plainly rather than implied: the write direction is witnessed end-to-end through a real guest
// (`TestAnEmbedderPassesAListArgument`); the read direction is witnessed here, at the conversion, and
// its end-to-end witness arrives with the lift arm.
//
// The lift's own limit is pinned where it belongs — `TestTaskReturnRefusesAResultKindItCannotLift` in
// `internal/component` drives `liftTaskReturnValue` with a `list<u8>` result and asserts the refusal
// names `list`. So "no guest returns a list today" is a checked claim and not a recalled one, and it is
// checked by a behaviour rather than by this file.

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
	// `tuple` is the usable specimen: the codec models its size and alignment (for lowering an empty
	// `list<tuple>`, which is what the WASI getters return), so the TYPE is buildable while the public
	// surface cannot spell it. That is precisely the gap this refusal covers.
	tup := canon.TupleType(canon.Type{Kind: canon.KindU32})
	lt := canon.Type{Kind: canon.KindList, Elem: &tup}

	_, err := componentTypeFromCanon(lt)
	if err == nil {
		t.Fatal("list<tuple<u32>> was spelled across the public boundary, which has no tuple")
	}
	if !errors.Is(err, ErrUnsupported) {
		t.Errorf("the refusal is not ErrUnsupported: %v — this one IS an engine gap, unlike a caller's "+
			"own malformed input, so the sentinel is right here", err)
	}
	// It must name the element, not just "list": a refusal saying "cannot spell list" would be false,
	// since lists are spellable.
	if !strings.Contains(err.Error(), "tuple") {
		t.Errorf("the refusal %q does not name the element type it could not spell", err)
	}

	// Empty is not an escape hatch. An empty `list<tuple>` has no element to fail on, so a conversion
	// that checked only the elements would let its type through and hand back a list of nothing.
	emptyTup, cerr := canon.List(tup)
	if cerr != nil {
		t.Fatalf("canon.List(empty list<tuple>): %v", cerr)
	}
	if _, verr := fromCanon(emptyTup); verr == nil {
		t.Error("an empty list<tuple<u32>> crossed the public boundary; with no element to refuse, only " +
			"the type check can catch it")
	}
}

// TestAListIsSpellablePubliclyEvenThoughNoGuestReturnsOne is the anti-vacuity half: it says the reason
// these tests are white-box is the **lift's** limit and not a gap in the conversion they exercise.
//
// The engine-side limit itself is already pinned behaviourally, one package down, by
// `TestTaskReturnRefusesAResultKindItCannotLift`'s `list` case — it calls `liftTaskReturnValue` with a
// `list<u8>` result and asserts the refusal names `list`. That is the witness, and this test
// deliberately does **not** duplicate it: a second assertion over the same property, written here as a
// grep for a phrase in the lift's error message, was drafted and deleted. *A check that greps will match
// its own documentation* — and a test asserting a sentence rather than a behaviour fails when the
// sentence is improved, which trains the next reader to edit the test rather than read it.
func TestAListIsSpellablePubliclyEvenThoughNoGuestReturnsOne(t *testing.T) {
	u32 := canon.Type{Kind: canon.KindU32}
	lt := canon.Type{Kind: canon.KindList, Elem: &u32}

	// The public surface CAN spell and carry a list outward. So when the lift arm lands, nothing here
	// has to change — which is the property that makes landing it a small slice rather than a wide one.
	if _, err := componentTypeFromCanon(lt); err != nil {
		t.Fatalf("list<u32> is not spellable publicly: %v — the read direction would then be blocked "+
			"by this boundary as well as by the lift, and this file's premise is wrong", err)
	}
	cv, err := canon.List(u32, canon.U32(9))
	if err != nil {
		t.Fatalf("canon.List: %v", err)
	}
	if _, err := fromCanon(cv); err != nil {
		t.Fatalf("a list<u32> does not convert outward: %v", err)
	}
}
