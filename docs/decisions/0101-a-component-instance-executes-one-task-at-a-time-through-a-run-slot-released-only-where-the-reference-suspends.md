# 0101 — A component instance executes one task at a time, through a run slot released only where the reference suspends

Date: 2026-10-12 · Status: **accepted** — chair's ruling, in session, 2026-10-12, on the #976 review:
engine design rather than scope, so the chair's to stamp. It was implemented first, under the chair's #975
order (*"If it's an engine defect: fix it with a witness that fails without the fix"*), and held open until
this ruling, since that order directed a fix and did not stamp a design. The acceptance came with four
requirements, discharged in the follow-up and recorded under **Follow-up** below. `Ratio-Class: carried` on
both commits: the order and the ruling are in session and have no approval artifact to cite.

**Every citation into this tree names a symbol rather than a line**, per ADR 0047. Citations into
`definitions.py` name a function or a method, since that file is fetched material under a gitignored path.

## Context

[#975](https://github.com/scttfrdmn/burroughs/issues/975) began as one `-race` report on Windows, in
`TestConcurrentHostCallsShareOneThreadID`. The two writes were both in `waitableSetWait`'s `c.Write`, made by
two goroutines that the test started with concurrent core `Invoke`s. Darwin did not reproduce it in 8,200
runs. Read from the code, it is not that test's problem.

**What the reference does.** One thread runs at a time:

- `Store.tick` resumes **one** ready thread;
- `Thread.resume` runs it until it suspends (`suspend`, under `block` and `switch_to`).

Two tasks of one instance therefore interleave only at suspension points, never in parallel. Both may be in
flight, and both may be parked at once. What cannot happen is both executing.

**What Burroughs did.** `asyncHandles.entrySem` serializes async-lift entries. It is the reference's
`exclusive_thread`, which `Task.needs_exclusive` asserts only of async-typed functions, so a sync-typed
task does not take it there either. `compFunc.invokeWith`, the sync-lift path, took nothing. So two
`CallValues` on one instance's sync export executed guest code on two goroutines over one unshared memory.

- **Measured:** two callers took the wall time of one, 2.2 ms each way, scaling 10× with the work.
- **Witnessed:** `TestTwoCallsIntoOneInstanceNeverExecuteAtOnce` failed 3 of 3 before this change.

The path is default-on, because `gate:async` is on by default and `Component.Call` uses `CallValues`.

**Two locks, not one, because the reference has two rules.** `exclusive_thread` is backpressure:
an async-callback task holds it across its whole callee, *including* while parked in a sync
`waitable-set.wait`. "One thread runs" is execution: a task parked in that same wait does **not** prevent
another task from running. Folding the second rule into `entrySem` would make a sync task wait for an async
task's whole callee, including its waits, which the reference does not do.

## Decision

**A per-instance run slot, `asyncHandles.runSlot`, held by whichever agent is executing the instance's guest
code.**

1. **Taken at every entry**, after `entrySem` where both are taken, and never in the other order:
   - `compFunc.invokeWith` (sync lift);
   - `compFunc.enterAndInvoke` and `compFunc.enterInvokeLowering` (async-lift entries);
   - `threadTable.yieldPoint` (a chain started at YIELD).
2. **Released only where the reference suspends**, and taken back before the agent returns to guest code:
   - `waitable-set.wait`;
   - a sync `subtask.cancel`'s wait.

   Both go through `c.Blocking(h.suspending(c, fn))`. `asyncHandles.suspending` wraps the excursion's
   body rather than replacing `Blocking`, so `TestEveryCanonFunctionsWaitIsInsideABlockingExcursion`
   still finds every wait lexically inside a `Blocking(...)` call. The first draft hid `Blocking` behind
   another name, and that control refused it.

   The re-take happens **inside** `c.Blocking`, so an agent waiting for the slot is counted at a safepoint
   (SP-2) like any other blocked agent.
3. **Held** across everything that is not a suspension in the reference:
   - a sync host function, which the reference runs inline on the thread;
   - `blockingRead`;
   - an async lower whose impl resolves inline;
   - a sync cross-component call.

   A held slot blocks the instance, as the reference's single-threaded store does.
4. **The holder is recorded by component-model Thread index**, the one the stack carries
   (`CanonCaller.CMThreadIndex`). A suspension point releases only a slot the calling agent holds. Two
   populations hold nothing to release:
   - a core-level `Invoke` that bypasses the component, which ADR 0073's two-agents population still
     permits at the core API;
   - a created Thread, which runs under its chain's hold (ADR 0100's amendment).
5. **`enterInvokeLowering` enters with the Thread index**, as `enterAndInvoke` already did. It entered
   through a plain `Invoke`, so a callee entered with lowered arguments carried no index. Point 4 needs one.

## Consequences

- **Concurrent `Component.Call`s on one instance execute one at a time.** They are still all in flight, and
  they interleave at suspension points. An embedder who wanted parallelism from one instance got it only by
  violating the component model, and loses it here.
- **The race #975 reported is closed at its cause.** Two callers can no longer be past `waitable-set.wait`
  at once, so its event store is ordered by the slot's channel.
- **Not covered, filed as issues** (both behind `gate:cm-threads`, which defaults off):
  - [#977](https://github.com/scttfrdmn/burroughs/issues/977): a created Thread that blocks in
    `waitable-set.wait` keeps its chain's slot. **It can deadlock, and the sequence is reachable**,
    derived from the code: the parent passes the child one of its own lifts (`walker.compArgRef`), and
    the child calls it to service the Thread's request. The reference progresses there, because its
    `block` ends the chain.
  - [#978](https://github.com/scttfrdmn/burroughs/issues/978): an async task's implicit Thread suspended
    mid-callee with nothing runnable keeps the slot, so a sync-typed call cannot enter. **Divergent:** in
    the reference, `Task.enter_implicit_thread` consults `exclusive_thread` only for async-typed entries,
    so the sync call enters and runs.
- **The core-level test that first showed the race** drove two core `Invoke`s with no slot, which is
  ADR 0073's population and ADR 0064's plain region (`Caller.Write`). Two agents of one unshared memory
  writing one address is a guest race the detector is entitled to report. The test no longer constructs it
  (#975).

## Follow-up: the chair's four requirements on accepting this record

**1. The behaviour change is stated where embedders read it.** `Component`'s doc comment now has a
*Concurrent calls* section: calls execute one at a time, interleaving at suspension points, and an embedder
wanting parallel guest execution uses separate `Component`s. The change is also under **Changed** in the
CHANGELOG, not only under Fixed. A sweep of the README, `docs/` and the component doc comments found one
sentence that read as parallel execution (`asyncHandles.lift`'s *"may run concurrently in one instance"*),
now corrected. `docs/laws/controls.md`'s three-concurrent-callers specimen is about a core interp
instance, ADR 0073's population, and is unchanged.

**2. The core door is not public, by symbol.**
- `burroughs.Component`'s one field is an unexported `*component.Instantiated`, and its methods are
  `Exports`, `Call` and `Close`.
- `ComponentConfig.Run` reaches guest code through `Instantiated.CallRun` → `Instantiated.Call` →
  `compFunc.invoke` → `compFunc.invokeWith`.
- `Component.Call` reaches it through `Instantiated.CallValuesCtx` → `compFunc.invokeWithPending`.
- A component's core instances live only in the unexported `walker.toClose`, and `internal/component`
  cannot be imported from outside the module. The only reader of `toClose` beside `walker.close` is the
  test helper `coreInstanceWithExport`.

So no embedder can reach a component's core `Invoke`, and every public entry into guest code takes the
run slot. #975's hole is closed on every public path.

**3. The race detector's silence is explained, and it is an instrument finding with reach**
([#979](https://github.com/scttfrdmn/burroughs/issues/979)).
- `interp.allocate` maps linear memory as an anonymous `syscall.Mmap` reservation on unix
  (`reserve_unix.go:mapReservation`). The only exception is when `reservationFor` returns 0: a declared
  max that fits the initial size, i.e. max equal to the initial size.
- On `!unix`, Windows included, `mapReservation` always fails, so `allocate` falls back to `make`, which
  is Go heap.
- The race detector shadows only memory the Go runtime allocates.

**Measured on darwin/arm64, with the run slot neutered:** one guest program whose only difference is the
memory's declared max. `(memory 1)`, mmap: **0 reports** in 5 runs. `(memory 1 1)`, heap: **reported**,
`execMemoryFill` against `execMemoryFill`. With the slot restored, the heap variant gave **0 reports in 50
runs**. That is #976's fix confirmed under a detector that can see guest memory, which the record's
earlier evidence could not claim.

This is why Windows reported #975 once and 8,200 darwin runs and every Linux CI run never did. The reach is
that **the `race` gate sees no guest-memory race on unix** unless a memory's max equals its initial size.
The remedy is proposed in #979 and not adopted: race witnesses that cover guest memory use heap-backed
memories.

**4. The two uncovered items are filed** as #977 and #978, above, each with its deadlock or conformance
classification.
