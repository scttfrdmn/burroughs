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

**1. The lower already consumes exactly what a lift produces.** The model's `canon_lower` ends `subtask.on_cancel = callee(on_start, on_resolve)` (def@2bed77e:canon_lower:"subtask.on_cancel = callee(on_start, on_resolve)"), where the callee is a `FuncInst = Callable[[OnStart, OnResolve], OnCancel]` (def@2bed77e:FuncInst) — and `Store.lift` produces precisely that from a core func (def@2bed77e:Store.lift). **Burroughs' `asyncLowerImpl` is already that type.** So the subtask protocol, the STARTING→STARTED transition, the cancel arm's terminal-state choice, the retptr lowering, the blocking arm's `[state | subtaski<<4]` — all unchanged. The work is **one adapter**.

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

**And it does not work yet, for a reason outside this slice.** Measured on #862's composed artefacts: the WAT parent's `subtask.cancel` returns **`0xffffffff` (BLOCKED)** and then traps at `subtask.drop`; the Rust parent traps `unreachable`. Both are [#892](https://github.com/scttfrdmn/burroughs/issues/892) — the model *waits* for resolution on a sync-lowered `subtask.cancel` (def@2bed77e:canon_subtask_cancel:"if not async_:") and Burroughs returns BLOCKED unconditionally. #888's recon predicted this path would meet #892 first, because a guest child's cancellation unwinds through the guest and so is never instantaneous; the prediction is now a measurement.

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

## Amendment, 2026-10-17, accepted: outcomes the reference's single runner forbids

**Status: accepted** (the chair's ruling of 2026-10-17 on this amendment's revision, relayed by Scott): F0 and F1 adopted in design, with the re-entry rule below added on that ruling. **Not yet built**; the build owes the list at the end. The chair ordered (ruling of 2026-10-17, item 2) that #1020 be characterized with fixtures, and the design written here, before any engine change. This section records both, and it is held for review as ADR 0101's Option A was.

**Revised after the chair's review of the first draft** (ruling of 2026-10-17 on #1025). The review corrected part 1's boundary and asked for one fence to be evaluated against the two-part rule. Taking the corrected boundary seriously found a third mechanism, (c), which is the common case rather than a race. The rule section below replaces the first draft's. Citations into the reference name the pin they read, 2e7895c, by symbol.

### The class

This ADR runs a cross-component callee on its own goroutine, concurrently with its caller. The reference has one runner: a callee advances only when its caller suspends, yields or returns, and a host event is delivered only on a later `Store.tick`. So **some outcomes the reference forbids are reachable here.** #1020 is one of them: `cancel-suspended` read RETURNED (2) once on ubuntu under `-race`, where every oracle reads 4.

### Measured scope: the class reaches the default path, by three mechanisms

Every reading below was taken with `gate:cm-threads` off, no Threads, and the gate-on run alongside. Each reference reading is calibrated on `wait-runs-ready` first, and agrees on the pin (2bed77e) and on head (2e7895c).

**(a) A callee's result store lands inside the caller's `inHost` window.** While a caller's async lower is inside its impl, the cross-component adapter, a resolution is queued and applied at `leaveHost`, before the lower returns. A callee that resolves inside that window makes the lower return RETURNED.

| fixture | reference | Burroughs, natural | Burroughs, window widened 2 ms |
|---|---|---|---|
| `call-wat-parent`, host `tick` resolving on a goroutine with no delay | **{STARTED}**: the host event arrives only on a later tick | STARTED, 300 of 300 per gate; 100 of 100 under `-race` | **RETURNED, 30 of 30** |

The host is modelled in the reference as head's own `mk_host_func` does: a host Thread waiting on a flag the embedder sets once it begins ticking. wasmtime's CLI cannot supply a host whose event arrives later, so the reference is the oracle here.

**(b) A cancel is judged against the callee's live task state.** While the caller runs, the callee's own task can complete. Its result store is fenced by the caller's run slot (#983), but `subtask.cancel` asks the callee's task directly, finds it resolved, and arms no hand-back.

| fixture | reference (head) | Burroughs, natural | Burroughs, cancel widened 2 ms |
|---|---|---|---|
| `cancel-wat-parent` (sync `go`), host `tick` resolving on a goroutine with no delay | **{20}**: STARTED, then CANCELLED_BEFORE_RETURNED | 20, 200 of 200 per gate | **trap at the sync tail, 30 of 30** (gate off) |
| `cmthreads/cancel-suspended` (#1020, async canceller, Threads) | {4} | 4, 0 failures in 1,000 on darwin; 2 once on ubuntu under `-race` | **2 (RETURNED), 30 of 30** |

In the first row the parent's store is still waiting for the slot the canceller holds, so the judged wait finds no candidate and traps. Before #1018 that wait was unjudged with the gate off, and it would have read RETURNED. Either way the outcome is one the reference forbids. The pin traps this row too, but that is the sync-cancel row it is stale on (#726).

**(c) A callee's first segment does not run inside the lower.** The reference's async lower runs the callee itself until it first blocks. `canon_lower` calls the callee (def@2e7895c:canon_lower:"subtask.on_cancel = callee(on_start, on_resolve)"), and `canon_lift` runs the new Thread until it first suspends or exits (def@2e7895c:canon_lift:"thread.resume()") before control returns. A callee that resolves inside that segment makes the lower return RETURNED inline (def@2e7895c:canon_lower:"return [Subtask.State.RETURNED]").

Burroughs' adapter, `liftAsAsyncImpl`, starts the callee on a goroutine and returns at once. So `asyncLowerFunc`'s re-check of `st.resolved` runs before the callee's first segment ends, and the lower returns STARTED.

| fixture | wasmtime 49.0.3 | reference | Burroughs, natural |
|---|---|---|---|
| `first-segment-return` (new: the child calls `task.return` and EXITs in its first core call) | **2**, 10 of 10 | **{2}** | **1 (STARTED), 200 of 200 per gate** |
| `call-wat-parent`, host `tick` resolving inline (`crossCallHost`'s `deferTick=false`) | no CLI host | **{2}** (`host-event-inline`) | **STARTED, 200 of 200 per gate** |

A probe confirmed that the receipt child's first core call returns EXIT on the inline `tick`, so the child did resolve inside its first segment. **No widened window is needed: this is the natural rate.** It went unseen because `TestACrossComponentAsyncCallCarriesParamsAndResult` accepts both arms, on the premise in its own comment that *"which arm is taken is a scheduling fact, not a property of the callee's ABI"*. For a callee that resolves in its first segment, the reference says it is a property: RETURNED. (wasmtime 49.0.3 is the CLI installed now; every earlier reading names 49.0.2.)

**Not in the class, measured:** `poll-before-suspend`, a caller polling a provider's subtask without suspending. Burroughs reads 1 (STARTED, no event seen), 200 of 200 per gate, matching wasmtime (1, 10 of 10) and the reference ({1, 9002}). The result store is fenced by the caller's slot, so the caller never observes the event early. The YIELDing provider can legitimately resolve inline (9002), so the window of (a) cannot produce a forbidden outcome there.

### The boundary is the callee's first suspension

The first draft's part 1 said *"a lower returns STARTED unless the callee resolved before the lower began running it"*. **That is not the reference**, and the chair corrected it. The reference's async lower runs the callee itself until the callee first blocks, so a resolution inside that first segment is RETURNED, inline. What the reference forbids is progress **after the callee's first suspension** while its caller has not suspended. A blocked callee is resumed only by `Store.tick`, which runs at nesting depth zero (def@2e7895c:Store.tick:"assert(self.nesting_depth == 0)"), so only once the whole running chain, its caller included, has given control back.

**In the adapter, the boundary is where `liftAsAsyncImpl` returns to `asyncLowerFunc`.** The reference returns there when the callee's first segment ends: `canon_lift`'s `thread.resume()` returns. Burroughs returns when the goroutine starts. That gap is (c).

### Two designs, evaluated

**The two-part rule** (the first draft, with its boundary corrected):

1. the lower waits for the callee's first segment; a store arriving after the callee's first suspension, while the caller is still in its lower, is applied at the caller's next suspension;
2. (2a) a callee's continuation past its first suspension waits until its caller next suspends, yields or returns.

**The single fence** (the chair's): a callee can run past its first suspension only after its caller has suspended since the call, which is a fence on each subtask edge.

What the evaluation found:

- **Given 2a, part 1's store deferral never fires.** A store from past the callee's first suspension cannot arrive while the caller is in its call, because 2a holds the callee until the caller suspends. So part 1's deferral is a consequence of the fence, as the chair said. Keeping it would mean a second mechanism, with its own neuter, guarding an empty case.
- **Part 1's other half is not a fence, and neither design removes (c) without it.** Waiting for the callee's first segment is the adapter's call shape: the reference's `thread.resume()` inside `canon_lift`. It is adopted on its own as F0 below, not as part of either design.
- **"Has suspended since the call" is not enough, and neither is 2a's "waits until its caller next suspends".** Both open the gate at a suspension and say nothing about the caller's resumption. A callee segment that began while the caller was suspended can still be running when the caller resumes. Then (b) recurs: the caller's `subtask.cancel` reads a callee task that is resolving underneath it. In the reference no callee is mid-segment when its caller resumes, because one thread runs. **So the fence has to be symmetric.**

### The rule adopted: F0 and a symmetric fence

- **F0, the boundary rule.** `liftAsAsyncImpl` returns when the callee's first segment ends, not when its goroutine starts. The segment ends at the callee's first suspension (a WAIT or YIELD park, a backpressure wait, a Thread suspension, a synchronous wait) or at the end of its task. A resolution inside the segment arrives while the caller is in host code, so the existing #983 queue applies it at `leaveHost`, and the lower returns RETURNED. **This removes (c).**
- **F1, the fence.** On each subtask edge, the callee's segments past its first suspension and the caller instance's guest execution exclude each other:
  - a callee segment starts only while no guest code of the caller's instance is executing, except on the callee's own call chain;
  - the caller instance's next guest execution waits until the edge's in-flight segments have reached their next suspension.

  **This removes (a) and (b).**
- **The callee's result store is inside its segment.** So it is atomic with the callee's task resolution, as the caller instance observes it. That is the property (b) is missing today: the store waits for the caller's run slot while the callee's task already reads as resolved.
- **The caller *instance*, not the caller task.** A subtask's handle lives in the caller instance's table, so any task of that instance can wait on it or cancel it. One task per instance executes at a time (the run slot, ADR 0101), so "no guest code of the caller's instance executing" is the run slot not being held by guest code.
- **2b, rejected as before:** rewriting the caller's observation, reporting CANCELLED_BEFORE_RETURNED for a callee that in fact returned, is not an outcome the reference allows.

### The four questions

1. **Nested chains, A → B → C.** The fence is per edge, and the edges compose:
   - C runs past its first suspension only while B's instance executes no guest code, and B only while A's does not.
   - C may run while A executes, when B is suspended. A cannot observe that through the chain: A sees C only through B, and B does not run while A executes.
   - The first segments nest exactly as in the reference: A's lower waits for B's first segment, which includes B's lower waiting for C's.
2. **A callee with several callers over its lifetime.** The fence is keyed by the call (the subtask edge), not by the callee instance:
   - B serving A1 and A2 has two tasks, each fenced by its own caller's instance. Inside B they still exclude each other through B's run slot.
   - Threads that a callee task creates belong to that task (ADR 0100), so the same edge fences them.
3. **Host events while every caller is suspended still progress in parallel.** In `fanout`'s `after` mode the caller WAITs, so all four callees continue at once, each in its own instance. The fence costs the caller one wait at resumption, for any callee segment still in flight. What it gives up is a callee continuing while its caller executes, which the reference never does.
4. **The run slot.** The fence is a second relation beside the slot, not a change to it. The slot orders tasks within an instance; the fence orders a callee's segments against its caller's instance. Two rules keep them from deadlocking:
   - **The fence is taken before the slot**, on both sides. A caller that took its slot and then waited on the fence would hold the slot its callee's result store needs, while the callee waits for that slot inside the segment the caller is waiting on. A callee's store runs under the fence it already holds.
   - **Re-entry is recognised by chain**, as the slot's is (#976), which the next section states as a rule.

### Re-entry across the fence (added on the chair's ruling of 2026-10-17)

A callee segment that holds F1 and calls synchronously back into its caller's instance needs that instance's guest execution, which F1 excludes. Without a rule the call waits on a fence its own chain holds. The shape is real: `reenter.wasm`, #977's `thread-wait-callback`, `residual`, and the new `reenter-after-suspend` all have it.

**The rule: a re-entry joins the callee's chain and is admitted.** The key is the chain identity the sync arm already carries for the run slot (#976, #984):
- `bindCrossComponent`'s sync arm passes the caller's `runChain` into the callee's context with `withRunChain`;
- the callee's entry reads it with `runChainFrom`, and `acquireRun` re-enters a slot that chain already holds.

F1 uses the same key. An entry into an instance waits for the in-flight callee segments on that instance's edges, **unless its chain is one of those segments' chains**. In that case it is the segment calling back, as one thread does in the reference, and it is admitted.

**F0's corollary: the first segment runs on the caller's chain.** In the reference, a callee's first segment runs on its caller's thread, inside `canon_lift`'s `thread.resume()`. So `liftAsAsyncImpl` gives the first segment the caller's `runChain`, as the sync arm does. A re-entry made inside a first segment is then admitted by the same rule. That is `residual`'s shape: `g`'s lower runs the parent's `h2`, whose first segment executes in the parent's instance while `g`'s segment holds the fence. Without the corollary, F0 and F1 would deadlock there, `g` waiting for `h2`'s first segment and `h2` waiting for `g`'s fence. The callee's segments after its first suspension run on its own task's chain, which is what F1 fences.

**The prediction table, for the build:**

| fixture | shape | wasmtime | reference | Burroughs now | after the build |
|---|---|---|---|---|---|
| `reenter.wasm` | sync parent → child → parent, through a lift the parent passes its child | 8 | — | 8, 50 of 50 per gate | 8 |
| `cmthreads/thread-wait-callback` (#977) | a Thread waits on a child whose async `g` calls the parent's sync `f` inside its first segment | 8 | — | 8, 50 of 50 | 8 |
| `cmthreads/residual` | `g` async-lowers the parent's async `h2`, a re-entry inside a first segment | BUG | 8 | 8, 50 of 50 | 8 |
| `reenter-after-suspend` (new) | `g` YIELDs, then calls the parent's sync `f` from its callback: past its first suspension | **8**, 10 of 10 (49.0.3) | **{8}** | 8, 200 of 200 per gate | 8 |

Two of these differ from the ruling's description, and the table follows the fixtures:
- **`residual` is not "`g` calls the parent's sync `f`".** That was its first shape, which its header records as replaced because it did not discriminate a neuter. Its committed shape re-enters through an async lower, so it exercises F0's corollary rather than the sync arm.
- **#977's fixture is `thread-wait-callback`.** Its `g` calls `f` inside its first segment.

`reenter-after-suspend` is the row that exercises F1's rule itself.

**Neuter:** the fence ignoring chain identity. All four rows then hang, and the witnesses fail on their bounds rather than waiting forever.

### What each design leaves unhandled

- **The two-part rule** leaves (b) open after the caller resumes with a callee segment in flight. It also keeps a deferral queue for a case 2a makes empty.
- **The adopted rule** leaves communication that does not follow a subtask edge, **stated as unmeasured, not as handled**:
  - a stream or future end passed to a component that is not its holder's caller or callee;
  - host state shared by two instances.

  Segments in different instances still run in parallel. Where two of them share such a channel, their interleaving need not be one the single runner allows; for example, two polls in one segment can see a write land between them. None of the class's fixtures has that shape, and it needs its own characterization before any claim is made about it.
- **Both** give up callee–caller overlap. F0 also gives up overlap between a callee's first segment and its caller, which the reference itself serializes.

### The cost, forecast before the build

`BenchmarkFanOut` (`testdata/fanout/`) has one caller, four callee instances, k = 1 or 4, and the work either in the first segment or past a YIELD. On main's engine, on janus:

| row | main, sec/op (n = 10) | overlap |
|---|---|---|
| `work=first/k=1` | 12.84m ± 0% | — |
| `work=first/k=4` | 14.21m ± 2% | four callees in 1.11× one |
| `work=after/k=1` | 14.23m ± 0% | — |
| `work=after/k=4` | 15.68m ± 0% | four callees in 1.10× one |

Taken on janus.local (i9-9960X, linux/amd64) in its `measured` group, as pueue task 32, with 0 concurrent tasks at submit, at 8197a3d (main's engine with the benchmark added). The summary is from `benchstat` over the task's full log.

The forecast for the build, against those figures:

- **`work=first/k=4` becomes about 4 × `work=first/k=1`**, about 51.4m (4 × 12.84m), against main's 14.21m: a 3.6× slowdown. F0 serializes the four first segments, as the reference does, so this is the parallelism the fix for (c) gives up.
- **`work=after/k=4` stays within 5% of main's 15.68m.** The work runs while the caller WAITs, which F1 leaves parallel.
- **`switch.wasm` is unchanged.** It has no cross-component edge.

The build measures all three under `make lab-ab`.

### Owed by the build

- `host-event-later` and `cancel-suspended` read in the reference set at their widened windows, 30 of 30. Since the build moves Burroughs into the reference set, the widened windows become the witness.
- `first-segment-return` and the inline `call-wat-parent` arm read RETURNED, 200 of 200 per gate. `TestACrossComponentAsyncCallCarriesParamsAndResult`'s inline case then asserts the arm instead of accepting either.
- A neuter for each mechanism:
  - F0 returning at goroutine start brings back (c);
  - a callee segment that does not take the fence brings back (a);
  - a caller resumption that does not wait for in-flight segments brings back (b);
  - the fence ignoring chain identity hangs the re-entry rows, on their bounds.
- The re-entry prediction table above, read after the build: every row 8.
- `BenchmarkFanOut` and `switch.wasm` under `make lab-ab` against the forecast, quoted with p-values.
- The concurrency witnesses run 50 times under `-race`.

### What the flip list carries

#1020's scope as measured above: **the class reaches the default path, gate off, by three mechanisms:**
- (a), with a host event;
- (b), with a cancel;
- (c), with a callee that resolves in its first segment.

(a) and (b) are rare at the natural rate and deterministic when widened. **(c) is the natural rate, 200 of 200.** The fix is this amendment's build, which is not required before the flip, because all three read the same in both gate states. The scope is stated so the flip is not decided on a flake.
