// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package burroughs

import (
	"fmt"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// The boundary conversion, in both directions, for types and for values.
//
// # Why a conversion and not a re-export
//
// Decision 0029 decision 2, which is Scott's ruling: hoisting `interp.Value` out of `internal/`
// would freeze the interpreter's representation as public API while the GC work is still widening
// it. `binary.ValType` took one widening for GC (the nullability bit and the type index), and
// `interp.Value` has taken three more since — 0024's v128 high word, 0027's host-reference
// discriminator, and 0039 *replacing* that discriminator with an enumerated payload kind plus an i31
// payload. A type that widened four times in four slices is not a type to publish, and the fourth is
// the sharpest specimen: it did not add to the surface, it **retyped a field**, which for a published
// type is a compatibility break rather than a minor version.
//
// The cost is real and bounded: this is crossed once per argument and once per result of a Call,
// never per instruction. Nothing here does arithmetic — it is a bit-pattern copy and a tag map,
// the same discipline `internal/spec`'s own harness conversion states for itself.
//
// # No silent default, in either direction
//
// Go has no exhaustiveness check, so a Kind added without an arm here would otherwise convert to
// *something*. Every unmapped input is an error naming what it was, and the guard in
// `convert_test.go` derives its domain from the space — every byte `binary.AbstractRefType`
// accepts, in both nullabilities, plus the numeric named types, plus the indexed form — rather
// than from a list written beside the tables it is checking.

// numericValTypes pairs each numeric/vector Kind with `internal/binary`'s own named ValType.
//
// Valued by the authority's variables rather than by wire bytes, so the five encodings are
// written down in exactly one place in the module — that package's own declarations, which the
// decoder and the encoder already read.
var numericValTypes = map[Kind]binary.ValType{
	KindI32:  binary.I32,
	KindI64:  binary.I64,
	KindF32:  binary.F32,
	KindF64:  binary.F64,
	KindV128: binary.V128,
}

// heapKindBytes pairs each abstract reference Kind with its heaptype kind byte.
//
// Valued by the exported `Heap*` constants for numericValTypes' reason, and those are themselves
// derived from each form's `sleb(7)` wire value by the arithmetic the decoder uses — so a
// transcription error would have to be made in the spec's own file to reach here.
var heapKindBytes = map[Kind]byte{
	KindFuncRef:     binary.HeapFunc,
	KindExternRef:   binary.HeapExtern,
	KindAnyRef:      binary.HeapAny,
	KindEqRef:       binary.HeapEq,
	KindI31Ref:      binary.HeapI31,
	KindStructRef:   binary.HeapStruct,
	KindArrayRef:    binary.HeapArray,
	KindNoneRef:     binary.HeapNone,
	KindNoFuncRef:   binary.HeapNoFunc,
	KindNoExternRef: binary.HeapNoExtern,
	KindExnRef:      binary.HeapExn,
	KindNoExnRef:    binary.HeapNoExn,
}

// byteKinds is the reverse direction, built from the two tables above rather than written out —
// two independently maintained maps over one bijection is the drift the repo's own two-registry
// rule names, and a reversal computed once cannot disagree with what it reverses.
var byteKinds = func() map[byte]Kind {
	out := make(map[byte]Kind, len(numericValTypes)+len(heapKindBytes))
	for k, vt := range numericValTypes {
		b, ok := vt.Kind()
		if !ok {
			// Unreachable: every named numeric ValType has a wire byte. Panicking at init
			// rather than skipping the row, because a silently short reverse table would make
			// a legitimate module's type convert to "unknown" — a wrong answer where this is a
			// loud one, and the package cannot function either way.
			panic(fmt.Sprintf("burroughs: %v has no kind byte", vt))
		}
		out[b] = k
	}
	for k, b := range heapKindBytes {
		out[b] = k
	}
	return out
}()

// payloadKinds pairs each public RefPayload with the engine's, and is the forward half of the one
// mapping decision 0039 puts at this seam.
//
// **A table and not a cast, though both enums have the same members in the same order.** Equal
// numbering is a coincidence that holds today and is exactly what a public vocabulary must not
// depend on: the engine's enum can gain a member for an internal reason (a proposal's new reference
// constructor arrives there first), and `RefPayload(iv.RefKind)` would then silently rename every
// member above it. The table makes that a missing row instead — and a missing row is a named error
// at the seam, which is what Scott's condition on the 0039 stamp asks for: *an enum whose whole
// purpose is to grow must fail loudly the first time it does*.
var payloadKinds = map[RefPayload]interp.RefPayload{
	PayloadNone:   interp.PayloadNone,
	PayloadHost:   interp.PayloadHost,
	PayloadI31:    interp.PayloadI31,
	PayloadStruct: interp.PayloadStruct,
	PayloadArray:  interp.PayloadArray,
	PayloadFunc:   interp.PayloadFunc,
	PayloadExn:    interp.PayloadExn,
}

// kindPayloads is the reverse direction, computed from the forward table rather than written out —
// byteKinds' reason, applied to the second bijection this file carries. Two independently maintained
// maps over one mapping is the drift the repo's two-registry rule names.
//
// It panics at init on a forward table that is not injective, because a collision would make one
// engine payload kind convert to whichever public member the map iteration reached last — a wrong
// answer produced nondeterministically, where this is a loud one and the package cannot function
// either way.
var kindPayloads = func() map[interp.RefPayload]RefPayload {
	out := make(map[interp.RefPayload]RefPayload, len(payloadKinds))
	for pub, in := range payloadKinds {
		if prev, dup := out[in]; dup {
			panic(fmt.Sprintf("burroughs: %v and %v both map to engine payload %v", prev, pub, in))
		}
		out[in] = pub
	}
	return out
}()

// typeToInternal converts a public Type to the engine's.
func typeToInternal(t Type) (binary.ValType, error) {
	if vt, ok := numericValTypes[t.kind]; ok {
		return vt, nil
	}
	if t.kind == KindTypedRef {
		return binary.RefType(t.idx, t.null), nil
	}
	if b, ok := heapKindBytes[t.kind]; ok {
		vt, ok := binary.AbstractRefType(b, t.null)
		if !ok {
			return binary.NoValType, fmt.Errorf("burroughs: %v maps to heaptype %#02x, which the "+
				"decoder does not recognize", t.kind, b)
		}
		return vt, nil
	}
	return binary.NoValType, fmt.Errorf("burroughs: no engine type for %v", t.kind)
}

// typeFromInternal converts an engine type to the public one.
func typeFromInternal(vt binary.ValType) (Type, error) {
	if vt.IsIndexed() {
		return TypedRefType(vt.Index(), vt.Null()), nil
	}
	b, ok := vt.Kind()
	if !ok {
		// The zero ValType: a field nothing wrote. It reaches here as an error rather than as a
		// type because the alternative is inventing one — and the accessor only tells the two
		// cases apart as of grave #300, whose fix this is the first consumer of.
		return Type{}, fmt.Errorf("burroughs: %v is not a value type", vt)
	}
	k, ok := byteKinds[b]
	if !ok {
		return Type{}, fmt.Errorf("burroughs: no public kind for engine type %v (kind %#02x)", vt, b)
	}
	if !k.IsRef() {
		return Type{kind: k}, nil
	}
	return Type{kind: k, null: vt.Null()}, nil
}

// valueToInternal converts a public Value to the engine's.
func valueToInternal(v Value) (interp.Value, error) {
	vt, err := typeToInternal(v.typ)
	if err != nil {
		return interp.Value{}, err
	}
	rk, ok := payloadKinds[v.refKind]
	if !ok {
		// A public payload kind with no engine counterpart. Unreachable while payloadKinds covers the
		// vocabulary — which `TestPayloadConversionCoversTheWholeVocabulary` is what checks — and an
		// error rather than a zero because PayloadNone is a *meaning* here ("no payload"), so
		// defaulting to it would convert an unmapped struct reference into a reference naming no
		// constructor and lose the failure.
		return interp.Value{}, fmt.Errorf("burroughs: no engine payload kind for %v", v.refKind)
	}
	return interp.Value{
		Type:    vt,
		Bits:    v.bits,
		Hi:      v.hi,
		Null:    v.null,
		RefKind: rk,
		RefID:   v.ref,
		I31:     v.i31,
	}, nil
}

// valueFromInternal converts an engine Value to the public one.
func valueFromInternal(iv interp.Value) (Value, error) {
	t, err := typeFromInternal(iv.Type)
	if err != nil {
		return Value{}, err
	}
	rk, ok := kindPayloads[iv.RefKind]
	if !ok {
		// The direction that will actually fire when the engine's enum grows first, which is the
		// likely order: a proposal's new reference constructor lands in `interp` and reaches here
		// before this package has a member for it. Named, with the engine's own spelling in the
		// message, rather than converted to PayloadNone — a result silently reporting "no
		// constructor" is the fabrication this whole seam exists to prevent (see Value.RefKind).
		return Value{}, fmt.Errorf("burroughs: no public payload kind for engine payload %v", iv.RefKind)
	}
	return Value{
		typ:     t,
		bits:    iv.Bits,
		hi:      iv.Hi,
		null:    iv.Null,
		refKind: rk,
		ref:     iv.RefID,
		i31:     iv.I31,
	}, nil
}
