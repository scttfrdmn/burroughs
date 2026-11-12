// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package canon

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// Witnesses for ADR 0116's kinds — flags, enum and option — beside the differential, which covers their
// layout. These cover what the differential cannot: the type constructors' refusals, the accessors, and
// #1245's equality.

func labelsN(prefix string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("%s%d", prefix, i)
	}
	return out
}

// mustFlags builds a flags value of n labels with the labels at the given indices set.
func mustFlags(t *testing.T, n int, set ...int) Value {
	t.Helper()
	labels := labelsN("f", n)
	ft, err := FlagsType(labels...)
	if err != nil {
		t.Fatalf("FlagsType(%d): %v", n, err)
	}
	names := make([]string, len(set))
	for i, s := range set {
		names[i] = labels[s]
	}
	v, err := Flags(ft, names...)
	if err != nil {
		t.Fatalf("Flags: %v", err)
	}
	return v
}

// mustEnum builds the enum value at index i of an n-label enum.
func mustEnum(t *testing.T, n, i int) Value {
	t.Helper()
	labels := labelsN("e", n)
	et, err := EnumType(labels...)
	if err != nil {
		t.Fatalf("EnumType(%d): %v", n, err)
	}
	v, err := Enum(et, labels[i])
	if err != nil {
		t.Fatalf("Enum: %v", err)
	}
	return v
}

func mustSome(t *testing.T, v Value) Value {
	t.Helper()
	o, err := Some(OptionType(v.Type), v)
	if err != nil {
		t.Fatalf("Some: %v", err)
	}
	return o
}

// TestFlagsTypeRefusesExactlyWhereTheModelDoes reads the flags label bound from the oracle rather than
// restating it: `gen.py` asked the pinned model for `elem_size` at each count, and recorded a refusal where
// its `assert(0 < n <= 32)` fired. So 33 and 64 labels are refusal rows — the pin has no layout for them —
// and `FlagsType` must refuse exactly those, and lay out the rest at the model's size and alignment.
func TestFlagsTypeRefusesExactlyWhereTheModelDoes(t *testing.T) {
	f := loadFixtures(t)
	if len(f.FlagsBounds) == 0 {
		t.Fatal("no flags_bounds in the fixtures: run make canon-fixtures")
	}
	var refused, laidOut int
	for _, b := range f.FlagsBounds {
		ft, err := FlagsType(labelsN("f", b.Labels)...)
		if b.Refused {
			refused++
			if err == nil {
				t.Errorf("FlagsType(%d labels) built a type the model refuses to lay out", b.Labels)
			}
			continue
		}
		laidOut++
		if err != nil {
			t.Errorf("FlagsType(%d labels) refused a type the model lays out: %v", b.Labels, err)
			continue
		}
		if got := size(ft); got != b.Size {
			t.Errorf("flags of %d labels: size %d, want %d", b.Labels, got, b.Size)
		}
		if got := alignment(ft); got != b.Align {
			t.Errorf("flags of %d labels: alignment %d, want %d", b.Labels, got, b.Align)
		}
	}
	// Both arms must have run, or the control proved half of what it says.
	if refused == 0 || laidOut == 0 {
		t.Fatalf("flags_bounds exercised %d refused and %d laid-out row(s); both must be non-zero", refused, laidOut)
	}
}

// TestLabelledTypesRefuseEmptyAndDuplicateLabels is the other half of the constructors' refusals: an empty
// label set has no layout in the model, and a duplicate makes its by-label match ambiguous.
func TestLabelledTypesRefuseEmptyAndDuplicateLabels(t *testing.T) {
	for _, c := range []struct {
		name string
		mk   func(...string) (Type, error)
	}{{"flags", FlagsType}, {"enum", EnumType}} {
		if _, err := c.mk(); err == nil {
			t.Errorf("%s with no labels was built", c.name)
		}
		_, err := c.mk("a", "b", "a")
		if err == nil {
			t.Errorf("%s with a duplicate label was built", c.name)
		} else if !strings.Contains(err.Error(), `"a"`) {
			t.Errorf("%s duplicate refusal %q does not name the label", c.name, err)
		}
		if _, err := c.mk("a", "b"); err != nil {
			t.Errorf("%s{a, b} refused: %v", c.name, err)
		}
	}
}

// TestNewKindsConstructAndReadBack covers the value constructors' refusals and the accessors.
func TestNewKindsConstructAndReadBack(t *testing.T) {
	ft, err := FlagsType("read", "write", "exec")
	if err != nil {
		t.Fatal(err)
	}
	fv, err := Flags(ft, "exec", "read")
	if err != nil {
		t.Fatal(err)
	}
	// Bit i is label i (def:pack_flags_into_int), so read|exec is 0b101.
	if fv.u != 0b101 {
		t.Errorf("flags read|exec packed to %#b, want 0b101", fv.u)
	}
	if set, ok := fv.FlagSet(); !ok || !slices.Equal(set, []string{"read", "exec"}) {
		t.Errorf("FlagSet = %v, %v; want [read exec] in declared order", set, ok)
	}
	if _, ferr := Flags(ft, "delete"); ferr == nil {
		t.Error("Flags accepted a label the type does not declare")
	}

	et, err := EnumType("io", "illegal-byte-sequence", "pipe")
	if err != nil {
		t.Fatal(err)
	}
	ev, err := Enum(et, "pipe")
	if err != nil {
		t.Fatal(err)
	}
	if l, ok := ev.EnumLabel(); !ok || l != "pipe" {
		t.Errorf("EnumLabel = %q, %v; want pipe", l, ok)
	}
	if _, eerr := Enum(et, "closed"); eerr == nil {
		t.Error("Enum accepted a label the type does not declare")
	}

	ot := OptionType(Type{Kind: KindU32})
	sv, err := Some(ot, U32(7))
	if err != nil {
		t.Fatal(err)
	}
	if p, some, ok := sv.Option(); !ok || !some || !valueEqual(p, U32(7)) {
		t.Errorf("Option of some(7) = %v, %v, %v", p, some, ok)
	}
	nv, err := None(ot)
	if err != nil {
		t.Fatal(err)
	}
	if _, some, ok := nv.Option(); !ok || some {
		t.Errorf("Option of none = some %v, ok %v; want a none", some, ok)
	}
	if _, serr := Some(ot, Str("x")); serr == nil {
		t.Error("Some accepted a string payload for option<u32>")
	}
	if _, _, ok := U32(1).Option(); ok {
		t.Error("a u32 read as an option")
	}

	// The renderings carry the labels and the element, for `Type.String`'s reason: two types with one
	// layout must not print the same.
	for got, want := range map[string]string{
		ft.String(): "flags{read, write, exec}",
		et.String(): "enum{io, illegal-byte-sequence, pipe}",
		ot.String(): "option<u32>",
	} {
		if got != want {
			t.Errorf("rendered %q, want %q", got, want)
		}
	}
	// And they compare structurally, labels included.
	other, err := EnumType("io", "pipe", "illegal-byte-sequence")
	if err != nil {
		t.Fatal(err)
	}
	if TypeEqual(et, other) {
		t.Error("two enums with the same labels in another order compared equal; the order is the discriminant")
	}
}

// TestValueEqualSeesARecordsFields is #1245's witness: two records that differ in one field must compare
// unequal, or the differential's lift and lift-flat checks compare nothing for a record.
//
// NEUTER, run: delete `valueEqual`'s `KindRecord` arm. This test fails, and `TestCodecMatchesReferenceModel`
// stays green, because without the arm every record compares 0 == 0 — the grave's specimen.
func TestValueEqualSeesARecordsFields(t *testing.T) {
	a := mustRecord(t, "small", U8v(1), "wide", U32(2))
	b := mustRecord(t, "small", U8v(1), "wide", U32(3))
	if valueEqual(a, b) {
		t.Fatal("two records differing in field `wide` compared equal; a record lift returning a wrong " +
			"field would pass the differential")
	}
	if !valueEqual(a, mustRecord(t, "small", U8v(1), "wide", U32(2))) {
		t.Fatal("two equal records compared unequal; the arm is refusing rather than comparing")
	}
	// The same defect one level in: a record inside an option and inside a list.
	if valueEqual(mustSome(t, a), mustSome(t, b)) {
		t.Error("some(record) values differing in one field compared equal")
	}
	la, err := List(a.Type, a)
	if err != nil {
		t.Fatal(err)
	}
	lb, err := List(b.Type, b)
	if err != nil {
		t.Fatal(err)
	}
	if valueEqual(la, lb) {
		t.Error("list<record> values differing in one field compared equal")
	}
}
