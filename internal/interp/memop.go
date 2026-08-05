package interp

// memop describes one load or store: how many bytes it touches, whether a narrow load
// sign-extends, and which value type the slot holds.
//
// **A table rather than 23 switch arms, because the facts are exactly what a hand-written
// switch gets wrong silently.** `i64_load16_s` is two bytes, sign-extended, into an i64 slot;
// getting any one of those three wrong produces a plausible value for a legal module, which is
// an accept-direction defect the suite scores green by construction (§9 G-3). The rows are
// machine-checked against the generated opcode table's mnemonics by
// TestMemopTableAgreesWithMnemonics, which parses `i64_load16_s` into (8-byte type, 2-byte
// access, signed) and compares — so the table is *derived* from an authority that already has a
// conformance record rather than transcribed from the spec by hand.
type memop struct {
	// width is the number of bytes the access touches in memory: 1, 2, 4, or 8.
	width uint64

	// signed is whether a narrow *load* sign-extends. Meaningless for stores, which
	// truncate, and for full-width accesses.
	signed bool

	// is64 is whether the stack slot is 64 bits (i64/f64) rather than 32 (i32/f32).
	is64 bool

	// isFloat is whether the slot is a float. It changes nothing about the bytes — the
	// engine moves float bits verbatim, never through a Go float, so a signalling NaN's
	// payload survives — and is retained because the *slot* type is what pushI64 versus
	// pushF64 select, and because the table's cross-check needs to know.
	isFloat bool
}

// memops is the load/store family, 0x28-0x3e.
//
// Loads first (0x28-0x35), then stores (0x36-0x3e), in the reference's own order
// (`decode.ml:429-452`). Absent keys are not part of the family and fall to the switch's
// default, which reports the engine gap rather than a wrong answer.
var memops = map[uint32]memop{
	// loads
	0x28: {width: 4},                            // i32.load
	0x29: {width: 8, is64: true},                // i64.load
	0x2a: {width: 4, isFloat: true},             // f32.load
	0x2b: {width: 8, is64: true, isFloat: true}, // f64.load
	0x2c: {width: 1, signed: true},              // i32.load8_s
	0x2d: {width: 1},                            // i32.load8_u
	0x2e: {width: 2, signed: true},              // i32.load16_s
	0x2f: {width: 2},                            // i32.load16_u
	0x30: {width: 1, signed: true, is64: true},  // i64.load8_s
	0x31: {width: 1, is64: true},                // i64.load8_u
	0x32: {width: 2, signed: true, is64: true},  // i64.load16_s
	0x33: {width: 2, is64: true},                // i64.load16_u
	0x34: {width: 4, signed: true, is64: true},  // i64.load32_s
	0x35: {width: 4, is64: true},                // i64.load32_u

	// stores
	0x36: {width: 4},                            // i32.store
	0x37: {width: 8, is64: true},                // i64.store
	0x38: {width: 4, isFloat: true},             // f32.store
	0x39: {width: 8, is64: true, isFloat: true}, // f64.store
	0x3a: {width: 1},                            // i32.store8
	0x3b: {width: 2},                            // i32.store16
	0x3c: {width: 1, is64: true},                // i64.store8
	0x3d: {width: 2, is64: true},                // i64.store16
	0x3e: {width: 4, is64: true},                // i64.store32
}

// isStore reports whether an opcode in the family stores rather than loads.
//
// Derived from the range boundary the reference itself uses rather than listed, so an opcode
// added to `memops` cannot be classified by omission.
func isStore(op uint32) bool { return op >= 0x36 && op <= 0x3e }

// loadValue reads width bytes little-endian and returns them as a slot.
//
// **Little-endian is the format's, not the host's**, and it is spelled out byte by byte rather
// than taken from encoding/binary so that the engine's answer does not depend on the machine it
// runs on. A big-endian host reading through unsafe would produce byte-swapped values for every
// vector, which dual-platform CI would catch only if one of its arches were big-endian — and
// neither is.
func loadValue(bs []byte, m memop) uint64 {
	var raw uint64
	for i := len(bs) - 1; i >= 0; i-- {
		raw = raw<<8 | uint64(bs[i])
	}
	// **No float branch, and its absence was verified rather than assumed.** `pushF32` stores
	// `uint64(math.Float32bits(v))` and `pushF64` stores `math.Float64bits(v)`, so a float
	// slot *is* the little-endian bits zero-extended — exactly what the loop above produced.
	// A branch reinterpreting through a Go float would round-trip a signalling NaN's payload
	// through a quiet one, which the suite asserts exact and which no arithmetic vector would
	// reveal. `isFloat` therefore exists for the mnemonic cross-check, not for this function.
	if m.signed {
		// Sign-extend from the access width, then narrow to the slot width. The two steps
		// differ for i32.load8_s: extending to 64 bits and truncating to 32 is what makes
		// `i32.load8_s` of 0xFF read 0xFFFFFFFF rather than 0xFFFFFFFFFFFFFFFF, and the i32
		// slot is defined as the low 32 bits with the high bits *zero* (exec.go's i32.const
		// grave).
		shift := 64 - m.width*8
		v := uint64(int64(raw<<shift) >> shift)
		if !m.is64 {
			return uint64(uint32(v))
		}
		return v
	}
	return raw
}

// storeBytes renders a slot as width little-endian bytes, truncating.
//
// Truncation is the spec's: `i32.store8` writes the low byte and discards the rest, with no
// range check — a store is not a conversion.
func storeBytes(v, width uint64) []byte {
	bs := make([]byte, width)
	for i := range bs {
		bs[i] = byte(v >> (8 * uint(i)))
	}
	return bs
}
