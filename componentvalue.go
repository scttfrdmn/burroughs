// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package burroughs

import (
	"fmt"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
)

// The public component value type, and the conversion that keeps it separate from the internal one.
//
// # Why a distinct type and not a re-export — this is not a choice made here
//
// Decision 0029 decision 2 is Scott's ruling: hoisting an internal representation into the public surface
// freezes it as API while the engine is still widening it. `interp.Value` was kept internal behind a
// conversion for exactly that reason, and the record is specific — it widened **four times in four
// slices**, and the fourth *retyped a field*, which for a published type is a compatibility break rather
// than a minor version.
//
// `canon.Value` is in the same position: it is the Canonical ABI's working representation, it grows as
// kinds are modelled, and gate:async has already added future/stream handles to it. So the same shape
// applies — a distinct public type, converted at the boundary, with **no silent default in either
// direction**. Go has no exhaustiveness check, so an unmapped kind must be an error naming what it was
// rather than something that converts to *a* value.
//
// The cost is the one `convert.go` already states for the core-module `Value`: crossed once per argument
// and once per result of a `Call`, never per instruction, and nothing here does arithmetic.

// ComponentKind is which WIT value type a [ComponentValue] carries.
//
// **Enumerated beyond what crosses today, deliberately.** Only `KindComponentU32` can cross the boundary
// in this release (ADR 0085's guest-driven scope, and the first-merge scope Scott stamped), and the others
// exist so a refusal can **name the kind it refused** rather than saying "unsupported". A kind becomes
// crossable by gaining a constructor and a conversion arm, which is additive.
type ComponentKind uint8

// The component value kinds. KindComponentNone is the zero value and names no type, so a ComponentValue
// nobody set refuses at the boundary rather than crossing as a zero u32 — the role `KindNone` plays for
// the core-module [Value].
const (
	KindComponentNone ComponentKind = iota

	KindComponentBool
	KindComponentU8
	KindComponentU16
	KindComponentU32
	KindComponentU64
	KindComponentS8
	KindComponentS16
	KindComponentS32
	KindComponentS64
	KindComponentF32
	KindComponentF64
	KindComponentChar
	KindComponentString
	KindComponentList
	KindComponentRecord
	KindComponentVariant
	KindComponentTuple
	KindComponentFlags
	KindComponentEnum
	KindComponentOption
	KindComponentResult
	KindComponentOwn
	KindComponentBorrow
)

// String names the kind as WIT spells it, so a refusal reads in the embedder's vocabulary rather than the
// engine's. Unknown values render their number rather than a guess.
func (k ComponentKind) String() string {
	switch k {
	case KindComponentNone:
		return "none"
	case KindComponentBool:
		return "bool"
	case KindComponentU8:
		return "u8"
	case KindComponentU16:
		return "u16"
	case KindComponentU32:
		return "u32"
	case KindComponentU64:
		return "u64"
	case KindComponentS8:
		return "s8"
	case KindComponentS16:
		return "s16"
	case KindComponentS32:
		return "s32"
	case KindComponentS64:
		return "s64"
	case KindComponentF32:
		return "f32"
	case KindComponentF64:
		return "f64"
	case KindComponentChar:
		return "char"
	case KindComponentString:
		return "string"
	case KindComponentList:
		return "list"
	case KindComponentRecord:
		return "record"
	case KindComponentVariant:
		return "variant"
	case KindComponentTuple:
		return "tuple"
	case KindComponentFlags:
		return "flags"
	case KindComponentEnum:
		return "enum"
	case KindComponentOption:
		return "option"
	case KindComponentResult:
		return "result"
	case KindComponentOwn:
		return "own"
	case KindComponentBorrow:
		return "borrow"
	}
	return fmt.Sprintf("ComponentKind(%d)", uint8(k))
}

// ComponentValue is a WIT value crossing the component boundary — the argument and result type of
// [Component.Call].
//
// It is **distinct from [Value]**, which is a core-module value (i32/i64/f32/f64/v128/refs). The two are
// not interchangeable and must not be: a core module's `i32` and a component's `u32` are different types
// in different type systems, and one type holding both would silently accept a core value where a WIT
// value belongs. That is the mis-typing ADR 0085 requires be refused.
//
// The fields are unexported, so a value is always built through a constructor and always carries the kind
// it claims — ADR 0085's "constructors carry their WIT type", which is what lets a mis-typed read refuse
// instead of reinterpreting bits.
//
// **The zero value carries no type.** `var v ComponentValue` has `KindComponentNone` and refuses at the
// boundary rather than crossing as `u32(0)`.
type ComponentValue struct {
	kind ComponentKind
	bits uint64
}

// Kind reports which WIT type this value carries.
func (v ComponentValue) Kind() ComponentKind { return v.kind }

// ComponentU32 constructs a `u32`.
//
// **This is the only constructor in this release**, and the narrowness is the stamped first-merge scope,
// not an oversight: `u32` is what the committed guests drive, and every other kind refuses by name at the
// boundary. Each additional kind is a new constructor plus a conversion arm — purely additive, so no
// embedder depending on this one is affected when the others arrive.
func ComponentU32(v uint32) ComponentValue {
	return ComponentValue{kind: KindComponentU32, bits: uint64(v)}
}

// U32 reads a `u32`, and reports whether this value is one.
//
// The boolean is the refusal: a value of another kind returns `(0, false)` rather than reinterpreting its
// bits. That is the read-side half of "constructors carry their WIT type" — a type tag nobody checks on
// the way out would make the construction-time check decorative.
func (v ComponentValue) U32() (uint32, bool) {
	if v.kind != KindComponentU32 {
		return 0, false
	}
	return uint32(v.bits), true
}

// String renders the value for diagnostics, naming its kind so an unexpected one is legible.
func (v ComponentValue) String() string {
	if v.kind == KindComponentU32 {
		return fmt.Sprintf("u32(%d)", uint32(v.bits))
	}
	return v.kind.String()
}

// toCanon converts a public component value to the engine's, refusing any kind this release does not
// carry — **by name**, so an embedder learns which kind was wrong rather than that something was.
//
// No silent default: an unmapped kind is an error, which is the discipline `convert.go` states for the
// core-module boundary and the reason it has a derived-domain guard.
func (v ComponentValue) toCanon() (canon.Value, error) {
	switch v.kind {
	case KindComponentU32:
		return canon.U32(uint32(v.bits)), nil
	case KindComponentNone:
		return canon.Value{}, fmt.Errorf("%w: a ComponentValue with no kind cannot cross the boundary — "+
			"the zero value names no WIT type, so it is a value nobody constructed rather than a u32(0)",
			ErrUnsupported)
	default:
		return canon.Value{}, fmt.Errorf("%w: a %s value cannot cross the component boundary yet; this "+
			"release carries u32 only, and the other kinds are added one at a time without breaking "+
			"changes", ErrUnsupported, v.kind)
	}
}

// fromCanon converts an engine component value to the public one, refusing any kind this release does not
// carry. The direction matters as much as the other: a result the engine can produce but this type cannot
// spell must be an error rather than a zero value the embedder reads as data.
func fromCanon(v canon.Value) (ComponentValue, error) {
	switch v.Type.Kind {
	case canon.KindU32:
		u, ok := v.U32()
		if !ok {
			// Unreachable while `canon.Value`'s kind and payload agree, and checked rather than assumed:
			// the accessor is the authority on that agreement, so trusting the kind alone here would be
			// the mis-read this type exists to prevent.
			return ComponentValue{}, fmt.Errorf("%w: a canon value tagged u32 did not read as one",
				ErrUnsupported)
		}
		return ComponentU32(u), nil
	default:
		return ComponentValue{}, fmt.Errorf("%w: an export returned a value this release cannot carry "+
			"across the boundary; it carries u32 only", ErrUnsupported)
	}
}
