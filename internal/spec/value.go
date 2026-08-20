package spec

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ValKind is a value type as the *harness* names it.
//
// **Deliberately not `binary.ValType`**, and the duplication is the neutrality rule (contract
// §0) rather than an oversight: this package is the oracle, and an oracle that imports the
// engine's type system can no longer be read as independent of it. Every other engine fact
// crosses this boundary as an injected function (DecodeFunc, ReadTextFunc); a *type* cannot be
// injected, so it is restated. The glue that owns both — the board runner in the test file —
// converts.
//
// Six members, widened from four by #196/#197: KindFuncRef and KindExternRef, the two
// reference kinds this engine can actually produce or consume — see Val's own comment for
// why the other twelve GC heaptypes stay outside what the harness can ask. Every prior
// four-member comment describing this as closed is superseded by that pair's arrival, per
// the doc comment's own rule that a fifth kind is "a widening ... and belongs here" — it did.
type ValKind byte

const (
	KindI32 ValKind = iota
	KindI64
	KindF32
	KindF64
	KindFuncRef
	KindExternRef

	// KindV128 is decision 0024's own widening (forced question 5): a v128 result or argument,
	// carried as N per-lane scalar `Val`s in the owning `Val`'s own `Lanes` field rather than
	// as a wider `Bits`/`NaN` pair — the harness's own comparator (`Matches`) already knows how
	// to compare one scalar `Val` against another, and decomposing at this boundary reuses it
	// unchanged instead of teaching it a second, wider comparison shape. See Val's own doc
	// comment on `Lanes`.
	KindV128

	// KindAnyRef is #270/0039's widening, and it is here — appended rather than slotted beside the
	// other two reference kinds — because nothing in this file derives anything from the members'
	// numeric order (unlike `burroughs.Kind`, whose IsRef is a range check and whose ordering is
	// therefore pinned by a control). Appending keeps every existing member's value fixed, which
	// costs nothing and means a stale serialized Kind cannot appear as a different one.
	//
	// **It is a measured necessity and not a tidying.** `extern.wast:42` passes a bare
	// `(ref.host 2)` at an **`anyref`** parameter. Carried as KindExternRef — the placeholder every
	// unnameable reference used before this — `toInterpValue` would build an `externref`, the engine
	// would set `Externalized`, `typeOfRef` would report `extern`, and `matchRefType(extern, any)` is
	// false: the vector would fail on a type mismatch the corpus does not contain. So the harness
	// needs to be able to *name* `anyref` before it can pass that argument, which is the third of the
	// three things #270 needed and the only one that touches this enum.
	//
	// It is also what keeps `(ref.host N)` and `(ref.extern N)` apart now that both are readable:
	// same identity, same Class, different Kind — exactly the distinction `interp.HostRef` and
	// `interp.ExternRef` make with their static types.
	//
	// **And it is the placeholder Kind for every reference this harness still cannot name** —
	// `structref`, `(ref $t)`, `exnref` and the rest all arrive as this, from `valKind`'s own refusal
	// path (spec_test.go). That is what makes the `want.Kind != got.Kind` gate in Matches *inert* for
	// #270's vectors rather than something #270 had to unpick: both sides of every one of the 28 rows
	// land here. The gate's own incorrectness survives and is [#441]'s subject, not this widening's.
	//
	// [#441]: https://github.com/scttfrdmn/burroughs/issues/441
	KindAnyRef
)

func (k ValKind) String() string {
	switch k {
	case KindI32:
		return "i32"
	case KindI64:
		return "i64"
	case KindF32:
		return "f32"
	case KindF64:
		return "f64"
	case KindFuncRef:
		return "funcref"
	case KindExternRef:
		return "externref"
	case KindV128:
		return "v128"
	case KindAnyRef:
		return "anyref"
	}
	return "unknown"
}

// isRef reports whether this kind lives in the reference half of a Val rather than the
// numeric half — the harness's own mirror of `binary.ValType.IsRef`, restated rather than
// imported for ValKind's own neutrality reason (see the type's doc comment).
//
// Enumerated rather than range-checked, unlike `burroughs.Kind.IsRef`, because this enum's members
// are *not* ordered with the references contiguous — KindV128 sits between KindExternRef and
// KindAnyRef, and appending KindAnyRef was the choice that kept the older members' values fixed. So
// the partition is stated here and TestKindOrderingIsTheRefPartition's analogue does not apply.
func (k ValKind) isRef() bool {
	return k == KindFuncRef || k == KindExternRef || k == KindAnyRef
}

// bits reports the width in bits.
//
// The 32-bit kinds are listed and everything else is 64, written as a `default` rather than as a
// post-switch return so that `exhaustive` can see the fallback is a real one. Naming
// KindI64/KindF64 in their own arm would read as safer and is not: a fifth kind (v128) must not
// silently acquire a width, and reaching the default with an unknown kind is the case a bare
// `return 64` would answer confidently and wrongly.
//
// **`KindV128` is the fifth kind that comment predicted, and it never reaches here** — a v128
// has no single width (each of its lanes does), so every caller of `width()` operates on a
// lane's own scalar `Kind`, never on `KindV128` itself. Callers: the NaN-class predicates in
// `Matches` and the literal readers (`readIntLit`/`readFloatLit`), both of which read a lane's
// `Val` — see `Val.Lanes`.
func (k ValKind) width() uint {
	switch k {
	case KindI32, KindF32:
		return 32
	default:
		return 64
	}
}

func (k ValKind) isFloat() bool { return k == KindF32 || k == KindF64 }

// NaNClass is `assert_return`'s payload-*class* expectation.
//
// `nan:canonical` and `nan:arithmetic` are not values, they are predicates: the spec leaves an
// arithmetic operation's NaN payload unspecified, so the vector asserts a *set* the result must
// belong to rather than a pattern it must equal. 933 and 961 vectors respectively inside the
// answerable population — 13.8% of it — so a harness that compared bit patterns here would
// report 1894 correct results as wrong.
//
// The sign bit is unconstrained in both classes (`NaN` in the spec's numerics is
// sign-agnostic), which is the part a bit comparison cannot express at all.
type NaNClass byte

const (
	// NaNNone means the expectation is an exact bit pattern, which is the ordinary case.
	NaNNone NaNClass = iota
	// NaNCanonical is the width's canonical NaN: exponent all ones, mantissa's top bit set,
	// every other mantissa bit clear, sign either.
	NaNCanonical
	// NaNArithmetic is any quiet NaN: exponent all ones, mantissa's top bit set, the rest
	// arbitrary, sign either. A superset of NaNCanonical.
	NaNArithmetic
)

func (c NaNClass) String() string {
	switch c {
	case NaNCanonical:
		return "nan:canonical"
	case NaNArithmetic:
		return "nan:arithmetic"
	default:
		return "exact"
	}
}

// RefClass names which reference shape a Val's reference half carries — meaningful only when
// Kind.isRef().
//
// **Four members, matching exactly the shapes #196/#197's own population measurement found the
// corpus needing as an invoke argument, an assert_return expectation, or an engine result**
// (measured over testdata/spec: 61 `(ref.extern N)` arguments/results, 90 `(ref.null
// <heaptype>)` results of which 29 are also arguments, 22 bare `(ref.func)`/4 bare
// `(ref.extern)` results as a *type-pattern* — never as a literal or an argument — and a fourth
// shape RefConcrete names below: a non-null funcref *result* the corpus never compares by
// identity, e.g. `table.wast`'s `get2`/`get3`/`get4`/`get5`, each `(assert_return (invoke
// "getN") (ref.func))` matched only by RefTypePattern's own predicate, never by value). No
// `HeapType` field: the reference's own `NullLit`/`literal_null` reader discards the heaptype it
// parses (`interpreter/script/runner.ml:365`, `NullLit ht -> Value.(Ref NullRef)`) and
// `assert_ref_pat` matches `NullPat _, NullRef -> true` unconditionally regardless of it
// (`interpreter/script/runner.ml:476`) — so retaining a null's heaptype here would be state this
// harness's own oracle never reads. What *does* carry the heap type is Kind
// (KindFuncRef/KindExternRef), which is exactly the two-member scope #7's engine can produce. A
// fifth RefClass member would be a widening exactly like ValKind's own doc comment describes,
// and belongs here when the corpus is measured to need one — not before (0006's
// premature-generality rule).
type RefClass byte

const (
	// RefNone is the zero value: Kind is not a reference kind, and this field is unread.
	RefNone RefClass = iota

	// RefLiteralNull is `ref.null <heaptype>` (as an argument, an expectation, or a result) —
	// a concrete null value, matched by Matches without regard to which heaptype spelled it,
	// per this type's own doc comment.
	RefLiteralNull

	// RefExternIdentity is `(ref.extern N)` (as an argument, an expectation, or a result) — a
	// non-null externref carrying the reference's own opaque host identity N (Extern below
	// has the citation and the reasoning for why N is exactly this: a bare handle compared by
	// equality, never interpreted).
	RefExternIdentity

	// RefTypePattern is the bare `(ref.func)` / `(ref.extern)` **expectation** shape —
	// `RefTypePat` in the reference's own script grammar (parser.mly:1524-1531): "a value of
	// this heap type", not a literal, and the *admits-null* question differs by Kind exactly
	// as the reference's `assert_ref_pat` differs by heaptype (`RefTypePat ExternHT, _ ->
	// true` admits anything including null; `RefTypePat FuncHT, Instance.FuncRef _ -> true`
	// admits only a non-null funcref) — see Matches. No corpus vector uses this as an
	// *argument*, only as an assert_return expectation, so readRefConst's argument-reading
	// callers never produce it, and no engine **result** is ever this class either — see
	// RefConcrete for the shape a result takes instead.
	RefTypePattern

	// RefConcrete is a non-null reference **result** this harness cannot name any more
	// specifically — fromInterpValue's own construction for a non-null funcref (spec_test.go),
	// since #7's engine tracks no funcref identity the corpus ever asks the harness to compare
	// (0 vectors compare two funcref results, or a funcref result against a literal). Matches
	// never receives this as a `want`, only as a `got`: it exists purely so a concrete non-null
	// result has *some* Class other than RefLiteralNull. That distinction used to be tested by
	// RefTypePattern's own arm reading `got.Class != RefLiteralNull`; since grave #266 it is
	// tested by *position* instead — every null `got` is answered by the dispatch at the top of
	// Matches, so reaching the RefTypePattern arm at all is what establishes non-nullness. The
	// member is no less load-bearing for that; it is the thing that makes the top dispatch's
	// guard (`got.Class == RefLiteralNull`) a real question rather than a tautology.
	RefConcrete
)

// RefPat is the heap type a bare `(ref.<ht>)` **expectation** names — `RefTypePat`'s argument in the
// reference's own script grammar, meaningful only when Class is RefTypePattern.
//
// **Separate from Kind, because the pattern's heaptype and the value's static kind are different
// facts and conflating them was #270's harness-side wall.** Until 0039 the heaptype was *implied* by
// Kind, which worked only while there were two of each: `KindFuncRef` meant `FuncHT` and
// `KindExternRef` meant `ExternHT`. `parser.mly:1517-1530`'s `result` production has **eight**
// `RefTypePat` arms, so an implied heaptype could not spell six of them — `(ref.array)` had no
// representation at all, and 17 vectors were declined for that one alone.
//
// Eight members plus a zero value, which is the authority's own arm count rather than the four the
// corpus exercises: the domain is a fixed grammar production, so enumerating it fully costs two lines
// and leaves nothing for a later slice to discover.
type RefPat byte

const (
	// PatNone is the zero value: this Val is not a RefTypePattern and the field is unread.
	PatNone RefPat = iota

	// PatAny is `(ref.any)`. Admits **everything except a function reference**, which is the one
	// arm order in `assert_ref_pat` that matters: `RefTypePat AnyHT, Instance.FuncRef _ -> false`
	// precedes `RefTypePat AnyHT, _ -> true`, so a null and an externalized value both satisfy it.
	PatAny

	// PatEq is `(ref.eq)`. Admits exactly `I31Ref | StructRef | ArrayRef` — an or-pattern over three
	// constructors, and *not* an externalized one of the three, because the or-pattern matches the
	// outer constructor.
	PatEq

	// PatI31, PatStruct and PatArray are the three single-constructor aggregates' patterns.
	PatI31
	PatStruct
	PatArray

	// PatFunc is `(ref.func)`: `FuncRef _` only, with no arm for a null, which is why the bare
	// funcref pattern refuses one where the bare externref pattern does not.
	PatFunc

	// PatExn is `(ref.exn)`: `ExnRef _` only.
	PatExn

	// PatExtern is `(ref.extern)`: `RefTypePat ExternHT, _ -> true`, the widest arm in the function —
	// it admits a null, a funcref, and everything else.
	PatExtern

	// patPastEnd is **not a pattern**; it is the domain's upper bound, declared inside this block so
	// `iota` maintains it. See `interp.RefPayload`'s own sentinel for the full argument and the
	// condition on 0039's stamp that asks for it.
	patPastEnd
)

func (p RefPat) String() string {
	switch p {
	case PatAny:
		return "any"
	case PatEq:
		return "eq"
	case PatI31:
		return "i31"
	case PatStruct:
		return "struct"
	case PatArray:
		return "array"
	case PatFunc:
		return "func"
	case PatExn:
		return "exn"
	case PatExtern:
		return "extern"
	case PatNone, patPastEnd:
		// Neither is a pattern, so neither has a spelling in the grammar. Named rather than left to
		// a default so `exhaustive` confirms a stated reading exists for every member.
		return "no-pattern"
	}
	return "no-pattern"
}

// admits reports whether this pattern is satisfied by a **non-null** reference result of the given
// constructor — `assert_ref_pat` (`interpreter/script/runner.ml:464-476`) transcribed arm for arm,
// with the null cases living in admitsNull below because Matches answers a null `got` before Kind is
// ever read (grave #266).
//
// **Two faithful divergences, stated rather than smoothed over, and both are the `want.Kind !=
// got.Kind` gate's doing rather than this function's.** In the reference, externalization is a
// *constructor*: an externalized i31 is `Extern.ExternRef (I31Ref …)`, so `RefTypePat EqHT`'s
// or-pattern does not match it and `RefTypePat AnyHT`'s catch-all does. Here externalization is
// carried by Kind and the payload underneath it survives, so:
//
//   - `(ref.i31)`/`(ref.eq)`/`(ref.struct)`/`(ref.array)` against an **externalized** payload: this
//     function would say true and the reference says false — but Matches never asks, because the
//     Kind gate (want KindAnyRef, got KindExternRef) refuses the row first. The answers agree for a
//     different reason, which is worth knowing precisely because it means fixing the gate changes
//     these rows.
//   - `(ref.any)` against an externalized value: the reference says **true** and Matches says false,
//     for the same Kind gate. A real disagreement, in the safe direction, over 0 corpus vectors —
//     `(ref.any)` appears nowhere in the suite.
//
// Both belong to [#441](https://github.com/scttfrdmn/burroughs/issues/441), which is the gate's own
// issue with the accept-direction census it needs; 0039 decision 2 is why they are separate.
func (p RefPat) admits(payload RefPayload) bool {
	// A non-null reference naming no constructor is an **engine inconsistency**, not a value: the
	// reference interpreter has no such thing to feed `assert_ref_pat`, so there is no arm to
	// transcribe. Refused by every pattern including the two catch-alls, which is a deliberate
	// divergence in the safe direction — admitting it would score a shape the authority cannot spell
	// as a pass, and `interp.payloadOf` produces it exactly where the engine has contradicted itself.
	if payload == PayloadNone {
		return false
	}
	switch p {
	case PatAny:
		// `RefTypePat AnyHT, Instance.FuncRef _ -> false` **precedes** `RefTypePat AnyHT, _ -> true`,
		// and the order is the whole content of the arm: `any` is the top of the internal hierarchy,
		// so it admits every constructor except the one that is not under it.
		return payload != PayloadFunc
	case PatEq:
		// `(I31.I31Ref _ | Aggr.StructRef _ | Aggr.ArrayRef _)` — three constructors, one arm. The
		// only pattern in the grammar that is not one-to-one, which is why a `want.Pat == got.Payload`
		// comparison could not have stood in for this function.
		return payload == PayloadI31 || payload == PayloadStruct || payload == PayloadArray
	case PatI31:
		return payload == PayloadI31
	case PatStruct:
		return payload == PayloadStruct
	case PatArray:
		return payload == PayloadArray
	case PatFunc:
		return payload == PayloadFunc
	case PatExn:
		return payload == PayloadExn
	case PatExtern:
		// `RefTypePat ExternHT, _ -> true`: the widest arm, and it is a wildcard on the value rather
		// than a check that the value is externalized — `extern.wast:46-49` rely on exactly that.
		return true
	case PatNone, patPastEnd:
		// Neither is a pattern, so neither admits anything. Named so `exhaustive` confirms a stated
		// reading for every member rather than a `default` absorbing a ninth arriving later.
		return false
	}
	return false
}

// admitsNull reports whether this pattern is satisfied by a **null** result.
//
// Two arms admit one, and it is not the two a reader would guess: `NullPat _` is a different pattern
// class entirely (RefLiteralNull here), and among the `RefTypePat`s only `ExternHT`'s wildcard and
// `AnyHT`'s catch-all match `NullRef` — every other heaptype has an arm naming a constructor, and a
// null matches none of them, falling through to `| _ -> false`.
//
// Separate from admits rather than a `PayloadNone` case inside it, because PayloadNone means
// something else there (see its refusal) and one function answering two questions by overloading a
// zero value is how the two get confused.
func (p RefPat) admitsNull() bool {
	switch p {
	case PatAny, PatExtern:
		return true
	case PatEq, PatI31, PatStruct, PatArray, PatFunc, PatExn, PatNone, patPastEnd:
		return false
	}
	return false
}

// RefPayload is which **constructor** a non-null reference *result* is — `assert_ref_pat`'s other
// operand, and the got-side half of what 0039 adds here.
//
// The harness restates the engine's `interp.RefPayload` rather than importing it, for ValKind's own
// neutrality reason: this package is the oracle, and an oracle that imports the engine's vocabulary
// can no longer be read as independent of it. The glue that owns both converts (`fromInterpValue` in
// the test file), which is where every other engine fact crosses this boundary too.
//
// **Why a result needs this and a Kind will not do.** `assert_ref_pat` dispatches on the runtime
// value's constructor and reads no static type at all, and a reference's type is an upper bound: an
// `anyref` result can be a host reference, an i31, a struct or an array, and Kind says only `anyref`.
// So without this field the harness can *receive* eleven of the 28 vectors' results and answer none
// of them.
type RefPayload byte

const (
	// PayloadNone is the zero value: not a reference, or a **null** one (whose constructor is
	// nullary — grave #266's fact, and the reason Class rather than this field carries nullity), or
	// a non-null reference whose constructor the engine could not determine, which is an engine
	// inconsistency rather than a payload.
	PayloadNone RefPayload = iota

	// PayloadHost is `HostRef` — the thing `(ref.host N)` names bare and `(ref.extern N)` wraps.
	// Which of the two spellings a Val is holding is carried by Kind, not here.
	PayloadHost

	PayloadI31
	PayloadStruct
	PayloadArray
	PayloadFunc
	PayloadExn

	// payloadPastEnd is **not a payload kind** — the domain's upper bound, `iota`-maintained inside
	// this block for the reason `interp.RefPayload`'s own sentinel states.
	payloadPastEnd
)

func (p RefPayload) String() string {
	switch p {
	case PayloadHost:
		return "host"
	case PayloadI31:
		return "i31"
	case PayloadStruct:
		return "struct"
	case PayloadArray:
		return "array"
	case PayloadFunc:
		return "func"
	case PayloadExn:
		return "exn"
	case PayloadNone, payloadPastEnd:
		return "reference"
	}
	return "reference"
}

// Val is one wasm value crossing the harness boundary, a NaN-class expectation, or a
// reference-shaped value/expectation (#196/#197).
//
// Bits is the value's representation read according to Kind, for the numeric kinds — the
// bit-pattern discipline, for the reason `assert_return` needs it: the assertion is bitwise, so
// `+0.0` and `-0.0` are different expectations and `NaN` compares equal to itself. A float-valued
// field would get both backwards, in opposite directions, and would collapse every NaN payload on
// the way in. Unread when Kind.isRef().
//
// NaN is NaNNone for every value; a Val with NaN set is an *expectation* only, never an
// argument. classifyAssertReturn enforces that asymmetry rather than leaving it to the matcher,
// because a NaN class in an argument position is a vector shape this harness cannot ask, not a
// value it can pass. Always NaNNone when Kind.isRef() — the two "no literal bit pattern" escapes
// (a NaN class, a reference class) are mutually exclusive rather than sharing a field, because a
// Val is never simultaneously a float expectation and a reference one.
//
// Class and Extern are the reference half, meaningful only when Kind.isRef(); see RefClass for
// what each shape means and Extern's own comment for the identity's provenance.
type Val struct {
	Kind ValKind
	Bits uint64
	NaN  NaNClass

	// Class is RefNone for every numeric Val and one of the three RefClass members for a
	// reference one.
	Class RefClass

	// Extern is the opaque identity for RefExternIdentity — `ref.extern N`'s and `ref.host N`'s N,
	// read as a plain uint32 per Extern's own comment. Unread for every other Class, and 0 is a
	// legitimate identity (`ref.extern 0` appears in the corpus), so it must never be read as
	// "unset".
	Extern uint32

	// Pat is the heaptype a bare `(ref.<ht>)` expectation names — **`want`-side only**, and unread
	// for every Class but RefTypePattern. See RefPat.
	Pat RefPat

	// Payload is which constructor a non-null reference **result** is — **`got`-side only**, set by
	// `fromInterpValue` and read only by Matches' RefTypePattern arms. See RefPayload.
	//
	// **The two fields are one dispatch's two operands and that is why they are two fields rather
	// than one shared one.** `assert_ref_pat` matches a *pattern* against a *value*: they range over
	// different sets (eight heaptypes against seven constructors, related by a table and not by a
	// bijection — `PatEq` admits three), and a Val is never both. A single field would make
	// `want.X == got.X` look like the comparison, which is exactly the wrong comparison: `(ref.eq)`
	// must match an array.
	Payload RefPayload

	// AnyNull is set only for the bare `(ref.null)` expectation — 13 vectors in the corpus,
	// `ref_null.wast`/`select.wast`/`table.wast`/`instance.wast` — which the reference's own
	// script grammar reads as "null, of *any* heap type" with no keyword naming which
	// (`literal_null`/`result`'s own `LPAR REF_NULL RPAR` arm, parser.mly:1519,
	// `RefResult (RefPat (Value.NullRef @@ sloc))` — no heaptype argument at all, unlike the
	// keyworded arm two lines below it). Kind holds whatever readRefConst assigned it
	// (KindFuncRef, arbitrarily, since some Kind value is needed to keep this Val constructible
	// without a third "no kind" sentinel), and reading it as a real constraint would wrongly
	// refuse an externref result the way a stray Kind tag refused it before this field existed.
	//
	// **This field no longer changes what Matches answers** (grave #266): a `ref.null <ht>`
	// expectation is *already* Kind-blind against a null result, the reference having exactly one
	// null value with no heaptype in it, so the bare spelling asks for nothing the keyworded one
	// does not. What survives is the argument side — `isPassable` refuses this shape because
	// `toInterpValue`'s `interp.NullRef(t)` needs a concrete type, and here there is no keyword to
	// derive one from. So the field's meaning narrowed from "matches Kind-blind" to "unpassable",
	// which is the half the reference also treats as a real distinction: `literal_null`'s bare arm
	// appears in `result` position only.
	AnyNull bool

	// Alts holds the alternatives of an `(either <result> <result>*)` expectation — relaxed
	// SIMD's non-determinism form, and the only shape in this harness where one expectation
	// admits more than one bit pattern. Non-nil means *this Val is a disjunction*, and then
	// every other field on it is meaningless: `Matches` dispatches on Alts before it reads Kind,
	// exactly as the reference's own `assert_result` does (`script/runner.ml:485`, `| _,
	// EitherResult rs -> List.exists (assert_result v) rs` — note the wildcard on the *value*,
	// so the disjunction applies to any result kind and not just to vectors).
	//
	// Recursive, because the grammar is: `LPAR EITHER result list(result) RPAR` (parser.mly:1536)
	// takes `result`s, and `EitherResult of result list` (script.ml:44) is an arm *of* `result`,
	// so an alternative may itself be an `either`. No corpus vector nests one, measured — and it
	// is read recursively anyway, because the alternative is a reader that would decline a shape
	// the reference accepts, and *a shape this harness declines is scored unsupported*: a silent
	// decline on a nested form would look like a vector nobody had got to yet rather than a
	// reader that stopped short.
	//
	// **The widths are 2, 3 and 4, not 2.** This sentence previously read "all 32 occurrences are
	// two flat alternatives", which was wrong in its second half and wrong in an instructive way:
	// 32 is the count of `(either` *sites* — a `grep -c` — and the width of a site is a different
	// measurement that was never taken, so a census of sites was written up as a census of their
	// contents. Measured over the parsed corpus: 32 sites, **17 of width 2, 2 of width 3, 13 of
	// width 4**, zero nested. The three- and four-wide ones are where the freedom is widest and
	// the lowering pin has the most to say — `relaxed_dot_product.wast`'s own comments name each
	// alternative's lowering (signed×signed, signed×unsigned, unsigned×unsigned), which is the
	// corpus stating outright that these lists are a menu of implementations. Grave #282.
	//
	// **Result position only.** `either` is not an arm of `literal` or of any const form, so it
	// cannot appear as an argument; `readResult` is where it is admitted and `readConst` never
	// sees it. `isPassable` refuses it regardless, since a disjunction is a predicate over a
	// value and not a value — the same asymmetry the NaN classes and the ref patterns have.
	Alts []Val

	// Hi is a v128 Val's high 64 bits, meaningful only when Kind == KindV128 — Bits carries the
	// low 64 bits for the identical Kind, mirroring `interp.Value`'s own Hi/Bits pair exactly
	// (decision 0024's own boundary shape, restated here rather than imported for ValKind's own
	// neutrality reason). Set by `fromInterpValue` for a v128 *result*, where no lane shape is
	// knowable — an engine result is 128 raw bits, not a shape-tagged value — and read by
	// `Matches` to slice a `got` Val into `want`'s own shape at comparison time. Never set by
	// `readV128Const`, whose own output always carries a shape and populates Lanes instead.
	Hi uint64

	// Lanes holds a v128 Val's per-lane scalar values, meaningful only when Kind == KindV128
	// and non-nil — Bits/Hi are the *raw-bits* reading of a v128 (a result, shapeless) and
	// Lanes is the *shaped* reading (an expectation or argument, always built from a
	// `v128.const shape ...` literal that names its own lane count and width); a v128 Val is
	// never both at once, since `readV128Const` never sets Hi and `fromInterpValue` never sets
	// Lanes. Each entry is an ordinary numeric Val (Kind one of KindI32/I64/F32/F64, never
	// KindV128 or a reference kind) at the shape's own lane width — an `i8x16`/`i16x8` lane
	// widens to KindI32 the same way `readIntLit`'s own literal reader does for a bare
	// `i32.const`, since the suite's v128 lane grammar admits no narrower literal kind than i32.
	//
	// **Decomposed at this boundary rather than compared as a wider bit pattern** (decision
	// 0024's forced question 5): `Matches` calls itself once per lane, reusing its own existing
	// scalar comparison — including per-lane NaN-class matching, which a single wider `NaN`
	// field could not express (the suite's own vectors mix exact-value lanes and NaN-class
	// lanes in one `v128.const`, e.g. `simd_f32x4_arith.wast:732`).
	Lanes []Val

	// LaneBits is a v128 lane's *wire* width in bits — 8/16/32/64 — meaningful only on a Val
	// that is itself an entry of some other Val's Lanes. This is deliberately **not**
	// `Kind.width()`: an i8x16 or i16x8 lane widens to KindI32 for storage (Lanes's own doc
	// comment), so Kind.width() reports 32 for a lane whose actual position in the v128's 128
	// bits is 8 or 16 wide. sliceV128Lanes/packV128Lanes need the wire width to place a lane
	// correctly — using the storage width there was a real bug this field exists to fix (caught
	// by TestPackAndSliceV128LanesRoundTrip's i8x16/i16x8 cases, which is exactly why the round
	// trip is tested at every tracked width and not only i32x4).
	LaneBits uint
}

func (v Val) String() string {
	if v.Alts != nil {
		// Printed as the disjunction it is, because this string lands in a mismatch message's
		// `Expect` field: reporting only the first alternative there would make a genuine fail
		// read as a near-miss against a value the engine was never obliged to produce, and *an
		// error message is testimony*.
		parts := make([]string, len(v.Alts))
		for i, alt := range v.Alts {
			parts[i] = alt.String()
		}
		return "either(" + strings.Join(parts, " | ") + ")"
	}

	if v.Kind.isRef() {
		switch v.Class {
		case RefLiteralNull:
			// **No heaptype, in either direction** — `runtime/value.ml:322` is
			// `| NullRef -> "null"`, and the reason is structural rather than stylistic: a null
			// carries no heaptype to print (`type ref_ += NullRef`, nullary), and `runner.ml:365`
			// discards the one a `(ref.null ht)` literal spells (`NullLit ht -> Value.(Ref
			// NullRef)`). Rendering `v.Kind` here named a heaptype the value does not have, which
			// on the *result* side is an outright fabrication: `fromInterpValue` collapses an
			// unnameable reftype to a placeholder Kind (grave #266), so a null `anyref` result
			// printed as "ref.null externref" would be the engine quoting a type nothing produced.
			// The cost is that a mismatch message no longer echoes which heaptype the *vector*
			// spelled; the vector's own file:line is in the same message, and an honest message
			// with less detail beats a detailed one that invents.
			return "ref.null"
		case RefExternIdentity:
			// **Which of the two spellings, decided by Kind**, because the class covers both: a host
			// identity is `(ref.extern N)` when it has been externalized and a bare `(ref.host N)`
			// when it has not (`parser.mly:1502` against `:1501`), and printing the wrapper on a
			// value that has none would fabricate an externalization — the same fabrication
			// `interp.ExternRef`/`interp.HostRef` keep apart with their static types.
			if v.Kind == KindExternRef {
				return fmt.Sprintf("ref.extern %d", v.Extern)
			}
			return fmt.Sprintf("ref.host %d", v.Extern)
		case RefTypePattern:
			// The **pattern's** heaptype, not the Kind's name: this used to print
			// `(ref.externref)` — the Kind's spelling, one letter's worth of coincidence away from
			// the corpus's own `(ref.extern)` — and there was no way at all to print `(ref.array)`
			// because the heaptype was implied by a Kind that had no member for it. Since 0039 the
			// heaptype is carried, so the message quotes what the vector wrote.
			return "(ref." + v.Pat.String() + ")"
		case RefConcrete:
			// The **constructor**, since 0039 carries it: "a non-null array" rather than "a non-null
			// anyref", which named the upper bound the result crossed at and not the result. Falls
			// back to the Kind's own name when the payload is PayloadNone, where there is nothing
			// more specific to say — RefPayload's String spells that case "reference".
			if v.Payload != PayloadNone {
				return "a non-null " + v.Payload.String()
			}
			return "a non-null " + v.Kind.String()
		case RefNone:
			// Unreachable given v.Kind.isRef() (RefNone means "not a reference Val" by its own
			// doc comment), named explicitly rather than left to a bare default so `exhaustive`
			// can confirm every RefClass member has a stated reading here, not just the ones
			// this function's author remembered to write an arm for.
		}
		return "unrepresentable ref"
	}
	if v.NaN != NaNNone {
		return fmt.Sprintf("%s %s", v.Kind, v.NaN)
	}
	if v.Kind == KindV128 {
		// A shaped Val (readV128Const's own output) prints each lane; a raw one
		// (fromInterpValue's own output — a result, with no shape of its own) prints the two
		// 64-bit halves, since there is no shape to slice it by. Printed here rather than left
		// to the generic `int64(v.Bits)` fallback below, which reported a v128 result as a
		// signed 64-bit integer of its *low* half alone — losing the high half and every
		// float/NaN reading a mismatch message needs to be useful.
		if v.Lanes != nil {
			parts := make([]string, len(v.Lanes))
			for i, lane := range v.Lanes {
				parts[i] = lane.String()
			}
			return fmt.Sprintf("v128 [%s]", strings.Join(parts, ", "))
		}
		return fmt.Sprintf("v128 hi=%#016x lo=%#016x", v.Hi, v.Bits)
	}
	if v.Kind.isFloat() {
		if v.Kind == KindF32 {
			return fmt.Sprintf("%s %#08x (%v)", v.Kind, uint32(v.Bits), math.Float32frombits(uint32(v.Bits)))
		}
		return fmt.Sprintf("%s %#016x (%v)", v.Kind, v.Bits, math.Float64frombits(v.Bits))
	}
	return fmt.Sprintf("%s %d", v.Kind, int64(v.Bits))
}

// kindGateReach is one arrival at `matches`' `want.Kind != got.Kind` gate, on a vector that went
// on to pass. `Want == Got` means the gate let the pair through; the two differing means it
// refused, and refusal is the whole point of the census — see #441.
//
// **Enumerated rather than counted, and the two are not interchangeable here.** A count would say
// how much traffic the gate sees; #441 needs to know *which* vectors' green depends on it, because
// the change under consideration is in the accept direction and no vector in the corpus can
// witness a wrong acceptance. A line and a result index are what make a row re-checkable by hand
// against the reference.
// **Kind is not enough to re-check a row, so the pattern and the constructor travel with it.**
// The question a reader has to answer for each refusal is what `assert_ref_pat` would say, and that
// function reads neither Kind: it dispatches a `RefTypePat <heaptype>` against a runtime
// constructor. A row saying only "want externref, got anyref" cannot be adjudicated — `RefTypePat
// ExternHT, _ -> true` admits every constructor, so whether the refusal is wrong depends on
// `WantClass`/`WantPat`, which the Kind pair does not carry.
type kindGateReach struct {
	Line   int     // the assert_return's line, stamped by the run loop
	Result int     // which result of that command, for the multi-value case
	Want   ValKind // the expectation's Kind as the gate read it
	Got    ValKind // the engine result's Kind as the gate read it
	InAlt  bool    // the reach happened inside an `(either …)`'s alternatives — see matchingAlt

	WantClass  RefClass   // which reference shape the expectation is, if any
	WantPat    RefPat     // the heaptype it names, read only for RefTypePattern
	GotClass   RefClass   // which reference shape the result is
	GotPayload RefPayload // the constructor the result is — what the authority dispatches on
}

// kindGateCensus accumulates every arrival at that gate for one command.
//
// **Two channels, because two populations of wildly different size answer two different
// questions.** `numericEqual` is the bulk — every `i32 1` against `i32 1` in the corpus reaches
// this gate and is let through — and enumerating it would bury the rows that matter under six
// figures of noise; it is counted so that the census can be checked for vacuity, since a census
// reporting no numeric traffic has stopped walking rather than found nothing. Everything else is
// enumerated: any reach touching a reference kind on either side (the population #441's
// replacement changes the reading of) and any numeric pair the gate refuses.
//
// The census's domain is the **passing** population, and the run loop enforces that by discarding
// it on the fail path — `AltChoices`' scoping for `AltChoices`' reason. That is not a limitation
// being hidden: a failing vector's census is a *prefix*, since `firstMismatch` stops at the first
// bad result, and a prefix of a walk reported beside complete walks would be a silent
// under-count.
type kindGateCensus struct {
	numericEqual int
	reaches      []kindGateReach

	// line and result are the run loop's stamp for the command being compared, set before each
	// call rather than passed down through `matches` — the matcher has no business knowing what
	// line it is serving, and threading it would put a harness concern in a comparison.
	line, result int

	// inAlt is a depth rather than a bool because `either` nests in the grammar (`parser.mly:1536`
	// takes `result`s, and `EitherResult` is an arm of `result`). No corpus vector nests one,
	// measured — and a bool would be wrong the day one does, in the direction of under-reporting.
	inAlt int
}

func (c *kindGateCensus) record(want, got Val) {
	if want.Kind == got.Kind && !want.Kind.isRef() {
		c.numericEqual++
		return
	}
	c.reaches = append(c.reaches, kindGateReach{
		Line: c.line, Result: c.result, Want: want.Kind, Got: got.Kind, InAlt: c.inAlt > 0,
		WantClass: want.Class, WantPat: want.Pat, GotClass: got.Class, GotPayload: got.Payload,
	})
}

// MatchingAlt reports which alternative of an `(either …)` expectation `got` satisfies, and
// whether any does. It returns `(-1, false)` for an expectation that is not a disjunction: the
// question "which alternative" has no answer there, and a zero index would be a wrong one.
//
// **This exists because `Matches` answers a question that is too coarse for the guarantee
// decision 0028 makes.** `either` is the corpus admitting more than one legal answer, so a
// vector carrying one passes whichever the engine picked — which is correct scoring and blind
// to the *choice*. 0028 d1 promises the choice is deterministic and architecture-uniform, and
// that promise is about the index this returns. Without it the pin would have to compare board
// text or re-invoke a second oracle; with it the run loop can record what it matched, the same
// shape `GatedAt` uses for the same reason.
//
// The search is `List.exists`'s order (runner.ml:485) — first match wins, and the alternatives
// are not disjoint in general, so "which" means "the first that accepts" and not "the only
// one". A corpus with two alternatives accepting the same value would pin the earlier, and that
// is a property of the corpus rather than a fact about the engine; `Of` beside the index in
// AltChoice is what lets a reader see the width of the freedom being reported on.
func (want Val) MatchingAlt(got Val) (int, bool) { //nolint:revive // `want`/`got` names the asymmetry, as on Matches below — the two must agree, since this is the method Matches delegates its Alts arm to
	return want.matchingAlt(got, nil)
}

func (want Val) matchingAlt(got Val, rec *kindGateCensus) (int, bool) { //nolint:revive // `want`/`got` again: this is MatchingAlt's body and the two must agree, per that method's own note
	if want.Alts == nil {
		return -1, false
	}
	// Recursion, not a loop over scalars: an alternative is a `result`, so it may be a NaN
	// class, a shaped v128, a ref pattern, or another `either`, and each of those has its own
	// arm in Matches. Calling Matches is what reuses them all.
	//
	// **The census is told it is inside a disjunction, and this is the only place that can tell
	// it.** An alternative that does not match is not a defect here — `List.exists` is *supposed*
	// to be told no by every alternative but one — so a `Kind` gate firing under this loop is the
	// one context in which the gate returns false inside a vector that goes on to pass. Every
	// other false it produces is the vector's own verdict. See kindGateCensus.
	if rec != nil {
		rec.inAlt++
		defer func() { rec.inAlt-- }()
	}
	for i, alt := range want.Alts {
		if alt.matches(got, rec) {
			return i, true
		}
	}
	return -1, false
}

// Matches reports whether an engine result satisfies this expectation.
//
// Exact expectations compare **bit for bit including the type tag**, because `i32 0` and `f32
// 0.0` share a pattern and are different values. The NaN classes compare structurally and
// ignore the sign, which is the whole reason they exist as a separate concept.
//
// The reference half mirrors that structure rather than falling back to a bit comparison:
// RefTypePattern is a *predicate* over `got`, exactly as the NaN classes are predicates over a
// float's bit pattern, and Extern's zero value (0) is a legitimate externref identity —
// comparing Bits/Extern the way the numeric arm's exact case does would make `(ref.extern 0)`
// match every reference class, silently, since a zero Val also zero-values Extern.
//
// The receiver is named `want` where `String`'s is `v`, and the asymmetry is the reason: this is
// the only method taking a second Val, and `v.Kind != got.Kind` would leave a reader to work out
// which side is the expectation. Renaming `String`'s receiver to `want` would be worse — it is not
// an expectation there — so the two linters that want one name per type are suppressed here, at
// the site that earns it, rather than the pair being made consistent and less clear.
func (want Val) Matches(got Val) bool { //nolint:revive,staticcheck // `want`/`got` names the asymmetry; see above
	return want.matches(got, nil)
}

// matches is Matches' body, with an optional census of the `want.Kind != got.Kind` gate below.
//
// **The recorder is threaded rather than the condition re-derived**, which is #441's own
// requirement and `AltChoices`' rule ("recorded by the run loop rather than re-derived by the
// control"). Whether a pair *reaches* that gate is a fact about this function's control flow —
// two arms return before it — so a census computed at the call site would be a second copy of
// that flow, and the two copies could be pointed at different sets without either looking wrong.
// A nil recorder is the production path and costs one branch per gate.
// `revive` only, where the exported `Matches` needs `staticcheck` too: nolintlint reported the
// `staticcheck` half unused here, and a suppression carried for a complaint that is not made is a
// suppression nobody has read. Narrowed rather than copied from the line above.
func (want Val) matches(got Val, rec *kindGateCensus) bool { //nolint:revive // `want`/`got` names the asymmetry; see Matches
	if want.Alts != nil {
		// `| _, EitherResult rs -> List.exists (assert_result v) rs` (runner.ml:485). **First**,
		// and before Kind is read, because the reference's arm matches on the *pattern* with a
		// wildcard for the value — a disjunction carries no Kind of its own, so a `want.Kind !=
		// got.Kind` gate reached before this point would compare `got` against a zero value and
		// refuse every `either` vector in the corpus while looking like a type check.
		//
		// Delegated rather than looped here, so the search for a satisfying alternative has one
		// authority: MatchingAlt answers *which*, this answers *whether*, and a `List.exists`
		// written twice is the two-places-know-one-fact shape on a question the lowering pin
		// depends on.
		_, ok := want.matchingAlt(got, rec)
		return ok
	}
	// **No `want.AnyNull` fast path here** — it used to be the first thing this function did
	// (`return got.Class == RefLiteralNull`), and the null-`got` dispatch below subsumes it
	// exactly: a null `got` reaches the RefLiteralNull arm and returns true whatever Kind the
	// bare `(ref.null)` expectation is carrying, and a non-null `got` is refused either by the
	// Kind comparison or by the RefLiteralNull arm one screen down. Verified case by case, and
	// pinned by TestRefNullMatchesAcrossTwoHeaptypes's bare-`(ref.null)` rows rather than left
	// to that argument.
	//
	// Deleted rather than kept as a harmless shortcut, because an early return that agrees with
	// the code it skips agrees only *today*: it is a fast path that bypasses the general null
	// dispatch, so the next refinement of that dispatch would silently not apply to the one
	// expectation shape whose whole point is that its Kind means nothing. The field itself stays
	// live on the argument side, where a bare `(ref.null)` genuinely cannot be passed —
	// `isPassable`, whose `interp.NullRef(t)` needs a heaptype this shape does not have.
	if got.Class == RefLiteralNull && want.Kind.isRef() {
		// GRAVE #266: a null `got` is dispatched on the *pattern* alone, and its own Kind is
		// never read — because in the reference a null carries no heaptype to read.
		// `runtime/value.ml:20` is `type ref_ += NullRef`, a **nullary** constructor; `:112`
		// types it `(Null, BotHT)` whatever it came from; `:151` makes every nullable type's
		// default that one same value. So `Val{KindFuncRef, RefLiteralNull}` and
		// `Val{KindExternRef, RefLiteralNull}` are two spellings of one reference value, and a
		// `want.Kind != got.Kind` gate above this point asserted a distinction with no referent.
		//
		// The arms are `assert_ref_pat`'s (`script/runner.ml:464-476`), not a generalization of
		// them: only `NullPat _` and `RefTypePat ExternHT` have arms admitting `NullRef`, and
		// every other pattern falls through OCaml's catch-all to `false`. Written as an explicit
		// refusal per class rather than as `return want.Class == RefLiteralNull || …` so a new
		// RefClass member cannot be silently admitted by a boolean that happens to be false.
		switch want.Class {
		case RefLiteralNull:
			// `NullPat _, Value.NullRef -> true` (runner.ml:476), unconditional.
			return true
		case RefTypePattern:
			// `RefTypePat ExternHT, _ -> true` (runner.ml:475) admits anything including a
			// null; `RefTypePat FuncHT` has no NullRef arm and so refuses one. That asymmetry
			// is the reference's, and it is why this is not `return true`.
			//
			// **Dispatched on the pattern's heaptype since 0039, where it used to read
			// `want.Kind == KindExternRef`.** The two agreed while there were only two patterns —
			// `KindExternRef` *was* ExternHT — and stop agreeing the moment six more exist, because
			// five of them carry Kind KindAnyRef and one of those five (`(ref.any)`) admits a null
			// while the other four refuse it. A Kind comparison cannot express that, and would have
			// answered *false* for `(ref.any)` against a null: a wrong answer, not a decline.
			return want.Pat.admitsNull()
		case RefExternIdentity:
			// `RefResult (RefPat r)` compares two concrete references; a null is not one.
			return false
		case RefNone, RefConcrete:
			// Unreachable as a `want` — RefNone means "not a reference Val" and RefConcrete is
			// result-only (fromInterpValue's own doc comment). Named so `exhaustive` confirms
			// every member has a stated reading here, matching this function's other switch.
			return false
		}
		return false
	}
	// **#441's subject.** The authority has no analogue for this comparison: `assert_ref_pat`
	// (`script/runner.ml:464-476`) matches a pattern against the runtime *constructor* and reads
	// no static type, so on the reference path this gate asks a question the reference does not
	// ask. It is recorded before it is read, and both outcomes are recorded — a census of arrivals
	// is the accept-direction evidence #441 needs, and no negative-direction vector can supply it.
	if rec != nil {
		rec.record(want, got)
	}
	if want.Kind != got.Kind {
		return false
	}
	if want.Kind == KindV128 {
		// Decision 0024's forced question 5: decompose into per-lane scalar comparisons rather
		// than teaching this function a wider comparison shape. `want` is always shaped
		// (`readV128Const`'s own output, from a `v128.const shape ...` literal that names its
		// lane count and width); `got` is always raw (`fromInterpValue`'s output — an engine
		// result is 128 bits with no shape of its own), so `want`'s own shape is what slices
		// `got`'s bits into comparable lanes, never the other way around.
		gotLanes := sliceV128Lanes(got.Hi, got.Bits, want.Lanes)
		for i := range want.Lanes {
			if !want.Lanes[i].matches(gotLanes[i], rec) {
				return false
			}
		}
		return true
	}
	if want.Kind.isRef() {
		switch want.Class {
		case RefTypePattern:
			// `assert_ref_pat`'s eight RefTypePat arms, against the **constructor** the result is —
			// which is what `got.Payload` carries and what this arm could not see before 0039. It
			// used to `return true` unconditionally, correctly, because the only two patterns
			// representable were FuncHT and ExternHT and Kind had already separated them; with six
			// more patterns and five of them sharing Kind KindAnyRef, Kind no longer decides
			// anything and the bare `true` would admit an array where `(ref.i31)` was asked for.
			//
			// **The null half stays above**, in the null-`got` dispatch (grave #266), so `got` here
			// is non-null.
			return want.Pat.admits(got.Payload)
		case RefLiteralNull:
			// A non-null `got` against a `ref.null <heaptype>` expectation — always a mismatch.
			// The null-vs-null case, which is the one `NullPat _, NullRef -> true` governs, is
			// answered above without consulting Kind (grave #266); by the time control reaches
			// here `got.Class != RefLiteralNull` is known, so this is the refusal and not the
			// comparison it used to be.
			return false
		case RefExternIdentity:
			return got.Class == RefExternIdentity && want.Extern == got.Extern
		case RefNone, RefConcrete:
			// RefNone is unreachable given want.Kind.isRef(); RefConcrete is a result-only
			// shape that never appears as `want` (fromInterpValue's own doc comment in
			// spec_test.go). Both named explicitly so `exhaustive` confirms every RefClass
			// member has a stated reading, rather than falling through a bare default that
			// would silently also cover a genuinely new member arriving later.
		}
		return false
	}
	switch want.NaN {
	case NaNCanonical:
		return isCanonicalNaN(got.Bits, got.Kind.width())
	case NaNArithmetic:
		return isArithmeticNaN(got.Bits, got.Kind.width())
	default:
		// NaNNone: an exact expectation, compared bit for bit.
		return want.Bits == got.Bits
	}
}

// isPassable reports whether v is a value this harness can hand to an engine as a call
// argument, as opposed to an *expectation*-only shape.
//
// Two families are expectation-only: a NaN class (NaNCanonical/NaNArithmetic) and RefTypePattern
// are *predicates* over a result, not concrete values a caller could construct and pass —
// `nan:canonical` names a set of bit patterns and `(ref.func)` names any value of a shape. AnyNull
// is refused for a **different** reason, and calling it a predicate too (as this comment did) was
// wrong in a way that mattered: a bare `(ref.null)` names exactly one value, perfectly concrete,
// and what it lacks is a *heaptype* for `toInterpValue`'s `interp.NullRef(t)` to be built from.
// The mis-stated reason is what let that function's own AnyNull refusal go missing — see its doc
// comment, and grave #266, whose sweep found it. Both families were
// checked the same way before this helper existed (`v.NaN != NaNNone` at invokeAction's and
// namedInvokeAction's argument loops); this names the check once so a third expectation-only
// shape arriving later has one site to extend rather than two to keep in sync — the same
// one-authority reasoning invokeAction's own doc comment gives for sharing readConst itself.
func (v Val) isPassable() bool {
	if v.Alts != nil {
		// An `(either …)` disjunction is a predicate over a value, not a value — nothing can
		// be *passed* for "one of these two". Unreachable through the grammar (`either` is an
		// arm of `result`, never of a const form, so `readConst` cannot produce this and only
		// `readResult` can), and refused here anyway on the same ground as the NaN classes
		// below it: the asymmetry is a statement about which vectors are askable, and it
		// belongs where that statement is made rather than resting on a grammar argument.
		return false
	}
	if v.NaN != NaNNone {
		return false
	}
	if v.Kind.isRef() && (v.Class == RefTypePattern || v.AnyNull) {
		return false
	}
	if v.Kind == KindV128 {
		// A v128 argument's own lanes must each be a concrete value — `readV128Const` admits a
		// NaN-class spelling in any lane position because the same grammar produces both
		// arguments and results, but the corpus's own argument-side v128 constants never write
		// one (measured: 0 `v128.const` argument occurrences of `nan:canonical`/`nan:arithmetic`
		// across testdata/spec) and this check is what makes that a stated fact rather than an
		// unenforced assumption — a future vector that did write one would be declined here,
		// named, rather than silently passed through as a wrong bit pattern.
		for _, lane := range v.Lanes {
			if !lane.isPassable() {
				return false
			}
		}
	}
	return true
}

// nanFields returns the exponent mask, the mantissa's top bit, and the payload mask for a width.
//
// Derived from the width rather than written per case, so the f32 and f64 arms cannot disagree
// about a rule they share: exponent is `bits - mantissa - 1` wide, mantissa is 23 or 52.
func nanFields(width uint) (expMask, quietBit, payloadMask uint64) {
	mantissa := uint(23)
	if width == 64 {
		mantissa = 52
	}
	expBits := width - mantissa - 1
	expMask = ((uint64(1) << expBits) - 1) << mantissa
	quietBit = uint64(1) << (mantissa - 1)
	payloadMask = quietBit - 1
	return expMask, quietBit, payloadMask
}

// isArithmeticNaN reports whether a pattern is a quiet NaN of the given width.
//
// The sign bit is masked off rather than tested, per the class's definition. What is *not*
// masked off is the payload: a NaN with the quiet bit clear is a signalling NaN, which
// `nan:arithmetic` does not admit — an operation is required to produce a quiet one.
func isArithmeticNaN(b uint64, width uint) bool {
	expMask, quietBit, _ := nanFields(width)
	b &= (uint64(1) << (width - 1)) - 1 // drop the sign
	return b&expMask == expMask && b&quietBit != 0
}

// isCanonicalNaN reports whether a pattern is the width's canonical NaN.
//
// Canonical is strictly narrower than arithmetic: the payload below the quiet bit must be
// **zero**. Written as a payload test rather than an equality against a literal so the f32 and
// f64 cases share one derivation, and so the relationship between the two classes (subset) is
// visible in the code rather than asserted in a comment.
func isCanonicalNaN(b uint64, width uint) bool {
	_, _, payloadMask := nanFields(width)
	if !isArithmeticNaN(b, width) {
		return false
	}
	return b&payloadMask == 0
}

// readConst converts a `(iN.const …)` / `(fN.const …)` node to a Val.
//
// # Why this reader exists rather than the engine's
//
// **13670 of the 13671 answerable `assert_return` vectors get their module from wat source**,
// which means `text`'s literal reader produced the module's own constants. 1111 of them invoke
// a function whose body contains a `const`, so for those the same reader would supply *both*
// the immediate under test and the expected answer: a literal-conversion bug shifts the two
// together and the vector passes by construction. That is grave #106's shape exactly — a
// premise measured with the subject's own instrument is an echo — and it lands on the worst
// possible files, `const.wast` (300) and `float_literals.wast` (98) being the two whose entire
// purpose is literal conversion.
//
// So the expectation side is read here, independently, from the reference's own derivations
// (fxx.ml / ixx.ml) rather than from `internal/text/num.go`. What the two share is
// `strconv.ParseFloat`, which is the Go standard library and not the subject; what they do not
// share is the sign XOR, the NaN payload construction, the width dispatch, the integer range
// composition, and the hex-float exponent graft — every place a bug would be *ours*.
// TestHarnessAndEngineLiteralReadersAgree cross-checks them over the corpus's distinct
// spellings, so the duplication is a second opinion rather than a drift.
//
// # Range failures are classification errors, not verdicts
//
// An `assert_return`'s expected literal is part of a *valid* vector, so a literal this reader
// cannot convert means the reader is wrong about the grammar — never that the vector is
// malformed. It is therefore reported as `ok=false` and the command is recorded unsupported
// with its head, where the column names it; it must never become a fail, which would score the
// harness's gap as an engine defect.
func readConst(n node) (Val, bool) {
	if v, ok := readRefConst(n); ok {
		return v, true
	}
	if n.isList() && n.head() == "v128.const" {
		return readV128Const(n)
	}
	if !n.isList() || len(n.list) != 2 || n.list[1].isList() || n.list[1].isS {
		return Val{}, false
	}
	var k ValKind
	switch n.head() {
	case "i32.const":
		k = KindI32
	case "i64.const":
		k = KindI64
	case "f32.const":
		k = KindF32
	case "f64.const":
		k = KindF64
	default:
		return Val{}, false
	}
	lit := n.list[1].atom
	if k.isFloat() {
		return readFloatLit(k, lit)
	}
	return readIntLit(k, lit)
}

// readResult reads one *result* position of an `assert_return`, which is a wider grammar than
// `readConst`'s: the reference's `result` (script.ml:41-45) has an `EitherResult` arm that
// `literal` does not, so the two are separate readers rather than one reader with a flag.
//
// The split is the argument/result asymmetry made structural. `readConst` is reached from both
// positions and must stay the *narrower* of the two, because a shape admitted there becomes
// passable-by-default and `isPassable` then has to claw it back; a shape admitted only here
// cannot leak into an argument position at all. Same seam `invokeAction`'s own `isPassable` call
// guards, one layer up.
func readResult(n node) (Val, bool) {
	if n.isList() && n.head() == "either" {
		// `LPAR EITHER result list(result) RPAR` (parser.mly:1536) — `$3 :: $4`, so **at least
		// one** alternative. A bare `(either)` is not in the grammar, and it is refused rather
		// than read as a disjunction of nothing, which would be a `Matches` that returns false
		// for every value: a vector no engine can pass, scored as a fail against the engine.
		// That is *a comparison against an empty set* with the polarity inverted, and it is the
		// reason this length check is not merely defensive.
		if len(n.list) < 2 {
			return Val{}, false
		}
		alts := make([]Val, 0, len(n.list)-1)
		for _, e := range n.list[1:] {
			// Recursive, per Alts' own comment: an alternative is a `result`, not a `literal`.
			v, ok := readResult(e)
			if !ok {
				return Val{}, false
			}
			alts = append(alts, v)
		}
		return Val{Alts: alts}, true
	}
	return readConst(n)
}

// v128LaneShapes maps a `v128.const` shape keyword to its lane count and per-lane width/kind —
// the harness's own reading of parser.mly's VECSHAPE list, mirroring `internal/text/instr.go`'s
// identically-purposed `vecShapeLanes` at the encoder's own boundary (two packages, two
// neutrality domains per contract §0, hence two copies of one fact rather than a shared import).
var v128LaneShapes = map[string]struct {
	lanes    int
	bits     uint
	isFloat  bool
	laneKind ValKind // the widened storage kind: KindI32/KindI64 for integer shapes narrower
	// than 32 bits, KindF32/KindF64 for float shapes — matching the interpreter's own
	// zero-extend-to-a-full-slot convention (stack.go's pushNum).
}{
	"i8x16": {lanes: 16, bits: 8, laneKind: KindI32},
	"i16x8": {lanes: 8, bits: 16, laneKind: KindI32},
	"i32x4": {lanes: 4, bits: 32, laneKind: KindI32},
	"i64x2": {lanes: 2, bits: 64, laneKind: KindI64},
	"f32x4": {lanes: 4, bits: 32, isFloat: true, laneKind: KindF32},
	"f64x2": {lanes: 2, bits: 64, isFloat: true, laneKind: KindF64},
}

// readV128Const reads `(v128.const <shape> <lane>*)` into a KindV128 Val whose Lanes field
// holds one scalar Val per lane — decision 0024's forced question 5, the harness-side half:
// every lane is read through the identical scalar readers (`readIntLitBits`/`readFloatLit`)
// bare `i32.const`/`f32.const` literals already use, so a lane's NaN-class spelling
// (`nan:canonical`/`nan:arithmetic`) and exact-value spelling are both admitted exactly as they
// already are for a scalar float result — `simd_f32x4_arith.wast:732` mixes both in one
// `v128.const`, which is why each lane is read independently rather than the whole list being
// read as one bit pattern.
//
// The lane count is checked exactly (`wrong number of lane literals` is `internal/text`'s own
// refusal for the identical mismatch on the encoder side; this reader declines the same shape
// as KindUnsupported rather than inventing a spec-shaped error string, since a malformed
// `v128.const` reaching an `assert_return`/argument position is not a vector this corpus writes
// — module-body grammar errors are the encoder's own `assert_malformed` population, disjoint
// from the script-level constant grammar this function reads).
func readV128Const(n node) (Val, bool) {
	if len(n.list) < 2 || n.list[1].isList() || n.list[1].isS {
		return Val{}, false
	}
	shape, ok := v128LaneShapes[n.list[1].atom]
	if !ok {
		return Val{}, false
	}
	lits := n.list[2:]
	if len(lits) != shape.lanes {
		return Val{}, false
	}
	lanes := make([]Val, shape.lanes)
	for i, lit := range lits {
		if lit.isList() || lit.isS {
			return Val{}, false
		}
		if shape.isFloat {
			v, ok := readFloatLit(shape.laneKind, lit.atom)
			if !ok {
				return Val{}, false
			}
			v.LaneBits = shape.bits
			lanes[i] = v
			continue
		}
		bits, ok := readIntLitBits(lit.atom, shape.bits)
		if !ok {
			return Val{}, false
		}
		lanes[i] = Val{Kind: shape.laneKind, Bits: bits, LaneBits: shape.bits}
	}
	return Val{Kind: KindV128, Lanes: lanes}, true
}

// readRefConst converts a `ref.null <heaptype>`, `ref.extern N`, `ref.func` (bare), or
// `ref.extern` (bare) node into a reference Val — the argument and result grammar's shared
// reference half (#196/#197). Called from readConst so every existing caller — invokeAction's
// argument loop, namedInvokeAction's, assertReturn's result loop — gains the reference shapes
// for free rather than needing its own second call.
//
// **Scope is exactly the population #196/#197 measured, not the full `literal_ref`/`result`
// grammar the reference admits.** `ref.func $name`/`ref.func N` as a literal (an *argument* or a
// non-bare result) is declined here structurally — it falls through every case below to `Val{},
// false` — because 0 vectors in the corpus use that shape: measured over testdata/spec,
// `ref.func` appears 1369+734 times as a *module-body* instruction (which `internal/text`
// already reads) and exactly 0 times inside an `(invoke …)` action or as a non-bare
// `assert_return` result. The reference's own `parser.mly:613` reads `ref.func $x` only inside
// the module-body `plaininstr` production, never inside `literal_ref`/`result` — so this is not
// a gap this reader declines, it is a shape the grammar the corpus exercises does not have. If a
// future corpus update adds one, TestFixtureProvenance's sibling coverage tests will find it
// unsupported rather than silently mis-parsed, and it is a widening exactly like ValKind's own
// doc comment describes.
//
// Heap types recognized for `ref.null`: the eight the corpus's `assert_return`/`assert_trap`/
// bare-`invoke` population actually spells — func, extern, any, exn, nofunc, noexn, noextern,
// none (measured, internal/spec's own corpus scan). Kind is KindFuncRef for "func"/"nofunc" and
// KindExternRef for every other named heaptype, because those are the only two reference kinds
// this harness can name (ValKind's own two-member reference scope) and — per RefClass's doc
// comment — the heaptype itself is not retained past this point; only which of the two static
// kinds it belongs to survives, because Matches never compares it. `any`/`exn`/`noexn`/`none` map
// to KindExternRef here on the same "no-name" reasoning `binary.ValType`'s String comment gives
// for its own abstractHeapNames fallback — this harness has no third reference Kind to give them,
// and the two live invoke sites that use them (`ref_null.wast`'s "anyref"/"exnref" exports) never
// pass such a Val to an *engine* boundary that checks its Kind against a declared param/result
// type of anyref/exnref, because #7's engine does not produce those types either. A vector that
// needs the distinction enforced would surface as a genuine type-mismatch fail rather than a
// silent pass, which is the honest failure mode for a scope this reader states rather than hides.
func readRefConst(n node) (Val, bool) {
	if !n.isList() || len(n.list) == 0 {
		return Val{}, false
	}
	switch n.head() {
	case "ref.null":
		// The bare `(ref.null)` form — result-only per the reference's own grammar (no
		// argument spelling exists; readRefConst's callers that read arguments never meet
		// it in practice, but nothing here distinguishes an argument reader from a result
		// reader, so the shape is accepted uniformly and AnyNull carries the fact).
		if len(n.list) == 1 {
			return Val{Kind: KindFuncRef, Class: RefLiteralNull, AnyNull: true}, true
		}
		if len(n.list) != 2 || n.list[1].isList() || n.list[1].isS {
			return Val{}, false
		}
		k, ok := heapKind(n.list[1].atom)
		if !ok {
			return Val{}, false
		}
		return Val{Kind: k, Class: RefLiteralNull}, true
	case "ref.extern":
		// The keyworded form is an identity; the bare form is a pattern and falls through to the
		// table below, which is where all eight bare spellings now live.
		if len(n.list) == 2 {
			if n.list[1].isList() || n.list[1].isS {
				return Val{}, false
			}
			id, ok := readNat(n.list[1].atom, 32)
			if !ok {
				return Val{}, false
			}
			return Val{Kind: KindExternRef, Class: RefExternIdentity, Extern: uint32(id)}, true
		}
	case "ref.host":
		// `literal_ref`'s other arm (`parser.mly:1501`): `LPAR REF_HOST NAT RPAR` is a **bare**
		// `Script.HostRef N`, with no `Extern.ExternRef` wrapper — the same identity `(ref.extern N)`
		// carries, minus the externalization. Same Class, and the difference rides on Kind:
		// KindAnyRef here against KindExternRef above, which is `script.ml:80`'s placement of a host
		// reference's own heaptype at `any`.
		//
		// **There is no bare `(ref.host)` pattern**, so this form requires its N: the grammar's
		// pattern arms are `RefTypePat`s over heaptypes, and `host` is not a heaptype. A
		// one-element `(ref.host)` therefore falls through to the refusal below rather than being
		// read as a pattern nothing in the grammar spells.
		if len(n.list) == 2 && !n.list[1].isList() && !n.list[1].isS {
			id, ok := readNat(n.list[1].atom, 32)
			if !ok {
				return Val{}, false
			}
			return Val{Kind: KindAnyRef, Class: RefExternIdentity, Extern: uint32(id)}, true
		}
		return Val{}, false
	}
	// The bare `(ref.<ht>)` patterns, from one table over the RefPat vocabulary rather than eight
	// switch arms — so a RefPat member without a spelling is a missing row that a control can see,
	// which is the same reason `byteKinds` is computed and `kindNames` is a map.
	if p, ok := refPatterns[n.head()]; ok && len(n.list) == 1 {
		return Val{Kind: p.kind, Class: RefTypePattern, Pat: p.pat}, true
	}
	return Val{}, false
}

// refPatterns pairs each bare `(ref.<ht>)` spelling with the pattern it names and the ValKind a Val
// carrying it takes — the reader's whole pattern half, and the domain
// TestEveryRefPatHasASpelling checks the RefPat vocabulary against.
//
// **The Kind column is what keeps Matches' `want.Kind != got.Kind` gate inert for #270's vectors**,
// and it is chosen by asking what `fromInterpValue` will produce for the results these patterns are
// asserted against, not by picking a plausible name: every reference type this harness cannot name
// arrives as KindAnyRef from `valKind`'s refusal path, so five of the eight patterns must be
// KindAnyRef to meet them. `func` and `extern` keep the two kinds they have always had, because
// `valKind` *can* name `funcref` and `externref` and does.
//
// Populations, measured over the corpus rather than assumed: `ref.array` 17, `ref.eq` 4, `ref.i31` 2,
// `ref.struct` 2 — and **`ref.any` and `ref.exn` are 0**. The two zero rows are here because the
// domain is a fixed grammar production (eight `RefTypePat` arms, `parser.mly:1517-1530`) rather than
// a population that might grow: enumerating it fully costs two lines, and the alternative is a reader
// that declines a shape the reference accepts, which scores as *unsupported* — a vector nobody had
// got to yet rather than a reader that stopped short.
var refPatterns = map[string]struct {
	pat  RefPat
	kind ValKind
}{
	"ref.any":    {PatAny, KindAnyRef},
	"ref.eq":     {PatEq, KindAnyRef},
	"ref.i31":    {PatI31, KindAnyRef},
	"ref.struct": {PatStruct, KindAnyRef},
	"ref.array":  {PatArray, KindAnyRef},
	"ref.exn":    {PatExn, KindAnyRef},
	"ref.func":   {PatFunc, KindFuncRef},
	"ref.extern": {PatExtern, KindExternRef},
}

// heapKind maps a `ref.null` heaptype keyword to the harness's own two-member reference
// ValKind — see readRefConst's comment for why the mapping is many-to-two rather than
// one-to-one.
func heapKind(heaptype string) (ValKind, bool) {
	switch heaptype {
	case "func", "nofunc":
		return KindFuncRef, true
	case "extern", "any", "exn", "noextern", "noexn", "none":
		return KindExternRef, true
	}
	return 0, false
}

// readIntLit is `I32.of_string` / `I64.of_string` (ixx.ml:328-342) as a bit pattern.
//
// The admitted range is **asymmetric**: `[-2^(w-1), 2^w - 1]`. Both halves are load-bearing and
// neither is the other's complement — `i32.const -2147483648` and `i32.const 4294967295` are
// both legal and neither fits the range the other needs. So a signed return type could not hold
// the answer, which is why this is a pattern.
//
// Negation is two's complement on the pattern (`Rep.neg`), *not* the sign-bit XOR floats use.
// Getting the two negations confused gives `i32.const -1` the pattern 0x80000001, which is a
// plausible wrong answer rather than an obvious one.
func readIntLit(k ValKind, s string) (Val, bool) {
	w := k.width()
	n, ok := readIntLitBits(s, w)
	if !ok {
		return Val{}, false
	}
	return Val{Kind: k, Bits: n}, true
}

// readIntLitBits is readIntLit's own bit-pattern logic, parameterized by a raw width rather
// than a ValKind — a v128 lane's width (8/16 for i8x16/i16x8) has no scalar ValKind of its own,
// since the harness's four numeric kinds are exactly i32/i64/f32/f64 (ValKind's own doc comment)
// and a v128 lane at a narrower width still widens into one of those two integer kinds for
// storage in Val.Lanes — matching the interpreter's own convention (stack.go's pushNum: a
// narrow lane occupies a full slot, zero-extended, never sign-extended) and readIntLit's own
// existing w==32 zero-extension for i32 itself, generalized to an arbitrary source width.
func readIntLitBits(s string, w uint) (uint64, bool) {
	neg := false
	switch {
	case strings.HasPrefix(s, "-"):
		neg, s = true, s[1:]
	case strings.HasPrefix(s, "+"):
		s = s[1:]
	}
	n, ok := readNat(s, 64)
	if !ok {
		return 0, false
	}
	if neg {
		// Magnitude bound, written as a comparison against a shift so that w=64's bound
		// (2^63, which no positive int64 holds) needs no special case.
		if n > uint64(1)<<(w-1) {
			return 0, false
		}
		n = -n
	} else if w < 64 && n >= uint64(1)<<w {
		return 0, false
	}
	if w < 64 {
		n &= mask64(w) // the slot holds the value zero-extended, never sign-extended
	}
	return n, true
}

// mask64 is a bitmask of the low n bits, for n in {8,16,32} — this package's own lane-width
// truncation, mirroring interp/simd.go's identically-purposed `mask` at the interpreter's own
// boundary (two packages, two neutrality domains per contract §0, hence two copies of one fact
// rather than a shared import).
func mask64(n uint) uint64 {
	if n >= 64 {
		return ^uint64(0)
	}
	return uint64(1)<<n - 1
}

// sliceV128Lanes reads len(shape) lanes out of a raw (hi, lo) v128 reading, one per entry of
// shape — each entry supplies only the lane's own *wire* width (`.LaneBits`, never
// `.Kind.width()` — see LaneBits's own doc comment for why the two differ for i8x16/i16x8) and
// Kind, never its value, since shape is `want.Lanes` and this function's whole job is producing
// the `got` side to compare it against. Byte layout matches `interp`'s own
// `v128Bytes`/`lanesOf` exactly (low half first, low-numbered lane in the low bits) — the two
// packages read the identical wire convention independently, per contract §0's neutrality rule,
// rather than sharing the function across the engine/harness boundary.
func sliceV128Lanes(hi, lo uint64, shape []Val) []Val {
	out := make([]Val, len(shape))
	bitOff := uint(0)
	for i, want := range shape {
		w := want.LaneBits
		var raw uint64
		if bitOff < 64 {
			raw = lo >> bitOff
			if bitOff+w > 64 {
				// The lane straddles the hi/lo boundary — only reachable if a future shape
				// mixes lane widths that do not evenly divide 64, which none of the tracked
				// v128LaneShapes entries do (8/16/32/64 all divide 64 evenly), so this branch
				// is unreachable today and stated rather than silently mishandled if that ever
				// changes.
				raw |= hi << (64 - bitOff)
			}
		} else {
			raw = hi >> (bitOff - 64)
		}
		out[i] = Val{Kind: want.Kind, Bits: raw & mask64(w), LaneBits: w}
		bitOff += w
	}
	return out
}

// readFloatLit is `F32.of_string` / `F64.of_string` (fxx.ml:305-332) as a bit pattern, plus the
// two `assert_return`-only spellings the module grammar has no use for.
//
// **The NaN classes are read here rather than in the caller**, because they occupy the same
// grammatical slot as a literal and the reference's own wast parser treats them that way: they
// are `NAN` tokens (lexer.mll:803-804) admitted by the *script* grammar's constant production
// and rejected by the module's. A caller sniffing for the two strings before calling this would
// be a second place that knows the constant grammar.
//
// Arm order is the reference's and it is not interchangeable: `inf` and `nan` are matched
// *before* any range check, which is the only reason `f32.const inf` is well-formed.
func readFloatLit(k ValKind, s string) (Val, bool) {
	w := k.width()
	// The class spellings carry no sign in the suite and are not given one here: a signed
	// `-nan:canonical` does not lex as one token in the reference, and admitting a spelling
	// the reference cannot produce would be this reader inventing grammar.
	switch s {
	case "nan:canonical":
		return Val{Kind: k, NaN: NaNCanonical}, true
	case "nan:arithmetic":
		return Val{Kind: k, NaN: NaNArithmetic}, true
	}
	negate := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		negate = s[0] == '-'
		s = s[1:]
	}
	n, ok := readSignlessFloat(k, s)
	if !ok {
		return Val{}, false
	}
	if negate {
		// `neg x = Rep.logxor x Rep.min_int` (fxx.ml:212) — the sign *bit*, on the pattern.
		// Not arithmetic negation: a float-valued path would be exhaustively equivalent for
		// f32 (measured, 0 of 2^32 patterns differ) and this is still the reference's
		// derivation, which is what a second opinion is supposed to reproduce.
		n ^= uint64(1) << (w - 1)
	}
	return Val{Kind: k, Bits: n}, true
}

// readSignlessFloat is `of_signless_string` (fxx.ml:305-323).
func readSignlessFloat(k ValKind, s string) (uint64, bool) {
	w := k.width()
	expMask, quietBit, _ := nanFields(w)
	// bare_nan is the exponent field alone; pos_nan additionally sets the quiet bit. The two
	// are derived from nanFields rather than written as literals per width, so this reader and
	// the NaN-class predicates above cannot disagree about where the exponent is.
	bareNaN, posNaN := expMask, expMask|quietBit
	switch s {
	case "inf":
		return expMask, true
	case "nan":
		return posNaN, true
	}
	// `String.length s > 6 && String.sub s 0 6 = "nan:0x"` (fxx.ml:310). Bare `nan:0x` falls
	// through to the float path and fails there, which is the reference's behaviour and is why
	// the guard is a length test rather than a prefix test alone.
	if len(s) > 6 && s[:6] == "nan:0x" {
		p, ok := readNat(s[4:], w)
		if !ok || p == 0 {
			return 0, false // Rep overflow, or "nan payload must not be zero" (:312)
		}
		if p&bareNaN != 0 || p >= uint64(1)<<(w-1) {
			return 0, false // exponent overlap (:314), sign overlap (:316)
		}
		return p | bareNaN, true
	}
	// The finite path. Underscores are stripped wholesale, as the reference does
	// (`String.concat "" (String.split_on_char '_' s)`, :321), rather than skipped per digit.
	t := strings.ReplaceAll(s, "_", "")
	// Go's hex-float grammar requires a binary exponent where wat's does not, so `0xa0ff.f141a59a`
	// — a real spelling in this corpus — is a syntax error to ParseFloat and legal to the
	// reference. Grafting `p0` multiplies by 2^0 and cannot touch the mantissa.
	if strings.HasPrefix(t, "0x") && !strings.ContainsAny(t, "pP") {
		t += "p0"
	}
	// Parsed at the *target* width, not at 64 and narrowed: two-step conversion double-rounds,
	// and 24 of the corpus's distinct f32 spellings are sensitive to it (`const.wast:444`).
	f, err := strconv.ParseFloat(t, int(w))
	if err != nil || math.IsInf(f, 0) {
		return 0, false // `if is_inf x then failwith` (:322-323); no underflow check exists
	}
	if w == 32 {
		return uint64(math.Float32bits(float32(f))), true
	}
	return math.Float64bits(f), true
}

// readNat converts a `nat` lexeme, reporting whether it fits in `bits`.
//
// Hex if `0x`, else **decimal** — never octal, which is `strconv`'s base-0 behaviour and would
// read `010` as 8 where wat says 10. Overflow is detected before the shift rather than after,
// because after is already wrong.
func readNat(s string, bits uint) (uint64, bool) {
	limit := ^uint64(0)
	if bits < 64 {
		limit = 1<<bits - 1
	}
	if s == "" {
		return 0, false
	}
	if len(s) >= 2 && s[0] == '0' && s[1] == 'x' {
		var n uint64
		for i := 2; i < len(s); i++ {
			c := s[i]
			if c == '_' {
				continue
			}
			d, ok := hexVal(c)
			if !ok || n > limit>>4 {
				return 0, false
			}
			n = n<<4 | uint64(d)
		}
		return n, true
	}
	var n uint64
	for i := range len(s) {
		c := s[i]
		if c == '_' {
			continue
		}
		if c < '0' || c > '9' {
			return 0, false
		}
		d := uint64(c - '0')
		if n > (limit-d)/10 {
			return 0, false
		}
		n = n*10 + d
	}
	return n, true
}
