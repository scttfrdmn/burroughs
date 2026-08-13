// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package interp

import "math"

// Relaxed SIMD's three arms that are not aliases of existing ones.
//
// **Decision 0028 is the constitution for this file** and its first clause is why the file exists
// as its own unit: relaxed lowerings here are **deterministic and architecture-uniform, as a
// stated guarantee exceeding the spec**. The proposal permits a *set* of results per relaxed
// instruction; this engine picks one member and produces that member on every platform, promised
// rather than merely true today. Decision 2 then fixes *which* member opcode by opcode: the
// reference interpreter's, because a choice the suite cannot score is the purest case for
// settling accept-direction facts by reading the authority rather than the weakest.
//
// **The consequence that shapes every line below: no oracle will ever check this.** A conforming
// alternative lowering passes every vector on both CI arches, and so does a lowering that differs
// *between* arches — the `_cmp` self-consistency vectors only require determinism within a single
// run, which one binary satisfies whatever it picked. So the citations here are load-bearing in a
// way the non-relaxed SIMD arms' are not: for those, a misreading of `v128.ml` shows up as a red
// vector, and for these it shows up as nothing at all. Each function names its reference lines and
// reproduces their *shape*, not merely their intent.
//
// The other seventeen relaxed opcodes are aliases — `execFD` routes them to the arms that already
// implement the reference's choice (the non-relaxed swizzle, the `trunc_sat` family, `bitselect`,
// `floatMin`/`floatMax`, `q15mulr_sat_s`). Fourteen of the twenty mappings being aliases is what
// makes decision 1's guarantee mostly free: their uniformity is a property already established and
// already watched by the non-relaxed suite, not a new claim.

// vecRelaxedFma implements `f32x4`/`f64x2`.`relaxed_madd` and `relaxed_nmadd` — `eval_vec.ml:130-133`
// mapping them to `V128.F32x4`/`F64x2`.`fma`/`fnma`, whose bodies are `v128.ml:236-238` over
// `fxx.ml:152-157`.
//
// The reference's `fma`, verbatim in shape:
//
//	let fma x y z =
//	  let t = Float.fma (to_float x) (to_float y) (to_float z) in
//	  if t = t then of_float t else determine_binary_nan x y
//
// Three properties of that, each of which a plausible re-derivation gets wrong:
//
//  1. **Fused — one rounding, and the fusion is the decided answer, not an accident.** Decision 3
//     forbids the bare expression `a*b + c` anywhere in this engine's floating-point paths, because
//     Go leaves fusion to the compiler: the same source compiles to `FMADD` on arm64 and to
//     separate `MULSS`/`ADDSS` on amd64. That is the #223 shape *by construction* rather than by
//     omission — and it is worse than #223, because the proposal's permitted set for madd has two
//     members (two roundings or one) and each arch would pick a different legal member. It is not
//     a conformance defect at all; it is a uniformity defect, invisible to every vector on both
//     runners, and decision 1 is the only thing in this project that forbids it. Hence `math.FMA`,
//     which IEEE 754 pins to a single correctly-rounded result independent of hardware.
//
//  2. **`nmadd` negates the *first multiplicand*, not the product and not the addend.**
//     `v128.ml:238` is `fnma x y z = fma (unop FXX.neg x) y z`. The negation happens *before* the
//     call, so it also feeds the NaN selection in property 3 — `determine_binary_nan` sees the
//     negated x, whose sign bit differs from the original's. Reading `fnma` as `-(x*y) + z` gives
//     the same number for finite inputs and a differently-signed NaN payload for others, which is
//     exactly the class of difference no vector here can see.
//
//  3. **The NaN result is selected from the two multiplicands, never from the addend.** When the
//     fused result is NaN, the reference discards it and returns `determine_binary_nan x y` — so
//     `fma 1.0 1.0 nan:0x200000` does *not* propagate the addend's payload. `quietNaNOf` is this
//     engine's `determine_binary_nan` (`simd.go`), and passing it the multiplicands in x, y order
//     reproduces the reference's own preference of x's payload over y's.
//
// The f32 case is a **widen-fuse-narrow composite** and is written that way deliberately: `fxx.ml`
// is a functor over `to_float`/`of_float`, so for F32 the operands widen to float64, `Float.fma`
// fuses in float64, and the result narrows back to f32. That is two roundings at f32 — one to
// float64 by the fma, one to f32 by the narrowing — where a direct f32 fma would round once, and
// decision 2 adopts it because it is what the only executable authority does.
//
// **The double rounding is observable, and that is now measured rather than open.** 0028 left it
// open on purpose: it observed that the classical `2p+2` innocuousness bound is stated for the
// basic operations while an fma is ternary, wrote *"I have not verified it and do not assert it
// either way"*, and filed the question with a tripwire instead of a paragraph. The tripwire found
// **four** differences in a thousand triples — `fma(3, 34.275555, 0x1p-149)` gives `0x42cda740`
// here against a correctly-rounded `0x42cda741` — identically on arm64 and amd64. The bound's
// hypothesis is that the wide format holds the **exact** result before the second rounding, which
// holds for `x*y` (48 bits into 53) and fails for `x*y + z`, whose exact sum spans most of f32's
// exponent range when the addend is subnormal: the float64 rounding discards what the narrowing
// needed. So the answer is *no*, and the engine is unaffected either way — the reference *is* this
// composite, double rounding included, and 0028 d2 binds this arm to the reference.
//
// **What was wrong was this comment's first draft, which asserted the innocuousness the record had
// declined to assert.** It named the bound, quoted `53 >= 2*24+2`, and cited 0028 as the source of
// the reasoning; 0028 says the opposite of that. A hedge is part of a record's content, so prose
// that resolves the record's open question in passing is drift in the direction that matters most —
// toward confidence. #280 carries the measurement and that finding; the tripwire survives with its
// subject inverted, as `TestF32MaddIsTheReferenceCompositeNotASingleRoundedFma`, pinning the four.
func (in *Instance) vecRelaxedFma(st *stack, width uint64, negateFirst bool) error {
	if err := st.needNum(6); err != nil {
		return err
	}
	// `Vec v3 :: Vec v2 :: Vec v1 :: vs'` (`eval.ml:979`) — v3 is on top, and the operator is
	// applied as `f v1 v2 v3`, so x=v1 is the *deepest* operand. Popping in the other order
	// computes y*x+z, which is the same product and a different NaN selection.
	hi3, lo3 := st.popV128()
	hi2, lo2 := st.popV128()
	hi1, lo1 := st.popV128()
	xs := lanesOf(hi1, lo1, width)
	ys := lanesOf(hi2, lo2, width)
	zs := lanesOf(hi3, lo3, width)
	result := make([]uint64, len(xs))
	for i := range xs {
		if width == 4 {
			x := float64(math.Float32frombits(uint32(xs[i])))
			y := float64(math.Float32frombits(uint32(ys[i])))
			z := float64(math.Float32frombits(uint32(zs[i])))
			if negateFirst {
				x = -x
			}
			t := math.FMA(x, y, z)
			if math.IsNaN(t) {
				t = quietNaNOf(x, y)
			}
			result[i] = uint64(math.Float32bits(float32(t)))
		} else {
			x := math.Float64frombits(xs[i])
			y := math.Float64frombits(ys[i])
			z := math.Float64frombits(zs[i])
			if negateFirst {
				x = -x
			}
			t := math.FMA(x, y, z)
			if math.IsNaN(t) {
				t = quietNaNOf(x, y)
			}
			result[i] = math.Float64bits(t)
		}
	}
	hi, lo := lanesToV128(result, width)
	st.pushV128(hi, lo)
	return nil
}

// vecRelaxedDotI8x16S implements `i16x8.relaxed_dot_i8x16_i7x16_s` — `eval_vec.ml:85` mapping it to
// `V128.I16x8_convert.dot_s`, whose body is `v128.ml:387-393`.
//
// **Both operands are sign-extended as i8, despite the mnemonic's `i7x16`.** The reference maps
// `Convert.I16_.extend_i8_s` over *both* lane lists; nothing anywhere treats the second operand as
// 7-bit. The mnemonic names the range over which the proposal *guarantees* a unique answer (i7 lanes
// cannot overflow the i16 sum), not a masking step — so an implementation that masked the second
// operand to 7 bits would agree with the reference on every input the mnemonic describes and differ
// outside it, which is precisely where the relaxed freedom lives and precisely what no vector here
// samples.
//
// Sums wrap at i16 (`I16.(add (mul x1 y1) (mul x2 y2))`), not saturate, and not widen to i32.
func (in *Instance) vecRelaxedDotI8x16S(st *stack) error {
	if err := st.needNum(4); err != nil {
		return err
	}
	hi2, lo2 := st.popV128()
	hi1, lo1 := st.popV128()
	xs := lanesOf(hi1, lo1, 1)
	ys := lanesOf(hi2, lo2, 1)
	result := make([]uint64, len(xs)/2)
	for i := range result {
		x1, x2 := signExtendLane(xs[2*i], 1), signExtendLane(xs[2*i+1], 1)
		y1, y2 := signExtendLane(ys[2*i], 1), signExtendLane(ys[2*i+1], 1)
		result[i] = maskLane(uint64(x1*y1+x2*y2), 2)
	}
	hi, lo := lanesToV128(result, 2)
	st.pushV128(hi, lo)
	return nil
}

// vecRelaxedDotAddS implements `i32x4.relaxed_dot_i8x16_i7x16_add_s` — `eval_vec.ml:138` mapping it
// to `V128.I32x4_convert.dot_add_s`, whose body is `v128.ml:428-442`.
//
// A **ternary** op: four i8 products per output lane, sign-extended straight to i32 rather than
// summed at i16 and then widened, then the third operand added lane-wise. Both the extension width
// and the add's width are i32 throughout and wrapping — `Int32.(add (add (mul x1 y1) (mul x2 y2))
// (add (mul x3 y3) (mul x4 y4)))` followed by `I32x4.add … z`. Summing the four products at i16
// first would agree with the reference whenever no intermediate overflows i16 and differ when one
// does, which is the same unwatched-difference shape as the i8-versus-i7 reading above.
//
// The reference's grouping `(x1y1 + x2y2) + (x3y3 + x4y4)` is not reproduced as written because
// wrapping two's-complement addition is associative, so every grouping gives the same bits — the
// one place in this file where the shape is *not* copied, said out loud because "reproduce the
// reference's shape" is otherwise the rule here and a silent departure from it would read as an
// oversight.
func (in *Instance) vecRelaxedDotAddS(st *stack) error {
	if err := st.needNum(6); err != nil {
		return err
	}
	hi3, lo3 := st.popV128()
	hi2, lo2 := st.popV128()
	hi1, lo1 := st.popV128()
	xs := lanesOf(hi1, lo1, 1)
	ys := lanesOf(hi2, lo2, 1)
	zs := lanesOf(hi3, lo3, 4)
	result := make([]uint64, len(zs))
	for i := range result {
		var sum int64
		for j := range 4 {
			sum += signExtendLane(xs[4*i+j], 1) * signExtendLane(ys[4*i+j], 1)
		}
		sum += signExtendLane(zs[i], 4)
		result[i] = maskLane(uint64(sum), 4)
	}
	hi, lo := lanesToV128(result, 4)
	st.pushV128(hi, lo)
	return nil
}
