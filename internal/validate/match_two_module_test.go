// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package validate

import (
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// This file asserts the two properties `match.go`'s generalization to two type contexts turns on,
// neither of which the spec suite witnesses:
//
//   - **The two-module direction has no board witness at all** while module-definition
//     instantiation is unscored (#367), which is how grave #368 survived — a linker comparing type
//     *indices* across two type sections was refusing four modules the reference links, on a board
//     with no column for them. `MatchDefType`/`MatchValType` are the exported entry points that
//     grave installed, so they are asserted directly.
//   - **`matchDeclaredSupertypes`' structural rung has no witness in either lane.** Both boards are
//     bit-identical before and after that rung was added, so *a control isn't born until it has been
//     watched die* leaves exactly one option: drive it from a constructed module.
//
// Every fixture below sets `RecStart`/`RecLen` explicitly, because those two fields *are* the rolled
// form's identity (see `sameDefType`) and a fixture that leaves them zero is asserting something
// about one enormous rec group rather than about the types it looks like it declares.

// singletons builds a module whose every type is its own one-member rec group, which is what a
// sequence of plain `(type …)` declarations decodes to.
func singletons(cts ...binary.CompType) *binary.Module {
	m := &binary.Module{Types: make([]binary.CompType, len(cts))}
	for i, ct := range cts {
		ct.RecStart = uint32(i)
		ct.RecLen = 1
		m.Types[i] = ct
	}
	return m
}

func fn(params, results []binary.ValType) binary.CompType {
	return binary.CompType{
		Kind: binary.CompFunc,
		Func: binary.FuncType{Params: params, Results: results},
	}
}

// TestMatchDefTypeAcrossModulesIgnoresIndices is grave #368's own assertion, in the direction the
// grave was wrong in: the relation must answer from the types the indices *name*, and the two
// indices carry no information about each other.
//
// Both halves matter and they fail in opposite directions:
//
//   - identical types at *different* indices must match — the over-rejection the linker produced,
//     four times, on `linking.wast`, `type-equivalence.wast` and `type-subtyping.wast`;
//   - different types at the *same* index must not — the under-rejection an `==` gets right by
//     coincidence and a comparator keyed on indices alone would get wrong the moment the two
//     modules' sections diverge.
func TestMatchDefTypeAcrossModulesIgnoresIndices(t *testing.T) {
	// The supplier declares the type of interest at index 2; the importer at index 0. Nothing else
	// about the two modules agrees.
	supplier := singletons(
		fn(nil, nil),
		fn([]binary.ValType{binary.I64}, nil),
		fn([]binary.ValType{binary.I32}, []binary.ValType{binary.F64}),
	)
	importer := singletons(
		fn([]binary.ValType{binary.I32}, []binary.ValType{binary.F64}),
	)

	if !MatchDefType(supplier, 2, importer, 0) {
		t.Error("MatchDefType(supplier 2, importer 0) = false, want true: the two indices name the " +
			"same functype in two different type sections, which is the whole of #368 — a linker " +
			"comparing the numbers 2 and 0 refuses a module the reference links")
	}
	if MatchDefType(supplier, 0, importer, 0) {
		t.Error("MatchDefType(supplier 0, importer 0) = true, want false: index 0 names (func) in " +
			"one module and (func (param i32) (result f64)) in the other, so a relation that " +
			"agreed here would be reading the index and not the type")
	}
}

// TestMatchValTypeAcrossModulesResolvesIndexedRefs is the same assertion for the arm the table and
// global rules use — an indexed reference type, whose `Index()` is a type index and therefore means
// nothing outside its own module.
//
// `binary.ValType` fuses reftype and heaptype into one comparable word, so `==` on two indexed refs
// compares the raw index. That is the field-wise equality the table and global arms used, and it is
// why `elemType` had to grow a defining module beside it.
func TestMatchValTypeAcrossModulesResolvesIndexedRefs(t *testing.T) {
	supplier := singletons(
		fn(nil, nil),
		fn([]binary.ValType{binary.I32}, nil),
	)
	importer := singletons(
		fn([]binary.ValType{binary.I32}, nil),
	)

	gotRef := binary.RefType(1, true)
	wantRef := binary.RefType(0, true)
	if gotRef == wantRef {
		t.Fatal("the two ValTypes are equal as words, so this test could not distinguish a " +
			"structural comparison from ==; the fixture is wrong, not the relation")
	}
	if !MatchValType(supplier, gotRef, importer, wantRef) {
		t.Error("MatchValType((ref null 1) in supplier, (ref null 0) in importer) = false, want " +
			"true: both name (func (param i32)) in their own module")
	}

	other := binary.RefType(0, true)
	if MatchValType(supplier, other, importer, wantRef) {
		t.Error("MatchValType((ref null 0) in supplier, (ref null 0) in importer) = true, want " +
			"false: equal indices naming unequal types, which only an index comparison accepts")
	}
}

// TestMatchDefTypeNullabilityIsCovariantNotEqual pins the fourth of #368's rows at the relation
// rather than at the linker: `(ref func)` is a subtype of `(ref null func)`, so a const global
// supplied as non-nullable satisfies a nullable declaration. `match_null` (match.ml:37-40) is the
// arm, and `==` on `binary.ValType` — where nullability is one bit of the word — refuses it.
func TestMatchDefTypeNullabilityIsCovariantNotEqual(t *testing.T) {
	m := singletons(fn(nil, nil))

	nonNull, okNonNull := binary.AbstractRefType(binary.HeapFunc, false)
	nullable, okNullable := binary.AbstractRefType(binary.HeapFunc, true)
	if !okNonNull || !okNullable {
		t.Fatal("binary.AbstractRefType rejected HeapFunc, which is one of the twelve; the " +
			"fixture cannot be built and the assertions below would be vacuous")
	}
	if nonNull == nullable {
		t.Fatal("(ref func) and (ref null func) are the same word, so the fixture cannot " +
			"distinguish covariance from equality")
	}
	if !MatchValType(m, nonNull, m, nullable) {
		t.Error("MatchValType((ref func), (ref null func)) = false, want true: match_null admits " +
			"a non-nullable supplier for a nullable declaration — linking.wast:112's own row")
	}
	if MatchValType(m, nullable, m, nonNull) {
		t.Error("MatchValType((ref null func), (ref func)) = true, want false: the relation is " +
			"covariant, not symmetric, and a mutable global's arm relies on that asymmetry")
	}
}

// TestMatchDefTypeSupertypeWalkComparesStructurally drives `matchDeclaredSupertypes`' middle rung —
// the one both boards are silent about.
//
// The shape: got's declared supertype is a *different index* from want, naming a *structurally
// identical* type. The reference reaches this through `match_heaptype c (UseHT ut1) (UseHT (Def
// dt2))`, which lands back in `match_deftype` and therefore gets all three disjuncts at every rung;
// an implementation that climbed the chain asking only `sup == want` has the first disjunct and the
// third and not the second, and refuses this module.
//
// The falsification is the second assertion, not a comment claiming one: the same walk must still
// refuse a chain whose supertype is genuinely a different type, or "it matches" would be reporting
// the walk's own exhaustion rather than an agreement.
func TestMatchDefTypeSupertypeWalkComparesStructurally(t *testing.T) {
	// idx 0 and idx 1 are two separate declarations of the same functype; idx 2 declares idx 1 as
	// its supertype. Asking whether idx 2 matches idx 0 has to go through the structural rung: idx 2
	// is not idx 0, is not structurally equal to it (it has a supertype and idx 0 does not), and its
	// only declared supertype is a different index.
	m := singletons(
		fn([]binary.ValType{binary.I32}, nil),
		fn([]binary.ValType{binary.I32}, nil),
		binary.CompType{
			Kind:       binary.CompFunc,
			Func:       binary.FuncType{Params: []binary.ValType{binary.I32}},
			Supertypes: []uint32{1},
		},
	)
	if !MatchDefType(m, 2, m, 0) {
		t.Error("MatchDefType(2, 0) = false, want true: type 2's declared supertype is type 1, " +
			"which is a separate declaration of the same functype as type 0 — the reference's " +
			"disjunct-2-inside-disjunct-3 reading, and the rung an index comparison skips")
	}

	// The same walk, one type changed: idx 1 now names a genuinely different functype, so no rung of
	// the chain agrees and the answer must be false. Without this the assertion above would pass for
	// a `matchDeclaredSupertypes` that returned true unconditionally.
	differs := singletons(
		fn([]binary.ValType{binary.I32}, nil),
		fn([]binary.ValType{binary.F32}, nil),
		binary.CompType{
			Kind:       binary.CompFunc,
			Func:       binary.FuncType{Params: []binary.ValType{binary.I32}},
			Supertypes: []uint32{1},
		},
	)
	if MatchDefType(differs, 2, differs, 0) {
		t.Error("MatchDefType(2, 0) = true, want false: type 2's only declared supertype names " +
			"(func (param f32)) and the target is (func (param i32)), so a true here means the " +
			"walk is agreeing rather than comparing")
	}
}

// TestMatchDefTypeSupertypeWalkTerminatesOnACycle is the depth bound's own witness. A `Supertypes`
// list pointing at itself is a module `check_subtype_sub` would reject — but this relation runs
// *during* that check and from the linker, whose supplier module this build may not have validated,
// so "it cannot happen" is the reasoning that makes it happen.
//
// A hang is the failure mode, so the assertion is that the call returns at all; `go test`'s own
// timeout is the instrument that catches the negative.
func TestMatchDefTypeSupertypeWalkTerminatesOnACycle(t *testing.T) {
	cyclic := singletons(
		fn(nil, nil),
		binary.CompType{Kind: binary.CompFunc, Supertypes: []uint32{2}},
		binary.CompType{Kind: binary.CompFunc, Supertypes: []uint32{1}},
	)
	if MatchDefType(cyclic, 1, cyclic, 0) {
		t.Error("MatchDefType(1, 0) = true, want false: neither rung of the 1→2→1 cycle names " +
			"type 0, and a true here would mean the bound is being read as an agreement")
	}
}
