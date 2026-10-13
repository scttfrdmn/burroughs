# 0100 — Phase 1 of the shared-everything-threads family is modelled behind `gate:cm-threads`, each form landing only once it has a committed oracle

Date: 2026-10-11 · Status: **accepted** — chair's ruling, in session, 2026-10-11, under Scott's stated
deferral (*"I defer to you on these types of question — and I do so here"*). `Ratio-Class: carried` on the
commit: the deferral and the ruling are both in session and have no approval artifact to cite.

**Every citation into this tree names a symbol rather than a line**, per ADR 0047. The first draft was
written positionally and converted in the same slice, which is ADR 0077's precedent and the reason
`TestPositionalCitationCensusIsPinned`'s pin does not move for this record. Citations into the upstream
spec documents name a production or a section heading, since those files are not in this tree and
`definitions.py` is fetched material under a gitignored path.

---

## Context

The component model's 🧵 shared-everything-threads family is eleven canonical built-ins. Since
[#957](https://github.com/scttfrdmn/burroughs/issues/957) the engine refuses all eleven **by name, as
unsupported, at decode** — it models none of them.

### What the engine does today

```
burroughs: feature not implemented in this phase: component: canon 0: canon thread.index (0x26)
is a shared-everything-threads built-in, which this engine does not support — the component is
well-formed and no gate admits this proposal
exit 5
```

Byte-identical with `BURROUGHS_ASYNC=0`, because no gate is involved. Before #957 the same component got
**exit 1** with *"canon built-in 0x26 is not modeled"* — a well-formed artefact reported as a malformed
one.

### Fixtures are authorable today, in WAT, with no Rust toolchain

Measured with `wasm-tools` 1.258.0, whose text format knows the whole family:

| fixture | bytes | form |
|---|---|---|
| `thread.index` | 38 | `(component (canon thread.index (core func $ti)))` — committed as `thread-index-synth` |
| `thread.spawn-ref` | 55 | `(canon thread.spawn-ref (core type $ft) (core func $sp))`; `(type $ft)` is refused |
| all eight phase-1 forms | 1044 | `cmthreads/switch.wat` — the oracle fixture below |

One spelling discrepancy, recorded and **not** resolved in the context: `wasm-tools` 1.258.0 spells it
`thread.available_parallelism` (underscore); `internal/component/testdata/canon-builtins.tsv`, generated
from `Binary.md` at `CANON_PIN = 2bed77e4228841c1d2721996d3ecc169ff96b158`, records
`thread.available-parallelism` (hyphen). The Decision below settles which Burroughs follows.

### One juxtaposition in the refusal, which is Scott's and not the chair's

The printed line begins `burroughs: feature not implemented in this phase:` — the public `ErrUnsupported`
text — ahead of an inner message saying *no gate admits this proposal*. The two are compatible:
unsupported now, scheduled by this decision. The public sentinel's text is public API surface and is
Scott's; nothing here changes it.

### Nothing on the tracker covers this family

Derived from the issues API rather than a cached listing or `gh issue list --label`: of 153 open issues,
**15** name threads in their titles and **all 15 are core-wasm `gate:threads`** (contract §§2–5) work.
None is the component model's family.

### Two same-name hazards, both of which caught a wrong premise

Each one changed the answer, and the second was caught only by being asked for:

1. **The component model's `thread.spawn-ref` is not core-wasm `interp.Instance.spawn`.** The first
   derivation had the family depending on `gateThreads`, from the shared-memory refusal in
   `internal/interp/thread.go:Instance.spawn`. That is the *core* threads proposal's spawn. At the pin,
   `thread.spawn-ref` is *"an optimization, fusing a call to `thread.new_ref` … with a call to
   [`thread.resume-later`]"*, and its `shared` immediate is **optional** — `Binary.md`'s `sh?` production
   reads `0x00 => (absent) | 0x01 => shared`. **The family does not require a shared memory.** The premise
   is retracted.
2. **Contract §7 is a third proposal again.** S-1 reads *"The engine implements the **stack-switching
   proposal** (phase-tracking; §9 gates) with **growable continuation stacks**"*. That is core-wasm stack
   switching, not the component model's `Thread`.

### The 🧵② marker is ambiguous at the pin

`Explainer.md`'s marker legend calls 🧵② *"[shared-everything-threads]-based threading built-ins"*, which
reads as a dependency. Its own § 🧵② `thread.spawn-ref` says the fusion *"is more impactful once given
[shared-everything-threads] and thus gated on 🧵②"*, which reads as a priority. The optional `sh?`
immediate favours the prose. Both readings are at the same pin, and this record does not pick one; the
Decision below leaves phase 2 unmodelled, so the ambiguity needs no resolution yet.

---

## What the reference requires, per production

From `definitions.py` at `CANON_PIN`, vendored under a gitignored path at
`internal/component/canon/gen/vendor/`. **Eight of the eleven are defined there; three are not** — which
is why `canon-builtins.tsv` is generated from the binary grammar rather than from the reference
implementation, as the `canon-builtins` Makefile target already records.

| op | production | operand shape | what the reference requires | what Burroughs would have to provide |
|---|---|---|---|---|
| `0x26` | `thread.index` | none | `canon_thread_index` returns `current_thread().index`; traps unless `inst.may_leave` | a per-Thread index in a per-instance table |
| `0x27` | `thread.new-indirect` | `ft tbl` | `canon_thread_new_indirect` builds `Thread(task, thread_func)` from a table entry and registers it on the task; `assert(new_thread.suspended())` — **the new Thread starts suspended** | a goroutine created parked, its own `stack`, a TLS slot, and a task-side registry |
| `0x28` | `thread.resume-later` | none | `canon_thread_resume_later` → `Thread.resume_later` → `start_waiting(ready_func = lambda: True)`; traps unless the target is suspended | a ready set on the instance |
| `0x29` | `thread.suspend` | `0x00` | `canon_thread_suspend` → `Thread.suspend` → `block()` → `suspend(switch_to=None)` | a handoff that parks this goroutine and releases the core thread to another |
| `0x2a` | `thread.suspend-then-resume` | `0x00` | `canon_thread_suspend_then_resume`; `trap_if(not other_thread.suspended())` | the same handoff, naming its successor |
| `0x2b` | `thread.yield-then-resume` | `0x00` | `canon_thread_yield_then_resume`; same trap condition, and the switcher stays ready | as above, leaving self in the ready set |
| `0x2c` | `thread.suspend-then-promote` | `0x00` | see § Promote below | a ready set that is **inspectable and removable by identity** |
| `0x2d` | `thread.yield-then-promote` | `0x00` | see § Promote below | as above |
| `0x40` | `thread.spawn-ref` | `sh? ft` | **not defined at this pin.** `Explainer.md` § 🧵② `thread.spawn-ref`: a fusion of `thread.new_ref` and `thread.resume-later` | nothing: phase 2 is not modelled |
| `0x41` | `thread.spawn-indirect` | `sh? ft tbl` | **not defined at this pin.** `Explainer.md` § 🧵② `thread.spawn-indirect`: a fusion of `thread.new-indirect` and `thread.resume-later` | nothing: phase 2 is not modelled |
| `0x42` | `thread.available-parallelism` | `sh?` | **not defined at this pin.** `Explainer.md` § 🧵② `thread.available-parallelism`: *"returns the number of threads that can be expected to execute in parallel"* | nothing: phase 2 is not modelled |

**None of the eleven requires a shared linear memory.** The reference files all of this under
`## Concurrency → ### Stack Switching`, where `Thread.cont` is a `Continuation` and `cont_new`, `resume`,
`block` and `switch_to` are the primitives. The Python implementation emulates continuations with OS
threads and locks — one runs at a time, each `resume` releasing one lock and acquiring another — which is
an implementation detail of the model, not a parallelism claim.

### Promote

`Thread.suspend_then_promote` and `Thread.yield_then_promote` in the reference:

```python
def suspend_then_promote(self, other):      def yield_then_promote(self, other):
  if other.ready():                           if other.ready():
    other.stop_waiting()                        other.stop_waiting()
    self.suspend_then_resume(other)             self.yield_then_resume(other)
  else:                                       else:
    self.suspend()                              self.yield_()
```

**Promote differs from resume in two ways, both load-bearing.**

1. **The target's required state is the opposite one.** `suspend_then_resume` asserts
   `other.suspended()`, and `canon_thread_suspend_then_resume` carries
   `trap_if(not other_thread.suspended())`. Promote tests `other.ready()` — a Thread already in the
   waiting set with a satisfied `ready_func`, i.e. queued to run — and its canon wrapper
   (`canon_thread_suspend_then_promote`) carries only `trap_if(i == thread.index)`. So resume takes a
   Thread that is *not* queued and promote takes one that *is*: promote lifts it out of the ready set with
   `stop_waiting` and switches to it immediately, ahead of whatever the scheduler would have picked.
2. **Promote degrades instead of trapping.** A target that is not ready leaves promote doing a plain
   `suspend()` or `yield_()`, naming no successor. The `*_then_resume` forms trap in the corresponding
   case.

**What Burroughs would have to provide**: a ready set that can be *asked about a particular Thread* and
*removed from by identity*, not a FIFO queue — `other.ready()` is a membership test and `stop_waiting` is
a removal. The direct handoff is the same one `*_then_resume` needs.

---

## What Burroughs already provides

### Execution state is per call, not per instance

`internal/interp/thread.go:runEntry` allocates the operand stack per call — a `&stack{}` literal whose
`num` slice is sized from the function body — and `internal/interp/interp.go:Instance` holds only the
index spaces: memories, tables, globals, elements, data, funcs, tags. **So two goroutines can alternately
run frames against one instance, each owning its own operand stack, frames and depth.**

The engine states this itself, in the note above `internal/component/async_context.go:contextGet`: *"two
concurrent agents are two stacks but one shared thread object"*.

### Suspension mid-function exists, and the ungated path is the host call

**The ungated path is the host call.** It marks its thread blocked —
`internal/interp/safepoint.go:enterBlocked` and its `leaveBlocked` pair, wrapped around the call
([ADR 0069](0069-a-host-function-is-a-caller-and-a-value-slice-a-host-call-marks-its-thread-blocked-and-shutdown-is-its-own-terminal-method.md)),
so *"a thread parked in an embedder's `select`"* is counted by SP-1's predicate as stopped rather than as
running guest code. It is reached whenever a guest calls a host import, and needs no gate.

**The futex path is gated and is not available to a 🧵 component.**
`internal/interp/futex.go:memory.wait` uses the same `enterBlocked`/`leaveBlocked` pair and §3 SP-2 counts
a thread suspended there as at a safepoint, so it is evidence that the mechanism exists. But a shared
memory requires the core threads proposal — `internal/binary/gatemap.go:gateThreads`, whose default is off
and whose flip is unscheduled by Scott's #670 ruling.

### `context.get` / `context.set` are built, which is what makes shared globals workable

The reference's per-Thread thread-local storage is a fixed **2-element** array — `Thread.__init__` sets
`self.storage = [0,0]` — read and written by `context.get` (`0x0a`) and `context.set` (`0x0b`), both in
the 🔀 async family and **both built** in this engine
(`internal/component/link_component.go:isBuiltAsyncBuiltin`, and `canonStatus` marks them
`statusAsyncDemandedBuilt`).

`Concurrency.md` § Thread-Local Storage:

> Each thread contains a distinct mutable **thread-local storage** array. … This makes thread-local
> storage a natural place to store: 1. a pointer to the linear-memory "shadow stack" pointer 2. a pointer
> to a struct used by the runtime to implement the language's thread-local features
>
> … Since the same mutable thread-local storage cells are shared by all core wasm running under the same
> thread in the same component, the cells' contents must be carefully coordinated in the same way as
> native code has to carefully coordinate native ISA state (e.g., the FS or GS segment base address).

A toolchain keeps its per-Thread shadow-stack pointer in TLS because the instance's globals are shared.
Shared globals work only if those built-ins do, and they do.

**One thing has to move.** Burroughs' TLS placement is already split by execution shape: context lives on
the caller's stack for a sync call — *"per-agent by construction"* — and on the durable **lift task** when
an async callback lift is in flight, *"because that loop spans multiple stack-creating calls"* (the dated
disposition above `internal/component/async_context.go:contextGet`). A CM Thread needs a **third**
placement: per Thread. The precedent for moving it exists, and ADR 0050's guess about when it would move
(*"§7 growable continuations"*) is already recorded as having been wrong — the callback lift is what split
it.

### Every field of `interp`'s `thread` is in the shared class

Thirteen fields, enumerated with a parser (`go/ast` over `internal/interp`'s non-test files), all declared
on `internal/interp/thread.go:thread`. Classified against the reference's `Thread`: **shared** = one
instance's CM Threads must see the same value; **owned** = each CM Thread needs its own; **n/a** = the
concept does not cross over.

| # | field | class | why, with its citation |
|---|---|---|---|
| 1 | `thread.id` | shared | T-1's tid, *"assigned once at creation and never written again"*. A CM Thread's `index` is a **different** numbering — per-instance, in the model's `inst.threads` table — so it is new state, not this field |
| 2 | `thread.globals` | **shared** | §2 T-6 / [ADR 0089](0089-non-shared-globals-are-per-agent-because-a-guests-per-thread-state-is-its-whole-global-set-and-t-4-sized-it-at-one.md). The reference's CM Threads hang off one `inst` and share its core state; `context.get/set` exists because globals are shared |
| 3 | `thread.globalsOf` | shared | the key saying which instance those globals belong to |
| 4 | `thread.stopReq` | shared | §3's stop request, polled at every back-edge ([ADR 0059](0059-the-safepoint-poll-is-guarded-at-the-pc-assignment-because-a-back-edge-is-a-runtime-comparison-and-straight-line-code-pays-nothing.md)). A `Stop` is about the instance's execution, and only one CM Thread runs at a time, so one flag suffices |
| 5 | `thread.exitReq` | shared | §2 T-5.4's terminal mark, set by `Close` ([ADR 0071](0071-t-5-is-live-only-membership-a-bounded-status-record-a-fault-in-two-channels-and-a-sentinel-panic-for-the-terminal-unwind.md)). Ending the thread ends all its CM Threads |
| 6 | `thread.blocked` | shared, **already a count** | its own comment: *"A count and not a flag, because a `thread` is per instance and a caller is per call"*. A suspended CM Thread increments it |
| 7 | `thread.callers` | shared, already a count | SP-2's denominator; `Stop` asks `blocked == callers` ([ADR 0067](0067-a-caller-count-joins-the-blocked-mark-because-sp-2s-predicate-is-about-callers-and-a-thread-is-not-one.md)). `blocked <= callers` still holds by construction: a suspending CM Thread is inside a call that already incremented `callers` |
| 8 | `thread.parked` | shared, already a count | [ADR 0074](0074-stop-waits-on-sp-1s-own-predicate-over-the-caller-marks-because-an-arrival-is-a-caller-and-the-protocol-named-neither-end-of-it.md)'s round-scoped count, for field 6's reason *"one step further in"* |
| 9 | `thread.done` | shared | closes when the **core** thread terminated and is retired. A CM Thread's completion is the reference's `task.unregister_thread` — new state, not this |
| 10 | `thread.err` | shared | the core thread's terminal error. A CM Thread's trap is the *call's* error — § Trap transport below |
| 11 | `thread.w` | shared | membership, established in `internal/interp/safepoint.go:world.addLocked` |
| 12 | `thread.ctx` | shared | §5 H-3's cancellation channel, *"per thread and not per call, which is the ruled shape"* — Scott's sub-choice on the #647 review. A host call from any CM Thread should see the instance's cancellation |
| 13 | `thread.cancel` | shared | field 12's other half |

**No field falls outside a clean class, and none has to change.** What a CM Thread needs is state the
`thread` type does not have: its own goroutine and `stack` (already per call), its own TLS slot (a third
placement), its own `index` in a per-instance table, and its own ready/suspended scheduling state.

**So a CM Thread is a new pairing of the shared core `thread` with its own goroutine and stack.**

### Fuel and interrupt state: there is none

The engine has no fuel counter and no interrupt budget. Preemption is §3's cooperative safepoint poll on
`thread.stopReq`, which is field 4. There is a call-depth bound, and it is enforced on the per-call `stack`
rather than on the `thread`, so it is per CM Thread by construction for the same reason the operand stack
is.

---

## Trap transport across goroutines

### What the reference requires

A spawned Thread's body is wrapped: `canon_thread_new_indirect`'s `thread_func` calls
`call_and_trap_on_throw(f.callee, [c])`, which converts a `CoreWebAssemblyException` into `trap()`. A
`trap()` raises `Trap(BaseException)`, which unwinds the entire embedding. **So a trap in any Thread is a
trap of the call, wherever that Thread is in the switching graph.**

**The reference does not model instance poisoning.** `may_leave` is a reentrancy guard toggled around lift
and lower, not a trap flag, and nothing sets a "faulted" bit on the instance. What happens to the
instance after a trap is therefore not constrained by the reference.

### What Burroughs would have to provide

**A Go panic does not cross goroutines, and the engine's existing recover deliberately re-panics.**
`runEntry`'s single `defer` recovers, repairs the caller marks, and then re-panics anything that is not
the `threadTerminated` sentinel — ADR 0070's repair and
[ADR 0071](0071-t-5-is-live-only-membership-a-bounded-status-record-a-fault-in-two-channels-and-a-sentinel-panic-for-the-terminal-unwind.md)'s
terminal unwind, with `TestASpawnEntryPanicLeavesNoCallerCounted` as the witness that a foreign panic still
escapes. **On a non-entry Thread's goroutine that is the wrong behaviour for a trap**: the panic would
leave the engine rather than fail the call, because there is no frame above it belonging to the caller.

So each CM Thread's goroutine needs a recover that captures the trap as a value and **delivers** it to
whoever is waiting on the call. The engine's precedent is per-thread and transfers directly:
`thread.err` is written by the thread itself, `retire` copies it into `world.exited` **before**
`thread.done` closes, and that ordering is why *"a join cannot observe a finished thread with no status"*.
A CM Thread needs the same two-step at task scope — record, then signal.

**The siblings have to be unwound, not abandoned.** In the reference the computation is over, so the other
Threads simply never run again. In Burroughs they are parked goroutines, and a parked goroutine does not
disappear: it has to be ended. That is § Threads left suspended's mechanism, reached from the trap path as
well as the completion path.

**Instance poisoning: no, and the reference does not require it.** A core trap in this engine is an error
returned from the call and the instance stays usable; nothing marks it faulted. A 🧵 trap would be the
call's error on the same terms. The choice is the engine's, because the reference does not constrain it,
and it is recorded here so a later reader does not have to re-derive that the silence was deliberate.

**What wasmtime does on re-entry after a 🧵 trap is unmeasured.** `wasmtime run` instantiates afresh for
each `--invoke`, so re-entry is not observable through this harness. No fixture for it was built, and the
reading says so.

---

## Threads that outlive their task's resolution

**An earlier draft of this record had this backwards**, from the chair's own first reading carried into
the text: it said a task cannot complete while Threads are registered. It can, and post-resolution work
on other Threads is part of the model. The section is derived from the code below in the direction the
code actually constrains.

### The ordering constraint `unregister_thread` imposes, stated in its own direction

```python
def unregister_thread(self, thread):
  self.threads.remove(thread)
  if len(self.threads) == 0:
    trap_if(self.state != Task.State.RESOLVED)
```

The `trap_if` fires only when the count reaches **zero**. So the constraint is one-directional:
**resolution must come before the last Thread exits.** It says nothing about Threads still being
registered at resolution, and it forbids nothing about them.

`Task.return_` and `Task.cancel` bear that out — each calls `on_resolve` and sets
`state = RESOLVED`, and neither touches `self.threads` or `store.waiting`.

### Threads do outlive resolution, in both parked states

**The sync lift's own loop stops at resolution rather than at thread exit.** `canon_lift`'s tail:

```python
thread.resume()
if not ft.async_:
  while task.state != Task.State.RESOLVED:
    candidates = { t for t in inst.threads if t.ready() }
    trap_if(not candidates)
    random.choice(list(candidates)).resume()
return task.request_cancellation
```

The loop's condition is the task's state. It exits the moment the task resolves and returns, **with other
Threads still registered in `inst.threads`**.

**For an async lift the host's scheduler keeps running them.** `Store.tick` filters on readiness and
nothing else:

```python
def tick(self):
  candidates = { thread for thread in self.waiting if thread.ready() }
  if candidates:
    thread = random.choice(list(candidates))
    thread.resume()
```

There is no task-state term, so a **waiting** Thread stays schedulable after its task has resolved. And a
**bare-suspended** Thread — `ready_func is None`, not in `store.waiting` — can still be revived by any
live or waiting Thread that names it with `thread.resume-later` or a `*-then-resume` / `*-then-promote`
form. Neither parked state is terminated by resolution.

**So cancellation must not unwind.** `Task.request_cancellation` resumes the implicit Thread and the task
resolves; an explicit Thread that was waiting may then legitimately run. Unwinding at cancel would cut off
work the reference permits.

### Where the trap rule does apply, and it is the sync lift's loop

`trap_if(not candidates)` above is the code behind `Concurrency.md` § Blocking's *"there are no more
eligible ready threads (and the call traps)"*. It is inside `if not ft.async_`, so it is the **sync**
rule. (`canon_lower`'s sync arm separately asserts `ft.async_ or subtask.state == RETURNED`, which is the
caller-side fact; the lift's loop is the one that traps.) wasmtime renders it as `wasm trap: cannot block
a synchronous task before returning`, measured on a sync fixture in this slice.

For an **async** callee there is no such loop and no such trap: with no ready candidate, `tick` does
nothing and the task simply does not progress.

### Which parked goroutines can never run again, and the bound the engine takes

A bare-suspended Thread is dead only once **no live or waiting Thread can name it**. That is a
reachability question over a set that any still-running Thread can change, and the engine cannot decide it
cheaply. **No reachability analysis is proposed.**

The bound instead: **unwind every Thread on a trap, and on `Instance.Close`.** A goroutine parked past its
task's resolution persists until `Close`, which is what the reference's Threads do — they stay in
`inst.threads` and `store.waiting`, schedulable, until the embedding goes away. The cost is a goroutine
per such Thread for the instance's remaining life; the alternative costs either a reachability analysis
or cutting off work the model permits.

**So two paths, not three.** The mechanism is `thread.exitReq` plus the `threadTerminated` sentinel panic
that `runEntry`'s recover converts into `ErrTerminated` — ADR 0071's terminal unwind, today driven by
`Instance.Close`:

1. a **trap**, on any Thread, which ends the computation for all of them (§ Trap transport);
2. **`Close`**, which already drives this path for core threads.

**Cancel is not one of them**: it resolves the task and leaves the Threads to the model.

### Post-resolution execution is unmeasured, and the control is why

A fixture was built to observe it and **its control falsified the discriminator**, so nothing is claimed.
The attempt: resolve with `task.return(7)` first, then create a Thread, make it ready with
`thread.resume-later`, and `thread.suspend` with nothing else able to resume the entry Thread — on the
theory that the call completing would prove the created Thread ran after resolution.

| arm | result |
|---|---|
| with the `resume-later` | `7`, exit 0 |
| **control**: `resume-later` deleted, so nothing can ever resume the entry Thread | `7`, exit 0 |

Both complete identically, so **the call's result is delivered at resolution** and anything after it is
invisible to `--invoke`. Neither fixture is committed: an artefact whose control does not discriminate
measures nothing. Recorded as unmeasured, on the same footing as re-entry after a trap.

---
## Consistency with the contract

**The clauses that govern threads in this engine are about T-1 agents, and a CM Thread is not one.**

- **T-1**: *"a thread-spawn host primitive … creating a wasm thread backed **1:1 by an OS thread**,
  sharing the module's shared linear memory."* A CM Thread is cooperative — only one runs at a time — so
  it cannot be a T-1 agent.
- **T-6**: *"A guest's non-shared global storage is per-agent. **Each agent created under T-1** has its
  own storage for every global the module *defines*."* The subject is agents created under T-1. **A CM
  Thread sharing the instance's globals therefore does not violate T-6**, because T-6 does not speak about
  it.
- **T-2**: *"There is **no main-thread special case**. … No agent is forbidden from sleeping."*
  Permissive; nothing here needs an exception.
- **§7 S-1**: the stack-switching proposal with growable continuation stacks, whose own note says *"each
  task runs on its own agent and the agent's goroutine is the continuation"* and whose #784 amendment says
  S-1 *"remains unconsumed until a guest async-lifts an export stackfully."* A goroutine-per-CM-Thread
  design consumes nothing of S-1 either: it captures no continuation.

**No contract amendment is needed.** The contract is silent on component-model Threads, and that silence
is what makes the shared-globals choice available without touching normative text. **If a future design
made CM Threads OS-backed, T-1 and T-6 would apply and an amendment would be needed** — contract text is
Scott's, and this record changes none.

---

## The differential oracle

**wasmtime 49.0.2 implements 🧵 behind `-W component-model-threading=y`, and executes all eight phase-1
forms.** The reading is committed at `internal/component/testdata/cmthreads/switch.reading`, its source at
`switch.wat`, and its build script at `build.sh`.

**One terminating fixture covers all eight.** The entry Thread records its own index, then creates five
Threads and reaches each through a different switching production; every body ORs one marker bit into
`mem[0]` **and writes its own `thread.index`** to its own slot. Both come back through `task.return`:

```
$ wasmtime run -W component-model-threading=y -W component-model-async=y --invoke 'run(0)' switch.wasm
143165567
exit 0
```

`143165567` is `0x0888887f`. Bits 0–5 are the marker mask, `0b111111`: `thread.index` (bit 1),
`suspend-then-resume` (2), `yield-then-resume` (4), `suspend-then-promote` (8), `yield-then-promote` (16)
and plain `suspend` (32), with `thread.new-indirect` exercised five times and `thread.resume-later` three.
**A body can only run if its switch happened**, so the mask is positive evidence per production rather
than a claim that the call did not trap. Identical across two runs.

**The six 4-bit index fields are what a reachability bit cannot give.** The entry Thread reads **1**, and
**every body reads 2** — because each body returns and unregisters before the next is created, so
`inst.threads` frees the slot and the next Thread reuses it. Thread numbering is observable guest
behaviour: an implementation that numbered monotonically would read 2, 3, 4, 5, 6 here and would set every
marker bit while being wrong.

**What the indices do not discriminate is the slot-reuse *policy*.** Exactly one slot is free at each
creation in this fixture, so LIFO, lowest-free and any other order all produce 2. The reference's policy,
quoted because the build slice has to implement it exactly:

```python
def __init__(self):            def add(self, e):                  def remove(self, i):
  self.array = [None]            if self.free:                      e = self.get(i)
  self.free = []                   i = self.free.pop()              self.array[i] = None
                                 else:                              self.free.append(i)
                                   i = len(self.array)              return e
                                   self.array.append(e)
                                 return i
```

`array = [None]` at construction, so **index 0 is reserved** and the first allocation is 1 — which is
where the entry Thread's 1 comes from. `free` is **LIFO**: `remove` appends and `add` pops the end, so a
reused index is the most recently freed one, not the lowest. The committed reading pins the 1 and the 2s
and **does not test the LIFO order**; a fixture holding two Threads live and freeing them out of order
would.

**What the reading pins, and what it deliberately does not.** Pinned: the mask and the six index values.
Not pinned: the **order** in which ready Threads run. `Store.tick` picks
`random.choice(list(candidates))` from the ready set, so an order is not a property of a conforming
implementation. The mask is an OR and each index is written by its own body, so the asserted value is
order-independent by construction — and a Burroughs witness built on this reading must assert those
values and must not assert an execution order.

**The two table slots are load-bearing.** The `yield-*` forms leave the switcher *ready*, and
`thread.resume-later` traps on a target that is not suspended — so a body reached by a `yield-*` switch
must not resume the switcher, while a body reached by a `suspend-*` switch must. Slot 0 marks only; slot 1
marks and resumes.

**With the flag off, wasmtime refuses by name at parse**, and this engine refuses the same production:

```
wasmtime: `thread.resume-later` requires the component model threading feature (at offset 0x47)   exit 1
burroughs: … canon thread.resume-later (0x28) is a shared-everything-threads built-in …           exit 5
```

**Three earlier fixtures did not work, and the causes were the fixtures'.** A callback returning YIELD
unconditionally hangs, because the event loop re-enters it forever. A callee that never calls
`task.return` traps with *"async-lifted export failed to produce a result"*. A `peek()` export cannot
observe a previous call's memory, because each `wasmtime run` instantiates afresh — which is why the mask
returns through `task.return`. All three are recorded in `build.sh` so they are not re-paid for.

**Phase 2 has no oracle and is not measured.** `definitions.py` does not define `0x40`–`0x42` at this pin.

---

## Decision

Chair's ruling, in session, 2026-10-11, under Scott's stated deferral. Five points.

1. **Model phase 1 — the eight forms `0x26`–`0x2d`. Do not model phase 2 (`0x40`–`0x42`).** The engine has
   what phase 1 needs: per-call execution state, a clean field table, built `context.get`/`context.set`,
   and an ungated suspension path. The proposal names Go green threads as a goal (`Concurrency.md`
   § Goals). Phase 2 stays refused by name: two of its three forms are not defined in the reference at the
   pin and the 🧵② marker is ambiguous, so it needs nothing further until the pin moves.
2. **Behind a new gate, `gate:cm-threads`, default off, which requires `gate:async` to be on.** Threads
   live inside tasks, so the dependency is real. **`gate:async` is already default-on** — its
   `asyncEnabled` reads `os.Getenv(asyncGateEnv) != "0"`, on since the 2026-09-18 flip (ADR 0086's
   amendment, #792), measured here with the variable unset — so a shared gate would put 🧵 semantics on by
   default with it. That is the argument for a separate gate rather than against it: `gate:cm-threads`
   defaults off and nothing in the async default carries thread semantics. wasmtime separates the two the
   same way (`component-model-threading`). With `gate:cm-threads` on and `gate:async` off, the engine
   refuses by name and says which gate is missing — and `BURROUGHS_ASYNC=0` is how that cell is reached,
   since the async gate is not off by default.

   *An earlier draft of this point read "a separate gate lets `gate:async` flip to default-on without
   carrying unmeasured thread semantics with it", which described the flip as still to come. It happened
   on 2026-09-18. The conclusion is unchanged and the premise is corrected rather than the sentence left
   standing.*
3. **The env spelling follows the existing component-model gates**, which are
   `componentsGateEnv = "BURROUGHS_COMPONENTS"` (ADR 0084) and `asyncGateEnv = "BURROUGHS_ASYNC"`
   (ADR 0086) — so `BURROUGHS_CM_THREADS`, declared as a const beside its check, not invented at a call
   site. `internal/binary/gatemap.go`'s `gateID`s are `Features` field names governing the core
   instruction stream and are not the mechanism for a component-model gate.
4. **Ordering: independent of core `gate:threads` and of §7.** Each form is built only once it has a
   wasmtime reading committed under `testdata`. Forms without an oracle keep refusing by name, even after
   their neighbours are built. All eight phase-1 forms have an oracle as of this record.
5. **Spelling: the pinned table's hyphen.** `wasm-tools` and the pinned table disagree; Burroughs follows
   the spec-facts oracle it already treats as authoritative, which is what
   `internal/component/threadbuiltins.go:threadBuiltinNames` already does.

**This acceptance holds only while the open cells come out clean.** They did: trap transport, the parked-
goroutine case and promote's definition are all answerable without changing the core `thread` type or the
contract. If a later slice finds otherwise, this record returns to **proposed** and the question goes back
to the chair.

---

## Consequences

- **The build order is fixed by point 4.** `thread.index` (`0x26`) first: it has an oracle, needs no
  switching, and exercises the per-instance Thread table and the gate end to end. Then
  `thread.new-indirect` with `thread.resume-later`, then the switching forms, each with its oracle. The
  per-Thread TLS placement lands with the first form that creates a second Thread.
- **The Thread table implements the reference's `Table` policy exactly as quoted**: index 0 reserved,
  LIFO free list, append when the free list is empty. The differential witness asserts the entry Thread's
  index of **1** from the committed reading — and not an execution order, which the reading does not pin.
- **The unwind is on two paths, trap and `Close`.** A goroutine parked past its task's resolution persists
  until `Close`, which is the bound this record chooses over a reachability analysis.
- **Phase 2's refusal is unchanged**, and `threadBuiltinNames` keeps all eleven names: the three phase-2
  entries are still what the by-name refusal prints.
- **Two of the actor's premises are retracted in the record above, with their cause**: the `gateThreads`
  dependency and the §7 dependency, both from a name shared across proposals.
- **The 🧵② legend-versus-prose tension stays unresolved**, and point 1 is why that is affordable.
- **If the family is later modelled with OS-backed Threads instead**, T-1 and T-6 apply and this record's
  contract reading does not carry: it rests specifically on a CM Thread *not* being a T-1 agent.
- **`starting.reading` and `cancel-wat.reading` do not cover Threads**, so nothing in the committed
  cancellation readings constrains what `subtask.cancel` must do to a suspended Thread. That reading does
  not exist yet, and `switch.reading` does not take it either.
- **Releases and version numbers remain Scott's.** Nothing here ships until he cuts a release.

---

## Amendment, 2026-10-11: the handoff, before the switching forms

Chair's ruling, in session, 2026-10-11: derive the handoff from the reference and write it down before
building any of `0x29`–`0x2d`. This amendment adds design; it reverses nothing above. **Revised the same
day, before anything was built**, on the chair's second ruling: a dedicated scheduler goroutine replaces
the first text's implicit-Thread-as-driver design, which is kept below as considered and rejected.

### Why the second slice's scheduler cannot carry the switching forms

`threadTable.runReady` (the second build slice) closes a Thread's `start` and waits on `<-t.done` — until
its goroutine **exits**. That is correct only while every Thread runs straight through. With `suspend` and
the switching forms, a Thread can give the slot back partway, and two deadlocks follow:

1. **A spawned Thread suspends partway.** `runReady` waits on a `done` that closes only after someone
   resumes that Thread — and the only goroutine that could is the one blocked in `runReady`. (The chair's
   finding.)
2. **The implicit Thread suspends partway, inside the callee.** `switch.wat` does exactly this: its callee
   calls `suspend-then-resume`, `yield-then-resume`, both `*-then-promote` forms and plain `suspend`.
   The implicit Thread is the lift loop's own goroutine, now blocked inside a canon call holding the entry
   slot, so **nothing is left to drive scheduling at all**.

### What the reference does

The unit the reference resumes is a **chain**, not a Thread. `Thread.resume`:

```python
thread = self
while thread is not None:
  cont = thread.cont
  thread.cont = None
  (thread.cont, switch_to) = resume(cont, thread)
  thread = switch_to
```

and every way of giving control up goes through one primitive, `suspend(switch_to)`, which hands the
resumer `(continuation, switch_to)`:

- `block()` is `suspend(switch_to=None)` — the chain ends and control returns to the resumer;
- `switch_to(other)` is `suspend(switch_to=other)` — the **same** resumer runs `other` next;
- a Thread whose function returns ends with `switch_to=None` too.

So **a resumer gets control back when the chain ends**, not when one Thread exits, and not when one Thread
suspends if that suspension names a successor. The resumers in the reference are the host's store
(`Store.tick`), `canon_lift` (which resumes the implicit Thread once), and `Task.request_cancellation`.

### The design

**1. One handoff primitive, on the Thread's own goroutine.** Every form that gives the slot up calls
`giveBack(next)` and then parks until resumed:

| form | `next` | the giver's state afterwards |
|---|---|---|
| `thread.suspend` (0x29) | none | suspended |
| `suspend-then-resume` (0x2a) | the named Thread (must be suspended) | suspended |
| `yield-then-resume` (0x2b) | the named Thread (must be suspended) | **ready** |
| `suspend-then-promote` (0x2c) | the named Thread **if ready**, removed from the ready set by identity; else none | suspended |
| `yield-then-promote` (0x2d) | as above | ready |
| the entry function returns | none | gone |

`promote`'s *"if ready, `stop_waiting`, then switch"* is a membership test and a removal by identity on the
ready set, which ADR 0100 already required; a FIFO slice with a scan meets it.

**2. One scheduler goroutine per instance drives every chain.** Chair's ruling, in session, 2026-10-11,
replacing the driver design recorded under *Considered and rejected* below. The **scheduler** runs
`resumeChain(first)`: wake `first`, wait for its `giveBack(next)`, and if `next` is set, wake `next`. That
is the reference's `while thread is not None` loop. It then pops the ready queue for the next chain. Its
properties are the reasons it was chosen:

- **Exactly one driver, and its identity never changes.** No Thread ever becomes the driver, so "who runs
  the next chain" has one answer on every path.
- **It holds `asyncHandles.entrySem` on behalf of the running chain.** The slot is never released and
  reacquired inside a chain, so a resumed Thread runs under the scheduler's hold rather than taking
  `entrySem` itself.
- **It never runs guest code.** It wakes goroutines and waits on them, and nothing else.
- **Created lazily**, on the first `thread.new-indirect` an instance executes, so an instance that never
  uses 🧵 pays nothing — no goroutine, no channel.

Every Thread, the implicit one included, gives control up the same way: `giveBack(next)` and park.

- **The implicit Thread suspending inside the callee** calls `giveBack(next)` and parks like any other.
  The scheduler runs the chain; a later chain that switches to it, or a `resume-later` on it, wakes it,
  and it returns to the guest.
- **The YIELD arm** (the callback loop, not in guest code) hands off to the scheduler and **waits for the
  chain to end**, replacing `runReady`'s "wait for the goroutine to exit". This is the reference's
  `wait_until` blocking and `Store.tick` picking.

So parked Threads come in two shapes only: **parked inside `giveBack`**, or **never started**. One wake
mechanism reaches both (point 4).

**Two refinements, found while building it and recorded here because each narrows a sentence above.**

- **The scheduler never acquires `entrySem` itself.** The ruling's *"holds `entrySem` on behalf of the
  running chain"* is kept in meaning. The slot is held by whoever starts the chain: the implicit Thread,
  which is inside its callee, or the YIELD arm, which takes it before handing off. A scheduler that
  acquired the slot could block on one held by an implicit Thread parked mid-callee, which only the
  scheduler can resume. This is the reference's shape too: `exclusive_thread` stays the implicit
  Thread's while it is suspended mid-callee, and a Thread a chain resumes never takes it.
- **Resuming an implicit Thread ends the scheduler's chain.** The implicit Thread returns to its own
  goroutine and may leave the callee for the callback loop without giving back again. A scheduler still
  waiting for its handoff would miss the YIELD arm's next request. So the scheduler resumes it and goes
  idle, and the implicit Thread's next handoff starts a new chain from idle, which the guest cannot tell
  apart.

**3. What the scheduler does when nothing is runnable.** It waits: on the ready queue, or on the wake.
Never on nothing. The reference's answer for a Thread waiting with nothing runnable depends on the
callee, and ADR 0100's two branches already say which. A **sync** callee traps
(`trap_if(not candidates)`), which was #970's subject and was built by its own amendment below. An
**async** callee simply does not progress.

**4. The wake reaches both parked shapes.** `killParked` currently ends only Threads that never started.
Neither shape is reached by `interp.ErrClosed` (needs a re-entry) or by `interp.ErrTerminated` (needs a
safepoint in guest code):

| parked shape | where its goroutine is | how the wake ends it |
|---|---|---|
| never started | the Thread goroutine's first `select` | exits without running |
| **parked inside `giveBack`** (spawned or implicit) | inside a canon call, mid-guest | the call returns a terminal error, the guest unwinds, the goroutine exits; for the implicit Thread, the lift returns it |

`Close` ends the scheduler goroutine **last**: after every Thread it could be waiting on has been woken
and has exited, so the scheduler is never left waiting on a `giveBack` that will not come. The second
shape is new and gets a witness with a neuter.

**5. `h.lift` is set by the scheduler.** A resumed Thread may belong to another task on the same
instance. So the scheduler sets `h.lift` to the resumed Thread's task when it wakes it, under
`asyncHandles.mu`, with the channel operations outside the lock (§4 B-MM-3, which the second slice's
`killParked` was already corrected against).

**6. Contract.** **The scheduler is not a T-1 agent and runs no guest code**: it is not created by
`spawn`, it holds no per-agent globals, and it never calls through `invokeIndex`. So T-1 (1:1 backing of
a spawned agent) and T-6 (per-agent global storage) do not apply to it. The Threads it wakes are what
those clauses govern, and they are unchanged.

**SP-2, confirmed by symbol, and the premise needed a correction.** The ruling's premise was *"a Thread
parked inside `giveBack` is inside a canon call, so `enterBlocked` already counts it"*. A 🧵 built-in is a
`CanonFunc`, which `interp.Instance.callAdapter` dispatches. **`callAdapter` does not call
`enterBlocked`** — its own comment says so, because an ABI lower's marshaling is brief and "never leaves
guest execution". Only three places call `thread.enterBlocked`:

- `interp.Instance.callHost` (embedder host functions);
- `interp.CanonCaller.Blocking`;
- futex `wait` (`memory.atomic.wait`).

So a Thread parked in `giveBack` is counted **only if the park itself runs inside `c.Blocking`**. That is
how `async_waitset.go`'s `waitable-set.wait` already parks. Without it, a `Stop` would wait forever on a
caller that is neither at a safepoint nor blocked. **The design therefore requires `giveBack`'s park to
run under `CanonCaller.Blocking`**, so that `enterBlocked` counts it and `leaveBlocked`'s poll is the
boundary SP-2 asks for on the way out.

- A never-started Thread is not a caller yet: it has not reached `invokeIndex`, so it adds no term.
- The scheduler is never a caller.
- The YIELD arm waits outside guest code, between core calls, so it is not a caller either.

**7. The goroutine census.** The scheduler is a new `go` statement. Its census entry in
`internal/testenv/observer_test.go` cites **this amendment**, beside the existing `threadNewIndirect`
entry that cites ADR 0100.

**8. The handoff's cost is measured on `switch.wasm` and quoted** by the slice that builds this. It is one
figure from the fixture that exercises all five forms, not a benchmark suite.

### Considered and rejected: the implicit Thread as driver

This amendment's first text had no scheduler goroutine. Whoever held `entrySem` drove the chain: the
YIELD arm, or the implicit Thread itself when it gave the slot up inside the callee, which would run
chains until one handed control back to it. **Rejected by the chair, 2026-10-11**, for the case it could
not express: **two implicit Threads from two tasks on one instance.**

Task 1's implicit Thread suspends inside its callee and becomes a driver, parked in its own driver loop.
Task 2's chain then reaches it: a switch to it, or a promote of it. The goroutine to be woken is a
*driver*, waiting for a chain to end, not a Thread parked in `giveBack`. So "wake the next Thread" has two
meanings depending on whose loop the target is in, and a chain can end inside a loop that belongs to a
different task.

With one scheduler whose identity never changes, every parked Thread is in the same place and is woken
the same way.

### Witnesses owed by the slice that builds this

- **A spawned Thread that suspends partway lets the yielding callback continue** — the first deadlock,
  neutered by reverting to "wait for exit".
- **A Thread suspended partway is reached by the `Close` wake** — neutered by restricting `killParked` to
  never-started Threads, which is the second slice's behaviour.
- **`switch.wasm` runs through Burroughs and returns `0x0888887f`**, the committed reading — the implicit
  Thread parking in `giveBack` on all five forms, and the slice's completion condition.
- **A Thread parked inside `giveBack` is at a safepoint** — every core instance `Stop`s while one is
  parked, neutered by parking outside `c.Blocking`. Added by the building slice: point 6's correction was
  an argument by symbol, and this makes it a measurement. Under the neuter the stop reports *"0 of 1
  callers at a safepoint (0 parked, 0 blocked)"*.

### What this does not decide

- **The sync lift's loop** (#970). A Thread inside a **sync** callee with nothing runnable traps, per the
  reference. This list left that to #970, and #970 built it: see *Amendment, 2026-10-12: the sync
  branch* below.
- **`promote` naming an implicit Thread that is waiting at its own YIELD arm.** In the reference that
  Thread is `waiting()` with a ready function, so `other.ready()` can be true and the promote switches to
  it. In Burroughs a YIELDing implicit Thread is not in the ready queue, so the promote falls to a plain
  suspend or yield. No reading measures it: a different task's Thread would have to run while that
  task's implicit Thread YIELDs, which no committed fixture constructs.
- **Cancellation delivered to a suspended Thread.** The reference schedules Threads to deliver it
  (`Concurrency.md` § Cancellation), and no reading measures it. Unchanged from ADR 0100's Consequences.

## Amendment, 2026-10-12: the sync branch (#970)

The chair's order on the #976 review: *"the design follows from the amendment: the scheduler and `giveBack`
already exist, and the sync branch's 'nothing runnable means trap' comes from `canon_lift`'s
`trap_if(not candidates)`."* The oracle came first. All three readings were taken with wasmtime 49.0.2
before the engine change:

| fixture | what it does | wasmtime |
|---|---|---|
| `sync-new` (#973) | creates a Thread from a sync lift and never resumes it | 2 |
| `sync-spawn` | creates and readies a Thread, then suspends; the body readies the caller back | 3, on 10 of 10 |
| `sync-trap` | creates a Thread it never readies, then suspends | traps, *"cannot block a synchronous task before returning"*, on 10 of 10 |

**The design, as built.**

1. **`new-indirect` and the switching forms accept a sync caller.** The caller is found the way
   `thread.index` finds it, through the index the stack carries. Each form refuses by name only where
   there is no component-model Thread at all: a core-level `Invoke` that bypasses the component.
2. **Threads are scoped by `owner`, not by `task`.** `owner` is the implicit Thread of the lift a Thread
   belongs to, and an implicit Thread is its own. A sync lift has no `liftTask`, and `killParked(nil)`
   already meant every Thread in the instance, so a trap in a sync lift's Thread would otherwise have
   ended every parked Thread of the instance.
3. **The trap is delivered by the scheduler**, in `cmScheduler.loop`: when a chain started by a **sync**
   lift's implicit Thread ends with nothing runnable (`drive`'s `idle`) and that Thread is still parked,
   it is claimed and sent the trap. An async origin in the same position keeps waiting, as point 3 of the
   handoff amendment records. The claim makes the trap and a late resume mutually exclusive.
4. **The sync lift's run slot carries the chain**, as ADR 0101 point 1 already provides: the sync lift
   takes the slot in `compFunc.invokeWith` and holds it through `giveBack`, so the Threads it readies run
   under its hold.

**One edge, recorded rather than closed.** The trap fires only when the sync origin's own chain ends
idle. If that chain instead ends by resuming a **different** task's implicit Thread, the sync origin stays
parked. The reference would re-enter its sync loop whenever control came back, and would trap if nothing
were ready then. No fixture constructs it, since it needs two tasks' Threads in one chain.

**Witnesses, each neutered and run.** The first neuter fails on the error, the second on the refusal, and
the third on its bound:

- `TestANewIndirectFromASyncLiftMatchesTheReading`: neutered by restoring the sync refusal in
  `threadNewIndirect`.
- `TestASyncLiftRunsASpawnedThreadBeforeItReturns`: neutered by restoring it in `threadSwitch`.
- `TestASyncLiftThatSuspendsWithNothingReadyTraps`: neutered by deleting the sync arm in
  `cmScheduler.loop`. `run` then hangs, as an async callee would.

## Amendment, 2026-10-13: the handoff carries the run slot (#978)

**Accepted on the chair's second review**, in session, 2026-10-13. The build has not started. The design
is one amendment covering this record and ADR 0101,
and it is written once, in [ADR 0101's Option A
amendment](0101-a-component-instance-executes-one-task-at-a-time-through-a-run-slot-released-only-where-the-reference-suspends.md#amendment-2026-10-13-option-a--the-run-slot-is-held-by-the-executing-thread-978).
What it would change here, once accepted:
- the handoff design's *"the slot is held by whoever starts the chain"*;
- *"a resumed Thread runs under the scheduler's hold"*;
- *"the scheduler never acquires `entrySem` itself"*.

Under it, every handoff carries the run slot and the scheduler holds it between Threads. The YIELD arm
takes neither the slot nor `entrySem`, and a wait is a handoff. Until A's build lands, the text above is
what the tree implements.
