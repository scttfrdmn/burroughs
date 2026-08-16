// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package validate

import (
	"fmt"
	"strings"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// Slice 2 of #9: the 0xfd region, which drained 668 of the 1059 vectors slice 1 declined.
//
// Measured, not forecast, and the two differ: the pre-registered figure was 884, on a count of
// vectors whose *file* is a SIMD one. 668 is the count whose decline this region's typing actually
// removes, and the difference is vectors that decline for a second reason as well — chiefly the
// bulk-memory and reference instructions that share those files. 648 of the 668 became passes and
// **20 landed in the admission stratum**, every one of them the alignment gap this file's own
// non-goals section declares. The ledger is at `spec_test.go`'s `validateAdmitCeiling`, because a
// figure that moves belongs beside the bound that watches it.
//
// # The same argument as `signature`, one authority further along
//
// `sig.go` derives a numeric instruction's type from its mnemonic and says why at length: a
// hand-written row per opcode is an accept-direction error per row, and an accept-direction error
// is invisible on this board because an `assert_invalid` vector is satisfied by *any* refusal
// (contract §9's G-3). SIMD is that argument at four times the scale — 236 core opcodes — with one
// difference that makes the naive version worse rather than better: the numeric families can be
// classified by name because `add` is visibly binary, and the vector families cannot. Nothing in
// the string `i32x4_bitmask` says it returns an `i32` while `i32x4_neg` returns a `v128`, and
// nothing in `i16x8_extmul_low_i8x16_s` says it takes two operands where `i16x8_extend_low_i8x16_s`
// takes one. A reader classifying those by eye gets most of them right, and the ones they get
// wrong are exactly the ones no vector can catch.
//
// So the classification is not made here. It is **read from the reference**, which states it
// exactly: `syntax/mnemonics.ml` binds every mnemonic to a constructor family
// (`let i8x16_swizzle = VecBinary (V128 (I8x16 V128Op.Swizzle))`), and `valid/valid.ml` types each
// family in one arm (`| VecBinary binop -> ... [t; t] --> [t]`). Twenty families cover all 256
// rows of the region. `vecFamily` below is that binding, and `TestVecFamiliesMatchTheReference`
// parses `mnemonics.ml` and compares in both directions, so a wrong family is a build failure
// rather than a silent acceptance.
//
// # Why twenty hand-written arms are sound where 236 hand-written rows are not
//
// The family-to-signature step *is* transcribed, from valid.ml's twenty arms, each cited by line.
// That is a deliberate line and it is drawn where the ratio changes: 20 arms against 236 rows is
// an order of magnitude fewer chances to be wrong, every arm is three lines of the authority
// quoted beside it, and `TestVecSignaturesMatchTheReferencesArms` re-derives the arity of each
// from valid.ml's own `[t; t] --> [t]` text. What is *not* checkable that way is the lane-type and
// lane-count mapping, which is why `exec/v128.ml` is licensed too and why both of its six-row
// functions are checked against it (`TestLaneFactsMatchTheReference`).
//
// # What this slice does not do
//
// **Alignment is not checked, and it cannot be here.** The decoder deliberately drops the memarg's
// alignment (`decodeMemop`: "Alignment is a validation constraint ... so keeping it would be
// storing a fact only #9 reads"), so the 99 alignment vectors stay the separately-declared slice
// `validate.go` lists them as. A vector whose only defect is an over-aligned SIMD access is
// therefore **accepted** by this package, which is the admission stratum `validate.go` describes:
// reported as a fail with a named cause, never as a pass.
//
// **Relaxed SIMD is not typed.** `mnemonics.ml` gives it families like everything else, and the
// twenty arms would cover all twenty of its rows — but relaxed SIMD is its own proposal behind its
// own gate (`gateRelaxedSIMD`, `binary/gatemap.go`), and `gateFor` resolves `fd 0x100..0x12f` to
// it inside SIMD's region. With that gate off the decoder refuses those opcodes before validation
// sees them, so typing them here would be a rule with no reachable subject; with it on, a flip is
// its own stamp-tier event and not a line in a typing PR. The dispatch below therefore declines at
// `0x100` and says so, which keeps the decline census a work plan for the relaxed-SIMD gate rather
// than a silence.
//
// **Multi-memory addressing follows slice 1 exactly.** `addrType` reads memory 0's index type and
// a memop's own memory index is not consulted, here or in `sig.go`. That is one behaviour rather
// than two, deliberately: a divergence between how `i32.load` and `v128.load` resolve their
// address type would be a second rule wearing the first one's name.

// vecInstr types one 0xfd instruction and applies it to the operand stack.
//
// Its own method rather than a case in `instr`'s trailing `signature` call, because the two paths
// resolve their signature from different authorities and a shared tail would hide which one
// answered. The application itself is identical — pop the params, push the results — and that
// sameness is load-bearing: a vector instruction is not special to the typing algorithm, only to
// the table, which is 0002's internal form doing its job.
func (v *validator) vecInstr(in binary.Instr) error {
	s, err := vecSignature(v.mod, in)
	if err != nil {
		return err
	}
	if err := v.popExpectAll(s.params); err != nil {
		return err
	}
	v.pushAll(s.results)
	return nil
}

// vecShape is the lane shape a vector constructor carries — `I8x16`, `F64x2`, and so on — or the
// empty shape for the whole-register families (`VecLoad`, `VecUnaryBits`, `VecConst`), which the
// reference spells `V128 V128Op.Not` with no shape at all.
//
// A string rather than an enum because it is the reference's own token, compared against text
// parsed out of `mnemonics.ml`. Same reasoning as `binary`'s `imm` type: the table speaks the
// authority's language, and the mapping to this package's vocabulary happens at the point of use.
type vecShape string

const (
	shapeNone  vecShape = ""
	shapeI8x16 vecShape = "I8x16"
	shapeI16x8 vecShape = "I16x8"
	shapeI32x4 vecShape = "I32x4"
	shapeI64x2 vecShape = "I64x2"
	shapeF32x4 vecShape = "F32x4"
	shapeF64x2 vecShape = "F64x2"
)

// laneType is a shape's lane scalar type — `V128.type_of_lane` (exec/v128.ml:31).
//
// The three-shapes-to-one-type row is the reason this is read from the authority rather than
// written from memory: `I8x16`, `I16x8` and `I32x4` all have `i32` lanes, because a lane narrower
// than a machine word is extracted *into* an `i32`. The plausible wrong version gives `i8x16` an
// `i8`-ish lane type, and since Wasm has no `i8`, the plausible wrong version invents one.
func laneType(s vecShape) (binary.ValType, bool) {
	switch s {
	case shapeI8x16, shapeI16x8, shapeI32x4:
		return binary.I32, true
	case shapeI64x2:
		return binary.I64, true
	case shapeF32x4:
		return binary.F32, true
	case shapeF64x2:
		return binary.F64, true
	case shapeNone:
		// The whole-register families, spelled out rather than left to the fallthrough: an answer
		// here would give `v128.not` a lane type, and `exhaustive` asking for the case is the
		// linter agreeing that the shape-less arm is a decision and not an omission.
		return binary.ValType{}, false
	}
	return binary.ValType{}, false
}

// numLanes is a shape's lane count — `V128.num_lanes` (exec/v128.ml:22).
func numLanes(s vecShape) (uint64, bool) {
	switch s {
	case shapeI8x16:
		return 16, true
	case shapeI16x8:
		return 8, true
	case shapeI32x4, shapeF32x4:
		return 4, true
	case shapeI64x2, shapeF64x2:
		return 2, true
	case shapeNone:
		// As above: a lane count for a shape-less family bounds an index that does not exist.
		return 0, false
	}
	return 0, false
}

// prefixSIMD is the vector region's prefix byte.
//
// Local rather than exported from `internal/binary`, and that is a deliberate non-change:
// `binary` spells the prefix as a literal at every one of its own sites, so a shared exported
// constant would be a convention change across two packages, which is not slice 2's to make. Named
// here so this package has one place the byte is written, which is the part that was cheap.
// `TestPrefixSIMDIsTheRegionBinaryDispatches` checks it against `binary`'s own dispatch rather than
// leaving the two agreeing by inspection.
const prefixSIMD = 0xfd

// relaxedSIMDFirst is the first sub-opcode of the relaxed-SIMD range inside 0xfd.
//
// Named rather than spelled at the comparison, and derived from the same fact `binary`'s gate map
// uses (`fd 0x100..0x12f` → `gateRelaxedSIMD`): the boundary is a proposal's edge, so a literal
// `0x100` at a dispatch would be a claim about which proposal owns an opcode, typed somewhere
// other than where proposals are declared.
const relaxedSIMDFirst = 0x100

// vecSignature returns a 0xfd instruction's type, or a decline.
//
// Three-valued exactly as `signature` is, and for the same reason: `nil` means the type is known,
// `ErrUnsupported` means this slice has no rule for the opcode and it lands in a named bucket, and
// anything else is a rule that *is* known and that the module fails.
func vecSignature(m *binary.Module, in binary.Instr) (sig, error) {
	if in.Op >= relaxedSIMDFirst {
		// Relaxed SIMD's own gate, not this slice's rule. Unreachable while the gate is off — the
		// decoder refuses these opcodes first — and stated anyway, so the census names the gate
		// rather than reporting a typing gap.
		return sig{}, fmt.Errorf("%w: %s (%#02x %#02x) is relaxed SIMD, whose gate is its own event",
			ErrUnsupported, mnemonic(in), in.Prefix, in.Op)
	}
	name, _, ok := binary.PrefixedOp(in.Prefix, in.Op)
	if !ok {
		return sig{}, errNoVecSignature(in)
	}
	fam, ok := vecFamily[name]
	if !ok {
		return sig{}, errNoVecSignature(in)
	}

	v128 := binary.V128
	switch fam.family {
	// The whole-register arms, valid.ml:885-937. Every one of them is `t` = `v128` on both sides,
	// so the shape the constructor carries is not consulted — which is why the bits families carry
	// none.
	case "VecConst": // valid.ml:885 — [] --> [t]
		return sig{results: []binary.ValType{v128}}, nil

	case "VecTest", "VecBitmask", "VecTestBits": // valid.ml:889,918,922 — [t] --> [NumT I32T]
		return sig{params: []binary.ValType{v128}, results: []binary.ValType{binary.I32}}, nil

	case "VecUnary", "VecConvert", "VecUnaryBits": // valid.ml:893,910,926 — [t] --> [t]
		return sig{params: []binary.ValType{v128}, results: []binary.ValType{v128}}, nil

	case "VecBinary", "VecCompare", "VecBinaryBits": // valid.ml:897,906,930 — [t; t] --> [t]
		// `VecCompare` returning `t` rather than `i32` is the one arm most likely to be written
		// wrong from analogy: a *numeric* comparison returns `i32` (`sig.go`'s compareOps), and a
		// lanewise one returns a mask in a register. valid.ml:906-908 is quoted because the
		// analogy is the trap.
		if err := checkVecBinaryRule(in, fam); err != nil {
			return sig{}, err
		}
		return sig{params: []binary.ValType{v128, v128}, results: []binary.ValType{v128}}, nil

	case "VecTernary", "VecTernaryBits": // valid.ml:902,934 — [t; t; t] --> [t]
		return sig{params: []binary.ValType{v128, v128, v128}, results: []binary.ValType{v128}}, nil

	case "VecShift": // valid.ml:914 — [t; NumT I32T] --> [t]
		return sig{params: []binary.ValType{v128, binary.I32}, results: []binary.ValType{v128}}, nil

	// The lane arms, valid.ml:938-955, where the shape decides the scalar type and the lane
	// bound. A family that needs a lane type and carries no shape is a defect in the table rather
	// than a fact about the module, so it declines rather than guessing a type.
	case "VecSplat": // valid.ml:938 — [NumT lane] --> [VecT t]
		lane, ok := laneType(fam.shape)
		if !ok {
			return sig{}, errNoVecSignature(in)
		}
		return sig{params: []binary.ValType{lane}, results: []binary.ValType{v128}}, nil

	case "VecExtract": // valid.ml:943 — [VecT t] --> [NumT lane], lane index bounded
		lane, ok := laneType(fam.shape)
		if !ok {
			return sig{}, errNoVecSignature(in)
		}
		if err := checkLaneIndex(in, fam, in.Imm0); err != nil {
			return sig{}, err
		}
		return sig{params: []binary.ValType{v128}, results: []binary.ValType{lane}}, nil

	case "VecReplace": // valid.ml:950 — [VecT t; NumT lane] --> [VecT t], lane index bounded
		lane, ok := laneType(fam.shape)
		if !ok {
			return sig{}, errNoVecSignature(in)
		}
		if err := checkLaneIndex(in, fam, in.Imm0); err != nil {
			return sig{}, err
		}
		return sig{params: []binary.ValType{v128, lane}, results: []binary.ValType{v128}}, nil

	// The memory arms, valid.ml:663-686. The address type is a module fact, so these route
	// through the same `addrType` slice 1's loads and stores use — including its `unknown memory
	// 0` verdict, which is a rule and not a decline.
	case "VecLoad": // valid.ml:663 — [addr] --> [VecT t]
		addr, err := checkMemop(m, in, name)
		if err != nil {
			return sig{}, err
		}
		return sig{params: []binary.ValType{addr}, results: []binary.ValType{v128}}, nil

	case "VecStore": // valid.ml:668 — [addr; VecT t] --> []
		addr, err := checkMemop(m, in, name)
		if err != nil {
			return sig{}, err
		}
		return sig{params: []binary.ValType{addr, v128}}, nil

	case "VecLoadLane": // valid.ml:673 — [addr; VecT t] --> [VecT t], lane index bounded
		addr, err := checkMemop(m, in, name)
		if err != nil {
			return sig{}, err
		}
		if err := checkPackedLaneIndex(in, name); err != nil {
			return sig{}, err
		}
		return sig{params: []binary.ValType{addr, v128}, results: []binary.ValType{v128}}, nil

	case "VecStoreLane": // valid.ml:680 — [addr; VecT t] --> [], lane index bounded
		addr, err := checkMemop(m, in, name)
		if err != nil {
			return sig{}, err
		}
		if err := checkPackedLaneIndex(in, name); err != nil {
			return sig{}, err
		}
		return sig{params: []binary.ValType{addr, v128}}, nil
	}

	// A family the table carries and no arm handles. Declines rather than falling through to a
	// zero `sig`, which would type the instruction as `() -> ()` and *accept* it — the
	// accept-direction failure this whole file is arranged against, arriving through the one door
	// a `switch` leaves open.
	return sig{}, errNoVecSignature(in)
}

// errNoVecSignature is the region's decline, and it names the family as well as the mnemonic.
//
// The family is what makes the bucket a work item: `i16x8.extmul_low_i8x16_s (0xfd 0x9a)` says
// which instruction, and `VecConvert` says which of twenty arms is missing.
func errNoVecSignature(in binary.Instr) error {
	name, _, _ := binary.PrefixedOp(in.Prefix, in.Op)
	if fam, ok := vecFamily[name]; ok {
		return fmt.Errorf("%w: %s (%#02x %#02x), family %s has no signature arm",
			ErrUnsupported, mnemonic(in), in.Prefix, in.Op, fam.family)
	}
	return fmt.Errorf("%w: %s (%#02x %#02x) is in no vector family",
		ErrUnsupported, mnemonic(in), in.Prefix, in.Op)
}

// checkVecBinaryRule is `check_vec_binop` (valid.ml:373-378), which is the *only* extra condition
// any vector family carries beyond its signature — and it applies to exactly one instruction.
//
// `i8x16.shuffle`'s sixteen lane indices select from two concatenated registers, so each must be
// below 32 rather than below 16. The reference puts this inside the `VecBinary` arm rather than in
// a shuffle-shaped family of its own, and this function keeps that placement: a shuffle-only
// arm here would be a third classification of an instruction two authorities already classify.
//
// The mask is `Imm0`/`Imm1`, sixteen bytes little-endian, low lane in the low byte of `Imm0` —
// `immLane16`'s own layout, which matches `immV128`'s so that a mask and a constant are the same
// sixteen bytes read the same way.
func checkVecBinaryRule(in binary.Instr, fam vecFamilyRow) error {
	if fam.op != "Shuffle" {
		return nil
	}
	words := [2]uint64{in.Imm0, in.Imm1}
	for i := range 16 {
		lane := (words[i/8] >> (8 * (i % 8))) & 0xFF
		if lane >= 32 {
			// The reference's own text, and it is the same string `VecExtract`'s bound produces —
			// which is a fact about the authority rather than a shortcut here: 48 vectors expect
			// `invalid lane index` and none of them distinguishes which rule refused.
			return fmt.Errorf("%w: lane %d of %s selects %d, and a shuffle selects from two "+
				"registers (32 lanes)", ErrInvalidLaneIndex, i, mnemonic(in), lane)
		}
	}
	return nil
}

// checkLaneIndex is the `VecExtract`/`VecReplace` bound — `lane < num_lanes shape`
// (valid.ml:946,953).
//
// The second number read 952 until TestLaneIndexCitationsResolveToTheReferencesSites was written:
// :952 is `let t2 = NumT (type_vec_lane replaceop)`, the statement *before* the bound, so the
// citation sent a reader to the arm's type binding and looked checked. Off by one, in a comment
// whose whole job is to be followable.
func checkLaneIndex(in binary.Instr, fam vecFamilyRow, lane uint64) error {
	lanes, ok := numLanes(fam.shape)
	if !ok {
		// A lane bound with no shape to bound against. Refuses rather than passing, because the
		// alternative is a check that silently answers "in range" for every index.
		return errNoVecSignature(in)
	}
	if lane >= lanes {
		return fmt.Errorf("%w: %s selects lane %d of %d", ErrInvalidLaneIndex, mnemonic(in), lane, lanes)
	}
	return nil
}

// checkPackedLaneIndex is the `VecLoadLane`/`VecStoreLane` bound — `lane < vec_size t /
// packed_size pack` (valid.ml:676,683), which is 128 bits divided by the access width.
//
// The width comes from the mnemonic (`v128_load8_lane` → 8 bits → 16 lanes) rather than from a
// shape, because these families carry none: the reference stores the width in the memop's `pack`
// field, which the decoder does not retain. Reading it from the name is the same move `sig.go`
// makes for `i32.load8_u`, and the set is the eight rows `optable.go` declares.
//
// The lane index comes back through `binary.MemargLane`, the memarg word having three tenants
// since #306 (memory index, lane index, alignment exponent) and the packing rule belonging to the
// package that writes it.
func checkPackedLaneIndex(in binary.Instr, name string) error {
	width, ok := packedLaneWidth(name)
	if !ok {
		return errNoVecSignature(in)
	}
	lanes := 128 / width
	lane := uint64(binary.MemargLane(in.Imm1))
	if lane >= lanes {
		return fmt.Errorf("%w: %s selects lane %d of %d", ErrInvalidLaneIndex, mnemonic(in), lane, lanes)
	}
	return nil
}

// packedLaneWidth reads a lane access's width in bits out of its mnemonic.
//
// `v128_load8_lane`, `v128_store32_lane` and their six siblings. Derived from the name for
// `sig.go`'s reason and not enumerated as eight rows: the eight rows are the same fact eight
// times, and the failure mode of an enumeration is a row omitted rather than a rule wrong.
func packedLaneWidth(name string) (uint64, bool) {
	rest, ok := strings.CutSuffix(name, "_lane")
	if !ok {
		return 0, false
	}
	switch {
	case strings.HasSuffix(rest, "8"):
		return 8, true
	case strings.HasSuffix(rest, "16"):
		return 16, true
	case strings.HasSuffix(rest, "32"):
		return 32, true
	case strings.HasSuffix(rest, "64"):
		return 64, true
	}
	return 0, false
}
