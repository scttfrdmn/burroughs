package binary

import "fmt"

// The `constexpr` production, and the three section grammars that need it (#25).
//
// A constant expression is an instruction sequence terminated by END (0x0B). It is
// *not* length-prefixed: its extent is discovered by reading instructions, which is
// why the global, element, and data sections could not be decoded until the decoder
// knew opcodes at all. Decision 0006 places that knowledge here rather than sharing
// #7's table from the start, and pre-registers the agreement test (#33) that will
// catch the two copies drifting.
//
// # What this reader is responsible for, and what it is not
//
// The spec's production is
//
//	expr ::= (in:instr)* 0x0B => in* end
//
// with the *constant*-ness of the instructions a side condition checked by
// validation, not by the grammar. The suite is unambiguous about which layer owns
// which: `constant expression required` appears 22 times across global/elem/data/
// array/func_ptrs.wast and **every occurrence is `assert_invalid`, never
// `assert_malformed`** (checked, not assumed — `(global f32 (f32.neg (f32.const 0)))`
// at global.wast:298 is invalid, and `(global i32 (i32.const 0) (nop))` at :313 is
// too). So:
//
//   - A byte that is not an opcode at all is *malformed*, and this reader's error.
//     binary.wast:345 is the vector: `\f3` in an element segment is "illegal opcode".
//   - A byte that is a real opcode but not const-legal — `f32.neg`, `local.get`,
//     `nop` — is *invalid*, and belongs to the validator. Rejecting it here would
//     report "malformed" for a module the spec calls well-formed, which is the same
//     error as a gate manufacturing malformedness (CLAUDE.md): it lies about the
//     module to conceal which layer noticed.
//
// That is why constLegal below is a *narrow* accept set for the values this reader
// evaluates, while readInstr decodes the immediate shape of every opcode it can be
// asked about. Conflating the two would have been the cheap generalisation: it
// passes all ten of #25's vectors and rejects valid modules, which is the direction
// the suite cannot catch (contract §9 G-3).
//
// # Why the extent, not just the opcodes
//
// Reading an instruction means consuming its immediates, and getting an immediate
// width wrong does not fail loudly — it shifts every subsequent byte, so the error
// surfaces somewhere else entirely, as a size mismatch or a bogus opcode. The
// element and data sections put a const-expr *before* other fields, so a wrong
// extent there is silently mis-parsed rather than rejected. #33's property 2 exists
// for exactly this failure mode.

// constExprOp is one opcode this reader recognises inside a constant expression.
//
// The immediate shape is the point of the table: the opcode byte alone is not
// enough to find the end of the instruction.
type constExprOp struct {
	name string
	imm  func(*Decoder, *reader) error
}

// opEnd is END (0x0B), the terminator. Listed as an opcode rather than special-cased
// in the loop because it *is* one — `expr` ends with a real instruction, and the
// agreement test (#33) walks this table expecting END to be in it.
const opEnd = 0x0B

// constExprOps is the const-legal subset of the opcode space for the tracked MVP.
//
// Closed by the *grammar*, not by what the suite exercises: the spec's constant
// expression is `t.const`, `ref.null`, `ref.func`, and `global.get` of an immutable
// import, terminated by END. Extended-const (`i32.add` and friends) and WasmGC
// (`struct.new` etc.) widen it, and both are gated — so they are absent here and
// arrive with their gates, routed through Features the way every other gated
// construct is (§9 G-2).
//
// All ten of #25's vectors use only i32.const, global.get, ref.func, and END; the
// other four entries are here because the production has them, not because a vector
// does. A table written from the vectors would be the oracle mistaken for the
// objective function.
// The immediates take the Decoder because ref.null reads a reftype, whose accepted
// set is gate-dependent (WasmGC widens it). Threading it keeps the acceptance layer
// in one place instead of letting this table hold a second, gate-blind opinion.
var constExprOps = map[byte]constExprOp{
	0x41:  {"i32.const", func(_ *Decoder, r *reader) error { _, err := r.s32(); return err }},
	0x42:  {"i64.const", func(_ *Decoder, r *reader) error { _, err := r.s64(); return err }},
	0x43:  {"f32.const", func(_ *Decoder, r *reader) error { _, err := r.bytes(4); return err }},
	0x44:  {"f64.const", func(_ *Decoder, r *reader) error { _, err := r.bytes(8); return err }},
	0x23:  {"global.get", func(_ *Decoder, r *reader) error { return discardIndex(r) }},
	0xD0:  {"ref.null", func(d *Decoder, r *reader) error { return d.decodeRefType(r) }},
	0xD2:  {"ref.func", func(_ *Decoder, r *reader) error { return discardIndex(r) }},
	opEnd: {"end", func(*Decoder, *reader) error { return nil }},
}

// decodeConstExpr reads a constant expression up to and including its END.
//
// The loop's exit condition (END) and its error condition (a byte that is not an
// opcode) are deliberately different predicates — the zero-progress shape from grave
// #18, where a reader whose "done" and "broken" tests are the same expression hangs
// on anything else. Every iteration either consumes at least the opcode byte or
// returns, so progress is structural here rather than asserted; FuzzConstExpr pins
// it anyway, because that is what the discipline asks of a reader.
func (d *Decoder) decodeConstExpr(r *reader) error {
	for {
		b, err := r.byte()
		if err != nil {
			return err
		}
		op, ok := constExprOps[b]
		if !ok {
			// Not in the const-legal set — and this reader deliberately does not
			// claim to know *why*, because it cannot. Two cases sit behind this
			// branch and they have different verdicts in the spec:
			//
			//   - a byte that is no opcode at all (`\f3`): malformed, "illegal opcode"
			//   - a real opcode that is not constant (`f32.neg`): invalid,
			//     "constant expression required"
			//
			// Telling them apart needs the *existence* question answered over the
			// whole opcode space, which is #7's table and not this set. So the error
			// names the reader's own limit and matches no spec string — the
			// featureErr manoeuvre (CLAUDE.md, "gates never manufacture
			// malformedness"): the module is rejected, because accept-and-ignore
			// would break the extent and every size check after it, but nothing here
			// pretends to a verdict it did not compute.
			//
			// The cost is exact and on the board: binary.wast:345 wants "illegal
			// opcode" and keeps failing. Buying it with `ErrIllegalOpcode` for every
			// non-const byte would pass that vector and report "illegal opcode 92"
			// for `f32.neg` — a malformed verdict on a module the spec calls
			// well-formed, in the accept direction the suite has no vector for. That
			// is the ruling on `data count section required` (#22, contract §9 G-3):
			// leave the bucket open and say why.
			return fmt.Errorf("%w: %#02x", ErrNonConstantExpr, b)
		}
		if err := op.imm(d, r); err != nil {
			return err
		}
		if b == opEnd {
			return nil
		}
	}
}

// decodeGlobal reads a globaltype followed by its initialiser.
func (d *Decoder) decodeGlobal(r *reader) error {
	if err := d.decodeGlobalType(r); err != nil {
		return err
	}
	return d.decodeConstExpr(r)
}

// decodeElemSegment reads one element segment.
//
// The flags byte is a bitfield, not an enum, and the three bits mean: bit 0 passive
// vs active-with-explicit-table, bit 1 declarative/table-index present, bit 2
// element *expressions* rather than function indices. Seven of the eight
// combinations are legal, which is why this reads as bit tests rather than a switch
// over eight cases.
//
// binary.wast:345 and :373 are both this grammar: flags 5 is passive with element
// expressions, so :345's `\f3` is read as an opcode (illegal) and :373's `\7f` is
// read as a reftype (malformed reference type). Two different errors from two
// different fields of the same segment, which is the check on whether the flag bits
// were decoded or guessed.
func (d *Decoder) decodeElemSegment(r *reader) error {
	flags, err := r.u32()
	if err != nil {
		return err
	}
	if flags > 7 {
		return fmt.Errorf("%w: %#02x", ErrMalformedElemFlags, flags)
	}
	const (
		passive  = 1 << 0 // no table index, no offset — or declarative
		explicit = 1 << 1 // table index present (active) / declarative (passive)
		exprs    = 1 << 2 // elements are const-exprs, not func indices
	)
	active := flags&passive == 0
	if active {
		if flags&explicit != 0 { // table index only when bit 1 is set
			if err := discardIndex(r); err != nil {
				return err
			}
		}
		if err := d.decodeConstExpr(r); err != nil { // offset
			return err
		}
	}
	// The element type field is present iff bit 0 or bit 1 is set, and bit 2 selects
	// its encoding: an elemkind byte (0x00, the only defined one) when the elements
	// are function indices, a full reftype when they are expressions.
	//
	// That presence rule is *derived from every element-segment encoding the suite
	// shows*, not from one vector, because two cheaper rules each fit part of the
	// evidence and fail elsewhere:
	//
	//	flags  encoding (elem.wast)          type field
	//	  0    \00\41\00\0b\01\00            no
	//	  1    \01\00\01\00                  yes (elemkind)
	//	  2    \02\00\41\00\0b\00\01\00      yes (elemkind)
	//	  3    \03\00\01\00                  yes (elemkind)
	//	  4    \04\41\00\0b\01\d2\00\0b      no
	//	  5    \05\70\01\d2\00\0b            yes (reftype)
	//
	// `flags != 0` fits everything but 4; `flags&explicit != 0` fits everything but 5.
	// Both were written and both were caught, one row apiece, which is the argument
	// for a table of every observed encoding over a rule inferred from the first
	// vector that fails. The two forms with no type field are exactly the two that
	// default to table 0 with funcref elements.
	if flags&(passive|explicit) != 0 {
		if flags&exprs != 0 {
			if err := d.decodeRefType(r); err != nil {
				return err
			}
		} else {
			kind, err := r.byte()
			if err != nil {
				return err
			}
			if kind != 0x00 {
				return fmt.Errorf("%w: %#02x", ErrMalformedElemKind, kind)
			}
		}
	}
	elem := discardIndex
	if flags&exprs != 0 {
		elem = d.decodeConstExpr
	}
	return d.decodeVec(r, elem)
}

// decodeDataSegment reads one data segment: an optional memory index and offset
// expression, then the contents.
//
// The contents are a `byteVec`, **not** a `name`. A data segment's bytes are
// arbitrary — `vec(byte)` with no encoding side condition — so applying the UTF-8
// rule here would reject modules the spec accepts, in the direction the suite has no
// vector for. TestByteVecIsNotAName is the control, and this is the caller that
// finally makes it non-vacuous: until now the data section was never descended into,
// so a probe pushing utf8.Valid down into byteVec left the test green (grave #32).
func (d *Decoder) decodeDataSegment(r *reader) error {
	if err := d.decodeDataSegmentMode(r); err != nil {
		return err
	}
	// ErrPayloadEnd, not ErrSectionOverrun: binary.wast:877 is a segment declaring 7
	// content bytes with 6 left in the image and expects "unexpected end of section or
	// function", where the same overrun on an export *name* (:754) is "length out of
	// bounds". See byteVecErr for why that split is a per-call-site choice.
	_, err := r.byteVecErr(ErrPayloadEnd)
	return err
}

// decodeDataSegmentMode reads the flags byte and whatever the mode it selects puts
// before the contents: nothing for passive, an offset expression for active, and a
// memory index too when the index is explicit.
//
// Split out from decodeDataSegment so no `err` is live across the switch. Reusing an
// outer `err` inside the cases trips gocritic's sloppyReassign, and shadowing it
// trips govet's shadow — two enabled linters pointing opposite ways, which is a
// signal about the shape rather than about the config (decision 0005's spirit
// clause). Narrowing the scope so neither applies is the fix both were asking for.
func (d *Decoder) decodeDataSegmentMode(r *reader) error {
	flags, err := r.u32()
	if err != nil {
		return err
	}
	switch flags {
	case 0x00: // active, memory 0 implied
		return d.decodeConstExpr(r) // offset
	case 0x01: // passive: no memory index, no offset
		return nil
	case 0x02: // active with an explicit memory index
		if err := discardIndex(r); err != nil {
			return err
		}
		return d.decodeConstExpr(r) // offset
	default:
		return fmt.Errorf("%w: %#02x", ErrMalformedDataFlags, flags)
	}
}
