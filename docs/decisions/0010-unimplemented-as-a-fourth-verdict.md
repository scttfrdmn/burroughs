# 0010 — `unimplemented` as a fourth verdict, and the `(module quote …)` admission

Date: 2026-08-01 · Status: **accepted** (Scott, 2026-08-01 — option (a) for the admission
scope, then option 2 for the verdict question the admission's probe surfaced)

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
  replacement — the version scheme, not a second board.
- **My pre-registered `1345 → 1334` claim was refuted**, and separately, my own refutation
  figures were themselves off: 67 files not 68, 1229 newly-scorable not 1236 (I missed the
  7 bare `(module quote …)` forms), 26741 not 26742. Second-order honesty is a live
  discipline and not a slogan; the probe that corrected me is in this PR's history, not in
  its tree.
