package interp

import (
	"errors"
	"strings"
	"testing"
)

// TestTruncSatSaturatesRatherThanTrapping is the eight arms' partition, and the partition is over
// **arms of the range analysis** rather than over opcodes: NaN, below the low bound, inside, at or
// above the high bound, times signedness, times the two input widths. Opcodes are how the rows are
// *spelled*; they are not what is being partitioned, and a table organized by opcode would have
// sixteen rows saying "1.5 truncates to 1" and none saying which arm each opcode's boundary lands
// in.
//
// Every row cites `conversions.wast`, because the file is dense with exactly these boundaries and a
// hand-chosen number here would be a second opinion about the spec competing with the suite's. The
// rows that are **derived** say so and state what they are derived from.
//
// The path is `run1` — text encoder → decoder → `Invoke` — so the prefix staging is under test
// rather than assumed. That matters more here than for a single-byte arm: `fc 00` and `unreachable`
// are both `Op == 0x00`, so a hand-built `binary.Instr` would let this file assert the interpreter
// against its own belief about what the decoder puts in `Prefix`. The encoder emits `fc 00`
// today — measured by printing the image, `... 43 00 00 c0 3f fc 00 0b` — which is why the frontier
// at #8 does not block this test the way it blocks the v128 work.
//
// # The falsification pass, and the two claims it killed
//
// Nine mutations were introduced and run; all nine terminated, none wedged the harness. Which
// control dies to which mutation, since a table test's rows are not individually named by a fail:
//
//	mutation                                    dies                              arch
//	NaN check removed (i32)                     4 NaN rows                        amd64 only
//	NaN check removed (i64)                     2 i64 NaN rows                    amd64 only
//	unsigned low arm removed                    the -1.0 row                      both
//	i64 unsigned via int64(d) not uint64(d)     3 rows above 2^63                 both
//	unsigned upper bound 1<<31 not 1<<32        2 rows                            both
//	popF32 for an f64 opcode                    the i32/f64 row                   both
//	math.Trunc → math.Floor                     the -1.5 row                      both
//	prefix gate removed from exec.go            all four tests                    both
//	execFC's default dropped                    the work-list test                both
//
// **Two of this file's claims were false before the pass and are corrected above.** The unsigned
// low arm's witness was written as `-0.5`, which cannot fail because Go truncates it to negative
// zero; it is `-1.0`. And the NaN rows were written as though they failed everywhere, which is
// wrong on the dev host — see the arch table on that block. Both were prose asserting a mechanism
// rather than reporting one, which is the shape the birth requirement exists to catch.
func TestTruncSatSaturatesRatherThanTrapping(t *testing.T) {
	cases := []struct {
		arm  string // which arm of the range analysis this row exercises
		src  string
		want Value
	}{
		// ---- NaN → 0, the arm that must be tested first ----
		//
		// All four opcode families, because the NaN test is written per function in the
		// reference and a Go implementation that shares the analysis could still lose the test
		// on one width. A NaN fails every comparison, so an implementation whose NaN check came
		// *after* the range tests falls through to the conversion.
		//
		// # These rows are architecture-dependent controls, and that was measured, not assumed
		//
		// Go leaves a float→integer conversion of a NaN implementation-dependent, and the two
		// arches this project gates on **disagree** — which makes these rows the only ones in
		// the file whose falsification depends on where it runs. Deleting the NaN check from
		// `truncSatToI32` and running the suite:
		//
		//	                 arm64 (dev host)   amd64
		//	int32(NaN)              0            -2147483648
		//	int32(uint32(NaN))      0                      0
		//	int64(NaN)              0   -9223372036854775808
		//	int64(uint64(NaN))      0   -9223372036854775808
		//
		// So on arm64 the check is dead code and every row here passes without it — a **stillborn
		// control on this host** — while on amd64 four of the six fail. The two that survive even
		// there are the *i32 unsigned* rows, because `uint32(NaN)` is 0 on both arches; they are
		// kept as the partition's negative side, marking where the conversion happens to be safe.
		//
		// The rows are correct and the check is required: the spec says 0, and one of the two
		// gated arches answers otherwise. But a reader who deletes the check, runs `make check` on
		// an Apple laptop, and sees green has *not* falsified anything — which is why the table is
		// here rather than a sentence claiming the rows fail. This is the falsification pass
		// finding that a control's death is conditional on the host, and the honest form of that
		// finding is the numbers from both.
		{
			"NaN, i32/f32 signed (conversions.wast:283)",
			`(module (func (export "c") (result i32) (i32.trunc_sat_f32_s (f32.const nan))))`,
			I32(0),
		},
		{
			"NaN, i32/f32 unsigned (conversions.wast:305)",
			`(module (func (export "c") (result i32) (i32.trunc_sat_f32_u (f32.const nan))))`,
			I32(0),
		},
		{
			"NaN, i64/f64 signed (conversions.wast:424)",
			`(module (func (export "c") (result i64) (i64.trunc_sat_f64_s (f64.const nan))))`,
			I64(0),
		},
		{
			"NaN, i64/f64 unsigned (conversions.wast:448)",
			`(module (func (export "c") (result i64) (i64.trunc_sat_f64_u (f64.const nan))))`,
			I64(0),
		},
		// A *negative* NaN and a signalling payload, because `d != d` is the only test that
		// catches both and a sign-aware check would send -nan to the low arm.
		{
			"negative NaN, i32/f32 signed (conversions.wast:285)",
			`(module (func (export "c") (result i32) (i32.trunc_sat_f32_s (f32.const -nan))))`,
			I32(0),
		},
		{
			"signalling NaN payload, i32/f32 signed (conversions.wast:284)",
			`(module (func (export "c") (result i32) ` +
				`(i32.trunc_sat_f32_s (f32.const nan:0x200000))))`,
			I32(0),
		},

		// ---- Signed: both saturation directions, and the last value inside each bound ----
		//
		// The pairs matter more than the singletons. `2147483648.0` saturating to max and
		// `2147483520.0` (the largest f32 below 2^31) converting exactly are the two sides of
		// one comparison, and an off-by-one bound fails exactly one of them.
		{
			"i32/f32 signed at 2^31, saturates to max (conversions.wast:279)",
			`(module (func (export "c") (result i32) ` +
				`(i32.trunc_sat_f32_s (f32.const 2147483648.0))))`,
			I32(2147483647),
		},
		{
			"i32/f32 signed just inside, converts exactly (conversions.wast:277)",
			`(module (func (export "c") (result i32) ` +
				`(i32.trunc_sat_f32_s (f32.const 2147483520.0))))`,
			I32(2147483520),
		},
		{
			"i32/f32 signed below -2^31, saturates to min (conversions.wast:280)",
			`(module (func (export "c") (result i32) ` +
				`(i32.trunc_sat_f32_s (f32.const -2147483904.0))))`,
			I32(-2147483648),
		},
		{
			"i32/f32 signed exactly -2^31, converts exactly (conversions.wast:278)",
			`(module (func (export "c") (result i32) ` +
				`(i32.trunc_sat_f32_s (f32.const -2147483648.0))))`,
			I32(-2147483648),
		},
		// The infinities, which are the arms' unbounded ends. Kept because an implementation
		// testing `math.IsInf` separately would pass the finite rows and this one names the
		// omission cheaply.
		{
			"i32/f32 signed +inf, saturates to max (conversions.wast:281)",
			`(module (func (export "c") (result i32) (i32.trunc_sat_f32_s (f32.const inf))))`,
			I32(2147483647),
		},
		{
			"i32/f32 signed -inf, saturates to min (conversions.wast:282)",
			`(module (func (export "c") (result i32) (i32.trunc_sat_f32_s (f32.const -inf))))`,
			I32(-2147483648),
		},

		// ---- Unsigned i32: the low arm, and the row that actually witnesses it ----
		//
		// **`-1.0`, not `-0.5`** — measured, and it corrected this test's first draft. Go's
		// `math.Trunc(-0.5)` is negative zero and `-0.0 < 0` is false, so the (-1, 0) vectors
		// fall through to `uint32(-0.0)` and answer 0 with the low arm **deleted**. They cannot
		// fail. `-1.0` truncates to -1, which without the arm reaches `int32(uint32(-1.0))` and
		// answers -1.
		{
			"i32/f32 unsigned -1.0, the low arm's witness (conversions.wast:302)",
			`(module (func (export "c") (result i32) (i32.trunc_sat_f32_u (f32.const -1.0))))`,
			I32(0),
		},
		// The two (-1, 0) vectors are kept anyway, marked as the arm's *negative* side: they
		// are the rows that say where the low arm stops mattering, and a future rewrite that
		// moved the bound to `<= -1.0` pre-truncation must keep them green too.
		{
			"i32/f32 unsigned in (-1,0), green with or without the arm (conversions.wast:299)",
			`(module (func (export "c") (result i32) ` +
				`(i32.trunc_sat_f32_u (f32.const -0x1.ccccccp-1))))`,
			I32(0),
		},
		{
			"i32/f32 unsigned -inf, saturates to 0 (conversions.wast:304)",
			`(module (func (export "c") (result i32) (i32.trunc_sat_f32_u (f32.const -inf))))`,
			I32(0),
		},
		// The unsigned high arm, and the value just inside it. `4294967040.0` is the largest
		// f32 below 2^32 and lands in the slot with the sign bit set — an i32 slot holding an
		// unsigned quantity, which is why the expectation is written `I32(-256)` exactly as the
		// suite writes it.
		{
			"i32/f32 unsigned at 2^32, saturates to all-ones (conversions.wast:301)",
			`(module (func (export "c") (result i32) ` +
				`(i32.trunc_sat_f32_u (f32.const 4294967296.0))))`,
			I32(-1),
		},
		{
			"i32/f32 unsigned just inside 2^32 (conversions.wast:298)",
			`(module (func (export "c") (result i32) ` +
				`(i32.trunc_sat_f32_u (f32.const 4294967040.0))))`,
			I32(-256),
		},
		// Above 2^31 but below 2^32: the row that fails if the unsigned path reuses the signed
		// bound. `2147483648` is legal unsigned and saturates under a signed reading.
		{
			"i32/f32 unsigned at 2^31 is not the signed bound (conversions.wast:297)",
			`(module (func (export "c") (result i32) ` +
				`(i32.trunc_sat_f32_u (f32.const 2147483648))))`,
			I32(-2147483648),
		},

		// ---- The i64 unsigned case: the arm the 32-bit version does not have ----
		//
		// This is the reason `truncSatToI64` diverges from the reference's shape rather than
		// transcribing it, so it gets the sharpest rows in the file. `int64(d)` above 2^63 is
		// implementation-defined — printed on this host as saturating to max — so an
		// implementation that converted through `int64` answers 0x7fffffffffffffff for both
		// rows below.
		{
			"i64/f64 unsigned exactly 2^63 (conversions.wast:443)",
			`(module (func (export "c") (result i64) ` +
				`(i64.trunc_sat_f64_u (f64.const 9223372036854775808))))`,
			I64(-9223372036854775808),
		},
		{
			"i64/f64 unsigned largest f64 below 2^64 (conversions.wast:438)",
			`(module (func (export "c") (result i64) ` +
				`(i64.trunc_sat_f64_u (f64.const 18446744073709549568.0))))`,
			I64(-2048),
		},
		{
			"i64/f64 unsigned at 2^64, saturates to all-ones (conversions.wast:444)",
			`(module (func (export "c") (result i64) ` +
				`(i64.trunc_sat_f64_u (f64.const 18446744073709551616.0))))`,
			I64(-1),
		},
		// The f32-input twin, which reaches the same arm through a widening. Kept because the
		// two opcodes read different stack helpers (`popF32` vs `popF64`) and a wrong pop is
		// invisible on whichever width is not exercised.
		{
			"i64/f32 unsigned above 2^63 through the f32 pop (conversions.wast:392)",
			`(module (func (export "c") (result i64) ` +
				`(i64.trunc_sat_f32_u (f32.const 18446742974197923840.0))))`,
			I64(-1099511627776),
		},
		{
			"i64/f32 unsigned at 2^64, saturates to all-ones (conversions.wast:395)",
			`(module (func (export "c") (result i64) ` +
				`(i64.trunc_sat_f32_u (f32.const 18446744073709551616.0))))`,
			I64(-1),
		},
		{
			"i64/f32 unsigned -1.0, the low arm at 64 bits (conversions.wast:396)",
			`(module (func (export "c") (result i64) (i64.trunc_sat_f32_u (f32.const -1.0))))`,
			I64(0),
		},

		// ---- i64 signed, both bounds ----
		{
			"i64/f64 signed at 2^63, saturates to max (conversions.wast:420)",
			`(module (func (export "c") (result i64) ` +
				`(i64.trunc_sat_f64_s (f64.const 9223372036854775808.0))))`,
			I64(9223372036854775807),
		},
		{
			"i64/f64 signed below -2^63, saturates to min (conversions.wast:421)",
			`(module (func (export "c") (result i64) ` +
				`(i64.trunc_sat_f64_s (f64.const -9223372036854777856.0))))`,
			I64(-9223372036854775808),
		},

		// ---- Inside the range: the rows that keep a blanket-saturating engine honest ----
		//
		// Without these, an implementation returning min or max unconditionally passes every
		// boundary row above. Truncation toward zero on both signs, which is `math.Trunc` and
		// not a floor.
		{
			"i32/f32 signed 1.5 truncates toward zero (conversions.wast:271)",
			`(module (func (export "c") (result i32) (i32.trunc_sat_f32_s (f32.const 1.5))))`,
			I32(1),
		},
		{
			"i32/f32 signed -1.5 truncates toward zero, not down (conversions.wast:274)",
			`(module (func (export "c") (result i32) (i32.trunc_sat_f32_s (f32.const -1.5))))`,
			I32(-1),
		},
		{
			"i32/f64 signed inside the range (conversions.wast:316)",
			`(module (func (export "c") (result i32) (i32.trunc_sat_f64_s (f64.const 1.5))))`,
			I32(1),
		},
		{
			"i64/f32 signed inside the range (conversions.wast:365)",
			`(module (func (export "c") (result i64) (i64.trunc_sat_f32_s (f32.const 1.5))))`,
			I64(1),
		},
	}
	for _, c := range cases {
		out := run1(t, c.src)
		if len(out) != 1 {
			t.Errorf("%s: got %d results, want 1", c.arm, len(out))
			continue
		}
		if out[0].Type != c.want.Type || out[0].Bits != c.want.Bits {
			t.Errorf("%s\n\t%s\n\tgot  %v %#016x\n\twant %v %#016x",
				c.arm, c.src, out[0].Type, out[0].Bits, c.want.Type, c.want.Bits)
		}
	}
}

// TestTruncSatNeverTraps is the signature stated as a test.
//
// The eight arms are total functions, and the *absence* of a failure channel is the thing most
// likely to be undone by a later hand: a reader who knows `0xa8`..`0xb1` trap will find it natural
// to add a trap here, and every boundary row above would stay green while `assert_return` vectors
// turned into trap answers. That is the `memory.grow` mistake — the right answer on the wrong
// channel — and it needs a row of its own because no value assertion can see it.
//
// The inputs are the three that trap in the non-saturating siblings, so this is the exact
// comparison: same bytes, same range analysis, different verdict.
func TestTruncSatNeverTraps(t *testing.T) {
	srcs := []string{
		`(module (func (export "c") (result i32) (i32.trunc_sat_f32_s (f32.const nan))))`,
		`(module (func (export "c") (result i32) (i32.trunc_sat_f32_s (f32.const inf))))`,
		`(module (func (export "c") (result i32) ` +
			`(i32.trunc_sat_f32_u (f32.const -1.0))))`,
		`(module (func (export "c") (result i64) ` +
			`(i64.trunc_sat_f64_u (f64.const 18446744073709551616.0))))`,
	}
	for _, src := range srcs {
		_, err := invokeErr(t, src)
		if err == nil {
			continue
		}
		var tr *Trap
		if errors.As(err, &tr) {
			t.Errorf("%s\n\ttrapped: %v\n\tthe saturating truncations are total: a trap here is "+
				"the right answer on the wrong channel, and it converts assert_return vectors "+
				"into assert_trap answers", src, tr)
			continue
		}
		t.Errorf("%s: %v", src, err)
	}
}

// invokeErr instantiates and invokes the export "c", **returning** the invocation's error instead
// of failing on it.
//
// `invoke1` one file over requires success and `run1` calls `t.Fatalf`, so neither can be used by a
// row whose subject *is* the error — and `Fatalf` in a loop stops at the first row and hides the
// rest of the partition, which is how a table test silently shrinks to its first case.
func invokeErr(t *testing.T, src string) ([]Value, error) {
	t.Helper()
	in, trap := instantiate1(t, src)
	if trap != nil {
		t.Fatalf("instantiate: %v", trap)
	}
	if err := in.Deferred(); err != nil {
		t.Fatalf("instantiate fell short: %v", err)
	}
	return in.Invoke("c")
}

// TestPrefixedInstructionIsNotDispatchedByOpAlone is the structural hazard, and it is the one row
// in this file that is not about arithmetic.
//
// `Instr.Op` holds the **sub**-opcode, so `fc 00` and `unreachable` are both `Op == 0x00`. If the
// prefix gate in `exec.go` were removed — or if a later prefixed arm were added to the main switch
// instead of to `execFC` — then `i32.trunc_sat_f32_s` would execute as `unreachable` and trap, and
// `fc 0b` (`memory.fill`) would execute as `end`. Both are silent in the sense that matters: the
// module is valid, so no vector expects a diagnostic.
//
// Asserted in both directions, because one alone is satisfied by a wrong engine:
//
//   - `fc 00` must **not** trap — an engine that lost the prefix gate traps here;
//   - `unreachable` must **still** trap — an engine that "fixed" the above by making `Op == 0x00`
//     harmless would pass the first assertion and break the real `unreachable`.
//
// The pair is the point. A single-direction test here is the fixed-point defect: right answer and
// wrong answer coincide.
func TestPrefixedInstructionIsNotDispatchedByOpAlone(t *testing.T) {
	// fc 00 — same Op as unreachable, different instruction.
	out := run1(t, `(module (func (export "c") (result i32) `+
		`(i32.trunc_sat_f32_s (f32.const 1.5))))`)
	if len(out) != 1 || out[0].Bits != 1 {
		t.Errorf("i32.trunc_sat_f32_s(1.5) = %v, want a single i32 1; a trap would mean the "+
			"prefixed instruction reached the main switch and ran as `unreachable`", out)
	}
	// And the single-byte 0x00 still means what it means.
	_, err := invokeErr(t, `(module (func (export "c") (result i32) (unreachable)))`)
	var tr *Trap
	if !errors.As(err, &tr) {
		t.Fatalf("unreachable: got %v, want a trap", err)
	}
	if !strings.Contains(tr.Error(), "unreachable") {
		t.Errorf("unreachable trapped with %q, want the message to name unreachable", tr)
	}
}

// TestUnhandledFCSubOpcodeStaysOnTheWorkList moved to `bulk_test.go` when `fc 0b` gained an arm:
// its row had to be re-pointed, and the test belongs beside the change that inverted it. The
// tripwire is unchanged in what it names.
