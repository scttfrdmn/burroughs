# Composed cancellation: what a caller cancelling its subtask actually does

[#862](https://github.com/scttfrdmn/burroughs/issues/862). Two parents cancel **the same child** — the
committed `receipt/` guest, unchanged — and the pair is one witness, not two experiments. This records the
facts, which parent each comes from, and the three questions the readings answer.

**Read [ABANDONMENT.md](ABANDONMENT.md) first if you are looking for the host-driven path.** That one
measured what happens when a host *drops a call future*, and the answer was that nothing is cancelled. This
is the other thing: a **caller** cancelling its **subtask**, which is the only cancellation the component
model has.

## The readings

```
cancel-wat.reading
MODE      cancel-wat
EVENTS    enter(1) -> suspend(1) -> lower-state(1) -> tick-dropped -> receipt(1) -> cancel-status(4)
OUTCOME   go() returned
ARRIVALS  1
RECEIPT   observed, code=1
TICKDROP  true
LOWERSTATE  1 (STARTED)
STATUS  4 (CANCELLED_BEFORE_RETURNED)

cancel-rust.reading
MODE      cancel-rust
EVENTS    enter(1) -> suspend(1) -> tick-dropped -> receipt(1)
OUTCOME   go() returned
ARRIVALS  1
RECEIPT   observed, code=1
TICKDROP  true
STATUS    none reported (this parent has no report channel)
```

## Coverage split: which fact comes from which parent

| fact | WAT parent | Rust parent | agree |
|---|---|---|---|
| the child was entered and suspended | `enter(1) -> suspend(1)` | `enter(1) -> suspend(1)` | **yes** |
| arrivals | 1 | 1 | **yes** |
| the child's receipt — cancellation **reached the guest** | `code=1` | `code=1` | **yes** |
| the child's pending host `tick` was **dropped** | `true` | `true` | **yes** |
| event order: `tick-dropped` **before** `receipt` | yes | yes | **yes** |
| the call returned rather than trapping | `go() returned` | `go() returned` | **yes** |
| the lower's returned state | `1` (STARTED) | — | n/a |
| **the status `subtask.cancel` returned** | **`4`** (CANCELLED_BEFORE_RETURNED) | — | n/a |

**The agreement is what licenses the status.** The WAT parent is a *synthetic* canceller: it issues
`subtask.cancel` by hand rather than through generated drop glue. Its status number is only worth something
if it behaves like a real canceller otherwise — and on **every fact the two share**, it does. If they had
disagreed on any shared fact, the status would describe something a real guest never does, and that
disagreement would be the finding instead.

**The Rust parent's only gap is the status**, for four measured reasons in `wit-bindgen-rt 0.44.0` — the
runtime `wit-bindgen 0.62.0` depends on, which is *not* 0.62:

1. the generated async import **awaits immediately** (`wit-bindgen-rust-0.61.1/src/interface.rs:1155`
   emits `_MySubtask{…}.call((args)).await`), so the guest never holds the `WaitableOperation` and cannot
   call `.cancel()` on it — the generated signature is `impl Future`, confirmed by the compiler;
2. cancellation therefore runs in `Drop`, which calls `cancel()` and **discards** its value
   (`waitable.rs:456`, a bare statement);
3. no `pub` item exposes the numeric status and `STATUS_*` is unexported;
4. even reachable, Rust maps `STATUS_STARTED_CANCELLED` (3) and `STATUS_RETURNED_CANCELLED` (4) to one
   `Ok(Err(()))` (`subtask.rs:176-203`).

So **issuing a cancellation and observing its result are different capabilities**, and `wit-bindgen` has
only the first. That is why there are two parents; it is the same reason `task-cancel-synth.wat` exists.

## Three questions these readings answer

### 1. May a cancelled task call an import? **Yes.**

Pre-registered on #857 with both outcomes fixed in advance, and this settles it: the receipt arrives,
`code=1`, in **both** parents. The child's `Drop` guard — a local in its async body, disarmed on success —
ran after cancellation and its `note` call was serviced.

**Contrast with abandonment**, where `RECEIPT none` was the expected and observed result: there the task
was never cancelled, so the guard never ran. The same guest, the same guard, the same harness latch, and
the opposite outcome — which is what makes the receipt a discriminator rather than an artefact.

### 2. What becomes of the child's pending host `tick`? **It is dropped.**

`TICKDROP true` in both. On the host-driven abandonment path it was **false** — the pending host call was
*not* dropped, because nothing was cancelled. So this is a second place where real cancellation and
abandonment differ observably, and Burroughs has to match the cancellation behaviour rather than the
abandonment one it measured first.

**The order is part of the finding:** `tick-dropped` appears **before** `receipt`. The engine tears down
the in-flight host call first, and the guest's cancellation path runs after. An implementation that ran the
guest's drop glue first and then dropped the host call would produce the same two facts in the wrong order,
which is why the order is recorded and not just the pair.

### 3. What status does `subtask.cancel` return? **4, `CANCELLED_BEFORE_RETURNED`.**

With `LOWERSTATE 1` (STARTED) recorded immediately before it, so the reading says **which case** it is a
status of: the child had started and had not returned. A status without the state beside it could not
distinguish this from a mis-set-up run.

The constants match the model exactly (`definitions.py` `Subtask.State`): 0 STARTING, 1 STARTED,
2 RETURNED, 3 CANCELLED_BEFORE_STARTED, 4 CANCELLED_BEFORE_RETURNED.

**Status 3 is not reachable with this child, and that is stated rather than left as a gap.** The async
lower returns STARTED synchronously, because the child runs to its first `await` before yielding — so
there is no window in which the parent holds a subtask that has not started. Reaching 3 needs a child that
cannot start promptly (backpressure, or an instance already holding its exclusive thread), which is a
different fixture and a different slice. **What is measured here is 4; 3 is unmeasured, not assumed.**

## Ruling 1, settled: ONE sentinel

**The cancelled outcome's form, decided on these readings** (ruling: chat-Claude, relayed by Scott —
within what Scott already approved in ADR 0085 amendment 1, so not new public surface):

| `subtask.cancel` status | what `Component.Call` does |
|---|---|
| 3 `CANCELLED_BEFORE_STARTED` | returns **`ErrCancelled`**, checkable with `errors.Is` |
| 4 `CANCELLED_BEFORE_RETURNED` | returns **`ErrCancelled`**, the same value |
| 2 `RETURNED` — the call finished before the cancellation took effect | returns its **result normally**, because the work completed |

**Statuses with no Go equivalent: none.** Recorded as a claim rather than left implicit, so a later status
added to the ABI is visibly outside this mapping instead of silently absorbed by it.

**What Burroughs must match**, and these readings show 3 and 4 behave identically on every one of them:
the event delivered to the child; the import call **allowed** after cancellation; the pending host call
**dropped before** the guest's cancellation code runs; and `task.cancel` reached.

### The 3-versus-4 distinction is deferred, not discarded

It **does** matter to an embedder — *"did any of the call's work happen?"* decides whether a retry is safe
— and Burroughs can see it where a Rust guest cannot. **No consumer has asked for it**, so it is not
exposed now; exposing it would be new public surface, which is Scott's.

It stays addable without a break: an error **type** wrapping `ErrCancelled` and carrying whether the task
had started, so `errors.Is(err, ErrCancelled)` keeps answering. Recorded on
[#858](https://github.com/scttfrdmn/burroughs/issues/858) as an **option with its reason**, not a decision.

## The reference reading this mapping does not have

**Status 3 is unmeasured on the reference side**, and Burroughs will produce it itself — any context
cancelled before the task starts is a 3. So the mapping's first row is, today, asserted rather than
observed against wasmtime. [#884](https://github.com/scttfrdmn/burroughs/issues/884) registers a child
that **cannot start promptly** (held back by backpressure, or an instance already holding its exclusive
thread) so 3 gets a reference reading; **Burroughs' cancelled-before-start path waits on it**, because the
four behavioural facts below are measured for 4 and *assumed* for 3, and the two need not agree. It does
not block 2b, whose subject is 4.

### The question the ruling above answered, kept for its reasoning

**The ABI distinguishes "cancelled before started" from "cancelled before returned". `wit-bindgen` does
not.** Burroughs' engine implements `subtask.cancel` and therefore sees the numeric status, so it *can*
expose a distinction a Rust guest cannot — which is what made the question real rather than academic, and
what the option recorded on #858 preserves.

The three candidates put to review were: one sentinel, matching what a Rust canceller observes and
discarding a distinction the ABI makes; two sentinels or one with a detail field, exposing it and asking an
embedder to handle a case `wit-bindgen` guests never see; and in every case **only 4 is measured here**.

**One sentinel was chosen** — see the mapping above. Kept rather than deleted because the reasoning is
what makes the deferral reviewable: a reader who later wants the distinction needs to know it was weighed
and why it waits, not just that it is absent.

## Why the Rust parent's export is `async`, which cost a build

A first version exported `go` as a **sync** func and drove the child with a manual
`Future::poll` and `Waker::noop()`. It started the child — the harness saw `enter(1) -> suspend(1)` — and
then **panicked** inside `poll_complete_with_code` at drop time (`unreachable!()`, frames 9-10 of the
guest backtrace).

The cause is the export's shape rather than the manual poll: an async import's waker and task bookkeeping
is established by `start_task`, which only an **async** export sets up, so driving one from a sync lift
leaves the operation in a state its own cancel path treats as impossible. The export is `async func()` and
the race is `select_biased!` with the child arm **first** — ordering that matters, because with the ready
arm first the child is never polled, never starts, and the reading would be of `start_cancelled` instead.

`futures` is a **direct** dependency of that parent: the runtime re-exports it
(`wit-bindgen-rt-0.44.0/src/async_support.rs:53`) but `wit-bindgen 0.62.0`'s own `async`-gated re-export
list does not include it, so the path a guest would guess does not resolve.
