# 0139 — A wait for the run slot ends at the instance's stop and the engine's, so Close ends a cross-instance deadlock, and Close's let-go waits only until its deadline

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

1. **The instance's stop, inside `acquireRun` itself**, for every caller. The stop is `asyncHandles.producerStop`: closed by `stopProducers`, which `walker.close` runs across the tree of nested instances (`stopAllProducers`) before any core instance closes. *For:* it is already Close's signal for this exact wait, passed by five deliveries; selecting on it inside the take means no caller can omit it. *Costs:* its name says producers, and every caller now gives up at Close, which needs each caller's unwind argued (below).
2. **A new channel closed at the start of `walker.close`.** *Costs:* a second channel with the first one's meaning, the two-places-know-one-fact shape; nothing measured needs the earlier instant, since `killAllParked` and `stopAllHostWaits`, which run before `stopAllProducers`, take no slot.
3. **The store's lock as well**, as `waitableSetWait` selects on it. *Costs:* it changes what lands after a trap — a store or an entry waiting behind the trapping holder would be dropped rather than run once the holder unwinds and frees the slot — and the cycle this record is about has no trap in it. The lock is a trap's signal and the holder's unwind already answers it; a cycle needs teardown.

**The engine's signal:** the waiting agent's thread context, where the waiter is an agent of another instance and its CanonCaller is in reach. *For:* that is what `waitableSetWait`'s `c.Context().Done()` arm is, and ADR 0132's teardown of the waiter's core instance (an agent's exit or trap) then ends the wait without waiting for the component's Close. *Costs:* `onResolve` has no CanonCaller for the agent it runs on, since its type is the contract every host impl calls (`func(canon.Value)`); threading one through would change every impl to serve one waiter that the instance's stop already reaches.

**For the let-go:** (a) pass Close's deadline to the let-go as its abort; (b) make the let-go asynchronous, which is a goroutine site needing a principal's ruling (`TestNoEngineGoroutineLandsWithoutAPrincipalsRuling`); (c) close the instance's stop before the let-go, which would end every task's own re-entries before they could exit within the deadline.

## Choice

**Option 1, with the engine's signal where the waiter is an agent and its context is in reach, and (a) for the let-go.**

### The wait

`acquireRun(ch, rec, abort)` keeps its signature and becomes `acquireRunFor(ch, rec, abort, nil)`. `acquireRunFor`'s fence wait (`fenceWriterEnter`) and its slot send each select on four things: the fence's wake or the slot, the caller's `abort`, **the instance's stop** (`producerStop`), and **the engine's** (`engine`, nil where there is none). A nil channel never fires, so a caller passing nothing still ends at Close. The suspension point's re-take inside `suspending` passes none of them, as before (below).

**What an ended wait returns:** `false`, holding nothing — no fence writer counted, no slot, and `asyncHandles.runChain` untouched — which was already the contract of `acquireRun`'s false. **A wait that finds the slot free takes it**, as before: the stop ends a wait, it does not refuse a take. A take that wins the select after the stop holds the slot, so its guest code runs under it, and the core instance's Close ends that code at a safepoint as it ends any.

**Who passes the engine's signal:** `invokeSync` passes its sync cross-component caller's `caller.Context().Done()` (nil for an embedder's call). `spawnAgent`'s `Enter` hook already passes the agent's own context as `abort`. No other waiter is an agent with a CanonCaller in reach.

### How each caller unwinds

- **A result store (`onResolve`):** the store is **discarded**: nothing is written, the caller's `realloc` is not called, and the subtask stays unresolved. The instance is being closed, and nothing will read it; a store into an instance Close has torn down is ADR 0071 fact 4's hazard, which the deliveries already refuse. Nothing is released, since nothing was taken.
- **ADR 0136's on-return hook** runs that store inside the callee's `task.return`. The resolution is recorded on the callee's task under `h.mu` before the hook runs, so a second `task.return` still traps, and the hook has already set `answered`, so the callee's exit adds nothing. When the store is discarded, the hook returns, and `task.return` returns as after any answered return. The callee runs on under its **own** slot, which it holds, until its core instance's Close ends it at a safepoint.
- **A callback task's entry** (`enterAndInvoke`, `enterInvokeLowering`): it returns an error wrapping `errThreadKilled` before the core func runs, after releasing the entry slot (`leaveTask`) and the fence reader the entry took (`fenceEntryEnd`). The task ends with that error. **`enterTask`'s rule is not reversed.** It says an admitted entry must proceed because the call's *cancellation* is delivered through an entry, and abandoning one delivers it to nobody. Close's stop is not a cancellation to deliver; it ends the task, as Close ends a parked Thread (`killParked`) without delivering anything.
- **A sync call** (`invokeSync`): it returns before the callee runs, as its context arm already did. The error names which signal ended it: the call's context is `ErrCancelled`, Close or the engine is `errThreadKilled`. For a cross-component caller the lower's error unwinds the caller's frames as any failing canon call does (ADR 0101's point 7, step 3).
- **A spawned agent's entry** and **the host's deliveries** (producers, sockets, files, `wasi:http`, the server's `underRunSlot`): unchanged. They already handled false, and the channel they passed is now also selected inside.
- **E2-in-b's let-go**, by choice (a): `HTTPServer.Close` derives a channel closed at its deadline before it lets the bodies go, and passes it to each let-go's delivery (`abortBodies` → `closeServedBody` → `httpReqBody.closeUntil` or `deliverHTTPUntil`, and `httpIncoming.abort` → `endIncoming`). A let-go held by a computing guest gives up at the deadline, discarding its store as a stopped delivery does. Close's bounded wait then finds the deadline passed and reports false. The instance's Close follows: its stop ends every other wait, and its core instances end the computing guest. Every other let-go, and every delivery outside Close, passes nil and is unchanged.

### Why no caller then runs guest code without the slot

`acquireRunFor` returns false only from its two selects, before the slot send has succeeded, and its false path sets no holder. Every caller's false branch returns before its guest work: `onResolve` before `apply` (the realloc and the memory write), an entry before `lowerPending` and `InvokeCMThread`, `invokeSync` before the same, an agent before its first instruction, a delivery before its store. No caller defers a release before its check, and `releaseRunFor` releases only a slot its own chain holds, so an ended wait cannot drain another holder's token either.

### What is not changed, and why

- **The suspension point's re-take** (`suspending`, after its wait returns) still waits on the fence and the slot with no signal. It waits for its own instance's slot, and its holder either executes guest code, which the core instance's Close ends at a safepoint, freeing the slot as its entry unwinds, or waits in `acquireRunFor`, which the stop now ends. A cycle needs a wait for another instance's slot, and every such wait is `acquireRunFor`'s.
- **A callee segment's fence reader** (`fenceReaderEnter`) and **`enterTask`'s wait for a re-entry** are not given the stop. A reader is taken before the slot (F1), so it holds no slot a writer needs, and an entry holds `entrySem` only around its own run-slot take, which now ends.
- **The store's lock** (Option 3).

## Consequences

- **`Close` ends a cross-instance run-slot deadlock.** Both waits of E2-in-a's cycle end at the instance's stop, and the parent's also at its engine's.
- **A teardown of an agent's core instance ends that agent's wait for another instance's slot**, through the engine's signal, without waiting for the component's Close.
- **`HTTPServer.Close` returns at its deadline** when a guest that answered computes without yielding, and reports false, which `Component.Close` reports as `ErrCloseIncomplete`.
- **After `stopProducers`, every result store and callback entry still waiting gives up**, where it used to wait for the slot and then run. A take that finds the slot free still proceeds.
- **Named limits:**
  - **A wait for the slot inside a teardown of the waiter's own core instance, with no component Close, ends only for an agent waiter.** A result store's waiter carries no engine signal, so a cycle of two stores, if one became constructible, would end only at the component's Close.
  - **A let-go outside `HTTPServer.Close`** (a response body recorded after the let-go, `setBody`) passes no deadline. It runs on the task's own path, not Close's.
  - `producerStop` keeps its name, because renaming it touches a file a parallel slice owns. Its comment now says it is the instance's stop for every wait for the slot.

## Rollback

Pass nil for the stop in `acquireRunFor`'s two selects, and nil for the engine in `invokeSync`: every wait returns to main's, deadlock included. The entries' and `onResolve`'s false branches are then unreachable and harmless. The let-go's deadline stands on its own; passing nil for it restores main's let-go.
