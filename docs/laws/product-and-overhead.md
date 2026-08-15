<!-- Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0 -->

# Laws — Product and overhead

What gets selected, and what the selection is charged to.

Relocated from `CLAUDE.md`'s `## Disciplines` section, **verbatim**, when that file
became an index (see the restructure PR). Each law's one-line compressed form remains in
`CLAUDE.md` as its recall key and points here for the specimen, the minting record, and the
token it was granted on. Nothing was rewritten in the move: the bodies below are the text as
it stood, which is why superseded wordings still appear inside them where a later ruling
amended rather than replaced.

`CLAUDE.md`'s recall key and each heading here are checked equal by
`TestEveryLawIsIndexed` (`internal/testenv`), so the two cannot drift.

### The phase's product is the work; instruments are overhead on it.

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
    - **The column moves only when what the harness *can ask* changes; where a PR cannot change
      that, the zero is structural and is stated as structural, naming the reason and the reward
      figure that does have a subject.** The mechanism is `internal/spec/wast.go`'s dispatch:
      `r.Unsupported++` sits in the **`default:` arm**, keyed by head atom because every
      unsupported command has `KindUnsupported`. So the column counts *commands the harness has no
      case for* — it measures that package's command vocabulary and nothing else. Everything below
      follows from that one fact, which is why this is written as the condition and not as a list
      of cases: **enumerated instances invite an amendment per instance**, and each amendment
      arrives as a question a principal has to answer about a case the rule already covered.
      A statement about *which instrument has a subject*, never an exemption — the actor still does
      not get to pick.

      Three specimens, all the same rule read from outside:

        - **A gate campaign.** A vector for a gated proposal is scored `gated`, never
          `unsupported`, so a pre-flip campaign PR cannot move the column however much engine
          capability it lands. The reward figure is the **all-on lane's fail delta**: the default
          lane's `gated` count is what the flip collapses, and until then all-on `fail` is the only
          figure that responds to an arm. SIMD's flip taught it — #227/#233, 24282 gated vectors
          moving while `unsupported` sat at 2689 across the largest board change the project has
          made — and #235 needed it a second time, at which point task-bar folklore became text.
          (Ruling: Scott, PR #235 — his token, his veto standing.)
        - **A PR that adds a consumer.** #302 published the engine's first API and drove the corpus
          through it; `unsupported` did not move, because *a new consumer changes who asks, not what
          can be asked*. The zero is **derived** rather than forecast — it follows from the column's
          definition plus an empty `git diff -- internal/spec/`, before any measurement — so
          measuring both boards checks the derivation instead of confirming a prediction. Same
          distinction a flip turns on in the other direction, a flip's forecast needing
          pre-registration precisely because its numbers do not exist until the mechanism does.
          (Ruling: chat-Claude, PR #302.)
        - **The classify arm**, which is the positive case and the reason the condition is not a
          licence: it changed what the harness can ask, and the column moved **2574**.

      Recorded as one condition on the relay that would otherwise have added the second instance as
      its own bullet: *"they aren't two rules, they're one rule stated twice from the outside."*
      (Ruling: chat-Claude, PR #302.)
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
    merges it went **1:1.8 → 1:5.1** (engine 2007→463 lines, test 3681→2347 — the ad-hoc
    comparator of that era; uniform, **1:2.0 → 1:5.1**) while the unsupported column did not
    move at all. That is the number that made this rule; a ratio worsening **while the column
    stands still** is the definition of spinning, and it was invisible because every
    individual PR was defensible. Note which half of that sentence carries the weight: #117's
    measurement found the ratio alone is mostly a size artefact, so *the conjunction with a
    flat unsupported column is the signal* and the quotient by itself is context.
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
    - **The recalibration ran, and the answer is no number: the ratio is a size artefact, so
      it is quoted and never compared to a bound.** `make ratio` (`scripts/ratio.sh`) is the
      recorded command, deliberately *not* a `check` dependency — a script CI runs is a gate
      whatever it is called, and a gate is what this ruling declines. The eras, both series,
      and the derivation:

      | window | ad-hoc comparator | uniform comparator |
      | --- | --- | --- |
      | six merges ending `06f64dc` | 2007 / 3681 = **1:1.8** | 1870 / 3818 = **1:2.0** |
      | six merges ending `5b5e4c9` | 463 / 2347 = **1:5.1** | 458 / 2352 = **1:5.1** |
      | six merges ending `72b4f53` (current) | — | 3439 / 4532 = **1:1.3** |

      The comparator change barely moves these two windows — the deltas are ±137 and ±5
      lines, exactly compensating, and the only reclassified files are `internal/spec/wast.go`
      and `sexpr.go`. That is not a reading of the old figures but a *reproduction* of them:
      dropping `internal/spec` from the script's instrument list yields **463 / 2347**, the
      published ad-hoc pair to the line, which identifies the old comparator exactly rather
      than approximately. A recalibration that cannot re-derive the number it replaces has
      compared two things it does not know the difference between. The issue predicted `internal/gen` would be the main mover and for
      *those* windows it is not, because they predate the generators; where it bites is
      0014 (`6376b27`), which reads **1:0.4** ad-hoc and **1:2.9** uniform. So the old
      series survives its own recalibration and the drift it recorded was real.

      What does not survive is reading a ratio as a *rate*. Over 31 first-parent merges with
      engine lines, ρ(engine lines, ratio) = **−0.55** and the fit is
      **instrument = 486 + 0.79 × engine** — a fixed ≈490-line instrument cost per PR plus
      four-fifths of a line thereafter. That model predicts 1:5.7 at 100 engine lines and
      1:1.0 at 2000, from one behaviour. **So every candidate threshold is a disguised
      minimum-PR-size rule**: at 1:3.0 it flags six merges of which the worst two are #92's
      board bounds (1:38.8, **18** engine lines) and the UTF-8 partition (1:17.9, **28**
      lines) — small PRs, not spinning ones — and #113's 1:6.6 sits above the model's 1:4.6
      for its size by 257 lines, **0.56** of a residual sd. A gate that fires on #113 fires on
      its being **127 lines long**. The stated failure mode is therefore concrete rather than
      hypothetical: a numeric threshold would train the habit of padding engine diffs or
      batching arms until the quotient clears it, which is the metric eating the work.
      And the fit explains only **half** the variance (R² = 0.51, residual sd 458 lines),
      so it is a description of the corpus, not a predictor — quoting it as an expectation
      would be the second-order-honesty error one level in.
    - **Era-band hypothesis: tested, not supported.** Scott's read on the fourth crossing was
      that the interpreter-arm era may simply run hotter than the encoder era. Partitioned by
      the package that received the engine lines, the aggregate is `interp` **1:1.7** (n=5),
      `text` **1:1.5** (n=18), `binary` **1:2.0** (n=8) — indistinguishable, and the arm era
      is the *middle* one. With size controlled, mean residuals are `interp` **+28**,
      `text` **+61**, `binary` **−155** lines against a 458-line sd: nothing. The apparent
      streak is the four crossing PRs being *small* — 190, 242, 270, 297 engine lines against
      a corpus median of 344, right where the fixed instrument cost dominates — which is the
      same finding as above wearing era clothes. Recorded because a hypothesis that
      measurement killed is worth more written down than omitted, and because the reason four
      tokens got granted in a row is now known: it is not that the arms are expensive to
      certify, it is that an arm is a *small* piece of work and the per-PR instrument floor
      does not shrink with it. (Measurement: #117. Ruling: pending Scott, who ordered it; the
      case is stated, not closed by the actor.)
  - **Two consecutive instrument-only PRs is a stop condition.** Not a soft
    preference — stop and take product work, or get Scott's word to continue. The
    ratchet only turns one way otherwise, because control work is always available,
    always passes review, and always produces a clean green.
    - **The counter counts PRs whose *purpose* is non-product, not PRs whose line-majority is
      instrument.** The refinement was forced by #159, which lands `table.init`/`memory.init` end
      to end — board strata moving on engine answers, 1702 fails drained — and reads **1:1.4**, so
      the letter of the old rule made it instrument-heavy and two-consecutive with #158. That is
      the counter misfiring on its own purpose: the stop condition exists to prevent drift *into
      meta*, and a PR landing engine capability is product whatever its falsification bill. The
      last four arm PRs' ratios had already demonstrated it — an arm is a *small* piece of work
      and the per-PR instrument floor does not shrink with it (#117's fit, above), so a
      line-majority test on an arm PR is the disguised minimum-PR-size rule wearing the stop
      condition's clothes.
      - **The classification is named in the PR body and is challengeable — which is what keeps
        it from being self-serving**, the actor-never-classifies-the-actor rule being live and
        unrepealed. Two things do that work: the naming obligation makes the claim reviewable at
        the moment it is made rather than reconstructible afterwards, and **the line ratio keeps
        its own separate instrument** — it is still quoted every PR, never compared to a
        threshold, so a purpose-classified product PR that is *also* drifting is still visible in
        the figure. The exemption rule below is untouched: a purpose classification is not an
        exemption, it is a statement about what the PR *is*, and where the two readings differ
        the actor states the case and a principal rules. Scott holds the veto line on this
        refinement as on every governance edit. (Ruling: Scott, PR #159, on the agent's own flag;
        #159 is product and the counter resets.)
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

### Control work is a debt against the product, so it is charged, deferred, or declined — never taken because it is available.

- **Control work is a debt against the product, so it is charged, deferred, or
  declined — never taken because it is available.** The genuine finding that a control
  is missing is *filed*, and filing it discharges the obligation (*a design debt is
  discharged by a tripwire, never by an intention* — that rule says file the tripwire,
  and it does **not** say build it now). The exception, and it is the only one: a
  control that would catch an **accept-direction** defect the suite cannot see (§9 G-3)
  is product work, because the suite scores such defects green by construction and
  nothing else will find them. `optable`'s reference agreement and #88's twelve
  wrongly-rejected valtypes are the paradigm; a citation sweep is not.

### A zero-fail board is not a green light, it is a lost instrument.

- **A zero-fail board is not a green light, it is a lost instrument.** *Bucketed
  failures are the work plan* presumes buckets. When fail reaches zero the project has
  not finished, it has lost the thing that was pulling it toward engine work — and the
  fallback (deferral citations, controls, metadata) is all overhead by nature, so the
  gradient silently inverts. At zero fail the plan comes from **the largest unsupported
  stratum and the artifact it names**, and that artifact is stated in the PR. Found the
  way these things are: 4162/0 was reached and the next three PRs were instruments.

### A representation is not a recognizer, and 93.6% of the board wants the representation.

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
