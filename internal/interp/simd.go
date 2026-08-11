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
