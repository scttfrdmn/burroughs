# 0039 — A reference's payload kind crosses the two boundaries as one enumerated kind, and the static-type gate is its own census

Date: 2026-08-19 · Status: **accepted** — stamped by Scott on the #443 relay ("0039 stamped.
Implement #270."), **with one condition**, quoted because it changed the implementation rather than
approving it as drafted: *"the payload kind is handled exhaustively at both boundaries, with a test
that enumerates the kinds from the type's own definition and fails on any unmapped one. No `default`
case that silently absorbs a future member — an enum whose whole purpose is to grow must fail loudly
the first time it does."*

The condition is discharged by three controls, one per restated vocabulary — the vocabularies are
restated rather than imported (see decision 1), so a single control could not have covered them:
`TestPayloadConversionCoversTheWholeVocabulary` (root, the public enum and the path through
`valueToInternal`/`valueFromInternal`), `TestInterpPayloadsCoverTheEngineVocabulary` and
`TestEveryRefPatHasASpelling` (`internal/spec`). Each enumerates from an `iota`-maintained in-block
sentinel placed after the last real member rather than from a hand-written list, each asserts count
equality and injectivity in both directions, none has an absorbing `default`, and **each was watched
failing on an injected member before it was believed** — a control isn't born until it's watched die.
`interp`'s sentinel is exported (`PayloadPastEnd`) because two other packages must be able to name
the bound; the other two stay unexported.

The record was held open until that stamp existed, on purpose: it widens the **public**
`burroughs.Value`, which [0029](0029-the-public-boundary-run-on-a-validated-path-decline-as-a-third-outcome-and-a-value-that-converts.md)
established as the surface a host crosses, and [0027](0027-an-externref-is-a-one-bit-wrapper-a-host-reference-is-an-anyref-payload-and-the-cast-familys-reftypes-live-in-a-side-table.md)'s
decision 3 is the precedent for the size of that question — adding *one bit* (`IsHost`) to a
boundary value was an ADR with a stamp. Written ahead of implementation on Scott's order of
2026-08-19 ("proceed with 270"); the sentence at the end is his.

## Context

[#270](https://github.com/scttfrdmn/burroughs/issues/270) is the largest single item left in v0's
`unsupported` column, which is — as of Scott's ruling of 2026-08-19 — **the column the phase ladder
is read off**. The all-on lane's 38 fails are gated GC and EH proposals, the same class as the 4159
gated rows, and by v0's own definition (MVP core suite green with 3.0-feature gates present and off)
they are not v0's remainder. So this issue is on the critical path to v0 in a way it was not when it
was filed.

### The population, re-measured, because two claims about it disagreed

#270 states its population as **28**. Scott's review of 2026-08-19 states **39**. The board's
`assert_return` unsupported sub-column is 39. Those are three sentences about one set, so it was
measured again through the real readers (`assertReturn`/`invokeAction`/`namedInvokeAction`/`readResult`,
partitioned by the shape that declines, not by grep):

| declining shape | n | first sites |
|---|---|---|
| `result: ref.array` | 17 | `array.wast:97,142,202,276`, `array_new_data.wast:12,13` |
| `action: head=get` | **11** | `exports.wast:93,94,97`, `linking.wast:67,68,69` |
| `result: ref.eq` | 4 | `array.wast:98,143,203,277` |
| `result: ref.host` | 2 | `extern.wast:39,56` |
| `result: ref.i31` | 2 | `extern.wast:53`, `i31.wast:33` |
| `result: ref.struct` | 2 | `extern.wast:54`, `struct.wast:122` |
| `action: invoke arg=ref.host` | 1 | `extern.wast:42` |

**#270 is 28 and 39 is the sub-column**: the other 11 are the global-`get` action stratum, which #270
itself excludes by name and [#323](https://github.com/scttfrdmn/burroughs/issues/323) carries. The
`result: either` 32 that #270 listed as the third exclusion are **gone** — `readResult` admits them
now — which is why the sub-column fell from 71 to 39 while #270's own 28 did not move. So the review's
figure was the column's, not this issue's, and the whole `unsupported` column decomposes with nothing
left over:

    28  (#270, here)  +  11  (#323)  +  15  (assert_exhaustion, #440)  +  3  (no head atom, #320)  =  57

The 28 are **gate-blind** — `unsupported` is decided at classification time, before gating — so they
are the same 28 in both lanes.

### Two of #270's stated walls have moved, and one has grown

The issue's "why this is not harness work" section names two widenings. Re-read against `main`:

- **`internal/binary` needs no widening.** #270 says it "exports no `anyref` ValType — only
  `FuncRef`, `ExternRef` and `RefType(idx, null)`; `refKind` is unexported." That was true when the
  issue was filed and is **stale now**: `AbstractRefType(kind byte, null bool) (ValType, bool)` is
  exported (it arrived for #9's slice 8 alongside `WithNull`) and admits every one of the twelve
  abstract heaptypes, `anyref` included. `extern.wast:42`'s `anyref` parameter type therefore has a
  static type to carry across the boundary already. One of the two widenings this issue was priced on
  does not exist.
- **The value boundary is two boundaries, not one.** #270 predates the public package
  ([#299](https://github.com/scttfrdmn/burroughs/issues/299)/0029). Today a payload kind must cross
  `interp.Value` **and** `burroughs.Value`, and `valueToInternal`/`valueFromInternal` (`convert.go`)
  map them field-for-field — so the widening is the same shape twice with a conversion in the middle,
  and the second one is the surface a host actually touches.
- **The type vocabulary at the public boundary is already done**, which is the half that got cheaper:
  `burroughs.Kind` has all twelve abstract reference kinds — `KindAnyRef`, `KindEqRef`, `KindI31Ref`,
  `KindStructRef`, `KindArrayRef`, `KindExnRef`, the four bottom types — plus `KindTypedRef`. What is
  missing is not a *type* for a reference; it is a way for a **value** to say which constructor it is.

A ruling's premises are checkable separately from its conclusion, and here the conclusion (this needs an
ADR) survives both stale premises — but it survives on the second bullet, not the first, and a record
that inherited #270's pricing would have charged for a `binary` widening nobody needs.

### What the authority does, and what the harness invented

`parser.mly:1517-1530`'s `result` production has **eight** `RefTypePat` arms — AnyHT, EqHT, I31HT,
StructHT, ArrayHT, FuncHT, ExnHT, ExternHT — and `literal_ref` (`:1501-1502`) has two: `(ref.host N)`
is a bare `Script.HostRef N`, `(ref.extern N)` is `Extern.ExternRef (HostRef N)`.

`assert_ref_pat` (`script/runner.ml:464-476`) matches a pattern against the **runtime value
constructor** and reads no static type at all:

```
| RefTypePat AnyHT, Instance.FuncRef _ -> false
| RefTypePat AnyHT, _
| RefTypePat EqHT, (I31.I31Ref _ | Aggr.StructRef _ | Aggr.ArrayRef _)
| RefTypePat I31HT, I31.I31Ref _
| RefTypePat StructHT, Aggr.StructRef _
| RefTypePat ArrayHT, Aggr.ArrayRef _ -> true
```

Two things follow, and they are separable:

1. **Six of the eight arms are unrepresentable.** `spec.Val` carries a `RefTypePattern` class whose
   heaptype is implied by `Kind`, and `ValKind` has exactly two reference members
   (`KindFuncRef`/`KindExternRef`). There is no way to write down "the `ref.array` pattern", and no way
   to write down a `got` that is an array either.
2. **`Matches` gates the non-null reference path on `want.Kind != got.Kind`** (`internal/spec/value.go:491`),
   which the authority has no analogue for. That gate is the same shape [grave #266](https://github.com/scttfrdmn/burroughs/issues/266)
   killed one layer down — a static type standing in for a value's identity — and unpicking it is an
   **accept-direction** change over every currently-passing vector.

The engine already knows the answer internally: `interp.ref` carries `Null`, `IsI31`, `Externalized`,
`IsHost`, `Obj != nil`, `Exc != nil` — six payload kinds by `IsHost`'s own count. **`fromRef` is where
the knowledge is dropped**: for any static type that is not `ExternRef` it returns
`Value{Type: t, Bits: uint64(r.Addr)}`, discarding every discriminator, so an `internalize`d host
reference arrives as `Value{Type: anyref, Bits: 1}` and an `externalize-i` result cannot say which
constructor it wraps.

## Decision

### 1. One enumerated payload kind, defined per layer, mapped at each seam

A reference value gains a **single enumerated payload-kind field** — not a family of bools — on both
boundary types, with the vocabulary restated per layer rather than imported, exactly as `spec.ValKind`
already documents for itself:

| layer | today | after |
|---|---|---|
| `interp.ref` | `IsI31`, `IsHost`, `Externalized`, `Obj`, `Exc` | unchanged (the internal shape is 0020's and is not this record's subject) |
| `interp.Value` | `Null`, `IsHost`, `RefID` | `Null`, `RefKind`, `RefID`, `I31` |
| `burroughs.Value` | `null`, `host`, `ref` | `null`, `refKind`, `ref`, `i31` (+ an accessor and a `Kind`-shaped public enum) |
| `spec.Val` | `Class RefClass`, `Extern` | `+ Pat` (the pattern's heaptype, `want` only) and `+ Payload` (the constructor, `got` only) |

Members: **none, host, i31, struct, array, func, exn**. `fromRef` maps `ref`'s discriminators onto it
in one place; `valueFromInternal`/`valueToInternal` carry it across the public seam.

**`IsHost` migrates rather than being joined.** `Payload == PayloadHost` is the same fact `IsHost`
states, and keeping both would be two places holding one fact — the shape three graves in this repo
share. 0027 decision 3's stamped bit is not repudiated: it is the first member of the enum this record
generalizes it into, and its doc comment's argument (identity 0 is a legitimate host reference, so a
zero `RefID` cannot select the kind) is *why* the discriminator has to be a field at all.

### 2. The `want.Kind != got.Kind` gate is a separate issue with its own census

This record covers the **representation**. It does not unpick `Matches`' static-type gate, because the
two have different oracles, and *[splitting at the oracle seam](../laws/citations.md)* is what that asks
for: the representation's oracle is the reference's constructor list, checkable vector by vector; the
gate's oracle is an accept-direction census over the whole passing population, which no
negative-direction corpus can falsify at all — [the suite is the oracle](../laws/engine.md), and a
corpus of rejections cannot witness a wrong acceptance.
Bundling them would be two verdicts wearing one green (#252's ruling). Filed as
[#441](https://github.com/scttfrdmn/burroughs/issues/441), `type:decision`, blocked on this one, with
the census it needs written down there rather than left as "wants a census".

### 3. `ref.array`'s 17 are not forecast to pass

See the forecast section. A harness that can finally *ask* 17 array questions may answer some of them
wrongly, and that is the honest reading rather than a risk to hedge.

## Options considered

**Option 1 — one bool per payload kind** (`IsI31`, `IsStruct`, `IsArray`, keeping `IsHost`).
Consistent with `interp.ref`'s own shape and with 0020's one-field-per-payload-kind rule, and it is the
smallest diff. Declined: the kinds are **mutually exclusive** and a bitfield does not say so, so
`IsI31 && IsStruct` is constructible at a *public* boundary and every reader needs a precedence order
to be safe. Three bools today and two more when `exn` and `func` arrive is five fields encoding one
choice, with the "meaningful only when" prose multiplying across both structs. The invalid combination
is the objection: a public type should not be able to spell a value the engine cannot produce.

**Option 2 — one enumerated payload kind. Chosen.** Mutual exclusion is by construction, one field
replaces three-plus bools, and it reads directly against the authority: `assert_ref_pat` dispatches on
the runtime **constructor**, and a variant is what OCaml's own value type is. Cost: it migrates a
stamped field (`IsHost`), and an enum at a public boundary is a vocabulary that must be exhaustive
before it is exported — which is why the member list is fixed here rather than grown per slice.

**Option 3 — carry the value's dynamic heaptype instead of a payload kind.** Attractive because the
patterns *are* heaptypes, so `Matches` would become a heaptype comparison. Declined on the authority:
`assert_ref_pat` reads the constructor and **no static type**, and a host reference's dynamic heaptype
is `any` (`script.ml:80`, and `interp/value.go`'s `IsHost` comment already cites `ref_test.wast:118`
vs `:127` as the corpus asserting that placement) — so `RefTypePat I31HT` against a heaptype cannot be
answered, and `RefTypePat AnyHT` against a `FuncRef` would answer *wrongly*. This is grave #266's shape
re-introduced one layer up: a static type standing in for a value's identity.

**Option 4 — widen nothing; have the engine answer the pattern.** Keep both `Value` types as they are
and export a predicate — "does this reference satisfy heaptype H" — that the harness calls. Cheapest by
diff, and it keeps the payload representation private, which 0002's GC-precision pin and the plain fact
that a `*gcObj` is not expressible in a `Value` both argue for. **Declined, and this is the strongest
argument in the record**: the harness would be asking the engine the very question it is scoring the
engine on, so a wrong answer is green by construction — the accept direction, where a corpus of
rejections has nothing to say. It is the same fabrication `IsHost` was introduced to prevent: a boundary
handing out a value it does not have. The oracle must be able to build `got` itself.

*(Stated in its own words rather than with this tree's usual `§9 G-3` citation, which
[#442](https://github.com/scttfrdmn/burroughs/issues/442) found does not resolve — §9 G-3 is the
neutrality guarantee, and 237 sites cite it for this proposition. Not repaired here; the resolution may
be a contract amendment, which is Scott's.)*

**Option 5 — defer, and leave the 28 unsupported.** Legitimate until 2026-08-19 and not after: with the
ladder read off the `unsupported` column, deferring the largest item in it is deferring v0. Recorded
because it was the status quo for the length of this issue's life and the thing that changed is a
ruling, not the code.

## Forecast, pre-registered

Before any line is written, and the *controls* are forecast too — the omission #439's forecast was
scored honest-but-incomplete for:

- **`unsupported` −28 in both lanes**, to **29**. The default lane's 28 land in **`gated`**, not
  `pass`, since GC is off — stated so the column's fall is not read as 28 passes. `gated` 4159 → 4187.
- **Default lane `fail` stays 0**, and `execFailCeiling` is the tripwire that says so.
- **All-on lane**: `extern.wast`'s 6 → pass (`:39/:56` are `HostRef` identity comparisons; `:53/:54`
  are I31/Struct patterns over a round trip that is an identity by construction; `:42` is a bare host
  reference argument at `anyref`). The other 22 — `array.wast` 21, `i31.wast` 1, `struct.wast` 1 — are
  **not forecast**, because the co-blocking probe has not been run over them and a bucket count is not
  a forecast (#161). So the all-on `fail` delta is **unsigned**, and `allOnFailCeiling` 38 is the bound
  that will be re-based with an account either way.
- **Controls expected to fire**: `unsupportedCeiling` (slack 0, must be re-based to 29),
  `allOnFailCeiling` (re-based with an account), `boundPopulation` if a bound is added,
  `TestValTypeNamedConstantsAreNotAlias`/`declaredValTypes` **only if** `binary` gains a name — on the
  premise correction above it should not, and if it does, that premise was wrong and the PR says so.
  `TestGatedVectors`' allowances are **not** expected to move: these rows become ordinary gate declines.
  `publicpath_test.go`'s conversion sweeps and `kindNames`' domain control are expected to demand an
  arm for every new enum member, which is the control doing its job.
- **Ratio**: engine lines non-zero for the first time in three PRs — `internal/interp/value.go`,
  `convert.go`, `value.go` are all in the module path.

### Settled, measured

Recorded here because a forecast that is only scored in the PR that ran it is a forecast whose
result does not outlive the review. The account lives with the bounds it moved
(`internal/spec/spec_test.go`, the `unsupportedCeiling` and `allOnPassFloor` sections); the verdict
is:

- **Confirmed to the vector**: `unsupported` 57 → **29** in both lanes, the 28 landing in `gated`
  (4159 → **4187**), default-lane `fail` **0**.
- **Conservative in one direction, and named as that**: **all 28 pass all-on**, where the forecast
  claimed only `extern.wast`'s 6 and declined to forecast the other 22. `allOnPassFloor` +28 to
  **65042**; `allOnFailCeiling` **did not move** from 38, and that zero is *derived* rather than
  observed — the 28 were `unsupported`, a third verdict, so none of them was ever a red to lose.
- **Wrong in one premise, kept**: the forecast's per-file split of the 22 (`array.wast` 21) was the
  *shape* census, not the file census; measured, the 28 spread over six files. Both total 28 and
  neither is a re-spelling of the other, because a `(ref.array)` expectation appears in three files.
- **One consequence the forecast did not name at all, and the gate did**: `HostRef` — the constructor
  this record's "one more as an *argument*" consequence rests on — had no caller in the module, so the
  deadcode gate refused it. The allowlist for that gate is empty by policy, so the resolution is a
  production caller: `ParseValue` now reads `ref:host:0:(ref null any)`, which also closes a
  print/parse asymmetry this record opened without noticing — `String` gained four payload spellings
  and no reader, outside the round-trip control's domain because that domain is the *Kind* vocabulary
  and a payload is not a Kind. A third exhaustiveness control
  (`TestEveryPayloadSpellingIsReadOrRefusedByName`) covers the parse seam, enumerating from
  `payloadPastEnd`.
- The controls fired as forecast, with one addition the forecast missed: `foreclosingLicensed`
  (`internal/testenv`) is keyed by line number, so editing `spec_test.go` invalidated twelve licences
  at once. That is the scheme working — it double-reports, unlicensed at the new line and stale at
  the old — but it belonged in the control list and was not in it.

## Consequences

- The public boundary can express every reference value the corpus writes as a **result**, and one
  more as an **argument** (`(ref.host N)` at `anyref`). It still cannot express a non-null funcref
  argument or a GC payload crossing outward, both of which stay stated gaps with measured populations
  of 0 (`Value.RefID`'s own comment).
- **A public enum is a compatibility surface.** Adding a member later is a minor-version fact under
  [0004](0004-versioning-and-contract-independence.md), which is why the member list here is the
  authority's whole constructor set rather than the four the corpus needs today.
- `Matches`' static-type gate survives this record and is a known incorrectness tracked as
  [#441](https://github.com/scttfrdmn/burroughs/issues/441) — declared and tracked, both halves, with a
  number that resolves rather than a promise that reads as one.
- The 28 leaving `unsupported` for `gated` is a **reclassification**, not a reward: the figure with a
  subject is the all-on lane's `extern.wast` 6, and 22 rows become askable for the first time.
