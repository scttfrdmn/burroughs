package interp

import (
	"fmt"
	"math"
	"math/big"
	"runtime"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// runRelaxed1 is runSIMD1 with the relaxed-SIMD gate on as well.
//
// A separate helper rather than a `runSIMDFeatures1` call at each site, because every test in this
// file needs the same pair and a per-site feature literal is a place for one of them to be written
// with `RelaxedSIMD` alone — which would decode nothing, relaxed SIMD's opcodes living inside the
// `0xfd` prefix the SIMD gate admits (`gatemap.go`'s narrowest-match-wins over a region and its
// sub-range).
func runRelaxed1(t *testing.T, src string, args ...Value) []Value {
	t.Helper()
	return runSIMDFeatures1(t, binary.Features{SIMD: true, RelaxedSIMD: true}, src, args...)
}

// TestRelaxedTruncWitnesses is decision 0028 d5's obligation discharged: the four
// `i32x4.relaxed_trunc_*` opcodes (0x101-0x104) land with **author-supplied witnesses, because the
// suite supplies none**.
//
// `i32x4_relaxed_trunc.wast` is eight lines long and contains **no assertions at all** — a module
// definition and nothing else, which the board reports as `1/1 pass` in both lanes. So these four
// arms are the one place in this engine where "the suite is the oracle" has no oracle to be, and a
// wrong lowering would be green everywhere: default lane (gated), all-on lane (the file asks
// nothing), and CI on both architectures.
//
// **Two halves, and the second exists because the first is a delta.**
//
//  1. **Differential against the non-relaxed twin.** 0028 d2 makes each relaxed trunc an alias of
//     its `trunc_sat_*` sibling (`eval_vec.ml:211-214`), and those siblings carry
//     `simd_conversions.wast`'s full vector set behind them. Asserting `relaxed_trunc(v) ==
//     trunc_sat(v)` therefore borrows an oracle rather than inventing one, and it is the check
//     that actually fails if the dispatch is rewired to the wrong helper.
//  2. **Absolute bit patterns beside it.** A differential is a *delta between two instruments*,
//     and a delta can be sound while both levels are wrong — the two arms share `vecTruncSatF32x4`
//     outright, so a defect *inside* that helper moves both sides together and the differential
//     agrees perfectly. The absolute column is what has an independent subject: each value below
//     is derived from `Convert.I32_.trunc_sat_*` (`interpreter/exec/i32_convert.ml`) and not read
//     back off this engine.
//
// **The inputs are chosen where the relaxed freedom actually lives.** The proposal permits any
// result for an out-of-range or NaN operand, so an in-range row proves nothing about which member
// of the permitted set this engine picked — it is exactly the rows a hardware lowering would get
// "wrong" (x86's `CVTTPS2DQ` yields `0x80000000` for out-of-range *and* for NaN, where the
// reference saturates and zeroes) that carry the whole claim. One in-range lane stays in each row
// as the negative side of the partition: a lowering that saturated everything, including values
// needing no saturation, would satisfy every other lane here.
func TestRelaxedTruncWitnesses(t *testing.T) {
	const (
		i32Max = 0x7fffffff
		i32Min = 0x80000000
		u32Max = 0xffffffff
	)
	f32 := func(v float32) uint32 { return math.Float32bits(v) }
	pack32 := func(lo, hi uint32) uint64 { return uint64(lo) | uint64(hi)<<32 }

	for _, tc := range []struct {
		name           string
		relaxed, plain string // the twin mnemonics; the differential compares them
		in             Value
		wantHi, wantLo uint64
		why            string
	}{
		{
			name:    "f32x4_s saturates at the signed bounds and zeroes NaN",
			relaxed: "i32x4.relaxed_trunc_f32x4_s", plain: "i32x4.trunc_sat_f32x4_s",
			// lanes 0-3: 1.9 (in range, truncates toward zero), 2^31 (one past i32 max),
			// -2^31 - 256 (past i32 min), NaN.
			in:     v128(pack32(f32(-2147484160), f32(float32(math.NaN()))), pack32(f32(1.9), f32(2147483648))),
			wantLo: pack32(1, i32Max),
			wantHi: pack32(i32Min, 0),
			why: "the reference's `trunc_sat_f32_s` clamps to the bounds and maps NaN to 0; x86's " +
				"CVTTPS2DQ would give 0x80000000 for all three of the last lanes, which is a " +
				"permitted relaxed result and not this engine's",
		},
		{
			name:    "f32x4_u zeroes negatives rather than wrapping them",
			relaxed: "i32x4.relaxed_trunc_f32x4_u", plain: "i32x4.trunc_sat_f32x4_u",
			// lanes 0-3: 1.9, 2^32 (one past u32 max), -1.0 (any negative), NaN.
			in:     v128(pack32(f32(-1.0), f32(float32(math.NaN()))), pack32(f32(1.9), f32(4294967296))),
			wantLo: pack32(1, u32Max),
			wantHi: pack32(0, 0),
			why: "-1.0 becomes 0, not 0xffffffff: the unsigned saturation floor is zero, and a " +
				"lowering that reused the signed conversion would produce the two's-complement " +
				"pattern here and agree with this engine on every other lane",
		},
		{
			name:    "f64x2_s_zero converts two lanes and zeroes the upper half",
			relaxed: "i32x4.relaxed_trunc_f64x2_s_zero", plain: "i32x4.trunc_sat_f64x2_s_zero",
			// lanes 0-1: -1.9 (truncates toward zero, so -1), 2^31 (past i32 max).
			in:     v128(math.Float64bits(2147483648), math.Float64bits(-1.9)),
			wantLo: pack32(0xffffffff, i32Max),
			wantHi: 0,
			why: "the `_zero` suffix is a claim about the upper 64 bits and it is asserted here: a " +
				"lowering that left them undefined — which the relaxed form permits — passes every " +
				"lane check and fails this one",
		},
		{
			name:    "f64x2_u_zero zeroes both a negative operand and the upper half",
			relaxed: "i32x4.relaxed_trunc_f64x2_u_zero", plain: "i32x4.trunc_sat_f64x2_u_zero",
			// lanes 0-1: -1.9 (negative, floors at 0), 2^32 (past u32 max).
			in:     v128(math.Float64bits(4294967296), math.Float64bits(-1.9)),
			wantLo: pack32(0, u32Max),
			wantHi: 0,
			why:    "the unsigned floor and the zeroed half in one row",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			one := func(mnemonic string) Value {
				out := runRelaxed1(t, `(module (func (export "c") (param $0 v128) (result v128) (`+
					mnemonic+` (local.get $0))))`, tc.in)
				if len(out) != 1 {
					t.Fatalf("%s: got %d results, want 1", mnemonic, len(out))
				}
				return out[0]
			}

			// The absolute half first, because it is the half with a subject: if the level is
			// wrong the differential's verdict is uninformative either way, and reporting them in
			// this order stops a reader from reading "they agree" as "they are right".
			got := one(tc.relaxed)
			if got.Hi != tc.wantHi || got.Bits != tc.wantLo {
				t.Errorf("%s = hi=%#016x lo=%#016x, want hi=%#016x lo=%#016x\n\t%s",
					tc.relaxed, got.Hi, got.Bits, tc.wantHi, tc.wantLo, tc.why)
			}
			if twin := one(tc.plain); twin.Hi != got.Hi || twin.Bits != got.Bits {
				t.Errorf(`%s and %s disagree on the same operand.

    %-34s hi=%#016x lo=%#016x
    %-34s hi=%#016x lo=%#016x

Decision 0028 d2 makes these one arm: eval_vec.ml:211-214 maps every relaxed trunc to the
`+"`trunc_sat_*`"+` family, so the dispatch in execFD joins their cases. A disagreement here is a
rewiring to the wrong helper, and it is the only check in this file with an oracle behind it — the
plain form carries simd_conversions.wast's vectors, the relaxed one carries nothing.`,
					tc.relaxed, tc.plain, tc.relaxed, got.Hi, got.Bits, tc.plain, twin.Hi, twin.Bits)
			}
		})
	}
}

// fmaExactThenRoundOnce is a **single-rounding** f32 fused multiply-add, computed independently of
// anything in this engine: the product and the sum are evaluated exactly in arbitrary precision and
// the result is rounded to f32 exactly once.
//
// It exists because the obvious spelling of the oracle is not one. Writing
// `float32(math.FMA(float64(x), float64(y), float64(z)))` for the "single-rounded" side gives an
// expression *identical* to the composite it is meant to check, so the comparison holds by
// construction and the test reports clean against a search it never ran. That spelling was in the
// first draft of this file, and what caught it was not review — it was noticing that two named
// functions had the same body.
//
// `big.Float` at 200 bits, and 200 is not decoration: the exact value of `x*y + z` is *not* bounded
// by 53 significand bits when the product is large and the addend subnormal (f32's exponent range
// spans ~277 bits between `0x1.fffffep+127` and `0x1p-149`), which is the very fact the test below
// exists to record. `Float32` performs the only rounding in the whole computation.
//
// Inputs are finite by contract — `big.Float.SetFloat64` panics on NaN and Inf — which is why the
// operand sets below carry f32's largest *finite* value rather than an infinity. NaN behaviour is
// not this function's subject: it belongs to `determine_binary_nan`, checked by the suite's own
// `relaxed_madd_nmadd.wast` vectors, which pass.
func fmaExactThenRoundOnce(x, y, z float32) float32 {
	const prec = 200
	r := new(big.Float).SetPrec(prec).Mul(
		new(big.Float).SetPrec(prec).SetFloat64(float64(x)),
		new(big.Float).SetPrec(prec).SetFloat64(float64(y)),
	)
	r.Add(r, new(big.Float).SetPrec(prec).SetFloat64(float64(z)))
	f, _ := r.Float32()
	return f
}

// f32MaddOperands are the triples both tests below sweep — shared so that the pinned counts and the
// positive control are measured over *one* operand set, which is what makes them comparable.
//
// Chosen where a rounding difference would live: values whose exact product needs more than 24
// significand bits, so the round bit and the sticky bits below it are both nonzero. Includes the
// reference vector's own operands (`relaxed_madd_nmadd.wast:43-46`'s 0x1.000004p+0 / 0x1.0002p+0
// pair, whose comment names the round bit explicitly) and both ends of f32's exponent range, since
// a large product against a subnormal addend is the configuration that carries the finding.
// Finite by contract: see fmaExactThenRoundOnce.
var (
	f32MaddXs = []float32{
		0x1.000004p+0, 0x1.0002p+0, 0x1.fffffep+127, 0x1p-126, 0x1.fffffep-1,
		1, 3, 0x1.123456p+5, -0x1.000002p+0, 0x1p-149,
	}
	f32MaddZs = []float32{
		-0x1.000204p+0, 0x1p-37, 0, 0x1.fffffep+127, -0x1.fffffep+127,
		1, -1, 0x1p-149, 0x1.7ffffep+3, -0x1p-126,
	}
)

// f32Composite is `vecRelaxedFma`'s width==4 arm, lifted rather than re-derived: what is under test
// is that path's rounding behaviour, so a second spelling of it would test the spelling.
func f32Composite(x, y, z float32) float32 {
	return float32(math.FMA(float64(x), float64(y), float64(z)))
}

// f32Unfused is the *other* member of the proposal's permitted set for madd, and decision 0028 d3's
// other allowed spelling: `a*b` rounded to f32, then the add rounded to f32. The explicit conversion
// is the rounding, and Go's specification lets an implementation fuse across operations *except*
// where an explicit floating-point conversion pins one — so this form is arch-uniform by the same
// idiom `float64(a*b) + c` uses at f64, and the conversion is load-bearing rather than decorative.
func f32Unfused(x, y, z float32) float32 {
	return float32(x*y) + z
}

// countF32Diffs runs two f32 multiply-add spellings over every triple in the shared operand set and
// returns how many disagree in bits, with the first disagreement formatted for a message.
//
// Every comparison in this file is "where do these two lowerings differ?", the oracle being just
// another spelling, so they share one loop. Sharing it is deliberate: a per-comparison loop is
// three places for the operand set to be silently truncated, and the vacuity floor each caller
// asserts is only worth having if `checked` comes from the same code path in all of them.
func countF32Diffs(got, want func(x, y, z float32) float32, wantName string) (checked, diff int, first string) {
	for _, x := range f32MaddXs {
		for _, y := range f32MaddXs {
			for _, z := range f32MaddZs {
				checked++
				g, w := got(x, y, z), want(x, y, z)
				if math.Float32bits(g) != math.Float32bits(w) {
					diff++
					if first == "" {
						first = fmt.Sprintf("fma(%v, %v, %v) = %#08x, %s %#08x",
							x, y, z, math.Float32bits(g), wantName, math.Float32bits(w))
					}
				}
			}
		}
	}
	return checked, diff, first
}

// sweepF32Madd runs one lowering over every triple and returns how many differ from the
// single-rounded oracle, plus the first difference for a message.
func sweepF32Madd(lowering func(x, y, z float32) float32) (checked, diff int, first string) {
	return countF32Diffs(lowering, fmaExactThenRoundOnce, "single-rounded")
}

// f32CompositeDoubleRoundings is the number of triples in the shared operand set on which the
// reference's widen-fuse-narrow composite differs from a correctly-rounded f32 fused multiply-add.
//
// **Pinned exactly, and identical on both architectures** — measured 4 on arm64 and 4 on amd64
// (`--platform linux/amd64` under Docker, the standing pre-push cross-arch check). Those two facts
// are doing different jobs: the *exactness* makes a change of lowering visible, and the *equality
// across arches* is decision 0028 d1's guarantee holding where it is hardest to see.
//
// A floor would not do here. Four differences out of a thousand is precisely the "small silent
// loss" a floor cannot see, and the number is the record of a falsified premise (see the test), so
// it is the number and not a bound.
const f32CompositeDoubleRoundings = 4

// TestF32MaddIsTheReferenceCompositeNotASingleRoundedFma pins what `vecRelaxedFma`'s f32 path
// actually is. **It is decision 0028's own filed question, answered, and the answer is the opposite
// of what this test's first draft was written to confirm.** The history is kept because the test's
// whole value is that it caught it.
//
// **What 0028 filed.** The record did *not* assert the innocuousness — it observed that the
// classical `2p+2` double-rounding bound is stated for the basic operations while an fma is ternary,
// wrote *"I have not verified it and do not assert it either way"*, and filed the question with this
// tripwire rather than resolving it in prose. Filing it was the right call and this is it paying off.
//
// **What the draft got wrong.** This test began as
// `TestF32FmaWidenNarrowIsDoubleRoundingInnocuous`, on reasoning it attributed to 0028 and that
// 0028 does not contain: quote the bound, note `53 >= 2*24+2 = 50`, conclude the composite must
// equal a single-rounded f32 fma, expect to find nothing. The record had already named the exact
// gap in that argument. A hedge is part of a record's content, and prose that quietly resolves an
// open question in the confident direction is the drift worth catching.
//
// **It found four.** `fma(3, 34.275555, 0x1p-149)` is the first: composite `0x42cda740`,
// correctly-rounded `0x42cda741`. The bound's hypothesis is that the wide format holds the **exact**
// result before the second rounding. For `x*y` alone that hypothesis is satisfied — a 24x24 product
// needs 48 bits and 53 are available, which is the version of the bound that is true. For `x*y + z`
// it is not: with a large product and a subnormal addend the exact sum spans most of f32's ~277-bit
// exponent range, so the float64 rounding *discards* the addend's contribution and the narrowing
// then rounds a value that has already lost the information deciding which way it should go.
// `0x1p-149` against a product near 2^7 is exactly that configuration, and it is why every one of
// the four involves the subnormal addend.
//
// **Nothing in the engine is wrong.** What this arm owes is the reference's answer (0028 d2), and
// the reference *is* this composite — `fxx.ml` is a functor over `to_float`/`of_float`, so F32's
// `fma` widens, calls `Float.fma`, and narrows, double rounding included. The relaxed proposal
// permits a whole set here anyway, so a correctly-rounded f32 fma would also conform; it simply is
// not the member this engine chose. 0028 carries this measurement as an appended resolution of its
// own open question, per that record's append rule. #280.
//
// **So the subject inverted, and the test kept its operands.** It no longer asserts equality — it
// pins the *count* of the difference, which is the honest form of a property that is deliberate,
// arch-uniform, and invisible to every vector in the suite.
func TestF32MaddIsTheReferenceCompositeNotASingleRoundedFma(t *testing.T) {
	checked, diff, first := sweepF32Madd(f32Composite)

	// The vacuity floor. A sweep that silently stopped iterating would satisfy a count assertion
	// by asking nothing, and this file's own subject is that shape.
	if want := len(f32MaddXs) * len(f32MaddXs) * len(f32MaddZs); checked != want {
		t.Fatalf("checked %d triples, want %d — the sweep did not cover its own operand set",
			checked, want)
	}
	if diff != f32CompositeDoubleRoundings {
		t.Errorf(`the composite differs from a single-rounded f32 fma on %d of %d triples, want exactly %d.

    first difference: %s

This number is pinned, not bounded, and it moves for two very different reasons:

  - **It went to 0.** Someone "fixed" the double rounding — narrowed differently, or called a
    genuinely single-rounding f32 fma. That is a *conformance-permitted* answer and still the wrong
    one: decision 0028 d2 binds this arm to the reference's own widen-fuse-narrow composite, and
    nothing in the suite would have caught the change (i32x4_relaxed_trunc.wast's sibling problem —
    relaxed_madd_nmadd.wast's vectors use `+"`(either …)`"+` and admit both).
  - **It moved to some other nonzero value.** The lowering changed shape. Read the first difference
    above and compare against fxx.ml's functor.

If it differs *between architectures* — this test running green on one runner and red on the other
with no source change — that is decision 0028 d1 broken, and it is the more serious finding of the
two: the guarantee this engine states beyond the spec is that a relaxed result is the same
everywhere.`, diff, checked, f32CompositeDoubleRoundings, first)
	}
	t.Logf("%d triples, %d double-rounding differences from a correctly-rounded f32 fma "+
		"(pinned; measured identically on arm64 and amd64)", checked, diff)
}

// TestBareMultiplyAddIsArchitectureDependent is decision 0028 d3's ground, measured rather than
// argued — and it doubles as the positive control for the test above.
//
// **As a control, and it asserts the comparison 0028 specified rather than a substitute.** The sweep
// above expects a specific small count, and a sweep that cannot detect a wrong lowering at all would
// report whatever count it liked, so a known-different lowering must be caught on the same operands.
// d3 named which one: *"a triple where the composite differs from the unfused f32 answer, which must
// be found."* That comparison — `f32Unfused` against `f32Composite` — is nonzero for the **same
// reason on every architecture**, both spellings being arch-uniform by construction.
//
// The draft of this test substituted a different pair, bare-versus-oracle, and that substitution is
// exactly why it was stillborn on arm64: bare is whatever the compiler chose, and on arm64 it chose
// the oracle's answer. Both pairings are asserted here now — the specified one because it is the
// control, and bare-versus-composite because it is d3's evidence below — but only the first is a
// control whose *reason* for firing is architecture-independent.
//
// **As d3's evidence.** 0028 d3 forbids the bare expression `a*b + c` in this engine's
// floating-point paths on the ground that Go leaves fusion to the compiler, so the same source
// yields a fused result on arm64 and an unfused one on amd64. That was an argument when it was
// written. Measured over these 1000 triples it is a number, and the number is worse than the
// argument suggested:
//
//	                                    arm64    amd64
//	bare `x*y + z` vs single-rounded        0       59
//	bare `x*y + z` vs the composite         4       55
//
// arm64 fuses into a genuine f32 `FMADD` and therefore lands on the *correctly-rounded* answer on
// every triple; amd64 emits a multiply and an add and misses it on 59. So a bare expression here
// would not merely be non-uniform, it would make the engine's answer depend on which runner
// produced it for **55 of 1000 triples** — while passing every vector in the suite on both, since
// `relaxed_madd_nmadd.wast`'s expectations are `(either …)` and admit fused and unfused alike. That
// is the uniformity-versus-conformance distinction with a magnitude attached.
//
// The assertion is *nonzero*, not a count, and the asymmetry is deliberate: the count is
// architecture-dependent by construction — that is the finding — so pinning it would pin the
// property the test is about. What must hold on every architecture is that the forbidden lowering
// is *detectable* here.
func TestBareMultiplyAddIsArchitectureDependent(t *testing.T) {
	bare := func(x, y, z float32) float32 {
		// The one place in this module where 0028 d3's forbidden expression is written on purpose.
		// It is not on an engine path: nothing in internal/interp calls this, and its only reader
		// is the comparison below.
		return x*y + z
	}

	triples := len(f32MaddXs) * len(f32MaddXs) * len(f32MaddZs)

	// The control 0028 d3 specified: the two *permitted* members disagree, arch-uniformly.
	checked, unfusedVsComposite, firstUnfused := countF32Diffs(f32Unfused, f32Composite, "composite")
	if checked != triples {
		t.Fatalf("checked %d triples, want %d — the sweep did not cover its own operand set",
			checked, triples)
	}
	if unfusedVsComposite == 0 {
		t.Errorf(`the unfused lowering agreed with the composite on all %d triples.

That is the positive control failing, not good news, and it is the comparison decision 0028 d3
named — two spellings that are both permitted, both arch-uniform, and must be distinguishable here
or the pinned count in the test above is unfalsifiable. Two causes with opposite remedies:

  - **The operand set stopped discriminating.** Fix the operands, never the assertion.
  - **`+"`float32(x*y) + z`"+` stopped being unfused** — a compiler that fuses across the explicit
    conversion, which would be a Go specification violation and a much larger finding than this
    test.`, checked)
	}

	// And against the bare form, which is d3's evidence rather than the control: the count here is
	// architecture-dependent *by construction*, that being the finding. Asserted nonzero and never
	// pinned, because pinning it would pin the property the test exists to report.
	_, bareVsComposite, firstBare := countF32Diffs(bare, f32Composite, "composite")
	_, bareVsOracle, _ := countF32Diffs(bare, fmaExactThenRoundOnce, "single-rounded")
	if bareVsComposite == 0 {
		t.Errorf(`the bare lowering agreed with the composite on all %d triples, so on this
architecture a forbidden madd arm would be indistinguishable from the decided one here.

Measured when written: 4 differences on arm64 (which fuses) and 55 on amd64 (which does not). A zero
means this architecture's compiler now emits exactly the reference composite for `+"`x*y + z`"+` —
which does not make the bare form safe, it makes *this* comparison blind to it. The specified control
above is unaffected and still guards the pinned count.`, triples)
	}

	t.Logf("%d triples on %s: unfused vs composite %d (the specified control, arch-uniform); "+
		"bare vs composite %d, bare vs single-rounded %d — the arm64/amd64 measurement for the bare "+
		"pair is 4/55 and 0/59, and that pair is d3's finding. first unfused diff: %s. first bare "+
		"diff: %s", triples, runtime.GOARCH, unfusedVsComposite, bareVsComposite, bareVsOracle,
		firstUnfused, firstBare)
}
