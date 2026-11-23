# 0130 — In an instance whose canonical memory is shared, a spawned agent is a thread of its spawner's task and executes outside the run slot, which keeps serialising tasks and stops serialising result stores

Date: 2026-11-23 · Status: **accepted**, 2026-11-22. Scott's ruling 1 on the [#1332 plan body](https://github.com/scttfrdmn/burroughs/issues/1332): *"D-F3 = (b). Spawned Ms are threads of the run task. This relaxes ADR 0101's run slot only for instances whose canonical memory is shared; that partly reverses a stamped ADR, and this ruling is the citation."* The ruling was given in session and posted on the plan issue by the actor, so commits resting on it are `Ratio-Class: carried`. It partly reverses [ADR 0101](0101-a-component-instance-executes-one-task-at-a-time-through-a-run-slot-released-only-where-the-reference-suspends.md), for shared-memory instances only, and that ruling is the citation for the reversal; ADR 0101's text is not edited. It is an engine mechanism inside `internal/component` and `internal/interp`: no contract text, and no public API (the root package does not change, and the one new exported method, `interp.Instance.SpawnAgent`, is in an internal package). Filed against [#1336](https://github.com/scttfrdmn/burroughs/issues/1336), E2 of the v0.15.0 plan.

**Every citation into this tree names a symbol rather than a line** (ADR 0047). Citations into `definitions.py` name a function or a method.

## Context

v0.15.0 is a Go program with GOMAXPROCS > 1 inside a component (#1332). Its Ms run guest code at once over one shared canonical memory (admitted behind `gate:cm-shared-memory`, [ADR 0129](0129-a-shared-canonical-memory-is-admitted-behind-gate-cm-shared-memory-as-a-burroughs-only-extension-that-departs-from-the-pins-canonopt-rule-and-with-the-gate-off-the-rule-is-enforced-as-before.md)), and each M calls the async ABI's built-ins directly: waitable sets, streams, futures, subtasks.

**ADR 0101 forbids that.** One agent of a component instance executes guest code at a time, through `asyncHandles.runSlot`, taken at every entry and released only where the reference suspends. The reference runs one thread at a time (`def:Store.tick` resumes one ready thread, and `def:Thread.resume` runs it until it suspends), and the slot is how Burroughs builds that over goroutines.

**Ruling 1 relaxes it for shared-memory instances.** A spawned M is a thread of the run task: it calls async lowers and `waitable-set.wait` itself, while the run task's own agent and its siblings execute. The plan maps twelve places where the engine assumes one agent executes (#1332, "Engine"). This record says what replaces each.

**The spawn door is not this row.** E3 ([#1337](https://github.com/scttfrdmn/burroughs/issues/1337)) binds `burroughs:threads/spawn` to the mechanism below, after C0's T-1 append. Cross-agent wake as a contract property is E4's ([#1338](https://github.com/scttfrdmn/burroughs/issues/1338)), and cross-agent payload ordering E5's ([#1339](https://github.com/scttfrdmn/burroughs/issues/1339)). E2 makes the engine **able** to run several agents of one instance, and its witnesses start them from the test harness.

## Options

1. **A run slot per agent, with no shared state.** Each agent gets its own handle table and scheduler, so nothing is shared and nothing needs a lock beyond today's. *Costs:* R-4's append (C0, [#1349](https://github.com/scttfrdmn/burroughs/pull/1349)) says an instance's parallel agents share its handle table. A Go netpoller on one M polls a waitable set whose members other Ms created, which a per-agent table cannot express.
2. **One lock held across every built-in, waits included.** Agents execute guest code in parallel and serialise at the boundary. *Costs:* a wait would hold the lock, so H-4's *"sibling agents in the same instance remain runnable"* fails at the first `waitable-set.wait`, and §4 B-MM-3 forbids holding an engine lock across a guest resume.
3. **OS-backed component-model Threads.** The 🧵 `thread.new-indirect` family run on real threads, and the Go fork uses those rather than a Burroughs spawn. This is the plan's destination if WASI 0.3.3 adopts cooperative threads. *Costs:* it changes the scheduler's whole model (ADR 0100's handoff, ADR 0101's Option A), and the fork's wasip3 runtime has no such build. It is not this milestone's.
4. **The plan's proposal as written: the slot keeps serialising result stores, `h.lift` and the scheduler's handoffs, and stops serialising guest execution.** *Costs, found by reading the stores against a Go runtime:* see "Why result stores leave the slot" below. The proposal is taken except for its first item.
5. **Option 4 with result stores taken out of the slot.** *Costs:* a store applied while its agent executes cannot run guest code on that agent's stack, so a store that needs `cabi_realloc` off its agent's own goroutine is refused by name.

## Choice

**Option 5.**

### 1. Which instances, and the predicate

**A free instance** is one whose walk admitted at least one shared canonical memory and named no unshared one (`asyncHandles.agentsRunFree`, set by `walker.noteCanonMemory` from `walker.refuseSharedCanonMemory`, which admits a shared one only with `gate:cm-shared-memory` on). It is an instance property, read at link and fixed for the instance's life. **There is no new gate.**

- **Every other instance keeps ADR 0101 exactly.** That includes an instance with an unshared canonical memory and a shared non-canonical one, where `interp.Instance.Spawn`'s precondition holds: its spawned agent takes the run slot at entry as a chain of its own and gives it up where the reference suspends, so it is one more chain of ADR 0101's population.
- **An instance with both a shared and an unshared canonical memory is not free.** Its agents could lower into the unshared one in parallel, which is a race on an unshared memory.

### 2. A spawned agent is a thread of its spawner's task

`asyncHandles.spawnAgent` (in `agents.go`) is the mechanism E3's import will call. Its caller in E2 is the test harness.

- **It is registered in the instance's Thread table** as a `cmThread` with `agent` set. Its `task` and `owner` are its spawner's, which is the reference's `Thread(task, …)` and `task.register_thread` (`def:Task.register_thread`), so a trap in it is routed to that owner (ADR 0101's Option A, point 7) and a trap of that owner ends its waits.
- **Its index is carried on its own stack** (`interp.AgentHooks.CMThread`), as `InvokeCMThread` carries an entry's, so `callerTask`, `callerStorage`, `thread.index` and `suspending` find its record and not its spawner's.
- **Its context storage is its own** (`cmThread.storage`, zero at start), as the reference keeps storage per Thread.
- **Its globals are its own (§2 T-6)**: it is an `interp` spawned thread (`interp.Instance.SpawnAgent`), whose defined globals are built from the module's initializers. A component's entries run on the instance's initial agent, `&in.host`, as before.
- **It is removed from the table when its entry returns**, on its own goroutine, before `interp`'s `world.retire` records its exit (`AgentHooks.Exit`).

### 3. What the run slot serialises in a free instance

**It keeps serialising tasks.** Every lift entry, sync or async, takes it as before; the 🧵 scheduler's handoffs carry it as before (Option A); `asyncHandles.lift` is saved and restored with it as before. Two tasks of a free instance still interleave only at suspension points.

**It stops serialising a spawned agent's guest execution.** A spawned agent of a free instance takes no slot at entry, holds none while it runs, and gives none up when it waits. Its waits are still marked as the reference's `wait_until` (state, `ready_func`, an abort channel), so the sync tail counts them and a trap or `Close` ends them.

**It stops serialising result stores.** An async lower's resolution (`asyncLowerFunc`'s `onResolve`), a host stream producer's delivery (`streamProducer.deliverStore`) and a socket send's (`sockSend.deliver`) are applied at once, on the goroutine that makes them, with no slot. The one exception is ADR 0101's in-host window, per agent below.

#### Why result stores leave the slot

**A store cannot be serialised against a task whose threads run in parallel.** ADR 0101's store waits for the slot so that it lands while no guest code of the instance executes. In a free instance that moment does not exist: the spawned agents never hold the slot. A store under the slot is serialised only against the run task's own agent, which orders nothing a spawned agent can observe.

**And it deadlocks the program this milestone is for.** The run task's agent is a Go M, and an idle M parks in `memory.atomic.wait32` (`notesleep`). That is not a suspension point, so it holds the slot across the park. A store for one of its subtasks, a `wait-for` timer that M started, would then wait for the slot while another M waits in `waitable-set.wait` on the set holding that subtask, and the parked M waits for that other M to wake it. No one runs. The proposal would have built that cycle into C3.

**What a store needed the slot for, and what replaces it:**

- **Not landing under another task's execution.** In a shared memory, a store writes a buffer the guest handed over for that operation (a return pointer, a read buffer), and the guest reads it only after it takes the event. The event is published under `asyncHandles.mu`, and taking it takes `asyncHandles.mu`, so the write happens-before the read (§4 B-MM-1's async-wake edge, R-3). A second agent writing that buffer meanwhile is the guest's race.
- **Running `cabi_realloc` on an idle stack.** A store that lowers a string or a list calls the lowering agent's `cabi_realloc` on that agent's stack (`CanonCaller.Realloc`). The slot guaranteed the stack was idle. In a free instance nothing guarantees it, so **a store applied off its agent's own goroutine refuses a realloc by name** (`offAgentHeap`). The refusal is the owner's trap, by the path a late store's failure already takes (`threadTable.routeFault`, #994). A store applied on its own agent's goroutine, which is every inline resolution, may call it.

The pin's own answer is to run the realloc as a call of its own (`def:LiftLowerContext.reallocate` calls `self.inst.store.invoke(realloc, …)`), which in Burroughs would be a fresh agent with fresh T-6 globals. Whether a Go guest's `cabi_realloc` can run on one is the fork's F9 question (#1332). So it is refused here and not built.

### 4. The in-host window is per agent

ADR 0101's result-store amendment queues a store that arrives while the slot's holder is in host code (an async lower's impl, a subtask's `onCancel`), and the holder applies the queue as it leaves host code, on its own goroutine, before its guest resumes. That ordering was the instance's because one agent was the holder.

In a free instance, **each spawned agent has a window of its own** (`cmThread.window`), and **a store addressed to an agent is queued in that agent's window**. The lowering agent is the address of a result store. The instance's window (`asyncHandles.window`) is the run task's agent's, and every task's, as before. So a spawned agent's inline resolution is applied on its own goroutine before its lower reads the subtask's state, and a store for another agent is never held behind it.

Producer and socket deliveries are addressed to no agent. They call no guest code, so they keep the instance's window as before and skip only the slot.

### 5. The twelve assumptions, one by one

| # | Assumption, at its site | What replaces it in a free instance | Why it is sound with several agents | Evidence |
|---|---|---|---|---|
| 1 | One slot holder: `acquireRun`, `releaseRunFor`, `suspending`; `cmScheduler.giveTo`, `giveBack`, `free` | The slot's holders are the tasks' chains, as before. A spawned agent is not one of them (§3). | The scheduler never resumes a spawned agent, and a spawned agent never hands the slot over, so the handoff invariant ("whoever sends on `back` holds the slot") has the same population. | Witnessed: peak executing agents, and its neuter. |
| 2 | One current task: `asyncHandles.lift`, read by `callerTask` and `callerStorage`, saved by `suspending` | A spawned agent's task is its record's (§2), read through its stack's index. | `h.lift` is read only as the fallback for a caller with no record, and a spawned agent has one. It is written only by slot holders. | Witnessed: `thread.index` and context per agent. |
| 3 | `inHost`/`hostStores` means "no guest is running" | A window per agent (§4); outside a window, a store is applied at once (§3). | A store never waits for an agent that is executing, and a realloc never runs on a stack that may be. | Witnessed: a store for one agent is not held behind another's host code, and its neuter. |
| 4 | `kick`'s no-lost-wakeup argument and `threadTable.assertSlotHeld` | Unchanged. A spawned agent's 🧵 calls that append to the ready queue or hand off (`thread.new-indirect`, `thread.resume-later`, `thread.suspend` and the switching forms) are refused by name. `thread.index` is not. | Every append is still made under the slot, which is the argument. | Argued, and the refusal is by name. |
| 5 | `threadTable.syncTail`'s candidates: one runner | A spawned agent that is not waiting is a candidate; a waiting one contributes its `ready_func`, as any Thread does. Its own wait runs the tail, as a slot holder's does. | An executing agent can make the waiter's event ready, which is what a candidate is. It is never judged itself: it is not a lift's implicit Thread. | Witnessed: a sync-typed wait on an executing agent does not trap, and its neuter. |
| 6 | `liftTask.threadIdx` is read unlocked | Unchanged. | It is written once in `newLiftTask`, before the task is published, and read by the task's own loop. A spawned agent reads its index from its stack. | Argued. |
| 7 | `stack.mayNotLeave` is sound "because one agent executes at a time" | Unchanged in mechanism; its argument is per stack. | A realloc runs on its caller's own stack, so the mark reaches exactly that agent's calls. Another agent's built-ins are inside no realloc. | Argued. |
| 8 | Every component agent runs on `&in.host`, so globals are shared, and `soleAgentLocked` counts one agent | A spawned agent is an `interp` thread with its own globals (T-6), counted in `world.live`. | `soleAgentLocked` already refuses to relocate an unshared memory while another live agent exists, and a shared memory does not relocate. | Witnessed: each agent reads its own global. |
| 9 | `driveStreamCopyLocked` copies through a stored caller under `h.mu` | Unchanged. | The copy touches only `Caller`'s memory (`Caller.Read`, `Caller.Write`), not the stored agent's stack, under `h.mu`, which also orders it against both ends' events. | Witnessed under `-race` over a Go-heap memory: a stream and a future between agents. |
| 10 | `fence.go` counts slot takes as the instance's writers | Unchanged: a spawned agent is not a fence writer. | The fence orders the reference's single runner against a cross-component callee's segment. Counting an agent's whole life as a writer would hold every callee's segment for that life. A callee's writes into this instance's memory are its result stores, ordered as in §3. | Argued. |
| 11 | `refuseSharedCanonMemory` | E1's gate, and now the predicate's input (§1). | — | E1's witnesses. |
| 12 | The phase-2 refusal in `instantiate.go` | Unchanged, and out of scope. | — | — |

### 6. The memory model

- **§4 B-MM-1, at every host-to-guest transition of every agent.** A spawned agent is an `interp` thread, so its waits are `CanonCaller.Blocking` excursions with their `leaveGuest`/`enterGuest` fence, and its built-ins' returns are its own, as the run agent's are. Nothing here adds a transition without one.
- **R-4's append (C0): the handle table is shared, and handle operations are linearizable.** Every built-in reads and writes the table under `asyncHandles.mu`. `waitableSetWait` looked its set up in one critical section and counted itself waiting (`num_waiting`) in a second, so an agent's `waitable-set.drop` between them dropped a set another agent was about to wait on, with neither trapping. The two are now one critical section.
- **R-3: a delivered event observes B-MM-1 for another agent's payload.** The payload is written before its end's state changes under `asyncHandles.mu`, and the event is taken under it.
- **§2 T-6: per-agent globals**, as in §2 above.
- **§4 B-MM-3: no engine lock across a guest resume.** No new path holds `asyncHandles.mu` or the Thread table's lock across guest code; a store's realloc still runs between its two critical sections.

### 7. D-F5: the lift

Recorded here, as the plan directs, unless the chair rules otherwise.

- **`run` stays a sync lift of an async-typed function**, as v0.14.0 shipped.
- **A spawned agent's sync `waitable-set.wait` is legal**, because it is a thread of that task. The sync tail judges only a sync-typed lift's implicit Thread (grave #1267), and a spawned agent is neither.
- **No stackful lift is built.**

### 8. What does not change

- **Unshared instances.** Every existing run-slot, Option-A, fence and race test is unchanged and stays green. `TestTwoCallsIntoOneInstanceNeverExecuteAtOnce` still holds for an unshared instance. Its free twin shows two agents of one instance executing at once.
- **Task-level semantics in a free instance.** Entries, the callback loop, `entrySem`, the 🧵 scheduler and the store lockdown work as before.

## Consequences

- **A free instance's agents execute guest code in parallel**, and their built-ins are linearizable over one shared handle table.
- **A late store that needs `cabi_realloc` is refused in a free instance**, as its owner's trap, naming this record. No WASI 0.3 import that Burroughs serves resolves late with a string or a list result through an async lower. `resolve-addresses`' list is lowered by its blocking sync lower on the caller's own goroutine (ADR 0126). A guest or a test impl that does this sees the refusal.
- **A spawned agent cannot use the 🧵 switching forms.** A Go guest does not use them.
- **A trap in the run task ends its spawned agents' waits** and locks the store (ADR 0105). An agent executing guest code then ends at its next built-in, at the lock guard, or at `Close`. That is §2 T-5's rule: there is no kill aimed at one thread.
- **A cross-agent wake works by the existing mechanism** (`waitableSet.signalLocked` closes the set's wake under `asyncHandles.mu`, whichever agent changes a member). E2's witnesses avoid depending on it: their cross-agent events are taken by `waitable-set.poll`. Its claim, witness and shutdown arm are E4's.

## Rollback

The predicate is the rollback's one line: `asyncHandles.agentsRunFree` false makes every instance an ADR 0101 instance, and a spawned agent then takes the slot at entry. With `gate:cm-shared-memory` off, which is the default, no instance is free.
