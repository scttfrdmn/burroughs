package interp

import (
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// TestSIMDShiftFamily is #212's VecShift rung: the 12 shl/shr_s/shr_u mnemonics across
// i8x16/i16x8/i32x4/i64x2. Every row round-trips the vector operand through a local (matching
// simd_bit_shift.wast's own module shape) and the shift count through a second param, since
// `eval.ml`'s own stack order pops the count first — a reader that swapped the pop order would
// shift by the vector's own low bits reinterpreted as a count, not by the count itself, which is
// exactly the class of bug a same-value-for-both-operands row could not catch.
func TestSIMDShiftFamily(t *testing.T) {
	for _, tc := range []struct {
		name           string
		mnemonic       string
		v              Value
		count          int32
		wantHi, wantLo uint64
	}{
		// i8x16.shl: verbatim from simd_bit_shift.wast's own i8x16.shl_1 row (:181-182) — each
		// byte lane shifted left by 1, low bit of each lane cleared.
		{
			"i8x16.shl by 1", "i8x16.shl",
			v128(0x0f0e0d0c0b0a0908, 0x0706050403020100), 1,
			0x1e1c1a1816141210, 0x0e0c0a0806040200,
		},
		// i8x16.shr_u by 8: verbatim from :183-184 — shifted by a multiple of the lane's own
		// bit width must be a no-op (mod-8 wrap of the count), not a full clear the way an
		// unmasked Go `>>8` on a byte-sized value would read.
		{
			"i8x16.shr_u by 8 is a no-op (mod-width wrap)", "i8x16.shr_u",
			v128(0x0f0e0d0c0b0a0908, 0x0706050403020100), 8,
			0x0f0e0d0c0b0a0908, 0x0706050403020100,
		},
		// i8x16.shr_s by 9: verbatim from :185-186 — masks to a shift of 1, and the result's
		// sign-extension (each output lane pairing up two input lanes' low bit into the next
		// lane's answer) is exactly what a signed shift-by-1 on byte lanes produces.
		{
			"i8x16.shr_s by 9 masks to a shift of 1", "i8x16.shr_s",
			v128(0x0f0e0d0c0b0a0908, 0x0706050403020100), 9,
			0x0707060605050404, 0x0303020201010000,
		},
		// i8x16.shr_s of two negative lanes (-128, -64): verbatim from :136-138 — the only row
		// in this table whose input has its sign bit set, so it is the one row a signed/unsigned
		// swap actually distinguishes: shr_u on the same input would leave the high bits of
		// lanes 0 and 1 zero-filled (0x40, 0x20) rather than sign-filled (0xC0, 0xE0).
		{
			"i8x16.shr_s of negative lanes sign-extends", "i8x16.shr_s",
			v128(0x0d0c0b0a09080706, 0x050403020100c080), 1,
			0x0606050504040303, 0x020201010000e0c0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := `(module (func (export "c") (param $0 v128) (param $1 i32) (result v128) (` +
				tc.mnemonic + ` (local.get $0) (local.get $1))))`
			out := runSIMD1(t, src, tc.v, Value{Type: binary.I32, Bits: uint64(uint32(tc.count))})
			if len(out) != 1 {
				t.Fatalf("got %d results, want 1", len(out))
			}
			if out[0].Hi != tc.wantHi || out[0].Bits != tc.wantLo {
				t.Errorf("%s(%+v, %d) = hi=%#x lo=%#x, want hi=%#x lo=%#x",
					tc.mnemonic, tc.v, tc.count, out[0].Hi, out[0].Bits, tc.wantHi, tc.wantLo)
			}
		})
	}
}

// TestSIMDShiftEveryWidth pins one shl row per width (i8x16 already covered above), since
// vecShiftLanes's mask (`width*8 - 1`) and lane packing are parameterized by width and a defect
// specific to one width would not be caught by testing only i8x16 — the scope-controls-to-the-
// space discipline pointed at this family's own four widths.
func TestSIMDShiftEveryWidth(t *testing.T) {
	for _, tc := range []struct {
		name           string
		mnemonic       string
		v              Value
		count          int32
		wantHi, wantLo uint64
	}{
		// i16x8.shl_1: verbatim from simd_bit_shift.wast:339-340.
		{
			"i16x8.shl by 1", "i16x8.shl",
			v128(0x0007000600050004, 0x0003000200010000), 1,
			0x000e000c000a0008, 0x0006000400020000,
		},
		// i32x4.shl_1: verbatim from :497-498.
		{
			"i32x4.shl by 1", "i32x4.shl",
			v128(0x0000000f0000000e, 0x0000000100000000), 1,
			0x0000001e0000001c, 0x0000000200000000,
		},
		// i64x2.shl_1: verbatim from :649-650.
		{
			"i64x2.shl by 1", "i64x2.shl",
			v128(0x000000000000000f, 0x0000000000000001), 1,
			0x000000000000001e, 0x0000000000000002,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := `(module (func (export "c") (param $0 v128) (param $1 i32) (result v128) (` +
				tc.mnemonic + ` (local.get $0) (local.get $1))))`
			out := runSIMD1(t, src, tc.v, Value{Type: binary.I32, Bits: uint64(uint32(tc.count))})
			if len(out) != 1 {
				t.Fatalf("got %d results, want 1", len(out))
			}
			if out[0].Hi != tc.wantHi || out[0].Bits != tc.wantLo {
				t.Errorf("%s(%+v, %d) = hi=%#x lo=%#x, want hi=%#x lo=%#x",
					tc.mnemonic, tc.v, tc.count, out[0].Hi, out[0].Bits, tc.wantHi, tc.wantLo)
			}
		})
	}
}

// TestSIMDShiftCountPopsFirst is the falsifiable control on vecShiftLanes's own pop order: the
// i32 shift count must be popped before the v128 operand (`eval.ml`'s `Num s :: Vec v :: vs'`),
// and this row uses a vector and a count that are each individually distinguishable in the
// output, so a reader that swapped the pop order (reading the count from the vector's own low
// bits) would produce a different, wrong answer rather than coincidentally the same one.
func TestSIMDShiftCountPopsFirst(t *testing.T) {
	// v's low 32 bits are 0xdeadbeef; if the pop order were swapped, "the count" would be read as
	// int32(0xdeadbeef) = -559038737, whose low 6 bits (masked to i64x2's own width-1 = 63) name a
	// shift very different from the real count (1) — so a swapped-order reader fails loudly rather
	// than by a coincidental match.
	out := runSIMD1(t,
		`(module (func (export "c") (param $0 v128) (param $1 i32) (result v128)
			(i64x2.shl (local.get $0) (local.get $1))))`,
		v128(0, 0xdeadbeef), Value{Type: binary.I32, Bits: 1})
	if len(out) != 1 {
		t.Fatalf("got %d results, want 1", len(out))
	}
	wantLo := uint64(0xdeadbeef) << 1
	if out[0].Hi != 0 || out[0].Bits != wantLo {
		t.Errorf("got hi=%#x lo=%#x, want hi=0 lo=%#x — the shift count (1) must pop before "+
			"the vector operand, not be read from the vector's own bits", out[0].Hi, out[0].Bits, wantLo)
	}
}
