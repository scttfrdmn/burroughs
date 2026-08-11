package interp

import "github.com/scttfrdmn/burroughs/internal/binary"

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
	default:
		return unsupported(ins)
	}
	return nil
}
