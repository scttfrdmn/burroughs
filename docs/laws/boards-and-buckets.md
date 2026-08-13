<!-- Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0 -->

# Laws — Boards and buckets

Reading the board as a work plan, and what a third verdict costs.

Relocated from `CLAUDE.md`'s `## Disciplines` section, **verbatim**, when that file
became an index (see the restructure PR). Each law's one-line compressed form remains in
`CLAUDE.md` as its recall key and points here for the specimen, the minting record, and the
token it was granted on. Nothing was rewritten in the move: the bodies below are the text as
it stood, which is why superseded wordings still appear inside them where a later ruling
amended rather than replaced.

`CLAUDE.md`'s recall key and each heading here are checked equal by
`TestEveryLawIsIndexed` (`internal/testenv`), so the two cannot drift.

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
