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
- **The cost**, pre-registered on #1167 before any head figure existed. A/B: `make lab-ab AB='--pkg ./internal/interp/membench --base 783d444 --head <this ADR's code commit>'`, `measured` on janus.local and orion.local, over the bulk and SIMD rows commit 783d444 added (`internal/interp/membench/bulkvec_test.go`):

  *(the measured tables are recorded here when the A/B completes, against the forecast's bands)*

- **The price was not a condition of the choice.** The repair was ordered, and the alternatives are leaving the race (1) or a wider and costlier repair (2) that buys nothing more, so no figure could have selected another option. The rollback is reverting this ADR's PR.
