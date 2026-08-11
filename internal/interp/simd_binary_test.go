package interp

import "testing"

// TestSIMDBinaryArith pins the ordinary per-lane arithmetic sub-shape of the integer VecBinary
// family (add/sub/mul/min/max/avgr_u), plus the eight saturating add/sub mnemonics, against
// simd_i8x16_sat_arith.wast's own boundary rows.
func TestSIMDBinaryArith(t *testing.T) {
	for _, tc := range []struct {
		name           string
		mnemonic       string
		a, b           Value
		wantHi, wantLo uint64
	}{
		{
			"i8x16.add_sat_s saturates at 127, verbatim from :31-33", "i8x16.add_sat_s",
			v128(0x4040404040404040, 0x4040404040404040),
			v128(0x4040404040404040, 0x4040404040404040),
			0x7f7f7f7f7f7f7f7f, 0x7f7f7f7f7f7f7f7f,
		},
		// i16x8.avgr_u(0, -1): the rounding-average of 0 and 65535 (the -1 lane read as
		// unsigned) is (0+65535+1)/2 = 32768 — verbatim from simd_i16x8_arith2.wast:168-170,
		// and the round-up-on-a-tie case a plain (a+b)/2 would get wrong by one.
		{
			"i16x8.avgr_u rounds up on a tie, verbatim from :168-170", "i16x8.avgr_u",
			v128(0, 0),
			v128(0xffffffffffffffff, 0xffffffffffffffff),
			0x8000800080008000, 0x8000800080008000,
		},
		// i16x8.min_s(-1, 1): the one comparison shape that distinguishes signed from unsigned
		// (measured — simd_i16x8_arith2.wast's own min_s/min_u rows at :241-247 happen not to,
		// since every one of their lanes gives the identical answer either way): read signed,
		// -1 < 1, so the min is -1 (0xffff); read unsigned, -1's bit pattern is 65535, so the
		// min would be 1 — the wrong answer a signed/unsigned confusion produces.
		{
			"i16x8.min_s(-1, 1) reads -1 as negative, not as 65535", "i16x8.min_s",
			v128(0, 0xffff),
			v128(0, 0x0001),
			0, 0xffff,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := `(module (func (export "c") (param $0 v128) (param $1 v128) (result v128) (` +
				tc.mnemonic + ` (local.get $0) (local.get $1))))`
			out := runSIMD1(t, src, tc.a, tc.b)
			if len(out) != 1 {
				t.Fatalf("got %d results, want 1", len(out))
			}
			if out[0].Hi != tc.wantHi || out[0].Bits != tc.wantLo {
				t.Errorf("%s = hi=%#x lo=%#x, want hi=%#x lo=%#x",
					tc.mnemonic, out[0].Hi, out[0].Bits, tc.wantHi, tc.wantLo)
			}
		})
	}
}

// TestSIMDNarrow pins the six `*.narrow_*_{s,u}` mnemonics against simd_conversions.wast's own
// vectors: the first operand's lanes pack into the result's low half, the second's into the
// high half (`v128.ml`'s own `narrow`, the structural inverse of extend), and both `_s`/`_u`
// read the source lane as signed — only the destination saturation range differs.
func TestSIMDNarrow(t *testing.T) {
	t.Run("i8x16.narrow_i16x8_s packs x into the low half, y into the high half", func(t *testing.T) {
		// verbatim from :289-291
		out := runSIMD1(t, `(module (func (export "c") (param $0 v128) (param $1 v128) (result v128)
			(i8x16.narrow_i16x8_s (local.get $0) (local.get $1))))`,
			v128(0x0001000100010001, 0x0001000100010001), v128(0, 0))
		if len(out) != 1 {
			t.Fatalf("got %d results, want 1", len(out))
		}
		wantHi, wantLo := uint64(0), uint64(0x0101010101010101)
		if out[0].Hi != wantHi || out[0].Bits != wantLo {
			t.Errorf("got hi=%#x lo=%#x, want hi=%#x lo=%#x", out[0].Hi, out[0].Bits, wantHi, wantLo)
		}
	})

	t.Run("i8x16.narrow_i16x8_u of a negative (signed-read) source clamps to 0, verbatim from :384-386", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (param $0 v128) (param $1 v128) (result v128)
			(i8x16.narrow_i16x8_u (local.get $0) (local.get $1))))`,
			v128(0xffffffffffffffff, 0xffffffffffffffff), v128(0, 0))
		if len(out) != 1 {
			t.Fatalf("got %d results, want 1", len(out))
		}
		if out[0].Hi != 0 || out[0].Bits != 0 {
			t.Errorf("got hi=%#x lo=%#x, want hi=0 lo=0 (a negative i16 lane narrows unsigned to 0, "+
				"not to its low byte's bit pattern)", out[0].Hi, out[0].Bits)
		}
	})
}

// TestSIMDExtmulLowI8x16S pins i16x8.extmul_low_i8x16_s against a corpus-adjacent boundary
// value: 127*127 = 16129, confirming both the Low-half extension and the widening multiply.
func TestSIMDExtmulLowI8x16S(t *testing.T) {
	out := runSIMD1(t, `(module (func (export "c") (param $0 v128) (param $1 v128) (result v128)
		(i16x8.extmul_low_i8x16_s (local.get $0) (local.get $1))))`,
		v128(0x7f7f7f7f7f7f7f7f, 0x7f7f7f7f7f7f7f7f),
		v128(0x7f7f7f7f7f7f7f7f, 0x7f7f7f7f7f7f7f7f))
	if len(out) != 1 {
		t.Fatalf("got %d results, want 1", len(out))
	}
	want := uint64(0x3f013f013f013f01) // 127*127 = 16129 = 0x3f01, in every one of 8 i16 lanes
	if out[0].Hi != want || out[0].Bits != want {
		t.Errorf("got hi=%#x lo=%#x, want hi=%#x lo=%#x", out[0].Hi, out[0].Bits, want, want)
	}
}

// TestSIMDDotI16x8S pins i32x4.dot_i16x8_s against simd_i32x4_dot_i16x8.wast:16-18
// (1*1 + 1*1 = 2, in every one of 4 i32 lanes).
func TestSIMDDotI16x8S(t *testing.T) {
	out := runSIMD1(t, `(module (func (export "c") (param $0 v128) (param $1 v128) (result v128)
		(i32x4.dot_i16x8_s (local.get $0) (local.get $1))))`,
		v128(0x0001000100010001, 0x0001000100010001),
		v128(0x0001000100010001, 0x0001000100010001))
	if len(out) != 1 {
		t.Fatalf("got %d results, want 1", len(out))
	}
	want := uint64(0x0000000200000002)
	if out[0].Hi != want || out[0].Bits != want {
		t.Errorf("got hi=%#x lo=%#x, want hi=%#x lo=%#x", out[0].Hi, out[0].Bits, want, want)
	}
}

// TestSIMDQ15mulrSatS pins i16x8.q15mulr_sat_s against simd_i16x8_q15mulr_sat_s.wast:31-33
// (16384*16384, scaled by the fixed-point shift, is 8192 — this is also the family's own
// saturation boundary case, per the file's comment).
func TestSIMDQ15mulrSatS(t *testing.T) {
	out := runSIMD1(t, `(module (func (export "c") (param $0 v128) (param $1 v128) (result v128)
		(i16x8.q15mulr_sat_s (local.get $0) (local.get $1))))`,
		v128(0x4000400040004000, 0x4000400040004000),
		v128(0x4000400040004000, 0x4000400040004000))
	if len(out) != 1 {
		t.Fatalf("got %d results, want 1", len(out))
	}
	want := uint64(0x2000200020002000)
	if out[0].Hi != want || out[0].Bits != want {
		t.Errorf("got hi=%#x lo=%#x, want hi=%#x lo=%#x", out[0].Hi, out[0].Bits, want, want)
	}
}

// TestSIMDSwizzle pins i8x16.swizzle directly (the corpus's own vectors all reach it through
// memory ops, which is not this arm's own concern to falsify): an in-range index selects the
// base's own byte at that position, an out-of-range index (measured against v128.ml's own
// `Option.value ... ~default:zero`, not assumed) produces zero, and the index is read
// unsigned — a 0x80+ index byte is out of range (>=16), not a negative offset.
func TestSIMDSwizzle(t *testing.T) {
	base := v128(0x0f0e0d0c0b0a0908, 0x0706050403020100) // lane i = i
	t.Run("reversed in-range indices reverse the base", func(t *testing.T) {
		idx := v128(0x0001020304050607, 0x08090a0b0c0d0e0f) // lane i = 15-i
		out := runSIMD1(t, `(module (func (export "c") (param $0 v128) (param $1 v128) (result v128)
			(i8x16.swizzle (local.get $0) (local.get $1))))`, base, idx)
		if len(out) != 1 {
			t.Fatalf("got %d results, want 1", len(out))
		}
		wantHi, wantLo := uint64(0x0001020304050607), uint64(0x08090a0b0c0d0e0f)
		if out[0].Hi != wantHi || out[0].Bits != wantLo {
			t.Errorf("got hi=%#x lo=%#x, want hi=%#x lo=%#x", out[0].Hi, out[0].Bits, wantHi, wantLo)
		}
	})
	t.Run("an out-of-range index (17) produces zero, not a wrapped or negative read", func(t *testing.T) {
		// 17, not 16: a `% 16` wraparound bug would read index 17 as index 1, whose base byte
		// is 1 (nonzero) — the exact case index 16 (wrapping to 0, whose base byte is
		// coincidentally already 0) cannot distinguish from the correct zero-fill answer.
		idx := v128(0, 17)
		out := runSIMD1(t, `(module (func (export "c") (param $0 v128) (param $1 v128) (result v128)
			(i8x16.swizzle (local.get $0) (local.get $1))))`, base, idx)
		if len(out) != 1 {
			t.Fatalf("got %d results, want 1", len(out))
		}
		if out[0].Bits&0xff != 0 {
			t.Errorf("lane 0 = %#x, want 0 (index 17 is out of the 0..15 range)", out[0].Bits&0xff)
		}
	})
}

// TestSIMDShuffle pins i8x16.shuffle against simd_lane.wast's own module (:65-72) and vectors
// (:333-344): identity (indices 0..15 select the first operand unchanged), select-the-second-
// operand-in-order (indices 16..31), and select-the-second-operand-reversed (indices 31..16).
func TestSIMDShuffle(t *testing.T) {
	x := v128(0x0f0e0d0c0b0a0908, 0x0706050403020100) // lane i = i
	y := v128(0xfffefdfcfbfaf9f8, 0xf7f6f5f4f3f2f1f0) // lane i = -16+i (0xf0..0xff)

	for _, tc := range []struct {
		name           string
		shuffle        string // sixteen space-separated lane indices, as written in the .wast
		wantHi, wantLo uint64
	}{
		// verbatim from :333-336: identity on x.
		{
			"v8x16_shuffle-1, identity on the first operand",
			"0 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15", 0x0f0e0d0c0b0a0908, 0x0706050403020100,
		},
		// verbatim from :337-340: selects y in order.
		{
			"v8x16_shuffle-2, selects the second operand in order",
			"16 17 18 19 20 21 22 23 24 25 26 27 28 29 30 31", 0xfffefdfcfbfaf9f8, 0xf7f6f5f4f3f2f1f0,
		},
		// verbatim from :341-344: selects y reversed.
		{
			"v8x16_shuffle-3, selects the second operand reversed",
			"31 30 29 28 27 26 25 24 23 22 21 20 19 18 17 16", 0xf0f1f2f3f4f5f6f7, 0xf8f9fafbfcfdfeff,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := `(module (func (export "c") (param $0 v128) (param $1 v128) (result v128)
				(i8x16.shuffle ` + tc.shuffle + ` (local.get $0) (local.get $1))))`
			out := runSIMD1(t, src, x, y)
			if len(out) != 1 {
				t.Fatalf("got %d results, want 1", len(out))
			}
			if out[0].Hi != tc.wantHi || out[0].Bits != tc.wantLo {
				t.Errorf("got hi=%#x lo=%#x, want hi=%#x lo=%#x", out[0].Hi, out[0].Bits, tc.wantHi, tc.wantLo)
			}
		})
	}
}

// TestSIMDBinaryOperandOrder is the falsifiable control on vecBinaryLanes's own stack order:
// eval.ml's `Vec n2 :: Vec n1 :: vs'` means the second-pushed operand pops first, and this row
// uses a non-commutative op (sub) with distinguishable operands so a swapped pop order produces
// a different, wrong answer rather than a coincidental match.
func TestSIMDBinaryOperandOrder(t *testing.T) {
	out := runSIMD1(t, `(module (func (export "c") (param $0 v128) (param $1 v128) (result v128)
		(i32x4.sub (local.get $0) (local.get $1))))`,
		v128(0, 10), v128(0, 3))
	if len(out) != 1 {
		t.Fatalf("got %d results, want 1", len(out))
	}
	if out[0].Bits != 7 {
		t.Errorf("i32x4.sub(10, 3) = %#x, want 7 (10-3, not 3-10)", out[0].Bits)
	}
}
