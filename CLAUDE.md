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

**v0's product is a module that runs**, and the ladder is a sequence of *artifacts*,
not of instruments: decoder → **internal form (0002)** → validator → interpreter. The
harness, the controls, and the generated tables are how those artifacts are known to be
right; they are never the deliverable. A phase is judged by its product.

**No measured figure lives in this file** — *any sentence asserting a measured quantity is
generated or deleted* (Scott's rider, ADR 0029). Ask the instrument:
`go test ./internal/spec/ -run TestPhase1Files -v` prints the board, `make ratio RATIO=<rev>`
the engine/instrument lines.

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

**The Board section carries the progress line**, because a rule with nowhere to be
written is a habit. Two figures beside the counts, both cheap and both quoted rather
than described:

- **`unsupported` delta** (`60872 → N`, or **`unmoved`** stated in that word), and when
  unmoved, the product work this PR is overhead *for* — **unless the PR is a gate-campaign
  PR, in which case the reward figure is the all-on lane's fail delta and the `unsupported`
  zero is stated as structural**, gated vectors having no way to reach that column before
  their flip. The full rule and its reason are below, under the product discipline; this
  line records the substitution so the Board section is not written from a rule its own
  sub-clause has amended.
- **engine / instrument lines** for the diff, from `make ratio RATIO=<rev>`
  (`scripts/ratio.sh`) under the uniform comparator ruled on #113 — *not* the "non-test `.go` versus `_test.go`" split this line
  used to describe, which counted generators as engine. Its purpose is to make the
  wheel-spinning visible *per PR* rather than only in a trailing window — the 1:1.8 →
  1:5.1 drift (ad-hoc comparator, and **1:2.0 → 1:5.1** recomputed uniform) was invisible
  precisely because each PR was individually defensible. **Quoted, never compared to a
  threshold**: #117 measured the trailing 31 merges and found the figure is dominated by PR
  size, so a bound on it is a bound on diff length. It is a **separate instrument from** the
  two-consecutive-instrument-PRs stop condition, which counts a PR's *purpose* and not its
  line-majority (#159's refinement, below): the ratio neither triggers that counter nor is
  discharged by it, and is quoted every PR precisely so a purpose-classified product PR that is
  also drifting stays visible.

Two principals review: **Scott** (owner, all decisions) and **chat-Claude**
(contract author, architecture review). Scott reviews in the GitHub UI and
relays to chat-Claude; directions come back through Scott. If a PR would
change the contract, say so explicitly in **Decisions needed** and label the
issue `type:contract`.

Keep descriptions terse and factual — written for a reader who wasn't in the
session. Anything Scott must decide is *flagged*, never decided for him.

### Waiting on CI

**Wait on the verdict, never on a timer — and wait in the background.** After
pushing, resolve the run for `HEAD` and watch it detached:

```bash
SHA=$(git rev-parse HEAD)
for _ in $(seq 30); do   # the run takes a moment to appear; poll, don't guess
  RUN=$(gh run list --commit "$SHA" --limit 1 --json databaseId -q '.[0].databaseId')
  [ -n "$RUN" ] && break
  sleep 2
done
if [ -z "$RUN" ]; then   # no run — say WHICH no, don't just time out
  gh pr list --head "$(git branch --show-current)" --state open --json number \
    -q 'if length == 0 then "no open PR for this branch: ci.yml is `push: branches: [main]` plus `pull_request`, so a topic-branch push creates no run until its PR exists. Open the PR, then resolve the run." else "PR exists and no run appeared in 60s — that is a real anomaly, not a wait." end'
  exit 1
fi
gh run watch "$RUN" --compact --exit-status   # run this with run_in_background
```

**The loop's negative has two meanings and must say which.** `ci.yml` triggers on
`push` to `main` only, plus `pull_request` — so a push to a topic branch produces
**no run at all** until its PR is opened, and `gh run watch ""` then 404s on
`/actions/runs/`. That is the mechanism behaving correctly, but the bare loop
reports it identically to "the run has not appeared yet", which is a different
condition with a different remedy (open the PR versus wait longer). *A bounded
wait that cannot distinguish its own failure modes is a timer with better
manners* — the branch above asks the question that separates them. Found the way
these things are always found: it fired for real, on #80, and the first reading
was "flake in the poll". (Directive: Scott, PR #82.)

Three separate mistakes are being avoided, and they were made in that order:

1. **`sleep N && gh pr checks` — a duration is not a completion signal.** It
   guesses low and reports a pending run as though that were news, or guesses high
   and wastes the difference; either way the shell, not the CI system, decided
   when to look. Same error as reading a verdict off a tool's stderr — and read the
   verdict from `gh run view "$RUN" --json conclusion`, **never off the end of a watch
   pipeline**: one command, no last-link ambiguity, nothing for a pipe to eat. A
   procedure, not a control, because no repo gate reaches your own command
   composition. (Directive: Scott, PR #331.)
2. **`gh pr checks --watch` races the run's creation.** It watches whatever checks
   exist *now*, so seconds after a push it finds the previous commit's run,
   reports pass, and exits 0 — a stale green. Always resolve the run id from the
   pushed SHA. `--exit-status` then makes failure non-zero. This is *a verdict
   without an identity check is hearsay*: binding the verdict to the SHA it
   judges is the CI face of stamp-don't-deduce.
3. **Blocking the tool call wastes the wait.** Watch with `run_in_background` and
   keep working; the completion arrives as a notification. A five-minute CI run
   should cost five minutes of *CI*, not five minutes of doing nothing.

**And `sleep` is never how you wait for a signal that exists — background it and let the
wake-up arrive.** This is mistake 1 restated because restating it was necessary: it was
committed *again*, on an already-backgrounded watch, by running `sleep 200` to poll the task's
own output file while its completion notification was in flight. Polling a background task with
a timer is strictly worse than a bare `sleep`, because the signal already exists and the timer
replaces it with a guess. The test is one question — **does a completion signal exist?** If
yes, wait on the signal; if no (GitHub has no "run created" event), poll for the *condition* in
a bounded loop that gives up loudly — the one honest `sleep` here, and honest because the loop
re-asks a real question until it gets an answer where a bare `sleep` asserts one. If nothing
else is ready to do, say what is pending and stop: an idle turn costs nothing, a blocked tool
call costs the whole wait. (Directive: Scott, PR #103 — *"stop using sleep"*, and he was right
to be terse about it, the rule having already been written here by the agent that broke it.)

The first two are *verdict channel and mechanism channel are different
instruments* applied to time and to identity: ask the right channel, and ask it
about the right run.

### Local cross-architecture verification

The dev box is arm64 — the weakly-ordered side, contract §9's own reason both CI
runners exist (`ci.yml`'s own header: x86-64/TSO plus AArch64/weakly-ordered). CI
gives both on push; a claim needing confirmation *before* pushing — a G-1
demonstration, a redistribution forecast, a flip's own board delta — needs the other
architecture locally, and **`scripts/xcheck-amd64.sh` is how**:

```bash
./scripts/xcheck-amd64.sh                              # go test ./...
./scripts/xcheck-amd64.sh go test ./internal/spec/ -run TestAllGatesOnLeavesNothingGated -v
```

It prefers **native x86_64 on `janus.local`** — real TSO hardware rather than an
emulation of it — and falls back to the **amd64 container** under QEMU, which is
slower but exact for this purpose: correctness across memory models, not speed. Its
header carries the reasons it is a script and not a recipe here; the operative
governance is only this. **Every exit path names its instrument, and a no says which
no** — a copy that failed, a daemon that is hung rather than down, no host at all.
The last two are `NOT RUN`, exit 4, *mechanism and not verdict*: nothing about the
code has been learned, and the answer is CI's x86-64 runner one push later. A PR
asserting a cross-architecture claim states which instrument confirmed it.

### After a squash merge, local main diverges from origin/main — verify, don't force

`gh pr merge --squash` rewrites history on GitHub: the merge commit's tree is
identical to the branch just merged, but its hash is new, so a local checkout of
that same branch now points at a commit `origin/main` has never heard of and a
plain `git pull`/`git checkout main` reports "diverged." The fix is **verify then
reset, never reset first**:

```bash
git checkout main && git fetch origin
git diff origin/main <the-branch-or-commit-just-merged>   # must be empty
git reset --hard origin/main                              # only after the diff is empty
```

An empty diff is the check that makes `reset --hard` safe here — it confirms the
"divergence" is purely the squash rewriting the commit's identity, not a real
content difference. This surfaced three times in one session, each time re-derived
from scratch; the pattern is mechanical once named; don't re-derive it.

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

**This section is an index, and `docs/laws/` is the corpus.** Every law below keeps its
**one-line compressed form**, which is the whole recall key — *lessons are indexed by shape*,
so the shape is what has to be in context, and the specimen, the minting history, and the
token each was granted on are looked up on demand exactly as an ADR is. The full text was
**relocated, never rewritten**: `docs/laws/` holds it verbatim, which is why superseded
wordings still appear inside the bodies where a later ruling amended rather than replaced.
`TestEveryLawIsIndexed` (`internal/testenv`) checks each key here against its heading there,
in both directions, so the index and the corpus cannot drift.

**Governance is the exception and stays here in full** — the stop condition, the exemption
token, the flip's stamp tier, an ADR's status, the product accounting. Those are not
lessons to recall on demand; they decide what a PR may do, so they must be in context every
turn. A law whose bullet carries only a pointer has its body in `docs/laws/`; a law whose
bullet carries operative text has that text nowhere else.

- **The phase's product is the work; instruments are overhead on it.** — governance, retained;
  minting record in [product-and-overhead](docs/laws/product-and-overhead.md#the-phases-product-is-the-work-instruments-are-overhead-on-it).
  This rule is first because it governs what gets *selected*, upstream of every rule below about
  doing selected work well. v0's product is a **running interpreter** — decoder → validator →
  interpreter over 0002's internal form. A control, a census, a board bound, a changelog gate, a
  citation sweep: each is overhead that must be *charged to* a piece of product work, and the
  honest accounting is per-PR, not per-session.
  - **Every PR states its unsupported-column delta, and a zero is a confession.** A PR that moves
    it by nothing says so, in those words, and names the product work it is overhead *for*. The
    gate already exists — `unsupportedCeiling` in `spec_test.go`, a **ceiling**, which per 0013
    rots by the system working — so lowering it *is* the record of progress and no second
    mechanism gets built for this (*one concept, one trigger*, #82).
    - **The column moves only when what the harness *can ask* changes; where a PR cannot change
      that, the zero is structural and is stated as structural, naming the reason and the reward
      figure that does have a subject.** A statement about *which instrument has a subject*, never
      an exemption — the actor still does not get to pick. Written as the condition and not as a
      list of cases, because enumerated instances invite an amendment per instance; the specimens
      are in `docs/laws/`. (Rulings: Scott, PR #235 — his token, his veto standing; chat-Claude,
      PR #302.)
  - **The actor never chooses the instrument that judges the actor.** Where a judgement is about
    the work, the actor makes it; where it is about the actor, the actor's job is to *state the
    case and flag it*, and a principal rules. The general form covers instruments not yet
    invented, which is why it sits above the two specific rules below. (Compression: Scott, PR #113.)
  - **Instrument-to-engine ratio is quoted, not felt** — every PR, from `make ratio RATIO=<rev>`
    (`scripts/ratio.sh`), deliberately *not* a `check` dependency: a script CI runs is a gate
    whatever it is called.
    - **The comparator is uniform and fixed: engine = code in the module path; instrument =
      tests, generators, harness. No per-file pleading, ever.** A ratio whose comparator moves
      per-PR measures advocacy instead of drift. Product-work classification (which governs
      *selection*) and ratio classification (which measures *drift*) are deliberately different
      questions with different answers for the same file. (Ruling: Scott, PR #113.)
    - **Quoted, never compared to a threshold.** #117 measured the trailing 31 merges: the figure
      is dominated by PR size (`instrument = 486 + 0.79 × engine`), so every candidate threshold
      is a disguised minimum-PR-size rule, and a numeric bound would train the habit of padding
      engine diffs until the quotient clears it. Historical quotes stand with their era noted.
      It is a **separate instrument from** the stop condition below, which counts a PR's
      *purpose* and not its line-majority: the ratio neither triggers that counter nor is
      discharged by it.
  - **Two consecutive instrument-only PRs is a stop condition.** Not a soft preference — stop and
    take product work, or get Scott's word to continue. The ratchet only turns one way otherwise,
    because control work is always available, always passes review, and always produces a clean
    green.
    - **The counter counts PRs whose *purpose* is non-product, not PRs whose line-majority is
      instrument.** A PR landing engine capability is product whatever its falsification bill; a
      line-majority test on an arm PR is the disguised minimum-PR-size rule wearing the stop
      condition's clothes. (Ruling: Scott, PR #159.)
      - **The classification is named in the PR body and is challengeable** — which is what keeps
        it from being self-serving. The naming obligation makes the claim reviewable at the moment
        it is made, and the line ratio keeps its own separate instrument, so a purpose-classified
        product PR that is *also* drifting is still visible in the figure.
    - **Exemptions are spent only by a principal's explicit order or stamp, never by
      self-classification.** "This PR wasn't elective" is a defence *every* drifting PR can plead,
      so it is inadmissible **from the actor**, however true it happens to be. What discharges the
      condition is a token from outside: a stamp, or a direct order. Absent such a token the
      condition trips regardless of how good the reasons feel, and the actor's job is to *flag* it
      rather than to rule on it. The counter then resets on product work, so the accounting closes
      exactly one PR wide. (Ruling: Scott, PR #113, on the agent's own flag.)
- **Control work is a debt against the product, so it is charged, deferred, or declined — never taken because it is available.** — [product-and-overhead](docs/laws/product-and-overhead.md#control-work-is-a-debt-against-the-product-so-it-is-charged-deferred-or-declined--never-taken-because-it-is-available)
- **A zero-fail board is not a green light, it is a lost instrument.** — [product-and-overhead](docs/laws/product-and-overhead.md#a-zero-fail-board-is-not-a-green-light-it-is-a-lost-instrument)
- **A representation is not a recognizer, and 93.6% of the board wants the representation.** — [product-and-overhead](docs/laws/product-and-overhead.md#a-representation-is-not-a-recognizer-and-936-of-the-board-wants-the-representation)
- **Decisions serve the thesis directionally, or they are not this project's decisions to make.** — [decisions-and-thesis](docs/laws/decisions-and-thesis.md#decisions-serve-the-thesis-directionally-or-they-are-not-this-projects-decisions-to-make)
- **Decision-before-code.** — governance, retained; body in
  [decisions-and-thesis](docs/laws/decisions-and-thesis.md#decision-before-code). Design choices
  get `docs/decisions/NNNN-*.md` (context, options, choice, consequences) *before*
  implementation. Decisions Scott must make are flagged in reports, not made for him. Its
  counterweight is the product rule above: a decision doc is *not* product work either, so **one
  ADR earns one implementation**, and an ADR whose implementation has not started is a reason to
  write code rather than another ADR. Deliberation lives in the issue; the ADR is the tombstone.
- **The suite is the oracle.** — [engine](docs/laws/engine.md#the-suite-is-the-oracle)
- **The spec is the objective function; the suite samples it.** — [evidence-and-instruments](docs/laws/evidence-and-instruments.md#the-spec-is-the-objective-function-the-suite-samples-it)
- **A verdict without an identity check is hearsay.** — [evidence-and-instruments](docs/laws/evidence-and-instruments.md#a-verdict-without-an-identity-check-is-hearsay)
- **A re-run green doesn't refute a fail — explaining the fail does.** — [evidence-and-instruments](docs/laws/evidence-and-instruments.md#a-re-run-green-doesnt-refute-a-fail--explaining-the-fail-does)
- **Budget by the quantity the purpose names.** — [evidence-and-instruments](docs/laws/evidence-and-instruments.md#budget-by-the-quantity-the-purpose-names)
- **A stateful instrument measures history until its state is controlled.** — [evidence-and-instruments](docs/laws/evidence-and-instruments.md#a-stateful-instrument-measures-history-until-its-state-is-controlled)
- **Gates.** — governance, retained; body in [gates](docs/laws/gates.md#gates). Proposals land
  behind build tags / config gates; acceptance is the proposal's own suite green (contract §9).
  Nothing defaults on without it.
  - **A flip is never in the mechanism's PR — it is its own stamp-tier event.** Mechanism is
    product and self-merges on a bound green; a flip is **governance** and holds for a principal's
    stamp, so they are separate artifacts with separate verdicts. The SIMD flip (#227/#233) is
    **the procedure**: G-1 measured on the proposal's suite *after* the mechanism exists, forecast
    **pre-registered**, rollback stated, one-line diff. The reason it is structural rather than
    stylistic — *you cannot pre-register a forecast inside the PR that creates the numbers*, which
    is the actor choosing the instrument that judges the actor, one level up from the ratio. So a
    mechanism PR that would "also flip while we're here" is two verdicts wearing one green.
    (Ruling: Scott, PR #252.)
- **A third verdict needs a structural bound, not just a watched one.** — [boards-and-buckets](docs/laws/boards-and-buckets.md#a-third-verdict-needs-a-structural-bound-not-just-a-watched-one)
- **A skip is not a verdict.** — [boards-and-buckets](docs/laws/boards-and-buckets.md#a-skip-is-not-a-verdict)
- **A control's green must be falsifiable, and the way to know is to break it.** — [controls](docs/laws/controls.md#a-controls-green-must-be-falsifiable-and-the-way-to-know-is-to-break-it)
- **A comparison against an empty set succeeds, so a control that compares needs a vacuity check.** — [controls](docs/laws/controls.md#a-comparison-against-an-empty-set-succeeds-so-a-control-that-compares-needs-a-vacuity-check)
- **A tripwire whose subject dissolves is re-pointed, never closed.** — [controls](docs/laws/controls.md#a-tripwire-whose-subject-dissolves-is-re-pointed-never-closed)
- **A test named for a partition must be checked against the partition, not against its own case labels.** — [controls](docs/laws/controls.md#a-test-named-for-a-partition-must-be-checked-against-the-partition-not-against-its-own-case-labels)
- **Gates never manufacture malformedness.** — [gates](docs/laws/gates.md#gates-never-manufacture-malformedness)
- **Artifacts become oracles.** — [graves-and-sweeps](docs/laws/graves-and-sweeps.md#artifacts-become-oracles)
- **Sweep after a grave.** — [graves-and-sweeps](docs/laws/graves-and-sweeps.md#sweep-after-a-grave)
- **Lessons are indexed by shape, not by file, so the sweep runs backwards too.** — [graves-and-sweeps](docs/laws/graves-and-sweeps.md#lessons-are-indexed-by-shape-not-by-file-so-the-sweep-runs-backwards-too)
- **Floors bound the catastrophic case; only an exact count sees a small silent loss.** — [controls](docs/laws/controls.md#floors-bound-the-catastrophic-case-only-an-exact-count-sees-a-small-silent-loss)
- **Reconcile an extent, never floor it.** — [controls](docs/laws/controls.md#reconcile-an-extent-never-floor-it)
- **A total is not a ledger.** — [controls](docs/laws/controls.md#a-total-is-not-a-ledger)
- **A suspiciously clean result is a tell, and *exactly zero* is the cleanest one.** — [controls](docs/laws/controls.md#a-suspiciously-clean-result-is-a-tell-and-exactly-zero-is-the-cleanest-one)
- **A control isn't born until it has been watched die.** — [controls](docs/laws/controls.md#a-control-isnt-born-until-it-has-been-watched-die)
- **A status field is a citation to an approval, and approvals are artifacts with provenance.** —
  governance, retained; body in
  [decisions-and-thesis](docs/laws/decisions-and-thesis.md#a-status-field-is-a-citation-to-an-approval-and-approvals-are-artifacts-with-provenance).
  An ADR's `Status:` is held open until a stamp exists to point at, and *an ADR marked accepted on
  a stamp nobody gave is a fabricated citation about the project's own governance* — worse than a
  wrong option, since a wrong option is arguable and a forged provenance is not. The actor states
  the case and flags it; a principal's stamp is what closes it, and then the record keeps **both**
  the stamp and the interval it spent open. (Ruling: Scott, PR #142.)
- **Unreachability is a grave only when it's silent.** — [graves-and-sweeps](docs/laws/graves-and-sweeps.md#unreachability-is-a-grave-only-when-its-silent)
- **A design debt is discharged by a tripwire, never by an intention.** — [controls](docs/laws/controls.md#a-design-debt-is-discharged-by-a-tripwire-never-by-an-intention)
- **A control scoped to the current sample inherits the current blind spot; scope controls to the space.** — [controls](docs/laws/controls.md#a-control-scoped-to-the-current-sample-inherits-the-current-blind-spot-scope-controls-to-the-space)
- **No cgo. Pure Go.** — [engine](docs/laws/engine.md#no-cgo-pure-go)
- **Parsers prove progress, they don't assume it.** — [engine](docs/laws/engine.md#parsers-prove-progress-they-dont-assume-it)
- **Fixtures cite the suite, and the citations are checked.** — [citations](docs/laws/citations.md#fixtures-cite-the-suite-and-the-citations-are-checked)
- **A doc comment's identifier is a citation, and it gets a resolving check.** — [citations](docs/laws/citations.md#a-doc-comments-identifier-is-a-citation-and-it-gets-a-resolving-check)
- **Three provenance categories: cited, derived, synthetic.** — [citations](docs/laws/citations.md#three-provenance-categories-cited-derived-synthetic)
- **A guard's trigger predicate is itself a claim about the space, and an under-matching one fails silently by construction.** — [controls](docs/laws/controls.md#a-guards-trigger-predicate-is-itself-a-claim-about-the-space-and-an-under-matching-one-fails-silently-by-construction)
- **Verdict channel and mechanism channel are different instruments.** — [evidence-and-instruments](docs/laws/evidence-and-instruments.md#verdict-channel-and-mechanism-channel-are-different-instruments)
- **A ruling retroactively falsifies prose written before it, so accepting a ruling includes sweeping for the sentences it orphaned.** — [citations](docs/laws/citations.md#a-ruling-retroactively-falsifies-prose-written-before-it-so-accepting-a-ruling-includes-sweeping-for-the-sentences-it-orphaned)
- **Coverage is a claim: an instrument's domain is an assertion it cannot check about itself.** — [evidence-and-instruments](docs/laws/evidence-and-instruments.md#coverage-is-a-claim-an-instruments-domain-is-an-assertion-it-cannot-check-about-itself)
- **Second-order honesty: apply the discipline to its own output.** — [evidence-and-instruments](docs/laws/evidence-and-instruments.md#second-order-honesty-apply-the-discipline-to-its-own-output)
- **Honest boards.** — [engine](docs/laws/engine.md#honest-boards)
- **Bucketed failures are the work plan — while there are buckets.** — [boards-and-buckets](docs/laws/boards-and-buckets.md#bucketed-failures-are-the-work-plan--while-there-are-buckets)
- **Bucket size estimates the reward, not the job.** — [boards-and-buckets](docs/laws/boards-and-buckets.md#bucket-size-estimates-the-reward-not-the-job)
- **An error from the wrong layer is evidence about where structure was lost.** — [errors-and-testimony](docs/laws/errors-and-testimony.md#an-error-from-the-wrong-layer-is-evidence-about-where-structure-was-lost)
- **An error message is testimony, and fabricated evidence is a lying witness even when the verdict is right.** — [errors-and-testimony](docs/laws/errors-and-testimony.md#an-error-message-is-testimony-and-fabricated-evidence-is-a-lying-witness-even-when-the-verdict-is-right)
- **Comments and ADRs are testimony too, and where prose and the reference's executable disagree, the executable outranks.** — [errors-and-testimony](docs/laws/errors-and-testimony.md#comments-and-adrs-are-testimony-too-and-where-prose-and-the-references-executable-disagree-the-executable-outranks)
- **A hedge is part of a record's content, so prose that resolves an accepted record's open question in passing has forged an agreement.** — [errors-and-testimony](docs/laws/errors-and-testimony.md#a-hedge-is-part-of-a-records-content-so-prose-that-resolves-an-accepted-records-open-question-in-passing-has-forged-an-agreement)
- **When two fields disagree about a value, the suite has handed you a bidirectional control.** — [errors-and-testimony](docs/laws/errors-and-testimony.md#when-two-fields-disagree-about-a-value-the-suite-has-handed-you-a-bidirectional-control)

## Tooling gates

See **decision 0005** for the full policy and its rationale. The short version —
**quality is a gate, not a habit**, because a convention that depends on
remembering decays across session boundaries:

- **Tools are pinned in `tools/go.mod`** via `tool` directives, never in CI
  YAML: `golangci-lint` v2, `govulncheck`, `deadcode`, `benchstat`. Run them as
  `go tool -modfile=tools/go.mod <name>`. A green board on a laptop and in CI
  must mean the same thing. The engine's own `go.mod` stays dependency-free.
- **`make check` is the gate** — fmt-check, build, vet, lint, test, deadcode. It
  is the local mirror of CI, so a surprise in CI is a bug in the Makefile, not
  in someone's habits. `make fuzz`, `make bench`, `make vuln` for the rest.
- **Curated linters, never `enable-all`.** Each enable in `.golangci.yml`
  carries a rationale comment. Lint noise is its own kind of dishonest board: a
  wall of findings nobody reads trains the reflex of scrolling past a warning.
- **gofumpt** (`extra-rules`) as a `--diff` check. Formatting is never a review
  topic.
- **`modernize` held at zero.** The engine reads like 2026 Go — `min`/`max`,
  `slices`/`maps`/`cmp`, range-over-int, iterators where they clarify.
- **Suppression discipline: noticed-and-named, or not at all.** Fix it, or
  `//nolint:<linter> // reason` with a tracking issue, or remove the linter in
  config with a commit message saying why. `nolintlint` requires the reason.
  This is the `ErrTrailingData` ruling applied to lint.
- **Fuzzing is standard equipment.** Every decoder and reader gets a target;
  corpora seed **from the spec suite at run time**, never hand-typed. Short fuzz
  per PR, 10-minute runs weekly. **Crashers are committed** to
  `testdata/fuzz/FuzzX/` — the never-commit-corpora rule is about *provenance*,
  not test data: upstream material we don't own stays vendored, but a crasher is
  authored here, it's a grave's reproducer, and Go's own convention expects it
  in-tree. It is the graveyard's executable annex. (Ruling: Scott, PR #21.)
- **benchstat or it didn't happen.** Performance claims cite `make bench`
  (n≥10, with variance bands), never a single run.
- **`deadcode` findings are classification questions**, not automatic bugs:
  declared-and-tracked passes, silent fails. The allowlist is inline while it has
  one or two entries and **becomes `tools/deadcode-allow.txt`, reason per entry,
  at the third** — the threshold isn't the count, it's that an inline allowlist
  can't hold justifications, and an unexplained entry is the unreachable-error
  pattern again: a suppression wearing a disguise. (Ruling: Scott, PR #21.)
- **A completion state can be true while its payload vanished — verify the artifact, not the
  flag.** *Presence-of-status is not presence-of-content*, and it is silence-is-not-evidence's
  uglier cousin: silence at least *looks* like nothing, where a status field looks like
  everything. The specimen: a grave issue read honestly **CLOSED** — the merge keyword did that,
  correctly — while the closing comment carrying its lesson had been silently eaten, so every
  query that asks "is it closed?" returns the reassuring answer and every reader who follows the
  link finds a tombstone with no inscription. The only move that catches the class is checking
  the **artifact's own measurable property** — the comment count, the body length, the row the
  table should contain — rather than the state that is supposed to imply it. Sited here because
  it is the same instrument confusion the `deadcode` and exit-code rules above name (a verdict
  channel cannot say *why*, and a status channel cannot say *what*), pointed at the tracker
  rather than at a tool: `gh issue close` reporting success is a verdict about the close, never a
  witness to the comment. Generalizes past GitHub — any write whose confirmation is a state
  transition needs its payload read back. **The recurrence is the point, and the specimens are
  counted by a query rather than by this file** — `gh issue list --label type:grave --state
  closed --limit 500 --json number,comments`, whose zero-comment rows *are* the list, and #303 is
  the filed control that runs it mechanically.
  The `--limit 500` is load-bearing, not decoration: `gh issue list` defaults to **30** silently, so
  a count taken without it reports a page as the population — body in
  [evidence-and-instruments](docs/laws/evidence-and-instruments.md#coverage-is-a-claim-an-instruments-domain-is-an-assertion-it-cannot-check-about-itself).
  This rule kept a running tally here until it was pointed out
  that a measured quantity in prose schedules its own next increment; the tally is generated or
  it is deleted, and this one is deleted. (Lesson: Scott, on the PR #247 relay; recurrences
  relayed by chat-Claude; count struck on his ruling, PR #317.)
- **Toolchain currency is a gated upgrade** — Go 1.27 and future linter majors
  land as their own branch with both arches green and a changelog entry. Never a
  drive-by bump in a PR about something else.
- **Spirit clause: linters serve the contract, not the reverse.** When a finding
  fights a deliberate engine design (payload aliasing, `uint64` slots,
  dispatchbench's intentional duplication), suppression-with-reason is the
  *correct* outcome and the reason is the documentation.

## Conventions

- Module: `github.com/scttfrdmn/burroughs` (vanity `burroughs.run` import
  path is a later decision — 0001 records this).
- Go ≥ 1.26. `make check` must be green before any report.
- License: **Apache 2.0**, © 2026 Scott Friedman. `LICENSE` is the verbatim
  upstream text; the copyright line lives in `NOTICE` (Apache 2.0 §4(d)).
- Fetched/vendored material (spec suite) lives under gitignored paths;
  never commit upstream test corpora.
