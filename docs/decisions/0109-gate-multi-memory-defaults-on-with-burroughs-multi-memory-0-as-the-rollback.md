# 0109 — `gate:multi-memory` defaults on, with `BURROUGHS_MULTI_MEMORY=0` as the rollback

Date: 2026-10-27 · Status: **accepted**. Scott, 2026-10-26: *"Agree with your recommendation"*, relayed in the chair's ruling of 2026-10-27, item 1: *"Scott's agreement already covers them."* The recommendation, posted on [#1131](https://github.com/scttfrdmn/burroughs/issues/1131) with its pre-registered forecast, was to flip each gate whose board is fully green, one at a time, with a rollback, and to hold any with open work. MultiMemory is the third of the four the ruling orders: ExtendedConst ([0107](0107-gate-extended-const-defaults-on-with-burroughs-extended-const-0-as-the-rollback.md)), TailCall ([0108](0108-gate-tail-call-defaults-on-with-burroughs-tail-call-0-as-the-rollback.md)), MultiMemory, Memory64. `Ratio-Class: carried`.

## Context

`gate:multi-memory` governs the multi-memory proposal: more than one memory in a module, and a nonzero memory index on a memory instruction (`internal/binary`, `gateMultiMemory`). It has been off by default (behaviour 4).

**The forecast, read from `TestEachDefaultOffGateOnItsOwnLane` (#1127) on top of 0108's flip, which is this flip's base:** with this gate alone added to the defaults, the board answers **645** of the default lane's declined vectors: **642 pass**, **3 bound**, **0 new failures**, 0 unsupported. The reading is identical on darwin/arm64 and on janus x86-64 (native, pueue `build` task 200), which is G-1's two-platform condition. The lane invokes no `ErrNotValidated` carve-out.

## Options

1. **Leave it off.** A module with a second memory is refused by name unless the embedder supplies the capability. Multi-memory is part of Wasm 3.0, so the refusal is of standard Wasm.
2. **Flip with a revert as the only rollback.**
3. **Flip with an environment rollback**, as 0106, 0107 and 0108 did. `BURROUGHS_MULTI_MEMORY=0` restores the old default.

## Choice

**Option 3**, for 0107's reason of uniformity across the four flips. `binary.DefaultFeatures()` returns `MultiMemory: os.Getenv("BURROUGHS_MULTI_MEMORY") != "0"`, read where the default is defined, so every path that starts there honours it alike.

## Consequences

- **The board moves by exactly the forecast:** +642 pass, +3 bound, −645 gated, 0 fail. `passFloor` moves with it.
- **Nothing else moves.** `TestEachFlippedGateMovesOnlyItsOwnVectors` gains a third row, `{"MultiMemory", 645}`, and compares the default board with this gate on and off, file by file. Its per-file rule admits `pass + bound` together, which is what the 3 bound vectors need.
- **The controls that pinned the old default move with it**, each saying why in the PR. `TestDefaultGatePolicyIsPinnedGateByGateWithItsStamp` cites this ADR.
- **The off state stays reachable**, through `BURROUGHS_MULTI_MEMORY=0`, and is exercised by the comparison above.
- **Rollback:** set `BURROUGHS_MULTI_MEMORY=0`, or revert this ADR's PR.
