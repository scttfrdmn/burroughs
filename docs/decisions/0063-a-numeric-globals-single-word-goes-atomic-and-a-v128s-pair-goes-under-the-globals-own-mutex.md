# 0063 — A numeric global's single word goes atomic and a v128's pair goes under the global's own mutex

Date: 2026-09-04 · Status: **proposed** — no stamp exists to cite, and *a `Status:` field is a citation to
an approval*, so it stays open until one does. Nothing here needs one to proceed: this is mechanism, which
is product work and self-merges on a bound green, and it changes no gate's default. The choice among the
five options below is this document's, decided by the pre-registration on #573, which was written before
the mechanism existed to measure.

Filed against **[#573](https://github.com/scttfrdmn/burroughs/issues/573)**, whose third arm — the
reference slot — stays open. This document covers two of the three.

## Context

`Invoke` is an exported method on `*Instance`, so two goroutines may call it on one instance. Nothing
gates that: it needs no threads proposal, no `shared` flag and no `Spawn` to exist, and
`TestAtomicRmwIsNotObservablyTornAcrossThreads` already does it. So a `global.set` on one thread races a
`global.get` on another, reaching the same `*global` through `globalFor`. Both arms dispatch inside
`get`/`set` rather than at the opcode
([`internal/interp/exec.go:Instance.runFrame`](../../internal/interp/exec.go), the `0x23`/`0x24` arms),
so there is exactly one path per shape and no fast path around it — checked, because a
benchmark measuring a mechanism nothing calls would print a null and read as a free mechanism.

**Two defects, not one, and they need different oracles.** This is why the decision splits rather than
picking one mechanism for the struct:

- **A numeric global is one `uint64`.** On both architectures this engine targets, an aligned 8-byte
  store is indivisible, so no value a reader observes distinguishes a plain field from an atomic one.
  What is wrong is that it is an unsynchronised read/write pair in the Go memory model, where the result
  is *undefined* rather than merely stale. The instrument that answers that is `-race`, and the witness's
  verdict therefore lives in CI's `race` **step** — inside the `build` job, on both matrix
  architectures — rather than in `make check`. Not in a `race` *job*: there is no such job, which is
  what this document and the witness both said first (recorded under Consequences).
- **A v128 global is two `uint64` fields** (decision 0024, grave #239's storage half), and `set` wrote
  them as two assignments. A `global.get` interleaved between them returns a vector built from the low
  half of one write and the high half of another — a value no `global.set` in the module ever wrote.
  That is observable **as a value**, needing no detector, and 452 of 200 000 reads witnessed it.

The third arm, `ref`, is 40 bytes — 2.5× a v128 — which falsifies the premise the ruling on #573 rested
on (*"the only case above word width, and rare in practice"*), so where the cheap/expensive boundary
belongs reopens as its own question and is not answered here.

## Options

1. **Leave the fields plain.** The status quo, and the defect. Rejected: the v128 arm hands back values
   the module never wrote, which is a wrong answer rather than a missing feature.
2. **One mutex over all three fields.** Uniform and simple. Rejected on cost shape, not on correctness: a
   lock on every `global.get` of a hot numeric global pays two-word serialisation for a one-word access
   that needs none, and the numeric arm is the common case.
3. **Atomics per word, no lock.** Fixes the numeric arm and leaves the v128 arm torn: two atomic loads
   with nothing between them still take `numHi` from one `set` and `num` from the next. Word atomicity is
   not pair atomicity. Rejected as a half fix that reads as a whole one.
4. **`atomic.Uint64` for the words, plus the global's own mutex across the v128 pair.** Chosen.
5. **A seqlock for the v128 pair.** Not rejected — deferred, and the measurement below is what promoted
   it from an alternative to a *named successor* with its own issue
   ([#625](https://github.com/scttfrdmn/burroughs/issues/625)). Two mechanisms in one revision is the
   comparison [#618](https://github.com/scttfrdmn/burroughs/issues/618) records `ab.sh` cannot make, and
   Scott's ruling on #573 granted the choice rather than demanding a bake-off: *"a lock or seqlock,
   implementer's choice, measured in the slice."* The slice owed the mechanism's cost, not a comparison.

## Choice

Option 4. `num` and `numHi` are `atomic.Uint64`; `mu sync.Mutex` is held across both stores in `set`'s
v128 arm and both loads in `get`'s and `value`'s.

**The type carries the discipline rather than a comment carrying it.** `atomic.LoadUint64(&num)` over a
plain `uint64` field would leave every plain read compiling silently, so the rule would live in prose and
be enforced by review; as `atomic.Uint64` a plain read does not build. Decision 0058 makes the same
argument one struct over.

`mu` is unconditional rather than allocated for v128 globals only — a `*sync.Mutex` is the same eight
bytes plus an allocation and a nil check — and is taken on the v128 arm alone. The numeric arm's freedom
from it is the whole point of the split.

## The measured board

`internal/interp/globalbench`, four rows, three arms (base / head / **null**), 10 rounds under grave
#552's interleaved protocol via `scripts/ab.sh --graft --null`. Each row is 1.6M global ops per `Invoke`
(100 000 trips × 16 unrolled), at 43% density — stated and asserted by
`TestTheArmsAreDenseAndDifferOnlyInTheGlobalOp`, because a sparse loop hides a real cost under dispatch
overhead and prints a null that is the instrument reporting its own blindness.

The arms differ at the **source**, not merely by binary hash: `main`'s `global.go` contains zero
`mu sync.Mutex` and zero `atomic.Uint64`; `HEAD`'s contains one and four.

### amd64 — `janus.local`, `measured` group, task 11, 0 concurrent at submit

```
goos: linux · goarch: amd64 · cpu: Intel(R) Core(TM) i9-9960X CPU @ 3.10GHz
           │    base     │                head                 │                null                │
           │   sec/op    │   sec/op     vs base                │   sec/op     vs base               │
GetI64-32    31.58m ± 0%   31.38m ± 0%   -0.63% (p=0.000 n=10)   31.63m ± 0%  +0.16% (p=0.029 n=10)
SetI64-32    30.47m ± 0%   35.22m ± 0%  +15.57% (p=0.000 n=10)   30.44m ± 0%       ~ (p=0.089 n=10)
GetV128-32   38.48m ± 0%   54.54m ± 0%  +41.73% (p=0.000 n=10)   38.48m ± 0%       ~ (p=0.971 n=10)
SetV128-32   37.17m ± 0%   61.99m ± 0%  +66.79% (p=0.000 n=10)   37.20m ± 0%       ~ (p=0.280 n=10)
geomean      34.25m        43.97m       +28.37%                  34.27m       +0.04%
```

**The null arm is what makes this board readable** — *compare the floor to the bar*. Its largest
excursion is +0.16%, against a smallest confirmed effect of 15.57%: the floor is roughly two orders of
magnitude below the bar, so every row here adjudicates.

### arm64 — dev box, **not queued** (`lab-group: none`)

```
goos: darwin · goarch: arm64 · cpu: Apple M4 Pro
           │     base     │                head                 │                null                 │
GetI64-12    60.05m ± 12%   64.17m ±  6%       ~ (p=0.280 n=10)   62.66m ±  8%       ~ (p=0.280 n=10)
SetI64-12    55.96m ± 20%   56.49m ±  9%       ~ (p=0.971 n=10)   54.86m ± 11%       ~ (p=0.579 n=10)
GetV128-12   77.57m ± 11%   78.33m ± 10%       ~ (p=0.739 n=10)   75.82m ± 10%       ~ (p=0.280 n=10)
SetV128-12   71.08m ± 14%   73.33m ±  7%       ~ (p=0.631 n=10)   67.77m ±  7%       ~ (p=0.280 n=10)
geomean      65.61m         67.55m        +2.96%                  64.83m        -1.19%
```

Every row is a null, and **the reading is bounded rather than confirmatory.** The null arm's own
excursions run to 4.66% on unchanged code, so this board cannot resolve anything below roughly 5% and
cannot *confirm* a null either — a blind instrument reporting no effect is reporting its blindness. What
it does do is exclude a large one: a 41.73% effect on `GetV128` would be +32 ms, far outside a ±11%
interval at p=0.739. So the amd64 effect is genuinely absent on arm64, and that is a real finding, not a
resolution artifact.

`make ab` is local and unqueued by design — the dev box is not shared lab hardware — and the price is
visible here: the unqueued box's floor is ~25× wider than the queued box's. **That comparison is
confounded by hardware** (M4 Pro against an i9-9960X) and is not offered as a measurement of queueing.

### The rows decompose, which is what identifies the carrier

Attributing 41.73% to "the mutex" would be an inference. The three amd64 rows price three separate pieces
of added work, and the pieces predict the fourth:

| added work | absolute Δ | per global op |
|---|---|---|
| `SetI64` — one `atomic.Uint64.Store`, a locked `XCHG` on amd64 | +4.75 ms | **2.97 ns** |
| `GetV128` — `Lock`+`Unlock` only; an atomic *load* is a plain `MOVQ`, adding nothing | +16.06 ms | **10.04 ns** |
| `SetV128` — the same pair plus two stores | +24.82 ms | **15.51 ns** |

Predicted `SetV128` = 16.06 + 2 × 4.75 = **25.56 ms**, observed **24.82 ms** — a 2.9% residual. The
mutex pair's 10.04 ns is measured on the *read* row, where the atomic loads cost nothing, independently of
the store cost the i64 row prices. So the v128 cost is the lock, and decision 0063 puts the mechanism
where the cost is.

**Corroborated by a figure this slice did not take.** [ADR
0061](0061-grow-serialises-on-its-own-mutex-rather-than-a-compare-and-swap-over-the-descriptor-because-the-length-lives-in-two-places-and-only-one-is-in-the-descriptor.md)
measured a `Lock`/`Unlock` pair at **11.32 ns** on this same host, in a different package, against a
different subject. This board's 10.04 ns agrees within 11%, and the two were arrived at by unrelated
arithmetic over unrelated rows — which is the check a single decomposition cannot perform on itself,
since *a repair is confirmed by the authority* and here the authority is a measurement with no stake in
this one.

## The forecasts, against the board

Five were pre-registered on #573 before the mechanism existed. Two are confirmed, two are falsified, one
is confirmed on a reading the board supplies. **A favourable miss is still a falsified forecast**, and
none of these is restated to fit.

- **F1 — `GetI64` within noise. FALSIFIED, favourably.** Observed −0.63% at p=0.000, outside the null
  arm's +0.16%: a small but real *speedup*, not a null. The forecast's instruction-form claim holds (an
  atomic load is a plain `MOVQ` on amd64, an `LDAR` on arm64), so the mechanism does not explain a
  change in either direction, and code layout shifting under the struct's new fields is the likely
  carrier — **flagged, not asserted**; nothing here measures layout. F1's rollback named a *regression*
  from a lost register allocation, and a speedup does not meet it, so the rollback does not fire.
- **F2 — `SetI64` measurably slower, direction only. CONFIRMED.** +15.57%, p=0.000, and the locked
  `XCHG` the forecast named is priced at 2.97 ns per store.
- **F3 — `GetV128` measurably slower. CONFIRMED.** +41.73%, p=0.000, a lock pair appearing on a read
  path that had neither.
- **F4 — `SetV128` slower by roughly the same absolute amount as F3. FALSIFIED as worded; its purpose
  satisfied.** The deltas are 24.82 ms against 16.06 ms — a factor of 1.55, not "roughly the same." F4
  existed to decide whether the v128 cost is the identical lock/unlock pair added to both arms, since a
  cost appearing on one side only would mean the mechanism is not where this document puts it. The
  decomposition answers that question and the forecast's arithmetic did not: the pair is 16.06 ms of
  *both* v128 rows, and the entire excess is the two atomic stores at the rate the i64 row measures
  independently. What the forecast got wrong is that it treated the two v128 arms as differing only in
  the lock, when `set` also adds two stores that `get` does not.
- **F5 — the i64 and v128 deltas differ in kind rather than degree. CONFIRMED.** The numeric arm pays
  nothing on reads and one store on writes; the v128 arm pays a serialising pair on **both** sides. The
  v128 read path acquired a cost the numeric read path does not have at all.

## The rollback fired

Pre-registered: *"if F3/F4 show a v128 `global.get` dominated by the acquire, the seqlock is the named
successor, filed rather than swapped in-slice."*

`GetV128`'s entire +41.73% is the acquire — the atomic loads are free on amd64 — so a pure read pays a
41.73% penalty that is all serialisation, and two concurrent readers exclude each other though neither
writes. **Filed as [#625](https://github.com/scttfrdmn/burroughs/issues/625), and the mutex stays.** The
successor is not a foregone conclusion and #625 records the two things it must measure rather than
assume: the win is TSO-side (arm64 shows no such cost, and a seqlock reader there needs fencing the
mutex was providing for free), and a seqlock reader can be *worse* under write contention, which
`globalbench`'s single-threaded arms do not price at all.

## Consequences

- **The reference arm is still unsynchronised**, and bounded rather than escalated: Go writes
  pointer-sized fields whole, so no torn `ref` carries a forged pointer — at worst `Null: false` against
  three nil pointers, a nil dereference. That is not the address-zero read a torn *slice header* produces
  ([#622](https://github.com/scttfrdmn/burroughs/issues/622)). `mu` would cover it in one line; the line
  is unwritten because a lock on every `global.get` of a hot `externref` is a different cost profile
  from the one the ruling weighed, and #573 stays open for it. (This sentence and the one at the top of
  this document both claimed #573 carried `decision-needed:scott`; the label is not on it, and **the tree
  may cite an issue but may not claim that issue's labels** — a label is tracker state, and no instrument
  in this repository has it in its domain. Corrected in #622's slice rather than by a postscript, because
  this document is still `proposed`.)
- **A v128 `global.get` is 41.73% slower on amd64 and unchanged on arm64.** Both figures are the price
  of returning values the module actually wrote; the previous behaviour was cheaper and wrong.
- **Two witnesses, two oracles, and neither covers the other.**
  `TestAV128GlobalIsNeverAssembledFromTwoWrites` is a value-level oracle needing no detector, because
  the writer only ever stores lane-equal vectors. `TestANumericGlobalIsNotWrittenAndReadWithoutSynchronisation`
  is close to toothless without `-race`, which was **measured rather than trusted**: on the defect it
  passes without `-race` across three runs and fails under it, and the failing arm was the detector — its
  weak value arm never fired. Both carry a vacuity arm requiring the reader to have observed *both*
  written values, so a schedule that serialised the agents fails rather than passing quietly.
- **The pre-registration's line citations into `global.go` are stale, and are re-pointed here in ADR
  0047's form rather than edited.** A pre-registration is append-only; correcting it in place would be
  indistinguishable from amending a forecast. It cited ten coordinates across four sites — the
  construction, the two read paths and the write path — against a 222-line file the mechanism then grew
  to 302. **The coordinates are not reproduced here**, because the sentence needs the subjects and not
  the addresses, and a stale line number copied forward into a durable record is exactly
  [#497](https://github.com/scttfrdmn/burroughs/issues/497)'s population arriving one document later.
  The pre-registration names all four in its own prose, so nothing is lost by naming them the way that
  resolves: [`internal/interp/global.go:Instance.newGlobal`](../../internal/interp/global.go),
  [`internal/interp/global.go:global.get`](../../internal/interp/global.go),
  [`internal/interp/global.go:global.value`](../../internal/interp/global.go) and
  [`internal/interp/global.go:global.set`](../../internal/interp/global.go), plus the field the
  mechanism added, [`internal/interp/global.go:global.mu`](../../internal/interp/global.go).
  `TestSymbolCitationsResolveToADeclaration` resolves each against a declaration, which is the half a
  line number does not have — and the mapping is worth doing carefully rather than by proximity: the
  first draft of this bullet re-pointed the construction coordinate at `mu sync.Mutex`, because the new
  field sits near where the old line fell. On `main` that line was `newGlobal`'s composite literal.
  *Re-key by content, not by arithmetic*, and a coordinate into a file that moved is the case where
  content is the only thing there is. The coordinate itself is not quoted, here or above, for the
  reason the whole bullet exists.
- **Both this document and the witness named that verdict channel a `race` *job*, and there is no such
  job.** `race` is a step inside `build` (`.github/workflows/ci.yml`), which is why it runs on both
  matrix architectures and appears in no run's job list — that list is fuzz-smoke, lint, conformance,
  citations, build twice and vuln. Found by reading this branch's own CI verdict per job, which is
  exactly where the sentence sends a reader and exactly where it fails them: finding no `race` row,
  they cannot tell a skipped verdict from a misnamed one. **The wrong name survived every gate this
  slice ran** — `make check`, `make cite` and CI's `citations` job all went green with both sentences
  in the tree, because the citation sweeps resolve file paths, heading anchors and Go symbols, and a
  workflow's job names are in none of their domains. Recorded rather than quietly corrected, for the
  same reason as the bullet above, and with the sharper form of it: **a wrongly named verdict channel
  is checkable, so it reads as though somebody had checked it.** No instrument is proposed, and the
  reason is a measurement rather than a preference — sweeping the tree for named-job claims returns
  these two sentences and nothing else, both this slice's own and both unmerged, while the one place
  that had said it before ([`CHANGELOG.md`](../../CHANGELOG.md), the `make race` timeout entry) already
  says *step*. A sweep built now would be vacuous on `main` on the day it landed, which is a control's
  first failure mode and not its absence.
- **`make lab-ab`'s first product use found grave
  [#624](https://github.com/scttfrdmn/burroughs/issues/624)** — `ab.sh` hashed its arms with `shasum`,
  absent on the lab host, and reported the two empty hashes as *"the two arms are byte-identical."* The
  amd64 board above could not be taken until that was repaired, so the repair rides this slice.
