<!-- Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0 -->

# Laws — Controls

A control's own failure modes: stillbirth, vacuity, scope, attribution.

Relocated from `CLAUDE.md`'s `## Disciplines` section, **verbatim**, when that file
became an index (see the restructure PR). Nothing was rewritten in the move: the bodies below
are the text as it stood, which is why superseded wordings still appear inside them where a
later ruling amended rather than replaced. The per-law recall keys `CLAUDE.md` carried were
retired with the index economy when that file became a brief and a pointer page (Scott's
directive, the four-workstream brief of 2026-08-17); the laws themselves were not touched.

`CLAUDE.md` links this family, and the two halves of that link are checked:
`TestClaudeMDLinksResolve` (`internal/testenv`) that every pointer on the page resolves, and
`TestLawFamiliesAreReachable` that every family here is reachable from it — a law nobody can
reach is a law out of context.

### A control's green must be falsifiable, and the way to know is to break it.

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

### A comparison against an empty set succeeds, so a control that compares needs a vacuity check.

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
  - **And the law reaches a *probe*, not just a control: a zero out of an instrument needs a
    positive control proving the channel was open.** The vacuity failure above is an assertion
    fed nothing; this is a *measurement* fed nothing, and it is worse in one specific way —
    an empty comparison at least ran, where a suppressed write never reached the reader at
    all. The specimen: `go test` **discards a passing binary's stderr without `-v`**, so a
    `println` counter at a guard site reported "0 hits across the corpus" when it had reported
    nothing whatsoever. Both a genuinely-unexercised path and a closed channel print zero
    lines, and the transcript is identical. The remedy is not `-v` — `-v` was the *bug's*
    repair and the class survives it — but an **unconditional print at the same site**, whose
    number (294) is what licenses reading the conditional zero as a fact about the code. So
    before a zero from a probe is quoted anywhere, ask what a *broken probe* would have
    printed; if the answer is "the same thing", the measurement has not happened yet. Sibling
    of *presence-of-status is not presence-of-content* one layer down: there a state field
    stood in for a payload, here an empty channel stands in for an empty result. (Earned on
    #248's counter, the second reading of which was quoted before it was bounded.)

### A tripwire whose subject dissolves is re-pointed, never closed.

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

### A test named for a partition must be checked against the partition, not against its own case labels.

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

### Floors bound the catastrophic case; only an exact count sees a small silent loss.

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
  - **A floor equal to the failure mode's output certifies the failure.** So a floor is derived
    from the *authority*, never frozen at what the current reader happens to produce. #159's
    specimen: the positional `plaininstr` reader's pair floor was set at **8**, which is exactly
    what the *degraded* reader yields — the alternation pattern finds 8 two-lookup arms where the
    positional one finds 10, the two extra being `STRUCT_GET`/`STRUCT_SET` whose second lookup is
    `$3 c (field x.it)`, not a word in the alternation. Stub the extractor down to the weaker
    pattern and the floor waves it through, having been set to the number the bug produces. What
    makes this worse than an ordinary loose floor is the **misdirection**: the run did go red, so
    the control looked alive — *the drift report was true, the attribution was the lie*. It named
    drift in `idxPairLookupKinds` (the two struct kinds now missing) when the defect was in the
    reader, so a reader following the failure message repairs the subject to match a broken
    instrument. A control's blind spot presenting as its subject's defect is strictly harder to
    catch than silence. The remedy is a **discrimination check** beside the floor — assert the
    reader exhibits the capability that distinguishes it from its degradation (here: at least one
    matched lookup is a parenthesised expression), because a count cannot separate two readers
    whose counts overlap. (Ruling: Scott, PR #159, naming the law from the finding.)

### Reconcile an extent, never floor it.

- **Reconcile an extent, never floor it.** A one-sided bound is silent in the direction it does not
  bound, and it is silent *with the right domain in hand* — which is what separates this from the
  three laws it sits between. The floors law above is about a bound's **tightness** on a scalar, and
  its remedy is the exact count. The ledger law below is about a bound's **subject**, and its remedy
  is per-item assertion. *Coverage is a claim* is about a bound's **domain**. This one is about a
  bound's **predicate strength** over a claim that has *two ends*: a range, an interval, a distance, a
  population with a known other side. For an extent there is no "exact count" to reach for — there is
  a start and an end, each needing its own derivation from the authority — so the remedy is not
  tightening but **reconciliation**: derive both ends independently and require them to meet.
  Containment is the pure specimen of the defect, being membership asked of a thing that has an
  extent.

  Three local specimens, and the third is the strongest evidence a law can have:

  - **`minBoundPopulation` at 8, content with 18 of 19** (`internal/spec/boardbound_test.go`). The
    board-bound walk's floor is a minimum over a population that is nineteen; a trigger that narrowed
    by one shape left 18 and the floor said nothing. Only the exact count beside it fired, on #307.
    That much is the floors law. What makes it *this* law's specimen too is the reason the floor was
    written loose on purpose — "so a nineteenth bound is covered rather than ignored" — which is a
    deliberate one-sidedness bought to tolerate growth, and the price is exactly blindness to shrink.
    A one-sided bound chosen for a good reason is still one-sided.
  - **`allOnPassFloor`'s slack of 250, read as evidence about a distance of 3.** Its row in the
    `boardBound` table carries `slack 250`, so the floor cannot resolve any effect smaller than that
    — and a bound's *not firing* was taken on both sides of a relay as evidence that a three-vector
    effect had not happened. Two independent instances of the same move: treating silence as a
    negative without asking whether the instrument's slack could have resolved the thing in question.
    Not minted for that on its own — #315 removes the confusion structurally, and a law duplicating a
    control is what the index economy cannot afford (ruling: Scott, PR #317) — so it stands here as a
    specimen of the extent shape rather than as the mint's occasion.
  - **Containment-as-floor, in a control built in the same PR to catch that very class.**
    `TestReferenceRangeCitationsContainTheirSubjectsSite` (`internal/validate/vec_authority_test.go`)
    checks that a `valid.ml:A-B` citation *contains* its subject's site, which catches a range that
    has retargeted wholesale. It cannot catch a range whose end is short while still covering the
    site — and that is precisely the defect its own author had just committed: `checkOffset` cited
    `:390-392` for a quoted block running to `:393`. **The control failed to catch its own PR's
    instance of the class it was written for**, and the off-by-one was found by hand instead. Both
    ends of that range are derivable from the reference (the `require` and the wrapped string), so
    reconciliation was available and containment was chosen.

  Relayed corroboration, recorded as relayed: Scott reports the same law derived independently in
  another of his projects, from an unrelated failure. That is the outside derivation the threshold
  asks for, and it is worth distinguishing from the module-form case, where two "independent"
  specimens turned out to share a mechanism and only impersonated corroboration.

  The operational test is one question — **does the claim have two ends?** If yes, a check that
  asserts one of them reads as checked and covers half. If no, the floors and ledger laws already say
  what to do. And the diagnostic that would have caught all three specimens is cheap: state the
  distance between the bound and what it bounds, because *an unasserted distance is the vacuum* and an
  extent has two of them. (Ruling: Scott, PR #317, minted against a demotion rather than against the
  ceiling.)

### A total is not a ledger.

- **A total is not a ledger: where items are enumerable, assert per item and let the total be a
  checksum on the ledger rather than a claim in its own right.** The mechanism is that **errors of
  opposite sign cancel**, so an aggregate bound of *any* shape — floor, ceiling, band, or exact
  equality — is satisfied by a distribution nobody predicted, and the closer the total lands to
  its forecast the more confidently the miss is read as a hit. This is the sibling of the floors
  law above and **not** a restatement of it: that law is about a bound's *tightness* on one
  quantity (a floor cannot see a small loss, so pin the exact count), where this one is about a
  bound's *subject* (an exact count on an aggregate still cannot see a redistribution, so pin the
  items). Tightening the ≤896 ceiling to `== 857` would have satisfied the floors law completely
  and caught nothing here.

  **The exception, and it is a real one: *a total is not a ledger, except where the consumer
  consumes the total — and even then it is never a substitute for per-item assertion when the
  items are individually checkable.*** The law demotes an aggregate because a *forecast* about a
  distribution was being read off a sum. Where something downstream actually consumes the sum,
  the sum is a claim in its own right and stays fatal: it is no longer a proxy for the items, it
  is the quantity itself. `claudeMDCeiling` (`internal/testenv/laws_test.go`) is the specimen —
  the consumer of `CLAUDE.md` is a context window, and context cost is **total bytes**, not
  per-entry bytes, so an index that stays under every per-entry bound and blows the file total
  has broken the thing the ceiling protects. But the exception buys the total its life, not its
  primacy, and the second half of the clause is the operative half: the index's entries are
  individually enumerable, so one entry can bloat while the total stays green, and trimming an
  unrelated entry buys room for it — exactly the cancellation above. So the two instruments
  stack rather than substitute. The **per-entry ledger is primary**, asserted on the nose, and it
  is the *attribution* instrument: when the total moves, the ledger's diff names which entry
  moved it instead of prompting a hunt. The **total remains fatal** as a reconciliation guard
  against the real artifact, because the artifact is what gets consumed and no sum of rows is a
  substitute for `os.Stat`. The general test is one question — **does anything downstream read
  the aggregate?** If yes, keep it and add the ledger under it; if no, the aggregate is a
  checksum on the ledger and nothing more. And on the specimen's own reading, same ruling: once
  the ledger catches the ratchet per entry, the surviving total is a **budget** and not a
  ratchet-stopper — so room for a new index key comes from demoting a law a live control already
  enforces, never from raising the bound. (Ruling: Scott, PR #298, on the ceiling's own trip.)

  Two independent specimens, which is why it was minted rather than noted:

  - **The ≤896 forecast bound held while four of its modules individually broke it.** 829 landed
    under 896, comfortably, and the per-module reading was `if.wast` +1, `i32.wast` +3,
    `load.wast` +1, `local_tee.wast` +1 each **above its own stated upper bound**, against
    `load64.wast` coming in **45 under**. One large miss of one sign paid for four small misses
    of the other, and netted to −39: a number that reads as conservative forecasting. Worth
    naming the two defects the per-module ledger exposed and the total concealed, because they
    are of different kinds: (a) the vocabulary predicate **does not consult the feature gate**,
    and `isGated` is asked first in the arm's fixed order, so `load64.wast`'s 46 vectors score
    `gated` and never reach the match at all — measured 46 gated, 0 matched; (b) the bound's
    stated justification, *"it cannot under-count"*, is simply **false** — the validator can
    refuse a module before the walk ever reaches the out-of-vocabulary instruction, so
    subset-of-vocabulary is a sufficient condition for conversion and never a necessary one.
  - **`TestGatedVectors` already did it right, in this repo, for this reason.** It asserts each
    file's count *on the nose* against a per-file, per-line `allowed` map rather than checking a
    sum, and pins `len(GatedAt) == Gated` so the line list and the count cannot drift apart. The
    law was therefore recoverable from the codebase before it was written down — *lessons are
    indexed by shape*, and the shape was sitting in a sibling test.

  **Attribution replaces the second end.** The pre-registered plan for the ≤896 bound was a
  two-ended interval; what the second end was reaching for is the question "did the engine's
  capability do this, or did the harness widening do it?", and an interval cannot answer that
  however tight it is, because both mechanisms move the same total in the same direction. A
  per-destination ledger answers it directly: the arm's column movement and the validator's pass
  column are separate rows, and neither can borrow the other's credit. So the remedy for a
  suspicious aggregate is not a narrower aggregate — it is **the ledger that says where each item
  went**.

  Scott's own share, recorded because a law whose history is only the agent's errors teaches the
  wrong lesson about where review fails: *"I questioned the bound's falsifiability but accepted
  'cannot under-count' as given. Monotonicity was a claim about the predicate and I never asked
  what established it."* A justification offered *for* a bound is itself a claim about the space,
  and review that interrogates the bound while accepting its warrant has checked the conclusion
  and not the premise.

  The implementation is `TestAssertInvalidDestinationLedgerCloses` (`internal/spec/ledger_test.go`),
  and three things found while building it belong with the law:

  - **The checksum's job is the partition, not the count.** The sum is computed from the measured
    tallies, so it *cannot* fail while every row passes — which is the point. What it detects is a
    destination nobody is counting: if the arm grows a sixth outcome, every pinned row still
    agrees and the identity closes short.
  - **The falsification had to be a compensating one, and the first attempt did not fire.** Moving
    one vector from `declined` to `mismatch` leaves the total untouched; the rows caught it (1055
    vs 1056, 11 vs 10) while `total = 2574` passed and the checksum closed — the law demonstrated
    inside its own control. The first perturbation attempt was guarded by
    `tl.declined == 3 && strings.Contains(key, "unsupported")`, which map iteration order never
    satisfied, so the run came back green: *an under-matching trigger predicate*, in the
    falsification rather than in the control, which is a green that means nothing and looks
    exactly like a green that means everything.
  - **Two populations shared a name.** Subtracting `UnsupportedByHead["assert_invalid"]` from a
    `Kind == KindAssertInvalid` total produced **negative pass residuals** in three files, because
    an unsupported `assert_invalid` *head* is a command `classify` gave a different Kind and so
    never entered the total. Reported beside the identity, never inside it — and the residual is
    checked for sign rather than assumed, since the negative number is the only reason the
    conflation was visible at all.

  (Ruling: Scott, PR #295, from the Board section's own 544-vector gap.)

  **Demoted on PR #317, and the heading above is the compressed key.** The index carried this law's
  whole sentence — *where items are enumerable, assert per item and let the total be a checksum on the
  ledger rather than a claim in its own right* — as its recall key, 347 bytes of `CLAUDE.md`. The
  content is now enforced by two live controls that fail when it is violated: `TestClaudeMDIndexLedger`
  over the index's own entries, and `alignmentAdmissions` in `internal/validate`. Under `claudeMDCeiling`'s
  ruling (PR #298) — *a law with a live control that fails when it's violated does not need index prose
  teaching it; the control teaches it, at the moment it matters* — that makes it the demotion candidate
  the ceiling's comment had already named by name. So the key is now four words and the sentence lives
  here, where the bold lead above still states it in full. Nothing was retracted and nothing moved to a
  weaker status: **demotion is an index-economy operation, not a downgrade of the law**, and it is how
  *reconcile an extent* above bought its room instead of raising the budget. One key out, one key in, no
  ceiling movement. (Ruling: Scott, PR #317.)

### A suspiciously clean result is a tell, and *exactly zero* is the cleanest one.

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
  - **The registry form: a control that names the fact it expects cannot notice the registry is
    missing that fact, because the control supplies it.** #106's echo is a premise measured over
    the sample the code reads; this is a *control written from the same name the registry lacks*,
    and it passes for the right reason while the omission stands. Grave **#264**: a new decoder
    sentinel arrived with two controls asserting `errors.Is(err, ErrMalformedBrOnCastFlags)` —
    both green, the sentinel being correct — and no entry in `declaredErrors`, the fuzz target's
    allowlist. Neither control could ask "is this declared?", that question being invisible from
    inside a test whose subject is the condition. Only an instrument that **enumerates the whole
    surface without knowing what any one condition expects** sees it, which is why the fuzzer
    found it in 41 seconds and `make check` could not. Two consequences worth carrying: the
    detector must be blind to the individual fact (the echo rule's own remedy, one level over),
    and the sweep for siblings is not optional — grepping every exported sentinel against the
    allowlist turned up a **second** omission whose call sites are all gate-blocked, so it was
    unreachable-by-construction in the fuzz target and would have surfaced as an unexplained red
    *at a gate flip*. A latent omission's arrival time is chosen by the flip, not by the defect.
    - **The space is (fact × registry), and the sentence above got that wrong while being
      written.** It says "grepping every exported sentinel against the allowlist" — *the*
      allowlist, singular — and the package has **two**: `declaredErrors`, and a second inline
      list inside `FuzzConstExprProgress`. Enrolling the sentinel in the first turned
      `FuzzDecodeModule` green and left `FuzzConstExprProgress` red on the same byte string, so
      the class recurred **in its own repair**, one push later, and the third instance indicts
      the sweep rather than the omission. Two things generalize. First, *a sweep run over the
      registry that failed is not a sweep over the space* — the failing registry is the one
      instance guaranteed to be fixed, which makes it the least informative place to look, and
      the tell is that the second run's green was bought by re-running only the target that had
      gone red. Second, the duplication is **not** the defect to fix: the constexpr list is the
      *narrower, stronger* claim (only these may come out of the instruction grammar), so
      deriving it from the broader one would collapse a real assertion into a tautology. Two
      registries over one space is a legitimate design that carries a sweep obligation, which is
      why the tripwire is scoped to the product and not to either list. (Grave #264, third
      instance, which is also where the re-scoped tripwire lives — the obligation widened, so
      the issue was re-pointed rather than reopened alongside a new one.)
  - **And its mirror: an impossible count is the strongest witness there is, because a value that
    cannot exist convicts the model rather than the measurement.** A clean result invites the
    suspicion above and can still be honest; an *impossible* one has already settled the question,
    and the only remaining work is finding which assumption it kills. #251's probe is the specimen:
    two pending operands under a `return` produced **`left -1 numeric`**, and a stack cannot have
    negative depth — so the arithmetic was being done against the wrong origin, which is exactly
    the missing frame base. Note what the tell bought that a plain refusal would not have: the
    one-operand row's `left 0 numeric` is a *plausible* number and reads as an ordinary arity
    disagreement, so it would have been argued about; `-1` cannot be argued with and named the
    cause in one row. So when a probe emits a quantity outside its own domain — a negative count, a
    length past an image's end, a depth below zero — stop reaching for the instrument and read it
    as a **derivation** of the defect. The family this joins is the wrong-layer error and the
    fabricated byte, both being the engine wrong about its own input; this one is the engine wrong
    about its own *shape*. (Ruling: Scott, PR #252, from #251's probe.)

### A control isn't born until it has been watched die.

- **A control isn't born until it has been watched die.** `Extract`'s partition check read as a
  real guard and could not fire on **any** input — `byGrammar` is keyed per token kind, `byLexer`
  per keyword, so `byLexer[kind]` asked whether a keyword is spelled `BINARY`. It was found by
  writing the falsification test and watching it *not fail*, which is the birth requirement
  working exactly as written. A no-op guard and a working guard produce identical output on every
  input that doesn't trip them, which is all of them. So budget for the falsification *passing*:
  that is not the test being wrong, it is the control being **stillborn**, and it is the most
  valuable outcome the exercise has. A lookup across two differently-keyed maps is a no-op
  wearing a predicate's clothes. (Ruling: Scott, PR #108.)
  - **A falsification that passes is a question with three answers — a no-op mutation, a blind
    control, or a wrong prediction — and it gets classified before anything proceeds.** The
    trichotomy is the parent of the print-the-diff rule below, which named the first two answers
    and called them exhaustive; the third was found by walking into it. The classification *is* the
    work, because the three demand opposite next moves — repair the mutation, retire the control,
    correct the belief — and picking wrong retires a working control or ships a broken one.
    1. **A no-op mutation: a non-empty diff is not a semantic mutation**, which makes #159's diff
       check a **floor, not a proof**. Rung 3's M6 "swapped" `array.copy`'s two `popArrayTarget`
       calls by writing `dst2, dstIdx2 := popArrayTarget(…); src, srcIdx := dst2, dstIdx2` — a
       clean, plausible, multi-line diff that **renamed a variable and changed nothing**, so the
       control was right to pass and the *mutation* was the defect. Rewritten as a real reordering
       of the two calls it failed with the exact signature the test's own message predicted
       (`destination[0]=1`). Read a diff for **behaviour**, never for non-emptiness: a diff that
       only moves names is the empty diff wearing clothes.
    2. **A blind control** — the stillbirth named in the parent rule, still the most valuable
       outcome the exercise has, and the one the other two answers get mistaken for.
    3. **A wrong prediction — the battery's highest function, because it falsifies the author's own
       model of the control.** Rung 3's M15 was written as an *expected pass*: a test comment
       claimed `Pack.U`'s unconditional unsigned read was unobservable because `pushField` masks on
       the way out. It **failed**, killing the prose rather than the code — both halves of the
       claim were wrong, and the real fact is a narrow-on-store / trust-on-read contract between
       `loadStorage` and `pushField`. So an expected-pass mutation earns its run precisely because
       green confirms a belief and red *destroys* one; a battery that only runs mutations it
       expects to kill can never correct its author. The parenthetical that dressed that prediction
       as a measurement ("the mutation leaves the all-on lane at 61764" — reasoned, never run)
       needs no new law: it is the standing never-quote-a-count-you-did-not-run rule, and the
       repair is to correct in place and keep the episode. (Ruling: Scott, PR #250, his token, on
       the agent's own two flags — "two-thirds of the same trichotomy".)
    - **The trichotomy's rider, and the deepest thing the method has produced: falsification proves
      an assertion *live*, never *right*.** All three answers above presuppose the prediction is
      about the right rule; **none of them can ask whether it is**, so a control can be born, kill
      its mutation on the nose, and pin a fact its own authority cannot represent. Grave **#266** is
      the specimen and it satisfied every clause the method demands — falsifiable, watched to die,
      failing with exactly the signature its comment predicted — while asserting that a null's
      `Kind` discriminates two nulls, where the reference has **one** heaptype-free null reference
      value (`runtime/value.ml:20`, nullary; `:112` types it `(Null, BotHT)` whatever produced it)
      and `assert_ref_pat` answers `NullPat _, NullRef -> true` unconditionally (`runner.ml:476`).
      So **liveness and correctness are separate audits**, and the second one has exactly one
      method: **read the authority against the assertion.** A mutation cannot do it, review cannot
      do it (review verifies code against claims, and here the claim was the defect), and the board
      cannot do it — the deviation was accept-direction, green on every vector by construction.
      - **The inverse of *the defect stated as the rule*, and both are caught the same way: the rule
        stated truly, directly over the defect.** #266's tell was `RefClass`'s own doc comment
        (`value.go:137-139`) transcribing the authority **correctly** — both `NullLit ht -> Value.(Ref
        NullRef)` and the unconditional `NullPat` arm — two declarations above code implementing its
        opposite, with a control pinning the opposite as well. So the cheap sweep is worth running
        whenever a control pins a *distinction*: grep the nearby prose for the arm that dissolves it,
        because the family's other face means the transcription may already be in the file. A codebase
        that cites its authority well enough to convict itself is worth reading before it is worth
        mutating.
      - **A citation list is itself a claim**, and it gets resolved before it is published like any
        other. #266's closing comment listed five fix sites; three were named at the wrong
        declaration until each one was grepped and pinned to a line. Same oracle as
        `TestFixtureProvenance` and #114/#115's identifier check, pointed at the enumeration rather
        than at a single reference — an approximately-correct list of citations reads exactly like an
        exact one.
      (Mint: chat-Claude, relayed by Scott, on grave #266; Scott's veto standing.)
  - **A control must fail, never hang — a timeout names no row.** The birth requirement's
    second failure mode, and it is not stillbirth: the control fires, it is technically red, and it
    is *worse* than red, because `panic: test timed out` identifies no case and takes the whole test
    binary with it. `br_table`'s loop row was first written with the **default** re-entering the
    loop, so two of four mutations — ignore the vector, read the labels as absolute depths — never
    left the loop and wedged the harness; reversed so the *table entry* is the loop, both report a
    number. A mutation that wedges the harness is the **zero-progress defect wearing a test's
    clothes**, and the sibling law is the parser one: a loop whose exit condition can be lost
    proves nothing by not returning. So when a row's subject is a loop, arrange it so a wrong engine
    *terminates and answers wrongly* — and confirm that by running the mutation, since which
    arrangement hangs is not deducible from reading it. (Ruling: Scott, PR #142.)
  - **Print the diff — the mechanism that separates the trichotomy's first answer from its
    second.** Written originally as "either a stillborn control or a mutation that did not apply,
    and nothing else tells the two apart", which the third answer above falsified; and #250's M6
    then sharpened what the diff has to be read *for*, non-emptiness being satisfiable by a
    rename. So the method is *edit, print the diff, then run* — permanently, not as a habit for
    suspicious cases. The specimen is #159's
    `TABLE_INIT` deletion, which **passed on its first attempt and the control was right to pass**:
    the mutation script's pattern matched `initSugarKinds`, which holds a byte-identical
    `"TABLE_INIT":  true,` line one screen above the intended map, so a row in a *different* table
    was deleted and the subject was never touched. Read as a stillbirth, that outcome retires a
    working control; read as a non-application, it costs one anchored retry (`(var
    initReversedKinds = map\[keywordKind\]bool\{\n)`), after which the control failed correctly.
    Note which way the ambiguity is dangerous — the two readings differ in *what you go and change
    next*, and the flattering one is the one that says the control is at fault.
    - **And field attribution is not first-match, wherever a mechanism edits or reads a named row
      — the gated allowlist, fix sites, and now the mutation scripts themselves.** The rule already
      governed generators (`gateFor`'s narrowest-match, `gatemap.go`) on the argument that an
      answer depending on slice order is a load-bearing invisible ordering; #159 showed a
      *falsification harness* is the same kind of mechanism, because a pattern that names a row by
      its text alone will find whichever copy comes first. Anchor on the containing declaration,
      not on the row. (Ruling: Scott, PR #159, from the mutation findings.)

### A design debt is discharged by a tripwire, never by an intention.

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

### A control scoped to the current sample inherits the current blind spot; scope controls to the space.

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

  **The sharpest specimen is #333, and it is sited here rather than under the extent law because the
  defect is upstream of any bound.** `citationFiles` (`internal/validate/vec_authority_test.go`)
  enumerated four of the package's eight citing files under a doc comment calling the domain "the
  package's non-test source"; the four it omitted held 26 of 59 `valid.ml` citations, and the
  sentinel walk could not see one file's three sentinels at all. What makes it this law's specimen and
  not the floors law's is the **defence the enumeration shipped with**: that a new file would fail the
  pinned range count and get read rather than swept in. The pinned count is summed *over the list*, so
  a file that is not on the list contributes zero citations, moves no pin, and arrives in total
  silence — the stated tripwire was the one thing that construction structurally cannot do. Not a
  floor that cannot see a missing member; a total computed **over the registry itself**, which can only
  ever agree with the registry. Everything downstream was consistent with a domain that came from a
  list, which is why nothing downstream could report it. Fixed by globbing the package and re-pinning
  both counts against a read of every newly covered range (9 → 27 ranges, keyed/residue 2/1 → 5/8).
  (Ruling: Scott, PR #335 relay; grave #333.)

  **Second instance, one file over, found while writing the paragraph above** (grave #336):
  `TestUnknownCategoriesMatchTheReference` checked the package's `unknown <category>` sentinels
  against the reference's ten `lookup` categories in both directions, with the forward direction
  iterating **a literal list of nine sentinel values** and the reverse pinning `wantUnclaimed =
  {"tag"}` under a comment promising that a later slice adding `tag` "has to come here and say so".
  The export slice declared `ErrUnknownTag` and said nothing, and the file stayed green: a sentinel
  absent from the literal is never *claimed*, so `tag` went on matching the out-of-scope arm and a
  scope declaration went on describing a rule that now existed. The promised mechanism needed somebody
  to extend an enumeration two directions away from the code they were writing.
  Two things make this worth recording beside #333 rather than as a repeat. The derived form of the
  same trigger was **directly below it in the same file** — the format check reads `ErrUnknown*` out
  of the AST — so proximity to the right pattern is not a defence. And the enumeration's failure mode
  was *inverted* relative to #333's: there a missing member contributed nothing to a total, here a
  missing member silently satisfied the opposite arm of a two-direction check, so the reverse
  direction that existed to catch exactly this was decoration for as long as the forward domain was a
  list. Fixed by deriving the forward domain from source, pinning its count, and draining
  `wantUnclaimed` to empty — which is now a *board figure*, every category the reference has being
  claimed.

### A guard's trigger predicate is itself a claim about the space, and an under-matching one fails silently by construction.

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

  **Third specimen, and this one is about a *sweep's* trigger rather than a control's**
  (finding 3 of the PR #281 review, filed here on Scott's ruling): the sweep
  obligation was "where else does this repo cite a theorem, a bound, or an asymptotic
  claim as a reason?", and the first pass ran it with `git grep`. `git grep` searches
  **tracked files only**. The two files carrying the defect were new and unstaged, so
  the sweep's population **excluded exactly the region the grave came from**, and it
  returned a clean result. Re-run over the working tree: 20 sites, all triaged, no
  second instance — the same answer, reached honestly. The tell generalizes past this
  tool: **a search command's default domain is a claim about the space, made silently
  by the tool rather than by the author.** `git grep` says tracked, `go test ./pkg`
  says one package, a bare `grep` says one directory, `gh search` says quoted phrases
  are optional. None of them announce the restriction and all of them return a
  confident empty set, which is why a sweep states its domain and why a sweep that
  found nothing has to say *over what*. (Ruling: Scott, on the PR #281 relay.)

  **The shorthand this specimen was filed under has since been minted as its own key**
  — *coverage is a claim: an instrument's domain is an assertion it cannot check about
  itself* (`evidence-and-instruments.md`), on the PR #285 relay. The parenthetical here
  used to record the shorthand as "the body's own phrase above", which was true when
  written and false the moment the phrase became a heading elsewhere; it is removed
  rather than left to read as a pointer at this law. The division is deliberate and both
  keys are load-bearing: **this** law is about a *predicate* that under-matches its
  population, where the remedy is to measure the trigger's coverage against the
  population it claims; the new one is the general form over any instrument's domain,
  including the ones with no predicate at all — a registry, a corpus directory, a default
  search scope. (Ruling: Scott, PR #285 relay — "the index ceiling is real and one idea
  shouldn't buy two keys", so the two keys are two ideas or this one is wrong.)

  **Fourth specimen — the *over*-matching direction, and it fired twice on one guard in one
  PR.** `citecheck.sh`'s check 4 ("no citation names the artifact it is written in") was drafted
  over a diff and failed on the *correct* prose of the PR adding it, because a code comment
  citing its own PR is this repo's attribution convention; narrowed to the body, it failed
  again, on a fenced block quoting `ratio.sh`'s output where the printed line **is** a commit's
  `Ratio-Class: ordered — #339` trailer. Both boundaries were found the way this law says an
  over-match is found: it fails loudly, on correct content, and each time the population turned
  out to be narrower than the sentence describing it. What is new here is the **remedy
  asymmetry**, and it is why an over-match is the more dangerous direction for a guard whose
  subject is a document: an under-match tempts nobody, while an over-match makes *editing the
  correct artifact* the cheapest way to green — here, deleting a token from a verbatim quotation,
  which is fabricated evidence to satisfy a prose check and a strictly worse defect than the one
  being gated. The rule that falls out: **when a guard fires on content you would have to falsify
  to satisfy it, the guard's population is wrong, not the content.** The narrowing then owes two
  things back, both taken — an odd-fence *failure*, since an unbalanced fence would swallow the
  rest of the body into the excluded region and reintroduce this law's own silent under-match, and
  the prose line count printed beside the verdict, so a population that collapsed to zero cannot
  report a pass. (Both boundaries measured on PR #339's own body; the second on its CI red.)
