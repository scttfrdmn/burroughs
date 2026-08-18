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
	vt, mut, err := d.decodeGlobalType(r)
	if err != nil {
		return err
	}
	init, casts, err := d.decodeConstExprKeep(r)
	if err != nil {
		return err
	}
	d.mod().Globals = append(d.mod().Globals,
		Global{Type: vt, Mutable: mut, Init: init, InitCasts: casts})
	return nil
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
//
// Retaining under 0016: the mode, the table index, the offset, the element type and the
// element vector are staged into an ElemSegment and appended to the module's index order,
// where all five used to be read and dropped. The consumers are `call_indirect` needing a
// table with something in it, `table.init` naming a segment by index, and the wat encoder's
// elem-section writer (#8). See ElemSegment for why the two element forms stay distinct.
func (d *Decoder) decodeElemSegment(r *reader) error {
	flags, err := r.u32()
	if err != nil {
		return err
	}
	if flags > 7 {
		return fmt.Errorf("%w: %#02x", ErrMalformedElemSegKind, flags)
	}
	const (
		passive  = 1 << 0 // no table index, no offset — or declarative
		explicit = 1 << 1 // table index present (active) / declarative (passive)
		exprs    = 1 << 2 // elements are const-exprs, not func indices
	)
	seg := ElemSegment{ByExpr: flags&exprs != 0}
	// The two bits together select the reference's three `segmentmode` arms, and a switch
	// rather than a chain because that is what they are — three cases of one classification,
	// not a condition with exceptions.
	switch {
	case flags&passive == 0: // active: flags 0, 2, 4, 6
		if flags&explicit != 0 { // table index only when bit 1 is set
			// Retained where it used to be discarded, and *recorded rather than checked* for
			// DataSegment.MemIndex's reason: whether it names a table the module has is #9's
			// question, and elem.wast turns on the difference — `(elem (table 3) …)` against
			// one table is a module that fails to instantiate, not one that is malformed.
			if seg.TableIndex, err = r.u32(); err != nil {
				return err
			}
		}
		if seg.Offset, seg.OffsetCasts, err = d.decodeConstExprKeep(r); err != nil { // offset
			return err
		}
	case flags&explicit != 0:
		// Bit 1 with bit 0 means declarative — flags 3 and 7. The reference's third
		// segmentmode arm, which the data section does not have (encode.ml's `data` asserts
		// on it) and which the text grammar's `elem` does.
		seg.Mode = ElemDeclarative
	default: // passive: flags 1, 5
		seg.Mode = ElemPassive
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
	// default to table 0 — and, as the paragraph below records, *not* to the same
	// element type as each other.
	// The default when no type field is present, and it is the *decoded* default rather
	// than a zero value: flags 0 and 4 are segments whose type the wire omits, so
	// leaving ElemType at NoValType would make the field say "unrepresentable" about a
	// module that plainly declared one. That is grave #36's class in a field — an engine
	// reporting a value its input never held.
	//
	// **The default is nullability-split, and the split is the whole of grave #400**: the
	// reference's four index forms yield `(NoNull, FuncHT)` — flag 0's own literal
	// (decode.ml:1163) and `elem_kind`'s only value (decode.ml:1154-1157) — while flag 4,
	// the expression form with no reftype field, yields `(Null, FuncHT)` (decode.ml:1183).
	// So `elemkind` is `(ref func)` and flag 4 is `funcref`, one field apart, and this
	// decoder gave every one of them `funcref`.
	//
	// It was unobservable until an element segment's type was compared against its table's:
	// nullability is only ever *read* by a subtype check, and until #328's `check_elemmode`
	// port there was none. It then showed up as an over-rejection rather than an admission —
	// `(table 1 (ref func) …) (elem (i32.const 0) func 0)` is a valid module the reference
	// accepts and this decoder made unrepresentable, because `(ref null func) <: (ref func)`
	// is false in the one direction that matters. That is why a decode-direction defect is
	// the accept direction's problem: the validator was right about the type it was handed.
	if flags&exprs != 0 {
		seg.ElemType = FuncRef // flag 4's `(Null, FuncHT)`
	} else {
		seg.ElemType = refNull(FuncRef, false) // `elemkind`'s `(NoNull, FuncHT)`
	}
	if flags&(passive|explicit) != 0 {
		if flags&exprs != 0 {
			if err := d.decodeRefType(r); err != nil {
				return err
			}
			seg.ElemType = d.valType
		} else {
			kind, err := r.byte()
			if err != nil {
				return err
			}
			if kind != 0x00 {
				return fmt.Errorf("%w: %#02x", ErrMalformedElemKind, kind)
			}
			// 0x00 is the only defined elemkind and it means `(ref func)`, so nothing to
			// read off: seg.ElemType already says so, from the non-expr default above.
		}
	}
	// **Appended per element, never preallocated from the declared count** — grave #138's law,
	// as 0016 property 2 requires. `decodeVec` allocates nothing and each element consumes
	// bytes, so a vector claiming 0xFFFFFFFE members runs out of image long before it would
	// allocate; `make([]uint32, 0, n)` here is 16 GiB from a five-byte immediate.
	elem := func(r *reader) error {
		idx, err := r.u32()
		if err != nil {
			return err
		}
		seg.Funcs = append(seg.Funcs, idx)
		return nil
	}
	if seg.ByExpr {
		elem = func(r *reader) error {
			e, casts, err := d.decodeConstExprKeep(r)
			if err != nil {
				return err
			}
			// Appended in one statement with `Exprs`, which is what keeps `ExprCasts`
			// index-parallel to it: two appends in sequence are two chances for an early
			// return to leave the slices at different lengths, and a side table one row
			// short answers a live index with its neighbour's heaptype.
			seg.Exprs, seg.ExprCasts = append(seg.Exprs, e), append(seg.ExprCasts, casts)
			return nil
		}
	}
	if err := d.decodeVec(r, elem); err != nil {
		return err
	}
	d.mod().Elems = append(d.mod().Elems, seg)
	return nil
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
// Retaining as of 0015: the mode and the offset are staged into a DataSegment and appended to
// the module's index order, where they used to be read and dropped. #7's memory work is the
// consumer that made this load-bearing — before it, nothing in the codebase could represent a
// module's data at all, which is what left the wat encoder's round-trip witness byte-level.
func (d *Decoder) decodeDataSegment(r *reader) error {
	seg, err := d.decodeDataSegmentMode(r)
	if err != nil {
		return err
	}
	// ErrPayloadEnd, not ErrSectionOverrun: binary.wast:877 is a segment declaring 7
	// content bytes with 6 left in the image and expects "unexpected end of section or
	// function", where the same overrun on an export *name* (:754) is "length out of
	// bounds". See byteVecErr for why that split is a per-call-site choice.
	init, err := r.byteVecErr(ErrPayloadEnd)
	if err != nil {
		return err
	}
	// Aliased, not copied — the decoder's in-place posture. A segment's bytes are already
	// the caller's image, and `Init` being empty is legal rather than a missing read:
	// `(data "")` is a real module in memory64.wast.
	seg.Init = init
	d.mod().Datas = append(d.mod().Datas, seg)
	return nil
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
// Retaining as of 0015: it returns the staged segment rather than reporting only whether the
// mode read. `decodeConstExprKeep` is the retaining twin whose doc comment named #7 as its
// missing consumer; this is that consumer arriving.
func (d *Decoder) decodeDataSegmentMode(r *reader) (DataSegment, error) {
	var seg DataSegment
	flags, err := r.u32()
	if err != nil {
		return seg, err
	}
	switch flags {
	case 0x00: // active, memory 0 implied
		seg.Offset, seg.OffsetCasts, err = d.decodeConstExprKeep(r)
		return seg, err
	case 0x01: // passive: no memory index, no offset
		seg.Passive = true
		return seg, nil
	case 0x02: // active with an explicit memory index
		// Retained where it used to be discarded. The index is *recorded*, not checked:
		// whether it names a memory the module has is #9's question, and data1.wast's 14
		// vectors turn on the difference — `(data (memory 2) …)` against three memories is
		// a module that traps, not one that is invalid.
		if seg.MemIndex, err = r.u32(); err != nil {
			return seg, err
		}
		seg.Offset, seg.OffsetCasts, err = d.decodeConstExprKeep(r)
		return seg, err
	default:
		return seg, fmt.Errorf("%w: %#02x", ErrMalformedDataSegKind, flags)
	}
}
