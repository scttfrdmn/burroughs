// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

// Package canon is the Canonical ABI value codec (contract §6, ADR 0084 slice 2 / PR A): lift and lower
// between a component's linear memory and a value, for the guest-driven type scope on #694.
//
// This slice is the value codec proper — `size`/`alignment`/`store`/`load` and the flat lowering — read
// at the pinned reference model (`component-model @ 2bed77e`, `CanonicalABI.md` and `definitions.py`).
// Its correctness is held by a differential test against fixtures the reference model itself emits
// (`gen/`), so a wrong offset, width, or encoding is caught rather than reasoned about. A type outside
// the modeled scope has no case here and is refused by name — the loader's refuse-by-name discipline,
// at the value surface.
package canon

import "fmt"

// Kind is a WIT value type's discriminant, for the types slice 2's guest drives. Types outside this set
// (record, tuple, flags, enum, option, and — until their own increments — f32/f64, variant, result,
// own/borrow) are not modeled here and are refused by name.
type Kind uint8

const (
	KindBool Kind = iota
	KindU8
	KindU16
	KindU32
	KindU64
	KindS8
	KindS16
	KindS32
	KindS64
	KindF32
	KindF64
	KindChar
	KindString
	KindList
	KindVariant
)

func (k Kind) String() string {
	switch k {
	case KindBool:
		return "bool"
	case KindU8:
		return "u8"
	case KindU16:
		return "u16"
	case KindU32:
		return "u32"
	case KindU64:
		return "u64"
	case KindS8:
		return "s8"
	case KindS16:
		return "s16"
	case KindS32:
		return "s32"
	case KindS64:
		return "s64"
	case KindF32:
		return "f32"
	case KindF64:
		return "f64"
	case KindChar:
		return "char"
	case KindString:
		return "string"
	case KindList:
		return "list"
	case KindVariant:
		return "variant"
	default:
		return fmt.Sprintf("kind(%d)", uint8(k))
	}
}

// Type is a WIT value type. Elem is the element type of a list; Cases are a variant's cases (a result
// is a two-case variant, "ok"/"err"). Both are nil/empty for the other kinds.
type Type struct {
	Kind  Kind
	Elem  *Type
	Cases []Case
}

// Case is one variant case: its name and payload type (nil for a payload-less case, like `closed` or an
// empty `result` arm).
type Case struct {
	Name string
	Type *Type
}

// typeEqual reports structural equality. Type carries slices (Cases) and pointers (Elem), so it is not
// comparable with ==; a constructor's type check uses this.
func typeEqual(a, b Type) bool {
	if a.Kind != b.Kind {
		return false
	}
	if (a.Elem == nil) != (b.Elem == nil) {
		return false
	}
	if a.Elem != nil && !typeEqual(*a.Elem, *b.Elem) {
		return false
	}
	if len(a.Cases) != len(b.Cases) {
		return false
	}
	for i := range a.Cases {
		if a.Cases[i].Name != b.Cases[i].Name {
			return false
		}
		if (a.Cases[i].Type == nil) != (b.Cases[i].Type == nil) {
			return false
		}
		if a.Cases[i].Type != nil && !typeEqual(*a.Cases[i].Type, *b.Cases[i].Type) {
			return false
		}
	}
	return true
}

// Value is one component value. Exactly one payload field is meaningful per Type.Kind: u holds the raw
// bits of bool/ints/char (a signed integer is held as its two's-complement bits, so a fixed-width store
// is a low-byte copy); s holds a string; list holds a list's elements.
type Value struct {
	Type    Type
	u       uint64 // bool/ints/char bits; a variant's case index
	s       string
	list    []Value
	payload *Value // a variant case's payload, nil for a payload-less case
}

// Bool, the unsigned and signed integers, Char, Str, and List construct a value against the type it
// claims. A constructor is where a mis-typed value is refused (ADR 0085); a signed integer keeps its
// two's-complement bits, and List requires every element to match the declared element type.
func Bool(b bool) Value {
	var u uint64
	if b {
		u = 1
	}
	return Value{Type: Type{Kind: KindBool}, u: u}
}

func U8(v uint8) Value   { return Value{Type: Type{Kind: KindU8}, u: uint64(v)} }
func U16(v uint16) Value { return Value{Type: Type{Kind: KindU16}, u: uint64(v)} }
func U32(v uint32) Value { return Value{Type: Type{Kind: KindU32}, u: uint64(v)} }
func U64(v uint64) Value { return Value{Type: Type{Kind: KindU64}, u: v} }
func S8(v int8) Value    { return Value{Type: Type{Kind: KindS8}, u: uint64(int64(v))} }
func S16(v int16) Value  { return Value{Type: Type{Kind: KindS16}, u: uint64(int64(v))} }
func S32(v int32) Value  { return Value{Type: Type{Kind: KindS32}, u: uint64(int64(v))} }
func S64(v int64) Value  { return Value{Type: Type{Kind: KindS64}, u: uint64(v)} }

// Char constructs a char from a Unicode code point, refusing a surrogate or an out-of-range value at
// construction (the reference model's char_to_i32 invariant).
func Char(r rune) (Value, error) {
	if r < 0 || (r > 0xD7FF && r < 0xE000) || r > 0x10FFFF {
		return Value{}, fmt.Errorf("canon: char %#x is not a Unicode scalar value", r)
	}
	return Value{Type: Type{Kind: KindChar}, u: uint64(r)}, nil
}

func Str(s string) Value { return Value{Type: Type{Kind: KindString}, s: s} }

// List constructs a list against a declared element type, refusing an element whose type does not match
// at construction rather than at the boundary (ADR 0085).
func List(elem Type, elems ...Value) (Value, error) {
	for i, e := range elems {
		if !typeEqual(e.Type, elem) {
			return Value{}, fmt.Errorf("canon: list element %d is %s, want %s", i, e.Type.Kind, elem.Kind)
		}
	}
	return Value{Type: Type{Kind: KindList, Elem: &elem}, list: append([]Value(nil), elems...)}, nil
}

// VariantType builds a variant type from its cases. ResultType builds the two-case `result` variant.
func VariantType(cases ...Case) Type { return Type{Kind: KindVariant, Cases: cases} }

func ResultType(ok, err *Type) Type {
	// The reference model despecializes `result` to a variant with cases "ok" and "error".
	return Type{Kind: KindVariant, Cases: []Case{{Name: "ok", Type: ok}, {Name: "error", Type: err}}}
}

// Variant constructs a variant value against its type, refusing at construction a case that does not
// exist, a payload whose type does not match the case, or a payload/no-payload mismatch (ADR 0085).
func Variant(vt Type, caseName string, payload *Value) (Value, error) {
	if vt.Kind != KindVariant {
		return Value{}, fmt.Errorf("canon: Variant on non-variant type %s", vt.Kind)
	}
	for i, c := range vt.Cases {
		if c.Name != caseName {
			continue
		}
		switch {
		case c.Type == nil && payload != nil:
			return Value{}, fmt.Errorf("canon: variant case %q takes no payload", caseName)
		case c.Type != nil && payload == nil:
			return Value{}, fmt.Errorf("canon: variant case %q needs a %s payload", caseName, c.Type.Kind)
		case c.Type != nil && !typeEqual(payload.Type, *c.Type):
			return Value{}, fmt.Errorf("canon: variant case %q payload is %s, want %s", caseName, payload.Type.Kind, c.Type.Kind)
		}
		return Value{Type: vt, u: uint64(i), payload: payload}, nil
	}
	return Value{}, fmt.Errorf("canon: variant has no case %q", caseName)
}
