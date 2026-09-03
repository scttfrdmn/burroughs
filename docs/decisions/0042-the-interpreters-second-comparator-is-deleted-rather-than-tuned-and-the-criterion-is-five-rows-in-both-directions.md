# 0042 — The interpreter's second comparator is deleted rather than tuned, and the criterion is five rows in both directions

Date: 2026-08-21 · Status: **accepted** — stamped by Scott on the #481 review, relayed to
[a durable comment](https://github.com/scttfrdmn/burroughs/issues/475#issuecomment-5377578628). Its
implementation landed first, under a `proposed` status; see
[Implementation](#implementation-landed-under-a-proposed-status-and-the-field-was-flipped-afterwards).

> **Held at `proposed` until 2026-08-22, and the reason it was held is kept rather than overwritten.**
> The authorization to *write* this document was Scott's order on the #474 review, relayed to
> [its own comment](https://github.com/scttfrdmn/burroughs/pull/474#issuecomment-5376316026):
> *"`sameFuncType` is unblocked: file the issue, write the ADR. Diverging in both directions is a
> stronger finding than the one-group report it replaced, and it's a design question now, not a tuning
> one."* That sentence authorizes **an ADR**; it does not choose between the two directions the issue
> left open. So the field stayed open through its own implementation, per the law that a Status field is
> a citation to an approval — there was an artifact for *write this* and none for *this option* — on
> 0019's precedent, which was held for the same reason and for the same relation. The stamp cited above
> is that missing artifact, and the order that produced it names the relay as the remedy: *"a `Status:`
> is a citation to an approval and my in-session stamp holds no artifact — relaying it durably is the
> established remedy."*

> **Line citations below describe the pre-change tree and are left in that tense.** Most of them name
> lines inside the five functions this ADR deletes, so there is nothing to re-point them at: the
> Context and Decision sections are a description of the code as it stood on 2026-08-21, which is what
> an ADR is. #456's open question — whether a line number inside a tombstone is a historical address —
> is answered here only for this document, and only because the deletion forces it.

Filed against **#475** and downstream of **0019** (accepted), whose stamp decided this question in
principle and deferred it on a condition that has since been met.

## Context

`internal/interp` computes the subtype relation twice. `sameFuncType` (`internal/interp/call.go:776`)
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

**And the deferral's stated reason has expired.** `call.go:766-769` flags the question and declines it
because *"unifying it is a wider change than the grave that exposed it."* Both halves of that wider
change landed for other reasons: `internal/interp` already imports `internal/validate` (`link.go:7`,
`tag.go:7`, no cycle), `MatchDefType` is already exported with a signature-compatible shape, and its
documented argument order — *"the supplier's type first, the importer's declared type second"* — is
already the order `call.go:591` passes. The surface was built for `match_externtype` in the meantime.

## Decision

**Route both call sites through `validate.MatchDefType` and delete the duplicate relation** —
`sameFuncType`, `matchDeftype`, `sameDeftype`, `compTypeEqual`, `structFuncTypeEqual`: five functions
spanning `call.go:704-991`, of which 202 of 288 lines are comment — rather than teaching the duplicate
to canonicalize.

**Two call sites, and the count is measured rather than taken from the code's own description.**
`call_indirect` (`call.go:591`, through `sameFuncType`) and the cast family's arm 9 (`castop.go:251`,
calling `matchDeftype` directly), which serves `ref.test`, `ref.cast` and
`br_on_cast`/`br_on_cast_fail`. The doc comment at `call.go:761` names *three* consumers —
*"`call_indirect`, `call_ref` and `ref.cast`"* — and `call_ref` is not one: `resolveCallRef`
(`call.go:658`) resolves the callee from the operand's own `r.Inst` and takes that function's type
directly, comparing nothing. That is correct behaviour, not a missing check (the reference compares
nothing there either; the validator owns it), which is precisely why the claim went unchallenged.

`compTypeAt` (`:943`) is **not** in the deletion set — `castop.go:308`, `gcobj.go:382` and
`value.go:1133` call it, and `internal/validate` has its own copy (`match.go:681`). Two bounds-checked
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
  `if false`, *"`type-rec.wast` goes 22/26 back to 19/26"* (`internal/spec/spec_test.go:7779`).

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

1. `call.go:743-744` — *"the decoder retains no rec-group boundary at all (no `RecGroup`/group-relative
   index anywhere in `binary.Module`)"*. Already false: `binary.CompType.RecStart`/`RecLen` are
   retained, and `:757-758` says so, fifteen lines below — the claim and its correction are in one
   comment block, in that order.
2. `call.go:761-762` — *"no corpus vector reaches the M10/M11 shape through any of them."* The five rows
   do not literally falsify this, because M10/M11 is a **cross-module** relabelling and all five vectors
   are single-module. What they falsify is the reading the sentence invites: that the disjunct-2 gap is
   unwitnessed. It is witnessed, in both polarities, through `call_indirect`.
3. `internal/spec/spec_test.go:10722` describes `Instance.link` as comparing with `sameFuncType`. Grave
   #368 moved the linker off it; `sameFuncType` has exactly one non-test caller and it is not the
   linker. (The number is the *current* location of that sentence, re-pointed twice since this list was
   written — it was `:10548`, then `:10593`, then `:10616` — because a pointer that asserts where a live sentence is
   gets repaired while a pointer recording where something used to be does not. The sentence itself now
   carries a tense correction rather than a deletion, per the implementation below.)
4. `call.go:740` cites **`matchesDeclaredSupertype` "below"** as disjunct 3. No such function exists
   anywhere in the tree — grave #261's refactor folded the walk into `matchDeftype` (`:865`) and left
   the pointer. **This is the failure mode of the citation form I would recommend over a line number,
   and it failed better**: a dangling *symbol* is found by one grep returning exactly one hit — its own
   citation — where a drifted *line* returns a plausible neighbour. Detectable, and detectable
   mechanically: a backticked identifier in a Go comment that matches no declaration in its package is
   a checkable predicate over data a build already has. Noted on #473, which owns that question.
5. `call.go:761` names `call_ref` as a consumer of this reduction. It is not one, per the Decision
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

## Implementation: landed under a `proposed` status, and the field was flipped afterwards

Appended after the fact. Everything above is left as it was written, including the two claims below
that it got wrong — the point of a pre-registration is that it can be scored.

**This heading's second clause used to read *"and the field is the open item"*, which the stamp on
2026-08-22 made false.** It is re-pointed rather than left standing, because a sentence written before
a flip and kept after it tells the next reader the tree is in a state it is not — the same
foreclosing-words shape the v0 ladder's *"gates present and off"* was in, and one this document is a
poor place to repeat, since it exists to score claims that turned out wrong. The one incoming citation,
in the header above, moved with it.

*(One pointer re-pointed three times by the text it names, never by a delta: `compTypeAt`'s caller
list read `value.go:1071`, then `:1083`, and reads `value.go:1133` — the line holding
`ct, ok := compTypeAt(r.Obj.mod, r.Obj.typeIdx)`, today inside `payloadOf`. #452's twelve-line dating
note moved the call, grave #491's repair moved it eleven more, and on 2026-09-03 a third move of
thirty-nine lines moved it again — twenty-eight from #553, which moved this call and re-pointed
nothing, and eleven from ADR 0023's withdrawal note at `pushNum`. Every delta moved the call, none
moved the claim, and no two are alike, which is why the text and not the arithmetic is what located it
each time. The twenty-eight had been unrepaired since #553 landed: the census control over this
channel counts positional citations and cannot ask whether one still resolves, which is
[#497](https://github.com/scttfrdmn/burroughs/issues/497)'s subject.)*

**The board moved 17 → 7. The forecast was 17 → 12.** Ten rows, not five: `65092 → 65102 pass`,
`17 → 7 fail`, `0 gated`, and the ten are exactly ten named rows, so the criterion's third line —
every other bucket unmoved, term for term — held. The bounds are re-based with a ledger entry each.

**The extra five are `type-subtyping.wast`'s — the site Scott ordered named in advance, and the
pre-registration is scored against the prediction it carried.** It was registered as where residue
would sit if the change fell short; instead all five *cleared* (`type-subtyping.wast: 119/119 pass,
11 bound`). Naming the site still did the work it was ordered to do: a doubled yield arrived already
attributed to a file someone had committed to watching, rather than as a surplus to be explained
after the fact by whoever wanted it to be good news.

**Attribution, measured by routing one call site at a time rather than inferred from the total:**

| routed | rows that clear | which |
|---|---|---|
| `call_indirect` alone | 5 | `type-equivalence.wast:131,156,188` + `type-rec.wast:183,192` — the forecast's five |
| arm 9 alone | 5 | `type-subtyping.wast:442,488,510,523,534` |
| both | 10 | 17 → 7 |

The ten are checkable against a *prior, independent* attribution: #357/#358's changelog entry
enumerated all seventeen survivors by row, and the ten that cleared here are exactly its
`indirect call type mismatch` five plus its `type-subtyping.wast` five, leaving its `array.wast` two
and its five local-initialization rows. Two ledgers written for different reasons agreeing member for
member is worth more than either total.

**Which falsifies this ADR's own fourth Consequence**: *"arm 9 has none"* — no corpus row witnessing
the cast family's verdict change. Arm 9 alone owns five, and they are five that were failing. The
claim was built by attributing the seventeen fails' *known* members to `call_indirect` and reading
the complement as empty, and **an attributed partition is not a partition**: attribution names where
you looked, and the rows nobody had attributed were sitting in a third file. The consequence of
getting it wrong was almost entirely benign here — it under-promised — but the same reasoning in the
other direction is a silent behaviour change with a bound that says nothing, which is exactly what
that bullet was written to prevent.

**The residue is 7 and none of it is this ADR's**, named rather than absorbed, per the order:
`array.wast` 2 (`constant expression required`) and the five local-initialization rows
(`func.wast:659`, `local_init.wast:25,29,39,52`), which are
[#452](https://github.com/scttfrdmn/burroughs/issues/452) — `decision-needed:scott`, deliberately not
taken. Neither group touches the subtype relation, so this change had no path to them.

**And the two-test list was incomplete — the finding is how it was built.** The Criterion names two
controls that must move with the deletion. **Seven** test functions actually did, derived from
`git diff -U0`'s hunk headers rather than from memory: four in `internal/interp/call_test.go`, two in
`castop_test.go`, one in `link_test.go`. The two named are exactly the two whose *names* begin
`TestSameFuncType`, which is the tell. Widening one step to the identifier — `grep sameFuncType` over
the pre-change test files — would have found five of the seven (`call_test.go`'s four, plus
`link_test.go:571`'s comment); it would still have missed `castop_test.go`'s two, which reach the
relation through arm 9 and **do not spell it once** — `grep -c sameFuncType` over that file at `HEAD`
returns 0. So neither a name nor an identifier bounds the set. **A deletion's control domain
is its call graph**, and the derivation that gets it right is the one that reads the callers of the
call site, not the mentions of the callee. `TestFuncTypeStringIsTheReferenceSpelling` is the case that
proves it: nothing in this document anticipated it, and it changed.

**The two named controls' successors**, so the pre-change names above resolve to something for a
later reader:

| named here | now |
|---|---|
| `TestSameFuncTypeDeclaredSupertypeWalk` | `TestCallIndirectComparisonDeclaredSupertypeWalk` (`call_test.go:417`), with `…Falsification` (`:512`) |
| `TestSameFuncTypeCorpusScope` | `TestCallIndirectComparisonRecGroupBoundary` (`:597`) — same shape, and the false positive it documented is now a refusal, so it asserts the right answer instead of the wrong one |

**What the implementation added that the ADR did not ask for**, both because a principal ordered it:
the birth requirement is discharged by asserting structural identity directly against
`MatchDefType`'s disagreement, and the mutation that *failed* to fire is kept as a measurement rather
than dropped — `internal/validate`'s ordinal-and-group-length condition, neutered, leaves
`internal/interp` green, because both fixture groups are length 2 with both ordinals 0 and that
condition never decides them. The discriminator is the cross-group refusal. *The condition a doc
comment finds easiest to name is not the condition the fixture turns on.*

**The `Status:` field is the one thing this PR could not do.** The stamp on the option exists as a
spoken review order; nothing durable holds it, and a self-authored relay of an approval is the
forged-provenance failure the Status law is written against — worse than a wrong option, because it
is a false statement about the project's own governance. So the field stays at `proposed` with its
implementation landed, which is an honest inconsistency rather than a hidden one, and the flip is an
ask in the implementation PR's **Decisions needed from Scott**.
