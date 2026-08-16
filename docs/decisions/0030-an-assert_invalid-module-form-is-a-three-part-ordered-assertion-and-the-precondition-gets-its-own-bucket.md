# 0030 — An `assert_invalid` module form is a three-part *ordered* assertion, and the precondition gets its own bucket

Date: 2026-08-16 · Status: **accepted** (Scott, relayed message quoted verbatim below).
Never held `proposed`: the design questions were put to Scott before implementation and answered
directly, so there is no open interval to record. The one thing decided *by* implementation rather
than by ruling — `AssembleFunc` in place of the widened `ReadTextFunc` he approved — is flagged as
such under decision 2, because it contradicts a sentence in his own message.

Filed against **#9** (the validator) and milestone **v0 interpreter**.

## Context

`spec.classify` gave the `assert_invalid` head one Kind, and that Kind required a bare
`(module …)` field. The corpus writes the assertion three ways:

```wast
(assert_invalid (module (func (result i32))) "type mismatch")           ; bare
(assert_invalid (module binary "\00asm" "\01\00\00\00" …) "…")          ; a pre-assembled image
(assert_invalid (module quote "(module (func (result i32)))") "…")      ; wat source in a string
```

The latter two — **17 vectors** across the vendored suite — scored `unsupported`. The recorded reason,
in `ledger_test.go`'s residual and in `unsupportedCeiling`'s own account, was that they "need a
text-format assembler the arm does not have."

**That reason was false when it was written.** `text.EncodeModule` existed and was already in use by
the public-path fixtures. What did not exist was a *boundary*: `spec.ReadTextFunc` is
`func(src []byte) error`, so the harness had a function that could say a module was clean and no way
to get the module back out. Scott named this directly, and it is the observation the whole record
hangs on:

> The correction on the assembler is the useful part — the record said the cost was a missing
> assembler when the assembler was already in use by the public-path fixtures.

## Decision 1 — three parts, in order, and the precondition is not folded into the verdict

`KindAssertInvalidBinary` and `KindAssertInvalidQuote` each assert three things **in sequence**:

1. the module comes into being — it decodes (binary) or assembles (quote);
2. validation refuses it;
3. the refusal's message matches the expected text.

A failure at step 1 is **not a pass, and does not join the validation fail bucket**. It gets its own
named bucket, at its own stratum:

| form | precondition bucket | stratum |
| --- | --- | --- |
| binary | `assert_invalid (binary) must decode before validation judges it` | `StratumBinary` |
| quote | `assert_invalid (quote) must assemble before validation judges it` | `StratumText` |

Scott's ruling, verbatim:

> **The Kinds: keep the layers apart, and make decode success a separately asserted precondition.**
> `KindAssertInvalidBinary` requires three things in order — the module decodes, validation rejects
> it, and the message matches. A decode refusal is not a pass and it does not join the validation
> fail bucket; it gets its own named bucket. Those two facts have different owners. A decoder
> refusing a module the spec says must decode and then fail validation is a defect in the decoder,
> and burying it under a validator fail makes the wrong team's work invisible. This is the same
> discipline as #288's message-match, extended one layer down: rejection alone is never the verdict,
> because rejected-for-the-wrong-reason reads identically to rejected-for-the-right-one.

> `KindAssertInvalidQuote` takes the same three-part shape once the boundary exists, and yes to
> changing `ReadTextFunc` to hand back the module.

**What makes the separation structural rather than tidy.** `StratumBinary`'s ceiling is `0` and it is
shared with nothing else on the board, so the first vector that decodes wrong here is a red in the
decoder's own column on the run that produces it — it cannot be absorbed by `validateFailCeiling`'s
slack. The quote form lands in `StratumText`, whose ceiling is likewise `0`. Neither number is a
budget the validator can spend.

### Options considered

| option | why not |
| --- | --- |
| Score a decode refusal as a pass | The vector says *must decode, then fail validation*. A refusal at step 1 satisfies the assertion's letter (something refused it) while falsifying its subject. This is G-3 accept-direction blindness turned inside out: the corpus cannot tell the two apart, so the harness must. |
| Put it in the validation fail bucket | Makes a decoder defect read as validator work-in-progress. Different owners, and the validator's bucket is a *work plan* — a decoder bug in it is a line nobody will action. |
| One bucket for both forms' preconditions | The two failures have different remedies (decoder vs emitter) and different strata. One bucket asserting two properties is the partition defect (grave #34). |

## Decision 2 — the boundary is a new `AssembleFunc`, not a widened `ReadTextFunc`

Scott approved widening `ReadTextFunc` to hand back the module. **The board refused it**, and this is
the one place this record departs from his message:

```go
type ReadTextFunc func(src []byte) error          // unchanged — 0011 stands
type AssembleFunc func(src []byte) ([]byte, error) // new, nil-checked at its one arm
```

Pointing `ReadTextFunc` at `text.EncodeModule` regressed **58 vectors** (`text 0 → 58`,
`pass 60756 → 60734`). The mechanism:

- `text.ReadModule` is `parseModule(src, recognize)`.
- `text.EncodeModule` is `parseModule(src, build)` **plus** `p.encode()`.

Build mode does strictly more work, and the emitter is behind — it cannot yet write `(table …)` or
`(start …)` fields (#8), nor #77's symbolic locals. So modules that recognize clean fail to encode,
and the arm `ReadTextFunc` serves handles **three** Kinds, only one of which wanted the image.

Decision **0011**'s error-only return therefore stands rather than being superseded. The engine-side
argument is 0011's own and unchanged: a single function with an invisible mode flag cannot say which
question was asked, so the second question gets a second function.

**This is a premise correction, not a re-decision.** Scott's "yes" was to *the harness getting the
module back*, and the harness gets it. What changed is which signature carries it, on grounds that
did not exist when he answered — the 58 were not measured yet. Flagged rather than quietly absorbed,
per the rule that an ADR is a citation to an approval: the approval covers the capability, and the
signature is the actor's call, reversible on his word.

### The grave inside this decision (grave #329)

The widening **was pre-measured, and the measurement still missed it.** A probe compared `ReadModule`
against `EncodeModule` and found **zero disagreement across 1236 commands**. Two independent defects
in one figure:

1. **The domain was narrower than the claim.** The probe covered `KindModuleQuote` and
   `KindAssertMalformedText`. The arm also serves `KindModuleText` — **1119 bare `(module <wat body>)`
   bodies** — which is precisely the population that regressed. The claim recorded was "every command
   this function sees": 1236 of 2355.
2. **The zero was vacuous.** 1229 of the 1236 fail inside the shared `parseModule` before the encoder
   is reached. Seven modules, 146 bytes, were the entire subject of the agreement — so the honest
   statement was "seven modules encode," not "1236 commands agree."

*Coverage is a claim: an instrument's domain is an assertion it cannot check about itself*, and *a
suspiciously clean result is a tell, exactly zero being the cleanest*. Both laws were already in the
index. The lesson that was not: **derive a probe's domain from the call sites of the thing it
measures, never from the cases you are thinking about** — the probe was written to clear a specific
change, which is the condition under which the domain gets drawn from recall.

## Decision 3 — the binary arm checks its two decode paths against each other

The quote arm assembles once and hands the resulting image to the validator, so steps 2–3 provably
judge the module step 1 accepted. **The binary arm cannot have that property**: its precondition calls
`DecodeFunc` on `c.Module`, and `ValidateFunc` re-derives its own image from the same bytes. Two paths
over one input, and nothing forces them to agree.

So a disagreement — step 1 accepted the module and validation came back at `StratumBinary` or
`StratumEncode` — is bucketed as `assert_invalid (binary) two decode paths disagree` rather than
reported as a validation outcome. Empty today. It is the *reason* the arm's ordering claim is true,
and an unasserted reason is an intention.

## Decision 4 — `Kind.isAssertInvalid()`, with a name-derived second mechanism

Three Kinds where there was one falsifies every `c.Kind == KindAssertInvalid` in the package. Two
existed, and their failure modes are the useful part (grave #330):

- **`TestGatedVectors`' bulk arm failed loudly.** Its per-line arm counts the complement, so two
  gated `(module binary …)` vectors — `align.wast:948` (multi-memory, `memarg flags bit 6`) and
  `elem.wast:524` (`gc`) — fell out of the bulk count and were demanded by name.
- **The destination ledger failed silently.** Its pinned rows kept agreeing with each other while the
  population beneath them grew by 17. No complement, no complaint.

The predicate replaces both, and it is enumerated rather than derived from `String()` because the run
loop calls it per command. That makes it checkable from a genuinely different field:
`TestAssertInvalidKindsAreExactlyTheAssertInvalidForms` derives the set from `String()`'s prefix in
both directions, pins the enum's size, probes above `KindUnsupported` for members outside the scanned
range, and asserts all renderings are distinct — because the ledger keys buckets on that string, and
a collision would merge two populations while the rows still summed. Watched dying on five mutations
tripping five different assertions; the two range guards are each blind to the other's mutation.

**`TestGatedVectors`' remedy text was also wrong**, and that is worth a line: it said to add the two
lines to the per-line allowlist with the feature named, which would have papered over the predicate.
A bucket names where a symptom surfaces, not where the defect lives (#194) — the control was right
that something was wrong and wrong about what.

## Consequences

- **The board.** `unsupported` **83 → 66**, the whole delta. Default lane 60749 → **60756 pass** /
  269 fail / **66 unsupported** / 4053 gated; all-gates-on 64631 → **64639 pass** / 407 fail / 0
  gated. Both lanes landed on the pre-registered forecast.
- **The 17 split 7 pass / 8 admitted / 2 gated**, and the 8 are the honest half: an `assert_invalid`
  the validator *accepts* is a rule this package does not have. `validateAdmitCeiling` 103 → 111
  records them as owed. A slice that reported 17 as reward would be counting its own gaps.
- **The ledger's residual is now 0**, and it has degraded from a two-sided identity to a one-sided
  tripwire — it can only rise. It stays asserted because the surviving direction is the one that
  matters: `classify` handing an `assert_invalid` head back to `KindUnsupported` would otherwise show
  as a *smaller population*, not as a loss.
- **`gatedAssertInvalid` sums to 465**, and its error message now names a third legitimate cause for
  a re-base: the harness learning to *ask* a form it previously scored unsupported. Not a gate moving
  and not the corpus moving. The gates declined those two vectors all along and nothing was listening.
- **#296's domain widens to boundary signatures**, on Scott's ruling — recorded there, not here.
- **Deferred:** `Needs` holds one capability while these commands need two (an assembler *and* the
  type checker). The quote arm's `Needs: CapValidator` is therefore an understatement, filed as a
  fourth boundary-signature specimen on #296 rather than papered over.
