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
	// typ is the declared comptype this object was allocated against — `type_of_struct`
	// (aggr.ml:54), the fact `type_of_ref'` reports for a `StructRef` and therefore the input to
	// every `ref.test`/`ref.cast` at rung 5.
	//
	// **Also load-bearing now, not merely retained for later.** Field *shape* decisions (which
	// stack array a value comes from, whether storage is packed) are taken from the
	// instruction's own type immediate, because `struct.set` must know the value's array
	// *before* it can pop anything — the target reference sits underneath the value, and with
	// two stack arrays there is no way to look at the object first. Validation guarantees the
	// immediate's type and the object's agree (a struct subtype matches its supertype's fields
	// exactly in storage kind), so `fieldStorage` checks that agreement and reports #9's verdict
	// when it fails, rather than letting a disagreement pick an array silently.
	typ *binary.CompType

	// fields is one entry per declared field, in declaration order — `field list`, and for an
	// array exactly the elements (rung 3's consumer).
	fields []gcField
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
// every read site. Flagged in the PR as a departure rather than taken silently.
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

// fieldStorage resolves field `i` of the instruction's declared comptype, checking it against the
// object's own type.
//
// **`undefined field` is `eval.ml:696`'s `Crash.error`, not a trap** — a field index out of range
// is something validation ruled out, so it is reported as #9's debt like every other crash-class
// case in this package (`refEq`'s `failwith` cases, `branch`'s out-of-range depth).
//
// The agreement check is what makes `gcObj.typ` load-bearing rather than decorative, and it catches
// a real class rather than a hypothetical one: with no validator, nothing otherwise stops a module
// from presenting an object of one struct type to an instruction naming another whose field `i` has
// a different *storage kind*, at which point the shape decision taken from the immediate pops from
// the wrong stack array. That is the grave #243 shape (a wrong array choice cascading into the next
// instruction's operands), so it gets a named error instead of a silent divergence.
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
		if obj.typ != nil && i < uint64(len(obj.typ.Fields)) {
			if got := obj.typ.Fields[i].Storage; got != ft.Storage {
				return binary.FieldType{}, fmt.Errorf(
					"%w: field %d is %s in the instruction's type and %s in the object's",
					ErrNotValidated, i, storageName(ft.Storage), storageName(got))
			}
		}
	}
	return ft, nil
}

// storageName renders a storage type for fieldStorage's disagreement message. Its own function
// because `binary.StorageType` has no String method and adding one to the decoder for an
// interpreter error message would put this package's diagnostic vocabulary in another package.
func storageName(s binary.StorageType) string {
	if s.Packed {
		return fmt.Sprintf("i%d", s.Width)
	}
	return s.Val.String()
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
// pattern match, this engine catches at `fieldStorage`'s agreement check or not at all, which is
// #9's absence and not a new hole.
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
