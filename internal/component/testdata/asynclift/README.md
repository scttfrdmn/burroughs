# Slice 1's async-lift guests and the concurrency witness

Committed artefacts for [#771](https://github.com/scttfrdmn/burroughs/issues/771), the async-lift half of
`gate:async`. **These bytes and readings ARE the evidence**; `build.sh` exists to regenerate them when a tool
moves, and nothing on the test path invokes a Rust toolchain. Same shape as `make watref` and the committed
wasmtime readings ADR 0086 already relies on.

**A consumer that lives only in `/tmp` is not one** — that is why this directory exists. Scott approved starting
#771 on 2026-10-02; the chair's condition was that the guest be committed with source, bytes and provenance on
the slice's first day.

## Provenance, read from the tools rather than recalled

```
wit-bindgen-cli  0.62.0
wasm-tools       1.258.0
wasmtime         49.0.1 (46c23a87d 2026-09-24)
rustc (guest)    1.91.1 (ed61e7d7e 2025-11-07)      rustup — has the wasm targets
rustc (host)     1.98.1 (48a229cea 2026-09-01)      Homebrew — new enough for the wasmtime crate
```

**Both rustc versions are recorded because neither alone suffices.** The guests need rustup's wasm targets but
its rustc is too old for the wasmtime 49 crate; the harness needs the newer rustc but that install has no wasm
targets. See `build.sh` for that and for three blocked paths worth not re-paying for.

## What is here

| path | what it is |
|---|---|
| `single/` | async-**lift** only: `compute: async func(x: u32) -> u32`. 46,171 B |
| `suspending/` | async-lift **and** async-**lower**: `run` awaits an imported `tick`, so a task suspends. 52,334 B |
| `harness/` | the wasmtime embedding that answers the concurrency question. Its own lockfile, outside the Go build |
| `concurrent.reading` | the positive arm's reading |
| `sequential.reading` | the negative arm's reading |

Each guest carries its **own `Cargo.lock`**, and that is load-bearing rather than tidiness: the manifests say
`wit-bindgen = "0.62"`, a caret range, so without the lock a rebuild is free to resolve a later 0.62.x and emit
different bytes. The provenance block above would then name a version the rebuild did not use.

**One edit was made to the copied locks, and it is recorded because it is exactly the kind of thing that breaks
a rebuild:** the guests were developed as crates named `conc` and `p62` and renamed on the way in, so each
lock's root `[[package]]` name was rewritten to match its manifest. Whether that rewrite is sound is not an
argument to have — the reproducibility check below is what settled it. (It did not survive: cargo re-sorts the
package list after a root rename, so the committed locks are the build's own output, not the hand edit.)

## Reproducibility, measured

**The committed bytes rebuild byte-for-byte from the committed sources.** Run from a clean `git worktree` at a
**different absolute path** from the one that produced them, so path-dependence would have shown as a mismatch:

```
single/component.wasm       0efba8e99ec4ead3890a4736f5e6e9e020f388f0b2d06b5265aef94aca7a4991   MATCH
suspending/component.wasm   a26009255f8519a0b1adc115de8bc6bae53f2174607517befe688061b6324b95   MATCH
concurrent.reading / sequential.reading                                                        byte-identical
```

### It did not pass first time, and the three failures are the reason to keep this section

Recorded because each one is invisible from inside the tree that produced the artefacts, which is the whole
argument for running the check at all rather than assuming provenance from a version block.

1. **`build.sh` pinned `cargo` but not `rustc`.** Cargo invokes a bare `rustc` through `PATH`; with Homebrew's
   bin directory ahead of `~/.cargo/bin`, the rustup cargo drove a rustc with no wasm targets. The error
   blamed a missing target that `rustup target list --installed` shows present.
2. **The WIT file was staged flattened.** `wit_bindgen::generate!` reads `path: "wit"`, a directory. The macro
   then reported an unresolved `exports` module, three errors downstream of the cause.
3. **A failing build produced a *passing* check.** `build.sh` exited before `wasm-tools component new` ran, so
   the committed `component.wasm` was never touched and the comparison was each file **against itself**. This
   is why the script now deletes its output before building and reads the artefact path from cargo's own JSON
   rather than guessing with `find -newer`.

Then the check failed **legitimately**, and the diagnosis mattered: the entire byte difference was Rust symbol
names (`p62` → `asynclift_single`, `conc` → `asynclift_suspending`). Not nondeterminism, not embedded paths —
the crates had been renamed during staging, so the original bytes were built from crates the committed
manifests no longer describe and **no rebuild from these sources could ever have reproduced them.** The bytes
were replaced with ones the sources do describe, after confirming the rebuilt pair still lifts stacklessly,
still imports the same eleven intrinsics, and still yields both readings unchanged.

## What the guests settle

ADR 0086 deferred the async-lift ABI choice — **stackless callback vs stackful** — to *"the first guest that
actually lifts an async export"*. `single/component.wasm` is that guest, and its canonical lift answers it:

```
(canon lift (core func $"[async-lift]test:probe/ops@0.1.0#compute") async
            (callback $"[callback][async-lift]test:probe/ops@0.1.0#compute"))
```

**Stackless callback.** The consumer makes the choice, which is what the clause required.

`suspending/component.wasm` additionally emits the subtask primitives — `[async-lower]`, `[subtask-cancel]`,
`[subtask-drop]` — with `(canon lower (func $tick) (memory $memory) async)`. It also imports `[task-return]`,
`[task-cancel]`, the waitable-set family (`new`/`poll`/`join`/`drop`) and the context slots
(`[context-get-0]`, `[context-set-0]`). **That import list is the demand set Burroughs must supply**, read from
the artefact rather than from a reading of the spec.

## The witness, and why it is a rendezvous

The verdict is **structural, not a timing observation**. `tick` blocks until **two** calls have arrived, then
releases both. A second task cannot reach `tick` unless the first has already **suspended** inside it — so the
second arrival *is* the proof that two tasks are live, and no clock enters the verdict.

An event-order verdict was the first design and was weaker than it looked: a fast concurrent run could finish
the first call before the second started (false negative), and a slow sequential run could log lines that read
as interleaved (false positive). Neither is a property of the engine.

**The readings:**

```
concurrent:  enter(1) -> suspend(1) -> enter(2) -> release(by 2) -> resume(1)    ARRIVALS 2   both Ok
sequential:  enter(1) -> suspend(1) -> expire(1)                                 ARRIVALS 1   expired at 5s
```

**The arms differ structurally** — concurrent completes, sequential expires — so **wasmtime 49 arbitrates two
concurrent component tasks**. The 5-second bound is not a tuning parameter: a rendezvous either closes almost
immediately or never, so hitting it is a verdict rather than a flaky timeout.

### The event log's job is diagnosis, not verdict

Kept as evidence beside the verdict, because it is what separates failures the verdict alone cannot:

| observation | diagnosis |
|---|---|
| concurrent completes, sequential expires | concurrency confirmed |
| both expire; `enter(1), suspend(1)`, no `enter(2)` | tasks serialised — concurrency absent |
| both expire; `enter(1)` with **no** `suspend(1)` | the host lowered `tick` **synchronously** — a harness fault, not an engine finding |
| **both** arms complete | the harness is not actually sequential — a harness fault |

The third row is called out on the strength of `func_wrap_concurrent`'s own warning that *"even if a host
function is defined in the 'concurrent' mode here a guest may still lower it synchronously"*: the guest's
`[async-lower]` import shows the guest **asks** for async lowering, not that the host honoured it. Both harness
faults are excluded by the committed readings — `suspend(1)` appears, and the sequential arm expired.

## Coverage, stated so these readings are not read as more than they are

- **Externally checkable, and checked here:** the ABI surface — async lift/lower results, and the built-ins as
  the component sees them.
- **Permanently internal, and untouched by this slice:** `sp5-stop-completes-with-agents-suspended-in-builtins`
  and `h4-a-suspended-agent-does-not-starve-its-siblings`. Those are claims about **Burroughs' own** agents and
  safepoints; no external engine can arbitrate them, and nothing here changes that.
