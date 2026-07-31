# 0005 — Tooling gates: idiomatic Go, enforced rather than intended

Date: 2026-07-30 · Status: **accepted** (Scott, 2026-07-30)
Contract refs: §0 (correctness-neutral, performance-partisan), §9 (gates)

## Decision

**Quality is a gate, not a habit.** Every convention this project relies on is
enforced by a pinned tool wired into CI, because a convention that depends on
remembering is a convention that decays across session boundaries — and this
project changes hands often enough for that to be the governing constraint.

Nine parts, one spirit clause.

### 1. Tools are pinned in repo state, never in CI YAML

`tools/go.mod` holds `tool` directives for every quality tool:

| tool | version | role |
|---|---|---|
| `golangci-lint/v2` | 2.12.2 | lint + formatting |
| `govulncheck` | 1.6.0 | known vulnerabilities |
| `deadcode` | x/tools 0.48.0 | unreachable functions |
| `benchstat` | x/perf | performance claims |

Installed with `go get -tool -modfile=tools/go.mod <pkg>`, run with
`go tool -modfile=tools/go.mod <name>`. Nothing is installed globally and no
version lives in a workflow file.

*Why a separate modfile:* tool dependencies are large and would otherwise
pollute the engine's own `go.mod`, which must stay empty — Burroughs is a
pure-Go runtime with no runtime dependencies, and that emptiness is a claim the
module file should make visibly.

*Why repo state and not CI YAML:* a green board on a laptop and a green board in
CI must mean the same thing. A version in a workflow file is invisible locally,
so the two diverge silently — which is the same objection as an unrun suite
count.

### 2. Configuration is curated, never `enable-all`

`.golangci.yml` starts from `linters.default: standard` and enables a deliberate
set, each with a rationale comment. Three groups earn their place:

- **Correctness in a package that decodes untrusted bytes:** `errorlint`
  (the decoder's error contract is matched with `errors.Is`; a `==` sneaking in
  is a real defect), `exhaustive` (a missing case in a dispatch switch is the
  interpreter's version of grave #3), `nilerr`, `makezero`.
- **The grave-hunters** — the sweep discipline promoted to tooling:
  `unparam`, `wastedassign`, `unconvert`. An always-nil error return is what hid
  grave #3; `unparam` is the tool that would have found it.
- **Idiom and modernity:** `revive`, `staticcheck` with the `modernize`
  analyzers, `copyloopvar`, `intrange`, `usestdlibvars`, `perfsprint`.

*Why not `enable-all`:* lint noise is its own kind of dishonest board. A wall of
findings nobody reads is indistinguishable from no linter, and it trains the one
reflex this project cannot afford — scrolling past a warning.

### 3. Formatting is gofumpt, checked as a diff

`gofumpt` with `extra-rules`, via the v2 `formatters` section. CI runs
`golangci-lint fmt --diff`, which reports without rewriting — the same command
`make fmt-check` runs, so local and CI verdicts cannot diverge.

Formatting is never a review topic.

### 4. Modern idioms held at zero

The `modernize` analyzers stay green: `min`/`max` builtins, `slices`/`maps`/`cmp`,
range-over-int, iterators where they clarify. The engine should read like 2026
Go, not 2019 Go with newer syntax available.

First run found 8 `intrange` sites and 1 `QF1002` tagged-switch opportunity, all
fixed. `go 1.26` in `go.mod` is the floor that makes these available.

### 5. Suppression discipline: noticed-and-named, or not at all

Every finding is **fixed**, or **suppressed as `//nolint:<linter> // reason`**,
or **the linter is removed by config change with a commit message saying why.**
Never silent. `nolintlint` enforces it with `require-explanation` and
`require-specific`, and `allow-unused: false` deletes stale suppressions.

This is the `ErrTrailingData` ruling applied to lint: *unreachability is a grave
only when it's silent; declared and tracked, it's a TODO with an audit trail.*
The reason string is the audit trail.

One suppression exists at adoption: `reader.u64` (#19), unused until i64
immediates (#7) or memory64.

### 6. Testing posture

- `-race` on both architectures, as before.
- **`-shuffle=on`** added: test order is never load-bearing.
- **Native fuzzing**, four targets:

| target | invariant |
|---|---|
| `FuzzDecodeModule` | total behaviour: a module or a *declared* error, never a panic, never both |
| `FuzzULEB` | width invariant + progress, at both 32 and 64 bits |
| `FuzzWastLexer` | full-input consumption and node well-formedness |
| `FuzzParseNodeProgress` | **a successful parse consumes ≥1 byte** |

Corpora are **seeded from the spec suite**, not hand-written: 809 module images
from 257 `.wast` files, extracted at run time. This is the structural fix for a
real defect — hand-transcribed fixtures had drifted from the vectors they
claimed to copy, one truncated from 11 bytes to 8. A corpus with no
transcription step cannot drift. `TestFixtureProvenance` machine-checks the
citations on what remains hand-written.

Short fuzz on every PR (proves the target builds and the corpus loads); 10-minute
runs weekly, one job per target.

*Why progress is asserted and not just absence-of-panic:* grave #18 was a lexer
whose loop exit condition and error condition were the same predicate. It
surfaced as an error only because `;` happened to be a delimiter; a different
byte class would have hung instead. **Parsers should prove progress, not assume
it** — so the fuzz target asserts the offset moved.

### 7. benchstat or it didn't happen

Any performance claim compares via `benchstat` over n≥10. `make bench` is the
only sanctioned way to produce one. A single `-count=1` number is not a
measurement, and the variance band is the part that decides whether two numbers
differ at all.

Applied retroactively to 0002's evidence: the conclusions hold, including the
load-bearing one — Rewrite is immune to immediate width (13.30µ vs 13.32µ, well
inside the bands) where in-place pays 14%.

### 8. Hygiene gates

- `go mod tidy` diff-check in CI, **for the engine module only**. Not for
  `tools/go.mod`: a tool modfile has no packages of its own, so `tidy` resolves
  the tools' transitive *test* dependencies and — finding no local package for
  the import path — discovers this repository through the module proxy and adds
  `require github.com/scttfrdmn/burroughs v0.0.1`. The project would depend on a
  published copy of itself to lint itself. `go get -tool` is what maintains that
  file; `tidy` damages it. Found by running it.
- **`deadcode` (x/tools)** — the unreachable-error grave promoted to a tool.
  A finding is a *classification question*, not automatically a bug: declared
  and tracked passes, silent fails. CI allowlists only `reader.u64` (#19).
- Doc comments on all exported identifiers (`revive`'s `exported` rule).
- `internal/` stays internal — which the toolchain enforced during this very
  change, refusing to let a throwaway verification script import
  `internal/spec`.

### 9. Toolchain currency is a gated upgrade

Go 1.27 is due on the Feb/Aug cadence within weeks. It is adopted like any
proposal: branch, CI green on both architectures, changelog entry, then the bump
lands. Same for future golangci-lint majors. **Never a drive-by version bump in
a PR about something else.**

## The spirit clause

**Linters serve the contract, not the reverse.** If a finding fights a
deliberate engine design — in-place payload aliasing, the `uint64` slot
representation, dispatchbench's intentional duplication — then
suppression-with-reason is the *correct* outcome and the reason is the
documentation.

Three such exclusions exist at adoption, each with its rationale in
`.golangci.yml`: `govet`'s `fieldalignment` (hot structs are ordered for cache
behaviour and readability; when layout matters it will be measured and
commented, which is stronger than an unexplained reorder), `gocritic`'s
`hugeParam`/`rangeValCopy` (the decoder aliases rather than copies, by decision
0002), and `dupl` over `dispatchbench` (the duplication *is* the experiment;
deduplicating it would destroy the measurement).

## Options considered

**A. Nothing — rely on `go vet` and `gofmt`.** What the project had. `go vet`
found none of the 14 findings golangci-lint's first run produced, and would
never have found the unused `u64`. Rejected.

**B. golangci-lint with `enable-all`.** Maximum coverage, and the reason
projects abandon linters. Rejected on the noise argument above.

**C. Pin in CI YAML, install with the official action.** Simplest CI, but the
version becomes invisible locally and local/CI verdicts drift. Rejected.

**D. Curated config in a pinned tool modfile.** **Chosen.**

## Consequences

- **A new contributor runs `make check` and gets CI's verdict.** That is the
  whole point: the gate is reproducible, and a surprise in CI means a bug in
  the Makefile, not a bug in someone's habits.
- **Adding a linter is a decision with a rationale**, recorded in
  `.golangci.yml` next to the enable. Removing one is a commit message.
- **`tools/go.mod` gains a `go.sum`** — the first checked-in sums in the repo.
  The engine's own `go.mod` stays dependency-free, which is the claim that
  matters.
- **CI is five jobs, not one:** build (×2 arch), lint, vuln, fuzz-smoke. Longer
  wall-clock, run in parallel; the fuzz smoke test needs the suite vendored, so
  it fetches.
- **Fuzz corpora live in `testdata/fuzz/` when a crasher is found** and *are*
  committed — a crasher is a regression test, unlike the upstream corpora which
  stay gitignored. The distinction: we authored the crasher, we vendored the
  suite.
- **The tools found three real problems on adoption**, which is the argument for
  the whole exercise: an unused-and-untracked `u64` (#19), a nil-vs-empty
  ambiguity in `parseString` found by `FuzzWastLexer` on its first run, and — via
  the fixture sweep this directive prompted — two hand-typed vectors that had
  drifted from the suite lines they cited.
