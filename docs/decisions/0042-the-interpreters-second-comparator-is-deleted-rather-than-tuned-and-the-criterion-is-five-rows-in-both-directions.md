# 0042 — The interpreter's second comparator is deleted rather than tuned, and the criterion is five rows in both directions

Date: 2026-08-21 · Status: **proposed** — authorized to be authored, not yet stamped on its choice

> The authorization is Scott's order on the #474 review, relayed to
> [a durable comment](https://github.com/scttfrdmn/burroughs/pull/474#issuecomment-5376316026):
> *"`sameFuncType` is unblocked: file the issue, write the ADR. Diverging in both directions is a
> stronger finding than the one-group report it replaced, and it's a design question now, not a tuning
> one."* That sentence authorizes **an ADR**; it does not choose between the two directions the issue
> left open. So the `Status:` stays open, per the law that a Status field is a citation to an approval —
> there is an artifact for *write this*, and none yet for *this option*. Held at `proposed` on 0019's
> own precedent, which was held for the same reason and for the same relation.

Filed against **#475** and downstream of **0019** (accepted), whose stamp decided this question in
principle and deferred it on a condition that has since been met.

## Context

`internal/interp` computes the subtype relation twice. `sameFuncType` (`internal/interp/call.go:775`)
delegates to `matchDeftype` (`:799`), a hand-reduced `match_deftype` with its own disjunct 2
(`sameDeftype`, `:903`), its own innermost equality (`compTypeEqual`, `:959`, and
`structFuncTypeEqual`, `:982`), and disjunct 3 inlined into the walk at `:865`. `internal/validate`
computes the same relation from the reference's own structure (ADR 0031's port of `match.ml`) and
**exports it**: `MatchDefType` (`internal/validate/match.go:699`).

The duplicate diverges from the reference **in both directions at once**, measured on the all-gates-on
lane at `65092 pass, 17 fail, 0 gated` — five of those seventeen rows, verbatim from the board:

| direction | rows | bucket key |
|---|---|---|
| too strict | `type-equivalence.wast:131,156,188` | `trap: indirect call type mismatch, expected func [(ref 1)] -> [] but got func [(ref 2)] -> []` (and two siblings) |
| too lax | `type-rec.wast:183,192` | `assert_trap (invoke) expected: indirect call type mismatch` |

**The two directions have different causes, and neither is the scope boundary the code documents.**
This matters because the tree already carries a named, cited, tested boundary for this function —
`type-subtyping.wast`'s M10/M11 pair, cross-module rec-group relabelling, pinned by
`TestSameFuncTypeCorpusScope` — and it would be easy to read five new rows as that boundary widening.
They are not it.

- **The over-strict rows are the innermost equality, not the boundary.** `type-equivalence.wast:107-130`
  is **one module, no imports**: `$s1` and `$s2` are byte-identical `(func (param i32 (ref $s0)))`
  declarations at two indices, `$t1`/`$t2` take `(ref $s1)`/`(ref $s2)`, and the vector asserts every
  cross-combination of `call_indirect` succeeds. Neither type declares a supertype, so disjunct 3 never
  runs and disjunct 2 reduces to plain structural equality — *the pre-existing MVP case the scope note
  explicitly says is unchanged*. It fails because a comptype **contains type indices**, and comparing
  them structurally compares `1` against `2` without ever asking what those indices name. The bucket key
  prints the defect: `(ref 1)` versus `(ref 2)`, two sides reported as indices.
- **The under-strict rows are a dropped fact, and the corpus hands over a discriminating triple**
  (`type-rec.wast:167-192`), three single-module `call_indirect` vectors that differ only in the rec
  group's shape: identical groups match; the same two members **reordered** must not; a **smaller**
  group must not. We match all three. So member position and group size are the dropped facts, which
  is iso-recursive equality exactly — `subst_deftype` over unrolled forms.
- **One cause at the level that matters**: how much of a type's context the comparison carries. Too
  little in the lax direction, index-identity-instead-of-content in the strict one. Any loosening that
  admits the three admits the two; any tightening that traps the two traps the three. The reference has
  **one** mechanism that gets both right and it is not a tolerance — it is canonicalization.

**And the deferral's stated reason has expired.** `call.go:765-768` flags the question and declines it
because *"unifying it is a wider change than the grave that exposed it."* Both halves of that wider
change landed for other reasons: `internal/interp` already imports `internal/validate` (`link.go:7`,
`tag.go:7`, no cycle), `MatchDefType` is already exported with a signature-compatible shape, and its
documented argument order — *"the supplier's type first, the importer's declared type second"* — is
already the order `call.go:590` passes. The surface was built for `match_externtype` in the meantime.

## Decision

**Route both call sites through `validate.MatchDefType` and delete the duplicate relation** —
`sameFuncType`, `matchDeftype`, `sameDeftype`, `compTypeEqual`, `structFuncTypeEqual`: five functions
spanning `call.go:703-990`, of which 202 of 288 lines are comment — rather than teaching the duplicate
to canonicalize.

**Two call sites, and the count is measured rather than taken from the code's own description.**
`call_indirect` (`call.go:590`, through `sameFuncType`) and the cast family's arm 9 (`castop.go:251`,
calling `matchDeftype` directly), which serves `ref.test`, `ref.cast` and
`br_on_cast`/`br_on_cast_fail`. The doc comment at `call.go:760` names *three* consumers —
*"`call_indirect`, `call_ref` and `ref.cast`"* — and `call_ref` is not one: `resolveCallRef`
(`call.go:657`) resolves the callee from the operand's own `r.Inst` and takes that function's type
directly, comparing nothing. That is correct behaviour, not a missing check (the reference compares
nothing there either; the validator owns it), which is precisely why the claim went unchallenged.

`compTypeAt` (`:943`) is **not** in the deletion set — `castop.go:308`, `gcobj.go:382` and
`value.go:1071` call it, and `internal/validate` has its own copy (`match.go:681`). Two bounds-checked
accessors are a duplicated three-line lookup, not a duplicated *judgement*, and collapsing them is not
what the one-authority law is about.

**This is 0019's decision executed on its own condition, not a new one.** Its stamp: *"using the
validator's own comparison functions at runtime types gives the subtype relation **one comparator with
two callers** — execution today, #9 whenever it exists — rather than a runtime approximation that could
drift from the eventual validator's truth."* The condition was *whenever #9 exists*. It exists, it is
the ported relation, and the drift the stamp predicted is the five rows above.

**The alternative — canonicalize inside `interp` — is declined for the reason 0019 gave.** It keeps two
implementations of one judgement, and the cost of that is now measured rather than forecast: the
coarseness `internal/validate` fixed in ADR 0031 (equi-recursive equality, falsified by
*"Every vector that discriminates the two is a `type mismatch` admission in `type-rec.wast`"*) is the *same coarseness* still live
in `internal/interp`, in the same file, one package over. The finding was made once and did not travel,
because nothing structural required it to.

**Not decided here:** whether the cast arms need `match_reftype` rather than `match_deftype` at the
boundary (`castop.go:454` names `subst_reftype` as a separate absence), and where the nullability and
heap-type dispatch above arm 9 should live. Those are implementation shape, recorded at the site.

## Criterion

**Five rows in both directions, plus a structural bound the rows cannot supply.** All five are
all-gates-on-lane rows, so the default lane's reward is **structurally zero** and the reward figure is
the all-on fail delta, per the product law's substitution clause.

| direction | population | now | required |
|---|---|---|---|
| over-strict | 3 rows, `type-equivalence.wast:131,156,188` | three distinct `trap: indirect call type mismatch` keys | **pass** |
| under-strict | 2 rows, `type-rec.wast:183,192` | `assert_trap (invoke) expected: indirect call type mismatch` ×2 | **pass (the trap fires)** |
| everything else | the other 12 all-on fails and 65092 passes | — | **unmoved, term for term** |

**All-on fail: 17 → 12.** The third row of that table is the criterion's load-bearing half, because this
change substitutes a *stricter* relation at a site that currently traps too rarely and a *more
structural* one at a site that currently traps too often, and either could move a row nobody named.
The bound is stated as term-for-term identity of the remaining buckets, not as a count.

**The forecast has grounds in both directions, and they are different grounds.**

- **Over-strict: `internal/validate` already makes this exact judgement on this exact module, and gets
  it right.** Validating `type-equivalence.wast:107-130` requires checking
  `(call_indirect (type $t1) (ref.func $s2) …)` — that is `(ref $s2)` against a declared `(ref $s1)`,
  the discriminating comparison — and the module **validates**: its rows fail as runtime traps, not as
  `module text` refusals. The relation that will replace the duplicate is therefore already answering
  the over-strict question correctly, in the same file, at validation time.
- **Under-strict: the corpus supplies a static twin of the discriminating triple, and it is already
  green.** `type-rec.wast:137-162` is the same three shapes through the *import* path — identical group
  links, reordered group is `assert_unlinkable "incompatible import type"`, smaller group likewise — and
  the linker has run on `validate.MatchDefType` since grave #368. Those rows are not among the 17, so
  the relation already decides rec-group position and group size correctly. Independently,
  `internal/validate`'s group-length condition has a falsification probe on the record: replaced with
  `if false`, *"`type-rec.wast` goes 22/26 back to 19/26"* (`internal/spec/spec_test.go:7756`).

So the two halves of the forecast rest on two separate already-measured facts, neither derived from the
other — and **neither is a measurement of the five rows themselves**, which is the honest statement of
what a pre-registration can be.

### The bound the five rows cannot supply, stated before the fact

0031's criterion was falsified by its own implementation and the lesson was that **a representation
that cannot express the wrong relation is a stronger bound than a population.** Applied here, before
the port:

**The bound is deletion, not agreement.** If the change leaves `matchDeftype` in the tree — behind a
different name, as a fast path, as a fallback for an unresolvable index — then two comparators still
exist, the five rows are the only thing standing between them, and the next divergence is as
unwitnessed as this one was for three months. **If the four call sites are the only callers and the
functions are gone, divergence between two comparators is unrepresentable rather than merely
untested.** A residue of one wrapper is not a lapse of taste; it is the criterion failing.

**Two things must move with it or the deletion is incomplete**, and both are named now because each is
a place a duplicate could survive:

- `TestSameFuncTypeCorpusScope` (`internal/interp/call_test.go:481`) asserts the M10/M11 false positive
  **directly**, and its own error text says the test *"should flip"* if a rec-group fix lands in
  `matchDeftype`. Its subject is being deleted, so it is a subject of this change, not a check on it:
  it either flips to asserting the correct verdict through the new relation or it retires with a
  citation. A tripwire whose subject dissolves is re-pointed, never quietly dropped.
- `TestSameFuncTypeDeclaredSupertypeWalk` (`:383`) pins 0019's widening through the deleted function's
  name. The property it pins belongs to the surviving relation and must be asserted of it.

### Five claims in the tree that this change falsifies or retires, and one no instrument can check

Recorded here because each is a sentence a reader will meet *after* the change and read as current.

1. `call.go:742-743` — *"the decoder retains no rec-group boundary at all (no `RecGroup`/group-relative
   index anywhere in `binary.Module`)"*. Already false: `binary.CompType.RecStart`/`RecLen` are
   retained, and `:757-758` says so, fifteen lines below — the claim and its correction are in one
   comment block, in that order.
2. `call.go:760-761` — *"no corpus vector reaches the M10/M11 shape through any of them."* The five rows
   do not literally falsify this, because M10/M11 is a **cross-module** relabelling and all five vectors
   are single-module. What they falsify is the reading the sentence invites: that the disjunct-2 gap is
   unwitnessed. It is witnessed, in both polarities, through `call_indirect`.
3. `internal/spec/spec_test.go:10593` describes `Instance.link` as comparing with `sameFuncType`. Grave
   #368 moved the linker off it; `sameFuncType` has exactly one non-test caller and it is not the
   linker.
4. `call.go:739` cites **`matchesDeclaredSupertype` "below"** as disjunct 3. No such function exists
   anywhere in the tree — grave #261's refactor folded the walk into `matchDeftype` (`:865`) and left
   the pointer. **This is the failure mode of the citation form I would recommend over a line number,
   and it failed better**: a dangling *symbol* is found by one grep returning exactly one hit — its own
   citation — where a drifted *line* returns a plausible neighbour. Detectable, and detectable
   mechanically: a backticked identifier in a Go comment that matches no declaration in its package is
   a checkable predicate over data a build already has. Noted on #473, which owns that question.
5. `call.go:760` names `call_ref` as a consumer of this reduction. It is not one, per the Decision
   above.

**And the one no instrument can check: claim 2 is a negative claim, so it has no target.** No citation
sweep in this tree resolves *"no corpus vector reaches …"*, because there is nothing to resolve. It
decayed silently while the validator slices unshadowed the rows that contradict it — which is the same
frontier-moving mechanism ADR 0032 recorded for opcode coverage, arriving here as a stale *absence*
claim instead of a stale count. Whether it was true when written is **unmeasured**: these rows sat
behind validator declines that cleared across slices 6–10, and re-deciding that would mean re-running
the all-on lane at an older revision. Stated as unmeasured rather than assumed either way.

## Consequences

- **`internal/interp` gains a hard dependency on `internal/validate`'s relation for execution**, not
  just for linking. That is the one-authority law's price and its point: a runtime verdict and a
  validation verdict can no longer disagree, because there is one function.
- **The default lane cannot move and is said to be structurally unable to.** All five rows are
  `gate:gc` rows; `unsupported` cannot move; the reward figure is the all-on fail delta.
- **Deleting five functions removes 202 lines of comment that are the best record the tree has of
  `match_deftype`'s three disjuncts.** That reasoning was earned across 0019, 0027, grave #261
  and grave #368, and the surviving relation's own comments do not carry the *reductions* those graves
  ruled out. What is worth keeping (why disjunct 2 is equality and not subtyping — #261; why disjunct 3
  is where the cycle guard belongs) moves to the call sites or into this record; what dies with the
  duplicate is the description of a relation that no longer exists. Named as a cost because a deletion
  that silently discards a grave's testimony is how the grave gets re-dug.
- **`ref.test`/`ref.cast`/`br_on_cast` verdicts change through arm 9 with no corpus row to witness the
  change.** All five failing rows arrive through `call_indirect`; arm 9 has none, so the criterion's
  third line — every other bucket unmoved, term for term — is the *only* thing that will say a silent
  change happened there. The implementation must state which site it moved and which it merely did not
  break.
- **This ADR's implementation queues behind #367**, which Scott approved as next in the same review. One
  ADR earns one implementation, and this one is outstanding until then; flagged rather than resolved,
  because the ordering is his.
