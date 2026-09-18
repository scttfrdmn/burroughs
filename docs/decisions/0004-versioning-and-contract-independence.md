# 0004 — Engine versioning, and the contract's independent version

Date: 2026-07-30 · Status: **accepted** (Scott, 2026-07-30) · Resolves contract §10.7 · **amended
2026-08-01 — guard 4 is a release gate** (decision 0010, guard 4; addendum for guard 6) · amended
2026-08-28 (the table's numbering overtaken by events) · §10 open questions remaining **as recorded
2026-07-30**: 1 (resolved by 0002), 2, 3, 4, 5, 6
Contract refs: §10.7 (open question), §9 (gates, conformance)

## Decision

**Two version numbers, deliberately independent, joined by a changelog
statement.**

1. **The engine follows Semantic Versioning 2.0.0.** Go's module system is
   SemVer-native, so the two agree by construction.
2. **The contract versions independently** (currently v0.1).
3. **Every release's changelog states which contract version it
   implements.** Engine SemVer governs *code compatibility*; the contract
   version governs *semantic promises*. That split is the resolution to
   §10.7 — the contract does not track WASI point releases, and it does not
   track the engine's tags either.

### The version number is a conformance statement, not a mood

Minor versions map to milestones, so the number says what is *green*:

| version | means |
|---|---|
| `v0.0.1` | scaffold: contract adopted, decoder, CLI — no suite run |
| `v0.1.0` | the MVP core suite goes green |
| `v0.2.0` | one proposal gate flipped (`+GC`), and one minor per gate after |
| `v1.0.0` | **reserved**: the v1 threads-and-safepoints milestone lands *with the §4 litmus battery passing on both TSO and a weakly-ordered platform* |

*The `v1.0.0` row is **amended below** (2026-09-18, #795): the requirement as written is satisfiable without doing what it reads as demanding, and is restated there. Read the amendment with the row.*

Living in `v0.x` is a privilege, not an embarrassment: no compatibility
promise, no `/v2` import-path dance, total freedom to break — exactly right
for an engine whose contract is still v0.1. `v1.0.0` is therefore gated on
the contract stabilizing (§1 non-goal 4: harden when the contract is
stable), not on the code feeling finished.

A `v2+` major would require a `/vN` module path suffix per Go's rules. Not a
near-term concern; recorded so it is not a surprise.

### Keep a Changelog composes with PR-as-report for free

`CHANGELOG.md` is a typical repo file, so it survives the
no-markdown-proliferation rule. The mechanism:

- A PR description's **Landed** section is already a changelog entry wearing
  a different hat. Update `[Unreleased]` **in the same PR**, categorized per
  the spec (Added / Changed / Deprecated / Removed / Fixed / Security).
- **Graves land under Fixed**, linked to their `type:grave` issues — so the
  changelog and `label:type:grave` agree rather than drifting.
- **Gate flips land under Added** with the `gate:` name, which is what makes
  the minor-version bump self-documenting.
- **Cutting a release is one motion:** close the milestone, move
  `[Unreleased]` under a new `## [X.Y.Z] - YYYY-MM-DD` header, tag `vX.Y.Z`
  signed. Three systems — milestones, changelog, tags — clicking as one
  mechanism.

## Consequences

1. `CHANGELOG.md` carries a `v0.0.1` entry recording the scaffold state
   retroactively, so the history is honest from the genesis commit forward
   rather than starting mid-story.
2. Every version header names its contract version.
3. No tag is cut without the milestone closed and the suite counts real —
   "the suite is the oracle" applies to release notes too. A version number
   that claims a gate is green when its suite was not run is the same class
   of dishonesty as an unreachable error constant (see #3).

## Amendment, 2026-08-01 — the fourth verdict is a release gate

Appended rather than rewritten, per *a ruling is discharged by appending to the ADR,
body preserved*. Nothing above is retracted; this adds a term the table could not
have named because the verdict did not exist.

Decision 0010 carves `unimplemented` — a command the harness *asked* and the engine
has no registered component to answer. It is the fourth verdict, and unlike `gated` it
has no configuration that makes it go away: the component either exists or does not.

**Guard 4 of that ruling is a versioning rule, so it is recorded here:**

> No minor version is cut while its milestone's `unimplemented` count is nonzero, and
> **`v0.1.0` requires it to be zero.**

The reason is the row already in the table above. `v0.1.0` means *the MVP core suite
goes green*, and a release claiming that with 1236 questions unanswered would be a
mood — precisely what "the version number is a conformance statement" forbids. The
same reasoning as consequence 3: a number claiming a gate is green when its suite was
not run is dishonest, and a number claiming a suite is green when 1236 of its vectors
were never asked is the identical error one column over.

This is also what stops the new verdict becoming permanent. `unsupported` may sit at
26742 indefinitely — it is a corpus fact, not a debt. `unimplemented` is a debt, so it
gets a mechanism that will not let a release paper over it: the category exists to
**drain**, and the version scheme is what enforces the draining rather than trusting
it.

Board at the time of this amendment: 1236 unimplemented, all of them waiting on the
wat reader (#53), which is therefore a `v0.1.0` blocker by this rule.

**Addendum, same day (PR #58):** this rule is one of two ends, not the whole
mechanism. 0010 gained a **guard 6** — a registry entry states at birth the condition
under which it must be deleted, and a capability the engine declares must have drained
its population to exactly zero. Guard 4 here constrains *releases*: the debt cannot be
released around. Guard 6 constrains *arrivals*: it cannot be abandoned mid-payment by a
component that lands and leaves vectors behind. Recorded here because a reader arriving
at this section to cut a release should know the count they are checking is also
defended at the other end, and not conclude that the version gate is the only thing
standing between the column and permanence.

## Amendment, 2026-08-28 — the table's numbering was overtaken by events, and the table is a map a reader will follow

**`v0.1.0` and `v0.2.0` were never cut.** The SIMD gate flipped before the GC gate did, so the project
released `v0.3.0` straight from `v0.0.1`, and the mark this table calls `v0.1.0` — *the MVP core suite
goes green* — shipped on 2026-08-28 as **`v0.4.0`**, because SemVer does not go backwards. The table
above is kept as written: it records what the scheme intended, and the intent is still the right one.
What it can no longer be used as is a lookup from a number to a meaning, which is what a reader arriving
here would use it for — so the meaning now travels in the changelog entry, and each release states its
own conformance claim in the terms this doc requires. Two milestones, `v0.1 wat parser` and `v0.2.0 GC
gate`, are still named for versions that were never released; they are renamed when their work is next
touched rather than by tracker surgery on release day (Scott, on the v0.4.0 release).

---

*This document carried a trailing `## Status` section until 2026-08-29. Its content — the
acceptance, both amendment dates, and the §10 open-questions list — was merged into the header
`Status:` line above and the section removed
([#520](https://github.com/scttfrdmn/burroughs/issues/520)). That issue's specimen is this document:
guard 4 is a release gate, and its statement lived where no sweep and no first reader looks. The
open-questions list is carried **as it was recorded** rather than brought up to date, because
correcting it is a measurement of §10's current state and not this repair's business — a reader who
needs today's list should count it, not read it here.*

## Amendment — `v1.0.0`'s requirement, restated to what is checkable (2026-09-18)

**Approved by Scott on [#795](https://github.com/scttfrdmn/burroughs/issues/795) ("I concur and approve the recommended path") — Option 1 with Option 3's enumeration folded in as the recorded reason.** The row above is superseded by this restatement; it is left visible because what was wrong with it is the point.

### Why the original requirement could not stand

*"The §4 litmus battery passing on both TSO and a weakly-ordered platform"* is **satisfiable as written while misleading as read** — a worse failure than a requirement plainly unmet, because nothing flags it. CI runs both memory models by design (`ubuntu-24.04` x86-64/TSO and `ubuntu-24.04-arm` AArch64/weakly-ordered, contract §9/G-1) and the battery passes on both, so the sentence can be reported met, truthfully. But measured across the 15 registered cases: **10 carry arbiter `neither`** — platform-independent scheduling/protocol claims that pass identically on one platform, so the second runner adds nothing to them; **5 carry `-race`** — Go's memory model, not the guest-visible hardware model; and **0** discriminate a hardware weak-memory outcome by value. A reader takes the sentence to mean *two memory models certify the boundary*; the battery does not deliver that.

### The restated requirement

**`v1.0.0` is reserved for the v1 threads-and-safepoints milestone landing with:**

1. **every registered §4 litmus case either landed or explicitly deferred with a live expiry** naming what would unlock it;
2. **the battery run on both a TSO and a weakly-ordered platform** — a portability and race-freedom requirement, which is what that run actually establishes;
3. **the tier's memory-model reach stated at its measured strength** wherever the version's coverage is claimed; and
4. **the contract stable** (§1 non-goal 4), unchanged from the original second conjunct.

### What is being given up, and what would restore it

Named here so the restatement cannot read as moving the goalposts — the thing conceded is in the same amendment that concedes it:

**Given up:** the reading that `v1.0.0` certifies the guest-visible memory model at the host→guest boundary on two platforms. The threaded tier's evidence is **one discriminating witness** (B-MM-2, the guest→guest wake — a control that dies), the host→guest boundary **certified by construction with a witness** (B-MM-1's async-wake crossing structural-by-identity, #780; Resume-after-Stop structural-by-redundancy at SP-6, #779), and **B-MM-1's host-call-return crossing an open hole with no available instrument** (#742).

**What would restore it** — any one of these, and each is outside this project's unilateral reach:

- **A second independent engine** implementing the threaded ABI. Blocked by **D-1**: no `thread.spawn` in the shipped Canonical ABI, so no engine to differ from.
- **Hardware or a runner that discriminates reliably.** Measured on arm64: the forbidden outcome appears at ~1e-5 and flaky — **zero in 2 of 5 trials** — below any floor a case could carry (#742 Q3).
- **A different instrument.** The #742 recon's conclusion was that no cleverer case closes it: every boundary crossing's acquire edge is **inherent to the mechanism performing the crossing**, so there is no deletable line whose removal opens a value window while leaving the crossing functional.

If any of the three arrives, this amendment is the record of what it would upgrade.

### Why the contract's amendments are not evidence of instability

The second conjunct asks for a stable contract. The contract carries **nine amendment markers across three stamp events** (#694 2026-09-10; #737 and #743 both 2026-09-12), plus #784's §7 correction (2026-09-18). **Each names what forced it**, and each was forced by work that found something rather than by churn — which is evidence the contract is being *tested*, not that it is unsettled. Stability is judged on whether new work still forces amendments, not on the count of those already taken.
