# 0010 — `unimplemented` as a fourth verdict, and the `(module quote …)` admission

Date: 2026-08-01 · Status: **accepted** (Scott, 2026-08-01 — option (a) for the admission
scope, then option 2 for the verdict question the admission's probe surfaced); **amended
2026-08-01** (chat-Claude, PR #58 — guard 6, the retirement condition)

## Decision

**Two changes, one PR, because neither is honest alone.**

1. **The harness admits the `(module quote …)` form.** `classify` recognizes it — bare and
   under `assert_malformed` — so the derived corpus (#52) admits the 54 files that hold
   nothing else scorable. The unsupported ceiling rises **1345 → 26742**, and that rise is
   *corpus admitted*, never regression.
2. **A fourth verdict, `unimplemented`,** for a command the harness can ask but the engine
   has no named component to answer. The 1236 newly-admitted quote vectors land there
   rather than in `fail`.

## Question

The admission was ruled first, on its own merits: the alternative was enumerating the
quote files a lexer could already answer, which is the `phase1Files` defect (#52) re-hired
under a new name — selection by convenience with a blind spot nobody measures.

What the ruling was not given, because it had not been measured, is that *admitting the
form does not determine what verdict its vectors receive.* Probing with the real
classifier rather than a grep found two readings:

| reading | pass | fail | unsupported | gated |
|---|---|---|---|---|
| **A** — quote scored; no wat reader, so every one fails | 783 | **1237** | 26742 | 15 |
| **B** — quote recognized but left unsupported | 783 | 1 | 27978 | 15 |

**B is a no-op**: a file whose every command is unsupported is not admitted, so B admits
nothing and changes no number. The choice was therefore A or a new category.

## Why not A — the fail column's signal is the board's most valuable property

The deciding argument is not `gated`'s precedent. It is what `fail` currently *means*.

Today the column means **defect**. The board's lone failure — `binary-gc.wast:1`, a
`malformed function type: 0x5e` under an expected "malformed mutability" — is visible
precisely *because* the column discriminates wrong-answer from not-built. Reading A takes
that column to 1237, of which 1236 are one missing component, and a genuine regression
landing tomorrow would arrive as 1238: invisible.

A column that cannot surface a new defect has stopped being an instrument. That is the
same failure as a wall of lint findings nobody reads (decision 0005) — dishonesty by
volume rather than by omission, and it trains the reflex of scrolling past.

`gated`'s precedent then confirms rather than carries it. `Gated` exists because scoring an
unanswered question as a failure "marks correct behaviour red" (wast.go, on
`binary_leb128_64.wast`). 1236 vectors the engine cannot yet *read* are the same situation
with a different cause: `gated` is absence-by-configuration, `unimplemented` is
absence-by-construction. Same architecture — carve-out, guard, lane.

## The distinction, stated where it can be found

Recorded here and at the verdict definitions, because if this sentence blurs the categories
merge back into mush:

- **`unsupported`** — *the harness cannot ask.* No `Kind` recognizes the form; there is no
  question, only a directive the oracle does not parse into one.
- **`unimplemented`** — *the harness asked and the engine lacks a named component to
  answer.* The question exists, is well-formed, and has a registered capability standing
  between it and a verdict.
- **`gated`** — the engine *could* answer but a feature gate is off, so it declined.
- **`fail`** — the engine answered, and the answer was wrong.

## Guards, which are conditions of the ruling and not suggestions

**1 — Capability-derived, never hand-assigned.** `classify` computes what a command
*needs*; the harness declares what the engine *has*; the gap is the verdict. There is no
per-vector allowlist, and at 1236 vectors there could not be one — that is `gated`'s
mechanism at a scale where it stops working. This is the reflection-over-`Features` move
(`allFeaturesOn`): coverage grows with the registry rather than with an editor's memory.

**2 — A closed capability registry, each entry bearing its tracking issue.** A vector may
be `unimplemented` only *via a registered capability*. An unregistered gap is a
classification failure, loudly — `TestEveryNeededCapabilityIsRegistered`. `wat-reader` is
the first and today the only citizen, tracked at #53. This is the
design-debt-needs-a-tripwire rule (0006) applied to a verdict: the category is a debt
discharged by a control, never by an intention.

**3 — The partition is asserted, not assumed.** `TestVerdictsPartitionCommands` gains the
fourth term. Adding a verdict is exactly when vectors go missing, since a command falling
through every branch simply vanishes.

**4 — Version honesty extends to the new column (decision 0004).** *No minor version is
cut while its milestone's `unimplemented` is nonzero, and `v0.1.0` requires zero.* The
version number is a conformance statement; a release claiming MVP core green with 1236
questions unanswered would be a mood. This is the permanence-prevention: the category
exists to **drain**, and the version scheme is what enforces the draining rather than
trusting it.

**5 — The account is written in the same PR as the change.** Both numbers — the ceiling
jump *and* the birth of 1236 `unimplemented` — named in `CHANGELOG.md` and in the board
note, so the 20× step reads as corpus-admitted to a reader who was not in the session.
That reader is who the account is for.

**And a ceiling, mirroring the unsupported one.** `unimplementedCeiling` may only fall.
Its purpose is the drain: when the wat reader lands, these 1236 convert to pass/fail in a
movement the board *shows*, which is what a board is for.

## Consequences

- Board after admission: **68 files, 28777 commands — 783 pass, 1 fail, 15 gated,
  26742 unsupported, 1236 unimplemented.** The partition sums, which is asserted.
- `fail` stays at **1**, and therefore stays an instrument.
- The 555-vector `unknown operator` bucket now has somewhere to be counted *from*: it is
  555 of the 1236, and the extracted keyword table (0009) is what it will earn against.
- **Six quote modules stay `unsupported`**, wrapped in `assert_invalid` rather than
  `assert_malformed`. That is correct and worth stating: the harness cannot ask a
  validity question at all yet, so the capability gap is not what stands in the way.
  A vector must not be `unimplemented` for the *wrong* reason.
- The default lane's `Run` (no capabilities declared) scores every quote vector
  `unimplemented`. There is deliberately **no** all-capabilities-on lane analogous to
  `TestAllGatesOnLeavesNothingGated`: turning on a component that does not exist is not a
  configuration change, so the structural control cannot be a lane. Guard 4 is its
  replacement — the version scheme, not a second board. *(Amended: guard 4 is not the only
  replacement. See the amendment below — a second control exists, and it is temporal rather
  than spatial.)*
- **My pre-registered `1345 → 1334` claim was refuted**, and separately, my own refutation
  figures were themselves off: 67 files not 68, 1229 newly-scorable not 1236 (I missed the
  7 bare `(module quote …)` forms), 26741 not 26742. Second-order honesty is a live
  discipline and not a slogan; the probe that corrected me is in this PR's history, not in
  its tree.

## Amendment, 2026-08-01 — guard 6: entries are born with their retirement conditions

Appended rather than rewritten, per *a ruling is discharged by appending to the ADR, body
preserved*. One sentence above is amended in place with a forward pointer, because leaving it
unqualified would be the orphaned-prose defect: the consequence list said guard 4 was the
replacement for the missing lane, and the ruling below makes that only half true.

### What the original guards missed

Guard 2 fences the registry's *membership* — a capability must be registered, and its entry
must bear an issue. Nothing fenced its *lifetime*. An entry could be correct on the day it was
written, the component could land, and the entry could simply stay: a capability with no
population and no retirement, which is a squatter. Worse, the component could land while
leaving some of its vectors in the fourth column, and no control would have said so — the
deferral would have become the disappearance the whole ruling exists to prevent.

`gated`'s guarantee comes from a lane (#27): every gate on, gated count zero, so a vector
parked in the third verdict is simultaneously being failed somewhere. The consequence list was
right that this cannot transfer — you cannot enable a component that has not been written —
but wrong to conclude that the only substitute was the version scheme. **The guarantee can be
delivered temporally instead of spatially:** where `gated`'s control asks *what happens under a
different configuration*, this one asks *what must be true when the thing arrives*.

### Guard 6

> **A registry entry states, at birth, the condition under which it must be deleted.** An
> entry may not outlive its component. A capability the engine declares must no longer be
> registered **and** must have drained its population to exactly zero; each half alone is a
> defect. An entry with no retirement condition is refused.

Mechanically: `capEntry` carries `Retires` beside `Issue`; `engineCapabilities` declares what
the engine has, explicitly rather than by omission, because guard 1 makes the engine's half a
*declaration* and an absence cannot be read as a claim; `RunGated` derives from that
declaration, so the board cannot drift from it; and
`TestNoCapabilityOutlivesItsComponent` fails in both directions, with a vacuity floor on the
registry — the control compares two sets and one of them is empty by design today, so the
registry is what must be non-empty for the comparison to assert anything.

The run loop refuses an entry with an empty `Retires`, panicking rather than counting. That is
the same shape as guard 2's refusal of an unregistered capability, and for the same reason: a
column that grows by omission grows without a decision.

### Why this is the right shape for this category

The category describes components that do not exist yet, so every guard on it is necessarily a
claim about the future. Guard 4 constrains *releases* (no minor while the count is nonzero),
guard 6 constrains *arrivals* (nothing lands leaving its column populated). Together they close
both ends: the debt cannot be released around, and it cannot be abandoned mid-payment.

`CapWatReader`'s condition, recorded on the day the entry was created: retire when a wat reader
is wired and `engineCapabilities` declares it, in the same commit, with
`unimplemented(wat-reader)` at 0. This makes #53's definition of done machine-checked rather
than reviewer-checked — the pre-registered-failing-test discipline (0006) applied to the
capability's *end* as well as its beginning.

Four falsifications, each introduced and watched fail: declaring the capability while leaving
the entry, a reader landing with 1236 still outstanding, the registry emptied without draining,
and an entry written with no retirement condition.

---

## Postscript — the first retirement, executed (2026-08-01, PR #61)

Appended rather than edited in place: the body above is the record of what was decided
before the reader existed, and what the guards *predicted* is the part worth keeping
alongside what they *did*.

`CapWatReader`'s stated condition was met in one commit. `engineCapabilities` declares it,
the `capabilityIssues` entry is deleted, and `unimplemented(wat-reader)` is **0** — 1236
vectors converted, none left behind. The board moved 783/1 → **1419/601**, which is the
largest single movement since the harness's genesis and every number in it was forecast
before the code existed.

**What the guards caught, which is the reason to record this at all.** Guard 6 was written
as a claim about a future arrival, and three of the controls it names failed on the
arrival — not because the retirement was wrong, but because *each of them had encoded the
pre-retirement state as an invariant*:

- `TestEveryNeededCapabilityIsRegistered` required every needed capability to hold a
  registry entry. `wat-reader` is needed by 1236 commands, has no entry, and that is the
  success condition. The invariant it *meant* was **accounted for — as a tracked debt or
  as a declared component**, which is stronger than the old reading rather than looser,
  because the two arms are exclusive. Before the retirement the two readings agreed on
  every input, so nothing could distinguish them.
- `TestNoCapabilityOutlivesItsComponent`'s vacuity floor read "the registry must be
  non-empty, because the engine's set is empty by design." The retirement inverted both
  clauses, and left as written the floor would have `Fatal`ed on the state it exists to
  certify — *a control asserting the absence of its own success*. Re-floored on `engine`,
  the set its two loops actually iterate.
- The run loop's guard-2 message told an under-declaring caller to "register it in
  `capabilityIssues`" — advice that would reinstate a debt that had just been paid. A
  third case now names the retirement instead.

That is *a ruling retroactively falsifies prose written before it* with a retirement in
the role of the ruling, and it is the reason the sweep is part of accepting one. The
generalization for the next capability: **a lifecycle guard written while its subject has
only ever been in one state will encode that state**, and the arrival is when you find
out. Not one of the three was wrong when written; all three were narrower than their own
names.

Guard 6's own verdict: it worked. Every one of the three was found by a *failing test* on
the retirement commit, not by review.

## The fail column's split, which the postscript above made necessary

The retirement raised `fail` 1 → 601, and the body of this ADR forbids exactly that shape
of number: a column of 601 in which 1 is a decoder defect cannot surface the 602nd.

The resolution is not a fourth verdict for the text layer — the lexer *exists*, so its
vectors have no excuse left, and parking them would be the disappearance guard 6 prevents
one layer up. It is that **the ceiling is partitioned where the column is**:
`Failure.Kind` splits `binaryFail` (ceiling 1, `binary-gc.wast:1`) from `textFail`
(ceiling 600, the work plan for #8's parser and the validator). A new decoder defect
arrives as `binaryFail 2 > 1` no matter what the text column does.

The partition key is structural and not the bucket string, because the two layers **share
strings** — `malformed UTF-8 encoding` is a bucket on both sides, 10 lexer vectors and 176
parser ones. *When a partition's members share a value, an equality on that value is not a
partition check* (grave #34). Falsified by swapping the arms: `binaryFail` reads 600,
`textFail` reads 1, and both ceilings fail.
