# 0093 — A speculative gate class for features ahead of the spec: never default-on, off the board, and labelled where it leaks

Date: 2026-10-04 · Status: **accepted** — Scott's stamp, *"Scott - stamp it"*, 2026-10-04, given on the
chair's summary of this record **with the ADR 0028 precedent correction included**. The stamp therefore
covers the text as corrected and nothing else; the correction was applied before the stamp was recorded,
and verifying it against ADR 0028's own text turned up no further change (see *The boundary case*).
`Ratio-Class: ordered` on the commit, as with ADR 0085's amendment.

**The stamp authorises the class only.** No speculative feature is approved by it. Shared-everything
threads needs its own ADR and registration, and nothing is built for it until that exists.

## Context

Burroughs gates every proposal (§9 G-1) and a gate's acceptance is **its upstream suite green**. That test
is the neutrality guarantee (G-3): partisanship lives in API surface and optimization priorities, never in
what the engine claims to conform to.

**Some features Burroughs wants have no upstream suite to be green against.** Two shapes:

1. **An unfinished proposal**, implemented from a draft before the draft is final — shared-everything
   threads is the immediate case, from its phase-2 draft.
2. **A Burroughs extension** the spec has not contemplated at all.

For either, G-1's acceptance criterion is not *unmet* — it is **unaskable**. There is no suite. A engine
that treated "no suite" as "no bar" would make the conformance board mean something weaker without
saying so, and the board's number is the whole of what "correctness-neutral" buys.

## The boundary case: ADR 0028 is *not* precedent for being ahead of the spec

**A first draft of this record cited ADR 0028 as precedent for shipping ahead of the spec. That was
wrong, and correcting it is what fixes the class's boundary.** (Correction: chat-Claude, before Scott's
stamp; verified against ADR 0028's own text and the flip's changelog entry, which support the corrected
account and required no further change.)

What ADR 0028 actually is:

* **Relaxed SIMD is a standard proposal and flipped through an ordinary G-1.** Its own suite is green —
  **77 pass / 0 fail / 0 unsupported / 0 gated** across the seven `*relaxed*.wast` files, identical on
  `darwin/arm64` and `linux/amd64` — satisfying G-1's *literal* reading and not invoking ADR 0025's
  carve-out at all. Nothing about the gate was speculative.
* **What exceeds the spec is the lowering choice.** ADR 0028's own title calls it *"a guarantee exceeding
  the spec"*, and Scott's ruling in it reads *"Where the spec permits a set, we pick once, uniformly, and
  write down why."* The proposal **permits a set** of results per instruction; Burroughs picks one
  deterministically and architecture-uniformly. Stricter than required — and **inside what the spec
  permits**.

So it is the **"Burroughs extension" kind in miniature**: a property chosen for this project's goals that
the spec does not promise. And it needed **no speculative gate** and **stays on the board**, because the
behaviour it chose is behaviour the spec already allows.

**That is the boundary G-5 draws.** Going beyond the spec's *guarantees* while staying within what it
*permits* is not speculative. G-5 applies only to behaviour the spec does not yet **define or permit**.

**It also already practises property 3's own-witness rule**, for a non-speculative feature: the
architecture-uniformity guarantee is held by `TestRelaxedLoweringChoicesArePinned`, because **no spec
vector can measure it** — a vector that permits a set cannot judge which member you picked. So the
mechanism G-5 requires of speculative features is one this project has run before; what G-5 adds is the
class, the never-default-on rule, the board exclusion, and the retirement plan.

**What my first draft confused it with** was a different ADR 0028-adjacent finding: that `CLAUDE.md` still
said every 3.0-feature gate was off after the flip, repaired at #464/#466. That is a **stale-prose**
finding — a sentence left standing after a stamped decision falsified it — and it is a good lesson about
closure conditions, but it is not about being ahead of the spec. Citing a record for something it did not
say is the defect the hedge-is-content rule exists to prevent, and it is recorded here rather than
silently fixed because the misreading is instructive: the two findings share an ADR number and nothing
else.

## Decision

A **speculative** gate class, distinct from G-1's, with four properties.

### 1. Never default-on while ahead of the spec

A standard gate flips when its proposal's suite passes. **A speculative gate cannot flip on that test,
because the test does not exist** — so it stays off, and becomes *eligible* for an ordinary G-1 flip only
when the spec catches up and a suite exists to judge it.

"Eligible", not "flipped": the arrival of a suite is the start of G-1's process, not a substitute for it.
And a flip remains behaviour 4's own stamp-tier event with a pre-registered forecast, exactly as for any
other gate.

### 2. One decision doc per speculative feature

Each carries its own, stating:

* **the goal it serves** — what the engine can do with it that it could not before, in terms a reader can
  check rather than a direction;
* **the draft or rationale it follows, at a pinned revision** — a URL and a commit, so drift is visible
  rather than inferred. A draft with no pin is a claim about a moving document;
* **what retires this**, as one of three named outcomes: **refactored** to match the shipped spec,
  **proposed upstream** and accepted, or **deprecated** because the spec went another way.

The retirement section is the load-bearing one. Without it a speculative feature is indistinguishable from
a permanent divergence that nobody decided to make, which is how an engine acquires a dialect.

### 3. Kept off the conformance board

Speculative behaviour gets its **own witnesses**, and **a check keeps it from counting toward the spec
numbers**. The board's total must keep meaning *"conforms to the spec"*; a speculative pass inflating it
would be the one number in this project that cannot be read at face value.

The check is part of the obligation, not an afterthought: a rule that nothing counts, enforced by nothing,
is the shape this corpus has paid for repeatedly. What it must *not* be is an exemption list, for the same
reason — the domain is derived (which gates are speculative) rather than enumerated per feature.

### 4. Guests that depend on it are labelled

A committed guest artifact built against a speculative gate is marked as such. **A Burroughs-only
artifact must not be mistakable for a portable one** — by a later reader, by a reproducibility check that
rebuilds it on another engine, or by a report that cites it as evidence of conformance.

## The two kinds, and their different costs

The decision doc must say which kind a feature is, because the liability differs and so does the exit.

| | an unfinished proposal, early | a Burroughs extension |
|---|---|---|
| the bet | a document that will change | a future the spec has not written |
| the cost | **tracking revisions** — the pinned revision is what makes drift visible | **reconciling with an unknown spec** later |
| the exit | the proposal ships; refactor to the shipped shape | **contribute it upstream**, which converts it into the first kind |
| what goes wrong quietly | the draft moves and the engine's claim silently ages | the dialect sets, and guests depend on it |

**For the second kind, contributing upstream is the point rather than a courtesy.** An extension nobody
has proposed is a liability with no retirement path, and the retirement section would have nothing true to
put in it.

## Consequences

**Costs.** A second gate class is a second thing to understand, and the prose in `CLAUDE.md` and §9 has to
stay consistent with it — precisely the kind of drift ADR 0028's aftermath shows this project is prone to.
The check in (3) is new instrument work charged to whichever speculative feature lands first.

**What this does not do.** It does not authorise any particular speculative feature; each needs its own
decision doc and, per behaviour 4, its own stamp-tier flip event when it is eligible. It does not weaken
G-1: a speculative gate is *outside* G-1's population, not inside it with a lower bar. And it does not
change the conformance board's definition — it protects it.

**Accepting this and then building nothing is a valid outcome.** The class costs nothing while empty, and
having it before the first feature is what prevents the reconciliation ADR 0028 required.

## Status and sequencing

**Held for Scott's stamp as a draft PR containing both the §9 G-5 text and this ADR, then stamped and
merged through the normal path.** It was not merged on a green build: §9 is normative text, which
`CLAUDE.md` forbids this agent from changing without his explicit sign-off, and a stamp cited to a green
CI run would be forged provenance about the project's own governance. The green is necessary and was never
sufficient.

**The order matters to what the stamp covers.** The chair asked for the ADR 0028 precedent correction, the
correction was made, and Scott stamped the corrected summary — so the stamp applies to this text and not
to the draft that misdescribed its own precedent. Had the correction turned up anything beyond what the
chair described, it would have gone back to Scott rather than ridden a stamp that had not seen it;
verifying it against ADR 0028's own text and the flip's changelog entry turned up nothing further.

Written during CI waits on the slices ahead of it, on the chair's instruction, so it cost no slice time.

**The first candidate is parallelism inside a component** — shared-everything threads, implemented early
from its phase-2 draft. The amendment is now stamped, so what gates that feature is **its own** ADR and
registration, not this one: a "what exists" recon at the draft's pinned revision, a decision doc carrying
the three required sections, and nothing built before both exist. **This stamp authorises the class and no
feature in it**, which is the distinction that makes an empty class a valid state rather than an unfinished
one.

**Not speculative, and named here so the two do not blur:** the multi-core scaling witness for core-module
guests. That measures what the engine already does against the shipped spec, and it rides the next Phase 4
slice as planned.
