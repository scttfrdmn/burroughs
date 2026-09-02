# 0058 — The memory image is published through an atomic pointer, because reachability is not a spawn-time property

Date: 2026-09-01 · Status: **accepted** — Scott's ruling on the [#584](https://github.com/scttfrdmn/burroughs/pull/584)
report, quoted in full on [#575](https://github.com/scttfrdmn/burroughs/issues/575): *"Take option 3, the
atomically-published header. It's the dissolving shape I asked for — reachability becomes irrelevant
rather than computed."* Recorded here by the agent that was ruled on, which is durable but not
independent. The choice among four options is his; the pre-registration below is this document's, and
it was written before the mechanism existed to measure.

## Context

[ADR 0056](0056-the-no-move-mark-is-set-where-the-reservation-happens-and-grow-refuses-on-the-mark-because-spawn-can-establish-it-while-one-thread-exists.md)'s
second half makes `Spawn` walk the entry instance's import closure and mark every memory it finds, so
that `grow` never replaces a backing array a second thread could be reading. That rests on a
completeness premise written into `reachableMemories`' own doc — *"a `funcref` cannot name another
instance's function"* — and [#575](https://github.com/scttfrdmn/burroughs/issues/575) shows the premise
false, and false when it was written: grave #163 widened `ref` to a pair, `funcRefTarget` resolves
through `r.Inst`, and `call.go` says so in the same package. A table slot can hold a foreign funcref.

Two probes settled it, and the second is the one that decides this ADR. Probe 1: a module `M` whose
entry does `call_indirect` on a slot another module `O` filled by element segment reaches `O`'s
unshared, unreserved memory — `cap == len`, so `grow` takes the allocate-and-blit arm and moves it
under a running thread. Probe 2: **`O` was instantiated after `m.spawn` returned.** It did not exist
when the walk ran.

So *"widen the walk to follow tables and globals as well as import slots"* is not an incomplete fix,
it is a fix of the wrong kind. **Reachability is not a spawn-time property**, and no spawn-time
computation of any shape can be the soundness argument. What is left is to make relocation safe, to
forbid it, or to make it unnecessary.

The hazard is precise and it is memory safety rather than a value race. A slice header is three words;
`grow`'s moving arm writes all three. A concurrent reader can observe the **new length paired with the
stale pointer** and index past the end of the old array. The other mixed reading — new pointer, old
length — is harmless. The spec is explicit that a wasm data race is *not* undefined behaviour
(`relaxed.rst:248`), so an engine that turns a permitted guest race into a Go out-of-bounds read has
strengthened the guest's crime into its own.

## Decision

**A memory's contents are reached through an immutable descriptor published by one atomic pointer
store, and `grow` publishes a new descriptor instead of mutating the header in place.**

```go
// memImage is one published state of a memory's contents. Immutable once published.
type memImage struct{ bytes []byte }

type memory struct {
    img    atomic.Pointer[memImage]
    limits binary.Limits
    noMove bool
}
```

Every reader loads the descriptor and uses the slice it names. Every writer of the *shape* — only
`grow` — builds a fresh `memImage` and publishes it with one `Store`. The three-word header is never
observed mid-write, because it is never written again after publication: **a reader holds exactly one
descriptor, and every descriptor is internally consistent by construction.**

That is what makes reachability irrelevant rather than computed. There is no set of memories to
enumerate at spawn time, no walk to widen, and nothing that depends on which instances exist or when
they were built. The unsound premise is not repaired; it is removed from the argument.

**Growth keeps both arms and both are now safe.** Within reserved capacity the new descriptor names
the same array at a greater length — the pointer is identical, so even the old descriptor stays valid
and in bounds. Past reserved capacity the new descriptor names a fresh array; the old one remains
valid, in bounds, and fully readable for as long as any thread holds it, which is what removes the
out-of-bounds read. The `noMove` mark stays exactly as ADR 0056 left it, because it now answers a
narrower question — see the residual below.

## Options considered

The four are #575's, and the reasons three of them lose are the part a later reader needs.

1. **`grow` refuses to relocate whenever any thread is live**, on a counter wider than an `Instance`
   (an instance-scoped flag cannot work: `O` has no way to know `M` spawned). The walk survives as an
   optimisation and stops being the soundness argument. **Sound, cheap, and rejected for
   non-locality**: it makes one instance's `grow` fail because *some other instance* spawned, which an
   embedder cannot reason about and cannot avoid. Scott's ruling: *"non-local behaviour an embedder
   can't reason about."* It also changes a guest-visible answer — `memory.grow` returning `-1` where
   it would have succeeded — for programs that never spawn but share a process with one that did.
   **Retained as this ADR's rollback**, because it is the only other sound option whose cost on the
   single-threaded path is exactly zero.
2. **Reserve at instantiation whenever the threads feature is on.** No refusal logic at all: every
   memory carries the `sharedReservePages` ceiling from birth. Rejected because **it penalises
   programs that never spawn, which is backwards** — the feature being *enabled* is not the feature
   being *used*, and §0's partisanship is about the path the workload runs, not the path it could
   have. A guest that declares `(memory 1 65536)` and never starts a thread would be capped at 128
   pages by a decision about a capability it did not exercise.
3. **This: publish the header atomically.** Preserves every program's semantics — no guest-visible
   answer changes on any path, single-threaded or not — and dissolves the reachability question rather
   than computing it. Its cost falls on the path every board vector runs, which is why the
   pre-registration below is the whole of its acceptance.
4. **Refuse the spawn when the entry's closure can reach a table a foreign funcref could enter.**
   Rejected as not viable rather than as expensive: *"could enter"* is not a local property — probe 2's
   `O` did not exist yet — and a module that exports a table is ordinary, so the refusal would fire on
   the common case to guard the rare one. It is option 1's non-locality moved earlier and made worse.

**Why 3 over 1, beyond the ruling's own reason.** Option 3 plausibly shares a mechanism with
[#573](https://github.com/scttfrdmn/burroughs/issues/573)'s shared globals, where the hazard is the
same shape one level down — a multi-word value (`v128`) published without a single-store discipline.
One mechanism answering two problems is worth something on its own, and the two should be read
together rather than decided a week apart.

## The pre-registration, written before the mechanism exists

**The bar and the rollback are fixed here, before any number.** #567 is the precedent and the reason
this section is not optional: its estimate ran 4.7–8.1% and the tuned mechanism measured free on
loads, so an estimate is not a decision and a decision needs a bar it cannot move afterwards.

**The claim being tested is distributional** — *"publishing through a pointer does not cost the board
path"* — so the statistic is a summary and not the best or worst row, and **the same statistic goes on
both sides** (ADR 0057's rule, on Scott's ruling).

| | governing |
| --- | --- |
| population | `internal/interp/membench`'s 4 rows: load/store × aligned/unaligned, driving the real interpreter through `Invoke` |
| protocol | the three-arm rotated protocol ADR 0057 used — `old`, `new`, and a byte-identical `null` copy of `old` asserted equal by sha256, 12 rounds, arm *i* in slot *(i+r) mod 3* |
| effect | **geomean regression over the 4 rows**, `benchstat`, on `darwin/arm64` and `linux/amd64` |
| matched null | geomean over the same 4 rows, `old` vs the byte-identical copy |
| **bar** | **the geomean regression is ≤ 2.0% on both architectures** |
| per-row verdicts | Bonferroni-corrected α/4 = 0.0125, reported for every row in all three arms |
| secondary, reported and not governing | `internal/interp/rmwbench`'s 49 atomic rows, which read the descriptor too; and the max-row regression against the **max** null row, matched |

**Why 2.0% and not a number that would obviously pass.** The bar is a *trade*, not a threshold on
noise: option 1 costs exactly nothing on the single-threaded path and buys the same safety, paying
instead in non-locality that only affects programs sharing a process with a spawner. Charging every
program more than 2% to spare those programs a non-local `-1` is the wrong side of §0 — so above the
bar, the non-locality is the cheaper defect and the rollback is correct. Below it, option 3's
preservation of every program's semantics is worth the cost.

**The estimate, stated separately so that a failed estimate narrows rather than licenses.** One
dependent pointer load per guest memory access, plus an acquire barrier on `arm64` where Go's
`atomic.Pointer.Load` emits `LDAR` and on `amd64` where it is an ordinary `MOV`. So: **1–4% on the
aligned rows, worse on arm64 than on amd64**, and the direction is the falsifiable part — an effect
that is *larger on amd64* means the cost is not the barrier and the mechanism is not what is being
measured.

**Tuning is permitted before the rollback fires, and its shape is named now**: hoist the descriptor
load from per-access to once per memory instruction, or once per frame where the frame cannot observe
a `grow`. #567's tuned form (2′) is the precedent — a mechanism that fails its bar in the obvious
spelling and passes it in a tuned one is a mechanism, not a curve fit, provided the tuning is
pre-registered rather than discovered. Any *third* spelling is a new ADR.

**Rollback, if the bar is missed after tuning:** take option 1 — `grow` refuses to relocate whenever
any thread is live, on a process-wide counter — and document the restriction as a named engine limit
in the channel ADR 0056 built for exactly this, a counter incremented on that arm and no other, with
the excluded programs stated. The refusal is conforming: `memory.grow` reports failure in its result
and the reference itself fails a grow for reasons of its own (`memory.ml:60-67`).

## The measured result, read back against the bar above

Protocol as registered: 12 rotated rounds, three arms, the null arm asserted byte-identical to `old`
by sha256 before the first round; `darwin/arm64` Apple M4 Pro and `linux/amd64` Intel i9-9960X native
(`janus.local`, both arms cross-built by one toolchain and the harness `diff`-verified identical
between them, so only the engine differs).

| | `darwin/arm64` | `linux/amd64` |
| --- | --- | --- |
| effect: geomean over the 4 rows | **+0.45%** | **+1.73%** |
| matched null: geomean, same 4 rows, `old` vs the byte-identical copy | +0.14% | −0.20% |
| the bar, pre-registered | ≤ 2.0% | ≤ 2.0% |
| verdict | **clears** | **clears** |

**The bar clears on both architectures, so no tuning is taken and the rollback does not fire.** The
registered tuning — hoisting the descriptor load to once per instruction or per frame — was licensed
only *"before the rollback fires"*, and the rollback fires on a miss. Taking it anyway would be
optimising after seeing a number against no registered criterion, which is the shape the
pre-registration exists to prevent. **The amd64 margin is thin and saying so is part of the result**:
+1.73% against a 2.0% bar is 87% of the allowance, and a noisier day would have failed it. That is
recorded rather than smoothed, because the next change on this path inherits the margin and not the
verdict.

Per-row, at the registered Bonferroni-corrected α/4 = 0.0125:

| row | arm64 effect | arm64 null | amd64 effect | amd64 null |
| --- | --- | --- | --- | --- |
| `LoadAligned` | −0.03% (p=0.561) | +0.10% (p=0.900) | **+2.45% (p=0.012)** | −0.46% (p=0.078) |
| `LoadUnaligned` | +0.38% (p=0.037) | +0.22% (p=0.452) | **+3.84% (p=0.000)** | −0.36% (p=0.242) |
| `StoreAligned` | **+0.74% (p=0.006)** | −0.05% (p=0.944) | +0.46% (p=0.326) | −0.01% (p=0.799) |
| `StoreUnaligned` | **+0.70% (p=0.000)** | +0.28% (p=0.165) | +0.21% (p=0.799) | +0.03% (p=0.600) |

Bold marks the rows that carry a verdict at α/4. **No null-arm row carries one on either
architecture**, which is what makes the effect readable at all.

**The registered direction is falsified, and it is falsified in the direction I named as fatal to the
attribution.** The estimate said *"worse on arm64 than on amd64"* on an acquire-barrier story — `LDAR`
on arm64, a plain `MOV` on amd64 — and added that an effect *larger on amd64* means the cost is not
the barrier. The effect is larger on amd64: +1.73% against +0.45%, a factor of about four the wrong
way. **So the barrier is not what this mechanism costs**, on the criterion written before the number
existed.

A second, independent sign points the same way: **the two architectures disagree about which rows
pay.** arm64's verdicts fall on both *store* rows and neither load row; amd64's fall on both *load*
rows and neither store row. A barrier story predicts the same rows on both machines, because the
barrier is per access and the access set is identical. Two disjoint row sets is not that.

**What the cost is instead is a hypothesis and is labelled one**, because nothing here measured it:
the dependent pointer load itself — an indirection the compiler cannot hoist into a register across an
access that may publish — plus, on amd64's store rows, ADR 0054's already-locked aligned store
(+10.16% on that row, measured there) being large enough that an added indirection is invisible beside
it. That is a plausible reading of the table and **not a finding**; it would need its own instrument,
and this ADR does not claim it. The falsification above stands on its own and does not depend on any
replacement story being right.

**The estimate's magnitude band held on one row of four.** Registered: 1–4% on the aligned rows.
Measured: amd64 `LoadAligned` +2.45% is inside it; arm64 `LoadAligned` (−0.03%, `~`), arm64
`StoreAligned` (+0.74%) and amd64 `StoreAligned` (+0.46%, `~`) are all below it. A band that a
favourable miss falls outside is still a missed band, and it narrows the next estimate on this path
rather than licensing it.

**The matched max-vs-max pairing, reported and not governing**, as registered — largest single-row
regression against the largest single-row null-arm magnitude over the same family: arm64 **+0.743%
against 0.282%**, a ratio of 2.6× where the 3× multiplier ADR 0057 used would ask 0.85%, so arm64's
worst row is *not* distinguishable from the instrument's own noise at that multiplier even though it
carries a p-verdict — the two tests ask different questions and here they disagree. amd64 **+3.843%
against 0.461%**, a ratio of 8.3×, which is real by both. The distributional pairing was nominated as
governing before any of this was computed and the multiplier is not re-tuned now that there are
numbers to tune it against.

**The atomic secondary, reported and not governing**, on the same protocol and both architectures —
`internal/interp/rmwbench`'s 49 rows, which reach the bytes through `cell` and so load the descriptor
exactly as the plain accesses do:

| | `darwin/arm64` | `linux/amd64` |
| --- | --- | --- |
| effect: geomean over the 49 rows | −0.27% | +0.38% |
| matched null: geomean, same 49 rows | −0.07% | +0.24% |
| rows carrying a verdict at α/49 = 0.00102 | 0 of 49 (effect), 0 of 49 (null) | 0 of 49 (effect), 0 of 49 (null) |
| rows at an uncorrected α = 0.05 | 0 of 49 (effect), 0 of 49 (null) | 1 of 49 (effect), 0 of 49 (null) |
| extreme rows, effect | +1.18% … −1.72% | +2.40% … −1.87% |

**The atomic rows do not detect the indirection, and the reason is power rather than absence.** An rmw
row spends most of its time inside the atomic operation itself — 33µs/op on arm64 against membench's
17–28µs for a plain access — so one added pointer load is a smaller fraction of an rmw row than of a
load row, and a per-row CI of ±1–2% is wide next to the +0.45%/+1.73% the governing population
reports. **A flat table here is the instrument declining to answer, not the mechanism being free on
atomics.** Saying otherwise would read a null result as a measurement.

**One reason to distrust *any* "0 of 49" as a property rather than a sample, from this project's own
record.** ADR 0057 ran this same benchmark's null arm on this same population and got 3 of 49 (arm64)
and 4 of 49 (amd64) spurious verdicts at α=0.05, against the 2.45 the multiplicity arithmetic predicts;
this run's null arm gets 0 and 0. Same instrument, same rows, a different day. So the 0-of-49 above is
one draw from a distribution that has already been observed to produce 3 and 4, and it bounds the cost
loosely rather than establishing that there is none.

## Consequences

**The residual, which this decision does not close and does not pretend to.** Publishing the
descriptor removes the out-of-bounds read; it does not make a relocation *coherent*. A thread holding
the old descriptor keeps reading and writing the abandoned array, so an update it makes after the copy
is lost, and an atomic RMW it performs there is invisible to every agent on the new array — **an
atomic operation on an abandoned array is not an atomic operation**, which is `allocate`'s own
argument about why the reservation exists. What changes is the class of the defect: memory unsafety
becomes a lost update in the value domain, which the spec permits for plain accesses on a memory that
is not shared and does not describe at all for atomics on one, because in spec terms a non-shared
memory belongs to one agent. That situation is an artifact of `Spawn` sharing the instance, so it is
**filed against the reachability of the residual rather than left in this document**, and the
`noMove` mark's reservation is what keeps it off the shared path where the spec does have something to
say.

**`noMove` narrows rather than retires.** It stops being the memory-safety argument — that is the
descriptor's job now, unconditionally and for every memory — and becomes the coherence argument for
shared memories: a reserved memory never relocates, so no agent is ever left on an abandoned array.
ADR 0056's mark, its refusal arm and its named engine limit all stay; what changes is which sentence
they are the reason for.

**The walk becomes an optimisation with no soundness load.** `Spawn` may keep it — marking a memory
it can prove is reachable lets that memory grow by reslicing rather than by relocating, which is
strictly better for the guest — but nothing depends on the walk being complete, which is what #575
proved it can never be. On `main` the control in this family is
`internal/testenv/observer_test.go:TestNothingInEngineCodeCreatesASecondObserver` (#568), whose subject
is untouched by this decision; the walk-pairing form of it,
`TestEveryGoStatementInEngineCodeIsPrecededByTheWalk`, exists only on
[#554](https://github.com/scttfrdmn/burroughs/pull/554)'s head
(`refs/pull/554/head`) and is named here as a branch artifact rather than as a control this tree runs.
Its remedy's qualification — *"pairing a `go` with the walk is necessary and no longer sufficient"* —
is discharged by this decision, since the pairing stops being load-bearing for memory safety.

**Every memory pays on the single-threaded path, which is the cost this ADR is accountable for.** The
descriptor is one indirection between the interpreter and its bytes, on the hot path of every load,
store, bulk operation and atomic. That is the whole subject of the pre-registration above, and it is
measured on the instrument that drives the real interpreter rather than a proxy.

**#556 is discharged for memories in the form the issue asked for**, and not by the walk. The claim
drafted for #554's changelog — that the walk discharged it — is what #575 falsified; this decision is
what makes the claim true, on a mechanism that does not depend on knowing who can reach what.
