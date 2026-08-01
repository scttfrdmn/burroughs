# 0004 — Engine versioning, and the contract's independent version

Date: 2026-07-30 · Status: **accepted** (Scott, 2026-07-30) · Resolves contract §10.7
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

## Status

Accepted 2026-07-30; amended 2026-08-01 (decision 0010, guard 4; addendum for guard 6).
Contract §10.7 is resolved by this doc; §10 open
questions remaining: 1 (resolved by 0002), 2, 3, 4, 5, 6.
