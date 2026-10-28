# 0111 — An unaligned typed access touches its containing words atomically, because a host race between disjoint guest bytes is not the guest's

Date: 2026-10-28 · Status: **accepted**. The chair's fifth ruling of 2026-10-28, item 2, ordered the repair: *"read it before mapping it … both engine code, so a cross-platform defect … reproduce under `-race` on darwin and janus with the 10k stress, fix it, add a witness and neuter."* The ruling orders the fix and not its mechanism. The mechanism is the one [ADR 0054](0054-every-aligned-guest-access-becomes-atomic-on-the-address-already-resolved-because-a-scoped-gate-is-unavailable-rather-than-unwritten.md)'s Consequences name and leave unwritten, and it was decided in the slice under the #647 review's decide-and-proceed: it is not contract text, not public API, and reverses no stamp. Recorded by the actor who was ruled on, so `Ratio-Class: carried`. Filed against [#982](https://github.com/scttfrdmn/burroughs/issues/982).

## Context

**The report.** Windows CI's race job intermittently reported a data race in two tests that run a Go program over two agents (`TestClause3GCAcrossTwoAgents`, and v0.12.0's `TestAForkBuiltGoProgramRunsWithRealThreadsThroughThePublicPath`). Both were mapped in `.github/windows-triage.tsv` to #982. Both stacks, recorded on #982, are engine code:

- a plain write at `memory.writeNum`'s unaligned arm, a byte loop, of a byte at `0x…931`;
- an atomic load at `atomicCell.load`, of the 32-bit word containing `0x…930`, for an aligned one-byte guest load.

**The guest bytes are disjoint.** The unaligned `i32.store` covers `0x931`–`0x934`, and the narrow load covers `0x930` alone. The guest model has no race between them, so the report is the host's race and not a reflection of the guest's. That is what [the engine law](../laws/engine.md#the-guests-data-races-must-not-be-the-hosts-except-where-the-model-permits-the-tear-and-the-region-is-enumerable) forbids: *the guest's data races must not be the host's except where the model permits the tear.* Here there is no guest race for the model to permit anything about.

**Why only Windows.** Guest memory on every other platform is an anonymous mapping ([ADR 0076](0076-a-memory-reserves-address-space-through-an-anonymous-mapping-and-the-go-allocator-becomes-the-fallback-rather-than-the-mechanism.md)), outside the race detector's view. Windows has no reservation, so its guest memory is on the Go heap, where the detector sees every access. The race exists on every platform. Windows is where it was visible.

**Reproduced on darwin/arm64** with `withoutReservation`, which forces the heap arm: the same two sites (`internal/interp/memory.go:memory.writeNum`'s unaligned loop, `internal/interp/atomic.go:atomicCell.load`) were reported on the first run.

**What 0054 left.** Its Consequences: *"The unaligned path still has no atomic mechanism at all … The realizable design is per-word atomic touches — report-free without being atomic, tearing exactly where the proposal already permits it — and it is unwritten. This ADR does not conjure it."* This ADR writes it.

## Options

1. **Per-word atomic touches on the unaligned typed path (0054's named design).** An unaligned load reads each containing 32-bit word with `atomic.LoadUint32` and extracts its bytes. An unaligned store writes each containing word's share through that word's `atomicCell`, whose full-word compare-and-swap leaves the neighbouring bytes alone. An access straddling a word boundary is two or three word operations, so it may tear at the boundary, which is where the proposal permits an unaligned access to tear.
2. **Make the aligned narrow accesses plain** (widths 1 and 2), so neither side is atomic. Rejected: a plain narrow access would then race every aligned *word* atomic over the same bytes, which is the race 0054 removed, moved rather than fixed.
3. **Exclude or suppress the report.** Closed twice by [ADR 0064](0064-the-bulk-and-simd-region-stays-plain-and-is-confined-by-an-enumeration-a-control-asserts-because-the-guest-model-permits-the-tear.md)'s option E, and a suppression would hide a real host race.
4. **Leave it and keep the triage row.** Contrary to the ruling, and to the law above.

## Choice

**Option 1.** `atomicLoadSpan` and `atomicStoreSpan` in `internal/interp/atomic.go`; `writeNum`'s and `memAccess`'s unaligned arms call them. Their names begin `atomic`, so `TestNoGuestMemoryAccessSiteJoinsWithoutAClassification`'s helper predicate sees them, and both rows of `guestMemoryRegimes` move from `mixed` to `atomic`.

## Consequences

- **The typed path has no plain arm.** Every typed guest access touches guest bytes only through word atomics, so no typed access can race the host over a byte the guest did not race over.
- **The witness is `TestAnUnalignedAccessDoesNotRaceANeighbouringByte`**: #982's pair as guest instructions on two agents over one shared memory, on the Go heap (premise asserted), 10,000 iterations each. Its verdict is the race detector's. Without `-race` it asserts that each agent's last value is in its own bytes. **Neuters, run** (darwin/arm64, `-race`): `writeNum`'s byte loop restored is reported at `writeNum`, and `memAccess`'s `loadValue` restored is reported at `loadValue`.
- **The cost is on the unaligned rows only**, measured by `make lab-ab AB='--pkg ./internal/interp/membench --base origin/main --head fix-982'` on janus.local (i9-9960X, x86-64), `measured` group, pueue task 202, 1-minute load 0.31–1.32 across the ten rounds, 2026-10-28:

  | row | base | head | delta |
  |---|---|---|---|
  | LoadAligned | 60.20µ ± 1% | 60.37µ ± 3% | ~ (p=0.971) |
  | LoadUnaligned | 61.96µ ± 4% | 66.63µ ± 2% | **+7.54%** (p=0.000) |
  | StoreAligned | 44.34µ ± 2% | 43.59µ ± 3% | ~ (p=0.306) |
  | StoreUnaligned | 41.75µ ± 5% | 65.68µ ± 0% | **+57.30%** (p=0.000) |

  The store's cost is the containing-word compare-and-swap: an unaligned `i32` covers two words, so it is two CAS loops where it was four byte writes. These rows are 100% unaligned by construction. How often a real Go guest issues an unaligned typed access is **unmeasured**; Go lays out fields at their natural alignment, so the population is expected to be small, and that expectation is not a figure.
  - **arm64, on orion.local** (M4 Pro, darwin/arm64), `measured` group, pueue task 43. Scott cleared the host in session on 2026-10-28 (*"run on orion it is not being used"*):

    | row | base | head | delta |
    |---|---|---|---|
    | LoadAligned | 28.89µ ± 2% | 28.72µ ± 3% | ~ (p=0.436) |
    | LoadUnaligned | 29.86µ ± 2% | 30.77µ ± 2% | **+3.07%** (p=0.000) |
    | StoreAligned | 18.15µ ± 2% | 18.18µ ± 1% | ~ (p=0.986) |
    | StoreUnaligned | 18.62µ ± 2% | 26.86µ ± 2% | **+44.29%** (p=0.000) |

    `ab.sh` noted that the 1-minute load passed a quarter of the 14 CPUs during the run (5.8–13.7, from 3.4 at submit, with nothing else in the queue). The spreads stayed at 1–3%, so the rows are read as the arms'. The note is recorded rather than dropped.
  - **The development machine's arm is not quoted.** Its load was 18–30 on 12 CPUs from other work, and every row came back `~` with ± up to 82%, which is the box and not the arms.
  - **Both tables were taken on the first implementation**, before the pointer repair below. That repair changes only how each word's pointer is formed (an index times four from the base, instead of an advancing pointer), not how many atomics an access makes. It has **not** been re-measured. A re-run was started and killed: pueue's `measured` group excludes other measured tasks, not the `build` group, and #406's 10,000-iteration stress was running in `build` on both hosts. The re-measure runs when the stress has freed them, and these tables are replaced if it differs.
  - **The price was not a condition of the choice.** The repair was ordered, and the alternatives are a moved race (option 2) or a hidden one (3, 4), so no figure here could have selected another option. It is recorded so the cost is known, and its rollback is reverting this ADR's PR.
- **A pointer is formed only for a word the access touches.** The first implementation advanced past its last word, so an access ending in the image's last word formed a pointer one past the allocation. `checkptr` rejected that on Windows, the platform whose image it can see, in this ADR's own PR. `TestAnUnalignedSpanEndingInTheLastWordFormsNoPointerPastTheImage` is the witness, and it supplies the neighbouring allocation `checkptr` needs to see the defect anywhere.
- **The triage rows stay until CI shows the race gone**, per the ruling. They are removed in a later slice, on a Windows run's evidence.
- **0054 gets one line pointing here**, under the bullet that called this design unwritten.
- **ADR 0064's region has the same shape, and this ADR does not touch it.** A plain bulk or SIMD write of byte `0x931` against an aligned narrow atomic of `0x930` is also a host race between disjoint guest bytes. 0064's ruling, *"the guest model permits the tear, so B buys no correctness"*, argues from a racy guest. It does not reach the disjoint case, where the guest has not raced. 0064 is stamped, so whether its region joins this mechanism is Scott's, and it is raised in this ADR's PR rather than decided here.
