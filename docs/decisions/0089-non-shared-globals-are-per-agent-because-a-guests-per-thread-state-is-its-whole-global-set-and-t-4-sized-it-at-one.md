# 0089 — Non-shared globals are per-agent, because a guest's per-thread state is its whole global set and T-4 sized it at one

Date: 2026-09-19 · Status: **accepted** · [#805](https://github.com/scttfrdmn/burroughs/issues/805) (option A ruled) · [#807](https://github.com/scttfrdmn/burroughs/issues/807) (the slice) · Contract amendment [#806](https://github.com/scttfrdmn/burroughs/issues/806) · Occasioned by Phase 4 slice 2 ([ADR 0087](0087-the-threaded-p3-fork-charter-a-geoexperiment-over-an-async-base-whose-threaded-tier-has-no-independent-engine.md))
Ratio-Class: ordered https://github.com/scttfrdmn/burroughs/issues/805

**Stamped 2026-09-19 (Scott, on [#805](https://github.com/scttfrdmn/burroughs/issues/805) — "I concur with the recommendation", option A).** `Status: accepted` cites that ruling — behaviour 3: an ADR's `Status` is a citation to an approval. Recorded by the actor the ruling was given to, so no independent provenance; commits resting on it stay `Ratio-Class: carried`.

**This ADR does not license the mechanism to land.** §2's normative text is Scott's, and T-6 ([#806](https://github.com/scttfrdmn/burroughs/issues/806)) is a **precondition for landing, not for building or scoping** — the [#737](https://github.com/scttfrdmn/burroughs/issues/737) → [#740](https://github.com/scttfrdmn/burroughs/issues/740) sequence, so nothing lands past the point where the contract does not say what the engine does.

## Context

**A Go guest cannot run two Ms in one Burroughs instance.** Go's wasm backend keeps its *entire register bank* in mutable wasm globals — `cmd/internal/obj/wasm/wasmobj.go`'s `regVars`: `SP`→0, `CTXT`→1, `g`→2, `RET0..3`→3–6, `PAUSE`→7 — and `globals []*global` is a field on `Instance` (`internal/interp/interp.go`), shared by every agent, because `spawn` runs `runEntry` on the same `*Instance`.

**Found by building the fork, not by re-reading §2**, and the four facts were each witnessed rather than argued:

| # | fact | how it was observed |
|---|---|---|
| 1 | the fork's spawned-M entry's first two instructions are `global.set 2` (`g`) and `global.set 0` (`SP`) | read off the emitted module's own bytes, not the compiler's register table |
| 2 | a spawned agent shares the instance's globals | a WAT probe: child sets a global to 222, parent rendezvouses on an atomic and reads **222** |
| 3 | two `Instance`s *can* share one memory with independent globals | 111/222 kept apart across two importers of one exported shared memory, with a memory write crossing |
| 4 | a second instantiation **re-applies active data segments** | guest writes `0x55`; instance B instantiates; `mem[0]` reads `0xaa` again |

**Fact 4 is retained as the costing of a declined option, not as a hazard this decision must handle.** It was option B's, and option B has more than one instance; A has one. Kept in the record because a future reader reconsidering B should start from the measurement rather than re-run it.

### What the contract already said, and the part it undersized

**T-1** promises a thread *"sharing the module's shared linear memory"* — **silent on globals**. **T-4** promises *"a per-thread slot readable at register-like cost (the `g` register analog)"* — so §2 **anticipated this problem and sized it at one item**. A guest's per-thread register state is every mutable global its toolchain allocates; **satisfying T-4 exactly as written would still leave `SP` shared**, and a shared stack pointer is a corrupted stack pointer the moment a second agent runs. T-4 named `g` because the wasip1 port's scar tissue was about `g`; the generalization was never taken.

T-4 is also **not implemented**: `thread.slot` is `//nolint:unused`, "neither read nor written", and its guest-visible accessor is unwritten public API ([#514](https://github.com/scttfrdmn/burroughs/issues/514)).

## Options and decision

**(A) Non-shared globals become per-agent in the engine — chosen.** Fixes all eight at once, needs no compiler change and no new guest surface, and is what T-4 was already reaching for. Cost: new normative §2 text (#806) and a change at the engine's global-resolution seam.

**(B) One `Instance` per M over an imported shared memory** (the wasi-threads / JS-worker model; fact 3 says it is available today with **no engine mechanism at all**). **Declined, and it is the one that needs stating plainly, because needing no engine work would otherwise make it the easy choice.** It binds `newosproc` to **host instantiation rather than `Spawn`**, falsifying [ADR 0087](0087-the-threaded-p3-fork-charter-a-geoexperiment-over-an-async-base-whose-threaded-tier-has-no-independent-engine.md)'s stated binding, and it **removes §2 from the evidence of the only tier that exists to demonstrate §2** — a tier whose evidence is already the weakest in the tree (**D-1**: no `thread.spawn` in the shipped Canonical ABI, so no independent engine). Cheap in engineering, expensive in the one currency this tier is short of. It also requires passive data segments (fact 4) and yields **no componentizable artifact**, since an imported memory cannot be a component — the same exclusivity the fork's `burroughsspawn` flag already records.

**(C) Implement T-4's accessor and move Go's register bank into memory.** Declined: a deep change to the wasm backend's calling convention, far beyond a slice, permanently diverging the fork's codegen from upstream Go.

**(D) Defer clause 1.** Declined: the threaded tier would produce no witness at all, and three slices of emission work would stand with no consumer.

## The mechanism, at the one seam that exists

`globalFor` is *"the only place that resolves a global index to its storage"*, so this is one site rather than a sweep.

- **A spawned thread gets its own `[]*global`**, built at spawn as the instance's slice with **defined** entries replaced by fresh `*global`s initialized from each global's own initializer, and **imported** entries aliased to the instance's pointers. The import/defined distinction is resolved **once at spawn, not per access**.
- **`globalFor` indexes the thread's slice when that slice is indexed in *this* instance**, and the instance's own otherwise.

  **This clause first read "the same single indexing, so the resolved path gains no branch", and a bug falsified it before the slice landed.** A global index is *module-local*, and a thread can run code from more than one module: a thread calling an **imported function** executes a body whose global indices are read in the **exporting** module's index space, so resolving against the calling thread's slice is simply wrong. The component tier said so immediately — `instruction names global 0 of 0`, a caller with no globals invoking a callee with one (`TestImportedFuncRunsOnExporterState`). **Per-thread global storage is per (thread, instance), not per thread.**

  So `thread` carries `globalsOf *Instance` and the hot path is `if t != nil && t.globalsOf == in`. The branch the design set out to avoid is **there**, put there by correctness rather than by taste, and the cost forecast below is corrected accordingly rather than quietly re-scoped. **The limit this leaves is stated at the field**: a *foreign* instance's defined globals stay shared across agents, which is guest-driven (the only spawning guests are single-instance, since there is no guest-reachable spawn) with the trigger recorded — a guest that spawns *and* calls into another instance's mutable globals.
- **The first thread aliases the instance's originals.** Not a main-thread special case in T-2's sense (T-2 is about blocking): every thread has its own globals, and the first thread's are the ones instantiation initialized. This is what makes instantiation, the public `Global()` accessor and every single-threaded behaviour identical rather than intended-identical — and it is why the board forecast is principled instead of hopeful.
- **Fresh initialization, not inheritance**, and the choice is observable: a global initialized `i32.const 7` and set to 99 by the parent reads **7** in the child. Fresh-init is what a separate instance would give, which is where both upstream threading models converge.

## Consequences

- **Imported globals stay shared.** An import names a cell the exporting instance owns; per-agent copies of it would answer a different question than the module asked. Witnessed by its own case, not left as prose.
- **Shared globals, where the format admits them, stay shared — and today the category is total.** `decodeMutability` refuses any globaltype mutability byte above `0x01`, and the shared-everything-threads encoding sets bit 1, so no module this engine can decode has a shared global. The clause is written over the non-shared category so it stays correct when that encoding is accepted, and **the refusal is witnessed firing on the byte** rather than asserted — the #732 standard applied to a clause's scope rather than to a gate.
- **[#514](https://github.com/scttfrdmn/burroughs/issues/514)'s accessor is dissolved, not deferred.** Under T-6 a guest wanting a per-thread slot at register-like cost can *declare a mutable global*, which is exactly that — so the host-function accessor has no consumer and no path to one. `thread.slot` therefore loses the retirement condition its own comment states (*"retired by T-4's guest-visible slot accessor"*) and is **deleted rather than re-pinned**: *a directive must not outlive its subject*, which is that comment's rule about itself.
- **T-4 stays, with a dated append.** It is correct about **cost** and undersized about **extent**; T-6 owns the extent and T-4 keeps register-cost and stability. One authority per fact — the two clauses answer "how cheap" and "how much", not the same question twice.
- **T-4's litmus entry is corrected in the same change.** It reads *"Blocked by: nothing — the mechanism landed with ADR 0050 (#514)"*, which a reader takes as T-4 satisfied; what landed was the field's placement. A ruling falsifies prose written before it.
### The cost forecast came back falsified, and it is recorded as falsified

[#807](https://github.com/scttfrdmn/burroughs/issues/807) pre-registered **"no statistically significant regression"** on the global access path, on the premise that the mechanism added no branch. **That premise died inside the slice** — the cross-instance repair above put the branch back — and the measurement then falsified the forecast on both architectures. Two-arm A/B over `./internal/interp/globalbench`, base `main` vs the slice, the x86-64 arm **queued into `janus.local:measured` with 0 concurrent tasks at submit**:

| row | x86-64 (janus, i9-9960X) | arm64 (dev box, M4 Pro) |
|---|---|---|
| `GetI64` | +3.01% (p=0.000) | +2.33% (p=0.001) |
| `SetI64` | +3.06% (p=0.000) | +3.14% (p=0.001) |
| `GetV128` | **−2.04%** (p=0.000) | +2.32% (p=0.009) |
| `SetV128` | +1.60% (p=0.000) | +1.55% (p=0.019) |
| `GetRef` | +2.90% (p=0.000) | +1.11% (p=0.043) |
| `SetRef` | +0.86% (p=0.023) | ~ (p=0.684) |
| **geomean** | **+1.55%** | **+1.79%** |

**What the falsification narrows to, rather than licenses:** the price of the correctness property is about **1.5–1.8% geomean on global access**, and it is paid on a path a real guest hits hard — Go writes `SP` (global 0) on every call. There is no cheaper design that keeps the property *and* answers a cross-instance call correctly, because some dispatch on "whose index space is this" is unavoidable; what is available is doing that dispatch **once per frame instead of once per access**, which is filed with its trigger and its own falsification condition at [#809](https://github.com/scttfrdmn/burroughs/issues/809) rather than bundled here.

**`GetV128` flips sign between architectures** — significant improvement on x86-64, significant regression on arm64. Reported and **not explained**: it is the mutex-protected arm, nothing here establishes why, and an explanation fitted to two numbers would be worse than the anomaly.

- **This unblocks Phase 4 slice 2's harness** and nothing else. It is not a gate flip, and `gate:threads`' default is untouched.

### A wrong consequence, drafted and then falsified inside this slice — kept visible

This ADR was drafted with a consequence claiming that **[#573](https://github.com/scttfrdmn/burroughs/issues/573)'s synchronisation of a global's storage becomes unnecessary on the defined path**, since a per-agent global "has exactly one accessor". It was filed as [#808](https://github.com/scttfrdmn/burroughs/issues/808) with a measured prize attached (a Go guest writes `SP` — global 0 — on **every call**), and it is **wrong**.

`global.go`'s own field comment had already written the refutation, at the site, before this ADR existed:

> **Atomic because a `global.set` races a `global.get` on a shared instance** (#573, decision 0063). **Nothing gates that: `Invoke` is an exported method on `*Instance` that two goroutines may call at once** — `TestAtomicRmwIsNotObservablyTornAcrossThreads` already does — **so the two threads reaching one `*global` need no threads proposal and no `Spawn` to exist.**

**T-6 removes sharing between agents, not between concurrent host invocations.** Every `Invoke` runs on `&in.host` — one thread object that any number of embedder goroutines may drive at once — so a defined global's storage is still reached concurrently and **all three of #573's repairs stay load-bearing on the exact path the consequence proposed to strip them from.** The premise *"a per-agent global has exactly one accessor"* is false: it has one *thread*, and a thread is not one accessor.

Kept in the record rather than deleted, because the failure mode is the instructive part: the claim was derived from **this ADR's own reasoning** rather than checked against the mechanism the repair protects, and the check cost one comment's worth of reading. #808 is closed invalid with the refutation on it. What survives is narrow and is not work: *if* the engine ever gave each concurrent `Invoke` its own thread — a question about the public surface, not a consequence of T-6 — the defined path's synchronisation would become strippable, and the three arms would then be worth measuring separately.

**Nothing in this ADR's mechanism changes as a result.** Storage synchronisation is untouched by the slice; what is corrected is a claim about what the slice makes possible next.
