// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package validate

import (
	"errors"
	"fmt"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// Slice 7 of #9's validator: **the 0xFB region's typing arms** — GC's struct, array, i31, cast and
// extern-convert instructions, `valid.ml:492-858`.
//
// # It is slice 7 because 5 is claimed twice and 6 by a slice that does not claim itself
//
// `bulk.go` opens "Slice 5 of #9's validator" and `validate.go`'s subtype-relation paragraph opens
// "Slice 5: the subtype relation"; slice 6 is the reference-type slice (#359/#363), named as 6 in
// `bulk_test.go` and `validate_test.go` and in ADR 0027 but never in `ref.go`'s own header. So 7 is
// the first ordinal no slice claims, and ADR 0032 records the collision rather than renumbering a
// landed slice — a renumber would falsify every citation pointing at it.
//
// # The boundary this retires, quoted where it stood
//
// Two sentences declared this region out of scope, and they disagreed with each other about the
// present. `instr.go`'s region dispatch said "**0xFB and 0xFE stay declined** — which is what keeps
// an unchecked module from being reported valid, and keeps the decline census a work plan for the
// slices that own them rather than a silence"; that half was accurate and is now half-true, 0xFE
// staying exactly as it was. `validate.go`'s slice-2 paragraph said "what remains declined is the
// three prefixed regions: 0xFB (GC), 0xFC (bulk memory/table), 0xFE (threads)", which had been
// **stale on 0xFC since slice 5** and was found by this slice reading its own boundary. Both are
// amended in the PR that lands this file, per ADR 0032.
//
// Unlike slice 5's move, this one holds no false accepts in reserve: all 81 rows the ADR
// pre-registers are **declines**, measured `declined=81 of 81`. The region was honestly refused.
//
// # Thirty-one opcodes, twenty-one arms, and the collapse is the bound
//
// `binary.opTableFB` has 31 contiguous entries and `valid.ml` has **21** arms for them. The
// difference is eight parameterized constructors: `StructNew`/`ArrayNew` take an `initop`,
// `StructGet`/`ArrayGet` an `exto`, `RefTest`/`RefCast` a nullability, `I31Get` an `ext`,
// `ExternConvert` a direction. 31 − 10 = 21.
//
// **Every one of the eight lands here as one function taking the reference's own parameter**, and
// that is a correctness bound rather than a tidiness preference. The decline message names the
// *first* offending instruction in a module, so a shadowed sibling never reports: `extern.wast`'s
// only key reads `(ref_i31)` while the file's modules hold six `extern.convert_any` /
// `any.convert_extern` occurrences, and `0x0f` (`array_len`) appears in no key at all while
// `array.wast` calls `array.len` four times. An implementation with one code path per opcode can
// therefore get `struct.get_s` right and `struct.get_u` wrong and still pass all 81 rows.
// Parameterizing makes that divergence unrepresentable instead of merely untested — ADR 0031's
// falsification lesson applied before the fact rather than after it.
//
// `internal/interp` already made this exact collapse, with the same parameter name: its
// `execStructGet` takes a `fieldExt` and its `execFB` supplies the three values at three opcodes.
// *Lessons are indexed by shape, not by file*, and this is the shape arriving at its second site
// with the first site's answer rather than a fresh one.
//
// # The authority's error strings, five of which the corpus witnesses
//
// The 27 reject-direction rows expect exactly five strings — `type mismatch` (17), `immutable
// array` (5), `array types do not match` (3), `array type is not numeric or vector` (1), `immutable
// field` (1). The reference's other strings in this region (`non-structure type`, `non-array type`,
// `field is packed`, `array is unpacked`, `unknown field`, both not-defaultable forms) appear
// **nowhere in the corpus**, checked. They are transcribed verbatim anyway, per 0003: the authority
// is `valid.ml` and the suite only samples it, so a rule whose message nothing checks is still a
// rule whose message is the reference's.

// prefixGC is the GC region's prefix byte.
//
// Local for prefixSIMD's and prefixBulk's reason, which those constants state in full.
const prefixGC = 0xfb

// The 0xFB region's sub-opcodes, all 31 of them, `0x00`-`0x1e` contiguous.
//
// Named here rather than read from `binary` for `bulk.go`'s and `ref.go`'s reason, and checked
// against the generated table by a control rather than trusted — a named constant is a
// transcription of a generated row, and an unchecked transcription is two authorities on one fact.
const (
	fbStructNew        = 0x00
	fbStructNewDefault = 0x01
	fbStructGet        = 0x02
	fbStructGetS       = 0x03
	fbStructGetU       = 0x04
	fbStructSet        = 0x05
	fbArrayNew         = 0x06
	fbArrayNewDefault  = 0x07
	fbArrayNewFixed    = 0x08
	fbArrayNewData     = 0x09
	fbArrayNewElem     = 0x0a
	fbArrayGet         = 0x0b
	fbArrayGetS        = 0x0c
	fbArrayGetU        = 0x0d
	fbArraySet         = 0x0e
	fbArrayLen         = 0x0f
	fbArrayFill        = 0x10
	fbArrayCopy        = 0x11
	fbArrayInitData    = 0x12
	fbArrayInitElem    = 0x13
	fbRefTest          = 0x14
	fbRefTestNull      = 0x15
	fbRefCast          = 0x16
	fbRefCastNull      = 0x17
	fbBrOnCast         = 0x18
	fbBrOnCastFail     = 0x19
	fbAnyConvertExtern = 0x1a
	fbExternConvertAny = 0x1b
	fbRefI31           = 0x1c
	fbI31GetS          = 0x1d
	fbI31GetU          = 0x1e
)

// fieldExt is `exto` — whether an accessor named a sign extension, and which.
//
// **Three values and not a bool, though only the boolean distinction is typed here.** Both
// `require`s that consume it ask `exto <> None`, so `extS` and `extU` are indistinguishable to
// every rule in this file. The third value exists anyway because it is the reference's own shape
// and `internal/interp`'s, where the distinction *is* semantic — the unpacking sign. A bool here
// would be a second, narrower vocabulary for one wire fact, and the arm that later needs the sign
// would have to reintroduce it.
type fieldExt byte

const (
	extNone fieldExt = iota
	extS
	extU
)

// packed reports whether this accessor named an extension — the reference's `exto <> None`.
func (e fieldExt) packed() bool { return e != extNone }

// The region's error set. Each message is the reference's verbatim, per 0003; the five the corpus
// witnesses are named in this file's header, and the rest are transcribed on the authority rather
// than on a vector.
var (
	// ErrImmutableField is `StructSet`'s mutability require (`valid.ml:775`). One corpus row.
	ErrImmutableField = errors.New("immutable field")

	// ErrImmutableArray is the mutability require shared by `array.set`, `array.copy`,
	// `array.fill`, `array.init_data` and `array.init_elem` (`valid.ml:817`, `:827`, `:833`,
	// `:839`, `:848`) — five arms, one string, five corpus rows. Not five sentinels: the reference
	// writes the same `require` five times, and a reader distinguishing them by message would be
	// distinguishing something the authority does not.
	ErrImmutableArray = errors.New("immutable array")

	// ErrArrayTypesMismatch is `array.copy`'s storage-type require (`valid.ml:828`). Three corpus
	// rows.
	ErrArrayTypesMismatch = errors.New("array types do not match")

	// ErrArrayNotNumericOrVector is the require `array.new_data` and `array.init_data` share
	// (`valid.ml:804`, `:842`): a data segment supplies bytes, so the element must be a number or
	// a vector. One corpus row.
	ErrArrayNotNumericOrVector = errors.New("array type is not numeric or vector")

	// ErrFieldNotDefaultable is `struct.new_default`'s require (`valid.ml:756`) and
	// ErrArrayNotDefaultable is `array.new_default`'s (`:783`). Two sentinels because the reference
	// writes two strings; no corpus row for either.
	ErrFieldNotDefaultable = errors.New("field type is not defaultable")
	ErrArrayNotDefaultable = errors.New("array type is not defaultable")

	// ErrUnknownField is the field-index bound on `struct.get`/`struct.set` (`valid.ml:763`,
	// `:773`). No corpus row. It is an index-space arrival of exactly the kind the bounds checks in
	// this package exist for: omitting it would index a field list out of range.
	ErrUnknownField = errors.New("unknown field")

	// The four packing requires, one sentinel per reference string. `struct.get` on a packed field
	// and `struct.get_s` on an unpacked one are both errors, and the reference reports them with
	// the word for what the field *is* rather than for what the instruction wanted
	// (`valid.ml:766`, `:811`) — which reads backwards until you notice it names the fact and not
	// the expectation. No corpus rows.
	ErrFieldPacked   = errors.New("field is packed")
	ErrFieldUnpacked = errors.New("field is unpacked")
	ErrArrayPacked   = errors.New("array is packed")
	ErrArrayUnpacked = errors.New("array is unpacked")

	// ErrNonStructureType and ErrNonArrayType are `struct_type`'s and `array_type`'s kind checks
	// (`valid.ml:66-74`) — the index resolves and names the wrong shape.
	//
	// **These follow the reference where `funcType`'s sibling check does not**, and the
	// disagreement is flagged rather than silently propagated. `funcType`'s comment claims "the
	// suite's string for this is `type mismatch`, not `unknown type`"; the corpus contains no
	// `non-function type` vector *and* no vector for that case at all, so the claim cites a suite
	// fact the suite does not carry. Changing a landed slice's message on no witness is not this
	// slice's call, so `funcType` is untouched and the inconsistency is in ADR 0032's
	// decisions-needed rather than resolved here.
	ErrNonStructureType = errors.New("non-structure type")
	ErrNonArrayType     = errors.New("non-array type")
)

// gcInstr types one 0xFB instruction.
//
// **A switch over 31 opcodes onto 21 arms, and the many-to-one rows are adjacent** so the collapse
// is visible at the dispatch rather than only in the arms' signatures. `i` is the instruction index,
// needed by the four cast rows to reach the retained reference types — the same arrangement
// `internal/interp`'s `execFB` uses, charging the side-table lookup to the arms that want it
// (0027 decision 1).
func (v *validator) gcInstr(i int, in binary.Instr) error {
	switch in.Op {
	case fbStructNew:
		return v.structNew(uint32(in.Imm0), true)
	case fbStructNewDefault:
		return v.structNew(uint32(in.Imm0), false)

	case fbStructGet:
		return v.structGet(uint32(in.Imm0), uint32(in.Imm1), extNone)
	case fbStructGetS:
		return v.structGet(uint32(in.Imm0), uint32(in.Imm1), extS)
	case fbStructGetU:
		return v.structGet(uint32(in.Imm0), uint32(in.Imm1), extU)

	case fbStructSet:
		return v.structSet(uint32(in.Imm0), uint32(in.Imm1))

	case fbArrayNew:
		return v.arrayNew(uint32(in.Imm0), true)
	case fbArrayNewDefault:
		return v.arrayNew(uint32(in.Imm0), false)

	case fbArrayNewFixed:
		return v.arrayNewFixed(uint32(in.Imm0), uint32(in.Imm1))
	case fbArrayNewData:
		return v.arrayNewData(uint32(in.Imm0), uint32(in.Imm1))
	case fbArrayNewElem:
		return v.arrayNewElem(uint32(in.Imm0), uint32(in.Imm1))

	case fbArrayGet:
		return v.arrayGet(uint32(in.Imm0), extNone)
	case fbArrayGetS:
		return v.arrayGet(uint32(in.Imm0), extS)
	case fbArrayGetU:
		return v.arrayGet(uint32(in.Imm0), extU)

	case fbArraySet:
		return v.arraySet(uint32(in.Imm0))
	case fbArrayLen:
		return v.arrayLen()
	case fbArrayFill:
		return v.arrayFill(uint32(in.Imm0))
	case fbArrayCopy:
		return v.arrayCopy(uint32(in.Imm0), uint32(in.Imm1))
	case fbArrayInitData:
		return v.arrayInitData(uint32(in.Imm0), uint32(in.Imm1))
	case fbArrayInitElem:
		return v.arrayInitElem(uint32(in.Imm0), uint32(in.Imm1))

	// The cast four collapse to two arms: nullability rides in the retained reftype rather than in
	// the opcode, the decoder's `castTypes` having resolved it from the opcode at decode time.
	case fbRefTest, fbRefTestNull:
		return v.refTest(i)
	case fbRefCast, fbRefCastNull:
		return v.refCast(i)

	// **The label is `Imm1`, not `Imm0`** — this pair stages its flags byte first. Read through
	// `binary.BrOnCastLabel` rather than spelled here, which makes misreading it require *ignoring*
	// something rather than merely forgetting it; that accessor's comment carries the measurement
	// and names the control that pins it.
	case fbBrOnCast:
		return v.brOnCast(i, uint32(binary.BrOnCastLabel(in)), false)
	case fbBrOnCastFail:
		return v.brOnCast(i, uint32(binary.BrOnCastLabel(in)), true)

	case fbAnyConvertExtern:
		return v.externConvert(binary.HeapExtern, binary.HeapAny)
	case fbExternConvertAny:
		return v.externConvert(binary.HeapAny, binary.HeapExtern)

	case fbRefI31:
		return v.refI31()

	// `I31Get`'s arm ignores its `ext` entirely — `[RefT (Null, I31HT)] --> [NumT I32T]` whichever
	// sign was named — so the two opcodes reach one parameterless function. The strongest form of
	// this file's collapse: there is no parameter left to get wrong.
	case fbI31GetS, fbI31GetU:
		return v.i31Get()
	}
	return fmt.Errorf("%w: prefixed opcode %#02x %#02x", ErrUnsupported, in.Prefix, in.Op)
}

// expandTypeAt is `expand_deftype (type_ c x)` (`valid.ml:44`): the type index space lookup.
//
// The bound is the type section's length and the string is `unknown type`, matching `funcType`'s and
// `checkValType`'s — one index space, one message, whichever slice asks. `match.go`'s `compTypeAt`
// answers the same question with a bool because a *relation* may not raise; this answers it with an
// error because a typing rule must.
func (v *validator) expandTypeAt(idx uint32) (binary.CompType, error) {
	ct, ok := compTypeAt(v.mod, idx)
	if !ok {
		return binary.CompType{}, fmt.Errorf("%w %d (%d in scope)", ErrUnknownType, idx, len(v.mod.Types))
	}
	return ct, nil
}

// structTypeAt is `struct_type c x` (`valid.ml:66-69`): resolve a type index and require a struct.
func (v *validator) structTypeAt(idx uint32) ([]binary.FieldType, error) {
	ct, err := v.expandTypeAt(idx)
	if err != nil {
		return nil, err
	}
	if ct.Kind != binary.CompStruct {
		return nil, fmt.Errorf("%w %d (a %s)", ErrNonStructureType, idx, ct.Kind)
	}
	return ct.Fields, nil
}

// arrayTypeAt is `array_type c x` (`valid.ml:71-74`): resolve a type index and require an array,
// returning its single field.
//
// **The arity check is not redundant with the decoder's.** An `arraytype` is one `fieldtype` with no
// vector, so `Fields` has exactly one entry for every array the decoder built — and this function is
// reached with an index the *module* chose, so a zero-length `Fields` on a `CompArray` would be an
// engine disagreement rather than an invalid module. Indexing `[0]` on the strength of a comment is
// how a panic gets into the package whose job is to decide whether a module is safe to run.
func (v *validator) arrayTypeAt(idx uint32) (binary.FieldType, error) {
	ct, err := v.expandTypeAt(idx)
	if err != nil {
		return binary.FieldType{}, err
	}
	if ct.Kind != binary.CompArray {
		return binary.FieldType{}, fmt.Errorf("%w %d (a %s)", ErrNonArrayType, idx, ct.Kind)
	}
	if len(ct.Fields) != 1 {
		return binary.FieldType{}, fmt.Errorf("%w: type %d is an array with %d fields",
			errArrayArity, idx, len(ct.Fields))
	}
	return ct.Fields[0], nil
}

// errArrayArity is a decoded `CompArray` whose field list is not exactly one long.
//
// Undeclared and unreachable by construction, on `errNoSelectAnnotation`'s posture and for its
// reason: not a decline, and not a verdict about the module — the decoder and this arm disagreeing
// about what an arraytype is, which is an engine bug and belongs in a channel nobody expects to see.
var errArrayArity = errors.New("internal: array comptype with a field count other than one")

// unpacked is `unpacked_storagetype` (`types.ml:116-118`): a packed width reads as an i32.
func unpacked(st binary.StorageType) binary.ValType {
	if st.Packed {
		return binary.I32
	}
	return st.Val
}

// defaultable is `defaultable` (`types.ml:97-101`): numbers and vectors are, a reference is exactly
// when it is nullable.
//
// The reference's `BotT -> assert false` has no counterpart, and that is not a dropped arm: every
// caller derives its argument from a *declared* field type, so `unknown` — this package's bottom —
// cannot arrive. Where the reference asserts, this returns the answer for a non-reference, which is
// the same claim about reachability made without a panic in a validator.
func defaultable(t binary.ValType) bool {
	if t.IsRef() {
		return t.Null()
	}
	return true
}

// isNumOrVec is `is_numtype t || is_vectype t` (`types.ml:80-86`), as the two `require`s that
// consume it spell it.
//
// **Bottom is admitted, because both of the reference's predicates admit it** (`NumT _ | BotT`,
// `VecT _ | BotT`). It cannot arrive here — both callers derive `t` from a declared field type — and
// admitting it anyway is the faithful transcription rather than a guess about which caller changes
// next.
func isNumOrVec(t binary.ValType) bool {
	return t == unknown || !t.IsRef()
}

// refAbstract builds `(ref ht)` / `(ref null ht)` for one of the twelve abstract heaptypes.
//
// **Returns `NoValType` for a byte that is not one of the twelve, rather than an error or a panic.**
// Every call site here passes a `binary.Heap*` constant or `topOfAbstract`'s output, so the case does
// not arise; what makes returning the zero value safe rather than a silent drop is that `NoValType`
// matches *nothing* — a mistake here fails loudly at the first `popExpect` and prints the
// unrepresentable type in its message, instead of being absorbed into a plausible one. The
// alternatives are both worse: an unreachable error arm is a suppression wearing a disguise, and a
// panic in a validator is the failure mode this package exists to prevent.
func refAbstract(heap byte, null bool) binary.ValType {
	t, _ := binary.AbstractRefType(heap, null)
	return t
}

// structNew is `StructNew (x, initop)` (`valid.ml:751-758`), both opcodes.
//
// `explicit` is the reference's `initop`: `struct.new` supplies every field as an operand,
// `struct.new_default` supplies none and requires every field to have a default. One function for
// the two, per this file's header.
func (v *validator) structNew(x uint32, explicit bool) error {
	fts, err := v.structTypeAt(x)
	if err != nil {
		return err
	}
	if !explicit {
		for j, ft := range fts {
			if t := unpacked(ft.Storage); !defaultable(t) {
				return fmt.Errorf("%w (type %d field %d is %s)", ErrFieldNotDefaultable, x, j, t)
			}
		}
	}
	if explicit {
		ts := make([]binary.ValType, len(fts))
		for j, ft := range fts {
			ts[j] = unpacked(ft.Storage)
		}
		if err := v.popExpectAll(ts); err != nil {
			return err
		}
	}
	v.push(binary.RefType(x, false))
	return nil
}

// structGet is `StructGet (x, i, exto)` (`valid.ml:760-768`), all three opcodes.
func (v *validator) structGet(x, field uint32, ext fieldExt) error {
	fts, err := v.structTypeAt(x)
	if err != nil {
		return err
	}
	if field >= uint32(len(fts)) {
		return fmt.Errorf("%w %d (type %d has %d)", ErrUnknownField, field, x, len(fts))
	}
	st := fts[field].Storage
	if ext.packed() != st.Packed {
		// The reference names what the field *is*, not what the instruction wanted.
		if st.Packed {
			return fmt.Errorf("%w (type %d field %d)", ErrFieldPacked, x, field)
		}
		return fmt.Errorf("%w (type %d field %d)", ErrFieldUnpacked, x, field)
	}
	if err := v.popExpect(binary.RefType(x, true)); err != nil {
		return err
	}
	v.push(unpacked(st))
	return nil
}

// structSet is `StructSet (x, i)` (`valid.ml:770-777`).
//
// **No packing check here, and its absence is the reference's**: a write takes the unpacked value
// and narrows it, so `struct.set` on a packed field is legal and has no `_s`/`_u` spelling. The
// asymmetry with `structGet` above is `valid.ml:765`'s require existing and nothing in
// `valid.ml:770-777` answering to it.
func (v *validator) structSet(x, field uint32) error {
	fts, err := v.structTypeAt(x)
	if err != nil {
		return err
	}
	if field >= uint32(len(fts)) {
		return fmt.Errorf("%w %d (type %d has %d)", ErrUnknownField, field, x, len(fts))
	}
	ft := fts[field]
	if !ft.Mutable {
		return fmt.Errorf("%w (type %d field %d)", ErrImmutableField, x, field)
	}
	return v.popExpectAll([]binary.ValType{binary.RefType(x, true), unpacked(ft.Storage)})
}

// arrayNew is `ArrayNew (x, initop)` (`valid.ml:779-785`), both opcodes.
//
// The length operand is present in both forms and the element only in the explicit one, which is
// what `(ts @ [NumT I32T])` says: the element sits *below* the length on the stack.
func (v *validator) arrayNew(x uint32, explicit bool) error {
	ft, err := v.arrayTypeAt(x)
	if err != nil {
		return err
	}
	t := unpacked(ft.Storage)
	if !explicit && !defaultable(t) {
		return fmt.Errorf("%w (type %d is %s)", ErrArrayNotDefaultable, x, t)
	}
	params := []binary.ValType{binary.I32}
	if explicit {
		params = []binary.ValType{t, binary.I32}
	}
	if err := v.popExpectAll(params); err != nil {
		return err
	}
	v.push(binary.RefType(x, false))
	return nil
}

// arrayNewFixed is `ArrayNewFixed (x, n)` (`valid.ml:787-790`): n copies of the element type and no
// length operand — the length is the immediate.
func (v *validator) arrayNewFixed(x, n uint32) error {
	ft, err := v.arrayTypeAt(x)
	if err != nil {
		return err
	}
	t := unpacked(ft.Storage)
	ts := make([]binary.ValType, n)
	for j := range ts {
		ts[j] = t
	}
	if err := v.popExpectAll(ts); err != nil {
		return err
	}
	v.push(binary.RefType(x, false))
	return nil
}

// arrayNewData is `ArrayNewData (x, y)` (`valid.ml:800-806`).
//
// **The data segment is resolved before the element type is judged**, in the reference's order:
// `let () = data c y` precedes the `require`, so `array.new_data 0 99` in a module with one segment
// reports the missing segment and not a complaint about the array's element type. `bulkSignature`'s
// header records the same ordering rule and the vectors that turn on it.
func (v *validator) arrayNewData(x, y uint32) error {
	ft, err := v.arrayTypeAt(x)
	if err != nil {
		return err
	}
	if err := dataSegmentAt(v.mod, y); err != nil {
		return err
	}
	t := unpacked(ft.Storage)
	if !isNumOrVec(t) {
		return fmt.Errorf("%w (type %d is %s)", ErrArrayNotNumericOrVector, x, t)
	}
	if err := v.popExpectAll([]binary.ValType{binary.I32, binary.I32}); err != nil {
		return err
	}
	v.push(binary.RefType(x, false))
	return nil
}

// arrayNewElem is `ArrayNewElem (x, y)` (`valid.ml:792-798`).
//
// The message renders the array's *unpacked* element type where the reference renders its whole
// fieldtype (`string_of_fieldtype ft`), there being no such renderer in `internal/binary`. A
// deliberate divergence in the detail and not in the sentinel: the suite matches on the `type
// mismatch` prefix, and inventing a fieldtype renderer for one message would be a second spelling of
// the wire format's own text.
func (v *validator) arrayNewElem(x, y uint32) error {
	ft, err := v.arrayTypeAt(x)
	if err != nil {
		return err
	}
	rt, err := elemTypeAt(v.mod, y)
	if err != nil {
		return err
	}
	if t := unpacked(ft.Storage); !v.matches(rt, t) {
		return fmt.Errorf("%w: element segment's type %s does not match array's field type %s",
			ErrTypeMismatch, rt, t)
	}
	if err := v.popExpectAll([]binary.ValType{binary.I32, binary.I32}); err != nil {
		return err
	}
	v.push(binary.RefType(x, false))
	return nil
}

// arrayGet is `ArrayGet (x, exto)` (`valid.ml:808-813`), all three opcodes.
func (v *validator) arrayGet(x uint32, ext fieldExt) error {
	ft, err := v.arrayTypeAt(x)
	if err != nil {
		return err
	}
	if ext.packed() != ft.Storage.Packed {
		if ft.Storage.Packed {
			return fmt.Errorf("%w (type %d)", ErrArrayPacked, x)
		}
		return fmt.Errorf("%w (type %d)", ErrArrayUnpacked, x)
	}
	if err := v.popExpectAll([]binary.ValType{binary.RefType(x, true), binary.I32}); err != nil {
		return err
	}
	v.push(unpacked(ft.Storage))
	return nil
}

// arraySet is `ArraySet x` (`valid.ml:815-819`). No packing check, for `structSet`'s reason.
func (v *validator) arraySet(x uint32) error {
	ft, err := v.arrayTypeAt(x)
	if err != nil {
		return err
	}
	if !ft.Mutable {
		return fmt.Errorf("%w (type %d)", ErrImmutableArray, x)
	}
	return v.popExpectAll([]binary.ValType{
		binary.RefType(x, true), binary.I32, unpacked(ft.Storage),
	})
}

// arrayLen is `ArrayLen` (`valid.ml:821-822`).
//
// **The operand is `(ref null array)` — the abstract heaptype, not an indexed type** — which is why
// this is the one arm in the region with no immediate and no type-index lookup. It reaches any array
// through the relation: `matchHeap`'s expand arm maps a `CompArray` to `HeapArray`.
func (v *validator) arrayLen() error {
	if err := v.popExpect(refAbstract(binary.HeapArray, true)); err != nil {
		return err
	}
	v.push(binary.I32)
	return nil
}

// arrayCopy is `ArrayCopy (x, y)` (`valid.ml:824-829`): destination x, source y.
//
// **Only the destination's mutability is required**, and the storage comparison is one-directional —
// the source's storage must match the destination's, not the reverse. `matchStorageType` is slice
// 5's, so this arm is a consumer of the relation rather than a second comparator (#368's lesson).
func (v *validator) arrayCopy(x, y uint32) error {
	dst, err := v.arrayTypeAt(x)
	if err != nil {
		return err
	}
	src, err := v.arrayTypeAt(y)
	if err != nil {
		return err
	}
	if !dst.Mutable {
		return fmt.Errorf("%w (type %d)", ErrImmutableArray, x)
	}
	if !matchStorageType(tctx{gotMod: v.mod, wantMod: v.mod}, src.Storage, dst.Storage) {
		return fmt.Errorf("%w (source type %d, destination type %d)", ErrArrayTypesMismatch, y, x)
	}
	return v.popExpectAll([]binary.ValType{
		binary.RefType(x, true), binary.I32, binary.RefType(y, true), binary.I32, binary.I32,
	})
}

// arrayFill is `ArrayFill x` (`valid.ml:831-835`).
func (v *validator) arrayFill(x uint32) error {
	ft, err := v.arrayTypeAt(x)
	if err != nil {
		return err
	}
	if !ft.Mutable {
		return fmt.Errorf("%w (type %d)", ErrImmutableArray, x)
	}
	return v.popExpectAll([]binary.ValType{
		binary.RefType(x, true), binary.I32, unpacked(ft.Storage), binary.I32,
	})
}

// arrayInitData is `ArrayInitData (x, y)` (`valid.ml:837-844`).
//
// The three `require`s run in the reference's order — mutability, then the segment lookup, then the
// element's numeric-or-vector check — and the order is observable: an immutable array initialized
// from a missing segment reports `immutable array`.
func (v *validator) arrayInitData(x, y uint32) error {
	ft, err := v.arrayTypeAt(x)
	if err != nil {
		return err
	}
	if !ft.Mutable {
		return fmt.Errorf("%w (type %d)", ErrImmutableArray, x)
	}
	if err := dataSegmentAt(v.mod, y); err != nil {
		return err
	}
	t := unpacked(ft.Storage)
	if !isNumOrVec(t) {
		return fmt.Errorf("%w (type %d is %s)", ErrArrayNotNumericOrVector, x, t)
	}
	return v.popExpectAll([]binary.ValType{
		binary.RefType(x, true), binary.I32, binary.I32, binary.I32,
	})
}

// arrayInitElem is `ArrayInitElem (x, y)` (`valid.ml:846-853`).
func (v *validator) arrayInitElem(x, y uint32) error {
	ft, err := v.arrayTypeAt(x)
	if err != nil {
		return err
	}
	if !ft.Mutable {
		return fmt.Errorf("%w (type %d)", ErrImmutableArray, x)
	}
	rt, err := elemTypeAt(v.mod, y)
	if err != nil {
		return err
	}
	if t := unpacked(ft.Storage); !v.matches(rt, t) {
		return fmt.Errorf("%w: element segment's type %s does not match array's field type %s",
			ErrTypeMismatch, rt, t)
	}
	return v.popExpectAll([]binary.ValType{
		binary.RefType(x, true), binary.I32, binary.I32, binary.I32,
	})
}

// castTypes reads the reftypes the decoder retained for a cast-family instruction.
//
// One reftype for `ref.test`/`ref.cast`, two for the `br_on_cast` pair, and the null bits are
// already resolved: the decoder's own `castTypes` did it from three different sources (the opcode
// for `fb 14`-`fb 17`, a flags byte for `fb 18`/`fb 19`, the instruction's meaning for `d0`), which
// is the whole argument its comment makes for not re-deriving them per consumer.
// **It validates the reftypes it returns, and that is why no caller does.** All three cast-family
// arms want `check_reftype` on every immediate before using it — the reference calls it first in each
// of `RefTest`, `RefCast`, `BrOnCast` and `BrOnCastFail` — so the check belongs to the accessor rather
// than being repeated three times with two chances to forget. It also removes a `govet shadow` /
// `gocritic sloppyReassign` standoff at the call sites, where the two linters wanted `=` and `:=` for
// the same `err`: the fix for a defensible-looking pair of findings was that the check was in the
// wrong function, which is the spirit clause pointing at the code instead of at the config.
func (v *validator) castTypes(i, want int) ([]binary.ValType, error) {
	ts, ok := v.curFunc.CastTypes(i)
	if !ok || len(ts) != want {
		return nil, fmt.Errorf("%w (instruction %d: got %d, want %d)", errNoCastTypes, i, len(ts), want)
	}
	for _, t := range ts {
		if err := v.checkValType(t); err != nil {
			return nil, err
		}
	}
	return ts, nil
}

// errNoCastTypes is a cast-family instruction reaching the validator without its retained reftypes.
//
// Undeclared and unreachable by construction, on `errNoSelectAnnotation`'s posture: the decoder
// files a vector for every one of the opcodes that has one, so this is the decoder and this file
// disagreeing rather than a fact about the module.
var errNoCastTypes = errors.New("internal: cast-family instruction with no retained reference types")

// refTest is `RefTest rt` (`valid.ml:732-735`), both opcodes.
//
// **The operand is the top of `rt`'s own hierarchy, not `rt`** — `[RefT (Null, top_of_heaptype ht)]`
// — which is what makes `ref.test` a *test*: it accepts anything in the same hierarchy and answers
// i32. Popping `rt` itself would require the operand to already be what the test asks about, and
// every `(ref.test (ref $t) (local.get $anyref))` in the corpus would be refused.
func (v *validator) refTest(i int) error {
	ts, err := v.castTypes(i, 1)
	if err != nil {
		return err
	}
	top, err := v.topOf(ts[0])
	if err != nil {
		return err
	}
	if err := v.popExpect(top); err != nil {
		return err
	}
	v.push(binary.I32)
	return nil
}

// refCast is `RefCast rt` (`valid.ml:737-740`), both opcodes: the same operand as `ref.test`, and
// the result is the named reftype with its own nullability.
func (v *validator) refCast(i int) error {
	ts, err := v.castTypes(i, 1)
	if err != nil {
		return err
	}
	top, err := v.topOf(ts[0])
	if err != nil {
		return err
	}
	if err := v.popExpect(top); err != nil {
		return err
	}
	v.push(ts[0])
	return nil
}

// topOf is `top_of_heaptype` (`match.ml:25-31`) lifted to a reftype: `(ref null T)` where T is the
// top of the hierarchy the argument's heaptype sits in.
//
// The indexed form resolves through the type space — a struct or array is under `any`, a functype
// under `func` — which is `top_of_typeuse`. `abstractOfCompType` is slice 5's and is reused rather
// than re-cased: it deliberately does *not* collapse struct and array the way the reference's
// `abs_of_comptype` does, and for this consumer that is immaterial, both leading to `any`.
func (v *validator) topOf(t binary.ValType) (binary.ValType, error) {
	if t.IsIndexed() {
		ct, err := v.expandTypeAt(t.Index())
		if err != nil {
			return binary.ValType{}, err
		}
		return refAbstract(topOfAbstract(abstractOfCompType(ct.Kind)), true), nil
	}
	kind, ok := t.Kind()
	if !ok {
		return binary.ValType{}, fmt.Errorf("%w: %s has no heap type", ErrTypeMismatch, t)
	}
	return refAbstract(topOfAbstract(kind), true), nil
}

// topOfAbstract is `top_of_heaptype`'s abstract rows (`match.ml:25-31`): four hierarchies, each with
// one top.
//
// Transcribed as the reference writes it rather than derived from `bottomHierarchyTop`'s inverse:
// that function maps four bottoms to four tops, this one maps *twelve* heaptypes onto the same four,
// and inverting a partial map would leave the eight non-bottom rows to a `default` — which is how a
// wrong answer arrives by omission rather than by a wrong arm.
func topOfAbstract(heap byte) byte {
	switch heap {
	case binary.HeapAny, binary.HeapNone, binary.HeapEq, binary.HeapStruct,
		binary.HeapArray, binary.HeapI31:
		return binary.HeapAny
	case binary.HeapFunc, binary.HeapNoFunc:
		return binary.HeapFunc
	case binary.HeapExn, binary.HeapNoExn:
		return binary.HeapExn
	default:
		// `extern`/`noextern`, the fourth hierarchy. A `default` rather than a fifth case pair
		// because the twelve are exhaustive and these two are what remains; the alternative is an
		// unreachable error arm, which is a suppression wearing a disguise.
		return binary.HeapExtern
	}
}

// externConvert is `ExternConvert op` (`valid.ml:855-858`), both opcodes.
//
//	let ht1, ht2 = type_externop op in
//	let (nul, _ht) = peek_ref 0 s e.at in
//	[RefT (nul, ht1)] --> [RefT (nul, ht2)], []
//
// **Nullability-polymorphic in both directions: the peeked operand's null bit types the operand as
// well as the result.** That is the only reason this arm cannot be a signature — a signature is a
// pair of type lists, and both of this one's lists depend on what is on the stack.
//
// **And the bottom case is non-null, which is the opposite of the obvious guess.** `peek_ref` returns
// `(NoNull, BotHT)` for a bottom or absent operand (`valid.ml:289`), not `(Null, …)` — so on an empty
// stack this pops `(ref extern)` and pushes `(ref any)`. The pop still succeeds, an unreachable
// frame's pad matching anything; what the choice decides is the *result*'s nullability, and reading
// it as nullable would push a nullable reference where the reference pushes a non-null one. Taken
// from the authority rather than from the shape of the neighbouring arms, every one of which spells
// `Null` in this position.
//
// `peek_ref` does raise for a non-reference operand that is not bottom, and that arm is kept:
// `refIsNull`'s comment records the same division between peeking and popping.
func (v *validator) externConvert(from, to byte) error {
	null := false
	if t := v.peekN(1)[0]; t != unknown {
		if !t.IsRef() {
			return fmt.Errorf("%w: instruction requires reference type but stack has %s",
				ErrTypeMismatch, t)
		}
		null = t.Null()
	}
	if err := v.popExpect(refAbstract(from, null)); err != nil {
		return err
	}
	v.push(refAbstract(to, null))
	return nil
}

// refI31 is `RefI31` (`valid.ml:745-746`): an i32 becomes a non-nullable `(ref i31)`.
func (v *validator) refI31() error {
	if err := v.popExpect(binary.I32); err != nil {
		return err
	}
	v.push(refAbstract(binary.HeapI31, false))
	return nil
}

// i31Get is `I31Get ext` (`valid.ml:748-749`), both opcodes.
//
// The reference's arm binds `ext` and never reads it: the type is `[RefT (Null, I31HT)] --> [NumT
// I32T]` for both spellings, the sign mattering only at run time. So this takes no parameter, and
// the two opcodes cannot diverge here because there is nothing for them to diverge on.
func (v *validator) i31Get() error {
	if err := v.popExpect(refAbstract(binary.HeapI31, true)); err != nil {
		return err
	}
	v.push(binary.I32)
	return nil
}

// brOnCast is `BrOnCast (x, rt1, rt2)` and `BrOnCastFail (x, rt1, rt2)` (`valid.ml:492-506`,
// `:508-523`) — one function, `onFail` selecting which.
//
// # The two arms differ in exactly two places, and folding them names both
//
// `rt1` is the operand's declared type and `rt2` the type being cast to. Both arms pop
// `ts0 ++ [rt1]`, both require `rt2 <: rt1`, and both require the label to be non-empty and its last
// type to admit what the *branch* carries. What differs:
//
//   - **which type the branch carries**: `br_on_cast` branches when the cast succeeds, so the label
//     receives `rt2`; `br_on_cast_fail` branches when it fails, so the label receives
//     `diff_reftype rt1 rt2` — `rt1`'s heaptype with the nullability a failed cast leaves.
//   - **what falls through**: the mirror. `br_on_cast` leaves `diff_reftype rt1 rt2` on the stack,
//     `br_on_cast_fail` leaves `rt2`.
//
// Written as one function because those two facts are one swap, and the pair is the region's clearest
// case of the sibling hazard this file's header is about: an implementation with two bodies can put
// the `diff` on the wrong side of one of them, and the corpus splits its twelve rows 6/6 across the
// two opcodes rather than concentrating them on either.
//
// The label lookup and the empty-label require come *before* the operand pops, in the reference's
// order.
func (v *validator) brOnCast(i int, depth uint32, onFail bool) error {
	ts, err := v.castTypes(i, 2)
	if err != nil {
		return err
	}
	rt1, rt2 := ts[0], ts[1]
	if !v.matches(rt2, rt1) {
		return fmt.Errorf("%w on cast: type %s does not match %s", ErrTypeMismatch, rt2, rt1)
	}

	// The type the branch carries and the type that falls through: one swap, per the header.
	branched, fallthru := rt2, diffRefType(rt1, rt2)
	if onFail {
		branched, fallthru = fallthru, branched
	}

	f, err := v.label(depth)
	if err != nil {
		return err
	}
	// `require (label c x <> [])` and `match_valtype (RefT branched) t1` carry the same message in
	// the reference, which is why they share one formatting site here rather than reading as two
	// unrelated refusals.
	if len(f.labelTypes) == 0 || !v.matches(branched, f.labelTypes[len(f.labelTypes)-1]) {
		return fmt.Errorf("%w: instruction requires type %s but label has %s",
			ErrTypeMismatch, branched, typeList(f.labelTypes))
	}

	// `(ts0 @ [RefT rt1]) --> (ts0 @ [fallthru])`: `ts0` is the label's types without their last, so
	// only the reference on top is exchanged and everything below it is popped and pushed unchanged.
	ts0 := f.labelTypes[:len(f.labelTypes)-1]
	if err := v.popExpect(rt1); err != nil {
		return err
	}
	if err := v.popExpectAll(ts0); err != nil {
		return err
	}
	v.pushAll(ts0)
	v.push(fallthru)
	return nil
}

// diffRefType is `diff_reftype` (`valid.ml:234-237`): `rt1`'s heaptype, with `rt1`'s nullability
// unless `rt2` was nullable — in which case a failed cast cannot have been a null, so the result is
// non-nullable.
//
// **The heaptype is always `rt1`'s and never `rt2`'s**, which reads like a subtraction and is not:
// the reference keeps `ht1` in both arms. Taking `ht2` would narrow a `br_on_cast`'s fall-through to
// the very type the cast *failed* to produce.
func diffRefType(rt1, rt2 binary.ValType) binary.ValType {
	if !rt2.Null() {
		return rt1
	}
	// `rt1` with its null bit cleared. Two constructors rather than one, the null bit and the
	// heaptype living in different places for the indexed and the abstract forms; `binary` exports
	// no nullability setter, and asking it for one would export a mutator for a wire fact.
	if rt1.IsIndexed() {
		return binary.RefType(rt1.Index(), false)
	}
	kind, ok := rt1.Kind()
	if !ok {
		// Neither indexed nor keyed by a wire byte: not a reference type at all, which
		// `check_reftype` has already refused at both call sites. Returned unchanged rather than
		// silently coerced, so a caller that skips that check sees its own type back.
		return rt1
	}
	return refAbstract(kind, false)
}
