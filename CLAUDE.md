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
right; they are never the deliverable. State of play, measured rather than felt:
`internal/interp` holds **0 engine lines** against 493 test lines, and the board is
**4162 pass / 0 fail / 60872 unsupported** — 93.6% of the corpus unanswered, essentially
all of it behind the one artifact that does not exist yet. A phase is judged by its
product, so v0 is early, and the next PR adds engine lines.

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
  unmoved, the product work this PR is overhead *for*.
- **engine / instrument lines** for the diff (non-test `.go` versus `_test.go`), which is
  the ratio the *product* rule above is enforced by. Its purpose is to make the
  wheel-spinning visible *per PR* rather than only in a trailing window — the 1:1.8 →
  1:5.1 drift was invisible precisely because each PR was individually defensible.

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
   when to look. Same error as reading a verdict off a tool's stderr.
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
a bounded loop that gives up loudly, which is the one honest `sleep` in this file. If nothing
else is ready to do, say what is pending and stop: an idle turn costs nothing, a blocked tool
call costs the whole wait. (Directive: Scott, PR #103 — *"stop using sleep"*, and he was right
to be terse about it, the rule having already been written here by the agent that broke it.)

The first two are *verdict channel and mechanism channel are different
instruments* applied to time and to identity: ask the right channel, and ask it
about the right run.

The one honest timer is the `sleep 2` inside that loop: GitHub has no
"run created" event to block on, so appearance genuinely has to be *polled*. Note
the difference — the loop re-asks a real question until it gets an answer and
gives up loudly after a bounded wait, where a bare `sleep` asserts an answer. When
no completion signal exists, poll for the condition; never stand in for it with a
duration.

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

- **The phase's product is the work; instruments are overhead on it.** This rule is
  first because it governs what gets *selected*, upstream of every rule below about
  doing selected work well. v0's product is a **running interpreter** — decoder →
  validator → interpreter over 0002's internal form. A control, a census, a board
  bound, a changelog gate, a citation sweep: each is overhead that must be *charged to*
  a piece of product work, and the honest accounting is per-PR, not per-session.
  - **Every PR states its unsupported-column delta, and a zero is a confession.** The
    board's `unsupported` count is the phase's real progress measure once fail hits
    zero; `Board` lines already carry it. A PR that moves it by nothing says so, in
    those words, and names the product work it is overhead *for*. The gate already
    exists — `unsupportedCeiling` in `spec_test.go`, a **ceiling**, which per 0013 rots
    by the system working — so lowering it *is* the record of progress and no second
    mechanism gets built for this (*one concept, one trigger*, #82). A PR that drains
    the column lowers the ceiling in the same PR, exactly as `textFailCeiling` fell
    stepwise with a per-PR account.
  - **The actor never chooses the instrument that judges the actor.** The umbrella the two
    rules below were always standing under, written down once so it does not have to be
    rediscovered a third time: not choosing the measure, not granting the exception — same
    root. Both were violated on #113 in the same PR, both plausibly, and the tell in each
    case was that the reading favouring the actor was *also* the reading the actor
    proposed. Where a judgement is about the work, the actor makes it; where it is about
    the actor, the actor's job is to *state the case and flag it*, and a principal rules.
    The general form covers instruments not yet invented, which is why it is worth having
    above the two specific ones. (Compression: Scott, PR #113.)
  - **Instrument-to-engine ratio is quoted, not felt.** Measured over the trailing six
    merges it went **1:1.8 → 1:5.1** (engine 2007→463 lines, test 3681→2347) while the
    unsupported column did not move at all. That is the number that made this rule; a
    ratio worsening while the column stands still is the definition of spinning, and it
    was invisible because every individual PR was defensible.
    - **The comparator is uniform and fixed: engine = code in the module path;
      instrument = tests, generators, harness. No per-file pleading, ever.** *A ratio
      whose comparator moves per-PR measures advocacy instead of drift* — which is
      precisely what was attempted: #113 argued its accept-direction control onto the
      engine side, on the true premise that the standing rule calls such a control
      product work, and the two readings differed by **1:5.2 versus 1:1.1**. Both
      numbers were honest and the *choice between them* was the dishonesty, because the
      actor picking the flattering comparator is the actor being measured. Product-work
      classification (which governs *selection*, above) and ratio classification (which
      measures *drift*) are now deliberately different questions with different answers
      for the same file: `internal/gen/xcorpus/accept_test.go` is product work to take
      and instrument to count. The uniform rule quotes uglier — #113 is **1:6.6** under
      it — so **recalibrate the threshold once against the uniform comparator and let
      historical quotes stand with their era noted**; the 1:1.8 → 1:5.1 figures above
      predate it. A claim needs a fixed comparator more than it needs flattering values.
      The recalibration is **#117**, and Scott spent the exemption token for it in advance
      when he ordered it — a threshold asserted without its trailing window re-measured
      would be the number-you-haven't-run that era-marking these figures exists to avoid.
      (Ruling: Scott, PR #113.)
  - **Two consecutive instrument-only PRs is a stop condition.** Not a soft
    preference — stop and take product work, or get Scott's word to continue. The
    ratchet only turns one way otherwise, because control work is always available,
    always passes review, and always produces a clean green.
    - **Exemptions are spent only by a principal's explicit order or stamp, never by
      self-classification.** "This PR wasn't elective" is a defence *every* drifting PR
      can plead, and every PR in the 1:1.8 → 1:5.1 drift could have pleaded it — so it
      is inadmissible **from the actor**, however true it happens to be. What discharges
      the condition is a token from outside: a stamp (#113's contract amendment), or a
      direct order (#103's *"do it now"*). Absent such a token the condition trips
      regardless of how good the reasons feel, and the actor's job is to *flag* it rather
      than to rule on it. The counter then resets on product work, so the accounting
      closes exactly one PR wide. Note the shape — this is the ratio-comparator ruling
      one level up: there the actor must not choose the measure, here the actor must not
      grant the exception. (Ruling: Scott, PR #113, on the agent's own flag.)
- **Control work is a debt against the product, so it is charged, deferred, or
  declined — never taken because it is available.** The genuine finding that a control
  is missing is *filed*, and filing it discharges the obligation (*a design debt is
  discharged by a tripwire, never by an intention* — that rule says file the tripwire,
  and it does **not** say build it now). The exception, and it is the only one: a
  control that would catch an **accept-direction** defect the suite cannot see (§9 G-3)
  is product work, because the suite scores such defects green by construction and
  nothing else will find them. `optable`'s reference agreement and #88's twelve
  wrongly-rejected valtypes are the paradigm; a citation sweep is not.
- **A zero-fail board is not a green light, it is a lost instrument.** *Bucketed
  failures are the work plan* presumes buckets. When fail reaches zero the project has
  not finished, it has lost the thing that was pulling it toward engine work — and the
  fallback (deferral citations, controls, metadata) is all overhead by nature, so the
  gradient silently inverts. At zero fail the plan comes from **the largest unsupported
  stratum and the artifact it names**, and that artifact is stated in the PR. Found the
  way these things are: 4162/0 was reached and the next three PRs were instruments.
- **A representation is not a recognizer, and 93.6% of the board wants the
  representation.** Both front ends recognize and retain nothing — 28 of the decoder's
  29 `decode*` functions return bare `error` and the 29th returns `(bool, error)` where
  the bool means *"this section has a grammar"*, not data; 0011 makes `text.ReadModule`
  error-only by design — so `binary.Module` is `{Version, Sections}` of aliased bytes and
  **nothing in the codebase can represent a module**. That is one missing artifact (0002's internal
  form) behind four blocked items: #7 execution, #9 validation, #67's half-2
  comparator, and the text encoder's target. When a single artifact blocks the majority
  of the board, building anything else is choosing the smaller number — and the
  encoder-first framing was wrong for exactly this reason: it bridges to a destination
  that does not exist. **Grow the representation out of the path that has a
  conformance record** (the decoder, 4162 vectors), never out of the path that has
  never accepted a module — that is 0006's load-bearing-spot rule and 0011's own
  option-B refusal.
- **Decisions serve the thesis directionally, or they are not this project's decisions
  to make.** The project has a **central core and thesis** — contract §0: a
  *language-directed engine* whose host contract, fast paths, and API surface are
  designed to the specification of the Go runtime, **correctness-neutral and
  performance-partisan**, with §1's non-goals naming what is deliberately given up
  (peak throughput parity, browser embedding, non-Go ergonomics, v0 hardening). Every
  option in an ADR is argued toward that, and the question a decision must answer is
  *"which option moves the engine toward being Go's engine?"* — not "which is more
  elegant", "more general", or "more like other runtimes".
  - **The paradigm is 0002, and it is worth copying exactly.** The side table did not
    lose on aesthetics; it lost because its win served *many short-lived modules*,
    a workload **§1 disclaims** — Go guests are megabytes that load once and run for
    hours, so rewrite's build cost amortizes to nothing on the thesis workload. The
    option died *on the thesis*, and that is the intended way for a design to die here.
  - **Generality without a Go-shaped consumer is a non-goal wearing a virtue's
    clothes.** Burroughs is allowed to be narrow — narrowness is the whole design
    philosophy, the B5000 lesson. So "but another guest language might want X" is not
    an argument in this repo unless X is spec conformance, which is
    non-negotiable for a *different* reason (correctness-neutral, §9 enforces it).
  - **State the direction, not just the choice.** An ADR names which thesis clause or
    non-goal the decision advances, and a decision that cannot cite one is a signal the
    question belongs upstream with Scott — or is not this project's question. Directional
    silence is how a general-purpose runtime gets built by accident, one locally
    reasonable choice at a time.
- **Decision-before-code.** Design choices get `docs/decisions/NNNN-*.md`
  (context, options, choice, consequences) *before* implementation.
  Decisions Scott must make are flagged in reports, not made for him.
  Its counterweight is the product rule above: a decision doc is *not* product work
  either, so **one ADR earns one implementation**, and an ADR whose implementation has
  not started is a reason to write code rather than another ADR. Deliberation lives in
  the issue; the ADR is the tombstone.
- **The suite is the oracle.** A feature exists when its spec tests pass.
  Claims of correctness cite counts (`N/N green`), not impressions.
- **The spec is the objective function; the suite samples it.** The oracle
  answers the questions it is asked — it does not define correctness. So never
  buy pass count with a check that is wrong about inputs the suite has no vector
  for: that is overfitting to the oracle, and it is invisible on the board by
  construction. A decoder that rejects valid modules is worse than one that
  misses an invalid one. When the cheap check would pass the vectors and be
  wrong in general, leave the bucket open and say why (contract §9 G-3; the
  ruling on `data count section required`, #22).
- **A verdict without an identity check is hearsay.** Bind a result to the thing
  it judges: the question is never "is it green," it is "is *this commit*
  green." A mechanism that cannot prove which run it is quoting is not a
  witness. A wait that returns the wrong run's verdict is worse than one that is
  merely slow — see *Waiting on CI* for the shape this took in practice.
- **A re-run green doesn't refute a fail — explaining the fail does.** The
  temporal cousin of the identity-check law: two runs are two witnesses, and the
  favourable one does not impeach the unfavourable one merely by speaking second.
  A flake is a *diagnosis*, not a default, and it is earned by bounding the cause
  — `fuzz-smoke`'s `context deadline exceeded` was ruled flake only after the
  interesting hypothesis (a pathological parser input) was measured and killed:
  34ms worst case over adversarial shapes, 6× throughput margin, so wall-clock
  starvation. Re-running until green, with nothing explained, is the same reflex
  as scrolling past a warning. (Ruling: Scott, PR #27; the fix is #28.)
- **Budget by the quantity the purpose names.** A gate whose budget unit differs
  from its purpose unit will eventually fail for reasons that are not findings.
  `fuzz-smoke` exists to catch a target that stopped building or a corpus that
  regressed — its purpose is *executions*, and it was budgeted in *seconds* on
  hardware whose throughput varies 6× from a dev box. Wall-clock budgets on
  shared runners are timing-sensitive by construction. Sharpened by measuring the
  runner: it does not run *slowly*, it **stalls and sprints** — 47 dead
  three-second windows, one 18s unbroken, against 605k/sec bursts faster than the
  dev box. So the ~70k/sec "floor" was an average over a bimodal distribution,
  describing neither mode, and *a wall-clock budget against a bimodal rate is a
  coin flip on where the stalls land*. The unit fix cost 65s → 3m26s and the
  budgets were **not** shrunk to buy it back, because the job's purpose is the
  questions. The sweep's negative space is half the lesson: `make fuzz` and the
  nightly runs stay wall-clock, their purposes being durations — same flag, three
  purposes, two units, stated at each site. *A sweep that knows where the class
  doesn't apply is a sweep that understood the class.* (#28.)
- **A stateful instrument measures history until its state is controlled.**
  *Fuzzing is stateful — a measurement that doesn't clear the corpus is measuring
  the last run.* Adopting the executions budget needed proof a real crasher still
  fails under it; the second reading was a `FAIL` in 0.175s that looked like a
  spectacular find and was a **replay of a crasher the previous run had written
  into `testdata/fuzz`** — an instrument contaminated by its own priors. Generalizes
  past fuzzing: any instrument persisting state between measurements is reporting
  history, not the present, until that state is cleared or asserted. The sibling
  law is that **a fuzzer has two halves that fail independently, so certify them
  independently**: seed-replay proven by reintroducing a *known* defect (grave
  #18's zero-progress bug), exploration proven by a mutation-only needle *no seed
  can reach*. A budget that passes the first and never exercised the second has
  tested a corpus, not a fuzzer. (#28.)
- **Gates.** Proposals land behind build tags / config gates; acceptance is
  the proposal's own suite green (contract §9). Nothing defaults on
  without it.
- **A third verdict needs a structural bound, not just a watched one.** `gated`
  is honest — a vector whose question presumes a declined feature was never
  asked, so scoring it pass or fail both lie — but any verdict that is neither
  pass nor fail is a lever for emptying a board by fiat. Per-vector allowlists
  are vigilance: they stop a vector hiding *unnoticed*. The structural control is
  a CI lane with **every tracked gate on, where the gated count must be zero** —
  under full features every vector answers on the merits, so a vector parked in
  `gated` on the default board is simultaneously being honestly *failed* in the
  all-on lane, and stays failed until its feature actually works. That makes a
  deferral something that cannot become a disappearance. (Ruling: Scott, PR #27.)
- **A skip is not a verdict.** `requireSuite` skips when the corpus is absent, so
  a board test in a job that never vendored the suite passes by asking nothing —
  a green that has never once asserted a count. Any job whose verdict depends on
  a corpus asserts the corpus is present *before* trusting a number out of it.
  This is the identity-check law pointed at the oracle's inputs rather than at
  the run: guard the guard, or the guard is decoration. The general form: **a
  precondition that excuses a gate is licensed at one place, or it is a hole.**
  Every skip routes through `internal/testenv`, names what it licenses, and is
  revoked by `BURROUGHS_NO_SKIP=1` — set workflow-wide in CI so strictness is
  inherited rather than remembered. `TestEverySkipSiteIsLicensed` reads the AST,
  because a rule requiring all skips go through one door needs something asserting
  that they do; otherwise the mechanism has the shape it exists to forbid. And
  *silent degradation is a skip one step quieter* — a fuzz seeder that falls back
  to two literal seeds still passes, and only an `f.Log` says the corpus was
  missing. (Directive: Scott, PR #30.)
- **A control's green must be falsifiable, and the way to know is to break it.**
  Write the test, then introduce the defect it names and watch it fail. A test
  asserting a property of code that does not run yet passes for the wrong reason and
  is indistinguishable, on the board, from one that passes for the right one — *a
  green that survives the bug it names is a control in name only*. Found twice in
  one session: a data-segment test that could never fail because the data section is
  not descended into yet (#25), and a strictness helper reporting a fail *and* a
  skip, because `Fatalf`-then-`Skip` leans on `runtime.Goexit` to not return. The
  first was caught by probing, the second by a `testing.TB` fake. Neither was caught
  by the suite, because the suite was never asked.
- **A comparison against an empty set succeeds, so a control that compares needs a
  vacuity check.** The falsifiability law's blind spot, and it is not covered by
  breaking the assertion — you can introduce a defect, watch the control fail, and
  still have a control that passes when it is fed *nothing*. A generated-table drift
  check comparing extractor output to a committed table agrees perfectly when the
  extractor finds zero arms, which is what a moved file or a changed indentation
  produces: an empty table and a green board, the mechanism intact and asserting
  nothing. So any control whose verdict is an agreement, a count, or a comparison
  asserts its input is **non-empty and plausibly sized** — a per-region floor, not just
  a non-nil check. This is *a skip is not a verdict* for code that never had a skip in
  it: the degenerate case is the skip, one step quieter still, because nothing even
  logs. Sibling of the early-return grave (#41's fetch script), where a fast path
  skipped the assertions it existed to run. (Condition on decision 0007; chat-Claude,
  #41.)
- **A tripwire whose subject dissolves is re-pointed, never closed.** A pre-registered
  control names a *risk*, not a code shape, so when the shape it was filed against
  disappears the obligation survives its subject. #33 was filed to catch two opcode
  readers drifting; the reference interpreter defers const-ness to validation, so the
  narrow reader **dissolves** into the full table rather than persisting as a second
  opinion — and the drift risk simply moved, to extractor-versus-reference. Closing it
  as "no longer applicable" would retire a live risk on a technicality. The re-pointing
  is also where scope creep back inward gets caught: the re-aimed control is scoped to
  the *space* (all 256 single-byte opcodes and the tracked prefixes), never to the
  subset today's code touches. (Directive: chat-Claude, #41.)
- **A test named for a partition must be checked against the partition, not against
  its own case labels.** The coverage cousin of the rule above: there, a green
  survives the bug it names; here, a green covers less than its name claims, and the
  pass count is *right* while the coverage is wrong. `TestSectionSizeBothSigns`
  existed to pin both signs of one comparison and pinned the short sign twice — one
  case labelled "grammar consumed MORE than declared" actually produced `declared 7,
  grammar consumed 4`, and the case meant to carry the long sign couldn't, because
  the grammar it needed did not exist yet. Nothing said so, because nothing compared
  the cases to the partition. Check by *printing what the code actually returns for
  each case*, not by reading the labels; then falsify by swapping the comparison's
  operands and confirming the named direction fails. The corollary is the mechanism:
  **when a partition's members share an error value, `errors.Is` is not a partition
  check** — assert the discriminating field (here the message's declared/consumed
  numbers), or every member scores as every other. And a sign the suite never
  exercises is a sign a pass count cannot defend, so it gets a synthetic vector and
  says that it is one. (Grave, #34.)
- **Gates never manufacture malformedness.** *Malformed* is the spec's word: it
  belongs to the grammar, and the grammar here is the **union of the tracked
  set** (§9 G-2) — section id 13 is defined by Wasm 3.0 and so is well-formed,
  while ≥14 is malformed because nothing tracked defines it. Gates partition
  *acceptance* within that grammar; they do not redraw it. A gate-off engine
  meeting a gated feature must still **reject** the module — accept-and-ignore
  silently breaks semantics — but it reports a **feature-named error**
  (*exception handling: gate disabled*), never a spoofed spec string. Reporting
  "malformed" for a module the spec calls well-formed lies about the module to
  conceal the engine's own configuration. So the structural layer (id range,
  order, uniqueness) stays gate-blind, and the features set governs per-section
  and per-opcode acceptance. (Ruling: Scott, #5; queued as a contract amendment
  in #16.)
- **Artifacts become oracles.** Bugs found by hand become regression tests
  in the same session. Graves get marked: a comment at the fix site naming
  the lesson and citing the issue.
- **Sweep after a grave.** A defined-but-never-returned error, an
  unreachable branch, a constant nothing reads — grep for siblings of the
  same shape in the same session. *An error constant with no reachable path
  is a missing check wearing a disguise* (grave, 0003); its inverse face is
  the predicate-property rule, and disguises come in families.
- **Lessons are indexed by shape, not by file, so the sweep runs backwards too.**
  `keywordgen` had already met and solved the wrapped-arm defect — its lexer arm head ends at
  `->` for exactly that reason — and `opgen` reintroduced it because the regexp shape was
  **re-derived instead of copied**: 411 rows where 436 were measured, silent. The graveyard's
  value is only collected when the next author searches it by *structure* rather than by
  filename, because a grave filed against one file reads as a fact about that file and is
  actually a fact about a shape. #78 → #80 → #105 is one structure in three packages that share
  nothing else. So before writing a reader, trigger, or regexp a sibling package already has,
  **read the sibling's version first** — a same-shaped problem next door is a place to read,
  not a place to invent — and title a grave by its shape, not its site. (Ruling: Scott, PR
  #108; grave #105.)
- **Floors bound the catastrophic case; only an exact count sees a small silent loss.** They are
  *different instruments*, not strong and weak settings of one. A floor answers "did the
  extraction happen at all" — a moved file, a changed indent. It cannot answer "did it get
  everything," and `Floors.Lexer` at 350 stayed green through a 411-of-436 loss. What makes that
  an indictment rather than an excuse is that **the 436-row measurement already existed**: the
  sharper instrument was in hand while the looser one did the asserting. So wherever the exact
  count is knowable, pin it *beside* the floor — the floor still covers the catastrophic case a
  diff would report to nobody — and where it genuinely cannot be exact, say so at the site. And
  floor **per partition, never one total**: 400 passes on one authority's 436 alone when the
  other finds zero, an empty half absorbed by a full one, which is the vacuity law with a
  partner to hide behind. (Ruling: Scott, PR #108; grave #105.)
- **A suspiciously clean result is a tell, and *exactly zero* is the cleanest one.** 0014's
  premise — overlap 0, **gap 0** between two authorities — was measured by a probe scoped to
  `plaininstr`, one of five instruction-building productions, which is *the same scope the
  reader had*. Premise and implementation agreed because they shared an assumption, not because
  either was right, and every control-flow instruction joined to nothing. That is the
  witness-correlated-with-subject grave in instrument form, and it is worse than a mis-scoped
  control: review verifies code against claims, and here the two concurred. **A premise measured
  over the same sample the code reads is not a premise, it is an echo.** So interrogate a perfect
  agreement between supposedly independent sets like a green that came too easily — ask what the
  *instrument* could not have seen. The repair needs a detector the mechanism does not supply,
  because asking whether everything the join resolved was resolved is a tautology; being unfit as
  a join key (a naming coincidence, not a derivation) is exactly what makes a signal fit as a
  second opinion. (Ruling: Scott, PR #108; grave #106.)
- **A control isn't born until it has been watched die.** `Extract`'s partition check read as a
  real guard and could not fire on **any** input — `byGrammar` is keyed per token kind, `byLexer`
  per keyword, so `byLexer[kind]` asked whether a keyword is spelled `BINARY`. It was found by
  writing the falsification test and watching it *not fail*, which is the birth requirement
  working exactly as written. A no-op guard and a working guard produce identical output on every
  input that doesn't trip them, which is all of them. So budget for the falsification *passing*:
  that is not the test being wrong, it is the control being **stillborn**, and it is the most
  valuable outcome the exercise has. A lookup across two differently-keyed maps is a no-op
  wearing a predicate's clothes. (Ruling: Scott, PR #108.)
- **Unreachability is a grave only when it's silent.** Declared and tracked,
  it's a TODO with an audit trail — scaffolding wearing a name tag, not a
  missing check wearing a disguise. The test is whether the deferral was
  *named at its definition site* and carries a tracking issue. A sweep that
  turns up a labelled placeholder has still done its job: it forced the
  classification question. (Ruling on `ErrTrailingData`, #6.)
- **A design debt is discharged by a tripwire, never by an intention.** The same
  manoeuvre as the declared-and-tracked ruling above, pointed at *architecture*
  instead of at a constant. Declining to share a structure is legitimate when the
  second consumer doesn't exist yet — building it early means shaping it from its
  only consumer's requirements, in the load-bearing spot. What makes that decline
  honest is that the risk it accepts (two places knowing the same fact, drifting
  silently) is **pre-registered as a failing test in the other work's definition of
  done**, filed and milestoned at the deciding ADR's acceptance. "Convertible into
  a failing test" is a claim about an obligation, not a hope, and the conversion is
  scoped to the *whole* space rather than the cases today's work needs — a
  cross-check narrowed to those is the overfitting failure applied to a control.
  So: *prefer the risk a control can catch, then file the control.* (Ruling: Scott,
  decision 0006; the tripwire is #33.)
- **A control scoped to the current sample inherits the current blind spot; scope
  controls to the space.** The general form of #33's widening: the condition asked
  for agreement over the const-legal *subset*, which would have cross-checked the
  eight opcodes the reader needs today and stayed green while saying nothing about
  whatever opcode either side adds next — a control that freezes at the moment of
  authorship. Scoped to all 256 single-byte opcodes plus the tracked multi-byte
  prefixes, the coverage grows with the thing controlled. Same move as reflecting
  over `Features` rather than listing today's gates: *derive the domain, never
  enumerate it*, because an enumeration is a sample and a sample has a blind spot
  by construction. This is the overfitting law (§9 G-3) turned on the controls
  themselves rather than on the engine. (Ruling: Scott, decision 0006 / #33.)
- **No cgo. Pure Go.** `make check` clean at every commit (see Tooling gates).
- **Parsers prove progress, they don't assume it.** A loop whose exit condition
  and error condition are the same predicate is the zero-progress bug; it
  surfaces as an error only when the offending byte happens to be a delimiter,
  and hangs otherwise. Every reader gets a fuzz target asserting the offset
  moved. *A delimiter set is a claim about what cannot start a token, and one
  that's right for the grammar can still be wrong for the corpus* (grave, #18).
- **Fixtures cite the suite, and the citations are checked.** A hand-typed test
  vector carries a `<file>.wast:N` comment that `TestFixtureProvenance`
  verifies, or it is marked `synthetic` with a reason. A citation nobody
  verifies is a claim, not a citation — two vectors claiming to be "verbatim"
  had drifted, one truncated from 11 bytes to 8. Prefer deriving corpora from
  the suite at run time: no transcription step, no drift.
- **A doc comment's identifier is a citation, and it gets a resolving check.** The
  class was left as convention on the stated criterion *convention until first
  drift* — and the drift arrived, measured: **`constWalk` was cited in three comments
  across three PRs and has never existed in this package.** Not renamed, not moved;
  fiction from the first keystroke, in prose describing where a gate is read. So the
  criterion now cuts the other way and the fixture-provenance treatment extends to
  prose-in-code: an identifier named in a comment resolves to a definition, or the
  comment is phrased historically. This is `TestEveryCitedTestNameResolves` (#93/#94)
  widened from test names to identifiers generally — same trigger discipline, and its
  three paid-for lessons carry over intact: rejoin hyphenated line wraps, scope the
  historical exemption **per sentence** rather than per block, and exclude
  declaration-shape spans rather than backticked ones. The unchecked sibling remains
  the **issue-number** class from #84 (`#NN` in prose asserting *what a defect is* —
  a diagnosis, and `annotations.wast:1` sat misattributed to #55 for three PRs while
  a ceiling's comment called it "not ours"); different oracle, still open. *Split
  issues at the oracle seam* — this half's oracle is local, so this half lands first.
  (Ruling: Scott, PR #113; graves #114, #115, #116.)
- **Three provenance categories: cited, derived, synthetic.** *Entailment from
  checked facts is legitimate provenance; unstated entailment is just synthetic
  with better manners.* A **derived** vector is one the suite implies but does not
  contain — `TestLEBWidthIsPerField`'s accept half asserts a wide-but-legal limits
  minimum is fine, which `binary-leb128.wast` cannot say because it only asserts
  malformedness, and which `:525`/`:217` jointly *bracket* by wanting different
  errors at ten and eleven bytes. What keeps the category from becoming a laundering
  channel is that a derived row **states its premises and its inference**, and
  `TestFixtureProvenance` machine-checks that the premises **resolve** — the
  inference is reviewed by eyes, but a premise citing a line that says something else
  is caught by the same mechanism that catches a drifted transcription. Same shape as
  every other rule here: the human judgement is allowed, the checkable part is
  checked, and silence is not an option. (Ruling: Scott, PR #37.)
- **A guard's trigger predicate is itself a claim about the space, and an
  under-matching one fails silently by construction.** The falsifiability law does
  not reach this: you can break a guard's *assertion*, watch it fail, and still have
  a guard that never fires on most of its population — because a regexp that
  under-matches produces **no finding rather than a wrong one**.
  `TestEveryFixtureFileIsChecked` triggered on `//\s*<file>.wast:\d+`, requiring a
  citation to *open* a comment, while the wat-fixture style puts it in a row field:
  **17 cited rows in two files went unregistered and the board said nothing.** What
  found it was measuring the trigger's **coverage against the population it claims**
  — *coverage is to a trigger what a vacuity check is to a comparison*, and both are
  the same defect class as the empty-set agreement. Two corollaries, both paid for:
  **registration is not verification** (a file registered with a checker that reads
  past everything in it looks checked and is worse than unlisted — only a
  `withRows` floor said so), and **one concept, one trigger** (the duplicated
  regexp is *how* a file came to be registered with a mechanism that could not read
  it). The recurrence proves it is a class, not an incident: a citation row **split
  across two lines** is invisible to a line-oriented trigger, so the file registers
  and contributes zero verified rows — the same defect, one PR later, in the guard
  repaired for it (#80). (Ruling: Scott, #82; grave #78.)
- **Verdict channel and mechanism channel are different instruments.** *An exit
  code is not a mechanism* — the verdict channel can't tell you why. *Don't infer
  a verdict from noise* — the output channel can't tell you whether. Read each
  for what it carries and never substitute one for the other: a tool that exits
  non-zero on findings is asked for its status, a tool that reports on stdout and
  exits 0 is asked for its output, and capturing `2>&1` to test for non-empty
  confuses a cold module cache with a defect (grave, PR #21).
- **A ruling retroactively falsifies prose written before it, so accepting a ruling
  includes sweeping for the sentences it orphaned.** *Truth has a maintenance cost.*
  `ci.yml` said the runner stalls were "tracked in #28's thread"; the no-issue
  ruling made that false the moment it was given, and the sentence would have sat
  there citing a tracking location that does not exist — the drifted-fixture-citation
  defect wearing different clothes, since a citation nobody re-checks is a claim.
  So a ruling is not applied when the decision is recorded; it is applied when
  everything the decision contradicts has been found. Grep for the old answer, not
  just for the place you expect it. (Ruling: Scott, #28.)
- **Second-order honesty: apply the discipline to its own output.** Catching a
  figure as fiction earns nothing if its replacement is quoted with the same
  overconfidence. The ~70k/sec average was correctly called an artefact of a
  bimodal distribution — and then replaced with one run's numbers, one witness
  dressed as an environmental fact. The re-measurement is what separated the two
  claims: *the shape reproduces, the numbers don't* — dead windows and sprint
  bursts in both runs, peaks differing 2× (605k → 1.25M). So the shape is the
  finding and the numbers are the weather, and a two-run range is the honest
  representation of exactly that much knowledge. The sibling of *benchstat or it
  didn't happen*, pointed at environmental measurement: n=1 cannot separate a
  property from an accident of one scheduling. (#28.)
- **Honest boards.** The PR description and the issue tracker reflect
  reality, including what's red. Never quote a suite count that wasn't run.
- **Bucketed failures are the work plan — while there are buckets.** A suite Board line
  reports pass / fail / unsupported, with failures bucketed by the missing feature (for
  the decoder, by expected spec error string). The biggest bucket is the next issue to
  take; a bucket going to zero is a PR's measure of done. Failures are reported, never
  skipped — skipping hides the queue. **At zero fail this rule has no subject and the
  fallback is not "whatever is available"** — see *a zero-fail board is not a green
  light, it is a lost instrument* above: the plan becomes the largest unsupported
  stratum and the artifact it names.
- **Bucket size estimates the reward, not the job.** The board buckets by the
  *expected spec string*, which is the right key for scheduling — it names what a
  user would see — but a spec string cuts across mechanism, so one bucket is
  usually several jobs. The LEB 18 partitioned into 13 blocked on the code-section
  grammar (#39 — *not* #7, which is the interpreter core; the conflation was carried in
  a session summary and cost real time before it was checked), 4 reachable immediately,
  and 1 unrelated question about the functype tag; the four cost an afternoon and the
  thirteen were a milestone. So
  take the biggest bucket, then **partition it by mechanism before estimating it**,
  and say in the PR which members were reachable and which are waiting on what. A
  bucket quoted as a single number is a plan that has not been made yet.
- **An error from the wrong layer is evidence about where structure was lost.**
  When a lower grammar is missing, its bytes do not vanish — they leak upward and
  get misread by whatever grammar *is* running, so the error names a field the
  input never contained. `malformed section id: 128` on eight LEB vectors was the
  code section's immediates being read as section ids: a diagnosis, not a defect in
  the section-id check. Read the mismatch between the error's layer and the
  vector's layer as a pointer to the missing descent. The same tell has an
  intra-layer form — an error message that reports a byte the image never held (the
  functype tag reconstruction printing `0x5e` as `0xde`) is the engine lying about
  its input, and no suite can see it, because the harness matches the sentinel and
  reads no further than the expected string does. (#36.)
- **An error message is testimony, and fabricated evidence is a lying witness even
  when the verdict is right.** The rule is **match what the suite's expected string
  contains** — and for most vectors that string stops at the sentinel, which is
  exactly why everything past it is ours alone to keep honest.
  `ErrMalformedFuncType`'s reconstruction or'd a high bit in for every negative form
  and reported a `0x5e` array tag as `0xde`: the right verdict, quoting a byte the
  image never held, and green on every board by construction. So a message that names
  a value from the input gets *printed for real inputs* before it is trusted — the
  print-don't-trust check applies to the half of the error the oracle cannot see, and
  that is where it earns the most. Its sibling above is the wrong-layer tell: both are
  the engine being wrong about its own input, one across layers and one inside a
  format string. (Ruling: Scott, PR #37; grave #36.)
  **Refinement, not repeal (#38):** *some expected strings carry data.*
  `binary.wast:1218` expects `illegal opcode ff` — the byte is *inside* the sentinel —
  so for those vectors message rendering is **oracle-covered**, and the invented-bits
  class has suite teeth in the one place vectors exist. The doctrine was never "ignore
  message text"; it was "the oracle reads exactly as far as its expected string
  does." Print-checks cover everywhere it stops short, which is nearly everywhere.
  Read the vector to know which case you are in — and note the shape: the sibling of
  the buried defect is the newly-checked case. (Ruling: chat-Claude, #38.)
- **Comments and ADRs are testimony too, and where prose and the reference's
  executable disagree, the executable outranks.** 0003's LEB section said "the order
  matters" and then prescribed the wrong order, so the implementation followed its
  documentation faithfully and every reviewer who checked code against claims found
  agreement — *the defect stated as the rule*, which is the strongest camouflage a bug
  can wear, because review verifies code against claims. The mechanical tell is in the
  same sentence: the order was "derived from the actual vectors", and those vectors
  were precisely the ones where the two conditions do not overlap, so they could not
  distinguish the orderings. An order-of-tests claim needs an *authority*
  (`interpreter/binary/decode.ml`), never a derivation from a sample that cannot
  falsify it — the scope-controls-to-the-space law pointed at documentation. And a
  ruling like this one is discharged by **appending** to the ADR, body preserved: the
  record of what was believed, and of why it survived review, is the part worth
  keeping. (Ruling: Scott, PR #37; the correction is in 0003.)
- **When two fields disagree about a value, the suite has handed you a
  bidirectional control.** The width-parameterized design's dividend: identical
  bytes `80 80 80 80 10` are *integer too large* as a data-segment memory index and
  perfectly legal as a limits minimum, because one field is 32 bits and the other
  64. Pin **both** directions in one test — a single width being wrong then fails
  the two halves in opposite directions, where either half alone would look like a
  plausible reading. Prefer such a pair over two independent assertions whenever a
  value's verdict depends on the field rather than on the bytes. (#36.)

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
