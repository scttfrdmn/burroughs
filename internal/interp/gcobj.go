// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package interp

import (
	"fmt"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// Decision 0020's object model: a struct or array instance is a Go pointer the collector
// already traces.
//
// # Why a Go pointer and not a handle into a side table
//
// 0020 priced both and chose the pointer for the thesis' reason rather than for elegance: the
// engine is language-directed for Go (contract §0), so the allocator and the collector the guest's
// objects want *already exist* and are the ones the host runtime is built around. A handle table
// would mean this package reimplementing allocation, liveness, and eventually compaction — three
// mechanisms Go supplies — and would put every GC object behind an indirection the Go collector
// cannot see through, which is the *opposite* of 0002's parallel-array pin. `ref.Obj` is a real Go
// pointer in a real Go struct field, so tracing is the collector's job and there is nothing here to
// keep in sync with it.
//
// This composes with 0002's pin instead of amending it: references live in `stack.refs`/`frame.refs`
// because a pointer inside a `uint64` is invisible to the collector, and `Obj` is one more pointer
// *inside a `ref`*, which is already in a traced array. No new array, no new exemption.
//
// # `ref.eq` is Go pointer comparison, and that is a citation
//
// `Value.eq_ref' = ref (==)` (value.ml:127) is OCaml physical equality, and **`aggr.ml` registers no
// `eq_ref'` override at all** — the file adds hooks for `type_of_ref'` and `string_of_ref'` and
// deliberately not for equality. So a struct or array instance's identity *is* its allocation, in
// the reference too, and `a.Obj == b.Obj` is the specified semantics rather than a cheap
// implementation that got lucky. See refEq, which this rung teaches that case.
//
// # Allocation count, stated rather than claimed
//
// 0020's consequences say "one Go allocation per `struct.new`". It is **two** as implemented — the
// `gcObj` and its `fields` slice — and the honest number is worth writing down rather than rounding
// to the ADR's. A single allocation would need the field storage inlined at a fixed arity or the two
// halves split into parallel slices, which is more allocations, not fewer. The property 0020
// actually needs is that allocation is *the Go allocator's*, and that holds.

// gcObj is a struct or array instance — `Aggr.Struct of deftype * field list` (aggr.ml:8), the
// deftype and the fields, nothing else.
//
// **A pointer to one of these is the value**, which is what makes the shared-mutation vectors work
// without any aliasing machinery: `struct.wast`'s `set_get_packed_g0_1` writes field 1 of the struct
// held by global `g0` and then reads it back through *another* `global.get`, so the two `ref` copies
// must name one object. They do, because copying a `ref` copies the `*gcObj` and not the fields.
type gcObj struct {
	// mod and typeIdx are the declared comptype this object was allocated against —
	// `type_of_struct` (aggr.ml:54), the fact `type_of_ref'` reports for a `StructRef` and
	// therefore the input to every `ref.test`/`ref.cast`.
	//
	// **The defining module travels *on the object*, which is 0027 decision 5.** A `deftype` in
	// the reference is self-contained; a type index here means nothing without the type space it
	// indexes, and `ref.cast` compares an object's type against an immediate belonging to the
	// *executing* module — two different modules whenever the object crossed an import. The
	// alternative was `ref.Inst`, and it loses on a counted fact rather than on taste: a `ref` is
	// copied through seven paths (stack, locals, globals, tables, struct fields, array elements,
	// element segments) and every one that forgot to carry the instance would produce an object
	// whose type resolves against the wrong type space — silently, since a wrong index is still an
	// index. Here it cannot be forgotten, because copying the `*gcObj` copies it.
	//
	// **Was a bare `*binary.CompType`, and rung 5 is what made the index the load-bearing half.**
	// `matchDeftype` walks declared supertypes, which are *indices*, so a resolved pointer cannot
	// be the retained form: the walk would have nothing to walk from. Both facts sit here rather
	// than beside a cached pointer for this file's own stated reason (see gcField on `packed`) —
	// two places knowing one thing is the drift shape #78/#105/#106 were filed for — and
	// `comptype` derives the pointer on demand.
	//
	// The previous field's history is worth keeping: it was retained *for* this rung and was
	// measured **not** load-bearing before it, the two types being the same pointer on 30 of 30
	// corpus field accesses. That measurement retired `fieldStorage`'s agreement check (#247,
	// second rider) and filed #248 to reinstate it once casts gave it a subject, which is now.
	mod     *binary.Module
	typeIdx uint32

	// fields is one entry per declared field, in declaration order — `field list`, and for an
	// array exactly the elements (rung 3's consumer).
	fields []gcField
}

// alloc builds a gcObj against the type index the instruction named, recording the type space that
// index belongs to.
//
// One constructor for all seven allocation sites rather than a literal at each, so the provenance
// pair cannot be half-written: a `gcObj{fields: …}` with no `mod` compiles, and its every later
// `ref.cast` panics on a nil module. `idx` is the caller's own already-validated immediate.
//
// **A `comptype()` accessor was written here and deleted before the PR landed, and the deletion is
// worth a sentence because the reasoning that justified it was sound and its premise was wrong.**
// The method resolved `&o.mod.Types[o.typeIdx]` with a documented no-range-check rationale (every
// caller of `alloc` has already resolved the index through `structType`/`arrayType`), and it had no
// consumer: the cast family compares a *heaptype* against a heaptype, and `matchConcreteAbstract`
// takes the `(mod, idx)` pair through `compTypeAt` because a target type comes from the module's own
// immediate and has no object behind it at all. `unused` is what said so. A helper written for a
// consumer that turned out not to want it is the accept-direction of dead code — it compiles,
// reviews cleanly, and documents an access pattern nothing uses.
func (in *Instance) alloc(idx uint64, fields []gcField) *gcObj {
	return &gcObj{mod: in.mod, typeIdx: uint32(idx), fields: fields}
}

// gcField is one field's storage: the numeric half, the reference half, and the v128 high half.
//
// **Three homes rather than a tagged union, and the field's declared type says which is live** —
// `global`'s own rule (global.go) and `frame`'s, for 0002's reason: a null reference and the integer
// zero are the same bits, so nothing can be recovered by inspecting the slot.
//
// **`hi` is decision 0024's third case, present because grave #243 was filed for its absence.** A
// struct field's storage type is a `valtype`, and `valtype` includes `v128` — so a `(field v128)` is
// a well-formed struct field whose value occupies **two** adjacent stack slots. #243's lesson is
// exactly this shape one site over: *wherever a two-slot stack value is copied into a
// one-index-per-value store, the conversion is a third case and never a default*, and its
// consequence there was not a truncated value but a caller's stack left one slot deep, so the next
// call read its arguments out of the leftovers. `frame.numHi` is the sibling that already had the
// arm; this is that arm read rather than re-derived (grave #105).
//
// **No corpus vector covers a v128 field**, measured: `struct.wast` and the array files declare
// i8/i16/i32/i64/f32/f64/anyref/funcref/indexed fields and no v128. So this case is written from the
// grammar and pinned by a synthetic control, not by the oracle — stated here because a case with no
// vector behind it is exactly the kind that gets quietly dropped.
//
// **`packed` and `packBits` are deliberately *not* fields here**, departing from 0020's sketch of
// this struct. The pack width is already retained by decision 0021 in `binary.FieldType.Storage`
// (`Packed`, `Width`), which `typ` points at, so a per-field copy would be two places knowing one
// fact with nothing keeping them equal — the drift this project files graves for (#78/#105/#106).
// The truncation itself is unaffected: `aggr.ml`'s `alloc_field`/`write_field` **wrap at write**, so
// the stored value is already narrowed and only the *read* needs the width, which is in hand at
// every read site. Flagged in the PR as a departure rather than taken silently — and **ratified**:
// packedness is a type-level fact with `FieldType` as its sole authority, so an instance-level copy
// had no consumer, and the one-truth law favours the omission because a copy would be an enrolled
// witness needing drift protection for a fact that never varies per instance. The door is open, not
// walled: if a bench ever shows the type derivation costing on a hot path the fields may return, as
// enrolled witnesses with their drift check. (Ratification: Scott, PR #247; 0020's append.)
type gcField struct {
	num uint64
	hi  uint64
	r   ref
}

// fieldExt is a struct/array read's sign-extension request — `read_field`'s `exto` parameter
// (aggr.ml:33), which is `None`, `Some Pack.U`, or `Some Pack.S` and comes from the *opcode*
// (`struct.get` versus `struct.get_u` versus `struct.get_s`) rather than from the field.
type fieldExt int

const (
	extNone fieldExt = iota
	extU
	extS
)

// packMask and packSignExtend are `aggr.ml:15-18` transcribed: `gap pt = 32 - 8 * pack_size pt`,
// `wrap` masks to the width at *write*, `extend_u` is the identity on an already-masked value, and
// `extend_s` is a left-then-arithmetic-right shift pair.
//
// `width` is in **bits** (8 or 16, `binary.StorageType.Width`'s own unit) where OCaml's `pack_size`
// is in bytes, so the gap is `32 - width` here and `32 - 8*size` there — the same number by two
// spellings, and worth naming because a units slip would produce a mask that is wrong only for one
// of the two widths and therefore passes half the vectors.
func packMask(width byte) uint32 { return ^uint32(0) >> (32 - uint32(width)) }

func packSignExtend(width byte, stored uint64) int32 {
	gap := 32 - uint32(width)
	return int32(uint32(stored)<<gap) >> gap
}

// structType resolves an instruction's type immediate to a struct comptype — `struct_type`
// (eval.ml:674, `structtype_of_comptype (expand_deftype …)`).
//
// Both failures are #9's, and the second is the reason `Module.Types` keeps every comptype's slot:
// a `struct.get (type $f)` naming a *function* type is a module the decoder accepts, exactly as
// `declaredFuncType`'s own comment says about the mirror case. `what` names the instruction for
// `globalFor`'s reason — a reader chasing this error needs the line of their module, and
// `struct.get` and `struct.new` are different lines.
func (in *Instance) structType(what string, idx uint64) (*binary.CompType, error) {
	if idx >= uint64(len(in.mod.Types)) {
		return nil, fmt.Errorf("%w: %s names type %d of %d",
			ErrNotValidated, what, idx, len(in.mod.Types))
	}
	ct := &in.mod.Types[idx]
	if ct.Kind != binary.CompStruct {
		return nil, fmt.Errorf("%w: %s names type %d, which is a %s",
			ErrNotValidated, what, idx, ct.Kind)
	}
	return ct, nil
}

// arrayType resolves an instruction's type immediate to its array element type, checking on the way
// that the index names an array at all — `array_type` (eval.ml:114,
// `arraytype_of_comptype (expand_deftype …)`) followed by the `let FieldT (_mut, st) = …` every array
// arm opens with.
//
// **It returned the comptype too until this rung, and dropping it is the object-provenance change
// working rather than a tidy-up.** The reason for that second return was that every caller passed it
// straight into a `gcObj{typ: ct, …}` literal; 0027 decision 5 moved an object's identity to a
// `(mod, typeIdx)` pair, `alloc` builds it from the caller's own already-resolved index, and the
// comptype pointer stopped having a consumer at all twelve call sites in one PR. `unparam` is what
// said so out loud — a return value nobody reads is a missing consumer wearing a disguise, and the
// honest answer is to stop returning it rather than to keep a plausible-looking parameter alive.
//
// Note what is *not* dropped: the element type still comes back with the kind check, because an
// array's element type is not a lookup the way a struct's field is. A struct instruction carries a
// *field index* and `fieldStorage` resolves it; an array's element type is the comptype's whole
// content — `arraytype` is one bare `fieldtype`, not a `vec(fieldtype)` (decode.ml:257-258, and
// `CompType.Fields`'s own comment on the two productions). Splitting *that* would put the arity
// assumption in fourteen places instead of one.
//
// **The arity check is not decoration and it is not the retired agreement check.** `Fields` is a Go
// slice, so `Fields[0]` on a zero-length one panics, and a panic is categorically worse than a named
// error (`fieldStorage`'s own rule). The decoder builds exactly one entry for a `CompArray`
// (sections.go:570), so this is a guard against *this package* being wrong about the decoder rather
// than against a module — which is why it reports #9's debt and says which arity it found.
func (in *Instance) arrayType(what string, idx uint64) (binary.FieldType, error) {
	if idx >= uint64(len(in.mod.Types)) {
		return binary.FieldType{}, fmt.Errorf("%w: %s names type %d of %d",
			ErrNotValidated, what, idx, len(in.mod.Types))
	}
	ct := &in.mod.Types[idx]
	if ct.Kind != binary.CompArray {
		return binary.FieldType{}, fmt.Errorf("%w: %s names type %d, which is a %s",
			ErrNotValidated, what, idx, ct.Kind)
	}
	if len(ct.Fields) != 1 {
		return binary.FieldType{}, fmt.Errorf(
			"%w: %s names array type %d with %d element types, want exactly 1 "+
				"(arraytype is one bare fieldtype, decode.ml:257)",
			ErrNotValidated, what, idx, len(ct.Fields))
	}
	return ct.Fields[0], nil
}

// notAggregate reports a non-null reference that is not the aggregate instance an instruction
// required — a `FuncRef` reaching `struct.get`, an exnref reaching `array.len`.
//
// **This is the arm the reference interpreter does not need and this engine cannot do without.**
// `eval.ml`'s matches have two cases each — `StructRef s`/`ArrayRef a` and `NullRef` — and anything
// else is a pattern-match failure, i.e. a validation error. Here the same fact is a `ref` whose `Obj`
// is nil, and it must be *said*: dereferencing nothing would panic, and treating it as a null trap
// would answer the corpus' null vectors correctly while quietly reporting a trap for a module that
// has a type error. #9's verdict, named as such.
//
// It also carries the diagnostic weight of the payload split: `ref` grows one field per payload kind
// (see the `Exc` precedent), so "not a struct" has a *which-kind-instead* answer worth printing, and
// grave #36's rule applies — a message naming a value from the input gets printed for real inputs
// before it is trusted, because the oracle stops at the sentinel and everything past it is ours alone
// to keep honest.
//
// **`want` is a phrase and not a `CompKind`**, which is the one thing about this signature worth
// defending: the kind a caller wants is a static property of the *instruction*, so passing the
// spelling puts it at the call site where a reader is already looking, and there is no second
// authority to drift from. A `CompKind` parameter would additionally invite the check this package
// deliberately does not make — see below on why `obj.typ.Kind` goes unread.
//
// **What is deliberately *not* here: a check that the object's own kind matches.** A struct instance
// reaching `array.len` would answer `len(fields)` — wrong, and not a panic, since every index bound
// in this package is taken against the object's own slice. Adding the discrimination would
// reintroduce exactly the shape retired on #247: `obj.typ.Kind` can only disagree with the
// instruction's expectation on a path `ref.cast`/`br_on_cast` opens, and those are rung 5. So it is
// filed on the same tripwire (#248) rather than written as a green that asserts nothing.
func notAggregate(what, want string, r ref) error {
	kind := "a function reference"
	switch {
	case r.IsI31:
		// Rung 4's payload, and the case that would otherwise be *misreported* rather than
		// merely unreported: an i31 sets none of the pointer fields, so without this arm
		// `struct.get` on an `i31ref` names "a function reference" — a message asserting
		// something about the input that the input does not contain, which is grave #36's
		// class exactly. A payload kind added to `ref` owes this switch an arm for the same
		// reason it owes `refEqTreatment` an entry.
		kind = "an i31 reference"
	case r.Exc != nil:
		kind = "an exception reference"
	case r.Inst != nil:
		kind = "a function reference with a defining instance"
	}
	return fmt.Errorf("%w: %s on %s, not %s", ErrNotValidated, what, kind, want)
}

// storageSize is `Types.storage_size` (types.ml:75-77) — a storage type's width **in bytes**, which
// is what `array.new_data` and `array.init_data` stride a data segment by.
//
// **Bytes here, bits in `StorageType.Width`**, the same units seam `packMask` already names: the
// packed case is `pack_size` (1 or 2) against a `Width` of 8 or 16, so the conversion is `/8` and
// stating it is cheaper than a wrong stride that is right for one of the two widths.
//
// A **reference** element type has no size, which is `val_of_bits (RefT _) = raise Type`
// (value.ml:190) — validation rejects `array.new_data` on an array of references, so this is #9's
// debt and not a trap. `what` is threaded through for the same reason `structType` threads it: the
// reader needs the line of their module.
func storageSize(what string, st binary.StorageType) (uint64, error) {
	if st.Packed {
		return uint64(st.Width) / 8, nil
	}
	switch st.Val {
	case binary.I32, binary.F32:
		return 4, nil
	case binary.I64, binary.F64:
		return 8, nil
	case binary.V128:
		return 16, nil
	}
	return 0, fmt.Errorf("%w: %s on an array of %s, which has no bytes in a data segment "+
		"(value.ml:190 is `raise Type`)", ErrNotValidated, what, st.Val)
}

// loadStorage is `Data.load_val_storage` (data.ml:45) — one element read out of a data segment's
// bytes, little-endian, already narrowed to the element's storage.
//
// **The packed case is `Pack.U`, unconditionally**, and that is the reference's own choice rather
// than a simplification: `val_of_storage_bits (PackStorageT pt)` passes `Pack.U` (value.ml:206-210),
// so an `(array i8)` initialized from the byte `0xff` holds **255**, and a later `array.get_s`
// re-derives −1 from the stored 255. Reading it signed here would store −1 and make `array.get_u`
// answer 4294967295; `array_new_data.wast` asserts both directions off one segment, so the two are
// not interchangeable.
//
// `bs` is exactly `storageSize` bytes; the caller has already bounds-checked the whole extent, which
// is `data_oob` running before any read (eval.ml:753).
func loadStorage(bs []byte, st binary.StorageType) gcField {
	if st.Packed || st.Val != binary.V128 {
		return gcField{num: loadValue(bs, memop{width: uint64(len(bs))})}
	}
	return gcField{
		num: loadValue(bs[:8], memop{width: 8}),
		hi:  loadValue(bs[8:], memop{width: 8}),
	}
}

// fieldStorage resolves field `i` of the instruction's declared comptype, bounds-checking the
// object too when one is in hand.
//
// **`undefined field` is `eval.ml:696`'s `Crash.error`, not a trap** — a field index out of range
// is something validation ruled out, so it is reported as #9's debt like every other crash-class
// case in this package (`refEq`'s `failwith` cases, `branch`'s out-of-range depth). Both range
// checks stay: they are what stop an out-of-range index from indexing a Go slice, and a panic is
// categorically worse than a named error.
//
// **What was here and is not: a storage-kind agreement check between the instruction's declared
// type and the object's.** It read as a real cross-check and was vacuous — `ct` and `obj.typ` are
// the *same pointer* on 30 of 30 corpus field accesses and differ on 0, every separating path going
// through rung 5's `ref.cast`/`br_on_cast`. A comparison of a thing to itself is a green that
// asserts nothing, so it is retired and its control with it (#248 is the tripwire that reinstates
// both when rung 5 gives them a subject).
//
// The risk it named is real and unretired, which is why it is filed rather than dismissed: field
// shape decisions come from the instruction's type immediate (`struct.set` must know the value's
// array before it can pop the reference underneath it), so once subtyping is reachable a
// disagreement picks the wrong stack array — the grave #243 shape, a wrong array choice cascading
// into the next instruction's operands.
func fieldStorage(ct *binary.CompType, obj *gcObj, i uint64) (binary.FieldType, error) {
	if i >= uint64(len(ct.Fields)) {
		return binary.FieldType{}, fmt.Errorf("%w: undefined field %d of %d in type %s",
			ErrNotValidated, i, len(ct.Fields), ct.Kind)
	}
	ft := ct.Fields[i]
	if obj != nil {
		if i >= uint64(len(obj.fields)) {
			return binary.FieldType{}, fmt.Errorf("%w: undefined field %d of %d in the object",
				ErrNotValidated, i, len(obj.fields))
		}
	}
	return ft, nil
}

// popField pops one field's initializer off the stack, wrapping a packed field at write —
// `alloc_field` (aggr.ml:20-25) and `write_field` (:27-31), which are the same three cases and
// share this function for that reason.
//
// **The packed arm is `wrap`, and truncating here rather than at read is the whole packed-field
// design.** `alloc_field (PackStorageT sz) (Num (I32 i))` stores `wrap sz i`, so the narrowing
// happens once at write and every read merely extends — which is why `set_get_packed_g0_1
// (i32.const 257)` reads back **1** and not 257, while the i16 field in the same struct reads back
// 257. Extending at read without wrapping at write passes the first of those and fails the second.
//
// `alloc_field`'s `failwith "alloc_field"` case — a packed field handed something that is not an
// i32 — is not reachable as a distinct branch here: the value's array is chosen *from the declared
// field type*, so a packed field always reads the numeric stack. What the reference catches by
// pattern match, this engine does not catch at all — #9's absence, not a new hole, and not
// something the retired agreement check covered either: it could only ever have fired on a type
// disagreement no corpus path reaches (`fieldStorage`, and #248).
func popField(ft binary.FieldType, st *stack) (gcField, error) {
	if ft.Storage.Packed {
		if err := st.needNum(1); err != nil {
			return gcField{}, err
		}
		return gcField{num: uint64(uint32(st.popI32()) & packMask(ft.Storage.Width))}, nil
	}
	switch t := ft.Storage.Val; {
	case t.IsRef():
		if err := st.needRef(1); err != nil {
			return gcField{}, err
		}
		return gcField{r: st.popRef()}, nil
	case t == binary.V128:
		if err := st.needNum(2); err != nil {
			return gcField{}, err
		}
		hi, lo := st.popV128()
		return gcField{num: lo, hi: hi}, nil
	default:
		if err := st.needNum(1); err != nil {
			return gcField{}, err
		}
		return gcField{num: st.popNum()}, nil
	}
}

// defaultField is `default_value (unpacked_fieldtype ft)` (value.ml:141-158, types.ml:116-120): the
// zero for a numeric or packed field, a null reference for a *nullable* reference field, and an
// error for a non-nullable one.
//
// **`non-defaultable type` is a crash in the reference, so it is #9's here.** `default_ref (NoNull,
// _)` is `None` and `eval.ml:683` turns that into `Crash.error "non-defaultable type"` — a module
// declaring `(struct (field (ref $t)))` and calling `struct.new_default` on it is one validation
// rejects, so this reports the layering debt rather than inventing a trap. Note that `unpacked`
// means a packed field defaults through `NumT I32T`: zero either way, but for the stated reason
// rather than by coincidence.
func defaultField(ft binary.FieldType) (gcField, error) {
	if ft.Storage.Packed {
		return gcField{}, nil
	}
	t := ft.Storage.Val
	if t.IsRef() {
		if !t.Null() {
			return gcField{}, fmt.Errorf("%w: non-defaultable type %s in a struct.new_default field",
				ErrNotValidated, t)
		}
		return gcField{r: ref{Null: true}}, nil
	}
	return gcField{}, nil
}

// pushField pushes field `i`'s value, applying the read's extension request — `read_field`
// (aggr.ml:33-38).
//
// **The two `failwith` cases are real and both are #9's**: `read_field (ValField _) (Some _)` is
// `struct.get_s`/`get_u` on an *unpacked* field, and `read_field (PackField _) None` is a plain
// `struct.get` on a *packed* one. Both are `Crash.error "type mismatch reading field"`
// (eval.ml:699) — validation rejects them — so neither is a trap and neither may silently do
// something reasonable. Reporting them is what keeps `struct.get` on an i8 field from quietly
// answering the untruncated bits.
func pushField(ft binary.FieldType, f gcField, ext fieldExt, st *stack) error {
	if ft.Storage.Packed {
		switch ext {
		case extU:
			// `extend_u _pt i = Int32.of_int i` — the identity on a value `wrap` already
			// narrowed, which is why this is not a second mask. If it needed one, the write
			// side would be wrong.
			st.pushI32(int32(uint32(f.num)))
		case extS:
			st.pushI32(packSignExtend(ft.Storage.Width, f.num))
		default:
			return fmt.Errorf("%w: struct.get with no sign on packed field of width %d "+
				"(aggr.ml:38 is `failwith`; the read must be get_s or get_u)",
				ErrNotValidated, ft.Storage.Width)
		}
		return nil
	}
	if ext != extNone {
		return fmt.Errorf("%w: struct.get_%s on unpacked field of type %s "+
			"(aggr.ml:38 is `failwith`; a signed read wants a packed field)",
			ErrNotValidated, map[fieldExt]string{extU: "u", extS: "s"}[ext], ft.Storage.Val)
	}
	switch t := ft.Storage.Val; {
	case t.IsRef():
		st.pushRef(f.r)
	case t == binary.V128:
		st.pushV128(f.hi, f.num)
	default:
		st.pushNum(f.num)
	}
	return nil
}
