# 0021 — A field type is a value type or a packed width, plus mutability

Date: 2026-08-08 · Status: **proposed** — no stamp exists yet

> Held open per the ruling on #142. Filed against #183's measurement: the largest remaining
> GC-attributable frontier (126 vectors, 110-of-126 sole-mechanism) is `CompType.Fields`
> retention, and both 0018 and 0019 named this exact decision as a deferred companion —
> 0018's own consequences section calls it "a companion representation decision at the
> *type-definition* level, not the *value-slot* level"; 0020's `gcObj.typ` cites
> `*binary.CompType` "on the assumption that decision lands with a `Fields []FieldType` (or
> equivalent) the decoder retains." The option below is offered for Scott's stamp and has
> not received one.

Filed against **#183** (the co-blocking probe that measured this frontier) and milestone
**v0.2.0 GC gate**.

## Question

`decodeCompType`'s struct and array branches (`internal/binary/sections.go:464-486`) already
read the full grammar — `decodeVec(r, d.decodeFieldType)` for a struct, one `decodeFieldType`
for an array — gated correctly behind `d.Features.GC`, and discard the answer: `CompType{Kind:
CompStruct}` / `CompType{Kind: CompArray}` with no field content, exactly `ValType`'s pre-0018
shape (validate, then throw away). `decodeFieldType` reads a storage type then a mutability
bit and returns only an error; `decodeStorageType` reads a `valtype` or one of two packed
forms (`i8`/`i16`) and does the same. The text parser (`internal/text/types.go`'s
`structtype`/`arraytype`/`fieldtype`/`storagetype`) has the identical gap — grammar and
gating complete, nothing retained, `compType`'s own comment stating the reason as design
("a struct or array type is a `non-function type` to every consumer here, and its fields are
never compared").

Decide what a field's type *is*, on the same terms 0018 decided what a value's type is,
before `CompType` grows a `Fields` slice and before `struct.new`/`struct.get`/`array.new`/
`array.get` get interpreter arms (0020's own stated blocker).

## What the corpus's own shape requires

Measured directly against the vendored suite rather than assumed:

- **Mutability is real and exercised**: `(field (mut i8))`, `(field (mut i16))`, and
  `(mut $t)`-wrapped `ValType` fields all appear in `struct.wast`/`array.wast`. `array.set`'s
  whole existence depends on a field being mutable; a field type that dropped mutability
  would make half the array/struct instruction family inexpressible.
- **Packed storage is real and exercised**: `i8`/`i16` fields appear standalone
  (`(field i8)`) and mixed into multi-field lists alongside full value types
  (`(field i8 i8 i8 i8)`, `(field i8 i16 i32 i64 f32 f64 anyref funcref (ref 0))`) — packed
  and unpacked storage are co-equal populations in the same struct, not a rare case beside a
  common one.
- **Named and anonymous fields both occur** (`structtype`'s two grammar arms,
  `internal/text/types.go:286-331`), but naming is a **text-side-only** fact — the wire
  format has no field name, per `decode.ml`'s `fieldtype` production carrying no identifier —
  so it is out of this ADR's scope entirely (see below).

The reference's own `storagetype` production (`decode.ml:236-241`) is exactly two shapes: a
full `valtype`, or a `packtype` (`i8`/`i16`, two forms only — the corpus never needs a third).
A field type is a storage type plus one mutability bit (`decode.ml:243-246`).

## What already exists, and why it is the authority

**0020 already anticipated this decision's shape**, without deciding it: `gcField` (the
per-slot representation for an allocated struct/array instance, `internal/interp`, not yet
built) is `{num uint64; r ref; packed bool; packBits byte}` — the identical two-way split
(packed-or-not, and a width when packed) this ADR needs for the *type-definition* level.
0020's own comment states the reason: "unpacking on every read and re-packing on every write
is the reference's own behavior for these two storage kinds and not a detail this engine gets
to skip" (`runtime/aggr.ml`'s `ValField`/`PackField`). A `FieldType` that mirrors this shape at
the type level is not a new design — it is reading a sibling this codebase already committed
to for the value level, per the read-the-sibling-for-shape rule 0018 used against
`resolvedVal`.

## Options

- **A — reuse `ValType` with an extended sentinel for packed storage (rejected).** Would
  require `ValType.kind` to grow two more byte values (`i8`/`i16`) that mean something
  different from every other kind it holds — a "value type" that sometimes describes a
  narrower storage width than the value it holds, which corrupts the one invariant 0018
  built `ValType` on (`kind` is the wire byte, and the wire byte *is* the type's identity).
  `i8`/`i16` are not value types at all — a struct field holding `i8` unpacks to an `i32` on
  every read (`Aggr.read_field`, `type_of_ref`), so the packed width is a **storage
  optimization** with no corresponding `ValType`, not a fifteenth valtype.

- **B — a tagged union / `any`-shaped storage value (rejected).** Same rejection 0018 already
  gave option B for the identical reason, restated rather than re-argued: 0002's own
  measurement killed a boxed representation on 0002's dispatch benchmark, and a field's
  storage type is read on every `struct.get`/`array.get`/`struct.set`/`array.set` — as hot a
  path as a stack slot's type, not a cooler one.

- **C — a small fixed struct, storage type plus mutability, chosen.**

  ```go
  // StorageType is a fieldtype's storage: either a full ValType, or one of the two packed
  // widths (i8/i16) the reference's packtype production admits — decode.ml:236-241, exactly
  // two forms, never a third.
  type StorageType struct {
      Val    ValType // meaningful when !Packed
      Packed bool
      Width  byte // 8 or 16, meaningful only when Packed
  }

  // FieldType is one struct or array field: its storage and whether it may be written after
  // allocation.
  type FieldType struct {
      Storage StorageType
      Mutable bool
  }
  ```

  `CompType` grows `Fields []FieldType` (struct: one per declared field, in declaration
  order; array: exactly one, per the grammar's own arity). Field *names* are not retained —
  see below.

## What this does not decide

- **Field names.** `structtype`'s bindidx arm (`internal/text/types.go:299-331`) names a
  field for use elsewhere in the *same type definition's source* (a forward reference within
  one `(type (struct (field $x i32) (field $y (ref $x))))`, if the grammar admits it — it
  does not currently need to, since a field's own type cannot name a sibling field), but the
  binary format carries no field name at all (`decode.ml`'s `fieldtype` has no identifier
  slot) — a decoded module has nothing to retain here regardless of this ADR's shape. This is
  0016's own precedent (`LocalGroup` keeps counts, not the text side's per-local names) applied
  a second time: the wire form is the shape authority, and the wire form has no name to keep.
- **Whether `struct.get`/`array.get`'s packed-field unpacking reads `Storage.Val`'s default
  sign/zero-extension rule from `StorageType` directly, or from a table keyed by `Width`** —
  an interpreter-arm implementation detail, downstream of this ADR and not decided by it.
- **`struct.new`/`array.new`'s arity check** (how many operands a `struct.new` with N fields
  pops) — a direct consequence of `len(CompType.Fields)` once this lands, not a decision.
- **Any change to `gcObj`/`gcField`'s own shape** (decision 0020, accepted). This ADR supplies
  the *type-definition*-level fact `gcObj.typ.Fields` will read; 0020's `gcField` at the
  *value* level is unaffected and unifies with this ADR's `StorageType`'s packed/width split
  by construction (both read the identical two-way distinction).
- **The `struct.get`/`struct.get_s`/`array.get`-family instruction-immediate encoding capacity**
  (`internal/text`'s `immIdxIdx` shape, #172's item 5) — a separate, already-named frontier
  (#183's own finding: `struct.wast`'s fails sit entirely behind *that* blocker, not this one,
  a two-blocker chain rather than a shared re-key). This ADR's implementation is not expected
  to convert `struct.wast`'s own vectors; it converts the 126 the co-blocking probe measured
  under the "fields are not retained" key specifically.

## Consequences

- **`decodeFieldType`/`decodeStorageType` (`internal/binary/sections.go`) stop discarding
  their reads.** `decodeCompType`'s struct and array branches accumulate `[]FieldType` and
  attach it to the `CompType` they append, instead of the bare `CompType{Kind: ...}` written
  today.
- **`internal/text`'s encoder gains the identical capability on its own side** — `compType`
  (`internal/text/typetable.go`) retains `[]resolvedField`-shaped content (or equivalent) for
  a struct/array definition, closing `encodableOrErr`'s current refusal
  ("cannot yet encode type %d: a struct or array type's fields are not retained"), the same
  two-sided implementation shape 0018 took (decoder PR, then encoder PR).
- **`gcObj.typ.Fields`'s exact type is `*binary.CompType`'s `Fields []FieldType`**, discharging
  0020's own forward reference and 0018's deferral, both citing this exact decision.
- **No change to gating.** GC's gate (`d.Features.GC` checks in `decodeCompType`) is
  unaffected — this is a representation decision, not a gate decision, per 0018's own
  precedent for the identical class of change.
