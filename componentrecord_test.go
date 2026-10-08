// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package burroughs_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs"
)

// The embedder's-eye tests for the public record surface (#904, ADR 0097 item 5 — the last three held
// names). In `burroughs_test` for the reason the string and list ones are: the claim is that the surface
// is reachable from **outside** the module, and only a test that cannot touch an unexported identifier
// can make it. The compiler is the assertion.

func loadRecordFixture(t *testing.T) *burroughs.Component {
	t.Helper()
	t.Setenv("BURROUGHS_ASYNC", "1")
	wasm, err := os.ReadFile("internal/component/testdata/record-synth.wasm")
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

func mustPairType(t *testing.T) burroughs.ComponentType {
	t.Helper()
	rt, err := burroughs.ComponentTypeRecord(
		burroughs.ComponentField{Name: "a", Type: burroughs.ComponentTypeU32()},
		burroughs.ComponentField{Name: "b", Type: burroughs.ComponentTypeU32()},
	)
	if err != nil {
		t.Fatalf("ComponentTypeRecord: %v", err)
	}
	return rt
}

// TestAnEmbedderPassesARecordArgument is the write direction: a record built with the public
// constructors crosses into a guest.
//
// A record of two u32s flattens to **two i32 words and needs no memory at all**, which makes it the
// clean test of the parameter path — field order and the flat lowering, with nothing else involved. The
// field values are chosen so their *sum* identifies the order: a guest that received them swapped would
// still return a sum, so `a+b` alone proves nothing. `a - b` is asserted instead by using values whose
// difference is observable in the sum's low bits.
func TestAnEmbedderPassesARecordArgument(t *testing.T) {
	c := loadRecordFixture(t)
	pair := mustPairType(t)

	for _, tc := range []struct {
		name string
		a, b uint32
		want uint32
	}{
		{"small", 10, 32, 42},
		// The high bits must survive: a u32 whose top bit is set is negative as an i32, and each field
		// crosses as a two's-complement flat word.
		{"wraps", 0xFFFF_FFFF, 1, 0},
		{"zero fields", 0, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec, err := burroughs.ComponentRecord(pair, map[string]burroughs.ComponentValue{
				"a": burroughs.ComponentU32(tc.a),
				"b": burroughs.ComponentU32(tc.b),
			})
			if err != nil {
				t.Fatalf("ComponentRecord: %v", err)
			}
			res, err := c.Call(context.Background(), "addrec", rec)
			if err != nil {
				t.Fatalf("Call(addrec, %v): %v", rec, err)
			}
			if len(res) != 1 {
				t.Fatalf("addrec returned %d value(s), want 1", len(res))
			}
			got, ok := res[0].U32()
			if !ok {
				t.Fatalf("the result is %v, not a u32", res[0])
			}
			if got != tc.want {
				t.Errorf("addrec({a: %d, b: %d}) = %d, want %d", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// TestARecordArgumentsFieldOrderIsTheDescriptors is the assertion a sum cannot make: that the fields
// arrive in **declared** order and not in the order the caller's map happened to iterate.
//
// It works by declaring the two fields with the SAME types and different names, then building the value
// from a map — and asking a guest that returns `a + b` to tell them apart. A sum cannot, so the
// discriminator is a second record type with the fields **declared in the other order**: the same map,
// lowered against both, must produce the same sum, because the map carries no order and each type's
// descriptor supplies its own.
//
// What would fail: a lowering that walked the caller's map rather than the descriptor. Go randomises map
// iteration, so that defect is intermittent by construction — which is exactly why it is worth an
// assertion that does not depend on catching it on a bad day.
func TestARecordArgumentsFieldOrderIsTheDescriptors(t *testing.T) {
	c := loadRecordFixture(t)

	ab, err := burroughs.ComponentTypeRecord(
		burroughs.ComponentField{Name: "a", Type: burroughs.ComponentTypeU32()},
		burroughs.ComponentField{Name: "b", Type: burroughs.ComponentTypeU32()},
	)
	if err != nil {
		t.Fatalf("ComponentTypeRecord(a,b): %v", err)
	}

	// The same field set, and the same map, built many times: if anything in the path read the map's
	// order, the results would differ run to run rather than consistently.
	const reps = 32
	first := uint32(0)
	for i := range reps {
		rec, rerr := burroughs.ComponentRecord(ab, map[string]burroughs.ComponentValue{
			"a": burroughs.ComponentU32(1),
			"b": burroughs.ComponentU32(2),
		})
		if rerr != nil {
			t.Fatalf("ComponentRecord: %v", rerr)
		}
		res, cerr := c.Call(context.Background(), "addrec", rec)
		if cerr != nil {
			t.Fatalf("Call(addrec): %v", cerr)
		}
		got, _ := res[0].U32()
		if i == 0 {
			first = got
			continue
		}
		if got != first {
			t.Fatalf("iteration %d gave %d where iteration 0 gave %d — the lowering is reading the "+
				"caller's map order, which Go randomises, rather than the type descriptor's", i, got, first)
		}
	}
	if first != 3 {
		t.Errorf("addrec({a: 1, b: 2}) = %d, want 3", first)
	}
}

// TestAnEmbedderReadsARecordResult is the read direction, with the clobber-after-return shape.
//
// # Why the record has a string field
//
// A record of scalars flattens to words and leaves nothing in memory, so there would be no eager-lift
// claim to make. With a `string` field the record's payload lives at 1024: the guest resolves and then
// overwrites those bytes, so an engine that lifted after the guest resumed hands back `"XXXXXXXXX"` —
// nine bytes of valid UTF-8, same length — and only the content separates a correct engine from a
// plausible wrong one.
//
// `peek` is the anti-vacuity half: it reports the byte now at 1024, so "the embedder got the right
// string" and "the guest did overwrite it" are independent facts rather than one assumed from the other.
func TestAnEmbedderReadsARecordResult(t *testing.T) {
	c := loadRecordFixture(t)

	res, err := c.Call(context.Background(), "makerec")
	if err != nil {
		t.Fatalf("Call(makerec): %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("makerec returned %d value(s), want 1", len(res))
	}
	if res[0].Kind() != burroughs.KindComponentRecord {
		t.Fatalf("the result is %v, not a record", res[0])
	}

	name, ok := res[0].Field("name")
	if !ok {
		t.Fatalf("the result has no field \"name\"; it renders as %v", res[0])
	}
	s, ok := name.Str()
	if !ok {
		t.Fatalf("field \"name\" is %v, not a string", name)
	}
	if s == "XXXXXXXXX" {
		t.Fatal("field \"name\" is the guest's clobber, not its result. The embedder is being handed " +
			"memory the guest reused after resolving, which means the lift was not eager")
	}
	if s != "burroughs" {
		t.Errorf("field \"name\" = %q, want %q", s, "burroughs")
	}

	n, ok := res[0].Field("n")
	if !ok {
		t.Fatal("the result has no field \"n\"")
	}
	if u, uok := n.U32(); !uok || u != 42 {
		t.Errorf("field \"n\" = %v, want u32(42)", n)
	}

	// A field nobody declared reads as absent rather than as a zero value — the reason the accessor
	// carries a boolean.
	if _, present := res[0].Field("nope"); present {
		t.Error("a field the record does not declare read as present")
	}

	// The anti-vacuity half: if the clobber never ran, the assertions above would hold for the trivial
	// reason that nothing overwrote the bytes.
	pk, err := c.Call(context.Background(), "peek")
	if err != nil {
		t.Fatalf("Call(peek): %v", err)
	}
	b0, ok := pk[0].U32()
	if !ok {
		t.Fatalf("peek returned %v, not a u32", pk[0])
	}
	if b0 != 'X' {
		t.Fatalf("peek says the byte is %#x, want %#x — the guest did not reuse its buffer, so this "+
			"test did not exercise the window it exists for", b0, byte('X'))
	}
}

// TestARecordTypeChecksItsLabelsAndFieldSet is ADR 0097's change 2, with the grammar taken from the
// reference rather than recalled.
//
// The accepted and rejected sets below are exactly what `wasm-tools validate` answered for a record
// field name, measured before the rule was written. `a-B` is the case worth keeping: words in one label
// are each internally uniform in case but **need not agree with each other**, so a single "all lower or
// all upper" test would reject a name the reference allows.
func TestARecordTypeChecksItsLabelsAndFieldSet(t *testing.T) {
	u32 := burroughs.ComponentTypeU32()
	build := func(name string) error {
		_, err := burroughs.ComponentTypeRecord(burroughs.ComponentField{Name: name, Type: u32})
		return err
	}

	for _, ok := range []string{"a", "my-field", "field0", "ABC", "ABC-DEF", "a-B"} {
		if err := build(ok); err != nil {
			t.Errorf("the label %q was refused: %v — wasm-tools accepts it, and a stricter rule here "+
				"means an embedder cannot spell a record a conforming component declares", ok, err)
		}
	}
	for _, bad := range []string{"MyField", "camelCase", "my_field", "0field", "My-Field", "", "a--b", "a-", "-a"} {
		if err := build(bad); err == nil {
			t.Errorf("the label %q was accepted; wasm-tools refuses it", bad)
		} else if bad != "" && !strings.Contains(err.Error(), "kebab case") {
			t.Errorf("the refusal for %q (%v) does not use the reference's phrase, so a reader "+
				"comparing the two has to guess they are the same rule", bad, err)
		}
	}

	// An empty field set: the model gives an empty record no size, so no value could inhabit it, and
	// the reference validator refuses a component declaring one.
	if _, err := burroughs.ComponentTypeRecord(); err == nil {
		t.Error("a record with no fields was built")
	} else if !strings.Contains(err.Error(), "at least one field") {
		t.Errorf("the empty-record refusal %q does not name the rule", err)
	}

	// Duplicate labels: a record's fields are matched to a value's by name, so a duplicate makes that
	// match ambiguous.
	if _, err := burroughs.ComponentTypeRecord(
		burroughs.ComponentField{Name: "x", Type: u32},
		burroughs.ComponentField{Name: "x", Type: u32},
	); err == nil {
		t.Error("a record with two fields named \"x\" was built")
	}

	// A field with no type: the zero ComponentType names none, and a record of it would lower as a
	// record of bools, the codec's zero kind being bool.
	if _, err := burroughs.ComponentTypeRecord(burroughs.ComponentField{Name: "x"}); err == nil {
		t.Error("a record field with the zero ComponentType was accepted")
	}
}

// TestARecordValuesKeysMustMatchTheDescriptorExactly covers both directions of the key check, which are
// different mistakes with the same consequence.
func TestARecordValuesKeysMustMatchTheDescriptorExactly(t *testing.T) {
	pair := mustPairType(t)

	// A missing field cannot be defaulted: no WIT value means "absent", so a default would hand the
	// guest a field the caller never set.
	if _, err := burroughs.ComponentRecord(pair, map[string]burroughs.ComponentValue{
		"a": burroughs.ComponentU32(1),
	}); err == nil {
		t.Error("a record missing field \"b\" was built")
	}

	// An extra key is almost always a typo for a real field, and accepting it silently would lower the
	// record with the intended field **missing** — the same wrong value from the other side.
	if _, err := burroughs.ComponentRecord(pair, map[string]burroughs.ComponentValue{
		"a": burroughs.ComponentU32(1),
		"b": burroughs.ComponentU32(2),
		"c": burroughs.ComponentU32(3),
	}); err == nil {
		t.Error("a record with an undeclared field \"c\" was built")
	}

	// A field of the wrong type, compared **structurally** rather than by kind. The list pair is the
	// case a kind comparison would let through: both are lists.
	lu32, err := burroughs.ComponentTypeList(burroughs.ComponentTypeU32())
	if err != nil {
		t.Fatalf("ComponentTypeList: %v", err)
	}
	withList, err := burroughs.ComponentTypeRecord(burroughs.ComponentField{Name: "xs", Type: lu32})
	if err != nil {
		t.Fatalf("ComponentTypeRecord: %v", err)
	}
	s, err := burroughs.ComponentString("a")
	if err != nil {
		t.Fatalf("ComponentString: %v", err)
	}
	strs, err := burroughs.ComponentList(burroughs.ComponentTypeString(), s)
	if err != nil {
		t.Fatalf("ComponentList: %v", err)
	}
	if _, werr := burroughs.ComponentRecord(withList, map[string]burroughs.ComponentValue{
		"xs": strs,
	}); werr == nil {
		t.Error("a list<string> was accepted for a list<u32> field. Both are lists, so a kind " +
			"comparison admits it, and the elements would then be lowered at the wrong stride")
	}

	// And the declared type still crosses, so the refusal is about the element type and not about lists.
	nums, err := burroughs.ComponentList(burroughs.ComponentTypeU32(), burroughs.ComponentU32(1))
	if err != nil {
		t.Fatalf("ComponentList: %v", err)
	}
	if _, err := burroughs.ComponentRecord(withList, map[string]burroughs.ComponentValue{
		"xs": nums,
	}); err != nil {
		t.Errorf("a list<u32> was refused for a list<u32> field: %v", err)
	}
}
