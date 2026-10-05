// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"testing"
)

// Witnesses for the signature resolution the bridge needs (#903). Three claims, three failure modes:
// the index space is the right one, the resolver reaches every compound, and a resolved functype is
// still the functype it was.

// TestSectionTypeAtResolvesThroughTheIndexSpaceNotTheSection is the one that matters most, because the
// failure it guards against is **silently wrong rather than an error**.
//
// A top-level `VRef` is an ordinal into the component's type-index *space*. `c.Types` is a compacted
// subset of that space — an alias contributes to the space without appending to the section — so for any
// ordinal past the first interleaved alias, `c.Types[ord]` names a **different type that exists**. No
// bound is exceeded, no error is returned, and the signature comes out concrete and wrong.
//
// The fixture is the smallest component that distinguishes the two: space ordinal 0 is an alias, so
// ordinal 1 is section item 0 and ordinal 2 is section item 1. Direct indexing of `c.Types` by ordinal 2
// would yield item 2's type — a `string` — where the space says `u64`.
func TestSectionTypeAtResolvesThroughTheIndexSpaceNotTheSection(t *testing.T) {
	c := &Component{
		Types: []TypeDef{
			{Kind: TDVal, Val: ValType{Kind: VU32}},    // section item 0, space ordinal 1
			{Kind: TDVal, Val: ValType{Kind: VU64}},    // section item 1, space ordinal 2
			{Kind: TDVal, Val: ValType{Kind: VString}}, // section item 2, space ordinal 3
		},
		Defs: []Def{
			{Space: SpaceType, Section: SectionAlias, Item: 0}, // ordinal 0: an alias, not a section type
			{Space: SpaceType, Section: SectionType, Item: 0},  // ordinal 1 -> item 0 (u32)
			{Space: SpaceType, Section: SectionType, Item: 1},  // ordinal 2 -> item 1 (u64)
			{Space: SpaceType, Section: SectionType, Item: 2},  // ordinal 3 -> item 2 (string)
		},
	}

	// The premise: the two indexings genuinely disagree here. Asserted rather than assumed, because if
	// the fixture ever stopped being skewed this test would pass while checking nothing — the shape of a
	// control that is green because its specimen became uninteresting.
	if c.Types[2].Val.Kind == c.Types[c.typeSpaceToTypes(2)].Val.Kind {
		t.Fatal("the fixture is not skewed: indexing the section directly by ordinal 2 gives the same " +
			"type as going through the space map, so this control cannot distinguish them")
	}

	at := c.sectionTypeAt()
	got := resolveVal(ValType{Kind: VRef, Ref: 2}, at, nil)
	if got.Kind != VU64 {
		t.Fatalf("space ordinal 2 resolved to %s, want u64. Resolving it as a direct c.Types index gives "+
			"%s, which is the wrong-type-that-exists this lookup prevents.",
			valKindName(got.Kind), valKindName(c.Types[2].Val.Kind))
	}

	// An ordinal naming the alias does not resolve, and is left as a reference rather than guessed at.
	if got := resolveVal(ValType{Kind: VRef, Ref: 0}, at, nil); got.Kind != VRef {
		t.Fatalf("ordinal 0 names an alias, not a parsed type; resolveVal returned %s rather than "+
			"leaving the reference alone", valKindName(got.Kind))
	}

	// And an out-of-range ordinal likewise.
	if got := resolveVal(ValType{Kind: VRef, Ref: 99}, at, nil); got.Kind != VRef {
		t.Fatalf("out-of-range ordinal 99 returned %s rather than leaving the reference alone",
			valKindName(got.Kind))
	}
}

// TestResolveValReachesRecordFieldsAndTupleElements covers the arms that were absent.
//
// `resolveVal` inlined list, option, result and variant and **not** record or tuple, so a record field
// or tuple element that was itself a reference stayed one while every sibling kind inlined. A resolver
// that reaches four of six compounds produces output indistinguishable by shape from a fully resolved
// type, which is why this is a witness and not a comment.
func TestResolveValReachesRecordFieldsAndTupleElements(t *testing.T) {
	local := []TypeDef{
		{Kind: TDVal, Val: ValType{Kind: VU32}},    // index 0
		{Kind: TDVal, Val: ValType{Kind: VString}}, // index 1
	}
	at := sliceTypeAt(local)

	t.Run("record-fields", func(t *testing.T) {
		in := ValType{Kind: VRecord, Fields: []NamedVal{
			{Name: "a", Type: ValType{Kind: VRef, Ref: 0}},
			{Name: "b", Type: ValType{Kind: VRef, Ref: 1}},
		}}
		got := resolveVal(in, at, nil)
		if len(got.Fields) != 2 {
			t.Fatalf("resolved record has %d field(s), want 2", len(got.Fields))
		}
		if got.Fields[0].Type.Kind != VU32 || got.Fields[1].Type.Kind != VString {
			t.Fatalf("record fields resolved to %s/%s, want u32/string",
				valKindName(got.Fields[0].Type.Kind), valKindName(got.Fields[1].Type.Kind))
		}
		// The names survive the rebuild — a resolver that rebuilt the slice and dropped the labels would
		// satisfy every type assertion above and produce an unnameable record.
		if got.Fields[0].Name != "a" || got.Fields[1].Name != "b" {
			t.Fatalf("record field names resolved to %q/%q, want a/b", got.Fields[0].Name, got.Fields[1].Name)
		}
	})

	t.Run("tuple-elements", func(t *testing.T) {
		in := ValType{Kind: VTuple, Elems: []ValType{{Kind: VRef, Ref: 1}, {Kind: VRef, Ref: 0}}}
		got := resolveVal(in, at, nil)
		if len(got.Elems) != 2 {
			t.Fatalf("resolved tuple has %d element(s), want 2", len(got.Elems))
		}
		if got.Elems[0].Kind != VString || got.Elems[1].Kind != VU32 {
			t.Fatalf("tuple elements resolved to %s/%s, want string/u32",
				valKindName(got.Elems[0].Kind), valKindName(got.Elems[1].Kind))
		}
	})

	// Nested one deeper: a record inside a tuple. The recursion has to carry `at` and `seen` down, and a
	// one-level-only implementation passes both sub-tests above.
	t.Run("a-record-inside-a-tuple", func(t *testing.T) {
		in := ValType{Kind: VTuple, Elems: []ValType{
			{Kind: VRecord, Fields: []NamedVal{{Name: "x", Type: ValType{Kind: VRef, Ref: 0}}}},
		}}
		got := resolveVal(in, at, nil)
		if len(got.Elems) != 1 || len(got.Elems[0].Fields) != 1 {
			t.Fatalf("nested shape did not survive resolution: %+v", got)
		}
		if k := got.Elems[0].Fields[0].Type.Kind; k != VU32 {
			t.Fatalf("the record field inside a tuple resolved to %s, want u32", valKindName(k))
		}
	})

	// The input must not be mutated: `resolveVal` takes its argument by value but the compound slices are
	// shared, so an in-place write would corrupt the decoded type for every other reader of it.
	t.Run("the-input-type-is-not-mutated", func(t *testing.T) {
		in := ValType{Kind: VRecord, Fields: []NamedVal{{Name: "a", Type: ValType{Kind: VRef, Ref: 0}}}}
		_ = resolveVal(in, at, nil)
		if in.Fields[0].Type.Kind != VRef {
			t.Fatalf("resolveVal mutated its input: the field is now %s, and the decoded type is shared",
				valKindName(in.Fields[0].Type.Kind))
		}
	})
}

// TestResolveFuncCarriesAsync pins the field the old implementation dropped.
//
// `resolveFunc` built `&FuncType{Params: …}` and set `Result`, so `Async` was silently false on every
// resolved signature. It cost nothing while the only caller was the instance-export path, which never
// read it back; the lift path does, and a resolved signature that forgot it was a `0x43` async functype
// is a different type than the one decoded.
func TestResolveFuncCarriesAsync(t *testing.T) {
	at := sliceTypeAt(nil)
	for _, async := range []bool{true, false} {
		in := &FuncType{Params: []NamedVal{{Name: "x", Type: ValType{Kind: VU32}}}, Async: async}
		got := resolveFunc(in, at)
		if got.Async != async {
			t.Fatalf("resolveFunc dropped Async: in=%v out=%v", async, got.Async)
		}
		if len(got.Params) != 1 || got.Params[0].Name != "x" {
			t.Fatalf("resolveFunc lost the parameter list: %+v", got.Params)
		}
	}
}
