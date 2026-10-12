# 0101 — A component instance executes one task at a time, through a run slot released only where the reference suspends

Date: 2026-10-12 · Status: **open** — awaiting the chair's ruling. Implemented in the same slice under the
chair's #975 order (*"If it's an engine defect: fix it with a witness that fails without the fix"*). Open
rather than accepted because that order directs a fix and does not stamp a design. `Ratio-Class: carried`.

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
- **Not covered, recorded rather than closed:**
  - A created Thread that blocks in `waitable-set.wait` keeps its chain's slot. In the reference its
    `block` ends the chain. That is ADR 0100's open edge and not this one: the scheduler waits on a handoff
    and a wait is not one.
  - An async task's callee that suspends with nothing runnable keeps the slot under its chain's hold, so a
    sync task cannot run meanwhile. The reference would run it.
- **The core-level test that first showed the race** still drives two core `Invoke`s with no slot, which is
  ADR 0073's population and ADR 0064's plain region (`Caller.Write`). Two agents of one unshared memory
  writing one address is a guest race the detector is entitled to report. The test stops constructing it
  (#975).
- **An open observation, not decided here:** under `-race`, two goroutines byte-writing one 4 KiB region
  for about 14 ms through `memory.fill` produced no report. No synchronization on the dispatch path explains
  it. Recorded on #975 as a question about what `-race` can see of the plain region.
