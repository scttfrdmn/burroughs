package interp

import "testing"

// TestSIMDBulkIntegerFirstBatch is #212's fifth ladder rung's own opening slice: the bulk
// per-lane family's integer-only, arch-safe mnemonics (`abs`/`neg`/`popcnt`, `all_true`,
// `bitmask` — 17 across the four integer shapes) landed before any floating-point arm, per
// #212's own risk ordering (no signed-zero/NaN-propagation cases in this sub-batch at all).
func TestSIMDBulkIntegerFirstBatch(t *testing.T) {
	// abs: -128 (i8's own minimum) must stay -128, not overflow into a wider type — the one
	// value that distinguishes correct two's-complement abs from an abs that promotes first.
	t.Run("i8x16.abs, including the width's own minimum", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (result v128)
			(i8x16.abs (v128.const i8x16
				-1 -128 5 0 0 0 0 0 0 0 0 0 0 0 0 0))))`)
		lanes := []byte{1, 0x80, 5, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
		var hi, lo uint64
		for i := 7; i >= 0; i-- {
			lo = lo<<8 | uint64(lanes[i])
			hi = hi<<8 | uint64(lanes[8+i])
		}
		if out[0].Hi != hi || out[0].Bits != lo {
			t.Errorf("got hi=%#x lo=%#x, want hi=%#x lo=%#x (lane 1, -128, stays -128 under abs)",
				out[0].Hi, out[0].Bits, hi, lo)
		}
	})
	t.Run("i16x8.abs", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (result v128)
			(i16x8.abs (v128.const i16x8 -5 5 0 0 0 0 0 0))))`)
		wantLo := uint64(0x0005_0005)
		if out[0].Hi != 0 || out[0].Bits != wantLo {
			t.Errorf("got hi=%#x lo=%#x, want hi=0 lo=%#x", out[0].Hi, out[0].Bits, wantLo)
		}
	})
	t.Run("i32x4.abs", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (result v128)
			(i32x4.abs (v128.const i32x4 -7 7 0 0))))`)
		wantLo := uint64(0x00000007_00000007)
		if out[0].Hi != 0 || out[0].Bits != wantLo {
			t.Errorf("got hi=%#x lo=%#x, want hi=0 lo=%#x", out[0].Hi, out[0].Bits, wantLo)
		}
	})
	t.Run("i64x2.abs", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (result v128)
			(i64x2.abs (v128.const i64x2 -9 9))))`)
		if out[0].Hi != 9 || out[0].Bits != 9 {
			t.Errorf("got hi=%#x lo=%#x, want hi=9 lo=9", out[0].Hi, out[0].Bits)
		}
	})

	t.Run("i8x16.neg", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (result v128)
			(i8x16.neg (v128.const i8x16 1 -1 0 0 0 0 0 0 0 0 0 0 0 0 0 0))))`)
		lanes := []byte{0xff, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
		var hi, lo uint64
		for i := 7; i >= 0; i-- {
			lo = lo<<8 | uint64(lanes[i])
			hi = hi<<8 | uint64(lanes[8+i])
		}
		if out[0].Hi != hi || out[0].Bits != lo {
			t.Errorf("got hi=%#x lo=%#x, want hi=%#x lo=%#x", out[0].Hi, out[0].Bits, hi, lo)
		}
	})

	// popcnt is i8x16's own mnemonic; no wider shape has one in the tracked proposal set.
	t.Run("i8x16.popcnt", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (result v128)
			(i8x16.popcnt (v128.const i8x16
				0xff 0x0f 0x01 0x00 0 0 0 0 0 0 0 0 0 0 0 0))))`)
		lanes := []byte{8, 4, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
		var hi, lo uint64
		for i := 7; i >= 0; i-- {
			lo = lo<<8 | uint64(lanes[i])
			hi = hi<<8 | uint64(lanes[8+i])
		}
		if out[0].Hi != hi || out[0].Bits != lo {
			t.Errorf("got hi=%#x lo=%#x, want hi=%#x lo=%#x", out[0].Hi, out[0].Bits, hi, lo)
		}
	})

	// all_true, both directions — one row per shape width, each pinning both the true and
	// false case so a reader that always answers one value cannot pass by coincidence.
	for _, tc := range []struct {
		name, mnemonic, arg string
		want                int32
	}{
		{"i8x16, all nonzero", "i8x16.all_true", `(v128.const i8x16 1 1 1 1 1 1 1 1 1 1 1 1 1 1 1 1)`, 1},
		{"i8x16, one zero", "i8x16.all_true", `(v128.const i8x16 1 1 1 1 1 1 1 0 1 1 1 1 1 1 1 1)`, 0},
		{"i16x8, all nonzero", "i16x8.all_true", `(v128.const i16x8 1 1 1 1 1 1 1 1)`, 1},
		{"i16x8, one zero", "i16x8.all_true", `(v128.const i16x8 1 1 0 1 1 1 1 1)`, 0},
		{"i32x4, all nonzero", "i32x4.all_true", `(v128.const i32x4 1 2 3 4)`, 1},
		{"i32x4, one zero", "i32x4.all_true", `(v128.const i32x4 1 0 3 4)`, 0},
		{"i64x2, all nonzero", "i64x2.all_true", `(v128.const i64x2 1 2)`, 1},
		{"i64x2, one zero", "i64x2.all_true", `(v128.const i64x2 0 2)`, 0},
	} {
		t.Run(tc.mnemonic+" "+tc.name, func(t *testing.T) {
			src := `(module (func (export "c") (result i32) (` + tc.mnemonic + " " + tc.arg + `)))`
			out := runSIMD1(t, src)
			if int32(out[0].Bits) != tc.want {
				t.Errorf("got %d, want %d", int32(out[0].Bits), tc.want)
			}
		})
	}

	// bitmask: lane 0's sign bit lands in bit 0, ascending — a row with the sign bits set on
	// alternating lanes (not all, not none, not a contiguous run) is what distinguishes correct
	// per-lane-to-bit-position mapping from a reversed or off-by-one one.
	t.Run("i32x4.bitmask, alternating sign bits", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (result i32)
			(i32x4.bitmask (v128.const i32x4 -1 0 -1 0))))`)
		if out[0].Bits != 0x5 { // lanes 0 and 2 negative -> bits 0 and 2 set -> 0b0101
			t.Errorf("got %#x, want 0x5", out[0].Bits)
		}
	})
	t.Run("i8x16.bitmask, single high lane", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (result i32)
			(i8x16.bitmask (v128.const i8x16
				0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 -1))))`)
		if out[0].Bits != 0x8000 { // lane 15 negative -> bit 15 set
			t.Errorf("got %#x, want 0x8000", out[0].Bits)
		}
	})
	t.Run("i64x2.bitmask", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (result i32)
			(i64x2.bitmask (v128.const i64x2 -1 0))))`)
		if out[0].Bits != 0x1 {
			t.Errorf("got %#x, want 0x1", out[0].Bits)
		}
	})
}
