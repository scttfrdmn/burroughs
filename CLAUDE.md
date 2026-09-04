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
  suite green with **every 3.0-feature gate present and its default a recorded decision** — off by
  behaviour 4 below unless its own flip event, with its own stamp, says otherwise. No compiler.
  This read *"gates present and off"* until #464/#466, when reconciling v0's closure conditions
  found that clause falsified by two flips Scott had himself stamped (SIMD, ADR 0025; relaxed SIMD,
  ADR 0028). **A closure condition must not retroactively unmake a stamped decision**, which is the
  foreclosing-words shape one level up: a sentence written before a flip, left standing after it,
  telling the next reader the tree is in a state it is not. (Ruling: Scott, on the #465 review.)
- **v1 — threads + safepoints.** Contract §§2–5: OS-thread spawn, futex wait/notify,
  engine-native epochs/STW, the §4 boundary memory model with its litmus battery.
- **v2 — stack switching.** Contract §7: growable continuations, morestack analog.
- **v3 — component model + WASI 0.3.** Contract §6.

Current phase: **v1**, since the signed `v0.4.0` tag of 2026-08-28 — *"v0's closure set is
complete"*, all twelve conditions discharged (#464, #499), the `v0 interpreter` milestone closed at 99
issues. Do not reach ahead of the phase without a decision doc approving it. The
ladder is a sequence of *artifacts*, not of instruments: the harness, the controls, and the
generated tables are how those artifacts are known to be right; they are never the deliverable.

**v1's own artifacts are untouched, and the line names the phase rather than the progress.** The
§§2–5 boundary work — OS-thread spawn, futex wait/notify, engine-native epochs and STW, the §4 memory
model with its litmus battery — has not started; what is in flight is gated front-end work for the
threads proposal (`gate:threads`: the `shared` flag, the keyword table, the 67 atomic mnemonics). This
is stated because `v0.4.0`'s own release note says *"it is not v1 … v0 closing means v0's conditions
are discharged, not that the next phase has begun"*, and **that sentence is true about the artifacts
and was written before the work that followed it** — so a reader who finds it must not read this line
as claiming §§2–5 exist. What advanced is which phase is current, on the only evidence that can settle
it: v0's conditions are discharged and its milestone is closed, so *no* phase-line value naming v0 is
true. (Ruling: Scott, on the #534 review — dissolve #527 rather than adjudicate it, since both of its
readings turn on which phase is current.)

## Where the work is tracked

**GitHub is the tracker.** The repo's markdown footprint is frozen at standard repo files;
project state lives in issues, milestones, and PRs. Milestones are the phase ladder (`v0
interpreter`, one `v0.x` per proposal gate, then `v1`, `v2`, `v3`), and **an issue attaches to one
when it is scheduled, not when it is filed** — a milestone is a commitment to do the work in a
phase, so requiring it at filing time prices filing at the cost of scheduling and the unscheduled
backlog becomes 32 standing violations of a rule nothing was gaining from (#324, retired by Scott).
An unmilestoned issue is a filed issue, which is the state most of them should be in. Labels stay
small: `phase:v0`…`phase:v3`, `gate:<proposal>`, `type:decision`, `type:grave`,
`type:harness`, `type:contract`, and **`decision-needed:scott`** — that last one, assigned to
Scott, *is* the decisions-needed queue, now queryable. **Queryable by the issues API, and never by
`gh issue list --label`**, which answers from a search index that lags the label mutation: a queue you
have just drained reads as full, in a report to the principal whose queue it is. Recipe, and the
two ways the API arm still under-reports: [reading the tracker's
state](docs/laws/operations.md#reading-the-trackers-state-the-queue-comes-from-the-issues-api-never-from-a-cached-listing).
**And the queue is parked: it is not reported at all until the §4 litmus battery ([#10](https://github.com/scttfrdmn/burroughs/issues/10))
completes.** No count, no list, no *"none blocking"* — an item is surfaced only when it **blocks code**,
and then only that item. Filing is untouched; what is parked is the reporting. Scott's order on the
#562 review, and the end point is half of it: *"the queue parking gets an end point so it stops
recurring … Until then, don't report the queue at all — 'twelve waiting, none blocking' is itself the
surfacing I asked you to stop, and half-parking pays both costs."* Recorded here by the actor who was
ordered, which is not independent provenance — *durability is not independence* — so commits in the
slices this covers stay `Ratio-Class: carried`.
Graves are closed issues labeled
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
   **Done since last review** · **Next**. Two principals review: **Scott** (owner, all decisions)
   and **chat-Claude** (contract author, architecture review), who is reached through Scott. Keep it
   terse and factual, written for a reader who wasn't in the session; anything Scott must decide is
   *flagged*, never decided for him, and a PR that would change the contract says so in
   **Decisions needed** and labels the issue `type:contract`. **A Landed section is a changelog
   entry wearing a different hat** — update `CHANGELOG.md`'s `[Unreleased]` in the same PR.
   **Done since last review** is Scott's, ordered on the #387 review after he asked three times for
   the disposition of work that was already on main: *"I asked three times for something that was
   already on main, which is my time and yours spent on a lookup you'd already done."* It names the
   work landed between his last review and this report — every issue closed and every named ask
   discharged, one line each — because **a report that only looks forward makes the reader do the
   lookup**, and *Next* cannot answer a question about the past.

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
  divergence](docs/laws/operations.md#after-a-squash-merge-local-main-diverges-from-originmain--verify-dont-force),
  [the PR body's own sweeps](docs/laws/operations.md#opening-a-pr-the-body-is-a-scanned-population-and-make-check-cannot-see-it),
  [sourcing a claim about the
  queue](docs/laws/operations.md#reading-the-trackers-state-the-queue-comes-from-the-issues-api-never-from-a-cached-listing).

Three controls in `internal/testenv` keep this page — and now the whole corpus — from rotting into
dead pointers, and they are three because they fail for unrelated reasons (grave #34):
`TestMarkdownLinksResolve` that every link in **every markdown file in the tree** names a file that
exists and an anchor some heading slugs to, `TestMarkdownLinkTargetsAreNotWrapped` that no
destination is broken across a line (which renders as literal text rather than as a link), and
`TestLawFamiliesAreReachable` that every family in the corpus is linked from here. The first was
`CLAUDE.md`-scoped until #466: the corpus cites itself, `CHANGELOG.md` cites the law a change
minted, and a heading rename breaks incoming citations no control could see. **The half still
uncovered is a citation carrying no anchor at all** — the file resolves, so nothing fires, and the
prose can still name a law that does not exist.

## Conventions

- Module `github.com/scttfrdmn/burroughs` (the vanity `burroughs.run` path is a later decision —
  [0001](docs/decisions/0001-project-genesis.md) records this). Go ≥ 1.26. **No cgo. Pure Go.**
- **`make check` is the gate** — fmt-check, build, vet, lint, test, deadcode — and must be green
  before any report. It is the local mirror of CI **on a precondition: where the Makefile observes a
  superset of what CI observes.** Inside that region a surprise in CI is a bug in the Makefile;
  outside it the surprise *is* the gap, and the gap is what to name rather than a Makefile bug to hunt
  ([the precondition, with its
  instances](docs/laws/operations.md#the-maxims-precondition-the-mirror-holds-where-the-makefile-observes-a-superset-of-ci)).
  The instances are unnumbered by ruling, because stating the precondition is what retires the
  question: a count of exceptions was written, amended, and stale twice before this page stopped
  keeping one. Containment fails **both** ways — CI sees what `make` cannot (a fetched artifact's
  presence is machine state, not repo state), and `make` sees what CI cannot (`make strict` printed a
  red run's testimony where its `-e`-shelled twin printed nothing, and the `deadcode` gate could not
  host its own repair at all, grave #544) — so *which half is worse* is asked, not assumed.
  `make fuzz`, `make bench`, `make ab`, `make vuln`, `make cite`, `make close` for the rest —
  **`bench` is one arm and `ab` is the comparison**, and the split is the repair for a target that
  demanded a p-value and ran the one `benchstat` invocation that cannot print one (grave #612). Tools are
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
- **An agent worktree lives outside the repo tree, or is removed when its agent finishes.** Three
  were found inside `.claude/worktrees/` — 126M holding full copies of the engine whose work had
  already squash-merged — and being untracked bought nothing: a stale duplicate is invisible to
  `git status` and **in every grep's domain**, so it answers searches with a past version of the
  tree. It had already cost one measurement (a caller search returned hits from the copy). Untracked
  is not out of the way; *an artifact that answers a question is an oracle whether or not anyone
  appointed it one*, and the reason this is one line rather than a control is that a control would
  have to run inside the tree it is checking for copies of itself.
- **`MEMORY.md`, the known limitation.** The agent's session memory is loaded beside this file and
  lives outside the repository, so no instrument here can assert anything about it: every control in
  `internal/testenv` has `CLAUDE.md` in its domain and `MEMORY.md` in nobody's. Stated because a gap
  named on the page is a gap a reader can price, and because the alternative was an open issue no
  work in this tree could ever discharge (#319, retired for exactly that reason).

## Running tests and benchmarks on lab hardware

Lab machines are shared by several projects at once. Historically nothing
coordinated them, so two projects would land on the same box simultaneously and
each would see the interference as unexplained performance divergence in its own
results. All runs on lab hardware now go through **pueue**, a per-machine job
queue with a daemon on every host.

**Rule: nothing that gets measured runs outside the queue.** Anything whose
timing, throughput, or memory-bandwidth numbers will be recorded, compared, or
committed must be submitted to an exclusive group. Interactive pokes and
one-off debugging are fine to run directly, but their numbers are not results.

### Fleet and groups

A group is a named queue on one host with its own concurrency limit. Every host
has exactly three:

- **`measured`** — parallel=1. The single measurement slot on that box. Every
  run whose numbers get recorded goes here, *whatever* it exercises: Metal,
  CUDA, ROCm, or plain CPU.
- **`build`** — wide. Anything unmeasured: compiles, unit tests, lint.
- **`default`** — parallel=1. A deliberate fallback, so a job someone forgets to
  assign a group serializes instead of quietly stomping a benchmark.

`fleet.conf` is the source of truth; this table is a convenience copy.

| Host | Hardware | Accelerator | `measured` | `build` |
|---|---|---|---|---|
| `juno.local` | M4 Max, 16 core, 64GB | Metal | yes | 6 |
| `orion.local` | M4 Pro, 14 core, 48GB | Metal | yes | 6 |
| `maya.local` | M4, 10 core, 16GB | Metal | **no** | 4 |
| `indigo.local` | M4, 10 core, 32GB | Metal | **no** | 4 |
| `castor.local` | DGX Spark, GB10, 20 core, 121GB | CUDA `sm_121` (unified mem) | yes | 8 |
| `pollux.local` | DGX Spark, GB10, 20 core, 121GB | CUDA `sm_121` (unified mem) | yes | 8 |
| `antares.local` | Ryzen AI MAX+ 395, Radeon 8060S, 60GB | ROCm/HIP `gfx1151` (unified mem) | yes | 8 |
| `vesta.local` | Ryzen 9 7950X3D, RTX 4070 Ti SUPER, 61GB | CUDA — no toolkit installed | yes | 8 |
| `ceres.local` | Ryzen 9 9950X3D, RTX 5090, 123GB | CUDA `sm_120` | yes | 8 |
| `janus.local` | i9-9960X, 32 core, 62GB, 2× TITAN RTX | CUDA `sm_75` | yes | 8 |

**`maya` and `indigo` are `build`-only on purpose.** Their GUI console user is
somebody else, so they are in daily interactive use. pueue serializes queued jobs
against each other; it cannot serialize against a person. A measurement slot on a
machine you don't control is worse than none — it produces numbers that look
valid. Send compiles there, never benchmarks.

The group is not named after the device on purpose. **Exclusivity in pueue is
per group, not per host** — two parallel=1 groups on one box run at the same
time as each other, which is precisely the interference this setup exists to
prevent. Verified on 4.0.4: a task in `gpu` and a task in a second exclusive
group both went `Running` in the same second. So there is one exclusive group
per machine and it takes all measured work.

### CPU-only runs on a GPU box

Yes — submit them to that host's `measured` group like anything else. Do **not**
add a separate CPU group to get a second slot; you would get concurrency, not
isolation. A CPU-only run in `measured` waits for the GPU run ahead of it and
vice versa, which is what you want, because "CPU-only" never means "isolated":

- On `castor` / `pollux` (GB10) and `antares` (Strix Halo) the CPU and the
  accelerator share one LPDDR5X memory system. A bandwidth-hungry CPU run and a
  GPU run degrade each other badly. Exclusivity matters *more* here, not less.
- Even on `vesta` / `ceres` / `janus` with discrete VRAM, they share cores for
  the host-side driver threads, PCIe, and power/thermal headroom — a 32-thread
  CPU run will move a GPU benchmark's numbers.

If you genuinely want two things overlapping on one box, that is what `build`
is for, and its results are not results.

### CUDA

| Host | Toolkit | `nvcc` | Driver | Arch |
|---|---|---|---|---|
| `castor.local` | 13.2.2-1 | V13.2.86 | 595.84 | `sm_121` |
| `pollux.local` | 13.2.2-1 | V13.2.86 | 595.84 | `sm_121` |
| `ceres.local` | 13.2.2-1 | V13.2.86 | 595.71.05 | `sm_120` |
| `janus.local` | 12.9 | V12.9.86 | 610.57.04 | `sm_75` |
| `vesta.local` | **none** | — | 595.84 | `sm_89` |

`janus` is Rocky 9 and deliberately not on 13.2: its TITAN RTXs are Turing
(`sm_75`), a different generation from the Blackwell/GB10 hosts, so cross-host
number comparison isn't meaningful there anyway. Its driver (610.57.04, supports
CUDA 13.3) is well ahead of its 12.9 toolkit, which is the safe direction — the
error-222 trap below only bites when the driver is *behind* the toolkit.

`/usr/local/cuda/bin` is prepended to PATH in `~/.bashrc` — deliberately there and
not in an interactive rc, because pueue captures the environment at submit time
from the non-interactive ssh env, so a login-shell-only PATH is invisible to every
queued build. `/usr/local/cuda` is an `update-alternatives` symlink, so naming it
rather than a versioned directory means bumping the alternative moves the fleet.

Watch for a second `nvcc`: Ubuntu's own `nvidia-cuda-toolkit` package installs one
at `/usr/bin/nvcc` a whole major version behind (12.0.140 next to a 13.2 install on
`ceres`) and it wins on the default PATH. That package is still installed on
`ceres`; the PATH order is what keeps it from being used.

**Prefer `-arch=sm_NN` over relying on PTX JIT.** Not required any more, but still
right for benchmarking: JIT-compiling PTX at first kernel launch puts a compile
cost inside your timings. It used to be mandatory on the Sparks, and the failure
mode is worth recognising because it will recur if driver and toolkit ever drift
apart again — a default-arch build compiles cleanly and then dies at launch with

```
CUDA error 222: the provided PTX was compiled with an unsupported toolchain.
```

which means the driver's JIT is older than the toolkit that emitted the PTX. Check
`cudaDriverGetVersion` against `cudaRuntimeGetVersion`: if the driver number is
lower, that's the bug. Compiling native SASS sidesteps it; matching the driver to
the toolkit fixes it properly.

`vesta` has a driver but **no CUDA toolkit at all** — it can run CUDA binaries, not
build them.

Hostnames are `.local` (mDNS) throughout. Some also resolve bare, but the
manifest and every example use the `.local` form.

### Submitting

```bash
# fire and forget — a notification arrives when it finishes
ssh juno.local "pueue add -g measured -w ~/src/umami -l umami/$(git rev-parse --short HEAD) -- ./bench.sh --full"

# watch it
ssh juno.local "pueue status"
ssh juno.local "pueue follow 12"      # live stdout, like tail -f
ssh juno.local "pueue log 12"         # after the fact

# submit and block on the result (see scripts/labrun below)
scripts/labrun juno.local measured -- ./bench.sh --full
```

`-w` sets the working directory, `-l` a human label, and everything after `--`
is the command. Do not wrap the command in `tmux` — pueue already detaches it,
captures stdout and stderr, and survives your ssh session ending. Wrapping in
`tmux new-session -d` breaks queueing, because the task returns immediately and
frees the slot while the real work is still running.

### `scripts/labrun` — submit, wait, propagate the exit code

Use this for anything scripted or CI-like. It exists because **`pueue wait`
exits 0 even when the task failed**, so a naive submit-and-wait silently passes.

```bash
#!/usr/bin/env bash
# labrun <host> <group> -- <command...>
set -euo pipefail
host=$1; group=$2; shift 2
# not `[ ... ] && shift` — under set -e a false test there exits the script
if [ "${1:-}" = "--" ]; then shift; fi
label="$(basename "$PWD")/$(git rev-parse --short HEAD 2>/dev/null || echo nogit)"
remote_dir=${LABRUN_DIR:-$PWD}

# Two levels of quoting, both required. pueue re-joins argv with plain spaces and
# runs the result through a shell, so the command has to arrive as ONE
# already-shell-safe argument or its grouping is silently lost — `sh -c 'exit 42'`
# becomes `sh -c exit 42`, which exits 0 and reports Success.
cmd=$(printf '%q ' "$@")
id=$(ssh "$host" "pueue add -g '$group' -w '$remote_dir' -l '$label' -p -- $(printf '%q' "$cmd")")
echo "submitted $host:$group task $id ($label)" >&2
ssh "$host" "pueue wait -q $id" >/dev/null

read -r code state < <(ssh "$host" "pueue status --json" | uv run --no-project python3 -c '
import json,sys
t=json.load(sys.stdin)["tasks"][sys.argv[1]]
d=(t.get("status") or {}).get("Done")
if not d: print("99 Unfinished"); raise SystemExit
r=d["result"]
if r=="Success": print("0 Success")
elif isinstance(r,dict) and "Failed" in r: print(r["Failed"],"Failed")
else: print(1, r if isinstance(r,str) else list(r)[0])
' "$id")
[ -n "${code:-}" ] || { code=98; state=NoResult; }

ssh "$host" "pueue log $id --lines 40" >&2
echo "task $id: $state (exit $code)" >&2
exit "$code"
```

### Python: uv, always

**uv is the only Python toolchain on the fleet.** Never call the system
`python3` directly, and never use pyenv, conda, or a hand-rolled venv. Every
host is bootstrapped with a pinned uv (0.12.9) and a pinned uv-managed
interpreter (3.13.15 — the patch is pinned too) installed as the default
`python3` in `~/.local/bin`, ahead of `/usr/bin` and Homebrew. `UV_MANAGED_PYTHON=1` is
exported fleet-wide, so uv will refuse to fall back to an OS interpreter even if
one is closer at hand.

The point is the same as pinning pueue itself: an interpreter that is 3.9 on one
box and 3.14 on another turns into performance divergence that looks like it
came from your code.

```bash
# in a project with a pyproject.toml — uv resolves and syncs the environment
ssh juno.local "pueue add -g measured -w ~/src/umami -- uv run pytest -q bench/"

# a throwaway snippet with no project around it
uv run --no-project python3 -c 'import json,sys; ...'

# stdlib isn't enough? declare deps inline, no venv to manage
uv run --no-project --with numpy python3 analyze.py
```

`--no-project` matters more than it looks: without it, `uv run` walks up from
the working directory, finds a `pyproject.toml`, and syncs that project's
environment before running your one-liner. Inside a queued job that is a
surprise write and a surprise delay.

Do not export `UV_PYTHON` to force a version fleet-wide. It overrides
`requires-python` rather than deferring to it, so any project pinned to a
different version fails outright. Pin the provider, let the project pick the
version.

### Recording provenance

Every recorded result must carry the host, the group, and whether the box was
otherwise busy. Contention that isn't recorded turns into a mystery later.
Capture at minimum:

```bash
ssh "$host" "pueue status --json"   # concurrent tasks at submit time
uname -sm; hostname -s              # on the runner
```

and store `host`, `group`, `task_id`, and the concurrent-task count alongside
the numbers.

### Gotchas that will bite

- **`pueue wait` always exits 0.** Check the task result explicitly.
- **On Secure Boot hosts, never `dkms install --force` an NVIDIA module.** All the
  Linux GPU hosts have Secure Boot enabled. Ubuntu's prebuilt
  `linux-modules-nvidia-*` packages are signed with an enrolled key; a DKMS build
  is signed with a local key that is not, so it loads fine on paper and then fails
  at boot with `modprobe: ERROR: could not insert 'nvidia': Key was rejected by
  service` — GPU gone. If a kernel upgrade leaves DKMS and the prebuilt package
  fighting over the same module ("already installed, override by specifying
  --force"), the fix is `dkms uninstall nvidia/<ver> -k <kernel>` to restore the
  signed original, *not* `--force`. Install the driver and
  `linux-modules-nvidia-<ver>-open-nvidia-hwe-24.04` in one apt transaction and the
  collision doesn't arise.
- **A driver bump on the Sparks drags a new kernel with it.** The `nvidia-hwe`
  stack moves together, so verify `modinfo -k <new-kernel> nvidia` resolves to
  `kernel/nvidia-595-open/nvidia.ko` (signed, prebuilt) and not `updates/dkms/`
  *before* rebooting. Rebooting into a kernel with no matching module is precisely
  what leaves a box in janus's state.
- **Two parallel=1 groups on one host do not exclude each other.** They run
  concurrently, so adding a second exclusive group to "separate" CPU from GPU
  work buys you nothing but a false sense of isolation. One `measured` group per
  host, always. Verified on 4.0.4.
- **Commands run through a shell — twice.** pueue joins the argv you give it back
  into a single string with plain spaces, then hands that to your shell, so inner
  quoting must survive two levels of parsing. `pueue add -- sh -c 'exit 42'`
  arrives as `sh -c exit 42`, which exits **0 and reports Success**. Verified on
  4.0.4. Wrap the whole command in quotes (`pueue add -- "sh -c \"exit 42\""`),
  or use `-e/--escape`, or just use `labrun`, which handles it. Also put `--`
  before the command so pueue doesn't eat your flags.
- **The environment is captured at submit time**, from the non-interactive ssh
  environment — not your interactive shell. Anything set in `.zshrc` will not be
  there. Pass what you need explicitly on the command line.
- **Client and daemon versions must match** (pinned fleet-wide to 4.0.4). If
  pueue starts erroring after an upgrade, the daemon needs a restart.
- **uv and the interpreter are pinned too.** `pueue-fleet.sh doctor` reports
  both, and flags a `python3` that isn't uv-managed. Bump `UV_VERSION` /
  `UV_PYTHON` in `fleet.conf` and re-run `bootstrap` (or `python`) — never
  upgrade one host by hand.
- **Labels are not available to the completion notification** — only id, group,
  command, path, result, exit code, and timing. Keep `-w` pointed at the repo
  root so the notification can identify the project by directory.
