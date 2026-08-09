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
	// result has *some* Class other than RefLiteralNull, which is what RefTypePattern's own
	// predicate (`got.Class != RefLiteralNull`) actually tests.
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
	// keyworded arm two lines below it). Matches bypasses the Kind comparison for this one
	// shape, because a Val naming no heap type has no Kind to compare — Kind still holds
	// whatever readRefConst assigned it (KindFuncRef, arbitrarily, since some Kind value is
	// needed to keep this Val constructible without a third "no kind" sentinel), and reading
	// it as a real constraint would wrongly refuse an externref result the way a stray Kind
	// tag refused it before this field existed.
	AnyNull bool
}

func (v Val) String() string {
	if v.Kind.isRef() {
		switch v.Class {
		case RefLiteralNull:
			if v.AnyNull {
				return "ref.null"
			}
			return "ref.null " + v.Kind.String()
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
	if want.AnyNull {
		// The bare `(ref.null)` expectation: null of any heap type, checked before the Kind
		// comparison because this Val's own Kind names no real constraint — see AnyNull's
		// doc comment.
		return got.Class == RefLiteralNull
	}
	if want.Kind != got.Kind {
		return false
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
			if want.Kind == KindExternRef {
				return true
			}
			return got.Class != RefLiteralNull
		case RefLiteralNull:
			// Matches without regard to heaptype, per this type's own doc comment — the
			// reference's `NullPat _, NullRef -> true` arm.
			return got.Class == RefLiteralNull
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
	neg := false
	switch {
	case strings.HasPrefix(s, "-"):
		neg, s = true, s[1:]
	case strings.HasPrefix(s, "+"):
		s = s[1:]
	}
	n, ok := readNat(s, 64)
	if !ok {
		return Val{}, false
	}
	if neg {
		// Magnitude bound, written as a comparison against a shift so that w=64's bound
		// (2^63, which no positive int64 holds) needs no special case.
		if n > uint64(1)<<(w-1) {
			return Val{}, false
		}
		n = -n
	} else if w < 64 && n >= uint64(1)<<w {
		return Val{}, false
	}
	if w == 32 {
		n = uint64(uint32(n)) // the slot holds an i32 zero-extended, never sign-extended
	}
	return Val{Kind: k, Bits: n}, true
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
