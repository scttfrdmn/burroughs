# 0034 — #9's reference-instruction slice closes as slice 8, and its criterion is fifty-five rows bounded by two bottoms

Date: 2026-08-17 · Status: **accepted on one clause** — Scott, [the #387 review
relay](https://github.com/scttfrdmn/burroughs/pull/387#issuecomment-5323241867)

> **What is binding is one sentence, and the ruling says so:** *"0034: accept it on that clause. The
> two bottoms stay two values, nothing downstream collapses the non-nullable reference bottom into
> the valtype bottom. That's a real invariant, it came out of the peekRef measurement rather than
> from argument, and it binds. The rest being retrospective is fine — Status can go to accepted with
> the clause named as what's binding."*
>
> So: **nothing downstream may collapse `RefT (NoNull, BotHT)` into `BotT`** — a constraint on #9's
> remaining slices and on `internal/interp`, enforced today by the representation (`botRef(false)` is
> a distinct value from `unknown`), by `matchHeap`'s index-keyed bottom arm, and by `typeStr`'s two
> spellings. Everything else here is **retrospective**: the 55-row criterion, the forecast, the
> falsification bill and the decline census describe what slice 8 did and bind nothing.
>
> **This field took two wrong values before it took this one, and the sequence is the lesson.** It
> was first flipped to accepted citing *"Slice 8: go, self-merge on green"* (the #385 relay), which
> Scott reverted: *"'go, self-merge on green' authorized the slice and not a criterion I never
> read."* The mechanics were right and the reasoning was not — a relay comment is a real approval
> artifact with a real URL, so the citation *resolved*; it just pointed at an approval of something
> else. **A citation that resolves is not thereby a citation to the right thing**, the same defect as
> a drifted reference range, one level up in the governance stack. It then stood at `proposed` with
> the clause stated in a line for a ruling, which is the form the ruling arrived on, and the citation
> above is to that ruling and not to a merge disposition.

## Context

`internal/validate` types the single-byte space, 0xFD (slice 2), 0xFC (slice 5) and 0xFB (slice 7).
Six named single-byte opcodes remain declined that belong to the register's **reference
instructions** entry — the half slice 6 (#359) did not take:

| opcode | mnemonic | proposal |
|---|---|---|
| `0xd3` | `ref.eq` | GC |
| `0xd4` | `ref.as_non_null` | function references |
| `0xd5` | `br_on_null` | function references |
| `0xd6` | `br_on_non_null` | function references |
| `0x14` | `call_ref` | function references |
| `0x15` | `return_call_ref` | function references |

**And the boundary is declared falsely in one place, which is why opening this is a decision rather
than a continuation.** `validate.go:26-29`, slice 2's paragraph, reads:

> *"This paragraph then said `select t` (#294) was the last instruction in the single-byte space
> slice 1 left, which was true when it was written and is not now — slice 4 took it. **The
> single-byte opcode space is fully in vocabulary as of that slice**, and what remains declined is
> **0xFE (threads) alone**."*

Both bolded clauses are **measurably false**, and were false when written. Eleven named single-byte
opcodes were declined at the time: the six above, plus `throw` (0x08), `throw_ref` (0x0a),
`return_call` (0x12), `return_call_indirect` (0x13) and `try_table` (0x1f). Each of the eleven is
witnessed — the six by decline rows in the all-on lane's buckets, the five by a dispatch probe over
`binary.OpMnemonic`'s single-byte rows.

That sentence is the *third* payout of one shape. ADR 0032 found this same paragraph stale on 0xFC
and amended the region list in the sentence immediately after this one — leaving the single-byte
clause standing, in the very motion that was auditing it. A boundary declared in prose with nothing
checking it does not survive the slice that moves it, however carefully the neighbouring sentence is
swept.

## Decision

**Close the register's reference-instruction entry as slice 8, retire the false single-byte
declaration, and replace it with a control rather than a corrected sentence.**

The six arms go in `ref.go`, beside slice 6's five: the two halves of one register entry live in one
file, and `call_ref`/`return_call_ref` are there on the same reading — they are the
function-references calls, whose whole content is the reftype their operand carries. Dispatch stays
in `instr.go`'s one switch, per the region-dispatch arm's standing argument against a second table.

**The prose is not merely corrected.** A sentence naming the declined set is exactly the artifact
that has now gone stale three times, so slice 8's charged overhead is
`TestSingleByteDeclinesAreExactlyTheTwoDeferredProposals`: the declined set, pinned as a literal set
of five with its mnemonics, derived by walking `binary.OpMnemonic`'s rows and asking the real
dispatch. The next slice that moves this boundary gets a failing test rather than a sentence nobody
reads.

**Not decided here:** the shape of the arms, which is normative reference behaviour ported from
`valid.ml:477-489, :532-534, :552-558, :728-730, :742-743` and argued at each site.

## Criterion

**55 rows, measured with the instrument.** Derived by running `run(s).Buckets` over the 256 board
files in the all-gates-on lane and matching decline messages against the six mnemonics longest-first
(so `return_call_ref (0x15)` is not charged to `call_ref`), under a vacuity guard that fatals if the
decline sentinel appears nowhere at all. **Not from a grep over the board log**, which #258's own
body rules out as an instrument, and which is where this slice's first scoping figure came from.

| direction | population | now | required |
|---|---|---|---|
| reject | 27 `assert_invalid (module)` | `declined` | **pass** |
| accept | 28 `module text` definitions | `declined` | **pass** |

Per-opcode frontier — a **lower** bound on coverage, since a decline names the first offending
instruction and shadows every later one: `return_call_ref` 16, `ref_eq` 12, `call_ref` 11,
`ref_as_non_null` 6, `br_on_null` 5, `br_on_non_null` 5.

Per file, accept/reject: `array_init_elem` 2/0, `array_new_elem` 1/0, `br_on_cast` 1/0,
`br_on_cast_fail` 1/0, `br_on_non_null` 3/1, `br_on_null` 3/1, `call_ref` 4/4, `ref_as_non_null` 2/1,
`ref_cast` 1/0, `ref_eq` 1/6, `return_call_ref` 5/11, `table_init` 1/0, `table_init64` 1/0,
`unreached-invalid` 0/3, `unreached-valid` 2/0. **In all fifteen files the slice-8 rows are the
file's entire all-on fail count**, which is what makes the per-file zero a checkable claim rather
than the total being a checkable claim.

**The accept figure carries 0032's stated upper-bound reading.** An `assert_invalid` module is
minimal by construction, so a single-term key really is sole-blocked; a `module text` definition is a
working module, so a single-term key means only "the validator stopped early". 28 is therefore a
ceiling on the accept side and 27 is a count on the reject side, stated apart because 0032's residue
was *entirely* in the accept direction.

### The bound the 55 rows cannot supply: this slice has no new sentinels

0032's criterion rested partly on four new error strings — a rule refusing everything with
`ErrTypeMismatch` would have shown up as ten lateral moves. **Slice 8 has no such lever.** All 27
reject rows expect exactly one string, `type mismatch`, already declared as `ErrTypeMismatch`. A rule
that refused every one of them for the wrong reason scores **27 of 27**. The reject count is
therefore weak evidence here in a way it was not there, and the criterion has to rest on something
structural.

**The reference has two bottoms, and slice 8 is where the difference becomes a verdict.**

- `BotT` — this package's `unknown` — is `match_valtype`'s `BotT, _ -> true`. It satisfies a
  **numeric** requirement.
- `RefT (nul, BotHT)` is `match_heaptype`'s `BotHT, _ -> true` reached *through* `match_reftype`. It
  satisfies every **reference** requirement and **no numeric one**, because the mixed-sort pair falls
  to `match_valtype`'s `_, _ -> false`.

`peek_ref` returns the second (`valid.ml:288`, `| BotT -> (NoNull, BotHT)`), and three of this
slice's six arms are built on `peek_ref`. Conflating the two — the obvious implementation, since
`unknown` was already spelled as an indexed reftype — accepts
`(unreachable) (ref.as_non_null) (f32.abs)`, which `unreached-invalid.wast:697` asserts invalid.

> Left as written, because it is the forecast and a forecast that gets edited after the measurement
> is not one. **Measurement falsified its second sentence**, though not its first: the row exists and
> is unique, but the mutation it kills is not the one named here. The bill is below.

**That is one row of fifty-five.** So the bound is representational and not populational: the
non-nullable reference bottom is a **distinct value** from `unknown` (`botRef(false)` against
`botRef(true)`), `matchHeap`'s bottom arm tests the bottom *heaptype* by index rather than testing
`got == unknown`, and the two print apart as `bot` and `(ref bot)` through `typeStr`. Written before
the port, on 0031's lesson and 0032's application of it: *a representation that cannot express the
wrong relation is a stronger bound than a population.*

The renderer is part of the bound and not a courtesy. Both bottoms are spelled as indexed reference
types naming an index no module can hold, so `binary.ValType.String()` prints `(ref 4294967295)` — an
engine asserting a type index its input does not have, which is grave #36's class.

## Consequences

- **This is a gate-campaign slice, so the default lane's reward is structurally zero.** All six
  opcodes are `gate:gc` (`gatemap.go:154,165,170`); the reward figure is the **all-on lane's fail
  delta**, per the product law's substitution clause. The zero is *measured* rather than argued from
  the gate map, which 0032's consequence list establishes as the stronger form.
- **`unsupported` cannot move**, and the delta is structural for the same reason: what the harness
  can *ask* is unchanged.
- **The instrument is the all-on lane's fail and declined columns, not `validateDeclineCeiling`** —
  that is a default-lane bound standing at 8 for relaxed SIMD, and naming it here would repeat the
  error 0032 caught in its own consequence list.
- **Two of `return_call_ref`'s three siblings stay declined**, and the reason is proposal boundaries
  rather than difficulty: `return_call` (0x12) and `return_call_indirect` (0x13) are the tail-call
  proposal. One of three tail-call shapes lands in this slice because it arrived with function
  references. Stated so the residue is not read as an omission.
- **`binary.ValType.WithNull` becomes exported**, because three arms need a heaptype re-emitted with
  the other null bit from outside `binary`. The alternative is a two-branch reconstruction from
  accessors at every call site — the "silently drop `idx`" hazard `refNull`'s comment names, moved
  into a package that cannot see the fields. `diffRefType` collapses onto it, and the comment there
  claiming `binary` exported no such setter retires with the prior text quoted.
- **ADR 0032's open `funcType` message item gains two call sites and still no witness.** `call_ref`
  and `return_call_ref` both resolve through `func_type`, whose wrong-kind message this package
  spells `type mismatch: type N is a %s, want func` where the reference spells `non-function type N`.
  Checked again here: `non-function type` appears in **no vector in the suite**. Changing a landed
  slice's message on no witness is not this slice's call either, so the item stands as flagged in
  0032 rather than resolved.
- **The rollback is the six dispatch arms.** Deleting them restores the declines exactly, which is
  also how the forecast below was measured.

## Measured result — 55 of 55, and the clean number is the thing to be suspicious of

Written after the port and before the stamp, so the record is accurate when it is stamped.

**All-on lane: 303 fail → 248, a fall of 55; 64743 pass → 64798, a rise of 55; declined 122 → 67, a
fall of 55.** Three columns closing on one figure. **Default lane: byte-identical** — `60837 pass,
188 fail, 66 unsupported, 4053 gated`, `fail by stratum` unchanged term for term including
`validateDeclineCeiling`'s 8, and **0 of its 188 fails naming a slice-8 opcode**.

**An exactly-closing ledger on a 55-row forecast is the shape a blind instrument makes**, so it was
checked against the claim the total cannot make: all fifteen files read **0 fail**, and the family
match over every bucket in the lane finds **0 surviving rows** under the same vacuity guard that
still sees 67 other declines. The per-file table is the verification; the total is the summary.

**Why it closed exactly, where slice 7 came in at 74 of 81 and slice 6 missed by 4.** Both of those
lost rows to *re-declining* — a module cleared of one out-of-slice opcode meeting the next one — and
slice 7's residue was precisely these six opcodes. Slice 8 is the end of that chain in the
single-byte space: there is nothing left for these modules to re-decline on, and `internal/interp`
already executes all six (#172 rung 1), so a validated module runs rather than arriving at a second
frontier. The clean number is a property of being *last*, not of the forecast being better.

**The declined column drained without moving within itself**, which is the discriminator 0032's
`ref_eq.wast` specimen established: 122 → 67 with the six mnemonics each reaching exactly zero. A
partially correct rule would leave a remainder under its own name.

**The one-row structural bound was load-bearing and is now witnessed, and the forecast named the
wrong line.** `unreached-invalid.wast:697` passes and is the only row of the 55 that separates the
two bottoms, so a port that conflated them would have read 54 of 55 with the missing row an
*accept*-direction defect wearing a reject-direction number. But the criterion above located the
conflation at `peek_ref`'s answer, and **making `peekRef` return `unknown` fails no row at all** —
every caller re-emits the null bit (`WithNull(true)` to pop, `WithNull(false)` to push), so the two
spellings are the same value before anything compares them, which is why the reference binds `_nul`
at all three sites. The mutation `:697` actually kills is the **push**: `if isBotHeap(rt) {
v.push(unknown) }` in place of `v.push(rt.WithNull(false))`, bottom leaving the arm as the valtype
bottom rather than as a reference. One line later than forecast, and the distinction is real; the
comments in `ref.go`, `ref_test.go` and `stack.go` that asserted the forecast's localization are
corrected in this PR with the measurement quoted, rather than re-worded.

### The falsification bill — seventeen mutations, rows failed each

Every arm and every claim above, mutated one at a time against the all-on lane. The unit is *rows of
the suite that fail*, so a zero is a claim the suite cannot see and has to be accounted for rather
than dropped.

| # | Mutation | Rows |
|---|---|---|
| A | bottom stays the valtype bottom at the push (`isBotHeap(rt) → push(unknown)`) | **1** |
| B | `ref.as_non_null` pushes `binary.FuncRef` | 9 |
| C | `ref.as_non_null` pushes the peeked type unchanged | 6 |
| D | `peekRef` drops its non-reference classification | 4 |
| E | `peekRef` answers `botRef(true)` instead of `botRef(false)` | **0** |
| F | `matchHeap`'s bottom arm keyed on `got == unknown` | 3 |
| G | `refEq` wants `anyref` | 1 |
| H | `refEq` wants `(ref eq)` (non-nullable) | 4 |
| I | `brOnNull`'s fall-through stays nullable | 1 |
| J | `brOnNull` drops the label-types pop/push | 1 |
| K | `brOnNonNull` drops both `require`s | 2 |
| L | `brOnNonNull` takes its heaptype off the operand | 1 |
| M | `callRef` resolves through `funcTypeAt` instead of `funcType` | 3 |
| N | `callRef` requires a non-nullable operand | 2 |
| O | `returnCallRef` drops the result `require` | 2 |
| P | `returnCallRef` compares results by equality, not by the relation | 1 |
| Q | `returnCallRef` drops the polymorphic tail | 3 |

**E is zero for a reason, and the reason is a documented unobservable rather than a gap.** All three
of the reference's `peek_ref` callers bind `_nul` and discard it, so the null bit this function
answers with is unobservable *by construction* — no suite, upstream's included, can distinguish the
two spellings. It is transcribed as the reference writes it and the zero is recorded here so that a
later reader who mutates it and sees green does not conclude the transcription was pointless.

**Three of the seventeen measured zero on the first attempt and are here at a non-zero figure
because the controls were strengthened, not because the claims were weakened** — F, J and K. F's
rows all happened to use bottoms that the wrong key still admitted, so concrete-`(ref $t)` and
abstract-`funcref` want rows were added; J's refusals came from the block-arity check rather than
from the pop it was supposed to exercise, so an accept-direction row with the label's types absent
from the stack was added; K's rows still refused through `popExpect`, so the two `require`s' own
message texts are now asserted by substring. That is the corpus's G-3 hole in miniature: a refusal
for the wrong reason scores the same as the right one, and the only way to tell is to key on the
wrapped detail.

**P and Q are the pair worth reading together.** Q needed its specimen replaced mid-measurement: the
first `(f32.const 0)` tail was itself invalid — explicit pushes in an unreachable frame still reach
`end` — so the row was refused for a reason unrelated to the polymorphism it was supposed to
discriminate. `(nop)` is what discriminates it, at 3 rows.

**A finding this slice did not go looking for: `allOnPassFloor` was 89 stale before this PR.** It
stood at **64654**, written by slice 6 (#363), against an actual **64743** — inside its 250 slack, so
nothing fired, and therefore unable to catch any regression smaller than 89. Two PRs moved the lane
and left it: #378 (the linker's type comparison) and **#382, slice 7 itself**, whose own ADR records
`377 fail → 303` for this lane and which does not touch this constant. It is re-based to **64798**
here.

The rule it breaks is 0013's, quoted in the slack's own comment: *a PR that moves the board and
raises the bound in the same PR leaves a distance of zero.* The slack predicted the failure mode in
the abstract — "a bound left behind by a large jump degrades into decoration" — and what it did not
predict is that a bound decays by *accumulation* of moves each too small to trip it. A slice-sized
+74 hid inside a 250-wide slack, which means the mechanism that was supposed to make re-basing
automatic is the mechanism that made forgetting it invisible. Filed against the bound, not against the
slack: widening the slack is the staleness defect one level up, and narrowing it is #42's business.
