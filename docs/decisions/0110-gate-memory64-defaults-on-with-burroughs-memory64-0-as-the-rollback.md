# 0110 — `gate:memory64` defaults on, with `BURROUGHS_MEMORY64=0` as the rollback

Date: 2026-10-27 · Status: **accepted**. Scott, 2026-10-26: *"Agree with your recommendation"*, relayed in the chair's ruling of 2026-10-27, item 1: *"Scott's agreement already covers them."* The recommendation, posted on [#1131](https://github.com/scttfrdmn/burroughs/issues/1131) with its pre-registered forecast, was to flip each gate whose board is fully green, one at a time, with a rollback, and to hold any with open work. Memory64 is the last of the four the ruling orders: ExtendedConst ([0107](0107-gate-extended-const-defaults-on-with-burroughs-extended-const-0-as-the-rollback.md)), TailCall ([0108](0108-gate-tail-call-defaults-on-with-burroughs-tail-call-0-as-the-rollback.md)), MultiMemory ([0109](0109-gate-multi-memory-defaults-on-with-burroughs-multi-memory-0-as-the-rollback.md)), Memory64. Exception handling (#95) and GC (#172, #262) stay off, with their open work. `Ratio-Class: carried`.

## Context

`gate:memory64` governs the memory64 proposal: an `i64` index type on a memory or a table, and the 64-bit addressing that follows from it (`internal/binary`, `gateMemory64`). It has been off by default (behaviour 4). ADR 0076's address-space reservation already covers 64-bit memories, since it reserves by limit and not by index type.

**The forecast, read from `TestEachDefaultOffGateOnItsOwnLane` (#1127) on top of 0109's flip, which is this flip's base:** with this gate alone added to the defaults, the board answers **2078** of the default lane's declined vectors: **2074 pass**, **4 bound**, **0 new failures**, 0 unsupported. The reading is identical on darwin/arm64 and on janus x86-64 (native, pueue `build`), which is G-1's two-platform condition. The lane invokes no `ErrNotValidated` carve-out.

## Options

1. **Leave it off.** A module with an `i64` index type is refused by name unless the embedder supplies the capability. Memory64 is part of Wasm 3.0, so the refusal is of standard Wasm.
2. **Flip with a revert as the only rollback.**
3. **Flip with an environment rollback**, as 0106 to 0109 did. `BURROUGHS_MEMORY64=0` restores the old default.

## Choice

**Option 3**, for 0107's reason of uniformity across the four flips. `binary.DefaultFeatures()` returns `Memory64: os.Getenv("BURROUGHS_MEMORY64") != "0"`, read where the default is defined.

## Consequences

- **The board moves by exactly the forecast:** +2074 pass, +4 bound, −2078 gated, 0 fail. `passFloor` moves with it.
- **Nothing else moves.** `TestEachFlippedGateMovesOnlyItsOwnVectors` gains its fourth row, `{"Memory64", 2078}`.
- **The controls that pinned the old default move with it**, each saying why in the PR. `TestDefaultGatePolicyIsPinnedGateByGateWithItsStamp` cites this ADR.
- **The off state stays reachable**, through `BURROUGHS_MEMORY64=0`, and is exercised by the comparison above.
- **With this flip the four are done.** The default gate set is every Wasm 3.0 proposal except exception handling and GC, and each of the four can be rolled back by its own variable.
- **Rollback:** set `BURROUGHS_MEMORY64=0`, or revert this ADR's PR.
