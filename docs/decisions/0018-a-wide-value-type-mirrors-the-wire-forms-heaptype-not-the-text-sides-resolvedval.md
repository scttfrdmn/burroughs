# 0018 — A wide value type mirrors the wire form's `heaptype`, not the text side's `resolvedVal`

Date: 2026-08-08 · Status: **proposed** — no stamp exists yet

> Held open per the ruling on #142: *a status field is a citation to an approval, and approvals
> are artifacts with provenance.* Scott ordered this ADR authored (PR #171's report, "author the
> value-type ADR") and has not ruled on the option; the two are different acts and only one of
> them has happened.

Filed against **#172** (the GC-gate recon) and milestone **v0.2.0 GC gate**. Per *one ADR earns
one implementation*, this is the first of the two ADRs the recon named as forced — this one, then
subtyping's execution-time shape — before any implementation PR is sized.

## Question

`binary.ValType` is a byte (`module.go:56`), by original design: the twelve GC reference forms
"do not fit a byte," and the comment at `module.go:50-55` states plainly that widening is
deferred until a gate forces it rather than guessed at now — the premature-generality trap 0006
names. #172 measured the force: **928 of 1135 board fails (81.8%)** are downstream of this one
representation gap, on both the encode side (`valTypeByte` returning `(0, false)` for any
GC-shaped value, 881 vectors) and the linker side (comparing a GC-shaped import descriptor, 47
vectors).

`ValType` is not a type in one place. It is `FuncType.Params`/`.Results`, `CompType.Func`,
`Global.Type`, `Table.ElemType`, `binary.Import`'s `GlobalType` (#164), and — as of this
session — `interp.ref`'s companion field set (#163 added `Inst *Instance` to the *same* struct
this ADR's consumer, `interp`, will need to widen a second time). Every one of those sites
currently branches on `IsRef()` — `t == FuncRef || t == ExternRef` — to decide which of the two
parallel stacks (0002's pinned split) a value lives in. Widening `ValType` means every one of
those sites keeps working for the two Wasm 2.0 forms and starts correctly recognizing twelve
more, without silently reinterpreting a byte that used to mean something else.

Decide the representation before the encoder, decoder, or interpreter grows a second widening
pass to fix the first one's shape.

## What already exists, and why it is the authority

**The text encoder already writes the wire form of `heaptype` completely.** `heapTypeBytes`
(`text/encode.go`) emits the twelve absolute forms via `absoluteHeaptypeBytes` — one map, twelve
`keywordKind → byte` entries, `0x6e`/`0x6d`/`0x6c`/… down to `0x69`, machine-checked against
`encode.ml` by `TestAbsoluteHeaptypeBytesAgreeWithEncodeML` — and the indexed form as a proper
`s33 typeuse` (`typeuseIdxBytes`), including forward-reference resolution through the deferred
phase (`heaptypeRetained`). This is finished work, not a sketch: `(ref.null $t)` naming a type
declared later in the same module already round-trips correctly.

**The decoder already validates the identical production and discards the answer.**
`decodeHeapType` (`sections.go:704-739`) is the same `either` alternation — a type index (`sleb`
at width 33, checked non-negative before the gate, per `TestHeapTypeGatesFormsNotThePosition`) or
one of the twelve abstract forms — gated exactly where decision 0008 puts the boundary (the two
Wasm 2.0 forms ungated, the other ten and the parameterized prefixes behind `gateGC`). Every
accepting path writes `d.valType = NoValType` and returns.

**The text side's own intermediate representation is already wide**, and it is the shape this
ADR is deciding *whether to promote*, not inventing: `resolvedVal` (`text/typetable.go:487`) is

```go
type resolvedVal struct {
	num   string      // number/vector type, by spelling
	null  bool        // nullability
	abs   keywordKind // which abstract heaptype, or the func/extern base
	idx   uint32       // resolved type index, when isIdx
	isIdx bool
}
```

So the wire-form authority (what the encoder writes, byte for byte) and the richest existing
in-memory shape (what the parser already resolves) point at the same five facts: **is this a
number/vector type, or a reference; if a reference, is it nullable; which of the fourteen
heaptype forms; and if the indexed form, which index.** Neither `internal/binary` nor
`internal/interp` has anything like this yet — `NoValType` is the entire footprint.

## The corpus's own shape, measured rather than assumed

Grepped directly against the vendored suite (file-count, not vector-count, since a single file's
module may use a form many times):

| form | files |
|---|---|
| indexed, non-null (`(ref $t)`) | 29 |
| indexed, nullable (`(ref null $t)`) | 27 |
| `funcref`/`(ref null func)` | 79 |
| `externref` | 31 |
| `anyref` | 10 |
| `arrayref` | 8 |
| `i31ref`/`structref` | 4 each |
| `eqref`/`nullref` | 3-4 each |
| `exnref`, `nullfuncref`, `nullexternref` | 1-3 each |

**No single form dominates enough to special-case.** The indexed forms are used about as widely
as the abstract-kind forms in aggregate, which argues against a design that treats the index as
a rare extension of an otherwise-enum type — index and abstract-kind are co-equal populations,
matching `resolvedVal`'s own flat field list rather than a tagged-variant shape that privileges
one over the other.

## Options

- **A — grow `ValType` in place: still a byte for the two Wasm 2.0 forms and every number/vector
  type, plus a second struct carrying the GC cases, joined by a sentinel.** Rejected. This is
  what `NoValType` already is, minus the second struct — the sentinel exists today specifically
  because there is nowhere to put the answer. Growing it "in place" while every call site still
  branches on a single byte reproduces the current bug shape (a value whose real type has been
  discarded) one layer later, for every GC form instead of all of them at once. Not a
  representation change, a bigger sentinel.

- **B — a tagged union / `any`-shaped value type.** Rejected on 0002's own precedent, restated
  rather than re-argued: `Instr`'s immediates were measured against exactly this shape
  (interface/variant vs. fixed fields) and a fixed form won because Go's `any` allocates and an
  interface defeats the escape analysis 0002's benchmark depends on. `ValType` is read on every
  stack push, every local access, every global get/set — hotter than `Instr`'s immediates, not
  cooler — so the case against a boxed representation is at least as strong here as it was there.

- **C — widen `ValType` to a small fixed struct mirroring `resolvedVal`'s fields, sized to the
  wire form's own cardinality (chosen).**

  ```go
  type ValType struct {
      kind byte    // one of: number/vector (the existing 5 bytes), or a heaptype tag
      null bool     // nullability, meaningful only when kind names a reference
      idx  uint32   // resolved type index, meaningful only when kind is the indexed form
  }
  ```

  Three fields, not five — `resolvedVal.num` and `resolvedVal.abs` collapse into one `kind` byte
  because `internal/binary` already has a byte-per-numeric-type convention (`I32`/`I64`/`F32`/
  `F64`/`V128`) that the twelve heaptype forms can extend without colliding (the wire form itself
  proves this: numeric types and heaptypes are already disjoint byte ranges in the LEB encoding
  `decodeValType`/`decodeRefType` read). `isIdx` collapses into `kind` too — the indexed form is
  its own tag, not a bit beside twelve others.

  This is `resolvedVal` read for its *shape* and re-derived at `internal/binary`'s own
  cardinality, not copied wholesale — the read-the-sibling rule's stated form ("read it to learn
  the shape, not to borrow its numbers"), and the reason it isn't literally `resolvedVal` moved
  into `binary` is that `resolvedVal.num` is a *string*, chosen for the text side's own reason
  (the lexer collapses four numeric spellings to one class and keeps the payload as text); the
  wire side has no text to keep, so its equivalent field is the byte the format already uses.

  Backward-compatible by construction: the five existing byte constants (`I32` etc.) become
  `ValType{kind: 0x7F}` and so on, so every existing comparison (`t == I32`) needs `ValType` to
  stay comparable with `==` — which a three-field struct of byte/bool/uint32 is, satisfying
  `resolvedVal`'s own stated reason for existing ("comparable with ==, which is the whole point
  of having a second representation").

## Recommendation: C

The wire form is the shape authority, per the `LocalGroup`/`Labels`/`Datas` precedent this
project's decoder-side retention decisions already follow (0016, 0015): retention is forced by a
consumer but *shaped* by the grammar, never by the first consumer's convenience. `heaptype`'s own
grammar is fourteen forms sharing three facts — is it null, which form, which index if indexed —
and C is that grammar read directly into a struct, the same move 0016 made for `br_table`'s label
vector and 0015 made for `Func.Locals`' run-length form.

**This is also the second widening of the same struct family in one milestone, which is the
argument for getting the shape right once rather than incrementally.** #163 (0017 Q2) added
`Inst *Instance` to `interp.ref` two weeks before this ADR; if `ValType` widens to carry a
resolved heaptype identity, `ref`'s own `Addr uint32` — currently a bare function index — is the
natural place a GC object's identity (once structs/arrays are live values, not just declared
types) will want to live beside `Inst`. This ADR does not decide that consequence — it is a
different struct, a different consumer, and a different PR — but it names the direction so a
reader sizing the next `ref` widening is not surprised that this is the second time in one
milestone the same struct grew for the same underlying reason (a value that used to fit two
words no longer does).

**The GC-precision pin is unaffected.** 0002's parallel-array requirement is about *pointers* —
a Go pointer inside a `uint64` is invisible to the collector — and none of `ValType`'s three
fields is a pointer; `idx` is a plain index into the module's own type space, exactly as
`Func.TypeIndex` already is. `ValType` describes a slot's *type*; it does not become the thing
the collector needs to trace. That remains `ref.Inst` and whatever `ref` grows next for object
identity, unchanged by this decision.

## What this does not decide

- **Subtyping.** Whether `ref.test`/`ref.cast`/`br_on_cast` compute a subtype relation at every
  use or consult a table built once at instantiation is #172's second forced question, its own
  ADR, and it is downstream of this one — a subtype relation has to be expressed over *something*,
  and this ADR is what that something is.
- **Struct/array field retention.** `CompType` growing `Fields []FieldType` (or equivalent) is a
  companion representation decision at the *type-definition* level, not the *value-slot* level
  this ADR covers. `FieldType`'s own storage type (a GC value type, or a packed i8/i16) will want
  this same `ValType`, but the struct/array container shape is a separate question with its own
  corpus shape to measure.
- **Where the interpreter's dispatch grows arms for `ref.test`/`ref.cast`/`br_on_cast`/`array.*`/
  `struct.*`.** Pure consequence of this ADR landing plus the subtyping ADR; not itself a
  decision.
- **Any change to the encodable-shapes / `ambiguousOpcodes` machinery** `text/code.go` already
  has for deferred immediates (`immPatch`'s single-thunk-per-instruction limit, #172's item 5).
  That is an encoder-internal capacity question, not a value-representation one, and the recon
  already named it as its own PR-sizing concern under the co-blocking probe.

## Consequences

- **Every `ValType` comparison site keeps compiling and keeps meaning the same thing for the two
  Wasm 2.0 forms and the five numeric/vector types**, because those become fixed-`kind`,
  zero-`null`/zero-`idx` values indistinguishable in practice from the current byte constants.
  `IsRef()` widens from `t.kind == FuncRef.kind || t.kind == ExternRef.kind` to a range check over
  the heaptype kind bytes — one function, one place, per the existing convention.
- **The decoder's accepting paths in `decodeRefType`/`decodeHeapType` stop writing `NoValType`**
  and start writing the resolved `ValType`, which is this ADR's whole point but is *not* this
  ADR's implementation — a follow-up PR, sized after this lands, per *one ADR earns one
  implementation*.
- **`valTypeByte`'s refusal sites (six of them, per #172's survey) collapse to one function that
  cannot fail** for any form the parser already resolves, once `internal/text` is updated to
  build this `ValType` instead of returning `(0, false)`. That update is #172's own encoder-side
  implementation PR, downstream of this ADR and the decoder-side PR both.
- **This is a representation decision, not a gate decision.** GC's gate (`decodeRefType`'s
  `d.Features.GC` checks) is unaffected — a widened `ValType` still round-trips through the same
  gate checks at the same sites; this ADR changes what gets *written* on the accepting path, not
  which modules are accepted.
