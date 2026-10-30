# 0112 — A bulk or SIMD extent touches its partial edge words atomically and its whole words plainly, because only an edge word can be shared with a disjoint neighbour

Date: 2026-10-30 · Status: **accepted**. The chair's ruling of 2026-10-30, item 4, ordered the repair and its evidence: *"#1167, ADR 0064's bulk/SIMD region: repair it, decided unless Scott objects. Apply #982's race repair to the bulk and SIMD paths. Pre-register the cost forecast on #1167, and run the A/B on janus and orion. Witness: the race the current code permits, reported under -race, gone after the repair. Neuter: the old path restored."* [ADR 0064](0064-the-bulk-and-simd-region-stays-plain-and-is-confined-by-an-enumeration-a-control-asserts-because-the-guest-model-permits-the-tear.md) is Scott's stamp. This ADR narrows its extent on the chair's *decided unless Scott objects*, and Scott has been told. The mechanism (option 3 below, #1167's recommendation) was chosen in the slice. Recorded by the actor who was ruled on, so `Ratio-Class: carried`. Filed against [#1167](https://github.com/scttfrdmn/burroughs/issues/1167).

## Context

**The case.** #1166 (#982's repair, [ADR 0111](0111-an-unaligned-typed-access-touches-its-containing-words-atomically-because-a-host-race-between-disjoint-guest-bytes-is-not-the-guests.md)) removed one host race from the typed path: a plain access and an atomic one to the same 32-bit word, over guest bytes that are disjoint. ADR 0064's plain region has the same pair.
- **The pair:** a plain write of byte 0x931 from `memory.fill`, `memory.copy`, `memory.init` or a SIMD store, beside an aligned narrow guest access to byte 0x930. That narrow access is an atomic of the word containing both.
- **The guest has not raced, and the host has.** The guest bytes are disjoint. The plain write and the atomic touch one 32-bit word.
- **The read side has it too:** a SIMD load, or `memory.copy`'s source, reading 0x931 plainly while a neighbour's narrow store updates the word atomically.

**Why 0064's argument does not reach it.** 0064's ruling 1 is *"the guest model permits the tear, so B buys no correctness — it buys report-freedom."* That argues from a guest that races: two accesses over one byte, whose interleaving the threads proposal leaves unordered. In #1167's case the guest's bytes do not overlap, so there is no guest race for the model to permit anything about. That is [the engine law](../laws/engine.md#the-guests-data-races-must-not-be-the-hosts-except-where-the-model-permits-the-tear-and-the-region-is-enumerable)'s case: *the guest's data races must not be the host's except where the model permits the tear.*

**Reproduced** with `withoutReservation`, which puts guest memory on the Go heap where the race detector sees it (Windows's only arm, ADR 0111's reason). See the witness below.

## Options

1. **Leave 0064 as it stands**, and record the disjoint case as accepted inside its region. Windows `-race` may then report it from a Go guest's bulk or SIMD operations, and nothing distinguishes that report from a real guest race. Contrary to the ruling.
2. **Extend 0111's span atomics to the whole region.** Every word of a bulk extent is touched atomically, so a page-sized `copy` becomes a per-word loop. That is the bulk-throughput cost 0064's ruling 1 declined, and it buys nothing beyond option 3, by the argument below.
3. **Make only each extent's partial edge words atomic**, the first and last word the extent covers only part of, and leave the interior plain.

## Choice

**Option 3.** The argument is that **only an edge word can be shared with a disjoint neighbour**:
- **An interior word lies wholly inside the extent**, so any other access to it overlaps the extent's own bytes. That is a guest race, which 0064 permits and which stays plain.
- **A neighbour that does not overlap the extent** can share a word with it only where the extent covers part of a word: its first and last.

So touching those through 0111's containing-word atomics removes exactly the case 0064's argument does not cover, at no more than two word operations per extent.

**Mechanically:**
- **`edgeSplit(i, n)`** (`internal/interp/edges.go`) measures an extent's head (the bytes before its first word boundary) and tail (the bytes after its last). An extent inside one word is all head.
- **`memory.fill` and `memory.init`** write their head and tail through `atomicStoreSpan`, once, around the chunk loop (ADR 0104's polls), which runs over the interior alone. `init`'s source is a data segment, which is host bytes, so only its destination has edges.
- **`memory.copy`** treats a byte as interior only when both its source and its destination lie in whole words of their extents. So [lo, hi) is the intersection of the two interiors, and what lies outside it (at most 3 bytes at each end) is copied by `atomicCopyEdge`: read through the source words' atomics in full, then written through the destination words'. The edges run in the interior's direction (head first going forward, tail first going backward), so `memory.copy`'s memmove semantics hold for them.
- **The SIMD loads and stores** (`vecLoad`, `vecLoadLane`, `vecStore`, `vecStoreLane`) go through `memory.readEdges` and `memory.writeEdges` instead of `read` and `write`. A whole-word access is unchanged: `readEdges` returns the image's own bytes. A partial one is assembled in the stack's 16-byte `edgeBuf`, so the unaligned load allocates nothing.
- **`memory.read` and `memory.write` are unchanged.** Their remaining callers are `runData` and the embedder's accessors, whose plainness 0064's amendment argues separately.

**The enumeration** (`guestMemoryRegimes`) gains a fifth regime, **`edges`**: atomic at an extent's partial edge words and plain at its whole words, which is `mixed`'s mirror image. It must call a synchronisation helper, as `atomic` and `mixed` must. `execMemoryFill`, `execMemoryCopy` and `execMemoryInit` move from `plain` to `edges`. The four SIMD rows are replaced by `memory.readEdges` and `memory.writeEdges`, also `edges`.

## Consequences

- **The witness is `TestABulkOrSIMDEdgeDoesNotRaceANeighbouringByte`** (`internal/interp/edgerace_test.go`). It is #982's pair, with agent A's side moved into the region:
  - memory.fill, init, v128.store and store8_lane writing 0x931;
  - memory.copy into and out of 0x931;
  - v128.load and load8_lane reading 0x931.

  Agent B loads and stores the aligned byte at 0x930. The memory is on the Go heap (premise asserted), and the verdict is the race detector's. **Neuter, run** (darwin/arm64, `-race`): with `edgeSplit` returning (0, 0), so every extent is all interior and plain as before this ADR, the detector made 17 reports. Their plain side was `execMemoryFill`, `execMemoryInit`, `execMemoryCopy`, `memory.writeEdges`, `v128FromBytes` and `replaceLaneBytes`, against B's `atomicCell.load` and `update`.
- **0064's obligation is met and asserted.** The B-MM-2 case (`TestAResumedAgentSeesASiblingFieldWrittenBeforeTheNotify`) takes its verdict from the race detector's silence over a plain `memory.fill`. Its extent is one whole aligned word, so it is interior and still plain. The case now asserts that premise every round, with `edgeSplit`, so an edit that moved its extent onto an edge fails there rather than leaving the case with nothing to detect.
- **The control caught a blind spot in the slice.** The copy's edge helper was first named `copyEdge`, so the regime control's predicate (*a callee whose name begins `atomic`*) saw no synchronisation, and `execMemoryCopy`'s row would have passed as `plain`. It is `atomicCopyEdge`, and its comment says why.
- **The cost, measured twice, with the forecast pre-registered on #1167 before either.** Each A/B is `make lab-ab` over the bulk and SIMD rows (`internal/interp/membench/bulkvec_test.go`) in the `measured` group on janus.local (x86-64) and orion.local (arm64). Every group on both hosts was checked idle first, with 0 tasks running at submit. A µs per 1000 instructions is a ns per instruction.

  **The first measurement falsified the forecast** (base 783d444, the rows; head 7030f8a, the repair; janus task 246, orion task 50; recorded on #1167):
  - **Aligned rows moved.** On janus: fill +7.67%, copy +15.19%, init +15.04%, v128 store +8.42%, v128 load +4.89%. On orion, v128 store +9.40%, while copy (−6.49%) and init (−9.95%) got faster. The forecast said aligned rows pay only `edgeSplit`'s arithmetic, but they also paid the edge helpers' calls, which returned on an empty span but were still made.
  - **janus's V128LoadUnaligned (+20.4 ns) was over twice its 2–10 ns band.** The forecast priced an edge as its atomics alone, and the span helpers' word loops and `packLE`/`unpackLE` cost more than the loads.

  **The second measurement is the code as merged** (base 3b467c2, the same rows rebased; head 8a6eeb0, the repair with each edge call guarded at its call site and `writeEdges` given `readEdges`'s whole-word fast path). orion task 51, and janus task 248. An earlier janus run, task 247, was discarded: another project's `keel.test` ran on janus outside pueue during its last two rounds (load rising to 7, head spreads ±24–37%), and the queue recorded nothing else in that window.

  | row | janus (task 248) | orion (task 51) |
  |---|---|---|
  | FillAligned | +5.18% | +9.07% |
  | FillUnaligned | +75.82% (+30.2 ns) | +18.24% (+4.5 ns) |
  | FillPage (4094 B) | +2.17% | ~ |
  | CopyAligned | +9.93% | −9.72% |
  | CopyUnaligned | +147.26% (+62.4 ns) | +65.10% (+16.2 ns) |
  | InitAligned | +6.23% | −5.19% |
  | InitUnaligned | +91.43% (+37.2 ns) | +31.73% (+7.6 ns) |
  | V128StoreAligned | +1.75% | +3.02% |
  | V128StoreUnaligned | +67.30% (+40.7 ns) | +52.86% (+13.7 ns) |
  | V128LoadAligned | +2.92% | +2.30% |
  | V128LoadUnaligned | +44.82% (+20.6 ns) | +34.53% (+7.4 ns) |
  | Store8Lane (1 B, always an edge) | +42.53% (+19.3 ns) | +27.62% (+5.6 ns) |

  The typed rows (Load/StoreAligned/Unaligned), which this ADR doesn't touch, were `~` on both hosts in both measurements.

  **What the second measurement says:**
  - **The guards recovered about half of janus's aligned overhead** (copy 15.2% → 9.9%, init 15.0% → 6.2%, v128 store 8.4% → 1.75%).
  - **The residual is real and is not the edges.** A whole-word extent makes no edge call. What it still pays is `edgeSplit` and, in `memory.copy`, the lo/hi bookkeeping, about 2–4 ns per instruction on janus.
  - **orion's aligned fill (+9.07%, ~ in the first measurement) has the same code path in both heads**, so the swing between measurements is variance in that row, not the change. Its aligned copy and init are faster than the base in both.
  - **The edge cost is 2 to 6 word operations through the span helpers:** about 20–60 ns per unaligned instruction on janus and 5–16 ns on orion. That is roughly twice what the forecast's per-atomic prices said, for the reason the first measurement found.
  - **A page-sized fill pays ≤2.2%.**

  The forecast stays falsified as registered. Nothing re-bands it after the fact.

  **The SHAs above are the branch's before its last rebase**, onto main's 67656c1 (#1194). They were measured locally and never pushed. The rebase changed none of what was measured: `git diff 8a6eeb0 8355bac` over `internal/interp`'s non-test sources and over `internal/interp/membench` is empty. So the second measurement is of this ADR's code as merged.

- **The price was not a condition of the choice.** The repair was ordered, and the alternatives are leaving the race (1) or a wider and costlier repair (2) that buys nothing more, so no figure could have selected another option. The rollback is reverting this ADR's PR.
