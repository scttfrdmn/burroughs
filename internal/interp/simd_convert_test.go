package interp

import "testing"

// TestSIMDExtendLanes is #212's VecConvert rung's own extend sub-family: the 12
// `*.extend_{low,high}_*_{s,u}` mnemonics. Every row is cited against
// simd_int_to_int_extend.wast's own vectors — `low` reads the source's *first* 8 lanes, `high`
// its *last* 8, per v128.ml's own `Lib.List.take`/`Lib.List.drop`, confirmed by reading the
// reference rather than assumed from the mnemonic's own wording (which reads identically under
// either interpretation of which half is meant).
func TestSIMDExtendLanes(t *testing.T) {
	for _, tc := range []struct {
		name           string
		mnemonic       string
		v              Value
		wantHi, wantLo uint64
	}{
		// verbatim from :102-103 (module at :6): lanes 1x8 low, 0x8 high — extend_low reads
		// only the low 8 lanes, so the output is all 1s, not a mix.
		{
			"i16x8.extend_low_i8x16_s reads only the source's low 8 lanes", "i16x8.extend_low_i8x16_s",
			v128(0, 0x0101010101010101), 0x0001000100010001, 0x0001000100010001,
		},
		// verbatim from :22-23: lanes 0x8 low, -1x8 high (0xff bytes) — extend_high reads only
		// the high 8 lanes, sign-extending 0xff to 0xffff.
		{
			"i16x8.extend_high_i8x16_s reads only the source's high 8 lanes, sign-extended", "i16x8.extend_high_i8x16_s",
			v128(0xffffffffffffffff, 0), 0xffffffffffffffff, 0xffffffffffffffff,
		},
		// verbatim from :143-144: -1 (0xff byte) in the low 8 lanes, zero-extended to 255 —
		// distinguishes _u from _s, which would sign-extend the same byte to 0xffff. All 8
		// source lanes read are -1, so all 8 output lanes are 255, filling both halves.
		{
			"i16x8.extend_low_i8x16_u zero-extends, not sign-extends", "i16x8.extend_low_i8x16_u",
			v128(0, 0xffffffffffffffff), 0x00ff00ff00ff00ff, 0x00ff00ff00ff00ff,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := `(module (func (export "c") (param $0 v128) (result v128) (` +
				tc.mnemonic + ` (local.get $0))))`
			out := runSIMD1(t, src, tc.v)
			if len(out) != 1 {
				t.Fatalf("got %d results, want 1", len(out))
			}
			if out[0].Hi != tc.wantHi || out[0].Bits != tc.wantLo {
				t.Errorf("%s(%+v) = hi=%#x lo=%#x, want hi=%#x lo=%#x",
					tc.mnemonic, tc.v, out[0].Hi, out[0].Bits, tc.wantHi, tc.wantLo)
			}
		})
	}
}

// TestSIMDExtaddPairwise pins the six `*.extadd_pairwise_*_{s,u}` mnemonics against
// simd_i16x8_extadd_pairwise_i8x16.wast's own all-(-1) row — the one row in that file where
// signed and unsigned extension of the *same* byte pattern (0xff) produce different sums
// (-2 vs. 510), so it also stands in as the signed/unsigned distinguishing case.
func TestSIMDExtaddPairwise(t *testing.T) {
	// verbatim from :15-16: sixteen -1 (0xff) i8 lanes, pairwise-summed signed -> eight -2 (0xfffe) i16 lanes.
	out := runSIMD1(t, `(module (func (export "c") (param $0 v128) (result v128)
		(i16x8.extadd_pairwise_i8x16_s (local.get $0))))`,
		v128(0xffffffffffffffff, 0xffffffffffffffff))
	if len(out) != 1 {
		t.Fatalf("got %d results, want 1", len(out))
	}
	wantHi, wantLo := uint64(0xfffefffefffefffe), uint64(0xfffefffefffefffe)
	if out[0].Hi != wantHi || out[0].Bits != wantLo {
		t.Errorf("i16x8.extadd_pairwise_i8x16_s = hi=%#x lo=%#x, want hi=%#x lo=%#x",
			out[0].Hi, out[0].Bits, wantHi, wantLo)
	}

	// The unsigned sibling on the identical input: 0xff+0xff = 0x1fe = 510, not -2 — the
	// signed/unsigned distinction this row exists to pin.
	out = runSIMD1(t, `(module (func (export "c") (param $0 v128) (result v128)
		(i16x8.extadd_pairwise_i8x16_u (local.get $0))))`,
		v128(0xffffffffffffffff, 0xffffffffffffffff))
	if len(out) != 1 {
		t.Fatalf("got %d results, want 1", len(out))
	}
	wantHi, wantLo = uint64(0x01fe01fe01fe01fe), uint64(0x01fe01fe01fe01fe)
	if out[0].Hi != wantHi || out[0].Bits != wantLo {
		t.Errorf("i16x8.extadd_pairwise_i8x16_u = hi=%#x lo=%#x, want hi=%#x lo=%#x — "+
			"unsigned 0xff+0xff must be 510 (0x1fe), not -2 the way signed extension gives",
			out[0].Hi, out[0].Bits, wantHi, wantLo)
	}
}

// TestSIMDTruncSatF32x4 pins i32x4.trunc_sat_f32x4_{s,u} against
// simd_i32x4_trunc_sat_f32x4.wast's own inf/NaN rows — the shared-authority claim
// (vecTruncSatF32x4 delegates to truncSatF64ToI32, the identical function the scalar
// f32.trunc_sat_f32_s arm uses) means these rows also confirm the scalar authority is being
// called correctly per lane, not just that some saturating logic exists.
func TestSIMDTruncSatF32x4(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mnemonic string
		v        Value
		want     uint64
	}{
		// verbatim from :92-93: +inf saturates to i32 max, all four lanes.
		{
			"trunc_sat_f32x4_s of +inf saturates to max", "i32x4.trunc_sat_f32x4_s",
			v128(0x7f8000007f800000, 0x7f8000007f800000), 0x7fffffff7fffffff,
		},
		// verbatim from :96-97: NaN truncates to 0, not a trap (this family is total).
		{
			"trunc_sat_f32x4_s of NaN is 0, not a trap", "i32x4.trunc_sat_f32x4_s",
			v128(0x7fc000007fc00000, 0x7fc000007fc00000), 0,
		},
		// verbatim from :16-17: -1.5 truncates to -1 signed. Paired with the _u row directly
		// below on the identical input — this is the row that distinguishes the two functions,
		// since a shared-authority bug that ignored the signedness argument would pass one of
		// this pair and fail the other, never both.
		{
			"trunc_sat_f32x4_s of -1.5 is -1", "i32x4.trunc_sat_f32x4_s",
			v128(0xbfc00000bfc00000, 0xbfc00000bfc00000), 0xffffffffffffffff,
		},
		// verbatim from :120-121: the identical -1.5 input, unsigned, truncates to 0 — not -1
		// and not the reinterpreted bit pattern of -1 as a u32.
		{
			"trunc_sat_f32x4_u of -1.5 is 0, not -1", "i32x4.trunc_sat_f32x4_u",
			v128(0xbfc00000bfc00000, 0xbfc00000bfc00000), 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := `(module (func (export "c") (param $0 v128) (result v128) (` +
				tc.mnemonic + ` (local.get $0))))`
			out := runSIMD1(t, src, tc.v)
			if len(out) != 1 {
				t.Fatalf("got %d results, want 1", len(out))
			}
			wantLo := tc.want
			if out[0].Hi != tc.want || out[0].Bits != wantLo {
				t.Errorf("%s(%+v) = hi=%#x lo=%#x, want hi=%#x lo=%#x",
					tc.mnemonic, tc.v, out[0].Hi, out[0].Bits, tc.want, wantLo)
			}
		})
	}
}

// TestSIMDTruncSatF64x2Zero pins i32x4.trunc_sat_f64x2_s_zero: two f64 lanes truncated into the
// *low* two i32 lanes of the result, with the high two zeroed — the shape v128.ml's own
// convert_zero states explicitly (`List.map f (F64x2.to_lanes v) @ I32.[zero; zero]`), pinned
// with distinct lane values (1.0, 2.0) so a reader that zeroed the wrong half or swapped lane
// order would produce a distinguishable wrong answer, not a coincidental match.
func TestSIMDTruncSatF64x2Zero(t *testing.T) {
	out := runSIMD1(t, `(module (func (export "c") (param $0 v128) (result v128)
		(i32x4.trunc_sat_f64x2_s_zero (local.get $0))))`,
		v128(0x4000000000000000, 0x3ff0000000000000)) // f64x2(1.0, 2.0)
	if len(out) != 1 {
		t.Fatalf("got %d results, want 1", len(out))
	}
	// lane 0 = 1, lane 1 = 2, lanes 2,3 = 0.
	wantHi, wantLo := uint64(0), uint64(0x0000000200000001)
	if out[0].Hi != wantHi || out[0].Bits != wantLo {
		t.Errorf("got hi=%#x lo=%#x, want hi=%#x lo=%#x (lanes 2,3 zeroed)",
			out[0].Hi, out[0].Bits, wantHi, wantLo)
	}
}

// TestSIMDConvertI32x4 pins f32x4.convert_i32x4_{s,u} against simd_conversions.wast's own -1
// row (:217-218), which distinguishes the two directly: -1 converts to -1.0 signed but
// 4294967295.0 unsigned.
func TestSIMDConvertI32x4(t *testing.T) {
	allOnes := v128(0xffffffffffffffff, 0xffffffffffffffff)

	out := runSIMD1(t, `(module (func (export "c") (param $0 v128) (result v128)
		(f32x4.convert_i32x4_s (local.get $0))))`, allOnes)
	if len(out) != 1 {
		t.Fatalf("got %d results, want 1", len(out))
	}
	wantLane := uint64(f32bits(-1.0))
	want := wantLane | wantLane<<32
	if out[0].Hi != want || out[0].Bits != want {
		t.Errorf("f32x4.convert_i32x4_s(-1) = hi=%#x lo=%#x, want hi=%#x lo=%#x (each lane -1.0)",
			out[0].Hi, out[0].Bits, want, want)
	}

	out = runSIMD1(t, `(module (func (export "c") (param $0 v128) (result v128)
		(f32x4.convert_i32x4_u (local.get $0))))`, allOnes)
	if len(out) != 1 {
		t.Fatalf("got %d results, want 1", len(out))
	}
	wantLaneU := uint64(f32bits(4294967295.0))
	wantU := wantLaneU | wantLaneU<<32
	if out[0].Hi != wantU || out[0].Bits != wantU {
		t.Errorf("f32x4.convert_i32x4_u(-1 as u32) = hi=%#x lo=%#x, want hi=%#x lo=%#x "+
			"(each lane 4294967295.0, not -1.0)", out[0].Hi, out[0].Bits, wantU, wantU)
	}
}

// TestSIMDDemoteAndPromote pins f32x4.demote_f64x2_zero and f64x2.promote_low_f32x4, each
// with two distinct lane values so a low/high mix-up or a zero-fill in the wrong half produces
// a distinguishable wrong answer.
func TestSIMDDemoteAndPromote(t *testing.T) {
	t.Run("f32x4.demote_f64x2_zero packs into the low two lanes, zeroing the high two", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (param $0 v128) (result v128)
			(f32x4.demote_f64x2_zero (local.get $0))))`,
			v128(f64bits(2.5), f64bits(1.5)))
		if len(out) != 1 {
			t.Fatalf("got %d results, want 1", len(out))
		}
		wantLo := uint64(f32bits(1.5)) | uint64(f32bits(2.5))<<32
		if out[0].Hi != 0 || out[0].Bits != wantLo {
			t.Errorf("got hi=%#x lo=%#x, want hi=0 lo=%#x", out[0].Hi, out[0].Bits, wantLo)
		}
	})

	t.Run("f64x2.promote_low_f32x4 reads only the operand's low two lanes", func(t *testing.T) {
		// lanes: 1.5, 2.5 (low two, read), 99.0, 99.0 (high two, must be ignored).
		v := v128(uint64(f32bits(99.0))|uint64(f32bits(99.0))<<32, uint64(f32bits(1.5))|uint64(f32bits(2.5))<<32)
		out := runSIMD1(t, `(module (func (export "c") (param $0 v128) (result v128)
			(f64x2.promote_low_f32x4 (local.get $0))))`, v)
		if len(out) != 1 {
			t.Fatalf("got %d results, want 1", len(out))
		}
		if out[0].Hi != f64bits(2.5) || out[0].Bits != f64bits(1.5) {
			t.Errorf("got hi=%#x lo=%#x, want hi=%#x lo=%#x (the high two source lanes, 99.0, "+
				"must not leak into the result)", out[0].Hi, out[0].Bits, f64bits(2.5), f64bits(1.5))
		}
	})
}
