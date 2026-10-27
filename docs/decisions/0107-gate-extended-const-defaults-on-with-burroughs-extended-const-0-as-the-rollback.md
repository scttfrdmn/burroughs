# 0107 — `gate:extended-const` defaults on, with `BURROUGHS_EXTENDED_CONST=0` as the rollback

Date: 2026-10-27 · Status: **accepted**. Scott, 2026-10-26: *"Agree with your recommendation"*, relayed in the chair's ruling of 2026-10-27, item 1: *"Scott's agreement already covers them."* The recommendation, posted on [#1131](https://github.com/scttfrdmn/burroughs/issues/1131) with its pre-registered forecast, was to flip each gate whose board is fully green, one at a time, with a rollback, and to hold any with open work. ExtendedConst is the first of the four the ruling orders: ExtendedConst, TailCall, MultiMemory, Memory64. `Ratio-Class: carried`.

## Context

`gate:extended-const` governs the extended constant expressions of Wasm 3.0: `i32`/`i64` `add`, `sub` and `mul` inside a constant expression (`internal/binary/gatemap.go`, `gateExtendedConst`). It has been off by default (behaviour 4) since the field was added for #109.

**The forecast, pre-registered on #1131 and read from `TestEachDefaultOffGateOnItsOwnLane` (#1127):** with this gate alone added to the defaults, the board answers **70** of the default lane's declined vectors, **70 pass**, 0 bound, **0 new failures**, 0 unsupported. This reading is identical on darwin/arm64 and on janus x86-64 (native), which is G-1's two-platform condition. The lane invokes no `ErrNotValidated` carve-out. The gate's label has no open issues.

## Options

1. **Leave it off.** A module using an extended constant expression is refused by name unless the embedder supplies the capability. Wasm 3.0 is a standard (the spec's own release appendix lists extended constant expressions among its ten features), so the refusal is of standard Wasm.
2. **Flip with a revert as the only rollback**, as SIMD's and relaxed SIMD's flips did.
3. **Flip with an environment rollback**, as `gate:threads`' ADR 0106 did. `BURROUGHS_EXTENDED_CONST=0` restores the old default.

## Choice

**Option 3.** `binary.DefaultFeatures()` returns `ExtendedConst: os.Getenv("BURROUGHS_EXTENDED_CONST") != "0"`. It is read where the default is defined, so every path that starts there honours it alike: core `Instantiate`, `IsWASIP1Command`, a component's core modules, and the CLI.

A field rollback is kept although extended-const has no runtime behaviour an operator might need to switch off, unlike threads. The reason is uniformity across the four flips: an embedder who must reproduce a pre-flip refusal can do so without rebuilding, for each gate, the same way.

## Consequences

- **The board moves by exactly the forecast.** 60957 → **61027 pass**, and 4187 → **4117 gated**, with 0 fail. `passFloor` moves with it.
- **Nothing else moves.** `TestEachFlippedGateMovesOnlyItsOwnVectors` compares the default board with this gate and without it, file by file. Every file's change must be declined vectors becoming answered, with no new failure and no new `unsupported`, and the total must equal the gate's own lane reading.
- **The controls that pinned the old default move with it, each saying why:**
  - `TestGatedVectors`' allowed-gated set loses its 70 extended-const entries (62 in `global.wast`, 8 in `elem.wast`). They were reported as *"no longer declined; remove the entry"*.
  - `TestKindGateCensusIsPinned`'s default lane goes 30 → 31 reference-kind reaches. The one new row, `global.wast:241`, was read by diffing the census with the rollback set and without it.
  - `TestDefaultGatePolicyIsPinnedGateByGateWithItsStamp` cites this ADR as the gate's stamp.
- **The off state stays reachable**, through `BURROUGHS_EXTENDED_CONST=0`, and is exercised by the lane comparison above.
- **Rollback:** set `BURROUGHS_EXTENDED_CONST=0`, or revert this ADR's PR.
