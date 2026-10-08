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

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Kind is a WIT value type's discriminant, for the types slice 2's guest drives. Modeled: the scalars,
// char, string, list, variant/result, own/borrow, and `tuple`'s size/alignment (for lowering an empty
// `list<tuple>` — the getters' result; tuple *element* lowering is not modeled). Types outside this set
// (record, flags, enum, option, non-empty tuple lowering) are refused by name until a guest drives one.
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
	KindOwn
	KindBorrow
	KindTuple
	// KindRecord is the base field-carrying form; `tuple` despecializes to it (definitions.py:1133),
	// so a tuple value never reaches a layout rule as itself. See [TupleType].
	KindRecord
	// KindFuture (gate:async increment 3) is a future<T> the host returns to a guest. Unlike the other
	// kinds it is NOT codec-lowered: a future handle is minted in the per-instance async handle table
	// (component layer), which the codec cannot reach, so the async-lower wrapper intercepts a KindFuture
	// result and writes the handle itself. It carries the value the future will deliver (future<u32>, first
	// slice) in `u`. The codec's Kind switches default-refuse it — it must never reach them.
	KindFuture
	// KindStream (gate:async increment 3, write side) is a stream<T> the host returns to a guest to WRITE
	// to (stdout). Like KindFuture it is wrapper-minted, not codec-lowered — a writable-stream-end handle
	// in the async handle table. It carries no payload (a stream is a channel, not a single value); the
	// guest supplies the elements via stream.write.
	KindStream
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
	case KindOwn:
		return "own"
	case KindBorrow:
		return "borrow"
	case KindTuple:
		// Unreachable through [TupleType], which despecializes. Named anyway: a hand-built
		// `Type{Kind: KindTuple}` must render as something truthful rather than as a number.
		return "tuple"
	case KindRecord:
		return "record"
	case KindFuture:
		return "future"
	case KindStream:
		return "stream"
	default:
		return fmt.Sprintf("kind(%d)", uint8(k))
	}
}

// Type is a WIT value type. Elem is the element type of a list; Cases are a variant's cases (a result
// is a two-case variant, "ok"/"err"); Fields are a record's fields. All are nil/empty for the other
// kinds.
type Type struct {
	Kind   Kind
	Elem   *Type
	Cases  []Case
	Fields []Field // record fields (KindRecord); a tuple's are labelled "0", "1", … — see [TupleType]
	RT     int     // resource-type id for own/borrow (PR A models a resource type as an opaque id)
}

// Field is one record field: its WIT label and its type.
//
// # Why the label is part of the type, and why a tuple has labels too
//
// The model's `FieldType` carries a `label` (definitions.py:128-130), and `store_record` reaches a
// field's value by that label — `v[f.label]` (def:1727). The label is therefore load-bearing for the
// layout's *input*, not decoration: it is how a record value's fields are matched to the declared order.
//
// A tuple's fields are labelled `"0"`, `"1"`, … because that is what `despecialize` produces
// (def:1133). This was `[]Type` with no labels while tuple was the only field-carrying kind, which was
// adequate and is not once records exist — two records with the same field *types* and different field
// *names* are different types, and `typeEqual` has to say so.
type Field struct {
	Name string
	Type Type
}

// Case is one variant case: its name and payload type (nil for a payload-less case, like `closed` or an
// empty `result` arm).
type Case struct {
	Name string
	Type *Type
}

// String renders a type **structurally**, as WIT spells it: `list<u32>`, `list<list<string>>`,
// `tuple<u32, string>`, `variant{ok(u32), error}`.
//
// # Why a type needs its own String when Kind already has one
//
// `Kind.String()` says `list` for every list. A message built from the kind therefore names the two
// sides of a type mismatch **identically** — "parameter is list, want list" — which reads as though the
// engine had refused a type for matching, and sends its reader looking for a bug in the comparison
// rather than at their own element type. That cost a review cycle one layer up, in `lowerFlatArgs`.
//
// So this exists for the same reason [TypeEqual] does, and the pair should be read together: comparing
// kinds is the defect, and **reporting** kinds is how the defect hides. There is one structural
// comparison and one structural rendering, and everything that refuses a type uses both.
//
// Having it as `String()` rather than a named helper is deliberate: it makes `%s` on a `Type` correct by
// default, so the next message to format a type gets the right rendering without its author knowing any
// of this. Safe to add now because nothing formatted a `Type` directly — every existing message reaches
// for `.Kind` explicitly, which is exactly the habit this replaces.
func (t Type) String() string { return t.describe(0) }

// describe is String's recursion. The depth bound is `canonTypeWalk`'s: a resolved type is acyclic by
// construction, so the bound is reachable only by a hand-built type — and a renderer that recursed
// forever would turn a diagnostic into a hang, which is a worse failure than an imprecise name.
func (t Type) describe(depth int) string {
	if depth > 8 {
		return "…"
	}
	switch t.Kind {
	case KindList:
		if t.Elem == nil {
			// A list with no element type is malformed rather than unmodeled. Rendered rather than
			// dereferenced, because the one place a reader meets this string is a diagnostic, and a
			// renderer that panics while explaining a defect has destroyed the explanation.
			return "list<?>"
		}
		return "list<" + t.Elem.describe(depth+1) + ">"
	case KindRecord:
		// **Labels included**, which is the whole reason this is not `tuple<…>`: `record{x: u32}` and
		// `record{y: u32}` have the same layout and are different types, so a rendering that dropped
		// the names would print a mismatch as a match — the exact defect this renderer exists for, one
		// level in. A despecialized tuple therefore reads `record{0: u8, 1: u32}`; see [TupleType].
		parts := make([]string, 0, len(t.Fields))
		for i := range t.Fields {
			parts = append(parts, t.Fields[i].Name+": "+t.Fields[i].Type.describe(depth+1))
		}
		return "record{" + strings.Join(parts, ", ") + "}"
	case KindTuple:
		// Unreachable through `TupleType`. A hand-built one renders as itself rather than being
		// mistaken for a record, so a diagnostic about it says what the reader actually has.
		return "tuple(not despecialized)"
	case KindVariant:
		parts := make([]string, 0, len(t.Cases))
		for _, c := range t.Cases {
			if c.Type == nil {
				parts = append(parts, c.Name)
				continue
			}
			parts = append(parts, c.Name+"("+c.Type.describe(depth+1)+")")
		}
		return "variant{" + strings.Join(parts, ", ") + "}"
	case KindOwn, KindBorrow:
		// The resource id is part of the type: `own<rt=3>` and `own<rt=4>` are different types, and
		// TypeEqual says so, so a rendering that dropped the id would print a mismatch as a match.
		return fmt.Sprintf("%s<rt=%d>", t.Kind, t.RT)
	default:
		return t.Kind.String()
	}
}

// TypeEqual reports whether two WIT types are **structurally** equal — element types, case names and
// payloads, tuple fields and resource ids, all the way down.
//
// # Why this is exported, and why comparing kinds is the bug it prevents
//
// `Kind` alone says `list` for both `list<u32>` and `list<string>`. A parameter check that compares kinds
// therefore accepts a `list<string>` where a `list<u32>` is declared, and the lowering then writes
// strings at the **value's** element stride into a buffer the guest reads as 4-byte integers: a plausible
// wrong value, not an error. That is ADR 0097's first change — compare whole types, not kinds — and it
// applies at **every** boundary a value crosses, not only at the public constructor, because
// `Instantiated.CallValues` is reachable without going through one.
//
// So this is the single structural comparison: the public constructors' element check and the
// argument lowering's parameter check both call it, rather than each growing a version.
func TypeEqual(a, b Type) bool { return typeEqual(a, b) }

// typeEqual reports structural equality. Type carries slices (Cases) and pointers (Elem), so it is not
// comparable with ==; a constructor's type check uses this.
func typeEqual(a, b Type) bool {
	if a.Kind != b.Kind || a.RT != b.RT {
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
	if len(a.Fields) != len(b.Fields) {
		return false
	}
	for i := range a.Fields {
		// **The label is compared, not only the type.** Two records with the same field types and
		// different field names are different types: `record{x: u32}` and `record{y: u32}` have an
		// identical byte layout and are not interchangeable, because a value's fields are matched to the
		// declaration *by name* (definitions.py:1727's `v[f.label]`). Comparing types alone would make
		// them equal and let a value built against one satisfy a signature declaring the other.
		//
		// Order matters too, and is compared by position rather than sorted: field order is the layout,
		// so `record{a: u8, b: u32}` and `record{b: u32, a: u8}` are different types with different
		// sizes.
		if a.Fields[i].Name != b.Fields[i].Name {
			return false
		}
		if !typeEqual(a.Fields[i].Type, b.Fields[i].Type) {
			return false
		}
	}
	return true
}

// RecordType is a `record` of the given fields. **It is the base form**: every field-carrying WIT type
// in this codec is one of these, and `tuple` is built from it.
//
// An empty field set is **not** refused here, because a constructor that cannot report an error would
// have to panic to do it, and a type is a description rather than an operation. It is refused where it
// becomes one: [Record] at construction, `StoreVia`/`LoadVia` before any layout is computed, and the
// bridge in `internal/component`, which is the layer a component's declared type actually passes
// through. See [sizeRecord] for why the model leaves it undefined.
func RecordType(fields ...Field) Type { return Type{Kind: KindRecord, Fields: fields} }

// TupleType is a `tuple<…>` of the given types, **despecialized to a record** with fields labelled
// "0", "1", … — exactly what `despecialize` does (definitions.py:1133).
//
// # Why the despecialization happens here rather than at each use
//
// The model calls `despecialize(t)` at the top of `alignment`, `elem_size`, `store`, `load` and
// `flatten`, so a `TupleType` never reaches a layout rule as itself. Doing it once in the constructor
// gives the same result with one arm instead of five, and makes "tuple derives from record" literal:
// there is no tuple arm to keep in step with the record one. The two readings are verified identical by
// the differential — `tuple-u8-u32` and `record-as-tuple-u8-u32` in `gen/cases.json` produce
// byte-identical store images and flat lowerings, which is what licenses the single arm.
//
// The consequence to know: a tuple **renders and compares as a record**. `TupleType(u8, u32).String()`
// is `record{0: u8, 1: u32}`, and `TypeEqual` finds it equal to the hand-built record of the same
// labels. That is the ABI's own view — WIT distinguishes the two, the Canonical ABI does not — and the
// distinction survives where it matters, in `ValKind`'s `VTuple`/`VRecord` and in `ComponentKind`.
func TupleType(ts ...Type) Type {
	fields := make([]Field, len(ts))
	for i := range ts {
		fields[i] = Field{Name: strconv.Itoa(i), Type: ts[i]}
	}
	return Type{Kind: KindRecord, Fields: fields}
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

// Future builds a future<u32> result value the host returns to a guest, carrying the value the future will
// deliver. It is intercepted and minted by the async-lower wrapper (component layer), never codec-lowered.
func Future(value uint32) Value { return Value{Type: Type{Kind: KindFuture}, u: uint64(value)} }

// FutureValue returns the value a KindFuture carries (the wrapper reads it to mint the readable end).
func (v Value) FutureValue() uint32 { return uint32(v.u) }

// U32 returns the value a KindU32 carries, and whether it is one.
//
// The payload field is unexported, so a value crossing out of this package needs an accessor per kind it
// is read at — #864's value-carrying export call reads `u32`. **Kind-checked rather than a bare read**:
// `FutureValue` above is unchecked because the async-lower wrapper is its only caller and has just minted
// the value, whereas this one is called on a value an outside caller supplied. A bare reader would return
// a plausible number for a `u64` or an `s32`, and the caller's own kind check would be the only thing
// standing between a mistyped argument and the guest.
func (v Value) U32() (uint32, bool) {
	if v.Type.Kind != KindU32 {
		return 0, false
	}
	return uint32(v.u), true
}

// Str returns the value a KindString carries, and whether it is one.
//
// Kind-checked for [Value.U32]'s reason: the payload field is unexported, so an outside reader needs an
// accessor per kind, and a bare read would hand back the empty string for every non-string rather than
// saying it was the wrong kind. The two are distinguishable and must be — `""` is a legitimate string.
//
// The package-level `Str(s string) Value` constructor and this method do not collide: Go keeps function
// and method names in separate namespaces, and naming both after the WIT type is what makes the
// construct/read pair legible.
func (v Value) Str() (string, bool) {
	if v.Type.Kind != KindString {
		return "", false
	}
	return v.s, true
}

// List returns the elements a KindList carries, and whether it is one.
//
// # The copy is deliberate
//
// `Value` is handed around by value and is otherwise immutable from outside the package — every payload
// field is unexported and every constructor copies. Returning the backing slice directly would hand a
// caller a writable window into a value somebody else also holds: a list lifted from a guest and then
// mutated through an accessor would change under the host that lifted it, which is the aliasing bug
// `List(…)`'s own `append([]Value(nil), elems...)` already declines to create on the way in. The way out
// gets the same treatment, so the asymmetry cannot be the hole.
//
// The cost is one allocation per read of a list, paid at the boundary and not per element of anything.
//
// Kind-checked for [Value.Str]'s reason, and the boolean matters more here than it looks: an **empty
// list** is a legitimate value — `list<u32>` with no elements is what the WASI getters return — so
// `(nil, false)` for a non-list and `(empty, true)` for an empty list have to be distinguishable, and a
// bare `len() == 0` read cannot tell them apart.
func (v Value) List() ([]Value, bool) {
	if v.Type.Kind != KindList {
		return nil, false
	}
	return append([]Value(nil), v.list...), true
}

// Stream builds a stream<T> result the host returns to a guest to write to. Carries no payload; the
// async-lower wrapper mints a writable-stream-end handle (component layer), never codec-lowered.
func Stream() Value { return Value{Type: Type{Kind: KindStream}} }

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
//
// The comparison was always structural; **the message was not**, and printed `.Kind` on both sides until
// the shared renderer existed. So a `list<string>` handed to a `list<list<u32>>`'s element slot was
// refused with "element 0 is list, want list" — the right verdict reported in a way that reads like a
// bug in the check. See [Type.String].
// Record constructs a record value against its type, from a **map of field name to value**.
//
// # Why a map, when the payload is stored positionally
//
// ADR 0097's record ruling, and it goes further than checking for a misordered field: a map makes
// misordering **unrepresentable**, because the caller supplies no order at all. The order comes from the
// descriptor, which is the only order in the system — this constructor walks `t.Fields` and looks each
// name up, so Go's map-iteration nondeterminism never reaches the ABI layout.
//
// The payload is then positional (`list`, in descriptor order), which is what [StoreRecord] walks. So
// the label does its work exactly once, here, at construction.
//
// # The keys must match the descriptor EXACTLY
//
// Both directions: a missing field and an unexpected one are each refused, and named. A missing field
// cannot be defaulted — there is no zero value for a WIT type that means "absent", and storing one
// would hand the guest a field the caller never set. An extra key is almost always a typo for a real
// field, so accepting it silently would lower the record with the intended field *missing*.
//
// The field *types* are compared with [TypeEqual] rather than by kind, for ADR 0097's first change: a
// `record{xs: list<u32>}` must not accept a `list<string>` for `xs`.
func Record(t Type, fields map[string]Value) (Value, error) {
	if t.Kind != KindRecord {
		return Value{}, fmt.Errorf("canon: Record on non-record type %s", t)
	}
	if len(t.Fields) == 0 {
		// The model has no size for an empty record (definitions.py:1256), so there is no layout to
		// build a value against. One of the error-returning layers that keeps `sizeRecord`'s panic
		// unreachable.
		return Value{}, fmt.Errorf("canon: a record type with no fields has no layout in the " +
			"Canonical ABI, so no value of it can be built")
	}
	vals := make([]Value, len(t.Fields))
	for i := range t.Fields {
		f := t.Fields[i]
		v, ok := fields[f.Name]
		if !ok {
			return Value{}, fmt.Errorf("canon: record field %q is missing; a record's fields must all be "+
				"supplied, because no WIT value means \"absent\"", f.Name)
		}
		if !typeEqual(v.Type, f.Type) {
			return Value{}, fmt.Errorf("canon: record field %q is %s, want %s", f.Name, v.Type, f.Type)
		}
		vals[i] = v
	}
	if len(fields) != len(t.Fields) {
		// Reported by name, and only after the loop above, so a caller with one typo'd key is told
		// which key is unexpected rather than which field is missing — both are true, and the extra key
		// is the one they wrote.
		declared := make(map[string]bool, len(t.Fields))
		for i := range t.Fields {
			declared[t.Fields[i].Name] = true
		}
		extra := make([]string, 0, len(fields)-len(t.Fields))
		for k := range fields {
			if !declared[k] {
				extra = append(extra, k)
			}
		}
		sort.Strings(extra) // deterministic, since map order is not
		return Value{}, fmt.Errorf("canon: record has no field(s) %v; the type is %s",
			extra, t)
	}
	return Value{Type: t, list: vals}, nil
}

// Record returns a record's field values **in descriptor order**, and whether this value is a record.
//
// # Ordered, where Field is by name, and both are needed
//
// [Value.Field] answers "what is field `x`", which is what a reader who knows the type wants. This
// answers "what are the fields, in layout order", which is what a *lowering* wants: the flat form and
// the memory form are both the fields in declared order, and looking each one up by name to rebuild
// that order would reintroduce the name-to-order step the descriptor exists to own.
//
// Copied, for [Value.List]'s reason: a `Value` is passed around by value and is otherwise immutable
// from outside the package, so handing back the backing array would be a writable window into a value
// somebody else also holds.
//
// The length is guaranteed to equal `len(v.Type.Fields)` for any value a constructor built, which is
// what lets a caller index the two together. A value whose payload disagrees with its type reads as
// **not a record** rather than returning a short slice — a caller pairing the two would otherwise index
// past the end of one of them.
func (v Value) Record() ([]Value, bool) {
	if v.Type.Kind != KindRecord || len(v.list) != len(v.Type.Fields) {
		return nil, false
	}
	return append([]Value(nil), v.list...), true
}

// Field returns the value of a record's named field, and whether this value is a record with one.
//
// The two-result form distinguishes "not a record" and "no such field" from a field whose value is a
// legitimate zero — the reason every accessor here carries a boolean.
func (v Value) Field(name string) (Value, bool) {
	if v.Type.Kind != KindRecord || len(v.list) != len(v.Type.Fields) {
		return Value{}, false
	}
	for i := range v.Type.Fields {
		if v.Type.Fields[i].Name == name {
			return v.list[i], true
		}
	}
	return Value{}, false
}

func List(elem Type, elems ...Value) (Value, error) {
	for i, e := range elems {
		if !typeEqual(e.Type, elem) {
			return Value{}, fmt.Errorf("canon: list element %d is %s, want %s", i, e.Type, elem)
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

// OwnType and BorrowType build handle types over a resource-type id. Own constructs an owned-handle
// value from a resource representation (an i32); the borrow value path is PR B's (its lend accounting
// lives at the call scope), so there is no Borrow value constructor here.
func OwnType(rt int) Type    { return Type{Kind: KindOwn, RT: rt} }
func BorrowType(rt int) Type { return Type{Kind: KindBorrow, RT: rt} }

func Own(rt int, rep uint32) Value { return Value{Type: OwnType(rt), u: uint64(rep)} }
