// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// The authority control for `typestring.go`, and why its expectations are **transcribed** rather
// than derived from the reference at run time.
//
// `internal/testenv` licenses reference files as authorities — a path constant plus a size floor,
// policed by `TestFetchScriptAssertsEveryAuthority` — and `syntax/types.ml` is deliberately **not**
// among them. The precedent is `internal/validate/align_authority_test.go:41`, which transcribes
// nine numbers out of `pack.ml`/`types.ml` and says why: the derive-don't-transcribe rule is about
// errors that are *accept-direction and invisible*, and a wrong transcription here cannot be one.
// A wrong spelling below disagrees with an implementation written from the same lines by a
// different reader and **the test fails, loudly, in the only direction it can fail**. Licensing a
// fifteenth authority to re-derive fifteen string constants would buy a governance step and no
// coverage.
//
// So every row cites the `types.ml` line its `want` was read off, and the whole table is one claim:
// *these are the strings the reference prints*. The rows were transcribed from
// `third_party/spec/interpreter/syntax/types.ml` at the pin `scripts/fetch-spec-ref.sh` carries;
// when that pin moves, a changed spelling arrives here as a failure, which is the point.
//
// # Why the spellings are worth a control at all
//
// Grave #368's fourth row was `expected global const funcref, got global const (ref func)` — a
// refusal whose *testimony* named two types that differ only in a null bit, in two different
// notations, one of which (`const`, `funcref`) the reference does not use. The verdict was also
// wrong, and the fix is the matcher; but a message that cannot be compared to the reference's own
// output is a witness the reader cannot check, and the fix's own error text is the one part of the
// change no board scores. This table is that part's oracle.

// spellFixture is the module every non-chain row below is rendered against. Laid out as explicit
// `CompType` values rather than through a builder, because `RecStart`/`RecLen` *are* the rolled
// form's identity (see `binary.CompType`'s field comment) and a fixture that computed them would be
// asserting the renderer against a second guess at the same fact.
//
//	0  (type (func (param i32) (result f64)))                    singleton, final, no supertypes
//	1  (rec (type (sub (func)))                                  member 0 of a two-member group
//	2       (type (struct (field (mut (ref null 1))))))           member 1; ref into the group
//	3  (type (array i8))                                         packed, immutable
//	4  (type (sub 1 (func)))                                     declared supertype, not final
//	5  (type (struct))                                           the empty-struct arm
//	6  (type (struct (field (mut i16)) (field i32)))              two fields, mixed mutability
//	7  (type (sub final 0 (func)))                               `sub final` with a supertype
func spellFixture() *binary.Module {
	return &binary.Module{Types: []binary.CompType{
		{
			Kind: binary.CompFunc, Final: true, RecStart: 0, RecLen: 1,
			Func: binary.FuncType{
				Params:  []binary.ValType{binary.I32},
				Results: []binary.ValType{binary.F64},
			},
		},
		{Kind: binary.CompFunc, Final: false, RecStart: 1, RecLen: 2},
		{
			Kind: binary.CompStruct, Final: true, RecStart: 1, RecLen: 2,
			Fields: []binary.FieldType{{
				Storage: binary.StorageType{Val: binary.RefType(1, true)},
				Mutable: true,
			}},
		},
		{
			Kind: binary.CompArray, Final: true, RecStart: 3, RecLen: 1,
			Fields: []binary.FieldType{{
				Storage: binary.StorageType{Packed: true, Width: 8},
			}},
		},
		{Kind: binary.CompFunc, Final: false, RecStart: 4, RecLen: 1, Supertypes: []uint32{1}},
		{Kind: binary.CompStruct, Final: true, RecStart: 5, RecLen: 1},
		{
			Kind: binary.CompStruct, Final: true, RecStart: 6, RecLen: 1,
			Fields: []binary.FieldType{
				{Storage: binary.StorageType{Packed: true, Width: 16}, Mutable: true},
				{Storage: binary.StorageType{Val: binary.I32}},
			},
		},
		{Kind: binary.CompFunc, Final: true, RecStart: 7, RecLen: 1, Supertypes: []uint32{0}},
	}}
}

// spellRecGroup is types 1 and 2 as `string_of_rectype`'s multi-member arm prints them
// (types.ml:392-396): `"rec " ^ concat " " (map (fun st -> "(" ^ string_of_subtype st ^ ")"))`,
// wrapped by `string_of_deftype`'s second arm (types.ml:400) which adds the outer parentheses and
// the ordinal.
//
// Written once because three rows share it, and because the `rec.0` inside it is the fact the whole
// rolled form turns on: type 2's field references type 1, which is *in the group being defined*, so
// it prints as an ordinal and does not recurse.
const spellRecGroup = "(rec (sub (func [] -> [])) (struct (field (mut (ref null rec.0)))))"

// TestSpellerMatchesTheReferenceSpellings is the transcribed table: one row per `string_of_*`
// production `typestring.go` ports, each `want` read off the cited `types.ml` lines.
func TestSpellerMatchesTheReferenceSpellings(t *testing.T) {
	m := spellFixture()
	s := speller{mod: m}

	nullFunc, okNull := binary.AbstractRefType(binary.HeapFunc, true)
	bareFunc, okBare := binary.AbstractRefType(binary.HeapFunc, false)
	nullAny, okAny := binary.AbstractRefType(binary.HeapAny, true)
	bareNone, okNone := binary.AbstractRefType(binary.HeapNone, false)
	if !okNull || !okBare || !okAny || !okNone {
		t.Fatal("binary.AbstractRefType rejected one of func/any/none, which are three of the " +
			"twelve named forms; the fixtures cannot be built and every row below would be " +
			"comparing a zero ValType against a transcription")
	}

	rows := []struct {
		what string // the reference production, and the lines the want was transcribed from
		got  string
		want string
	}{
		{
			what: "ExternFuncT + typeuse Def + singleton deftype + bare subtype + FuncT " +
				"(types.ml:429, :334, :399, :386, :382-383)",
			got:  s.externFunc(0),
			want: "func (func [i32] -> [f64])",
		},
		{
			what: "rectype's multi-member arm, member 0, and the `sub` header with no " +
				"supertypes (types.ml:392-396, :400, :387-390)",
			got:  s.externFunc(1),
			want: "func (" + spellRecGroup + ".0)",
		},
		{
			what: "the same group, member 1: StructT with one field, and `Rec x` for the " +
				"reference into the group being defined (types.ml:377-379, :373-374, :333)",
			got:  s.externFunc(2),
			want: "func (" + spellRecGroup + ".1)",
		},
		{
			what: "ArrayT of an immutable packtype — `string_of_mut` returns the storage type " +
				"unwrapped for Cons (types.ml:380, :365-367, :314-316)",
			got:  s.externFunc(3),
			want: "func (array i8)",
		},
		{
			what: "SubT (NoFinal, [ut], ct): the header is bare `sub`, and the supertype is a " +
				"typeuse, so its own parentheses nest inside (types.ml:387-390, :310-312)",
			got:  s.externFunc(4),
			want: "func (sub (" + spellRecGroup + ".0) (func [] -> []))",
		},
		{
			what: "StructT [] — a distinct arm, not `struct ` with an empty join " +
				"(types.ml:377)",
			got:  s.externFunc(5),
			want: "func (struct)",
		},
		{
			what: "StructT with two fields: `(field …)` per member, mutability wrapping the " +
				"storage type and not prefixing it (types.ml:378-379, :373-374)",
			got:  s.externFunc(6),
			want: "func (struct (field (mut i16)) (field i32))",
		},
		{
			what: "SubT (Final, [ut], ct): `string_of_final Final` is \" final\", a leading " +
				"space appended to `sub` (types.ml:310-312, :387-390)",
			got:  s.externFunc(7),
			want: "func (sub final (func [i32] -> [f64]) (func [] -> []))",
		},
		{
			what: "ExternTagT through string_of_tagtype, which is string_of_typeuse of the " +
				"tag's deftype (types.ml:425, :407-408)",
			got:  s.externTag(0),
			want: "tag (func [i32] -> [f64])",
		},
		{
			what: "ExternGlobalT, Cons, non-nullable reftype — #368's fourth row, whose old " +
				"spelling was `global const funcref` (types.ml:426, :410-411, :352-353)",
			got:  s.externGlobal(false, bareFunc),
			want: "global (ref func)",
		},
		{
			what: "ExternGlobalT, Var, nullable reftype: `string_of_null Null` is \"null \" " +
				"with its trailing space (types.ml:306-308, :352-353, :314-316)",
			got:  s.externGlobal(true, nullFunc),
			want: "global (mut (ref null func))",
		},
		{
			what: "ExternGlobalT over a numtype — no reftype spelling involved " +
				"(types.ml:319-323, :356)",
			got:  s.externGlobal(false, binary.I32),
			want: "global i32",
		},
		{
			what: "string_of_heaptype's named arms, reached through a reftype (types.ml:337, " +
				"and :341 for `none`)",
			got:  s.externGlobal(false, nullAny) + " " + s.externGlobal(false, bareNone),
			want: "global (ref null any) global (ref none)",
		},
		{
			what: "ExternMemoryT: addrtype is the *numtype* it indexes with, and limits print " +
				"the max only when there is one (types.ml:427, :413-414, :325-326, :402-405)",
			got:  s.externMemory(binary.Limits{Min: 1}),
			want: "memory i32 1",
		},
		{
			what: "ExternMemoryT, 64-bit with a max (types.ml:413-414, :404-405)",
			got:  s.externMemory(binary.Limits{Min: 2, Max: 4, HasMax: true, Addr64: true}),
			want: "memory i64 2 4",
		},
		{
			what: "ExternTableT: addrtype, limits, element reftype (types.ml:428, :416-418)",
			got:  s.externTable(binary.Limits{Min: 0}, binary.RefType(0, true)),
			want: "table i32 0 (ref null (func [i32] -> [f64]))",
		},
	}
	for _, r := range rows {
		if r.got != r.want {
			t.Errorf("%s\n\t got: %s\n\twant: %s", r.what, r.got, r.want)
		}
	}
	if len(rows) < 16 {
		t.Fatalf("the table has %d rows: a row removed rather than fixed is how a control's "+
			"domain shrinks without its verdict changing", len(rows))
	}
}

// TestSpellerDegradesToTheIdxArmAtTheDepthBound is the one behaviour below that is *not* the
// reference's: `spellDepth` bounds a recursion the reference does not have, because its `deftype`
// values are already rolled and this port resolves indices instead.
//
// The bound's honesty is the claim under test, in both halves:
//
//   - it degrades to `string_of_typeuse`'s **`Idx x` arm** — a bare index, a real production — so a
//     reader sees an unresolved index rather than an invented marker;
//   - it degrades **at** the bound and not before it, which is what makes the message useful: eight
//     levels resolve, and the ninth is the one that does not.
//
// The `want` is built from a two-line recurrence rather than typed as one 400-character literal,
// and the recurrence is transcribed, not called: `R(8)` is the `Idx` arm and `R(k)` is `Def`'s.
// Nothing here calls the speller, so a bug in the renderer cannot travel into the expectation.
func TestSpellerDegradesToTheIdxArmAtTheDepthBound(t *testing.T) {
	// A chain: type i is `(func (param (ref null i-1)))`, so rendering type 9 needs ten levels
	// and the bound stops it at eight. Every type is its own singleton group, so no `rec.N`
	// shortcut can absorb a level.
	const chain = 10
	types := make([]binary.CompType, chain)
	for i := range chain {
		ct := binary.CompType{
			Kind: binary.CompFunc, Final: true,
			RecStart: uint32(i), RecLen: 1,
		}
		if i > 0 {
			ct.Func.Params = []binary.ValType{binary.RefType(uint32(i-1), true)}
		}
		types[i] = ct
	}
	if chain <= spellDepth {
		t.Fatalf("the chain is %d long and the bound is %d: the fixture cannot reach the bound, "+
			"so this test would be asserting the unbounded path and passing", chain, spellDepth)
	}

	s := speller{mod: &binary.Module{Types: types}}

	// R(spellDepth) is the `Idx x` arm: the type reached at the bound is type
	// `chain-1-spellDepth`, printed as its bare index with no parentheses of its own.
	want := "1"
	for range spellDepth {
		want = "(func [(ref null " + want + ")] -> [])"
	}
	want = "func " + want

	if got := s.externFunc(chain - 1); got != want {
		t.Errorf("externFunc(%d) over a %d-deep chain:\n\t got: %s\n\twant: %s\n\t"+
			"the bound must resolve exactly %d levels and then print the reference's `Idx x` arm",
			chain-1, chain, got, want, spellDepth)
	}

	// An index the module does not have degrades the same way, at `deftype` rather than at
	// `typeuse` — so it keeps the `Def` arm's parentheses. Not a reference production either:
	// whether an index resolves is the validator's question, and a message is not the place to
	// raise it a second time.
	if got, wantMissing := s.externFunc(99), "func (99)"; got != wantMissing {
		t.Errorf("externFunc(99) with 10 types: got %s, want %s: an out-of-range index must "+
			"render as the index, not panic and not claim a type", got, wantMissing)
	}
}
