// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package interp

import (
	"fmt"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// The 0xfb region's four cast sub-opcodes (decode.ml:636-641).
//
// **Four opcodes, two questions, and the second one is not on the wire.** Which relation is asked
// is the ordinary opcode distinction — `ref.test` pushes a boolean, `ref.cast` traps — but the
// *nullability* of the type being tested comes from the opcode too, because the immediate is a bare
// `heaptype`:
//
//	| 0x14 -> ref_test (NoNull, heaptype s)
//	| 0x15 -> ref_test (Null, heaptype s)
//	| 0x16 -> ref_cast (NoNull, heaptype s)
//	| 0x17 -> ref_cast (Null, heaptype s)
//
// So `ref.test (ref $t)` and `ref.test (ref null $t)` encode the *same immediate bytes* and differ
// only in their sub-opcode. The decoder applies the bit while staging (`instr.go`'s `castTypes`),
// which is why the four arms below take a full reftype out of the side table and no arm re-derives
// nullability: a reader who put that switch here would have two places computing one fact, and the
// one that got it wrong would be invisible on every vector whose answer does not depend on nulls.
// The pair added by rung 5's second slice reads its type pair the same way and branches on the
// answer (`decode.ml:640-650`, `eval.ml:246-258`); their arms are below `execRefCast`.
const (
	opRefTest     = 0x14
	opRefTestNull = 0x15
	opRefCast     = 0x16
	opRefCastNull = 0x17
	opBrOnCast    = 0x18
	opBrOnCastFal = 0x19
)

// brOnCastLabel is the branch depth of a `br_on_cast`/`br_on_cast_fail` instruction.
//
// **A named accessor for one field read, because this is the only branching instruction in the
// engine whose label is not `Imm0`.** Every other one — `br`, `br_if`, `br_table`, `br_on_null`,
// `br_on_non_null` — stages its label first and so finds it in `Imm0`; this pair stages the flags
// byte first (wire order, `decode.ml:641-642`), so the label lands in `Imm1`. A reader who copies
// a neighbouring arm gets `Imm0`, which on the flags values the corpus actually contains is
// **0, 1, 2 or 3** — a plausible depth, silently wrong, and indistinguishable from a correct
// branch on any vector whose intended depth happens to be the flags value.
//
// The slot assignment is *printed, never reasoned about* (0027 decision 1): decoding
// `br_on_cast 2 (ref null any) (ref null i31)` — flags `0x03`, label `0x02` — yields
// `Imm0=3 Imm1=2`, and `TestBrOnCastSlotsAreFlagsThenLabel` is that print turned into a control so
// the next reader inherits the measurement instead of re-deriving it. The helper is what makes
// misreading require *ignoring* something rather than merely forgetting it.
func brOnCastLabel(ins binary.Instr) uint64 { return ins.Imm1 }

// branchCastTargetAt resolves the type `br_on_cast`/`br_on_cast_fail` casts **to** — `rt2` alone.
//
// # Why this returns one type where the side table holds two
//
// `Func.Casts` stages the pair in the reference's order: `rt1` is the instruction's declared *input*
// type and `rt2` is what it tests against (`decode.ml:644-645`). Only `rt2` is a run-time question —
// `eval.ml:246` and `:252` bind the first as **`_rt1`**, discarded — and reading element 0 where 1
// was meant produces a test against a *supertype*, which is legal, plausible, and wrong: for
// `br_on_cast $l anyref (ref i31)` it asks whether the operand is an `anyref`, which every operand
// on that stack already is, so the branch is always taken.
//
// So the arms are not handed the slice. This is a signature standing in for a warning comment: a
// comment saying "read element 1" is discipline that a copied line can drop, while an accessor that
// cannot return element 0 makes the wrong read **unrepresentable** — there is no expression for it
// at the call site. The full pair stays available through `binary.CastTypes` because #9's validator
// genuinely needs `rt1`: it is the input type the operand must already match, which is a question
// about the module rather than about the value, and therefore that layer's and not this one's.
//
// The arity is checked at exactly 2 for `castTypeAt`'s reason, and the count is the discriminator
// rather than a lower bound — `len(v) < 2` would accept a three-type vector from a decoder that
// began staging something else here, which is the drift a side table between two packages is most
// exposed to.
func branchCastTargetAt(fn *binary.Func, pc int, site string) (binary.ValType, error) {
	v, ok := fn.CastTypes(pc)
	if !ok || len(v) != 2 {
		return binary.NoValType, fmt.Errorf(
			"%w: %s at instruction %d staged %d cast types, want exactly 2 (rt1 then rt2)",
			ErrNotValidated, site, pc, len(v))
	}
	return v[1], nil
}

// heapType is a `heaptype` as the subtype relation needs one (types.ml:87-100): one of the twelve
// abstract forms, a concrete type in some module's type space, or bottom.
//
// **Three states with two discriminators, and `mod != nil` is the concrete one** — the same idiom
// `ref` uses for its payload kinds, where a non-nil pointer both selects and carries (see
// `ref.IsI31`'s comment for why that stops working once a payload has no distinguished value).
// Here it keeps working, and it earns its place: a concrete heaptype is *meaningless* without the
// type space its index resolves in, so the field that makes it concrete is exactly the field it
// cannot do without. Two modules' index 3 name different types the moment one instance's table holds
// a reference another instance allocated — grave #163's finding about funcrefs, arriving at
// aggregates.
//
// **Bottom gets a bool rather than a kind byte** because it has no wire form: `BotHT` exists only as
// the dynamic type of a null (value.ml:112), and nothing can encode it. A sentinel byte would be a
// value in the same space as the twelve real ones, which is how `NoValType` came to be overloaded
// in the first place.
type heapType struct {
	// bottom is `BotHT`. When set, kind/idx/mod are meaningless.
	bottom bool

	// kind is one of binary.Heap*, meaningful exactly when this is neither bottom nor concrete.
	kind byte

	// idx and mod are the concrete form `UseHT (Def dt)` — a type index and the module whose
	// type space it indexes. mod non-nil *is* the concrete discriminator.
	idx uint32
	mod *binary.Module
}

// refType is a `reftype` — `match_reftype`'s pair (match.ml:107-110), a nullability and a heaptype.
//
// Distinct from `binary.ValType`, which can also represent one, and the difference is the whole
// reason this type exists: a `ValType`'s indexed form carries an index with **no module**, because a
// static type is always read in the module that declared it. A *dynamic* type is read wherever the
// value travelled to, so it has to carry its own type space. `castTarget` lifts the static form into
// this one at the single point where a module is still known to be the right one.
type refType struct {
	null bool
	heap heapType
}

// abstractHeap is one of the twelve named forms.
func abstractHeap(kind byte) heapType {
	return heapType{kind: kind}
}

// String renders a reftype the way `string_of_reftype` does (types.ml:352-353) — uniformly
// `(ref null? ht)`, never `ValType.String`'s `funcref`/`externref` shorthand, for the reason
// `binary.HeapTypeName`'s comment gives: this string appears inside `ref.cast`'s trap text, where
// the oracle stops reading at `cast failure` and the tail's honesty is ours alone to keep.
//
// **Bottom prints `something`**, which is the reference's own word for it (types.ml:350) and looks
// enough like a placeholder that a reader would "fix" it. It is not a placeholder: `(ref null
// something)` is what the reference prints when a *null* fails a cast, because a null's dynamic type
// is `(Null, BotHT)` and `BotHT` names no type in particular. Spelled the authority's way so the
// message can be compared against it directly.
func (t refType) String() string {
	null := ""
	if t.null {
		null = "null "
	}
	switch {
	case t.heap.bottom:
		return "(ref " + null + "something)"
	case t.heap.mod != nil:
		return fmt.Sprintf("(ref %s%d)", null, t.heap.idx)
	}
	if name, ok := binary.HeapTypeName(t.heap.kind); ok {
		return "(ref " + null + name + ")"
	}
	// A kind byte the name map does not have, which the decoder cannot produce — every heaptype
	// it accepts is one of the twelve. Named rather than left to print an empty parenthesis,
	// because a message that silently drops the type it is about is worse than one that admits
	// it does not know: this is a fact about the engine, and saying so keeps it out of grave
	// #36's class.
	return fmt.Sprintf("(ref %s?%#02x)", null, t.heap.kind)
}

// castTarget lifts a static reftype into a dynamic one, binding the module its index resolves in.
//
// The one place a `binary.ValType` becomes a `refType`, and therefore the one place that can get
// the type space wrong. `mod` is the module the *instruction* lives in, which is right because the
// immediate was decoded from that module's body and its index means whatever that module's type
// section says — never the module the operand came from.
func castTarget(t binary.ValType, mod *binary.Module) refType {
	if t.IsIndexed() {
		return refType{null: t.Null(), heap: heapType{idx: t.Index(), mod: mod}}
	}
	kind, _ := t.Kind()
	return refType{null: t.Null(), heap: abstractHeap(kind)}
}

// matchNull is `match_null` (match.ml:58-61) in full:
//
//	| NoNull, Null -> true
//	| _, _ -> nul1 = nul2
//
// Which is to say a non-nullable type is a subtype of the nullable one and nothing else widens. The
// asymmetry is the whole content — `(ref $t) <: (ref null $t)` but not the reverse — and it is what
// makes `ref.test (ref $t)` on a null answer **0** while `ref.test (ref null $t)` answers **1**.
func matchNull(a, b bool) bool {
	return !a || b
}

// matchRefType is `match_reftype` (match.ml:107-110): both halves, independently.
func matchRefType(a, b refType) bool {
	return matchNull(a.null, b.null) && matchHeapType(a.heap, b.heap)
}

// matchHeapType is `match_heaptype` (match.ml:75-105), arm for arm.
//
// # The reference, and where each arm went
//
//	| EqHT, AnyHT | StructHT, AnyHT | ArrayHT, AnyHT | I31HT, AnyHT -> true      (1)
//	| I31HT, EqHT | StructHT, EqHT | ArrayHT, EqHT -> true                       (2)
//	| NoneHT, t when t <> BotHT -> match_heaptype c t AnyHT                      (3)
//	| NoFuncHT, t when t <> BotHT -> match_heaptype c t FuncHT                   (4)
//	| NoExnHT, t when t <> BotHT -> match_heaptype c t ExnHT                     (5)
//	| NoExternHT, t when t <> BotHT -> match_heaptype c t ExternHT               (6)
//	| UseHT (Idx x1), _ -> match_heaptype c (UseHT (Def (lookup c x1))) t2       (7)
//	| _, UseHT (Idx x2) -> match_heaptype c t1 (UseHT (Def (lookup c x2)))       (8)
//	| UseHT (Def dt1), UseHT (Def dt2) -> match_deftype c dt1 dt2                (9)
//	| UseHT (Def dt), t -> (* expand and compare against an abstract top *)     (10)
//	| BotHT, _ -> true                                                          (11)
//	| _, _ -> t1 = t2                                                           (12)
//
// Arms 7 and 8 have **no analogue here and that is not a gap**: an `Idx`/`Def` distinction is the
// reference's own two-stage type representation, and this engine has only the resolved form — a
// concrete heaptype is always an index plus the module that resolves it, so the lookup those arms
// perform has already happened by construction.
//
// **Arm 11 is hoisted to the top even though it is eleventh.** Reordering a `match` is exactly the
// kind of change that silently alters a relation, so the reason has to be that no earlier arm can
// match a bottom source, and it holds by inspection: arms 1-6 name abstract sources that are not
// bottom, arms 7-10 name concrete ones, and arm 11 is the first that `BotHT` reaches. What makes
// hoisting *necessary* rather than merely safe is the `t <> BotHT` guard in arms 3-6, which is about
// the **target**: `none <: bot` is false, so those four arms must fall through to arm 12 when the
// target is bottom, and reading them as unconditional would make every bottom type a subtype of
// bottom.
//
// **Arms 3-6 recurse with the arguments swapped**, which reads like a transcription error and is
// the reference's own text: `NoneHT, t -> match_heaptype c t AnyHT` asks whether the *target* is a
// subtype of `any`, because `none` is the bottom of the `any` hierarchy and so a subtype of exactly
// the types in it. That is why `none <: (ref $someStruct)` is **true** — the recursion reaches arm
// 10 and finds a struct under `any` — a verdict no reading of "none is the empty type" would
// predict without following the reduction.
func matchHeapType(a, b heapType) bool {
	if a.bottom {
		return true // arm 11, hoisted; see the doc comment for why that is sound
	}

	if a.mod != nil {
		// Arms 9 and 10: a concrete source.
		if b.bottom {
			return false // arm 12, `t1 = t2`, and a concrete type is not bottom
		}
		if b.mod != nil {
			return matchDeftype(a.mod, a.idx, b.mod, b.idx, nil) // arm 9
		}
		return matchConcreteAbstract(a, b.kind) // arm 10
	}

	// Arms 1 and 2: the abstract aggregate lattice, exactly the seven pairs the reference lists
	// and no closure over them. `eq <: any` is here; `eq <: eq` is not, and arrives at arm 12
	// instead — which matters, because writing this as a reachability walk over a hierarchy would
	// quietly add pairs the reference does not have.
	abstractB := !b.bottom && b.mod == nil
	if abstractB {
		switch a.kind {
		case binary.HeapEq:
			if b.kind == binary.HeapAny {
				return true
			}
		case binary.HeapStruct, binary.HeapArray, binary.HeapI31:
			if b.kind == binary.HeapAny || b.kind == binary.HeapEq {
				return true
			}
		}
	}

	// Arms 3-6: the four bottom types, each reduced to its own hierarchy's top. The `!b.bottom`
	// guard is the reference's `when t <> BotHT`; without it these would answer true for a
	// bottom target, since `match_heaptype BotHT top` hits the hoisted arm 11.
	if !b.bottom {
		switch a.kind {
		case binary.HeapNone:
			return matchHeapType(b, abstractHeap(binary.HeapAny))
		case binary.HeapNoFunc:
			return matchHeapType(b, abstractHeap(binary.HeapFunc))
		case binary.HeapNoExn:
			return matchHeapType(b, abstractHeap(binary.HeapExn))
		case binary.HeapNoExtern:
			return matchHeapType(b, abstractHeap(binary.HeapExtern))
		}
	}

	// Arm 12: `t1 = t2`. Two abstract forms are equal when their kinds are; an abstract source
	// against a concrete or bottom target is not equal to it.
	return abstractB && a.kind == b.kind
}

// matchConcreteAbstract is arm 10 — `UseHT (Def dt), t` — where a concrete type is compared against
// an abstract one by expanding it and asking which hierarchy it belongs to:
//
//	| StructT _, AnyHT | StructT _, EqHT | StructT _, StructHT -> true
//	| ArrayT _, AnyHT | ArrayT _, EqHT | ArrayT _, ArrayHT -> true
//	| FuncT _, FuncHT -> true
//	| _ -> false
//
// Note what is **absent**: `FuncT _, AnyHT` is false, so a funcref is not an `anyref` — the two
// hierarchies are disjoint, and `ref.test anyref` on a `ref.func` answers 0. That is the single
// most surprising verdict in the whole relation for a reader who expects `any` to mean any, and it
// is why this arm enumerates the reference's seven pairs instead of testing "is it an aggregate".
func matchConcreteAbstract(a heapType, kind byte) bool {
	ct, ok := compTypeAt(a.mod, a.idx)
	if !ok {
		// #9's layering debt, the same one `compTypeAt` documents: an index naming nothing.
		// False rather than a panic (grave 0003) — and false is also the honest verdict, since
		// a type that does not exist belongs to no hierarchy.
		return false
	}
	switch ct.Kind {
	case binary.CompStruct:
		return kind == binary.HeapAny || kind == binary.HeapEq || kind == binary.HeapStruct
	case binary.CompArray:
		return kind == binary.HeapAny || kind == binary.HeapEq || kind == binary.HeapArray
	case binary.CompFunc:
		return kind == binary.HeapFunc
	}
	return false
}

// typeOfRef is `type_of_ref` (value.ml:110-113) — a runtime value's dynamic reftype.
//
// # A null carries no heaptype, and that is the reference's design rather than this engine's shortcut
//
// The reference's constructor is nullary — `type ref_ += NullRef` (value.ml:20) — and
// `type_of_ref NullRef = (Null, BotHT)` (value.ml:112). So `ref.null any` and `ref.null none`
// produce values that are **indistinguishable at run time**, and every cast against a null is
// decided by nullability alone: `ref.test (ref null X)` answers 1 for every X, `ref.test (ref X)`
// answers 0 for every X. 0027's decision 4 chose this before the authority was read, on the
// argument that a null matches exactly the nullable reftypes; reading value.ml turned that from a
// defensible choice into a transcription, which is the better outcome — the alternative design
// (retaining the heaptype `ref.null` was written with) is not a conservative extension but a
// *different relation*, and it would have made `ref.test (ref null i31)` on a `ref.null none`
// answer 0 where the reference answers 1.
//
// # One arm per payload kind, in `ref`'s own discriminator order
//
// The switch is 0027 decision 6 and it owes an arm to every payload `ref` can hold, for the reason
// `notAggregate`'s i31 arm states: a payload with no arm here is not *unreported*, it is
// *misreported* as whichever arm catches it, which is grave #36's class. The default is therefore an
// error rather than a plausible-looking type.
func typeOfRef(r ref, site string) (refType, error) {
	if r.Null {
		return refType{null: true, heap: heapType{bottom: true}}, nil
	}
	switch {
	case r.IsI31:
		return refType{heap: abstractHeap(binary.HeapI31)}, nil

	case r.Obj != nil:
		// The concrete aggregate type the object was allocated with, read off the provenance
		// pair rather than a cached comptype pointer — 0027 decision 5, and the index is the
		// load-bearing half here: `matchDeftype` walks supertype *indices*, so a pointer to
		// the comptype could not answer a cast at all.
		return refType{heap: heapType{idx: r.Obj.typeIdx, mod: r.Obj.mod}}, nil

	case r.Exc != nil:
		return refType{heap: abstractHeap(binary.HeapExn)}, nil

	case r.Inst != nil:
		// A funcref's dynamic type is its function's *declared* type — `Func.type_of`, a
		// concrete deftype, never the abstract `func`. Resolved through `funcRefTarget` so an
		// imported slot reports the definer's type index in the definer's type space: the
		// importing module's own type index would be an index into the wrong space, which is
		// the funcref-is-a-pair finding (grave #163) reaching the cast relation.
		target, fn, err := funcRefTarget(r, site)
		if err != nil {
			return refType{}, err
		}
		return refType{heap: heapType{idx: fn.TypeIndex, mod: target.mod}}, nil
	}

	// No discriminator set on a non-null reference. Reported as the engine inconsistency it is,
	// and deliberately *not* named as a payload kind: `notAggregate`'s default arm says "a
	// function reference" for this shape, which is right there (it is describing what the fields
	// look like) and would be a fabrication here, where the whole output is a type.
	//
	// This is also the slot rung 5's slice 3 fills — an externalized reference, whose dynamic
	// type is `extern`. Until it exists, an `externref` in this engine is either null or a
	// funcref-shaped value from the harness's `ref.extern`, and the honest answer for anything
	// else is that there isn't one.
	//
	// **The design that fills it is 0027 decision 3, which is `proposed` and *not* covered by that
	// ADR's stamp** — Scott accepted 1, 2, 4, 5 and 6 on the #259 relay and carved 3 out until
	// slice 3's scoping firms it. Stated rather than cited bare, because a comment naming a
	// decision reads as naming a settled one, and *a status field is a citation to an approval*
	// (#142) does not stop being true when the citation moves from an ADR header into prose.
	return refType{}, fmt.Errorf("%w: %s on a non-null reference with no payload discriminator set",
		ErrNotValidated, site)
}

// castTypeAt resolves the reftype `ref.test`/`ref.cast` was decoded with, out of `Func.Casts`.
//
// **The absent case is unreachable and reported anyway, per `badLocal`'s precedent** — that helper
// reports a local index past the end with `ErrNotValidated` for a condition validation makes
// impossible, on the same reasoning: the alternative is a nil-deref or a plausible zero value, and a
// staged immediate that is not there is precisely the shape 0016 exists to make visible rather than
// silent. It is unreachable because the decoder stages exactly one reftype for each of these four
// sub-opcodes at the same instruction index `emit` appends at, and a const expression cannot contain
// one (no cast opcode is const-legal), so the only way here is a decoder and interpreter that
// disagree about which opcodes have a staged type.
func castTypeAt(fn *binary.Func, pc int, site string) (binary.ValType, error) {
	v, ok := fn.CastTypes(pc)
	if !ok || len(v) != 1 {
		return binary.NoValType, fmt.Errorf(
			"%w: %s at instruction %d staged %d cast types, want exactly 1",
			ErrNotValidated, site, pc, len(v))
	}
	return v[0], nil
}

// execRefTest is `RefTest` (eval.ml:648-651):
//
//	| RefTest rt, Ref r :: vs' ->
//	  let rt' = subst_reftype (subst_of c.frame.inst.types) rt in
//	  value_of_bool (Match.match_reftype [] (type_of_ref r) rt') :: vs', []
//
// **It never traps.** A null operand is not an error here — it is a value with a dynamic type, and
// the answer is a plain 0 or 1 like any other. That is the difference from every other GC arm
// written so far, all of which trap on null, and it is why this file shares nothing with
// `trapNullStruct` and friends.
//
// `subst_reftype` is the substitution this engine does not have (see `matchDeftype`'s scope note);
// what stands in for it is that both sides carry their own module, so a rec-group-relative index
// never needs canonicalizing to be resolved — only to be compared *across* rec groups, which is the
// documented boundary.
func (in *Instance) execRefTest(fn *binary.Func, pc int, st *stack) error {
	want, err := castTypeAt(fn, pc, "ref.test")
	if err != nil {
		return err
	}
	if short := st.needRef(1); short != nil {
		return short
	}
	r := st.popRef()
	got, err := typeOfRef(r, "ref.test")
	if err != nil {
		return err
	}
	st.pushBool(matchRefType(got, castTarget(want, in.mod)))
	return nil
}

// execRefCast is `RefCast` (eval.ml:652-659):
//
//	| RefCast rt, Ref r :: vs' ->
//	  let rt' = subst_reftype (subst_of c.frame.inst.types) rt in
//	  if Match.match_reftype [] (type_of_ref r) rt' then vs, []
//	  else [Trapping ("cast failure, expected " ^ string_of_reftype rt ^ " but got " ^
//	    string_of_reftype (type_of_ref r))]
//
// **On success the operand goes back unchanged** — `vs`, not `Ref r :: vs'`, so the reference does
// not even rebuild the stack. A cast is a *check*, never a conversion: the value's representation is
// identical afterwards, which is what makes it free at run time and is worth stating because the
// name suggests otherwise.
//
// **The trap text names `rt`, the un-substituted immediate**, not `rt'`. In the reference those
// differ (one has had the frame's rec-group substitution applied) and it prints the one the
// programmer wrote. This engine has no substitution step, so the two coincide — but the *choice* is
// transcribed rather than reinvented, because the day a substitution arrives is the day a message
// built from the wrong one starts naming a type the module does not contain, which is grave #36
// exactly. Only `cast failure` is oracle-covered; the tail is ours.
func (in *Instance) execRefCast(fn *binary.Func, pc int, st *stack) error {
	want, err := castTypeAt(fn, pc, "ref.cast")
	if err != nil {
		return err
	}
	if short := st.needRef(1); short != nil {
		return short
	}
	r := st.popRef()
	got, err := typeOfRef(r, "ref.cast")
	if err != nil {
		return err
	}
	target := castTarget(want, in.mod)
	if !matchRefType(got, target) {
		return &Trap{Reason: fmt.Sprintf("cast failure, expected %s but got %s", target, got)}
	}
	st.pushRef(r)
	return nil
}

// brOnCastTaken answers whether a `br_on_cast`/`br_on_cast_fail` branches, having already put the
// operand back on the stack — `eval.ml:246-258`:
//
//	| BrOnCast (x, _rt1, rt2), Ref r :: vs' ->
//	  let rt2' = subst_reftype (subst_of c.frame.inst) rt2 in
//	  if Match.match_reftype [] (type_of_ref r) rt2' then
//	    Ref r :: vs', [Plain (Br x) @@ e.at]
//	  else
//	    Ref r :: vs', []
//
//	| BrOnCastFail (x, _rt1, rt2), Ref r :: vs' ->
//	  ... if match then Ref r :: vs', [] else Ref r :: vs', [Plain (Br x) @@ e.at]
//
// # The reference survives on all four paths, which is what makes this different from br_on_null
//
// Every one of the four arms above rebuilds `Ref r :: vs'`. That is not the shape the two `br_on_*`
// arms in `runFrame` have — `br_on_null` *consumes* the null when it branches and `br_on_non_null`
// preserves it only when it branches — so a reader who generalizes from those two writes a
// conditional push here and is wrong on one path out of four. The push is therefore unconditional
// and happens before the verdict is returned: a value the reference keeps on every path is not a
// value this engine may decide about.
//
// **And the push has to precede `branch()`**, for the reason `br_on_non_null`'s arm states: `branch`
// does the label's stack surgery, keeping `refArity` references from the top, so an operand pushed
// after it would sit *above* the surgery rather than inside it — the branch target would receive
// whatever was underneath. Since this arm cannot branch (it has no `ctrl`), the ordering is
// structural rather than remembered: the push is done here and the caller can only branch after.
//
// # Why the branch itself is the caller's
//
// `execFB` returns an `error`, and branching needs `ctrl` and `pc`, which are `runFrame`'s locals.
// Splitting at the predicate keeps the semantics — the type test, the stack discipline, the
// side-table read — in this file with the rest of the family, and leaves `runFrame` doing exactly
// what its other five branching arms do. The alternative, widening `execFB` to return a branch
// verdict, would put a control-flow signal on 27 arms that cannot produce one.
//
// `_rt1` is not read, and it is not *available* to be read: see `branchCastTargetAt`.
func (in *Instance) brOnCastTaken(ins binary.Instr, st *stack, fn *binary.Func, pc int) (bool, error) {
	site := "br_on_cast"
	if ins.Op == opBrOnCastFal {
		site = "br_on_cast_fail"
	}
	want, err := branchCastTargetAt(fn, pc, site)
	if err != nil {
		return false, err
	}
	if short := st.needRef(1); short != nil {
		return false, short
	}
	r := st.popRef()
	got, err := typeOfRef(r, site)
	if err != nil {
		return false, err
	}
	// Unconditional, and before the verdict — see the two paragraphs above.
	st.pushRef(r)
	matched := matchRefType(got, castTarget(want, in.mod))
	// `br_on_cast_fail` is the exact inversion and nothing else: same operand, same type, same
	// stack effect, opposite verdict (`eval.ml:253-258`). Written as one negation rather than two
	// arms so the two opcodes cannot drift into disagreeing about anything but the branch.
	if ins.Op == opBrOnCastFal {
		return !matched, nil
	}
	return matched, nil
}
