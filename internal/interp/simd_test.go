package interp

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// runSIMD1 is run1 with the SIMD gate pinned on at the call site.
//
// It was written when the gate was default-*off* and its arms were unreachable without this
// helper; #227/ADR 0025 has since flipped SIMD default-on, so `DefaultFeatures` now carries
// `SIMD: true` and plain `run1` would reach these arms too. The helper is kept, and the pin is
// the reason: `DefaultFeatures` is a per-flip moving target by construction (its own doc comment
// says so — "diverges one field at a time as gates flip default-on"), so a rung that asserts a
// SIMD arm should say which gate it needs rather than inherit whatever the default happens to be
// this milestone. Stated historically because the original sentence outlived its fact: it still
// read "the gate stays default-off" for two PRs after the flip, which is a citation to a policy
// that had been superseded.
func runSIMD1(t *testing.T, src string, args ...Value) []Value {
	t.Helper()
	return runSIMDFeatures1(t, binary.Features{SIMD: true}, src, args...)
}

// runSIMDFeatures1 is runSIMD1 with an explicit feature set, for the one row
// (TestSIMDLoadStore's explicit-second-memory case) that also needs MultiMemory on.
func runSIMDFeatures1(t *testing.T, features binary.Features, src string, args ...Value) []Value {
	t.Helper()
	img, err := text.EncodeModule([]byte(src))
	if err != nil {
		t.Fatalf("encode %s: %v", src, err)
	}
	d := &binary.Decoder{Features: features}
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

// TestUnhandledFDSubOpcodesStayOnTheWorkList is **#429**: the re-pointing declared by grave #428
// and deliberately not made there.
//
// # The risk, which is #33's ruling applied for the second time
//
// A tripwire names a risk, not a code shape. The risk
// `TestUnhandledFCSubOpcodeStaysOnTheWorkList` was written for is that an unhandled sub-opcode
// stops rendering as `<prefix> NN` and collapses either into one bucket for the whole region or
// into a bare `NN` that reads as a single-byte opcode. The board's fail buckets are keyed by that
// message and the work list is read off them, so a change that erased the partition would erase
// the schedule.
//
// `execFC`'s region is drained — all 18 sub-opcodes answered — so over there the risk survives only
// as a format pin on an input the decoder can never admit. `0xfd` is where the risk still has a
// *subject*: `execFD`'s own header states the property this test checks ("unhandled sub-opcodes
// fall through to `unsupported`, rendering as `fd NN` — the board's existing bucket key"), and
// *the defect stated as the rule* is exactly the shape where a header asserting a property is what
// makes review confirm its absence. This is the assertion that header was missing.
//
// # Why a derived sweep and not 19 direct calls
//
// #429 named two shapes and this is the second. A call per unhandled arm would pin the format just
// as well and would inherit today's blind spot: the population drains as SIMD families land, so a
// test naming `fd 9a` goes stale the same way the `fc 0b` row did, and the treadmill that produced
// #428 starts again. The domain here is derived from `binary.PrefixedOp` — every `0xfd` row the
// decoder's own table admits — so it moves on its own as the table does. *Scope controls to the
// space, never to the sample.*
//
// The literal `fd ` in `want` is hand-written rather than derived from `unsupported`, which is the
// half that keeps this from checking a formatter against itself: the collapse worth catching is a
// message that drops the prefix, and a test that asked `unsupported` how it renders the prefix
// could not see it. What it cannot see is `%x` in place of `%02x`, since no currently-unhandled
// sub-opcode is below `0x10` — stated because a reader would otherwise assume the format is pinned
// whole.
//
// # The three ways it dies quietly, each with its own guard
//
//   - **The population drains to zero.** Then every message check is quantified over nothing and
//     the sweep is a green that asked no questions — *a comparison against an empty set succeeds*.
//     `len(unhandled) > 0` is the floor, and its message says re-point rather than delete, because
//     at that point the risk has dissolved for a second time and #33's ruling is what it was.
//   - **The domain stops being read.** A `PrefixedOp` that lost its `0xfd` arm, or a scan that
//     never entered the region, yields an empty domain and the same clean zero one layer up.
//     `inTableFloor` catches it, well below the measured 275 so corpus-independent table growth
//     does not fail a test about message format.
//   - **The table grows past the scan.** `scanCeiling` is a range *claim* — that no `0xfd` row
//     lives above it — of exactly the kind an iota block or a map gives no way to ask, so it is
//     asserted the way `kindCount`'s probe-above-the-end asserts the other one: the last row found
//     must sit at least `headroom` below the ceiling, so a region that grows toward the bound fails
//     here and asks for a re-base instead of silently leaving its top unswept.
//
// Watched dying on five mutations, and the point of listing them is that no two share a guard:
// rendering the sub-opcode without its prefix (`unsupported`'s `%02x %02x` → `%02x` on `Op`
// alone) fails the message check on all 19; dropping the bytes entirely fails it the same way with
// a different diff; `execFD`'s `default` returning `nil` empties the population and trips the
// floor; making `PrefixedOp` refuse `0xfd` trips `inTableFloor`; and lowering `scanCeiling` to
// `0x120` — above the table's real top, so the sweep still finds every row — trips the headroom
// check, which is the one mutation the other four are all blind to.
func TestUnhandledFDSubOpcodesStayOnTheWorkList(t *testing.T) {
	const (
		// A little over twice the region's top row (`0x113`), so the sweep covers the table with
		// room for the relaxed-SIMD tail to grow into.
		scanCeiling = 0x200
		// The gap the top row must keep below the ceiling. 0x40 rather than 1 because a region
		// grows a family at a time, and a bound that fails only once something has already gone
		// unswept is a bound that reports the damage rather than preventing it.
		headroom = 0x40
		// The vacuity floor on the domain, well below the measured 275.
		inTableFloor = 200
	)

	inTable, handled := 0, 0
	var unhandled []uint32
	lastInTable := uint32(0)
	for op := range uint32(scanCeiling) {
		if _, _, ok := binary.PrefixedOp(0xfd, op); !ok {
			continue
		}
		inTable++
		lastInTable = op

		err := execFDOnAnEmptyStack(op)
		if !errors.Is(err, ErrUnsupportedOp) {
			// The arm exists: it returned nil, or a trap, or the `needNum` validation verdict every
			// arm opens with, or it panicked reaching for state a bare `&Instance{}` does not have.
			// Which of those it was is not this test's question — anything other than
			// `ErrUnsupportedOp` means the sub-opcode is off the work list.
			handled++
			continue
		}
		unhandled = append(unhandled, op)
		want := fmt.Sprintf("fd %02x", op)
		if got := err.Error(); !strings.Contains(got, want) {
			t.Errorf("fd %02x: message is %q, want it to name %q — the board's fail buckets are "+
				"keyed by this string and the work list is read off them, so %02x alone would read "+
				"as a single-byte opcode and a message without the bytes would collapse 275 "+
				"sub-opcodes into one bucket", op, got, want, op)
		}
	}

	if inTable < inTableFloor {
		t.Fatalf("the scan found %d rows under 0xfd, want at least %d — `binary.PrefixedOp` is not "+
			"answering for this region, so every message check above was quantified over nothing "+
			"and this test is a clean zero rather than a pass", inTable, inTableFloor)
	}
	if lastInTable+headroom >= scanCeiling {
		t.Errorf("the region's top row is fd %02x, within %#x of the scan's ceiling %#x — rows at "+
			"or above the ceiling are unswept and this test would not say so. Raise scanCeiling",
			lastInTable, headroom, scanCeiling)
	}
	if len(unhandled) == 0 {
		t.Errorf("every one of the %d sub-opcodes under 0xfd has an arm, so this tripwire's subject "+
			"has dissolved for the second time (`fc` was the first, grave #428). **Re-point it, do "+
			"not delete it**: a tripwire names a risk and the risk — an unhandled sub-opcode losing "+
			"its `<prefix> NN` rendering and with it the board's partition — belongs to whichever "+
			"prefixed region still has unanswered rows (0xfb next). That is #33's ruling, and "+
			"closing this as no longer applicable would retire a live risk", inTable)
	}
	t.Logf("0xfd: %d rows in the decoder's table, %d with arms, %d still on the work list: %s",
		inTable, handled, len(unhandled), fdOpList(unhandled))
}

// execFDOnAnEmptyStack asks `execFD` what it does with a sub-opcode, on a zero `Instance` and an
// empty stack, and reports a panic as an ordinary error.
//
// The recover is not defensive coding, it is the classification: an arm that reaches for memory,
// a table, or a stack slot that is not there has *demonstrated it exists*, which is the only fact
// this sweep needs from it. Letting the panic out would make a test about message formatting fail
// on the first arm that happens to dereference something — and skipping the arms that panic would
// mean choosing the domain by hand again, which is what #429 exists to avoid.
func execFDOnAnEmptyStack(op uint32) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("arm panicked on an empty stack: %v", r)
		}
	}()
	return (&Instance{}).execFD(binary.Instr{Prefix: 0xfd, Op: op}, &stack{})
}

// fdOpList renders the work list the way `execFD`'s header and #429 both write it, so the logged
// set can be pasted into either.
func fdOpList(ops []uint32) string {
	var b strings.Builder
	for i, op := range ops {
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%02x", op)
	}
	return b.String()
}
