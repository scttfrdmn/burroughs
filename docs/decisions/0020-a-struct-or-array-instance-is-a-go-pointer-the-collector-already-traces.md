# 0020 — A struct or array instance is a Go pointer the collector already traces

Date: 2026-08-08 · Status: **accepted** — option C, stamped by Scott on PR #177; **body amended by
append** (PR #247's ratified departure), so read the append before relying on the `gcField` sketch
below

> Held at `proposed` while it had no stamp, per the ruling on #142. Ordered by Scott on 0019's
> acceptance (PR #176): heap-object representation earns its own document now, on the 0017
> authoring bar — a measured, real consumer (zero `0xfb` dispatch today, `struct.new` waiting on
> `CompType.Fields`) and a choice that propagates into 0002's GC-traceability pin, the value model,
> and every struct/array arm to come.
>
> The stamp names why option C is the right one on this project's own terms rather than in the
> abstract: it services the GC-traceability pin 0002 already pinned, rather than reopening it —
> `Obj *gcObj` is a fourth field the collector traces exactly as it already traces `Inst
> *Instance`, no new array, no new exemption. `ref.eq` as bare pointer comparison is not a
> shortcut taken for convenience; it is the reference's own default (`eq_ref' = ref (==)`,
> unoverridden in `aggr.ml`) *earning* the cheap implementation rather than the cheap
> implementation getting lucky — the citation is what makes the free performance trustworthy
> instead of coincidental. And both rejections hold on precedent already spent elsewhere in this
> campaign: option A repeats `ref.Inst`'s own reason for rejecting an index-into-owner shape for
> funcrefs, and option B repeats 0017 Q2's correctness rejection of copy-on-read — neither needed
> a fresh argument because the shape of the mistake was already on file.
>
> **Four for four.** Value type (0018), subtyping (0019), heap objects (0020), and probe
> discipline (the co-blocking check run ahead of every candidate implementation PR, standing since
> the recon) are the campaign's whole decision phase, closed with this stamp. The 928-vector
> figure the recon opened with is no longer a design question sitting on top of a to-do list; it is
> the to-do list, now with a constitution to build against. The ladder is implementation from here.

Filed against **#172** (the GC-gate recon) and milestone **v0.2.0 GC gate**, the fourth decision
in the sequence 0018/0019 named but did not resolve: `ValType` (0018) supplies the type a runtime
value's identity is checked against; the subtyping relation (0019) is what checks it; this ADR
supplies the *value* — what a struct or array instance actually is, once `struct.new`/`array.new`
exist as executable opcodes.

## Question

`interp` has no `0xfb`-prefix dispatch at all today — confirmed by direct search, not inferred —
and the reference's own runtime representation for a struct or array (`runtime/aggr.ml`) is
exactly the kind of allocated, mutable, identity-bearing value this engine has never needed
before: `global`, `table`, and `memory` are each one Go value holding flat contents, allocated
once at instantiation or growth, but none of them is *itself* a reference another value can hold
and compare for identity. A struct or array is. Decide what that Go value is, and how `ref`
(0002's parallel array, already widened once this milestone by #163's `Inst` field) names it,
before any `struct.new`/`array.new`/`struct.get`/`array.get` opcode gets an arm.

## What the reference's shape already settles

`Aggr.alloc_struct dt vs` / `alloc_array dt vs` (`runtime/aggr.ml:43-49`) allocate **one runtime
value per call**, each carrying its own concrete `deftype` (the resolved type identity — not a
module-relative index, because a recursive or parameterized GC type's identity is a property of
*which allocation*, not of a static slot) and a `field list`, each field a **mutable** cell
(`ValField of value ref` for an unpacked field, `PackField of packtype * int ref` for `i8`/`i16`
storage). Every field access (`read_field`/`write_field`) goes through that cell.

**Reference equality is pointer identity, and `Aggr` does not override it.** `Value.eq_ref' = ref
(==)` is the base case (`runtime/value.ml:127`), and unlike `i31.ml`/`extern.ml`/`instance.ml`,
`aggr.ml` registers no `eq_ref'` hook — `ref.eq` on two struct or array references is exactly
OCaml's physical equality on the allocated value, nothing computed. `i31` needs no allocation at
all (`type i31 = int`, boxed only by the `ref_` variant tag); `extern` wraps an opaque `ref_` the
host supplies and this engine's default board never constructs GC-typed externs, so it is out of
this ADR's scope by absence of a consumer.

## Decision

**A struct or array instance is a Go pointer to a struct this package defines, held directly in
`ref`'s existing `Addr`-shaped slot — a fourth field, not a fifth representation.**

```go
// gcObj is a struct or array instance: one allocation per struct.new/array.new call, carrying
// its own resolved type (not a module-relative index — a struct or array's identity is the
// allocation, and 0019's subtype relation is checked against this field) and its fields.
type gcObj struct {
	typ    *binary.CompType // the concrete type this instance was allocated with (0018/0020's
	                        // Fields, once that companion decision lands)
	fields []gcField
}

// gcField is one field: its value, and whether it stores a packed 8/16-bit integer — the same
// split runtime/aggr.ml's ValField/PackField make, because unpacking on every read and re-packing
// on every write is the reference's own behavior for these two storage kinds and not a detail
// this engine gets to skip.
type gcField struct {
	num    uint64 // holds a packed integer directly, or an unpacked numeric value's bits
	r      ref    // holds a reference-typed field's value; live only when the field's declared
	              // type is a reference (0002's two-array split, at field granularity)
	packed bool
	packBits byte // 8 or 16, meaningful only when packed
}
```

`ref` (`value.go`) gains:

```go
type ref struct {
	Null bool
	Addr uint32
	Inst *Instance

	// Obj is the struct or array instance this reference names, non-nil exactly when the
	// reference's runtime type is StructRef/ArrayRef. Addr and Inst are meaningless for a GC
	// object — there is no module-relative index for an allocation, which is the same fact
	// Inst's own comment states about Addr needing an instance to resolve against: an
	// allocated object resolves against nothing but itself.
	Obj *gcObj
}
```

**Three properties are load-bearing:**

1. **One Go allocation per `struct.new`/`array.new`, matching the reference's one-OCaml-value-
   per-call shape and this codebase's own precedent** (`newTable`, `newMemory`, `newGlobal` are
   each "allocate once, mutate the allocation" — a struct/array instance is the same pattern one
   level more granular, since it is itself a *value* other values can hold, where `table`/
   `memory`/`global` are always reached through an index into the instance that owns them).
2. **`ref.eq` is Go pointer comparison on `Obj`, no custom comparator, because that is what the
   reference does.** `eq_ref' = ref (==)` and no override in `aggr.ml` means implementing this any
   other way (a value-based equality, a generated ID compared by number) would be doing work the
   reference deliberately does not do and risks getting the identity semantics wrong in exactly
   the case §9 G-3 warns about — two structurally identical but separately-allocated structs must
   compare unequal, and Go's own `==` on two pointers gets that right for free.
3. **`Obj` is the fourth field the collector must trace, and it composes with the GC-precision pin
   `Inst` already satisfies rather than reopening it.** 0002's parallel-array requirement is that
   nothing the collector must trace hides inside a `uint64`; `*gcObj` is a Go pointer like `*Instance`
   already is, so `refs []ref` remains the one array 0002 pinned, now carrying two kinds of pointer
   instead of one. No new array, no new pin.

**Struct/array field *types* are `CompType.Fields`, 0018's own deferral, and this ADR does not
resolve it — `gcObj.typ` above cites `*binary.CompType` on the assumption that decision lands with
a `Fields []FieldType` (or equivalent) the decoder retains; if that companion decision chooses a
different shape, `gcObj.typ`'s type changes to match and nothing else in this ADR does, since
`gcObj` only ever asks its type for two things — how many fields, and each field's storage kind —
both of which are `CompType.Fields`' whole content however it ends up shaped.**

## Options considered

- **A — an index into a per-instance heap slice, `Addr`-shaped like a funcref (rejected).** Denser,
  and it is the option `ref.Inst`'s own comment already rejected once for funcrefs on the same
  ground: it makes the reference mean something only the owning instance's heap slice can resolve,
  and a struct/array's *identity* — the fact `ref.eq` needs — becomes "same instance, same index"
  instead of "same allocation," which is a heavier invariant to hold across `table.copy`/
  `global.set` moving the reference between instances than a bare pointer is. GC objects can
  outlive the frame or even the instance that allocated them (a struct stored into an imported
  table, exactly `ref.Inst`'s own motivating case) — a heap slice tied to one instance's lifetime
  is the wrong scope for a value that must outlive it.
- **B — copy-on-read, no persistent identity (rejected on correctness, the same shape 0017 Q2's
  option C was rejected on).** A struct is defined by having identity and mutable fields — copying
  it on every load would make `struct.set` through one reference invisible to a second reference
  to the same object, which is a wrong answer the moment two locals alias the same struct, not
  merely a slower one. Rejected the way 0017 rejected copying a callee's `Func` into a table slot:
  correctness, not cost.
- **C — a Go pointer, held in a new `ref` field (chosen).** See Decision.

## What this does not decide

- **`CompType.Fields`' own shape** (0018's deferral) — this ADR's `gcObj.typ` is written against
  whatever that decision produces, not the reverse.
- **Packing/unpacking arithmetic's exact placement** (a `gcField` method vs. inline at each
  `struct.get`/`array.get` arm) — an implementation detail the PR that lands these opcodes decides,
  not a representation question this ADR needs to settle.
- **`extern`'s GC-typed case** — no consumer on the default board (0019's own scoping move,
  repeated here for the same reason), named so a future reader does not read the silence as an
  oversight.
- **Any change to `ref.Inst`'s existing meaning for funcrefs.** `Obj` is a new, disjoint field;
  a funcref never sets it and a struct/arrayref never sets `Addr`/`Inst`. `Null` alone continues
  to mean "no value," independent of which of the two payload shapes a non-null reference carries.

## Consequences

- **`ref` grows to four fields plus `Null`, the second widening of the same struct in one
  milestone** (0018's own ADR named this direction; this is where it lands). Every existing
  construction site (`segmentRefs`, `constExprRef`, `table.grow`'s fill value, `global.set`) sets
  `Obj` to nil implicitly (Go's zero value), so no existing call site needs to change to keep
  compiling correctly — only the new `struct.new`/`array.new` arms set it.
- **`struct.get`/`struct.set`/`array.get`/`array.set`/`array.len` become buildable once
  `CompType.Fields` (0018's companion) lands**, reading/writing through `gcObj.fields` exactly as
  `Aggr.read_field`/`write_field` do, packing and unpacking on the same two storage kinds the
  decoder already parses (`decodeStorageType`, per #172's survey).
- **`ref.eq` becomes a one-line arm**: two references are equal if both null, or if neither is null
  and (for the GC case) `Obj` pointers match — composeable with whatever `func`/`extern`/`i31`
  equality 0019's scope eventually needs, without this ADR deciding those.
- **No change to `binary`'s decode side or `text`'s encode side.** This ADR is purely
  `internal/interp`'s runtime value model; the wire format (0018) and the grammar (already
  finished, per #172's survey) are unaffected.

## Append — the implementation dropped `gcField.packed`/`packBits`, and the departure is ratified (PR #247)

Appended rather than edited, per *comments and ADRs are testimony too* and the rule that a ruling is
discharged by appending with the body preserved: the sketch above is the record of what was believed
at authoring time, and the reason it survived review is part of what is worth keeping.

**The departure.** Rung 2's `gcObj` (`internal/interp/gcobj.go`) is
`{typ *binary.CompType; fields []gcField}` as sketched, but `gcField` is `{num, hi uint64; r ref}` —
**no `packed`, no `packBits`.** Two of the three differences are additive and uncontroversial (`hi`
is 0024's v128 half, filed as grave #243's shape one site over); the omission is the one that needed
a ruling, and it was flagged in #247 as a departure rather than taken silently.

**Why the omission is right, in the terms the ratification used.** The sketch's `packed`/`packBits`
were *design-time speculation about what the instance would need*, and the implementation's own
consumer analysis found the truth: **packedness is a type-level fact, `binary.FieldType.Storage`
(decision 0021) is its sole authority, and an instance-level copy had no consumer.** Every read site
already holds the declared field type — it has to, because `struct.set` must know which stack array
the value comes from *before* it can pop anything, the target reference sitting underneath the value
— so the width is in hand wherever it is needed, derived rather than stored. And `aggr.ml`'s
`alloc_field`/`write_field` **wrap at write**, so a stored value is already narrowed and only the
read needs the width at all.

**The one-truth law favours the omission rather than merely tolerating it.** A per-field copy of a
fact that never varies per instance would be two places knowing one thing with nothing keeping them
equal — the drift this project files graves for (#78/#105/#106) — so the copy would have had to be
enrolled as a witness, with a lifetime drift check, for a constant. That is a control debt bought
with nothing.

**The evidence that derivation suffices, rather than the argument that it should.** `struct.wast`'s
packed vectors pass green — the file goes 7/25 to **25/25**, and the packed rows
(`set_get_packed_g0_1 (i32.const 257)` reading back **1** where the i16 field in the same struct
reads back 257) are exactly the ones a missing or mismatched width fails — and fifteen of fifteen
mutations were watched die, including the two that break wrap-at-write and extend-at-read
independently.

**Authority ordering, stated because it is the general point.** The accepted document loses to the
consumer-forced law here: **ADRs record decisions; the tree records truths.** An ADR's sketch is
binding on the *choice* (option C, a Go pointer with identity) and advisory on the *shape*, and where
implementation finds a field with no consumer the ADR is amended by append, not obeyed by inertia.

**Era-stamped, and the door is open rather than walled.** As of rung 2 the width is derived at every
read from `FieldType.Storage`. If a bench ever shows that type derivation costing on a hot path,
**the fields may return** — as *enrolled witnesses, with their drift check*, which is the condition
and not a footnote to it. This append is a fork in the road, not a wall.

(Ratification: Scott, PR #247, on the agent's own flag. The stamp is what closes it, per *a status
field is a citation to an approval*; the departure spent one PR flagged and open, which the record
keeps alongside the stamp. Second rider of the same ratification retired `fieldStorage`'s
type-agreement check for having no reachable subject — the tripwire is #248.)

**Rung 3 carried the ratified shape unchanged, which is the evidence the ratification could not yet
have.** The append above argued the omission was right from rung 2's consumers alone — one rung, one
witness. Rung 3 (the fourteen `array.*` arms, PR #249's implementation) is the second, and it added
**no representation at all**: `gcObj` and `gcField` are byte-identical, and `popField`/`defaultField`/
`pushField` are reused verbatim including both packed cases. The reference agrees at the same seam —
`Aggr.Struct of deftype * field list` and `Aggr.Array of deftype * field list`, one constructor each
over the *same* `field list` (`aggr.ml:8-9`) — so the two rungs differ only in how the field *type* is
found, by index for a struct and as the comptype's single content for an array. Had `gcField.packed`
survived, rung 3 would have been its second writer and the drift check its second obligation, on a
fact that still never varies per instance. The fork in the road above stays open on the same terms;
nothing about rung 3 approached it.
