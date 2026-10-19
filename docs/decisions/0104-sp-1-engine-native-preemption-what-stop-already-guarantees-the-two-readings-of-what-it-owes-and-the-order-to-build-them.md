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
- **Witnesses:** `TestATightLoopReachesASafepointWithinTheBound`, the bulk-fill witness (landed with a 10ms bound,
  replaced since by `TestAStopLandsInsideABulkFill` for the timing reason given in the list-lift section),
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
  - **The callee's own execution**, as this half landed. While the callee executed, the caller's agent held no
    blocked mark, so a `Stop` of the caller's instance waited for the callee to return. This section said "this
    ADR does not repair it". The chair's third ruling ordered the repair, and it is built (the amendment
    below).
  - **A list's lifted value was one zeroed Go allocation** (`canon.LoadList`'s `make([]Value, count)`): runtime
    work proportional to the element count, which no poll could interrupt. Locally, a 512 Ki-element
    `list<u8>` crossing the adapter, with every poll in place, reached 9ms arrivals. **Built since: the
    list-lift section below.**
- **Reading (a) is not complete.** This line said it was until the chair's third ruling of 2026-10-18, which
  corrected it: the forecast's max is 1ms, and the list lift's 9ms stretch is inside ADR 0103's adapter, which
  ruling 1 named.
  - **Reached:**
    - the bound, registered and measured by instruction class;
    - polls in the bulk memory ops and in the adapter's string copies, each meeting its forecast on janus;
    - the tight-loop witness, the cost, and the litmus case.
  - **Owed, both ordered by that ruling:**
    - **The list lift**, built since (the section below).
    - **The callee's execution.** The caller's agent holds a blocked mark across a sync callee's run, and every
      re-entry into the caller polls first. This comes as a dated amendment here, then the build.
  - Reading (b)'s proposal is #1064, and per ruling 3 none of (b) is built before Scott has seen it.

## As built, 2026-10-18: the list lift (the chair's third ruling, item 2)

- **Built:** a list lift is never one allocation proportional to its count.
  - **Packed scalars.** A list of a packable scalar kind (bool, the integers, char: what the heaps carry) is
    lifted as its canonical bytes, `StringChunk` at a time, with no `[]Value`. bool is normalised to 0/1 and
    char is validated per element through `LiftFlatScalar`. It is stored the same way.
  - **Segments.** Other lists longer than 4096 elements are built in 4096-element segments.
  - **The representation.** `canon.Value` marks both forms in `u`, which a list did not use. `List()`
    materialises, and `elems`/`listLen` read every form.
  - **f32 and f64 lists stay refused, as they were.** No heap carries floats today, so packing them would widen
    what crosses.
- **The forecast is met** (janus `measured`; before task 53, after task 54; tables on #515):
  - `list<u8>`, 512 Ki: p99 20–22ms → 35–45µs, max 83–94ms → 61–111µs;
  - `list<string>`, 512 Ki: p99 11–27ms → 13–17µs, max 87–92ms → 15–32µs.
- **The cost is inside ADR 0059's bar** (task 55): `ListRoundTrip` `list-u8` −70.19%, `list-string` ~.
- **The witnesses, and a replaced one.** The two timing witnesses (#1062's string case, and this slice's
  first list cases) are replaced by `TestAStopLandsInsideAnAdapterCopy`, which asserts a place and not a time.
  - **How it works.** After each stop, the parent's result regions are read
    (`testdata/adapter-copy-flip.wat`). A region holding two fill bytes is a write stopped part-way, which only
    a poll inside the copy can produce.
  - **The readings.** Polled, 15 to 26 of 200 stops land inside a copy. Neutered (`pollCaller`, or the packed
    store in one write), 0 of 200, every run.
  - **What covers what is out of its reach.** The lift side's chunking, a read no memory records, is
    `canon`'s `TestAPackedListCrossesTheHeapInChunks`. The segmentation is `TestALongListLiftsInBoundedPieces`.
    The packed form's equivalence to the plain lift is `TestAPackedListLiftsToThePlainElements` and
    `TestAPackedListStoresItsCanonicalBytes`.
  - **The bound is carried by the janus readings**, as the chair's ruling put it.
  - **The bulk-fill witness got the same repair**, because the same defect surfaced. Its 10ms single-miss bound
    missed on trial 0 under `make ci`'s `strict` gate, with the poll in place. It became
    `TestAStopLandsInsideABulkFill`: the fill byte alternates per pass, and a mid-fill park leaves the region's
    two ends disagreeing. Polled, 40 of 40 trials park mid-fill; with the per-chunk poll removed, 0 of 40.
- **Measured and set aside:** an arrival witness for the segmentation. Under the one-allocation neuter it failed
  4 of 5 runs at 512 Ki elements and 0 of 10 at 1 Mi, because one large allocation's cost varies run to run.
  The structural witness replaces it.

## Amendment, 2026-10-18: the callee-execution gap (the chair's third ruling of that day, item 3)

**The problem.** While a sync cross-component callee executes, the caller's agent is inside ADR 0103's adapter
and holds no blocked mark. A `Stop` of the caller's instance therefore waits for the callee to return. The
callee's frames poll the callee's own instance, which is another world, so for a composition of components
**the bound is unbounded**: any callee that loops holds the caller's `Stop` for as long as it loops.

**The design, as the ruling sets it:**

1. **The blocked mark.** For the span of a sync cross-component callee's execution, the caller's agent holds a
   blocked mark, as it does across a wait (`CanonCaller.Blocking`). The span is the callee's core invocation
   and its `post-return`. It is *not* the adapter's copies, which run on the caller's agent and keep their
   polls.
   - **Why #1062's objection does not apply.** #1062 refused a blocked mark across the copies, because the
     adapter writes guest memory there and SP-2 forbids a stopped agent to. Across the callee's execution,
     nothing writes the caller's memory: the callee has its own memory, so the caller's instance is not
     executing.
2. **The entry rule.** Every re-entry into the caller's instance during or after the span polls the caller's
   stop request before it runs. That covers three re-entries:
   - **the adapter's result lowering**, after the span: `leaveBlocked` unmarks and then polls, before #1062's
     per-call polls;
   - **the caller's realloc**, a guest frame, which polls at entry;
   - **a callback into the caller's exports**, a new entry into the caller's instance, whose first frame polls
     before its first instruction.

   So a stopped caller is never written while it is counted as stopped, which keeps SP-2.

**The alternative, rejected:** making the callee's frames poll every stop word on the thread's call chain. That
costs a poll per back-edge per chain depth, on every callee, for what the blocked mark gets at no cost on the
poll path.

**Witnesses owed:**
- **A stop of the caller while the callee tight-loops** arrives within the tight-loop witness's bound, and the
  caller's memory is unchanged until `Resume`, including after the callee returns. Neuter: no blocked mark, and
  the stop misses its bound.
- **A stop of the caller while the callee calls back into it** parks the agent before any of the caller's code
  runs: a write counter in the caller's memory does not move while stopped. Neuter: no entry poll, so the
  caller's code runs while stopped and the counter moves.

**`Stop`'s documented behaviour.** If `Stop`'s API doc text would change, and not only its internals, that
sentence goes to Scott before merging. `Stop` lives in `internal/interp`, and the root package exposes none.

### As built, 2026-10-18

- **Built:** `invokeSync` runs a sync callee's core invocation and its `post-return` through
  `CanonCaller.Blocking` when a caller waits (`underCallerMark`).
  - **Three paths pass their caller:** ADR 0103's adapter; the flat-word passthrough, which had said *"there is
    nothing to mark"*; and an async lower of a sync lift (#1050), through `invokeWithCaller`. **The first draft
    marked only the adapter path, and its witness still missed**, because a `(u32) -> u32` call never reaches
    the adapter.
- **The entry rule needed no new poll.** `Blocking` ends in `leaveBlocked`, which polls, and a callback's first
  frame polls in `enterFrame`.
  - `Blocking`'s doc rule against realloc and `post-return` inside `fn` is narrowed to the *calling agent's
    own*. The callee's `post-return` runs in another instance.
- **`Stop`'s doc text is unchanged.** It reads *"returns nil when no caller of the instance is executing guest
  code"*, which a caller waiting on another instance's code satisfies. So nothing goes to Scott under the
  ruling's condition.
- **Readings** (janus, tables on #515):
  - stops of a caller whose callee spins arrived in 259ns to 2.9µs (task 57), where the stop had waited for the
    whole callee;
  - the cost is ~ on every row, geomean −0.12% (task 56).
- **Witnesses:** `TestAStopOfTheCallerDoesNotWaitForItsCallee` and `TestAStopOfTheCallerParksACallbackBeforeItRuns`
  (`testdata/callee-gap.wat`; wasmtime 49.0.3 reads `run-spin(3, 10)` = 3 and `run-cb(2, 3)` = 6).
  - **No blocked mark:** misses its bound on trial 0.
  - **No entry poll:** the callback counter moved by about 140 while the caller was stopped.
  - Both neuters were run, and both fail every run.
- **What reading (a) still names as not reached.** This section does not call the reading complete. That is the
  chair's call, and the claim was withdrawn once already. Two stretches are still named here:
  - the table and GC-array bulk instructions, unpolled and unmeasured (the first half's section);
  - straight-line code, bounded by its module's size by ADR 0059's choice.

## 2026-10-19: what the witnesses assert, and what carries the bound (the chair's ruling of that day)

- **The structural witnesses pin the poll interval** (item 1). A witness passing on any mid-copy stop would also
  pass a copy that polled once per 16 MiB. So each now finds where every observed stop landed and asserts two
  things:
  - every stop point is a multiple of the chunk (`canon.StringChunk` for the adapter, `bulkChunk` for fill and
    copy);
  - some point is not a multiple of twice the chunk, so a poll sits at every chunk, not every k-th.
  - **Neuter, run:** each loop's step ×64. Stops still land mid-copy, and the second check is what fails.
  - The witnesses: `TestAStopLandsInsideAnAdapterCopy`, `TestAStopLandsInsideABulkFill`, and the new
    `TestAStopLandsInsideABulkCopy`.
- **Janus is the only reading of the arrival bound.** The witnesses assert where a stop parks, never how
  long it took. So **a PR that touches a poll site re-reads the arrival on janus**: `BenchmarkSP1Arrival`,
  `BenchmarkSP1AdapterArrival` or `BenchmarkSP1AdapterListArrival` for the site's class. Its report reads the
  result against #515's forecast.
- **Reading (b)'s fairness case is a recorded reading, not a gate** (item 2): two goroutines on one agent, one in
  a tight loop, the other required to run.
  - **Known limit:** wasm Go has no time-slice preemption, because the runtime does not run `sysmon` on wasm
    (`haveSysmon = GOARCH != "wasm"`), and (b) does not add one. So the case is **predicted to fail**, with the
    loop checks on and with the import alike.
  - It is recorded when (b)'s build runs it. It gates nothing.

## As built, 2026-10-19: the table and GC-array bulk instructions (item 3), and straight-line code (item 4)

- **Item 3, built.** `table.fill`, `table.copy`, `table.init`, `array.fill`, `array.copy`, `array.init_data`,
  `array.init_elem`, and the element loops of `array.new`, `array.new_default`, `array.new_data` and
  `array.new_elem` run in `elemChunk` pieces (4096 elements) with a poll between them.
  - **Direction.** `table.copy` and `array.copy` keep their direction across pieces.
  - **Images.** Each operation loads its images once. That is #1061 as built and decision 0065's rule, safe
    across a park because `table.grow` refuses to relocate under `soleAgentLocked`.
  - **Where this differs from the ruling.** The ruling's *"view reloaded after each poll"* is not what #1061
    did; #1061's reload draft broke decision 0058's one-load control.
- **Item 3, readings** (janus, before task 58, after task 63; tables on #515):
  - **The fill, copy and init classes meet the forecast:** p99 15–66µs and max 17–120µs, from 0.2–12ms.
  - **The `array.new*` classes stay above 1ms, as forecast.** `array.new` and `new_default` read p99 6.4–7.1ms
    and max about 7.3ms; `new_data` about 3.9ms; `new_elem` (64 Ki) is under 1ms. The remaining stretch is the
    backing allocation: one contiguous `[]gcField` the runtime zeroes as one piece, 112 MiB at 2 Mi elements.
    **Named, not repaired:** repairing it means a segmented array representation, which is its own decision.
- **Item 3, cost:** inside ADR 0059's bar at the third draft (task 62), with geomean −0.16% and worst row
  +1.58%.
  - **Draft 1** was a shared `eachPiece(body func)` and missed, at +6.66% and geomean +4.12% (task 60): the
    closure call on every instruction.
  - **Draft 2** wrote the loops out and still missed, at +5.21% and geomean +2.38% (task 61): a per-element
    bounds check the original's `k < end` had let the compiler drop.
  - **The fix:** each piece is a sub-slice.
- **Item 3, witnesses:**
  - **The seven readable classes** meet item 1's boundary rule, in `TestAStopLandsInsideATableOrArrayBulkInstruction`.
  - **The four `array.new*` classes** cannot show where a stop landed, because the array is unpublished until
    built. So `TestAStopLandsInsideAnArrayNew` counts parks inside one instruction instead. It is coarser than
    the lattice rule, and it is what an unpublished array allows.
  - **Its first draft passed with the poll removed.** Its stops landed before the guest had entered, where every
    `Stop` is satisfied at once. It is now floored on the guest being inside its loop.
  - **Direction:** `TestABulkPieceCopyKeepsItsDirection`.
  - **Neuters, run:** no poll, the step ×64, and a forward-only copy. Every class failed the first two. The ×64
    neuter was re-run on the final loops at four sites.
  - **The witness modules are now validated.** `mustInst` does not validate, and the validator caught two
    invalid drafts.
- **Item 4, read.** The longest poll-free stretch, computed statically (`BenchmarkSP1StraightLine`, a longest
  path over each body's forward control flow), is:
  - **525–532 instructions** in the Go guest corpus (clauses 1 to 3);
  - **592–701** in the Rust fixtures (the four p3 components and `cancel-rust-parent`).

  A straight-line body of that length arrives on janus at **p99 9–37µs and max ≤ 127µs**. **Neither exceeds the
  forecast's max of 1ms**, so the ruling's report-before-building condition is not met.
  - **Why the Go stretches are short:** the Go backend lowers every jump to a `br` to the function's entry
    loop, so every Go basic block ends in a back-edge.
  - **What the reading does not model:** `br_on_cast*` and `try_table` edges, and a test checks the corpora
    contain neither.
