# 0131 — The component spawn door starts the `mstart` of the core instance that imports it, which must use a shared canonical memory, and every refusal is at link

Date: 2026-11-23 · Status: **accepted**, decided in the slice. It is the mechanism of Scott's ruling 2 on the [#1332 plan body](https://github.com/scttfrdmn/burroughs/issues/1332) (*"D-F4 = a Burroughs WIT interface, `burroughs:threads/spawn@0.1.0#spawn: func(arg: u32)`, with the host resolving `mstart`"*) and of contract §2 T-1's component paragraph, appended by Scott's stamp on [#1334](https://github.com/scttfrdmn/burroughs/issues/1334). Both leave the resolution of `mstart` to the engine (*"resolved by the engine"*), so which core instance's `mstart` runs is the engine's to choose. It is an engine mechanism inside `internal/component`, with one internal method in `internal/interp` (`Instance.UsesMemory`): no contract text, no public API (no root-package file changes), and no stamped ADR is reversed, so by the #647 review it is decided here and reported as a decision taken. The record is the actor's, so commits resting on it are `Ratio-Class: carried`. Filed against [#1337](https://github.com/scttfrdmn/burroughs/issues/1337), E3 of the v0.15.0 plan.

**Every citation into this tree names a symbol rather than a line** (ADR 0047).

## Context

T-1's component paragraph: *"In a component, T-1's primitive is the import `burroughs:threads/spawn@0.1.0#spawn: func(arg: u32)`, whose entry is the core export `mstart` of type `(i32) -> ()`, resolved by the engine. The new agent shares the component's shared canonical memory and is a thread of the task that called the import. … A component that imports it without exporting `mstart` is refused at link."*

The text fixes three things and leaves one open:

- **`mstart` is a core export**, not a component-level one. Its type, `(i32) -> ()`, is a core function type. A component exports component functions, typed in component value types (`u32`), and never a core function, so *"a component that imports it without exporting `mstart`"* can only mean that none of its core instances exports one.
- **The agent shares the component's shared canonical memory.** So it must run in a core instance whose memory is that memory.
- **It is a thread of the calling task.** ADR 0130's `asyncHandles.spawnAgent` already makes it one.
- **Which core instance's `mstart`** is not said. A component has several core instances, and more than one may export an `mstart`.

## Options

1. **The core instance that imports the lowered door.** The core instance whose code calls the import is the one whose `mstart` runs. *For:* it is the preview-1 door's shape one level in (*"A module that imports it … and exports a function `mstart`"*), it is decided the moment that instance exists, so the refusal is at link, and each importer has one answer. *Costs:* a component whose door is imported by an adapter or shim module, and whose `mstart` is in another module, is refused. wit-component lowers a function with no canonical options directly into the module that imports it, and `func(arg: u32)` has none, so the shim case does not arise for this signature.
2. **The core instance whose memory is the shared canonical memory, wherever the door is imported.** *Costs:* a canonical memory is defined by one instance and imported by others (the shape of every fixture in this tree, where `$mm` defines it and `$gm` imports it), so "whose memory" names several instances. A component with two shared canonical memories names more. The answer is a search with a tie to break, and the search can be settled only when the walk ends.
3. **A component-level export named `mstart`.** Ruled out by the text: it says core export, and the type is a core type.

## Choice

**Option 1, with option 2's property required of the answer.**

- **The door's `mstart` is the importing core instance's.** The canon lower of the door is bound, in `walker.resolverFor`, to the core module being linked. When its instantiation returns, that instance must export a function `mstart` of type `(i32) -> ()` with a guest body, or the component is refused at link (`walker.adoptSpawnDoors`).
- **The importer must use one of the component's canonical memories** (`interp.Instance.UsesMemory`, by identity, since an imported memory is the supplier's allocation), checked when the walk ends (`walker.finishSpawnDoors`). This is T-1's *"shares the component's shared canonical memory"*, so the agent cannot run over a memory of its own while the guest's canonical traffic goes elsewhere.
- **The door is the engine's, not the Host's.** It is keyed by ADR 0115's compatibility class (`spawnDoors` in `hostsys.go`, class 0.1), and every walk offers it, whichever host it was given. A version of another class reaches the cross-track refusal naming 0.1.

### The refusals, all before any guest code runs

| Refusal | Where | Sentinel |
|---|---|---|
| `gate:cm-shared-memory` off | the canon lower of the door (`walker.spawnDoorStep`) | `ErrUnsupportedForm` |
| a component type other than `func(arg: u32)` | the canon lower | `ErrLinkRefused` |
| a core import type other than `(i32) -> ()` | linking the importer | `ErrLinkRefused` |
| no `mstart`, a mistyped one, or a host function | the importer's instantiation | `ErrLinkRefused` |
| the instance not free: an unshared canonical memory, or none (ADR 0130's §1) | the walk's end | `ErrUnsupportedForm` |
| an importer that uses no canonical memory | the walk's end | `ErrLinkRefused` |

- **The gate is checked at the lower**, and so first. With the gate off no canonical memory can be shared, so no instance is free, and a component whose shared canonical memory precedes the door would otherwise hear E1's canon refusal instead of the door's. The gate-off and unshared refusals are `ErrUnsupportedForm`, as E1's gate-off refusal is (ADR 0129): the component is not admitted in this configuration.
- **The free check is at the walk's end**, because a later canon can still name an unshared canonical memory, which makes the instance not free.
- **The door opens when the walk ends.** A call before then, from a core start function, is refused by name: it would spawn into an instance whose walk could still refuse it.

### A call

`spawnDoor.call` starts an agent through `asyncHandles.spawnAgent` at the importer's `mstart`, with the argument, as a thread of the calling Thread's task (ADR 0130's §2). A spawn the engine refuses (no caller record, a locked store, or one of `interp.Instance.spawn`'s refusals) is the caller's host trap, as the preview-1 door's is (`internal/wasi/spawn.go`). `func(arg: u32)` returns nothing, so the agent's `tid` is not returned. The goroutine is `interp.Instance.spawn`'s, which ADR 0068 authorises in the census. The door adds no `go` statement.

## Consequences

- **A component can start agents over its shared canonical memory** with the gate on, through `burroughs run`, `ComponentConfig.Run` and the library's component paths, since every walk offers the door.
- **A component that imports the door from a module other than the one exporting `mstart` is refused at link**, by name. A shim-lowered door would need option 2. The trigger to revisit this is a toolchain that emits that shape for a function with no canonical options.
- **With the gate off, which is the default, nothing a guest can do reaches the door**: it is refused at the lower.

## The build, as measured

On darwin/arm64, in the worktree of #1337's branch, stacked on #1336's.

- **`TestAComponentStartsItsOwnAgentsThroughTheSpawnDoor`** (`internal/component/spawndoor_test.go`, over `testdata/spawn-door.wat`): the output matched, byte for byte, and the sampled peak of executing agents read 3, 10 of 10, and 10 of 10 under `-race` with no report. No engine goroutine survived `Close`.
- **The same fixture through `dispatch` and `ComponentConfig.Run`**: the output matched, and with the gate off each was refused by name (exit 5, `ErrUnsupported`).
- **`TestTheSpawnDoorIsRefusedByName`**: eight rows, each refused by name and sentinel: the gate off (over spawn-door and over the unshared fixture), the unshared canonical memory, no `mstart`, a mistyped `mstart`, the wrong component type, the wrong core type, and an importer using no canonical memory. A host function as `mstart` is refused by the same check and has no fixture.
- **The neuters, each run, each reverted:**
  - the door invoking `mstart` on the caller's own goroutine (`core.Invoke`): the peak read 1, both agents printed `released=00000000` after their 2s bound, and each read the previous runner's global at entry (0x77, then 1), and run read 2;
  - the free-instance check dropped: the unshared fixture instantiated.

## Rollback

`spawnDoors` empty: the door's identity reaches the host tables, which do not serve it, and a call meets the refusing stub, as before #1337.

## Postscript (#1338, PR #1359): the sampled peak is logged, not asserted

The first reading above was true when it was taken. Since then, #1359's macOS CI run read a peak of **2**, with the output byte-identical.
- **Why:** on a 3-vCPU runner shared with every other test package, the sampler's thread can miss run's whole 50 ms hold.
- **Not P starvation:** 20 of 20 local runs read 3 at `-cpu` 8, 3 and 2 alike, because the count is a mark and a descheduled agent keeps it.

So the witness now asserts the overlap by its place. `released=00000001` is printed only from an agent's spin, after run met both agents while itself executing. The sampled count is logged. The first neuter above still fails the place.

The engine's count under a place is E2's `TestAgentsOfASharedMemoryInstanceExecuteAtOnce`, whose agents spin until its probe has read 3.
