# 0054 — Every aligned guest memory access becomes atomic on the address already resolved, because a scoped gate is unavailable rather than unwritten

Date: 2026-08-31 · Status: **accepted** — stamped by Scott on the #568 review, relayed to
[a durable comment](https://github.com/scttfrdmn/burroughs/issues/567#issuecomment-5488009843). The
relay pattern is [0042](0042-the-interpreters-second-comparator-is-deleted-rather-than-tuned-and-the-criterion-is-five-rows-in-both-directions.md)'s,
and it is the pattern rather than a formality: an in-session stamp holds no artifact, and a `Status:`
field is a citation to an approval. Scott's own words on the independence question, since the relay was
written by the party the decision favours: *"the independence comes from the choice being reviewed
here."*

Filed against **#567**. **Supersedes one bullet of
[0053](0053-tear-freedom-is-one-aligned-word-access-chosen-from-the-slices-own-host-address-because-0051-already-asserts-the-base.md)** —
its *"Plain accesses, not `sync/atomic`"* reasoning, which took this decision at mechanism tier on
grounds the standing ruling rejects.

**What stands from 0053 is its predicate, and this sentence is a correction of an earlier draft of this
ADR.** The draft said *"the rest of 0053 stands and is the base this builds on."* It does not:
`wordAligned` stands, and this ADR leans on it entirely — the host address it resolves is exactly the
address made atomic below — but 0053's *access helpers* do not. `loadWord`, `storeWord`, and
`guestWord16` have no caller once the two dispatch sites are rewired, and the `deadcode` gate said so by
name. **A supersession is easy to describe as an amendment when it is a deletion**, and the instrument
caught this one rather than the author; the corrected account is left in place of the wrong one rather
than overwritten, and it is also recorded where those functions stood (`internal/interp/memop.go`,
`internal/interp/atomic.go`).

## Context

### The standing ruling, which closed two of the four options before any measurement

Scott, on the #566 review:

> The principle is that **the guest's data races must not be the host's.** … If the byte loop races the
> detector against an atomic store, that byte loop is the defect, not an inconvenience the battery works
> around. Fix it, and the battery runs under `-race` with no exclusions, which makes any race report a
> real finding rather than expected noise.

That closes *keep plain accesses* (option 1) and *keep plain accesses and run the battery outside
`-race`* (option 4). It leaves **all guest accesses atomic** against **atomic only where a memory is
reachable by more than one thread**, and #567 was opened because 0053 had settled that at the wrong
tier.

### The measurement, and what it moved

Three arms in one binary — `plain` (0053 as merged), `cell` (routed through `atomicCell`, ADR 0051),
and a tuned arm doing the atomic instruction on the address already in hand — validated on arm64
against the pre-existing two-binary figures before the amd64 number was read. Tables in
[the measurement report](https://github.com/scttfrdmn/burroughs/issues/567#issuecomment-5486979517).

| | arm64 (`LDAR`/`STLR`) | amd64 (TSO) |
|---|---|---|
| aligned load, atomic instruction only | free | free |
| aligned store, atomic instruction only | free | **+13.72%** |
| `atomicCell`'s bookkeeping on top | ≈ +4.5pp load, +8.2pp store | ≈ +5.4pp load, +7.2pp store |

**The +4.70%/+8.13% that option 2 was priced at, and that Scott declined, was the cost of one
implementation and not of the property.** `atomicCell` recomputes the effective address and the bounds
check its caller already did, then shifts and masks; that is the ≈5–8pp, and it is roughly
architecture-independent because it is a fixed count of extra instructions rather than anything about
fences. What survives as the price of atomicity itself is amd64 aligned stores alone.

## Decision

**Every aligned guest memory access becomes atomic, using the atomic instruction on the address
`wordAligned` already resolved** — widths 4 and 8 directly, widths 1 and 2 through `atomicCell`'s
CAS loop on the containing 32-bit word, because `sync/atomic` has no narrow operations.

Soundness for the direct case is not a measurement convenience: `wordAligned` checks the **host**
address, not the guest offset —

```go
// internal/interp/memop.go:wordAligned
return uintptr(unsafe.Pointer(&bs[0]))&uintptr(width-1) == 0
```

— which is exactly `sync/atomic`'s alignment requirement, so an atomic operation on that pointer is
well-defined at both widths.

### Why not scoped: the gate is unavailable, not unwritten

Scoped needs a sound notion of *"this memory can be reached by more than one thread."* Two candidate
gates are already closed:

- **`limits.Shared` is unsound.** `Spawn` refuses an instance with no shared memory and then runs the
  entry in the *same* instance, so a spawn-capable instance's unshared memories are reachable from two
  threads. The flag does not answer the question. (This is the same falsification `#568`'s tripwire
  guards, and it is why that comment in `allocate` now has one.)
- **Refusing to spawn on an instance holding unshared memories is rejected** by Scott: a module may
  legitimately hold both, so the refusal rejects valid programs.

That leaves reachability per memory, and it fails on *when* reachability becomes true:

```go
// internal/interp/thread.go, on branch v1/t1-spawn (#554) — not yet on main, which is why this is the
// only citation here that carries no line and no symbol: nothing in this tree can resolve either.
func (in *Instance) Spawn(entry uint32, arg int32, stackHint int) (ThreadID, error) {
```

**`Spawn` is ambient.** It is an exported method on any `*Instance`, with no spawn-capability declared
at instantiation. Nothing at instantiation time can therefore soundly conclude that a memory will stay
single-observer, because the embedder decides later and is not obliged to say so in advance. **A
conservative static gate is unavailable rather than merely unwritten** — the distinction that decides
this ADR, and it comes from reading T-1's code rather than reasoning about what a gate might look like.

Which leaves a per-access runtime check, and two things kill it:

1. **You cannot dodge a free instruction with a branch and come out ahead.** The measurement says the
   operation scoped would be avoiding costs nothing measurable on three of the four rows. Paying a
   load-and-test on the hot path to skip it is a straight loss there, in exchange for a saving confined
   to amd64 aligned stores.
2. **The flag itself would be read racily.** A per-memory reachability flag mutated at spawn and read on
   every access puts a memory-ordering question *inside* the mechanism whose purpose is to settle memory
   ordering. Scott: *"the clincher."*

### Why the trade is bought — Scott's grounds, which supersede the ones this ADR was drafted with

The ADR as first argued priced the purchase as **an exclusion-free battery**: #10 runs under `-race`
with no exclusion list, so every report on it is a real finding rather than expected noise. That is a
test property, and Scott had already declined to buy a smaller number for it.

**His grounds are different and stronger, and they are the operative ones:**

> my refusal rested on the corrected premise that only a test property was at stake. Your finding
> changes that. With no sound gate available the racy region can't be confined at all, so the
> alternative isn't cheaper plain accesses — it's a permanently unbounded racy region that #10 could
> never certify anything against. That's foundational, not testability.

The narrower claim is kept rather than overwritten, because the record of *why* a price was paid should
show which argument carried it. The counterfactual to paying +13.72% on amd64 aligned stores is not
cheaper plain accesses; it is a racy region with no boundary anyone can state.

## Consequences

- **amd64 aligned stores pay +13.72% uncontended**, and that is the honest headline cost. arm64 pays
  nothing measurable on any row.
- **Narrow widths (1 and 2 bytes) pay `atomicCell`'s CAS loop, unmeasured**, and the engine-wide cost
  therefore sits somewhere between the tuned and the `atomicCell` arms, weighted by a width mix nothing
  here measures.
- **Every figure above is uncontended**, measured single-threaded with no cross-core traffic. They are a
  **floor** for the contended case, and #10's battery is contended by construction.
- **The unaligned path still has no atomic mechanism at all.** `atomicCell` assumes alignment, an
  unaligned 4-byte access can straddle an 8-byte boundary, and pure Go has no 16-byte CAS. The realizable
  design is per-word atomic touches — report-free without being atomic, tearing exactly where the
  proposal already permits it — and it is unwritten. This ADR does not conjure it.
- **#10's coverage statement is fixed in advance by the stamp: aligned only, with unaligned named as
  uncovered.** Stated in the battery rather than left to be inferred from what it happens to contain,
  because a suite's silence otherwise reads as coverage.

### Amendment, 2026-09-04 — the covered population is typed word accesses, not aligned ones (#627)

The decision stands and nothing about the mechanism changes; what is corrected is **this ADR's account of
which accesses it reaches**, measured in the tree while #10's B-MM-2 witness was being redesigned and filed
as [#627](https://github.com/scttfrdmn/burroughs/issues/627).

- **The title's "every aligned guest access" is true of typed word accesses and false of the tree as a
  whole.** For the typed path the reach is *wider* than the bullets above imply — `atomicLoadWord` /
  `atomicStoreWord` serve widths 4 and 8, and `atomicCell` covers 1 and 2, so all four aligned widths are
  atomic. But the **bulk family** (`memory.fill`, `memory.copy`, `memory.init`) and the **SIMD family**
  (`v128.store`, `v128.store*_lane`, the SIMD reads) go through plain `copy` and plain byte loops at *every*
  alignment, and this ADR's mechanism never touches them.
- **So the partition is not alignment.** It is *typed word access* versus *bulk and SIMD*, and the second
  group is uncovered at every address. Three of the six sites are reachable from instructions needing **no
  gate at all** (the `0xFC` bulk family), so the uncovered region is not confined to a proposal's blast
  radius the way ADR 0051's rejected option C priced it.
- **The bullet above about the unaligned path is true and was read as exhaustive.** It named the one
  uncovered region this ADR had thought about, and a reader — including this project, in #10's stamped
  coverage sentence — took the complement to be covered. *An unmeasured complement is not an empty one*: the
  region was never measured, and naming one part of it made the rest invisible.
- **The title is not being changed, deliberately.** Every citation to this ADR resolves through its
  filename, and a rename would break them all to repair a scope that a Consequences bullet can state
  precisely. What the title claims is therefore read as scoped by this amendment, which is why the amendment
  sits here rather than in a successor ADR.
- **Whether the bulk and SIMD families join the atomic regime is #627's question and is not decided here.**
  The trade is different in kind from the one this ADR priced: a `memory.copy` of a page is one `copy` today
  and would become a per-word atomic loop, so it is a bulk-throughput cost with no figure in hand, and it is
  Scott's the way #567 was.
- **One thing the gap is already good for, stated so it is not mistaken for a silver lining.** #10's
  `b-mm-2-sibling-field-after-wake` uses `memory.fill`'s plain write as the **carrier** for a `-race`
  verdict, because a detector needs one non-atomic side to have anything to say. That makes #627's repair a
  change that would silently void a litmus case, which is recorded in the pre-registration and on both
  issues. It is a use for the gap, not an argument for keeping it.
- **This does not discharge #568's tripwire, and the reason is a distinction worth keeping.** The
  tripwire guards `allocate`'s *reservation* — whether an unshared memory reserves its maximum so `grow`
  cannot move the backing array. An atomic access to a **relocated** array is a correct atomic operation
  on the wrong memory, because the slice header is read non-atomically: stale base paired with fresh
  length (#556). Atomicity of the element access does not fix a stale base pointer, so #554 still owes
  *reserve for every memory an executing instance can reach*.
- **Declared spawn-capability at instantiation is closed for now**, on Scott's stamp: it is public API
  surface, which §0 makes partisan, and v0's boundary has just shipped. It stands as **the one change
  that would reopen scoped**, recorded here so that a later reader finds a closed door rather than an
  absence.

## Pre-registration for the implementation, and the rollback

One ADR earns one implementation, and the implementation's own numbers can falsify the reason it was
chosen — the tuned path was picked over routing through `atomicCell` precisely because the measurement
separated them, so shipping something that measures like `atomicCell` would mean the tuning was lost in
translation.

Registered before the mechanism is written, on `membench` under grave #552's protocol:

1. `LoadAligned` and `StoreAligned` on arm64: **no significant delta** against `main`.
2. `LoadAligned` on amd64: **no significant delta**.
3. `StoreAligned` on amd64: **significantly positive, and near +13.72%** — call the band +8% to +20%.
4. All four unaligned rows, both architectures: **no significant delta**. They are untouched by
   construction and are the noise reference.

**Rollback:** if the aligned rows land at or above the `atomicCell` arm's figures (≈+6% amd64 load,
≈+21% amd64 store, ≈+4.5%/+8.2% arm64), the implementation is routing through the bookkeeping this ADR
chose against, and it is reverted rather than tuned in place — the measurement that distinguished the two
arms already exists, so there is no excuse for shipping the wrong one and calling it close enough.

**If (3) comes out flat**, the amd64 store attribution to the locked operation is wrong somewhere in the
translation from measurement arm to mechanism, and that is a finding to report before the mechanism
lands, not a favourable result to bank. A forecast beaten is a forecast falsified.

## What came out

Ten interleaved rounds per architecture, `-benchtime=300x`, both arms compiled to binaries up front and
their hashes checked distinct before any round ran (grave #552's protocol). arm64 is the dev box
(Apple M4 Pro); amd64 is native x86-64 on `janus.local` through `scripts/xcheck-amd64.sh`, which reported
`verdict from NATIVE x86_64 (janus.local), exit 0` — not the QEMU container, and named because *a PR
asserting a cross-architecture claim states which instrument confirmed it*.

The arms are `main` at `3135a29` against `551b3f7`, the commit that carries the mechanism. The commits
after it in this slice touch markdown only, so the Go is byte-identical and these figures describe the
engine as merged — the same reason a re-run green over a `CHANGELOG.md`-only diff refutes nothing, read
in the direction where it licenses rather than withholds.

```
arm64 (Apple M4 Pro)          base = main @ 3135a29     new = 551b3f7
LoadAligned-12      28.10µ ± 2%   28.15µ ± 3%       ~ (p=1.000 n=10)
LoadUnaligned-12    28.59µ ± 2%   28.81µ ± 1%       ~ (p=0.436 n=10)
StoreAligned-12     18.82µ ± 5%   18.28µ ± 7%       ~ (p=0.218 n=10)
StoreUnaligned-12   18.63µ ± 6%   18.92µ ± 5%       ~ (p=0.469 n=10)
geomean             23.04µ        23.01µ       -0.12%

amd64 (Intel i9-9960X @ 3.10GHz, native)
LoadAligned-32      59.18µ ± 1%   59.40µ ± 1%        ~ (p=0.481 n=10)
LoadUnaligned-32    59.09µ ± 2%   60.36µ ± 3%        ~ (p=0.165 n=10)
StoreAligned-32     38.88µ ± 4%   42.83µ ± 4%  +10.16% (p=0.000 n=10)
StoreUnaligned-32   42.08µ ± 7%   41.07µ ± 4%        ~ (p=0.436 n=10)
geomean             48.91µ        50.11µ        +2.46%
```

**All four registrations hold.** (1) arm64's two aligned rows are `~`. (2) amd64's aligned load is `~`.
(3) amd64's aligned store is significantly positive at **+10.16%, p=0.000**, inside the registered +8%
to +20% band. (4) All four unaligned rows are `~` on both architectures, which is the noise reference
doing its job — they are untouched by construction and they read untouched.

**The rollback did not fire, and the margin is what it was registered to see.** Its trigger was the
aligned rows landing at the `atomicCell` arm's figures (≈+6% amd64 load, ≈+21% amd64 store): the load is
flat rather than +6%, and the store is +10.16% rather than ≈+21%. The tuning survived translation.

### The stamp was given against the worse number, which makes it safe a fortiori

**Scott stamped this decision against +13.72%; production measures +10.16%.** Recorded because the
direction matters and is easy to lose: a stamp given against a *worse* number is stronger than one given
against a better one. The price he agreed to pay — and gave his grounds for paying, above — is larger
than the price this mechanism actually charges, so nothing about the approval needs re-examining in the
light of the measurement. Had the arm read +10.16% and production +13.72%, the approval would have been
obtained under a figure the artifact then exceeded, and the stamp would be owed a second look. It runs
the other way. (Scott, on the #569 review: *"the decision is safe a fortiori … a stamp against a worse
number is stronger than one against a better one."*) Recorded by the actor who was told, so this slice
stays `Ratio-Class: carried` — *durability is not independence*.

### The 3.6pp between the arm and the mechanism, named rather than banked

The measurement arm read **+13.72%** on this row and the mechanism reads **+10.16%**. Both are inside the
band, so nothing here is falsified — but *a forecast beaten is a forecast falsified* applies to figures
too, and a favourable difference banked as a win never gets asked why it moved. **The two baselines are
not the same code.** The arm-selectable binary carried an environment-variable branch in the dispatch
path so that one binary could run three arms; the mechanism carries none. Its `plain` baseline was
36.61µ against 38.88µ here, so the denominators differ by more than the gap does, and a ratio computed
against a different baseline is a different ratio. That is the *available* explanation and it is not a
measured one: nothing here isolates the selector's cost, and no figure below should be read as having
done so. What is measured is the mechanism's own row, and **+10.16% is the number this decision costs**;
+13.72% was the arm's.

## Provenance of the instrument

The arm-selectable binary was a throwaway on `measure/0567-arm-selectable`, committed so the figures
were reproducible and **never merged** — it puts an environment variable in a hot dispatch path. It is
deleted with this ADR, which is why the citations above point at the measurement report's comment rather
than at a branch that will not exist. Its selector was witnessed twice per host before any figure was
read: a six-injection battery with `-count=1` (three arms each, expected reddening only), and a
behavioural `-race` witness whose expectation inverts by arm.

`Ratio-Class` on this slice is **carried**, not `ordered`: the stamp is relayed durably above and the
ADR's `Status:` properly cites it, but the relay was written by the actor, and *durability is not
independence*. Nothing turns on the trailer either way, so it takes the conservative side.
