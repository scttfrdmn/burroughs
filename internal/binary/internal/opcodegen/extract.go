// Package opcodegen extracts the opcode immediate-shape table from the reference
// interpreter's decoder source.
//
// # Why this exists
//
// Decision 0007. The table is ~500 facts of the form "byte 0x28 takes a memarg", and
// the spec suite cannot check them: every vector bearing on the table is
// assert_malformed, so a table that wrongly *rejects* a valid opcode is invisible on
// the board by construction (contract §9 G-3). Accept-direction facts need the
// objective's other representation, which is the reference interpreter — the thing the
// suite's expected strings were minted by. Hand-transcription is not on the menu: this
// repo's measured rate is seven wrong citations in twelve hand-written items.
//
// So the authority is read by a machine. This package parses
// third_party/spec/interpreter/binary/decode.ml and emits the table; the generated
// file is committed, and a check re-runs the extraction and asserts equality, which
// makes reference drift a build failure rather than a diff nobody ordered.
//
// # Why parsing OCaml text is acceptable here
//
// It is the weak point of the mechanism and it is bounded deliberately. The extractor
// **never skips a line it does not understand** — an unrecognized arm is a hard error,
// not an omission. That inverts the failure mode: an upstream refactor breaks the
// build loudly instead of quietly shrinking the table, which is the whole difference
// between this and a careful reading. See errUnrecognized and, for the failure that
// error cannot catch, Extract's vacuity check.
package opcodegen

import (
	"errors"
	"regexp"
)

// Imm is one immediate operand's reader, named as the reference names it.
//
// The names are the reference's own (`idx`, `memop`, `laneidx`) rather than
// Burroughs' — this table is a transcription of an authority, so it speaks the
// authority's vocabulary and the translation to Burroughs' readers happens at the
// point of use. A renaming here would be an editorial change to evidence.
type Imm string

// The immediate vocabulary. Closed deliberately: a reader not in this set is an
// unrecognized arm, which is an error. Adding one is a deliberate act with a diff.
const (
	ImmIdx        Imm = "idx"         // a u32 index (type, func, table, local, label…)
	ImmMemop      Imm = "memop"       // u32 flags (align in low 6 bits, 0x40 = memidx) + u64 offset
	ImmLaneIdx    Imm = "laneidx"     // a single byte lane index
	ImmByte       Imm = "byte"        // a raw byte (br_on_cast flags)
	ImmU32        Imm = "u32"         // a bare u32
	ImmS32        Imm = "s32"         // sleb32 (i32.const)
	ImmS64        Imm = "s64"         // sleb64 (i64.const)
	ImmF32        Imm = "f32"         // 4 raw bytes
	ImmF64        Imm = "f64"         // 8 raw bytes
	ImmV128       Imm = "v128"        // 16 raw bytes
	ImmValType    Imm = "valtype"     // a value type byte
	ImmHeapType   Imm = "heaptype"    // a heap type (sleb33)
	ImmBlockType  Imm = "blocktype"   // 0x40 | valtype | sleb33 type index
	ImmVecValType Imm = "vec_valtype" // vec(valtype) — select's type list
	ImmVecIdx     Imm = "vec_idx"     // vec(idx) — br_table's label list
	ImmCatchVec   Imm = "vec_catch"   // vec(catch) — try_table's handler list
	ImmBlock      Imm = "block"       // a nested instruction sequence terminated by END
	// ImmLane16 is sixteen lane bytes, i8x16_shuffle's mask (decode.ml:699, the only
	// `repeat n f s` in the decoder). It is its own vocabulary entry rather than
	// sixteen ImmLaneIdx because the count is the fact: the first print-check reported
	// this arm as a *single* laneidx, silently losing fifteen bytes — exactly the
	// shifts-every-subsequent-byte failure 0007 exists to prevent, and invisible on any
	// board because every vector bearing on it is assert_malformed.
	ImmLane16 Imm = "laneidx16"
)

// Arm is one decoded opcode: its encoding and the immediates that follow it.
type Arm struct {
	// Prefix is 0 for a single-byte opcode, or the prefix byte (0xfb, 0xfc, 0xfd)
	// when Code is a u32 sub-opcode read after it.
	Prefix byte
	Code   uint32
	// Mnemonic is the reference's own constructor name, kept for the generated
	// table's readability and for error messages. Not load-bearing.
	Mnemonic string
	// Imms is the immediate sequence, in the order the reference reads them. Order is
	// the point: a wrong order shifts every subsequent byte.
	Imms []Imm
	// Illegal marks an arm the reference explicitly rejects (`illegal s pos b`).
	// These are *not* absent from the table — an opcode the reference names as illegal
	// is a fact, and conflating "rejected by the authority" with "not in the table" is
	// how a hole becomes an accept.
	Illegal bool
	// Line is the 1-indexed line in decode.ml this arm was read from, so a generated
	// row can be audited against the authority without a search.
	Line int
	// Reason carries the reference's own error text for arms that report one
	// (misplaced ELSE/END), where the arm is neither an instruction nor `illegal`.
	Reason string
	// Escape marks a single-byte arm that is a *prefix escape*: 0xfb, 0xfc, 0xfd,
	// whose RHS is a nested `match u32 s with` rather than an instruction. The value
	// is the prefix byte, which equals Code — stored rather than inferred so a
	// consumer walking the table reads the fact instead of pattern-matching on the
	// absence of every other field.
	//
	// Recorded because it was found missing by #33's agreement test: the escape's head
	// line is consumed by the block it opens, so all three prefixes were absent from
	// the single-byte table and a decoder walking it could not tell "escape to a
	// sub-table" from "no such opcode". That is the absent-versus-rejected conflation
	// Arm.Illegal exists to prevent, in a third flavour — and the reason it hid is
	// that the extractor's own partition test *enumerated* {0xfb, 0xfc, 0xfd} as a
	// literal instead of deriving them, so the one control that walked all 256 bytes
	// treated the hole as expected. Scope controls to the space (0006/#33): a
	// hardcoded exception list is a blind spot with a comment.
	Escape bool
}

// errUnrecognized is the load-bearing error: it is what makes this extraction
// different from a careful reading.
//
// GRAVE-ADJACENT (0007, condition 1): this error alone is *insufficient*, and the
// reason is worth stating where the error lives. It fires when a line looks like an
// arm and cannot be parsed — but it cannot fire when nothing looks like an arm at
// all. A moved file, a changed indent, or a wholesale refactor produces zero arms and
// zero unrecognized lines, and a drift check comparing an empty table to an empty
// table agrees perfectly. The vacuity floors in Extract are that case's control.
var errUnrecognized = errors.New("unrecognized arm in decode.ml")

// ErrVacuous reports an extraction that produced implausibly little. See Extract.
var ErrVacuous = errors.New("extraction produced too few arms")

var (
	// An arm's head: one or more hex codes, optionally `as b` / `as n` / `as opcode`,
	// then `->`. The `l` suffix marks an OCaml int32 literal, which is how the
	// prefixed sub-opcodes are written.
	reArmHead = regexp.MustCompile(`^\s*\|\s*((?:0x[0-9a-f]+l?\s*\|\s*)*0x[0-9a-f]+l?)\s*(?:as\s+\w+\s*)?->(.*)$`)
	reHexCode = regexp.MustCompile(`0x([0-9a-f]+)l?`)
	// A head that wraps: codes and bars, no arrow. See parseArm.
	reHeadContinues = regexp.MustCompile(`^\s*\|\s*(?:0x[0-9a-f]+l?\s*\|\s*)*0x[0-9a-f]+l?\s*$`)
	// A line that starts a prefix block: `| 0xfd ->` followed by `(match u32 s with`.
	reMatchU32 = regexp.MustCompile(`\(?\s*match u32 s with`)
)

// vocab is the immediate vocabulary paired with the Go identifier the generated table
// uses for each. It is the *single* source for both: Emit derives the generated const
// block from it, so the table's identifiers cannot drift from the extractor's values.
//
// Ordered, and the order is the generated const block's order — deterministic output is
// what makes the drift check a control rather than a coin flip.
var vocab = []struct {
	ident string
	imm   Imm
}{
	{"immIdx", ImmIdx},
	{"immMemop", ImmMemop},
	{"immLaneIdx", ImmLaneIdx},
	{"immLane16", ImmLane16},
	{"immByte", ImmByte},
	{"immU32", ImmU32},
	{"immS32", ImmS32},
	{"immS64", ImmS64},
	{"immF32", ImmF32},
	{"immF64", ImmF64},
	{"immV128", ImmV128},
	{"immValType", ImmValType},
	{"immHeapType", ImmHeapType},
	{"immBlockType", ImmBlockType},
	{"immVecValType", ImmVecValType},
	{"immVecIdx", ImmVecIdx},
	{"immCatchVec", ImmCatchVec},
	{"immBlock", ImmBlock},
}

func immConsts() []struct {
	ident string
	imm   Imm
} {
	return vocab
}

// identOf returns the generated table's identifier for an immediate, or "" if the
// immediate is not in the vocabulary — which Emit must never be handed, and which the
// generator checks rather than papering over with a fallback.
func identOf(im Imm) string {
	for _, v := range vocab {
		if v.imm == im {
			return v.ident
		}
	}
	return ""
}

// immPatterns maps the reference's reader calls to our vocabulary.
//
// Ordered longest-first where one pattern is a prefix of another, so `vec valtype`
// is not mistaken for `valtype`. An ordered slice rather than a map because the
// scan order is semantic here.
var immPatterns = []struct {
	pat *regexp.Regexp
	imm Imm
}{
	{regexp.MustCompile(`\brepeat\s+16\s+laneidx\s+s\b`), ImmLane16},
	{regexp.MustCompile(`\bvec\s+\(at\s+idx\)\s+s\b`), ImmVecIdx},
	{regexp.MustCompile(`\bvec\s+\(at\s+catch\)\s+s\b`), ImmCatchVec},
	{regexp.MustCompile(`\bvec\s+valtype\s+s\b`), ImmVecValType},
	{regexp.MustCompile(`\bblocktype\s+s\b`), ImmBlockType},
	{regexp.MustCompile(`\bheaptype\s+s\b`), ImmHeapType},
	{regexp.MustCompile(`\bmemop\s+s\b`), ImmMemop},
	{regexp.MustCompile(`\blaneidx\s+s\b`), ImmLaneIdx},
	{regexp.MustCompile(`\bat\s+v128\s+s\b`), ImmV128},
	{regexp.MustCompile(`\bat\s+f32\s+s\b`), ImmF32},
	{regexp.MustCompile(`\bat\s+f64\s+s\b`), ImmF64},
	{regexp.MustCompile(`\bat\s+s32\s+s\b`), ImmS32},
	{regexp.MustCompile(`\bat\s+s64\s+s\b`), ImmS64},
	{regexp.MustCompile(`\bat\s+valtype\s+s\b`), ImmValType},
	{regexp.MustCompile(`\bat\s+idx\s+s\b`), ImmIdx},
	{regexp.MustCompile(`\bidx\s+s\b`), ImmIdx},
	{regexp.MustCompile(`\bbyte\s+s\b`), ImmByte},
	{regexp.MustCompile(`\bu32\s+s\b`), ImmU32},
	{regexp.MustCompile(`\binstr_block\s+s\b`), ImmBlock},
}
