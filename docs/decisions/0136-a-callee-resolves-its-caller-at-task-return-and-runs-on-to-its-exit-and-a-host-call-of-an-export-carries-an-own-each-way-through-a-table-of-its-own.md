# 0136 — A callee resolves its caller at `task.return` and runs on to its exit, and a host call of an export carries an own each way through a table of its own

Date: 2026-11-25 · Status: **accepted**, 2026-11-25, in the slice. The contract reading it rests on is Scott's: his stamp of 2026-11-24 on [#1382](https://github.com/scttfrdmn/burroughs/issues/1382) (C1, §6 R-5's append: *"A host-initiated call of a component export, such as `wasi:http/handler#handle` once per request … is a call, not a delivery of readiness"*), ruling 4 of his E2 rulings on [#1375](https://github.com/scttfrdmn/burroughs/issues/1375). Everything here is an engine mechanism inside `internal/component`: no contract text changes, no public surface is added, and no stamped ADR is reversed, so by the #647 review (*"Report decisions taken, not rulings requested. Escalate only for: contract (§) text, public API surface, reversing a stamped ADR"*) it is decided here and reported as a decision taken. The design is R2's, from its closing comment on [#1372](https://github.com/scttfrdmn/burroughs/issues/1372), whose gaps 1 and 2 are this slice's subject. The record is the actor's, so commits resting on it are `Ratio-Class: carried`. Filed against [#1384](https://github.com/scttfrdmn/burroughs/issues/1384), E2-in-a of the v0.16.0 plan ([#1370](https://github.com/scttfrdmn/burroughs/issues/1370)).

**Every citation into this tree names a symbol rather than a line** (ADR 0047).

## Context

Serving `wasi:http/handler#handle` is a host call of an async-lifted export, once per request. R2 found two gaps in the engine for it (#1372 (c)), both verified in code again for this record:

1. **The result arrives at the task's exit, not at `task.return`.** `taskReturn` only recorded the result on the task. `runLiftTask` returns only from its `callbackExit` arm, and the cross-component adapter (`liftAsAsyncImpl`) resolved the caller's subtask only after `runLiftTask` returned. A handler that returns its response and then streams the body would deadlock against a host that waits for the response before reading the body.
2. **No own crosses a host's call of an export.** `lowerOneArg` carried `u32`, `string`, `list` and `record`, and `liftFlatValue`, the lift inside `task.return`, had no arm for an own, so `handle(request: own<request>) -> result<own<response>, error-code>` could be neither called nor answered.

The pin answers the first. `canon_task_return` lifts the result and calls `Task.return_`, which calls the caller's `on_resolve` at once (def:Task.return_:"self.on_resolve(result)") and only then marks the task RESOLVED. The callee runs on in the same callback, and its task ends at `exit_implicit_thread`, whose `unregister_thread` traps if it was never resolved (def:Task.unregister_thread:"trap_if(self.state != Task.State.RESOLVED)"). For a cross-component caller, `on_resolve` is `canon_lower`'s, which lowers the result and resolves the subtask there (def:canon_lower.on_resolve).

### What the oracles do, measured

`task-return-early.wasm` (testdata, with its `.wat` and `.reading`, committed before this record) composes a parent whose async `go(row)` async-lowers its child's async `g(row)`, reads g's result when its subtask is RETURNED, asks the child's sync `exited` whether g has reached its EXIT, releases g, and then waits for it to exit. Each row was read on wasmtime 49.0.3 (`run -W component-model-async=y,component-model-more-async-builtins=y --invoke 'go(ROW)'`, 10 of 10), on the pin 2e7895c (`refread.py`'s `task_return_early`, both profiles, 8 seeds), and on Burroughs at f358ff8, gate:cm-threads off and on:

| row | g | wasmtime 49.0.3 | pin | Burroughs before |
|---|---|---|---|---|
| early | `task.return`, then YIELD until released | 66055 | {66055} | hang |
| later | YIELD; its first callback returns, then YIELD until released | 65799 | {65799, 66055} | hang |
| trap-after | `task.return`, YIELD, and its callback traps | trap (`unreachable`) | {trap} | hang |
| twice | `task.return` twice | trap (*"`task.return` or `task.cancel` called more than once"*) | {trap} | hang |
| no-return | EXIT with no `task.return` | trap (*"async-lifted export failed to produce a result"*) | {trap} | 70144 |
| at-exit | YIELD; its callback returns and EXITs | 69895 | {69895, 70151} | 69895 |
| cancel-poll | YIELD until TASK_CANCELLED, then `task.return` and EXIT, while the parent's async cancel is followed by a poll that never suspends | no result in 10s, spinning, 3 of 3 | {4615} | 4615 |

The value is g's result, the lower's state, `exited` when the parent read the result, and `exited` at the end, packed in that order from bit 0, 8, 12 and 16. **66055 and 65799 are the parent holding g's result while g has not exited**, which both oracles read and Burroughs could not: g waits for the parent's release, and the parent waited for a resolution the adapter made at g's exit. The pin's second values are its random profile running g's next callback inside the lower; Burroughs ends a YIELD arm's segment as the deterministic profile does (ADR 0095's amendment, F0), which is wasmtime's reading too.

**cancel-poll is the hand-back row, and the oracles part on it.** The pin's async `subtask.cancel` resumes the callee inside the cancel (upstream #726), so g's TASK_CANCELLED callback has returned before the parent polls. wasmtime 49.0.3 runs g only when the parent suspends, and this parent's poll never does, so it spins. Burroughs returns BLOCKED from an async cancel and runs the callee's callback beside its canceller (#999's recorded divergence), so this is the one row in which a callee's `task.return` runs while its caller executes. It read the pin's 4615 on main, and the row exists to keep it.

**The trap rows found a second divergence on the same path.** A cross-component callee's trap resolved the caller's subtask RETURNED with nothing (`crossComponentResolve`'s `NoResolution`) and set no lock, so the parent read a result of 0 and ran on: 70144 is `0 | 2<<8 | 1<<12 | 1<<16`, and trap-after and twice hung waiting on work that would never come. Both oracles end the store at the trap (def:trap:"current_instance().store.lock_down()"), which ADR 0105 already made Burroughs' rule for every trap it sees. This one it did not see, because the adapter's goroutine was not among the places ADR 0105 sets the lock.

## Options

**For the resolution:**

1. **An on-return hook on the task, called by `task.return`.** The task carries the caller's resolution; `task.return` calls it after recording the result. *For:* it is the pin's shape (`Task.on_resolve`), one mechanism serves the cross-component adapter and a host call, and `runLiftTask`'s loop does not change. *Costs:* the hook runs inside the callee's entry, so it runs holding the callee's run slot, and what it does there needs a lock-order argument.
2. **A channel the loop selects on, and resolution by the caller's goroutine.** *Costs:* the callee's next segment would race the resolution: the caller could resume before its result store landed, or after the callee had run further than the pin lets it. The store belongs at the return, inside the callee's call.
3. **Return from `runLiftTask` at the return and run the rest on a new goroutine.** *Costs:* a second loop driver per task, and a goroutine site for every async call, to answer a question the hook answers in place.

**For an own crossing a host call:**

- **(a) A host table of its own** (`newHostTable`): a `hostResources` that belongs to no instance. An own argument moves out of it into the callee's table, and an own in the result moves from the callee's table into it. *For:* it is the pin's host-as-instance reading (the host lifts from its table and the callee lowers into its own, def:lift_own and def:lower_own), and `mint`, `give` and `takeAs` already do each step.
- **(b) Pass Go objects beside the values.** *Costs:* `canon.Value` carries no object, so a parallel channel of objects keyed by position, which is the drift a value union exists to avoid.

## Choice

**Option 1, with (a).**

### The hook

`liftTask.onReturn` is set by the caller before the task's goroutine starts. `taskReturn` looks up the caller's task first (the pin's order, def:canon_task_return:"task = current_task()"), lifts the result, records the resolution under `asyncHandles.mu` exactly as before, and then, **with no engine lock held**, calls the hook with the lifted result and the calling agent's Thread record. The record is recorded before the hook runs, so a second `task.return` traps on the resolved state and cannot answer twice. `task.cancel` does not call it (below).

`runLiftTask` is unchanged. **The callee's task runs on to its exit under the run slot as before**: the hook is a call inside one of its entries, and the loop that follows decodes the callback's code and parks, yields or exits as it always did. The task's exit still frees its implicit Thread and its entry in `liftsInFlight`, and an EXIT with no `task.return` still traps there.

### The cross-component caller

`liftAsAsyncImpl` sets the hook to resolve the parent's subtask (`crossComponentResolve`) at the child's `task.return`, on the agent executing it. The store's chain is that agent's held fence reader, so the caller's fence admits the store, as it admitted the store the adapter made at the exit (F1). **Lock order, the argument:** the hook waits for the caller instance's run slot while the callee holds its own. A callee segment past its first suspension holds a reader of the caller's fence, and a writer (any take of the caller's slot) is admitted only when no reader is in flight, so the caller cannot be executing and waiting for the callee's slot. A first segment holds no reader, and runs inside the caller's lower, which is host code, so the store is queued for the caller's `leaveHost` and waits for nothing.

**The one exception is upstream #726's hand-back window.** There the callee's TASK_CANCELLED callback is exempt from the caller's fence, and an async canceller has been released at once and runs on, so the argument above does not hold: a store waiting for the caller's slot inside the callee's entry waits on a caller that may be waiting for the callee's slot. cancel-poll is that case, and resolving inside the window deadlocks it. So inside the window the hook answers nothing, and the exit resolves the parent as on main. That is what a sync canceller's judgement after the hand-back already reads (`subtaskCancel`: *"Resolved by the delivery: what remains is the result store landing"*), so nothing on the cancel path moves.

**What happens to the caller's wait:** nothing new. The subtask resolves at the child's return, so a caller in its lower reads RETURNED inline, a caller WAITing on the subtask gets its event, and one that polls sees it. At the child's exit the adapter does nothing more for a child it already answered; a child that never returned resolves the subtask as before.

**And a child's trap locks the store.** The adapter's goroutine sets the store's lock from the child's trap (`isTrap`) before the child's first segment ends, whether or not it had returned, so the caller's lower and every later wait and built-in end with the lock's error, which wraps the trap (lockdown.go). It no longer resolves the parent RETURNED with nothing. This completes ADR 0105's *"set by the first trap anywhere in the store"* at a place it did not reach, so it reverses nothing. A child that failed with no trap of its own (a park's bound, a refusal) still resolves the parent with no value, for which there is no ABI status.

### The host call

`Instantiated.callReturning(ctx, name, held, onReturn, args…)` is internal, and is what E2-in-b's `Component.Handler()` will use:

- It calls an **async-lifted** export only; a sync lift's answer is its exit, which `CallValues` carries. It takes the store lock's entry check and exit mapping from `CallValuesCtx`.
- **onReturn is called once, with the lifted result, inside the `task.return` that resolves the task**, on the callee's agent and while it holds the instance's run slot. So it must hand the result off and return; it must not block on the callee's progress or call into the instance. It is not called for a task that traps first or is cancelled.
- **It returns when the task has exited**, with nil or the error that ended it, including a trap after the return, which locks the store as any trap does. Whether onReturn ran is how a caller tells a failure before the answer from one after it.
- **A top-level `own<R>` argument** is `canon.Own(R's RT, its index in held)`. It is checked against the host resource the declared own names (`crossOwnType`), and moved from held into the callee's table **on the callee's entry**, inside its task, where def:canon_lift lowers its parameters (`lowerPending`'s own arm). A call that ends before its entry leaves the handle in held. A host's bad handle is a caller error, not a guest trap.
- **An `own<R>` anywhere in the result** is moved from the callee's table into held **inside `task.return`**, where def:lift_own removes it (`liftFlatValue`'s own arm, through `ownsInto`), with the pin's four traps on the guest's handle. The result value carries its index in held, and the host takes the object with `takeAs`. `canon.Value.OwnHandle` reads the index.

**What an embedder's call does with an own result:** `CallValues`, and so `Component.Call`, refuses an async export whose result carries an own, by name and before the guest runs. It has no table for the own to enter. On main such a call ran the guest and trapped its `task.return` (`liftFlatValue` had no own arm), blaming the guest for the host's limit and locking the store. `Component.Call` keeps refusing resource handles, as its documentation says, and keeps returning at the task's exit.

## Consequences

- **A cross-component caller is resolved at its callee's `task.return`**, and the callee runs on to its exit. task-return-early's first six rows match wasmtime 49.0.3 and are in the pin's sets, and cancel-poll reads the pin's value, gate:cm-threads off and on.
- **A cross-component callee's trap ends the call**, as both oracles end it.
- **The host can call `handle`'s shape**: mint an own argument, be answered at the return with an own in a `result`, and see the task run on.
- **Named limits:**
  - **A `task.return` inside the hand-back window resolves at the exit**, as above. The pin has no such window, since it runs the callee inside the cancel; the window is the async-cancel divergence recorded on #999.
  - **`task.cancel` still resolves at the task's exit.** The pin's `Task.cancel` calls `on_resolve(None)` at the call (def:Task.cancel), so a cancelled callee that calls `task.cancel` and then waits on its caller would deadlock here. Moving it puts the result store inside upstream #726's hand-back window, where a sync canceller waits for the callee's callback to return; that is its own reading, with no row asking for it yet. wit-bindgen's guests return EXIT from the callback that cancels.
  - **`CallValues` and `Component.Call` are answered at the exit**, which is public behaviour, unchanged. A guest that returns and then waits on its embedder through them would wait forever; E2-in-b's surface is the one answered at the return.
  - **Only a top-level own argument crosses**, and no borrow, stream or future in either direction, as the cross-component boundary's handles are limited (#1281). `handle`'s request is top-level, and its response is inside a `result`, which the result's lift reaches.
  - **An own the host is handed and never takes stays in held** until the host closes held (`closeAll`). The table is the host's.

## The build, as measured

On darwin/arm64, in the worktree of #1384's branch. The readings were committed before this record and before the engine code.

- **`TestACrossComponentCallerIsResolvedAtTaskReturn`** (`taskreturnearly_test.go`): early 66055, later 65799 and at-exit 69895, 20 of 20 each, gate:cm-threads off and on, plain and under `-race`.
- **`TestACrossComponentCalleesTrapEndsTheCall`**: trap-after, twice and no-return end with the child's own trap, 10 of 10 each, both gate states, plain and under `-race`.
- **`TestAReturnInsideTheCancelHandBackIsResolvedAtTheExit`**: cancel-poll reads 4615, 20 of 20, both gate states, plain and under `-race`.
- **`TestAHostCallIsAnsweredAtTaskReturnWhileTheTaskRuns`** (`hostcall_test.go`, over `host-call-own.wasm` and the thing host of `hostresource_test.go`): the host is answered with `ok(own)` while the guest is parked on a future only `release` writes, so the call has not returned and `exited` reads 0. The own taken from held is the object the host passed, and the guest's borrow read its id. After `release` the task exits and the call returns nil. Both gate states, plain and under `-race`.
- **`TestAnOwnResultIsRefusedWithoutAHostTable`**: `CallValues` refuses the own result before the guest runs, and `callReturning` refuses a sync lift.
- **What the neuters showed** (each run and reverted; the full list is at the sites as `NEUTER, run:` comments):
  - **The hook is the whole of the early resolution.** With `taskReturn` skipping it, early and later hang again in both gate states and at-exit still reads 69895, and the host call is never answered.
  - **The trap's lock is the whole of the trap rows.** With the adapter resolving a trapped child as before, trap-after and twice hang and no-return reads 70144.
  - **The hand-back exception is what keeps cancel-poll.** Resolving inside the window too, the row's first run deadlocks, and Close does not end it. The goroutine dump is the cycle: the child's `taskReturn` waits in `onResolve`'s `acquireRun` for the parent's slot while holding its own, and the parent's sync call of `exited` waits in `invokeSync`'s for the child's.
  - **Each own move is load-bearing.** Refusing the result's own ends the host call before an answer; patching the host's index into the argument without moving it makes the guest's borrow trap.

## Rollback

Leave `liftTask.onReturn` unset in `liftAsAsyncImpl`, and resolve at the exit as before: the cross-component path returns to main's behaviour, deadlock included, and nothing else moves. `callReturning` has no public caller, so removing it removes nothing an embedder sees. The trap's lock stands on its own; reverting it restores the RETURNED-with-nothing resolution.
