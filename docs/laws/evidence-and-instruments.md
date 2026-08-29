<!-- Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0 -->

# Laws — Evidence and instruments

Which channel carries which answer, and what a measurement is worth.

Relocated from `CLAUDE.md`'s `## Disciplines` section, **verbatim**, when that file
became an index (see the restructure PR). Nothing was rewritten in the move: the bodies below
are the text as it stood, which is why superseded wordings still appear inside them where a
later ruling amended rather than replaced. The per-law recall keys `CLAUDE.md` carried were
retired with the index economy when that file became a brief and a pointer page (Scott's
directive, the four-workstream brief of 2026-08-17); the laws themselves were not touched.

**`claudeMDCeiling`, named in the ninth specimen of the coverage law below, no longer resolves** — it
was retired with that economy. The specimen stands because the lesson is about a *unit* (`len(str)`
counts characters, `wc -c` counts bytes) and the ceiling is only the quantity it was mismeasured on.
The sentence naming it is in the past tense, and says at the name that the instrument is retired: **a
body naming an instrument that no longer exists is a dangling reference**, so the amendment is to the
tense and to the one clause that carried it, never to the lesson (Scott's ruling, PR #377's relay).

`CLAUDE.md` links this family, and the two halves of that link are checked:
`TestMarkdownLinksResolve` (`internal/testenv`) that every pointer in every markdown file in the
tree resolves, and
`TestLawFamiliesAreReachable` that every family here is reachable from it — a law nobody can
reach is a law out of context.

### The spec is the objective function; the suite samples it.

- **The spec is the objective function; the suite samples it.** The oracle
  answers the questions it is asked — it does not define correctness. So never
  buy pass count with a check that is wrong about inputs the suite has no vector
  for: that is overfitting to the oracle, and it is invisible on the board by
  construction. A decoder that rejects valid modules is worse than one that
  misses an invalid one. When the cheap check would pass the vectors and be
  wrong in general, leave the bucket open and say why (contract §9 G-3; the
  ruling on `data count section required`, #22).

### A verdict without an identity check is hearsay.

- **A verdict without an identity check is hearsay.** Bind a result to the thing
  it judges: the question is never "is it green," it is "is *this commit*
  green." A mechanism that cannot prove which run it is quoting is not a
  witness. A wait that returns the wrong run's verdict is worse than one that is
  merely slow — see *Waiting on CI* for the shape this took in practice.

### A re-run green doesn't refute a fail — explaining the fail does.

- **A re-run green doesn't refute a fail — explaining the fail does.** The
  temporal cousin of the identity-check law: two runs are two witnesses, and the
  favourable one does not impeach the unfavourable one merely by speaking second.
  A flake is a *diagnosis*, not a default, and it is earned by bounding the cause
  — `fuzz-smoke`'s `context deadline exceeded` was ruled flake only after the
  interesting hypothesis (a pathological parser input) was measured and killed:
  34ms worst case over adversarial shapes, 6× throughput margin, so wall-clock
  starvation. Re-running until green, with nothing explained, is the same reflex
  as scrolling past a warning. (Ruling: Scott, PR #27; the fix is #28.)

### A failure establishes an event, not a condition — and "unavailable" is self-serving where "flake" is not.

- **A failure establishes an event, not a condition — and "unavailable" is
  self-serving where "flake" is not.** The law above run backwards. A green does
  not refute a fail; a fail does not establish a *standing state* either. Two
  timed-out probes are two events, and "the instrument is gone" is a claim about
  every future run — a different kind of statement, needing different evidence,
  and the cheapest evidence against it is to try the thing again.
  - **The asymmetry is the reason this is a key and not a footnote on the flake
    law.** Both diagnoses are inferences from a transient failure, and they differ
    in which direction they cost the actor. Calling a fail a *flake* incurs an
    obligation: bound the cause, or the diagnosis is inadmissible. Calling an
    instrument *unavailable* **retires** one — the check need not run, and the
    procedure that ordered it can be rewritten to match. So the incentive gradient
    points at exactly one of the two readings, which means the reading has to be
    earned against that gradient rather than adopted because it fits the evidence.
    An actor's diagnosis about its own obligations is the class where the actor
    does not rule: *state the case and flag it.*
  - **The specimen.** `docker version` timed out twice on the arm64 dev box across
    two PRs, and the absence was reported honestly both times. On the third
    reading the honest report was replaced by an inference — the daemon is
    unrecoverable — and three artifacts were built on it in one session: a
    procedure change demoting the local cross-check, a `CLAUDE.md` edit ordering CI
    as primary, and a *law* in this file blessing the demotion. Scott restarted the
    daemon and it came back, then named `janus.local` — a native x86_64 machine
    that had been on the network the entire time and that no procedure here had
    ever mentioned. **The cheap remedies were a restart and a question, and neither
    had been tried.** The question was the more expensive omission: it would have
    produced a *better* instrument than the one declared dead, not merely the same
    one revived.
  - **A corrected law and a law on a refuted premise are different artifacts**
    (ruling: Scott, on the mint). The demotion law was deleted rather than amended,
    because a law is read by every future session and inherits the truth of its
    example — a specimen ending in a false sentence teaches the false sentence,
    and it teaches it with this corpus's authority. It exists nowhere in history:
    it was written, falsified within the hour, and removed before its first commit.
    That is the record it deserves, and this entry is the part of it worth keeping.
  - Operative, for a check that will not run: report the **event** (this probe, this
    time, this exit code), never the condition. Distinguish *hung* from *down* from
    *absent*, since a hung mechanism does not refuse but hangs and everything built
    on it inherits the hang — a bounded probe reading exit 124 is the cheapest
    instance of *verdict channel and mechanism channel are different instruments*
    applied to availability. Then repair, or escalate, before any procedure is
    rewritten; `scripts/xcheck-amd64.sh` carries the shape as code, with `NOT RUN`
    at exit 4 for both unavailability paths and every exit path naming its
    instrument. (Ruling: Scott, PR #339 review, minting a corrected key over a
    withdrawn one: *"a failure establishes an event, not a condition — and
    'unavailable' is self-serving in a way 'flake' isn't, because it retires an
    obligation. No control can reach it, which is exactly what index space is
    for."*)
  - **The funding was retroactive, and the sequence should read that way wherever
    it is recorded.** The mint was authorized on a figure nobody had checked: the
    PR claimed the index had returned **165 bytes**, and that was `len(str)` in
    Python over a UTF-8 file dense with em-dashes, so it counted *characters*. By
    `wc -c` the section had **grown by 132 bytes**. The order in which this actually
    happened is: Scott authorized the key on a wrong number, the agent found the
    error afterwards, and the 283 bytes were then paid for by trimming that same
    section to **109 under its baseline**. Scott's own words on being shown it: *"I
    authorized that mint on a number I didn't check, and you found it. The law is
    real and the funding was retroactive — that's the honest sequence and it should
    read that way wherever it's recorded."* Kept because a record that quietly
    reorders the two makes a governance decision look better evidenced than it was,
    which is the *status field is a citation to an approval* defect applied to a
    number instead of to a stamp. The unit half is the ninth specimen under
    *coverage is a claim*: `os.Stat` and `wc -c` answer in the unit the ceiling is
    written in.
  - **The same organ in a different register: a hedge that drifts toward the
    reading which retires an obligation** (cross-reference ordered by Scott, PR
    #347 relay — *"they are the same organ; the second is already minted and the
    first is its specimen in a different register"*). This law's register is
    **diagnosis**, about a failure that already happened. The other is
    **forecast**, about work not yet done, and it reads: while costing #343's
    remaining causes, the agent wrote that a too-permissive port would be "caught
    by the reject-direction corpus, which is large", then measured it and found
    **21 admissions in one file** — a figure that was available the whole time and
    that changed the plan when it arrived. Its own correction named the mechanism:
    *vague in the direction that flattered the plan.* The gradient is identical.
    "Unavailable" retires the obligation to run a check; a vague "the corpus will
    catch it" retires the obligation to say **what** would catch it, **how many**
    there are, and **which column** they sit in — and it retires it by being
    unfalsifiable rather than by being wrong, which is why no verdict channel
    reports it. What separates the two registers is only *when* the cheap
    measurement was available: for the daemon it was a restart and a question,
    here it was one `grep` and one board read. **Operative, and it is the
    diagnosis law's rule run forwards:** a claim about what an instrument *will*
    catch is stated as a population — a count, a file, a bucket — or it is stated
    as unmeasured. A hedge with no number in it is not a cautious claim; it is an
    absent one wearing caution.

### Budget by the quantity the purpose names.

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

### A stateful instrument measures history until its state is controlled.

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

### Verdict channel and mechanism channel are different instruments.

- **Verdict channel and mechanism channel are different instruments.** *An exit
  code is not a mechanism* — the verdict channel can't tell you why. *Don't infer
  a verdict from noise* — the output channel can't tell you whether. Read each
  for what it carries and never substitute one for the other: a tool that exits
  non-zero on findings is asked for its status, a tool that reports on stdout and
  exits 0 is asked for its output, and capturing `2>&1` to test for non-empty
  confuses a cold module cache with a defect (grave, PR #21).
  - **Re-scoped from pipes to compounds: the observed status belongs to whichever command
    produced it last, and appending anything to a command replaces its verdict.** Grave #289
    fixed this for *pipes* — `go test | tee` reporting `tee`'s success — and `pipefail-check`
    asserts the fix is live. **The scoping was too narrow, and the proof arrived within days**:
    running `make check > log 2>&1; echo "check exit: $?"` made the harness's reported status the
    **echo's 0** for a run that exited 2. Nothing was piped. The `$?` inside the echo was correct;
    the defect is that the *compound's* status is the last command's, so the wrapper added to
    observe the verdict is the thing that destroyed it. `pipefail` cannot help, because the class
    is not pipes — it is **any construct where the observed status belongs to a different command
    than the one under test**: `cmd; echo`, `cmd || true`, `cmd &`, a `$(…)` whose status is
    discarded by the next link in a `; \` chain. The remedy at the keyboard is one rule with no
    exceptions worth remembering — **run the command under test bare, and read its status from its
    own invocation**; if a wrapper is unavoidable, capture into a variable *before* anything else
    runs (`cmd > log 2>&1; st=$?`) and print the variable. This is the same instrument confusion
    the parent law names, applied to *time order* rather than to channel: the last writer to the
    status wins, and it is not necessarily the witness you called.
    - Sibling project `keel` carries the pipe half of this independently
      (`scripts/l1-bench.sh`: "without it the indent pipe would swallow the one status that says
      whether a comparison happened") and the adjacent rule that *a killed run is `unmeasured`,
      never an exit code*. **The exit-capture checker is `umami`'s — PRs `umami#396`/`umami#397`, merged as
      `umami@0b4ac1c`** (pointer: Scott, PR #298; a search of `~/src` from here had not found it, and it
      was flagged rather than guessed at, because a design invented here and described as grafted
      would be a fabricated provenance). Two things about the port, and the second is the load-
      bearing one:
      - **`umami` reached this place from a different failure**, so what transfers is a **design,
        not evidence about this repo**. A checker that caught a real defect there is a witness to
        `umami`'s codebase and to nothing here; the shapes a Go-plus-Make tree can hide a
        displaced status in are its own question. So the port earns **its own falsification** —
        write a compound whose status belongs to the wrong command, watch the checker go red on
        *this* tree — exactly as if the design had been invented here. Grafting the mechanism does
        not graft the *watched-die*, which is the half a control is not born without.
      - It is therefore a **second instrument, not a replacement for `pipefail-check`**: that
        target asserts a shell *setting* is live and covers pipes; a checker reads *constructs* and
        covers `cmd; echo`, `cmd || true`, `cmd &`, and the `$(…)` whose status the next link in a
        `; \` chain discards. #297 carries the port. (Ruling: Scott, PR #295; pointer supplied on
        PR #298.)

### A command's exit status belongs to whatever ran last.

- **A command's exit status belongs to whatever ran last.** If you need a
  command's verdict, nothing runs after it: redirect its output and read the
  file. Minted after **six** instances across four unrelated tools, which is what
  established that the subject is command composition and not any one tool.
  (Ruling: Scott, PR #347 relay — *"'a command's exit status belongs to whatever
  ran last' is right, it covers all six, and no control can reach ad-hoc
  composition, which is exactly what index space is for."*)
  - **The five wordings that failed, in order, because the sequence is the
    evidence.** `read the verdict from its own command` · `never off the end of a
    watch pipeline` · `state the boundary` · `prefer a chain-free habit` · `never
    append a status-reporting suffix to a command`. Every one of them describes
    **care while composing a chain**, so each leaves the construct's premise
    standing and asks only that it be written more carefully. The premise is the
    thing to remove: the status is *already reported natively*, so the wrapper has
    no job, and a construct with no job cannot be used carefully — only be absent.
  - **The specimens.** `; echo "xcheck exit=$?"` on a cross-architecture run, so a
    background task reported **exit 0** over a suite that never started (#344).
    Then `gh run watch … --exit-status ; echo "watch done"` — a suffix appended to
    a flag whose entire purpose is to supply the status the suffix discarded, on a
    command printed correctly in `CLAUDE.md` two lines away.
  - **The sixth is why the general form was needed, and it broke the fifth wording
    within one tool call of that wording being recorded.** `xcheck … | grep -E
    'board total|…'` on a run printing thousands of lines: the runner reported
    **exit 0**, which was `grep`'s. A filter is **not** a status-reporting suffix
    and it is appended for a completely real reason (volume), so "don't" is not
    its remedy — *not in the same pipeline* is. Scott's own diagnosis of the mint:
    *"I scoped my rule to the mechanism I'd just seen instead of to the class,
    which is the over-narrow predicate committed in prose rather than in code."*
    That is *a guard's trigger predicate is itself a claim about the space*,
    committed by a rule instead of by a regex — and prose is the worse medium for
    it, because an under-matching predicate in code can at least be falsified.
  - **What rescued the sixth is not available in general.** `xcheck-amd64.sh`
    prints `verdict from NATIVE x86_64 (host), exit N` itself, so the fact
    survived in the *output* channel after the *status* channel was overwritten —
    and only because that script was built to name its instrument on every path.
    Any ordinary command in that position leaves a green from `grep` and no way to
    know. Two laws land on it exactly: *verdict channel and mechanism channel are
    different instruments* (two channels existed and the overwritten one was
    read), and *a verdict without an identity check is hearsay* (`exit 0` was a
    true statement about a different process than the one being asked about).
  - **The seventh names the *tell*, which is what the first six were missing.**
    `go test ./... | tail -3 && make cite && git add … && git commit …` on the
    slice-8 branch: a real `FAIL` in the suite was reported as `tail`'s 0, `make
    cite` ran, and **the commit landed over a red tree** — caught only on the next
    read of the full log, and amended. Same class as the sixth, one tool call from
    a page that already carried the general form, and Scott's ruling says why the
    general form was not enough: *"the pattern is that the pipe appears when you
    want shorter output. Redirect to a file and read the file. Nothing gets to
    stand between a command and its status."* The first six wordings all describe
    the *construct*; this one describes the **motive**, which is the thing present
    in the writer's head *before* the construct is typed. Volume is the recurring
    reason — `grep` in the sixth, `tail` here — so **"I want less output" is the
    moment to redirect**, and it is checkable introspectively in a way "compose
    carefully" is not.
  - Operative: run it bare. `cmd > /tmp/out 2>&1` then read `/tmp/out`, and take
    the verdict from the runner's own exit code or from a dedicated query such as
    `gh run view "$RUN" --json conclusion`. No control can reach this — it is
    composition at the moment of writing, upstream of every gate in the repo,
    which is the reason it is index-resident rather than a test.

### Second-order honesty: apply the discipline to its own output.

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

### A claim about your own diff is sourced from the diff — an edit made after an amend is not in the amend.

- **A claim about your own diff is sourced from the diff, not from memory of having typed
  it.** Two false sentences shipped in #492, both about the author's own tree. The body said a
  grave's lesson was *"Recorded at the site and in the citations family"* when
  `docs/laws/citations.md` was not in the diff at all; a report to the principal said an ADR
  repair had been amended into the commit when the edit was made **after** the amend and never
  shipped. The available instrument was `git diff --name-only`, one command away.

  **The sharp sub-form is the one worth carrying: an edit made after an amend is not in the
  amend**, and the tree at the moment you speak is not the tree you remember editing. An amend
  is the specific trap because it leaves the SHA looking freshly authored while the working tree
  keeps accepting edits that nothing has captured. (Ruling: Scott, on the #492 review.)

  **This is a recurrence in a new channel, not a novelty.** #434's lesson was a premise sourced
  from a paragraph when the tracker was one query away; the sourcing failure is identical and only
  the subject changed, from someone else's record to your own edits. *Apply the lesson to the
  sourcing, not the fact* — repairing the two sentences and leaving the method running is how this
  family's graves recur inside their own fixes.

  **And the fix's first draft was wrong the same way.** The correction comment posted on #492
  attributed the second error to the PR body's Landed section; the body says nothing of the kind,
  and the claim lived only in a chat report. So the correction was itself a claim about a text,
  written from memory of that text, and it needed a second correction — which is *second-order
  honesty* arriving as a bill rather than as a principle. Read the channel you are describing
  before describing it.

  **The control this argues for is narrower than the obvious one**, and the narrowing was measured
  rather than reasoned: the naive form — every file path named in a body must be in the diff —
  fires **twice on #492 and is wrong both times**, while missing both real errors, because the
  false sentence names no path and correct sentences name files on purpose (a proposal about a file,
  work carried into another issue). The trigger has to be the **claim shape** — a discharge verb
  paired with a resolvable location — not the path.
  [#493](https://github.com/scttfrdmn/burroughs/issues/493).

### A correction is an edit, and carries the same error rate as the edit that needed correcting.

- **A correction is an edit, and carries the same error rate as the edit that needed
  correcting.** The section above closes with a fix whose first draft was wrong in the same way as
  the thing it fixed, and that was read at the time as a fact about one comment. It is not. A
  correction is written under the same conditions as the original — from memory of a text, in a
  hurry, by someone who has just been told they were wrong and is therefore *less* inclined to go
  re-read the channel — so the base rate does not drop when the word "correction" appears at the
  top of the paragraph. It may rise, because a correction is published with the authority of having
  already been checked once.
  - **Two specimens, two channels.** The correction posted on #492 attributed an error to the PR
    body's Landed section, which said nothing of the kind, and needed a second correction. And a
    repair to the citation-form census's doc comment — narrowing a claim about how many of ADR 0024's
    four drifted citations were genuinely wrong, on the grounds that one of them was *arguable* —
    replaced a true sentence with a false one: the ADR quotes the definition immediately below the
    coordinate it cites, so all four were wrong. Both are #434's shape, *apply the lesson to the
    sourcing, not the fact*, recurring inside the fix rather than in the original.
  - **The narrowing correction is the dangerous kind**, which is the non-obvious half. Narrowing
    feels conservative — it looks like retreating toward safety, so it attracts less scrutiny than
    the claim it replaces — while in fact it asserts a *new* boundary, and a boundary is a
    measurement. "Three of four, and the fourth is arguable" is a stronger claim than "four of
    four", not a weaker one, because it says something about the fourth.
  - **So a correction is verified against the artifact it describes before it is published**, to the
    same standard as the claim it replaces and not a lower one. Where the artifact is a text, that
    means reading the text; where it is a tree, `git diff`.
  (Ruling: Scott, on the #502 review — *"a correction is an edit and carries the same error rate.
  The arguable-range fix turned a true body claim false, which is #434's shape again."*)

### A probe's apparatus stays behind its tag until the measurement justifies promoting it.

- **A probe's apparatus stays behind its tag until the measurement justifies promoting it.**
  `internal/interp/ends_table.go` precomputes opener→`end` so a probe can measure what `matchEnd`'s
  linear scan costs ([#136](https://github.com/scttfrdmn/burroughs/issues/136)). It stays behind
  `burroughs_endtable` with the scan lane beside it, and the tempting next step — promote the table
  into the default build, since it is written and green and the scan is obviously O(n) — is the
  instrument-on-speculation pattern with the direction reversed: paying the engine's permanent
  complexity for a win nobody has yet shown is a win. Untagged apparatus is engine code, and engine
  code is judged on whether the runtime needs it. (Disposition (a), ruled by Scott on the #502
  review: *"a probe's apparatus stays behind its tag until the measurement justifies promoting it.
  Promoting first is the instrument-on-speculation pattern."*)
  - **The first thing the measurement said was that the probe's distances were fantasy.** The sweep
    used 0 / 64 / 512 / 4096 slots, chosen for convenience, and could not say whether any of them
    occur in code. A static census of every structural opener the spec suite's modules decode to
    (`TestSuiteScanDistanceDistributionIsMeasured`) puts the corpus **maximum** more than an order of
    magnitude below the largest swept row, with the median at a handful of slots: two of the four
    rows described distances that occur **zero** times. Promote on the strength of that sweep and the
    justification is a measurement of a distance no module contains. This is why the materiality
    input goes *before* the A/B rather than after it — it decides which distances are worth sweeping.
  - **The zero survived a second mechanism, which is the only reason it is reportable.** An exact
    zero on rows one has just benchmarked is the shape of an instrument reporting its own blindness,
    and the innocent explanation is arithmetic: bodies too short to hold the span. So the census also
    tracks the longest function body it decodes and asserts no span exceeds it — and the corpus's
    longest body is more than an order of magnitude longer than its longest span. The room is there;
    openers do not use it. *A suspiciously clean result is a tell*, and what clears it is a mechanism
    that cannot fail the way the first one can, never a re-run of the first one.
  - **The half a static census cannot buy is stated where its numbers are printed.** It counts
    openers in code; `matchEnd` is called once per *executed* entry. So an **absent** distance is a
    real negative and retires a swept row, while a **common** one transfers nothing: a cold
    4000-slot arm contributes one opener and zero entries, and a two-slot loop body inside a hot loop
    contributes one opener and millions. The dynamic half needs a counter in `runFrame` and is not
    built.

### Coverage is a claim: an instrument's domain is an assertion it cannot check about itself.

- **Coverage is a claim: an instrument's domain is an assertion it cannot check
  about itself.** Every instrument makes two claims and only one of them is
  falsifiable from the inside. Its **assertion** — this count equals that count,
  this table agrees with the reference — can be broken on purpose and watched to
  fail, which is what *a control isn't born until it has been watched die* buys.
  Its **domain** — the files it reads, the rows it matches, the population it
  ranges over — cannot be, because a domain that is too small produces **no
  finding rather than a wrong one**, and a green from an under-covered instrument
  is indistinguishable at the board from a green earned over everything. So the
  domain is stated, derived from the space rather than enumerated, and floored;
  and an instrument that reports a clean result has to say *over what*.

  **A population derived from what a mechanism prints is not the population the
  mechanism has — filter on the mechanism, not on its output.** ADR 0037's
  pre-registration forecast 19 surviving exec fails and 15 survived, because the
  population was derived by filtering the fail column on the string
  `unknown import` (62 rows) while the mechanism it was forecasting keys on a
  *cause*: a module importing from a declined name. Four more rows had that cause
  and printed something else entirely — out-of-bounds memory and table accesses,
  where the module linked against `spectest`'s memory instead of the one its
  declined dependency would have supplied. The forecast was **right about what it
  measured and measured the wrong thing**, which is the same defect as an
  under-covered domain with the error moved one step earlier: the domain was
  derived from a symptom, so it could not contain a member that shares the cause
  and not the symptom. The remedy is the one this section already prescribes,
  applied to *forecasts* and not only to controls — derive the population from
  the mechanism (here: neuter the gate and diff the boards, which is what
  `gatedDeclinedRegistration`'s membership does) and never from a string the
  mechanism happens to emit. Scott's classification on the PR #409 review: the
  fourth instance of the key-versus-cause distinction in this campaign, *"worth
  carrying into fact 3 explicitly rather than as a memory"* — so [#367](https://github.com/scttfrdmn/burroughs/issues/367)
  carries it, since its population has the same hazard and no string on today's
  board identifies the vectors that would flip. (His three priors are not
  enumerated here: the count is recorded as his, and inventing the list to make
  it resolve would be exactly the kind of manufactured provenance
  [citations.md](citations.md) exists to forbid.)

  **First specimen, and the one that names the shape: a control that names the
  fact it expects cannot notice that fact is missing** (#264, whose closing
  comment is that sentence). `ErrMalformedBrOnCastFlags` landed with its
  sentinel, its `%w` wrap, and **two controls asserting `errors.Is` against it**
  — and was not added to `declaredErrors`, the fuzz target's allowlist of errors
  the decoder is permitted to return. Both controls passed, because the sentinel
  they named was the right one; neither could ask *is this sentinel declared?*,
  since a test written from a sentinel's own name supplies the very fact the
  allowlist is missing. `make check` was green and `fuzz-smoke` went red in 41
  seconds. The instrument that can see it is the one that enumerates the
  decoder's **whole error surface** without knowing what any individual condition
  expects — which is why the repair was a bijection derived from the package
  (`internal/binary/errdecl_test.go`, #283) and not another assertion.

  **Second specimen, the recursion, which is why this is a key and not a
  corollary of the vacuity law: the remedy for a coverage defect can be a
  coverage defect.** `wholeFileGated`'s keys, after #285 zeroed them, were the
  six relaxed-SIMD files that had been *observed* declining — a registry of past
  findings wearing the shape of a domain, so a seventh file that never declined
  had no zero to violate. Scott's question on the flip named it exactly: *that is
  #264's sentence arriving inside #264's own remedy.* The answer was admissible
  only because the domain-side check beside it (`internal/spec/spec_test.go`) is
  derived from the space — all 254 files, all their declines, needing no registry
  — and because the seventh file's absence was then shown to be **structural**: it
  holds one command, which passes with every gate off, so no gate can reach a
  scorable command there and a zero key would be an assertion that cannot fail.
  *Derive the domain from the space, never from the registry* (#264) is the
  operative sentence in both.

  **Third specimen, one layer out, where the domain is chosen by a tool and
  never announced:** `git grep` searches tracked files only, so a sweep run with
  it excluded exactly the new unstaged region the grave came from and returned a
  confident empty set. Full text at *a guard's trigger predicate is itself a
  claim about the space* (`controls.md`), whose third specimen this is — that law
  is this one applied to a *predicate*, and it stays where it is; **a search
  command's default domain is a claim about the space, made silently by the tool
  rather than by the author** is the sentence that generalizes past regexps to
  every instrument with a default scope. `go test ./pkg` says one package, a bare
  `grep` says one directory, a fuzz target says whatever is in its corpus
  directory, and none of them announce the restriction.

  **Fourth specimen, minted in the same PR as the law and by it:**
  `scripts/citecheck.sh`, the check that every `#NNNN` a diff adds resolves,
  matched the `+` lines of a diff one at a time — and missed `ADR 0025` in its own
  PR, because the citation was split by a prose wrap (`... citing ADR` / `0025's
  carve-out ...`). Its assertion was sound and its population was short by
  whatever wraps, which is most prose citations in this repo. It is the
  wrapped-lead defect a fourth time (#78, #105, and the un-graved recurrence on
  #80), in the instrument written to enforce
  the law about it, and it was found the only way this class is ever found: by
  running the instrument over the diff that introduced it, then reading the
  printed citation count instead of the exit status. *Artifacts become oracles*,
  and the sibling that had already paid for it — `internal/testenv`'s law-index
  reader, which splits into bullets and joins each before matching — was two
  directories away.

  **Fifth specimen, and the one the law caught in its own falsification
  procedure: a probe set can exclude the invocation form its consumers use.**
  Every one of the four probes above was run as `sh scripts/citecheck.sh …`, so
  all four passed while the file's **executable bit was never committed** — and
  both binding consumers invoke it as `./scripts/citecheck.sh`, which is the one
  form no probe used. CI said `citations -> failure`, exit 126, `Permission
  denied`: a red gate that had not run its check at all. The falsification was
  sound in its assertions and short in its domain by exactly one dimension —
  *how the thing is called* — and the local mirror could not disagree, because
  `make cite` was never the command that ran it. The repair is the same shape as
  every other repair in this entry: exercise the **path**, not the artifact
  behind it, so `make cite` and the CI job are one invocation with two homes.
  Also the cheapest available reading of *verdict channel and mechanism channel
  are different instruments* — a `citations` job going red says nothing about
  whether a citation failed.

  **Recurrence, in the same file, two PRs later, and counted rather than
  re-minted:** the `project#N` arm added on #298 was falsified against an
  unqualified number and a `umami`-qualified one — two adjacency shapes, and the
  defect lived in the third. (Those probe numbers are described rather than
  quoted, because a fake number written into a law is scanned by the checker the
  law is about; the first draft of this paragraph quoted them and went red.)
  Letting the qualifier end in `-` meant `pre-#298`, ordinary hyphenated
  English, parsed as project `pre-` and a real citation was silently exempted
  from resolution. Same domain-short-by-one-dimension as the executable bit,
  and the dimension is again *the forms a consumer actually writes* rather than
  the forms the author enumerated. A probe set of two is a probe set that has
  chosen its axis; the space of characters that can precede a `#` is derivable,
  and was not derived.

  **Sixth specimen, with the census itself as the subject, and filed as a
  specimen rather than minted as a law** (ruling: Scott, PR #307).
  `TestEveryBoardBoundIsChecked` derives the board's bounds from the package AST
  by matching `*ast.ValueSpec` — the naming convention on a declaration, which is
  the right trigger and was a complete domain for eighteen bounds. #307 made
  `validateAdmitCeiling` **derived** (`142 + <live members of a named set>`), so
  it became a `:=` and therefore an `*ast.AssignStmt`, and the census reported
  **18 bounds** while all nineteen were present, correctly declared, and
  correctly routed through `boardBound`. Nothing was undocumented and nothing
  unbounded; the instrument's claim about *where a bound lives* had gone false
  under it. The specimen's value is which guard caught it: `minBoundPopulation`
  is **8**, so the floor was satisfied by 18 and said nothing — a floor cannot
  notice a missing member of a population of 19 — and the **exact count** fired
  on its own. That is this entry's closing paragraph demonstrated rather than
  restated, which is also why it is a specimen and not a new key: the law already
  said it, and the census was the subject that had not been tested. Its sibling
  reading is *floors bound the catastrophic case; only an exact count sees a
  small silent loss* (`controls.md`), reached in another project from an
  unrelated failure — **two independent derivations, which is corroboration in
  the way a single shape recurring inside one instrument is not**.

  **Seventh specimen, the third-specimen shape pointed at the tracker: `gh`'s
  undocumented default of 30 rows.** `gh issue list` and `gh pr list` page
  silently, so a census taken from either — how many closed `type:grave` issues
  carry no closing comment, how many issues a label holds, how many PRs a
  milestone has open — reports a **page** as the population and never says so.
  It is *a search command's default domain is a claim about the space, made
  silently by the tool rather than by the author* in the one place this repo keeps
  its project state, which is why it earns a specimen rather than a footnote on
  the third: the tracker is the authority for everything the code cannot check
  about itself, so a truncated read there has no second mechanism above it to
  disagree. Two reassuring failure directions, both silent: fewer rows than exist,
  and a defect-hunting sweep that finds nothing because the rows carrying the
  defect were on page two — the grave-comment sweep is exactly such a query, so
  the shape's own remedy was subject to it. The rule is mechanical: **any `gh`
  invocation whose output is counted, summed, or asserted against carries an
  explicit `--limit` above the plausible population**, and a result landing
  *exactly* on the limit is read as a truncation rather than a count until a
  second query says otherwise — *a suspiciously clean result is a tell*, with the
  round number supplied by the tool instead of by the data. (Directive: Scott, on
  the 17-head slice's relay: *"The `gh` default-30 truncation is the boardFiles
  error again: a tool's silent limit becoming a census. Worth a line wherever
  counts are taken from `gh`."* — the `boardFiles` he names is the third specimen's
  sibling, one instrument over.)

  **Eighth specimen, where two globbers disagree about what `*` means and the
  verifier holds the weaker one.** The amd64 cross-check copies the working tree to
  a native x86_64 host, and macOS `tar` had written AppleDouble sidecars
  (`._address.wast`) into `testdata/spec` on the far side. The board reddened
  twenty instruments — every row a parse failure on a file named `._…`, which is
  the tell, and the arch was not the subject. The verification I had run against
  that copy was `ls testdata/spec/*.wast | wc -l`, and it reported **257 on the
  poisoned tree and 257 on the clean one**: the shell's `*` skips a leading dot and
  Go's `filepath.Glob` does not, so the consumer saw 514 files where the checker
  could only ever see 257. An assertion sound in what it compared, over a
  population defined by a *different globber than its subject's* — the third
  specimen's shape with the domain chosen not by a tool's default flag but by two
  tools' incompatible definitions of the same metacharacter. **A copy is verified
  with the consumer's globber, not the shell's**, and the repair is a
  reconciliation rather than a floor (`scripts/xcheck-amd64.sh`): `find`-based and
  dot-aware on both ends, remote count checked *equal* to local so that too few
  catches a lossy copy and too many catches junk. A floor would have passed 514
  without a word. (Found this session, running the check Scott's `janus.local`
  directive made available for the first time, PR #339.)

  **Ninth specimen, where the domain was right and the *unit* was wrong: byte
  counts come from the tool that measures bytes.** `claudeMDCeiling` — retired with
  the index economy, and named here as the occasion rather than as a live control —
  was written in
  bytes and `os.Stat` enforced it in bytes; the figure I reported a section's
  change in — and reported to Scott as the funding for a new law's key — was
  Python's `len(str)` over a UTF-8 file dense with em-dashes and `§`, which counts
  **characters**. It said the section had shrunk 165; `wc -c` says it *grew* 132.
  Wrong in magnitude and in sign, on a quantity a governance decision was then
  taken against. The instrument was reading the right file, the whole file, and
  nothing but the file — so no domain check reaches this — and it still answered a
  different question than the one the ceiling asks. **A unit is an unchecked claim
  the same way a domain is**, and the remedy has the same shape: ask the instrument
  the *consumer* uses, which here is `wc -c` or `os.Stat` and never a length
  function whose unit depends on encoding. Scott classified it on the relay as the
  **third instance of a proxy quoted as the measurement in one campaign** — after
  the single-line `grep` that undercounted the `unknown table` family by four
  multi-line assertions, and the eighth specimen's shell glob — which is why it is
  a specimen here and not a key: the shape is established, and what recurs is the
  reflex of reaching for the convenient reader. (Found by re-measuring after the
  law was already authorized on the wrong figure; ruling on the classification:
  Scott, PR #339.)

  **Tenth specimen: a glob is not the corpus, and the claim it carried survives only
  once it is scoped to what the glob actually saw.** #394's body argued from
  `grep -rn "instruction requires" testdata/spec/*.wast` — "exactly those two rows in
  the whole corpus". The tree holds **288 `.wast` files, not 257**: `legacy/` and
  `proposals/` sit under the directory the glob names and outside what it matches, and
  they carry **seven more** rows in that wording, one of them a spelling
  (`type mismatch: block requires [] but stack has [i32]`) this validator produces
  nowhere. The sentence was written as a claim about the corpus and is only true as a
  claim about the **board** — which it happens to be, because `testenv.SuitePaths`
  globs `filepath.Join(suiteDir, "*.wast")` and `boardFiles` is built from it, so the
  subdirectories are outside the board by construction. **That disposition is the
  reusable part**: the repair is to *scope* the claim to the population the instrument
  had, not to delete it, because deleting it discards a true statement along with the
  false one, and not to widen it silently either, because then nothing records that the
  original reading was wrong. Scott's classification on the PR #397 review — the fourth
  instance of *a pattern standing in for a population*, after the single-line `grep`
  that undercounted the `unknown table` family, the eighth specimen's shell glob, and
  the ninth's character count. What makes it the same defect rather than a new one is
  that in every case a **convenient matcher's silence was read as the space's
  emptiness**, and the tell is available every time for the price of one wider query.

  The two failure modes are worth keeping separate because they are found
  differently. An **assertion** defect is found by falsification — break it, watch
  it die. A **coverage** defect is found only by measuring the instrument's
  population against an independently derived one, which is why floors, exact
  counts, and vacuity guards are not redundant with each other: a floor catches a
  domain that collapsed, an exact count catches one that shrank quietly, and a
  vacuity guard catches one that emptied. (Ruling: Scott, PR #285 relay; minted
  from #264, whose closing comment is the first specimen's own wording.)

### An unmeasured complement is not an empty one.

- **An unmeasured complement is not an empty one.** Take a population with a known
  size, attribute the members you can name to one cause, and the remainder is not
  *nothing* — it is **unmeasured**, and the difference is invisible because both
  read as a number you did not have to go and get. The sentence that carries the
  defect always sounds like a measurement: *"the other cause has no rows."* What it
  actually says is *"I looked at the rows I could name, and they were all the first
  cause's."* Attribution names where someone looked; it does not partition. The
  remedy is mechanical and cheap: **route one call site at a time and print the
  per-site delta**, so each cause's rows are read off a board rather than off a
  subtraction.

  **The specimen is [0042](../decisions/0042-the-interpreters-second-comparator-is-deleted-rather-than-tuned-and-the-criterion-is-five-rows-in-both-directions.md)'s
  own fourth Consequence**, which said the cast family's arm 9 had no corpus row —
  built by attributing the known members of an all-on fail set of 17 to
  `call_indirect` and reading the complement as empty. Measured afterwards by
  routing each call site alone: `call_indirect` clears 5, **arm 9 clears 5**, both
  clear 10. The forecast said 17 → 12 and the board went 17 → 7, so the miss was in
  the project's favour — which is the reason to state the law in this direction. **A
  forecast beaten is a forecast falsified**, and the ten-instead-of-five is the
  cheap half of that finding; the valuable half is *why* the five were missing, and
  a favourable miss banked as a win never gets asked. The sibling of *an attributed
  partition is not a partition* one level up: that one is about a fail set's members,
  this one is about the arithmetic that takes the rest for nothing. (Ruling and
  minting: Scott, on the #481 review — *"attributing the known members and taking the
  remainder for nothing is a shape that will recur."*)

  **The second specimen is a cost, not a count, and it shows the complement is not always
  small.** #136's end-table A/B measured the scan at **0.602 ns per slot** off the decoupled rows,
  then assumed removing the scan delivers that. On the cheap-instruction arm it does — 93% to 120% of
  the available saving lands. On the realistic arithmetic arm it delivers **88%, 64%, 53%** as the span
  grows, and the shortfall per slot rises **0.072 → 0.218 → 0.284 ns** with the padding's cache
  footprint. The complement was never zero: the scan was *subsidising* the execution that followed it,
  pulling the same instruction slots into cache a few nanoseconds before the interpreter walked them.
  Delete the scan and the execution pays the misses itself. So *"the scan costs 0.602 ns/slot"* and
  *"a table saves 0.602 ns/slot"* are different claims, and the first was measured while the second was
  assumed. **The delivered figure is the one a decision is denominated in** — what you get, not what
  the removed thing cost.
  - **A forecast whose terms share a source is one estimate, not two.** The same measurement's
    pre-registered prediction — *4.2%, below the 5% bar, materiality refuted* — was built from a scan
    cost and an execution cost **both** read off the prior probe's rows, and both erred toward the same
    verdict: the scan slope was low by **2.08×** because the prior sweep anchored it on a
    zero-distance row, and that one flaw propagated into both terms. Two numbers multiplied into a
    forecast look like two independent checks and are not, when one dataset produced both. State the
    provenance of each term, and where they share one, say that the forecast has a single point of
    failure rather than a bracket.

### A criterion measured against a question set the project controls is jointly a claim about the answerer and the asker.

- **A criterion measured against a question set the project itself controls is jointly a claim
  about the answerer and the asker.** The coverage law above is about an instrument's domain
  being unfalsifiable from the inside. This is the same hazard one level up, aimed at a
  **closure criterion**: when the questions are asked by machinery this project also owns, a
  criterion of the form *nothing is declined* is satisfied by the validator improving **and** by
  the harness asking less. Both readings produce the identical green, and no control in the tree
  can tell them apart, because the thing that moved is not in either instrument's domain — it is
  the boundary between them.

  So the discipline is **temporal, not structural**: a criterion of this shape is *decidable at
  any moment* and **a discharge is dated, not permanent.** Closure can be un-achieved later by a
  widening with no regression whatsoever in the answerer. State the date beside the claim, and
  make a widening of the question set a **tracked event that reissues that date**.

  **The specimen is #9's own closure criterion**, and it is why that criterion is phrased *"no
  `assert_invalid` vector is declined against the corpus **as the harness may currently ask
  it**"* rather than as a bare zero. `validateDeclineCeiling` rose **31 → 55 at #341** with **no
  change to the validator at all**: what changed was that module definitions began being scored
  on the validator's answer rather than on the reader's, so a population that had never been
  asked started asking. The number went the wrong way while the engine stood still.

  **A widening is not a population-size change, which is why nothing notices it.** #341 scored
  the same module-definition commands it always had; what moved was *which oracles each command
  kind is consulted against*. Counts cannot see that, a pass/fail delta cannot distinguish it
  from a regression, and a re-based bound absorbs it in one edit — three instruments, all
  reporting normally.
  [#477](https://github.com/scttfrdmn/burroughs/issues/477) is the ordered
  remedy: a declared per-command-kind predicate table whose digest is pinned beside the claim and
  its date, so a widening fails a control that names the reissue instead of passing quietly.

  Corollary, since the shape is not specific to #9: **a criterion whose subject includes your own
  question set names the date it was measured, or it is a claim about a moment while reading as a
  claim about a state.** (Ruling: Scott, on the PR #476 review — *"a criterion measured against a
  question set the project itself controls is jointly a claim about the answerer and the asker.
  Decidable at any moment; a discharge is dated, not permanent … Closure can be un-achieved by a
  later widening with no regression in the validator, and nothing currently notices that."*)

### A completion state can be true while its payload vanished — verify the artifact, not the flag.

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
  [Coverage is a claim](#coverage-is-a-claim-an-instruments-domain-is-an-assertion-it-cannot-check-about-itself),
  above in this family (the path was `docs/laws/evidence-and-instruments.md#…` while this law lived
  in `CLAUDE.md`; **relocating the text unchanged would have changed what the link means**, since a
  repo-root-relative path read from inside `docs/laws/` resolves to nothing. The target is the same
  law it always was).
  This rule kept a running tally here until it was pointed out
  that a measured quantity in prose schedules its own next increment; the tally is generated or
  it is deleted, and this one is deleted. (Lesson: Scott, on the PR #247 relay; recurrences
  relayed by chat-Claude; count struck on his ruling, PR #317.)

### A control is a pattern plus the text it is handed.

- **A control is a pattern plus the text it is handed.** A regex has no population of its own; the
  population is whatever the control feeds it, and two errors follow from forgetting that. The
  narrow one is that **a pattern able to match the instrument's own output is satisfied by the
  instrument** — a matcher asks whether a token appears *somewhere* in a payload, the payload
  happens to contain the checker's own vocabulary, and the condition is met by the words describing
  the check rather than by the thing checked. It reads as a pass and it asserted nothing. The wide
  one is that **a pattern reproduced without its control's preprocessing measures a different thing
  entirely** — same regex, different text, and therefore a different question, answered
  confidently. Filed as a **class and not an anecdote** because it arrived twice in one session with
  one remedy both times, and widened when the third specimen turned out to be the same mistake made
  from the other end.
  - `TestEveryPayloadSpellingIsReadOrRefusedByName` asked `strings.Contains(err.Error(), name)`
    for each payload spelling. For `name == "struct"` the substring sat inside the word
    **constructor** in the refusal's own sentence, so a refusal that never named its payload
    satisfied the check anyway (`value_test.go:198`, with the reason written at the refusal it
    corrupted, `value.go:745` — the comment line ending *"which is the fact `fromRef` used to drop"*,
    re-pointed **twice** on 2026-08-22 by that text, first after #452's dating note moved it twelve
    lines and again after grave #491's repair moved it eleven more; the text located it both times and
    neither delta was reusable). The
    falsification probe that should have failed on six payloads
    failed on five, and *the shape of what survives named the bug*.
  - Reading a CI verdict out of a captured `gh run watch`, `grep -E '^JOB'` for the sentinel's
    `JOB <name> <conclusion>` lines matched the watch's own **`JOBS`** section headers — the
    progress display returned dozens of rows as if they were the verdict, and a green-looking
    harvest came out of a file whose sentinel had not been written.
  For those two the remedy is mechanical and identical: **anchor the match, or quote the token** —
  `strconv.Quote(name)` rather than `name`, `^JOB ` rather than `^JOB`. A delimited token cannot
  be a coincidence inside a longer word, which is *aboutness is not proximity* applied to a
  matcher instead of to a sentence.

  - **The third specimen arrives from the other end: the pattern was right and the text was not
    the control's.** Auditing #456's citation drift, `citation_test.go`'s `wrapJoin`
    (`-\n\s*([A-Z])`, which rejoins an identifier wrapped across a comment line break) was run
    against the raw `.go` files to find out where its two cited instances had moved to. It found
    **zero** in `internal/spec/spec_test.go` — because a wrapped identifier's continuation line
    begins with `//`, and `\s*` does not match a slash. The control never sees raw bytes: it feeds
    the pattern `group.Text()`, go/ast's comment text with the markers already stripped, where the
    same file holds **two**. Re-measured on `0b7c315`: 2 preprocessed, 0 raw.

    The claim one keystroke from being published was *"the citation has no referent anywhere"* —
    much stronger than the true finding, wholly false, and **larger**, which is the direction this
    error runs in. The true finding is one half of the same audit and survived: `wast.go` holds 0
    instances either way, so that half of the citation names the wrong *file* rather than a drifted
    line, which is the half a line-drift sweep cannot see. One number was worth publishing and the
    other would have been a fabrication about the tree, and the pattern was identical in both.

    Same family as the census clause *measure with the harness (`run(s).Buckets`), never a grep over
    the board log* under [bucket size estimates the reward, not the
    job](boards-and-buckets.md#bucket-size-estimates-the-reward-not-the-job), where bucket keys hold
    embedded newlines and a line-oriented sum therefore answered a different question three times in
    one session. There the regex stands in for the code path, here it stands in for the control, and
    the remedy is the same shape: **call the control, or reproduce its preprocessing and say that you
    did.** A copied regex is half an instrument, and the missing half is invisible because it lives
    at the call site rather than in the pattern.

    **This bullet's own first draft cited that law under a title the corpus does not contain**, in a
    file that does not hold it, recalled from a session's working vocabulary rather than read off the
    page — caught by grepping the corpus for the title, which returned only the line being written.
    Recorded here rather than filed apart because it is the same error one layer up — a citation
    reproduced without the text it names — and because [a `file:N` resolves to a line, not to the
    thing it names](citations.md#a-filen-resolves-to-a-line-not-to-the-thing-it-names--and-the-miss-is-systematic-not-careless)
    already owns the family: there the pointer resolves and names the wrong thing, here it names a
    title the corpus never had, so no line-drift sweep could have found it. The remedy generalizes
    past regexps: **the cheapest check on a cross-reference is to search for it before writing it,
    and a search that returns only your own new line is a finding.**

  Sited in this family because it is the domain error
  [Coverage is a claim](#coverage-is-a-claim-an-instruments-domain-is-an-assertion-it-cannot-check-about-itself)
  names, contaminated from the one direction an instrument cannot see: the checker's vocabulary is
  *inside* the population it scans. Adjacent to *a ban reported in the banned form is still the
  banned form* in [operations.md](operations.md) — there the scanner reads its own report, here it
  reads its own words — and the tell is the same in both, a needle that is a short common word in
  a haystack of prose the checker or its subject wrote.
  (Class: Scott, PR #460 — *"two instances with one remedy is a class."* Widened to the text-side
  form on his ruling on the [#463
  review](https://github.com/scttfrdmn/burroughs/pull/463#issuecomment-5365343139) — *"the class was
  'a pattern that can match the instrument's own output'; this adds that a pattern reproduced without
  its control's preprocessing measures a different thing entirely"* — with the heading renamed to the
  wider statement and the fold-in ordered in place of a near-duplicate entry.)

### A truncated search proves nothing about absence, because a display limit is not a result set.

- **A truncated search proves nothing about absence, because a display limit is not
  a result set.** Scott's mint, on the #498 relay, and the words are his:

  > Retracting your own sharpest argument by posting is the right handling, and the
  > cause deserves minting: **a truncated search proves nothing about absence.** `|
  > head` is a display limit, not a result set — the declaration was eleventh.
  > Absence is only claimable over a complete enumeration.

  **The specimen is the sharpest argument #456 had, and it was false.** A symbol was
  reported to a principal as *"defined nowhere in the tree"*, with five present-tense
  sentences named as citations to a thing that did not exist and the finding called the
  issue's strongest evidence. The search behind it ended in `| head`. It returned ten
  lines because ten is what `head` returns; the declaration was the eleventh match.
  Retracted by posting rather than editing
  ([#456](https://github.com/scttfrdmn/burroughs/issues/456#issuecomment-5383301564)),
  since a silent edit leaves the record showing a claim that was never made.

  **This is not a sharper `--limit` and no limit discipline reaches it**, which is why
  it is a key rather than a tenth specimen under
  [coverage is a claim](#coverage-is-a-claim-an-instruments-domain-is-an-assertion-it-cannot-check-about-itself).
  A truncation *distorts* a count — that family's subject, remedied by a limit above the
  plausible population and by reading an exactly-on-the-limit result as a truncation. It
  **inverts** a negative claim: from *at least ten exist* to *none exist*, which is not an
  error of magnitude but of sign, and the direction it errs in is always the flattering
  one, because a search that finds nothing is the shape of a finding. The asymmetry is
  what makes the rule mechanical rather than a caution: a positive claim is licensed by
  **one** row, so a truncated read can support it; a negative claim is licensed only by a
  **complete enumeration**, so no truncated read can ever support it. `head`, `--limit`,
  a pager, an IDE's match cap, a subagent's summarized excerpt, the first screen of
  output — all of them serve a positive claim and none of them serve this one.

  **Nothing downstream catches it, and that is the second half.** A negative claim is a
  citation with no target: *"defined nowhere"*, *"no vector reaches this"*, *"never in X"*
  name no artifact, so `citecheck.sh` has nothing to resolve, no line-drift sweep applies,
  and review confirms it by reading a plausible sentence. Every other citation in this repo
  is checkable *because* it points at something. So the guard is at the moment of writing,
  and it is one question: **would a complete enumeration have been cheap?** It nearly always
  is — `grep -c`, `wc -l`, the same query without the pipe — and a negative claim stated
  without one is stated without evidence. Its sibling on the sourcing side is *count with a
  counter, not by eye*; the difference is that eye-counting produces a wrong number while
  this produces a confident nothing. (Mint: Scott, on the #498 relay. Specimen and retraction:
  [#456](https://github.com/scttfrdmn/burroughs/issues/456#issuecomment-5383301564).)

  **The other half of the same relay: name which grounds are load-bearing after a retraction.**
  Removing a false argument changes what the conclusion rests on, and a record that only deletes
  the argument leaves the next reader to assume the case is unchanged. Scott's term — *"the record
  should say which grounds are load-bearing now"* — so
  [ADR 0047](../decisions/0047-a-location-citation-is-path-qualified-and-names-a-symbol-and-the-positional-population-is-pinned-rather-than-banned.md)
  states it in the ADR itself: the case for symbol citations *"rests on the two measured defects above
  and not on a rename that broke prose."* A retraction that does not re-state the load-bearing grounds
  is half a retraction, and the surviving half is the part that gets cited later.

### An instrument whose subject is changed by its own conclusion stops reproducing it.

- **An instrument whose subject is changed by its own conclusion stops reproducing it.**
  A measurement that decides something, and whose decision then edits the thing measured,
  reports a different number the next time it runs. Both numbers are correct about
  different subjects, which is worse than one of them being wrong: the record and the
  instrument now contradict each other and neither is lying.

  **Specimen: ADR 0048's memory bill.** The instrument priced ten representations for
  #136's pairing table and chose one on a single term — `binary.Func` opens `TypeIndex
  uint32` before a slice header, so it has one 4-byte interior hole, and an `int32` placed
  there costs **zero** against 8 B appended (75144 B over the corpus, the largest term in
  the comparison). The mechanism that implemented the decision added `Func.EndsOff`, which
  **fills that hole**. From that commit the live `Func` had none left, so the next run
  charged a *second* `int32` at full width, moved the chosen row 154520 B → 229664 B, and
  re-ordered its own table — under a printed sentence reading *"the only interior hole in
  `binary.Func` is 0 B wide, and the arena's offset is what fits in it … which is why the
  arena leads the dense rows"*, on a board where the arena was fifth. Every figure in that
  sentence was a real measurement of the post-landing struct.

  **The repair is a counterfactual base, not a provenance note.** "Measured at the parent
  commit" is honest and strictly worse: a table nobody can re-derive drifts silently the
  next time its subject moves, and the reader who re-runs the instrument gets a
  contradiction with no way to tell which side is stale. `endSizeUncommittedFunc` charges
  every row against `Func` **less** the field the decision added, so the ADR's table
  re-derives at HEAD *and* each row keeps the question it was answering — *what would this
  representation cost, added to a struct that does not already have it* — which is also the
  only base all ten rows share. The absorption itself is then asserted separately, by
  `TestEndsOffsetIsFreeInTheLayout` in `internal/binary`, so a `Func` that starts paying
  for the field fails there rather than quietly changing what every figure means.

  **How to see it coming: ask whether the conclusion is an edit to the measured object.**
  Most measurements are safe — a benchmark of a scan does not change the scan. The unsafe
  shape is narrow and recognizable: a measurement over a *layout*, a *count of sites*, a
  *set of remaining cases*, or a *census of some population*, whose conclusion is "so add
  one / fix these / occupy that". Every closure condition of that form is an oracle that
  will disagree with its own record. Two mechanical outs: charge against the state *before*
  the conclusion (this specimen), or make the instrument assert the post-conclusion state
  directly, so the number it prints is the one the tree can still produce.

  **This is the artifact-becoming-an-oracle shape with the artifact and the oracle one
  commit apart** — see
  [graves-and-sweeps.md](graves-and-sweeps.md). The
  difference is the interval: the usual specimen is a stale file answering searches months
  later, and this one is a live, passing, correct test refuting the ADR it was written for,
  in the same PR. Reproducibility of a cited figure is therefore not a documentation
  property but a property of the instrument, and it has to be designed in at the point where
  the instrument's subject and the decision's subject are the same object.

### An A/B across a build tag gives both lanes a tag of equal length, because the tag string is itself in the binary.

- **An A/B across a build tag gives both lanes a tag of equal length, because the tag string is
  itself in the binary.** #136's flip measurement compared an untagged build to `-tags
  burroughs_endtable`, and `BenchmarkStraight` — a shape with **no structural openers**, where the
  table lane does strictly more work and cannot be faster — read **-1.43% (p=0.001)** in #504 and
  **-1.25% (p=0.000)** on re-measurement. An impossible speedup at p=0.000 is not noise; it is a
  confound with a mechanism, and the mechanism is that `runtime.modinfo.str` records `-tags`. Adding
  any tag grows it, which shifts every data and bss address downstream: `go tool nm -size`, sorted,
  differs on **2474 of 5824 lines** between untagged and inert-tagged — **1434 data, 1016 bss, 24
  read-only, and zero text** — so not one instruction moved and 2474 addresses did. Give both lanes an
  equal-length tag and the same row reads **~ (p=0.369)**.
  - **The fix is removal, not subtraction, and that distinction is the whole entry.** #504's
    pre-registration diagnosed the bias correctly and prescribed *flooring* — measure the bias, then
    net it out of every row. That silently assumes the perturbation is a constant with a sign, and it
    is not: across twelve rows the same tag swap ran from **-3.05% to +0.65%**, both directions at
    p<0.01, because a layout shift helps some access patterns and hurts others. A single floor
    subtracted from every row is a mean standing in for a spread. Two lanes with equal-length tags
    have **provably identical layout** — 0 differing nm lines of 5824, identical byte size — so there
    is nothing to net out. **Ruling, Scott on the #508 review, correcting his own earlier framing:**
    *"I called the build-boundary floor 'the load-bearing' constraint. It was wrong. The artifact was
    untagged-versus-tagged, not a boundary tax, and flooring would have propagated the error rather
    than removed it."* Recorded with its author because a principal's order is what a floor would have
    been built against, and the correction reaches every future A/B in this tree — the failure mode is
    that *naming* a confound convincingly makes the prescription for it feel measured too.
  - **Equal-length is the requirement; equal *content* is not, and byte equality is the wrong
    check.** Go's build ID hashes the tag list, so two inert tags of the same length produce binaries
    that differ in bytes and agree in layout. `cmp` says "different" and proves nothing. The claim a
    timing difference could actually come from is *layout*, and `go tool nm -size` is what answers it.
  - **The residual noise floor is worth knowing once you can see it.** With layout provably identical,
    n=20, this harness still produced **up to -0.97% at p=0.000** on four of twelve rows. So ~1% is
    the floor below which no A/B in this tree means anything, and a pre-registered threshold set at
    5% is 5× the floor rather than the comfortable margin it reads as.
  - **A tagged-vs-tagged A/B estimates the code's effect, not any shipped binary's.** The default
    build carries no tag, so the figure a user experiences cannot be isolated from layout at all;
    what the equal-length comparison buys is attribution — the delta is the files that compiled, not
    the string that named them. Say which of the two a number is.
  - **And the tree is an input to a benchmark that is already running.** The first attempt at this
    measurement was void: a new `_test.go` landed in the measured package 21 seconds before the second
    lane compiled, so the two binaries differed by the tag *and* by a file, and the mtimes were the
    only surviving evidence. Any multi-lane run therefore hashes its own sources before and after and
    refuses to report if they moved — the same discipline as *a skip is not a verdict*, applied to the
    inputs rather than the outputs.
