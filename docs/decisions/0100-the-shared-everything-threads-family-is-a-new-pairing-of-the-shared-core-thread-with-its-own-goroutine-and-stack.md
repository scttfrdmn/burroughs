# 0100 — The shared-everything-threads family is a new pairing of the shared core thread with its own goroutine and stack

Date: 2026-10-11 · Status: **proposed** — no stamp exists yet. Scott is asked to decide **whether to model
the 🧵 family, behind which gate, and in what order relative to the core `gate:threads` work.** Nothing in
this record has been built, and nothing will be until it is stamped. `Ratio-Class: carried` on the commit:
the order to draft it was given in session (chair's ruling, in session, 2026-10-10) and has no artifact to
cite.

---

## Context

The component model's 🧵 shared-everything-threads family is eleven canonical built-ins. Since
[#957](https://github.com/scttfrdmn/burroughs/issues/957) the engine refuses all eleven **by name, as
unsupported, at decode** — it models none of them. This record is the decision about whether that changes.

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

One spelling discrepancy, recorded and **not** resolved here: `wasm-tools` 1.258.0 spells it
`thread.available_parallelism` (underscore); `internal/component/testdata/canon-builtins.tsv`, generated
from `Binary.md` at `CANON_PIN = 2bed77e4228841c1d2721996d3ecc169ff96b158`, records
`thread.available-parallelism` (hyphen). `threadBuiltinNames` follows the pinned table, since that is the
spec-facts oracle this repo already treats as authoritative. Which spelling Burroughs follows elsewhere is
part of what this ADR asks.

### One juxtaposition in the refusal, recorded for Scott because the text is his

The printed line begins `burroughs: feature not implemented in this phase:` — the public `ErrUnsupported`
text — ahead of an inner message saying *no gate admits this proposal*. The two are compatible:
unsupported now, possibly scheduled by this decision. The public sentinel's text is public API surface and
is Scott's; nothing here proposes changing it.

### Nothing on the tracker covers this family

Derived from the issues API rather than a cached listing or `gh issue list --label`: of 153 open issues,
**15** name threads in their titles and **all 15 are core-wasm `gate:threads`** (contract §§2–5) work.
None is the component model's family.

### Two same-name hazards, both of which caught a wrong premise of mine

Recorded because each one changed the answer, and because the second was caught only by being asked for:

1. **The component model's `thread.spawn-ref` is not core-wasm `interp.Instance.spawn`.** I first derived
   that the family depends on `gateThreads`, from `spawn`'s refusal when the instance reaches no shared
   memory (`internal/interp/thread.go:600`). That is the *core* threads proposal's spawn. At the pin,
   `thread.spawn-ref` is *"an optimization, fusing a call to `thread.new_ref` … with a call to
   [`thread.resume-later`]"*, and its `shared` immediate is **optional** —
   `sh? ::= 0x00 => (absent) | 0x01 => shared` (`Binary.md:348`). **The family does not require a shared
   memory.** The premise is retracted.
2. **Contract §7 is a third proposal again.** S-1 reads *"The engine implements the **stack-switching
   proposal** (phase-tracking; §9 gates) with **growable continuation stacks**"*. That is core-wasm stack
   switching, not the component model's `Thread`.

### The 🧵② marker is ambiguous at the pin, and that is left unresolved

The legend calls 🧵② *"[shared-everything-threads]-based threading built-ins"* (`Explainer.md:73`), which
reads as a dependency. The per-production prose says the fusion *"is more impactful once given
[shared-everything-threads] and thus gated on 🧵②"* (`Explainer.md:2307`), which reads as a priority. The
optional `sh?` immediate favours the prose. **Both readings are at the same pin and this record does not
pick one.** It matters only for the three 🧵② forms.

---

## What the reference requires, per production

From `definitions.py` at `CANON_PIN`, vendored at
`internal/component/canon/gen/vendor/definitions.py`. **Eight of the eleven are defined there; three are
not** — which is why `canon-builtins.tsv` is generated from the binary grammar rather than from the
reference implementation, as the `canon-builtins` Makefile target already records.

| op | production | operand shape | what the reference requires | what Burroughs would have to provide |
|---|---|---|---|---|
| `0x26` | `thread.index` | none | `current_thread().index`; traps unless `inst.may_leave` (`definitions.py:2628`) | a per-Thread index in a per-instance table. **Measured as executable in the reference implementation** — see the differential below |
| `0x27` | `thread.new-indirect` | `ft tbl` | builds `Thread(task, thread_func)` from a table entry and registers it on the task; `assert(new_thread.suspended())` — **the new Thread starts suspended** (`:2641`) | a goroutine created parked, its own `stack`, a TLS slot, and a task-side registry |
| `0x28` | `thread.resume-later` | none | `other_thread.resume_later()` → `start_waiting(ready_func = lambda: True)` (`:2657`) | a ready queue on the instance |
| `0x29` | `thread.suspend` | `0x00` | `thread.suspend()` → `block()` → `suspend(switch_to=None)` (`:2668`) | a handoff that parks this goroutine and releases the core thread to another |
| `0x2a` | `thread.suspend-then-resume` | `0x00` | `thread.suspend_then_resume(other)`; traps unless `other` is suspended (`:2684`) | the same handoff, naming its successor |
| `0x2b` | `thread.yield-then-resume` | `0x00` | `thread.yield_then_resume(other)` (`:2695`) | as above, leaving self ready |
| `0x2c` | `thread.suspend-then-promote` | `0x00` | `thread.suspend_then_promote(other)`; traps if `i == thread.index` (`:2706`) | as above, plus whatever "promote" means for the engine's own scheduling |
| `0x2d` | `thread.yield-then-promote` | `0x00` | `thread.yield_then_promote(other)` (`:2716`) | as above |
| `0x40` | `thread.spawn-ref` | `sh? ft` | **not defined at this pin.** `Explainer.md:2300`: a fusion of `thread.new_ref` and `thread.resume-later` | `0x27` + `0x28`, given a `thread.new_ref` that does not exist at this pin |
| `0x41` | `thread.spawn-indirect` | `sh? ft tbl` | **not defined at this pin.** `Explainer.md:2315`: a fusion of `thread.new-indirect` and `thread.resume-later` | `0x27` + `0x28` |
| `0x42` | `thread.available-parallelism` | `sh?` | **not defined at this pin.** `Explainer.md:2330`: *"returns the number of threads that can be expected to execute in parallel"* | a number. No memory, no threads, no scheduling |

**None of the eleven requires a shared linear memory.** The reference files all of this under
`## Concurrency → ### Stack Switching`, where `Thread.cont` is a `Continuation` and `cont_new`/`resume`/
`block`/`switch_to` are the primitives (`definitions.py:213–262`). The Python implementation emulates
continuations with OS threads and locks — one runs at a time, each `resume` releasing one lock and
acquiring another — which is an implementation detail of the model, **not a parallelism claim**.

---

## What Burroughs already provides

### Execution state is per call, not per instance

`runEntry` allocates the operand stack per call: `st := &stack{t: t, num: make([]uint64, 0, max(stackHint,
len(fn.Body)))}` (`internal/interp/thread.go:751`). `Instance` (`internal/interp/interp.go:45`) holds only
the index spaces — memories, tables, globals, elements, data, funcs, tags. **So two goroutines can
alternately run frames against one instance, each owning its own operand stack, frames and depth.**

The engine states this itself, in the async context note: *"two concurrent agents are two stacks but one
shared thread object"* (`internal/component/async_context.go:12`). That is independent of the reading
above and agrees with it.

### Suspension mid-function exists, and the ungated path is the host call

**Lead citation, ungated:** a host call marks its thread blocked — `enterBlocked`/`leaveBlocked` around
the call ([ADR 0069](0069-a-host-function-is-a-caller-and-a-value-slice-a-host-call-marks-its-thread-blocked-and-shutdown-is-its-own-terminal-method.md),
`internal/interp/host.go:481`), so *"a thread parked in an embedder's `select`"* is counted by SP-1's
predicate as stopped rather than as running guest code. This path needs no gate: it is reached whenever a
guest calls a host import.

**Secondary, and gated:** `memory.atomic.wait` uses the same `enterBlocked`/`leaveBlocked` pair
(`internal/interp/futex.go:68`) and §3 SP-2 counts a thread suspended there as at a safepoint. It is
evidence about the mechanism and **not a path a 🧵 component can take**, because a shared memory requires
the core threads proposal — `gateThreads` (`internal/binary/gatemap.go:70`), whose default is off and
whose flip is unscheduled by Scott's #670 ruling. The distinction is drawn here because leading with the
futex path would smuggle `gate:threads` back into the dependency after the premise above was retracted.

### `context.get` / `context.set` are built, which is what makes shared globals workable

The reference's per-Thread thread-local storage is a fixed **2-element** array (`Thread.storage = [0,0]`,
`definitions.py:309`), read and written by `context.get` (`0x0a`) and `context.set` (`0x0b`) — both in the
🔀 async family and **both built** in this engine (`isBuiltAsyncBuiltin`, and `canonStatus` marks them
`statusAsyncDemandedBuilt`).

Why that matters is the reference's own argument, quoted because the chair asked for the citation rather
than the characterisation (`Concurrency.md:426`):

> Each thread contains a distinct mutable **thread-local storage** array. … This makes thread-local
> storage a natural place to store: 1. a pointer to the linear-memory "shadow stack" pointer 2. a pointer
> to a struct used by the runtime to implement the language's thread-local features
>
> … Since the same mutable thread-local storage cells are shared by all core wasm running under the same
> thread in the same component, the cells' contents must be carefully coordinated in the same way as
> native code has to carefully coordinate native ISA state (e.g., the FS or GS segment base address).

So a toolchain keeps its per-Thread shadow-stack pointer in TLS **because the instance's globals are
shared**. Shared globals only work if those built-ins do, and they do.

**One thing would have to move.** Burroughs' TLS placement is already split by execution shape, and the
note recording it says so: context lives on the caller's stack for a sync call — *"per-agent by
construction"* — and on the durable **lift task** when an async callback lift is in flight, *"because that
loop spans multiple stack-creating calls"* (`internal/component/async_context.go:10–29`). A CM Thread
would need a **third** placement: per Thread. The precedent for moving it exists, and ADR 0050's guess
about when it would move (*"§7 growable continuations"*) is already recorded as having been wrong — the
callback lift is what split it.

### Every field of `interp`'s `thread` is in the shared class

Thirteen fields, enumerated with a parser rather than by reading (`go/ast` over
`internal/interp/*.go`, non-test files). Classified against the reference's `Thread`: **shared** = one
instance's CM Threads must see the same value; **owned** = each CM Thread needs its own; **n/a** = the
concept does not cross over.

| # | field | class | why, with its citation |
|---|---|---|---|
| 1 | `id ThreadID` | shared | T-1's tid, *"assigned once at creation and never written again"* (`thread.go:103`). A CM Thread's `index` is a **different** numbering — per-instance, in the model's `inst.threads` table — so it is new state, not this field |
| 2 | `globals []*global` | **shared** | §2 T-6 / [ADR 0089](0089-non-shared-globals-are-per-agent-because-a-guests-per-thread-state-is-its-whole-global-set-and-t-4-sized-it-at-one.md). The reference's CM Threads hang off one `inst` and share its core state; `context.get/set` exists *because* globals are shared (quoted above) |
| 3 | `globalsOf *Instance` | shared | the key saying which instance those globals belong to (`thread.go:151`) |
| 4 | `stopReq atomic.Bool` | shared | §3's stop request, polled at every back-edge ([ADR 0059](0059-the-safepoint-poll-is-guarded-at-the-pc-assignment-because-a-back-edge-is-a-runtime-comparison-and-straight-line-code-pays-nothing.md)). A `Stop` is about the instance's execution, and only one CM Thread runs at a time, so one flag suffices |
| 5 | `exitReq atomic.Bool` | shared | §2 T-5.4's terminal mark, set by `Close` ([ADR 0071](0071-t-5-is-live-only-membership-a-bounded-status-record-a-fault-in-two-channels-and-a-sentinel-panic-for-the-terminal-unwind.md)). Ending the thread ends all its CM Threads |
| 6 | `blocked int` | shared, **already a count** | *"A count and not a flag, because a `thread` is per instance and a caller is per call"* (`thread.go:212`). A suspended CM Thread increments it |
| 7 | `callers int` | shared, already a count | SP-2's denominator; `Stop` asks `blocked == callers` ([ADR 0067](0067-a-caller-count-joins-the-blocked-mark-because-sp-2s-predicate-is-about-callers-and-a-thread-is-not-one.md)). `blocked <= callers` still holds by construction: a suspending CM Thread is inside a call that already incremented `callers` |
| 8 | `parked int` | shared, already a count | [ADR 0074](0074-stop-waits-on-sp-1s-own-predicate-over-the-caller-marks-because-an-arrival-is-a-caller-and-the-protocol-named-neither-end-of-it.md)'s round-scoped count, for field 6's reason *"one step further in"* (`thread.go:272`) |
| 9 | `done chan struct{}` | shared | closes when the **core** thread terminated and is retired (`thread.go:289`). A CM Thread's completion is the reference's `task.unregister_thread` — new state, not this |
| 10 | `err error` | shared | the core thread's terminal error (`thread.go:302`). A CM Thread's trap is the *call's* trap — see below |
| 11 | `w *world` | shared | membership, established in `addLocked` (`thread.go:311`) |
| 12 | `ctx context.Context` | shared | §5 H-3's cancellation channel, *"per thread and not per call, which is the ruled shape"* — Scott's sub-choice on the #647 review (`thread.go:335`). A host call from any CM Thread should see the instance's cancellation |
| 13 | `cancel context.CancelFunc` | shared | field 12's other half |

**No field falls outside a clean class, and none would have to change.** What a CM Thread needs is state
the `thread` type does not have: its own goroutine and `stack` (already per call), its own TLS slot (a
third placement), its own `index` in a per-instance table, and its own ready/suspended scheduling state.

**So a CM Thread is a new pairing of the shared core `thread` with its own goroutine and stack** — which
is this record's title and the whole of its technical claim.

### Fuel and interrupt state: there is none to classify

Named because the chair's list asked for it: the engine has no fuel counter and no interrupt budget.
Preemption is §3's cooperative safepoint poll on `stopReq`, which is field 4. There is a call-depth bound,
and it is enforced on the per-call `stack` rather than on the `thread`, so it is per CM Thread by
construction for the same reason the operand stack is.

---

## Trap and cancellation propagation

### What the reference requires

A sync task that blocks traps, but **a suspended Thread does not block the task** (`Concurrency.md:524`):

> if the task has not yet returned a value and the callee's function type does *not* declare the `async`
> effect, then the task is not allowed to "block" and will trap if it ends up blocking. However, just
> because the *current thread* has suspended doesn't mean that the overall *task* is blocked: if there are
> any other threads in the callee's component instance that are in the "ready to run" state, progressing
> them may unblock returning a value … and so the Component Model repeatedly resumes threads that are
> ready to run until either the task returns a value or there are no more eligible ready threads (and the
> call traps).

So the engine would owe a **scheduler with a termination condition**: resume ready Threads until the task
returns or none are eligible, and trap the *call* in the second case. That is new behaviour, not a
re-use of anything in §3.

The proposal's own goals name this engine's language first (`Concurrency.md:44`): *"green threads
scheduled by the language's own runtime in languages like Go"*.

### Cancellation, including `subtask.cancel` against suspended Threads

The reference delivers cancellation through the same cooperative scheduling (`Concurrency.md:807`):
*"cooperative `thread.yield` that allows the host to nondeterministically schedule any cooperative threads
it wants in the hopes of delivering the cancellation"*. So `subtask.cancel` on a task with suspended
Threads is not a direct kill: it needs those Threads scheduled before the cancellation can be observed.

What Burroughs has to compare against: `subtask.cancel`'s status values, measured against wasmtime and
committed — status 4 in `cancel-wat.reading` and status 3 in `starting.reading`
([#884](https://github.com/scttfrdmn/burroughs/issues/884)). Neither reading involves a CM Thread, so
**what cancellation does to a suspended Thread is unmeasured** and would need its own reading before any
implementation claimed parity.

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
  task runs on its own agent and the agent's goroutine is the continuation"* and whose #784 amendment
  says S-1 *"remains unconsumed until a guest async-lifts an export stackfully."* A goroutine-per-CM-Thread
  design consumes nothing of S-1 either, for the same reason: it captures no continuation.

**So no contract amendment is proposed, and that is a claim worth stating rather than leaving implied:**
the contract is *silent* on component-model Threads, and silence is what makes the shared-globals choice
available without touching normative text. **If a future design made CM Threads OS-backed, T-1 and T-6
would apply and an amendment would be needed** — contract text is Scott's, and this record does not
change any.

---

## The differential oracle

**wasmtime 49.0.2 implements 🧵 behind `-W component-model-threading=y`, and executes at least three of
the eight phase-1 forms.** So an implementation here would have a reference to measure against, for those.

**With the flag off, it refuses by name at parse** — the same shape #957 gave this engine:

```
$ wasmtime run -W component-model-threading=n thread-index-synth.wasm
Error: failed to parse WebAssembly module
Caused by:
    `thread.index` requires the component model threading feature (at offset 0xb)
```

**`thread.index` executes.** A 168-byte component lifting a core func that calls it:

```
$ wasmtime run -W component-model-threading=y --invoke 'idx()' idx.wasm
1
exit 0
```

**`thread.new-indirect` and `thread.resume-later` execute**, and the run then trapped at
`thread.suspend` on a **sync** lift:

```
wasm trap: cannot block a synchronous task before returning
```

That is the reference's own rule, measured in the reference implementation — `Concurrency.md:524`, quoted
above. It is a semantic constraint on sync tasks, not an unimplemented built-in.

**`thread.suspend` under an async lift is UNMEASURED, and the reason is my fixtures rather than
wasmtime.** Stated plainly because an unmeasured cell presented as a finding is worse than a gap:

- the first attempt hung, because its callback returned YIELD unconditionally so the task never exited —
  the same defect #884's negative control has, and mine here;
- removing the suspend produced `wasm trap: async-lifted export failed to produce a result`, because the
  callee never called `task.return`;
- a `peek()` export could never have worked, because each `wasmtime run` instantiates afresh, so it cannot
  observe the previous call's memory.

A fixture that terminates must have the spawned Thread resume the suspender, which means passing the
suspender's index through TLS or memory. **This slice did not build it**, and the ADR records the cell as
open rather than guessing at it.

**What this bears on, which is the reason the chair asked:** for `thread.index`, `thread.new-indirect` and
`thread.resume-later` a differential check is available today. For the five suspend/resume/promote forms,
**no differential check has been demonstrated**, and establishing one is a prerequisite rather than a
detail — an implementation of cooperative switching with no oracle is the position this project refuses
elsewhere.

---

## Decision

**For Scott.** Three questions, and the third is the one the material above actually bears on.

1. **Model the family, or keep refusing it by name?** Refusing is correct today and costs nothing: the
   refusal names the production, the proposal and its own stage, and `#957`'s witness holds it.
2. **If modelled, behind which gate?** `gate:threads` is the core-wasm proposal and does not cover this
   family, so the options are a new gate (`gate:cm-threads` or similar) or an extension of `gate:async` —
   which already owns `thread.yield` (`0x0c`), the one 🔀-marked member of the thread vocabulary, and
   already owns `context.get/set`, on which the whole design depends. A new gate would need ADR 0093's
   speculative-gate class if it ran ahead of the spec; this family is not ahead of the spec, since
   wasmtime ships it.
3. **In what order, relative to the core `gate:threads` work?** The material says **the family does not
   depend on it** — not on shared memory, not on §7 stack switching, not on `gate:threads`. The ordering
   is therefore free, and the real constraint is the differential gap in §The differential oracle: three
   forms have an oracle and five do not.

**What the actor recommends, so this is a decision and not a survey:** do not model it yet, and not
because of the engine. Everything the design needs is present — per-call execution state, an ungated
suspension path, `context.get/set`, and a clean field classification with no contract amendment. What is
missing is an oracle for the five switching forms, and *that* is the thing to buy first if the family is
wanted. A slice that builds a terminating `suspend`/`resume-later` fixture and takes its wasmtime reading
would cost little and would convert question 3 from an estimate into a measurement.

---

## Consequences

- **Nothing is built.** The eleven productions keep refusing by name at decode, which is #957's state.
- **Two of the actor's premises are retracted in the record above, with their causes**: the
  `gateThreads` dependency (two same-named spawns) and the v2/§7 dependency (a third same-named proposal).
  Both are left visible rather than deleted, because the shape — *a name shared across proposals* — is the
  one that produced both.
- **The 🧵② legend-versus-prose tension is unresolved** and affects only `0x40`–`0x42`. If this ADR is
  stamped for phase 1 only, the tension need not be resolved at all.
- **The spelling question is live but small**: `threadBuiltinNames` follows the pinned table today, and a
  decision to follow `wasm-tools` would change one map entry and its control.
- **If the family is later modelled with OS-backed Threads instead**, T-1 and T-6 apply and this record's
  contract reading does not carry: it rests specifically on a CM Thread *not* being a T-1 agent.
- **`starting.reading` and `cancel-wat.reading` do not cover Threads**, so nothing in the committed
  cancellation readings constrains what `subtask.cancel` must do to a suspended Thread. That reading does
  not exist yet.
