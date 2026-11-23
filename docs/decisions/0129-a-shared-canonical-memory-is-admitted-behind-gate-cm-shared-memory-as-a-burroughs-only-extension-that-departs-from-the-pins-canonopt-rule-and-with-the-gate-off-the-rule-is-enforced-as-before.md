# 0129 — A shared canonical memory is admitted behind gate:cm-shared-memory as a Burroughs-only extension that departs from the pin's canonopt rule, and with the gate off the rule is enforced as before

Date: 2026-11-22 · Status: **accepted**, 2026-11-22. Scott, in session: *"admit it anyway, as a deliberate, named, Burroughs-only extension behind `gate:cm-shared-memory`, off by default, the way the spawn interface is Burroughs-only"*, recorded on [#1332](https://github.com/scttfrdmn/burroughs/issues/1332#issuecomment-7092909150) as "Ruling 4, revised". It replaces ruling 4 as written in the [#1332 plan body](https://github.com/scttfrdmn/burroughs/issues/1332) (*"admit a shared canonical memory where the pin permits it"*), because E1 found that the pin permits none. The ruling was given in session, so the record is the actor's, and commits resting on it are `Ratio-Class: carried`. It is an engine mechanism inside `internal/component`: no contract text, no public API (no exported symbol, and the root package does not change), and no stamped ADR is reversed. [ADR 0102](0102-the-pinned-reference-defines-the-semantics-and-a-toolchains-stricter-refusal-is-followed-unless-burroughs-implements-and-witnesses-what-the-pin-admits.md) gets a postscript pointing here, and its text is unchanged. Filed against [#1335](https://github.com/scttfrdmn/burroughs/issues/1335), E1 of the v0.15.0 plan, and it answers [#1141](https://github.com/scttfrdmn/burroughs/issues/1141)'s premise.

## Context

**The need.** v0.15.0 is a Go program with GOMAXPROCS > 1 inside a component (#1332). Such a program's linear memory is shared, and it is the memory the Canonical ABI lifts from and lowers into. On main at e07a7b2, `internal/component/walk.go:refuseSharedCanonMemory` refuses any canon whose `(memory …)` option names a shared memory, so no such component can load.

### What the pin says

The pin is component-model 2e7895c (`CANON_PIN`).

- **CanonicalABI.md, "`canonopt` Validation":**

  > * `memory` - this is a subtype of `(memory 0)`
  >   * 🐘 `memory` may also be a subtype of `(memory i64 0)`

  `(memory 0)` is an unshared memory type.
- **Under the threads proposal, a shared and an unshared memory type never match.** Four sources agree:
  - The threads Overview: *"It is a validation error to attempt to import shared linear memory if the module's memory import doesn't specify that it allows shared memory. Similarly, it is a validation error to import non-shared memory if the module's memory import specifies it as being shared."*
  - The suite. `proposals/threads/imports.wast` at suite pin de54fd2 asks for `incompatible import type` in both directions, at its lines 502 and 505.
  - This tree. `internal/interp/link.go:matchMemoryType` compares `Shared` for equality (grave #522). Its comment records that the threads pin cc535ad's own matcher, and its spec document, drop the flag, and that the tree follows the suite.
  - wasmparser, below.

  So a `(memory 1 1 shared)` is not a subtype of `(memory 0)`, and **a shared canonical memory is invalid at the pin.**
- **`definitions.py` does not model the rule.** `def:MemInst` carries `bytes` and `addrtype`, `def:LiftOptions` carries `string_encoding` and `memory`, and `def:LiftLowerContext` assumes nothing about who else writes the memory. definitions.py at the pin never mentions shared. The rule lives in the validation prose, which `definitions.py` does not execute.
- **The pin names a shared canonical option as future work.**
  - `Binary.md`, "Binary Format Warts to Fix in a 1.0 Release": *"Add optional `shared` immediate to all canonical definitions (explicitly or via `<canonopt>`) when shared-everything-threads (🧵②) is added."*
  - `Concurrency.md`, "TODO": *"integrate with `shared`: define how to lift and lower functions `async` *and* `shared`"*.
- **Upstream main at 585526a (read 2026-11-22) has the same rule**, word for word, so a re-pin would not change it.

**#1141's premise was wrong.** It said *"`definitions.py` never mentions 'shared'"*, which is true, and concluded that the pin sets no rule, which is false. It had read the first 100k characters of the Explainer and not `CanonicalABI.md`. The refusal it led to, #1145's, was right, for a reason it did not give. Its comment, and the doc comment of `TestASharedMemoryAsACanonMemoryOptionIsRefusedByName`, cited ADR 0102 as the reason. Both are corrected in this slice.

### What the toolchains do

They implement the pin's sentence literally.

- **wasmparser.** `validator/component.rs:cabi_memory_at` builds `MemoryType { initial: 0, maximum: None, shared: false, .. }` and runs `SubtypeCx::memory_type` against the option's type, which fails with *"mismatch in the shared flag for memories"*. That was read in wasmparser 0.258.3's source; wasm-tools 1.258.0 prints the same message.
- **The readings**, on E1's fixture (`internal/component/testdata/shmem-canon-roundtrip.wasm`):
  - wasm-tools 1.258.0 `validate --features all`: *"mismatch in the shared flag for memories (at offset 0x14b)"*.
  - wasmtime 49.0.3 `run -W threads=y,shared-memory=y`: the same, exit 1.

**So ADR 0102 does not cover this.** Its rule is about a toolchain that refuses what the pin admits. Here the toolchains refuse what the pin refuses, and admitting it departs from the pin itself, which ADR 0102's first point (*"the pinned reference defines the semantics"*) does not license. That is why this needed Scott's ruling and not a slice's decision.

## Options

1. **Keep the refusal, and redesign v0.15.0 so the canonical memory stays unshared.** A Go program's strings and slices live in its heap, which is the shared memory, so every lift and lower would need a copy through a second memory that the toolchain does not emit. *Costs:* the plan's multi-M rows, and a fork change with no consumer outside Burroughs.
2. **Wait for shared-everything-threads' `shared` canonical option.** *Costs:* the pin lists it as future work and gives no schedule, so the milestone would stall.
3. **Admit it as a named Burroughs-only extension behind a default-off gate.** *Costs:* a component that uses it is valid to no other engine and no validator, which is already true of the threaded tier (ADR 0087), and the departure has to be named wherever it is mentioned.

## Choice

**Option 3, by Scott's revised ruling.**

1. **The gate is `gate:cm-shared-memory`, read from `BURROUGHS_CM_SHARED_MEMORY`, and it is off by default.** It reads `== "1"`, an explicit opt-in, as `gate:cm-threads` did before its flip (`internal/component/cmsharedmem.go:cmSharedMemoryEnabled`). This ADR does not schedule a flip. A flip is its own stamp-tier event, and an extension that departs from the pin may never be flipped.
2. **What admitting it means, layer by layer:**
   - **Decode** does not change, in either gate state. A shared memory is core grammar under `gate:threads`, and the canon's option is a memory index, so there is nothing for the decoder to refuse. *Gates never manufacture malformedness.*
   - **Validation** does not change. Burroughs' component validation never judged the option's memory type, and it still does not.
   - **Link** is where the rule is applied, as it was before this ADR. `refuseSharedCanonMemory` runs in the walk at both canon sites, the core func step (lowers and built-ins) and the lift step, at every depth. With the gate on it admits the canon. With the gate off it refuses, as before.
3. **With the gate off, the pin's rule is enforced exactly as before.** The refusal keeps its sentinel, `ErrUnsupportedForm` (CLI exit 5), and its #1141 name. Its message now says the rule is the pin's (*"the Canonical ABI requires an unshared canonical memory (canonopt validation …)"*) and names the variable that admits the extension.
   - **Not taken: reclassifying it as `ErrCanonOptionInvalid`**, which the pin's reading would support. That would change a public error class on the gate-off path, which the ruling keeps as it was. This build can run the component when the extension is on, so "not admitted in this configuration" is also true.
4. **What E1 claims: the memory is admitted, and nothing more.** Guest code runs on one agent at a time, through ADR 0101's run slot, which this ADR does not change. No component can start a second agent, because the spawn door does not exist on the component path. So nothing lifts or lowers through a memory that another agent is writing. **Not yet claimed:**
   - guest code on several agents at once over a shared canonical memory (E2, [#1336](https://github.com/scttfrdmn/burroughs/issues/1336), with its own ADR citing ruling 1);
   - the component spawn door (E3, [#1337](https://github.com/scttfrdmn/burroughs/issues/1337));
   - any cross-agent ordering at the boundary (E4, E5).

### Every canonical-memory site, read for an unshared-only assumption

The question for each site was whether it assumes the memory is unshared, with one agent running. The answer for every site is no, for one reason found by reading rather than assumed:

- **No access path reads the shared flag.** The non-test code that reads it, on main at e07a7b2, is:
  - `Extern.SharedMemory`, called only by `refuseSharedCanonMemory`;
  - `matchMemoryType`, for import matching;
  - `allocate`'s fallback arm;
  - `hasSharedMemory`, for `Spawn`;
  - `atomicWait`'s *"expected shared memory"* trap;
  - the type printer.
- **So a shared canonical memory is the same `*memory`, reached through the same lines.** It differs only in how it is allocated, and in whether `memory.atomic.wait` may block on it.

| Site | Path | Verdict |
|---|---|---|
| The codec's reads and writes through a canon lower, and waitable-set and built-in result stores | `interp.CanonCaller` → `Caller.Read`/`Write`/`CheckRange` → `readUnderGrowthLock`/`writeUnderGrowthLock` → `memory.read`/`write` | **no unshared assumption.** A plain copy under `growMu`, read-shared. The image is loaded inside the lock, so a realloc that grew the memory is seen. Witnessed: `get-arguments` and `blocking-write-and-flush` in the fixture. |
| The host-side lowering and lifting of an export's arguments and results, and of a sync cross-component call (ADR 0103) | `interp.WriteBoundaryMemory`, `ReadBoundaryMemory`, `CheckBoundaryRange` → the same two functions | **no unshared assumption.** Witnessed: the embedder's `shout` call, and the parent-to-child `shout` crossing. |
| `cabi_realloc` | `CanonCaller.Realloc`, and the export path's `reallocCore` | **no unshared assumption.** It is guest execution on the calling agent, and memory-agnostic. A realloc that grows a shared memory grows it in place: on the mapping arm (ADR 0076), as for any memory; on the fallback arm, up to `sharedReservePages`, beyond which `memory.grow` returns -1. That cap is the existing shared-memory limit, and it is a legal -1, not a hazard. Witnessed: three reallocs in the fixture (the parent's for `get-arguments`, the child's for each crossing, the parent's for each result). |
| Stream and future copy | `driveStreamCopyLocked` and the future copy, through the stored reader's and writer's `CanonCaller` | **no unshared assumption**, by reading, not by a fixture. The same `Caller.Read`/`Write`, under the run slot as before. A guest-to-guest copy over a shared memory is E5's row. |
| Guest accesses to the canonical memory | `memAccess`, ADR 0051's and 0054's word atomics, and ADR 0112's edge words | **no difference.** ADR 0054 made every aligned access atomic on every memory, so the brief's *"apart from its access being atomic-word"* turns out not to be a difference at all. |
| `memory.atomic.wait` on the canonical memory | `atomicWait` | **the one behavioural difference**, and it is the guest's own. On an unshared memory it traps. On a shared one it blocks, and with no second agent to notify it an infinite wait blocks for ever, as ADR 0128 rules for a core module. That is the threads proposal's semantics. It is not a canonical-memory site. |

None was unsafe, so the stop condition did not fire.

### The witnesses

- **The gate-off row:** `TestASharedMemoryAsACanonMemoryOptionIsRefusedByName`, whose assertions are unchanged.
- **The gate-on row over the same fixtures:** `TestASharedCanonMemoryIsAdmittedAndRunsWithTheGateOn`.
- **Real use:** `TestASharedCanonMemoryRoundTripIsByteIdenticalToItsUnsharedTwin` over `shmem-canon-roundtrip.wasm` and `shmem-canon-roundtrip-unshared.wasm`.
  - **The twins differ only in the word `shared`**, on three memory declarations.
  - **The oracle is the unshared twin's wasmtime 49.0.3 reading**, 10 of 10 (`shmem-canon-roundtrip.reading`). The shared fixture has no external reader, so its oracle is that reading, byte for byte.
- **The gate predicate:** `TestTheSharedCanonMemoryGateIsAnOptIn`.
- **The neuters are recorded at the tests.**
  - With the gate check replaced by `true`, the gate-off rows fail.
  - With the admission removed, the gate-on rows fail.

## Consequences

- **A component that uses the extension is valid to no other engine and no validator.** wasmtime and wasm-tools will refuse it for as long as the pin's rule stands. This is a divergence from the pin itself, not from a toolchain, and it is named as such in the refusal, in `cmsharedmem.go`, at the refusal site, in the witnesses and in the CHANGELOG.
- **A conforming component never uses it**, so with the gate on, every component the pin admits behaves as before. The gate admits only what the pin refuses.
- **With the gate off, nothing changes.** The gate-off witness's assertions are the ones it had, and its fixtures are the same bytes.
- **The extension's oracle is always a twin.** No external engine can read a shared canonical memory, so every later witness over one, E2's to C3's, needs an unshared or single-threaded twin read by wasmtime or stock Go. #1332's plan already says so for C3.
- **If the pin later admits a shared canonical option** (the `shared` immediate that `Binary.md` anticipates), the form it takes decides what happens. A form Burroughs follows would replace this extension rather than sit beside it, and that is the slice's own decision.

## Rollback

Leave the gate unset, which is the default. Deleting `cmsharedmem.go` and the two lines that read it in `refuseSharedCanonMemory` restores the refusal at every gate value.
