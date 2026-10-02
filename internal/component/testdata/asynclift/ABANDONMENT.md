# Abandonment: dropping a host call future does **not** cancel a started task

Reference behaviour for [#857](https://github.com/scttfrdmn/burroughs/issues/857), captured on wasmtime 49.0.1.
**This is not a cancellation witness**, and it was built believing it was. Kept under its true name because it
is real behaviour, and because it is the reason a premise given to a principal failed.

## The readings

`abandonment.reading` — the receipt guest, round-tripped:

```
MODE      abandon
EVENTS    enter(1) -> suspend(1)
OUTCOME   abandon: host future dropped after guest arrival
ARRIVALS  1
RECEIPT   none (expected: the task was abandoned, not cancelled)
TICKDROP  false
```

`abandonment-stripped.reading` — the same guest with its single `task.cancel` call deleted
(`scripts/stripcall.py`) — is **byte-identical** to the above.

**The identical arms are the finding.** A deleted `task.cancel` call cannot matter in a run where nothing was
cancelled, so this pair discriminates nothing about `task.cancel`. Reported rather than smoothed over, per the
witness registration's own rule.

## A property of the witness design worth keeping: the pending host call is what makes a null result readable

The host's `tick` in this mode **never completes** — held pending with no bound. That was registered to remove
a race (a bounded `tick` could return before the drop took effect, letting the guest resume and call
`task.return`, so the arm would record a normal completion while claiming to be a cancellation arm). It earned
its keep in an unexpected way.

**With a bounded `tick` this run would have shown a normal completion**, and the reading would then have had to
distinguish *"cancelled, then resumed anyway"* from *"never cancelled"* — two very different facts with the
same surface. Held pending, the only possible exits were cancellation or nothing, so *nothing* is a verdict
rather than an ambiguity.

Generally: **when a witness may produce a null result, remove every path by which the subject could end for an
uninteresting reason.** Then the null is informative. The same move as the rendezvous one level in — the
rendezvous made a second arrival the proof of concurrency; this makes cancellation the only way the call can
end.

## What the reading establishes

Three facts, all of which Burroughs' behaviour will be compared against:

1. **The task is not cancelled.** The guest's cancellation path never runs — `RECEIPT none`, from a `Drop`
   guard local to the guest's async body that calls a host import and is disarmed only on success.
2. **The pending host call is not dropped** — `TICKDROP false`, from a `Drop` guard on the host future itself.
   A host function held pending across an abandonment stays held.
3. **The call site returns** while the guest remains suspended. The host walks away; the task does not end.

## Why dropping the future is not cancellation

Three independent confirmations in wasmtime 49.0.1, found by building this and reading the source rather than
by trusting a comment:

| site | what it says |
|---|---|
| `TaskId::host_future_dropped` | cancels eagerly **only** in the `!already_lowered_parameters()` branch — a task that has not started. Otherwise sets `host_future_state = Dropped` and defers deletion until the task's threads finish. |
| `Event::Cancelled` | produced in exactly **one** place: inside `subtask_cancel`, the `0x06` built-in. |
| `subtask_cancel` | reachable only from `libcalls.rs` — the **guest** libcall path. No public host API reaches it. `HostFutureState::Dropped` is read only by `ready_to_delete()`. |

**So wasmtime 49 gives a host no way to cancel a task that has already started.** Cancellation exists in the
component model as the semantics of a *caller* cancelling its subtask, and only a guest can invoke it.

## How the wrong premise arose, which is the part worth keeping

`concurrent.rs:2404` reads *"Dropping a host `call_async` future which needs to cancel the task"*. That is
about **`call_async`** — the non-concurrent API — and about the pre-lowering case. It was generalised to
`call_concurrent` and to started tasks **without checking**, and then used as the justification in
[ADR 0085's amendment 1](../../../../docs/decisions/0085-the-public-component-api-surface-a-new-component-value-type-resource-handles-first-class-and-wit-typed-constructors.md),
where a principal relied on it. A comment read as a specification — the shape
[`docs/laws/errors-and-testimony.md`](../../../../docs/laws/errors-and-testimony.md) names, applied to
someone else's comment instead of our own.

The correction is appended to that amendment. **The decision it supports is unchanged**: Burroughs' `ctx`
cancellation will implement the component model's own cancellation semantics, with the host acting as the
caller — a stronger basis than "it matches wasmtime's host API", of which there is none.

## Burroughs is not required to reproduce abandonment

Go's `context` offers no way to walk away from a call *without* cancelling it, so the state wasmtime enters
here has no expressible analogue at the planned public surface. The reading is reference behaviour, not a
conformance target. What Burroughs must match is the **cancellation** reading, which needs a guest caller and
is slice 2b.

## What carries forward

- The **receipt guest** (`receipt/`) becomes 2b's **child** component, unchanged.
- **`scripts/stripcall.py`** is unchanged and still produces the two arms; in 2b they become arms of a witness
  where the deleted call can actually matter.
- The harness's `pending_tick` and the receipt **latch** carry over. The latch is load-bearing and was a
  repair: `Notify` stores no permit, so a receipt arriving while nobody awaits is lost — and the receipt
  arrives during engine work, when the caller is not waiting. The first version could not have observed a
  receipt even had one been sent.
- **`wit-bindgen` issues `subtask.cancel` when an in-flight async import's future is dropped**
  (`WaitableOperation`'s `Drop` calls `cancel()` → `in_progress_cancel` → the `0x06` intrinsic). So 2b's
  parent can be ordinary Rust; no hand-written WAT is needed.
