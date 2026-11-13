# 0121 — wait-for and wait-until are async host imports on the host's monotonic clock, cancelled inline and stopped by Close, and a sync-lifted function blocks or traps by its type

Date: 2026-11-13 · Status: **accepted**, 2026-11-13. Scott, in session, approving the plan for v0.14.0's seventh slice (E5, #1233, with grave #1267). The approval was given in session and has no comment to cite, so the record is the actor's, and commits resting on it are `Ratio-Class: carried`. It is an engine mechanism inside `internal/component`: no contract text, no public API (no root-package file changes and no exported symbol), and no stamped ADR is reversed. Filed against [#1233](https://github.com/scttfrdmn/burroughs/issues/1233) and [#1267](https://github.com/scttfrdmn/burroughs/issues/1267).

## Context

WASI 0.3's `wasi:clocks/monotonic-clock` has two async functions, `wait-until: async func(when: mark)` and `wait-for: async func(how-long: duration)`, both u64 nanoseconds (the WIT the nightly's std vendors, `wasip3-0.8.0+wasi-0.3.0`). They are the first async host imports the production host serves. `Host.asyncImpls` was set only by tests. Rust std's `thread::sleep` reaches `wait-for` through wasi-libc's `clock_nanosleep`, which async-lowers it and then blocks in `waitable-set.wait` inside `run`. `thread::sleep_until` reaches `wait-until` the same way, with `TIMER_ABSTIME`. `run` is async-typed (`func async`) and lifted plainly.

Measured on main at 62209b6, with `p3sleep` (below), a std guest that sleeps: *"import wasi:clocks/monotonic-clock@0.3.0::wait-for is not provided (stub host …)"*. Serving the import is not enough. Exploration found four obstacles behind it.

1. **Grave #1267: the sync tail is judged by lift form, not by function type.** `syncTail` (`cmthreads.go`) traps every waiting sync-lifted Thread with no ready candidate. The pin's `trap_if(not candidates)` is inside `if not ft.async_` (def:canon_lift:"if not ft.async_:"). ADR 0100 says the same in *"Where the trap rule does apply, and it is the sync lift's loop"*: for an async callee, *"`tick` does nothing and the task simply does not progress."* So the code disagreed with its own ADR and with the pin. A std guest's `run` would trap where both oracles block. This is a grave, not a reversal of ADR 0100.
2. **The callback-lift park bound.** `liftParkBound` (30s) ends any WAIT with `ErrLiftParkExpired`. So a callback-lifted guest's `wait-for(60s)` would expire rather than complete.
3. **`Close` never stops pending host work.** `requestCancelAll` reaches lift tasks only, and `walker.close` has no step for host work. A pending timer would fire after `Close` and resolve a subtask of a torn-down instance, which is ADR 0071 fact 4's hazard.
4. **A sync lower of an async-only import** missed every table, because `resolveHost` consults the async table only for an async lower. It bound the refusing stub with a misleading *"not provided"*.

### What the oracle does, measured

wasmtime 49.0.3 (`.oracle/wasmtime-49.0.3/wasmtime`, never the 50.0.1 on PATH), darwin/arm64, `-W component-model-async=y -W component-model-more-async-builtins=y -S p3=y --invoke`. Each row is 10 of 10 runs, on `testdata/wait-cancel.wasm` (below) unless named:

- **`wait-for(0)`, `wait-until(0)` and `wait-until(now())` resolve inline:** each lower returns 2 (RETURNED), with no subtask registered.
- **The order:** `wait-for(10s)` and then `wait-for(10ms)` join one set, and the first event is (SUBTASK, the short one's index, RETURNED). The long one's sync cancel then reads 4, so the row takes about 10 ms. A first draft used 300 ms for the long wait and read the same; 10 s puts the order beyond any scheduling delay.
- **The sync `subtask.cancel` of a pending `wait-for(1h)`:**
  - in an async-typed function, the lower reads STARTED and the cancel reads 4 (CANCELLED_BEFORE_RETURNED): 20;
  - in a **sync-typed** function, it traps: *"cannot block a synchronous task before returning"*. wasmtime's host-task cancel does not resolve synchronously. Its `subtask_cancel` *"falls through to block waiting for the host task to actually finish"* (concurrent.rs), and a sync-typed task may not block.
- **The async `subtask.cancel` of a pending `wait-for(1h)`** returns BLOCKED (-1). A later wait delivers a SUBTASK event in state 4. The row reads the event's code and state, not its index.
- **A sync lower of `wait-for(10ms)`** (`testdata/sync-lower-wait-for.wat`, whose header records the reading): it traps *"cannot block a synchronous task before returning"* in a sync-typed function. In an async-typed function it blocks and returns 7.
- **`p3sleep`:** exit 0 and nothing on stderr. stdout is three lines, byte-identical across 10 of 10 runs (`p3sleep/build.sh` takes and compares them):
  - `sleep(20ms) took at least 20ms: true`;
  - `sleep_until(deadline) returned past its mark: true`;
  - `sleep(ZERO) returned`.

### What the pin does

- `canon_lower`'s async arm returns `[RETURNED]` when `subtask.resolved()` after the callee returns, and `[state | (subtaski << 4)]` otherwise (def:canon_lower:"return [subtask.state | (subtaski << 4)]").
- `canon_subtask_cancel` calls `subtask.on_cancel()`. If that resolved the subtask, both forms return its state. Otherwise the sync form waits and the async form returns BLOCKED (def:canon_subtask_cancel:"if not async_ and not subtask.resolved():").
- So whether a cancel reads 4 or BLOCKED is the **host's** choice of whether its `on_cancel` resolves before it returns. The pin defines no clock host.
- A sync lower of an async callee waits (def:canon_lower:"thread.wait_until(subtask.resolved)"), and the wait meets the sync tail only if the lifted function is sync-typed.

## Options

For the sync tail (grave #1267):

1. **Judge by the lifted function's type:** a waiter is judged only if its lift's `sig.Async` is false. This is the pin's `if not ft.async_`.
2. **Keep judging by lift form, and lift std's `run` some other way.** *Costs:* the guest's binary is fixed, and the rule stays wrong for every async-typed, sync-lifted export.

For the cancellation:

1. **`onCancel` stops the timer and resolves the subtask inline,** with `on_resolve(None)`. Both forms then read 4, the pin's arm for a host whose `on_cancel` resolves.
2. **Stop the timer and resolve later, from another goroutine,** to match wasmtime's BLOCKED. *Costs:* it manufactures latency the pin does not need, to copy a host implementation detail. And the sync-typed sync form would then have to trap to match wasmtime, which the pin does not do for a host callee.

For `Close`:

1. **A per-instance registry of pending host waits on the instance's async table**, stopped by `walker.close` across the tree of nested instances.
2. **A registry on the `Host`.** *Costs:* one Host serves many instances, so one instance's `Close` would stop another's timers, or would need a key back to the instance anyway.

For a sync lower of an async-only import:

1. **Refuse at instantiate, by name.**
2. **Build the pin's blocking sync lower.** *Costs:* a second waiting path in the sync lower adapter, with its own sync-tail judgement, for a form that neither wasi-libc nor std emits.

## Choice

1. **`wait-for` and `wait-until` are served as async host imports, for WASI 0.3 only** (`hostsys.go`).
   - A production async table, `asyncSource`, registers both through `serve(m, wasiP3, …)`, and `asyncWasi` merges the test-only `asyncImpls` into it under the no-shadowing rule (`mergeExtras`), the way `wasi()` merges `syncImpls`.
   - An entry is a factory the walk calls with the instance's async table, as `streamConsumer`'s is, because a pending wait is the instance's state.
   - `onStart` lifts the one u64. The deadline is on the host's monotonic clock: `time.Since(h.start)`, the clock `now` reads. `wait-until`'s `when` is that clock's mark. `wait-for`'s deadline is `now + how-long`, saturating.
   - **A deadline at or before now resolves inline,** so the lower returns 2, as the oracle's does.
   - Otherwise a `time.AfterFunc` resolves the subtask when the deadline passes: `onResolve(canon.Value{})`, RETURNED.
2. **The cancel stops the timer and resolves inline** (option 1).
   - The race is closed by one claim, under `asyncHandles.mu`: the timer's callback and `onCancel` each remove the wait from the registry, and only the remover acts.
   - If `onCancel` wins, it stops the timer and resolves `canon.NoResolution()` before it returns. So both forms read CANCELLED_BEFORE_RETURNED (4) inline.
   - If the timer won, the cancel does nothing, and the subtask resolves RETURNED, which is #1009's rule: the arm is chosen by the resolution, not by the request.
   - **The timer callback is a new concurrent caller of `onResolve`**, authorised here, under ADR 0101's result-store amendment. It takes the caller instance's run slot, or queues the store while the slot's holder is in host code. ADR 0095's census is the precedent: what is new is who calls `onResolve`, not where the store lands or what orders it. The store writes nothing, since both imports return no result. It marks RETURNED and wakes the set under `asyncHandles.mu`, which is the B-MM-1 edge the blocking arm already has.
3. **Grave #1267: a sync-lifted function is judged by its type** (option 1).
   - A lift's implicit Thread records whether its function type is async (`cmThread.asyncTyped`, from `sig.Async`). `syncTail` judges only a waiter whose function is sync-typed.
   - A sync-lifted, async-typed function with no ready candidate keeps waiting on its wake, which is what the pin's `tick` does when nothing is ready.
   - A sync-typed export keeps the trap, in both gate states. The cancel726 rows and `TestASyncWaitOnAnotherInstancesEventTraps` are sync-typed, and are unchanged.
4. **Pending host waits are the instance's, and `Close` stops them** (option 1).
   - `asyncHandles.hostWaits` holds every wait that has started and not yet fired, been cancelled or been stopped.
   - `walker.close` stops them right after `killAllParked`, across `w.children`. The stop marks the table closed, so no later wait can start or resolve, and stops each timer.
   - A callback that claimed its wait before the stop is waited for at the end of `close`. It runs to the end of its store, after the core instances have closed, so the slot it may be waiting for has been released.
   - **The park bound stands where nothing can wake the park.** `awaitEvent`'s bound re-arms instead of expiring while the instance has a pending host wait, since that wait is a waker. With none, the bound keeps its purpose, a named outcome rather than a hang.
     - **A callback past its claim still counts as pending** (`hostWaitsResolving`), until its store has landed. The first build counted only the registry. Under `-race`, the park witness's 5 ms bound then expired in the window between the timer's claim and its store, 1 run of 3: the wait had left the registry, and its event had not yet arrived.
     - **An event already pending re-arms the bound too.** A wake and the deadline ready together are chosen between at random, and a park whose event has arrived has not expired.
5. **A sync lower of an async-only host import is refused at instantiate,** by name (option 1): *"canon lower of wasi:clocks/monotonic-clock@0.3.0::wait-for has no async option, but the host serves it only as an async import …"*. The refusal is `ErrUnsupportedForm`, since the pin admits the form and Burroughs does not build it.

### Divergences from wasmtime 49.0.3, recorded

ADR 0102 makes the pin the semantics. Each row below is the pin's arm for a host whose `on_cancel` resolves before it returns, against wasmtime's host, whose cancel does not.

- **The async-form cancel** reads 4 inline. wasmtime returns BLOCKED and delivers a SUBTASK event in state 4 at the next wait.
- **The sync-form cancel in a sync-typed function** reads 20. wasmtime traps *"cannot block a synchronous task before returning"*. In an async-typed function both read 20.
- **A sync lower of `wait-for`** is refused at instantiate. wasmtime traps it in a sync-typed function and blocks in an async-typed one, which is the pin's behaviour too. Building the pin's blocking sync lower is a follow-up.

### Out of scope, recorded

- **The pin's blocking sync lower of an async import** (def:canon_lower:"thread.wait_until(subtask.resolved)"), which wasmtime also builds. It is refused here by name. A follow-up is to be filed.
- **A timer callback blocked on the run slot at `Close`.** `Close` waits out a callback that claimed its wait before the stop. Its store takes the run slot, which the core instances' `Close` frees. A store held up by a cross-component fence that never clears would hold `Close` with it. No committed guest reaches that, and no witness covers it.

## Consequences

- **Witnesses:**
  - `TestP3SleepMatchesWasmtime`, in both gate states. It runs `testdata/p3sleep/p3sleep.wasm` under `gate:async` through `CallRun`, and compares stdout, stderr and the exit code with wasmtime 49.0.3's reading, `testdata/p3sleep.reading`, byte for byte. The guest is a dependency-free Rust `wasm32-wasip3` program (nightly 1.100.0, built offline by `p3sleep/build.sh`, which takes 10 readings and refuses unless they are identical). It calls `thread::sleep(20ms)`, `thread::sleep_until(now + 20ms)` and `thread::sleep(ZERO)`, and prints whether each one's deadline had passed.
  - `TestWaitCancelMatchesWasmtime`, in both gate states. It runs `testdata/wait-cancel.wasm`'s five rows against `wait-cancel.reading`. Three match wasmtime. The two divergences are asserted at the pin's value, after checking that the reading still says what this ADR records.
  - `TestAHostWaitResolvesOnce`: the claim, in three orders (fire then cancel, cancel then fire, stop then both), drives the wait directly.
  - `TestCloseStopsAPendingWait`, also run under `-race`. `wait-park.wasm`'s `start` leaves a `wait-for(1h)` pending. After `Close` the timer is stopped, the registry is empty, and a forced fire does not resolve the subtask.
  - `TestALongWaitOutlivesTheParkBound`: `wait-park.wasm`'s callback-lifted `run` waits on a `wait-for(300ms)`, with `liftParkBound` at 5 ms, and reads 18 (SUBTASK, RETURNED), as wasmtime does, 5 of 5.
  - `TestASyncLowerOfWaitForIsRefusedAtInstantiate`: `sync-lower-wait-for.wasm` is refused, naming `wait-for`.
  - `TestAnAsyncTypedSyncWaitOnAHostEventBlocks`, the async-typed twin of `TestASyncWaitOnAnotherInstancesEventTraps`, in both gate states. `async-waitset-async-typed.wasm` is `async-waitset-synth` with `run` async-typed, and it waits for the host's `op` and returns 7. The host resolves `op` once the guest is parked on the set, a place read under the table's lock rather than a delay.
- **Neuters, run**, each named at its site:
  - `syncTail` without the type test: p3sleep traps *"cannot block a synchronous task before returning"* in both gate states. So do the twin and `wait-cancel`'s `order` row.
  - `waitUntilMark` resolving inline whatever the deadline: p3sleep's sleep and sleep_until lines both read false.
  - `wait-until` read against the system clock (`time.Now().UnixNano()` for the mark): p3sleep's sleep_until line reads false.
  - `hostWait.cancel` doing nothing: the three sync cancels in `wait-cancel` expire as `ErrSubtaskCancelExpired` (bound shortened to 2s). The async cancel reads BLOCKED and waits on its 1h timer until the hang guard. The claim test's cancel-then-fire order resolves RETURNED.
  - `hostWait.cancel` without its claim: fire-then-cancel resolves twice, and a cancel after the stop resolves once.
  - `walker.close` without `stopAllHostWaits`: the timer is still pending after `Close`, and the forced fire resolves the subtask.
  - The park bound expiring unconditionally: the long wait reads `ErrLiftParkExpired`.
  - The sync-lower refusal removed: the fixture instantiates, and its call reaches the stub's *"not provided"*.
- **Tests changed:** none. The cancel726 rows, `TestASyncWaitOnAnotherInstancesEventTraps`, every liftpark, runslot, cancel and cm-threads suite, and p3std, p3sys, p3async-hello, p3hello and p3echo pass unchanged, with and without `-race`.
- **What a guest sees.** A WASI 0.3 std guest's `thread::sleep` and `thread::sleep_until` return once their deadline has passed. A guest that cancels a wait reads CANCELLED_BEFORE_RETURNED at once. `Close` with a wait pending returns, and nothing it started runs after it.
- **No contract text changes, and no public API.**

## Rollback

Each of the following restores its defect, and each is a neuter run against its witness:

- restoring the lift-form judgement in `syncTail` brings back #1267's trap;
- dropping the registry stop from `walker.close` brings back the post-Close resolution;
- restoring the unconditional park-bound expiry brings back `ErrLiftParkExpired` for a long wait.
