package interp

import (
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// TestSIMDLoadStore is #212's second ladder rung: the memory family (13 `VecLoad`/`VecStore`
// mnemonics plus the eight lane-load/store forms, 23 total) built on the MVP load/store family's
// own `memoryFor`/`mem.read`/`mem.write`/`mem.addr` helpers rather than duplicating them — the
// wire staging (`Imm0` offset, `Imm1` memory index) is the identical shape `decodeMemop` already
// uses for opcodes 0x28-0x3e.
func TestSIMDLoadStore(t *testing.T) {
	t.Run("v128.load", func(t *testing.T) {
		out := runSIMD1(t, `(module (memory 1)
			(data (i32.const 0) "\00\01\02\03\04\05\06\07\08\09\0a\0b\0c\0d\0e\0f")
			(func (export "c") (result v128) (v128.load (i32.const 0))))`)
		wantHi, wantLo := uint64(0x0f0e0d0c0b0a0908), uint64(0x0706050403020100)
		if out[0].Hi != wantHi || out[0].Bits != wantLo {
			t.Errorf("got hi=%#x lo=%#x, want hi=%#x lo=%#x", out[0].Hi, out[0].Bits, wantHi, wantLo)
		}
	})

	// The six load-and-extend forms: read 8 bytes (half a v128), sign- or zero-extend each
	// narrow lane to double its width. `0xff` in a source byte pins the sign-extension
	// direction — a zero-extending reader would produce 0x00ff (or 0x000000ff/...) where a
	// sign-extending one produces all-ones through the widened lane's own sign bit.
	t.Run("v128.load8x8_s sign-extends", func(t *testing.T) {
		out := runSIMD1(t, `(module (memory 1)
			(data (i32.const 0) "\ff\01\ff\03\ff\05\ff\07")
			(func (export "c") (result v128) (v128.load8x8_s (i32.const 0))))`)
		wantHi, wantLo := uint64(0x0007ffff0005ffff), uint64(0x0003ffff0001ffff)
		if out[0].Hi != wantHi || out[0].Bits != wantLo {
			t.Errorf("got hi=%#x lo=%#x, want hi=%#x lo=%#x", out[0].Hi, out[0].Bits, wantHi, wantLo)
		}
	})
	t.Run("v128.load8x8_u zero-extends", func(t *testing.T) {
		out := runSIMD1(t, `(module (memory 1)
			(data (i32.const 0) "\ff\01\ff\03\ff\05\ff\07")
			(func (export "c") (result v128) (v128.load8x8_u (i32.const 0))))`)
		wantHi, wantLo := uint64(0x000700ff000500ff), uint64(0x000300ff000100ff)
		if out[0].Hi != wantHi || out[0].Bits != wantLo {
			t.Errorf("got hi=%#x lo=%#x, want hi=%#x lo=%#x", out[0].Hi, out[0].Bits, wantHi, wantLo)
		}
	})
	t.Run("v128.load32x2_s widens two i32 lanes to i64", func(t *testing.T) {
		out := runSIMD1(t, `(module (memory 1)
			(data (i32.const 0) "\ff\ff\ff\ff\01\00\00\00")
			(func (export "c") (result v128) (v128.load32x2_s (i32.const 0))))`)
		wantLo := ^uint64(0) // -1 sign-extended to i64
		wantHi := uint64(1)
		if out[0].Hi != wantHi || out[0].Bits != wantLo {
			t.Errorf("got hi=%#x lo=%#x, want hi=%#x lo=%#x", out[0].Hi, out[0].Bits, wantHi, wantLo)
		}
	})

	// The four splat forms: read one scalar, replicate across every lane. load64_splat is its
	// own check that the "splat" is a copy into *both* halves, not a single write.
	t.Run("v128.load32_splat", func(t *testing.T) {
		out := runSIMD1(t, `(module (memory 1)
			(data (i32.const 0) "\2a\00\00\00")
			(func (export "c") (result v128) (v128.load32_splat (i32.const 0))))`)
		wantHi, wantLo := uint64(0x0000002a0000002a), uint64(0x0000002a0000002a)
		if out[0].Hi != wantHi || out[0].Bits != wantLo {
			t.Errorf("got hi=%#x lo=%#x, want hi=%#x lo=%#x", out[0].Hi, out[0].Bits, wantHi, wantLo)
		}
	})
	t.Run("v128.load64_splat", func(t *testing.T) {
		out := runSIMD1(t, `(module (memory 1)
			(data (i32.const 0) "\01\02\03\04\05\06\07\08")
			(func (export "c") (result v128) (v128.load64_splat (i32.const 0))))`)
		want := uint64(0x0807060504030201)
		if out[0].Hi != want || out[0].Bits != want {
			t.Errorf("got hi=%#x lo=%#x, want both halves %#x", out[0].Hi, out[0].Bits, want)
		}
	})

	// The two zero forms: the scalar in lane 0, every other bit zero — not splat, and a row
	// asserting a nonzero source with a zero high half is what distinguishes the two.
	t.Run("v128.load32_zero", func(t *testing.T) {
		out := runSIMD1(t, `(module (memory 1)
			(data (i32.const 0) "\2a\00\00\00")
			(func (export "c") (result v128) (v128.load32_zero (i32.const 0))))`)
		if out[0].Hi != 0 || out[0].Bits != 0x2a {
			t.Errorf("got hi=%#x lo=%#x, want hi=0 lo=0x2a", out[0].Hi, out[0].Bits)
		}
	})
	t.Run("v128.load64_zero", func(t *testing.T) {
		out := runSIMD1(t, `(module (memory 1)
			(data (i32.const 0) "\01\02\03\04\05\06\07\08")
			(func (export "c") (result v128) (v128.load64_zero (i32.const 0))))`)
		if out[0].Hi != 0 || out[0].Bits != 0x0807060504030201 {
			t.Errorf("got hi=%#x lo=%#x, want hi=0 lo=0x0807060504030201", out[0].Hi, out[0].Bits)
		}
	})

	// **Grave: v128.load8_splat/load16_splat/load32_splat over-read past their own scalar
	// width, tripping a spurious out-of-bounds trap** — verbatim addresses from
	// simd_load_splat.wast:47/52/57, each one byte, two bytes, and four bytes (respectively)
	// short of a one-page memory's end: legal for the splat's own scalar, but `vecLoadWidth`
	// used to route every packed opcode through the same 8-byte branch as the six
	// load*x*_s/u forms, so a load positioned so those extra unread bytes crossed the
	// memory's edge faulted where the spec expects a value. load64_splat genuinely does read
	// 8 bytes, so it is not a row here — the three narrower splats are.
	t.Run("v128.load8_splat at the last legal byte does not over-read into a trap, verbatim from :47", func(t *testing.T) {
		out := runSIMD1(t, `(module (memory 1)
			(data (i32.const 65535) "\1f")
			(func (export "c") (result v128) (v128.load8_splat (i32.const 65535))))`)
		want := uint64(0x1f1f1f1f1f1f1f1f)
		if out[0].Hi != want || out[0].Bits != want {
			t.Errorf("got hi=%#x lo=%#x, want hi=%#x lo=%#x (a one-byte read, not an 8-byte "+
				"over-read that would trap 65535+8 past the memory's end)", out[0].Hi, out[0].Bits, want, want)
		}
	})
	t.Run("v128.load16_splat at the last legal address does not over-read into a trap, verbatim from :52", func(t *testing.T) {
		out := runSIMD1(t, `(module (memory 1)
			(data (i32.const 65534) "\1e\1f")
			(func (export "c") (result v128) (v128.load16_splat (i32.const 65534))))`)
		want := uint64(0x1f1e1f1e1f1e1f1e)
		if out[0].Hi != want || out[0].Bits != want {
			t.Errorf("got hi=%#x lo=%#x, want hi=%#x lo=%#x (a two-byte read)", out[0].Hi, out[0].Bits, want, want)
		}
	})
	t.Run("v128.load32_splat at the last legal address does not over-read into a trap, verbatim from :57", func(t *testing.T) {
		out := runSIMD1(t, `(module (memory 1)
			(data (i32.const 65532) "\1c\1d\1e\1f")
			(func (export "c") (result v128) (v128.load32_splat (i32.const 65532))))`)
		want := uint64(0x1f1e1d1c1f1e1d1c)
		if out[0].Hi != want || out[0].Bits != want {
			t.Errorf("got hi=%#x lo=%#x, want hi=%#x lo=%#x (a four-byte read)", out[0].Hi, out[0].Bits, want, want)
		}
	})

	t.Run("v128.store", func(t *testing.T) {
		out := runSIMD1(t, `(module (memory 1)
			(func (export "c") (result i32)
				(v128.store (i32.const 0) (v128.const i32x4 1 2 3 4))
				(i32.load (i32.const 4))))`)
		if out[0].Bits != 2 {
			t.Errorf("got %+v, want i32 2 (the second i32x4 lane, written at byte offset 4)", out)
		}
	})

	// The stack's own operand order — memAccess's own doc comment states the identical rule
	// for the MVP family (address pushed first, value second, value popped first): a row
	// where address and value are both plausible-looking small integers is exactly the case a
	// reversed pop order would still "work" on by accident, so the assertion pins offset 4
	// where a reversed read would produce a different, wrong value.
	t.Run("v128.store writes at the pushed address, not a coincidence", func(t *testing.T) {
		out := runSIMD1(t, `(module (memory 1)
			(func (export "c") (result i32)
				(v128.store (i32.const 4) (v128.const i32x4 0xAAAAAAAA 0xBBBBBBBB 0xCCCCCCCC 0xDDDDDDDD))
				(i32.load (i32.const 8))))`)
		if out[0].Bits != 0xBBBBBBBB {
			t.Errorf("got %#x, want 0xbbbbbbbb — the store's address (4) plus the load-back's own "+
				"offset (8) must land on the second i32x4 lane", uint32(out[0].Bits))
		}
	})

	t.Run("v128.load8_lane replaces exactly one lane, leaving the rest of the operand intact", func(t *testing.T) {
		out := runSIMD1(t, `(module (memory 1)
			(data (i32.const 0) "\ff")
			(func (export "c") (result v128)
				(v128.load8_lane 3 (i32.const 0) (v128.const i8x16
					1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16))))`)
		// Lane 3 (byte offset 3) becomes 0xff; every other byte is untouched. Built by hand from
		// the original 16-byte sequence with byte 3 replaced, rather than asserted as a literal,
		// so the expected value's own derivation is visible in the test.
		orig := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
		orig[3] = 0xff
		var lo, hi uint64
		for i := 7; i >= 0; i-- {
			lo = lo<<8 | uint64(orig[i])
			hi = hi<<8 | uint64(orig[8+i])
		}
		if out[0].Hi != hi || out[0].Bits != lo {
			t.Errorf("got hi=%#x lo=%#x, want hi=%#x lo=%#x", out[0].Hi, out[0].Bits, hi, lo)
		}
	})

	t.Run("v128.store8_lane writes exactly one lane's byte", func(t *testing.T) {
		out := runSIMD1(t, `(module (memory 1)
			(func (export "c") (result i32)
				(v128.store8_lane 1 (i32.const 0) (v128.const i32x4 0xAABBCCDD 0 0 0))
				(i32.load8_u (i32.const 0))))`)
		if out[0].Bits != 0xCC {
			t.Errorf("got %#x, want 0xcc — lane 1 of the i8x16 view of 0xAABBCCDD is byte 0xCC",
				out[0].Bits)
		}
	})

	// The explicit-memory-index form (arm 1 of laneImms), exercising Imm1's low 32 bits
	// (memory index) alongside its bits 32-39 (lane index) in the same instruction — the two
	// fields this opcode's own wire form packs together and the one place a masking mistake
	// between them would show up.
	t.Run("v128.load8_lane with an explicit second memory", func(t *testing.T) {
		out := runSIMDFeatures1(t, binary.Features{SIMD: true, MultiMemory: true}, `(module (memory 1) (memory $m 1)
			(data (memory $m) (i32.const 0) "\ff")
			(func (export "c") (result v128)
				(v128.load8_lane $m 2 (i32.const 0) (v128.const i32x4 0 0 0 0))))`)
		// Lane 2 (byte offset 2) becomes 0xff; everything else stays 0.
		if out[0].Hi != 0 || out[0].Bits != 0x00ff0000 {
			t.Errorf("got hi=%#x lo=%#x, want hi=0 lo=0x00ff0000", out[0].Hi, out[0].Bits)
		}
	})
}
