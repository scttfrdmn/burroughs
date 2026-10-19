# 0104 — SP-1, engine-native preemption: what `Stop` already guarantees, the two readings of what it owes, and the order to build them

Date: 2026-10-18 · Status: **accepted, with three rulings**: the chair's ruling, in session, 2026-10-18 (the second of the day): *"ADR 0104, the SP-1 recon: accepted, with three rulings … Merge … as accepted, with these three rulings appended to the ADR."* The rulings are in the section at the end, and they answer *Decisions this draft needs*. Drafted under the first ruling of that day, item 5: *"the SP-1 recon as an ADR draft held for review"*. **A recon: no engine code.** Every figure below is either a record cited by date or marked **unmeasured**. The facts were gathered read-only at Burroughs 82d722a and fork `wasmpreempt/phase4` d147ddc568.

## Context

**The clause** (contract §3): *"**SP-1.** Preemption is **engine-native**. The engine MUST implement epoch/safepoint checks (loop back-edges and call sites) such that a host request `stop(deadline)` brings every guest thread to a safepoint within a bounded, configurable interval. The guest runtime MUST NOT need to self-instrument its own code generation to be stoppable."* SP-2 and SP-5 make a thread blocked in a host call, a futex wait or a suspending built-in count as at a safepoint. SP-4 requires the stop to complete without waking such a thread. SP-6 makes `Resume` an acquire edge.

### What is on main

- **`Instance.Stop(deadline)`** (`internal/interp/safepoint.go`, ADRs 0059, 0067, 0074). It sets `stopReq` on every thread and returns once ADR 0074's predicate holds (`parked + blocked >= callers` per thread), or at the deadline with `ErrStopDeadline`, naming each thread still executing.
  - On expiry the world stays stopped and `Resume` is still required.
  - **Nothing forces a late thread.**
- **The poll** is one atomic load in `thread.poll`, read nowhere else. It is reached from:
  - the fourteen `jumpTo` arms in `exec.go`, which cover every back-edge. They are held complete by `TestEveryPCAssignmentInRunFrameGoesThroughJumpTo` and `TestNothingTakesTheAddressOfRunFramesPC`.
  - `enterFrame`, which covers every call entry and every `return_call` re-entry (`tailcall.go`).
  - the boundary of a blocking call (`enterBlocked`/`leaveBlocked`).
- **Three stretches that never poll:**
  1. **Straight-line code**, until its next back-edge or call. ADR 0059 chose this deliberately: *"straight-line code pays nothing at all"*. The stretch is at most one function body's straight-line run, but **no numeric bound is stated anywhere**.
  2. **A bulk instruction** (`memory.fill`, `memory.copy`, the table and array bulk ops) is bounded only by its operand size.
  3. **`callAdapter`** (`internal/interp/host.go`) sets no blocked mark, because *"the adapter never leaves guest execution"*. A long lift or lower that does not route through `CanonCaller.Blocking` is a stretch `Stop` waits for.
- **The poll's cost, as recorded by ADR 0059 on 2026-09-02** (status still proposed): Tight +0.63% on arm64 and +1.07% on amd64, geomean +0.38% and +0.73%, against a registered bar of worst row ≤ +3.0% and geomean ≤ +1.5%. The fitted cost per back-edge is about 177 ps on arm64 and 523 ps on amd64. These are records, not re-measurements.
- **The litmus case** `sp1-stop-brings-every-agent-to-a-safepoint-by-the-deadline` (`docs/litmus-battery-preregistration.md`) is registered and **unwritten**: N = 8 agents in arithmetic-only loops, every counter frozen after `Stop` returns, read twice 10 ms apart. Its status is *"blocked — #10"*, re-pointed from #554 as *"writing the case, not a missing mechanism"*. #10 is open and **unscheduled** by Scott's #670 ruling.

### What the fork's runtime does, and does not, expect

- **The fork has cooperative preemption only.** In the fork tree, the runtime's wasm `preemptMSupported` is `false` and its `preemptM` is empty (`os_wasm.go` in that tree's `src/runtime`, recorded on #781). Its stop-the-world reaches a running worker through the compiler's `insert resched checks` pass, which polls the process-wide `wasmSafepoint` word. `GOEXPERIMENT=burroughsspawn` turns that pass on, and a g0-stack caller declines to yield (`wasmpoll_wasm.go`).
- **The fork never calls the engine's `Stop`.** Its only engine import is `burroughs spawn`. **No non-test code in Burroughs calls `Stop` either.** #781's plan (*"GC STW → a host import calling `Stop`"*) and ADR 0087 (*"GC-STW trigger → `Stop`"*) describe a binding that exists in neither tree. That correction is recorded on #781 (comment of 2026-10-18).
- **So clause 3 does not exercise SP-1.** It witnesses the Go runtime's own cooperative stop-the-world among its threads, and the engine's safepoints take no part in it.
- **And the fork relies on exactly what SP-1's last sentence forbids:** the guest self-instruments its code generation to be stoppable.

## The two readings

**(a) SP-1 as a host stopping wasm threads.** This is mostly built. What it still owes is engine-internal:

1. **A stated bound on the arrival interval.** Today the deadline is configurable, but the interval it must exceed is neither stated nor measured.
2. **Polls in the two unpolled stretches that scale with data rather than code.** Bulk instructions would poll per chunk, and an adapter's long copy would poll or be marked blocked. Straight-line code is bounded by the module's own size and stays unpolled, which is ADR 0059's choice.
3. **The litmus case, written.** It is a battery case, and the battery is unscheduled.

**(b) SP-1 as preempting the guest runtime's own goroutines**, so the fork could drop its self-instrumentation. **Nothing exists.** Go's stop-the-world needs the stopped goroutine to *run* `gcstopm` or `runSafePointFn`, and a park and resume does not cause that. It needs the engine, at its own safepoint, to run a guest entry point on that agent's stack: an `asyncPreempt` analogue, with the fork setting `preemptMSupported = true` and a `preemptM` that requests it. **That is public API**, so it is Scott's subject under behaviour 2.

## Options

- **A. Build reading (a), and record (b) as a decision for Scott with its consumer trigger.** Engine-internal only:
  - state the bound;
  - a chunked poll in bulk instructions, under a chunk size chosen and measured;
  - the adapter stretch closed, by a poll or a blocked mark, whichever ADR 0069's mark semantics permit;
  - the SP-1 case and a bulk-instruction case.
- **B. Reading (b) first.** This is the reading that matches SP-1's last sentence. But it starts with public API that has no stamp, and with a guest-entry mechanism the engine has never had. It would also be built ahead of its consumer: the fork works today on its own polls, and nothing in it is blocked on (b).
- **C. Neither yet.** Keep SP-1 as it is. This leaves a §3 MUST, "a bounded interval", unstated, on the path v1 requires.

## Recommendation

**A.** It closes what SP-1 owes the host without new API. It measures what is currently unmeasured, so (b) can be decided on numbers. And it keeps (b) as Scott's decision, with a stated trigger: **a consumer that cannot be made stoppable by its own polls.** That would be a fork build with the resched pass off, or a goroutine stuck in a loop the pass does not instrument.

## What each claim is measured against

| claim | oracle |
|---|---|
| a running loop reaches a safepoint within a bounded time, without guest instrumentation | **wasmtime, for the shape only.** Its `Config::epoch_interruption` documentation (49.0.2 source) says the check *"cannot be avoided by WebAssembly code"*, that bulk ops *"only check the epoch once at the start"*, and that epochs *"do not assist"* code blocked in a host call. It interrupts a single store; it does not park N agents. Where wasmtime places its checks: **unmeasured**. |
| N agents parked, counters frozen (the litmus case) | **None independent.** No engine ships `thread.spawn` (ADR 0087, D-1). The registered arbiter is *"neither — a scheduling claim"*, so only Burroughs's own battery measures it. |
| a bulk instruction or adapter stretch is stopped within the bound | **None independent.** wasmtime's documented bulk-op behaviour (one check at the start) is weaker than what (a) proposes, so it is a floor, not an arbiter. |
| the poll's cost | Burroughs's own A/B: `internal/interp/loopbench` for the effect and `membench` for scope, against ADR 0059's registered bar. |
| reading (b) | **None.** The reference `definitions.py` (pin 2e7895c) is silent on preemption, because its threads switch only at explicit yield and wait points. Upstream Go's async preemption on wasm is *Unplanned* (ADR 0087, Context). |

## Forecast and witnesses owed (for A, to be registered before building)

- **Witnesses:**
  - the SP-1 litmus case as registered;
  - a bulk-instruction case: an agent inside one large `memory.fill` when `Stop` is requested must arrive within the stated bound, not after the fill;
  - an adapter-stretch case;
  - each with a neuter that removes the new poll.
- **Forecast:**
  - the whole tree stays green;
  - `loopbench` and `membench` stay inside ADR 0059's bar, so the bulk chunking must not move the scope control;
  - `switch.wasm` and `BenchmarkFanOut` are unchanged under `make lab-ab`.
  - **The bound itself is unmeasured today**, so the first measurement registers it rather than asserting a number nobody has taken.
- **Known noise:** #896 and #946 (the tail-call test's post-`Resume` wait) are open, and any timing assertion built on that shape inherits them. #946's question, load or a lost wake, is still unmeasured.

## Decisions this draft needs

- **The chair's:** whether A is the recommendation to build.
- **The chair's:** whether the SP-1 litmus case may be written under this slice while #10 stays unscheduled. By Scott's #670 ruling the battery is unscheduled, and SP-1's witness is one of its cases. So building A with its witness schedules one case of a parked battery. **That ruling is Scott's to vary, so it is named here rather than assumed.**
- **The chair's:** whether ADR 0087's *"GC-STW trigger → `Stop`"* is corrected by amendment.
- **Scott's, if reading (b) is ever wanted:** the guest-entry API.

## Accepted, 2026-10-18, with three rulings

**The chair's ruling, in session, 2026-10-18, accepted this recon with three rulings.** They are carried here verbatim in substance, and they answer *Decisions this draft needs* above.

1. **Reading (a), the host stopping wasm threads, is SP-1 as the contract writes it. Build it.**
   - **The forecast:** a numeric arrival bound, under a configurable deadline, with the worst case measured by instruction class.
   - **The polls:** inside the unbounded loops that have none, meaning the bulk memory ops and the adapter copies, including ADR 0103's.
   - **The witness:** a tight loop that makes no calls must reach a safepoint within the bound.
   - **The neuter:** removing the back-edge poll must make it miss the bound and fail, not hang.
   - **The cost:** poll overhead measured with `make lab-ab` against the janus baseline.
2. **The witness conflict resolves without the battery.** SP-1's own litmus case can be a standalone witness now. Scheduling the §4 battery as a whole stays parked under #10, and building one case does not schedule it.
3. **Reading (b), the Go guest's GC stop-the-world, is a contract gap: recorded here as unmet.**
   - Today the fork does exactly what SP-1 says a guest must not need: it self-instruments with the `resched` checks on its `wasmSafepoint` word.
   - Closing the gap means a guest-facing import, a stop request modelled on `burroughs spawn`. That is contract surface, so it is Scott's.
   - After (a) lands, Scott gets one concrete proposal, decided unless he objects: the import's shape, the contract amendment text, and the fork-side change.
   - **None of (b) is built before he has seen it.**

**On ADR 0087:** its *"GC-STW trigger → `Stop`"* binding exists in neither tree. A dated correction is appended to 0087, citing this recon's search, with 0087's body unedited.

**The order the ruling sets:** ADR 0103's adapter, a default-path defect, is built first, then reading (a).

## As built, 2026-10-18: reading (a), first half

**The bound, as registered on #515 before building:** B = (the longest straight-line stretch in the module) +
one bulk chunk + scheduling noise. So arrival no longer scales with any bulk operand.

- **Built:** `memory.fill`, `memory.copy` and `memory.init` poll between 64 KiB chunks (`bulkChunk`,
  `internal/interp/bulk.go`). Each executor still loads its image once. Holding the slice across a park is safe
  because `soleAgentLocked` refuses a relocating grow while any other live thread has a caller, and a parked
  agent is inside a call. A first draft re-loaded the view after each poll, which guarded nothing and broke
  decision 0058's one-load rule (`TestEveryOperationLoadsAPublishedImageAtMostOnce` caught it).
- **The forecast is met.** The table, with janus tasks 43 (before) and 44 (after), is on #515 and in the
  CHANGELOG. The 64 MiB fill went from p99 16.7ms to p99 30µs or less, and the 32 MiB copy from 4.2ms to 28µs or
  less. The tight loop and the straight line are unchanged.
- **The cost is inside ADR 0059's bar:** `loopbench` and `membench` show no significant row.
- **Witnesses:** `TestATightLoopReachesASafepointWithinTheBound`, `TestABulkFillReachesASafepointWithinOneChunk`,
  and `TestSP1StopBringsEveryAgentToASafepointByTheDeadline`, the litmus case standalone. Each neuter fails
  rather than hanging.
- **Owed by reading (a) when this half landed, and built in the second half below:** poll points in the sync
  adapter's copies (ADR 0103). They run in host Go code on the caller's thread inside `callAdapter`, and they
  need an exported poll on `CanonCaller` and chunked heaps. A blocked mark is not the alternative: the adapter
  writes guest memory, and SP-2 forbids a stopped agent to.
- **Not reached by this half:** the table and array bulk instructions (`table.fill`, `table.copy`,
  `table.init`, and the GC array ops). Their operands are element counts, typically far smaller than a
  memory's, and they remain unpolled. That is unmeasured, and it is named here so the bound's scope is not
  read as wider than it is.

## As built, 2026-10-18: reading (a), second half

- **Built:** ADR 0103's four adapter copies poll the caller's agent. `CanonCaller.Poll` is the back-edge's poll,
  and every read and write the copies make calls it first:
  - the caller-side heap (`adapterHeap`);
  - the callee-side `argHeap` and `liftHeap`, with the caller passed through `invokeWithPending`.

  The codec's string lift and lower stream in 64 KiB chunks (`canon.StringChunk`) with incremental UTF-8
  validation. Without that, a whole-value `[]byte(s)`, `utf8.Valid` or `string(b)` would sit between two
  polls. A list moves element by element, so it is already one heap call per element.
- **Only the sync adapter polls.** An async lower's result store can run on another agent's goroutine, and a
  poll there would park the caller's thread from the wrong goroutine, possibly while the caller holds a blocked
  mark.
- **The forecast is met.** The table, with janus tasks 47 (before) and 52 (after), is on #515. A 16 MiB string
  went from p99 23–25ms to p99 59µs or less, and its max to 134µs or less.
- **The cost is inside ADR 0059's bar**, but only at the second draft.
  - The first draft (task 50) read +8.46% on ADR 0103's sync string round trip and +2.23% on the async one.
    The causes were eight allocations per call, from a poll closure, a context value and boxed wrappers, plus
    the streamed form's overhead on a short string.
  - The repair: the caller is a parameter, the caller-side heap is one pointer, and a string of at most one
    chunk keeps the whole-value form. It reads +0.99% and ~, with a geomean of −0.01% (task 51).
- **Witnesses:**
  - `TestAStopDuringAnAdapterCopyArrivesWithinTheBound`, as landed: a 2ms bound over 40 trials, failing at 6 or
    more misses. Locally, under a whole-tree test load, the polled form missed 0 of 40 in 20 runs, and its
    neuter missed 15 to 24 and failed 20 of 20. The bound had been 10ms as registered, which passed under the
    neuter on darwin/arm64, and the count replaced a single-miss criterion that failed once under `make ci`.
    - **CI falsified the separation, and the witness is replaced** (the list-lift section below). On #1066's
      run, CI amd64 missed 11 of 40 with the polls in place. A wall-clock bound tight enough for a neuter to
      miss on a fast machine sits inside a loaded runner's noise.
  - `TestUTF8JoinAgreesWithWholeValidation`, which caught the joiner's first draft.
- **Not reached, and named:**
  - **The callee's own execution.** While the callee executes, the caller's agent holds no blocked mark, so a
    `Stop` of the caller's instance waits for the callee to return. The callee's frames poll the callee's own
    instance, which is another world. This is #515's instance-scoped `Stop`, and this ADR does not repair it.
  - **A list's lifted value is one zeroed Go allocation** (`canon.LoadList`'s `make([]Value, count)`). That is
    runtime work proportional to the element count, and no poll can interrupt it. Locally, a 512 Ki-element
    `list<u8>` crossing the adapter, with every poll in place, reached 9ms arrivals and missed a 10ms deadline.
    The copies poll per element; the allocation is the stretch left. This is unmeasured on janus.
- **Reading (a) is not complete.** This line said it was until the chair's third ruling of 2026-10-18, which
  corrected it: the forecast's max is 1ms, and the list lift's 9ms stretch is inside ADR 0103's adapter, which
  ruling 1 named.
  - **Reached:**
    - the bound, registered and measured by instruction class;
    - polls in the bulk memory ops and in the adapter's string copies, each meeting its forecast on janus;
    - the tight-loop witness, the cost, and the litmus case.
  - **Owed, both ordered by that ruling:**
    - **The list lift.** Scalar elements cross memory to memory in `StringChunk` pieces with polls and no
      `[]Value`; other elements build their slice in bounded pieces.
    - **The callee's execution.** The caller's agent holds a blocked mark across a sync callee's run, and every
      re-entry into the caller polls first. This comes as a dated amendment here, then the build.
  - Reading (b)'s proposal is #1064, and per ruling 3 none of (b) is built before Scott has seen it.
