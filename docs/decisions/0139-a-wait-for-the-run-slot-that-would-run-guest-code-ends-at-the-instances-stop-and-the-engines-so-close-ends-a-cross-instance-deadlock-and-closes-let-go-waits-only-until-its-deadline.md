# 0139 — A wait for the run slot that would run guest code ends at the instance's stop and the engine's, so Close ends a cross-instance deadlock, and Close's let-go waits only until its deadline

Date: 2026-11-26 · Status: **accepted**, 2026-11-26, in the slice. Everything here is an engine mechanism inside `internal/component`: no contract text changes, no public surface is added, and no stamped ADR is reversed, so by the #647 review (*"Report decisions taken, not rulings requested. Escalate only for: contract (§) text, public API surface, reversing a stamped ADR"*) it is decided here and reported as a decision taken. The record is the actor's, so commits resting on it are `Ratio-Class: carried`. Filed against [#1398](https://github.com/scttfrdmn/burroughs/issues/1398), found by E2-in-a ([#1384](https://github.com/scttfrdmn/burroughs/issues/1384), [ADR 0136](0136-a-callee-resolves-its-caller-at-task-return-and-runs-on-to-its-exit-and-a-host-call-of-an-export-carries-an-own-each-way-through-a-table-of-its-own.md)), with a second instance from E2-in-b ([#1385](https://github.com/scttfrdmn/burroughs/issues/1385), [ADR 0137](0137-a-request-is-a-host-call-of-handle-on-an-instance-that-serves-one-task-at-a-time-reused-only-after-its-task-exits-and-a-failure-before-the-answer-is-a-fixed-500.md)'s postscript).

**Every citation into this tree names a symbol rather than a line** (ADR 0047).

## Context

The run slot ([ADR 0101](0101-a-component-instance-executes-one-task-at-a-time-through-a-run-slot-released-only-where-the-reference-suspends.md)) is taken by `asyncHandles.acquireRun`: the fence first (`fenceWriterEnter`, ADR 0095's amendment, F1), then a send on `runSlot`. Its `abort` parameter ends the wait without the slot, and it was passed only where an entry may still be abandoned: an embedder's call context before the first entry, a spawned agent's context (`spawnAgent`'s `Enter` hook, [ADR 0130](0130-in-an-instance-whose-canonical-memory-is-shared-a-spawned-agent-is-a-thread-of-its-spawners-task-and-executes-outside-the-run-slot-which-keeps-serialising-tasks-and-stops-serialising-result-stores.md)), and `producerStop` for the host's deliveries. **Three waits passed nil**, so nothing could end them:

- a result store's (`asyncLowerFunc`'s `onResolve`), including the one ADR 0136's on-return hook makes inside a callee's `task.return`;
- a callback task's entries after `enterTask` has admitted them (`enterAndInvoke`, `enterInvokeLowering`);
- a sync cross-component call's (`invokeSync`), whose context is `context.Background()` carrying the caller's chain (`walker.callerChainContext`), so its `Done()` is nil.

**The defect, measured by E2-in-a's neuter.** With the hook answering inside upstream #726's hand-back window, task-return-early's cancel-poll row deadlocked on its first run: the child's `taskReturn` waited in `onResolve`'s `acquireRun` for the parent's slot while holding its own, and the parent's sync call of the child's `exited` waited in `invokeSync`'s for the child's. `Close` did not end it, and the test binary timed out. ADR 0136's hand-back exception keeps the shipped path out of the cycle, but no instrument guards a future path into it. **A teardown that cannot end a wait is the shape contract §2 T-5.4 and [ADR 0071](0071-t-5-is-live-only-membership-a-bounded-status-record-a-fault-in-two-channels-and-a-sentinel-panic-for-the-terminal-unwind.md) forbid elsewhere.**

**Every other wait already ends this way.** `waitableSetWait` selects on its set's wake, on `c.Context().Done()` (the agent's thread lifetime context, cancelled by `interp.Instance.Close` and by [ADR 0090](0090-proc-exit-is-instance-scoped-and-its-teardown-is-a-request-routed-into-the-shutdown-mechanism-that-already-exists.md)'s teardown), on its Thread's `waitAbort` and on the store's lock ([ADR 0128](0128-a-guest-wait-holds-a-timer-that-never-fires-so-gos-deadlock-detector-cannot-end-the-host-and-a-wait-nothing-can-wake-blocks-for-ever.md) names the posture: a wait nothing can wake blocks, and `Close` ends it).

**E2-in-b's instance is a different waiter on the same wait.** `HTTPServer.Close` lets every body go (`serveRequest.abortBodies`) before its bounded wait, and each let-go is a delivery (`deliverHTTP`, through `httpReqBody.Close` and `endIncoming`) on Close's own goroutine. A guest that has answered and then computes without yielding holds the slot, so Close waits inside the let-go, before its deadline is armed, and `producerStop`, the only abort the delivery passes, closes only when Close reaches the instance's own Close after that wait. ADR 0137's postscript names it; no p3serve row computes after answering, so it is not driven by a fixture.

## Options

**What ends the wait:**

1. **The instance's stop, for every wait for the slot.** The stop is `asyncHandles.producerStop`: closed by `stopProducers`, which `walker.close` runs across the tree of nested instances (`stopAllProducers`) before any core instance closes. *For:* it is already Close's signal for this exact wait, passed by five deliveries. *Costs:* a result store that gives up is dropped, and **a store is what wakes a stackless park** (`awaitEvent`), which Close does not end directly. This was the first build, and it hung a parked call past Close (below).
2. **The instance's stop for every wait that would run guest code of its own**: a callback task's entry and a sync lift's call (`acquireRunFor`). A result store keeps waiting (`acquireRun`). *For:* every holder a store can wait on then unwinds at Close — an entry or a call by the stop, guest code at a safepoint by the core instance's Close — so the store lands, and the park it wakes ends. In the measured cycle, the parent's call ends at the stop, the parent's slot is freed as its frames unwind, and the child's store lands under it. *Costs:* a cycle made only of stores would not end (named below).
3. **A new channel closed at the start of `walker.close`.** *Costs:* a second channel with `producerStop`'s meaning, the two-places-know-one-fact shape; nothing measured needs the earlier instant, since `killAllParked` and `stopAllHostWaits`, which run before `stopAllProducers`, take no slot.
4. **The store's lock as well**, as `waitableSetWait` selects on it. *Costs:* it changes what lands after a trap — a store or an entry waiting behind the trapping holder would be dropped rather than run once the holder unwinds and frees the slot — and the cycle this record is about has no trap in it. The lock is a trap's signal and the holder's unwind already answers it; a cycle needs teardown.

**The engine's signal:** the waiting agent's thread context, where the waiter is an agent of another instance and its CanonCaller is in reach. *For:* that is what `waitableSetWait`'s `c.Context().Done()` arm is, and ADR 0132's teardown of the waiter's core instance (an agent's exit or trap) then ends the wait without waiting for the component's Close. *Costs:* none for `invokeSync`, whose sync cross-component caller is in its parameters. A result store keeps waiting by Option 2, so it needs no engine signal either.

**For the let-go:** (a) pass Close's deadline to the let-go as its abort; (b) make the let-go asynchronous, which is a goroutine site needing a principal's ruling (`TestNoEngineGoroutineLandsWithoutAPrincipalsRuling`); (c) close the instance's stop before the let-go, which would end every task's own re-entries before they could exit within the deadline.

## Choice

**Option 2, with the engine's signal where the waiter is an agent and its context is in reach, and (a) for the let-go.**

### The wait

Both acquire forms share one take, `takeRunSlot(ch, rec, abort, stop, engine)`: its fence wait (`fenceWriterEnter`) and its slot send each select on the fence's wake or the slot, the caller's `abort`, `stop` and `engine`. A nil channel never fires.

- **`acquireRunFor(ch, rec, abort, engine)`** is the take for a waiter that will run guest code of its own under the slot. It passes **the instance's stop** (`producerStop`) and **the engine's** (`engine`, nil where there is none). `enterAndInvoke`, `enterInvokeLowering` and `invokeSync` take the slot through it.
- **`acquireRun(ch, rec, abort)`** keeps its signature and its behaviour: `abort` only. A result store takes the slot through it, and so do the host's deliveries, which pass `producerStop` as their `abort`, as before.
- The suspension point's re-take inside `suspending` passes nothing, as before (below).

**What an ended wait returns:** `false`, holding nothing — no fence writer counted, no slot, and `asyncHandles.runChain` untouched — which was already the contract of `acquireRun`'s false. **A wait that finds the slot free takes it**, as before: the stop ends a wait, it does not refuse a take. So the slot is tried first, on its own, and only a slot already held is waited on with the ends (*The build, as measured*). A take of a free slot after the stop holds it, so its guest code runs under it, and the core instance's Close ends that code at a safepoint as it ends any.

**Who passes the engine's signal:** `invokeSync` passes its sync cross-component caller's `caller.Context().Done()` (nil for an embedder's call). `spawnAgent`'s `Enter` hook already passes the agent's own context as `abort`. No other waiter is an agent with a CanonCaller in reach.

### How each caller unwinds

- **A result store (`onResolve`) is not ended by the stop, and lands.** Its waiter waits for the caller instance's slot. Every holder of that slot either executes guest code, which the core instance's Close ends at a safepoint, or waits for another slot as an entry or a call, which the stop ends, and its unwind frees the slot. The store then lands under the slot and wakes whatever waits on its event. That includes a stackless park (`awaitEvent`), which Close does not end directly. This is the build's correction of the first draft, which dropped the store (below).
- **ADR 0136's on-return hook** runs that store inside the callee's `task.return`, holding the callee's slot. In E2-in-a's cycle the store waits for the parent's slot while the parent's sync call waits for the callee's. The stop ends the parent's call, the parent's frames unwind and free its slot, and the store lands. The hook returns, `task.return` returns, and the callee runs on under its own slot to its exit.
- **A callback task's entry** (`enterAndInvoke`, `enterInvokeLowering`): it returns an error wrapping `errThreadKilled` before the core func runs, after releasing the entry slot (`leaveTask`) and the fence reader the entry took (`fenceEntryEnd`). The task ends with that error. **`enterTask`'s rule is not reversed.** It says an admitted entry must proceed because the call's *cancellation* is delivered through an entry, and abandoning one delivers it to nobody. Close's stop is not a cancellation to deliver; it ends the task, as Close ends a parked Thread (`killParked`) without delivering anything.
- **A sync call** (`invokeSync`): it returns before the callee runs, as its context arm already did. The error names which signal ended it: the call's context is `ErrCancelled`, Close or the engine is `errThreadKilled`. For a cross-component caller the lower's error unwinds the caller's frames as any failing canon call does (ADR 0101's point 7, step 3).
- **A spawned agent's entry** and **the host's deliveries** (producers, sockets, files, `wasi:http`, the server's `underRunSlot`): unchanged. They already handled false, on the abort they pass.
- **E2-in-b's let-go**, by choice (a): `HTTPServer.Close` derives a channel closed at its deadline before it lets the bodies go, and passes it to each let-go's delivery (`abortBodies` → `closeServedBody` → `httpReqBody.closeUntil` or `deliverHTTPUntil`, and `httpIncoming.abort` → `endIncoming`). `deliverHTTPUntil` waits with the deadline as its abort and `producerStop` as its stop. A let-go held by a computing guest gives up at the deadline, dropping its store as a stopped delivery does. Close's bounded wait then finds the deadline passed and reports false. The instance's Close follows: its stop ends every other wait, and its core instances end the computing guest. Every other let-go, and every delivery outside Close, passes nil and is unchanged.

### Why no caller then runs guest code without the slot

`takeRunSlot` returns false only from its two selects, before the slot send has succeeded, and its false path sets no holder. Every caller's false branch returns before its guest work: an entry before `lowerPending` and `InvokeCMThread`, `invokeSync` before the same, an agent before its first instruction, a delivery before its store. A result store's wait does not end at the stop, so it runs only under the slot, as before. No caller defers a release before its check, and `releaseRunFor` releases only a slot its own chain holds, so an ended wait cannot drain another holder's token either.

### What is not changed, and why

- **The suspension point's re-take** (`suspending`, after its wait returns) still waits on the fence and the slot with no signal. It waits for its own instance's slot. Its holder either executes guest code, which the core instance's Close ends at a safepoint, freeing the slot as its entry unwinds, or waits for another slot, which the stop ends for every waiter that would run guest code.
- **A callee segment's fence reader** (`fenceReaderEnter`) and **`enterTask`'s wait for a re-entry** are not given the stop. A reader is taken before the slot (F1), so it holds no slot a writer needs, and an entry holds `entrySem` only around its own run-slot take, which now ends.
- **The store's lock** (Option 4).

## Consequences

- **`Close` ends a cross-instance run-slot deadlock.** The parent's call in E2-in-a's cycle ends at the instance's stop, and the child's store then lands.
- **A teardown of a sync cross-component caller's core instance ends its wait for the callee's slot**, through the engine's signal, without waiting for the component's Close.
- **`HTTPServer.Close` returns at its deadline** when a guest that answered computes without yielding, and reports false, which `Component.Close` reports as `ErrCloseIncomplete`.
- **After `stopProducers`, a callback entry or a sync call still waiting for the slot gives up**, where it used to wait for the slot and then run, or find its core instance closed. A take that finds the slot free still proceeds. A result store still waits and lands, as before.
- **Named limits:**
  - **A cycle made only of result stores is not ended**: each store waiter would hold a slot inside a hook while waiting for another's. That needs two instances each resolving the other's subtask inside its own `task.return`, which no fixture constructs.
  - **A result store's waiter carries no engine signal**, so a teardown of its own core instance does not end it; it ends when its holder does.
  - **A let-go outside `HTTPServer.Close`** (a response body recorded after the let-go, `setBody`) passes no deadline. It runs on ServeHTTP's own path, not Close's.
  - **A let-go dropped at the deadline wakes nobody.** Where the guest holding the slot is the task the let-go was for, which is the case ADR 0137's postscript names, the instance's Close ends that guest. Where another task holds it, the task the let-go was for stays parked until its park's bound, as after any delivery Close's stop drops (ADR 0137's posture for a task parked on something no host work will deliver).
  - `producerStop` keeps its name, because renaming it touches a file a parallel slice owns. Its comment now says it is the instance's stop for every wait for the slot that would run guest code.

## The build, as measured

On darwin/arm64, go1.26.11, in #1398's worktree.

- **The first build dropped a result store at the stop (Option 1), and the build corrected it to Option 2.** With the store dropped, the control of `TestACancelRequestedWhileSuspendedIsDeliveredAtTheLoop` hung past Close in 1 of 4 runs alone, and in the package's run. Its call was parked in `awaitEvent` on a subtask whose resolution, the callee's NoResolution at its exit, had been dropped. Main passed it 40 of 40. The goroutine dump showed the callee's driver gone and the caller's park left with nothing to wake it. With the store waiting, a 20-run loop of the control passed 60 of 60.
- **`TestCloseEndsACrossInstanceRunSlotDeadlock`** (`runslotclose_test.go`): the cycle, driven through the test seam `answerInsideHandBack` on task-return-early's cancel-poll row, was reached in 20 of 20 runs (19 of 20 in one gate-on pass) in both gate states, and Close ended it within `closeEndsBound`, with the call returned and no engine goroutine left. Plain and under `-race`.
- **`TestATeardownOfTheCallersCoreInstanceEndsItsWaitForTheCalleesSlot`**: closing only the parent's core instance fell quiet, 20 of 20, both gate states.
- **`TestCloseIsBoundedByItsDeadlineWhileAGuestHoldsTheSlot`**: Close returned false at its deadline with the slot still held by the test, the let-go's store dropped and nothing pending.
- **`TestAReturnInsideTheCancelHandBackIsResolvedAtTheExit` under its old neuter**, through the seam: hang in 20 of 20 runs per gate state, each ended by the read's Close, where the binary used to time out.
- **What the neuters showed** (each run and reverted; the full list is at the sites as `NEUTER, run:` comments):
  - **The stop is what Close's end rests on.** Without the stop and the engine arms in `takeRunSlot`, the first run of each gate state reached the cycle and Close did not return within `closeEndsBound`, both waiters parked. Without only the stop's arms, the same.
  - **The engine's arm is the teardown witness's whole.** With `invokeSync` passing no engine, the parent's core instance's Close returned its bound's error in both gate states, its one caller counted executing with the sync lower in flight.
  - **The deadline is the let-go's whole.** With `HTTPServer.Close` passing nil for it, Close was still at its hang guard, its goroutine in `takeRunSlot` under `httpReqBody.closeUntil`.
- **The first build's take did not keep the free-slot rule, and `make ci` found it.** One select held the slot's send beside the three ends, and Go picks among ready cases at random, so with the stop closed a free slot could be refused. `TestCloseReachesItsBoundWithANamedOutcome`'s uncooperative guest then ended at its callback re-entry as *"Close ended this entry while it waited for the run slot"*, where `interp.ErrClosed` or `interp.ErrTerminated` reclaim it. `takeRunSlot` now tries the slot alone first. `TestAFreeRunSlotIsTakenAfterTheStop` asks the rule directly: 300 of 300 takes hold the slot with the stop and the engine closed, and a take of a held slot ends. Under a neuter that drops the first try, it refuses 209 of 300, and the reclamation test fails 120 of 200; with the fix, that test passes 200 of 200 plain and 50 of 50 under `-race`. `fenceWriterEnter` already checks admission before each wait, so it needed nothing.
- **The witness's first draft read a stale cycle.** It counted every goroutine, and under the neuter the gate-off subtest's cycle, left parked by its failure, was read as the gate-on runs': that subtest closed instances that had not reached the cycle, and passed 20 of 20 under the neuter. The place is now read only over goroutines made after each run began.

## Rollback

Pass nil for the stop and the engine in `acquireRunFor`: every wait returns to main's, deadlock included, and the entries' and `invokeSync`'s new false branches are then reached only as before. The let-go's deadline stands on its own; passing nil for it in `HTTPServer.Close` restores main's let-go.
