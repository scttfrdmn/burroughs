// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package validate

import "github.com/scttfrdmn/burroughs/internal/binary"

// This file is `third_party/spec/interpreter/valid/match.ml` — the subtype relation, ported as
// decision 0031's slice. Before it, `matches` was `got == want` plus a bottom wildcard, which is
// exactly right for the numeric families and wrong for every reference form; 0031's context
// records that the boundary was not holding a slice in reserve but twenty-one false accepts.
//
// # The port is by type index, because that is what this engine's module holds
//
// The reference's `context` is a `deftype list` and its `heaptype` carries `UseHT (Idx x)` or
// `UseHT (Def dt)` — an unresolved index or a resolved definition. `binary.Module` has one
// representation for both: `Types []CompType` indexed by type index, and `ValType.Index()`.
// So the reference's `Idx`/`Def` arms collapse into a single indexed case here, and
// `expand_deftype` is a slice lookup rather than an unrolling — there is no `Rec` indirection
// in this representation to unroll through.
//
// That collapse is *not* free, and the one place it costs something is named rather than
// implied: the reference's `Def dt` carries the whole rec group, so `match_deftype`'s
// structural-equality disjunct compares *rolled* forms in which the group's shape is part of
// the type's identity. An index alone cannot express that, which is why `binary.CompType`
// retains `RecStart`/`RecLen` and why sameDefType reads them — see sameRef for the one arm
// where a bare index comparison gives the wrong answer.
//
// # Null is read by exactly one function
//
// `binary.ValType` fuses the reference's `reftype` and `heaptype` sorts into one struct — a
// kind byte, a null bit, and an index. The reference keeps them apart, and `match_reftype` is
// the only rule that reads the null bit (`match_null` then `match_heaptype` on the halves).
// This port keeps that division by discipline instead of by the type system: **matchHeap reads
// kind and index and never the null bit**, matchRefType reads the bit and delegates. A
// heaptype rule that consulted nullability would make `(ref $t) <: (ref null $t)` and
// `(ref null $t) <: (ref $t)` both true, since the second is where the null rule is the only
// thing saying no.

// matchValType is `match_valtype` (match.ml:110-116).
//
// **The bottom arm is bidirectional here and one-directional in the reference**, which is a
// pre-existing divergence this port preserves rather than repairs. `match_valtype` has
// `BotT, _ -> true` and no mirror; `matches` has had both since slice 1. The mirror's subject is
// `want == unknown`, and it is unreachable: `unknown` enters the stack only from `pop`/`peekN`
// padding below a frame's entry height, so it is always the *got* side, and every `want` comes
// from a signature or a frame's label types, which are read from the module. Removing it is a
// behaviour change on an unreachable path, which is a different PR's risk than this one's —
// recorded here rather than silently kept, since *unreachability is a grave only when it is
// silent*.
func matchValType(m *binary.Module, got, want binary.ValType) bool {
	if got == unknown || want == unknown {
		return true
	}
	// `NumT/NumT` and `VecT/VecT` are `match_numtype`/`match_vectype`, both `t1 = t2`
	// (match.ml:67-73), and the mixed-sort cases fall to `_, _ -> false`. One equality covers
	// all three: two non-reference ValTypes are equal iff they are the same numeric or vector
	// kind, and a numeric compared against a reference is unequal.
	if !got.IsRef() || !want.IsRef() {
		return got == want
	}
	return matchRefType(m, got, want)
}

// matchRefType is `match_reftype` (match.ml:105-108): the null bit, then the heaptype.
func matchRefType(m *binary.Module, got, want binary.ValType) bool {
	return matchNull(got.Null(), want.Null()) && matchHeap(m, got, want)
}

// matchNull is `match_null` (match.ml:59-62): non-null satisfies nullable, never the reverse.
//
//	| NoNull, Null -> true
//	| _, _ -> nul1 = nul2
func matchNull(got, want bool) bool {
	return !got || want
}

// matchHeap is `match_heaptype` (match.ml:76-103).
//
// **The arm order is the reference's and is load-bearing**, in one place specifically: the four
// bottom arms (`NoneHT`, `NoFuncHT`, `NoExnHT`, `NoExternHT`) sit *above* the indexed arms, so
// `none <: (ref $s)` is decided by asking whether `(ref $s) <: any` — which the indexed-versus-
// abstract arm answers by expanding `$s`. Moving them below would answer that pair by identity
// and refuse it.
//
// Reads kind and index only; the null bit is matchRefType's, per this file's header.
func matchHeap(m *binary.Module, got, want binary.ValType) bool {
	if got == unknown {
		return true // `BotHT, _ -> true`
	}

	gk, gAbstract := got.Kind()
	wk, wAbstract := want.Kind()

	if gAbstract && wAbstract {
		if abstractSubtype(gk, wk) {
			return true
		}
	}
	// The four bottom-of-hierarchy arms. Each is `Bot* <: t` whenever `t` is in that bottom's
	// own hierarchy, expressed by the reference as a recursive call against the hierarchy's top
	// — so `want` may be indexed here and the recursion falls through to the arms below.
	if gAbstract {
		if top, ok := bottomHierarchyTop(gk); ok {
			// `when t <> BotHT`: the guard is already discharged, `got == unknown` having
			// returned above.
			if abstractRef, okTop := binary.AbstractRefType(top, false); okTop {
				return matchHeap(m, want, abstractRef)
			}
		}
	}

	switch {
	case !gAbstract && !wAbstract:
		// Both indexed: `UseHT (Def dt1), UseHT (Def dt2) -> match_deftype`.
		return matchDefType(m, got.Index(), want.Index())
	case !gAbstract && wAbstract:
		// `UseHT (Def dt), t` — expand the definition and place it in the abstract lattice.
		ct, ok := compTypeAt(m, got.Index())
		if !ok {
			// An index the module does not have. Index *validity* is check_typeuse's rule and
			// not this relation's, so the answer here is "does not match" rather than an error:
			// a relation that reported errors would have to be threaded through every operand
			// comparison, and the vector reaching this point has a refusal coming from the rule
			// that owns the question.
			return false
		}
		return abstractSubtype(abstractOfCompType(ct.Kind), wk)
	default:
		// Two cases reach here, and naming only the first would be the comment describing a
		// narrower predicate than the code has:
		//
		//   - abstract on the left, indexed on the right. The reference has no arm for it, so it
		//     falls to `_, _ -> t1 = t2`, which two different sorts cannot satisfy.
		//   - both abstract and unrelated. `abstractSubtype` above is the whole of the reference's
		//     abstract lattice *including* its `t1 = t2` fallthrough, so a false from it is the
		//     final answer and this is where that answer is returned.
		//
		// Written as an explicit false rather than as a comparison, because `got == want` on
		// ValTypes would also compare the null bit this function is forbidden to read.
		return false
	}
}

// abstractSubtype is `match_heaptype`'s abstract arms plus its `t1 = t2` fallthrough, over the
// twelve abstract heaptype kinds (match.ml:77-83, :102).
//
// The seven proper pairs are transcribed from the reference one arm per row rather than derived
// from a `top_of_heaptype`-style walk, because the reference does not derive them either: `eq`,
// `struct`, `array` and `i31` under `any`, and `i31`, `struct`, `array` under `eq`. A derived
// version would have to invent the intermediate structure the spec deliberately does not have.
func abstractSubtype(got, want byte) bool {
	if got == want {
		return true
	}
	switch want {
	case binary.HeapAny:
		switch got {
		case binary.HeapEq, binary.HeapStruct, binary.HeapArray, binary.HeapI31:
			return true
		}
	case binary.HeapEq:
		switch got {
		case binary.HeapI31, binary.HeapStruct, binary.HeapArray:
			return true
		}
	}
	// The four bottoms are *not* here even though `none <: any` holds: their arms are recursive
	// (`NoneHT, t -> match_heaptype c t AnyHT`) and so cannot be a pair table. matchHeap owns
	// them, and putting a `none`/`any` row here as well would be a second authority that agrees
	// today and drifts the first time the hierarchy grows a level.
	return false
}

// bottomHierarchyTop maps each of the four bottom heaptypes to the top of its own hierarchy —
// `match_heaptype`'s four recursive arms (match.ml:84-87).
func bottomHierarchyTop(kind byte) (byte, bool) {
	switch kind {
	case binary.HeapNone:
		return binary.HeapAny, true
	case binary.HeapNoFunc:
		return binary.HeapFunc, true
	case binary.HeapNoExn:
		return binary.HeapExn, true
	case binary.HeapNoExtern:
		return binary.HeapExtern, true
	}
	return 0, false
}

// abstractOfCompType is `abs_of_comptype` (match.ml:13-15): a struct or an array sits under
// `struct`'s and `array`'s common ancestor, a function under `func`.
//
// The reference collapses `StructT` and `ArrayT` to `StructHT` here, which reads like a typo and
// is not: `abs_of_comptype` is only ever consumed by `top_of_*`/`bot_of_*`, where both answers
// lead to `AnyHT`. This port's consumer is different — matchHeap's expand arm — so it must not
// inherit that collapse, and it does not: each kind maps to its own heaptype and
// `abstractSubtype` supplies `struct <: eq <: any` and `array <: eq <: any` from the lattice.
// Collapsing here would make `(ref $array) <: (ref struct)` true, which match.ml:96-98's own
// arms (`ArrayT _, ArrayHT` and no `ArrayT _, StructHT`) refuse.
func abstractOfCompType(k binary.CompKind) byte {
	switch k {
	case binary.CompStruct:
		return binary.HeapStruct
	case binary.CompArray:
		return binary.HeapArray
	default:
		return binary.HeapFunc
	}
}

// matchDefType is `match_deftype` (match.ml:149-155), less its structural-equality disjunct.
//
// The reference's three disjuncts:
//
//	dt1 == dt2                                             physical equality
//	subst_deftype s dt1 = subst_deftype s dt2              structural equality of canonical forms
//	exists ut1 in dt1's supertypes: ut1 <: dt2             the declared-supertype walk
//
// The first is index equality here, and the third is the walk below. **The second is absent, and
// it is absent by representation rather than by choice** — the same shape as the table
// initializer's missing const check in `module.go`, and named the same way so the next slice
// finds the gap where it can see it.
//
// `subst_deftype` compares *rolled* canonical forms: a deftype is a whole rec group plus the
// member's ordinal within it, so `(rec (type $a (func)) (type (struct)))` and
// `(rec (type (struct)) (type $b (func)))` hold two structurally identical functypes that are
// **not** equal types, and neither are `$a` and the `(func)` in a three-member group.
// `binary.Module` retains no rec-group boundaries at all — `decodeRecType` reads the group and
// appends flat `CompType`s — so this function cannot compute the reference's answer, and the
// available approximation (coinductive bisimulation over indices) is the *equi*-recursive
// relation, which is strictly coarser and wrong on exactly those vectors. Implementing the
// approximation would convert five `type-equivalence.wast` over-rejections and be wrong in the
// accepting direction, which is the worse half.
//
// All three are implemented. The second needed `binary.CompType.RecStart`/`RecLen`, which
// `decodeRecType` did not retain — see sameDefType for what the extent is *for*, and
// `binary.CompType` for why the flat comptypes alone admit only the coarser equi-recursive
// relation.
//
// **The three disjuncts are not independent, and the measurement that says so is worth keeping.**
// `checkTypes` was written first, with the first and third disjuncts only, and it converted all
// 21 reject-direction vectors *and created five new over-rejections* on modules that had been
// passing (`type-subtyping.wast:124,159,632,682,980`), because a declared supertype comparison
// reaches structurally-equal-but-differently-indexed types immediately. So the relation could not
// land one disjunct at a time: the reject direction's rule is a consumer of the equality the
// accept direction's rows are the witnesses for.
func matchDefType(m *binary.Module, got, want uint32) bool {
	if got == want {
		return true
	}
	if sameDefType(m, got, want, 0) {
		return true
	}
	return matchDeclaredSupertypes(m, got, want, 0)
}

// sameDefType is `match_deftype`'s structural-equality disjunct — `subst_deftype s dt1 =
// subst_deftype s dt2` (match.ml:151) — computed directly rather than by materializing the
// substituted forms.
//
// # What "rolled" means, because the whole rule is in it
//
// The reference canonicalizes a rec group by `roll_deftypes`, which rewrites every reference
// *into the group being defined* as `Rec i` — an ordinal — while references out of the group stay
// as resolved definitions. Two deftypes are equal iff those rolled forms are equal, which makes
// the comparison finite (the cycles have become ordinals) and makes the group's shape part of the
// answer.
//
// So this compares, for two indices:
//
//   - the **ordinal within the group** must agree, and the **group lengths** must agree. This is
//     the half a bisimulation has no way to express — and the corpus has **no free witness** that
//     this engine is iso-recursive rather than equi-recursive, because all eight discriminating
//     vectors are blocked behind a deferred rule (#351, and ADR 0031's own falsification). So this
//     comparison is asserted by construction here and nowhere else, and it is what makes
//     `(rec (type $a (func)) (type (struct)))` and `(rec (type (struct)) (type $b (func)))`
//     different types despite holding identical functypes.
//   - every member of one group against the positionally corresponding member of the other,
//     which is why this walks the whole group rather than only the two named types. Two groups
//     agree or they do not; a per-member answer would be meaningless.
//
// # Termination
//
// A reference inside the group becomes an ordinal comparison and does not recurse. A reference out
// of the group recurses on *another* group, which in a module whose type section is well-scoped is
// an earlier one — so the recursion descends. `depth` bounds it anyway, for
// matchDeclaredSupertypes's reason: the scoping rule that guarantees the descent is not this
// function's, and is not yet implemented at all (see checkTypes), so nothing here may rely on it.
func sameDefType(m *binary.Module, got, want uint32, depth int) bool {
	if got == want {
		return true
	}
	if depth > len(m.Types) {
		return false
	}
	g, okGot := compTypeAt(m, got)
	w, okWant := compTypeAt(m, want)
	if !okGot || !okWant {
		return false
	}
	// The ordinal and the group length — the two facts a flat comptype list cannot supply.
	if g.RecLen != w.RecLen || got-g.RecStart != want-w.RecStart {
		return false
	}
	gs := recScope{g.RecStart, g.RecLen}
	ws := recScope{w.RecStart, w.RecLen}
	for i := range g.RecLen {
		gm, okG := compTypeAt(m, gs.start+i)
		wm, okW := compTypeAt(m, ws.start+i)
		if !okG || !okW {
			return false
		}
		if !sameSubType(m, gm, wm, gs, ws, depth) {
			return false
		}
	}
	return true
}

// recScope is one side of a rolled comparison: the group whose members' references become
// ordinals rather than definitions.
//
// A named type rather than four loose uint32 parameters threaded through six functions, because
// the two extents are what every one of them needs and swapping a pair is the mistake this
// comparison is most vulnerable to — a `sameRef` called with got's extent for want's operand
// answers `true` on exactly the pairs that share an index, which is a false agreement on the
// fixed points and correct everywhere else.
type recScope struct {
	start, length uint32
}

// sameSubType compares two members' `SubT (fin, uts, ct)` tuples under their groups' scopes.
//
// **The whole tuple, because `subst_deftype s dt1 = subst_deftype s dt2` is OCaml structural
// equality over the whole thing** — finality and the declared supertype list included. That is
// not a detail: `type-subtyping.wast:602,610` is `$t1` non-final and `$t2` final, both `(func)`,
// and they must compare **unequal**. `binary.CompType.Final`'s own comment records that this is
// why the bit is retained; this is the comparison it was retained for.
func sameSubType(m *binary.Module, got, want binary.CompType, gs, ws recScope, depth int) bool {
	if got.Final != want.Final || len(got.Supertypes) != len(want.Supertypes) {
		return false
	}
	for i := range got.Supertypes {
		if !sameRef(m, got.Supertypes[i], want.Supertypes[i], gs, ws, depth) {
			return false
		}
	}
	return sameCompType(m, got, want, gs, ws, depth)
}

// sameRef compares two type *references* under the two rolled scopes — the one place `Rec i` and
// `Def dt` are distinguished, and the reason the extent had to be retained.
//
// A reference into its own group is an ordinal, and matches only the same ordinal on the other
// side. A reference out of the group is a definition, and matches by the full structural equality
// above. **A reference into the group never matches one out of it**, which is the arm that makes
// `(rec (type $f1 (sub (func))) (type (struct (field (ref $f1)))))` differ from
// `(rec (type $f2 (sub (func))) (type (struct (field (ref $f1)))))` — the same field type spelled
// with an intra-group and a cross-group reference (`type-subtyping.wast:139`).
func sameRef(m *binary.Module, got, want uint32, gs, ws recScope, depth int) bool {
	inGot := got >= gs.start && got < gs.start+gs.length
	inWant := want >= ws.start && want < ws.start+ws.length
	if inGot != inWant {
		return false
	}
	if inGot {
		return got-gs.start == want-ws.start
	}
	return sameDefType(m, got, want, depth+1)
}

// sameCompType is structural equality over two comptypes under their rolled scopes — the same
// three-arm shape as matchCompType, with every predicate tightened from "matches" to "is the
// same" and every type reference routed through sameRef.
//
// Written separately rather than parameterizing matchCompType on a comparison function: the two
// differ in more than strictness. `matchCompType` has struct *width* subtyping and functype
// *contravariance*, both of which are asymmetries this must not have — equality is symmetric, and
// a version that inherited width subtyping would report `(struct i32 i64)` equal to
// `(struct i32)`.
func sameCompType(m *binary.Module, got, want binary.CompType, gs, ws recScope, depth int) bool {
	if got.Kind != want.Kind {
		return false
	}
	switch got.Kind {
	case binary.CompStruct, binary.CompArray:
		if len(got.Fields) != len(want.Fields) {
			return false
		}
		for i := range got.Fields {
			if !sameFieldType(m, got.Fields[i], want.Fields[i], gs, ws, depth) {
				return false
			}
		}
		return true
	default:
		return sameValTypes(m, got.Func.Params, want.Func.Params, gs, ws, depth) &&
			sameValTypes(m, got.Func.Results, want.Func.Results, gs, ws, depth)
	}
}

// sameFieldType is structural equality over two fieldtypes: mutability, then storage.
func sameFieldType(m *binary.Module, got, want binary.FieldType, gs, ws recScope, depth int) bool {
	if got.Mutable != want.Mutable || got.Storage.Packed != want.Storage.Packed {
		return false
	}
	if got.Storage.Packed {
		return got.Storage.Width == want.Storage.Width
	}
	return sameValType(m, got.Storage.Val, want.Storage.Val, gs, ws, depth)
}

// sameValTypes is structural equality over two type sequences.
func sameValTypes(m *binary.Module, got, want []binary.ValType, gs, ws recScope, depth int) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if !sameValType(m, got[i], want[i], gs, ws, depth) {
			return false
		}
	}
	return true
}

// sameValType is structural equality over two value types under the rolled scopes.
//
// The indexed reference form is the only one whose equality is not `==`: two `(ref $t)` with
// different indices can be the same type, and two with the *same* index are the same type only if
// their nullability agrees too. Everything else — the numeric kinds, the vector kind, the twelve
// abstract heaptypes — is identity, and `==` on `binary.ValType` compares exactly kind, null and
// index, which for a non-indexed form is the whole of it.
func sameValType(m *binary.Module, got, want binary.ValType, gs, ws recScope, depth int) bool {
	if !got.IsIndexed() || !want.IsIndexed() {
		return got == want
	}
	if got.Null() != want.Null() {
		return false
	}
	return sameRef(m, got.Index(), want.Index(), gs, ws, depth)
}

// matchDeclaredSupertypes is `match_deftype`'s third disjunct (match.ml:154-155): got matches
// want if any of got's *declared* supertypes does, transitively.
//
// # The depth bound is a termination guard, not a rule
//
// It is here because the property that makes the walk finite is established by a *different*
// function. `check_subtype_sub` requires `xi < x` — a supertype's index is strictly below the
// subtype's (valid.ml:169) — so in a module that has passed that check every chain strictly
// decreases and terminates. This relation is also called *from* that check, while the property is
// still being established one type at a time, and from the operand comparisons of a module whose
// type section this build may not have reached. A cyclic `Supertypes` list in a hand-built or
// hostile module must not hang the validator, and "it can't happen" is the reasoning that makes
// it happen.
//
// The bound is the type index space's own size, which is the worst case length of any acyclic
// chain, so it cannot refuse a chain a valid module can have.
func matchDeclaredSupertypes(m *binary.Module, got, want uint32, depth int) bool {
	if depth > len(m.Types) {
		return false
	}
	ct, ok := compTypeAt(m, got)
	if !ok {
		return false
	}
	for _, sup := range ct.Supertypes {
		if sup == want {
			return true
		}
		if matchDeclaredSupertypes(m, sup, want, depth+1) {
			return true
		}
	}
	return false
}

// matchResultType is `match_resulttype` (match.ml:118-120): same length, componentwise.
func matchResultType(m *binary.Module, got, want []binary.ValType) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if !matchValType(m, got[i], want[i]) {
			return false
		}
	}
	return true
}

// matchStorageType is `match_storagetype` (match.ml:126-130). The packed widths match by
// equality (`match_packtype`, `t1 = t2`); a packed storage and a value storage never match,
// which is the `_, _ -> false` arm and the reason this is not a comparison of `Val` alone.
func matchStorageType(m *binary.Module, got, want binary.StorageType) bool {
	if got.Packed != want.Packed {
		return false
	}
	if got.Packed {
		return got.Width == want.Width
	}
	return matchValType(m, got.Val, want.Val)
}

// matchFieldType is `match_fieldtype` (match.ml:132-137), whose treatment of the two mutability
// bits reads as two rules and is one:
//
//	mut1 = mut2 && match_storagetype c st1 st2 && (match mut1 with
//	  | Cons -> true
//	  | Var -> match_storagetype c st2 st1)
//
// Mutability is **invariant** — `mut1 = mut2`, so an immutable field never matches a mutable one
// in either direction, which is four of the twenty-one vectors on its own. A *constant* field is
// covariant in its storage type; a *mutable* one is invariant, the second check being the first
// with the arguments swapped. The suite has all four combinations for arrays and all four for
// structs, adjacent, so a port that dropped either half fails on rows two lines apart.
func matchFieldType(m *binary.Module, got, want binary.FieldType) bool {
	if got.Mutable != want.Mutable {
		return false
	}
	if !matchStorageType(m, got.Storage, want.Storage) {
		return false
	}
	if !got.Mutable {
		return true
	}
	return matchStorageType(m, want.Storage, got.Storage)
}

// matchCompType is `match_comptype` (match.ml:139-147).
//
// Three arms, and the cross-kind cases are `_, _ -> false`: a struct is never an array, which is
// six of the twenty-one vectors.
//
// **The struct arm is width subtyping and the function arm is contravariant**, and both are easy
// to write backwards:
//
//   - `StructT`: got may have *more* fields than want (`length fts1 >= length fts2`), and the
//     first `len(want)` of them must match pairwise. A subtype extends by appending.
//   - `FuncT`: parameters are checked `want -> got` and results `got -> want`
//     (`match_resulttype c ts21 ts11 && match_resulttype c ts12 ts22` — note the reversed
//     subscripts on the first). A function that accepts more is a subtype of one that accepts
//     less.
func matchCompType(m *binary.Module, got, want binary.CompType) bool {
	if got.Kind != want.Kind {
		return false
	}
	switch got.Kind {
	case binary.CompStruct:
		if len(got.Fields) < len(want.Fields) {
			return false
		}
		for i := range want.Fields {
			if !matchFieldType(m, got.Fields[i], want.Fields[i]) {
				return false
			}
		}
		return true
	case binary.CompArray:
		// `arraytype` carries exactly one fieldtype by its own grammar, not a vector, and
		// `binary.CompType.Fields` reflects whichever production filled it. A length check
		// rather than an unguarded `Fields[0]`: this relation is reached from a module the
		// decoder accepted, and a panic is not one of the answers it is allowed to give.
		if len(got.Fields) != 1 || len(want.Fields) != 1 {
			return false
		}
		return matchFieldType(m, got.Fields[0], want.Fields[0])
	default:
		return matchResultType(m, want.Func.Params, got.Func.Params) &&
			matchResultType(m, got.Func.Results, want.Func.Results)
	}
}

// compTypeAt resolves a type index, reporting absence rather than returning an error.
//
// The relation's return type is a bool all the way down, so an out-of-range index has to be
// answered inside it. That is sound because index validity belongs to `check_typeuse`
// (valid.ml:113-121) and not to `match_*`: every vector that can reach here with a bad index has
// a refusal coming from the rule that owns the question, and "does not match" is the answer that
// does not pre-empt it with a different message.
func compTypeAt(m *binary.Module, idx uint32) (binary.CompType, bool) {
	if idx >= uint32(len(m.Types)) {
		return binary.CompType{}, false
	}
	return m.Types[idx], true
}
