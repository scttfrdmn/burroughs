<!-- Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0 -->

# 0066 — A reference global's forty-byte value is published through an atomic pointer, because reads are the hot direction and a mutex taxes every get

Date: 2026-09-05 · Status: **proposed** — no stamp exists to cite, and *a `Status:` field is a citation to
an approval*, so it stays open until one does. Nothing here needs one to proceed: this is mechanism, which
is product work and self-merges on a bound green, and it changes no gate's default. The mechanism was
**ruled** rather than chosen here — Scott, on the #626 report — and this document is the tombstone for
that ruling plus the measurement it ordered.

Filed against **[#573](https://github.com/scttfrdmn/burroughs/issues/573)**, whose third arm this is.
[ADR 0063](0063-a-numeric-globals-single-word-goes-atomic-and-a-v128s-pair-goes-under-the-globals-own-mutex.md)
covered the other two and said in terms that this one stayed open.

## Context

`Invoke` is an exported method on `*Instance`, so two goroutines may call it on one instance. Nothing
gates that — no threads proposal, no `shared` flag, no `Spawn` — and `TestAtomicRmwIsNotObservablyTorn
AcrossThreads` already does it. So a `global.set` on one thread races a `global.get` on another, reaching
the same `*global` through `globalFor`. 0063 made the numeric word atomic and put the v128 pair under the
global's own mutex. The reference slot was left plain, and its comment said so.

**A `ref` is 40 bytes**, pinned by `TestRefWidthIsMeasuredNotAssumed`: two discriminator bools, two
`uint32`s and three pointers. `g.ref = st.popRef()` is one struct assignment the compiler lowers to five
word stores, so a concurrent `global.get` can pair one write's discriminator with another write's payload
— the v128 arm's defect at 2.5× the width. Bounded rather than escalated: Go writes each pointer-sized
field whole, so no torn `ref` carries a forged pointer; what it carries is `Null: false` against nil
pointers, which is a wrong value and at worst a nil dereference, not the address-zero read a torn *slice
header* produces ([#622](https://github.com/scttfrdmn/burroughs/issues/622)).

**Why this reopened rather than following 0063's v128 arm.** The ruling that settled v128 priced it as
*"the only case above word width, and rare in practice"*, and both halves of that are false here: a `ref`
is wider, and a hot `externref` `global.get` is not rare. Scott, on the #626 report:

> I priced v128 as "the only case above word width, and rare in practice." A ref is 40 bytes — 2.5× a
> v128 — and a hot externref `global.get` isn't rare.
>
> **Ref globals take an `atomic.Pointer` to an immutable ref value.** `get` is one atomic load; `set`
> allocates and swaps. Reads are the hot direction on a global and writes are rare, so this keeps the
> common path free where a mutex would tax every get. Pre-register the cost with a rollback to mutex if
> set-heavy rows regress past the bar, measured in the slice.
>
> Don't churn v128 — it shipped with a mutex and measured acceptably. Note the inconsistency and let the
> ref measurement say whether v128 should follow later.

Recorded on the issue by the actor the ruling was given to, so it is durable and **not independent** —
*durability is not independence* — and every commit resting on it stays `Ratio-Class: carried`.

## Options

1. **Leave the field plain.** The status quo and the defect: an unsynchronised read/write pair over 40
   bytes, undefined in the Go memory model and genuinely tearable. Rejected.
2. **Extend `mu` over the reference slot.** One line, and the field is already beside a mutex the v128 arm
   uses. Rejected on cost *shape* rather than correctness, and rejected by the ruling above: every
   `global.get` of a hot `externref` would pay an acquire for a write that mostly does not happen. 0063's
   own board prices that pair at 10.04 ns per access on this host, which is what a read path would take on.
3. **Five atomic fields.** Word atomicity is not struct atomicity — the same half-fix 0063 rejected for
   v128 for the same reason, and here it needs five loads with nothing between them. Rejected.
4. **`atomic.Pointer[ref]` to an immutable value. Chosen**, by ruling. `get` is one load and a copy out of
   a cell nothing will overwrite; `set` allocates a fresh cell and swaps the pointer. The read path pays a
   `MOVQ` on amd64 and the write path pays an allocation.
5. **A seqlock over the five words.** The same successor the v128 arm has
   ([#625](https://github.com/scttfrdmn/burroughs/issues/625)), and not taken here for the ruling's
   reason: an atomic pointer already leaves the read path free, so the seqlock's advantage over *this*
   mechanism would be avoiding the allocation on the write path, which is a different trade from the one
   #625 is about.

**The immutability is held by the accessors, not by prose.** `storeRef` takes a `ref` *by value* and
stores the address of its own parameter, so the published cell is reachable only through the pointer and no
caller — including the one whose value was copied — can write to what a reader holds. That is decision
0065's shape one struct over: a reader holds a whole value that nothing will overwrite, so the load is the
whole synchronisation. `loadRef` is the only read.

**A non-nil pointer is an invariant of construction, established in one place.** `newGlobal` stores
unconditionally for every shape, including the numeric ones that never read the slot, which costs one
40-byte cell per global and buys `loadRef` the right to dereference without a nil check. The one
non-production constructor — a composite literal in `table_test.go` — routes through `storeRef` for the
same reason, and its comment says which invariant it is satisfying.

## The measured board

`internal/interp/globalbench`, the two pre-registered rows `GetRef` and `SetRef`, three arms (base /
head / **null**), 12 rounds under grave #552's interleaved protocol via `scripts/ab.sh --graft --null`.
Each row is 1.6M global ops per `Invoke` (100 000 trips × 16 unrolled) at 43% density, stated and
asserted by `TestTheArmsAreDenseAndDifferOnlyInTheGlobalOp` — a sparse loop would hide a real cost under
dispatch overhead and print a null that is the instrument reporting its own blindness.

**The base arm is the defect, not an alternative mechanism**, and that was said before any number
arrived. `main`'s plain field is wrong, so a delta against it is a *cost of correctness* and not a
regression. The comparison this decision actually faced — atomic pointer against mutex — is not a
measured arm, because [#618](https://github.com/scttfrdmn/burroughs/issues/618) records `ab.sh` cannot
A/B two mechanisms inside one revision. The mutex comparator is 0063's decomposed figure, **10.04 ns per
access**, corroborated within 11% by ADR 0061's independent 11.32 ns on this same host: a predicted
comparator, stated as predicted.

### amd64 — janus.local, `measured` group, task 12, 0 concurrent at submit

```
lab-host: janus
lab-group: measured
lab-task-id: 12
lab-concurrent-at-submit: 0
goos: linux
goarch: amd64
pkg: github.com/scttfrdmn/burroughs/internal/interp/globalbench
cpu: Intel(R) Core(TM) i9-9960X CPU @ 3.10GHz
          │    base     │                 head                 │                null                │
          │   sec/op    │   sec/op     vs base                 │   sec/op     vs base               │
GetRef-32   38.49m ± 0%   45.11m ± 0%   +17.19% (p=0.000 n=12)   38.51m ± 0%       ~ (p=0.319 n=12)
SetRef-32   43.73m ± 0%   95.40m ± 0%  +118.15% (p=0.000 n=12)   43.70m ± 0%       ~ (p=0.347 n=12)
geomean     41.03m        65.60m        +59.89%                  41.02m       -0.01%
```

The null arm's binary hashed **equal** to base's (`3bdbd1ea…`) and head's differed (`2fc3678e…`), which is
what makes the third column a floor rather than a third mechanism. **The floor is narrower than the bar by
two and a half orders of magnitude** — the null excursions are +0.05% and −0.07%, neither significant
(p=0.319, p=0.347), against effects of +17.19% and +118.15% at p=0.000 — so this board adjudicates both
rows. Where a floor and an effect meet, a board does not adjudicate; that is not this board.

Per-access figures below are **derived** from the row times by the row's own advertised density, 1.6M
global ops per `Invoke` (100 000 trips × 16 unrolled), and are stated as derived because benchstat printed
the millisecond row and nothing else:

| row | base | head | delta |
|---|---|---|---|
| `GetRef` | 24.06 ns | 28.19 ns | **+4.14 ns** |
| `SetRef` | 27.33 ns | 59.63 ns | **+32.29 ns** |

### arm64 — dev box, not queued (`lab-group: none`)

```
lab-host: terror
lab-group: none (not queued)
lab-task-id: none (not queued)
lab-concurrent-at-submit: unknown (not injected by the submitter)
goos: darwin
goarch: arm64
pkg: github.com/scttfrdmn/burroughs/internal/interp/globalbench
cpu: Apple M4 Pro
          │    base     │                 head                  │                null                │
          │   sec/op    │    sec/op     vs base                 │   sec/op     vs base               │
GetRef-12   23.95m ± 5%   25.07m ±  6%    +4.66% (p=0.010 n=12)   23.38m ± 3%       ~ (p=0.178 n=12)
SetRef-12   24.00m ± 7%   80.41m ± 15%  +234.96% (p=0.000 n=12)   24.29m ± 7%       ~ (p=0.799 n=12)
geomean     23.98m        44.89m         +87.24%                  23.83m       -0.61%
```

Same hash discipline: null equal to base (`6ec7d43e…`), head distinct (`b60489ae…`). **This board adjudicates
`SetRef` and does not adjudicate `GetRef`.** The `SetRef` effect is two orders of magnitude outside the null
arm's ±3–7% spread. The `GetRef` effect is +4.66% against a null arm whose own spread is ±3% and arms whose
spreads are ±5% and ±6% — *compare the floor to the bar*, and here they meet, so the p=0.010 says the medians
separate while the board cannot say the separation is the mechanism. Derived per access:

| row | base | head | delta | amd64's delta |
|---|---|---|---|---|
| `GetRef` | 14.97 ns | 15.67 ns | **+0.70 ns** | +4.14 ns |
| `SetRef` | 15.00 ns | 50.26 ns | **+35.26 ns** | +32.29 ns |

**The two columns disagree in opposite ways, and that is the most useful thing on either board.** The write
delta agrees *in absolute terms* across two unrelated microarchitectures — 32.29 ns and 35.26 ns, within 9% —
which is the signature of a runtime cost rather than an ISA cost. The read delta does not: 4.14 ns on the
i9-9960X against 0.70 ns on the M4 Pro, a factor of six. And it disagrees in the direction that rules out the
atomic-ness of the load as the carrier, because Go compiles `atomic.Pointer.Load` to a plain `MOVQ` on amd64
and to a load-**acquire** `LDAR` on arm64: if the instruction were paying for the delta, arm64 would be the
worse column, not the better one by 6×.

### The residual decomposes by allocation count, not by subtraction

`ab.sh` has no `-benchmem` pass-through, so this is a separate run: `go test -bench Ref -benchmem -count=1`
over both arms, base built from `main` in a throwaway worktree with head's `globalbench` grafted onto it the
same way `ab.sh --graft` does. **Allocation counts are what is recorded from it and its `ns/op` column is
not** — it is one unqueued round on a dev box, which cannot carry a timing figure, but an allocation count is
deterministic and does not need the queued slot.

| row | arm | B/op | allocs/op |
|---|---|---|---|
| `GetRef` | base | 744 | 12 |
| `GetRef` | head | 744 | **12** |
| `SetRef` | base | 744 | 12 |
| `SetRef` | head | 76,802,897 | **1,600,031** |

The base arm's 12 allocations and 744 bytes are the harness's own fixed cost, and their appearing unchanged
on head's `GetRef` row is the point: **the read path allocates nothing.** Head's `SetRef` row is 1 600 019
allocations above it, against a row that executes 1 600 000 `global.set`s — one cell per set, with the
remaining nineteen accounted for by `newGlobal` storing a cell for every global in the module and the
harness's own setup. At 76 802 897 bytes that is **48.0 bytes per cell**: a 40-byte `ref` does not get a
40-byte allocation, it gets Go's 48-byte size class, so 8 bytes of every cell are size-class padding.

**What the counter separates and what it leaves bundled.** It establishes that the write path's residual is
carried by allocation and the write path only — no subtraction needed, and no appeal to *"whatever is not the
store"*, since *an unmeasured complement is not an empty one*. It does **not** separate the allocation itself
from the collection of the 76.8 MB per `Invoke` that it produces; both are consequences of the same decision
and the counter cannot price them apart. The instrument that would is a `GOGC`-varied arm, which is a timing
measurement and would need the queued slot; it is not run here because R3 asks which side of the store the
majority sits on, and that is answered.

Two candidates survive for the read delta, and this board does not choose between them: the dependent load
(the copy's source address must now be loaded before the copy can start, where before it was a constant
offset from `g`) and the loss of static addressing off the `*global` itself. What is ruled out is allocation
(the counts above), call overhead (`internal/interp/global.go:loadRef` and `:storeRef` both inline, at all
four call sites), and the atomic instruction (the cross-architecture direction above).

## The forecasts, against the board

Four forecasts, written on #573 before the mechanism existed. **Two came back falsified**, and each is
scored as written rather than as intended.

- **R1 — `GetRef` within the null arm's excursion. FALSIFIED, and not narrowly.** +17.19% at p=0.000
  against a null excursion of +0.05% at p=0.319 — a factor of 330. Its stated ground was that *"an atomic
  pointer load is a plain `MOVQ` on amd64, and the 40-byte copy out of the field happens in both arms, so
  the mechanism adds nothing measurable to a read."* Both clauses are still true and the conclusion was
  wrong anyway, which is why the section above spends its length on ruling carriers out: the delta is
  neither allocation, nor call overhead, nor the atomic instruction.
- **R2 — `SetRef` measurably slower, direction only. CONFIRMED.** +118.15% at p=0.000 on amd64, +234.96%
  on arm64, +32.29 ns and +35.26 ns per set.
- **R3 — the allocation, not the store, is R2's carrier. CONFIRMED**, and by a counter rather than by the
  subtraction R3 offered. Its own test was *"the residual after subtracting 2.97 ns is the majority of the
  delta"*: 32.29 − 2.97 = 29.32 ns, which is 90.8%, so it passes on its own terms. The `-benchmem` arm is
  what makes that a finding instead of a remainder — one 48-byte cell per set and none on the read path,
  with allocation and collection named as still bundled together.
- **R4 — arm64 shows no `SetRef` effect above its own floor. FALSIFIED as written**, by the largest effect
  on either board: +234.96% at p=0.000, where R4 predicted nothing resolvable. The failure is a **drafting
  defect** and is recorded as one rather than repaired by re-reading: R4's own next clause — *"the
  allocation should be the only carrier that survives"* — is a claim about which *component* arm64 can
  resolve, and the sentence in front of it says instead that the row shows no effect at all. Those are
  different predictions and the scored one is the sentence that was written. What arm64's board does
  establish is reported above as a reading and not as a forecast, because a forecast rescued after
  measurement is not a forecast.

### The 30 ns bar was withdrawn by its own clause, and the ordering has a hole worth naming

The pre-registration set the rollback at *"`SetRef`'s per-op delta over plain exceeds 30 ns"*, derived
rather than picked, and it wrote its own kill switch in the same breath:

> **What would make the bar the wrong instrument, said now.** It credits the read side the entire mutex
> pair on every `get`, which R1 cannot verify against a mutex arm that #618 prevents building. If R1 comes
> out non-null the bar's derivation is unsound and the bar is withdrawn *before* being compared to a
> number, never after — the ordering is the only thing separating that from amending a threshold.

R1 came out non-null, so the bar is withdrawn on a condition written in advance. **The ordering guarantee
it asks for was not available, and pretending otherwise would be the exact substitution it was written to
prevent:** one `benchstat` invocation printed both rows, so R1's falsification and `SetRef`'s 32.29 ns
became visible in the same instant. And the number would have crossed: 32.29 > 30. Recorded plainly
because a withdrawal that quietly omits the number it escaped is indistinguishable from amending a
threshold, which is the thing the clause exists to forbid.

The bar is **not replaced with a second bar**. A threshold derived after seeing the numbers is the same
defect wearing the other hat. What replaces it is the crossover below, which is a *reading* of this board.

### The choice does not reverse, and its basis is restated as relative

Scott, on this board:

> **My rollback was aimed at the wrong row.** I wrote "roll back to mutex if set-heavy rows regress past
> the bar," and the regression landed on the read path. Owned.
>
> **R1's falsification doesn't reverse the choice, but it changes its basis.** My argument was that reads
> are *free* under `atomic.Pointer` where a mutex taxes every get. The honest argument now is that reads
> are *cheaper than the alternative* — +4.14 ns against the 11.32 ns `Lock`/`Unlock` pair #600 measured on
> amd64. Record R1 as falsified in the ADR and restate the basis as relative rather than absolute. A
> decision left resting on a falsified premise is the shape this project keeps digging back out.

So the ground of this decision, as amended: a `ref` `global.get` under an atomic pointer costs **+4.14 ns**
where the same read under `mu` would cost the mutex pair, and 4.14 is **2.7× cheaper** than the 11.32 ns
that pair measured on this host. Not free. Cheaper than the alternative, on the direction the mechanism
claims is hot. Recorded by the actor the ruling was given to, so `Ratio-Class: carried`.

### The crossover, which turns "reads dominate" from assumed into auditable

The premise the decision now rests on is a claim about workload, and it is the one thing on this page no
instrument here measures. It is at least *arithmetic*, and costs no benchmark. Over `R` reads and `W`
writes, with the mutex's per-access pair `m` applied to both directions:

    mutex:            m·(R + W)
    atomic pointer:   4.14·R + 32.29·W        (measured, amd64, per access)

The atomic pointer loses when `R/W < (32.29 − m) / (m − 4.14)`:

| `m`, per access | source | crossover `R/W` |
|---|---|---|
| 11.32 ns | ADR 0061, #600's measured `Lock`/`Unlock` on this host | **2.9 : 1** |
| 10.04 ns | ADR 0063's decomposed pair | **3.8 : 1** |

**The atomic pointer is the right mechanism above roughly 3.8 reads per write and the wrong one below
roughly 2.9**, with the band between them set by which mutex figure is believed — and the two are 11%
apart, so the band is narrow enough to act on. Two consequences worth stating in the same breath:

- At the pre-registration's own **2:1** — offered there as *"the most set-heavy ratio anyone could still
  call reads are the hot direction"* — the atomic pointer **loses under both comparators** (40.57 ns
  against 33.96, and against 30.12). So the premise as landed needs to be stronger than the
  pre-registration's own reading of it.
- Both crossovers are amd64-only. ADR 0063's arm64 board could not resolve a mutex pair below ~5%, so
  there is no arm64 `m` to put in this table, and arm64's own read delta of +0.70 ns would move the
  crossover sharply in the mechanism's favour if one existed.

Neither figure in the `m` column is measured against *this* mechanism in *this* revision — #618 records
that `ab.sh` cannot A/B two mechanisms inside one revision — so the crossover is a measured numerator over
a **predicted** denominator, and inherits the prediction's uncertainty entirely.

## Consequences

- **The race is closed and its witness has an oracle.**
  `TestARefGlobalIsNotWrittenAndReadWithoutSynchronisation` reports `WARNING: DATA RACE` against the
  pre-0066 shape and is clean against this one. **Its verdict lives in CI's `race` step inside the
  two-architecture `build` job, not in `make check`** — so a green local gate says nothing about this
  test's subject, and the verdict is two readings rather than one. It is a race-detector control for a
  different reason than the numeric arm's: a 40-byte value genuinely can tear, but no *guest-visible*
  value oracle exists at this gate set, since MVP lets a guest ask only `ref.is_null` about a reference it
  holds and telling two non-null references apart needs `ref.eq` or `call_ref`.
- **The price, stated in both directions.** Reads cost +4.14 ns on amd64 and +0.70 ns on arm64. Writes cost
  +32.29 ns and +35.26 ns, and one 48-byte heap allocation per `global.set` — a figure the write path did
  not previously have at all. A `ref` is 40 bytes and Go's size classes step 32 → 48, so **8 bytes of every
  cell are padding**; a `ref` at or under 32 bytes would drop a size class, which is an observation about
  where a future saving is and not a reason to reshape the type here.
- **Every global carries a cell, including the numeric ones that never read it.** That is the
  non-nil-by-construction invariant's price, paid once per global at instantiation, in exchange for
  `loadRef` needing no per-read nil check on the hot path. `newGlobal` is the only production constructor
  and stores unconditionally for every shape.
- **The decision now rests on an unmeasured workload premise, and the crossover is where to audit it.**
  Below roughly 3 reads per write this mechanism is the wrong one, and nothing in this tree measures the
  ratio a real embedding produces. That is a stated open flank rather than a defect:
  [#640](https://github.com/scttfrdmn/burroughs/issues/640) is where the premise can be falsified, and the
  falsification is cheap — a set-heavy `ref` global in a real module, against the crossover table above.
  Its repair, if it fires, is the one line ADR 0063 left one line away: cover the field with `mu`.
- **v128 stays under `mu`, and the ref board sharpens *why* rather than reversing it.** Scott's ruling
  asked this measurement to say whether v128 should follow. It says **no**, and for a reason that also
  bears on the successor: the write-side carrier here is the *allocation*, which a v128 cell would pay just
  as surely (one per `global.set`, in a smaller size class). Meanwhile v128's read side has a **measured**
  mutex cost — `GetV128` +41.73%, dominated by the acquire — which is a stronger read-side case than
  reference globals could make with a predicted comparator. So an atomic pointer would help v128's reads
  more than it helped these, and would hand it the same write-path allocation. **The mechanism that wins
  both directions is the seqlock**, already filed as the named successor
  ([#625](https://github.com/scttfrdmn/burroughs/issues/625)) and still scheduled behind
  [#10](https://github.com/scttfrdmn/burroughs/issues/10): free reads and no allocation. Moving v128 to an
  atomic pointer now would be a migration #625 undoes.
- **The two mechanisms above word width are now inconsistent on purpose.** A v128 pair goes under the
  global's own mutex and a reference slot goes through an atomic pointer, which is a difference a reader
  will notice in one struct. It is *noted rather than resolved*, per the ruling — `internal/interp/global.go`
  says so at the field — and #625 is what carries the question.
- **`internal/interp/globalbench` is now the six-row instrument** both pre-registrations named, and
  `TestTheArmsAreDenseAndDifferOnlyInTheGlobalOp` covers all six by taking its population from `arms`, so
  the reference pair joined the control by being declared rather than by an edit to the control.
