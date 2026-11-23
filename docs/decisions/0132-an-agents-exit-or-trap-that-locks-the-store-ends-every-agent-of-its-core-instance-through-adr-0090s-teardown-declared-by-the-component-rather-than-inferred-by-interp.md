# 0132 — An agent's exit or trap that locks the store ends every agent of its core instance, through ADR 0090's teardown, declared by the component rather than inferred by `interp`

Date: 2026-11-23 · Status: **accepted**, decided in the slice. By the #647 review (*"Report decisions taken, not rulings requested. Escalate only for: contract (§) text, public API surface, reversing a stamped ADR"*) it is decided here and reported as a decision taken: it adds no contract text, changes no root-package file, and reverses no stamped ADR (the section on ADR 0071 below says why). The clause it implements is contract §2 T-2, *"There is **no main-thread special case**. Every thread, including the first, MUST be permitted to block in `memory.atomic.wait`"*: before it, the first agent could block in `memory.atomic.wait32` only if no sibling would exit or trap. It reuses §2 T-5.4's mechanism without claiming T-5.4's obligation, as [ADR 0090](0090-proc-exit-is-instance-scoped-and-its-teardown-is-a-request-routed-into-the-shutdown-mechanism-that-already-exists.md) does. The record is the actor's, so commits resting on it are `Ratio-Class: carried`. Filed against [#1357](https://github.com/scttfrdmn/burroughs/issues/1357), found by F7 (#1341).

**Every citation into this tree names a symbol rather than a line** (ADR 0047).

## Context

A component whose canonical memory is shared starts agents through the spawn door ([ADR 0131](0131-the-component-spawn-door-starts-the-mstart-of-the-core-instance-that-imports-it-which-must-use-a-shared-canonical-memory-and-every-refusal-is-at-link.md)). Each is a thread of its spawner's task and runs outside the run slot ([ADR 0130](0130-in-an-instance-whose-canonical-memory-is-shared-a-spawned-agent-is-a-thread-of-its-spawners-task-and-executes-outside-the-run-slot-which-keeps-serialising-tasks-and-stops-serialising-result-stores.md)). When one calls `wasi:cli/exit`, or traps, while `run` is parked in `memory.atomic.wait32`, the run never returns. #1357 read it in a fork-built Go program: 4 of 10 runs hung after printing their full stdout, and a host dump showed every remaining agent in `interp.(*memory).wait` with an infinite timeout.

**The cause is four pieces of existing code, read together:**

1. `Host.exit` and `Host.exitWithCode` return `exitError`, and `interp` wraps a host function's error as `interp.ErrHostTrap`, so an exit unwinds only the agent that called it.
2. The agent's Exit hook, `threadTable.agentEnded`, routes that error to the owner through `threadTable.routeFault`. The store locks ([ADR 0105](0105-a-trap-locks-the-component-instance-and-every-later-call-is-refused-with-the-trap-because-the-pin-locks-the-store.md)), and the owner's wait is aborted **only if it is a component-model wait**.
3. A `memory.atomic.wait32` is a core instruction, not a canon built-in or a component-model suspension, so neither `lockGuard` nor a lock case in the async waits reaches it.
4. `world.retire` requests the teardown that would end it (`world.requestGuestTeardown`, ADR 0090) only for an error that declares `interp.InstanceEnder`. The component's `exitError` does not declare it: its comment says a component-model Thread runs on the host agent and does not need to, which was true before ADR 0130 gave a component agents.

So the store is locked, the instance is over by ADR 0105, and the work in flight that ADR 0105 says *"ends with the same error"* is parked where the lock cannot reach it.

### What the contract says, checked

- **It has no instance-lifetime clause and never names a guest exit.** ADR 0090 verified that by grep over the normative text, and it is still so. Whether a non-main thread's exit or trap ends the run is therefore an ADR's subject, and two accepted ADRs have answered it for the two paths: ADR 0090 (*"`proc_exit` on any agent ends the instance"*) for preview 1, and ADR 0105 (a trap anywhere locks the store, and in-flight work ends) for a component.
- **§2 T-2** forbids a main-thread special case. The defect is one: the same exit ends the run from `run`'s own agent and hangs it from any other (#1357's `LockOSThread` reading, 10 of 10 against 6 of 10).
- **§2 T-5**: *"exit is the only termination directed at an individual thread: there is no cancel and no kill aimed at one thread."* An instance-wide teardown is aimed at no one thread, so T-5 is not contradicted, and it rules out "kill each sibling" as the repair's shape, as it did for ADR 0090.
- **§2 T-5.4** is engine shutdown's obligation, and a guest's exit is not shutdown. What is borrowed is its *how*: a thread suspended in `memory.atomic.wait` is ended *"by trapping it out of the wait rather than by returning one of that instruction's defined results"*. `memory.wait`'s `<-t.context().Done()` arm already does that, and `world.requestGuestTeardown` already drives it.

**No contract text is needed and none is contradicted**, so the repair is this ADR's to decide.

## Options

- **A. The component's `exitError` declares `interp.InstanceEnder`** (ADR 0090's mechanism, unchanged). An agent's exit tears its core instance's world down. *Covers* row (a). *Costs:* row (b), an agent's trap, still hangs a parked `run` on a locked store, so the defect is halved.
- **B. `agentEnded` declares every fault it routes.** The Exit hook returns the error the agent's thread retires with, and when the hook has routed the agent's end as a fault (so the store is locked), it returns that error wrapped in a component type that declares `interp.InstanceEnder`. `world.retire` then requests ADR 0090's teardown, which cancels every live thread of the agent's core instance: `run`'s host agent and every sibling agent. *Covers* (a) and (b) with one rule: an agent's end that locks the store ends the core instance's agents. *Costs:* `interp.AgentHooks.Exit` changes type from `func(error)` to `func(error) error`, in an internal package; and the teardown reaches only the agent's own core instance's world (named below).
- **C. `world.retire` infers the teardown from any trap of an agent that has hooks.** *Costs:* `interp` would decide a component's semantics by inference, which is the shape ADR 0090's amendment 4 withdrew in favour of a declaration.
- **D. Close every core instance at the routed fault.** This is ADR 0105's option C, rejected there because a call after the lock would fail on a closed instance, so the entry check could not be seen. It also cannot run where the fault is routed: `agentEnded` runs on the agent's goroutine before `world.retire`, and `Close` waits for that agent.
- **E. Wake every futex waiter (`notify` all).** *Costs:* the waiter returns one of `wait32`'s defined results into a locked store, which T-5.4's mechanism exists not to do, and a guest that loops on its futex (Go's `notesleep`) parks again.

## Choice

**B.**

- **The rule**: an agent's end that `agentEnded` routes as a fault ends every agent of its core instance. That is every exit and every trap of an agent, except one Close or a teardown caused (`agentEndedByTeardown`) and one that lands while the instance is closing (`hostWaitsStopped`), which are not routed today and are not declared.
- **The declaration is the component's** (`agentEndsInstance`, internal/component/agents.go), and `interp` is unchanged in what it infers. `world.retire` still tears down only for an error that declares `interp.InstanceEnder`, so a core instance outside a component keeps ADR 0071's answer to a spawned trap.
- **The routing is unchanged and comes first**: the store locks before the teardown is requested, so no call can start between them, and the owner's fault and a component-model wait's abort are as before.
- **The wrapper's text is the routed error's, and it unwraps to it**, so the fault `interp.Instance.Fault` reports and the error `run` returns keep their chains: `errors.As` finds the `exitError` or the `*interp.Trap`, and `errors.Is` finds `interp.ErrHostTrap`.
- **What `run` returns.** Its parked `wait32` is trapped out with `interp.ErrTerminated`, and `invokeIndex`'s terminal conversion returns the recorded first cause (`world.guestExitCause`, ADR 0090), which carries the agent's error. So `CallRun` returns the exit code for (a) through its existing `errors.As`, and the trap for (b). The cause is the trap the store locked on, so `storeLock.finish` reads it as the call's own, as a trap routed to its owner is.

### Why this does not reverse ADR 0071

ADR 0090's amendment 4 withdrew "a spawned trap ends the instance" because, in `interp`, it reversed stamped ADR 0071: a host entry after a spawned thread's trap *"must run"*. That answer is about a core instance, and it is unchanged here, because `interp` infers nothing new. In a component the answer was already different, and by an accepted record: ADR 0105 locks the store on every trap, an agent's included (ADR 0130 routes an agent's trap to its owner, and `routeFault` locks), and refuses every later call at the entry check. A core instance inside a locked component has no entry that reaches it, so ending its agents changes nothing a caller can observe except that work in flight ends, which ADR 0105 already requires. This extends ADR 0105's in-flight list by one suspension, a `memory.atomic.wait`, which it did not name because no component had agents when it was written.

## Consequences

- **A component's `run` ends when any agent exits or traps**, with the exit code or the trap, through `burroughs run`, `ComponentConfig.Run`, and `Instantiated.CallRun`. `Close` afterwards ends what is left and waits for it.
- **The exit from `run`'s own agent is unchanged** (row (c)): it returns through `CallRun` as before, and `Close` ends the parked agents by T-5.4.
- **No public API changes.** `interp.AgentHooks.Exit` changes type, in an internal package, and its one caller is `asyncHandles.spawnAgent`. No identifier is added outside `internal/component`'s unexported ones.
- **The error value an in-flight call returns changes** when an agent trapped while it was parked: it was nothing (the call never returned), and is now the recorded fault, `interp.ErrThreadFault` wrapping the agent's trap.

### Named limit: the teardown reaches the agent's own core instance

`world.requestGuestTeardown` cancels the live threads of one `interp` world, the one the agent's core instance owns (the one whose `mstart` it runs, ADR 0131). A thread's world is the instance it was invoked on, not the one whose code it is executing. So a `run` lifted from core instance A that calls into core instance B, imports the door there, and parks in `wait32` inside B's code is A's host agent, and B's teardown does not reach it. Every fixture in this tree, and the Go fork's output, lifts `run` from the instance that imports the door, so the case has no consumer. Its trigger is a component that lifts `run` from one core instance and spawns from another. The repair would be a teardown request across the component's core instances, which needs a new `interp` entry point.

## Registered witness, before the repair is built

Three fixtures, `testdata/agent-ends-run-{exit,trap,control}.wat`, each a `wasi:cli/run` component whose `run` spawns one agent through the door:

| row | the agent | `run` | before (forecast) | after (forecast) |
|---|---|---|---|---|
| (a) exit | `exit-with-code(7)`, once `run` has published that it is about to wait | parks in an unbounded `wait32` | hangs; the witness names `run` parked in `interp.(*memory).wait` within its bound | exit 7, every agent ended, no engine goroutine survives `Close` |
| (b) trap | `unreachable`, at the same point | as (a) | hangs, as (a) | the trap, as (a) |
| (c) control | parks in an unbounded `wait32` | `exit-with-code(7)` once the agent has published | exit 7, and `Close` ends the agent | unchanged |

Read at `internal/component` (`CallRun`), at the root package (`ComponentConfig.Run`), and at `burroughs run` (its exit code). The neuter of the declaration (the hook returning the routed error unwrapped) must turn (a) and (b) back into a hang that the witness names as a place within its bound.

## Rollback

`agentEnded` returns the routed error unwrapped: no agent's end declares the teardown, and a parked `run` hangs on a locked store, as before #1357.
