<!-- Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0 -->

# Laws — Product and overhead

What gets selected, and what the selection is charged to.

Relocated from `CLAUDE.md`'s `## Disciplines` section, **verbatim**, when that file
became an index (see the restructure PR). Nothing was rewritten in the move: the bodies below
are the text as it stood, which is why superseded wordings still appear inside them where a
later ruling amended rather than replaced. The per-law recall keys `CLAUDE.md` carried were
retired with the index economy when that file became a brief and a pointer page (Scott's
directive, the four-workstream brief of 2026-08-17); the laws themselves were not touched.

`CLAUDE.md` links this family, and the two halves of that link are checked:
`TestMarkdownLinksResolve` (`internal/testenv`) that every pointer in every markdown file in the
tree resolves, and
`TestLawFamiliesAreReachable` that every family here is reachable from it — a law nobody can
reach is a law out of context.

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

      **A fourth case arrived on the very next PR and got no bullet, which is the evidence the
      generalization works.** #307 is a validator slice: its `assert_invalid` vectors score `fail`
      with a named cause, never `unsupported`, so the column cannot move for a third distinct
      mechanism — and the actor's flag asked whether #235's gate-campaign carve-out *extended*. The
      answer was that nothing needed to extend: the ordinary reward figure was not structurally zero
      here (`passFloor` moved +648), so there was no substitution to authorize, only the normal line
      quoted in the normal case. A rule stated as a list would have needed an amendment to say that;
      stated as a condition, it answered on its own. **This paragraph is prose and not a fourth
      specimen deliberately** — a case answered without an amendment is evidence about the rule, and
      filing it as an instance would re-create exactly the enumeration this clause refuses.
      (Ruling: Scott, PR #307.)

      **A fifth case tried the substitution from the other end and was refused: a *borrowed*
      figure is out.** #348 is overhead — a CI-subsection rewording, no engine diff — and the
      actor offered, as its reward figure, the delta of the product work the rewording rides
      with. Scott declined it, and the reason names a defect class rather than this instance:
      *"a borrowed figure is a reward figure with a different subject, which is the same defect
      as the membership swap in the 103 and the harness-widening-read-as-engine-capability
      conflation — a number that was true about something else, standing where a number about
      this artifact belongs."* **#235's carve-out is not the precedent**, and its two conditions
      are why: there the ordinary figure is structurally zero *and* a same-subject substitute
      exists (the all-on lane measures the same artifact's own vectors). A figure whose subject is
      a different PR satisfies neither, and he had already declined to extend #235 once on that
      reasoning (#307, above).
      So the honest report for an overhead PR is **`unmoved`, plus the named subject on its own
      line as context** — the subject is what the PR is overhead *for*, which the rule already
      requires, and it is context rather than a reward figure. **An overhead PR's reward figure is
      *none*, and this project already prefers `NOT RUN` to zero, `UNAVAILABLE` to negative, and
      absent to `0.0`**: a figure standing in a slot where no figure has a subject is the same
      false witness in the reward column that a fabricated `0.0` would be in a measurement one.
      Prose again rather than a specimen bullet, per the clause above. (Ruling: Scott, PR #364.)
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
    - **A ratio can be *structurally forced by the defect class*, and that reading is available to
      a principal and not to the actor.** [#600](https://github.com/scttfrdmn/burroughs/issues/600)
      landed at **1:10.5**, and the ruling on it was that the figure is forced: no `.wast` vector can
      witness a `memory.grow`-against-`memory.grow` race, because the corpus has no way to express two
      agents, so **a Go test is the only possible witness** and the instrument column is a property of
      what the defect is rather than of how the slice was worked. Two limits come with it, both from the
      ruling itself. It **does not generalise to a slice where a corpus vector could have been the
      witness** — which makes the claim a checkable one, since *could a vector have witnessed this?* has
      an answer — and it is not a classification the actor may reach for: *the actor never chooses the
      instrument that judges the actor*, so the case is stated in the report and a principal grants it.
      The failure it guards against is the inverse of padding: a slice whose witness could only ever be
      a Go test reads as undisciplined against a comparator that was never being compared to anything.
      (Ruling: Scott, on the #601 review — *"it's structurally forced here … That's a fact about the
      defect class rather than about discipline — and it doesn't generalise to slices where a corpus
      vector could have been the witness."*)
  - **Two consecutive instrument-only PRs is a stop condition — RETIRED on the #647 review, and
    everything below it is the minting record rather than a live rule.** Scott's words: *"the
    instrument/product counter is retired. Keep quoting the ratio; gate nothing on it."* Nothing is
    gated on the count any more — no stop, no principal's discharge to seek, no classification to
    defend. **The sub-bullets are kept, and kept in the present tense they were written in, because
    what they establish outlived the gate they were establishing it for**: the runtime-vs-harness
    test, the purpose-not-line-majority reading, the structurally-forced carve-out and its
    non-generalisation, and the exemption tokens are all still how a *class* is read when a report
    names one. What went is the consequence, not the vocabulary. Read what follows as: this is how
    the classification works, for a counter that no longer stops anything.
    - It was, while it ran, not a soft preference — stop and take product work, or get Scott's word
      to continue — because the ratchet only turns one way otherwise: control work is always
      available, always passes review, and always produces a clean green. **That asymmetry did not
      go away with the counter**, which is why the ratio is still quoted every PR and still compared
      to no threshold. The retirement's own argument is that the ratio prices the drift the counter
      was built to catch, so the drift is *observed* where it used to be *gated*.
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
    - **The test the classification is made against: does the PR change what the runtime *can
      do*, or only what the harness can *say* about what it does?** The rule above says the
      counter counts purpose and that the purpose is named and challengeable; it did not say how
      to decide, and #457 is where that cost something. Scott's ruling, verbatim: *"does the PR
      change what the runtime can do, or only what the harness can say about what it does?
      `callBudget` and `trapExhaustion` already existed, so #457 is the latter."* The arm made 15
      `assert_exhaustion` vectors askable and the engine's answers were already correct and
      already there, so the column moved 15 on a PR that added no capability.
      - **And "it clears a v0 board column" is explicitly not sufficient** — *"nearly any
        instrument work can be described that way, and a counter that accepts that argument stops
        counting."* This is the sharp edge, because the brief *requires* every PR to quote its
        `unsupported` delta, which makes the column the most available justification in the
        project. A reward figure is not a classification: a PR can honestly report a non-zero
        `unsupported` delta **and** be instrument, which is exactly what #457 did. Where the
        column drains without capability changing, see
        [boards-and-buckets](boards-and-buckets.md#a-column-draining-to-zero-is-not-the-engine-reaching-a-milestone)
        — v0's whole remaining residue measured that way.
      - **Class is by purpose, not by location: engine code can be an instrument.** The
        runtime-vs-harness test asks what the PR changes, and "it edits `internal/interp/`" is not an
        answer to it. The specimen is #136's proposed execution counter: weighting the opener
        distribution by *dynamic* block entries requires a counter in `runFrame`, which is engine
        code by every file-path measure and **instrument by purpose** — it changes what can be said
        about the runtime and nothing about what the runtime can do. The tell is that the location
        argument arrives exactly when the actor wants the work reclassified: *"engine code, which is
        where the next slice has to go anyway"* was the phrasing, and it would have bought a fourth
        consecutive instrument PR under a product label. Note this runs **opposite** to the ratio
        comparator, which is uniform and location-based on purpose (`engine = code in the module
        path`, no per-file pleading) — the two questions are deliberately different, so the same
        counter counts as engine for drift and as instrument for selection. Neither reading is
        available to the actor as a choice. (Ruling: Scott, on the #503 review — *"a counter in
        `runFrame` is instrument by purpose, regardless of living in engine code. The counter
        measures purpose, not directory."*)
      - **And "a contract clause names it" is the third of these arguments rather than a new one:
        normativity does not decide class.** A §4 clause can be discharged by a *test*, so a slice
        whose subject is normative can still be pure harness — the question is about the diff, not
        about which section of the contract the diff serves. The specimen is #607's own report, where
        the actor flagged that §4 **B-MM-5** makes the litmus battery normative and asked whether that
        made the battery product. Under the test it was not close: no line of that diff is reachable
        from any guest program, so nothing about what the runtime *can do* moved. What earns it a place
        here is the family — it is the **third** sibling of an argument this page had already ruled
        inadmissible twice, after *"it clears a board column"* (#457) and *"it lives in engine code"*
        (#503), and all three substitute a correlate of the work's importance for the deciding
        question. *Lessons are indexed by shape, not by file*: the shape is "an argument that the work
        matters, offered as an argument about its class", and both siblings were already on this page
        when the flag was raised. The ruling also settles what the actor *may* do with the test —
        **apply it** — which is not the self-classification the exemption rule forbids: the rule comes
        from outside, and running it is execution. (Ruling: Scott, on the #607 report — *"Normativity
        doesn't decide class … Purpose, not directory, not line count, and not which contract section
        names it. Applying a rule I supply is execution rather than self-classification."* Recorded by
        the actor who was ruled on, which is durable and not independent, so the commit carrying it is
        `Ratio-Class: carried`.)
      - **A repair the PR's own verdict compels is part of that PR, and the check is one question:
        was the PR blockable without it?** The counter counts PRs, so *what is one PR* decides
        whether two-consecutive trips, and without a boundary the actor can merge slices to keep
        the count low. The specimen is #597, which repaired graves #598 and #599 alongside #595:
        both were found by that PR's own CI verdict and it had no bound green without them, so
        they were blockers to landing rather than independently scheduled work, and the three
        counted as **one**. The criterion cuts the other way just as hard — **found by browsing,
        or already scheduled, and they are their own slices and their own count** — which is what
        makes it a boundary rather than a licence. Note what made the check possible: the actor
        stated the motive for wanting one count and named the alternative reading, so the
        classification arrived challengeable instead of banked. That is the
        actor-never-classifies-the-actor rule working as designed, not an exception to it.
        (Ruling: Scott, on the #597 report. Recorded here by the actor who was ruled on, which is
        durable and not independent — *durability is not independence* — so the commit carrying
        it is `Ratio-Class: carried`.)
      - **Two scope corrections that arrived with the test.** First, **the stop condition governs
        what comes next, not whether a finished PR lands**: *"holding a bound green was never part
        of the stop condition anyway."* The actor's instinct on #457 was to hold a green PR pending
        the ruling, and the hold was aimed at the wrong object — ask the question and let the work
        land. **Recurred on #503, and the corpus already contained the answer**: a third consecutive
        instrument-class PR was held unmerged pending a stop-condition discharge, on the reasoning
        that self-merging would be the actor discharging its own condition. The reasoning is sound
        and the object was still wrong — this clause says so in as many words — and it cost a
        principal a decision cycle on a question the tree could have answered. *Lessons are indexed
        by shape, not by file*: the shape was "may a bound green be held", the family was this one,
        and it was not read before the flag was raised. Second, the counter has a **third state** besides *blocked* and *argue your way out*,
        and it matters near the end of a phase: *"Near v0 close the remaining legitimate work may
        simply be harness. The counter stands, but if the classification comes back and the next
        required work is also instrument, bring it to me for a stamp — not blocked, and not
        self-exempted."* That is the exemption rule below, not a hole in it: the token still comes
        from outside, and what is new is only that a phase's tail is a *recognized* reason to ask
        for one. (Ruling: Scott, on #458, disposing of PR #457/#440.)
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
      - **An order can be *scoped* rather than blanket, and the scope is three bounds: subject,
        expiry, and a carve-out.** #113's stamp and #103's *"do it now"* were both one-off, so the
        clause above had no shape for a standing order — and a standing order is what the §4 litmus
        battery needed, since a battery *is* a test suite and every slice of it is instrument by the
        deciding test, which would have meant three consecutive stamp requests for work a principal
        was already ordering. Scott's form, verbatim: *"the §4 battery's slices are chair-ordered
        harness for the duration of [#10], and the counter doesn't advance on them … Three bounds,
        because a blanket exemption is exactly the shape that let the ratio climb: It covers slices of
        #10's battery only. It expires when #10 closes. Any slice that **also changes runtime
        behaviour** … is product, and gets classified and measured normally. And quote the ratio on
        every one of them regardless. The cost stays visible rather than exempted into
        invisibility."* Three readings the actor does not get to choose, all confirmed rather than
        assumed: the order is **prospective**, so the counter keeps whatever value it had when the
        order arrived; the provenance is **`carried`, never `ordered`**, because an in-session order
        has no artifact for the trailer to cite; and the carve-out **fires early and that is healthy**
        — #543's wait/suspend mechanism was the battery's own prerequisite and was classified and
        measured as product.
        - **Coupling a deadline to a scope makes both widening and narrowing that scope into
          levers.** #10's closure carries two other things — this exemption expires there, and the
          parked decisions queue drains there — so *"don't let #10 sprawl"* guards one direction only:
          the actor gains from a **narrow** #10 exactly as much as from a wide one, and narrowing looks
          like discipline while doing it. Disclosed by the actor in the #607 report and generalised by
          Scott into the law above, with the disposition that closes the hole: **closure comes on the
          registered cases being discharged, and a still-blocked row at the end comes to him** — so
          neither edge of the scope is the actor's to move. (Ruling: Scott, on the #607 report.
          Recorded by the actor it constrains, so `Ratio-Class: carried`.)

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

### An obligation charged to a rider is lost the moment its carrier lands.

- **An obligation charged to a rider is lost the moment its carrier lands.** The clause
  above says control work is *charged* to product work, and it does not say what a charge
  is attached to. Attaching one to **another item's landing** — "the comparator rides
  #130's slice, charged to it as its falsification bill" — makes the obligation's survival
  depend on an event whose whole purpose is to stop existing. It is the one place a
  charge must never go.
  - **The specimen, measured in #464's reconciliation.** #67 half 2's accept-direction
    comparator was charged to #130 in a scheduling comment, on the sound argument that a
    column reaching zero needs a successor instrument *in the same slice* rather than a
    report of the zero. #130 then landed **as #425** (`075e11c`), taking `encodeFailCeiling`
    3 → 0 — the very zero the comparator was charged to protect — with a closing comment
    about `immPart`, deferred-immediate positions and a derived control domain, and **no
    mention of the comparator at all**. The bill was charged to a slice that landed without
    paying it, and from the outside the citation still reads as *tracked*.
  - **Why the carrier's rename is not the cause but the tell.** #130 landing under a
    different number is what made the loss visible; it is not what made it possible. An
    obligation attached to an event has no state of its own, so nothing anywhere goes red
    when the event passes unpaid — the failure mode is *silent by construction*. It is the
    amendment under [artifacts become
    oracles](graves-and-sweeps.md#artifacts-become-oracles) arriving through a different
    channel — *"closing is a state transition on an issue, but a queue label is a claim
    about the world"* — because there a state transition **carried** a claim about the world
    with it, and here a state transition **dropped** one.
  - **So the remedy is the cheap one, and it is the one #67's own half 2 already models
    for its subject: give the obligation its own number.** An issue has a state a query can
    read, a label a sweep can see, and a milestone a reconciliation can count. A sentence
    inside another issue's comment has none of the three. *Declared-and-tracked has two
    halves*, and a rider satisfies only the first.
  (Order: Scott, on the #465 review — *"re-charge it as its own issue, with its own number
  … never charge an obligation to a rider again."*)

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
