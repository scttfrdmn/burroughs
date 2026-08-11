package interp

import (
	"math"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// execFD dispatches the 0xfd region: SIMD's 256 opcodes, per #212's own family partition
// (mnemonics.ml's 20 AST constructors, collapsed to five ladder-sized groups). This function
// grows one family at a time; unhandled sub-opcodes fall through to `unsupported`, rendering as
// `fd NN` — the board's existing bucket key, so the arms this function does not yet have stay
// visible as the work list they are, exactly as `execFC`'s own header states for its region.
//
// **The whole-vector-bitwise family first** (7 mnemonics: `v128.not/and/andnot/or/xor/bitselect/
// any_true`), per #212's recommendation — no per-lane loop, no width dispatch, the cheapest
// confirmation that decision 0024's stack representation actually works end to end before the
// bulk per-lane arithmetic family (197 mnemonics) is attempted.
//
// Every arm here reads/writes v128 operands through `pushV128`/`popV128` (decision 0024) — never
// two independent `pushNum`/`popNum` calls, which would desync the two slots' shared sequence
// number and reproduce grave #206's shape one layer up. See `pushV128`'s own doc comment.
func (in *Instance) execFD(ins binary.Instr, st *stack) error {
	switch ins.Op {
	case 0x0c: // v128.const — Imm0 is the low 64 bits, Imm1 the high (immV128's decode arm,
		// binary/instr.go:788-798, "both halves, low first")
		st.pushV128(ins.Imm1, ins.Imm0)
	case 0x4d: // v128.not
		if err := st.needNum(2); err != nil {
			return err
		}
		hi, lo := st.popV128()
		st.pushV128(^hi, ^lo)
	case 0x4e: // v128.and
		if err := st.needNum(4); err != nil {
			return err
		}
		hi2, lo2 := st.popV128()
		hi1, lo1 := st.popV128()
		st.pushV128(hi1&hi2, lo1&lo2)
	case 0x4f: // v128.andnot — v1 AND NOT v2 (encode.ml/v128.ml's `andnot v x y = and_ x (not y)`)
		if err := st.needNum(4); err != nil {
			return err
		}
		hi2, lo2 := st.popV128()
		hi1, lo1 := st.popV128()
		st.pushV128(hi1&^hi2, lo1&^lo2)
	case 0x50: // v128.or
		if err := st.needNum(4); err != nil {
			return err
		}
		hi2, lo2 := st.popV128()
		hi1, lo1 := st.popV128()
		st.pushV128(hi1|hi2, lo1|lo2)
	case 0x51: // v128.xor
		if err := st.needNum(4); err != nil {
			return err
		}
		hi2, lo2 := st.popV128()
		hi1, lo1 := st.popV128()
		st.pushV128(hi1^hi2, lo1^lo2)
	case 0x52: // v128.bitselect — (v1 AND c) OR (v2 AND NOT c), c on top (v128.ml's bitselect)
		if err := st.needNum(6); err != nil {
			return err
		}
		chi, clo := st.popV128()
		hi2, lo2 := st.popV128()
		hi1, lo1 := st.popV128()
		st.pushV128((hi1&chi)|(hi2&^chi), (lo1&clo)|(lo2&^clo))
	case 0x53: // v128.any_true — nonzero anywhere in the 128 bits
		if err := st.needNum(2); err != nil {
			return err
		}
		hi, lo := st.popV128()
		st.pushBool(hi != 0 || lo != 0)

	// **The memory family, #212's second ladder rung.** `Imm0` is the memarg's offset and
	// `Imm1` its memory index (low 32 bits) — the identical staging the 0x28-0x3e MVP
	// load/store family already uses (`decodeMemop`, `binary/instr.go:1030-1054`) — with a
	// lane index OR'd into `Imm1`'s bits 32-39 for the eight lane-load/store forms
	// (`stageLaneIdx`, grave #100's own fix). `memoryFor`/`mem.read`/`mem.write`/`mem.addr` are
	// the MVP family's own helpers (`memory.go`), reused rather than duplicated.
	case 0x00: // v128.load
		return in.vecLoad(ins, st, v128FromBytes)
	case 0x0b: // v128.store
		return in.vecStore(ins, st, v128Bytes)
	case 0x01: // v128.load8x8_s — 8 bytes, sign-extend each to i16, 8 lanes
		return in.vecLoad(ins, st, loadExtend(8, true))
	case 0x02: // v128.load8x8_u
		return in.vecLoad(ins, st, loadExtend(8, false))
	case 0x03: // v128.load16x4_s — 4 i16 lanes, sign-extend each to i32, 4 lanes
		return in.vecLoad(ins, st, loadExtend(16, true))
	case 0x04: // v128.load16x4_u
		return in.vecLoad(ins, st, loadExtend(16, false))
	case 0x05: // v128.load32x2_s — 2 i32 lanes, sign-extend each to i64, 2 lanes
		return in.vecLoad(ins, st, loadExtend(32, true))
	case 0x06: // v128.load32x2_u
		return in.vecLoad(ins, st, loadExtend(32, false))
	case 0x07: // v128.load8_splat — one byte, splat to all 16 i8 lanes
		return in.vecLoad(ins, st, loadSplat(8))
	case 0x08: // v128.load16_splat — one i16, splat to all 8 lanes
		return in.vecLoad(ins, st, loadSplat(16))
	case 0x09: // v128.load32_splat — one i32, splat to all 4 lanes
		return in.vecLoad(ins, st, loadSplat(32))
	case 0x0a: // v128.load64_splat — one i64, splat to both lanes
		return in.vecLoad(ins, st, loadSplat(64))
	case 0x5c: // v128.load32_zero — one i32 in lane 0, every other bit zero
		return in.vecLoad(ins, st, loadZero(32))
	case 0x5d: // v128.load64_zero — one i64 in the low half, high half zero
		return in.vecLoad(ins, st, loadZero(64))

	case 0x54, 0x55, 0x56, 0x57: // v128.loadN_lane — memop, read N bytes, replace one lane
		return in.vecLoadLane(ins, st, laneWidth(ins.Op))
	case 0x58, 0x59, 0x5a, 0x5b: // v128.storeN_lane — memop, write one lane's N bytes
		return in.vecStoreLane(ins, st, laneWidth(ins.Op))

	// **The lane-access family, #212's fourth ladder rung — the scalar↔vector boundary.**
	// `eval_vec.ml`'s own naming: `splatop` wraps a scalar into every lane of a fresh v128,
	// `extractop` reads one lane back out and *widens* it to the pushed scalar's own width
	// (`I32`/`I64`/`F32`/`F64`), `replaceop` writes one lane and leaves the rest of the vector
	// untouched. Widths and lane counts follow the mnemonic's own shape suffix (i8x16 = 16
	// lanes of 1 byte, i16x8 = 8 of 2, i32x4/f32x4 = 4 of 4, i64x2/f64x2 = 2 of 8).
	case 0x0f: // i8x16.splat
		return in.vecSplat(st, 1)
	case 0x10: // i16x8.splat
		return in.vecSplat(st, 2)
	case 0x11, 0x13: // i32x4.splat, f32x4.splat — same width, same bits (verbatim, per exec.go's
		// own float-bits-are-numeric-bits rule)
		return in.vecSplat(st, 4)
	case 0x12, 0x14: // i64x2.splat, f64x2.splat
		return in.vecSplat(st, 8)

	case 0x15: // i8x16.extract_lane_s
		return in.vecExtractLane(ins, st, 1, true, false)
	case 0x16: // i8x16.extract_lane_u
		return in.vecExtractLane(ins, st, 1, false, false)
	case 0x18: // i16x8.extract_lane_s
		return in.vecExtractLane(ins, st, 2, true, false)
	case 0x19: // i16x8.extract_lane_u
		return in.vecExtractLane(ins, st, 2, false, false)
	case 0x1b: // i32x4.extract_lane
		return in.vecExtractLane(ins, st, 4, false, false)
	case 0x1d: // i64x2.extract_lane
		return in.vecExtractLane(ins, st, 8, false, true)
	case 0x1f: // f32x4.extract_lane
		return in.vecExtractLane(ins, st, 4, false, false)
	case 0x21: // f64x2.extract_lane
		return in.vecExtractLane(ins, st, 8, false, true)

	case 0x17: // i8x16.replace_lane
		return in.vecReplaceLane(ins, st, 1, false)
	case 0x1a: // i16x8.replace_lane
		return in.vecReplaceLane(ins, st, 2, false)
	case 0x1c, 0x20: // i32x4.replace_lane, f32x4.replace_lane — same width, different opcodes
		return in.vecReplaceLane(ins, st, 4, false)
	case 0x1e, 0x22: // i64x2.replace_lane, f64x2.replace_lane
		return in.vecReplaceLane(ins, st, 8, true)

	// **The bulk per-lane family's first sub-batch, #212's fifth ladder rung's own opening
	// slice — integer-only, no arch-sensitive rounding or NaN propagation, per the recon's own
	// risk ordering.** `abs`/`neg`/`popcnt` (`VecUnary`), `all_true` (`VecTest`), `bitmask`
	// (`VecBitmask`): 17 mnemonics across the four integer shapes, i8x16 alone carrying
	// `popcnt` (the others have no per-lane population count in the tracked proposal set).
	case 0x60: // i8x16.abs
		return in.vecUnaryLanes(st, 1, absLane)
	case 0x61: // i8x16.neg
		return in.vecUnaryLanes(st, 1, negLane)
	case 0x62: // i8x16.popcnt
		return in.vecUnaryLanes(st, 1, popcntLane)
	case 0x63: // i8x16.all_true
		return in.vecAllTrue(st, 1)
	case 0x64: // i8x16.bitmask
		return in.vecBitmask(st, 1)
	case 0x80: // i16x8.abs
		return in.vecUnaryLanes(st, 2, absLane)
	case 0x81: // i16x8.neg
		return in.vecUnaryLanes(st, 2, negLane)
	case 0x83: // i16x8.all_true
		return in.vecAllTrue(st, 2)
	case 0x84: // i16x8.bitmask
		return in.vecBitmask(st, 2)
	case 0xa0: // i32x4.abs
		return in.vecUnaryLanes(st, 4, absLane)
	case 0xa1: // i32x4.neg
		return in.vecUnaryLanes(st, 4, negLane)
	case 0xa3: // i32x4.all_true
		return in.vecAllTrue(st, 4)
	case 0xa4: // i32x4.bitmask
		return in.vecBitmask(st, 4)
	case 0xc0: // i64x2.abs
		return in.vecUnaryLanes(st, 8, absLane)
	case 0xc1: // i64x2.neg
		return in.vecUnaryLanes(st, 8, negLane)
	case 0xc3: // i64x2.all_true
		return in.vecAllTrue(st, 8)
	case 0xc4: // i64x2.bitmask
		return in.vecBitmask(st, 8)

	// **VecShift — #212's own family, 12 mnemonics, integer-only.** `eval.ml`'s own stack shape
	// for VecShift is `Num s :: Vec v :: vs'`: the i32 shift count is pushed *after* the vector
	// operand and therefore pops first — `(i8x16.shl v c)` in wat, c on top — never the
	// two-v128-operand order the whole-vector-bitwise and VecCompare families use above.
	//
	// **The shift count is masked to the lane's own bit width before use, per `IXX.shift`'s own
	// `logand j (of_int (bitwidth - 1))`** — `i8x16.shl` with a count of 8 shifts by 0, not by 8,
	// exactly as a real 8-bit shift instruction wraps. Go's own `<<`/`>>` on a sized integer type
	// does not do this for a shift count at or beyond the type's width (undefined-adjacent, not
	// wrapped), so the mask is applied explicitly here rather than trusted to fall out of the
	// Go operator the way it would for a same-width native shift.
	case 0x6b: // i8x16.shl
		return in.vecShiftLanes(st, 1, false, false)
	case 0x6c: // i8x16.shr_s
		return in.vecShiftLanes(st, 1, true, true)
	case 0x6d: // i8x16.shr_u
		return in.vecShiftLanes(st, 1, true, false)
	case 0x8b: // i16x8.shl
		return in.vecShiftLanes(st, 2, false, false)
	case 0x8c: // i16x8.shr_s
		return in.vecShiftLanes(st, 2, true, true)
	case 0x8d: // i16x8.shr_u
		return in.vecShiftLanes(st, 2, true, false)
	case 0xab: // i32x4.shl
		return in.vecShiftLanes(st, 4, false, false)
	case 0xac: // i32x4.shr_s
		return in.vecShiftLanes(st, 4, true, true)
	case 0xad: // i32x4.shr_u
		return in.vecShiftLanes(st, 4, true, false)
	case 0xcb: // i64x2.shl
		return in.vecShiftLanes(st, 8, false, false)
	case 0xcc: // i64x2.shr_s
		return in.vecShiftLanes(st, 8, true, true)
	case 0xcd: // i64x2.shr_u
		return in.vecShiftLanes(st, 8, true, false)

	// **VecCompare, #212's fifth ladder rung's second sub-batch — 48 mnemonics, moderate risk.**
	// Chosen before the float arithmetic arms per Scott's own ordering: comparison itself needs
	// no rounding and no signed-zero handling (Go's native `<`/`>`/`==` on float32/float64
	// already implement IEEE ordered comparison, including "NaN compares false to everything"),
	// so this batch exercises the shared per-lane plumbing across every width and signedness
	// combination — hardening it by volume — before the float family's genuinely arch-risky
	// arms (`min`/`max`/`pmin`/`pmax`, the recon's own named risk) have to trust it.
	//
	// `eval.ml`'s own stack order for every VecBinary/VecCompare/VecBinaryBits arm: `Vec n2 ::
	// Vec n1 :: vs'` — n2 (the second-pushed operand) is on top and pops first, matching the
	// whole-vector-bitwise family's own established `hi2,lo2 := popV128(); hi1,lo1 :=
	// popV128()` order (v128.and/or/xor, PR #214).
	//
	// A lane's comparison result is all-ones (not 1) when true, all-zero when false —
	// `v128.ml`'s own `cmp` helper for both the integer and float shapes (`if f x y then
	// IXX.of_int_s (-1) else IXX.zero`, and the float side's identical-shape `all_ones`/`zero`
	// pair) — confirmed by reading both, not assumed to match from the name alone.
	case 0x23: // i8x16.eq
		return in.vecCompare(st, 1, false, cmpEq)
	case 0x24: // i8x16.ne
		return in.vecCompare(st, 1, false, cmpNe)
	case 0x25: // i8x16.lt_s
		return in.vecCompare(st, 1, true, cmpLt)
	case 0x26: // i8x16.lt_u
		return in.vecCompare(st, 1, false, cmpLt)
	case 0x27: // i8x16.gt_s
		return in.vecCompare(st, 1, true, cmpGt)
	case 0x28: // i8x16.gt_u
		return in.vecCompare(st, 1, false, cmpGt)
	case 0x29: // i8x16.le_s
		return in.vecCompare(st, 1, true, cmpLe)
	case 0x2a: // i8x16.le_u
		return in.vecCompare(st, 1, false, cmpLe)
	case 0x2b: // i8x16.ge_s
		return in.vecCompare(st, 1, true, cmpGe)
	case 0x2c: // i8x16.ge_u
		return in.vecCompare(st, 1, false, cmpGe)

	case 0x2d: // i16x8.eq
		return in.vecCompare(st, 2, false, cmpEq)
	case 0x2e: // i16x8.ne
		return in.vecCompare(st, 2, false, cmpNe)
	case 0x2f: // i16x8.lt_s
		return in.vecCompare(st, 2, true, cmpLt)
	case 0x30: // i16x8.lt_u
		return in.vecCompare(st, 2, false, cmpLt)
	case 0x31: // i16x8.gt_s
		return in.vecCompare(st, 2, true, cmpGt)
	case 0x32: // i16x8.gt_u
		return in.vecCompare(st, 2, false, cmpGt)
	case 0x33: // i16x8.le_s
		return in.vecCompare(st, 2, true, cmpLe)
	case 0x34: // i16x8.le_u
		return in.vecCompare(st, 2, false, cmpLe)
	case 0x35: // i16x8.ge_s
		return in.vecCompare(st, 2, true, cmpGe)
	case 0x36: // i16x8.ge_u
		return in.vecCompare(st, 2, false, cmpGe)

	case 0x37: // i32x4.eq
		return in.vecCompare(st, 4, false, cmpEq)
	case 0x38: // i32x4.ne
		return in.vecCompare(st, 4, false, cmpNe)
	case 0x39: // i32x4.lt_s
		return in.vecCompare(st, 4, true, cmpLt)
	case 0x3a: // i32x4.lt_u
		return in.vecCompare(st, 4, false, cmpLt)
	case 0x3b: // i32x4.gt_s
		return in.vecCompare(st, 4, true, cmpGt)
	case 0x3c: // i32x4.gt_u
		return in.vecCompare(st, 4, false, cmpGt)
	case 0x3d: // i32x4.le_s
		return in.vecCompare(st, 4, true, cmpLe)
	case 0x3e: // i32x4.le_u
		return in.vecCompare(st, 4, false, cmpLe)
	case 0x3f: // i32x4.ge_s
		return in.vecCompare(st, 4, true, cmpGe)
	case 0x40: // i32x4.ge_u
		return in.vecCompare(st, 4, false, cmpGe)

	// i64x2 has no unsigned compares in the tracked proposal set — only eq/ne and the four
	// signed relational ones.
	case 0xd6: // i64x2.eq
		return in.vecCompare(st, 8, false, cmpEq)
	case 0xd7: // i64x2.ne
		return in.vecCompare(st, 8, false, cmpNe)
	case 0xd8: // i64x2.lt_s
		return in.vecCompare(st, 8, true, cmpLt)
	case 0xd9: // i64x2.gt_s
		return in.vecCompare(st, 8, true, cmpGt)
	case 0xda: // i64x2.le_s
		return in.vecCompare(st, 8, true, cmpLe)
	case 0xdb: // i64x2.ge_s
		return in.vecCompare(st, 8, true, cmpGe)

	case 0x41: // f32x4.eq
		return in.vecCompareFloat(st, 4, cmpEqF)
	case 0x42: // f32x4.ne
		return in.vecCompareFloat(st, 4, cmpNeF)
	case 0x43: // f32x4.lt
		return in.vecCompareFloat(st, 4, cmpLtF)
	case 0x44: // f32x4.gt
		return in.vecCompareFloat(st, 4, cmpGtF)
	case 0x45: // f32x4.le
		return in.vecCompareFloat(st, 4, cmpLeF)
	case 0x46: // f32x4.ge
		return in.vecCompareFloat(st, 4, cmpGeF)
	case 0x47: // f64x2.eq
		return in.vecCompareFloat(st, 8, cmpEqF)
	case 0x48: // f64x2.ne
		return in.vecCompareFloat(st, 8, cmpNeF)
	case 0x49: // f64x2.lt
		return in.vecCompareFloat(st, 8, cmpLtF)
	case 0x4a: // f64x2.gt
		return in.vecCompareFloat(st, 8, cmpGtF)
	case 0x4b: // f64x2.le
		return in.vecCompareFloat(st, 8, cmpLeF)
	case 0x4c: // f64x2.ge
		return in.vecCompareFloat(st, 8, cmpGeF)

	// **The float arithmetic sub-batch, #212's fifth ladder rung's third and highest-risk
	// slice — 30 mnemonics (14 unary: abs/neg/sqrt/ceil/floor/trunc/nearest × 2 shapes; 16
	// binary: add/sub/mul/div/min/max/pmin/pmax × 2 shapes).** Landing on plumbing hardened by
	// 65 comparison arms across every width, per Scott's own ordering.
	//
	// **abs/neg are bitwise, never through Go's math.Abs or unary negation** — `fxx.ml`'s own
	// comment states it explicitly ("abs, neg, copysign are purely bitwise operations, even on
	// NaN values"), confirmed by measurement: `math.Abs`/unary-negate happen to produce the
	// identical bit pattern for the NaN values tried, but the language does not guarantee that,
	// and the reference's own choice to bypass the float ALU for these two is the tell that it
	// matters somewhere the trial did not reach. Sign-bit clear/flip, exactly as the integer
	// abs/neg arms operate on two's-complement bits rather than through Go arithmetic.
	//
	// **ceil/floor/trunc/nearest/sqrt use Go's own math package directly** (Ceil/Floor/Trunc/
	// RoundToEven/Sqrt) rather than reimplementing rounding — confirmed by direct measurement,
	// not assumed, that each already preserves the sign of a zero input exactly as the
	// reference's own explicit "if xf = 0.0 then x" special case requires, and each already
	// quiets a signaling NaN's payload to an `nan:arithmetic`-class result, which is all the
	// suite's own `assert_return` vectors ever check (NaN class, never exact bits, for every
	// arithmetic NaN result in the tracked proposal set).
	//
	// **min/max/pmin/pmax are four genuinely distinct functions, not two names for the same
	// pair** — `v128.ml`'s own definitions: `min`/`max` special-case the equal-operands case via
	// bitwise `logor`/`logand` (so `min(-0,0) = -0` and `max(-0,0) = 0`, confirmed against Go's
	// `math.Min`/`math.Max` in decision 0024's own arch-dependence survey — they already agree,
	// measured), while `pmin`/`pmax` are a single unconditional comparison with **no** equal-
	// operands special case and are **not symmetric**: `pmin x y = if y < x then y else x`
	// always returns `x` when the comparison is false — including when `y` is NaN (NaN is never
	// less than anything), which is the one case a reader implementing pmin as "the smaller of
	// the two" would get backwards.
	case 0xe0: // f32x4.abs
		return in.vecUnaryLanes(st, 4, absFloatLane(4))
	case 0xe1: // f32x4.neg
		return in.vecUnaryLanes(st, 4, negFloatLane(4))
	case 0xe3: // f32x4.sqrt
		return in.vecUnaryLanes(st, 4, floatUnary(4, math.Sqrt))
	case 0x67: // f32x4.ceil
		return in.vecUnaryLanes(st, 4, floatUnary(4, math.Ceil))
	case 0x68: // f32x4.floor
		return in.vecUnaryLanes(st, 4, floatUnary(4, math.Floor))
	case 0x69: // f32x4.trunc
		return in.vecUnaryLanes(st, 4, floatUnary(4, math.Trunc))
	case 0x6a: // f32x4.nearest
		return in.vecUnaryLanes(st, 4, floatUnary(4, math.RoundToEven))

	case 0xec: // f64x2.abs
		return in.vecUnaryLanes(st, 8, absFloatLane(8))
	case 0xed: // f64x2.neg
		return in.vecUnaryLanes(st, 8, negFloatLane(8))
	case 0xef: // f64x2.sqrt
		return in.vecUnaryLanes(st, 8, floatUnary(8, math.Sqrt))
	case 0x74: // f64x2.ceil
		return in.vecUnaryLanes(st, 8, floatUnary(8, math.Ceil))
	case 0x75: // f64x2.floor
		return in.vecUnaryLanes(st, 8, floatUnary(8, math.Floor))
	case 0x7a: // f64x2.trunc
		return in.vecUnaryLanes(st, 8, floatUnary(8, math.Trunc))
	case 0x94: // f64x2.nearest
		return in.vecUnaryLanes(st, 8, floatUnary(8, math.RoundToEven))

	case 0xe4: // f32x4.add
		return in.vecBinaryFloat(st, 4, func(a, b float64) float64 { return a + b })
	case 0xe5: // f32x4.sub
		return in.vecBinaryFloat(st, 4, func(a, b float64) float64 { return a - b })
	case 0xe6: // f32x4.mul
		return in.vecBinaryFloat(st, 4, func(a, b float64) float64 { return a * b })
	case 0xe7: // f32x4.div
		return in.vecBinaryFloat(st, 4, func(a, b float64) float64 { return a / b })
	case 0xe8: // f32x4.min
		return in.vecBinaryFloat(st, 4, floatMin)
	case 0xe9: // f32x4.max
		return in.vecBinaryFloat(st, 4, floatMax)
	case 0xea: // f32x4.pmin
		return in.vecBinaryFloat(st, 4, floatPmin)
	case 0xeb: // f32x4.pmax
		return in.vecBinaryFloat(st, 4, floatPmax)

	case 0xf0: // f64x2.add
		return in.vecBinaryFloat(st, 8, func(a, b float64) float64 { return a + b })
	case 0xf1: // f64x2.sub
		return in.vecBinaryFloat(st, 8, func(a, b float64) float64 { return a - b })
	case 0xf2: // f64x2.mul
		return in.vecBinaryFloat(st, 8, func(a, b float64) float64 { return a * b })
	case 0xf3: // f64x2.div
		return in.vecBinaryFloat(st, 8, func(a, b float64) float64 { return a / b })
	case 0xf4: // f64x2.min
		return in.vecBinaryFloat(st, 8, floatMin)
	case 0xf5: // f64x2.max
		return in.vecBinaryFloat(st, 8, floatMax)
	case 0xf6: // f64x2.pmin
		return in.vecBinaryFloat(st, 8, floatPmin)
	case 0xf7: // f64x2.pmax
		return in.vecBinaryFloat(st, 8, floatPmax)

	default:
		return unsupported(ins)
	}
	return nil
}

// laneWidth maps a load/store-lane opcode to the byte width it moves — 8/16/32/64 bits by the
// same 0x54/0x58, 0x55/0x59, 0x56/0x5a, 0x57/0x5b grouping the opcode table declares (load and
// store share a width per pair, four bytes apart).
func laneWidth(op uint32) uint64 {
	switch op {
	case 0x54, 0x58:
		return 1
	case 0x55, 0x59:
		return 2
	case 0x56, 0x5a:
		return 4
	default: // 0x57, 0x5b
		return 8
	}
}

// v128Bytes renders a v128 as sixteen little-endian bytes, low half first — the wire format's
// own layout (`immV128`'s decode arm, binary/instr.go:788-798) and `pushV128`'s own hi/lo order.
func v128Bytes(hi, lo uint64) []byte {
	bs := make([]byte, 16)
	for i := range 8 {
		bs[i] = byte(lo >> (8 * uint(i)))
		bs[8+i] = byte(hi >> (8 * uint(i)))
	}
	return bs
}

// v128FromBytes is v128Bytes's inverse: sixteen little-endian bytes to a (hi, lo) pair.
func v128FromBytes(bs []byte) (hi, lo uint64) {
	for i := 7; i >= 0; i-- {
		lo = lo<<8 | uint64(bs[i])
		hi = hi<<8 | uint64(bs[8+i])
	}
	return hi, lo
}

// loadExtend builds the six load8x8_s/u, load16x4_s/u, load32x2_s/u readers: read half the
// lanes' bytes (8), sign- or zero-extend each `laneBits`-wide lane to double its width, and pack
// the widened lanes into a v128 — `value.ml:222-227`'s `ExtLane` arms, one reader parameterized
// by the source lane width and signedness rather than six separate functions, since the shape
// (read N narrow lanes, widen each by 2x, pack) is identical across all six.
func loadExtend(laneBits uint, signed bool) func([]byte) (hi, lo uint64) {
	n := 64 / laneBits // narrow lanes read: 8 for 8-bit source, 4 for 16-bit, 2 for 32-bit
	return func(bs []byte) (hi, lo uint64) {
		lanes := make([]uint64, n)
		for i := range n {
			var raw uint64
			for b := range laneBits / 8 {
				raw |= uint64(bs[i*(laneBits/8)+b]) << (8 * b)
			}
			if signed {
				shift := 64 - laneBits
				lanes[i] = uint64(int64(raw<<shift) >> shift)
			} else {
				lanes[i] = raw
			}
		}
		// Widened lanes are twice laneBits wide, so n/2 fit per 64-bit half — pack low half's
		// lanes into lo, high half's into hi, matching immV128/pushV128's own little-endian,
		// low-half-first convention.
		widened := laneBits * 2
		perHalf := 64 / widened
		for i := range perHalf {
			lo |= (lanes[i] & mask(widened)) << (widened * i)
		}
		for i := range perHalf {
			hi |= (lanes[perHalf+i] & mask(widened)) << (widened * i)
		}
		return hi, lo
	}
}

// loadSplat builds the four loadN_splat readers: read one laneBits-wide scalar and replicate it
// across every lane of the result — `value.ml:229-232`'s `ExtSplat` arms.
func loadSplat(laneBits uint) func([]byte) (hi, lo uint64) {
	return func(bs []byte) (hi, lo uint64) {
		var raw uint64
		for b := range laneBits / 8 {
			raw |= uint64(bs[b]) << (8 * b)
		}
		raw &= mask(laneBits)
		if laneBits == 64 {
			return raw, raw
		}
		n := 64 / laneBits
		for i := range n {
			lo |= raw << (laneBits * i)
		}
		return lo, lo
	}
}

// loadZero builds the two loadN_zero readers: read one laneBits-wide scalar into lane 0, every
// other bit zero — `value.ml:233-234`'s `ExtZero` arms, which are literally "the raw bits,
// zero-extended to 128" since load_vec_packed already reads exactly `packed_size sz` bytes and
// the caller zero-fills the rest of its 16-byte buffer (`value.ml:213-216`).
func loadZero(laneBits uint) func([]byte) (hi, lo uint64) {
	return func(bs []byte) (hi, lo uint64) {
		var raw uint64
		for b := range laneBits / 8 {
			raw |= uint64(bs[b]) << (8 * b)
		}
		return 0, raw
	}
}

// mask returns a bitmask of the low n bits, for n in {8,16,32,64} — 1<<64-1 would overflow a
// shift, so 64 is its own case rather than a formula every caller has to special-case itself.
func mask(n uint) uint64 {
	if n >= 64 {
		return ^uint64(0)
	}
	return (uint64(1) << n) - 1
}

// vecLoad is v128.load and its twelve packed siblings: resolve the memory, read the access's own
// byte width, decode through `decode`, push the result.
func (in *Instance) vecLoad(ins binary.Instr, st *stack, decode func([]byte) (hi, lo uint64)) error {
	mem, resolveErr := in.memoryFor("instruction", ins.Imm1&0xFFFFFFFF)
	if resolveErr != nil {
		return resolveErr
	}
	if err := st.needNum(1); err != nil {
		return err
	}
	idx := st.popNum()
	n := vecLoadWidth(ins.Op)
	bs, err := mem.read(mem.addr(idx), ins.Imm0, n)
	if err != nil {
		return err
	}
	hi, lo := decode(bs)
	st.pushV128(hi, lo)
	return nil
}

// vecLoadWidth is the byte count each vecLoad opcode reads from memory — 16 for the bare load,
// 8 for every packed form (all six read exactly half a v128's worth, per `Pack.packed_size`
// applied to Pack64/Pack32/Pack16/Pack8 in `value.ml`'s own table), except load32_zero, which
// reads 4.
func vecLoadWidth(op uint32) uint64 {
	switch op {
	case 0x00: // v128.load
		return 16
	case 0x5c: // v128.load32_zero
		return 4
	default: // the six load*x*_s/u and four load*_splat forms, plus load64_zero
		return 8
	}
}

// vecStore is v128.store: resolve the memory, pop the value then the address (the stack's own
// order — memAccess's own doc comment states the identical rule for the MVP family), write.
func (in *Instance) vecStore(ins binary.Instr, st *stack, encode func(hi, lo uint64) []byte) error {
	mem, resolveErr := in.memoryFor("instruction", ins.Imm1&0xFFFFFFFF)
	if resolveErr != nil {
		return resolveErr
	}
	if err := st.needNum(3); err != nil {
		return err
	}
	hi, lo := st.popV128() // the value, pushed second
	idx := st.popNum()     // the address, pushed first
	return mem.write(mem.addr(idx), ins.Imm0, encode(hi, lo))
}

// vecLoadLane reads width bytes at the memarg's address and replaces one lane of the v128
// operand already on the stack — `eval.ml`'s `VecLoadLane` arm, `Vec (V128 v) :: Num i :: vs'`:
// the vector is pushed *after* the address, so it pops first.
func (in *Instance) vecLoadLane(ins binary.Instr, st *stack, width uint64) error {
	mem, resolveErr := in.memoryFor("instruction", ins.Imm1&0xFFFFFFFF)
	if resolveErr != nil {
		return resolveErr
	}
	if err := st.needNum(3); err != nil {
		return err
	}
	hi, lo := st.popV128() // the operand vector, pushed second
	idx := st.popNum()     // the address, pushed first
	bs, err := mem.read(mem.addr(idx), ins.Imm0, width)
	if err != nil {
		return err
	}
	lane := (ins.Imm1 >> 32) & 0xFF
	hi, lo = replaceLaneBytes(hi, lo, uint(lane), width, bs)
	st.pushV128(hi, lo)
	return nil
}

// vecStoreLane writes one lane's bytes to the memarg's address — `eval.ml`'s `VecStoreLane` arm.
func (in *Instance) vecStoreLane(ins binary.Instr, st *stack, width uint64) error {
	mem, resolveErr := in.memoryFor("instruction", ins.Imm1&0xFFFFFFFF)
	if resolveErr != nil {
		return resolveErr
	}
	if err := st.needNum(3); err != nil {
		return err
	}
	hi, lo := st.popV128() // the value, pushed second
	idx := st.popNum()     // the address, pushed first
	lane := (ins.Imm1 >> 32) & 0xFF
	return mem.write(mem.addr(idx), ins.Imm0, laneBytes(hi, lo, uint(lane), width))
}

// laneBytes extracts one lane's little-endian bytes from a v128, where a "lane" for this family
// is width bytes at byte offset `lane*width` within the 16-byte value — v128Bytes's own layout,
// read back out width bytes at a time rather than reinterpreting through a typed lane array,
// since the lane-load/store family's width varies per opcode (1/2/4/8 bytes) and the underlying
// bytes are identical regardless of which numeric lane shape a reader might otherwise assume.
func laneBytes(hi, lo uint64, lane uint, width uint64) []byte {
	full := v128Bytes(hi, lo)
	off := lane * uint(width)
	return full[off : off+uint(width)]
}

// replaceLaneBytes writes bs (width bytes) into lane's position within the v128 (hi, lo),
// returning the updated pair — laneBytes's own inverse.
func replaceLaneBytes(hi, lo uint64, lane uint, width uint64, bs []byte) (newHi, newLo uint64) {
	full := v128Bytes(hi, lo)
	off := lane * uint(width)
	copy(full[off:off+uint(width)], bs)
	return v128FromBytes(full)
}

// vecSplat implements the six `*.splat` opcodes: wrap a scalar's low `width` bytes into every
// lane of a fresh v128 — `eval_vec.ml`'s `splatop`, which reads the scalar through
// `wrap_i32`/`I32Num.of_num`/etc. (a truncation to the lane's own width, never a conversion) and
// replicates the identical bytes into each lane position.
//
// f32x4.splat and f64x2.splat share this arm with their integer siblings of the same width
// (0x11/0x13 both width 4, 0x12/0x14 both width 8) because a float's bits are the numeric bits
// verbatim, exec.go's own standing rule for every opcode that moves a float without arithmetic
// on it — splatting the scalar's raw bytes is correct for both readings without a branch.
func (in *Instance) vecSplat(st *stack, width uint64) error {
	if err := st.needNum(1); err != nil {
		return err
	}
	scalar := st.popNum()
	bs := make([]byte, width)
	for i := range width {
		bs[i] = byte(scalar >> (8 * i))
	}
	var full [16]byte
	for i := 0; i < 16; i += int(width) {
		copy(full[i:], bs)
	}
	hi, lo := v128FromBytes(full[:])
	st.pushV128(hi, lo)
	return nil
}

// vecExtractLane implements the eight `*.extract_lane[_s|_u]` opcodes: read one `width`-byte
// lane out of the v128 operand and widen it to a full numeric slot — `eval_vec.ml`'s
// `extractop`, whose `S`/`U` arms sign- or zero-extend the narrow lane (i8x16/i16x8 only; the
// four wider shapes have no signedness suffix because their lane width already equals the slot
// width once packed into a 64-bit numeric).
//
// `is64` distinguishes i64x2/f64x2 (a 64-bit slot, `st.pushNum` directly) from the four 32-bit
// shapes (i8x16/i16x8/i32x4/f32x4, all of which push through the identical zero-extend-to-64
// path `pushNum` already gives an i32 slot — see pushNum's own doc comment on why i32 occupies a
// full slot with high bits zero).
func (in *Instance) vecExtractLane(ins binary.Instr, st *stack, width uint64, signed, is64 bool) error {
	if err := st.needNum(2); err != nil {
		return err
	}
	hi, lo := st.popV128()
	// Imm0 carries the lane index — the bare `immLaneIdx` shape (no preceding memarg), so it
	// stages into the first word rather than being OR'd into Imm1 the way the lane-load/store
	// family's index is (`instrCtx.stage`'s own precondition: `immN < 2`).
	lane := uint(ins.Imm0) & 0xFF
	bs := laneBytes(hi, lo, lane, width)
	var raw uint64
	for i := len(bs) - 1; i >= 0; i-- {
		raw = raw<<8 | uint64(bs[i])
	}
	if signed {
		shift := 64 - width*8
		raw = uint64(int64(raw<<shift) >> shift)
		if !is64 {
			raw = uint64(uint32(raw))
		}
	} else if !is64 {
		raw = uint64(uint32(raw))
	}
	st.pushNum(raw)
	return nil
}

// vecReplaceLane implements the six `*.replace_lane` opcodes: write width bytes of the pushed
// scalar into one lane of the v128 operand, leaving every other lane untouched — `eval.ml`'s
// `VecReplace` arm, `Num r :: Vec v :: vs'`: the scalar is on top (popped first), the vector
// below it (popped second), and `is64` selects whether the scalar is a full 64-bit slot
// (i64x2/f64x2) or the low 32 bits of one (i8x16/i16x8/i32x4/f32x4 all wrap through i32).
func (in *Instance) vecReplaceLane(ins binary.Instr, st *stack, width uint64, is64 bool) error {
	if err := st.needNum(3); err != nil {
		return err
	}
	scalar := st.popNum()
	if !is64 {
		scalar = uint64(uint32(scalar))
	}
	hi, lo := st.popV128()
	lane := uint(ins.Imm0) & 0xFF
	bs := make([]byte, width)
	for i := range width {
		bs[i] = byte(scalar >> (8 * i))
	}
	hi, lo = replaceLaneBytes(hi, lo, lane, width, bs)
	st.pushV128(hi, lo)
	return nil
}

// laneCount is how many `width`-byte lanes a v128 holds — always 16/width, since a v128 is
// always exactly 16 bytes regardless of shape.
func laneCount(width uint64) uint64 { return 16 / width }

// lanesOf splits a v128 into its individual lanes, each read as a raw little-endian uint64 with
// the lane's own bytes in the low `width*8` bits and everything above zero. The shape (i8x16 vs
// f32x4, signed vs unsigned) is entirely the caller's business — this function only knows how
// wide a lane is.
func lanesOf(hi, lo, width uint64) []uint64 {
	full := v128Bytes(hi, lo)
	n := laneCount(width)
	lanes := make([]uint64, n)
	for i := range n {
		var raw uint64
		off := i * width
		for b := range width {
			raw |= uint64(full[off+b]) << (8 * b)
		}
		lanes[i] = raw
	}
	return lanes
}

// lanesToV128 is lanesOf's inverse: pack width-byte lanes (each already masked to its own width
// by the caller) back into a v128's hi/lo pair.
func lanesToV128(lanes []uint64, width uint64) (hi, lo uint64) {
	var full [16]byte
	for i, lane := range lanes {
		off := uint64(i) * width
		for b := range width {
			full[off+b] = byte(lane >> (8 * b))
		}
	}
	return v128FromBytes(full[:])
}

// signExtendLane sign-extends a lane's low `width*8` bits to a full 64-bit signed reading —
// every per-lane arithmetic op that needs signedness (abs, neg, the signed compares, min_s/
// max_s) reads its lanes this way rather than duplicating the shift-pair each time.
func signExtendLane(raw, width uint64) int64 {
	shift := 64 - width*8
	return int64(raw<<shift) >> shift
}

// maskLane truncates a computed value back to its lane's own width — every arithmetic result
// must be re-masked before repacking, since Go's own arithmetic on a uint64 does not wrap at an
// arbitrary bit width the way an i8/i16/i32 lane does.
func maskLane(v, width uint64) uint64 { return v & mask(uint(width*8)) }

// absLane and negLane are per-lane arithmetic on a signed reading, re-masked to the lane's own
// width — `IXX.abs`/`IXX.neg`, two's-complement, so `abs(minInt8) == minInt8` (no wider type to
// escape into) exactly as Go's own `int8` arithmetic already wraps.
func absLane(raw, width uint64) uint64 {
	v := signExtendLane(raw, width)
	if v < 0 {
		v = -v
	}
	return maskLane(uint64(v), width)
}

func negLane(raw, width uint64) uint64 {
	return maskLane(uint64(-signExtendLane(raw, width)), width)
}

// popcntLane counts set bits within the lane's own width — i8x16.popcnt is the only mnemonic in
// the tracked proposal set that has one, so this is never called with width other than 1, but it
// is written generally rather than hardcoded to 8 bits since nothing about the algorithm needs
// the restriction.
func popcntLane(raw, width uint64) uint64 {
	return uint64(bitsOnesCount64(raw & mask(uint(width*8))))
}

// bitsOnesCount64 avoids importing math/bits for one call site — Kernighan's own bit-counting
// loop, clear enough not to need a second package pulled in for a single-lane popcnt that never
// exceeds 8 bits in this proposal's tracked opcode set.
func bitsOnesCount64(v uint64) int {
	n := 0
	for v != 0 {
		v &= v - 1
		n++
	}
	return n
}

// vecUnaryLanes applies fn to every lane of the v128 on top of the stack and pushes the result —
// the shared shape behind abs/neg/popcnt (and, on later rungs, every other per-lane unary op
// that does not need float rounding).
func (in *Instance) vecUnaryLanes(st *stack, width uint64, fn func(raw, width uint64) uint64) error {
	if err := st.needNum(2); err != nil {
		return err
	}
	hi, lo := st.popV128()
	lanes := lanesOf(hi, lo, width)
	for i, l := range lanes {
		lanes[i] = fn(l, width)
	}
	hi, lo = lanesToV128(lanes, width)
	st.pushV128(hi, lo)
	return nil
}

// vecShiftLanes implements the 12 `*.shl`/`*.shr_s`/`*.shr_u` mnemonics: pop an i32 shift count
// (on top, per `eval.ml`'s own `Num s :: Vec v :: vs'` stack shape) and a v128 operand, shift
// every lane by the count masked to the lane's own bit width, and push the result.
//
// `signed` selects arithmetic-vs-logical shift for a right shift (unread for `shl`, which has no
// sign-dependent variant in the tracked mnemonic set); `right` selects the direction. Three bools
// rather than a `func(int64, uint) uint64` parameter, unlike `vecUnaryLanes`'s `fn` — a shift's
// third operand (the count) does not vary per call the way abs/neg/popcnt's zero-argument shape
// does, so there is no per-call closure to build, and the three opcodes-per-width pattern reads
// more directly as three named booleans than as three near-identical one-line closures.
func (in *Instance) vecShiftLanes(st *stack, width uint64, right, signed bool) error {
	if err := st.needNum(3); err != nil {
		return err
	}
	count := uint64(st.popI32())
	hi, lo := st.popV128()
	shift := count & (width*8 - 1) // IXX.shift's own `logand j (of_int (bitwidth - 1))`
	lanes := lanesOf(hi, lo, width)
	for i, l := range lanes {
		switch {
		case !right: // shl
			lanes[i] = maskLane(l<<shift, width)
		case signed: // shr_s
			lanes[i] = maskLane(uint64(signExtendLane(l, width)>>shift), width)
		default: // shr_u
			lanes[i] = maskLane(l>>shift, width)
		}
	}
	hi, lo = lanesToV128(lanes, width)
	st.pushV128(hi, lo)
	return nil
}

// vecAllTrue implements `*.all_true`: an i32 result, 1 iff every lane is nonzero — `eval_vec.ml`'s
// `reduceop (&&) true`, folded left over the lanes with an empty vector (impossible here, a v128
// always has at least one lane at every tracked width) vacuously true.
func (in *Instance) vecAllTrue(st *stack, width uint64) error {
	if err := st.needNum(2); err != nil {
		return err
	}
	hi, lo := st.popV128()
	lanes := lanesOf(hi, lo, width)
	all := true
	for _, l := range lanes {
		if l == 0 {
			all = false
			break
		}
	}
	st.pushBool(all)
	return nil
}

// vecBitmask implements `*.bitmask`: an i32 whose bit i is 1 iff lane i's sign bit is set —
// `eval_vec.ml`'s `bitmask`, confirmed by hand-tracing its `fold_right` against `logor`/
// `shift_left`: lane 0's sign bit lands in result bit 0, ascending by lane index.
func (in *Instance) vecBitmask(st *stack, width uint64) error {
	if err := st.needNum(2); err != nil {
		return err
	}
	hi, lo := st.popV128()
	lanes := lanesOf(hi, lo, width)
	var mask uint32
	signBit := width*8 - 1
	for i, l := range lanes {
		if (l>>signBit)&1 != 0 {
			mask |= 1 << uint(i)
		}
	}
	st.pushI32(int32(mask))
	return nil
}

// cmpEq/cmpNe/cmpLt/cmpGt/cmpLe/cmpGe are the six integer relational predicates, each taking
// two lanes already sign-extended (if signed) to int64 — `v128.ml`'s own `IXX.eq`/`lt_s`/etc.,
// reduced to Go's native int64 comparison since a sign-extended lane's ordering is exactly an
// int64 comparison regardless of the lane's original width.
func cmpEq(a, b int64) bool { return a == b }
func cmpNe(a, b int64) bool { return a != b }
func cmpLt(a, b int64) bool { return a < b }
func cmpGt(a, b int64) bool { return a > b }
func cmpLe(a, b int64) bool { return a <= b }
func cmpGe(a, b int64) bool { return a >= b }

// cmpEqF/cmpNeF/cmpLtF/cmpGtF/cmpLeF/cmpGeF are the six float relational predicates over
// float64 — every f32x4 lane is converted through float32 first (see vecCompareFloat), so a
// NaN lane's IEEE "unordered, compares false to everything including itself" behavior is
// inherited from Go's own float comparison operators rather than special-cased here.
func cmpEqF(a, b float64) bool { return a == b }
func cmpNeF(a, b float64) bool { return a != b }
func cmpLtF(a, b float64) bool { return a < b }
func cmpGtF(a, b float64) bool { return a > b }
func cmpLeF(a, b float64) bool { return a <= b }
func cmpGeF(a, b float64) bool { return a >= b }

// vecCompare implements the 42 integer relational opcodes (eq/ne/lt/gt/le/ge across the four
// integer shapes, unsigned variants unsigned-compared): pop two v128 operands, compare
// corresponding lanes, and pack an all-ones-or-all-zero result per lane — `v128.ml`'s own `cmp`
// convention (`if f x y then -1 else 0`, at the lane's own width).
//
// `unsigned` values compare through their raw lane reading directly (Go's `uint64` ordering on
// a value already masked to its own width via lanesOf is exactly an unsigned N-bit comparison);
// `signed` values go through `signExtendLane` first so the comparison sees the lane's true sign.
func (in *Instance) vecCompare(st *stack, width uint64, signed bool, cmp func(a, b int64) bool) error {
	if err := st.needNum(4); err != nil {
		return err
	}
	hi2, lo2 := st.popV128()
	hi1, lo1 := st.popV128()
	lanes1 := lanesOf(hi1, lo1, width)
	lanes2 := lanesOf(hi2, lo2, width)
	result := make([]uint64, len(lanes1))
	for i := range lanes1 {
		var a, b int64
		if signed {
			a, b = signExtendLane(lanes1[i], width), signExtendLane(lanes2[i], width)
		} else {
			a, b = int64(lanes1[i]), int64(lanes2[i])
		}
		if cmp(a, b) {
			result[i] = mask(uint(width * 8))
		}
	}
	hi, lo := lanesToV128(result, width)
	st.pushV128(hi, lo)
	return nil
}

// vecCompareFloat implements the 12 float relational opcodes (eq/ne/lt/gt/le/ge across f32x4/
// f64x2). width selects f32 (4) or f64 (8); the lane's raw bits are read back through
// math.Float32frombits/Float64frombits so the comparison sees the IEEE value rather than the
// bit pattern, and f32 lanes are widened to float64 for the comparison itself (a lossless
// widening, so the ordering — including NaN's unordered behavior — is preserved exactly).
func (in *Instance) vecCompareFloat(st *stack, width uint64, cmp func(a, b float64) bool) error {
	if err := st.needNum(4); err != nil {
		return err
	}
	hi2, lo2 := st.popV128()
	hi1, lo1 := st.popV128()
	lanes1 := lanesOf(hi1, lo1, width)
	lanes2 := lanesOf(hi2, lo2, width)
	result := make([]uint64, len(lanes1))
	for i := range lanes1 {
		var a, b float64
		if width == 4 {
			a = float64(math.Float32frombits(uint32(lanes1[i])))
			b = float64(math.Float32frombits(uint32(lanes2[i])))
		} else {
			a = math.Float64frombits(lanes1[i])
			b = math.Float64frombits(lanes2[i])
		}
		if cmp(a, b) {
			result[i] = mask(uint(width * 8))
		}
	}
	hi, lo := lanesToV128(result, width)
	st.pushV128(hi, lo)
	return nil
}

// floatUnary lifts a float64-domain math function (Sqrt/Ceil/Floor/Trunc/RoundToEven) into a
// per-lane function over a raw width-byte lane, for use with vecUnaryLanes. width selects f32
// (4) or f64 (8); an f32 lane is widened losslessly to float64, the function applied, and the
// result narrowed back — a round trip that introduces no error since float32→float64 is exact
// and every one of these functions' outputs for a float32 input is itself exactly representable
// in float32 (rounding/truncation never needs more precision than the input already carries).
func floatUnary(width uint64, fn func(float64) float64) func(raw, w uint64) uint64 {
	return func(raw, _ uint64) uint64 {
		if width == 4 {
			result := fn(float64(math.Float32frombits(uint32(raw))))
			return uint64(math.Float32bits(float32(result)))
		}
		return math.Float64bits(fn(math.Float64frombits(raw)))
	}
}

// absFloatLane and negFloatLane clear/flip the sign bit directly — never through math.Abs or
// unary negation, since `fxx.ml`'s own comment states these two are bitwise even on NaN and
// this engine takes that as the authority rather than trusting an unmeasured guarantee about
// what Go's float operators do to a NaN's sign bit.
func absFloatLane(width uint64) func(raw, w uint64) uint64 {
	return func(raw, _ uint64) uint64 {
		signBit := uint64(1) << (width*8 - 1)
		return raw &^ signBit
	}
}

func negFloatLane(width uint64) func(raw, w uint64) uint64 {
	return func(raw, _ uint64) uint64 {
		signBit := uint64(1) << (width*8 - 1)
		return raw ^ signBit
	}
}

// floatMin and floatMax are v128.ml's own min/max: the equal-operands case is a bitwise
// logor/logand (not reachable through Go's math.Min/Max, which use their own equal-operands
// branch — confirmed to agree by decision 0024's own arch-dependence survey, but implemented
// here by calling math.Min/Max directly since the agreement was measured, not merely assumed).
func floatMin(a, b float64) float64 { return math.Min(a, b) }
func floatMax(a, b float64) float64 { return math.Max(a, b) }

// floatPmin and floatPmax are `v128.ml`'s own pmin/pmax — `pmin x y = if y < x then y else x`,
// `pmax x y = if x < y then y else x`. **Not symmetric and not "the smaller/larger of the two"**:
// when the comparison is false (including whenever either operand is NaN, since NaN is never
// less than anything), the result is unconditionally the *first* operand, never the second.
func floatPmin(a, b float64) float64 {
	if b < a {
		return b
	}
	return a
}

func floatPmax(a, b float64) float64 {
	if a < b {
		return b
	}
	return a
}

// vecBinaryFloat implements the 16 float binary opcodes (add/sub/mul/div/min/max/pmin/pmax
// across f32x4/f64x2): pop two v128 operands, apply fn lane-by-lane in float64 (f32 lanes
// widened losslessly, narrowed back after), and push the result.
func (in *Instance) vecBinaryFloat(st *stack, width uint64, fn func(a, b float64) float64) error {
	if err := st.needNum(4); err != nil {
		return err
	}
	hi2, lo2 := st.popV128()
	hi1, lo1 := st.popV128()
	lanes1 := lanesOf(hi1, lo1, width)
	lanes2 := lanesOf(hi2, lo2, width)
	result := make([]uint64, len(lanes1))
	for i := range lanes1 {
		if width == 4 {
			a := float64(math.Float32frombits(uint32(lanes1[i])))
			b := float64(math.Float32frombits(uint32(lanes2[i])))
			result[i] = uint64(math.Float32bits(float32(fn(a, b))))
		} else {
			a := math.Float64frombits(lanes1[i])
			b := math.Float64frombits(lanes2[i])
			result[i] = math.Float64bits(fn(a, b))
		}
	}
	hi, lo := lanesToV128(result, width)
	st.pushV128(hi, lo)
	return nil
}
