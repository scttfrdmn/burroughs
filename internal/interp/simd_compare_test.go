package interp

import "testing"

// TestSIMDCompare is #212's fifth ladder rung's second sub-batch: the 48-mnemonic VecCompare
// family (eq/ne/lt/gt/le/ge across all six shapes, unsigned variants for the three narrow
// integer shapes). Scott's own ordering: comparison needs no rounding and no signed-zero
// handling, so this batch exercises the shared per-lane plumbing across every width and
// signedness combination before the float arithmetic family's genuinely arch-risky arms
// (min/max/pmin/pmax) have to trust it.
func TestSIMDCompare(t *testing.T) {
	// eq/ne, one row per integer shape — a lane-by-lane pattern with both matches and
	// mismatches, so a reader that always answers "all equal" or "all not equal" cannot pass.
	t.Run("i8x16.eq", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (result v128)
			(i8x16.eq (v128.const i8x16 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16)
			          (v128.const i8x16 1 0 3 0 5 0 7 0 9 0 11 0 13 0 15 0))))`)
		lanes := []byte{0xff, 0, 0xff, 0, 0xff, 0, 0xff, 0, 0xff, 0, 0xff, 0, 0xff, 0, 0xff, 0}
		var hi, lo uint64
		for i := 7; i >= 0; i-- {
			lo = lo<<8 | uint64(lanes[i])
			hi = hi<<8 | uint64(lanes[8+i])
		}
		if out[0].Hi != hi || out[0].Bits != lo {
			t.Errorf("got hi=%#x lo=%#x, want hi=%#x lo=%#x", out[0].Hi, out[0].Bits, hi, lo)
		}
	})
	t.Run("i16x8.ne", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (result v128)
			(i16x8.ne (v128.const i16x8 1 2 3 4 5 6 7 8)
			          (v128.const i16x8 1 0 3 0 5 0 7 0))))`)
		// lane0: 1!=1 false, lane1: 2!=0 true, lane2: 3!=3 false, lane3: 4!=0 true (lo); the
		// same pattern repeats for lanes 4-7 (hi).
		wantLo := uint64(0xffff0000_ffff0000)
		wantHi := uint64(0xffff0000_ffff0000)
		if out[0].Hi != wantHi || out[0].Bits != wantLo {
			t.Errorf("got hi=%#x lo=%#x, want hi=%#x lo=%#x", out[0].Hi, out[0].Bits, wantHi, wantLo)
		}
	})

	// The signed/unsigned pair, pinned with a negative value read two ways — -1 is less than 0
	// signed, and 0xffffffff is not less than 0 unsigned, so the two readings genuinely diverge
	// on the identical bit pattern.
	t.Run("i32x4.lt_s vs lt_u on the same bits", func(t *testing.T) {
		src := `(v128.const i32x4 -1 0 5 0) (v128.const i32x4 0 0 3 0)`
		outS := runSIMD1(t, `(module (func (export "c") (result v128) (i32x4.lt_s `+src+`)))`)
		outU := runSIMD1(t, `(module (func (export "c") (result v128) (i32x4.lt_u `+src+`)))`)
		// signed: lane0 (-1<0) true, others false -> lo = 0xffffffff, hi = 0
		if outS[0].Hi != 0 || outS[0].Bits != 0xffffffff {
			t.Errorf("lt_s: got hi=%#x lo=%#x, want hi=0 lo=0xffffffff", outS[0].Hi, outS[0].Bits)
		}
		// unsigned: 0xffffffff < 0 is false everywhere -> both zero
		if outU[0].Hi != 0 || outU[0].Bits != 0 {
			t.Errorf("lt_u: got hi=%#x lo=%#x, want hi=0 lo=0", outU[0].Hi, outU[0].Bits)
		}
	})
	t.Run("i8x16.gt_s vs gt_u", func(t *testing.T) {
		src := `(v128.const i8x16 -1 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0) (v128.const i8x16 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0)`
		outS := runSIMD1(t, `(module (func (export "c") (result v128) (i8x16.gt_s `+src+`)))`)
		outU := runSIMD1(t, `(module (func (export "c") (result v128) (i8x16.gt_u `+src+`)))`)
		if outS[0].Bits&0xFF != 0 { // -1 > 0 signed is false
			t.Errorf("gt_s lane 0: got %#x, want 0", outS[0].Bits&0xFF)
		}
		if outU[0].Bits&0xFF != 0xFF { // 0xff > 0 unsigned is true
			t.Errorf("gt_u lane 0: got %#x, want 0xff", outU[0].Bits&0xFF)
		}
	})

	// le/ge, i16x8 — equal values must satisfy both, distinguishing them from the strict lt/gt
	// pair above.
	t.Run("i16x8.le_s, equal lanes are included", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (result v128)
			(i16x8.le_s (v128.const i16x8 5 5 5 5 5 5 5 5)
			            (v128.const i16x8 5 5 5 5 5 5 5 5))))`)
		if out[0].Hi != ^uint64(0) || out[0].Bits != ^uint64(0) {
			t.Errorf("got hi=%#x lo=%#x, want all-ones both halves", out[0].Hi, out[0].Bits)
		}
	})
	t.Run("i32x4.ge_u", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (result v128)
			(i32x4.ge_u (v128.const i32x4 5 4 5 6) (v128.const i32x4 5 4 6 5))))`)
		// lane0: 5>=5 true, lane1: 4>=4 true, lane2: 5>=6 false, lane3: 6>=5 true. lo packs
		// lanes 0,1 (both true -> all-ones); hi packs lanes 2,3 (false then true -> high half
		// of hi set, low half zero).
		wantLo := uint64(0xffffffff_ffffffff)
		wantHi := uint64(0xffffffff_00000000)
		if out[0].Hi != wantHi || out[0].Bits != wantLo {
			t.Errorf("got hi=%#x lo=%#x, want hi=%#x lo=%#x", out[0].Hi, out[0].Bits, wantHi, wantLo)
		}
	})

	// i64x2: no unsigned variants in the tracked proposal set, so only eq/ne/lt_s/gt_s/le_s/
	// ge_s exist — one row confirming the signed reading is reachable at this width too.
	t.Run("i64x2.lt_s", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (result v128)
			(i64x2.lt_s (v128.const i64x2 -1 5) (v128.const i64x2 0 3))))`)
		if out[0].Hi != 0 || out[0].Bits != ^uint64(0) {
			t.Errorf("got hi=%#x lo=%#x, want hi=0 lo=all-ones (lane 0: -1<0 true, lane 1: 5<3 false)",
				out[0].Hi, out[0].Bits)
		}
	})

	// Float compares: eq/ne/lt/gt/le/ge for both f32x4 and f64x2, plus the NaN row that is the
	// entire reason this sub-batch is judged low-risk rather than skipped as "just like integer
	// eq" — NaN must compare false to *everything* including itself, which a bit-pattern
	// equality check (rather than a genuine float comparison) would get wrong.
	t.Run("f32x4.eq", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (result v128)
			(f32x4.eq (v128.const f32x4 1 2 3 4) (v128.const f32x4 1 0 3 0))))`)
		wantLo := uint64(0x00000000_ffffffff)
		wantHi := uint64(0x00000000_ffffffff)
		if out[0].Hi != wantHi || out[0].Bits != wantLo {
			t.Errorf("got hi=%#x lo=%#x, want hi=%#x lo=%#x", out[0].Hi, out[0].Bits, wantHi, wantLo)
		}
	})
	t.Run("f32x4.eq, NaN compares false to itself", func(t *testing.T) {
		// 0x7fc00000 is a canonical quiet NaN in f32.
		out := runSIMD1(t, `(module (func (export "c") (result v128)
			(f32x4.eq (v128.const i32x4 0x7fc00000 1 1 1) (v128.const i32x4 0x7fc00000 1 1 1))))`)
		wantLo := uint64(0xffffffff_00000000) // lane 0 (NaN==NaN) false, lane 1 (1==1) true
		wantHi := uint64(0xffffffff_ffffffff)
		if out[0].Hi != wantHi || out[0].Bits != wantLo {
			t.Errorf("got hi=%#x lo=%#x, want hi=%#x lo=%#x — NaN must not equal itself",
				out[0].Hi, out[0].Bits, wantHi, wantLo)
		}
	})
	t.Run("f32x4.ne, NaN compares true (not-equal) to itself", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (result v128)
			(f32x4.ne (v128.const i32x4 0x7fc00000 1 1 1) (v128.const i32x4 0x7fc00000 1 1 1))))`)
		if out[0].Bits&0xffffffff != 0xffffffff {
			t.Errorf("got lane 0 = %#x, want all-ones — NaN != NaN is true", out[0].Bits&0xffffffff)
		}
	})
	t.Run("f64x2.lt", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (result v128)
			(f64x2.lt (v128.const f64x2 -1.5 5.0) (v128.const f64x2 0 3.0))))`)
		if out[0].Hi != 0 || out[0].Bits != ^uint64(0) {
			t.Errorf("got hi=%#x lo=%#x, want hi=0 lo=all-ones", out[0].Hi, out[0].Bits)
		}
	})
	t.Run("f64x2.ge", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (result v128)
			(f64x2.ge (v128.const f64x2 5.0 3.0) (v128.const f64x2 5.0 4.0))))`)
		if out[0].Hi != 0 || out[0].Bits != ^uint64(0) {
			t.Errorf("got hi=%#x lo=%#x, want hi=0 lo=all-ones (5>=5 true, 3>=4 false)",
				out[0].Hi, out[0].Bits)
		}
	})

	// Operand order: eval.ml's own stack shape (n2 on top, pops first) — a row whose two
	// operands are not symmetric under the comparator pins that the "first"/"second" operand
	// roles are assigned correctly rather than by luck of a commutative predicate.
	t.Run("i32x4.gt_s operand order matters", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (result v128)
			(i32x4.gt_s (v128.const i32x4 10 0 0 0) (v128.const i32x4 5 0 0 0))))`)
		if out[0].Bits&0xffffffff != 0xffffffff {
			t.Errorf("got %#x, want all-ones — first operand (10) is greater than second (5)",
				out[0].Bits&0xffffffff)
		}
	})
}
