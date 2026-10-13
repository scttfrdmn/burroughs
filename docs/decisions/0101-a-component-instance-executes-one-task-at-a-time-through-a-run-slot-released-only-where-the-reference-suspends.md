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
    `block` ends the chain. **Since fixed:** see *Amendment: #977* below. A fixture built the sequence,
    wasmtime returned 8, and Burroughs hung until a created Thread's wait released the slot.
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

## Amendment, 2026-10-12: the run slot covers result stores, and it is held by a call chain

Chair's ruling, in session, 2026-10-12, on the classification of main's Windows race reports: *"Take the
run slot at the result store … Do not defer the store to the waiter."* Two defects are fixed under it,
and both are graves.

### #983: the run slot covers result stores as well as execution

**The gap.** #976's witness checked only that two tasks never **execute** at once. A subtask's result
store is guest-memory work done by a goroutine that is not executing any task, and nothing made it take
the slot.
- The store path is `asyncLowerFunc`'s `onResolve` → `canon.StoreVia` → `Caller.Write`.
- The production caller is `crossComponentResolve`, on a nested child's goroutine.
- It wrote into the caller instance's memory while another task executed there. Main's Windows race job
  reported it on `9f11079`, and it was rebuilt heap-backed on darwin.

**The rule.** `onResolve` takes the caller instance's run slot, as a fresh chain, before it writes. It
writes at the store, not deferred to the waiter, for the chair's two reasons:
- lowering a result may call the caller's `realloc`, which is guest code and must run under the slot in
  every case;
- deferring would move the write later than the reference's resolution point, a timing change with nothing
  to measure it against.

**The one exception: the slot's holder is in host code.** That means inside an async lower's impl, or a
subtask's `onCancel`, on its own goroutine with the slot held. Waiting for the slot there would deadlock,
because the holder may be the very goroutine resolving. The exception is per **instance**
(`asyncHandles.inHost`), not per subtask, and a measurement put it there. The first draft flagged only the
subtask whose own impl was running, and three liftpark tests deadlocked. In one of them, task B's impl
resolves task A's subtask from inside B's own impl call.

**And the store does not write at once there: it is queued, and the holder applies it as it leaves host
code.** #985 wrote at once, on the argument that no guest code can run while the holder is in host code.
That argument was about *guest* code. The chair asked whether the window was safe against the holder's
*host* code, and it was not:
- the holder's impl runs on its goroutine and reaches guest memory through `Caller`, ADR 0064's
  deliberately plain region;
- a resolver on **another** goroutine wrote a result into that same memory meanwhile;
- nothing ordered the two.

`TestAResultStoreDoesNotLandWhileTheHolderIsInHostCode` shows it deterministically. The holder's probe impl
writes a sentinel to a retptr through `c.Write`, resolves another caller's subtask on another goroutine,
and reads the word back. Under #985 it read the other caller's result.

**The ordering now** is `asyncHandles.hostStores`: a store arriving while `inHost` is raised is queued,
and `asyncHandles.leaveHost` applies the queue on the holder's own goroutine, before its guest resumes.
That is the reference's order. The resolver's work is another thread's execution, which cannot run during
this thread's host call, but does run before this thread executes guest code again.
- **It keeps #983's deadlock fix**, since nothing waits.
- **It is not the deferral to the waiter the chair ruled out**, since the store lands before any guest code
  of the instance runs.
- **Inline resolutions are queued too**, and applied as the impl returns, before the lower reads the
  subtask's status. So the sync-resolving arm still sees RETURNED.

Neutered by applying at once instead of queueing, the witness reads back `0x2`, the other caller's result,
in place of its sentinel, on 3 of 3 runs.

**The store's guest code runs with no engine lock held.** The chair asked whether the store ever runs
guest code while holding `h.mu`. It did. A string or list result is lowered by `canon.StoreString` /
`canon.StoreList`, which call `Heap.Realloc`, and `guestHeap.Realloc` routes that to
`CanonCaller.Realloc`, the caller's `cabi_realloc`. The store made that call inside one `h.mu` hold.
`TestAResultStoresReallocRunsWithNoEngineLockHeld` measured it:
- **the setup:** `result-store-realloc.wasm`'s import returns a string, and its impl resolves inline, so the
  store takes the queued path. Its `realloc` is a pure bump allocator, legal under the reference's
  `may_leave = False`, with a long spin. A poller recorded the longest unbroken stretch `h.mu` was held;
- **before:** about **120 ms**, which is the whole realloc;
- **after:** under **1 µs**;
- **the neuter**, holding `h.mu` across the guest-memory step again, brings back 121 ms, 3 of 3.

The store is now three steps: (1) under `h.mu`, the cancellation check and minting any future or stream
handle; (2) with no lock, the retptr write or the codec's store, `realloc` included; (3) under `h.mu`,
recording an error or resolving. `leaveHost` releases `h.mu` around each queued store. The §4 B-MM-1
edge is unchanged: the write in (2) precedes (3)'s acquisition, inside which `resolved` is published.

**The queue cannot strand a store for a valid guest, and the reason is a symbol search.** The chair's case:
the holder's impl gives up the run slot while `inHost` is raised, another task runs, a store for it is
queued, and that task waits for a store that drains only when the holder's host call returns.
- **The slot is given up in exactly two places:** `asyncHandles.releaseRun`, at an entry's end, and
  `asyncHandles.suspending`. `suspending` is called only by `waitableSetWait` and `subtaskCancel`, which
  are canon built-ins that guest code calls.
- **`CanonCaller.Blocking` does not touch the slot.** It marks the agent blocked and nothing else, which is
  `TestASiblingWaitsForABlockingImportAndDoesNotTrap`'s guarantee that no other task runs while a host
  import blocks.
- **So the holder can give up the slot while in host code only by running guest code that suspends.** The
  only guest code `CanonCaller` lets host code run is `Realloc` (ADR 0064's H-2 carve-out).
- **And the reference forbids a `realloc` from calling any built-in.** `LiftLowerContext.reallocate` sets
  `may_leave = False`, and every built-in and `canon_lower` traps on it.

So for a valid guest the queued store's owner never runs, and so never waits, while the store is queued.
**Burroughs does not enforce `may_leave`**, so an invalid `realloc` that calls `waitable-set.wait` would
release the slot here where the reference traps. That is filed as #990 rather than closed here.

**Every path that resolves a subtask, and what the slot's holder does while waiting:**

| resolver | where `onResolve` runs | the waiting holder | deadlock? |
|---|---|---|---|
| an impl resolving inline | the lowering agent's goroutine, inside the impl | none: it *is* the holder, in host code | no (`inHost`: queued, applied as the impl returns) |
| `onCancel` resolving inline | the cancelling agent's goroutine | none, likewise | no (`inHost`: queued, applied as `onCancel` returns) |
| a host impl resolving later | the host's goroutine | waits through `waitable-set.wait` or a sync `subtask.cancel`, both via `suspending`, or between callback entries (`awaitEvent`), where no slot is held | no |
| `crossComponentResolve` | the child's goroutine | likewise | no |
| a sync lower of an async-lifted child | — | refused by name in `bindCrossComponent`: the lower must match its import's asyncness | unreachable |

The chair's case to check by symbol was a parent that blocks until a child returns without suspending.
That case does not exist: the sync arm refuses an async callee, and a sync callee runs inline on the
parent's goroutine with no resolver at all. **But checking it found #984.**

### #984: the slot is held by a call chain, not an agent

#976 recorded the holder by component-model Thread index. That self-deadlocked a valid chain, parent →
child → parent, all synchronous, through a lift the parent passes its child (`walker.compArgRef`).
- In `reenter.wasm`, the parent's two core instances share its slot, and the inner entry waited for a slot
  its own call chain held.
- **wasmtime 49.0.2 returns 8.** The reference has no reentrance rule at this pin (`canon_lower` checks
  only `may_leave`), and the chain is one thread there.
- **Burroughs returned 8 at `d2eccdf`, the commit before #976, and hung on `7dddfea`.**

**The repair.**
- **The slot belongs to a `runChain`.** Each record of a component-model Thread carries the chain it
  executes under.
- **The chain travels with the call.** The synchronous cross-component arm passes it to its callee through
  the context, and the callee's entry re-enters a slot its chain holds instead of waiting (`runDepth`).
- **A suspension releases the whole slot**, whatever the depth, and restores the depth on the re-take.
- **The scheduler stamps the holding chain on each created Thread it resumes**, so that Thread's
  synchronous calls re-enter too. A created Thread's `waitable-set.wait` released nothing at the time,
  which was #977's edge, left for #977. It now releases the slot (*Amendment: #977* below).

### Witnesses, each neutered and run

| Witness | Neuter | Result |
|---|---|---|
| `TestAResultStoreWaitsForTheExecutingTask` (`result-store-heap.wasm`) | skip the acquisition in `onResolve` | X sees the store land mid-loop and returns `0x10002`, 3 of 3; under `-race` it also reports the pair. **Its first assertion was a clock comparison and was wrong**: X releases the slot inside `invokeWith`, before `CallValues` returns, so Y's store may legitimately complete in that gap, and CI's ubuntu amd64 runner caught it at 2 ms on #986. It now asserts the guest's own observation. |
| `TestAChildsResultStoreWaitsForTheParentsExecutingTask` (`xc-gated.wasm`) | the same | fails on the value (`0x10065`), 3 of 3 |
| `TestAChainReentersAnInstanceItAlreadyHolds` (`reenter.wasm`) | drop the chain from the synchronous arm | hangs, and fails on its bound |

- **`TestAResultStoreWaitsForTheExecutingTask`:** caller Y's subtask is resolved from outside while caller X
  executes. Y's resolve must not return before X does. The fixture is heap-backed, so `-race` can see the
  pair.
- **`TestAChildsResultStoreWaitsForTheParentsExecutingTask`:** the production path. A nested child resolves
  the parent's subtask, gated on a test host import, while another parent task spins over that result
  word.
- **`TestAChainReentersAnInstanceItAlreadyHolds`:** returns wasmtime's 8, bounded.
- **The public door:** `TestConcurrentCallsIntoANestedComponentDoNotInterleaveAResultStore` drives
  `xc.wasm`, for which wasmtime returns 101 and 102, through two concurrent `Component.Call`s. Its passing
  direction is deterministic. Its failing direction depends on timing, because the public API has no hook
  to gate the child (ADR 0096).

### A divergence the fixtures found, recorded

**wasmtime 49.0.2 traps a SYNC task that calls `waitable-set.wait`** (*"cannot block a synchronous task
before returning"*). The pin's reference has no such rule: there is no `may_block` in `definitions.py`, and
`canon_waitable_set_wait` checks only `may_leave`. Burroughs follows the pin, so a sync-lifted callee may
wait, as `async-waitset-synth.wasm` and `run-slot.wasm` do. The cross-component fixtures use an async
parent, so wasmtime can run them. The divergence is wasmtime's lead on the pin, and is recorded rather
than adopted.

## Amendment, 2026-10-12: #977 — a created Thread's wait releases its chain's slot

The chair's order: build #977's fixture first, then fix it so the slot is released where the reference
suspends.

**The fixture** is `cmthreads/thread-wait-callback.wasm`:
- the parent's async `run` spawns Thread T and YIELDs until T is done;
- T async-lowers a nested child's `g` and parks in `waitable-set.wait`;
- the child's `g` calls back into a lift of the parent's own `f` (`walker.compArgRef`) and returns
  `f() + 1`.

**wasmtime 49.0.2 returns 8, on 10 of 10 runs. Burroughs hung**, exactly as #977 derived. T's wait
released nothing, so the child's call into the parent waited for the slot T's chain held, and T waited for
the child.

**The repair is this record's own rule, applied.** The slot is released only where the reference
suspends, and a created Thread's `wait_until` is a `block()`: a suspension. So `asyncHandles.suspending`
now releases the chain's slot for a created Thread as well as an implicit one, finding the chain the
scheduler stamped on it. T then holds no slot while it waits. The child's call runs, the child's result
store resolves T's subtask, and T takes the slot back with its chain and depth restored.

**Witness:** `TestACreatedThreadsWaitReleasesTheRunSlot` returns wasmtime's 8, bounded. Neutered by
restricting `suspending` to implicit Threads again, it hangs.

**What is left, recorded as a divergence rather than a deadlock.** In the reference, T's `block()` ends
the *chain*, so the store may run other ready Threads while T waits. Here the slot is released, but the
scheduler still waits on T's handoff, so another ready Thread of the instance does not run until T comes
back. A task outside the chain can now enter, which is what #977's sequence needed. A sequence whose
progress needs another *Thread of the same chain* to run while T waits would still stall here, where the
reference progresses. No fixture constructs one. Closing it means making a wait a handoff, a scheduler
change for ADR 0100.
