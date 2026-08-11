package interp

import (
	"math"
	"testing"
)

// f32bits/f64bits are tiny helpers so test rows can write a v128.const module using i32x4/i64x2
// integer literals for bit patterns (NaN, signed zero) that the wat float-literal grammar cannot
// spell directly, while still exercising the float arms under test.
func f32bits(v float32) uint32 { return math.Float32bits(v) }
func f64bits(v float64) uint64 { return math.Float64bits(v) }

// TestSIMDFloatArithmetic is #212's fifth ladder rung's third and highest-risk sub-batch: the 30
// float mnemonics (14 unary: abs/neg/sqrt/ceil/floor/trunc/nearest × f32x4/f64x2; 16 binary:
// add/sub/mul/div/min/max/pmin/pmax × f32x4/f64x2). Landing on plumbing hardened by the prior
// sub-batches' 65 comparison arms across every width, per Scott's own ordering.
func TestSIMDFloatArithmetic(t *testing.T) {
	// abs/neg are bitwise (never through math.Abs/unary negation) — pinned with a NaN operand,
	// the one value where a bitwise implementation and a through-the-FPU one could plausibly
	// diverge (this engine's own choice is bitwise, per fxx.ml's explicit comment).
	t.Run("f32x4.abs clears the sign bit, including on NaN", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (result v128)
			(f32x4.abs (v128.const i32x4 0xffc00001 0x80000000 0 0))))`)
		wantLo := uint64(0x00000000_7fc00001)
		if out[0].Hi != 0 || out[0].Bits != wantLo {
			t.Errorf("got hi=%#x lo=%#x, want hi=0 lo=%#x", out[0].Hi, out[0].Bits, wantLo)
		}
	})
	t.Run("f64x2.neg flips the sign bit, including on NaN", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (result v128)
			(f64x2.neg (v128.const i64x2 0x7ff8000000000001 0x8000000000000000))))`)
		wantHi, wantLo := uint64(0x0000000000000000), uint64(0xfff8000000000001)
		if out[0].Hi != wantHi || out[0].Bits != wantLo {
			t.Errorf("got hi=%#x lo=%#x, want hi=%#x lo=%#x", out[0].Hi, out[0].Bits, wantHi, wantLo)
		}
	})

	// ceil/floor/trunc, each pinned on a negative fraction so the sign of the result and the
	// direction of rounding are both distinguishable from a same-magnitude positive input.
	t.Run("f32x4.ceil", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (result v128)
			(f32x4.ceil (v128.const f32x4 -1.5 2.1 0 0))))`)
		wantLo := uint64(f32bits(3))<<32 | uint64(f32bits(-1))
		if out[0].Hi != 0 || out[0].Bits != wantLo {
			t.Errorf("got hi=%#x lo=%#x, want hi=0 lo=%#x", out[0].Hi, out[0].Bits, wantLo)
		}
	})
	t.Run("f32x4.floor", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (result v128)
			(f32x4.floor (v128.const f32x4 -1.5 2.9 0 0))))`)
		wantLo := uint64(f32bits(2))<<32 | uint64(f32bits(-2))
		if out[0].Hi != 0 || out[0].Bits != wantLo {
			t.Errorf("got hi=%#x lo=%#x, want hi=0 lo=%#x", out[0].Hi, out[0].Bits, wantLo)
		}
	})
	t.Run("f64x2.trunc, toward zero not toward -infinity", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (result v128)
			(f64x2.trunc (v128.const f64x2 -1.9 1.9))))`)
		wantHi, wantLo := f64bits(1), f64bits(-1)
		if out[0].Hi != wantHi || out[0].Bits != wantLo {
			t.Errorf("got hi=%#x lo=%#x, want hi=%#x lo=%#x — trunc(-1.9) is -1, not -2 (floor's answer)",
				out[0].Hi, out[0].Bits, wantHi, wantLo)
		}
	})

	// nearest is round-half-to-even, pinned with both an odd and an even tie so a reader that
	// always rounds ties up (or always down) is caught by whichever tie it gets wrong.
	t.Run("f32x4.nearest, round-half-to-even on both tie directions", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (result v128)
			(f32x4.nearest (v128.const f32x4 2.5 3.5 -2.5 0))))`)
		wantLo := uint64(f32bits(4))<<32 | uint64(f32bits(2))
		wantHi := uint64(f32bits(-2))
		if out[0].Hi != wantHi || out[0].Bits != wantLo {
			t.Errorf("got hi=%#x lo=%#x, want hi=%#x lo=%#x (2.5->2, 3.5->4, -2.5->-2, all to the "+
				"nearest even integer)", out[0].Hi, out[0].Bits, wantHi, wantLo)
		}
	})

	t.Run("f32x4.sqrt", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (result v128)
			(f32x4.sqrt (v128.const f32x4 4 9 0 0))))`)
		wantLo := uint64(f32bits(3))<<32 | uint64(f32bits(2))
		if out[0].Hi != 0 || out[0].Bits != wantLo {
			t.Errorf("got hi=%#x lo=%#x, want hi=0 lo=%#x", out[0].Hi, out[0].Bits, wantLo)
		}
	})

	// min/max: the equal-operands signed-zero special case, pinned exactly as decision 0024's
	// own arch-dependence survey measured it — min(-0,0) is -0, max(-0,0) is 0.
	t.Run("f32x4.min of -0 and 0 is -0", func(t *testing.T) {
		negZero := f32bits(float32(math.Copysign(0, -1)))
		src := `(module (func (export "c") (result v128)
			(f32x4.min (v128.const i32x4 ` + itoa(uint64(negZero)) + ` 0 0 0)
			           (v128.const i32x4 0 0 0 0))))`
		out := runSIMD1(t, src)
		if out[0].Hi != 0 || out[0].Bits != uint64(negZero) {
			t.Errorf("got hi=%#x lo=%#x, want hi=0 lo=%#x (min(-0,0) is -0)",
				out[0].Hi, out[0].Bits, negZero)
		}
	})
	t.Run("f32x4.max of -0 and 0 is 0", func(t *testing.T) {
		negZero := f32bits(float32(math.Copysign(0, -1)))
		src := `(module (func (export "c") (result v128)
			(f32x4.max (v128.const i32x4 ` + itoa(uint64(negZero)) + ` 0 0 0)
			           (v128.const i32x4 0 0 0 0))))`
		out := runSIMD1(t, src)
		if out[0].Hi != 0 || out[0].Bits != 0 {
			t.Errorf("got hi=%#x lo=%#x, want hi=0 lo=0 (max(-0,0) is +0)", out[0].Hi, out[0].Bits)
		}
	})

	// pmin/pmax: the asymmetric, NaN-defaults-to-first-operand behavior that distinguishes them
	// from min/max — two rows per function, NaN as each operand in turn, since a reader that
	// implemented pmin/pmax as ordinary min/max (which special-case NaN symmetrically) would
	// pass neither row exactly, and a reader that only tested NaN in one position could still
	// have the wrong operand order for the other.
	t.Run("f32x4.pmin, NaN as the second operand returns the first", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (result v128)
			(f32x4.pmin (v128.const f32x4 3 0 0 0) (v128.const i32x4 0x7fc00000 0 0 0))))`)
		wantLo := uint64(f32bits(3))
		if out[0].Hi != 0 || out[0].Bits != wantLo {
			t.Errorf("got hi=%#x lo=%#x, want hi=0 lo=%#x (the first operand, 3, since NaN<3 is false)",
				out[0].Hi, out[0].Bits, wantLo)
		}
	})
	t.Run("f32x4.pmin, NaN as the first operand returns NaN", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (result v128)
			(f32x4.pmin (v128.const i32x4 0x7fc00000 0 0 0) (v128.const f32x4 5 0 0 0))))`)
		wantLo := uint64(0x7fc00000)
		if out[0].Hi != 0 || out[0].Bits != wantLo {
			t.Errorf("got hi=%#x lo=%#x, want hi=0 lo=%#x (the first operand, NaN, since 5<NaN is false)",
				out[0].Hi, out[0].Bits, wantLo)
		}
	})
	t.Run("f32x4.pmax, NaN as the second operand returns the first", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (result v128)
			(f32x4.pmax (v128.const f32x4 3 0 0 0) (v128.const i32x4 0x7fc00000 0 0 0))))`)
		wantLo := uint64(f32bits(3))
		if out[0].Hi != 0 || out[0].Bits != wantLo {
			t.Errorf("got hi=%#x lo=%#x, want hi=0 lo=%#x (the first operand, 3, since 3<NaN is false)",
				out[0].Hi, out[0].Bits, wantLo)
		}
	})

	// Standard arithmetic, one row each, plus the operand-order-sensitive case (sub/div) to
	// confirm the stack's own pop order (second operand on top) is honored.
	t.Run("f64x2.add", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (result v128)
			(f64x2.add (v128.const f64x2 1.5 2.5) (v128.const f64x2 0.5 0.5))))`)
		wantHi, wantLo := f64bits(3), f64bits(2)
		if out[0].Hi != wantHi || out[0].Bits != wantLo {
			t.Errorf("got hi=%#x lo=%#x, want hi=%#x lo=%#x", out[0].Hi, out[0].Bits, wantHi, wantLo)
		}
	})
	t.Run("f64x2.sub, operand order", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (result v128)
			(f64x2.sub (v128.const f64x2 10 0) (v128.const f64x2 3 0))))`)
		wantLo := f64bits(7)
		if out[0].Hi != 0 || out[0].Bits != wantLo {
			t.Errorf("got hi=%#x lo=%#x, want hi=0 lo=%#x (10-3=7, not 3-10=-7)",
				out[0].Hi, out[0].Bits, wantLo)
		}
	})
	t.Run("f64x2.div", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (result v128)
			(f64x2.div (v128.const f64x2 10 0) (v128.const f64x2 4 1))))`)
		wantLo := f64bits(2.5)
		if out[0].Hi != 0 || out[0].Bits != wantLo {
			t.Errorf("got hi=%#x lo=%#x, want hi=0 lo=%#x", out[0].Hi, out[0].Bits, wantLo)
		}
	})
	t.Run("f32x4.mul", func(t *testing.T) {
		out := runSIMD1(t, `(module (func (export "c") (result v128)
			(f32x4.mul (v128.const f32x4 3 0 0 0) (v128.const f32x4 4 0 0 0))))`)
		wantLo := uint64(f32bits(12))
		if out[0].Hi != 0 || out[0].Bits != wantLo {
			t.Errorf("got hi=%#x lo=%#x, want hi=0 lo=%#x", out[0].Hi, out[0].Bits, wantLo)
		}
	})
}

// itoa is a tiny local decimal-string helper — the wat integer-literal grammar needs a decimal
// or hex string, and this avoids reaching for strconv for one call site's worth of use.
func itoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	return string(b)
}
