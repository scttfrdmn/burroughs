// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package validate

import (
	"errors"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// Slice 2's behavioural witnesses — the vector region driven through the real path.
//
// # Why these exist beside the authority tests
//
// `vec_authority_test.go` checks the tables and the signatures against the reference, and every one
// of those checks calls `vecSignature` or a lane helper **directly**. That is the right shape for
// comparing a transcription to its authority, and it is also the shape that can be entirely green
// while nothing in the engine reaches the code it checks: a correct table behind a dispatch that
// never fires, or behind one that fires and then discards the result. The lesson is on file — a
// control can exercise the fix's helper while no path calls it — so the rows below go through
// `validated()`, which is wat → `text.EncodeModule` → `Decoder.DecodeModule` → `Module`. If the
// region dispatch in `instr.go` were reverted to a decline, the authority tests would all still
// pass and every row here would fail.
//
// # The accept direction is first, because the board has no half for it
//
// All 648 vectors slice 2 converts are `assert_invalid` rows, and an `assert_invalid` row is
// satisfied by *any* refusal (contract §9 G-3). So a wrong signature that makes this package refuse
// a **valid** vector module is invisible on the board — it does not show up as a fail, it shows up
// as a vector nobody wrote. Those rows are the ones no other instrument in this project can host.

// TestVecAcceptsValidModules is the accept direction for the vector region.
//
// Each row is a module the spec accepts, chosen because a plausible wrong signature rejects it. The
// three noted in `vec.go` as the likely transcription errors are all here: `VecCompare` returning a
// register rather than an `i32`, `VecShift`'s scalar count, and `VecBitmask` returning an `i32`
// rather than a register.
func TestVecAcceptsValidModules(t *testing.T) {
	for _, c := range []struct {
		name string
		why  string
		wat  string
	}{
		{
			name: "a lanewise compare yields a register, not an i32",
			why: "the analogy vec.go names as the trap: `i32.eq` returns an i32, so `i32x4.eq` " +
				"looks like it should too. It returns a lane mask in a v128, and typing it the " +
				"numeric way rejects this module while passing every reject-direction vector",
			wat: `(module (func (result v128)
				(i32x4.eq (v128.const i32x4 0 0 0 0) (v128.const i32x4 0 0 0 0))))`,
		},
		{
			name: "a shift takes a v128 and an i32",
			why: "the count is a *scalar*, not a second register. `[t; NumT I32T] --> [t]` " +
				"(valid.ml:914) is the only arm mixing the two, so a signature written from the " +
				"binary-operator shape takes two registers and refuses this",
			wat: `(module (func (result v128)
				(i32x4.shl (v128.const i32x4 1 2 3 4) (i32.const 1))))`,
		},
		{
			name: "bitmask yields an i32, not a register",
			why: "the reverse of the compare trap, and the one the file header uses to make the " +
				"point that a mnemonic cannot be classified by eye: `i32x4.bitmask` and " +
				"`i32x4.neg` are the same shape as names and different signatures",
			wat: `(module (func (result i32)
				(i32x4.bitmask (v128.const i32x4 0 0 0 0))))`,
		},
		{
			name: "any_true yields an i32 from a register",
			why: "VecTestBits, whose arm has no shape at all — a signature that consulted a shape " +
				"here would decline for want of one",
			wat: `(module (func (result i32) (v128.any_true (v128.const i64x2 0 0))))`,
		},
		{
			name: "bitselect takes three registers",
			why: "VecTernaryBits is core SIMD's only three-operand instruction, so it is the whole " +
				"of that arm's subject — and the sentence that used to stand here said VecTernary " +
				"had *no* reachable subject, which is the claim grave #427 was: the lanewise " +
				"ternary family is relaxed SIMD, and relaxed SIMD has been reachable since the flip",
			wat: `(module (func (result v128) (v128.bitselect
				(v128.const i32x4 0 0 0 0) (v128.const i32x4 0 0 0 0) (v128.const i32x4 0 0 0 0))))`,
		},
		{
			// The four relaxed rows below are grave #427's accept direction, and they are the half
			// that needed writing: the board's eight `module text declined` rows are already the
			// reject-shaped evidence, and they went green the moment the guard came out. What no
			// board row can show is a relaxed instruction *wrongly refused* once it is typed, which
			// is the same §9 G-3 argument the header makes for the region as a whole.
			//
			// One row per family that carries relaxed rows — `VecTernary` 9, `VecBinary` 7,
			// `VecConvert` 4, twenty in total — rather than one row per opcode: the families are
			// what `vecSignature` dispatches on, so a per-opcode table would be the same assertion
			// twenty times while `TestVecFamiliesMatchTheReference` already binds each opcode to its
			// family against `mnemonics.ml`.
			name: "relaxed_madd takes three registers — VecTernary's first reachable subject",
			why: "the arm that had no witness at all while the guard stood: `[t; t; t] --> [t]` " +
				"(valid.ml:902), and the plausible wrong reading is the *bits* ternary's, which is " +
				"the same signature and would have hidden a missing arm rather than a wrong one",
			wat: `(module (func (result v128) (f32x4.relaxed_madd
				(v128.const f32x4 1 1 1 1) (v128.const f32x4 2 2 2 2) (v128.const f32x4 3 3 3 3))))`,
		},
		{
			name: "relaxed_swizzle takes two registers — the opcode the deleted guard keyed on",
			why: "`fd 0x100` exactly, `relaxedSIMDFirst` itself, so this row is the boundary the " +
				"guard tested written as a module that must now validate",
			wat: `(module (func (result v128) (i8x16.relaxed_swizzle
				(v128.const i8x16 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0)
				(v128.const i8x16 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0))))`,
		},
		{
			name: "relaxed_trunc yields a register — VecConvert, not a lane extraction",
			why: "`[t] --> [t]` (valid.ml:910): the name says `trunc_f32x4_s` and a signature " +
				"written from the *numeric* `i32.trunc_f32_s` would take and return scalars",
			wat: `(module (func (result v128)
				(i32x4.relaxed_trunc_f32x4_s (v128.const f32x4 1 1 1 1))))`,
		},
		{
			name: "relaxed_dot_add takes three registers where its two-operand sibling takes two",
			why: "the one relaxed pair that splits across two arms — `i16x8.relaxed_dot_…` is " +
				"VecBinary and `i32x4.relaxed_dot_…_add_s` is VecTernary — so a table keyed on the " +
				"`relaxed_dot` prefix rather than on the reference's families gets one of them wrong",
			wat: `(module (func (result v128) (i32x4.relaxed_dot_i8x16_i7x16_add_s
				(v128.const i8x16 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0)
				(v128.const i8x16 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0)
				(v128.const i32x4 0 0 0 0))))`,
		},
		{
			name: "extract_lane yields the shape's lane type",
			why: "the three-shapes-to-one-type row `laneType` is read from the authority for: an " +
				"`i8x16` lane extracts into an *i32*, and Wasm has no narrower integer to return",
			wat: `(module (func (result i32) (i8x16.extract_lane_s 15 (v128.const i8x16 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0))))`,
		},
		{
			name: "an f64x2 lane extracts as f64",
			why: "the same rule on the other side of the integer/float split, where a shape-blind " +
				"signature would return i32 for everything",
			wat: `(module (func (result f64) (f64x2.extract_lane 1 (v128.const f64x2 0 0))))`,
		},
		{
			name: "replace_lane takes the lane type and yields a register",
			why: "the asymmetric arm — `[VecT t; NumT lane] --> [VecT t]` (valid.ml:950) — where " +
				"the operand order is the trap: register first, scalar second",
			wat: `(module (func (result v128) (i16x8.replace_lane 7
				(v128.const i16x8 0 0 0 0 0 0 0 0) (i32.const 1))))`,
		},
		{
			name: "splat takes the lane type",
			why: "the mirror of extract: `f32x4.splat` takes an f32, and a shape-blind arm would " +
				"demand a register or an i32",
			wat: `(module (func (result v128) (f32x4.splat (f32.const 1))))`,
		},
		{
			name: "shuffle at the top of the two-register range",
			why: "lane 31 is *in* bounds because a shuffle selects from two concatenated " +
				"registers; bounding it at 16 like an extract refuses this valid module and still " +
				"passes every `invalid lane index` vector",
			wat: `(module (func (result v128) (i8x16.shuffle 31 30 29 28 27 26 25 24 23 22 21 20 19 18 17 16
				(v128.const i8x16 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0)
				(v128.const i8x16 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0))))`,
		},
		{
			name: "a load takes an address and yields a register",
			why: "the memory arms route through `addrType`, so this is also the row that fails if " +
				"the vector path resolves an address differently from `i32.load`",
			wat: `(module (memory 1) (func (result v128) (v128.load (i32.const 0))))`,
		},
		{
			name: "a store takes an address and a register and yields nothing",
			why: "VecStore's empty result list — an arm written from the load's shape leaves a " +
				"register on the stack and the body fails at `end` instead",
			wat: `(module (memory 1) (func (v128.store (i32.const 0) (v128.const i32x4 0 0 0 0))))`,
		},
		{
			name: "load8_lane at the top of its 16-lane range",
			why: "the packed bound is `128 / access width`, read from the mnemonic because the " +
				"decoder drops the reference's `pack` field — so lane 15 is in bounds for an " +
				"8-bit access and a bound taken from a shape would have no shape to take",
			wat: `(module (memory 1) (func (result v128) (v128.load8_lane 15
				(i32.const 0) (v128.const i8x16 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0))))`,
		},
		{
			name: "store64_lane at the top of its 2-lane range",
			why: "the same bound at the other end of the width range: a 64-bit access has two " +
				"lanes, so lane 1 is the last legal one and a fixed 16 would accept lane 5",
			wat: `(module (memory 1) (func (v128.store64_lane 1
				(i32.const 0) (v128.const i64x2 0 0))))`,
		},
		{
			name: "a vector value crosses a block boundary",
			why: "0024 makes a v128 two slots, so the arity bookkeeping this pass hands forward " +
				"has to count slots and not values — a row that exercises the region *and* the " +
				"frame accounting together",
			wat: `(module (func (result v128)
				(block (result v128) (v128.const i32x4 1 2 3 4))))`,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := validated(t, c.wat, nil); err != nil {
				t.Errorf("valid module refused: %v\nwhy this row exists: %s\n%s", err, c.why, c.wat)
			}
		})
	}
}

// TestVecRejectsWithTheRuleThatRefused is the reject direction, keyed on the wrapped detail.
//
// The suite already establishes that these modules are refused — that is what the 648 converted
// vectors are. What it cannot establish is **which rule refused**, because all five of the
// reference's lane-index sites produce the identical `invalid lane index` and 84.3% of the corpus
// expects the bare `type mismatch`. So each row names the bound it means to trip, and the assertion
// is on the `%w`-wrapped text only that bound produces.
func TestVecRejectsWithTheRuleThatRefused(t *testing.T) {
	for _, c := range []struct {
		name   string
		why    string
		wat    string
		is     error
		detail string
	}{
		{
			name: "extract_lane past the shape's lane count",
			why:  "the VecExtract bound, valid.ml:946 — lane 16 of an i8x16",
			wat: `(module (func (result i32) (i8x16.extract_lane_s 16
				(v128.const i8x16 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0))))`,
			is:     ErrInvalidLaneIndex,
			detail: "selects lane 16 of 16",
		},
		{
			name: "replace_lane past a narrow shape's count",
			why: "the VecReplace bound, valid.ml:953 — the arm whose citation was off by one, so " +
				"this row is also the reason that citation is now checked",
			wat: `(module (func (result v128) (i64x2.replace_lane 2
				(v128.const i64x2 0 0) (i64.const 1))))`,
			is:     ErrInvalidLaneIndex,
			detail: "selects lane 2 of 2",
		},
		{
			name: "a shuffle lane past the two-register range",
			why: "`check_vec_binop`, valid.ml:373-378 — the *only* extra condition any vector " +
				"family carries, and it applies to exactly one instruction",
			wat: `(module (func (result v128) (i8x16.shuffle 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 32
				(v128.const i8x16 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0)
				(v128.const i8x16 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0))))`,
			is: ErrInvalidLaneIndex,
			// The index within the mask, which is what makes this message worth more than the
			// sentinel: sixteen lanes are checked and the message says which one.
			detail: "lane 15 of i8x16_shuffle selects 32",
		},
		{
			name: "load32_lane past its packed lane count",
			why: "the `check_memop` bound, valid.ml:676 — 128 bits over a 32-bit access is four " +
				"lanes, and the width comes from the mnemonic because the decoder drops `pack`",
			wat: `(module (memory 1) (func (result v128) (v128.load32_lane 4
				(i32.const 0) (v128.const i32x4 0 0 0 0))))`,
			is:     ErrInvalidLaneIndex,
			detail: "selects lane 4 of 4",
		},
		{
			name: "a compare's result used as an i32",
			why: "the accept-direction trap's reject half: if the signature were written the " +
				"numeric way this module would be *accepted*, so the row is a witness in the " +
				"direction the board can see for a defect it cannot",
			wat: `(module (func (result i32)
				(i32x4.eq (v128.const i32x4 0 0 0 0) (v128.const i32x4 0 0 0 0))))`,
			is:     ErrTypeMismatch,
			detail: "instruction requires [i32] but stack has [v128]",
		},
		{
			name: "a shift given a register as its count",
			why: "the other half of the shift row: two registers is the plausible wrong " +
				"signature, and under it this module validates",
			wat: `(module (func (result v128)
				(i32x4.shl (v128.const i32x4 0 0 0 0) (v128.const i32x4 1 1 1 1))))`,
			is:     ErrTypeMismatch,
			detail: "instruction requires [v128 i32] but stack has [v128 v128]",
		},
		{
			name: "a load with no memory in the module",
			why: "`addrTypeAt`'s `unknown memory 0` — a *rule*, not a decline, and the row that " +
				"pins the vector path onto slice 1's address resolution rather than a second copy",
			wat:    `(module (func (result v128) (v128.load (i32.const 0))))`,
			is:     ErrUnknownMemory,
			detail: "unknown memory 0",
		},
		{
			name: "a splat given the wrong lane type",
			why: "the shape-derived operand type: `f64x2.splat` takes an f64, so an i64 is a " +
				"mismatch — and a shape-blind arm taking a register would refuse the *valid* form " +
				"instead and pass this one for the wrong reason",
			wat:    `(module (func (result v128) (f64x2.splat (i64.const 0))))`,
			is:     ErrTypeMismatch,
			detail: "instruction requires [f64] but stack has [i64]",
		},
		{
			// Grave #427's reject direction, and the reason it is one row rather than four: what the
			// deleted guard did was refuse *every* relaxed module, which means it also refused every
			// invalid one — with `ErrUnsupported` instead of a rule. So the accept rows above are
			// what the repair earns and this row is what it *risks*: a relaxed range that types by
			// falling through core SIMD's path could fall through to an accept, which is the door
			// `vecSignature`'s trailing decline exists to close and the one no board row can watch.
			name: "a relaxed ternary given a scalar for its third operand",
			why: "`f32x4.relaxed_madd` is `[t; t; t] --> [t]`, so an i32 in the third position is a " +
				"mismatch — and under the guard this module was refused for the wrong reason, which " +
				"is indistinguishable from this one on any board",
			wat: `(module (func (result v128) (f32x4.relaxed_madd
				(v128.const f32x4 1 1 1 1) (v128.const f32x4 2 2 2 2) (i32.const 3))))`,
			is:     ErrTypeMismatch,
			detail: "instruction requires [v128 v128 v128] but stack has [v128 v128 i32]",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := validated(t, c.wat, nil)
			if err == nil {
				t.Fatalf("invalid module accepted — and an accepted-but-invalid module is the "+
					"failure no `assert_invalid` vector can report (§9 G-3)\nwhy this row "+
					"exists: %s\n%s", c.why, c.wat)
			}
			if !errors.Is(err, c.is) {
				t.Errorf("refused with %v, want the %v family\nwhy: %s", err, c.is, c.why)
			}
			// The detail is the whole point of the row: the sentinel is shared by thousands of
			// vectors and by all five lane-index sites, so a witness that stops at the sentinel
			// asserts only that something refused.
			if !strings.Contains(err.Error(), c.detail) {
				t.Errorf("refused with %q, which does not name the rule — want a message "+
					"containing %q\nwhy: %s", err, c.detail, c.why)
			}
		})
	}
}

// TestVecDeclinesWhatThisSliceDoesNotType is the decline direction, and it is a census not a pass.
//
// A decline is `ErrUnsupported`, which the harness scores as a **fail with a named cause** rather
// than as a pass — so the rows here assert that what slice 2 leaves out says so, in words that name
// the region or the slice rather than reporting an anonymous typing gap.
//
// **What it leaves out is now one thing rather than two**, and the count falling is the finding
// rather than a tidying: relaxed SIMD was the other member and it was never a decline anyone had
// decided on (#427). The population this test asserts over is therefore the unclaimed *prefix*
// regions, which is a fact about the phase ladder — 0xFE is threads', a v1 milestone — and not about
// a gate whose state can move underneath the sentence.
func TestVecDeclinesWhatThisSliceDoesNotType(t *testing.T) {
	// **Relaxed SIMD used to be this census's first row and is now its inverse**, kept here rather
	// than deleted because a census that loses a member silently cannot report that the member left.
	// The row read: *"vecSignature typed a relaxed-SIMD opcode; the flip is its own event, so typing
	// it here would land a proposal's capability in a typing PR"* — and its comment asserted the arm
	// was unreachable because the decoder refuses these with the gate off. The gate had been on for a
	// day when that was written (`7315b57`, #275/ADR 0028) and the board was reaching the arm eight
	// times a run. Grave #427.
	//
	// So the assertion flips: `relaxedSIMDFirst` must **type**, and it is asserted here — at the
	// boundary the deleted guard keyed on — rather than only through `validated()` above, because
	// what went wrong was a claim about this function's dispatch and this is the direct question.
	if _, err := vecSignature(nil, instrAt(relaxedSIMDFirst)); err != nil {
		t.Errorf("relaxedSIMDFirst (%#x) declined with %v; the range is typed since #427, and a "+
			"decline here is either the guard restored or a family that lost its arm — check "+
			"`vecFamily`'s 20 relaxed rows before re-adding anything", relaxedSIMDFirst, err)
	}

	// The prefixed regions that are still someone else's stay declined, and the message names the
	// opcode so the bucket is a work item.
	//
	// **Re-pointed by slice 5, then re-pointed and re-mechanised by slice 7.** This row's specimen
	// was `memory.init` — `0xfc 0x08`, "whose slice is not this one" — and slice 5 was that slice;
	// it moved to `ref.i31` (`0xfb 0x1c`) under the note that GC's region was still unclaimed, and
	// slice 7 is that slice. The risk it was filed against is *a prefixed region falling through to
	// an accept*, which is a property of the dispatch and not of any one region, so the row follows
	// the boundary rather than closing with its second specimen.
	//
	// What changed with the second re-point is the *mechanism*, because 0xFE is the last unclaimed
	// region and the paragraph this one replaces had already recorded why no module can carry it:
	// the text encoder has no operator for the region ("unknown operator memory.atomic.notify"). So
	// this row now drives the dispatch directly, exactly as the relaxed-SIMD row above it does and
	// for the same reason — the specimen is unreachable through `validated()` by design, and the
	// layering is named instead of worked around. `TestPrefixedRegionsPartitionIntoClaimedAndDeclined`
	// remains the control scoped to the whole space; this row is the one witness inside slice 2's own
	// file, which is what keeps the boundary visible to whoever reads this file next.
	v := &validator{mod: &binary.Module{}}
	v.frames = []frame{{}}
	err := v.instr(0, binary.Instr{Prefix: 0xfe, Op: 0})
	if err == nil {
		t.Fatal("a threads instruction validated; the regions no slice has claimed must decline " +
			"rather than fall through to an accept")
	}
	if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "0xfe") {
		t.Errorf("the 0xFE region declined with %q, want an ErrUnsupported naming the prefix", err)
	}
}

// instrAt is a bare vector instruction at one sub-opcode, for the arms no module can reach.
func instrAt(op uint32) binary.Instr {
	return binary.Instr{Prefix: prefixSIMD, Op: op}
}
