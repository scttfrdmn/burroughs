package interp

import (
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// runSIMD1 is run1 with the SIMD gate on — every one of #212's ladder rungs needs it, since the
// gate stays default-off (decision: no SIMD gate flip without its own procedure, #153) and this
// package's own arms are unreachable without it.
func runSIMD1(t *testing.T, src string, args ...Value) []Value {
	t.Helper()
	img, err := text.EncodeModule([]byte(src))
	if err != nil {
		t.Fatalf("encode %s: %v", src, err)
	}
	d := &binary.Decoder{Features: binary.Features{SIMD: true}}
	m, err := d.DecodeModule(img)
	if err != nil {
		t.Fatalf("decode %s: %v", src, err)
	}
	in, trap := Instantiate(m)
	if trap != nil {
		t.Fatalf("instantiate %s: %v", src, trap)
	}
	out, err := in.Invoke("c", args...)
	if err != nil {
		t.Fatalf("invoke %s: %v", src, err)
	}
	return out
}

// v128 constructs a Value carrying a v128, hi/lo split exactly as decision 0024 and
// `Instr.Imm0`/`Imm1` do — a helper here so every row below states its bit pattern once rather
// than spelling out the Value literal.
func v128(hi, lo uint64) Value {
	return Value{Type: binary.V128, Bits: lo, Hi: hi}
}

// TestSIMDWholeVectorBitwiseFamily is #212's first ladder rung: the seven whole-vector-bitwise
// mnemonics (`v128.not`/`and`/`andnot`/`or`/`xor`/`bitselect`/`any_true`), chosen first per the
// recon's own recommendation for having no per-lane loop and no width dispatch — the cheapest
// end-to-end confirmation that decision 0024's stack representation actually works.
//
// Every row round-trips a v128 through `local.get`, exercising both the stack's own
// `pushV128`/`popV128` (decision 0024) and the frame's `numHi`/`isV128` widening the recon found
// forced (frame's flat local indexing does not admit the stack's two-adjacent-slots trick) —
// `simd_bitwise.wast`'s own vectors use exactly this shape (`(param $0 v128) (local.get $0)`),
// not a bare `v128.const`, so a row using only constants would not exercise the frame path these
// vectors actually need.
func TestSIMDWholeVectorBitwiseFamily(t *testing.T) {
	for _, tc := range []struct {
		name           string
		src            string
		args           []Value
		wantHi, wantLo uint64
	}{
		{
			"v128.not", `(module (func (export "c") (param $0 v128) (result v128)
				(v128.not (local.get $0))))`,
			[]Value{v128(0x0000000000000000, 0xffffffffffffffff)},
			^uint64(0), ^uint64(0xffffffffffffffff),
		},
		{
			"v128.and", `(module (func (export "c") (param $0 v128) (param $1 v128) (result v128)
				(v128.and (local.get $0) (local.get $1))))`,
			[]Value{
				v128(0xff00ff00ff00ff00, 0x0f0f0f0f0f0f0f0f),
				v128(0x00ff00ff00ff00ff, 0xf0f0f0f0f0f0f0f0),
			},
			0x0000000000000000, 0x0000000000000000,
		},
		{
			"v128.or", `(module (func (export "c") (param $0 v128) (param $1 v128) (result v128)
				(v128.or (local.get $0) (local.get $1))))`,
			[]Value{
				v128(0xff00ff00ff00ff00, 0x0f0f0f0f0f0f0f0f),
				v128(0x00ff00ff00ff00ff, 0xf0f0f0f0f0f0f0f0),
			},
			0xffffffffffffffff, 0xffffffffffffffff,
		},
		{
			"v128.xor", `(module (func (export "c") (param $0 v128) (param $1 v128) (result v128)
				(v128.xor (local.get $0) (local.get $1))))`,
			[]Value{
				v128(0xffffffffffffffff, 0x0000000000000000),
				v128(0xffffffffffffffff, 0xffffffffffffffff),
			},
			0x0000000000000000, 0xffffffffffffffff,
		},
		{
			// v1 AND NOT v2 — the operand order the reference's own `andnot v1 v2` states
			// (v128.ml:330: `andnot = binop (fun x y -> and_ x (not_ y))`), pinned by a row
			// whose two operands are not commutative-looking: swapping them would change the
			// answer, unlike and/or/xor's own rows above.
			"v128.andnot", `(module (func (export "c") (param $0 v128) (param $1 v128) (result v128)
				(v128.andnot (local.get $0) (local.get $1))))`,
			[]Value{
				v128(0xffffffffffffffff, 0xffffffffffffffff),
				v128(0x00000000ffffffff, 0x0000000000000000),
			},
			0xffffffff00000000, 0xffffffffffffffff,
		},
		{
			// v128.bitselect(v1, v2, c) = (v1 & c) | (v2 & ~c) — c is the *third* operand and
			// the mask, per `eval.ml:1011`'s stack order (`Vec v3 :: Vec v2 :: Vec v1`, c pushed
			// last, popped first). This row's mask alternates by byte so a swapped v1/v2 or an
			// inverted mask both produce a different, distinguishable answer.
			"v128.bitselect", `(module (func (export "c") (param $0 v128) (param $1 v128) (param $2 v128) (result v128)
				(v128.bitselect (local.get $0) (local.get $1) (local.get $2))))`,
			[]Value{
				v128(0xffffffffffffffff, 0xffffffffffffffff), // v1: all ones
				v128(0x0000000000000000, 0x0000000000000000), // v2: all zeros
				v128(0xff00ff00ff00ff00, 0x00ff00ff00ff00ff), // c
			},
			0xff00ff00ff00ff00, 0x00ff00ff00ff00ff,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := runSIMD1(t, tc.src, tc.args...)
			if len(out) != 1 || out[0].Type != binary.V128 {
				t.Fatalf("got %+v, want one v128 result", out)
			}
			if out[0].Hi != tc.wantHi || out[0].Bits != tc.wantLo {
				t.Errorf("got hi=%#x lo=%#x, want hi=%#x lo=%#x", out[0].Hi, out[0].Bits, tc.wantHi, tc.wantLo)
			}
		})
	}
}

// TestSIMDAnyTrue pins v128.any_true's own boundary: all-zero is false, any single set bit
// anywhere in either half is true — two rows rather than one so a reader checking only the low
// half (or only the high half) is caught by whichever row it missed.
func TestSIMDAnyTrue(t *testing.T) {
	for _, tc := range []struct {
		name string
		v    Value
		want int32
	}{
		{"all zero", v128(0, 0), 0},
		{"low bit set", v128(0, 1), 1},
		{"high bit set", v128(1, 0), 1},
		{"both halves all ones", v128(^uint64(0), ^uint64(0)), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := runSIMD1(t, `(module (func (export "c") (param $0 v128) (result i32)
				(v128.any_true (local.get $0))))`, tc.v)
			if len(out) != 1 || out[0].Type != binary.I32 {
				t.Fatalf("got %+v, want one i32 result", out)
			}
			if int32(out[0].Bits) != tc.want {
				t.Errorf("v128.any_true(%+v) = %d, want %d", tc.v, out[0].Bits, tc.want)
			}
		})
	}
}

// TestV128RoundTripsThroughAFrameLocal is the family's own control on decision 0024's frame
// widening (`numHi`/`isV128`), isolated from any 0xfd arithmetic: a v128 argument crosses into a
// local via `local.get`, is written back with `local.set`, and read again — if `numHi`/`num` ever
// desynced (wrong index, high half dropped, tee vs. set confusion) this would return a value
// whose high half is stale or zero rather than the one actually stored.
func TestV128RoundTripsThroughAFrameLocal(t *testing.T) {
	out := runSIMD1(t, `(module (func (export "c") (param $0 v128) (result v128)
		(local $tmp v128)
		(local.set $tmp (local.get $0))
		(local.get $tmp)))`, v128(0x1122334455667788, 0x99aabbccddeeff00))
	if len(out) != 1 || out[0].Type != binary.V128 {
		t.Fatalf("got %+v, want one v128 result", out)
	}
	if out[0].Hi != 0x1122334455667788 || out[0].Bits != 0x99aabbccddeeff00 {
		t.Errorf("got hi=%#x lo=%#x, want hi=%#x lo=%#x",
			out[0].Hi, out[0].Bits, uint64(0x1122334455667788), uint64(0x99aabbccddeeff00))
	}
}

// TestV128TeeLocalPreservesTheStackTop falsifies teeLocal's own peek arithmetic
// (`st.num[len(st.num)-2]`/`[-1]` for hi/lo) against a genuine off-by-one: `local.tee` must not
// consume its operand, so chaining a second read of the same value after the tee proves the
// stack's top was left intact rather than merely that the local was written correctly (which the
// round-trip test above already covers via local.set, a different opcode with different stack
// discipline).
func TestV128TeeLocalPreservesTheStackTop(t *testing.T) {
	out := runSIMD1(t, `(module (func (export "c") (param $0 v128) (result v128 v128)
		(local $tmp v128)
		(local.tee $tmp (local.get $0))
		(local.get $tmp)))`, v128(0xdeadbeefdeadbeef, 0xcafebabecafebabe))
	if len(out) != 2 {
		t.Fatalf("got %d results, want 2", len(out))
	}
	for i, out := range out {
		if out.Hi != 0xdeadbeefdeadbeef || out.Bits != 0xcafebabecafebabe {
			t.Errorf("result %d: got hi=%#x lo=%#x, want hi=%#x lo=%#x",
				i, out.Hi, out.Bits, uint64(0xdeadbeefdeadbeef), uint64(0xcafebabecafebabe))
		}
	}
}

// TestDropPopsAV128WhenItIsTheLogicalTop is grave #206's own successor, found while implementing
// this rung rather than in production: `drop`'s pre-0024 logic distinguishes num-vs-ref by
// comparing sequence numbers, but a v128's two slots share *one* sequence number (`pushV128`'s
// own design), so a bare `popNum` — what `drop` fell back to before this rung — would remove only
// the low half and leave the high half behind as a stray slot the next instruction reads as
// garbage.
//
// **The reproducer needs no reference at all**, which is the specific gap this test pins: a
// function that only ever pushes v128 values never touches `pushRef`, so 0023's own
// activate-on-first-reference gate would leave `tracking` false and `drop`'s v128-pair check
// (which reads `numSeq`) could never fire — reproducing #206's shape for a population that did
// not exist when 0023 was written. `pushV128` therefore activates tracking itself now, exactly as
// `pushRef` already did, and this test is what would have caught the gap if it had shipped
// without that fix: `(v128.const …) (drop) (i32.const 7)` must return 7, with nothing left behind
// on the stack for `i32.const` to trip over.
func TestDropPopsAV128WhenItIsTheLogicalTop(t *testing.T) {
	out := runSIMD1(t, `(module (func (export "c") (result i32)
		(v128.const i32x4 1 2 3 4) (drop) (i32.const 7)))`)
	if len(out) != 1 || out[0].Bits != 7 {
		t.Errorf("got %+v, want i32 7 — drop must remove both of the v128's slots, not just "+
			"the low half", out)
	}
}

// TestV128SurvivesABranchOutOfABlock is forced design question 4's own control: `countByArray`
// must report 2 numeric slots for a v128 result, or `branch`'s truncation (`control.go`, keyed
// entirely on that count) computes the wrong source/destination window the moment a block's
// result type includes a v128 — silently keeping the wrong bytes or reading past the block's
// base. A block that returns a v128 alongside an ordinary i32, exited via `br`, is the shape that
// exercises the split: if `countByArray` still counted v128 as one slot, this block's `br 0`
// would truncate `st.num` to one slot short of what the result actually needs, corrupting either
// the v128 or the sibling i32 depending on which one the truncation clips.
func TestV128SurvivesABranchOutOfABlock(t *testing.T) {
	out := runSIMD1(t, `(module (func (export "c") (param $0 v128) (result v128 i32)
		(block (result v128 i32)
			(local.get $0) (i32.const 42) (br 0)
		)))`, v128(0x0102030405060708, 0x090a0b0c0d0e0f10))
	if len(out) != 2 {
		t.Fatalf("got %d results, want 2", len(out))
	}
	if out[0].Type != binary.V128 || out[0].Hi != 0x0102030405060708 || out[0].Bits != 0x090a0b0c0d0e0f10 {
		t.Errorf("result 0: got %+v, want v128 hi=0x0102030405060708 lo=0x090a0b0c0d0e0f10", out[0])
	}
	if out[1].Type != binary.I32 || out[1].Bits != 42 {
		t.Errorf("result 1: got %+v, want i32 42", out[1])
	}
}
