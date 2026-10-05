# 0094 — Host cancellation reaches a lift task through an internal trigger this slice, because the model hands the embedder a per-call `OnCancel` and `Invoke` blocks

Date: 2026-10-04 · Status: **accepted** · [#887](https://github.com/scttfrdmn/burroughs/issues/887) · Sets the `cancelled` field [ADR 0092](0092-the-async-lift-abi-is-stackless-callback-because-the-first-guest-that-lifts-async-chose-it-and-the-demand-set-is-read-from-that-guest.md)'s addendum left unset · **Amendment 1 below: the public surface was already decided and this ADR wrongly presented it as open**
Ratio-Class: carried

**The mechanism is decided here under the #647 narrowing** — escalation is owed for *"contract (§) text, public API surface, reversing a stamped ADR"*, and the pending-cancel state, the `TASK_CANCELLED` delivery and what `task.cancel` may now do are none of those. The **host entry point** is public API surface, and it was **already decided** by [ADR 0085 amendment 1](0085-the-public-component-api-surface-a-new-component-value-type-resource-handles-first-class-and-wit-typed-constructors.md#amendment-1-2026-10-02--componentcall-takes-a-contextcontext-and-cancelling-it-cancels-the-task); see amendment 1 below for the correction. What this slice builds is the internal trigger that decision's implementation will sit on.

## Amendment 1 (2026-10-04) — the public surface was decided, not open, and this ADR escalated a settled question

**The `Status:` line read *"accepted for the mechanism, open for the public surface"*, and the section below offered Scott three options.** Both were wrong: the surface was settled on 2026-10-02 by **ADR 0085 amendment 1, stamped by Scott** (*"I agree with the recommendation"*), which decides

```go
func (c *Component) Call(ctx context.Context, name string, args ...Value) ([]Value, error)
```

and that **cancelling the context cancels the in-flight task**. What this ADR called "option A" *is* that decision. So the surface is **decided and unimplemented**, which is a different state from undecided, and only the implementation is pending — it lands with ADR 0085's surface as amendment 1's own slice-3 (now [#858](https://github.com/scttfrdmn/burroughs/issues/858)).

**Declining the handle form was right, and the record already supported it** without my re-deriving it: amendment 1 item 4 declines a `Start`-returning-`*Task` form **for want of a consumer**, not permanently, and notes it stays addable on top. My "declined loudly, the phase's largest API decision for its smallest reason" reached the same place by argument where a citation was available.

**The lesson is the *what exists* rule applied to decisions rather than to code** (chair's words on the #894 review): *check the ADR record before escalating something as open.* I ran the measured recon on the model and on the engine, and did not run the equivalent pass over this project's own decisions — so I spent a principal's review slot on a question he had already answered, which is the same cost as a wrong option and harder to notice.

The original text of the status line and the options section is left below and struck in place rather than deleted, because what was believed is the part worth keeping.

**Amendment 1 does not change what this slice built.** The internal trigger is still the right mechanism and is still what the decided surface will drive; what changes is that it is implementing a decision rather than standing in for a missing one.

## Amendment 2 (2026-10-04) — the before-started arm is honoured, because this ADR's reason for refusing it is false of Burroughs

**This ADR refused `request_cancellation`'s INITIAL arm** with `ErrTaskCancelUnbuilt`, under "Scope, and what this slice must not claim":

> **Nothing about status 3.** The before-started path … has **no reference reading** … so the honest disposition is to **refuse that path by name** until #884 lands.

**The scope statement is right and the mechanism attached to it was wrong.** Nothing about status 3 should be claimed, and honouring the INITIAL arm does not claim anything about it — which this ADR did not check.

### Why the reason does not hold here

| | when is the lower's subtask STARTED? | so a lift task in INITIAL implies |
|---|---|---|
| **the model** | `canon_lift`'s `thread_func` calls `task.start()` → the lower's `on_start`, **inside** the lift (def:2097-2102) | subtask STARTING → `on_resolve(None)` picks **status 3** |
| **Burroughs** | the cross-component adapter calls `onStart()` **itself, synchronously, before the child's lift task exists** — it must, because `on_start` reads the caller's flat args and those belong to the caller's frame ([ADR 0095](0095-a-cross-component-async-call-is-a-siblings-lift-adapted-to-the-lower-side-the-substrate-already-wants-run-on-one-goroutine-per-call.md)) | subtask already STARTED → **status 4**, whatever the lift task's state |

So status 3 is unreachable from this path, and the refusal was protecting against a consequence Burroughs' own ordering had already removed. **The divergence is one ADR 0095 introduced deliberately and for a stated reason**, which is why this is a correction to the inference and not to either ADR's mechanism.

For the *host* trigger the question does not arise at all: there is no subtask, and `subtask.cancel` statuses are a guest-parent construct.

### And the refusal's cost had changed underneath it

This ADR reasoned that a dropped refusal was acceptable because the parent would learn of it through BLOCKED. **Grave #892 removed BLOCKED from the sync path** — the model waits there — so the refusal stopped being a reported outcome and became a **30s hang**. Measured as a *flaky* one: 150 runs of the composed witness could not all complete, where 200 now run in under three seconds.

*A gap whose consequence has changed needs re-deciding, not re-citing.* ADR 0095 cited this gap as "recorded"; the recording was accurate when written and stopped describing the behaviour one slice later.

### What changed, concretely

- `requestCancelLocked` honours INITIAL and STARTED alike, both setting PENDING_CANCEL — **which is what the model's own code does** (def:463-470); only the delivery carrier differs.
- `runLiftTask`'s INITIAL→STARTED transition is **guarded**, because an unconditional write would erase a cancellation recorded before it ran — the same hang one step later.
- `ErrTaskCancelUnbuilt` is **deleted**. This arm was its last producer, and a sentinel nothing returns is the defect ADR 0094 was itself cleaning up. Never public surface: `internal/component`.
- **#884 keeps its subject and loses this consumer.** Status 3 still has no reference reading and the mapping's first row is still asserted rather than observed. What it no longer blocks is the before-started cancel path.

Recorded by the actor the work reached, so no independent provenance; commits resting on it stay `Ratio-Class: carried`.

## Context — the field with nothing to set it

`liftTask.cancelled` has existed since [#864](https://github.com/scttfrdmn/burroughs/issues/864) with a comment saying so in those words: *"**Nothing sets it today**: Burroughs has no way to cancel a task, and the engine it is measured against has none either for a task that has already started."* `task.cancel` (0x05) checks it and, finding it false, traps; finding it true, returns `ErrTaskCancelUnbuilt`.

[#862](https://github.com/scttfrdmn/burroughs/issues/862) then measured the reference's behaviour **through composition**, because wasmtime gives a host no way to cancel a started task — `Event::Cancelled` has exactly one producer, the guest-reachable `subtask_cancel`. Two parents, one child, and four committed facts in [`cancel-wat.reading`](../../internal/component/testdata/asynclift/cancel-wat.reading) and [`cancel-rust.reading`](../../internal/component/testdata/asynclift/cancel-rust.reading).

**That limitation is the reference's, not a requirement on Burroughs.** In Burroughs the host *is* the caller, so this is cancellation of a **lift task** and needs no parent component — which is why #888's cross-component work is scheduled after this rather than before it. I had initially treated composition as a prerequisite; the chair corrected it, on the standing shape *a capability requirement can be a mechanism choice*.

## What the model does, read at `CANON_PIN = 2bed77e4228841c1d2721996d3ecc169ff96b158`

Fetched and read rather than recalled, because an accept-direction fact needs the authority and not a summary of it. The `Makefile`'s own pin, same revision `canon-fixtures` and `canon-builtins` read.

| piece | site |
|---|---|
| `Task.State`: INITIAL, STARTED, **PENDING_CANCEL**, **CANCEL_DELIVERED**, RESOLVED | `def:389-394` |
| `request_cancellation()` | `def:463-470` |
| `has_pending_cancel()` / `deliver_pending_cancel()` | `def:472-480` |
| `Task.cancel()` — traps unless CANCEL_DELIVERED, then resolves with `None` | `def:494-498` |
| `EventCode.TASK_CANCELLED = 6` | `def:703` |
| the callback lift loop | `def:2126-2151` (the two cancel checks at `def:2129` and `def:2137`) |
| `Store.invoke` → `OnCancel` | `def:510-518` |

### Two findings the registration's summary did not carry

**1. There are *two* `deliver_pending_cancel()` sites in the lift loop, and the YIELD arm is the one with a second.**

```python
while code != CallbackCode.EXIT:
  if thread.task.deliver_pending_cancel():          # def:2129 — before exclusivity is released
    event = (EventCode.TASK_CANCELLED, 0, 0)
  else:
    inst.exclusive_thread = None
    match code:
      case CallbackCode.YIELD:
        thread.wait_until(lambda: inst.exclusive_thread is None)
        if thread.task.deliver_pending_cancel():    # def:2137 — after the yield's wait
          event = (EventCode.TASK_CANCELLED, 0, 0)
        else:
          event = (EventCode.NONE, 0, 0)
      case CallbackCode.WAIT:
        event = wset.wait_from_callback()
```

The first check is **in place of waiting**: a cancel pending when the guest returns WAIT or YIELD is delivered instead, and **exclusivity is never released** on that path — the guest goes straight back in. The second catches a cancel that arrives *during* a yield.

### Amendment 1 — the WAIT arm's check exists, and this ADR's first draft said it did not

**The paragraph that stood here was wrong, and it was wrong in the direction that matters.** It read:

> **The WAIT arm has no second check**, so a cancel arriving while the guest is parked on a waitable set is not delivered by this loop at all; it is delivered on the next iteration's first check, after the set produces some event. That asymmetry is a property to reproduce, not a gap to improve on.

There is a third `deliver_pending_cancel`, and it is **inside the wait**: `WaitableSet.wait_from_callback` (def:781-792) has readiness *"has_pending_event() **or** task.has_pending_cancel()"* and, on waking, prefers the cancel —

```python
def wait_from_callback(self) -> EventTuple:
    def ready():
      return (thread.task.inst.exclusive_thread is None
              and (self.has_pending_event() or thread.task.has_pending_cancel()))
    thread.wait_until(ready)
    if thread.task.deliver_pending_cancel():
      return (EventCode.TASK_CANCELLED, 0, 0)
    else:
      return self.get_pending_event()
```

**The method of the error is the part worth recording.** I read `canon_lift`, found two checks, and inferred a third's absence from the function I had read — an inference that required `wait_from_callback` to be cancel-blind, which I never looked at. The model distributes the mechanism across two functions and `canon_lift` alone does not show it. *Don't derive a follow-up from your own reasoning* — read the site.

**And the consequence was not the one the wrong paragraph priced.** It said "delivered on the next iteration", i.e. a latency difference. It is a **hang**: the task a host cancels is characteristically parked on a set that *nothing will ever resolve* — that is usually why the host is cancelling — so there is no next iteration. A cancel-blind park makes cancellation unreachable in its main case. Measured as exactly that hang on #887's first run, and now as neuter 1 of the slice's falsification matrix.

So Burroughs puts the check where the model puts it: in `awaitEvent`, with the model's cancel-before-event preference, plus the top of the dispatch loop for the path that never parks.

**2. The model's host entry point is a per-call `OnCancel`, handed *out* by the lift.** `canon_lift` ends `return task.request_cancellation`, and the embedding API is explicit about what the embedder gets:

```python
def invoke(self, f: FuncInst, on_start: OnStart, on_resolve: OnResolve) -> OnCancel:
```

So in the model, invoking a function **returns** the way to cancel it. There is no instance-level cancel, and no context parameter.

### `request_cancellation`'s two arms, exactly

```python
def request_cancellation(self):
    if self.state == Task.State.INITIAL:
      self.state = Task.State.PENDING_CANCEL
      self.implicit_thread.resume()
      assert(self.state == Task.State.RESOLVED)
    else:
      assert(self.state == Task.State.STARTED)
      self.state = Task.State.PENDING_CANCEL
```

Two things worth stating because a summary loses both. **Both arms set PENDING_CANCEL** — "INITIAL resolves immediately" is the arm's *outcome*, reached by resuming the thread so that `enter_implicit_thread`'s backpressure path (`def:432`) delivers the cancel and calls `cancel()`. And the else arm **asserts** `state == STARTED`: requesting cancellation of a task that is RESOLVED, already PENDING_CANCEL, or CANCEL_DELIVERED is an assertion failure. The model therefore specifies **no behaviour** for a redundant or late request — it is the embedder's job not to make one.

## The structural mismatch: the model's shape cannot be copied

`Store.invoke` can return the cancel trigger because **it does not block**. The model drives threads cooperatively — `Store.tick()` resumes one ready thread per call — so `invoke` returns while the task is still running and the embedder holds `on_cancel` to use later.

**Burroughs' `Invoke` blocks until the task resolves.** A trigger returned by it would arrive when there is nothing left to cancel. So copying the reference's signature produces a value that is correct in type and useless in fact — the shape is downstream of a scheduling model Burroughs does not have, which is exactly the case where *a capability requirement is a mechanism choice* cuts the other way: here the reference's mechanism is the thing that cannot be lifted.

Go's idiomatic inward form of the same capability is `context.Context`: the trigger is passed *in* before the call blocks, instead of handed *out* after it would not have. **That is also what ADR 0085 amendment 1 decided**, which the section below failed to check — see amendment 1 at the top.

## ~~Options for the host entry point~~ — superseded, and kept as what was believed

**This section was written as a live question put to Scott. It was not one.** ADR 0085 amendment 1 had already decided the surface on 2026-10-02 with his stamp, and the right move was to cite it. Struck rather than deleted, per amendment 1 above; the text stands as the record of the error.

> **A. `context.Context` on the call path (#880's shape).** Go's form of the model's `OnCancel`, inverted because the call blocks. Cost: it changes an exported signature, so it is public API surface and Scott's; and #880 has its own acceptance that this slice must not pre-empt or duplicate.
>
> **B. A non-blocking `Invoke` variant returning a handle with `Cancel()`.** The closest structural copy of the model. Cost: it requires an async public API Burroughs does not have and has not designed — a far larger surface than cancellation, decided as a side effect of a cancellation slice. This is the option to decline loudly rather than quietly: it would be the biggest API decision in the phase, taken for the smallest reason.
>
> **C. An internal trigger this slice; the surface decided in #880.** The mechanism is built and witnessed against #862's readings; the trigger is unexported and test-reachable. Cost: `gate:async`'s cancellation is not embedder-reachable when this lands, and the slice must say so rather than implying a capability it withholds.

**What each option's fate actually is**, read against the stamped record rather than offered:

- **A is the decision**, already taken: `Component.Call(ctx, …)`, cancelling the context cancels the task. Not an option — the thing to implement.
- **B was already declined**, by amendment 1 item 4, **for want of a consumer** and with a note that it stays addable on top. My reasoning above reached the same conclusion independently, which is the mild version of the same error: a conclusion argued where a citation existed.
- **C is what this slice builds**, and it is correctly scoped — not as a stand-in for a missing decision, but as the internal mechanism the decided surface will drive.

## Choice

**C as the mechanism, implementing A as already decided.**

The surface's implementation lands with ADR 0085's surface, which amendment 1's own ordering section calls its slice 3 and which is now [#858](https://github.com/scttfrdmn/burroughs/issues/858). Building C first still makes that slice smaller — a trigger that works needs a caller — but the reason is sequencing, not an open question.

### A second decision, which is this ADR's own rather than the model's

**A cancel request against a task that is not STARTED is refused by name, not trapped and not silently ignored.** The model asserts, which specifies nothing, so Burroughs must choose and the choice should not be read out of the reference.

- **Not a trap**, because a trap blames the guest. A host that requests cancellation while the task resolves underneath it has raced a legitimate race, and nothing the guest did is wrong. Trapping would make the host's timing into the guest's fault — the shape [`errors-and-testimony.md`](../laws/errors-and-testimony.md) exists for.
- **Not silently ignored**, because then "I cancelled it" and "it finished first" are the same observation, and the host cannot tell which happened. That is the ignorable-return-value failure one level up.
- **So: refused, with the state in the message.** The request is not idempotent — a second one is refused the same way — which matches the model's assertion rather than inventing a laxer contract around it.

## Scope, and what this slice must not claim

**Status 4 only.** The before-started path (`request_cancellation`'s INITIAL arm, the model's route to status 3) has **no reference reading**; [#884](https://github.com/scttfrdmn/burroughs/issues/884) registers the child that would produce one. Burroughs will generate 3 the moment a cancel arrives before a task starts, so the honest disposition is to **refuse that path by name** until #884 lands — the same treatment `ErrTaskCancelUnbuilt` already gives an unmeasured branch, and for the same reason: *a negative claim buys a branch an exemption* only if something checks the exemption, and a named refusal is what checks it.

## Consequences

- `eventCode` gains `eventTaskCancelled = 6`, completing the enum's gap at 6.
- `liftTask` gains a pending-cancel state, so `cancelled` stops being a field nothing writes — and #864's comment saying nothing writes it is **repaired rather than left standing**, since it is the foreclosing-words shape.
- `task.cancel`'s cancelled branch stops returning `ErrTaskCancelUnbuilt` and resolves the task, so `ErrTaskCancelUnbuilt` loses its only producer on the delivered path. It stays for the paths still unbuilt.
- The caller of a cancelled lift gets `ErrCancelled`, per #862's ruling-1 mapping for status 4.
- **Two checks, with a witness each, because one of them had none.** `awaitEvent` carries the park's check (def:789) and the dispatch loop carries the top-of-loop one (def:2129). Neutering the loop check left the **whole component suite green**, including this slice's own end-to-end witness — which drives a `wit-bindgen` guest, and a `wit-bindgen` guest awaits an import and therefore returns WAIT, so the park's check is always what delivers. That is grave #885's shape one level up (two call sites, one witness), found by falsifying rather than reading, and the repair is a hand-authored fixture that **yields repeatedly and never parks** (`testdata/lift-cancel-yield-synth.wat`) so the loop check is the only path a cancellation has. Each neuter now fails exactly one witness.
- **The YIELD arm's second check (def:2137) is the one genuine divergence.** Burroughs' analog of its `wait_until` is `enterTask`'s semaphore acquire, and the loop does not re-check after it; such a cancel is delivered one callback entry later. That consequence really is latency rather than outcome, which is what the WAIT claim was wrongly asserted to be.
- **A host async impl's `onCancel` must resolve its subtask, or a conforming guest panics** — and this is a separate, filed gap rather than a property of this slice. The model's `canon_subtask_cancel` **waits** for resolution when the cancel is sync-lowered (`thread.wait_until(subtask.resolved)`, def:2427-2428) and returns BLOCKED only on the async form; Burroughs returns BLOCKED unconditionally. `wit-bindgen`'s drop glue is synchronous, cannot await, and meets BLOCKED in `in_progress_update`'s `other => panic!("unknown code {other:#x}")`. Measured as a bare `unreachable` with no receipt on this slice's first run. It is `subtask.cancel`'s arm — the parent's path — so it is out of scope here and filed with the citation.
- `gate:async` cancellation is **not embedder-reachable** when this lands. Said in the slice's report rather than implied — and the reason is that the decided surface (`Component.Call(ctx, …)`, ADR 0085 amendment 1) is **unimplemented**, which is a sequencing fact rather than an open design question. [#858](https://github.com/scttfrdmn/burroughs/issues/858) is where it becomes reachable.
