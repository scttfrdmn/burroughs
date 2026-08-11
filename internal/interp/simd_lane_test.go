package interp

import (
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// TestSIMDLaneAccess is #212's fourth ladder rung: the lane-access family (6 `splat` + 8
// `extract_lane[_s|_u]` + 6 `replace_lane`, 20 mnemonics) — the scalar↔vector boundary decision
// 0024 built the representation for but this is the first rung to exercise it directly, since
// the whole-vector-bitwise and memory families both stayed inside the v128 representation
// (never converting to or from a plain numeric slot).
func TestSIMDLaneAccess(t *testing.T) {
	t.Run("i8x16.splat replicates the low byte into all 16 lanes", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (param $0 i32) (result v128)
			(i8x16.splat (local.get $0))))`, Value{Type: binary.I32, Bits: 0xAB})
		want := uint64(0xABABABABABABABAB)
		if out[0].Hi != want || out[0].Bits != want {
			t.Errorf("got hi=%#x lo=%#x, want both halves %#x", out[0].Hi, out[0].Bits, want)
		}
	})
	t.Run("i16x8.splat", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (param $0 i32) (result v128)
			(i16x8.splat (local.get $0))))`, Value{Type: binary.I32, Bits: 0x1234})
		want := uint64(0x1234123412341234)
		if out[0].Hi != want || out[0].Bits != want {
			t.Errorf("got hi=%#x lo=%#x, want both halves %#x", out[0].Hi, out[0].Bits, want)
		}
	})
	t.Run("i32x4.splat", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (param $0 i32) (result v128)
			(i32x4.splat (local.get $0))))`, Value{Type: binary.I32, Bits: 0x11223344})
		want := uint64(0x1122334411223344)
		if out[0].Hi != want || out[0].Bits != want {
			t.Errorf("got hi=%#x lo=%#x, want both halves %#x", out[0].Hi, out[0].Bits, want)
		}
	})
	t.Run("i64x2.splat", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (param $0 i64) (result v128)
			(i64x2.splat (local.get $0))))`, Value{Type: binary.I64, Bits: 0x1122334455667788})
		want := uint64(0x1122334455667788)
		if out[0].Hi != want || out[0].Bits != want {
			t.Errorf("got hi=%#x lo=%#x, want both halves %#x", out[0].Hi, out[0].Bits, want)
		}
	})
	// f32x4/f64x2.splat share the arm with their integer siblings (float bits are numeric bits
	// verbatim) — a row asserting a non-round float value pins that the bits pass through
	// unconverted, not reinterpreted through a Go float and back.
	t.Run("f32x4.splat carries the bits verbatim", func(t *testing.T) {
		// 1.5f32 = 0x3FC00000
		out := runSIMD1(t, `(module (func (export "c") (param $0 f32) (result v128)
			(f32x4.splat (local.get $0))))`, Value{Type: binary.F32, Bits: 0x3FC00000})
		want := uint64(0x3FC000003FC00000)
		if out[0].Hi != want || out[0].Bits != want {
			t.Errorf("got hi=%#x lo=%#x, want both halves %#x", out[0].Hi, out[0].Bits, want)
		}
	})

	// The sign/zero-extension pair, pinned with a byte whose top bit is set — the one value
	// that distinguishes the two readings. A row using only small positive bytes would pass
	// under either extension rule.
	t.Run("i8x16.extract_lane_s sign-extends", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (result i32)
			(i8x16.extract_lane_s 1 (v128.const i8x16 0 0xff 0 0 0 0 0 0 0 0 0 0 0 0 0 0))))`)
		if int32(out[0].Bits) != -1 {
			t.Errorf("got %d, want -1", int32(out[0].Bits))
		}
	})
	t.Run("i8x16.extract_lane_u zero-extends", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (result i32)
			(i8x16.extract_lane_u 1 (v128.const i8x16 0 0xff 0 0 0 0 0 0 0 0 0 0 0 0 0 0))))`)
		if out[0].Bits != 255 {
			t.Errorf("got %d, want 255", out[0].Bits)
		}
	})
	t.Run("i16x8.extract_lane_s sign-extends", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (result i32)
			(i16x8.extract_lane_s 1 (v128.const i16x8 0 0xffff 0 0 0 0 0 0))))`)
		if int32(out[0].Bits) != -1 {
			t.Errorf("got %d, want -1", int32(out[0].Bits))
		}
	})
	t.Run("i16x8.extract_lane_u zero-extends", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (result i32)
			(i16x8.extract_lane_u 1 (v128.const i16x8 0 0xffff 0 0 0 0 0 0))))`)
		if out[0].Bits != 0xffff {
			t.Errorf("got %#x, want 0xffff", out[0].Bits)
		}
	})
	t.Run("i32x4.extract_lane, no signedness suffix", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (result i32)
			(i32x4.extract_lane 2 (v128.const i32x4 0 0 0xAABBCCDD 0))))`)
		if out[0].Bits != 0xAABBCCDD {
			t.Errorf("got %#x, want 0xaabbccdd", out[0].Bits)
		}
	})
	t.Run("i64x2.extract_lane", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (result i64)
			(i64x2.extract_lane 1 (v128.const i64x2 0 0x1122334455667788))))`)
		if out[0].Bits != 0x1122334455667788 {
			t.Errorf("got %#x, want 0x1122334455667788", out[0].Bits)
		}
	})
	t.Run("f32x4.extract_lane carries the bits verbatim", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (result f32)
			(f32x4.extract_lane 1 (v128.const f32x4 0 1.5 0 0))))`)
		if out[0].Bits != 0x3FC00000 {
			t.Errorf("got %#x, want 0x3fc00000 (1.5f32)", out[0].Bits)
		}
	})
	t.Run("f64x2.extract_lane carries the bits verbatim", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (result f64)
			(f64x2.extract_lane 1 (v128.const f64x2 0 1.5))))`)
		if out[0].Bits != 0x3FF8000000000000 {
			t.Errorf("got %#x, want 0x3ff8000000000000 (1.5f64)", out[0].Bits)
		}
	})

	// replace_lane: every row asserts the *other* lanes survived untouched, not merely that the
	// target lane changed — a writer that zeroed the whole vector before writing one lane would
	// pass a narrower assertion.
	t.Run("i8x16.replace_lane changes one lane, leaves the rest", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (result v128)
			(i8x16.replace_lane 1 (v128.const i8x16
				1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16) (i32.const 0xff))))`)
		orig := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
		orig[1] = 0xff
		var hi, lo uint64
		for i := 7; i >= 0; i-- {
			lo = lo<<8 | uint64(orig[i])
			hi = hi<<8 | uint64(orig[8+i])
		}
		if out[0].Hi != hi || out[0].Bits != lo {
			t.Errorf("got hi=%#x lo=%#x, want hi=%#x lo=%#x", out[0].Hi, out[0].Bits, hi, lo)
		}
	})
	t.Run("i32x4.replace_lane", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (result v128)
			(i32x4.replace_lane 2 (v128.const i32x4 1 2 3 4) (i32.const 99))))`)
		wantHi, wantLo := uint64(0x0000000400000063), uint64(0x0000000200000001)
		if out[0].Hi != wantHi || out[0].Bits != wantLo {
			t.Errorf("got hi=%#x lo=%#x, want hi=%#x lo=%#x", out[0].Hi, out[0].Bits, wantHi, wantLo)
		}
	})
	t.Run("i64x2.replace_lane", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (result v128)
			(i64x2.replace_lane 0 (v128.const i64x2 1 2) (i64.const 0x1122334455667788))))`)
		wantHi, wantLo := uint64(2), uint64(0x1122334455667788)
		if out[0].Hi != wantHi || out[0].Bits != wantLo {
			t.Errorf("got hi=%#x lo=%#x, want hi=%#x lo=%#x", out[0].Hi, out[0].Bits, wantHi, wantLo)
		}
	})
	t.Run("f32x4.replace_lane, different opcode than i32x4's own", func(t *testing.T) {
		// i32x4.replace_lane (0x1c) and f32x4.replace_lane (0x20) share a width but are
		// distinct opcodes — this row exercises the one execFD does not share with the i32x4
		// row above.
		out := runSIMD1(t, `(module (func (export "c") (result v128)
			(f32x4.replace_lane 0 (v128.const f32x4 0 0 0 0) (f32.const 1.5))))`)
		wantLo := uint64(0x3FC00000)
		if out[0].Hi != 0 || out[0].Bits != wantLo {
			t.Errorf("got hi=%#x lo=%#x, want hi=0 lo=%#x", out[0].Hi, out[0].Bits, wantLo)
		}
	})
	t.Run("f64x2.replace_lane, different opcode than i64x2's own", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (result v128)
			(f64x2.replace_lane 1 (v128.const f64x2 0 0) (f64.const 1.5))))`)
		wantHi := uint64(0x3FF8000000000000)
		if out[0].Hi != wantHi || out[0].Bits != 0 {
			t.Errorf("got hi=%#x lo=%#x, want hi=%#x lo=0", out[0].Hi, out[0].Bits, wantHi)
		}
	})
}
