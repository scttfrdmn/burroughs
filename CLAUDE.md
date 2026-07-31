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
gh run watch "$RUN" --compact --exit-status   # run this with run_in_background
```

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

- **Decision-before-code.** Design choices get `docs/decisions/NNNN-*.md`
  (context, options, choice, consequences) *before* implementation.
  Decisions Scott must make are flagged in reports, not made for him.
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
- **Bucketed failures are the work plan.** A suite Board line reports pass /
  fail / unsupported, with failures bucketed by the missing feature (for the
  decoder, by expected spec error string). The biggest bucket is the next
  issue to take; a bucket going to zero is a PR's measure of done. Failures
  are reported, never skipped — skipping hides the queue.
- **Bucket size estimates the reward, not the job.** The board buckets by the
  *expected spec string*, which is the right key for scheduling — it names what a
  user would see — but a spec string cuts across mechanism, so one bucket is
  usually several jobs. The LEB 18 partitioned into 13 blocked on the code-section
  grammar (#7), 4 reachable immediately, and 1 unrelated question about the
  functype tag; the four cost an afternoon and the thirteen are a milestone. So
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
