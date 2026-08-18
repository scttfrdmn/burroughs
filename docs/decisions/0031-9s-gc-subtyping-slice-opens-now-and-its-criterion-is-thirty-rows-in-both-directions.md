# 0031 — #9's GC-subtyping slice opens now, and its criterion is thirty rows in both directions

Date: 2026-08-17 · Status: **accepted** — stamped by Scott on the PR #347 relay

> *"Go — as the slice, with the ADR first. It's the slice, and the twenty-one settle it. […] the
> ADR records the boundary move and its criterion — the thirty rows — not the design of the
> relation."* Deliberation is #343; this is its tombstone. The decision is stamped; the wording of
> this record is the agent's and is reviewable as any other.

## Context

`internal/validate` is built in numbered slices of #9, and the subtype relation is **declared out
of scope in two places** — which is why moving it is a decision and not a rule to implement:

- `stack.go:129`, `matches`'s doc comment: *"Everything else is identity, and that is slice 1's
  declared limit rather than an oversight: proper subtyping — the whole `match.ml` relation — is
  the GC slice's, and its vectors expect `sub type` rather than `type mismatch`."*
- `validate.go:63`, the out-of-scope register: *"each with its own expected string in the suite and
  so its own measurable slice: **GC subtyping (21)**, constant expressions (24), limits (16) …"*

Both were accurate. What changed is a measurement taken while costing #343's remaining causes: the
21 vectors are not *declined* for want of the relation, they are **admitted**. All 21 read
`assert_invalid accepted, expected: sub type` in the all-gates-on lane — the validator says yes to
twenty-one invalid modules, because `got == want` answers true often enough to accept them.

So the boundary is not holding a slice in reserve; it is holding twenty-one false accepts, and the
nine over-rejections tracked as #343 are the same missing relation seen from the other side.

## Decision

**Open the GC-subtyping slice now, ahead of its declared position, and retire both boundary
statements.** The alternative considered and rejected was to repair #343's causes 1, 3 and 4 within
the existing boundary. That is a half-port by construction: it fixes the nine over-rejections using
a relation the 21 admissions are the only witness for, so it lands the accept direction while
leaving the reject direction red, and nothing on the board would say the relation was wrong in the
direction that accepts invalid modules.

**Not decided here, deliberately:** the design of the relation. Porting `match.ml` is normative
reference behaviour, and where the `context` comes from, whether canonicalization is memoized, and
what file it lands in are implementation shape recorded in comments at the site — the same
treatment every other ported validator rule has had.

## Criterion

Thirty rows, pre-registered in both directions, and the slice is not done until both move:

| direction | population | now | required |
|---|---|---|---|
| reject | 21 (`type-subtyping.wast`) | `assert_invalid accepted` | **pass** |
| accept | 9 (`moduleOverRejections`) | `over-rejected` | **pass** |

**The reject side's target is `pass`, not `refused`.** Those vectors expect the message `sub type`;
a relation that refuses them with `type mismatch` moves them from `admitted` into the
**wrong-message** bucket, which today has two inhabitants (#346). That is a real improvement scored
as a lateral move — Scott, on the same relay: *"a lateral move into a two-inhabitant bucket would
read as a small improvement while a real one had happened."* So the criterion is a string as much
as a count.

## Consequences

- **The two declared boundaries retire, and the retirement is recorded rather than absorbed** —
  same species as 0025's carve-out. `matches`'s doc comment and `validate.go:63`'s register are
  amended in the implementing PR, with the prior text quoted where it stood.
- **`GC subtyping (21)` leaves the out-of-scope register**, which drops from six entries to five.
  The register's remaining figures are unaffected: the populations are disjoint by construction,
  each being defined by its own expected string.
- **This is the campaign's first artifact with free coverage in both directions.** Every prior
  reward figure has been reject-direction only, or accept-direction witnessed by an oracle #341 had
  to build first. Here the suite supplies both, so no hand-written reject-direction unit witness is
  needed and a half-port cannot pass.
- **A permissive relation is the failure mode with teeth, and it is bounded by the 21.** The risk is
  asymmetric: too-strict shows up as new over-rejections in a table that is checked in both
  directions, too-permissive shows up as those 21 staying admitted.

## Falsified by its own implementation — the last bullet, 2026-08-16 (#351)

Recorded rather than corrected, because an accepted record's claims are testimony and the honest
move is to say which one the work refuted. The sentence is *"it is bounded by the 21."* It is
**false**, and the criterion's own thirty rows cannot see the case it misses.

The relation the 21 witness is *equi-recursive* type equality — bisimulation over type indices —
which is strictly **coarser** than the spec's iso-recursive equality: it accepts modules the
reference rejects. Every vector that discriminates the two is a `type mismatch` admission in
`type-rec.wast` (`:51,59,93,103,114,124,204,216`), and all eight are admitted **before and after**
this slice, because each puts its grouping-sensitive reference in a `(global (ref $ft) …)`
initializer whose type check is a separate deferred rule. So a bisimulation port would have
satisfied all thirty rows, passed the criterion, and been wrong in exactly the direction the
criterion was built to bound.

Two things follow, and only the first is about this ADR:

- **The claim should have been "bounded by the 21 *plus a claim about the representation*."** What
  actually caught the coarseness was not a vector: it was `match_deftype`'s second disjunct being
  unportable against a flat comptype list, which forced `binary.CompType.RecStart`/`RecLen` and made
  the rolled comparison explicit. A representation that *cannot* express the wrong relation is a
  stronger bound than a population that happens to exclude it — and the wrong relation here was the
  easier one to write.
- **The eight are a named blind spot, not casualties.** They are neither guards nor regressions;
  they are rows the criterion counted on and could not deliver. Whatever slice lands the global
  initializer's type check inherits them as its own reward figure, and inherits the discrimination
  they were supposed to perform here.

*A suspiciously clean result is a tell*, and "the criterion is exactly the population that could
witness the risk" was one. The prediction that the eight *currently pass* was also made, in a
comment, and was wrong in the same direction — measured, then corrected before landing.

## The falsification above was itself too pessimistic — 2026-08-18 (#402)

Appended on the same ground the section above gives: an accepted record's claims are testimony, and
that includes the claims made *while* correcting it. Two sentences of the 2026-08-16 section are
false, and this time the error is in the safe direction — the risk was better defended than the
correction said.

**"Every vector that discriminates the two is a `type mismatch` admission in `type-rec.wast`."** It is
not. Replacing `sameDefType`'s ordinal-and-group-length condition with `if false` and reading the
all-gates-on lane costs six rows, and one of them is in `tag.wast` — an `assert_unlinkable` whose tag
import differs from the export only in grouping, so the coarser relation calls the two compatible and
the module links. It dies to that neuter with grave #402 applied and without it, so it was never
blocked behind the global initializer's check or behind anything else. Two further `type-rec.wast`
rows were also already live.

**"What actually caught the coarseness was not a vector."** A vector did, in a stratum the search did
not cover: `tag.wast` reaches the rule through **linking**, not validation. The enumeration was by
file and by expected text (`type mismatch` admissions), and both filters were blind to it.

What survives is the first bullet's reasoning, strengthened rather than weakened: the representation
*was* the stronger bound, and it is now also the one with a machine check. What does not survive is
the second bullet's premise that the eight were the corpus's only route to the property. Three of
them — `:51`, `:204`, `:216` — were blocked on something the section never names, `inlineFuncType`
handing an inline signature a member of a multi-member rec group, and they convert with grave #402;
the other five converted with #328. So the eight are spent, and they did perform the discrimination
they were kept for: with the condition neutered, exactly those rows revert to admissions.

The lesson is one the corpus keeps re-teaching in new clothing: **a search for witnesses that
enumerates files enumerates the strata you already thought of.** The falsifier — neuter the line, read
the board — is what finds the ones you did not, and it is cheaper than the argument for why none
exist.
