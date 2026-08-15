// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

// Package burroughs is the engine's public surface: load a module, instantiate it, call an
// export, read the result.
//
// # Why this package exists at all
//
// It is the first path in this repo a user can actually cross, and until it landed there was
// none. Measured on `main` at 676aa29: one package sat outside `internal/`, `cmd/burroughs`,
// which imported only the decoder — so `inspect` was a section dump and the interpreter was
// unreachable from outside the module. Every one of the 59682 passing spec assertions was
// obtained through a single consumer, `internal/spec`, and the boundary a host would use had no
// vectors and no invocation path. That is this project's own coverage law at project scale: *an
// instrument's domain is an assertion it cannot check about itself*, and the missing dimension
// was **how the thing is called**. Decision 0029 records the ruling; #299 carries the work.
//
// So the vectors run through *here* now (`publicpath_test.go`), not only through the harness.
//
// # Three outcomes, because two would be a mixture
//
// Loading a module can succeed, be refused, or be **declined**, and a decline is not a failure.
// The validator (#9) is landing in slices: it types the spine today and has no rules yet for
// several instruction families. A module using one of those has not been found invalid — it has
// not been fully *asked*. Reporting that as invalid would refuse working modules for the length
// of a campaign; reporting it as valid would claim a check that did not happen. So it is its own
// outcome, readable with errors.Is against ErrDeclined and reported by Instance.Decline, and the
// module runs unless Config.Strict says otherwise.
//
// This is the same distinction the spec board makes between `failed` and `gated`, at the API
// instead of in a column. Decision 0029 states why running a partly-unvalidated module is
// defensible *here specifically*, and what would change the answer.
//
// # Values convert; they do not alias
//
// Value and Type below are this package's own, converted at the boundary rather than hoisted out
// of `internal/interp`. Publishing the interpreter's representation would freeze it as
// compatibility surface while the GC work is still widening it — `binary.ValType` has taken one
// widening for GC and `interp.Value` two more since (0024's v128 high word, 0027's host-reference
// discriminator). The conversion is crossed once per argument and once per result, not per
// instruction. Decision 0029 decision 2 has the ruling and the cost argument.
package burroughs

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Kind is a wasm value type's coarse classification — the part of a Type that is a closed
// vocabulary and can therefore be a constant.
//
// **A distinct enum rather than the wire byte**, which is the whole of the convert-not-alias
// decision expressed in one type. The wire bytes are the binary format's fact and the engine
// reads them from the spec's own arithmetic; a public constant equal to `0x7F` would make every
// consumer's `switch` a transcription of the encoding, and would have to keep meaning what it
// means through every future format revision. These are dense, start at one, and mean nothing
// outside this package.
//
// Nullability and the type index are **not** here, because they are not a closed vocabulary:
// they live on Type, which is the full type. `KindTypedRef` is the one Kind that does not
// determine a type by itself — `(ref $3)` and `(ref null $7)` share it and differ in Type.
type Kind uint8

// The value kinds. KindNone is the zero value and names no type, so a Value nobody set converts
// to an error rather than to an i32 — the same role `binary.NoValType` plays internally.
const (
	KindNone Kind = iota

	KindI32
	KindI64
	KindF32
	KindF64
	KindV128

	// The twelve abstract reference forms. Two are Wasm 2.0's and ungated; the other ten are
	// GC's and reachable only with that gate on (contract §9).
	KindFuncRef
	KindExternRef
	KindAnyRef
	KindEqRef
	KindI31Ref
	KindStructRef
	KindArrayRef
	KindNoneRef
	KindNoFuncRef
	KindNoExternRef
	KindExnRef
	KindNoExnRef

	// KindTypedRef is the parameterized form, `(ref $t)` / `(ref null $t)`. Consult
	// Type.TypeIndex for which type; the Kind alone does not say.
	KindTypedRef
)

// kindNames spells each Kind for String, and is also the domain `kindByte`'s own control checks
// itself against: one map with every Kind in it, so a Kind added without a conversion arm is a
// missing row rather than an unnoticed absence.
var kindNames = map[Kind]string{
	KindI32:         "i32",
	KindI64:         "i64",
	KindF32:         "f32",
	KindF64:         "f64",
	KindV128:        "v128",
	KindFuncRef:     "func",
	KindExternRef:   "extern",
	KindAnyRef:      "any",
	KindEqRef:       "eq",
	KindI31Ref:      "i31",
	KindStructRef:   "struct",
	KindArrayRef:    "array",
	KindNoneRef:     "none",
	KindNoFuncRef:   "nofunc",
	KindNoExternRef: "noextern",
	KindExnRef:      "exn",
	KindNoExnRef:    "noexn",
	KindTypedRef:    "typed",
}

// IsRef reports whether values of this kind are references.
//
// Derived from the kind's position in the enum rather than from a second list, so a reference
// form added between KindFuncRef and KindTypedRef is a reference without anyone remembering to
// say so. The ordering is therefore load-bearing and TestKindOrderingIsTheRefPartition pins it.
func (k Kind) IsRef() bool { return k >= KindFuncRef && k <= KindTypedRef }

func (k Kind) String() string {
	if n, ok := kindNames[k]; ok {
		return n
	}
	if k == KindNone {
		return "none-kind"
	}
	return fmt.Sprintf("Kind(%d)", uint8(k))
}

// Type is a wasm value type: a Kind, plus the nullability and type index a reference form needs.
//
// **Unexported fields, deliberately.** The representation is this package's own business and can
// widen — a future proposal adding a dimension to a reference type is a field here, not a
// breaking change for a consumer. What is public is the vocabulary (Kind), the accessors, and the
// three constructors. Comparable with `==`, which mirrors `binary.ValType`'s own load-bearing
// property: consumers compare a returned type against a named one.
//
// The three constructors match the internal type's own partition — named numeric values, an
// abstract-reference constructor, an indexed-reference constructor — rather than inventing a
// different one, because a boundary whose shape disagrees with the authority it converts from
// needs a mapping table for the disagreement too.
type Type struct {
	kind Kind
	null bool
	idx  uint32
}

// NumberType returns the Type for a numeric or vector Kind, reporting false for any other.
func NumberType(k Kind) (Type, bool) {
	if k == KindNone || k.IsRef() {
		return Type{}, false
	}
	return Type{kind: k}, true
}

// AbstractRefType returns the Type for one of the twelve abstract reference Kinds with the given
// nullability, reporting false for a numeric Kind or for KindTypedRef (which needs an index).
func AbstractRefType(k Kind, null bool) (Type, bool) {
	if !k.IsRef() || k == KindTypedRef {
		return Type{}, false
	}
	return Type{kind: k, null: null}, true
}

// TypedRefType returns the Type for `(ref $t)` / `(ref null $t)`, naming type index idx.
func TypedRefType(idx uint32, null bool) Type {
	return Type{kind: KindTypedRef, null: null, idx: idx}
}

// Kind returns this type's classification. KindNone for the zero Type.
func (t Type) Kind() Kind { return t.kind }

// Nullable reports a reference type's nullability. Always false for a numeric type, where the
// question has no meaning.
func (t Type) Nullable() bool { return t.null }

// TypeIndex returns the type index a KindTypedRef names, reporting false for every other Kind.
func (t Type) TypeIndex() (uint32, bool) {
	if t.kind != KindTypedRef {
		return 0, false
	}
	return t.idx, true
}

// IsRef reports whether this is a reference type.
func (t Type) IsRef() bool { return t.kind.IsRef() }

// String spells the type the way the spec's text format does.
func (t Type) String() string {
	if t.kind == KindNone {
		return "none-type"
	}
	if !t.IsRef() {
		return t.kind.String()
	}
	inner := t.kind.String()
	if t.kind == KindTypedRef {
		inner = strconv.FormatUint(uint64(t.idx), 10)
	}
	if t.null {
		return "(ref null " + inner + ")"
	}
	return "(ref " + inner + ")"
}

// Value is one wasm value crossing the host boundary, carrying its type.
//
// Tagged, for the reason `interp.Value` gives for being tagged where the operand stack is not:
// the boundary is where static knowledge stops. A host handing arguments to Call has its types
// checked against the function's signature, and a host reading results has no other way to know
// what it got.
//
// Fields are unexported and read through methods, which is the difference between this type and
// the internal one it converts from. An accessor read against the wrong Kind returns the zero
// value of what it names rather than reinterpreting bits — Type is how a caller knows which
// accessor to use, and Call's results always carry it.
type Value struct {
	typ  Type
	bits uint64
	hi   uint64
	null bool
	host bool
	ref  uint32
}

// I32 constructs an i32 value.
func I32(v int32) Value { return Value{typ: Type{kind: KindI32}, bits: uint64(uint32(v))} }

// I64 constructs an i64 value.
func I64(v int64) Value { return Value{typ: Type{kind: KindI64}, bits: uint64(v)} }

// F32 constructs an f32 value. NaN payloads survive: the float is stored by bit pattern, never
// widened to f64 and back, which is the conversion that quietly canonicalizes a signalling NaN.
func F32(v float32) Value {
	return Value{typ: Type{kind: KindF32}, bits: uint64(math.Float32bits(v))}
}

// F64 constructs an f64 value, by bit pattern for F32's reason.
func F64(v float64) Value { return Value{typ: Type{kind: KindF64}, bits: math.Float64bits(v)} }

// V128 constructs a v128 value from its low and high 64-bit halves — two words because that is
// what a v128 is everywhere a slot is a thing (decision 0024).
func V128(lo, hi uint64) Value {
	return Value{typ: Type{kind: KindV128}, bits: lo, hi: hi}
}

// NullRef constructs a null reference of the given reference type, reporting false if t is not a
// reference type **or is not nullable**. This is the only reference *argument* shape the spec corpus
// needs beyond ExternRef below.
//
// The nullability check is not defensive tidiness: `(ref func)` is precisely the type whose values
// are never null, so a null value carrying it is a type error the boundary can refuse at
// construction instead of passing into the engine. It also makes the value's own spelling total —
// `null:func` names a nullable type, so a null of a non-nullable type could be printed and not read
// back, and an unrepresentable value is one a round trip cannot pin.
func NullRef(t Type) (Value, bool) {
	if !t.IsRef() || !t.null {
		return Value{}, false
	}
	return Value{typ: t, null: true}, true
}

// ExternRef constructs a non-null externref carrying an opaque host identity — the corpus's
// `(ref.extern N)`. Zero is a legitimate identity, so a caller must never read the identity as
// "unset": IsNull is the question that means that.
func ExternRef(id uint32) Value {
	return Value{typ: Type{kind: KindExternRef, null: true}, host: true, ref: id}
}

// Type returns this value's type.
func (v Value) Type() Type { return v.typ }

// Int32 reads an i32. Zero for any other Kind.
func (v Value) Int32() int32 {
	if v.typ.kind != KindI32 {
		return 0
	}
	return int32(uint32(v.bits))
}

// Int64 reads an i64. Zero for any other Kind.
func (v Value) Int64() int64 {
	if v.typ.kind != KindI64 {
		return 0
	}
	return int64(v.bits)
}

// Float32 reads an f32 by bit pattern, preserving NaN payloads. Zero for any other Kind.
func (v Value) Float32() float32 {
	if v.typ.kind != KindF32 {
		return 0
	}
	return math.Float32frombits(uint32(v.bits))
}

// Float64 reads an f64 by bit pattern, preserving NaN payloads. Zero for any other Kind.
func (v Value) Float64() float64 {
	if v.typ.kind != KindF64 {
		return 0
	}
	return math.Float64frombits(v.bits)
}

// V128 reads a v128's low and high halves. Both zero for any other Kind.
func (v Value) V128() (lo, hi uint64) {
	if v.typ.kind != KindV128 {
		return 0, 0
	}
	return v.bits, v.hi
}

// IsNull reports whether this is a null reference. False for a numeric value, where the question
// has no meaning.
func (v Value) IsNull() bool { return v.typ.IsRef() && v.null }

// ExternID returns the host identity a non-null externref carries, reporting false when this
// value has none — a numeric value, a null reference, a funcref, or an externref wrapping a GC
// payload rather than a host reference.
//
// **The second result is not a nil check**, and conflating the two is a live defect in this
// engine's history: `extern.convert_any` produces non-null externrefs with no identity at all,
// and handing those out as identity 0 would name a host reference the value does not have (see
// `interp.Value.IsHost`, decision 0027).
func (v Value) ExternID() (uint32, bool) {
	if !v.host {
		return 0, false
	}
	return v.ref, true
}

// String spells the value the way this package's own CLI argument syntax does — `i32:42`,
// `f64:nan`, `extern:3`, `null:func` — so a printed result is a re-readable argument.
//
// One syntax with two directions, deliberately: a `run` invocation that prints `i32:7` and cannot
// accept `i32:7` back would be two spellings of one thing, which is the two-registry shape this
// repo polices elsewhere.
func (v Value) String() string {
	switch v.typ.kind {
	case KindNone:
		return "none"
	case KindI32:
		return fmt.Sprintf("i32:%d", v.Int32())
	case KindI64:
		return fmt.Sprintf("i64:%d", v.Int64())
	case KindF32:
		return "f32:" + formatFloat(v.bits, 32)
	case KindF64:
		return "f64:" + formatFloat(v.bits, 64)
	case KindV128:
		return fmt.Sprintf("v128:%#016x:%#016x", v.bits, v.hi)
	default:
		// Every reference kind, handled below rather than here: a reference's spelling depends on
		// its nullness and its host identity, not on the kind alone, so one arm per kind would be
		// thirteen copies of the same three-way decision. A real fallthrough, not a shrug.
	}
	if v.null {
		// The *kind's* name, not the type's: `null:func`, which is what ParseValue reads. A typed
		// reference has no such spelling and prints its full type instead — it is the one Kind this
		// syntax cannot round-trip, asserted as exactly one by
		// TestValueRoundTripsThroughItsOwnSpelling rather than left as an omission.
		if v.typ.kind == KindTypedRef {
			return "null:" + v.typ.String()
		}
		return "null:" + v.typ.kind.String()
	}
	if id, ok := v.ExternID(); ok {
		return fmt.Sprintf("extern:%d", id)
	}
	return "ref:" + v.typ.String()
}

// formatFloat spells a float the way the spec's text format does, including the NaN payload —
// which is the half a `%v` drops. `nan:0x200000` and `nan` are different values to the corpus, and a
// printer that renders both as `NaN` makes a result unreadable exactly where floats are interesting.
//
// **Takes bits, not a float64, and that signature is the fix for a defect this comment used to
// describe while the code did the opposite.** The first version accepted a `float64` and callers
// widened an f32 into it. Widening a signalling NaN to double sets the quiet bit in hardware, so an
// f32 holding `nan:0x200000` printed as `nan:0x600000` — the payload altered by the act of
// formatting it, under a comment promising preservation. Caught by *printing* a NaN through the CLI
// rather than by reading the function: `burroughs run add.wasm fnan`. A float64 parameter cannot
// carry an f32's payload faithfully, so the type is the guard, and
// TestFloatSpellingPreservesNaNPayloads is the control.
func formatFloat(bits uint64, size int) string {
	var f float64
	var payload, quiet uint64
	if size == 32 {
		f = float64(math.Float32frombits(uint32(bits)))
		payload, quiet = bits&0x007fffff, 0x00400000
	} else {
		f = math.Float64frombits(bits)
		payload, quiet = bits&0x000fffffffffffff, 0x0008000000000000
	}
	sign := ""
	if size == 32 && bits&0x80000000 != 0 || size == 64 && bits&0x8000000000000000 != 0 {
		sign = "-"
	}
	switch {
	case math.IsNaN(f) && payload == quiet:
		return sign + "nan"
	case math.IsNaN(f):
		return fmt.Sprintf("%snan:%#x", sign, payload)
	case math.IsInf(f, 0):
		// The spec's own spelling, and one ParseValue reads back — Go's `%g` would write `+Inf`.
		return sign + "inf"
	}
	return strconv.FormatFloat(f, 'g', -1, size)
}

// ParseValue reads the spelling Value.String writes: `i32:42`, `i64:-1`, `f32:nan:0x200000`,
// `f64:inf`, `v128:0x0:0x0`, `extern:3`, `null:func`.
//
// **The inverse of String, and in the same file for that reason.** A CLI that prints a result it
// cannot accept as an argument would be two spellings of one thing — the two-registry shape this
// repo polices elsewhere — so the round trip is a property, pinned by
// TestValueRoundTripsThroughItsOwnSpelling over a domain derived from the Kind vocabulary rather
// than from a handful of literals.
//
// Integers accept both the signed and the unsigned spelling of one bit pattern, because the spec
// corpus writes i32 results both ways (`-1` and `4294967295` are one value) and a parser that took
// only one would reject half the vectors it is meant to read. Hex is accepted with an `0x` prefix,
// for the same reason.
func ParseValue(s string) (Value, error) {
	kind, rest, ok := strings.Cut(s, ":")
	if !ok {
		return Value{}, fmt.Errorf("burroughs: %q has no type prefix — values are spelled "+
			"`i32:42`, `f64:nan`, `extern:3`, `null:func`", s)
	}
	switch kind {
	case "i32":
		n, err := parseInt(rest, 32)
		if err != nil {
			return Value{}, err
		}
		return Value{typ: Type{kind: KindI32}, bits: n}, nil
	case "i64":
		n, err := parseInt(rest, 64)
		if err != nil {
			return Value{}, err
		}
		return Value{typ: Type{kind: KindI64}, bits: n}, nil
	case "f32":
		b, err := parseFloatBits(rest, 32)
		if err != nil {
			return Value{}, err
		}
		return Value{typ: Type{kind: KindF32}, bits: b}, nil
	case "f64":
		b, err := parseFloatBits(rest, 64)
		if err != nil {
			return Value{}, err
		}
		return Value{typ: Type{kind: KindF64}, bits: b}, nil
	case "v128":
		lo, hi, found := strings.Cut(rest, ":")
		if !found {
			return Value{}, fmt.Errorf("burroughs: v128 wants two 64-bit halves, "+
				"`v128:<lo>:<hi>`, got %q", rest)
		}
		l, err := parseInt(lo, 64)
		if err != nil {
			return Value{}, err
		}
		h, err := parseInt(hi, 64)
		if err != nil {
			return Value{}, err
		}
		return V128(l, h), nil
	case "extern":
		n, err := parseInt(rest, 32)
		if err != nil {
			return Value{}, err
		}
		return ExternRef(uint32(n)), nil
	case "null":
		// Ranged over the name table rather than switched on, so the spellings a null reference
		// accepts are exactly the ones String emits, from one map — the same reason byteKinds is
		// computed instead of written.
		for k, name := range kindNames {
			if name != rest || !k.IsRef() || k == KindTypedRef {
				continue
			}
			t, _ := AbstractRefType(k, true)
			v, _ := NullRef(t)
			return v, nil
		}
		return Value{}, fmt.Errorf("burroughs: %q is not an abstract reference type", rest)
	}
	return Value{}, fmt.Errorf("burroughs: unknown value type %q in %q", kind, s)
}

// parseInt reads an integer literal into `bits` bits, accepting the signed and the unsigned
// spelling of one bit pattern and `0x` hex in either.
func parseInt(s string, bits int) (uint64, error) {
	base, digits := 10, s
	neg := strings.HasPrefix(digits, "-")
	digits = strings.TrimPrefix(digits, "-")
	if lower := strings.ToLower(digits); strings.HasPrefix(lower, "0x") {
		base, digits = 16, lower[2:]
	}
	if neg {
		n, err := strconv.ParseInt("-"+digits, base, bits)
		if err != nil {
			return 0, fmt.Errorf("burroughs: %q is not a %d-bit integer: %w", s, bits, err)
		}
		if bits == 32 {
			return uint64(uint32(int32(n))), nil
		}
		return uint64(n), nil
	}
	n, err := strconv.ParseUint(digits, base, bits)
	if err != nil {
		return 0, fmt.Errorf("burroughs: %q is not a %d-bit integer: %w", s, bits, err)
	}
	return n, nil
}

// parseFloatBits reads a float literal, including the NaN payload forms the spec's text format
// uses, and returns its bit pattern.
//
// **Payloads are parsed rather than dropped**, which is not a nicety: `nan` and `nan:0x200000` are
// different values to the conformance suite, and a parser mapping both to a canonical quiet NaN
// would make every arithmetic-NaN vector unrepresentable at this boundary while looking like it
// worked. `strconv.ParseFloat` cannot spell them, so the payload forms are read here and everything
// else is delegated — and it returns *bits* for the reason formatFloat takes them: a float64
// intermediate quiets an f32's signalling payload on the way through.
func parseFloatBits(s string, bits int) (uint64, error) {
	body, neg := s, false
	if strings.HasPrefix(body, "-") {
		body, neg = body[1:], true
	} else {
		body = strings.TrimPrefix(body, "+")
	}
	if strings.HasPrefix(body, "nan") {
		payload := uint64(0)
		if arg, ok := strings.CutPrefix(body, "nan:"); ok {
			p, err := parseInt(arg, 64)
			if err != nil {
				return 0, err
			}
			payload = p
		} else if body != "nan" {
			return 0, fmt.Errorf("burroughs: %q is not an f%d: a NaN is `nan` or `nan:0xPAYLOAD`", s, bits)
		}
		if bits == 32 {
			b := uint32(0x7f800000)
			if payload == 0 {
				payload = 0x00400000
			}
			b |= uint32(payload) & 0x007fffff
			if neg {
				b |= 0x80000000
			}
			return uint64(b), nil
		}
		b := uint64(0x7ff0000000000000)
		if payload == 0 {
			payload = 0x0008000000000000
		}
		b |= payload & 0x000fffffffffffff
		if neg {
			b |= 0x8000000000000000
		}
		return b, nil
	}
	f, err := strconv.ParseFloat(s, bits)
	if err != nil {
		return 0, fmt.Errorf("burroughs: %q is not an f%d: %w", s, bits, err)
	}
	if bits == 32 {
		return uint64(math.Float32bits(float32(f))), nil
	}
	return math.Float64bits(f), nil
}
