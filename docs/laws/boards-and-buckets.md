<!-- Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0 -->

# Laws — Boards and buckets

Reading the board as a work plan, and what a third verdict costs.

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

### A third verdict needs a structural bound, not just a watched one.

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

### A column draining to zero is not the engine reaching a milestone.

- **A column draining to zero is not the engine reaching a milestone — say which of the two
  it is at the point the zero will be read.** A board column measures what the harness
  *asked*, and a row can sit in `unsupported` for either of two unrelated reasons: the engine
  cannot answer, or the harness cannot pose the question. Those look identical in the column
  and identical in the delta, and the second is the one that flatters. The specimen is v0's
  own residue, measured on #459 under
  [the runtime-vs-harness test](product-and-overhead.md#the-phases-product-is-the-work-instruments-are-overhead-on-it)
  (`internal/spec/spec_test.go`'s `unsupportedCeiling` ledger carries the full account):
  **all 14 remaining rows were harness debt.** Three were a lexical transparency
  the s-expression reader lacked, over modules the engine had been decoding, validating and
  instantiating correctly the whole time; eleven wanted a public read path for a global
  export, not a global. So `unsupported → 0` for v0 will mean the harness can finally ask
  every question the corpus writes, and will mean nothing whatever about capability gained
  in that slice.
  - **The general form is guidance and belongs here; the dated claim about this project does
    not.** *"v0's remaining `unsupported` column is entirely harness debt"* is a position
    statement about where Burroughs stands at one moment, not a rule for reading boards, and
    a law page is the wrong home for it because reaching it requires already knowing the law
    exists and thinking to apply it. It is stated where the closure set is defined instead —
    the five-verdict list in [`README.md`](../../README.md#conformance), beside the column
    it is about and beside the `unimplemented` bullet that names the other v0 closure
    condition. (Ruling: Scott, PR #460, correcting a first attempt that filed the position
    statement here and in the ceiling's ledger only.)
  - **The consequence for reporting is that the column's own comment carries the
    classification, not just the count.** A ledger entry that records `14 → 11` and the
    mechanism is still readable, two years on, as three capabilities landing. The reader who
    needs the distinction is the one arriving at the zero, which is why it is written where
    the zero lives rather than in the PR that produced it — *a report is read once and a
    bound is read every time it moves*.
  - **And it cuts the other way for the pass floor.** `passFloor` rising cannot distinguish
    "the engine started answering" from "the harness started asking", so a slice that moves
    it by widening vocabulary says so in the floor's entry too. On #459 the +3 was
    entirely the latter, and what made that assertable rather than assumed was a probe
    (`TestAnnotatedModulesInstantiate`) — because the board *structurally cannot* see an
    instantiation decline on a module with no dependent commands (#124's pass-on-decline
    ruling), so the rows would have read `pass` with instantiation wholly broken. *A row
    that passes without asking is the same shape as a skip*, one section down.
  (Order: Scott, on the #459 measurement — *"record the measurement as a finding in the repo,
  not only as an issue comment. 'v0's remaining unsupported column is entirely harness debt'
  changes what closing that column means. Nobody reading `unsupported → 0` later should take
  it for an engine milestone."*)

### A skip is not a verdict.

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

### Bucketed failures are the work plan — while there are buckets.

- **Bucketed failures are the work plan — while there are buckets.** A suite Board line
  reports pass / fail / unsupported, with failures bucketed by the missing feature (for
  the decoder, by expected spec error string). The biggest bucket is the next issue to
  take; a bucket going to zero is a PR's measure of done. Failures are reported, never
  skipped — skipping hides the queue. **At zero fail this rule has no subject and the
  fallback is not "whatever is available"** — see *a zero-fail board is not a green
  light, it is a lost instrument* above: the plan becomes the largest unsupported
  stratum and the artifact it names.
  - **And it holds per stratum, so the sentence to keep is: *a zero there is a lost
    instrument, not a target*.** The whole-board rule is easy to read as being about the
    total, which lets a *stratum* ceiling drain to zero while the board still has buckets
    elsewhere and nothing registers what was lost. It is the same inversion at smaller
    scale: a stratum's bound is only able to catch a regression while it has a distance
    to its subject, so the last member leaving is simultaneously the work's success and
    the instrument's death. Say it in the ceiling's own comment at the point it becomes
    reachable rather than after — `encodeFailCeiling`'s entry says it at 3, with the three
    named, which is one slice before it can happen. **A ceiling approaching zero is
    therefore a scheduling signal and not a finish line**: what the next slice needs is
    the bound's replacement named, not the bound congratulated. (Ruling: Scott, PR #424 —
    *"worth keeping verbatim wherever ceilings get read"*.)
  - **A bucket names where a symptom surfaces, not where the defect lives — so a slice's first
    act is locating the defect, not fixing the named one.** This is the qualifier the rule above
    needs rather than a separate rule, because without it the rule invites exactly one mislabel:
    an issue filed from a bucket reads as precise *either way*, since the bucket key is a real
    measured string whether or not it names the cause. #194 is the specimen it is named for — a
    bucket that read as an interpreter gap and was an element-expression evaluator with no
    `global.get` arm. Selecting work from the board stays right; what the qualifier forbids is
    treating the bucket key as a diagnosis. Two consequences worth spelling out, because both
    have since been paid for:
    - **A control's remedy text is subject to the same rule.** `TestGatedVectors` printed
      *"declined by a feature gate but is not in the allowed set; if the gate is right, add it
      with the feature named"* over two vectors whose actual defect was its own bulk-arm
      membership predicate narrowing when a Kind split (grave #330). Following the remedy would
      have added two allowlist entries and left the predicate broken. A control can be right that
      something is wrong and wrong about what — *an error message is testimony*, and a remedy
      naming the wrong layer is a lying witness even when the verdict is right.
    - **The relocation is reported, not silently absorbed.** When the defect turns out to sit
      somewhere other than the bucket's name, the PR says so and the issue title is corrected —
      otherwise the tracker accumulates precise-sounding titles that send the next reader to the
      wrong layer, which is the cost #194 actually carried.
    - **An issue's stated *plan* is subject to the same rule as its key and its title, and it is
      the expensive one.** A title sends one reader to the wrong layer; a plan sends the *work*
      there. #394 was filed to converge an operand-mismatch message "arm by arm, board re-measured
      after each" across the eight landed slices, because eight arms is where the divergence
      **appears**. It lives in two `fmt.Errorf`s in `popExpect` (`stack.go:211,214`), reached by
      **62 call sites across seven files**, so the prescribed ordering was not merely wasteful but
      impossible: converging one arm's wording means duplicating the helper or moving that arm off
      it, and both are worse than the divergence. The plan was written from the symptom's location
      by an author who had read the arms and not the helper. **What catches it is doing the census
      before the commits** — the issue's own step 1, which is why an order of work that begins with
      a measurement is worth more than one that begins with a schedule. Scott's classification on
      the #395 relay: the third instance, after #194's title and `check_elem`'s file-as-rule-owner
      proxy, where *a vector's file is not its stratum* had the same shape one layer down. (A fourth
      followed on PR #409 — the *mis-attributed bucket built out of true sentences* bullet below,
      named rather than pointed at positionally, since a fifth instance has since been filed between
      this sentence and it.)
      - **And a fifth on #459, where the plan named the wrong *layer* rather than the wrong
        ordering — and the authority is what said so.** #320's three no-head-atom rows came with a
        remedy in the body: *teaching classify to skip annotation nodes when it looks for the head*.
        That is where the symptom is visible — `head()` returns `""`, so `classify` falls through —
        and it is not where the mechanism is. The reference's lexer records an annotation into a side
        table and tail-calls `token lexbuf` (`lexer.mll:821-828`), emitting **no token**, three rules
        above the `;;` and `(;` cases that do the same: an annotation is transparent to the grammar
        *wherever a token may appear*. So the plan would have bought one head-finder a skip and left
        the same package's **six positional reads** — `len(n.list) == 3` and `n.list[1]`/`n.list[2]`
        in the `assert_malformed`, `register` and action arms — each needing their own, which is six
        chances to miss one against zero. The fix went into the s-expression reader as one predicate.
        What caught it was **reading the authority before the plan**, which is this family's census
        rule pointed at a grammar instead of at a population: the harness's oracle is the reference,
        so a question about where a lexical rule belongs has an answer that can be looked up rather
        than designed. Worth its own instance because the previous four were all wrong about *scope*
        — which rows, which stratum, which ordering — and this one was wrong about *layer*, and the
        remedy that finds it is different.
    - **A mis-attributed bucket can be built entirely out of true sentences, and that is what makes
      it invisible.** The registry defect (#366, ADR 0037) put 66 rows in the exec stratum — the one
      column whose fails are supposed to mean *the interpreter answered wrongly* — where the actual
      cause was the harness losing a gate decline. Every one of those rows carried
      `interp: link failed: unknown import`, which is **correct about the resolver**: after a declined
      `register` the name really was unbound, so the engine really could not resolve it. Nothing on
      the board was false. The board was **answering a different question than the one being read off
      it**, and no amount of scrutiny applied to a row finds that, because the row is right. What
      finds it is asking what a bucket's rows have in common besides their message — here, that all
      66 vanished rather than shrank in the all-gates-on lane, which no single row could have said.
      - **The consumer of a mis-attributed bucket can be a principal, and the rule does not stop at
        the tracker.** Scott's assignment — *"exec's 81, then encode's 68 — exec first because it's
        the interpreter getting answers wrong, which is the most product-shaped defect on the
        board"* — was a work order derived from a bucket key, and 66 of the 81 were not the
        interpreter. He withdrew the premise himself on the PR #409 review (*"That's your own lesson
        landing on the person who issued the order: a mis-attributed bucket recommends the wrong
        work, and I recommended from it"*). So the qualifier's audience is not only the agent filing
        an issue: **an order sourced from a bucket inherits the bucket's attribution**, and the
        instrument-shaped response to one is to re-measure the population before working it, then
        report the discrepancy rather than quietly serving the purpose behind the words.
      - Fourth instance by this ledger's own count, after #194's title, `check_elem`'s
        file-as-rule-owner proxy, and #394's plan. Scott called it the third, counting the two he
        had named before; the difference is #394's, recorded in the bullet above, and it is noted
        here rather than reconciled silently because *a ledger that counts instances is wrong the
        moment its count is*.
    (Ruling: Scott, on the 17-head slice's relay: *"Write that into the law when it lands —
    without it the rule invites exactly the mislabel #194 carried."* Third bullet on the PR #397
    review: *"#394's plan was written from where the divergence appears — eight arms — rather than
    where it lives, two lines in `popExpect`. Doing the census before the commits is what keeps
    catching it."*)

### Bucket size estimates the reward, not the job.

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
  - **And the estimate errs in *both* directions, so the census rule is symmetric: a bucket named
    after a missing opcode understates an arm whose absence corrupts state that later vectors
    read.** The over-promise half above (18 quoted, 4 reachable) had its mirror measured on the
    bulk trio: three `no arm for opcode` buckets held **82** vectors and the arms moved **411**.
    The other 329 sat in `assert_return value mismatch` and `trap: uninitialized element N`,
    because `memory_copy.wast` and `table_copy.wast` are generated with up to forty read-backs per
    instruction, and a read-back's expected string names a *value* — so the board cannot key it to
    the opcode that will answer it. Under- and over-promise are now both paid for with data, and
    the remedy is the same one in both directions: partition by mechanism, then quote the
    measurement rather than the bucket. A corollary worth stating because it is where the asymmetry
    lives — an arm that only *computes* moves its own bucket, while an arm that **writes state**
    moves every bucket downstream of that state, so the multiplier is predictable in kind if never
    in size. (Ruling: Scott, PR #155.)
    - **And for an `init`, *where* the blocker sits inside the write sequence is the entire
      forecast, because an init is a sequence of independent writes and not one construct.** The
      state-writing half above says an arm that writes moves everything downstream of the state;
      this is the same fact read from the other end, and it is what makes an arm's payoff
      *predictable* rather than merely large-in-kind. A `(func $init)` full of `table.set`s is N
      independent facts about N slots, so a missing arm at write *k* leaves slots `k…N` at their
      previous contents while `0…k-1` are correct — and every later read of a slot below `k`
      already passes. Rung 5 slice 2's specimen, **re-measured per vector after the first
      reading of it was wrong** (see the compensating-errors clause below): the forecast booked
      **8** of the 44 cast vectors to slice 3 as slot-4 readers, and **2** actually stayed
      failing — `br_on_cast.wast:99` and `br_on_cast_fail.wast:99`, the `null-diff` invocations,
      re-keyed from `no arm` to `assert_return value mismatch`. `init`'s last write is
      `any.convert_extern` (slice **3**'s arm), so slot 4 stays `ref.null any`. The six that
      paid early — lines **81/87/93** in each file — **pass by coincidence, and the coincidence
      is the finding**: a null operand correctly fails the *non-nullable* `(ref i31)`/`(ref
      struct)`/`(ref array)` target, so the function returns the expected `-1` for the same
      reason a wrongly-typed operand would. They attest that the arms handle null, not that they
      handle slot 4. Line **75** (`br_on_null` at index 4) failed before and after, so it is
      neither departure nor arrival — a fifth slot-4 reader the forecast did not count, which is
      how the "four index-4 reads" figure came to be wrong. Read the init, find the first write
      the arm does not supply, and forecast from *that index onward*; a bucket count over the
      whole file assumes the sequence is a unit, which is the assumption an init is built to
      violate. And when an over-delivery is coincidental, say so: six vectors that pass for a
      reason unrelated to the capability under test are not six vectors of evidence.
      Note the miss was **optimistic** here and **pessimistic** in the previous entry, so the
      probe's error is unsigned variance rather than a bias to correct for — which is why the
      remedy is reading the sequence, not shading the estimate.
  - **And there is a third outcome that is neither over- nor under-payment: a bucket whose members
    share a *deeper* blocker **re-keys rather than pays**.** The taxonomy is now complete, one
    measured specimen each: an *embedded construct* overpays its bucket (export's 39-paid-55); a
    *sole blocker* pays par (control flow's 90.5%); a *shared deeper blocker* pays **zero** while
    the bucket still drains — #161's `ref.null` heaptype arm emptied a 609-vector bucket and moved
    no column in either lane, all 609 arriving in sibling keys one layer up (+446 parameterized
    reftype, +163 cast immediates, closure exact, 0 unclassified). The arm was correct and required;
    the bucket was **shadowing**, because `ref.null` is the first refusal a GC vector meets and its
    key was counting vectors that need three or four other things as well.
    - **Partitioning by mechanism cannot see this, which is why the remedy is a different
      instrument.** The partition is over the failures the board *reports*, and the second refusal
      in a chain is invisible until the first is gone — so the standing remedy for both earlier
      clauses is structurally blind to this one. **The co-blocking probe is therefore standard
      pre-selection: bucket size × sole-blocker fraction = expected pay, measured *before* the
      bucket is chosen.** The instrument has existed since the control-flow recon; what changes is
      that it runs **first**, which converts a re-key surprise into a re-key forecast. Cheapest
      sufficient version when the full probe is overkill: list the bucket's files and read one
      vector — `br_table.wast` 146, `ref_eq.wast` 82, `i31.wast` 61 would have said "GC files, and
      a `ref.null` is one token in a module that also declares `(ref null $t)` fields" before any
      code was written.
    - **A zero-delta PR is an account, not an alibi, and what makes it one is that the
      redistribution *sums*.** #161's body is the standard: every changed key itemized, departures
      and arrivals equal, residual forced to zero and stated, unclassified arrivals counted (and
      zero). An itemization that reaches its total by having the right number of terms is the defect
      #155's memarg batch was corrected for; a zero-delta claim without exhaustive closure is that
      defect with nothing to check it. Measure with the harness (`run(s).Buckets`), never a grep over
      the board log — bucket keys can contain embedded newlines, and a line-oriented sum split them
      into 1697 and 1672 against a true 1699 three times in one session, with a `join` artifact
      briefly reading as a real ±9 behavioural change. (Ruling: Scott, PR #161.)
      - **Summing is necessary and not sufficient: two errors of opposite sign are invisible to
        the total, so a census is closed on *vector identity*, never on counts.** The clause above
        says force the residual to zero and state it — rung 5 slice 2's census did exactly that,
        reconciled to the measured **−43**, and was wrong in three of its four terms. It reported
        departures as `fb 18` **29** / `fb 19` **15** (true: **22/22**), arrivals as **+3** (true:
        **+2**), and grave #261 as **−2** across `ref_test.wast` and `type-subtyping.wast` (true:
        **−1**, `ref_test.wast:329` alone — `type-subtyping.wast` never moved). −44+3−2 and
        −44+2−1 both equal −43, so the residual check the rule prescribes **passed on a fabricated
        decomposition**. That is the right-number-of-terms defect one level deeper: not a wrong
        count of terms but wrong *values* that cancel, and no arithmetic over the total can see it.
        The remedy is an identity check, which is the standing law pointed at a census: diff the
        **set of failing `file:line` pairs** between a baseline worktree and the tree, because
        `Failure.Line` is right there and two sets either match or name their difference. Counts
        can compensate; sets cannot. What makes this indictable rather than unlucky is that the
        sharper instrument was in hand while the looser one did the reporting — the floors rule's
        own finding, in the census's clothes — and the whole reconciliation took one throwaway
        probe over `r.Buckets[k]`. So: **a census that cannot name its vectors has not been taken**,
        and a forecast is reconciled against measurement, never against recollection of the
        forecast. (Found by Scott asking the census to reconcile against its own pre-registration,
        PR #263.)

### A magnitude is not a cost estimate.

- **A magnitude is not a cost estimate.** A population's *size* does not price the work over it; its
  **shape** does. A sentence of the form "N is large, therefore this is expensive" has not been
  measured — it has been felt, and the missing step is the decomposition that turns rows into
  mechanisms. This is the sibling of the rule above rather than a restatement: that one says a
  bucket's size mis-estimates its *job* because the key cuts across mechanism, and this one says the
  undecomposed figure is not evidence **in either direction**.
  - **The asymmetry is the reason it needs its own paragraph.** "Large, therefore expensive" argues
    for doing nothing, so no bill ever arrives to falsify it, and an estimate that cannot be wrong
    is not an estimate. Its inverse — **"cheap" is a grammar claim, as falsifiable as any claim
    about the spec** — is at least pre-registerable: say the number the cheap step will cost and the
    measurement either lands or does not. So the two errors are not symmetric in *discoverability*,
    which is why the expensive direction is the one that gets the law.
  - **Specimen: #455's substring-award census** (Scott's ruling on the #486 review — *"the shape of
    a population decides the work, not its size"*). The issue posed a three-way choice whose
    selector was a magnitude, in its own words: near zero makes option 1 cheap; large makes option 1
    an engine-wide rewrite priced against nothing and option 3 the honest record. The probe returned
    **6542** substring-only awards, unambiguously large — and the inference still failed. Collapsing
    index digits to `N` and parenthesized opcode names to `(op)` showed the 6542 rows arriving from
    **three rendering sites** covering 98.5% of them. Large *and* cheap, so the two halves of
    "large ⇒ rewrite" came apart, and a **fourth** option the three-way question could not contain
    became visible in the same table. Scott took it.
  - **The excuse for skipping the decomposition is what the specimen removes.** Neither the estimate
    nor its refutation needed the engine touched: one regex pair over data the census had *already
    collected* repriced the repair from roughly two hundred string edits to three sites. A
    decomposition that costs one pass over a table already in hand is not expensive relative to the
    estimate it corrects, so "we would have to measure it" is not available as a reason.
  - **The count-side statement, carried here rather than cited, because no file in this corpus held
    it.** A count is not a price: **decompose by mechanism** before quoting a figure as work. 6542
    rows across three renderings is the same measurement read forwards, and an issue's "if large
    then expensive" premise can fail at *both* ends — the population large, the repair small. Both
    this and the grammar-claim clause above lived only in the agent's session memory, which no
    instrument in this tree can reach (`CLAUDE.md`'s stated limitation), so a `docs/laws/` reader
    following a pointer to either found nothing. Written out here for that reason: a law cited from
    a place the reader cannot follow is a citation with no target.
