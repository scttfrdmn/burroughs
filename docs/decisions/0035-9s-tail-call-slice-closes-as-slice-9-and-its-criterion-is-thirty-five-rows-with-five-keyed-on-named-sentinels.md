# 0035 — #9's tail-call slice closes as slice 9, and its criterion is thirty-five rows with five keyed on named sentinels

Date: 2026-08-17 · Status: **proposed** — retrospective once measured, and **nothing in it binds future
work**, which is stated so the Status field is not spent on again

> *"Merge on green and start slice 9."* — Scott. That authorizes the slice and its merge tier, per
> 0034's correction: a disposition on the work is not a stamp on a criterion nobody read. This record
> holds a forecast, a measurement and a grave; it introduces no invariant for later slices to obey, so
> `proposed` is where it stays.

## Context

`internal/validate` types the single-byte space except three opcodes, 0xFD (slice 2), 0xFC (slice 5)
and 0xFB (slice 7). What remains declined in the single-byte space is **two proposals**, and slice 8's
consequence list named the split:

> *"Two of `return_call_ref`'s three siblings stay declined, and the reason is proposal boundaries
> rather than difficulty: `return_call` (0x12) and `return_call_indirect` (0x13) are the tail-call
> proposal. One of three tail-call shapes lands in this slice because it arrived with function
> references."*

This slice is that pair. **The validator is the sole blocker for both**, which is worth stating
because it is not the usual case: `internal/interp` already executes them as real tail calls
(`internal/interp/exec.go:runFrame`, ADR 0026 / #253), the decoder reads them under `gateTailCall`,
and the wat encoder
emits them. Nothing is missing but the two typing rules — the exact shape ADR 0025's G-1 carve-out
names, arriving in the direction that retires the carve-out rather than widening it.

**No boundary moves here, and that is a difference from slice 5.** Tail calls were never in
`validate.go`'s out-of-scope list; they were declined for want of arms. Exception handling *is* in
that list, so `throw` (0x08), `throw_ref` (0x0a) and `try_table` (0x1f) stay declined and the deferred
set becomes **one proposal instead of two** — which is a change to a control's own name, handled
below.

## Decision

**Port `ReturnCall` and `ReturnCallIndirect`, and repair the `require` that reading them exposed in a
landed arm.**

- `returnCall` — `valid.ml:544-550`. Function index → functype, require the callee's results satisfy
  *this* function's declared results, pop the parameters, go polymorphic.
- `returnCallIndirect` — `valid.ml:560-570`. Table, then functype from the type index, then **two**
  requires: the table's element type against `(ref null func)`, and the same result-type condition;
  then the table index at the table's address type, the parameters, and the polymorphic tail.
- **`callIndirect` gains the element-type require it never had** (#390). `valid.ml:563` is in both
  arms and the landed one omits it, so `(table 10 externref)` + `call_indirect` is accepted today.
  Written once and called from both sites, because writing it only in the new arm would leave the
  grave standing in the file that just proved it exists.

`returnCallRef` (slice 8) is the shape both new arms follow — its `matchResultType` against
`v.frames[0].labelTypes` and its `setUnreachable` are the two halves this proposal's opcodes need, and
the third sibling having landed first is the accident that makes this slice small.

**Not decided here:** the elem-segment function-index rule (#391), whose two admissions sit in these
same files and are *not* instruction rules — the code-section walk never visits an elem segment. A
vector's file is not its stratum.

## Criterion

**35 rows, pre-registered, measured over the all-on lane with `run(s).Buckets` and a vacuity guard
that fatals if the decline sentinel appears nowhere.** Not from a grep over board text (#161).

| direction | population | now | required |
|---|---|---|---|
| reject | 28 `assert_invalid (module)` | `declined` | **pass** |
| accept | 6 `module text` definitions | `declined` | **pass** |
| admission | 1 `call_indirect.wast:994` | **accepted** | **pass** |

Per file, and **in both files these are the file's entire decline population**: `return_call.wast` 15
(12 reject / 3 accept), `return_call_indirect.wast` 19 (16 reject / 3 accept). Per opcode:
`return_call_indirect` 18, `return_call` 16.

### The reject side is *not* one sentinel, and slice 8's could not say that

Slice 8's 27 reject rows all expected `type mismatch`, so a rule refusing everything for the wrong
reason scored 27 of 27 and the criterion had to rest on a representational bound instead. Here the
expected strings are:

| expected | rows |
|---|---|
| `type mismatch` | 23 |
| `unknown function` | 2 |
| `unknown type` | 2 |
| `unknown table` | 1 |

**A rule that refused all 28 with `ErrTypeMismatch` therefore scores 23 of 28**, and the five that
disagree are the three index-space lookups — the function index for `return_call`, the type index and
the table index for `return_call_indirect`. That is a populational discriminator this slice has and
its predecessor did not, so the criterion rests on the count in a way slice 8's deliberately did not.

The **accept** direction remains the weaker half, per 0032's reading: a `module text` definition is a
working module, so a decline naming one opcode means only that the validator stopped there. 6 is a
ceiling on that side and 28 is a count on the other.

## Consequences

- **A gate-campaign slice, so the default lane's reward is structurally zero.** `TailCall` is absent
  from `DefaultFeatures()`, so both opcodes are declined at *decode* in the default lane and land in
  the gated column, not the fail column. The reward figure is the all-on lane's fail delta, measured
  rather than argued from the gate map.
- **`unsupported` cannot move**, and the delta is structural: what the harness can *ask* is unchanged.
- **The deferred set drops to three rows, all exception handling**, so slice 8's charged control is
  renamed: `TestSingleByteDeclinesAreExactlyTheTwoDeferredProposals` →
  `TestSingleByteDeclinesAreExactlyExceptionHandling`. A test name is a checkable citation, and a name
  asserting *two* proposals over a set holding one is a false one. The rename is swept through every
  Go comment citing it, which `TestEveryCitedTestNameResolves` enforces.
- **The specimen test stops being re-pointed by hand.** `TestDeclinesAreDeclinesAndNameTheirOpcode`'s
  single-byte case has been re-aimed four times — every slice that types its opcode breaks it, and
  slice 9 types `return_call`, which is where slice 8 aimed it. The literal specimen stays (a wat
  module cannot be synthesized per opcode), but the failure now **prints the still-declined set** so
  the repair is a one-line edit with the answer in the message. That is Scott's #42 ruling applied to
  a different mechanism: *a consequence is only a cost after its remedy is priced*.
- **The rollback is the two dispatch arms plus the shared require.** Deleting them restores the
  declines and re-opens #390 exactly.
- **#354 rides here** as this slice's other charged overhead, per Scott's scheduling ruling: the
  module-definition mutation table enumerates three `Kind`s where it can derive them.

## Measured result

**35, exactly.** All-on lane pass 64798 → 64833; fail 248 → 213.

An exactly-closing total is the one result a pre-registration should be least willing to accept on its
own — 35 forecast and 35 measured is also what a miscount plus a compensating miscount looks like — so
it was taken three independent ways, and the three do not share a mechanism:

| reading | before | after | delta |
|---|---|---|---|
| all-on pass count (`TestAllGatesOnLeavesNothingGated`) | 64798 | 64833 | **+35** |
| all-on `Declined` census, per file | 67 | 33 | **−34** |
| `assert_invalid` destination ledger, `accepted` column | 31 | 30 | **−1** |

The second and third sum to the first and the split is the *content* of the criterion rather than an
arithmetic coincidence: 34 of the rows were declines becoming verdicts and the 35th was grave #390's
admission becoming a refusal, which is a different kind of gain and lives in a different column. A
single figure could not have said that.

Per file and per opcode, against the pre-registration above: `return_call.wast` 15 of 49 declines →
0, `return_call_indirect.wast` 19 of 81 → 0 — in both cases the file's *entire* decline population, as
forecast — and per opcode `return_call_indirect` 18, `return_call` 16. The 33 that remain are 25
exception handling (`try_table` 16, `throw` 7, `throw_ref` 2, three of them inside `instance.wast`)
and 8 relaxed SIMD, which is the deferred set the renamed control now names.

### The default lane's +1 was not forecast, and the reason is the shape worth keeping

The Consequences above say the default lane's reward is *structurally* zero because `TailCall` is
absent from `DefaultFeatures()`. That was right about the two opcodes and wrong about the slice:

| default lane | before | after |
|---|---|---|
| pass | 60837 | 60838 |
| validate-stratum fail | 39 | 38 |
| admitted | 31 | 30 |

**#390's repair rides `call_indirect`, which is MVP core and ungated.** The forecast was written from
inside the slice's subject — the two new arms — and the grave the slice dug is in a landed arm that
ships in every lane. *A repair rides the lane its subject ships on, not the lane its discoverer was
working in*, and a gate-campaign slice is exactly where that is easy to miss, because "gated
proposal, therefore no default-lane delta" is true often enough to be reached for without checking.
Re-based at three sites (`passFloor`, `validateFailCeiling`, `validateAdmitCeiling`) plus the
destination ledger, and the ledger's own instruction — say which destination the delta came *from* —
is what makes the entry legible: it came out of `accepted` alone, so it is a correctness gain and not
a vocabulary one, which is the discriminator that separates it from the 34.

### The falsification bill

Thirteen mutations of the two arms and the shared helper, each built, then run against
`internal/validate` and against both board lanes. Twelve die; the full table is in the PR. Three rows
are the informative ones:

- **M12 — `indirectTarget` resolves the type before the table (order swapped): caught by nothing.**
  Zero failures in `internal/validate`, board green in both lanes. This is the *pre-registered* zero:
  `tailcall.go`'s comment states the ordering is unwitnessed and names the four vectors that would have
  to differ for it to be visible. A claim of unwitnessedness is checkable, and this is it checked.
- **M11 — the index operand hardcoded to `i32`: board green, caught only by `internal/validate`.**
  The corpus has no 64-bit-table vector for this opcode, so the `i32 index on a 64-bit table` row is
  the only thing anywhere that objects — the same single-row exposure #343 cause 2 records for
  `call_indirect`, now measured for this arm rather than argued from it.
- **M5 — the element-type require deleted, i.e. grave #390 restored: the only mutation of thirteen
  that moves the default lane** (validate fail 38 → 39, admitted 30 → 31). The bill therefore
  re-derives the forecast miss above from the other direction, which is a stronger account of the +1
  than the re-basing alone.

**One mutation was answered by strengthening the witnesses rather than by weakening the claim.** M13
— deleting `popExpectAll` from `returnCall` — passed every test in `internal/validate` and was caught
only by the board (−6 all-on passes). A tail call whose parameters are never popped accepts any
operand stack, because `setUnreachable` then excuses whatever is left on it: the arm's two halves were
covering for each other's absence. Fixed by adding an `argument of the wrong type` row and a per-row
`msg` field, so the rows keyed on the result require can no longer be satisfied by a refusal from the
operand pop. Recorded because a bill whose findings are argued away measures the arguer.

### #354, the charged overhead

Closed as forecast: the module-definition mutation table is now a named variable and a fourth subtest
derives the domain from it, over `suitePaths` rather than `boardFiles` — a domain derived from the
board selector would inherit *that* selector's blind spot while removing another's. Measured domain:
four Kinds, `module text` 2143, `module binary` 88, `<unsupported>` 9 (the `definition`/`instance`
forms, excused by name with the reason), `module quote` 7; counts pinned exactly beside the set,
because set equality is satisfied by a population that drained to one command. Watched die three ways
— deleting the quote row (re-creating the #353 omission), excusing a Kind nothing produces (the
vacuity arm), and narrowing `classify` so `definition`/`instance` fall into `KindModuleText`, which
trips all three arms at once.

One correction to the issue's premise, since it was slightly wider than the mechanism: #354 reasoned
that a new module form "arrives as a new Kind in `classify`". That holds for a form someone gives an
arm and not for a bare new keyword — `moduleFormKeyword` is deliberately not an allowlist, so
`(module newthing …)` falls through to the wat reader as `KindModuleText`. The census cannot see that
and does not need to: such a form lands on the arm the text row already falsifies. The hole this
closes is the other one, a Kind gaining an arm without gaining a witness, which is how the quote row
came to be missing until #353.
