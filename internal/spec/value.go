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
	}
	return "unknown"
}

// isRef reports whether this kind lives in the reference half of a Val rather than the
// numeric half — the harness's own mirror of `binary.ValType.IsRef`, restated rather than
// imported for ValKind's own neutrality reason (see the type's doc comment).
func (k ValKind) isRef() bool { return k == KindFuncRef || k == KindExternRef }

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

	// Extern is the opaque identity for RefExternIdentity — `ref.extern N`'s N, read as a plain
	// uint32 per Extern's own comment. Unread for every other Class, and 0 is a legitimate
	// identity (`ref.extern 0` appears in the corpus), so it must never be read as "unset".
	Extern uint32

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
			return fmt.Sprintf("ref.extern %d", v.Extern)
		case RefTypePattern:
			return "(ref." + v.Kind.String() + ")"
		case RefConcrete:
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
			return want.Kind == KindExternRef
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
			if !want.Lanes[i].Matches(gotLanes[i]) {
				return false
			}
		}
		return true
	}
	if want.Kind.isRef() {
		switch want.Class {
		case RefTypePattern:
			// `assert_ref_pat`'s two RefTypePat arms differ by heaptype: ExternHT admits any
			// value including null (`RefTypePat ExternHT, _ -> true`), FuncHT admits only a
			// non-null funcref (`RefTypePat FuncHT, Instance.FuncRef _ -> true`, with no arm at
			// all for `NullRef`, which falls through to the catch-all `false`). Kind already
			// matched above, and this harness's two Kind.isRef() members map exactly to those
			// two heaptypes, so the dispatch is on Kind rather than on a heaptype this Val does
			// not carry.
			//
			// **The null half of that distinction now lives above**, in the null-`got` dispatch
			// (grave #266) — so `got` here is non-null and both heaptypes admit it. Stated as a
			// plain `true` with the reason, rather than kept as `got.Class != RefLiteralNull`:
			// that comparison is now *always* true, and a condition that cannot be false is a
			// missing check wearing a disguise (0003) even when it was a real check yesterday.
			return true
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
// Two families are expectation-only, for the identical reason: a NaN class (NaNCanonical/
// NaNArithmetic) and RefTypePattern/AnyNull are all *predicates* over a result, not concrete
// values a caller could construct and pass — `nan:canonical` names a set of bit patterns, and
// `(ref.func)`/`(ref.null)` name "any value of this shape" rather than one. Both families were
// checked the same way before this helper existed (`v.NaN != NaNNone` at invokeAction's and
// namedInvokeAction's argument loops); this names the check once so a third expectation-only
// shape arriving later has one site to extend rather than two to keep in sync — the same
// one-authority reasoning invokeAction's own doc comment gives for sharing readConst itself.
func (v Val) isPassable() bool {
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
		switch len(n.list) {
		case 1:
			return Val{Kind: KindExternRef, Class: RefTypePattern}, true
		case 2:
			if n.list[1].isList() || n.list[1].isS {
				return Val{}, false
			}
			id, ok := readNat(n.list[1].atom, 32)
			if !ok {
				return Val{}, false
			}
			return Val{Kind: KindExternRef, Class: RefExternIdentity, Extern: uint32(id)}, true
		}
		return Val{}, false
	case "ref.func":
		// Bare only — see the function comment for why a literal `ref.func N`/`ref.func $x`
		// is out of this reader's scope rather than merely unhandled.
		if len(n.list) != 1 {
			return Val{}, false
		}
		return Val{Kind: KindFuncRef, Class: RefTypePattern}, true
	}
	return Val{}, false
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
