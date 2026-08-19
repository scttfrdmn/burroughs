// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package validate

import (
	"fmt"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// Slice 5 of #9's validator: **the instructions whose type reads a module index space.**
//
// # What this slice is, and why it is not "the 0xFC region"
//
// 0xFC holds two unrelated families that share a prefix byte and nothing else. `0x00`-`0x07` are
// the saturating float→int conversions, whose type is a *numeric* signature and is already
// derivable from the mnemonic; `0x08`-`0x11` are the bulk operations, whose types read the
// module's memory, table, data-segment and element-segment index spaces. The region is an
// encoding fact, not a typing family, and this file keeps them apart: the first eight route
// straight into `signature`, and only the other ten get rules here.
//
// Once that is said, the slice cannot then be *drawn* on the prefix byte, which is the fact just
// disclaimed. `memory.size` and `memory.grow` are plain `0x3F`/`0x40` and belong to this slice on
// the criterion that actually defines it: they resolve `memory c x` and take their type from the
// memory's address width. The reference is explicit about the grouping — `MemorySize` at
// `valid.ml:687`, `MemoryGrow` at `:691`, and `MemoryFill` at `:695` are three consecutive arms
// with the identical `let MemoryT (at, _lim) = memory c x in` preamble, so an encoding-shaped
// boundary would cut an arm sequence the authority wrote as one. They are typed by memoryIndexOp
// below, routed from `signature` because their opcodes are unprefixed.
//
// This widening was slice 5's own scope call and is named in its PR rather than settled here: the
// eight vectors it converts were on no slice's list, and a rule that reads an index space had no
// other slice to be in. The classification is challengeable — what is *not* arguable is drawing
// the line on 0xFC after arguing 0xFC is not a typing family.
//
// That routing is the point rather than a tidiness. `signature`'s doc comment has claimed since
// slice 1 that `i32.trunc.sat.f32.s` "resolves here" — and it could not, because `signature`
// looked its mnemonic up in `binary.OpMnemonic`, the *plain* table, which has no row for a
// prefixed opcode. The sentence was true of the parsing and false of the lookup, so the
// conversion arm was unreachable for the eight opcodes the comment named. The fix is to ask the
// authority's table for the name rather than one half of it (`opMnemonic`), which makes the
// existing sentence true instead of adding a second path that agrees with it.
//
// # The immediates are reversed for two of the four two-index arms, and both are 0 in most vectors
//
// `memory.init` and `table.init` name the destination first in the text and *second* in the
// encoding: `0x08l -> let y = at idx s in let x = at idx s in memory_init x y` and its `0x0cl`
// twin (`decode.ml:669,674`). So `Imm0` is the **segment** and `Imm1` the memory or table, which
// is the opposite assignment from `memory.copy`/`table.copy`, whose `0x0al`/`0x0el` rows read
// `x` then `y` in order.
//
// `internal/interp/bulk.go` derived this first and states it at length; this file cites that
// rather than re-deriving it, which is what *lessons are indexed by shape* asks for when the
// shape has already been paid for one package over. The hazard is that both indices are 0 in
// nearly every vector, so a swap is right by coincidence exactly the way `addrTypeAt`'s
// hardcoded memory 0 was — and unlike that one, the table side is observable *today*:
// multi-table is not gated, so `table.init $t $el` with two tables and two segments
// distinguishes the two readings in the default lane. TestBulkInitImmediatesAreNotSwapped is
// that module.
//
// # The reference's two `type mismatch` messages have their operands transposed
//
// The `TableInit` arm (`valid.ml:641-647`) binds `t1` from the *table* and `t2` from the *segment*,
// then reports
// `"element segment's type " ^ string_of_reftype t1 ^ " does not match table's element type " ^
// string_of_reftype t2` — each label attached to the other one's type. `:632-639` does the same
// with source and destination. The `require` itself is right (`match_reftype c.types t2 t1`:
// the source must be a subtype of the destination), so this is *an error message is testimony,
// and fabricated evidence is a lying witness even when the verdict is right* — in the authority,
// and reached only by a module that is being rejected anyway, which is why nothing upstream has
// tripped over it.
//
// The labels are corrected here rather than transcribed. Decision 0003 binds the *sentinel* to
// the suite's expected string, and the suite expects `type mismatch` and nothing more; the
// wrapped detail is this engine's own witness, and copying a transposition into it would be
// choosing to print evidence known to be mislabelled for the sake of matching a string no
// vector compares. Recorded rather than filed upstream, which is a judgement about scope and
// not about the finding.
//
// # The authority
//
// `valid.ml:618-651` (the table arms) and `:695-714` (the memory arms), with `min at1 at2` over
// `addrtype = I32AT | I64AT` (`types.ml:15`) — OCaml's structural `min` on a two-constructor
// variant is constructor order, so a copy between a 32-bit and a 64-bit side takes an **i32**
// length. See minAddrType.

// prefixBulk is the bulk memory/table region's prefix byte.
//
// Local for prefixSIMD's reason, which that constant states in full: `internal/binary` spells
// the prefix as a literal at each of its own sites, so exporting a shared one is a two-package
// convention change no slice has been asked to make. TestPrefixBulkIsTheRegionBinaryDispatches
// checks it against `binary`'s dispatch rather than leaving the two agreeing by inspection.
const prefixBulk = 0xfc

// The 0xFC sub-opcodes. `truncSatLast` is a *range* end rather than a member: the eight
// conversions have no rule in this file and are named as a boundary so the dispatch below reads
// as "the numeric family, then the bulk family" instead of as eight fall-through cases.
//
// Asserted against `binary`'s generated table by TestBulkOpcodesMatchTheTable, the control
// `TestStructuralOpcodesMatchTheTable` already applies to slice 1's plain constants: a named
// constant is a transcription of a generated row, and an unchecked transcription is the class
// `sig.go`'s header argues out of existence.
const (
	truncSatFirst = 0x00
	truncSatLast  = 0x07

	fcMemoryInit = 0x08
	fcDataDrop   = 0x09
	fcMemoryCopy = 0x0a
	fcMemoryFill = 0x0b
	fcTableInit  = 0x0c
	fcElemDrop   = 0x0d
	fcTableCopy  = 0x0e
	fcTableGrow  = 0x0f
	fcTableSize  = 0x10
	fcTableFill  = 0x11
)

// bulkInstr types one 0xFC instruction, mirroring vecInstr: resolve the signature, pop the
// operands, push the results.
func (v *validator) bulkInstr(in binary.Instr) error {
	s, err := v.bulkSignature(in)
	if err != nil {
		return err
	}
	if err := v.popExpectAll(s.params); err != nil {
		return err
	}
	v.pushAll(s.results)
	return nil
}

// bulkSignature returns a 0xFC instruction's type, or a decline.
//
// Three-valued exactly as `signature` and `vecSignature` are: nil means the type is known,
// ErrUnsupported means this slice has no rule for the opcode, and anything else is a rule that
// *is* known and that the module fails. The index-space lookups and the two reference-type
// requirements are performed here rather than by the caller, because a `sig` cannot carry a
// `require` — the same arrangement `vecSignature` uses for `invalid lane index`.
//
// **The lookups come before the operand pops, in the reference's order.** `valid.ml` resolves
// `table c x` / `memory c x` / `elem c y` / `data c y` in the `let` bindings above the `-->`,
// so a `table.init 4 0` in a module with one table is `unknown table 4` and not a type
// mismatch about an operand that was never going to be reached. Ten corpus vectors turn on
// this ordering; reversing it would refuse all ten with the wrong string while agreeing with
// the reference on every verdict.
func (v *validator) bulkSignature(in binary.Instr) (sig, error) {
	if in.Op <= truncSatLast {
		// The numeric half. `signature` derives it from the mnemonic like every other conversion —
		// see the header on why that call could not previously reach these eight.
		return signature(v.mod, in)
	}

	i32 := binary.I32
	switch in.Op {
	// The memory arms. `valid.ml:695` (fill), `:700` (copy), `:706` (init), `:711` (data.drop).
	case fcMemoryFill:
		at, err := addrTypeAt(v.mod, uint32(in.Imm0))
		if err != nil {
			return sig{}, err
		}
		// `[at; I32T; at]`: the value is a byte-in-an-i32 whatever the memory's width, and the
		// destination and the length are both addresses.
		return sig{params: []binary.ValType{at, i32, at}}, nil

	case fcMemoryCopy:
		dst, err := addrTypeAt(v.mod, uint32(in.Imm0))
		if err != nil {
			return sig{}, err
		}
		src, err := addrTypeAt(v.mod, uint32(in.Imm1))
		if err != nil {
			return sig{}, err
		}
		return sig{params: []binary.ValType{dst, src, minAddrType(dst, src)}}, nil

	case fcMemoryInit:
		// Imm1 is the memory and Imm0 the data segment — the encoding's reversal, see the header.
		at, err := addrTypeAt(v.mod, uint32(in.Imm1))
		if err != nil {
			return sig{}, err
		}
		if err := dataSegmentAt(v.mod, uint32(in.Imm0)); err != nil {
			return sig{}, err
		}
		// `[at; I32T; I32T]`: only the destination is an address. A segment is *indexed*, not
		// addressed, so its offset has no width and the length is bounded by the segment side —
		// the same asymmetry `internal/interp`'s execTableInit records, and the reason this is not
		// `minAddrType` of anything.
		return sig{params: []binary.ValType{at, i32, i32}}, nil

	case fcDataDrop:
		return sig{}, dataSegmentAt(v.mod, uint32(in.Imm0))

	// The table arms. `valid.ml:618` (size), `:622` (grow), `:627` (fill), `:632` (copy),
	// `:641` (init), `:649` (elem.drop).
	case fcTableSize:
		t, err := tableTypeAt(v.mod, uint32(in.Imm0))
		if err != nil {
			return sig{}, err
		}
		return sig{results: []binary.ValType{tableAddrType(t)}}, nil

	case fcTableGrow:
		t, err := tableTypeAt(v.mod, uint32(in.Imm0))
		if err != nil {
			return sig{}, err
		}
		at := tableAddrType(t)
		// `[RefT rt; at] --> [at]` — the initializer value is below the delta on the stack.
		return sig{params: []binary.ValType{t.ElemType, at}, results: []binary.ValType{at}}, nil

	case fcTableFill:
		t, err := tableTypeAt(v.mod, uint32(in.Imm0))
		if err != nil {
			return sig{}, err
		}
		at := tableAddrType(t)
		return sig{params: []binary.ValType{at, t.ElemType, at}}, nil

	case fcTableCopy:
		dst, err := tableTypeAt(v.mod, uint32(in.Imm0))
		if err != nil {
			return sig{}, err
		}
		src, err := tableTypeAt(v.mod, uint32(in.Imm1))
		if err != nil {
			return sig{}, err
		}
		if !v.matches(src.ElemType, dst.ElemType) {
			return sig{}, fmt.Errorf("%w: source element type %s does not match destination "+
				"element type %s", ErrTypeMismatch, src.ElemType, dst.ElemType)
		}
		dat, sat := tableAddrType(dst), tableAddrType(src)
		return sig{params: []binary.ValType{dat, sat, minAddrType(dat, sat)}}, nil

	case fcTableInit:
		// Imm1 is the table and Imm0 the element segment — the reversal again.
		t, err := tableTypeAt(v.mod, uint32(in.Imm1))
		if err != nil {
			return sig{}, err
		}
		seg, err := elemTypeAt(v.mod, uint32(in.Imm0))
		if err != nil {
			return sig{}, err
		}
		if !v.matches(seg, t.ElemType) {
			return sig{}, fmt.Errorf("%w: element segment's type %s does not match table's "+
				"element type %s", ErrTypeMismatch, seg, t.ElemType)
		}
		// `[at; I32T; I32T]`, memory.init's asymmetry for the same reason.
		return sig{params: []binary.ValType{tableAddrType(t), i32, i32}}, nil

	case fcElemDrop:
		_, err := elemTypeAt(v.mod, uint32(in.Imm0))
		return sig{}, err
	}

	// No row. Unreachable through a decoded module — `opTableFC` stops at 0x11, so an opcode
	// above it is refused as malformed before this package sees it — and returned rather than
	// panicked for the reason `errNoSignature` exists: an opcode arriving here from a widened
	// table should land in a named bucket, not in a crash or an unearned accept.
	return sig{}, fmt.Errorf("%w: %s (%#02x %#02x)", ErrUnsupported, mnemonic(in), in.Prefix, in.Op)
}

// memoryIndexOp types `memory.size` and `memory.grow` — `valid.ml:687` and `:691`.
//
// `[] --> [at]` and `[at] --> [at]`, where `at` is the named memory's address width. Not in the
// switch above because these two are unprefixed opcodes and arrive through `signature`; in this
// file because the rule they follow is `memory c x`, which is what makes them this slice's.
//
// The bool rather than the mnemonic string is deliberate: the caller has already parsed the name
// to get here, and re-deriving "is it grow" from a second string comparison would put the same
// classification in two places, where the second one can be wrong on its own.
func memoryIndexOp(m *binary.Module, in binary.Instr, grow bool) (sig, error) {
	at, err := addrTypeAt(m, uint32(in.Imm0))
	if err != nil {
		return sig{}, err
	}
	if grow {
		return sig{params: []binary.ValType{at}, results: []binary.ValType{at}}, nil
	}
	return sig{results: []binary.ValType{at}}, nil
}

// minAddrType is the reference's `min at1 at2` over `addrtype = I32AT | I64AT`.
//
// OCaml's polymorphic `min` on a variant with no payload compares by constructor order, and
// `types.ml:15` declares `I32AT` first — so the result is i64 only when *both* sides are, and a
// copy between a 32-bit and a 64-bit memory takes an i32 length. Written as "both or i32" rather
// than as a comparison on ValType, because ValType's ordering is this engine's and the rule is
// the reference's; a `<` here would be reading a fact off the wrong table.
//
// Reachable only with memory64 or table64 on, so its witnesses are all-gates-on ones — but the
// corpus does hold them, which is worth stating precisely because the easy assumption is that a
// gated rule is unwitnessed. `table_copy_mixed.wast` is the file whose whole subject this is, and
// it moves 1/4 → 4/4 in the all-on lane and 0 in the default lane. TestMixedWidthCopyTakesAnI32Length
// is the unit witness for the same rule, kept because a corpus file that agrees says the verdict
// was right and never says the *length operand's type* was, that being accept-direction (G-3).
func minAddrType(a, b binary.ValType) binary.ValType {
	if a == binary.I64 && b == binary.I64 {
		return binary.I64
	}
	return binary.I32
}

// tableTypeAt resolves a table index to its type, across the imports-then-definitions index
// space.
//
// `requireTable` delegates here rather than keeping its own bound check, which is the #241
// consolidation applied one package over: two functions that answer "does this table index
// resolve" would agree until one of them learned about imports and the other did not. This one
// returns the *type*, because slice 5 needs the element type and the address width and slice 1
// needed neither — a bound check is the special case of a lookup that discards its result.
//
// **`binary.TableType`, which is what "the type" was already claiming**: the two arms below are an
// import's descriptor and a definition, and only the second has an initializer. Returning `Table`
// made the import arm widen its value to a struct with fields it can never fill — the shape grave
// #420 was, one package along — while every caller here reads exactly the two fields both arms have.
func tableTypeAt(m *binary.Module, idx uint32) (binary.TableType, error) {
	imported := m.ImportedTables()
	if int(idx) < imported {
		n := 0
		for i := range m.Imports {
			if m.Imports[i].Kind != binary.ExternTable {
				continue
			}
			if n == int(idx) {
				return m.Imports[i].Table, nil
			}
			n++
		}
	}
	if defined := int(idx) - imported; defined >= 0 && defined < len(m.Tables) {
		return m.Tables[defined].Type(), nil
	}
	// The message is `requireTable`'s verbatim, including its parenthetical, because the corpus
	// matches it by substring (0003): 12 vectors expect the bare `unknown table` and a further 4
	// expect `unknown table 0`, so any text between the category and the index breaks the second set
	// while leaving the first green. The count is stated as the two keys it is rather than as one
	// number — the sentence here read "12 corpus vectors match `unknown table` and `unknown table 0`",
	// which is true on the reading that 12 want the bare string and misreads as the family's total,
	// and the family is 16. Whose rules those 16 belong to is `authority_test.go`'s
	// message-oracle-resolution section; this function is the producer for the bulk operands' four.
	n := uint32(imported) + uint32(len(m.Tables))
	return binary.TableType{}, fmt.Errorf("%w %d (%d in scope)", ErrUnknownTable, idx, n)
}

// tableAddrType is a table's address type — i64 for a table64, else i32.
//
// `Limits.Addr64` for both memories and tables, which is where the decoder puts it and which
// that field's own comment anticipated ("table64 will want the identical field from the
// identical position"). addrTypeOf next door is the memory spelling of this and the two are
// deliberately not merged: they read the same bit off different index spaces, and a shared
// helper taking `Limits` would hide which space a caller resolved against, the distinction the
// `unknown table` / `unknown memory` split exists to keep.
func tableAddrType(t binary.TableType) binary.ValType {
	if t.Limits.Addr64 {
		return binary.I64
	}
	return binary.I32
}

// dataSegmentAt is `data c x` (`valid.ml:52`), which returns unit in the reference: a data
// segment has no type, only an existence.
//
// **`len(m.Datas)` is the declared count, not a survivor count**, and that is what makes this
// the right operand. A module whose bulk instructions name a data segment must carry a data
// count section, the decoder enforces that the count and the section agree
// (`binary.ErrDataCountRequired`, `ErrDataCountMismatch`), and a module failing either is
// *malformed* — refused a layer below with no chance to reach this rule. So the only module that
// gets here is one whose count is honest, and the sentinel is reached exactly when the index
// overruns it.
func dataSegmentAt(m *binary.Module, idx uint32) error {
	if int(idx) < len(m.Datas) {
		return nil
	}
	return fmt.Errorf("%w %d (%d in scope)", ErrUnknownDataSegment, idx, len(m.Datas))
}

// elemTypeAt is `elem c x` (`valid.ml:51`), which unlike `data` *does* return a type: an element
// segment carries a reference type, and `table.init` compares it against the table's.
func elemTypeAt(m *binary.Module, idx uint32) (binary.ValType, error) {
	if int(idx) < len(m.Elems) {
		return m.Elems[idx].ElemType, nil
	}
	return binary.ValType{}, fmt.Errorf("%w %d (%d in scope)", ErrUnknownElemSegment, idx, len(m.Elems))
}
