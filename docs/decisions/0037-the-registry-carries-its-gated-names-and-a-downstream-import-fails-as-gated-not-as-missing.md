# 0037 — The registry carries its gated names, and a downstream import fails *as gated* rather than as missing

Date: 2026-08-18 · Status: **accepted, self-decided** — the shape of a harness-internal state slot,
which no principal has been asked about and no token is claimed for.

**What is *not* self-decided is the board-shape consequence**, and it is separated from the design
here rather than bundled with it: this change moves **62 rows out of the fail column** — 66 as
measured, see the scoring section, and the forecast below is left as written — which is a reward
figure produced by reclassification rather than by an engine getting anything right. It is
pre-registered below, before the code exists, and flagged in the PR that lands it. Scott can reverse
it by deleting one branch.

## Context

The run loop keeps three kinds of state about what a module command left behind, and until now only
two of them recorded gate declines ([#366](https://github.com/scttfrdmn/burroughs/issues/366)):

| slot | keyed by | gate state |
|---|---|---|
| `cur` | the most recent module command | `curGated` |
| `named` | the script `$name` | `namedGated` |
| `registry` | the *module name* an import asks for | **nothing** |

`registry` is written by the `register` arm, which already knows the answer: when its module was
declined it calls `r.gate(c)` — correctly, a decline is not a defect — and then **leaves the name
unbound**. Every later import against that name resolves against nothing, and the engine reports
`unknown import`, which is the truth about the resolver and a lie about the cause.

**Measured, and the figure is why this is the head of the exec slice rather than a tidy-up.** Of the
default lane's 81 exec-stratum fails, **62 are this defect** — each carrying
`interp: link failed: unknown import: …` where the reference expects `incompatible import type`:
`imports.wast` 35 (its auxiliary module carries `(tag …)`, declined under EH-off, so `(register
"test")` gates), `linking.wast` 13, `linking{1,2,3}.wast` 9 on `"Mm" "mem1"`,
`memory64-imports.wast` 4, `type-rec.wast` 1. In the all-gates-on lane the `incompatible import
type` key and the `Mm/mem1` key are **absent** rather than reduced — nothing declined, nothing
leaked, which is the independent confirmation that all 62 are gate consequences.

So the exec column, the one column whose fails are supposed to be the interpreter answering wrongly,
is three-quarters a mis-attribution. That is not a cosmetic complaint: it inverts what the board's
largest bucket recommends doing next.

### A second defect in the same arm, found while reading it for the fix

`register` is not idempotent in the grammar — the reference's semantics is last-register-wins. A
re-register of an already-bound name **whose new module was declined** currently leaves the *stale*
instance bound. A stale binding can satisfy an import and award a **pass for the wrong program**,
which is strictly worse than the mis-attributed fail that motivated the issue. It has no measured
witness in today's corpus; it is fixed here because the state that fixes the first defect is the
state that forecloses it, and leaving it would mean writing the invariant down as prose instead.

## Options

**A — per-entry gated state.** `registry` becomes `map[string]entry{in Instance; gated bool}`.
Honest, and the gated flag cannot drift from the binding because they are one value. But every read
site changes, the `spectest` pre-population changes shape, and the entry type crosses into the
engine-side adapter, which has no business knowing the harness's bookkeeping.

**B — a parallel set consulted at scoring time**, matching the engine's error text for the missing
name. Rejected outright: parsing an engine error message to decide a verdict is the wrong-layer
coupling this package exists to avoid, and the match would be against a sentence the engine is free
to reword. The injected-predicate seam (`IsGated`, `IsTrap`) exists precisely so the harness never
does this.

**C — a gated sentinel `Instance` bound under the name**, which #366 proposed as the option that
"makes the downstream vector's verdict correct rather than merely explicable". **It cannot be built,
and its correction is the useful part of this document.** `Instance` is an opaque interface the
*engine* owns; the harness has nothing to construct. Worse, anything it could bind is a thing the
linker may successfully **resolve against** — an import needing only that a name exist would link,
turning a mis-attributed fail into a false pass, which is the direction no board can see.

**D — a parallel gated-name set, threaded to the adapter, checked against the decoded module's
imports.** The harness owns names→instances *and* names→declined, and hands both across the seam it
already documents ("the harness hands over what it owns and the caller, which legitimately knows both
sides, builds the resolver"). The adapter has the decoded module in hand, so it reads the import
section, and on a hit returns an error wrapping `binary.ErrFeatureDisabled`. Every arm's existing
`isGated` path then does the rest, unchanged.

## Choice: D

It keeps option C's *effect* — the downstream vector fails **as gated**, so its verdict is correct
rather than merely explicable — without inventing an `Instance` the harness cannot own.

Two properties decided it over A:

**It is uniform over all three module forms.** The tempting cheap version derives the import names in
the *parser*, from `c.Source`, and gates before the verdict switch the way the `c.Needs` capability
gap already does. That is syntactically cleaner and it is **blind to exactly part of the population**:
`linking{1,2,3}.wast`'s 9 rows are binary-literal modules with no s-expression to read imports out
of. Checking after the decode covers every form because it acts at resolution time, not parse time.

**It stays within [#149](https://github.com/scttfrdmn/burroughs/issues/149)'s ruling that a
command's classification is a syntactic function of the command.** The gate here is a function of the
command's own module and of prior commands' text under the enabled feature set — no execution result,
no engine behaviour. That is the same dependence `curGated` already has when it gates the *next*
command, and this is that ruling applied to the third slot rather than a new commitment. Stated as an
extension, not as an approval: the citation is to the rule this follows, and #149 was not asked about
the registry.

## Consequences

**The gated-name set and the binding are mutually exclusive**, which is the whole of the second
defect's fix: a successful register binds the name and clears its gated mark; a declined register
marks the name **and deletes any binding under it**, so no stale instance survives to satisfy an
import against a module the reference would have replaced.

**Pre-registered forecast, written before the mechanism exists**, derived from the all-on lane rather
than from the change:

| figure | before | forecast |
|---|---:|---:|
| exec-stratum fail | 81 | **19** |
| total fail | 157 | **95** |
| gated | 4053 | **4115** |
| pass | 60868 | **60868, unchanged** |
| unsupported | 66 | **66, unchanged** |

The pass row is the one that matters and it is the one at risk: an `assert_unlinkable` whose expected
text happens to be `unknown import` is passing today **for the wrong reason** and would become
gated. The forecast is that there are none. **If any pass moves, that is a finding to report, not a
number to adjust** — it would mean the board holds passes awarded by a gate consequence, which is a
worse defect than the one being fixed and belongs in its own issue.

**The exec column becomes readable as a work plan**, which is the point. What survives is 19 rows,
and they are the interpreter: value mismatches in `linking.wast`/`linking3.wast`, two
`uninitialized element 0`, and the `type-equivalence`/`type-rec`/`type-subtyping` rows the all-on
lane already isolates.

**[#367](https://github.com/scttfrdmn/burroughs/issues/367) is unblocked.** Its own text says
scoring fact 3 before this lands would push gate consequences straight into the fail column; that
argument expires here.

**Rollback is one branch.** Deleting the adapter's import check restores today's classification
exactly; the gated-name set becomes unread state, and the control below fails, which is the honest
way for a reverted decision to announce itself.

**The control asserts the mechanism, not the count.** A row-count assertion would pass on a board
that reclassified the right number of rows for the wrong reason. The control is a script whose
registered module is declined and whose next module imports from that name: the second command must
score **gated**, and — the falsification that makes it worth writing — a variant whose register
*succeeds* must score a verdict, so the check cannot pass by gating everything.

## The forecast, scored — 2026-08-18

Recorded here rather than only in the PR because a pre-registration that is never scored is half an
instrument, and because both misses are more interesting than the hits.

| figure | before | forecast | actual |
|---|---:|---:|---:|
| exec-stratum fail | 81 | 19 | **15** |
| total fail | 157 | 95 | **91** |
| gated | 4053 | 4115 | **4124** |
| pass | 60868 | 60868 unchanged | **60863** |
| unsupported | 66 | 66 unchanged | 66 ✓ |

**Miss 1 — exec 15, not 19.** The forecast's population was derived by filtering the fail column on
the `unknown import` *message*, which found 62 rows. The mechanism keys on the *cause*, and four more
rows had the same cause with a different message (out-of-bounds memory and table accesses, where the
module linked against `spectest`'s memory instead of the one its declined dependency would have
supplied). **A forecast derived from a symptom under-counts a population defined by a cause** — the
number was right about what it measured and measured the wrong thing.

**Miss 2 — `pass` moved by 5, and the paragraph above said what that meant before the number
existed.** Five `assert_unlinkable` vectors (`imports.wast` :138/:297/:442/:540, `linking3.wast`:14)
assert that a module *lacks one export* and expect `unknown import`; they passed because the module
did not exist at all. Right text, wrong fact, and a substring match cannot tell them apart. Filed and
closed as **grave #408**, not adjusted — which is the whole reason the row was pre-registered as a
finding rather than as a tolerance.

**One further deviation, in the mechanism rather than the forecast, caught by the stratum ledger.**
The first draft charged the declined-import error to `StratumBinary`, and 13 rows went through the
module arms' *two decode paths disagree* branch, breaking a ceiling of 0. The tempting fix — exclude
gate declines from that branch — would have deleted a deliberate assertion whose own comment records
that `allOnLane` twice nearly shipped the defect it catches. The branch was right and the stratum was
wrong: a declined *import* is not a disagreement about decoding.
