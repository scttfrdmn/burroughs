# Prepared, not filed: wasmtime 49.0.2 reports "BUG: delivering non-present event"

**Status: prepared for Scott, not filed.** Filing is an outward act under Scott's name, so the chair's
second review of ADR 0101's Option A amendment held it for him. It is item 3 of the `gate:cm-threads` flip
list ([#999](https://github.com/scttfrdmn/burroughs/issues/999)). The text below is the issue as it would
be filed against `bytecodealliance/wasmtime`.

---

**Title:** Component-model threading: `BUG: delivering non-present event` when a created thread's
`waitable-set.wait` receives a subtask event

**Version:** `wasmtime 49.0.2 (3c8a3e79a 2026-10-02)`, `wasm-tools 1.258.0`, on darwin/arm64.

**Reproduction:** [`repro.wat`](repro.wat) in this directory.

```
wasm-tools parse repro.wat -o repro.wasm
wasmtime run -W component-model-threading=y -W component-model-async=y --invoke 'run()' repro.wasm
```

**Expected:** `5`.

**Observed, on every run (5 of 5):**

```
Error: failed to run main module `repro.wasm`

Caused by:
    BUG: delivering non-present event
location: crates/wasmtime/src/runtime/component/concurrent.rs:948
version: 49.0.2
```

**What the component does:**
- A thread created with `thread.new-indirect` async-lowers a nested child component's `g` and blocks in a
  synchronous `waitable-set.wait` on the returned subtask.
- `g` is callback-lifted and returns `YIELD` until a flag in the child is set.
- The parent's own callback-lifted `run` returns `YIELD`, and on each callback it sets the flag through the
  child's synchronous `release` until the thread has stored `g`'s result.

**What we narrowed it to.** The message appears when a created thread's synchronous
`waitable-set.wait` receives the event of a subtask whose callee had YIELD-looped. A shape where the callee
completes by calling back synchronously, without looping, runs correctly. Separately, and possibly
unrelated, the same build refuses a `future` copy whose two ends belong to one instance with *"resource not
present"*, with or without threads.
