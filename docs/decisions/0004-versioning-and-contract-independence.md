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

## Status

Accepted 2026-07-30. Contract §10.7 is resolved by this doc; §10 open
questions remaining: 1 (resolved by 0002), 2, 3, 4, 5, 6.
