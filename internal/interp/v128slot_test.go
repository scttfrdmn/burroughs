package interp

import (
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// v128Lanes packs four i32 lanes into the (hi, lo) slot pair decision 0024 stores a v128 as,
// little-endian: lanes 0 and 1 live in the low slot, lanes 2 and 3 in the high one.
//
// **Written out rather than reusing the engine's own packer on purpose.** A control that computes
// its expectation with the code under test agrees with itself — the repair-confirmed-by-the-
// authority rule — and the hi/lo *pairing* is precisely what grave #242's second half could get
// wrong, so the expected pair has to be derived independently of `pushV128`/`popV128`.
func v128Lanes(l0, l1, l2, l3 uint32) (hi, lo uint64) {
	lo = uint64(l1)<<32 | uint64(l0)
	hi = uint64(l3)<<32 | uint64(l2)
	return hi, lo
}

// TestV128KeepsBothSlotsAtEveryValueMovingSite is grave #242's control, and it is deliberately
// scoped to the *space* rather than to the two defects that were found.
//
// # The shape
//
// Decision 0024 stores a v128 in two adjacent `st.num` slots. Every site that *moves a value
// without being told its type* therefore has to recognize the pair, and there are exactly two
// ways to be wrong: move one slot where two were meant (the value arrives truncated, and the
// function ends up short of its own declared arity) or move two where the site's arithmetic
// expected one (halves are orphaned on the stack and the function ends up long). Grave #242 had
// both polarities, from two different sites:
//
//   - `blockArity`'s single-valtype arm returned 1 numeric slot for `(result v128)`, bypassing
//     `countByArray` — so `branch`, faithfully truncating to `height+arity`, dropped the high
//     half. 17 vectors, reported as `declares 2 numeric results and left 1 values`.
//   - `select`'s numeric arm popped three slots and pushed one, where a v128 select has five
//     operand slots — so both operands' high halves were stranded. 6 vectors, reported as
//     `left 3 values`.
//
// # Why a table over sites, not two tests
//
// Two tests would freeze the control at the moment of authorship, which is the blind spot the
// scope-controls-to-the-space rule names: the next construct to move a value (or the next arm to
// re-derive "one value is one slot") would be uncovered, and the failure mode is silent by
// construction — a lost half reads as an arity mismatch somewhere else entirely, which is exactly
// why this grave was invisible until someone partitioned the board by error site. So the rows are
// the *kinds of value movement the engine has*, whether or not each was broken: `br`, `br_if`,
// `br_table`, `return`, `select` in both directions, and `drop`. Four of the seven rows passed
// before the fix and are here to stay passing.
//
// # Why all four lanes, and why the lanes differ
//
// An arity-only assertion cannot tell "two slots moved" from "two slots moved in the wrong order":
// both leave the stack the right length. So every row checks all four i32 lanes against an
// independently computed (hi, lo) pair, and the lane values are four distinguishable constants
// rather than a splat — a splat is a fixed point of the transposition, so it would pass with the
// halves swapped, which is the protection-by-coincidence failure. `drop` and the two `select`
// rows additionally use *different* constants for the value that must survive and the value that
// must not, so that discarding the wrong one is a wrong answer rather than the same answer.
func TestV128KeepsBothSlotsAtEveryValueMovingSite(t *testing.T) {
	// The surviving value in every row, chosen so that all four lanes and both slots differ.
	const kept = `(v128.const i32x4 0x11111111 0x22222222 0x33333333 0x44444444)`
	// The discarded value, for the rows where something must be thrown away. Distinct in every
	// lane from `kept`, so a row that keeps the wrong one fails on lane values and not merely
	// on arity.
	const other = `(v128.const i32x4 0xaaaaaaaa 0xbbbbbbbb 0xcccccccc 0xdddddddd)`

	wantHi, wantLo := v128Lanes(0x11111111, 0x22222222, 0x33333333, 0x44444444)

	rows := []struct {
		name string
		// site names the value movement under test, for the failure message — a row that
		// fails should say which construct lost the slot, not just that a number was wrong.
		site string
		src  string
		args []Value
	}{
		{
			name: "br",
			site: "branch() truncating to height+arity",
			src: `(module (func (export "c") (result v128)
				(block (result v128) (br 0 ` + kept + `))))`,
		},
		{
			name: "br_if",
			site: "branch() via a taken conditional branch",
			src: `(module (func (export "c") (result v128)
				(block (result v128) (br_if 0 ` + kept + ` (i32.const 1)) (drop) ` + other + `)))`,
		},
		{
			name: "br_table",
			site: "branch() via br_table's selected label",
			src: `(module (func (export "c") (result v128)
				(block (result v128) (br_table 0 0 ` + kept + ` (i32.const 0)))))`,
		},
		{
			name: "return",
			site: "returnFrom()'s frame-relative truncation",
			src: `(module (func (export "c") (result v128)
				(return ` + kept + `)))`,
		},
		{
			name: "select-true",
			site: "select's v128 arm, condition true (keeps the first operand)",
			src: `(module (func (export "c") (param i32) (result v128)
				(select ` + kept + ` ` + other + ` (local.get 0))))`,
			args: []Value{{Type: binary.I32, Bits: 1}},
		},
		{
			name: "select-false",
			site: "select's v128 arm, condition false (keeps the second operand)",
			src: `(module (func (export "c") (param i32) (result v128)
				(select ` + other + ` ` + kept + ` (local.get 0))))`,
			args: []Value{{Type: binary.I32, Bits: 0}},
		},
		{
			name: "drop",
			site: "drop's v128 pair recognition (topIsV128)",
			src: `(module (func (export "c") (result v128)
				` + kept + ` ` + other + ` (drop)))`,
		},
	}

	// Vacuity floor: a comparison against an empty set succeeds, and a table this test reads
	// from its own literal could be emptied by an edit without any assertion noticing. Seven
	// rows are the seven value movements enumerated in the doc comment above.
	if len(rows) < 7 {
		t.Fatalf("table has %d rows, want at least 7 — the space this control claims to cover "+
			"is every value-moving site, and a shrunken table asserts less than its name", len(rows))
	}

	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			out := runSIMD1(t, r.src, r.args...)
			if len(out) != 1 {
				t.Fatalf("%s: got %d results, want 1: %+v — a v128 that lost a slot arrives "+
					"as the wrong arity, which is how grave #242 presented", r.site, len(out), out)
			}
			if out[0].Type != binary.V128 {
				t.Fatalf("%s: got type %s, want v128", r.site, out[0].Type)
			}
			if out[0].Bits != wantLo || out[0].Hi != wantHi {
				t.Errorf("%s: got (hi=%#016x lo=%#016x), want (hi=%#016x lo=%#016x)\n"+
					"\tlanes wanted 0x11111111 0x22222222 0x33333333 0x44444444;\n"+
					"\tequal-length-but-wrong is a transposed or wrongly-chosen pair, not a lost slot",
					r.site, out[0].Hi, out[0].Bits, wantHi, wantLo)
			}
		})
	}
}
