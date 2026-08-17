# CLAUDE.md — Burroughs

You are taking over **Burroughs** (burroughs.run): a WebAssembly runtime in pure Go,
language-directed for Go itself. The B5000 favored ALGOL; Burroughs favors Go. This scaffold
was set up by chat-Claude with Scott; you are the implementation agent.

[`docs/burroughs-contract-v0.1.md`](docs/burroughs-contract-v0.1.md) is **normative** — read it
before writing any code. You MUST NOT edit its normative text (§§0–9) without Scott's explicit
sign-off; §10 open questions are resolved *by decision doc*, never silently in code. Posture:
**correctness-neutral, performance-partisan** (§0) — the upstream spec suite is the neutrality
guarantee, and partisanship lives in API surface and optimization priorities only.

**This file is a brief and a pointer page.** Five behaviours below change what you do; the rest
of the corpus lives in `docs/laws/` and is read when its subject is in play. **No measured
figure lives here** — any sentence asserting a measured quantity is generated or deleted
(Scott's rider, [ADR 0029](docs/decisions/0029-the-public-boundary-run-on-a-validated-path-decline-as-a-third-outcome-and-a-value-that-converts.md)).
Ask the instrument: `go test ./internal/spec/ -run TestPhase1Files -v` prints the board,
`make ratio RATIO=<rev>` the engine/instrument lines.

## Phase ladder

- **v0 — interpreter.** Decoder → internal form
  ([0002](docs/decisions/0002-interpreter-strategy.md)) → validator → interpreter, Wasm MVP core
  suite green with 3.0-feature gates present and off. No compiler.
- **v1 — threads + safepoints.** Contract §§2–5: OS-thread spawn, futex wait/notify,
  engine-native epochs/STW, the §4 boundary memory model with its litmus battery.
- **v2 — stack switching.** Contract §7: growable continuations, morestack analog.
- **v3 — component model + WASI 0.3.** Contract §6.

Current phase: **v0**. Do not reach ahead of the phase without a decision doc approving it. The
ladder is a sequence of *artifacts*, not of instruments: the harness, the controls, and the
generated tables are how those artifacts are known to be right; they are never the deliverable.

## Where the work is tracked

**GitHub is the tracker.** The repo's markdown footprint is frozen at standard repo files;
project state lives in issues, milestones, and PRs. Milestones are the phase ladder (`v0
interpreter`, one `v0.x` per proposal gate, then `v1`, `v2`, `v3`) and every issue attaches to
one. Labels stay small: `phase:v0`…`phase:v3`, `gate:<proposal>`, `type:decision`, `type:grave`,
`type:harness`, `type:contract`, and **`decision-needed:scott`** — that last one, assigned to
Scott, *is* the decisions-needed queue, now queryable. Graves are closed issues labeled
`type:grave`, lesson in the closing comment, with a comment at the fix site citing the number.

Do not reintroduce `PROGRESS.md`, `docs/reports/`, or any status file.

## The brief

Five behaviours, chosen because each one changes what a PR *does* before any lesson is recalled.
Their bodies — specimens, minting records, the token each was granted on — are in `docs/laws/`.

1. **The phase's product is the work; instruments are overhead on it.**
   ([body](docs/laws/product-and-overhead.md#the-phases-product-is-the-work-instruments-are-overhead-on-it))
   v0's product is a **running interpreter**. A control, a census, a board bound, a changelog
   gate, a citation sweep: each is overhead that must be *charged to* a piece of product work,
   accounted per PR.
   - **Every PR states its `unsupported` delta, and a zero is a confession** — stated in that
     word, naming the product work it is overhead *for*. The column moves only when what the
     harness *can ask* changes, so where a PR cannot change that, the zero is **structural**, is
     said to be structural, and the reward figure that does have a subject is named instead.
   - **Two consecutive instrument-only PRs is a stop condition.** The counter counts a PR's
     *purpose*, not its line-majority; the classification is named in the PR body and is
     challengeable. It is discharged only by a principal's explicit order or stamp, never by
     self-classification — because **the actor never chooses the instrument that judges the
     actor**. State the case and flag it; a principal rules.
   - **Instrument-to-engine ratio is quoted, not felt** — every PR, from `make ratio RATIO=<rev>`
     (`scripts/ratio.sh`), uniform comparator (engine = code in the module path; instrument =
     tests, generators, harness), **never compared to a threshold**, and with its provenance
     split: a `Ratio-Class: carried` or `Ratio-Class: ordered <citation>` trailer per commit,
     absence counted `unattributed`.

2. **The PR *is* the report.** Work happens in PRs, even self-merged ones, and the description
   carries exactly these sections: **Board** (suite counts, build status, plus the two figures
   above) · **Landed** · **Decisions taken** · **Decisions needed from Scott** · **Graves** ·
   **Next**. Two principals review: **Scott** (owner, all decisions) and **chat-Claude**
   (contract author, architecture review), who is reached through Scott. Keep it terse and
   factual, written for a reader who wasn't in the session; anything Scott must decide is
   *flagged*, never decided for him, and a PR that would change the contract says so in
   **Decisions needed** and labels the issue `type:contract`. **A Landed section is a changelog
   entry wearing a different hat** — update `CHANGELOG.md`'s `[Unreleased]` in the same PR.

3. **Decision-before-code.** ([body](docs/laws/decisions-and-thesis.md#decision-before-code))
   Design choices get `docs/decisions/NNNN-*.md` — context, options, choice, consequences —
   *before* implementation. Deliberation lives in the issue (`type:decision`); the ADR is the
   tombstone, an accepted record only. A decision doc is not product work either, so **one ADR
   earns one implementation**, and an ADR whose implementation has not started is a reason to
   write code rather than another ADR. **An ADR's `Status:` is a citation to an approval**
   ([body](docs/laws/decisions-and-thesis.md#a-status-field-is-a-citation-to-an-approval-and-approvals-are-artifacts-with-provenance)):
   held open until a stamp exists to point at, because a forged provenance about the project's
   own governance is worse than a wrong option.

4. **Nothing defaults on without its own suite green.** ([body](docs/laws/gates.md#gates))
   Proposals land behind build tags / config gates; acceptance is the proposal's own suite green
   (contract §9). **A flip is never in the mechanism's PR — it is its own stamp-tier event.**
   Mechanism is product and self-merges on a bound green; a flip is governance and holds for a
   principal's stamp, with its forecast **pre-registered** and its rollback stated, because you
   cannot pre-register a forecast inside the PR that creates the numbers.

5. **Wait on the verdict, never on a timer — and wait in the background.** Resolve the CI run
   from the pushed SHA, watch it with `run_in_background`, and read the conclusion from the run:
   `gh pr checks --watch` races the run's creation and reports the previous commit's green, and
   *a command's exit status belongs to whatever ran last*. `sleep` is never how you wait for a
   signal that exists. The recipe, its two-meanings-of-no branch, and the three mistakes in the
   order they were made: [operations.md](docs/laws/operations.md#waiting-on-ci).

## The law corpus — `docs/laws/`

Lessons are indexed by **shape**, so a defect that feels familiar has probably been paid for
already: read the family whose subject is in play, and sweep backwards through it when a grave
is dug.

- [product-and-overhead.md](docs/laws/product-and-overhead.md) — what gets selected, and what
  the selection is charged to.
- [decisions-and-thesis.md](docs/laws/decisions-and-thesis.md) — ADRs, stamps, and whether a
  decision is this project's to make.
- [gates.md](docs/laws/gates.md) — what a gate may and may not do to the grammar.
- [engine.md](docs/laws/engine.md) — the suite is the oracle, no cgo, parsers prove progress,
  honest boards.
- [boards-and-buckets.md](docs/laws/boards-and-buckets.md) — reading a board: buckets as the
  work plan, third verdicts, skips.
- [controls.md](docs/laws/controls.md) — a control's own failure modes: stillbirth, vacuity,
  scope, attribution.
- [evidence-and-instruments.md](docs/laws/evidence-and-instruments.md) — verdict channels,
  coverage claims, second-order honesty, and reading a write's payload back rather than its
  status flag.
- [errors-and-testimony.md](docs/laws/errors-and-testimony.md) — error messages, comments, and
  ADRs as testimony.
- [citations.md](docs/laws/citations.md) — every citation resolves, and the sweeps that check it.
- [graves-and-sweeps.md](docs/laws/graves-and-sweeps.md) — graves, sweeps, artifacts becoming
  oracles.
- [operations.md](docs/laws/operations.md) — the recipes: [waiting on
  CI](docs/laws/operations.md#waiting-on-ci), [local cross-architecture
  verification](docs/laws/operations.md#local-cross-architecture-verification), [post-squash
  divergence](docs/laws/operations.md#after-a-squash-merge-local-main-diverges-from-originmain--verify-dont-force).

Two controls in `internal/testenv` keep this page from rotting into a page of dead pointers, and
they are two because they fail for unrelated reasons (grave #34): `TestClaudeMDLinksResolve` that
every link here names a file that exists and an anchor some heading slugs to, and
`TestLawFamiliesAreReachable` that every family in the corpus is linked from here.

## Conventions

- Module `github.com/scttfrdmn/burroughs` (the vanity `burroughs.run` path is a later decision —
  [0001](docs/decisions/0001-project-genesis.md) records this). Go ≥ 1.26. **No cgo. Pure Go.**
- **`make check` is the gate** — fmt-check, build, vet, lint, test, deadcode — and must be green
  before any report; it is the local mirror of CI, so a surprise in CI is a bug in the Makefile.
  `make fuzz`, `make bench`, `make vuln`, `make cite`, `make close` for the rest. Tools are
  pinned in `tools/go.mod` via `tool` directives, never in CI YAML
  ([0005](docs/decisions/0005-tooling-gates.md)), and the engine's own `go.mod` stays
  dependency-free. Suppression is **noticed-and-named or not at all**; **benchstat or it didn't
  happen**; fuzz corpora seed from the spec suite at run time and crashers are committed. A
  toolchain bump is its own gated PR.
- Versioning is **SemVer 2.0.0** with minors mapped to milestones, so the version number is a
  conformance statement rather than a mood; the contract versions independently and every release
  states which contract version it implements
  ([0004](docs/decisions/0004-versioning-and-contract-independence.md)). `CHANGELOG.md` follows
  **Keep a Changelog 1.1.0**, hand-maintained, newest first, `[Unreleased]` at the top — gate
  flips are **Added** with their `gate:` name, graves are **Fixed** with their `type:grave` link.
- License **Apache 2.0**, © 2026 Scott Friedman. `LICENSE` is the verbatim upstream text; the
  copyright line lives in `NOTICE` (Apache 2.0 §4(d)).
- Fetched/vendored material (the spec suite) lives under gitignored paths; never commit upstream
  test corpora.
