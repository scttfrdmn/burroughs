# 0105 — A trap locks the component instance, and every later call is refused with the trap, because the pin locks the store

Date: 2026-10-20 · Status: **accepted** — the chair's third ruling of 2026-10-20, in session (*"#1040's repair design is accepted in engine design, and goes to Scott as one package, decided unless he objects"*). Scott's package is on [#1040](https://github.com/scttfrdmn/burroughs/issues/1040), decided unless he objects: no objection as of 2026-10-20. `Ratio-Class: carried`.

**Supersedes** [ADR 0100](0100-phase-1-of-the-shared-everything-threads-family-is-modelled-behind-gate-cm-threads-each-form-landing-only-once-it-has-a-committed-oracle.md)'s section *"Instance poisoning: no, and the reference does not require it"*, and the paragraph before it that reads *"The reference does not model instance poisoning"*. The rest of ADR 0100 stands. **Amends** [ADR 0101](0101-a-component-instance-executes-one-task-at-a-time-through-a-run-slot-released-only-where-the-reference-suspends.md)'s Option A for the moment after a trap, in a dated section there.

## Context

After a component trapped, Burroughs ran the next call on whatever state the trap left behind. Both oracles refuse it (#1040, readings recorded there):

| | call 1 (traps) | call 2 on the same instance |
|---|---|---|
| wasmtime 49.0.3 (a two-call host on the crate, `testdata/cmthreads/twocall/`) | the trap | refused: *"cannot enter component instance"* |
| the pin 2e7895c (`refread.py` `trap_then_call`) | the trap | refused: the store is locked |
| Burroughs, before this record | the trap | **ran, and returned 7** |

It held **with no Threads at all** (`trap-then-call-plain.wasm`), so the defect was the whole default path's, gate on or off.

**ADR 0100's premise went false when the pin moved.** It said the reference *"does not model instance poisoning"*. That was true at 2bed77e, which defines no `lock_down`. It is false at 2e7895c, the pin since 2026-10-17:
- the trap locks the store first, `def@2e7895c:trap:"current_instance().store.lock_down()"`;
- every later call is refused, `def@2e7895c:Store.invoke:"assert(not self.lockdown)"`;
- nothing else in the store runs, `def@2e7895c:Store.tick:"assert(not self.lockdown)"`.

Nothing caught the change, because a negative claim names no symbol to resolve. That repair is #1083 (`docs/upstream/negative-claims.tsv`). Under [ADR 0102](0102-the-pinned-reference-defines-the-semantics-and-a-toolchains-stricter-refusal-is-followed-unless-burroughs-implements-and-witnesses-what-the-pin-admits.md), the pin defines the semantics, so the reference's behaviour is the answer.

**What the pin does with work in flight.** A Thread's trap reaches only its resumer chain (`def@2e7895c:resume:"raise handler.result"`). Every other parked Thread is never resumed, because the only scheduler entry asserts the lock, and cancellation asserts it too. **So the pin leaves an in-flight call unresolved**: it neither returns nor traps.

## Options

- **A. Lock per `Instantiated`, refuse at the embedder entry, end in-flight work where it next suspends or calls a built-in.** This is the pin's lock, plus a rendering of "unresolved" that a host can wait on.
- **B. Refuse only the next call.** This leaves in-flight tasks running on a store the reference has stopped, which is the half of the defect a second call does not show.
- **C. Tear the instance down at the trap** (close every core instance). It ends everything, but it makes the entry check unobservable. A call after the lock would fail on a closed instance, so the check's neuter could not fail, and a guess whose neuter cannot fail is not tested.

## Choice

**A**, as ruled.
- **Scope.** The lock is one `storeLock` per `Instantiated`, shared by the root walker, every composed child walker, and their Thread and async tables (`shareLock`). A child's built-ins see the parent's trap.
- **Set.** It is set by the first trap anywhere in the store:
  - in `routeFault`, before the owner learns of the trap, and for an orphan trap too;
  - at the embedder entry, when a call's own error is a trap (`*interp.Trap`, or `interp.ErrHostTrap`). Host-side refusals that ran no guest code are not traps.
- **Refuse.** Every embedder entry checks the lock first: `Call`, `CallRun`, `CallValues`, `CallValuesCtx`. `CancelAll` on a locked store does nothing.
- **The refusal** is `ErrInstanceLocked`, wrapping the trap: `errors.Is` reaches both the sentinel and the trapping call's error, and `errors.As` reaches the `*interp.Trap`. The public surface is `burroughs.ErrInstanceLocked`, mapped in `Component.Call`.
- **The trapping call** returns its own trap unchanged, which is wasmtime's call 1.
- **In flight.** A call in flight when the store locks ends with the same error:
  - at its next canon built-in or lowered import, through `lockGuard`, checked before the function runs and again after it returns, so a host call that returns into a locked store ends;
  - at a suspension point, through a lock case in the async waits;
  - or when it returns: started unlocked and ended locked.
- **A call that started locked** is the entry check's alone. It is never relabelled after it has run.
- **Core wasm is untouched.** An `interp.Instance` keeps the core spec's semantics, and stays callable after a trap.

## Consequences

- **What a host sees from `Call` after a trap changes.** It is refused with `ErrInstanceLocked` where it used to run. That is public API, and it is in Scott's package on #1040.
- **#1041 is answered.** An orphan trap (one whose owner's call had returned) locks the store, and the next call's refusal carries it. No separate surface is added.
- **Witnesses** (`internal/component/lockdown_test.go`, and `componentlockdown_test.go` at the public boundary):
  - both fixtures' call 2 is refused, wrapping call 1's trap, and stays refused;
  - the control returns 1, then 7;
  - a composed child's never-ending task ends at its next `context.get`, and its lift leaves flight;
  - a core module traps and stays callable;
  - a host call returning into a locked store ends with the wrapped error. That arm sets the lock directly, because its guest has no trapping export; it tests the guard's post-call check.
- **Neuters, run:**
  - the lock never set: call 2 returns 7;
  - the entry check removed: call 2 returns 7. This needed `finish` to pass a locked-start call through unmapped; without that, the neuter passed;
  - the lock scoped to one instance: the composed child stays in flight.
- **Two witnesses had depended on reuse after a trap, and were re-read.**
  - `TestAReallocThatCallsABuiltInTraps` gets one instance per arm: its `res` arm had begun passing on `arg`'s trap.
  - `TestConcurrentHostCallsShareOneThreadID` moved to an async-lift fixture. Its first caller had been trapping at the sync tail since #1005, and the second reached the import only because the instance kept running.
- **What this does not fix:** a component's trap reaches the public API as the engine's internal trap type, not `burroughs.Trap`, which predates this record. So a public embedder matches the lockdown's cause with `errors.Is` against call 1's error, not `errors.As` against `burroughs.Trap`.
