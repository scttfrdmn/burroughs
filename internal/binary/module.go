package binary

import "fmt"

// The internal form: what the decoder *retains* rather than merely recognizes.
//
// # Why this file exists at all, and why here
//
// Before it, nothing in the codebase could represent a module. 28 of the 29 `decode*`
// functions returned a bare `error` and the 29th returned `(bool, error)` where the bool
// reported whether a section *has* a grammar — so `Module` was `{Version, Sections}` of
// payloads aliasing the input image, which is a *verdict* about bytes and not a program.
// That one missing artifact was behind 93.6% of the board: #7 execution, #9 validation,
// #67's half-2 comparator, and the text encoder's target all wait on it.
//
// It grows out of the **decoder** rather than out of the text parser, and that is 0006's
// load-bearing-spot rule plus 0011's own option-B refusal: the decoder has a conformance
// record (4162 vectors) and the text path has never accepted a module, so shaping the
// representation from the text side would fit it to a producer that cannot yet exercise
// it. 0011's appended ruling states the ordering — internal form first, encoder targets
// it afterward.
//
// # The producer seam, which 0002 left open
//
// 0002 decides the *form* (`[]ins` with pre-decoded immediates and resolved branch
// targets, giant-switch dispatch, `[]uint64` plus a parallel `[]ref`). It does not decide
// who builds it. Two candidates: the descent grows retention as it goes, or a second pass
// re-reads the payloads the first pass aliased.
//
// **The descent, and the precedent is already in the tree.** `sawDataRef` is per-decode
// state on `Decoder`, set from inside `prefixed` at the bottom of the instruction
// grammar, because the question it answers is asked at the module level. Retention is the
// same shape and gets the same answer: one grammar, one traversal, state on the decoder.
// A second pass would be a second grammar over the same bytes — two places knowing the
// binary format, drifting silently, which is precisely the risk 0006 says to prefer away
// from.
//
// The cost of that choice is stated rather than hidden: `decode*` functions that used to
// return only an error now have somewhere to put what they read, so their signatures stay
// error-only and the *fields* land on the decoder's in-progress module. That keeps the
// existing error contract — every one of those 4162 vectors is a rejection, and a
// rejection's path must not change shape — while giving the accept direction a
// destination.

// ValType is a value type: a number/vector kind, or a reference shape wide enough to carry
// the twelve GC heaptypes and the indexed form alongside Wasm 2.0's two.
//
// **A struct rather than a byte, per decision 0018 (option C).** The five number/vector
// kinds and the two Wasm 2.0 reference kinds still fit one byte, and `kind` is exactly that
// byte — the encoding the decoder already reads, unconverted. What no longer fits is the
// other twelve `heaptype` forms `decodeRefType`/`decodeHeapType` validate: `(ref ht)` and
// `(ref null ht)` are *parameterized*, carrying a nullability bit and, for the indexed form,
// a resolved type index — two facts a single byte has nowhere to put. `null` and `idx` are
// those two facts, meaningful only when `kind` names a reference form (a numeric `kind` value
// leaves both at their zero value, which is exactly what makes the five numeric constants and
// the two Wasm 2.0 reference constants below identical in behavior to the byte enum this
// replaces).
//
// **Three fields of byte/bool/uint32, not a fourth for the pointer that isn't here.** 0002's
// GC-traceability pin is about a Go pointer hiding inside a `uint64` slot, invisible to the
// collector; `idx` is a plain index into the module's own type space, exactly as
// `Func.TypeIndex` already is, so this struct does not reopen that pin. Comparable with `==`
// by construction — no slice, no map, no pointer field — which is load-bearing: many call
// sites compare a retained type against a named constant (`t == I32`), and 0018 requires that
// every one of them keeps compiling and keeps meaning what it meant.
type ValType struct {
	// kind is the number/vector byte for the five Wasm 1.0/SIMD forms, or a heaptype tag for
	// every reference form: Wasm 2.0's two (0x70 func, 0x6F extern), the ten further GC
	// abstract forms (0x6E any, 0x6D eq, 0x6C i31, 0x6B struct, 0x6A array, 0x71 none, 0x73
	// nofunc, 0x69 exn, 0x74 noexn, 0x72 noextern), or kindIndexed — the sentinel meaning "the
	// indexed form; consult idx".
	kind byte

	// null is this reference type's semantic nullability, per the reference's own model —
	// `match_reftype`'s fact, the one a subtype check needs (non-null under nullable, never the
	// reverse). Meaningless when kind names a numeric/vector form, where it is always false.
	//
	// **Normalized at decode time, not spelled-bit retention** (grave #180, correcting a ruling
	// made on #179 that this field should retain the wire's *spelled* null bit instead — that
	// framing collapsed under its own worked example: `funcref` and `(ref func)` differ in the
	// spec and collided under it, while `funcref` and `(ref null func)` are the *same* spec type
	// and split apart. `decodeRefType`'s Wasm 2.0 branch expands the bare abbreviation to its
	// true nullable meaning (decode.ml's `funcref = ref null func`) rather than hardcoding
	// non-null for backward compatibility — see FuncRef/ExternRef below for the constants this
	// forces to move in lockstep. The load-bearing argument for spelled-bit retention was that
	// byte-identical re-encoding needs the distinction — checked and found false: nothing on the
	// decode side re-encodes a decoded module, so there was no consumer for the shape it was
	// blessed to serve.
	null bool

	// idx is the resolved type index. Meaningful only when kind is kindIndexed; zero
	// otherwise.
	idx uint32
}

// The twelve abstract heaptype forms' `kind` bytes.
//
// **Each derived from its own `sleb(7)` wire form by the same arithmetic `decodeHeapType` uses**
// — `form & 0x7F` — rather than transcribed as a hex literal. That is the difference between two
// places computing one fact from one number and two places agreeing by copy: the sleb form is the
// spec's own value (decode.ml:602-620), it appears once per line here, and a reader can check the
// line against the spec without knowing this type's packing. `TestHeapKindsAreWhatTheReaderProduces`
// closes the loop from the other end, decoding all twelve forms and comparing.
//
// Exported because the subtype lattice is the *interpreter's* — `match_heaptype` (match.ml:76-105)
// names these twelve forms explicitly and `ref.test`/`ref.cast` evaluate it — while the mapping from
// wire form to kind byte is this package's. A consumer that spelled 0x6E for `any` would be the
// second authority this comment exists to prevent.
const (
	HeapNoExn    = byte(-0x0C & 0x7F) // noexn
	HeapNoFunc   = byte(-0x0D & 0x7F) // nofunc
	HeapNoExtern = byte(-0x0E & 0x7F) // noextern
	HeapNone     = byte(-0x0F & 0x7F) // none
	HeapFunc     = byte(-0x10 & 0x7F) // func — Wasm 2.0, ungated
	HeapExtern   = byte(-0x11 & 0x7F) // extern — Wasm 2.0, ungated
	HeapAny      = byte(-0x12 & 0x7F) // any
	HeapEq       = byte(-0x13 & 0x7F) // eq
	HeapI31      = byte(-0x14 & 0x7F) // i31
	HeapStruct   = byte(-0x15 & 0x7F) // struct
	HeapArray    = byte(-0x16 & 0x7F) // array
	HeapExn      = byte(-0x17 & 0x7F) // exn
)

// kindIndexed tags the indexed reference form — `(ref $t)` / `(ref null $t)` — inside
// ValType.kind.
//
// **A value outside every legal wire byte, so it cannot collide with one.** `decodeRefType`
// and `decodeHeapType` read `sleb(7)`, whose legal range is -64..63 folded to a byte
// (`form & 0x7F`), which spans 0x00-0x7F — every byte a `kind` might otherwise hold. 0x80 sits
// one past that range, so no numeric/vector byte, no Wasm 2.0 reference byte, and no GC
// abstract heaptype byte is ever equal to it; the indexed form is the one case with no wire
// byte of its own to reuse; a real type index is carried in idx instead.
const kindIndexed byte = 0x80

// The eight named ValTypes, package-level `var` rather than `const` because a struct value
// is not a Go constant — the same reason resolvedVal's sibling shape (text/typetable.go)
// could not be `const` either. Not a weaker guarantee: nothing in this package or its
// consumers assigns through one of these variables, and TestValTypeNamedConstantsAreNotAlias
// pins that. `declaredValTypes` (module_test.go) reads them from source by walking
// composite-literal ValueSpecs rather than BasicLits, which is the AST shape that changed
// with this decision.
var (
	// NoValType is the zero ValType: kind 0x00, null false, idx 0.
	//
	// 0x00 is not the encoding of any value type, numeric or reference, so a field nothing
	// wrote reads as "unrepresentable" rather than silently reading as some real type — the
	// same role it played as the byte enum's sentinel, preserved by being the zero value of
	// every field rather than of one.
	NoValType = ValType{}

	I32  = ValType{kind: 0x7F}
	I64  = ValType{kind: 0x7E}
	F32  = ValType{kind: 0x7D}
	F64  = ValType{kind: 0x7C}
	V128 = ValType{kind: 0x7B}

	// FuncRef and ExternRef are Wasm 2.0's two ungated reference types, **nullable** — the
	// reference's own abbreviation (`funcref = ref null func`), corrected here from the non-null
	// value #179 shipped (grave #180). Every existing `t == FuncRef`-style comparison keeps
	// compiling and keeps returning the same answer it always did, because this constant moved
	// in lockstep with decodeRefType's Wasm 2.0 branch — the two were never independently
	// observable from outside this package, only their agreement was. What changes is what `==`
	// now means: type identity (`funcref == (ref null func)`, `funcref != (ref func)`) rather
	// than wire-spelling identity. The other twelve reference forms are GC's; decodeRefType/
	// decodeHeapType resolve them into ValTypes with the matching abstract kind, null bit, and
	// (for the indexed form) idx — see refKind and RefType below.
	FuncRef   = ValType{kind: 0x70, null: true}
	ExternRef = ValType{kind: 0x6F, null: true}
)

// refKind constructs an abstract-heaptype ValType — one of the twelve GC forms, or Wasm 2.0's
// FuncRef/ExternRef — with the given nullability.
//
// The decoder's own constructor, so `decodeRefType`/`decodeHeapType` never spell out a
// `ValType{...}` literal at the call site — the struct's field layout is this file's fact,
// not sections.go's, matching the reasoning BlockType already gives for keeping its packing
// rule in one place.
func refKind(kind byte, null bool) ValType {
	return ValType{kind: kind, null: null}
}

// refNull returns t with its nullability set to null, preserving kind and idx.
//
// The constructor for the case where the heaptype and the null bit come from *different places in
// the wire* — the cast family, where `decodeHeapType` yields the heaptype (always non-null, since
// the production has no null bit) and the opcode or a flags byte supplies the rest. Written as a
// function beside refKind and RefType for their reason: the field layout is this file's fact, so
// `castTypes` does not spell out a `ValType{...}` literal and cannot silently drop `idx` when the
// heaptype is the indexed form. Dropping it is the live hazard — `(ref null $t)` and
// `(ref null any)` differ only in the two fields a partial copy loses.
func refNull(t ValType, null bool) ValType {
	t.null = null
	return t
}

// RefType constructs the indexed reference form — `(ref $t)` / `(ref null $t)` — naming
// resolved type index idx.
//
// Exported, unlike refKind: the abstract forms are this package's own closed vocabulary and
// every call site lives here, but the indexed form's index comes from the decoder's read of
// the module's own type space and both decodeRefType and decodeHeapType (sections.go, two
// different productions with two different callers) need to build one.
func RefType(idx uint32, null bool) ValType {
	return ValType{kind: kindIndexed, null: null, idx: idx}
}

// IsIndexed reports whether this is the parameterized indexed reference form — `(ref $t)` /
// `(ref null $t)` — in which case Index and Null are meaningful.
func (t ValType) IsIndexed() bool {
	return t.kind == kindIndexed
}

// Index returns the resolved type index for the indexed reference form. Meaningless, and
// always 0, when !t.IsIndexed().
func (t ValType) Index() uint32 {
	return t.idx
}

// Null reports this reference type's nullability — `match_reftype`'s fact, the one a subtype
// check needs (non-null under nullable, never the reverse; 0019's forced consumer). Meaningless,
// and always false, for a numeric/vector ValType.
//
// **Semantic, not wire-spelling, as of grave #180's fix.** #179 shipped this as the *spelled*
// null bit (the wire's own reftype/heaptype-prefix distinction) with a separate Nullable()
// accessor for semantic nullability, on the argument that FuncRef/ExternRef's spelled bit and
// semantic nullability genuinely differ. They don't have to: decodeRefType now normalizes the
// bare abbreviation to its true nullable meaning at decode time (FuncRef/ExternRef moved to
// null:true in lockstep), so this single field is correct under both readings and the two-accessor
// split doesn't exist anymore. See ValType.null's field comment for the full account, including
// why the old split was itself part of the defect.
func (t ValType) Null() bool {
	return t.null
}

// Kind returns the raw kind byte for a ValType that has one: the five numeric/vector wire
// bytes, or Wasm 2.0's FuncRef (0x70) / ExternRef (0x6F). The second result is false for the
// indexed form (which has no single byte — consult Index instead) and for NoValType.
//
// **The accessor `internal/text`'s encoder needs and nothing more**, per 0018's consequences:
// this PR does not teach the encoder to emit GC forms, so byte(binary.I32)-style conversions
// at existing call sites need a replacement that keeps behaving identically for exactly the
// two Wasm 2.0 reference kinds and the five numeric kinds they already handle. A GC abstract
// kind (any, eq, i31, …) also has a byte and this reports it — the predicate is "has one
// wire byte", not "is one of the seven pre-0018 forms" — but nothing in this PR's scope calls
// Kind with one, since the encoder's own frontier (absoluteHeaptypeBytes) is untouched.
//
// **The NoValType arm is a repair, not a widening** (grave #300). This doc comment has always
// said the second result is false "for the indexed form … and for NoValType", and the code
// checked only the first — so a zero ValType reported `(0x00, true)`, claiming 0x00 is a wire
// byte in the same file whose NoValType comment says 0x00 "is not the encoding of any value
// type, numeric or reference". No caller was wrong as a result: the three encoder sites pass
// named types, `castop.go` discards the second result, and `instr.go`'s site reads it only
// after `decodeValType` succeeded. That is what kept it silent, and it is also why the fix is
// safe — every existing call returns exactly what it returned before. It surfaced the way this
// class always does, by writing the first consumer that takes the documented guarantee at its
// word: the public boundary's type conversion (0029), which is outside this package and so has
// only the doc comment to go on.
func (t ValType) Kind() (byte, bool) {
	if t.kind == kindIndexed || t == NoValType {
		return 0, false
	}
	return t.kind, true
}

// AbstractRefType constructs one of the twelve abstract reference forms — the heaptype named by
// kind, with the given nullability — reporting false for a byte that is not one of the twelve.
//
// **refKind's exported sibling, and it exists because the space is not constructible from
// outside this package.** The twelve `Heap*` kind bytes are exported, `RefType` builds the
// indexed form, and the five numeric types plus Wasm 2.0's two references are named `var`s — so
// an external consumer can *name* every reference form and could not *build* ten of them. The
// consumer that needs to is the public boundary's exhaustiveness guard (0029), whose whole
// method is to range over the reference space and assert every element converts; enumerating it
// with hand-written literals would make the guard's domain a transcription of the thing it is
// checking, which is the coverage defect it exists to prevent.
//
// The predicate is `abstractHeapNames`, the same single table HeapTypeName reads, so the two
// accessors agree by construction rather than by a cross-check between two copies. That is what
// makes ranging over all 256 bytes and keeping the ones this accepts a *derivation* of the
// twelve rather than a second enumeration of them.
func AbstractRefType(kind byte, null bool) (ValType, bool) {
	if _, ok := abstractHeapNames[kind]; !ok {
		return NoValType, false
	}
	return refKind(kind, null), true
}

func (t ValType) String() string {
	switch t {
	case NoValType:
		// Its own case, and not folded into the "unknown" default: the two are different
		// facts and the lint that required this arm was right to. *Unknown* means a byte
		// this type has no name for; *unrepresentable* means a form the spec defines and
		// this type deliberately cannot hold — the twelve GC reference types. A consumer
		// printing "unknown" for one of those would report a defect in the module where the
		// truth is a limitation of the engine's representation, which is grave #36's class
		// (an engine lying about its input) in a String method.
		//
		// **Stale as of 0018's implementation**: every GC reference form now has a
		// representation, so this case names only the genuine zero value — a field nothing
		// wrote — rather than a form this type declines to hold. Left as "unrepresentable"
		// rather than folded into "unknown" for the same reason it was split out originally:
		// the two are still different facts about *why* a String call reached the default,
		// even though the population that used to reach it is gone.
		return "unrepresentable"
	case I32:
		return "i32"
	case I64:
		return "i64"
	case F32:
		return "f32"
	case F64:
		return "f64"
	case V128:
		return "v128"
	case FuncRef:
		return "funcref"
	case ExternRef:
		return "externref"
	}
	if t.kind == kindIndexed {
		if t.null {
			return fmt.Sprintf("(ref null %d)", t.idx)
		}
		return fmt.Sprintf("(ref %d)", t.idx)
	}
	if name, ok := abstractHeapNames[t.kind]; ok {
		if t.null {
			return "(ref null " + name + ")"
		}
		return "(ref " + name + ")"
	}
	return "unknown"
}

// abstractHeapNames names every abstract heaptype String can print by kind byte, including
// func/extern (0x70/0x6F): FuncRef/ExternRef's own named cases above intercept the *nullable*
// spelling (`switch` tests full-struct equality, matching kind *and* null, and both constants are
// null:true as of grave #180's fix, so `funcref`/`(ref null func)` match there directly) — but
// `(ref func)`/`(ref extern)`, the genuinely non-null spelling, is a different ValType that falls
// through to this map, and needs its own entry or String prints "unknown" for a well-formed type.
// That gap predates #180 (present since 0018's implementation, PR #179) and is fixed alongside it
// as the same sweep-after-a-grave: a struct/array kind reaching here needed a name and got one; the
// two Wasm 2.0 kinds needed the identical treatment for their non-null spelling and had not gotten
// it. TestValTypeStringMatchesTheReferenceOnFuncExternNonNull pins the fix.
//
// **Keyed on the Heap* constants rather than on twelve hex literals as of 0027**, which is a
// deduplication and not a tidy-up: this map was the second enumeration of the wire-form-to-kind-byte
// mapping, and the interpreter's cast lattice needed a third. Rekeying leaves exactly one place
// where a form's byte is written down, so a form whose kind byte were wrong here would now be wrong
// in the reader too — visible rather than a name printed against the wrong type.
var abstractHeapNames = map[byte]string{
	HeapAny:      "any",
	HeapEq:       "eq",
	HeapI31:      "i31",
	HeapStruct:   "struct",
	HeapArray:    "array",
	HeapNone:     "none",
	HeapNoFunc:   "nofunc",
	HeapExn:      "exn",
	HeapNoExn:    "noexn",
	HeapNoExtern: "noextern",
	HeapFunc:     "func",
	HeapExtern:   "extern",
}

// HeapTypeName names an abstract heaptype form by its kind byte — `string_of_heaptype`
// (types.ml:336-350) for the twelve forms that have a name, reporting false for the indexed form
// and for any byte that is not a heaptype at all.
//
// **Exported so the interpreter can render a reftype the reference's way rather than this type's**
// (0027). `ValType.String` intercepts the *nullable* func and extern spellings as `funcref` and
// `externref`, which is the right rendering for a valtype and the wrong one inside `ref.cast`'s trap
// text: the reference builds that message from `string_of_reftype`, which is uniformly
// `(ref null func)`. A trap tail is the half of an error the oracle does not read (it matches
// `cast failure` and stops), so it is ours alone to keep honest — grave #36 — and honest here means
// spelling the type the way the authority whose verdict we are quoting spells it. Exposing the names
// rather than a whole reftype formatter keeps the *naming* authority in one place, this map, while
// leaving the assembly to the caller that knows which convention it wants.
func HeapTypeName(kind byte) (string, bool) {
	name, ok := abstractHeapNames[kind]
	return name, ok
}

// IsRef reports whether values of this type live in the reference array rather than the
// numeric one.
//
// **0002 pins this as a consequence, not a detail.** A Go pointer stored in a `uint64` is
// invisible to the garbage collector and pure Go (no cgo) offers no escape hatch, so the
// value stack is two parallel arrays from the first line of interpreter code — and the
// predicate that decides which array a slot uses has to exist before any opcode touches
// the stack. Adding it later means auditing every stack-touching opcode.
//
// **Widened per 0018's consequences**, from `t == FuncRef || t == ExternRef` to a range check
// over every reference-shaped kind: the two Wasm 2.0 forms, the ten further GC abstract
// forms, and the indexed sentinel. Every one of those is a reference wire-form, so every one
// belongs in the reference array — including the ten this engine could not represent before
// 0018's struct, where they were reachable in the all-gates-on lane only as NoValType, which
// reports numeric (false) and would have misfiled a live reference as a number the moment
// this type grew a way to name it.
func (t ValType) IsRef() bool {
	if t.kind == kindIndexed {
		return true
	}
	switch t.kind {
	case FuncRef.kind, ExternRef.kind,
		0x6E, 0x6D, 0x6C, 0x6B, 0x6A, 0x71, 0x73, 0x69, 0x74, 0x72:
		return true
	}
	return false
}

// FuncType is a function's signature: the params and results of `functype`.
//
// Two slices rather than a count plus a flat array, because the validator asks about them
// separately and an arity is derivable from a slice while a slice is not derivable from
// an arity.
type FuncType struct {
	Params  []ValType
	Results []ValType
}

// CompKind distinguishes the three composite type forms — `comptype` (decode.ml:250-259).
type CompKind byte

const (
	CompFunc CompKind = iota
	CompStruct
	CompArray
)

func (k CompKind) String() string {
	switch k {
	case CompFunc:
		return "func"
	case CompStruct:
		return "struct"
	case CompArray:
		return "array"
	}
	return "unknown"
}

// CompType is one entry in the type index space.
//
// **This is a slice of comptypes rather than of functypes, and index alignment is why.**
// GC's `struct` and `array` forms occupy type indices exactly as `func` does, so a
// `[]FuncType` that skipped them would silently shift every later index — and it would do
// so *only* in the all-gates-on lane, where GC accepts them. That is the worst shape a
// defect can have here: correct on the default board, wrong in the lane whose whole job is
// to catch what the default board cannot see. So every accepted comptype takes a slot.
//
// **The struct and array contents are retained as of decision 0021** (option C): a
// struct's fields, one per declared field in declaration order, or an array's single field
// — `comptype`'s own arity, never a vector for the array case (decode.ml:250-259). Field
// *names* are not retained; 0021 excludes them by the same wire-form authority `LocalGroup`
// already established (0016) — the wire's `fieldtype` production carries no identifier, so
// there is nothing here to keep.
//
// **Supertypes and Final are retained as of 0019's own named gap** (the `sameFuncType` widening
// its ADR text calls out by name): a subtype's declared supertype list — `vec(typeuse u32)`
// (decode.ml:262-271) — as plain type indices, following `Func.TypeIndex`'s convention for a
// retained-but-unresolved index (index *validity* is #9's question, not this struct's, so the
// slot holds what the module said rather than what it resolves to), and its finality bit.
//
// **`Final` turned out to be load-bearing, reversing this field's first draft.** The draft
// argued it away on the grounds that `check_subtype_sub`'s finality check (valid.ml:163-174,
// "a sub type may not name a final super type") is #9's static well-formedness rule and this
// fix computes no such check — true, but a different fact than whether `match_deftype`
// (match.ml:151-155) reads the bit, and it does: `SubT (fin, uts, ct)` is compared as a whole
// tuple in the structural-equality disjunct (`subst_deftype s dt1 = subst_deftype s dt2`), so
// two declared types with identical comptypes and supertype lists but opposite finality compare
// UNEQUAL — exactly `type-subtyping.wast:602/610`'s shape (`$t1` non-final, `$t2` final, both
// `(func)`). Missing the bit there would keep those two vectors wrongly accepting.
type CompType struct {
	Kind CompKind

	// Func is the signature, valid only when Kind is CompFunc.
	Func FuncType

	// Fields is the struct's or array's field list, valid only when Kind is CompStruct or
	// CompArray: one entry per declared field for a struct, in declaration order; exactly
	// one entry for an array, per arraytype's own arity (decode.ml:257-258, one fieldtype,
	// no vector — a struct's `vec(fieldtype)` and an array's bare `fieldtype` are different
	// productions, and this field's length reflects whichever one produced it).
	Fields []FieldType

	// Supertypes is the subtype's declared supertype list, one type index per `typeuse` in
	// `sub`/`sub final`'s `vec(typeuse u32)` (decode.ml:262-271) — empty for a bare comptype
	// with no `sub` wrapper, which the grammar defaults to `Final, []` (no declared
	// supertypes at all). Unresolved, for `Func.TypeIndex`'s reason: this is what the module
	// said, and whether an index names anything is #9's question.
	Supertypes []uint32

	// Final is the subtype's finality bit: true for a bare comptype or an explicit `sub final`
	// (both `SubT (Final, ...)`, decode.ml:262-271's default and its 0x4f arm), false only for
	// an explicit `sub` without `final` (the 0x50 arm, `SubT (NoFinal, ...)`).
	Final bool

	// RecStart and RecLen are the extent of the recursive type group this entry belongs to:
	// the type index of the group's first member, and the number of members. Every entry is in
	// exactly one group — a bare subtype is a singleton, per decode.ml:276's `RecT [st]` — so
	// neither field has an "ungrouped" value and `RecStart <= own index < RecStart+RecLen`
	// always holds.
	//
	// **These two fields are the type's *identity*, and that is the whole reason they are
	// retained.** A `deftype` in the spec is `DefT (rectype, i)` — the entire group paired with
	// the member's ordinal — not the member's comptype. Two structurally identical functypes in
	// differently-shaped groups are different types, and `match_deftype`'s structural-equality
	// disjunct (`subst_deftype s dt1 = subst_deftype s dt2`, match.ml:151) compares the *rolled*
	// forms, in which references inside the group have become ordinals. Retained by
	// `labelRecGroup`, consumed by `internal/validate`'s port of that disjunct.
	//
	// The alternative, which is what this struct held before, is worth naming because it looks
	// adequate: with only the flat comptypes, the finest relation computable is bisimulation over
	// type indices — the *equi*-recursive equality. It is strictly coarser than the spec's, so it
	// **accepts** modules the spec rejects, which is the direction the board cannot see. Eight
	// `type-rec.wast` vectors are the witnesses for the difference, and they were admissions
	// blocked behind a *different* unwritten rule when this field was added, so the falsifier for
	// getting this wrong was not available at the time it would have been needed.
	RecStart uint32
	RecLen   uint32
}

// StorageType is a fieldtype's storage: either a full ValType, or one of the two packed
// widths (i8/i16) the reference's packtype production admits — decode.ml:236-241, exactly
// two forms, never a third.
//
// **Not folded into ValType, per decision 0021's rejection of that option**: i8/i16 are
// not value types at all — a struct field holding i8 unpacks to i32 on every read
// (`Aggr.read_field`, `type_of_ref`) — so the packed width is a storage optimization with
// no corresponding ValType, not a fifteenth valtype. The spec's own storage-versus-value
// distinction (`storagetype` versus `valtype`, two different productions) maps to two Go
// types here, the grammar's own boundary respected rather than flattened.
type StorageType struct {
	// Val is the full value type, meaningful when !Packed.
	Val ValType

	// Packed reports whether this storage is one of the two packed widths rather than a
	// full ValType.
	Packed bool

	// Width is 8 or 16, meaningful only when Packed.
	Width byte
}

// FieldType is one struct or array field: its storage and whether it may be written after
// allocation — `fieldtype` (decode.ml:243-246), storage type plus one mutability bit.
type FieldType struct {
	Storage StorageType
	Mutable bool
}

// Func is one function: its type index, its declared locals, and its body.
//
// The type index rather than a resolved *FuncType, and the reason is layering: index
// *validity* is #9's question, so the decoder records what the module said and the
// validator decides whether it names anything. Resolving here would make the decoder
// reject a module for a reason the spec calls invalid rather than malformed — the
// wrong-layer error the `constant expression required` debt already documents.
type Func struct {
	TypeIndex uint32

	// Locals is the declared local **groups**, one entry per `(count, valtype)` run in the
	// image — *not* one entry per local.
	//
	// # It was the flattened vector, and 30 bytes bought a 4 GiB lunch
	//
	// The wire form is runs, and this field used to hold them expanded: one `ValType` per
	// local, flattened by `decodeLocals` once the `too many locals` sum check passed. That
	// check is the reference's (`total >= 1<<32`) and it is right about the *verdict* — but
	// `ValType` is a byte, so a body declaring `0xFFFFFFFE` locals is a **legal** module
	// that the old field expanded into 4.00 GiB from a 30-byte image, measured. Grave #138.
	//
	// **No vector could see it, in either direction**: the module is spec-legal, this engine
	// agreed with the reference on accepting it, and the only witness was a resource
	// measurement. The suite is the oracle for verdicts and is silent on cost by
	// construction, which is why the fuzzer found it and three years of boards would not
	// have. The hazard was even *stated* one comment away — the old prose explained that
	// flattening waits for the sum check so four billion entries are not allocated for a
	// module the next line refuses, which is true of `0xFFFFFFFF` and silent about
	// `0xFFFFFFFE`, the neighbour that next line **accepts**. The rule was right; only its
	// extent was wrong, and a boundary comment that does not state its extent reads as a
	// proof. (Ruling: Scott, PR #137.)
	//
	// # So retention is the wire form, which is the truer reading anyway
	//
	// Keeping runs is not merely the cheap fix. The image says "n of this type" and this now
	// says the same thing, so decode cost is proportional to the *compressed* size — the
	// decoder stops paying execution's rent. A consumer that genuinely needs 4Gi slots bills
	// itself, where the cost can be bounded or refused on execution's terms — `interp` does
	// exactly that, and it refuses as an **engine limit** rather than as a decode error or a
	// trap. Not a trap specifically: a trap is a spec-defined outcome, so reporting one would
	// have this engine claim the module traps when the spec says it runs. The module is
	// well-formed and this phase cannot run it, which is precisely the third category.
	//
	// `TotalLocals` is the flat count, and `EachLocal` iterates the flat reading for the
	// consumers that want one — both without materializing anything.
	Locals []LocalGroup

	// Body is the internal form: `[]Instr` with immediates pre-decoded and branch
	// targets resolved to indices in this slice (0002, Q1 option B).
	Body []Instr

	// Labels holds the unbounded immediates that do not fit `Instr`'s two words, keyed by the
	// instruction's index in Body. Today that is `br_table`'s label vector and nothing else.
	//
	// **A side table rather than a field on Instr, and the reason is 0002's arithmetic** (0016).
	// A `[]uint32` on Instr is 24 bytes on every instruction of every module to serve one
	// opcode in 256; on a megabyte-scale Go guest — §1's workload, the one the engine is
	// designed to — that is a tax the rewrite's win cannot absorb. This map costs nothing for
	// the overwhelming majority of functions, which contain no `br_table` at all.
	//
	// **Nil is the normal case and reading a missing key is not an error.** A nil map yields nil
	// for every lookup, which is the right answer for an instruction that has no label vector.
	// Consumers go through `LabelVector` rather than indexing directly, so the "no vector" and
	// "empty vector" cases stay distinguishable — a `br_table` with zero labels is legal and
	// means *every* index takes the default.
	//
	// **The vector is the wire form: label indices in written order, default excluded.** The
	// default lives in the instruction's `Imm0` — measured by decoding a `br_table 0 1` with
	// default `1` and printing the fields, because `immVecIdx` stages no word and so `immIdx` is
	// this opcode's *first* staged immediate rather than its second. Reasoning from the table
	// row's field order gives `Imm1` and is wrong. Keeping the
	// written order and the written length rather than a resolved target array is deliberate —
	// resolving a label index to a body offset needs the enclosing block structure and needs the
	// index to be *in range*, which is #9's judgement, and the text encoder needs the length as
	// written. Same rule `LocalGroup` follows: retention is forced by a consumer, but its shape
	// comes from the grammar, because the first consumer is rarely the last.
	Labels map[int][]uint32

	// Catches holds `try_table`'s handler-clause vectors, keyed the same way Labels is —
	// this is the side table `immCatchVec`'s own comment named as waiting on a consumer,
	// shaped like Labels but for a clause that is a tag index plus a label rather than a
	// bare label. See the Catches type for the full shape rationale.
	Catches Catches

	// Casts holds the reference types the cast family tests against — `ref.test`/`ref.cast`'s
	// one, `br_on_cast`/`br_on_cast_fail`'s two — keyed the same way Labels and Catches are
	// (0027 Q1 option B).
	//
	// **A third side table rather than packed words, and the reason is a capacity control this
	// project already paid for.** 0027's first draft packed a heaptype into `Instr`'s two words:
	// a heaptype is a kind byte plus a 32-bit index plus a null bit, so `ref.test` fits with
	// room over and even `br_on_cast`'s pair fits in 114 of 128 bits. It was rejected before a
	// line was written by `TestInstrImmediateWidthCoversTheTable`, which sums `immStagedBits`
	// **per immediate kind, globally**, against the words the decoded probe actually fills.
	// `br_on_cast` already sits at exactly 128 bits (label + flags + two heaptypes), so
	// `immHeapType` cannot cost 40 bits for `ref.test` and 0 bits for `br_on_cast` — making the
	// row fit needs a per-row exception in the very control that bounds the packing, which is
	// the mechanism that let grave #100 drop fourteen lane indices. A side table costs the words
	// nothing and needs no exception.
	//
	// **The types are retained as full ValTypes, nullability included, because the wire holds no
	// reftype anywhere in this family — it holds a bare heaptype and puts the null bit somewhere
	// a heaptype reader cannot see.** Where that somewhere is differs per opcode, and the
	// previous revision of this sentence asserted otherwise in bold ("nullability comes from the
	// opcode and not from the encoding"), which was true of the four rows that existed when it
	// was written and false as the family-wide claim it was phrased as:
	//
	//   - `fb 14`-`fb 17` read a bare *heaptype* and take the null bit from which of the four
	//     opcodes it is — `ref.test null` versus `ref.test` (decode.ml:636-639).
	//   - `fb 18`/`fb 19` read a flags **byte** and take rt1's null bit from bit 0 and rt2's from
	//     bit 1 (`:644-645`), so one instruction can be nullable on one side and not the other.
	//     For this pair the nullability *is* encoding, and a reader following the old sentence
	//     would look for it in the opcode and find two rows that do not have it.
	//
	// Either way a consumer reconstructing the reftype would have to re-derive the mapping — a
	// switch for four rows, a bit test for two — at every call site. `castTypes` does it once, at
	// decode. Corrected here in the diff that added the pair, rather than left for the reader who
	// trusts a bold claim over the four lines below it: *the defect stated as the rule* is the
	// strongest camouflage a bug wears, because review checks code against claims.
	//
	// Nil is the normal case and a missing key is not an error, exactly as for Labels; consumers
	// go through `CastTypes`.
	Casts map[int][]ValType

	// Selects holds `select`'s optional result-type annotation (`0x1C`), keyed the way the three
	// side tables above are — the retention `immVecValType`'s own comment deferred until a
	// consumer existed ("the retention it wants is its own field, on the day #9 needs the
	// annotation"). #294 is that day; `internal/validate`'s annotated arm is the consumer.
	//
	// **The whole vector, not its single legal element**, and the reference is why. `valid.ml:443`
	// caps the arity at one — `require (List.length ts = 1)`, "invalid result arity other than 1
	// is not (yet) allowed" — but that is the *validator's* rule, and the two corpus vectors it
	// converts are a `(result)` of arity 0 and a `(result i32 i32)` of arity 2 (`select.wast:368`,
	// `:373`). A decoder that kept only `ts[0]` would have discarded the fact those vectors are
	// about, and a decoder that *rejected* them would be manufacturing malformedness out of a
	// typing rule — both wire forms decode, and their arity is a question for the layer that has
	// the reference's `require`.
	//
	// **`Instr.Imm0`'s reference-ness bit stays, and it is a derived cache of this field rather
	// than a second source for it.** The interpreter dispatches `select` onto one of two stacks and
	// cannot afford a map lookup per execution to learn which (#196/#197's argument, at
	// `immVecValType`); the validator needs the types themselves. So the word carries a bit
	// computed from the vector at decode, and `TestSelectImm0AgreesWithTheAnnotation` asserts the
	// two agree over the whole shape of the annotation — the bidirectional control two fields
	// holding one fact are owed, written when the second field landed rather than after they
	// disagreed.
	//
	// Nil is the normal case and a missing key is not an error, as for Labels; consumers go
	// through `SelectTypes`.
	Selects map[int][]ValType
}

// LabelVector returns the label vector retained for the instruction at index i, and whether one
// was retained at all.
//
// The two-result form is the whole reason this is a method: `f.Labels[i]` cannot distinguish a
// `br_table` whose vector is empty — legal, and meaning every index takes the default — from an
// instruction that is not a `br_table` and has no vector. Those are different facts, and a
// consumer that conflates them executes a `br_table` as though its labels were absent rather
// than empty.
func (f *Func) LabelVector(i int) ([]uint32, bool) {
	v, ok := f.Labels[i]
	return v, ok
}

// CastTypes returns the reference types retained for the cast-family instruction at index i, and
// whether any were retained at all.
//
// Two results for LabelVector's reason one step sharper: an arm reading `f.Casts[i]` gets a nil
// slice both for "not a cast instruction" and for a cast instruction whose types the decoder
// failed to file, and the second is an engine bug that must be reported rather than executed. A
// `ref.cast` against a nil type would otherwise test against the zero ValType — `NoValType`,
// which is no type at all — and silently answer the wrong question.
func (f *Func) CastTypes(i int) ([]ValType, bool) {
	v, ok := f.Casts[i]
	return v, ok
}

// SelectTypes returns the result-type annotation retained for the `select` at index i, and whether
// one was retained at all.
//
// Two results, and here the distinction is load-bearing in a way it is not for the other three
// tables: **`select` has two opcodes and only one of them carries a vector.** `0x1B` is the bare
// form and files nothing; `0x1C` files its vector, which may legally be *empty* on the wire — a
// `(select (result) …)` decodes and is rejected by the validator's arity rule, not by the decoder
// (`select.wast:368`). So `len(v) == 0` is exactly the case a consumer must be able to tell from
// "this is the unannotated opcode", and the second result is the only thing that can tell it.
func (f *Func) SelectTypes(i int) ([]ValType, bool) {
	v, ok := f.Selects[i]
	return v, ok
}

// CatchKind is the one-byte tag on a `try_table` handler clause that selects which of the
// four catch forms a Catch value is (decode.ml:975-981, encode.ml:905-910).
type CatchKind byte

const (
	// CatchTag is wire byte 0x00: `catch` — a tag index and a label index.
	CatchTag CatchKind = 0x00

	// CatchTagRef is wire byte 0x01: `catch_ref` — the same wire shape as CatchTag (a tag
	// index and a label index; decode.ml:977-978 reads both forms identically). What
	// distinguishes it is not an extra wire field but an execution fact: eval.ml's reduction
	// for `CatchRef` pushes an exnref naming the caught exception *ahead of* the payload
	// values before branching, where plain `Catch` pushes only the payload
	// (eval.ml:1084-1094: `vs0 @ vs` versus `Ref Exn.(ExnRef (Exn (a, vs0))) :: vs0 @ vs`).
	// Retention needs no extra field for this — the Kind byte alone selects the behaviour,
	// which is rung 2's (execution) to build.
	CatchTagRef CatchKind = 0x01

	// CatchAny is wire byte 0x02: `catch_all` — a label index only, no tag. Matches any
	// exception; TagIndex is meaningless for this kind and is not retained.
	CatchAny CatchKind = 0x02

	// CatchAnyRef is wire byte 0x03: `catch_all_ref` — the same single label index as
	// CatchAny, with the same exnref-before-branch execution fact CatchTagRef adds to
	// CatchTag (eval.ml:1096-1104). Again no extra wire field, just the Kind byte.
	CatchAnyRef CatchKind = 0x03
)

// Catch is one `try_table` handler clause, retained exactly as `catch` reads it
// (decode.ml:975-981) and as `catch` writes it (encode.ml:905-910).
//
// **Verified against the reference's own AST rather than assumed structurally identical to
// "a non-ref sibling plus a flag bit"**: `ast.ml`'s `catch'` variant is `Catch of tagidx *
// labelidx | CatchRef of tagidx * labelidx | CatchAll of tagidx | CatchAllRef of tagidx`
// (ast.ml:266-271) — every wire form's *data* is exactly the one or two indices the wire
// grammar already reads (decodeCatch's existing `idxs` count: 2 for catch/catch_ref, 1 for
// catch_all/catch_all_ref), and decode.ml/encode.ml never read or write an extra byte for
// either "_ref" form. The distinguishing fact between a form and its "_ref" sibling lives
// entirely in `eval.ml`'s reduction rules (:1084-1104): the "_ref" forms additionally push
// an ExnRef ahead of the payload before branching. That is a fact about *execution*, not
// about the wire, so CatchKind alone (four values, matching the four wire bytes) is
// sufficient retention for this rung — there is no fifth field to add.
type Catch struct {
	// Kind is the wire byte: which of the four clause forms this is.
	Kind CatchKind

	// TagIndex is the tag this clause matches, for CatchTag and CatchTagRef only. Zero and
	// meaningless for CatchAny and CatchAnyRef, which match every exception —
	// decodeCatch never reads a tag index for those two kinds, so there is nothing to
	// retain here for them.
	TagIndex uint32

	// LabelIndex is the block this clause branches to on a match, retained for every kind —
	// decode.ml:977-980 reads one for all four forms.
	LabelIndex uint32
}

// Catches holds `try_table`'s handler-clause vectors, keyed by the instruction's index in
// Body — `Labels`' exact shape and staging discipline (0016), for a clause that is a tag
// index plus a label rather than a bare label, which is why `immCatchVec`'s own comment
// names this as wanting "a side table of its own shape rather than a share of Labels".
//
// **Nil is the normal case, and a nil map yields nil for every lookup** — the same
// br_table-derived reasoning `Labels` carries: this map costs nothing for the overwhelming
// majority of functions, which contain no `try_table` at all.
//
// **A `try_table` with zero catch clauses is legal** — decode.ml's `vec (at catch) s` accepts
// a zero-length vector, and it means every exception thrown in the body falls through
// uncaught to the enclosing frame. So `len(x) == 0` cannot mean "absent" any more than it
// can for `Labels`, and `CatchVector`'s two-result form is what keeps "no vector" and
// "empty vector" distinguishable for a consumer.
type Catches map[int][]Catch

// CatchVector returns the catch-clause vector retained for the instruction at index i, and
// whether one was retained at all.
//
// The two-result form mirrors `LabelVector` exactly and for the identical reason: `f.Catches[i]`
// cannot distinguish a `try_table` whose clause vector is empty — legal, and meaning every
// exception falls through uncaught — from an instruction that is not a `try_table` and has no
// vector at all. A consumer that conflated them would execute a `try_table` as though its
// clauses were absent rather than deliberately empty.
func (f *Func) CatchVector(i int) ([]Catch, bool) {
	v, ok := f.Catches[i]
	return v, ok
}

// LocalGroup is one `(count, valtype)` run of a body's local declarations, exactly as the
// image spells it.
//
// **Count is a uint32 and deliberately not narrowed**, because narrowing it here would move
// the `too many locals` verdict out of the decoder and into this type's constructor — and
// the verdict belongs where the reference puts it, on the 64-bit sum across all groups. A
// single group's count is legal up to 0xFFFFFFFF; it is the *sum* that is bounded.
type LocalGroup struct {
	Count uint32
	Type  ValType
}

// TotalLocals is the flat local count: the sum of the groups' counts.
//
// Returns a uint64 because that is the width the sum is *computed* at — `decodeLocals`
// rejects a sum at or above 2^32, so every retained Func's total fits in 32 bits, and
// returning the wider type keeps this function honest for a hand-built Func that never went
// through the decoder. A consumer that needs an int says so at its own call site, where the
// bound it is checking against lives.
func (f *Func) TotalLocals() uint64 {
	var total uint64
	for _, g := range f.Locals {
		total += uint64(g.Count)
	}
	return total
}

// EachLocal yields every local's type in index order — the flat reading, without the flat
// slice.
//
// An iterator rather than a `[]ValType` accessor, and that is the whole point of #138: an
// accessor returning the flattened vector would put the 4 GiB allocation back, one call
// deeper and harder to see. Anything wanting the flat reading gets it a value at a time and
// pays only for what it consumes; a consumer that must materialize slots checks its own
// bound against TotalLocals *first* and then fills them.
func (f *Func) EachLocal(yield func(idx uint32, vt ValType) bool) {
	var idx uint32
	for _, g := range f.Locals {
		for range g.Count {
			if !yield(idx, g.Type) {
				return
			}
			idx++
		}
	}
}

// Instr is one instruction in the internal form.
//
// Fixed-width and immediate-carrying, which is the whole content of 0002's Q1: the
// measurement that decided it (`internal/interp/dispatchbench`, n=10 with benchstat)
// found rewrite **immune to immediate width** (13.30µs vs 13.32µs, inside the bands)
// where in-place pays 14% for multi-byte LEBs — and found the side-table compromise
// *slowest of the three*, because splitting the program across two arrays costs two cache
// lines per instruction.
//
// The `Imm` pair is deliberately not a variant type or an `any`. Every immediate shape the
// decoder reads fits two 64-bit words — an index, a signed constant, a memarg's
// offset-and-flags, a block's arity-and-target — and the opcode says which reading
// applies, exactly as it says which stack effect applies. An interface here would
// allocate per instruction and defeat the measurement that chose this form.
type Instr struct {
	// Op is the opcode, or the sub-opcode for a prefixed instruction. Prefix carries
	// the prefix byte; a single-byte instruction leaves it zero.
	//
	// **A uint32, not a byte, and that was a measurement rather than a preference**
	// (grave #101). The
	// first version of this field was a byte, on the reasonable-sounding ground that an
	// opcode is one — and the 0xfd sub-table reaches **0x113 (275)**, because SIMD has
	// more than 256 instructions. A byte would have truncated `v128.load32_zero` and
	// friends into *different instructions than the module contains*, silently, on valid
	// input: an accept-direction defect no board can see, since every affected vector is
	// one the suite expects to pass. Found by printing the sub-tables' maxima instead of
	// trusting the word "opcode" (`opTableFB` max 0x1e, `opTableFC` 0x11, `opTableFD`
	// **0x113**). TestPrefixedSubOpcodesFitOp is the control, scoped to every row of
	// every region rather than to the one that overflowed.
	Op     uint32
	Prefix byte

	// Imm0 and Imm1 are the pre-decoded immediates, read according to Op. Two words
	// rather than one because no immediate shape the table defines needs three and
	// several need two (memarg, br_table's default plus count, if's two arms).
	Imm0 uint64
	Imm1 uint64
}

// The blocktype encoding used by Instr.Imm0 (and, as of 0018's implementation, Imm1) for the
// four structural arms — block, loop, if, try_table.
//
// A blocktype is three disjoint things — a type index, the empty result type, or a single
// valtype — and the first two still fit Imm0 alone exactly as before: the tags sit above
// 2^32 because a type index is read as `s33` and is non-negative when it *is* an index, so
// no legal index can collide with either tag.
//
// **The valtype case is what changed, and why it now spills into Imm1.** Before 0018, a
// bare-valtype blocktype's single result was a `ValType` byte, and `blockTypeValType |
// uint64(t)` packed it into Imm0's low bits alongside the two tag bits at 33/34 — the whole
// value fit in one word with room to spare. 0018 widens ValType to a three-field struct
// (kind byte, null bool, idx uint32) so that a GC-gated `(ref $t)`/`(ref null $t)` result can
// be represented at all, and a uint32 index plus a kind byte plus a null bit no longer share
// a word with two tag bits above 2^32 — `kindIndexed` (0x80) alone already exceeds a byte's
// nice-round low-order slice, and the index needs the full 32 bits BlockType's tag scheme
// never reserved room for.
//
// **The chosen encoding, verified free rather than assumed:** every opcode whose immediates
// include immBlockType (0x02 block, 0x03 loop, 0x04 if_, 0x1f try_table — optable.go) has no
// other immediate that stages into Imm1; block/loop/try_table's remaining immediates are
// immBlock/immCatchVec, both of which stage zero bits (instr_width_test.go's
// immStagedBits), and if_'s two immBlock arms are its ELSE/END-delimited bodies, also zero
// bits. So Imm1 is free for every structural opcode and this is not an assumption — it is
// what TestBlockTypeImm1IsFreeForStructuralOpcodes checks against the generated table rather
// than against today's four opcodes by name, so a fifth structural arm arriving upstream
// with its own Imm1 use fails loudly here instead of silently colliding.
//
//   - **Imm0** keeps exactly its old three-way tag: a non-negative value is the type-index
//     form; blockTypeEmpty is the empty form; blockTypeValType tags the valtype form, whose
//     *kind byte and null bit* — not the whole ValType — pack into Imm0's low bits (kind in
//     bits 0-7, null in bit 8), because those two fit easily and keeping them beside the tag
//     is one word closer to the values that determine arity without an index lookup.
//   - **Imm1** carries the valtype's `idx`, meaningful only when Imm0 tags the indexed
//     valtype form (kind == kindIndexed) and zero otherwise — including for the type-index
//     and empty forms, where Imm1 is unused and BlockType does not read it.
const (
	// blockTypeEmpty is the `0x40` form: no parameters, no results.
	blockTypeEmpty uint64 = 1 << 33

	// blockTypeValType tags a single-result blocktype; the low nine bits of Imm0 hold the
	// ValType's kind byte and null bit, and Imm1 holds its idx when kind is kindIndexed.
	blockTypeValType uint64 = 1 << 34

	// blockTypeNullBit is the valtype form's nullability, one bit above the eight-bit kind
	// field packed into Imm0 alongside blockTypeValType.
	blockTypeNullBit uint64 = 1 << 8
)

// BlockType reads a structural instruction's `Imm0`/`Imm1` back into the three cases the
// encoding above packs into them.
//
// **An accessor rather than exported constants, because the packing is this package's fact
// and not its consumers'.** The interpreter needs a block's arity — how many values the
// block yields, so `br` can keep exactly that many and discard the rest — and it needs to
// ask without knowing that the tags live at bits 33 and 34 of Imm0, or that a valtype's
// index rides in Imm1. Exporting the constants would put the decoding rule in every consumer
// that reads a blocktype, which is the two-places-know-one-fact shape; a function keeps it
// here, where `decodeBlockTypeValue` writes it, so the writer and the reader cannot drift.
//
// The three returns are disjoint by construction and the caller must branch on them in this
// order — `empty` first, then `valType`, then `typeIdx` — because only the tags distinguish
// a tagged word from an index, and an index of 0 is legal.
//
// **Two-word signature, not one**, since 0018's implementation: a valtype result whose kind
// is the indexed form needs Imm1 for its idx, and BlockType is the one place that packing
// rule may be read back, so it takes both words rather than making every caller learn which
// second word to pass. TestBlockTypeFormsMatchTheReference and TestBlockTypeAlternationIsTheAuthority
// were both updated for the wider call, not rewritten around it.
func BlockType(imm0, imm1 uint64) (typeIdx uint32, valType ValType, empty bool) {
	switch {
	case imm0 == blockTypeEmpty:
		return 0, ValType{}, true
	case imm0&blockTypeValType != 0:
		kind := byte(imm0 & 0xFF)
		null := imm0&blockTypeNullBit != 0
		if kind == kindIndexed {
			return 0, RefType(uint32(imm1), null), false
		}
		return 0, ValType{kind: kind, null: null}, false
	default:
		return uint32(imm0), ValType{}, false
	}
}

// Imm1's three tenants for a memory access, and the reason they are packed rather than
// spread: `Instr` is fixed-width by 0002, a memarg already spends Imm0 on the u64 offset,
// and the eight `v128.loadN_lane` rows carry a third value on top of that.
//
//   - **bits 0-31** — the memory index, `memopIndex`'s result (0 when bit 6 of the flags
//     byte is clear, multi-memory's explicit index otherwise).
//   - **bits 32-39** — the lane index, for the memarg+laneidx rows only, written by
//     stageLaneIdx *after* the memarg has taken both words.
//   - **bits 40-45** — the alignment exponent, `flags & 0x3f`, six bits because the flags
//     byte's `>= 0x80` rejection bounds it at 63.
//
// **The alignment's retention is #306, and the reason it was not retained is worth keeping
// rather than deleting.** The original comment was right that alignment carries no execution
// semantics — it is a validation constraint (`valid.ml:380-389`'s `check_memop`) — and wrong
// about the conclusion, because "only #9 reads it" stopped being a reason not to keep it on
// the day #9 existed. Dropping it made the validator *accept* 54 `assert_invalid` vectors it
// knows how to reject, sixteen of them under-rejections no message-match could see
// (`validateAdmitCeiling`'s note, internal/spec/spec_test.go).
const (
	// memargLaneShift is where stageLaneIdx packs a lane index, above the memory index.
	memargLaneShift = 32

	// memargAlignShift is where decodeMemop packs the alignment exponent, above the lane
	// index so the eight rows that carry both do not contend.
	memargAlignShift = 40

	// memargAlignMask bounds the exponent at the six bits `flags & 0x3f` can produce.
	memargAlignMask = 0x3F
)

// Memarg reads a memory access's two staged words back into the three facts decodeMemop put
// there: the static offset, the memory index, and the alignment exponent.
//
// **An accessor for BlockType's reason, and this packing has the evidence that reason needs.**
// Before #306 the layout had two tenants and four hand-rolled readers, and *two of them
// masked while two did not*: the SIMD lane paths wrote `ins.Imm1&0xFFFFFFFF` because the lane
// index taught them to, while the core load/store path passed the bare `ins.Imm1` as a memory
// index. That was correct only because nothing packed above bit 32 for those rows — a latent
// break that #306's six bits would have made live, turning every `i32.load` with natural
// alignment into memory index `2<<40`. So the packing rule lives here, where decodeMemop
// writes it, and a third tenant cannot silently falsify a consumer's arithmetic.
//
// The lane index is deliberately *not* returned: it is a different immediate (immLaneIdx)
// that happens to share the word, and MemargLane is its own accessor for exactly that reason.
func Memarg(imm0, imm1 uint64) (offset uint64, memIdx, alignExp uint32) {
	return imm0, uint32(imm1), uint32(imm1>>memargAlignShift) & memargAlignMask
}

// StageMemarg composes the word Memarg reads, and it exists so the layout has exactly one
// definition.
//
// **decodeMemop used to compose this inline**, which put the shift and the mask in one file and the
// reader in another — tolerable while nothing else needed to build the word, and not once a second
// producer arrived. `internal/text`'s round-trip expectations are that producer: with the alignment
// retained (#306) a decoded memarg carries a value those rows have to *state*, and stating it as a
// hand-written `2 << 40` would be a second copy of the layout in a file with no reason to know it.
// The Memarg comment directly above records what a second reader of this packing already cost; a
// second writer is the same defect facing the other way.
//
// The exponent is masked here rather than trusted: it comes from six bits of a flags byte at the
// only call site that matters, and a caller passing a wider value would otherwise corrupt whatever
// tenant lands above bit 45.
func StageMemarg(memIdx uint64, alignExp uint32) uint64 {
	return memIdx | uint64(alignExp&memargAlignMask)<<memargAlignShift
}

// MemargLane reads the lane index stageLaneIdx packs above a memarg's memory index.
//
// Separate from Memarg because the two are separate immediates: every memory access has a
// memarg, and only the eight `v128.loadN_lane`/`v128.storeN_lane` rows add a lane. A single
// accessor returning both would hand every caller a field that is zero-and-meaningless for
// 37 of the 45 rows, which is the shape that gets read anyway.
func MemargLane(imm1 uint64) uint8 { return uint8(imm1 >> memargLaneShift) }

// Global is one global: its type, mutability, and initializer.
type Global struct {
	Type    ValType
	Mutable bool

	// Init is the constant expression, in the same internal form as a function body.
	// One form for both, because the reference's `const` production *is* the full
	// instruction grammar (decode.ml:983) — const-ness is a validation fact, which is
	// why `ErrConstExprRequired` is a declared layering debt rather than a grammar rule.
	Init []Instr
}

// Tag is one tag declaration — the tag section's element, `tagtype = TagT of typeuse`
// (`types.ml:40`, `decode.ml:288-290`). No mutability, no init expression: a tag names a
// function type and nothing else, the fixed zero byte ahead of the type index (`zero s` in
// `tagtype`, decode.ml:288) being a reserved attribute byte the reference itself never reads
// back — `tag.ml`'s `Tag.alloc ty = {ty}` takes only the resolved type.
type Tag struct {
	// TypeIndex names the tag's function type in the module's type index space — the params
	// are the exception's payload shape, and the result type is required empty ("non-empty
	// tag result type" is #9's own already-cited rejection, `tag.wast:20-26`).
	TypeIndex uint32
}

// Import is one import: its two names and what kind of thing it brings in.
type Import struct {
	Module string
	Name   string
	Kind   ExternKind

	// Index is the type index for a function import. The other kinds carry their
	// descriptor in the module's own index spaces, which the decoder appends to as it
	// reads them — an imported function occupies function index 0 before any defined
	// function does, and that ordering is the validator's and the interpreter's to rely
	// on.
	Index uint32

	// Table, Memory, GlobalType and GlobalMutable carry the descriptor for kinds
	// 0x01-0x03 — exactly the fields `decodeTableForm`/`decodeLimits`/`decodeGlobalType`
	// already read and used to discard (#164). Only the fields matching Kind are
	// meaningful; the rest are zero.
	//
	// A func import needs no such field because its descriptor *is* a type index into the
	// module's own type space, already carried in Index — the linker resolves it through
	// Module.Types like any other type-indexed use, rather than duplicating the functype
	// here.
	Table         Table
	Memory        Memory
	GlobalType    ValType
	GlobalMutable bool
}

// Export is one export: a name and what it names.
type Export struct {
	Name  string
	Kind  ExternKind
	Index uint32
}

// ExternKind is the kind byte shared by imports and exports.
type ExternKind byte

const (
	ExternFunc   ExternKind = 0x00
	ExternTable  ExternKind = 0x01
	ExternMemory ExternKind = 0x02
	ExternGlobal ExternKind = 0x03
	ExternTag    ExternKind = 0x04
)

func (k ExternKind) String() string {
	switch k {
	case ExternFunc:
		return "func"
	case ExternTable:
		return "table"
	case ExternMemory:
		return "memory"
	case ExternGlobal:
		return "global"
	case ExternTag:
		return "tag"
	}
	return "unknown"
}

// Limits is a table's or memory's size bounds.
//
// Min and Max are 64-bit, and that is the suite's ruling rather than future-proofing:
// `binary-leb128.wast:525` is a memory32 whose min is a ten-byte LEB with unused bits set
// and wants `integer too large`, which only a 64-bit read reports (grave #36). A memory32
// limit above 2^32 therefore *decodes*, and rejecting it is #9's job.
type Limits struct {
	Min    uint64
	Max    uint64
	HasMax bool

	// Addr64 is set when the flags byte selected the memory64 address type (the 0x04-0x07
	// range), making this memory's or table's addresses i64 rather than i32.
	//
	// **On Limits rather than on Memory, because it is read from the limits flags** — the
	// same byte HasMax comes from — and because table64 will want the identical field from
	// the identical position.
	//
	// Retained as of 0015 because it governs the **size** limit, not the effective-address
	// computation. `memory.ml:27`'s `valid_size` caps an i32 memory at `0xffff` pages and an
	// i64 memory at nothing, and both `alloc` and `grow` consult it — so the width decides
	// which allocations and which `memory.grow` deltas are legal.
	//
	// It is deliberately *not* consulted when computing an address, and the first draft of
	// this comment said the opposite: `value.ml:292` **zero-extends** an i32 index to 64
	// bits (`extend_i32_u`) and `effective_address` then adds the static offset in 64 bits
	// with an unsigned-overflow check. There is no 32-bit wrapping at any width, so an
	// address path that branched on this field would be inventing a distinction the
	// reference does not make. Read from the executable rather than from the word
	// "memory64" — comments and ADRs are testimony, and the executable outranks.
	//
	// It cannot be recovered later: the flags are gone, and `Min > 1<<32` is the wrong
	// question because a memory64 of one page is still 64-bit addressed.
	Addr64 bool
}

// Table is one table: its element type and limits.
type Table struct {
	ElemType ValType
	Limits   Limits
}

// Memory is one memory: its limits, which carry its address type (see Limits.Addr64).
type Memory struct {
	Limits Limits
}

// ElemMode is an element segment's mode: the three arms of the reference's `segmentmode`
// (ast.ml:305-308), which the wire's eight flag values select among.
//
// Three arms and not a `Passive bool`, which is where DataSegment stops. A data segment has
// two modes because the reference's `Declarative` arm is an *error* for data
// (`illegal declarative data segment`); for elements it is mode 3 and mode 7, and the text
// grammar has an arm for it (parser.mly:1167, elem.wast:573). So the shape that was adequate
// next door is not adequate here, which is why this is not a copy of it.
type ElemMode byte

const (
	// ElemActive is the zero value deliberately: wire flags 0 is active-with-implicit-table,
	// so a segment whose mode nothing wrote reads as the mode the smallest encoding means.
	ElemActive ElemMode = iota

	// ElemPassive is flags 1 and 5: not copied at instantiation, reachable only through
	// `table.init`.
	ElemPassive

	// ElemDeclarative is flags 3 and 7: not copied and not reachable, existing only so that
	// `ref.func` may name the functions in it. Its elements are still retained, because
	// forward-declaring *which* functions is the segment's entire content.
	ElemDeclarative
)

func (m ElemMode) String() string {
	switch m {
	case ElemActive:
		return "active"
	case ElemPassive:
		return "passive"
	case ElemDeclarative:
		return "declarative"
	}
	return "unknown"
}

// ElemSegment is one element segment: where it goes, what type its elements are, and the
// elements themselves in whichever of the two forms the wire used.
//
// **Retained under 0016's rule that shape follows the wire form, not the consumer**, and the
// place that bites is `ByExpr`. The reference does *not* keep the two element forms apart: it
// normalizes a function index into a one-instruction `[ref_func x]` const-expr
// (decode.ml:1150-1152) and recovers the distinction at encode time by asking whether every
// expression has that shape (encode.ml:1052-1054). That recovery is **not available here**, and
// the reason is measured rather than assumed: it turns on the reftype's *nullability* —
// `is_elem_kind` accepts `(NoNull, FuncHT)` and rejects `(Null, FuncHT)` — and this engine's
// ValType is a byte with no nullability bit, so both collapse to FuncRef.
//
// The suite has the pair that makes that concrete, and it annotates it itself:
// elem.wast:259 is `\00\41\00\0b\01\00` commented `(i32.const 0) func 0`, and elem.wast:327 is
// `\04\41\00\0b\01\d2\00\0b` commented `(i32.const 0) (ref.func 0)` — the same active-at-table-0
// segment holding the same single function, once as an index and once as an expression.
// Normalized, they are one value and an encoder must guess which bytes to write back. Kept
// apart, the round trip is exact. Same property as LocalGroup keeping `(count, type)`
// runs — flattening cannot distinguish one run of two from two runs of one.
type ElemSegment struct {
	// Mode is which of the three segment modes this is; TableIndex and Offset are
	// meaningful only for ElemActive.
	Mode ElemMode

	// TableIndex is the table the segment initializes, 0 for the implicit-index modes.
	// **Recorded, not checked** — whether it names a table the module has is #9's question,
	// for the reason DataSegment.MemIndex gives.
	TableIndex uint32

	// Offset is the offset constant expression, in the same internal form as a function body.
	// Nil unless Mode is ElemActive.
	Offset []Instr

	// ElemType is the element type: FuncRef for the elemkind forms, whatever the reftype
	// says for the expression forms — every reftype resolves to a real ValType as of 0018's
	// implementation, including the ten further GC abstract forms and the indexed form the
	// all-gates-on lane reaches; there is no longer a form decodeRefType declines to
	// represent here.
	ElemType ValType

	// ByExpr distinguishes the two element encodings — wire bit 2. It is a field rather than
	// a derived predicate because both slices are empty for a zero-element segment, which is
	// legal in either form, and `len(Exprs) > 0` would then answer "index form" for a
	// perfectly good empty expression segment. Same distinction 0016's LabelVector keeps with
	// its second result: absent and empty are different facts.
	ByExpr bool

	// Funcs is the element vector in function-index form; nil when ByExpr.
	Funcs []uint32

	// Exprs is the element vector in const-expr form; nil unless ByExpr.
	Exprs [][]Instr
}

// DataSegment is one data segment: where it goes, and what goes there.
//
// **Retained as of 0015**, which is the consumer-forced retention pre-registered when the wat
// encoder's round-trip witness was found blind: `decodeDataSegment` was error-only, so nothing
// in the codebase could represent a module's data, so the only available witness was
// byte-level over `Section.Payload`. #7 executing memory tests is the consumer that knocked.
type DataSegment struct {
	// Passive is set for the 0x01 mode: the segment is not copied at instantiation and is
	// only reachable through `memory.init`. Active segments (modes 0x00 and 0x02) are
	// copied at time zero and may trap doing it.
	Passive bool

	// MemIndex is the memory the segment initializes, 0 for the implicit-index mode.
	// Meaningless when Passive.
	MemIndex uint32

	// Offset is the offset constant expression, in the same internal form as a function
	// body — one form for both, for Global.Init's reason. Nil when Passive.
	Offset []Instr

	// Init is the segment's bytes, aliasing the decoder's image (the in-place posture).
	// Never nil for a segment that decoded, and empty is legal: `(data "")` is a real
	// module in memory64.wast.
	Init []byte
}

// OpMnemonic returns the reference's constructor name for a single-byte opcode, and whether the
// table has a row for it.
//
// **Exported so that a consumer's hand-written table can be cross-checked against this one**,
// which decision 0014 already made legitimate by promoting the mnemonic from "a label" to a
// fact. The consumer it exists for is `internal/interp`'s `memops`: the load/store family's
// width, signedness, and slot type are all recoverable from `i64_load16_s`, so a control can
// parse the name and compare, rather than trusting 23 hand-written rows whose errors would be
// accept-direction and invisible on the board (§9 G-3).
//
// Single-byte only, because that is the region the consumer needs; a prefixed accessor is worth
// adding when something asks, not before. **Something asked** — see PrefixedOp below, added for
// `internal/interp`'s struct arms rather than in anticipation of them.
func OpMnemonic(op uint32) (string, bool) {
	info, ok := opTable[op]
	if !ok {
		return "", false
	}
	return info.mnemonic, true
}

// HasMemarg reports whether an instruction's immediates include a memarg — that is, whether
// Memarg may be read back from its two words. `prefix` is 0 for a single-byte opcode.
//
// **Exported so a consumer's rule can have a *derived* domain rather than a guessed one.** The
// alignment constraint (`valid.ml:380-389`) applies to exactly the rows carrying `immMemop`, and
// that set lives in the generated table — 45 rows at the pinned revision, 23 core and 22 vector.
// A consumer inferring it from the mnemonic instead would be asserting that "load"/"store" in a
// name means "carries a memarg", which is a claim about the naming of every proposal not yet
// merged upstream; `memory.copy` and `table.get` are already counterexamples in one direction,
// and an atomic access arriving with a memarg would be one in the other. So the question is
// answered by the table that knows, which is also what lets `internal/validate`'s width table be
// checked against this predicate in both directions instead of against its own key set.
func HasMemarg(prefix byte, op uint32) bool {
	var tab map[uint32]opInfo
	switch prefix {
	case 0:
		tab = opTable
	case 0xfb:
		tab = opTableFB
	case 0xfc:
		tab = opTableFC
	case 0xfd:
		tab = opTableFD
	default:
		return false
	}
	info, found := tab[op]
	if !found {
		return false
	}
	for _, im := range info.imms {
		if im == immMemop {
			return true
		}
	}
	return false
}

// PrefixedOp returns a prefixed sub-opcode's mnemonic and immediate count, and whether the region's
// table has a row for it. OpMnemonic's prefixed twin, added on the condition that comment set: a
// consumer asked.
//
// The consumer is `internal/interp`'s 0xfb arms, and the hazard it needs checking is sharper than the
// single-byte case's. `fb 02`/`fb 03`/`fb 04` are `struct_get`/`struct_get_s`/`struct_get_u` —
// adjacent bytes differing only in a signedness the interpreter dispatches to three separate arms —
// and `fb 00`/`fb 01` are `struct_new`/`struct_new_default`, which are *indistinguishable in
// behaviour* on a struct whose fields are all zero. A hand-written constant naming the wrong one of
// either pair produces a module that decodes perfectly, so the error is accept-direction and the
// board scores it green wherever the values coincide (§9 G-3).
//
// **Both facts in one call, because a constant carries two.** The mnemonic alone would pass a
// regenerated table that renumbered a region while keeping names; the immediate count catches that,
// `fb 00`/`fb 01` taking one immediate where `fb 02`-`fb 05` take two. Returning them together is
// what stops a consumer from checking the cheap half and calling it agreement.
//
// An unknown *prefix* and an unknown sub-opcode are one `false` deliberately: the caller's question
// is "does the table have this instruction", and a consumer that cared which half was missing would
// be reimplementing the decoder's own escape/illegal/absent distinction (see opInfo.escape) from
// outside.
func PrefixedOp(prefix byte, op uint32) (mnemonic string, imms int, ok bool) {
	var tab map[uint32]opInfo
	switch prefix {
	case 0xfb:
		tab = opTableFB
	case 0xfc:
		tab = opTableFC
	case 0xfd:
		tab = opTableFD
	default:
		return "", 0, false
	}
	info, found := tab[op]
	if !found {
		return "", 0, false
	}
	return info.mnemonic, len(info.imms), true
}
