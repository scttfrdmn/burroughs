# CLAUDE.md — Burroughs

You are taking over **Burroughs** (burroughs.run): a WebAssembly runtime in
pure Go, language-directed for Go itself. The B5000 favored ALGOL; Burroughs
favors Go. This scaffold was set up by chat-Claude with Scott; you are the
implementation agent.

## The law

`docs/burroughs-contract-v0.1.md` is **normative**. Read it before writing
any code. Every design choice is measured against it. You MUST NOT edit the
contract's normative text (§§0–9) without Scott's explicit sign-off; §10
open questions are resolved *by decision doc* (see Disciplines), never
silently in code.

Posture: **correctness-neutral, performance-partisan** (contract §0). The
upstream spec test suite is the neutrality guarantee; partisanship lives in
API surface and optimization priorities only.

## Phase ladder

- **v0 — interpreter.** Decoder → validator → interpreter over an
  internal-form rewrite (decision 0002), Wasm MVP core suite green with
  3.0-feature gates present and off. No compiler. Correctness and
  spec-tracking agility are the product.
- **v1 — threads + safepoints.** Contract §§2–5: OS-thread spawn, futex
  wait/notify, engine-native epochs/STW, boundary memory model (§4) with
  its litmus battery.
- **v2 — stack switching.** Contract §7: growable continuations, morestack
  analog.
- **v3 — component model + WASI 0.3.** Contract §6.

Current phase: **v0**. Do not reach ahead of the phase without a decision
doc approving it.

## Where the work is tracked

**GitHub is the tracker.** The repo's markdown footprint is frozen at
standard repo files; project state lives in issues, milestones, and PRs.

- **Milestones = the phase ladder.** `v0 interpreter`, one `v0.x` milestone
  per proposal gate (GC, EH, tail-calls, memory64, SIMD), `v1 threads +
  safepoints`, `v2 stack switching`, `v3 component model + WASI 0.3`.
  Every issue attaches to a milestone. The ladder does not live in a file.
- **Issues replace the queues.** Harness phases, decoder sections, contract
  §10 open questions, CI gaps — each is an issue.
- **Labels, kept small:** `phase:v0`…`phase:v3`, `gate:<proposal>`,
  `type:decision`, `type:grave`, `type:harness`, `type:contract`, and
  **`decision-needed:scott`**. That last label, assigned to Scott, *is* the
  old reports' "Decisions needed" section — now queryable.
- **Graves are closed issues** labeled `type:grave`, lesson in the closing
  comment, and a comment at the fix site citing the issue number.
  `label:type:grave` is the graveyard; there is no markdown registry.

Do not reintroduce `PROGRESS.md`, `docs/reports/`, or any status file.

## Reporting protocol — the PR *is* the report

Work happens in PRs, even self-merged ones. The PR description carries
exactly these sections: **Board** (suite counts, build status) · **Landed** ·
**Decisions taken** (with ADR links) · **Decisions needed from Scott** ·
**Graves** (bugs found, lessons) · **Next**.

Two principals review: **Scott** (owner, all decisions) and **chat-Claude**
(contract author, architecture review). Scott reviews in the GitHub UI and
relays to chat-Claude; directions come back through Scott. If a PR would
change the contract, say so explicitly in **Decisions needed** and label the
issue `type:contract`.

Keep descriptions terse and factual — written for a reader who wasn't in the
session. Anything Scott must decide is *flagged*, never decided for him.

## Versioning and the changelog

See **decision 0004** for the full scheme; the short version:

- **Semantic Versioning 2.0.0** (semver.org), which Go's module system is
  native to. Public API is everything exported outside `internal/`.
- **The version number is a conformance statement, not a mood.** Minor
  versions map to milestones: `v0.1.0` = MVP core suite green, one minor per
  proposal gate flipped (`v0.2.0` = +GC), `v1.0.0` **reserved** for the v1
  threads-and-safepoints milestone landing *with the §4 litmus battery
  passing dual-platform*. Never bump a minor for a gate whose suite you did
  not run.
- **`v0.x` is a privileged place to live** — no compatibility promise, no
  `/vN` import-path dance, freedom to break — and it is the right place while
  the contract is still v0.1. A `v2+` major would need a `/vN` module path
  suffix.
- **The contract versions independently** and every release states which
  contract version it implements: engine SemVer for code compatibility,
  contract version for semantic promises (resolves contract §10.7).
- **Keep a Changelog 1.1.0** (keepachangelog.com). `CHANGELOG.md` is
  maintained by hand, newest first, with an `## [Unreleased]` section at the
  top and the standard groups — **Added · Changed · Deprecated · Removed ·
  Fixed · Security**. Entries are written for humans reading the project, not
  copied from `git log`.
- **A PR's Landed section is a changelog entry wearing a different hat.**
  Update `[Unreleased]` **in the same PR** as the change — the two are the
  same information, so they cannot be allowed to drift.
- Two project conventions, because they are what this project actually ships:
  gate flips (a proposal's suite going green) are **Added** with the `gate:`
  name, and graves are **Fixed** with their `type:grave` issue link — so the
  changelog and `label:type:grave` agree.
- **Cutting a release is one motion:** close the milestone, move
  `[Unreleased]` under a new `## [X.Y.Z] - YYYY-MM-DD` header, open a fresh
  `[Unreleased]`, tag `vX.Y.Z` signed. Milestones, changelog, and tags click
  as one mechanism.

`CHANGELOG.md` is a standard repo file and therefore an allowed exception to
the frozen-markdown rule above.

## Decision records

`docs/decisions/NNNN-*.md` stays as files, but ADRs are **accepted records
only** — the tombstone of a decision, not the deliberation. Deliberation
happens in the issue (`type:decision`); the ADR records context, options,
the choice, and consequences once Scott has called it.

## Disciplines (Scott's, non-negotiable)

- **Decision-before-code.** Design choices get `docs/decisions/NNNN-*.md`
  (context, options, choice, consequences) *before* implementation.
  Decisions Scott must make are flagged in reports, not made for him.
- **The suite is the oracle.** A feature exists when its spec tests pass.
  Claims of correctness cite counts (`N/N green`), not impressions.
- **Gates.** Proposals land behind build tags / config gates; acceptance is
  the proposal's own suite green (contract §9). Nothing defaults on
  without it.
- **Artifacts become oracles.** Bugs found by hand become regression tests
  in the same session. Graves get marked: a comment at the fix site naming
  the lesson and citing the issue.
- **Sweep after a grave.** A defined-but-never-returned error, an
  unreachable branch, a constant nothing reads — grep for siblings of the
  same shape in the same session. *An error constant with no reachable path
  is a missing check wearing a disguise* (grave, 0003); its inverse face is
  the predicate-property rule, and disguises come in families.
- **Unreachability is a grave only when it's silent.** Declared and tracked,
  it's a TODO with an audit trail — scaffolding wearing a name tag, not a
  missing check wearing a disguise. The test is whether the deferral was
  *named at its definition site* and carries a tracking issue. A sweep that
  turns up a labelled placeholder has still done its job: it forced the
  classification question. (Ruling on `ErrTrailingData`, #6.)
- **No cgo. Pure Go.** `go vet`, `go test ./...`, and `gofmt -l` clean at
  every commit.
- **Honest boards.** The PR description and the issue tracker reflect
  reality, including what's red. Never quote a suite count that wasn't run.
- **Bucketed failures are the work plan.** A suite Board line reports pass /
  fail / unsupported, with failures bucketed by the missing feature (for the
  decoder, by expected spec error string). The biggest bucket is the next
  issue to take; a bucket going to zero is a PR's measure of done. Failures
  are reported, never skipped — skipping hides the queue.

## Conventions

- Module: `github.com/scttfrdmn/burroughs` (vanity `burroughs.run` import
  path is a later decision — 0001 records this).
- Go ≥ 1.26. `make build test vet` must be green before any report.
- License: **Apache 2.0**, © 2026 Scott Friedman. `LICENSE` is the verbatim
  upstream text; the copyright line lives in `NOTICE` (Apache 2.0 §4(d)).
- Fetched/vendored material (spec suite) lives under gitignored paths;
  never commit upstream test corpora.
