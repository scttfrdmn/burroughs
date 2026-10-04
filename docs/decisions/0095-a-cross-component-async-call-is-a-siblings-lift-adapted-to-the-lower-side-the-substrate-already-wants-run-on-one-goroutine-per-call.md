# 0095 — A cross-component async call is a sibling's lift adapted to the lower side the substrate already wants, run on one goroutine per call

Date: 2026-10-04 · Status: **accepted** · [#888](https://github.com/scttfrdmn/burroughs/issues/888) · Authorises the goroutine site `internal/component/cross_component.go` / `liftAsAsyncImpl` · Builds on [ADR 0094](0094-host-cancellation-reaches-a-lift-task-through-an-internal-trigger-this-slice-because-the-model-hands-the-embedder-a-per-call-oncancel-and-invoke-blocks.md)
Ratio-Class: carried

Decided in the slice under the #647 narrowing (*"Escalate only for: contract (§) text, public API surface, reversing a stamped ADR"*), and none applies: no contract clause changes, no embedder-visible surface changes — `Instantiated.CallValues` keeps its signature, and what changes is which callees the engine can reach behind it — and no stamped ADR is reversed.

**This ADR exists because a control demanded it, which is the intended order.** `TestEveryEngineGoroutineIsAtASiteADecisionAuthorises` refused the implementation: *"these non-test sites start a goroutine at no site a decision authorises … Adding the entry without the decision forges the authorisation."* Engine concurrency arrives one decided site at a time, and §4 of this document is the price of the second site.

Recorded by the actor the work reached, so no independent provenance; commits resting on it stay `Ratio-Class: carried`.

## Context — the capability, and what was actually missing

A composed component's parent calls its child: an async `canon lower` whose callee is **another component's async lift**. #862 produced the first such artefact and #888 recorded where it failed:

```
CallValues(go) -> component: link refused: import ::run is not provided (stub host)
```

That message reads as *"the composition did not wire the import"*, and it is not that. The import resolved fine, to the child's lift. **The lower could not express a non-host callee.**

### The measured recon, which changed the shape of the work

#888's own registration said its opening recon *"is not sufficient to design from"*. The pass it asked for found three things that between them made this a smaller slice than it looked.

**1. The lower already consumes exactly what a lift produces.** The model's `canon_lower` ends `subtask.on_cancel = callee(on_start, on_resolve)` (def:2226), where the callee is a `FuncInst = Callable[[OnStart, OnResolve], OnCancel]` (def:385) — and `Store.lift` produces precisely that from a core func (def:522-528). **Burroughs' `asyncLowerImpl` is already that type.** So the subtask protocol, the STARTING→STARTED transition, the cancel arm's terminal-state choice, the retptr lowering, the blocking arm's `[state | subtaski<<4]` — all unchanged. The work is **one adapter**.

**2. `coreDef` recorded only a host-impl lookup key.** `lowerName` derives from `cf.stubName`, so it is populated only when the callee is an *unfilled import*. A lower whose callee was a real component func fell through to a bare `coreDef{stub: true}` recording **nothing**; every downstream lookup then ran with an empty key and missed.

**3. The hole was sort-agnostic.** That fall-through did not read `cn.Opts.Async`, so a **sync** guest-to-guest call was equally unreachable. Nothing exercised either, because every composed artefact in the tree either cancelled the child or asserted load-and-instantiate only.

## Decision

**A cross-component call binds the lower to its callee's component func, through a `coreDef.lowerCallee` field, and the async arm adapts that func into the `asyncLowerImpl` the lower side already consumes.** Both sorts are handled, because the hole was in both.

### The goroutine, and what it inherits

**The child's lift runs on one goroutine per call.** The model's `canon_lift` creates a Thread, resumes it, and returns the cancel trigger *immediately* for an async lift. **`invokeAsyncLiftWith` returns only when the task resolves**, so calling it inline would block the parent's lower inside the child's entire task — turning an async cross-component call into a synchronous one, which is the opposite of the capability.

**This is the second slice in a row to turn on the same engine property**, and it is worth stating once: *Burroughs' component entry points block, so every place the model returns a continuation is a place that needs a goroutine.* ADR 0094 met it from the other side — the model hands the embedder a per-call `OnCancel` and `Invoke` has nowhere to put it.

What the census requires this ADR to say about the two things a new goroutine inherits:

**§4's boundary memory model (ADR 0052 / B-MM-1).** This goroutine introduces **no new crossing**. It resolves the parent's subtask through the *same* `onResolve` closure the existing blocking arm uses, under the *same* `asyncHandles` mutex, woken by the *same* `signalLocked` wake-channel close→receive. Both carriers are the ones `async_waitset.go`'s header already names as load-bearing, and the retptr write still precedes `resolved` under the mutex, so a waiter that observes the resolution sees the lowered result. **What is new is who calls `onResolve` — a guest's lift instead of a Go impl — not where the write lands or what orders it.** The litmus battery's absence (#10, parked) therefore prices no differently here than it does for the host-impl blocking arm that has shipped since 2a-i-B.

**The table-twin coherence residual (#662).** Not reachable from this site. A `table.set` through an abandoned image needs a sibling agent holding an older image of a table being grown; the child's guest code runs **only** on this goroutine, serialized by the child's own `entrySem`, and the child's memory and tables are touched by nothing else. The parent's memory is touched by this goroutine only through `onResolve`'s retptr write, which is a memory write and not a table one. Stated rather than omitted, because the census's question is about both and "not applicable" is an answer a reader can check.

**One goroutine per call, and it ends when the child's task resolves.** `invokeAsyncLiftWith` returns on resolution, a trap, or its park's bound (`liftParkBound`), so the goroutine cannot outlive the call indefinitely — *a launched process is a claim that something will end it*, applied inside the engine. The claim here is the park's bound, which already existed for exactly this reason.

### Cancellation composes out of ADR 0094 rather than needing its own mechanism

The adapter owes an `onCancel`, and the model's is `task.request_cancellation` — which ADR 0094 built as `liftTask.requestCancelLocked`, with the cancel-aware park and the top-of-loop delivery behind it. So the returned closure requests the child's lift-task cancellation through the child's own handle table.

**And it does not work yet, for a reason outside this slice.** Measured on #862's composed artefacts: the WAT parent's `subtask.cancel` returns **`0xffffffff` (BLOCKED)** and then traps at `subtask.drop`; the Rust parent traps `unreachable`. Both are [#892](https://github.com/scttfrdmn/burroughs/issues/892) — the model *waits* for resolution on a sync-lowered `subtask.cancel` (def:2427-2428) and Burroughs returns BLOCKED unconditionally. #888's recon predicted this path would meet #892 first, because a guest child's cancellation unwinds through the guest and so is never instantaneous; the prediction is now a measurement.

**A second, smaller finding rides with it** — ~~and it was neither as small nor as recordable as this paragraph claimed~~. As written:

> the parent can request cancellation before the child's task has started, where `requestCancelAll` answers `ErrTaskCancelUnbuilt` (ADR 0094's refused status-3 path) and the adapter drops it, so an early cancellation is **lost**. … new information about #884's priority rather than a defect to patch here. Recorded on #892 and #884 rather than worked around.

**Amended by grave #892's slice, in two steps.** Calling it "recorded rather than worked around" rested on the parent learning of the loss through BLOCKED; #892 removed BLOCKED from the sync path, because the model waits there, and the loss became a **30s hang** — measured as a *flaky* one, which is the worst form. So it was a defect to patch after all:

1. **The registration half**: the adapter now creates and registers the child's task **synchronously, before the impl returns** (`newLiftTask`, split from `runLiftTask`), so registration happens-before anything the caller can do next. Previously the task was created on the goroutine and a prompt parent often found nothing to cancel.
2. **The before-started half**: [ADR 0094 amendment 2](0094-host-cancellation-reaches-a-lift-task-through-an-internal-trigger-this-slice-because-the-model-hands-the-embedder-a-per-call-oncancel-and-invoke-blocks.md) honours the INITIAL arm instead of refusing it, having found ADR 0094's reason for the refusal false of Burroughs — **and the reason it is false is this ADR's own doing**: the adapter calls `onStart()` before the child's lift task exists, so the parent's subtask is already STARTED and status 3 is unreachable here.

What survives of the paragraph: the cross-component path does reach the before-started case naturally, and #884 keeps its subject (status 3 has no reference reading). What it loses is this consumer.

### Result lifting is scoped to scalars, by name

A child's `task.return` hands over **flat core values in the child's own ABI**. For a scalar that is the value. For anything aggregate the flat value is a *pointer into the child's memory*, which the parent cannot read: two components, two memories, two allocators, so carrying one means copying through the parent's `realloc` with the child's memory as the source.

That copy is real work with its own witnesses and it is not what #888 registered. The measured cases return `u32`. So an aggregate **refuses by name at the resolution** (`ErrCrossComponentResult`, naming the kind), which is the discipline `ErrTaskCancelUnbuilt` already applies to an unmeasured branch — a boundary a reader meets rather than a silent truncation.

**The type used is the child's lift signature, not the parent's import declaration.** They agree when the composition is well-typed, and when they disagree the one that describes the bytes is the right oracle.

## Consequences

- `coreDef` gains `lowerCallee`, and the resolver's *"a real export, by reference"* guard gains it as an exclusion. **It had to**: a cross-component lower has an empty `lowerName` and is not a stub, so it matched that guard and was handed back as `d.extern` — **the zero Extern** — which `InstantiateLinked` reports as *"a supplier with no defining module"*. That message is accurate and unhelpful, and the shape is worth more than the fix: **a predicate written as "none of the known special cases" silently admits the next special case**, and admits it into the *default* arm, where the failure surfaces as far as possible from its cause.
- **A sync lift's results stop being discarded.** `invokeWith` returned `nil` for the sync arm, with a comment saying sync lifts move no values through that path — true of the callers that existed, false of the path. It surfaced twice here from opposite directions: `CallValues` on a sync export declaring a result refused with *"the guest returned 0 flat value(s)"* when the guest had returned one, and the **sync cross-component arm** would have silently produced no result. The second is the worse one: a wrong value reported as success.
- A composed artefact that **completes** exists for the first time (`testdata/asynclift/call-wat-parent/`). Every prior one cancelled, which is why the capability had no end-to-end arm.
- The inline arm of the parent's dispatch (`RETURNED` from the lower, result already at the retptr) is **correct code and unwitnessed**, because it is unreachable through a `wit-bindgen` child: such a child's lift always returns WAIT. A first draft of the fixture's prose claimed a fast host would reach it; measuring showed both hosts take the park arm, because the inline arm is a property of the *callee's* ABI and not of the host's latency. Said in the fixture rather than left to a coverage report.
