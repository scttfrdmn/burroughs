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
wit-bindgen-rt   0.44.0                             the RUNTIME 0.62.0 depends on — not 0.62
wasm-tools       1.258.0
wac-cli          0.12.0                             composition; see ../compose/README.md
wasmtime (CLI)   49.0.2 (3c8a3e79a 2026-10-02)      produces compute.reading
wasmtime (crate) 49.0.2                             LINKED by harness/; produces every other reading
rustc (guest)    1.91.1 (ed61e7d7e 2025-11-07)      rustup — has the wasm targets
rustc (host)     1.98.1 (48a229cea 2026-09-01)      Homebrew — new enough for the wasmtime crate
```

**The wasmtime CLI and the wasmtime crate are two artefacts and are recorded separately.** They happen to
agree at 49.0.2 today; nothing makes them. `compute.reading` is taken through the **CLI** (`wasmtime run
--invoke`), every other reading through the **crate** the harness links, whose version is pinned by
`harness/Cargo.lock`. A reading that disagreed with another would need to be read against the right one,
and one line in a provenance block naming "wasmtime" could not say which.

**Measured mid-slice, which is why it is spelled out:** the CLI moved 49.0.1 → 49.0.2 while #862 was being
built, and the only thing that changed in `compute.reading` was its own provenance header — every value
identical. So the bump invalidated nothing; it did invalidate the single-line version block that had been
standing in for both.

**`wit-bindgen-rt` is recorded separately because the version a reader would guess is wrong**, and the
difference is load-bearing. The guests depend on `wit-bindgen 0.62.0`, whose *runtime* crate is **0.44.0**
— so a question about what the generated code does at run time is answered by `wit-bindgen-rt-0.44.0`'s
source, not by anything numbered 0.62. Four facts about cancellation were read out of it (see
[CANCELLATION.md](CANCELLATION.md)), and looking them up under the wrong version would have found nothing.

**Both rustc versions are recorded because neither alone suffices.** The guests need rustup's wasm targets but
its rustc is too old for the wasmtime 49 crate; the harness needs the newer rustc but that install has no wasm
targets. See `build.sh` for that and for three blocked paths worth not re-paying for.

## What is here

| path | what it is |
|---|---|
| `single/` | async-**lift** only: `compute: async func(x: u32) -> u32`. 46,171 B |
| `suspending/` | async-lift **and** async-**lower**: `run` awaits an imported `tick`, so a task suspends. 52,334 B |
| `receipt/` | the `suspending` guest **plus a receipt guard**: a `Drop` local to the async body that calls a `note` host import, disarmed on success. 52,443 B. Built for #857's cancellation witness; see [ABANDONMENT.md](ABANDONMENT.md) for what it actually measured, and why that is not cancellation |
| `cancel-rust-parent/` | #862's **real** canceller: drops an in-flight async import, which is what a Rust guest does. Sees no status — see [CANCELLATION.md](CANCELLATION.md) |
| `cancel-wat-parent/` | #862's **synthetic** canceller: calls `subtask.cancel` by hand and reports the numeric status it returns. Its `.wat` is committed beside its `.wasm` |
| `call-wat-parent/` | #888's **completer**: calls the child, parks on its subtask, and reports the value it returned. The only composed artefact here that does **not** cancel — see below. Its `.wat` is committed beside its `.wasm` |
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
receipt/component.wasm      4c0f63f97637e1b00c82ad3eeb37f7665fb79dffdf952c7ede3a1caab5ea88d1   MATCH
concurrent.reading / sequential.reading                                                        byte-identical
abandonment.reading / abandonment-stripped.reading                                             byte-identical
receipt.wat.diff                                                                               byte-identical
cancel-rust-parent/component.wasm  0b2ce9ac3a56fe221d4fcae7bdb5047c8de02c57657683b5a9ba7598a08d550f  MATCH
cancel-rust-parent/composed.wasm   b2d5d0f3bf597de39b649212710d070496e8b3bd3bde24023633bab7e58c4dc9  MATCH
cancel-wat-parent/parent.wasm      189ea0de6396c597a9e0c6b9401b692a83da0f0188164d163a128c4cb3e37238  MATCH
cancel-wat-parent/composed.wasm    d2d02c27cc322b95e92ba64a388f623589fe9b525b7550d14326a6d9d35beeb2  MATCH
cancel-wat.reading / cancel-rust.reading                                                       byte-identical
```

**The #862 rows include the COMPOSED artefacts, so `wac`'s output is checked here too** — the composition
step is part of the build and therefore part of what has to reproduce. Run from `/tmp/repro-862`, a
different absolute path, with `git status` reporting nothing changed.

**All three guests are rows in this check, not two rows and an exception.** The run above is a clean
`git worktree` at `/tmp/repro-allthree` — a different absolute path from the tree that produced the bytes —
and `git status` in it reported **nothing changed**: every component, both pairs of readings, and the
committed WAT diff regenerated byte-identically. `stripcall.py` re-derived the same symbol, deleted the same
single call at the same wat line, and produced the same two arms (`efc3af554b65` / `51a0592ce361`).

**The two abandonment readings are identical to each other, and that is the finding** rather than a defect —
documented in [ABANDONMENT.md](ABANDONMENT.md). A run where they *differed* would be the surprise, because
nothing in that mode is cancelled and a deleted `task.cancel` therefore cannot matter. `build.sh` prints that
expectation on every run so the identity is never read as a failure.

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

## The parity witness does not run yet

[PARITY-BLOCKERS.md](PARITY-BLOCKERS.md) records why, measured: the guest's bare world-level `tick` import
has no host path, a guest that suspends cannot complete (the callback park is unbuilt), and one lift task
per component instance. **Three blockers, not one**, and the first is earlier than the concurrency question
the `concurrent.reading` above was captured to answer.

So the readings in this directory are **wasmtime's** — the oracle's side of a comparison whose other side
does not exist yet. That is the normal state for a committed reading here, and it is said plainly because a
directory full of readings invites the assumption that something was compared against them.

## Why `call-wat-parent/` exists, and why it has no `.reading`

**Every other composed artefact here cancels**, because #862 built them to measure cancellation. That left
the cross-component *call* with no end-to-end arm at all: the composed paths either cancelled the child or,
in `resultlist_test.go`, asserted load-and-instantiate without calling. So #888's capability could not be
witnessed by anything in the tree, and a cancelling witness would have failed on
[#892](https://github.com/scttfrdmn/burroughs/issues/892) — `subtask.cancel` returns BLOCKED where the model
waits — for a reason that is not about the call.

This parent is the completing path: start the child, park on its subtask, read the result the lower lowered
to the retptr, report it. The value crosses **two** boundaries (host `tick` → child `task.return` → parent
retptr → the parent's own lift result), which is why the witness asserts a value rather than a success.

**It carries no committed `.reading`**, and that is a property of the subject rather than an omission. The
other parents have readings because wasmtime can run them; this one's subject is a capability Burroughs is
adding, and the reference side of it is the composed cancellation already recorded in
[CANCELLATION.md](CANCELLATION.md). There is no wasmtime number here that would mean anything a reading
could hold — *a figure with no subject is worse than no figure.*

## Coverage, stated so these readings are not read as more than they are

- **Externally checkable, and checked here:** the ABI surface — async lift/lower results, and the built-ins as
  the component sees them.
- **Permanently internal, and untouched by this slice:** `sp5-stop-completes-with-agents-suspended-in-builtins`
  and `h4-a-suspended-agent-does-not-starve-its-siblings`. Those are claims about **Burroughs' own** agents and
  safepoints; no external engine can arbitrate them, and nothing here changes that.
