# 0108 — `gate:tail-call` defaults on, with `BURROUGHS_TAIL_CALL=0` as the rollback

Date: 2026-10-27 · Status: **accepted**. Scott, 2026-10-26: *"Agree with your recommendation"*, relayed in the chair's ruling of 2026-10-27, item 1: *"Scott's agreement already covers them."* The recommendation, posted on [#1131](https://github.com/scttfrdmn/burroughs/issues/1131) with its pre-registered forecast, was to flip each gate whose board is fully green, one at a time, with a rollback, and to hold any with open work. TailCall is the second of the four the ruling orders: ExtendedConst ([0107](0107-gate-extended-const-defaults-on-with-burroughs-extended-const-0-as-the-rollback.md)), TailCall, MultiMemory, Memory64. `Ratio-Class: carried`.

## Context

`gate:tail-call` governs the tail-call proposal's two opcodes, `return_call` (0x12) and `return_call_indirect` (0x13) (`internal/binary/gatemap.go`, `gateTailCall`). It has been off by default (behaviour 4). `return_call_ref` (0x15) is not this gate's: it is mapped to `gate:gc`, which stays off, so this flip does not admit it.

**The forecast, pre-registered on #1131 and read from `TestEachDefaultOffGateOnItsOwnLane` (#1127):** with this gate alone added to the defaults, the board answers **26** of the default lane's declined vectors, **26 pass**, 0 bound, **0 new failures**, 0 unsupported. The reading was retaken on top of ExtendedConst's flip, which is this flip's base, on darwin/arm64 and on janus x86-64 (native). It is identical on both, which is G-1's two-platform condition. The lane invokes no `ErrNotValidated` carve-out.

## Options

1. **Leave it off.** A module using `return_call` or `return_call_indirect` is refused by name unless the embedder supplies the capability. Tail calls are part of Wasm 3.0, so the refusal is of standard Wasm.
2. **Flip with a revert as the only rollback**, as SIMD's and relaxed SIMD's flips did.
3. **Flip with an environment rollback**, as 0106 and 0107 did. `BURROUGHS_TAIL_CALL=0` restores the old default.

## Choice

**Option 3**, for 0107's reason of uniformity across the four flips. `binary.DefaultFeatures()` returns `TailCall: os.Getenv("BURROUGHS_TAIL_CALL") != "0"`, read where the default is defined, so core `Instantiate`, `IsWASIP1Command`, a component's core modules and the CLI honour it alike.

## Consequences

- **The board moves by exactly the forecast:** +26 pass, −26 gated, 0 fail. `passFloor` moves with it.
- **Nothing else moves.** `TestEachFlippedGateMovesOnlyItsOwnVectors` gains a second row, `{"TailCall", 26}`, and compares the default board with this gate on and off, file by file.
- **The controls that pinned the old default move with it, each saying why:** `TestGatedVectors`' allowed-gated set loses the gate's entries, and `TestDefaultGatePolicyIsPinnedGateByGateWithItsStamp` cites this ADR as the gate's stamp. Any further pin that moves is named in the PR.
- **The off state stays reachable**, through `BURROUGHS_TAIL_CALL=0`, and is exercised by the comparison above.
- **Rollback:** set `BURROUGHS_TAIL_CALL=0`, or revert this ADR's PR.
