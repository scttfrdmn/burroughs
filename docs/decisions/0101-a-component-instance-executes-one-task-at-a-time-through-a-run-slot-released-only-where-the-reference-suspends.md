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

## Amendment, 2026-10-13: Option A — the run slot is held by the executing Thread (#978)

Status: **accepted** — the chair's second review, in session, 2026-10-13, on the PR that carries this
amendment. That review accepted owner routing and killable waiters, accepted items 2 and 3 as written, and
required three text additions before merge, which are made in point 7. The chair ruled Option A on #978
and ordered this design first, as its own commit. It amends ADR 0100's handoff design as well as this
record, and ADR 0100 carries a pointer here rather than a second copy. `Ratio-Class: carried`: both reviews
are in session and have no approval artifact to cite.

**The chair's first review, in session, 2026-10-13, on #997.** It accepted the invariant and transitions
1–6, and required three amendments before merge, on points 7, 3 and 5. It also gave a merge rule: if the
amended text does not change the design, merge it as accepted with no second review.

**Item 1 does change it.** Point 7 now routes a trap to its Thread's owner rather than to the chain's
origin, and makes a waiting Thread reachable by the trap's wake. That is new mechanism, so the merge rule
sends the record back for review rather than letting it merge. Items 2 and 3 do not change the design:
- item 2 states a wait the design already had, and adds the context to it;
- item 3 turns an argument into an assertion.

Item 1 also turned up a defect in today's behaviour, filed as #998 with its oracle taken first.

### The invariant

**The run slot is held exactly by the Thread executing guest code, re-entrantly for that Thread's own
synchronous nesting.**

Everything below follows from that sentence and from one consequence of it. A Thread that stops executing
guest code gives the slot to whoever runs next, and nothing else may take it in between. **Every handoff to
the scheduler therefore carries the slot, like a baton.** Three conditions keep that true:

- whoever sends on `cmScheduler.back` holds the slot at the moment of sending;
- the scheduler holds it from the receive until it resumes a Thread, which then holds it;
- when nothing is runnable, the scheduler frees it.

What this replaces is the chain model of ADR 0100's handoff amendment and of this record's #984
amendment:
- the hold belonged to whoever *started* a chain;
- a created Thread ran under that hold;
- a parked implicit Thread kept it.

#978's sequence is the one that model cannot express. An async task's implicit Thread suspends mid-callee
with nothing runnable, so under the old model it keeps the slot, and the sync-typed call that would resume
it can never enter.

### What the reference does, by symbol

There are two rules, and they stay two locks.

**`exclusive_thread` is backpressure, and Burroughs's `entrySem` is it.**
- **Where it is consulted:** `Task.needs_exclusive` asserts an async-typed function. `Task.enter_implicit_thread`
  consults `exclusive_thread` only under `if self.ft.async_:`.
- **Where it is set:** `enter_implicit_thread` sets it, and `Task.exit_implicit_thread` clears it.
- **In the callback loop:** `canon_lift` clears it on every callback return, before the `match code:`
  (`assert(inst.exclusive_thread is task.implicit_thread)` then `inst.exclusive_thread = None`). It takes
  it back after the arm (`inst.exclusive_thread = task.implicit_thread`).
- **While suspended mid-callee:** nothing clears it. `Thread.suspend` → `block()` touches no instance
  field.

**"One thread runs" is execution, and the run slot is it.** In the reference it is not a field at all.
- The Python threads are serialised by `Continuation.lock` and `Handler.lock`, so exactly one runs.
- Control passes only through `suspend(switch_to)`, under `block`, `switch_to`, or a returning
  `cont_func`.
- `Thread.resume` loops `while thread is not None`, and `Store.tick` resumes one ready Thread when nothing
  runs.

So the slot's owner at every instant is "the Thread whose continuation holds the lock", which is the
invariant above.

### Each transition, derived

**1. Entry.** Unchanged in mechanism. The sites are:
- `compFunc.invokeWith` (sync lift);
- `compFunc.enterAndInvoke` and `compFunc.enterInvokeLowering` (async entries);
- `asyncLowerFunc`'s `onResolve` (a result store).

Each takes the slot through `acquireRun`, after `entrySem` where both are taken. A sync-typed entry takes
no `entrySem`, so it enters past a suspended async task's `exclusive_thread`, as
`enter_implicit_thread`'s `if self.ft.async_:` admits. #978's fixture needs exactly this, and it was
already true. What blocked the fixture was the slot, not `entrySem`.

**2. `giveBack` and each switching form.** The reference sources, by form:

| form | reference | what happens to the giver |
|---|---|---|
| `thread.suspend` | `Thread.suspend` → `block()` | suspended |
| `suspend-then-resume` | `Thread.suspend_then_resume` → `switch_to(other)` | suspended |
| `yield-then-resume` | `Thread.yield_then_resume` → `start_waiting`, then `switch_to(other)` | waiting |
| `suspend-then-promote` / `yield-then-promote` | `if other.ready(): other.stop_waiting()` → the matching form above, else `suspend()` / `yield_()` | suspended / waiting |

All five end in `suspend(switch_to)`: the giver's continuation stops holding the lock, and `switch_to`'s
runs, or the resumer's does. **So `giveBack` hands the slot over in the same step as the handoff:**
1. Under `h.mu`, save `(h.runChain, h.runDepth)` on the giver's record, and set `h.runChain` to nil. The
   channel stays full, because the slot is in transit, not free.
2. Send the handoff.
3. The scheduler restores the next Thread's saved `(chain, depth)` (depth 1 for a first resume) before
   waking it.

The giver's resume is the same restore, done by the scheduler. It is never a contended `acquireRun`,
because in the reference nothing runs between `switch_to(other)` and `other` running.

- **A created Thread's entry returning** (`cont_func` returning `None`) is the same handoff, with nothing
  saved.
- **A created Thread's first resume** no longer inherits `h.runChain`. It gets its own chain at
  `thread.new-indirect`, because the hold is now the Thread's.

**3. The YIELD arm.** The cited lines settle it. The reference has already cleared `exclusive_thread` when
the YIELD arm runs (`inst.exclusive_thread = None` above, on every callback return). Its
`thread.wait_until(lambda: inst.exclusive_thread is None)` then gives `Store.tick` the chance to run other
ready Threads **with `exclusive_thread` clear**, and takes it back only after the arm.

- **Burroughs already matches on the callback's return.** `leaveTask` releases `entrySem` at the end of
  every entry.
- **But `threadTable.yieldPoint` re-took `entrySem` before running the ready queue.** That is a
  divergence. While the yielding task's created Threads ran, another async task could not enter, where
  the reference admits it.
- **Under A, `yieldPoint` takes neither lock.** It sends its request, and the scheduler acquires the slot
  on the queue's behalf (point 5). `entrySem` stays free for the whole arm, and the callback re-entry
  takes both back through `enterAndInvoke`, which is the reference's `inst.exclusive_thread =
  task.implicit_thread`.
- The old note "the scheduler never takes the slot" was right under the chain model, where a parked
  implicit Thread held it. Under A no parked Thread holds it, so the scheduler waiting for the slot waits
  only on a Thread that is executing.

**The order at the arm (the chair's item 2): `yieldPoint` blocks until the scheduler reports, and the
re-entry comes after.** Today `yieldPoint` sends `reply` on `cmScheduler.yield` and then receives on
`reply`. Under A it keeps that shape and waits on a single `select`. Its arms, and when each fires:

- **`reply`:** the scheduler sends on it exactly once per request, when the chain it ran for the request
  has ended. That is after it has popped the ready queue empty and freed the slot, or after a created
  Thread trapped (the trap is the reply's value).
- **`cmScheduler.stop`:** `Close`. This arm is new, because today's receive on `reply` relies on the
  scheduler always replying. Under A the scheduler may be in its acquire `select` when `stop` closes, and
  it returns from there, so the reply is then sent as `errThreadKilled` before the goroutine exits. The
  `stop` arm makes the wait not depend on that.
- **the task's context `Done()`:** today's abandonable wait was `enterTask`'s, before the request. Under A
  there is no `entrySem` wait here, so the context moves to this wait. An abandoned wait leaves `reply`
  unread, which is safe because `reply` has capacity 1 and the scheduler never blocks on it. The
  cancellation is then delivered by the callback loop's re-entry, which contends for `entrySem` and the
  slot in the ordinary way. A trap that chain produces is not lost with the reply: point 7 routes it to
  the owner's fault, which the re-entry checks.

So **the re-entry cannot win a race against the queue**: it is not attempted until the reply. Within the
scheduler's chain, the queue is run FIFO and to empty before the reply. That is the order today's
`yieldPoint` gives, and the order the committed readings (`spawn.reading`, `suspend-partway`) were taken
under.

Those fixtures also re-YIELD until their body has run, so each is order-independent in value. The
pinning is of the number of YIELDs, which is invisible to the reading. The build runs
`TestASpawnedThreadRunsAtTheYieldAndHoldsItsOwnIndex` and
`TestAThreadThatSuspendsPartwayLetsTheYieldingCallbackContinue` 50 times each.

**4. `waitable-set.wait` and a sync `subtask.cancel`.** Both are `wait_until` in the reference:
- `WaitableSet.wait` → `current_thread().wait_until(self.has_pending_event)`;
- `canon_subtask_cancel`'s `if not async_: thread.wait_until(subtask.resolved)`.

`wait_until` is `start_waiting` then `block()`, so the chain ends there, and `Store.tick` picks another
ready Thread. **So `asyncHandles.suspending` becomes a handoff too**, not a bare release:

- **Where the instance has a scheduler** (the ready queue can be non-empty only there), the waiter saves
  its `(chain, depth)` and sends a `detach` handoff: *"I have stopped executing and am not parked in
  `giveBack`."* The scheduler takes the slot with it and continues with the ready queue, which is the
  `Store.tick` half. When nothing is runnable, it frees the slot.
- **Where there is no scheduler**, the release is a plain free, as it is today.
- **The waiter takes the slot back by contention** (`acquireRun` with its saved chain and depth) after
  its event, inside the same `c.Blocking`. That is the reference's waiting Thread becoming `ready()` and
  being picked by a later `tick`, an order the reference leaves to `random.choice`.

**This answers #977's residual.** Another ready Thread runs while T waits, because T's wait is now the end
of the scheduler's chain rather than a release the scheduler cannot see. The async form of `subtask.cancel`
is `thread.yield_()` in the reference. Burroughs returns BLOCKED without yielding, and that is not changed
here. It belongs to the cancellation slice the chair has ordered after A.

**5. What triggers the scheduler when the slot is freed with ready Threads queued: the `Store.tick`
equivalent.** Under A, a Thread made ready by a task that then *returns*, rather than suspending, has
nobody to run it. #978 is exactly that: `f` calls `resume-later(run)` and returns. So:

- **`releaseRun`, when it frees the slot at depth 0, kicks the scheduler if the ready queue is non-empty**:
  a non-blocking send on a capacity-1 `tick` channel, coalescing. So does `suspending`'s plain-free path,
  for completeness.
- **The scheduler answers a kick, or a YIELD request, by acquiring the slot** in a `select` that also
  receives `back`. Either the slot comes free, or an executing Thread hands it over, and either way the
  scheduler then holds it. It also receives `stop`. Without the `back` arm, a Thread whose `giveBack`
  sends to a scheduler blocked waiting for the slot that Thread holds would deadlock.
- **No wakeup is lost** (the chair's item 3). Every write to `threadTable.ready` was found by searching the
  package for the field:

  | site, by symbol | appends? | under what hold |
  |---|---|---|
  | `threadTable.park`, `state == threadReady` (from `giveBack`, for the `yield-*` forms) | **yes** | the giver, which is executing and so holds the slot. `park` runs before the handoff is sent. |
  | `threadTable.markReady` (from `threadResumeLater`, `thread.resume-later`) | **yes** | the calling guest, executing, so it holds the slot |
  | `threadTable.popReady` (from `cmScheduler.drive`) | removes | the scheduler, holding the slot |
  | `threadTable.claim` → `unreadyLocked` (from `drive`) | removes | the scheduler, holding the slot |
  | `threadTable.takeReady` → `unreadyLocked` (promote's `stop_waiting`, from `threadSwitch`) | removes | the calling guest, holding the slot |
  | `threadTable.killParked`'s filter of the queue | removes | the scheduler in `drive`'s trap path, or `walker.close`, which holds nothing and needs nothing at teardown |

  Sites that do **not** touch the queue:
  - the sync branch in `cmScheduler.loop`, which resumes the origin with the trap;
  - `drive`'s trap path, apart from `killParked`'s removal above;
  - `threadNewIndirect`'s `threadTable.add`, which registers a *suspended* Thread;
  - every resolver, meaning `asyncLowerFunc`'s `apply` and `onResolve`, `crossComponentResolve`, and the
    `asyncHandles.leaveHost` drain. These resolve subtasks and waitables, never Threads. A waiter becomes
    runnable by its own contended re-take (point 4), not through the queue.

  **So only two sites append, and both hold the slot.** A removal cannot lose a wakeup. The scheduler pops
  until the queue is empty while it holds the slot, and nobody can append while it holds it, so a non-empty
  queue at any free is seen by that free.

  **An instrument, not prose: the build adds a test-build assertion at both append sites.** In a test
  binary (`testing.Testing()`), each append first checks under `asyncHandles.mu` that the slot is held,
  meaning `h.runChain` is non-nil or the scheduler has it (a new `h.schedHolds`, set from its receive to
  its handoff). A violation panics, naming the site. The neuter is to call `markReady` from a goroutine
  holding nothing, and the assertion then fires.
- **A stale `tick`.** A kick sent while the scheduler was already driving is consumed after it has drained
  and freed the slot. The scheduler then acquires, `popReady` returns nil, and it frees the slot with a
  bare `<-h.runSlot`, never through `releaseRun`. **The scheduler's own free never kicks**, so a stale tick
  costs one acquire and cannot re-arm itself.
- **What the scheduler's acquire waits on.** `runSlot`, `back` and `stop`. **No context, and no
  `entrySem`**: the scheduler never takes `entrySem` under A, and the YIELD arm no longer does either. It
  must also receive `yield`, or a YIELD request sent while it serves a tick would wait for the tick's
  chain to end. That wait is harmless, because the requester holds nothing, but it costs a chain's latency
  for no reason.

**6. #984's re-entry survives**, because what it keys on was never "who started the chain". It keys on the
identity a *synchronous call* carries.

- **Each Thread has its own `runChain`:**
  - an implicit Thread's is its entry's, from `runChainFrom(ctx)` for a sync lift and fresh for an async
    entry;
  - a created Thread's is minted at `thread.new-indirect`.
- **The sync cross-component arm still passes its caller's chain** in the context, and the callee's entry
  still re-enters when `h.runChain` equals it.

`reenter.wasm` is parent → child → parent, all synchronous on one Thread. Its outer parent frame holds the
parent's slot under that Thread's chain, and the inner entry finds that chain holding it and nests.

- **What A changes is when `h.runChain` moves:** only at a handoff, to the next Thread's own chain. A
  re-entry from a *suspended* Thread's nested frames is impossible, because those frames are not
  executing.
- **One case to state:** a Thread inside a synchronous call P → C keeps P's slot while C executes, though
  no P code runs. That is the invariant's *"re-entrantly for that Thread's own synchronous nesting"*, and
  it is the reference's shape too. `canon_lift`'s sync tail resumes only candidates from `inst.threads`
  of C until C's task resolves, so no P Thread runs meanwhile.
- **If C's nested entry back into P suspends**, it releases P's whole slot, as `suspending` already does
  at any depth. The reference's sync tail then picks among P's `inst.threads`, so other P Threads may run.

**7. The sync branch and the trap delivery** (amended on the chair's item 1).

- **Sync branch:** unchanged in logic. The origin is resumed with the trap, so the slot goes to the
  origin, which unwinds through its entry's `releaseRun`.
- **An origin that reached the scheduler by a `detach`** is not parked, so `claim` refuses it. It does not
  get the sync-branch trap, which is right, because it is waiting on an event rather than suspended. The
  first text stopped there, and that was incomplete: it also said such an origin receives no *created
  Thread's* trap, and left an unclaimed trap to "the scheduler frees it". Below is what happens instead.

**What the reference requires.** ADR 0100 § Trap transport reads it as: *"a trap in any Thread is a trap of
the call, wherever that Thread is in the switching graph"*. `trap()` raises `Trap(BaseException)`, and
nothing in `canon_lift`, `Thread.resume` or `Store.tick` catches it, so the computation ends. No Thread
keeps waiting after it.

**Where a created Thread's trap surfaces today, by symbol:**
1. `cmThread.run` sends `handoff{exited, err}` on `cmScheduler.back`.
2. `cmScheduler.drive` sets `deliver = origin != nil && s.tt.claim(origin)`, then
   `s.tt.killParked(hb.from.owner)`, and returns the error.
3. `cmScheduler.loop` routes it:
   - **the `back` arm with `deliver`:** `hb.from.resume <- err`. The origin's `giveBack` returns it, and
     its lift returns it as the call's error.
   - **the `yield` arm:** `reply <- err`. `threadTable.yieldPoint` returns it, and the callback loop's
     `case callbackYield` returns it as the call's error.
   - **the `back` arm without `deliver`:** **dropped.** The arm's `switch` has no case for `err != nil &&
     !deliver`.

Two further facts about today:
- **The origin is the chain's starter, not the trapping Thread's owner.** `killParked` is scoped by owner,
  but delivery is to whoever started the chain. So a chain started by task A that runs task B's Thread
  hands B's trap to A's call, and B's own call is never told. Under the chain model that sequence needs
  B's Thread queued while B is in its WAIT arm, and #998 (below) is what makes the WAIT arm leave it
  queued. Unmeasured: a two-call fixture would need a parent driving both calls concurrently.
- **Nothing poisons the instance.** That is ADR 0100's recorded choice.

**What wakes a waiter whose event can now never arrive: today, only `Close`.**
- `waitableSetWait`'s loop receives on the set's `wake` and on `c.Context().Done()`, and that context is
  the Thread's lifetime context, cancelled by `Instance.Close`.
- `killParked` skips the waiter, because it reaches only `threadSuspended` and `threadReady`, and a waiter
  is `threadRunning`.
- The chain model hid this: no sibling could run while a Thread waited (#977's residual), so no sibling
  could trap. Under A siblings do run, so the case becomes reachable.

**Under A, a trap goes to its Thread's owner, and every Thread the owner has is ended.**
- **Recorded on the owner.** The trapping Thread's `owner` record (its lift's implicit Thread, which a sync
  lift has as well) gets a `fault` error and a `faulted` channel, closed once; the first trap wins.
- **Delivered to the owner's implicit Thread wherever it is:**
  - parked in `giveBack`: claimed and resumed with the error, which is today's delivery, keyed on the owner
    instead of the origin;
  - in the YIELD arm's wait (point 3): that `select` receives `faulted` too;
  - in the callback loop's WAIT arm (`asyncHandles.awaitEvent`): its `select` receives `faulted`;
  - in a `waitable-set.wait` or sync `subtask.cancel` inside the callee: those waits receive `faulted`;
  - not waiting at all: the callback loop checks `fault` before each re-entry. It cannot be executing
    guest code of this instance at that moment, because the trapping Thread held the slot.
- **Every other Thread of the owner is ended.** `killParked(owner)` extends to waiters: `suspending`
  marks a detaching waiter `threadWaiting` and gives it a `kill` channel, `killParked` closes it, and the
  wait returns `errThreadKilled`. A created waiter's goroutine then exits without reporting, as a killed
  `giveBack` does today.
- **The slot:** the scheduler frees it, unless the owner's implicit Thread was claimed from `giveBack`,
  in which case it takes the slot, as today's `deliver` does.
- **The one case with no call to surface to:** an owner whose lift has already resolved, a Thread
  outliving its task. ADR 0100 records post-resolution execution as unmeasured. The trap ends that owner's
  Threads, and it is **not dropped silently** (the chair's second review). It is recorded on the instance
  in an internal field, `threadTable.orphanTraps`, appended under `threadTable.mu`, which a test reads
  after the fact.
  - **Witness:** the build adds a test that resolves a task, lets one of its Threads trap afterwards, and
    reads the field.
  - **Neuter:** the field is never set.
  - **Surfacing it publicly would be public API.** It is on the flip list
    ([#999](https://github.com/scttfrdmn/burroughs/issues/999)), decided *internal only* unless Scott
    objects.

The design changed on two counts in the first review's amendment: the routing is by owner, and waiters
become killable. The second review accepted both, and added the three items below.

**A divergence that remains, recorded with both readings (the chair's second review, item 1).**
- **The reference:** `trap()` raises `Trap(BaseException)`, and nothing in `canon_lift`, `Thread.resume`
  or `Store.tick` catches it. The whole store's computation ends, so every task's call ends, not only the
  owner's.
- **Burroughs under A:** the trap ends the owner's call and the owner's Threads. Other tasks' calls on the
  instance continue, under ADR 0100's choice not to poison an instance.

Owner routing is the closest to the reference that the no-poison choice allows. It is on the flip list
(#999) as **unmeasured**, with no fixture. A two-call fixture needs a parent driving both calls at once,
which is the two-task note above.

The same divergence covers one nested case. A **sync lift's** implicit Thread is its own owner (#970), so
it is a different task from any Thread that called it. A trap in one task therefore does not end a wait
inside a nested sync lift of the same instance that a sibling task entered. That is a waiter outside the
owner, and in the reference it would end with the store.

**A killed waiter nested in another component's frames (the chair's second review, item 3).** Take a
waiter whose wait sits inside a synchronous call into instance C: C's sync lift has been entered on the
caller's goroutine through `bindCrossComponent`'s sync arm, and its implicit Thread waits. The unwind, by
symbol, from the wake outward:

1. **The wait returns.** `waitableSetWait`'s loop, or sync `subtask.cancel`'s, receives its record's `kill`
   or its owner's `faulted`, and returns `errThreadKilled` or the trap.
2. **The slot is taken back.** That happens inside `asyncHandles.suspending` before the excursion ends,
   whatever `fn` returned, and the saved `(chain, depth)` is restored. So C's depth is whole for the
   unwind's `releaseRun`.
3. **The canon call's error becomes a trap of C's guest.** `interp.Instance.callAdapter` returns the
   error, and the interpreter unwinds C's frames as it does for any failing host call. That is the
   existing path every refusing canon built-in already takes.
4. **C's entry cleans up.** `compFunc.invokeWith`'s deferred `f.h.releaseRun()` drops C's run-slot depth,
   and its deferred `f.threads.remove(tid)` frees C's implicit Thread record. The error is returned as the
   call's.
5. **The caller sees an error.** `bindCrossComponent`'s sync arm returns `res.flat, cerr`. The caller's
   canon call fails, and step 3 repeats in the caller's frames.

Two fields have nothing to clean:
- **No subtask entry:** the sync arm creates none (a subtask exists only on the async arm,
  `asyncLowerFunc`).
- **No borrow entry:** the sync arm passes flat core values and lowers no handle.

**The existing trap-unwind path covers steps 3–5.** Steps 1 and 2 are A's, and no committed fixture drives a
trap through a sync cross-component call at all. **So the build owes a fixture, oracle first:**
- a nested child's sync lift spawns T and waits;
- T traps;
- the parent called the child synchronously;
- expected: the parent's call returns T's trap, and C's run slot and Thread table are left empty.

ADR 0102 records that wasmtime 49.0.2 traps a sync-lift `waitable-set.wait` outright. So this oracle is the
**reference reading** (below), with wasmtime's refusal recorded beside it.

**The reference has decided the case, and the expected value above holds** (the chair's ruling after the
merge: the reference decides, not the prose). The scenario is `nested_sync_unwind` in
`testdata/cmthreads/reference/refread.py`, calibrated first on `wait-runs-ready`, where the reference
gives wasmtime's 5. Readings, over both `DETERMINISTIC_PROFILE` settings and eight seeds each:

| arm | reference reading |
|---|---|
| T traps | **T's trap**, the only outcome |
| control: T releases instead | **the wait's own trap**, raised at `canon_lift`'s sync tail, `trap_if(not candidates)` (traced) |

- **The trap arm agrees with the prose:** the parent's call returns T's trap.
- **The control arm does not complete.** `h`'s wait is on a subtask whose callee runs in *another*
  instance, and the sync tail resumes only `h`'s own instance's Threads. Once T has run, no candidate is
  left, and the tail traps.
- **Burroughs admits that wait and would return 5 under A.** That is a **measured divergence**, added to
  #999. It also narrows ADR 0102's recorded example, that the pin admits a sync `waitable-set.wait`. The pin
  admits it where a Thread of the waiting instance can satisfy it, and traps it where the event depends on
  another instance. ADR 0102 is with Scott, and its text is not touched here.

So the fixture discriminates by trap **site**: T's trap where the control traps in the sync tail.

**`killParked`'s close follows the rule from its earlier fix.** It already closes every `kill` *after*
`tt.mu.Unlock()`, and it takes no `asyncHandles.mu`. A's extension keeps both properties:
- **Waiter `kill` channels:** closed in the same post-unlock loop.
- **The owner's `faulted` channel:** closed after its `fault` is written under `tt.mu` and the lock is
  released.
- **The receivers** (the wait loops, `awaitEvent`, `yieldPoint`) select with no lock held. The wait loop
  drops `h.mu` before its `select`, as `waitableSetWait` does today.

So no close is made while the closer holds a lock a receiver needs in order to observe it.

### Witnesses: every existing one, and what changes

The predictions name **what each asserts and whether that moves**. Neuters move only where the mechanism
they name moved.

| witness | value | mechanism |
|---|---|---|
| `TestTwoCallsIntoOneInstanceNeverExecuteAtOnce` | unchanged | unchanged |
| `TestAChainReentersAnInstanceItAlreadyHolds` (`reenter.wasm`, 8) | unchanged | unchanged: the chain is the Thread's, and the sync arm still carries it |
| `TestAResultStoreWaitsForTheExecutingTask`, `TestAChildsResultStoreWaitsForTheParentsExecutingTask` | unchanged | unchanged; the scheduler is one more contender for the slot |
| `TestAResultStoreDoesNotLandWhileTheHolderIsInHostCode`, `TestAResultStoresReallocRunsWithNoEngineLockHeld` | unchanged | unchanged (`inHost` and `hostStores` are untouched) |
| `TestACreatedThreadsWaitReleasesTheRunSlot` (8) | unchanged | **changes**: the wait is a `detach` handoff, not a bare release. Its neuter ("restrict `suspending` to implicit Threads") names a branch that no longer exists. It becomes "make the wait keep the slot", which hangs. |
| `TestSwitchWasmReturnsTheCommittedReading` (`0x0888887f`) | unchanged | **changes**: `giveBack` carries the slot. Neuter unchanged. |
| `TestAThreadThatSuspendsPartwayLetsTheYieldingCallbackContinue`, `TestASpawnedThreadRunsAtTheYieldAndHoldsItsOwnIndex` | unchanged | **changes**: `yieldPoint` takes neither lock. Neuters unchanged. |
| `TestASyncLiftRunsASpawnedThreadBeforeItReturns`, `TestASyncLiftThatSuspendsWithNothingReadyTraps` | unchanged | the trap's slot goes to the origin (point 7). Neuters unchanged. |
| `TestCloseEndsAParkedThreadsGoroutine`, `TestCloseEndsAThreadParkedInsideAHandoff` | unchanged | the scheduler's new acquire `select` must also receive `stop`, or Close hangs on it |
| `TestAThreadParkedInsideAHandoffIsAtASafepoint` | unchanged | unchanged: the park is still inside `c.Blocking`. The scheduler is not a caller. |
| the `cmthreads_test.go` index and gate tests (7) | unchanged | unchanged |
| the `liftpark_test.go` witnesses (7) | unchanged | unchanged: `entrySem` around each entry is untouched |
| the `ctxcancel_test.go` witnesses (5) | unchanged | unchanged. `TestCancellingTheContextOfAYieldingGuestEndsIt` uses no 🧵, so `yieldPoint` returns before the change. It loses one wait, the context-abandonable `enterTask` it took when the queue was non-empty. |
| `crosscall_test.go`, `resultlist_test.go` (6) | unchanged | unchanged |

### Owed by the build

- **#978's fixture returns 8:** `cmthreads/sync-enters-past-suspended.wasm`, wasmtime 49.0.2's reading.
  - Neuter 1: `giveBack` keeps the slot. Neuter 2: `releaseRun` never kicks. Each hangs, bounded.
- **#977's residual gets a fixture, oracle first.** The shape written here first cannot be read:
  wasmtime 49.0.2 refuses a future copy whose two ends are in one instance (*"resource not present"*),
  with or without Threads, so T2 cannot release T1 through a future.

  The next shape has T1 wait on a nested child's subtask and T2 release the child. Measured while
  answering item 1, that hits wasmtime's *"BUG: delivering non-present event"* (`concurrent.rs:948`)
  when a created Thread's `waitable-set.wait` receives the event of a subtask whose callee YIELD-looped.
  #977's own fixture avoids it, because its callee calls back synchronously and does not loop.

  So the build tries a shape on that pattern first. **If no shape reads on wasmtime, the oracle is the
  pinned reference itself**, under ADR 0102: the pin defines the semantics, and it is executable.
  - **The scenario:** written in Python against the vendored `definitions.py` (the `Store`, `canon_lift`
    and Thread API, driven the way `canon/gen/gen.py` already drives the pin), mirroring the fixture.
  - **Where it lives:** committed beside the fixture, its output labelled **reference reading** so it is
    never mistaken for a wasmtime reading.
  - **The wasmtime bug** is recorded against the fixture, as now.

  Reporting without a fixture is **not** the fallback; the chair's second review ruled it out. Neuter: the
  wait as a bare release, which hangs (the #977 amendment's behaviour).
- **#998's fixture, `cmthreads/wait-runs-ready.wasm`, returns 5** (wasmtime 49.0.2, 10 of 10; committed
  with this amendment, oracle first). A Thread readied before the callback returns WAIT runs during the
  wait. Neuter: `releaseRun` never kicks, which hangs, bounded.
- **The trap-while-waiting fixture, `cmthreads/trap-while-waiting.wasm`** (the chair's item 1; committed
  with this amendment). wasmtime 49.0.2 traps with `unreachable` in `bodymod!t2`, 10 of 10. The call must
  return that trap, not hang, and T1's wait must be ended. Neuter: the scheduler frees the slot and drops
  the trap, which hangs, bounded.
  - **Its completing control** (`unreachable` → `nop`) has a **reference reading** from the pin, because
    both wasmtime control shapes hit the wasmtime bug. The reading is `value 1`, and the trap arm reads
    `trap t2`. Both are in `testdata/cmthreads/reference/refread.reading`, taken after the method's
    calibration on `wait-runs-ready`. That makes the
    fixture discriminate in both directions: a trap with `t2` and a completion without it. The fixture's
    header records the wasmtime bug.
- **The owner-resolved trap test** (point 7): the trap is read from `threadTable.orphanTraps` after the
  fact. Neuter: the field is never set.
- **The nested sync-call unwind** (point 7): a fixture, with its oracle the reference reading. The parent's
  call returns T's trap, and C's run slot and Thread table are left empty.
- **The append assertion** (point 5), with its neuter run.
- **The YIELD arm's `entrySem`, its own fixture, oracle first.** While task A YIELDs, its created Thread
  makes a call that reaches an async lift of the same instance, which needs `entrySem`. Under the old
  arm that deadlocks. Neuter: `yieldPoint` re-taking `entrySem`.
- **Unchanged unless predicted above:** every existing 🧵, run-slot, liftpark, result-store and
  cross-component witness.
- **The handoff re-measured on `switch.wasm`** against the chair's baseline of 45.15 µs, by `make ab`.
  Each handoff gains an `h.mu` save and restore, so the forecast is a small increase, quoted with its
  p-value.
- **The concurrency witnesses, 50 times under `-race`**, on heap-backed fixtures.

### What this does not decide

- **A sync-typed task's `waitable-set.wait`.** ADR 0102 records it as admitted, and A changes neither its
  admission nor what it waits for. Only the slot moves, at point 4.
- **Cancellation delivered to a suspended Thread, and the promote divergence.** These are the two slices the
  chair ordered after A's build.
