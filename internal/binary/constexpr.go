package binary

import "fmt"

// The three section grammars that need a constant expression (#25).
//
// The `constexpr` production itself moved to instr.go when its narrow accept set was
// dissolved into the authority-derived table (#43/#39) — read that file's header for
// why the const-ness verdict is now deferred rather than aborting, and why
// binary.wast:112 is the vector that forced it.
//
// A constant expression is an instruction sequence terminated by END (0x0B). It is
// *not* length-prefixed: its extent is discovered by reading instructions, which is
// why the global, element, and data sections could not be decoded until the decoder
// knew opcodes at all. Decision 0006 declined to share #7's table from the start and
// pre-registered the agreement test (#33) that would catch the two copies drifting;
// the debt is discharged in the other direction — there is now one table, and the
// tripwire became the drift check against the reference (0007).

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
