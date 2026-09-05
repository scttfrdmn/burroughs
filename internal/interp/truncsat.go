package interp

import (
	"math"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// execFC dispatches the 0xfc region: the eight saturating truncations, plus the seven bulk and
// segment operations whose arms live in `bulk.go`.
//
// **A separate switch rather than arms in the main one, because `Op` is the sub-opcode.**
// `fc 00` and `unreachable` are both `Op == 0x00`, so a single switch would need every arm to
// test the prefix and would execute the wrong instruction the first time one forgot. The
// region's own function makes the prefix a precondition of the whole switch instead of a
// condition on each arm.
//
// Unhandled sub-opcodes fall through to `unsupported`, which renders them as `fc NN` — the
// board's existing bucket keys, unchanged, so the arms this function does not yet have stay
// visible as the work list they are. Counted rather than described: `opTableFC` has 18 entries
// and this switch now answers all 18. (The count in an earlier draft of this paragraph said
// "ten" and was never derived from the table — a number nobody ran, which is the class this
// project keeps finding.)
func (in *Instance) execFC(ins binary.Instr, st *stack) error {
	switch ins.Op {
	case 0x00, 0x01, 0x02, 0x03: // i32.trunc_sat_f{32,64}_{s,u}
		if err := st.needNum(1); err != nil {
			return err
		}
		st.pushI32(truncSatToI32(ins.Op, st))

	case 0x04, 0x05, 0x06, 0x07: // i64.trunc_sat_f{32,64}_{s,u}
		if err := st.needNum(1); err != nil {
			return err
		}
		st.pushI64(truncSatToI64(ins.Op, st))

	case 0x08: // memory.init
		return in.execMemoryInit(ins, st)

	case 0x09: // data.drop
		// No `st`: the drops pop nothing, and a stack parameter they ignore would be a
		// signature claiming an operand the instruction does not have.
		return in.execDataDrop(ins)

	case 0x0a: // memory.copy
		return in.execMemoryCopy(ins, st)

	case 0x0b: // memory.fill
		return in.execMemoryFill(ins, st)

	case 0x0c: // table.init
		return in.execTableInit(ins, st)

	case 0x0d: // elem.drop
		return in.execElemDrop(ins)

	case 0x0e: // table.copy
		return in.execTableCopy(ins, st)

	case 0x10: // table.size — `eval.ml:363-365`, and the only sub-opcode in this switch that
		// pushes rather than pops: `size` reads the slice's length back rather than keeping a
		// counter (table.go's own comment on `size`, the same rule `memory.size` follows).
		//
		// **Width follows the table's address type, `memory.size`'s rule exactly**
		// (`num_of_addr`, `eval.ml:365`) — a table64's size is an i64, gated off by default
		// (`table_size64.wast`) but live in the all-gates-on lane, which is why the split is
		// written even though the default board never takes the i64 branch.
		tab, err := in.tableFor("instruction", ins.Imm0)
		if err != nil {
			return err
		}
		// One `size()`, hoisted above the branch, for `memory.size`'s reason in exec.go: `size`
		// is an image load and the load-once control counts it as one.
		sz := tab.size()
		if tab.limits.Addr64 {
			st.pushI64(int64(sz))
		} else {
			st.pushI32(int32(uint32(sz)))
		}

	case 0x0f: // table.grow — `eval.ml:366-373`
		tab, err := in.tableFor("instruction", ins.Imm0)
		if err != nil {
			return err
		}
		if err := st.needNum(1); err != nil {
			return err
		}
		if err := st.needRef(1); err != nil {
			return err
		}
		// The two operands live in separate arrays (`num`, `refs`), so there is no shared
		// ordering to get backwards the way `callIndirect`'s Imm0/Imm1 has — popping the ref
		// before or after the delta reads the same two values either way, since each pop
		// only ever touches its own array.
		r := st.popRef()
		if tab.limits.Addr64 {
			st.pushI64(tab.grow(uint64(st.popI64()), r))
		} else {
			st.pushI32(int32(tab.grow(uint64(uint32(st.popI32())), r)))
		}

	case 0x11: // table.fill — `eval.ml:375-392`
		tab, err := in.tableFor("instruction", ins.Imm0)
		if err != nil {
			return err
		}
		if err := st.needNum(2); err != nil {
			return err
		}
		if err := st.needRef(1); err != nil {
			return err
		}
		n := tableAddr(tab, st.popNum())
		r := st.popRef()
		i := tableAddr(tab, st.popNum())
		return tab.fill(i, n, r)

	default:
		return unsupported(ins)
	}
	return nil
}

// The saturating float-to-integer truncations, `fc 00`..`fc 07`.
//
// # Total functions, and that is the whole difference from 0xa8..0xb1
//
// `truncToI32`/`truncToI64` are the same range analysis with a different verdict: where they
// trap, these clamp. So the three-way split is identical — NaN, below the low bound, above the
// high bound — and only the arms' *results* change:
//
//	                     trapping (0xa8..0xb1)      saturating (fc 00..07)
//	NaN                  invalid conversion         0
//	below the range      integer overflow           the minimum
//	at or above it       integer overflow           the maximum
//
// This is `convert.ml:97-143` and `:198-248`. The reference writes them as eight separate
// functions rather than sharing the analysis, and the arm order matters: **the NaN test comes
// first**, because every comparison against a NaN is false and a NaN reaching the range tests
// would fall through to the truncation arm and produce whatever the conversion instruction
// yields for it. Go's `int32(math.NaN())` is implementation-defined in exactly the way the
// spec is not.
//
// # Why these do not return an error
//
// A total function has no failure channel, and giving it one would be the `memory.grow` mistake
// (see 0x40): the right answer reported on the wrong channel. `conversions.wast` asserts these
// as `assert_return` throughout — `i32.trunc_sat_f32_s(nan)` is **0**, not a trap — so an arm
// returning a trap here would convert passing vectors into `assert_trap` answers. The signature
// is the semantics.
//
// # The unsigned upper bound is not the signed one doubled by accident
//
// `-.Int32.(to_float min_int) *. 2.0` is 2^32, written that way in the reference because it
// derives the bound from `min_int` rather than naming it. Spelled here as `1<<32` for the same
// value.
//
// The reference's *lower* unsigned bound is `<= -1.0` where this reads `< 0`, and the two agree
// because the comparison here is on the **truncated** value: everything in (-1, 0) truncates to
// -0.0, so `<= -1.0` before truncation and `< 0` after select the same inputs.
//
// **A first draft of this paragraph justified the arm with `f32.const -0.5` and that was
// measured false.** Go's `math.Trunc(-0.5)` is negative zero, `-0.0 < 0` is false, and
// `uint32(-0.0)` is 0 — so the (-1, 0) vectors (`conversions.wast:299`, `:300`) answer 0 with
// this arm **deleted entirely** and cannot witness it. The discriminating vector is `-1.0`
// (`:302`), which without the arm reaches `int32(uint32(-1.0))` and answers -1 where the suite
// wants 0. Printed, not reasoned: the draft's claim was about a boundary Go places differently
// than the reference does, and the row it named was on the wrong side of it.

// truncSatToI32 is `fc 00`..`fc 03` — i32.trunc_sat_f{32,64}_{s,u}.
//
// The float is widened to float64 before truncation for `truncToI32`'s reason: the widening is
// lossless and `math.Trunc` is exact, so one analysis serves both input widths. 2^31 and 2^32
// are both exactly representable, which is what lets the bounds be written as shifts.
func truncSatToI32(op uint32, st *stack) int32 {
	var d float64
	if op == 0x00 || op == 0x01 {
		d = float64(st.popF32())
	} else {
		d = st.popF64()
	}
	signed := op == 0x00 || op == 0x02
	return truncSatF64ToI32(d, signed)
}

// truncSatF64ToI32 is `truncSatToI32`'s own analysis, factored out to a pure function of the
// float and the signedness so `i32x4.trunc_sat_f32x4_s`/`_u` and `i32x4.trunc_sat_f64x2_s_zero`/
// `_u_zero` (`internal/interp/simd.go`) share this one authority per lane rather than a second
// copy of the range analysis — the shared-authority rule `truncSatToI32`'s own doc comment
// already states for the scalar family, extended to its per-lane callers.
func truncSatF64ToI32(d float64, signed bool) int32 {
	// NaN first: see truncSatToI32's own block comment. A NaN fails every comparison below, so
	// an arm order that tested the range first would reach the truncation with a NaN in hand.
	if d != d {
		return 0
	}
	d = math.Trunc(d)
	if signed {
		if d < -(1 << 31) {
			return math.MinInt32
		}
		if d >= 1<<31 {
			return math.MaxInt32
		}
		return int32(d)
	}
	// Unsigned. The low arm is on the *truncated* value, so -0.5 arrives as -0.0 and yields 0
	// rather than saturating — `<= -1.0` in the reference, which after truncation is `< 0`.
	if d < 0 {
		return 0
	}
	if d >= 1<<32 {
		return -1 // all ones in the slot, which is the reference's `-1l`; pushI32 zero-extends
	}
	return int32(uint32(d))
}

// truncSatToI64 is `fc 04`..`fc 07` — i64.trunc_sat_f{32,64}_{s,u}.
//
// **The 64-bit unsigned case is the one that cannot be done in float64 arithmetic alone**, and
// the reference says so by having an extra arm the 32-bit version does not: `convert.ml:219`
// splits at 2^63 and computes `(xf - 0x1p63) XOR min_int` above it. The reason is that `int64(d)`
// for d ≥ 2^63 is out of range for a signed 64-bit conversion, which Go leaves
// implementation-defined.
//
// **What it actually does here was printed rather than assumed**, because a draft of this comment
// asserted it yields 0x8000000000000000 — the sign bit — and it does not: on this arm64 host
// `int64(9223372036854775808.0)` is `9223372036854775807`, saturating to **max**, and so is
// `int64(1e19)`. Same defect either way and worse in one respect, since a saturating wrong answer
// looks plausible: every value in [2^63, 2^64) would answer `0x7fffffffffffffff`, and
// `conversions.wast:443` wants `-9223372036854775808` while `:438` wants `-2048`. The specific
// wrong value is not portable, which is the whole reason the conversion is avoided; naming one as
// though it were the rule would have been an invented-evidence grave (#36) in a comment.
//
// Go can express the split more directly than OCaml can: `uint64(d)` is well-defined for
// d in [0, 2^64), so the subtract-and-xor dance is unnecessary *provided* the range check
// precedes it. That is a deliberate divergence from the reference's shape, not an oversight —
// it computes the same function, and the comment exists because "the Go version is shorter than
// the OCaml" is exactly the shape a silent conversion bug hides in.
func truncSatToI64(op uint32, st *stack) int64 {
	var d float64
	if op == 0x04 || op == 0x05 {
		d = float64(st.popF32())
	} else {
		d = st.popF64()
	}
	if d != d {
		return 0
	}
	d = math.Trunc(d)
	if op == 0x04 || op == 0x06 { // signed
		if d < -(1 << 63) {
			return math.MinInt64
		}
		if d >= 1<<63 {
			return math.MaxInt64
		}
		return int64(d)
	}
	if d < 0 {
		return 0
	}
	if d >= 1<<64 {
		return -1 // all ones, the reference's `-1L`
	}
	// Well-defined for the whole remaining range, unlike `int64(d)` above 2^63 — see the doc
	// comment on why this does not reproduce the reference's subtract-and-xor.
	return int64(uint64(d))
}
