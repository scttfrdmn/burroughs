# 0023 — `drop` tags each stack slot with a lazily-activated push sequence number

Date: 2026-08-10 · Status: **accepted** — stamped by Scott, contingent on two additions both now
delivered: the gated-u8 quadrant (measured, with its variance stated honestly rather than
smoothed over — see the measurement table's own caveat) and a deferral pointer for #9 (see "What
this decision is *for*, and what retires it").

> **That deferral pointer's condition inherits the shape [0043](0043-g-1s-carve-out-retires-on-zero-call-sites-not-on-the-validator-umbrellas-closure.md)
> names.** "When #9 lands, this ADR's own mechanism is the thing to delete" is a tracker event;
> what actually retires `stack.tracking` is `drop`'s arm being able to read a validated operand
> type. The wording below is left as written — the pointer is the remedy, since rewriting it would
> amend an accepted record about a subject 0043 did not deliberate.

Filed against **grave #206** (found during #201's rung 2c falsification, PR #207), on Scott's own
ruling: a decision that "amends 0002's core value model and has genuinely competing shapes,
decided the way 0002 itself was decided — `make bench` numbers as the ADR's evidence, hot-path
cost measured, not argued." `internal/interp/dropbench` is the measurement instrument this
decision cites; its own doc comment and correctness controls (`TestAllAgree*`) are load-bearing
parts of this record, not scaffolding discarded after use.

## Question

`drop` (opcode `0x1a`) pops whichever of `stack.num`/`stack.refs` holds the logical top-of-stack
value. Its wire form carries no immediate at all (`ast.ml:172`, bare `Drop`), and the reference's
own validation rule accepts any operand type unconditionally (`valid.ml:432-433`,
`[peek 0 s] --> [], []`) — so with no validator (#9) to consult statically, the engine has no
signal today for which array to pop from, and its current code (`st.popNum()` unconditionally)
silently corrupts the stack whenever the top is actually a reference. Confirmed with a
three-instruction reproducer carrying no exception-handling machinery at all:
`(ref.null func) (drop) (i32.const 7)` returns garbage instead of `7`. Decide what signal `drop`
consults, and at what cost to every other push/pop in the interpreter's hot path.

## What was tried and rejected before any code was written

A flat push-order log (`order []bool` on `stack`, appended at every push, popped at every pop) was
the first candidate and was falsified by inspection before being built: `branch`/`returnFrom`
(control.go) don't pop one slot at a time on a label exit — they truncate each array
**independently by position** (`st.num = st.num[:l.height+l.arity]`, likewise for `refs`), keeping
an arity-sized window from each array's own top and discarding everything else *in that array*,
regardless of when it was pushed relative to the other array's contents. A concrete case: push
num `a`, ref `b`, num `c`; branch with arity (numeric=1, ref=0) keeps `c` and discards `b`
entirely. The *logical* stack after that branch has one live value, a numeric — but a flat log
recorded three entries in push order and has no way to know that entry 2 was independently
discarded without also being told which physical array-position survived. Removing "the last N
entries" from the log does not match what the branch actually kept, because the branch's
discipline is per-array position, not global recency, and this construct is exactly where the two
diverge. Fixing the log to track position would need the same per-slot information a direct tag
carries — the log doesn't avoid needing it, it duplicates it one layer removed.

## What was measured

> **Protocol note, added by [grave #612](https://github.com/scttfrdmn/burroughs/issues/612). The
> figures below are left as written; this note is the pointer.** The ruling quoted above — *"decided
> the way 0002 itself was decided — `make bench` numbers as the ADR's evidence"* — names the
> discipline this record used, and that target could not express a two-arm A/B: it wrote a hardcoded
> output file, printed its comparison as a suggestion nothing ran, and summarised one file, so the
> arms here are benchmark rows in one binary run consecutively rather than interleaved. Grave
> [#552](https://github.com/scttfrdmn/burroughs/issues/552)'s protocol is what controls for that, and
> it is now executable as `make ab`. **The decision survives on effect size** — +27.5% to +75.1%
> dwarfs the 4.1–9.1% same-code drift #552 measured on this hardware, so *a per-slot tag is expensive
> and gating cuts it to about 28%* holds. **One comparison inside it does not:** *"the zero-reference
> case costs the same as the mixed case"* rests on +71.9–74.1% against +71.9–75.1%, two overlapping
> ranges from sequential arms, and is not resolvable from what was recorded. Whether it is
> re-measured is Scott's call and is not decided here.

`internal/interp/dropbench` (n=10, `benchstat`, two independent runs each — see its own package
doc comment for the full access-pattern rationale) compares:

- **Base** — today's shape, the bug as shipped, no tracking.
- **Seq64** — every push (`pushNum`/`pushRef`) appends a `uint64` sequence number to a parallel
  array; `branch`'s existing copy+reslice is extended one line to carry the tag along; `drop`
  reads whichever array's top sequence number is higher.
- **Seq8** — identical mechanism, `uint8` sequence numbers (deliberately wraps every 256 pushes —
  measured for its cost, not proposed as sound on its own).
- **Gated** — `Seq64`'s mechanism, but inert (`tracking = false`, no array even allocated) until
  the first `pushRef`, mirroring `frame`'s own lazy `refs`/`isRef` allocation (`newFrame`,
  value.go). Activation backfills sequence numbers for every already-pushed numeric slot.

| comparison | result (two independent n=10 runs) |
|---|---|
| Seq64 vs Base, mixed numeric+ref workload | **+71.9–74.1%** |
| Seq8 vs Base, mixed workload | **+37.5–38.6%** |
| Seq64 vs Base, **zero** references ever pushed | **+71.9–75.1%** |
| Gated (u64) vs Base, zero references ever pushed | **+27.5–28.8%** |
| Gated (u64) vs Base, mixed workload | **+73.2–73.5%** |
| Gated (u8) vs Base, zero references ever pushed | **+25.5–38.2%** (elevated variance — see below) |

Every comparison is `p=0.000` at `n=10` for the first five rows, both times measured — not a
single run (decision 0005's own rule). **The gated-u8 row is the odd one out and is reported with
the caveat this project's own rules require rather than smoothed over**: measured under machine
load this project's tooling has no control over (a persistently high, multi-user, non-transient
load average during measurement), it needed three independent runs before a stable neighbourhood
emerged (`+38.24%`, `+36.38%`, `+25.50%`, each itself `p≤0.037` at `n=15`), against
gated-u64-no-refs' own re-measurement under the identical noisy conditions landing in the same
`+25–45%` neighbourhood rather than repeating its earlier clean `+27–29%`. The two candidates are
**statistically indistinguishable from each other** under the conditions available to measure
them, which is itself the finding: unlike the *always-on* comparison (where u8 clearly and
repeatably halves u64's cost, tight variance, both runs agreeing), gating appears to erase most of
the gap width alone would otherwise buy — plausible on the mechanism (once gated, the array is
short-lived and small for the dominant no-ref case, so the bytes-per-slot saved by narrowing
matter less than they do against Seq64/Seq8's always-tracking, always-growing array), but this ADR
states that as a plausible reading of noisy data, not a measured result at the confidence its
other rows carry. Four findings, in the order they change the shape of the decision:

1. **The cost is not about references at all.** Seq64 costs the same (~72-75%) whether or not a
   reference is ever pushed in the run. The regression comes from the extra `append`/reslice pair
   on the **numeric** array's own operations — every `pushNum`/`popNum`/`branch` pays it, which is
   the overwhelming majority of all stack traffic (0 of the numeric core's 13671-vector corpus
   needs a reference at all, exec.go's own header). This rules out "most functions never touch a
   reference, so the average cost is low" as a reason to skip gating: the ungated cost is paid in
   full by every function, reference or not.
2. **Width matters, roughly in half — the u8 speculation this ADR's own benchmark comment first
   guessed at was wrong, and the correction is on the record rather than quietly fixed.** Seq8 at
   +38% against Seq64's +72% shows narrowing the tag genuinely buys back real cost, not a
   rounding error — so a u8-or-narrower design is worth pursuing *if* its wraparound can be made
   sound (a per-call-frame generation reset is the obvious candidate, not benchmarked here).
3. **Gating recovers most of the cost for the population that matters, and none for the
   population that doesn't.** A lazy activation check brings the zero-reference cost from ~72%
   down to ~28% — a real, large win for the common case. But once a function pushes even one
   reference, tracking activates and never turns back off for the rest of that function's
   execution, so the mixed-workload cost stays at ~73%, statistically indistinguishable from the
   always-on variants. Gating is a genuine improvement bounded exactly by exec.go's own measured
   population, not a general fix.
4. **Once gated, width stops being the clear lever it was when always-on.** The always-on
   comparison (finding 2) shows u8 reliably halving u64's cost — tight variance, reproduced twice.
   The *gated* comparison shows gated-u8 landing in the same noisy `+25–38%` neighbourhood as
   gated-u64's own re-measurement under identical conditions, indistinguishable from it at the
   confidence available. This is a plausible, not a proven, reading (see the caveat on the
   measurement table above) — but it changes what a future u8 proposal has to argue: not "u8 saves
   half of the *original* regression," but "u8 saves some fraction of what gating has *already*
   reduced to ~28%," a much smaller number to be fighting over, on top of a wraparound-soundness
   design that does not exist yet.

## Decision

**Ship the gated design (Candidate C: lazy activation, `uint64` sequence numbers) as the
immediate fix, and file the u8-narrowing question as its own future measurement rather than
bundling it here.**

- **Gated over ungated**: the ~72%-vs-~28% gap for the zero-reference population is too large to
  leave on the table, and that population is the overwhelming majority of real function bodies
  per exec.go's own measured corpus split. The mixed-workload population pays the full ~73% cost
  either way — gating costs nothing extra to add for that population and helps every function
  that doesn't need it.
- **u64 over u8, for this ADR, on two grounds now rather than one**: u8's wraparound-soundness
  question (a per-call-frame generation counter, or some other correctness patch) is real design
  work this ADR has not done — and finding 4 means the number that design work would be fighting
  over is not even clearly there: gated-u8 measured statistically indistinguishable from
  gated-u64 under the conditions available, where the always-on comparison showed a clear,
  reproduced, tight-variance halving. Shipping a *known-unsound* narrowing to chase a saving this
  ADR could not measure cleanly is not warranted. u64 is unconditionally sound. If a future
  measurement — taken under quieter conditions than this ADR had available, so it can actually
  resolve the question at the confidence its other findings carry — shows gated-u8 with
  a sound wraparound story buys back enough to matter, that is a follow-up decision with its own
  bench numbers — not a reason to hold this one.
- **`drop`'s own arm**: reads `numSeq[len(numSeq)-1]` vs `refSeq[len(refSeq)-1]` (absent-as-`-1`),
  pops whichever is larger. `stack.tracking` (or equivalent) gates whether `pushNum`/`popNum`
  touch `numSeq` at all; `pushRef` backfills `numSeq` for every already-pushed numeric slot on
  first activation, exactly as `dropbench`'s `stackGated.pushRef` does and
  `TestAllAgreeGated`/`TestDropPicksTheCorrectArray` confirm correct.
- **`branch`/`returnFrom`/`catchThrown`** (control.go) each gain one more `if tracking` guarded
  copy+reslice pair for `numSeq`, parallel to the existing `num`/`refs` handling — no new
  algorithm, the same truncation-by-position logic extended one field.

## Options considered

- **A — flat push-order log.** Rejected before implementation; see "What was tried and rejected"
  above. Falsified by inspection against `branch`'s own documented truncation semantics, never
  built.
- **B — always-on per-slot sequence numbers (u64 or u8), no gating.** Rejected: measured at
  +72-75% (u64) or +38% (u8) regardless of whether the function ever uses a reference, which pays
  the full cost for the 0-reference population that dominates the corpus for no benefit to that
  population.
- **C — lazily-gated per-slot sequence numbers, u64 (chosen).** See Decision.
- **D — lazily-gated, u8, with a wraparound fix.** Not rejected, deferred: the wraparound-soundness
  design does not exist yet, and this ADR's own measured numbers (u8 buys back roughly half the
  always-on regression) are the evidence a future proposal would need to argue it is worth
  designing. Named here so the next reader finds the pointer rather than re-deriving the question.
- **E — defer to #9 (the validator).** Considered and rejected on timing: #9 does not exist and
  has no scheduled date, and grave #206 is a live accept-direction defect today — leaving it
  unfixed until an unscheduled future milestone is not the same posture as this project's own
  declared-and-tracked deferrals, which need a tripwire and a scope, not an open-ended wait on a
  different phase's ladder rung.

## What this does not decide

- **The u8/generation-counter narrowing** (Option D) — a real, measured-as-promising direction,
  not designed here.
- **Whether `select`'s own `Imm0`-bit dispatch (0196/#197) should be revisited in light of this
  mechanism** — `select` already has a static signal (the decoder-staged bit) and does not need
  `drop`'s dynamic one; this ADR does not touch it and does not claim it should be.
- **Any change to 0002's own switch-dispatch choice** — this widens the value stack's per-slot
  bookkeeping, not the dispatch loop's shape; `dispatchbench`'s own findings are unaffected.

## What this decision is *for*, and what retires it

**This is the right design for an untyped v0 interpreter, not a permanent piece of
architecture.** The whole reason `drop` needs a runtime signal at all is that nothing today knows
`drop`'s operand type statically — there is no validator (#9), so the engine cannot ask "what is
the declared type of the value at this point in this body" and instead has to ask the stack itself
at the moment `drop` runs. Once #9 exists, that question has a cheap, static answer: `drop`'s
operand type is known at validation time from the body's own type-checking pass, the same way
every other instruction's operand types already are. At that point the correct fix is not "keep
the runtime tag, since it works" — it is to **retire the runtime tracking entirely** and let
`drop` branch on a fact the validator already computed, the same shape `blockArity`/`countByArray`
already read a *retained* type fact rather than infer one dynamically. The zero-reference
population's ~28% tax (Consequences, below) disappears at that point, not merely shrinks: gating
buys back the common case today because most functions never push a reference, but a validated
`drop` costs nothing to check *regardless* of whether references are involved, because the answer
was already computed once, at validation time, for every instruction in the body.

**This is a pre-written consequence for #9's own author, not a promise this ADR can keep on its
own — #9 does not exist and has no scheduled date**, so nothing here commits to a timeline. What
it commits to is the shape of the retirement: when #9 lands, this ADR's own mechanism
(`stack.tracking`/`numSeq`/`refSeq`) is the thing to delete, and `drop`'s arm becomes a plain
read of the validated operand type — stated here so that the next author who lands #9 finds the
consequence already written rather than having to re-derive that this tracking was always meant
to be temporary.

## Consequences

- **`stack` gains `numSeq []uint64`, `refSeq []uint64`, `next uint64`, `tracking bool`** —
  allocated lazily (nil/zero until the first reference is pushed in a given call), mirroring
  `frame`'s own established lazy-allocation shape for exactly the same reason.
- **Every one of `control.go`'s three stack-truncation sites** (`branch`, `returnFrom`,
  `catchThrown`) gains a few lines extending the existing copy+reslice pattern — no new
  algorithm, and each site's own falsifiable tests (branch's, returnFrom's, and #201's own
  catch-mechanism controls) need a mixed-numeric-and-reference row added if one does not already
  exist, to exercise the sequence-number carry-through under real truncation.
- **A function that never touches a reference pays ~28% more on its stack operations than today**
  — real, and the tradeoff this ADR makes deliberately in exchange for `drop` computing a correct
  answer rather than a plausible-looking wrong one. Contract §0's correctness-neutral,
  performance-partisan posture is not violated by this tradeoff (it is not a partisanship
  question — G-3's accept-direction discipline requires a correct answer here), but the cost is
  real and is why this ADR exists rather than a one-line patch.
- **`internal/interp/dropbench` stays in the tree** as the measurement's own reproducer, on
  `dispatchbench`'s precedent: the next author changing this mechanism has a benchmark already
  built to re-run rather than a number to trust from a stale comment.
