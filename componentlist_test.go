// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package burroughs_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs"
)

// The embedder's-eye tests for the public list surface (#902, ADR 0097 item 4's public half).
//
// In `burroughs_test` for `componentstring_test.go`'s reason: the claim is that the surface is reachable
// from **outside** the module, and only a test that cannot touch an unexported identifier can make it.
// The compiler is the assertion.

func loadListFixture(t *testing.T) *burroughs.Component {
	t.Helper()
	t.Setenv("BURROUGHS_ASYNC", "1")
	wasm, err := os.ReadFile("internal/component/testdata/string-arg-realloc-synth.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	c, err := burroughs.LoadComponent(wasm)
	if err != nil {
		t.Fatalf("LoadComponent: %v", err)
	}
	t.Cleanup(func() {
		if cerr := c.Close(); cerr != nil {
			t.Errorf("Close: %v", cerr)
		}
	})
	return c
}

// TestAnEmbedderPassesAListArgument is the deliverable: a `list<u32>` built with the public
// constructors crosses into a guest, which sums it.
//
// The guest reads its elements at a 4-byte stride, so a lowering at the wrong pitch gives a wrong
// **sum** rather than a wrong length — which is what makes the sum a real verdict on the framing and
// not just on the count.
func TestAnEmbedderPassesAListArgument(t *testing.T) {
	c := loadListFixture(t)

	for _, tc := range []struct {
		name string
		in   []uint32
		want uint32
	}{
		{"three elements", []uint32{1, 2, 3}, 6},
		{"one element", []uint32{42}, 42},
		// An empty list is the case with no element to infer a type from, which is the whole reason
		// ComponentType is a spellable thing.
		{"empty", nil, 0},
		// The high bits must survive: a u32 whose top bit is set is negative as an i32, and the flat
		// word is two's complement.
		{"high bits", []uint32{0xFFFF_FFFF, 1}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vals := make([]burroughs.ComponentValue, 0, len(tc.in))
			for _, u := range tc.in {
				vals = append(vals, burroughs.ComponentU32(u))
			}
			xs, err := burroughs.ComponentList(burroughs.ComponentTypeU32(), vals...)
			if err != nil {
				t.Fatalf("ComponentList: %v", err)
			}

			res, err := c.Call(context.Background(), "sum-list", xs)
			if err != nil {
				t.Fatalf("Call(sum-list, %v): %v", xs, err)
			}
			if len(res) != 1 {
				t.Fatalf("sum-list returned %d value(s), want 1", len(res))
			}
			got, ok := res[0].U32()
			if !ok {
				t.Fatalf("the result is %v, not a u32", res[0])
			}
			if got != tc.want {
				t.Errorf("sum-list(%v) = %d, want %d — a wrong sum over a right length is a lowering at "+
					"the wrong element stride", tc.in, got, tc.want)
			}
		})
	}
}

// TestAListElementIsCheckedStructurallyNotByKind is ADR 0097's first change on the public side, and the
// neuter target.
//
// The ADR records what the first draft did: compare `vals[i].Kind()` against the element kind. `Kind()`
// says `list` for every list, so `list<list<u32>>` would have accepted a `list<string>` element. The
// check calls the **same** `canon.TypeEqual` the engine's parameter check uses, so the two cannot drift
// about what "the same type" means.
func TestAListElementIsCheckedStructurallyNotByKind(t *testing.T) {
	u32s, err := burroughs.ComponentList(burroughs.ComponentTypeU32(), burroughs.ComponentU32(1))
	if err != nil {
		t.Fatalf("building list<u32>: %v", err)
	}
	s, err := burroughs.ComponentString("a")
	if err != nil {
		t.Fatalf("ComponentString: %v", err)
	}
	strs, err := burroughs.ComponentList(burroughs.ComponentTypeString(), s)
	if err != nil {
		t.Fatalf("building list<string>: %v", err)
	}

	listOfU32, err := burroughs.ComponentTypeList(burroughs.ComponentTypeU32())
	if err != nil {
		t.Fatalf("ComponentTypeList: %v", err)
	}

	// `list<list<u32>>` accepts a `list<u32>` element...
	if _, nerr := burroughs.ComponentList(listOfU32, u32s); nerr != nil {
		t.Fatalf("a list<u32> was refused as an element of list<list<u32>>: %v", nerr)
	}
	// ...and must refuse a `list<string>` one. Both are lists, so a kind comparison lets it through.
	_, err = burroughs.ComponentList(listOfU32, strs)
	if err == nil {
		t.Fatal("a list<string> was accepted as an element of list<list<u32>>. Both are lists, so a " +
			"kind comparison admits it; the elements would then be lowered at the string stride into a " +
			"buffer read as u32s — a plausible wrong value, which is what this check exists to stop")
	}
	// **The message must name both element types**, not print "list" twice: a refusal naming the two
	// sides of a mismatch identically reads as though a type had been refused for matching.
	for _, want := range []string{"list<string>", "list<u32>"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not mention %q", err, want)
		}
	}

	// The scalar case through the same comparison, so the check is not list-specific.
	if _, err := burroughs.ComponentList(burroughs.ComponentTypeU32(), s); err == nil {
		t.Error("a string was accepted as a list<u32> element")
	}
}

// TestAListRefusesATypeOrElementNobodyBuilt covers the two zero values, which are different mistakes and
// are named differently.
func TestAListRefusesATypeOrElementNobodyBuilt(t *testing.T) {
	// A list of the ZERO TYPE would reach the codec as a list whose element tag reads `bool`, because
	// the codec's zero Kind is bool. So an unset element type must refuse at construction rather than
	// lower a list of booleans.
	var noType burroughs.ComponentType
	if _, err := burroughs.ComponentTypeList(noType); err == nil {
		t.Error("ComponentTypeList accepted the zero ComponentType; a list of nothing would lower as a " +
			"list of bools")
	} else if !strings.Contains(err.Error(), "bool") {
		t.Errorf("the refusal %q does not say what a list of the zero type would become", err)
	}
	// And the value constructor refuses it too, through the same check rather than a second copy.
	if _, err := burroughs.ComponentList(noType); err == nil {
		t.Error("ComponentList accepted the zero element type")
	}

	// An unset ELEMENT is a different mistake: the value was never constructed, rather than being the
	// wrong type. Reporting "want u32, got none" would invite a hunt for a `none` type.
	var noValue burroughs.ComponentValue
	_, err := burroughs.ComponentList(burroughs.ComponentTypeU32(), noValue)
	if err == nil {
		t.Fatal("ComponentList accepted the zero ComponentValue as an element")
	}
	if !strings.Contains(err.Error(), "never constructed") {
		t.Errorf("the refusal %q does not say the element was never constructed", err)
	}
}

// TestAnEmptyListIsDistinguishableFromANonList is why the accessor returns a boolean rather than just a
// slice: `list<u32>` with no elements is a legitimate value, and it is what a WASI getter returns.
func TestAnEmptyListIsDistinguishableFromANonList(t *testing.T) {
	empty, err := burroughs.ComponentList(burroughs.ComponentTypeU32())
	if err != nil {
		t.Fatalf("an empty list<u32> was refused: %v", err)
	}
	xs, ok := empty.List()
	if !ok {
		t.Fatal("an empty list did not read as a list; `len(xs) == 0` cannot tell this from a non-list")
	}
	if len(xs) != 0 {
		t.Errorf("an empty list read back %d element(s)", len(xs))
	}

	// A non-list returns (nil, false) rather than an empty slice, which is the distinction the boolean
	// carries. Without it both cases would be "no elements".
	if _, ok := burroughs.ComponentU32(7).List(); ok {
		t.Error("a u32 read as a list")
	}

	// The type survives on an empty list, which is the one question an empty list raises — and it is why
	// the element type is a parameter rather than inferred from the first element.
	if got := empty.String(); !strings.Contains(got, "list<u32>") {
		t.Errorf("an empty list renders as %q, which does not say what it is a list of", got)
	}
}

// TestAListAccessorHandsBackACopy closes the aliasing hole the construction side already declines to
// create: `ComponentList` copies its input, so the read side must copy too. A value that is immutable in
// two of three directions is mutable.
func TestAListAccessorHandsBackACopy(t *testing.T) {
	xs, err := burroughs.ComponentList(burroughs.ComponentTypeU32(),
		burroughs.ComponentU32(1), burroughs.ComponentU32(2))
	if err != nil {
		t.Fatalf("ComponentList: %v", err)
	}

	first, _ := xs.List()
	first[0] = burroughs.ComponentU32(99)

	second, _ := xs.List()
	got, ok := second[0].U32()
	if !ok {
		t.Fatalf("element 0 is %v, not a u32", second[0])
	}
	if got != 1 {
		t.Errorf("element 0 reads %d after a write through an earlier List() result, want 1 — the "+
			"accessor handed back the backing array, so a caller can reach inside a value somebody "+
			"else also holds", got)
	}

	// The same on the way in: a caller's slice, mutated after construction, must not reach the value.
	vals := []burroughs.ComponentValue{burroughs.ComponentU32(5)}
	ys, err := burroughs.ComponentList(burroughs.ComponentTypeU32(), vals...)
	if err != nil {
		t.Fatalf("ComponentList: %v", err)
	}
	vals[0] = burroughs.ComponentU32(77)
	zs, _ := ys.List()
	if g, _ := zs[0].U32(); g != 5 {
		t.Errorf("element 0 reads %d after the caller's own slice was written, want 5", g)
	}
}

// TestAComponentTypeRendersStructurally is the reporting half of ADR 0097's first change. Comparing
// kinds is the defect; reporting kinds is how the defect hides, so the renderer is part of the repair
// rather than a cosmetic follow-up.
func TestAComponentTypeRendersStructurally(t *testing.T) {
	listOfU32, err := burroughs.ComponentTypeList(burroughs.ComponentTypeU32())
	if err != nil {
		t.Fatalf("ComponentTypeList: %v", err)
	}
	nested, err := burroughs.ComponentTypeList(listOfU32)
	if err != nil {
		t.Fatalf("ComponentTypeList(list<u32>): %v", err)
	}
	listOfString, err := burroughs.ComponentTypeList(burroughs.ComponentTypeString())
	if err != nil {
		t.Fatalf("ComponentTypeList(string): %v", err)
	}

	for _, c := range []struct {
		got  string
		want string
	}{
		{burroughs.ComponentTypeU32().String(), "u32"},
		{burroughs.ComponentTypeString().String(), "string"},
		{listOfU32.String(), "list<u32>"},
		{listOfString.String(), "list<string>"},
		{nested.String(), "list<list<u32>>"},
	} {
		if c.got != c.want {
			t.Errorf("rendered %q, want %q", c.got, c.want)
		}
	}

	// The zero type renders as "none" and NOT as "bool", which is what delegating straight to the codec
	// type would print — the codec's zero Kind is bool, so a zero struct would claim to be one.
	var none burroughs.ComponentType
	if got := none.String(); got != "none" {
		t.Errorf("the zero ComponentType renders as %q, want \"none\"", got)
	}
	if none.Kind() != burroughs.KindComponentNone {
		t.Errorf("the zero ComponentType's kind is %v", none.Kind())
	}
}

// TestAListValueRendersItsLengthNotItsElements guards a diagnostic against the values this engine
// deliberately accepts: the kind-dependent byte cap exists so a 100-million-element `list<u32>` is
// legal, and a renderer that expanded one would be a denial of service against whoever reads the log.
func TestAListValueRendersItsLengthNotItsElements(t *testing.T) {
	xs, err := burroughs.ComponentList(burroughs.ComponentTypeU32(),
		burroughs.ComponentU32(1), burroughs.ComponentU32(2), burroughs.ComponentU32(3))
	if err != nil {
		t.Fatalf("ComponentList: %v", err)
	}
	got := xs.String()
	if !strings.Contains(got, "list<u32>") || !strings.Contains(got, "3") {
		t.Errorf("a list renders as %q, which does not carry both its type and its length", got)
	}
	// Not the elements. `u32(1)` appearing would mean the renderer expands its contents.
	if strings.Contains(got, "u32(1)") {
		t.Errorf("a list renders its elements (%q); a large one would flood a log", got)
	}
}

// TestAnUnspellableElementTypeIsRefusedByName is the read direction's refusal, which is the direction
// nobody was looking: a `record` element has to be refused **naming the record**, rather than arriving
// as some other element type.
//
// Asserted through the error taxonomy rather than through a guest, because no committed guest returns a
// `list<record>` — the codec has no record at all yet (#904). What is checked is that the public
// surface cannot *spell* one, which is the property the refusal rests on.
func TestAnUnspellableElementTypeIsRefusedByName(t *testing.T) {
	// The public type vocabulary is u32, string, and lists of those. Any kind outside it has no
	// constructor at all — which is the strongest form of this check, because an embedder cannot build
	// the unsupported type to pass it in. Enumerated here so the claim is visible rather than implied.
	//
	// When `ComponentTypeRecord` lands, this test gains a case: a record element refused by name on the
	// way out. Until then the honest assertion is about the vocabulary's extent.
	for _, k := range []burroughs.ComponentKind{
		burroughs.KindComponentRecord,
		burroughs.KindComponentVariant,
		burroughs.KindComponentTuple,
		burroughs.KindComponentOption,
	} {
		// Each kind has a name, so a refusal can say which one it refused — that is what the enum is
		// for, and it is why the kinds are enumerated beyond what crosses.
		if k.String() == "" || strings.HasPrefix(k.String(), "ComponentKind(") {
			t.Errorf("kind %d does not name itself, so a refusal naming it would be opaque", k)
		}
	}

	// And a value of a kind the conversion does not carry refuses with ErrUnsupported rather than
	// crossing as something else. The zero value is the reachable case: it names no type at all.
	var none burroughs.ComponentValue
	_, err := burroughs.ComponentList(burroughs.ComponentTypeU32(), none)
	if err == nil {
		t.Fatal("an unconstructed element was accepted")
	}
	if errors.Is(err, burroughs.ErrUnsupported) {
		t.Errorf("the refusal for a caller's own unset value carries ErrUnsupported (%v), which means "+
			"\"this engine does not implement that yet\" and has its own CLI exit code; an embedder "+
			"matching it would wait for a release that is never coming", err)
	}
}
