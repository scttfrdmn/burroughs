// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package burroughs

import (
	"fmt"
	"unicode/utf8"

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

// ComponentType is a WIT value type: what a [ComponentValue] is a value *of*.
//
// # Why a type has to be spellable at all
//
// A `u32` needs no descriptor — `ComponentU32(7)` is complete. A **list** does: an empty `list<u32>` and
// an empty `list<string>` are different values, and neither carries an element to infer the type from. A
// **record** needs its field names. So a compound value cannot be constructed without a way to say what
// it is, and that is this type.
//
// # Why it is public and opaque rather than derived from the signature
//
// ADR 0097's ruling, on the ground that ADR 0085 already settled it: *"constructors carry their WIT
// type, so a mis-typed value refuses **at construction** rather than at the boundary."* Deriving a
// compound's type from the called export's signature would move that refusal to the boundary, which is
// the thing that sentence refuses by name. The signature check at [Component.Call] stays as a second line
// of defence, not as the only one.
//
// The fields are unexported and the value converts at the boundary — [ADR
// 0029](https://github.com/scttfrdmn/burroughs)'s treatment, the same one [ComponentValue] gets, and the
// reason a public type over an internal representation is safe here: the representation can widen as
// kinds arrive without any of it being API.
//
// **The zero value names no type**, like [ComponentValue]'s, and is refused wherever it would be used
// rather than defaulting to anything. That is why the kind is held separately from the codec type: the
// codec's own zero `Kind` is `bool`, so a zero struct would otherwise claim to be one.
type ComponentType struct {
	kind ComponentKind // KindComponentNone for the zero value
	t    canon.Type
}

// ComponentTypeU32 is the `u32` type.
//
// Total, with no error, because `u32` has no validity condition — unlike a list (whose element type can
// be the zero type) or a record (whose field set can be empty or carry duplicate names). The split is
// deliberate: a constructor that cannot fail should not make its caller pretend it can.
func ComponentTypeU32() ComponentType {
	return ComponentType{kind: KindComponentU32, t: canon.Type{Kind: canon.KindU32}}
}

// ComponentTypeString is the `string` type.
func ComponentTypeString() ComponentType {
	return ComponentType{kind: KindComponentString, t: canon.Type{Kind: canon.KindString}}
}

// ComponentTypeList is the `list<elem>` type.
//
// # Why this one returns an error where ComponentTypeU32 does not
//
// A list has a validity condition its scalars do not: **the element type can be the zero type.** `var e
// ComponentType` names nothing, so `ComponentTypeList(e)` would be a list of nothing — and if that were
// allowed to construct, it would reach the boundary as a `canon.Type{Kind: KindList, Elem: &zero}` whose
// element reads as `bool`, because the codec's zero `Kind` **is** `bool`. A list of nothing would lower
// as a list of booleans. That is the silent-wrong-value outcome the whole construction-time discipline
// exists to prevent, so it is refused here, at construction, per ADR 0085.
//
// Nesting needs no special case: `ComponentTypeList` of a list is a list of lists, and the element check
// in [ComponentList] compares whole types, so `list<list<u32>>` will not accept a `list<string>` element.
func ComponentTypeList(elem ComponentType) (ComponentType, error) {
	if elem.kind == KindComponentNone {
		return ComponentType{}, fmt.Errorf("a list's element type must be a type, and the zero " +
			"ComponentType names none; build the element with ComponentTypeU32, ComponentTypeString or " +
			"ComponentTypeList first. A list of the zero type would lower as a list of bools, because " +
			"the codec's zero type tag is bool")
	}
	e := elem.t
	return ComponentType{kind: KindComponentList, t: canon.Type{Kind: canon.KindList, Elem: &e}}, nil
}

// Kind reports which WIT type this is, or KindComponentNone for the zero value.
func (t ComponentType) Kind() ComponentKind { return t.kind }

// String renders the type as WIT spells it, so a refusal naming a type reads in the embedder's
// vocabulary. The zero value renders as "none" rather than as a guess.
//
// **Structural, not the kind alone** — `list<u32>` and `list<string>` must not both render as "list".
// This was `t.kind.String()` while `u32` and `string` were the only types, where the two agree; it
// stops agreeing the moment a list exists, and a refusal that named both sides of a mismatch
// identically would read as though the engine had rejected a type for matching. The rendering is
// `canon.Type.String`'s, which is the same one the engine's internal refusals use, so the embedder and
// the engine describe a type the same way.
//
// The zero value is special-cased rather than delegated, for the reason [ComponentType] records: the
// codec's own zero `Kind` is `bool`, so handing a zero `canon.Type` to the renderer would print "bool"
// for a type nobody built.
func (t ComponentType) String() string {
	if t.kind == KindComponentNone {
		return "none"
	}
	return t.t.String()
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
// **It carries its full type, not just its kind** — ADR 0097's first change. Comparing kinds is not
// enough once compounds arrive: `list<list<u32>>` and `list<string>` are both lists, so a kind-only check
// on a list element would accept the wrong one. The type is held rather than the kind so that
// construction can compare **structurally**, and that check is internal — it needs no exported accessor,
// which is why this change adds no name to the stamped surface.
type ComponentValue struct {
	typ  ComponentType
	bits uint64
	s    string           // KindComponentString's payload
	list []ComponentValue // KindComponentList's payload
}

// Kind reports which WIT type this value carries.
func (v ComponentValue) Kind() ComponentKind { return v.typ.kind }

// There is deliberately **no** accessor for the full type, exported or not. The `typ` field is what ADR
// 0097's first change requires — construction compares types structurally rather than by kind — and that
// comparison arrives with the list and record constructors that need it. An accessor added now would have
// no caller, which `deadcode` says plainly and which is the honest signal: *decline speculative API with
// a consumer trigger.* ADR 0097 records the trigger for a public `Type()` — code that must branch on a
// returned value's type rather than knowing it from the signature it called.

// ComponentU32 constructs a `u32`.
//
// **This is the only constructor in this release**, and the narrowness is the stamped first-merge scope,
// not an oversight: `u32` is what the committed guests drive, and every other kind refuses by name at the
// boundary. Each additional kind is a new constructor plus a conversion arm — purely additive, so no
// embedder depending on this one is affected when the others arrive.
func ComponentU32(v uint32) ComponentValue {
	return ComponentValue{typ: ComponentTypeU32(), bits: uint64(v)}
}

// ComponentString constructs a `string`, refusing one that is not valid UTF-8.
//
// # Why this returns an error where ComponentU32 does not
//
// The Canonical ABI's `string` is UTF-8. A Go `string` is an arbitrary byte sequence, so an invalid one
// is expressible — and lowered raw it would put invalid UTF-8 in guest memory, where a Rust guest's
// `String::from_utf8` panics. The condition is real, so the refusal has to live somewhere, and ADR 0085
// puts it **at construction**.
//
// The in-tree precedent is the codec's own `Char` constructor, which already refuses a surrogate or an
// out-of-range code point at construction for exactly this reason. So an error-returning value
// constructor is the established shape for a kind with a validity condition, and `ComponentU32` stays
// total because `u32` has none.
func ComponentString(s string) (ComponentValue, error) {
	if !utf8.ValidString(s) {
		// **No sentinel, deliberately.** `ErrUnsupported` means "this engine does not implement that yet"
		// and the CLI maps it to its own exit code; invalid UTF-8 is the **caller's own bad input**, not a
		// gap in the engine. An embedder matching `errors.Is(err, ErrUnsupported)` would read this as a
		// missing feature and wait for a release that is never coming.
		//
		// A plain error is classified by the taxonomy as the invocation's own failure, which is what it
		// is — and it adds no exported name. (Caught by the chair on the #913 review.)
		return ComponentValue{}, fmt.Errorf("a component string must be valid UTF-8, and this one is not; " +
			"the Canonical ABI's string is UTF-8 while a Go string is an arbitrary byte sequence, so the " +
			"two differ exactly here")
	}
	return ComponentValue{typ: ComponentTypeString(), s: s}, nil
}

// ComponentList constructs a `list<elem>` from values that must each **be** an `elem`.
//
// # The element check is structural, and it is the same function the engine uses
//
// ADR 0097's first change: as first drafted this compared `vals[i].Kind()` against the element kind, so
// `list<list<u32>>` would have accepted a `list<string>` element — both being lists. It calls
// [canon.TypeEqual] instead, through the value's full internal type, and that is the **same** function
// `Instantiated.CallValues` uses on every parameter. One structural comparison with two callers, rather
// than two that agree until they do not.
//
// The signature-level check at [Component.Call] is not made redundant by this and is not meant to be:
// this refuses a list that is internally inconsistent, that one refuses a well-formed list handed to the
// wrong parameter. Both are needed because `CallValues` is reachable without going through this
// constructor at all.
//
// # An empty list is legitimate and needs the element type
//
// `ComponentList(ComponentTypeU32())` is an empty `list<u32>`, which is a different value from an empty
// `list<string>` and must stay so — nothing in an empty list says what it is a list of. That is the
// whole reason [ComponentType] exists as a spellable thing, and the reason `elem` is a separate
// parameter rather than inferred from `vals[0]`.
func ComponentList(elem ComponentType, vals ...ComponentValue) (ComponentValue, error) {
	// Routed through the type constructor rather than building the type here, so the zero-element-type
	// refusal has one home. Duplicating that check would be the second copy that drifts.
	lt, err := ComponentTypeList(elem)
	if err != nil {
		return ComponentValue{}, err
	}
	for i, v := range vals {
		if v.typ.kind == KindComponentNone {
			// Named separately from a type mismatch because it is a different mistake: an unset value
			// rather than a wrong one. Reporting "want u32, got none" invites a reader to look for a
			// `none` type; what happened is that `vals[i]` was never constructed.
			return ComponentValue{}, fmt.Errorf("list element %d was never constructed — the zero "+
				"ComponentValue carries no type, so it is not a %s", i, elem)
		}
		if !canon.TypeEqual(v.typ.t, elem.t) {
			return ComponentValue{}, fmt.Errorf("list element %d is %s, but the list's element type is "+
				"%s; the types must match structurally, so a list of the wrong element type is refused "+
				"here rather than lowered at the wrong stride", i, v.typ, elem)
		}
	}
	// Copied, so a caller's later write to their own slice cannot reach inside a value they have already
	// handed over. `canon.List` does the same on the way in and `canon.Value.List` on the way out; a
	// value that is immutable in two of three directions is mutable.
	return ComponentValue{typ: lt, list: append([]ComponentValue(nil), vals...)}, nil
}

// U32 reads a `u32`, and reports whether this value is one.
//
// The boolean is the refusal: a value of another kind returns `(0, false)` rather than reinterpreting its
// bits. That is the read-side half of "constructors carry their WIT type" — a type tag nobody checks on
// the way out would make the construction-time check decorative.
func (v ComponentValue) U32() (uint32, bool) {
	if v.typ.kind != KindComponentU32 {
		return 0, false
	}
	return uint32(v.bits), true
}

// Str returns a `string` value's **content**, and reports whether this value is one.
//
// # Str versus String — do not confuse them
//
// [ComponentValue.String] is the **debug rendering**: it exists for `fmt`, it describes the value, and it
// returns something for *every* kind — `"u32(7)"`, or just the kind's name. `Str` returns the string's
// **content** and only for a `string`; for any other kind it returns `("", false)`.
//
// So `fmt.Sprintf("%s", v)` on a `string` value gives you a description, not the text. The text comes
// from `Str`. This is called out because the mistake is the obvious one and the compiler cannot catch it:
// both return a `string`, and the wrong one is silently plausible.
//
// The name is not an invention either — the codec's own `string` value constructor is `Str`, so this
// names its WIT type exactly as `U32` does. `String` was unavailable, being `fmt.Stringer`'s.
//
// The boolean is the refusal, as with [ComponentValue.U32]: `("", false)` for a non-string rather than
// the empty string alone, because `""` is a legitimate string and the two outcomes must be
// distinguishable.
func (v ComponentValue) Str() (string, bool) {
	if v.typ.kind != KindComponentString {
		return "", false
	}
	return v.s, true
}

// List reads a `list`'s elements, and reports whether this value is one.
//
// The boolean carries more weight here than for the scalars, because **an empty list is a legitimate
// value**: `(nil, false)` for a non-list and `(empty, true)` for an empty `list<u32>` must be
// distinguishable, and a caller checking `len(xs) == 0` cannot tell them apart. A `list<u32>` with no
// elements is exactly what a WASI getter returns.
//
// The slice is a copy, for the reason [ComponentList] records on the other side: a `ComponentValue` is
// passed by value and is immutable from outside this package, and handing back the backing array would
// be a writable window into a value somebody else also holds.
//
// There is deliberately **no element-type accessor** to go with this. A caller knows the signature it
// called, so it knows what the elements are; `ComponentType` introspection is ADR 0097's deferred
// surface with a recorded trigger — code that must branch on a returned value's type rather than
// knowing it in advance.
func (v ComponentValue) List() ([]ComponentValue, bool) {
	if v.typ.kind != KindComponentList {
		return nil, false
	}
	return append([]ComponentValue(nil), v.list...), true
}

// String renders the value for diagnostics, naming its kind so an unexpected one is legible.
//
// **This is the debug rendering, not the content** — see [ComponentValue.Str], which is the content and
// only for a `string`. A long string is truncated here, because a diagnostic that prints a megabyte of
// guest data is not a diagnostic.
func (v ComponentValue) String() string {
	switch v.typ.kind {
	case KindComponentU32:
		return fmt.Sprintf("u32(%d)", uint32(v.bits))
	case KindComponentString:
		const renderLimit = 64 // not `max`: that is a builtin as of Go 1.21 and shadowing it reads badly
		if len(v.s) > renderLimit {
			return fmt.Sprintf("string(%q… %d bytes)", v.s[:renderLimit], len(v.s))
		}
		return fmt.Sprintf("string(%q)", v.s)
	case KindComponentList:
		// **The length and the type, not the elements.** A `list<u32>` of a hundred million elements is
		// a value this engine accepts (that is the whole point of the kind-dependent byte cap), so a
		// renderer that expanded it would turn a diagnostic into a denial of service against whoever is
		// reading the log. The type comes from `v.typ`, which renders structurally, so an empty list
		// still says what it is a list of — the one question an empty list raises.
		return fmt.Sprintf("%s(%d element(s))", v.typ, len(v.list))
	default:
		// Every kind that cannot cross the boundary renders as its name alone, which is all there is to
		// say about a value that cannot exist yet. An explicit default rather than a fallthrough, so the
		// exhaustiveness linter is satisfied by a decision rather than by listing twenty-two kinds that
		// would all do the same thing.
		return v.typ.kind.String()
	}
}

// toCanon converts a public component value to the engine's, refusing any kind this release does not
// carry — **by name**, so an embedder learns which kind was wrong rather than that something was.
//
// No silent default: an unmapped kind is an error, which is the discipline `convert.go` states for the
// core-module boundary and the reason it has a derived-domain guard.
func (v ComponentValue) toCanon() (canon.Value, error) {
	switch v.typ.kind {
	case KindComponentU32:
		return canon.U32(uint32(v.bits)), nil
	case KindComponentString:
		// **A string argument crosses now** (ADR 0098). It was refused here until the engine could place
		// the bytes: lowering one means allocating in the guest's own linear memory through its
		// `cabi_realloc`, inside the callee's task on the entry the callee runs on.
		//
		// The refusal that stood here named the realloc as the obstacle, which was accurate and is now
		// discharged. What refuses instead, and refuses further in, is a lift that declares no `(realloc)`
		// or no `(memory)` canonopt — a condition of the *component*, not of this value, and so not
		// knowable at this point.
		return canon.Str(v.s), nil
	case KindComponentList:
		// **Elements convert through this same method**, so a `list<string>` gets `string`'s arm and a
		// `list<list<u32>>` recurses — rather than this arm learning each element kind, which is how the
		// two conversions would drift.
		//
		// The element type comes from the **list's own type descriptor**, not from `elems[0].Type`: an
		// empty list has no element to ask, and that is precisely the value whose type must survive the
		// crossing. `ComponentList` has already established that every element matches it.
		if v.typ.t.Elem == nil {
			// Unreachable through `ComponentList`, which always sets it, and checked rather than
			// dereferenced: this is the one place a nil would become a panic instead of an error, and a
			// hand-built zero value reaching here must refuse rather than crash the embedder's process.
			return canon.Value{}, fmt.Errorf("%w: a list value carries no element type, so it was not "+
				"built by ComponentList", ErrUnsupported)
		}
		elems := make([]canon.Value, 0, len(v.list))
		for i, e := range v.list {
			ce, err := e.toCanon()
			if err != nil {
				return canon.Value{}, fmt.Errorf("%w: list element %d: %w", ErrUnsupported, i, err)
			}
			elems = append(elems, ce)
		}
		// Through `canon.List` rather than building the value here, so the codec's own structural element
		// check runs on the way in as well. It is the third check on the same property and the cheapest:
		// a disagreement between it and `ComponentList` is a bug in the conversion, and this is where it
		// would surface rather than inside a lowering.
		return canon.List(*v.typ.t.Elem, elems...)
	case KindComponentNone:
		return canon.Value{}, fmt.Errorf("%w: a ComponentValue with no kind cannot cross the boundary — "+
			"the zero value names no WIT type, so it is a value nobody constructed rather than a u32(0)",
			ErrUnsupported)
	default:
		return canon.Value{}, fmt.Errorf("%w: a %s value cannot cross the component boundary yet; the "+
			"kinds are added one at a time without breaking changes", ErrUnsupported, v.typ.kind)
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
	case canon.KindString:
		s, ok := v.Str()
		if !ok {
			return ComponentValue{}, fmt.Errorf("%w: a canon value tagged string did not read as one",
				ErrUnsupported)
		}
		// **Not through `ComponentString`**, deliberately. That constructor validates UTF-8 because a Go
		// string from an embedder can be arbitrary bytes; this string came up through
		// `canon.LoadStringFromRange`, which already applied the model's UTF-8 trap
		// (`definitions.py:1386-1389`) against guest memory. Re-validating would be a second opinion about
		// a check that has already run and whose failure is a guest trap, not an embedder error — and if
		// the two ever disagreed, this is the side that would wrongly report an engine fault as a value
		// the embedder built wrong.
		return ComponentValue{typ: ComponentTypeString(), s: s}, nil
	case canon.KindList:
		elems, ok := v.List()
		if !ok {
			return ComponentValue{}, fmt.Errorf("%w: a canon value tagged list did not read as one",
				ErrUnsupported)
		}
		if v.Type.Elem == nil {
			return ComponentValue{}, fmt.Errorf("%w: a canon list carries no element type", ErrUnsupported)
		}
		// **The element type is converted first, and its refusal is the list's.** A `list<record>` coming
		// back has to fail here, naming `record`, rather than succeeding as a list whose elements then
		// each fail — which would report the same gap once per element and bury the one fact that matters.
		et, err := componentTypeFromCanon(*v.Type.Elem)
		if err != nil {
			return ComponentValue{}, err
		}
		out := make([]ComponentValue, 0, len(elems))
		for i, e := range elems {
			ce, cerr := fromCanon(e)
			if cerr != nil {
				return ComponentValue{}, fmt.Errorf("%w: list element %d: %w", ErrUnsupported, i, cerr)
			}
			out = append(out, ce)
		}
		// Through the public constructor, so an element whose type disagrees with the declared element
		// type is caught on the way **out** too. A guest cannot produce that through a correct codec, but
		// "cannot through a correct codec" is the assumption worth checking at a boundary: this is the
		// last place a wrong value is still an error rather than data in an embedder's hands.
		return ComponentList(et, out...)
	default:
		return ComponentValue{}, fmt.Errorf("%w: an export returned a %s, which this release cannot carry "+
			"across the public boundary; it carries u32, string and list", ErrUnsupported, v.Type)
	}
}

// componentTypeFromCanon converts a codec type to the public one, refusing a kind the public surface
// cannot spell — **by name**, in the direction nobody was looking.
//
// It exists because a list arriving from a guest needs its element type spelled publicly, and the value
// conversion cannot supply that: `fromCanon` maps a *value*, and an empty list has no element value to
// map. So the type needs its own conversion, and it needs to refuse on the same terms — a
// `list<record>` is refused naming `record`, not silently given some other element type.
//
// Kept beside `fromCanon` rather than hung off `ComponentType` as a method: it is a boundary conversion
// in this package's private direction, and ADR 0029's treatment is that those stay unexported so the
// representation can widen without any of it being API.
func componentTypeFromCanon(t canon.Type) (ComponentType, error) {
	switch t.Kind {
	case canon.KindU32:
		return ComponentTypeU32(), nil
	case canon.KindString:
		return ComponentTypeString(), nil
	case canon.KindList:
		if t.Elem == nil {
			return ComponentType{}, fmt.Errorf("%w: a canon list type carries no element type",
				ErrUnsupported)
		}
		// Recursive, so `list<list<u32>>` is spellable the moment `list<u32>` is. The depth is the
		// guest's type's depth, which the decoder has already bounded.
		et, err := componentTypeFromCanon(*t.Elem)
		if err != nil {
			return ComponentType{}, err
		}
		return ComponentTypeList(et)
	default:
		return ComponentType{}, fmt.Errorf("%w: an export returned a value of type %s, which this release "+
			"cannot spell across the public boundary; it spells u32, string and list of those",
			ErrUnsupported, t)
	}
}
