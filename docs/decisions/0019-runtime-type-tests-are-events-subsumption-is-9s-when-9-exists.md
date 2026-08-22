# 0019 — Runtime type tests are events; subsumption is #9's, whenever #9 exists

Date: 2026-08-08 · Status: **accepted** — stamped by Scott on PR #175

> Held at `proposed` while it had no stamp, per the ruling on #142. The stamp names the structural
> improvement the design earns beyond the line it draws: using the validator's own comparison
> functions at runtime types gives the subtype relation **one comparator with two callers** —
> execution today, #9 whenever it exists — rather than a runtime approximation that could drift
> from the eventual validator's truth. `sameFuncType` widening into the declared-supertype walk,
> rather than growing a second comparator for casts, is the one-authority law paying #164's
> 4-vector debt as the same motion.
>
> On the flagged question — whether heap-object representation earns its own ADR now or waits for
> the implementation PR that needs it — **its own ADR, authored now: filed as 0020.** The
> criterion is the one this campaign runs on: a decision earns a document when multiple defensible
> shapes exist and the choice propagates, and this one propagates into 0002's GC-traceability pin,
> the value model, and every struct/array arm to come — 0002-scale, never made ambient inside an
> implementation PR where it would be reviewed as code style instead of as architecture. The
> consumer is measured and real (zero `0xfb` dispatch, `struct.new` waiting on `CompType.Fields`),
> which is 0017's own bar for authoring, met exactly.
>
> Also on the record: the recon (#172) conflated "how much subtyping" with "subtyping over what
> value," and tracing this design forward caught the conflation and split it into a fourth
> decision, named rather than silently absorbed. The map got corrected by travel, which is what a
> map is for.

Filed against **#172** (the GC-gate recon) and milestone **v0.2.0 GC gate**, downstream of
**0018** (accepted): the wide `ValType` that decision supplies is what this ADR's relation is
expressed over.

## Question

`ref.test`, `ref.cast`, and `br_on_cast`/`br_on_cast_fail` each ask the same question — does this
runtime reference's actual type match a target type — and the reference computes the answer with
`Match.match_reftype (type_of_ref r) rt'` (`eval.ml:648-657`, `:246-260`): the *live value's*
dynamic type against a *statically named* target. `sameFuncType` (`interp/call.go`) already
answers a narrower version of this question for `call_indirect` and is a declared MVP reduction —
structural equality, no subtyping — with a measured gap: 4 corpus vectors (#164) using `rec`/`sub`
fail because two functions related by a declared supertype, not by identical shape, compare
unequal.

GC is where that gap stops being a rounding error, because casting *is* subtyping — there is no
version of `ref.test`/`ref.cast` that does not ask a subtype question — and v0 has no validator
(#9 is unimplemented): every rejection this engine issues today is either the decoder's
grammar-level malformedness or an interpreter-side layering debt, never a validator's static
verdict. Decide what this phase computes, when, and over what representation, before any cast
opcode gets an arm.

## The line the reference already draws, and this project already uses the language for

**A runtime type test is an event; a static subtype judgment is #9's, however long #9 does not
exist.** The reference's own architecture states this directly: `match_reftype`/`match_heaptype`
(`match.ml`) are pure functions of two types with no side effect and no store access — they are
the *validator's* machinery (`valid/match.ml`'s own path), and `RefTest`/`RefCast` in `eval.ml`
call them **at the value the store actually holds**, not at a type the module merely declares.
The validator uses the same relation to check that a module's static types are consistent; the
interpreter uses it to answer "is this particular value, right now, an instance of that." Two
different questions sharing one comparison function.

This project already has the vocabulary for the boundary this crosses. `#9`'s arity question
(`call.go:205`), `#9`'s verdict (`table.go`'s inverted-limits case, `newTable`), `#9`'s judgement
(`declaredFuncType`) — every place this codebase reports something a validator would normally
have ruled out, it says so by citing #9 and reports the fact as a layering debt rather than
inventing a verdict of its own. `sameFuncType`'s own doc comment already does this for structural
equality: *"a real gap under GC, which is the same shortfall the doc comment above declares."*
This ADR is that citation, generalized to every place GC needs the same relation, and the
generalization is what makes it a decision rather than another one-line comment.

## Decision

**Runtime type tests execute; static subtyping stays #9's.** Concretely:

1. **`ref.test`/`ref.cast`/`br_on_cast`/`br_on_cast_fail` compute the relation at the point of
   use, against the value's actual runtime type, every time.** No precomputed subtype table, no
   caching at instantiation. This is execution's own event, on the reference's own architecture —
   `type_of_ref` reads the live value, and a value's dynamic type cannot be known before the value
   exists (a `struct.new` allocates a concrete `deftype` identity at the moment it runs; nothing
   static can precompute what a runtime `ref.cast` will meet, since the whole point of the
   proposal is that a table or a global can hold values of different concrete types under one
   static bound).
2. **`sameFuncType`'s reduction is what widens, not what gets replaced by a second mechanism.**
   `call_indirect`'s type check and `ref.test`'s type check are the same question — does this
   runtime value's type match a target — asked at two call sites. #164's 4-vector gap is
   `sameFuncType` reaching GC's edge; the fix is `sameFuncType` (renamed to whatever a
   general `matchType`/`matchHeapType`-shaped function ends up being called) learning `rec`/`sub`,
   not a parallel comparator built for casts alone. One relation, every caller.
3. **The relation is a direct MVP reduction of `match_heaptype`/`match_reftype`, expressed over
   0018's `ValType`, computed structurally with the declared-supertype walk `match_deftype`
   already needs** (`dt1 == dt2 || subst_deftype s dt1 = subst_deftype s dt2 || <declared
   supertype chain>` — `match.ml:151-155`). No new abstraction beyond widening the existing
   reduction; the reference's own function is small enough that "compute it every time" is not a
   performance question this ADR needs to answer with a cache.
4. **A type mismatch discovered this way is `interp`'s trap channel for `ref.cast` (the reference
   traps, `"cast failure, expected … but got …"`) and its boolean-result channel for `ref.test`
   (never a trap) — never `#9`'s layering-debt channel.** The layering-debt channel is reserved
   for the case this engine cannot answer the question at all (an index past the type space, a
   kind mismatch between what an opcode expects and what a slot holds) — exactly `sameFuncType`'s
   existing siblings (`declaredFuncType`'s two `#9` failures). A cast that *can* be evaluated and
   fails is a spec-defined outcome, not an engine limitation; conflating the two would be the
   `memory.grow`-mistake shape (§0x40's doc comment) on a new opcode.

## What this does not decide, named rather than left implicit

**Heap-object representation is a separate forced question this decision depends on and does not
answer.** `type_of_ref` on a struct or array reads the *allocated instance's* recorded type
(`Aggr.alloc_struct`, `eval.ml:685`) — there has to be a live object somewhere in this engine's
memory model before any cast can be tested against it. Today there is none: `interp` has no
`0xfb`-prefix dispatch at all (confirmed by direct search — zero arms, not merely incomplete
ones), and `struct.new`'s own arity depends on `CompType.Fields`, which 0018 explicitly deferred
as "a type-definition-level decision this ADR's `ValType` feeds but does not resolve." So there
are two layers beneath this ADR, not one: 0018 supplies the *type* representation this decision's
relation is expressed over; a fourth, not-yet-filed decision must supply the *value* — how a
struct or array instance is represented once `struct.new`/`array.new` exist as executable
opcodes — before `ref.test`/`ref.cast` can be exercised against anything but `func`/`extern`/`i31`
(which need no heap object: a funcref already resolves through `ref.Inst`+`Addr`, and `i31` is an
unboxed 31-bit integer with no allocation at all). This ADR's relation-computation rule applies
identically once that representation exists; it is written now because the *rule* does not
depend on the representation, only its exercise does.

**Recommendation for that fourth decision, stated as a direction rather than decided here**: read
`Aggr.alloc_struct`/`alloc_array`'s own shape (`runtime/aggr.ml`) for the wire-form-is-the-
authority reason 0018 used, when it is filed — likely its own small struct alongside `ref`, given
`ref.Inst`+`Addr` already carries one runtime-object-identity shape and a GC object needs the
same kind of thing (which instance's heap, which slot) rather than a new pattern.

**This ADR also does not decide `array.*`/`struct.*`'s encoder-capacity gaps** (`immIdxIdx`'s
missing shape, `immPatch`'s one-thunk-per-instruction limit, both #172's item 5) — those are
`internal/text` capacity questions with no representation decision inside them, separable and
already scoped by the recon as PR-sizing concerns rather than ADR-sized ones.

## Consequences

- **`sameFuncType` grows the declared-supertype walk and keeps its name and callers unchanged.**
  `call_indirect`'s existing call site needs no change beyond the comparator getting smarter;
  #164's 4 vectors convert as a side effect of this ADR's implementation, not as their own PR.
- **`ref.test`/`ref.cast`/`br_on_cast`/`br_on_cast_fail` get interpreter arms only after the heap-
  object representation (the deferred fourth decision) lands for the struct/array cases** — but
  the `func`/`extern`/`i31` cases can land against this ADR alone, since `ref.Inst`+`Addr`
  already resolves a funcref's runtime identity and `i31` needs no object at all. A PR that lands
  the ungated-shape casts first, before struct/array representation exists, is in scope and
  correctly sized by this ADR; the recon's co-blocking probe should confirm how many corpus
  vectors that alone reaches before it is sized as a PR.
- **No caching, no instantiation-time precomputed table** — stated so a future PR proposing one
  for performance reasons has to argue against this ADR explicitly rather than drift into it
  unnoticed. If `#136`-style benchmarking later shows the per-use computation costing real time on
  cast-heavy Go-guest code, that is a *measured* case to reopen this decision, not a default.
