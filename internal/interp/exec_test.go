package interp

import (
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// run1 compiles a one-function module and invokes its single export.
//
// Through `text.EncodeModule` → `binary.DecodeModule` → `Invoke` rather than by hand-building a
// `binary.Module`, because the immediate staging is *part of the subject*: grave #125 was a
// disagreement between what the decoder puts in `Imm0` and what the interpreter expects a slot to
// hold, and a hand-built module would let this test assert the interpreter against its own
// assumption about the decoder. The path is the thing under test.
func run1(t *testing.T, src string) []Value {
	t.Helper()
	img, err := text.EncodeModule([]byte(src))
	if err != nil {
		t.Fatalf("encode %s: %v", src, err)
	}
	m, err := binary.DecodeModule(img)
	if err != nil {
		t.Fatalf("decode %s: %v", src, err)
	}
	out, err := New(m).Invoke("c")
	if err != nil {
		t.Fatalf("invoke %s: %v", src, err)
	}
	return out
}

// TestI32ConstOccupiesItsSlotZeroExtended is grave #125's reproducer.
//
// **The defect:** `i32.const`, `i64.const`, `f32.const` and `f64.const` shared one `case` arm
// pushing `Imm0` unexamined. That is right for three of them and wrong for i32, because the decoder
// stages an s32 immediate **sign-extended to 64 bits** (`immS32`) while an i32 slot is defined as
// the low 32 bits with the high bits *zero*. So `i32.const -1` sat in the stack as
// `0xFFFFFFFFFFFFFFFF` instead of `0xFFFFFFFF`.
//
// **Why the suite could not see it.** 114 of the corpus's 6498 distinct const spellings were
// converted wrongly and the board did not move by a single vector — 17923/14330/32764 before and
// after the fix, byte-identical. Two reasons compounded: the vectors that would have compared a
// negative i32 were already failing on the encoder's instruction frontier (#8), and the ones that
// were not compare against `spec.readIntLit`, a *second* reader that was right. What found it was
// `spec.TestHarnessAndEngineLiteralReadersAgree` on its first run, which is the whole argument for
// keeping two independently-derived literal readers: the engine agreeing with itself is not
// evidence.
//
// **What is asserted here, and why it is a partition rather than one case.** A test pinning only
// the boundary result would pass on a fix that truncated at `Invoke` and left the stack wrong, so
// each row names a *different observation path* out of the slot:
//
//   - direct — the host boundary, which is what an `assert_return` reads;
//   - through a local — `local.set`/`local.get` copy the raw slot, so a wrong one survives;
//   - widened unsigned — `i64.extend_i32_u`, reaching the slot through a *different* type;
//   - through arithmetic — `i32.or`, which routes via `popI32`/`pushI32`.
//
// **Which rows actually fail was measured by reintroducing the defect, and it corrected this
// comment.** Three of the six do: direct, through-a-local, and the hex spelling. The draft of this
// paragraph called `i64.extend_i32_u` "the sharpest witness" on the reasoning that a sign-extended
// slot would make it yield -1 instead of 4294967295 — plausible, and wrong: `extend_i32_u` is
// `pushI64(int64(uint32(st.popI32())))`, and `popI32` truncates, so it was protected the whole
// time. Every path through the `popI32`/`pushI32` helpers was, which is precisely why the damage
// stopped where it did and why the raw-slot paths are the only witnesses. The claim was a
// prediction about a mechanism rather than an observation of one; the falsification pass is what
// separated them, which is the second-order-honesty rule applied to a test's own documentation.
//
// The three green rows stay because a partition needs its negative side: a "fix" that truncated at
// `Invoke` instead of at the push would satisfy the failing rows and break nothing here, and the
// rows that were *never* wrong are what mark the boundary of the damage for whoever reads this
// while repairing a future regression.
//
// The expected patterns are written as `I32(…)`/`I64(…)` rather than as hex literals, because those
// constructors *are* the slot discipline (`uint64(uint32(v))`) and a hand-written `0xFFFFFFFF` would
// be a second statement of the same rule, free to drift from it.
func TestI32ConstOccupiesItsSlotZeroExtended(t *testing.T) {
	cases := []struct {
		what string
		src  string
		want Value
	}{
		{
			"direct to the boundary",
			`(module (func (export "c") (result i32) (i32.const -1)))`,
			I32(-1),
		},
		{
			"copied through a local",
			`(module (func (export "c") (result i32) (local i32)` +
				` (i32.const -1) (local.set 0) (local.get 0)))`,
			I32(-1),
		},
		{
			// Green before the fix as well as after — `extend_i32_u` truncates via popI32.
			// Kept because it reaches the slot through a different *type*, so a future
			// change to the widening path is covered here rather than nowhere.
			"widened unsigned by i64.extend_i32_u (correct pre-fix)",
			`(module (func (export "c") (result i64) (i32.const -1) (i64.extend_i32_u)))`,
			I64(4294967295),
		},
		{
			// Correct before the fix as well as after: the arithmetic helpers always
			// zero-extended. Kept so the partition says where the defect *stopped*, which
			// a reader repairing a future regression needs as much as the failing rows.
			"through arithmetic (correct pre-fix; marks the damage boundary)",
			`(module (func (export "c") (result i32) (i32.const -1) (i32.const 0) (i32.or)))`,
			I32(-1),
		},
		{
			// The other sign, so the assertion is not satisfied by a blanket truncation:
			// a positive constant near the boundary must keep its bits too.
			"the largest positive i32",
			`(module (func (export "c") (result i32) (i32.const 2147483647)))`,
			I32(2147483647),
		},
		{
			// The hex spelling of the same pattern the suite writes as an unsigned
			// literal. `i32.const 0xffffffff` and `i32.const -1` are one value.
			"unsigned hex spelling of the same pattern",
			`(module (func (export "c") (result i32) (i32.const 0xffffffff)))`,
			I32(-1),
		},
	}
	for _, c := range cases {
		out := run1(t, c.src)
		if len(out) != 1 {
			t.Errorf("%s: got %d results, want 1", c.what, len(out))
			continue
		}
		if out[0].Type != c.want.Type || out[0].Bits != c.want.Bits {
			t.Errorf("%s\n\t%s\n\tgot  %v %#016x\n\twant %v %#016x\n"+
				"\tgrave #125: an i32 slot holds the low 32 bits with the high bits zero, "+
				"and the decoder stages i32.const sign-extended",
				c.what, c.src, out[0].Type, out[0].Bits, c.want.Type, c.want.Bits)
		}
	}
}

// TestI64AndFloatConstsArePushedVerbatim is the other half of the partition the grave split.
//
// The fix separated `0x41` from `0x42`/`0x43`/`0x44`, so there are now two rules where there was
// one, and a repair that truncated *all four* would fix the i32 test above and silently break
// these. In particular an f32.const's pattern must survive as the low 32 bits of the slot without
// being reinterpreted, and an i64.const's full 64 bits must survive untouched — which a `uint32`
// truncation applied uniformly would destroy in the most expensive place, a NaN payload.
//
// `-nan:0x200000` is the row that matters: a signalling NaN payload does not survive a round trip
// through a Go float, so it is also the row that would catch a conversion being inserted into the
// three-opcode arm later.
func TestI64AndFloatConstsArePushedVerbatim(t *testing.T) {
	cases := []struct {
		what string
		src  string
		want Value
	}{
		{
			"i64.const keeps all 64 bits",
			`(module (func (export "c") (result i64) (i64.const -1)))`,
			I64(-1),
		},
		{
			"i64.const at the negative boundary",
			`(module (func (export "c") (result i64) (i64.const -9223372036854775808)))`,
			I64(-9223372036854775808),
		},
		{
			"f32.const negative zero keeps its sign bit",
			`(module (func (export "c") (result f32) (f32.const -0.0)))`,
			F32(negZero32()),
		},
		{
			"f64.const negative zero keeps its sign bit",
			`(module (func (export "c") (result f64) (f64.const -0.0)))`,
			F64(negZero64()),
		},
		{
			// A signalling NaN payload, which is the case a numeric conversion destroys
			// and a bit copy preserves.
			"f32.const signalling NaN keeps its payload",
			`(module (func (export "c") (result f32) (f32.const nan:0x200000)))`,
			Value{Type: binary.F32, Bits: 0x7fa00000},
		},
		{
			"f64.const signalling NaN keeps its payload",
			`(module (func (export "c") (result f64) (f64.const nan:0x4000000000000)))`,
			Value{Type: binary.F64, Bits: 0x7ff4000000000000},
		},
		{
			// float_literals.wast:233, named in value.go as the first instruction
			// Burroughs ever executed. Pinned here so the claim has an oracle.
			"float_literals.wast:233, the first executed vector",
			`(module (func (export "c") (result f64) (f64.const 4294967249)))`,
			F64(4294967249),
		},
	}
	for _, c := range cases {
		out := run1(t, c.src)
		if len(out) != 1 {
			t.Errorf("%s: got %d results, want 1", c.what, len(out))
			continue
		}
		if out[0].Type != c.want.Type || out[0].Bits != c.want.Bits {
			t.Errorf("%s\n\t%s\n\tgot  %v %#016x\n\twant %v %#016x\n"+
				"\tthese three opcodes push Imm0 unexamined; a conversion here loses "+
				"NaN payloads and signed zeros",
				c.what, c.src, out[0].Type, out[0].Bits, c.want.Type, c.want.Bits)
		}
	}
}

// negZero32 and negZero64 build negative zero without writing its pattern down.
//
// `-0.0` is not a constant expression in Go — the compiler folds it to `+0.0` — so the sign bit has
// to be produced by division rather than by literal, and writing `0x80000000` instead would restate
// the pattern this test exists to check.
func negZero32() float32 { var z float32; return -1 / (1 / z) }

func negZero64() float64 { var z float64; return -1 / (1 / z) }
