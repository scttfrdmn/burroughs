# 0052 — The §4 boundary edge is one package-level sequentially-consistent counter, because a shared memory spans instances

Date: 2026-08-31 · Status: **accepted**, **stamped by relay** — Scott, on the #647 review:
*"Record the relay stamps on 0050, 0052 and 0060 citing my prior report — the stamp was given last turn; this is the recording."* The independence
mechanism is his, stated on the #646 review: *"I reviewed each in the report that landed it."* For this ADR
that report is **PR 564**.

**What this citation is, and what it is not.** It resolves to a principal's approval and to the PR whose
report carried this document to him, which is what the `Status:` rule asks for. It does **not** resolve to a
GitHub review artifact: the review was a session turn and the recording is made by the actor who was
reviewed, so *durability is not independence* and the commit that records it is `Ratio-Class: carried`. That
is stated here rather than left for a reader to discover, because a forged provenance about the project's own
governance is worse than a wrong option.

The **scheduling** was stamped earlier and separately, and is left recorded because it is what put this ADR
in its place in the chain: Scott's option-1 ruling on [ADR
0050](0050-the-per-thread-context-is-its-own-object-reached-by-one-pointer-on-stack-because-3-and-5-need-more-per-thread-state-than-a-slot.md)
and in `internal/interp/thread.go` — *"discharge #542 first — #542 → #516 → #10"* — with #542
discharged by [ADR
0051](0051-the-atomics-become-sequentially-consistent-word-operations-over-the-backing-array-because-the-proposal-fixes-the-ordering-and-leaves-only-the-mechanism.md).
This is #516, the middle link.

Filed against **#516**. One ADR, one implementation.

## Context

Contract §4's three clauses this decision is the mechanism for, verbatim:

> - **B-MM-1.** Every host→guest transition — host-call return, trap resume, async wake,
>   stack-switch resume — MUST constitute an **acquire edge over the entire shared address space**
>   for the resuming agent. Every guest→host transition MUST constitute the corresponding release
>   edge. Equivalently: a sequentially-consistent fence at the boundary, both directions.
> - **B-MM-3.** The engine MUST NOT hold engine-internal locks across a guest resume, and MUST NOT
>   resume a guest agent in a state where a previously acquired guest lock is held without the
>   acquire edge of B-MM-1 having been established. […] the contract closes it for every field, not
>   per-field.
> - **B-MM-4.** Each host call's memory-publication semantics MUST be documented in its signature.
>   The default, absent annotation, is sequentially consistent.

B-MM-2 is not here: it needs a wake, so it needs `memory.atomic.notify` and the 0xfe region's
wait/notify half (**#512**). B-MM-5 is **#10**, the battery.

### What the boundary is today, derived rather than listed

The clauses name four host→guest transitions — host-call return, trap resume, async wake,
stack-switch resume — and **the engine has none of them.** That is not a reason to defer: it is the
scope, and it was measured rather than assumed.

- **No host call exists in either direction.** `Extern`'s function arm is `owner *Instance` plus
  `fnIdx uint32` (`internal/interp/link.go`), so an import is satisfied only by *another wasm
  instance's* function. There is no host-function type in the tree: `HostFunc`, `hostFunc` and `type
  Host` match nothing in any non-test `.go` file. A guest cannot call out.
- **No engine-internal lock exists.** `sync.` appears once in the engine, in a comment about
  amortization (`internal/interp/ends_table.go`). B-MM-3 therefore has no lock to constrain.
- **No second agent exists.** No `go` statement in any non-test file, which is what
  `TestPlainAccessesAreUnsynchronisedWhileTheInterpreterIsSingleThreaded` keeps true; T-1's `Spawn`
  is parked in **#554**.

So the transitions that exist are the ones a *host Go caller* makes into `internal/interp` and back,
and the derivation that matters is that **entering the interpreter is the same event as creating a
stack**. The three non-test `stack{…}` literals are exactly the three execution entries —
`runConst` (`constexpr.go`), the start function and `invokeIndex` (`interp.go`) — and they are
already a derived, parsed population, because `TestEveryStackCreationSiteCarriesAThread` derives it
for 0050's per-thread context. The enclosing functions are `internal/interp/constexpr.go`'s
`runConst`, `internal/interp/interp.go`'s `build` — the start function's literal is there, not in
`InstantiateLinked` — and `internal/interp/interp.go`'s `invokeIndex`. Two entries touch mutable guest
state without creating a stack: `internal/interp/link.go`'s `InstantiateLinked`, whose `link` fills
import slots and whose data- and element-segment copies write guest memory outside any interpreter
run, and `internal/interp/interp.go`'s `Instance.Global`, which reads a global's storage directly.

Five sites, and none of them is on the list §4's prose names. **The transitions §4 enumerates are
the ones a threaded engine has; the transitions this engine has are the same boundary at a smaller
radius**, and B-MM-1 is written over "every host→guest transition", not over its own examples.

## Options

### Where the shared location lives

**A. One word per `Instance`.** Cheapest — it rides an allocation the constructor already makes, and
two instances never contend. **Rejected, because it has a hole that opens exactly under the
concurrency v1 is building.** A shared memory is imported, so two instances can hold the same
`*memory`; two agents over one address space would then release and acquire on *different* words and
no edge would exist between them. Writing the version that is correct for today's tree and wrong for
#554's costs the same as writing the correct one.

**B. One word per shared object** — per memory, per table, per global. Closest to "the entire shared
address space" read as though the edge were per-datum. **Rejected on the memory model, not on cost.**
Go's `sync/atomic` release publishes *every* prior write of that goroutine, not the writes near the
word; the location's identity decides only *who can observe whom*, never *which memory is covered*.
So per-object words buy nothing over one word, and they cost one atomic per import per crossing.

**C. One package-level word.** Every crossing anywhere observes every other crossing anywhere, so the
edge exists between any two agents regardless of instance or of which memories they share. **Chosen.**
Its cost is a single contended cache line once agents cross concurrently — a real cost, stated in the
consequences and filed rather than solved, because this phase has one agent.

**D. Nothing — rely on the guest atomics.** ADR 0051 made the 67 atomics sequentially consistent word
operations, so one might argue the ordering is already there. **Rejected.** 0051 orders accesses *at
one address*; B-MM-1 is over the whole space at a transition, which is precisely what an atomic on one
cell does not give. And plain guest accesses are still unsynchronised byte loops (**#557**).

### What operation establishes it

**i. A release-store leaving, an acquire-load entering.** The minimum. Conforming — Go's memory model
makes `sync/atomic` operations sequentially consistent — but the entry side is a load whose value is
discarded, which is invisible to a reader, invisible to `deadcode`, and **invisible to any test.**

**ii. A read-modify-write in both directions.** Chosen, for a reason that is about testability rather
than about ordering. An RMW is unambiguously both halves under either reading of B-MM-1 — the
acquire/release one and the "sequentially-consistent fence, both directions" gloss — so no argument
about which flavour Go gives is load-bearing. And incrementing makes the word a **count of
transitions**, which is the only thing about B-MM-1 that a single-agent tree can assert at all.

**iii. `sync.Mutex` Lock/Unlock around each transition.** Rejected on sight: the mechanism for B-MM-1
must not be the construct B-MM-3 exists to forbid, and it would serialise every crossing.

## The decision

One package-level `atomic.Uint64` in `internal/interp`, incremented on entry to and on exit from each
of the five boundary sites. B-MM-3 gets a tripwire rather than a check, and B-MM-4 gets a stated
default rather than an enforced one; both of those are argued below, because in each case the honest
deliverable is smaller than the clause's word count suggests.

### The count is a presence oracle and not an ordering oracle

Stated first because it is the claim most easily overread. The counter witnesses that **the operation
ran at every site** — delete a crossing and a delta comes out short. It does not witness that a write
before a release is visible after an acquire; nothing running on one architecture with one agent can.
That is B-MM-5's job and **#10**'s battery, on a TSO *and* a weakly-ordered platform, and this ADR
hands it a mechanism to test and a counter to assert against rather than a claim to trust.

### B-MM-3 is satisfied vacuously, so what lands is a tripwire

There are no engine-internal locks, so "MUST NOT hold engine-internal locks across a guest resume" is
true of the tree by having no subject. A test asserting it today would be *an analytic zero* — it
could not have come out otherwise — and this ADR does not pretend otherwise. What is worth building is
the thing that fires when the subject arrives: a control over the package's non-test sources that
fails on the first `sync.Mutex`, `sync.RWMutex` or `sync.Locker` declaration and carries the
instruction in its failure message. Its green today says nothing; its *failure* later says the one
thing a reader will need. It was watched die by injection, the method #561 paid for.

### B-MM-4's default makes absence conforming, so presence cannot be enforced

B-MM-4 says the semantics are documented in the signature and that **the default, absent annotation,
is sequentially consistent.** A control demanding an annotation at every site would therefore be
*stricter than the contract*: an unannotated boundary call is conforming and means SC. So the
deliverable is the convention itself — the default written where a reader of the boundary will find
it, and an annotation form defined (`// Publication: …` in the doc comment) so that the first call
with non-SC semantics has a spelling to use rather than inventing one under pressure. There is nothing
to validate until one exists.

**Two readings of "host call", and this ADR takes the wider one.** Narrowly it means a call the *guest*
makes out into host code — §3's host-import surface, which does not exist. Widely it means any call
that crosses the boundary, which today is the five sites. The rule is stated over the derived
population "every call that crosses the boundary", which serves both: today that is the public entry
methods, and when §3's surface lands host functions join the population with no rule change. This is
an interpretation of normative text and not an amendment to it, so it is flagged for Scott and
chat-Claude rather than decided quietly — and it blocks no code either way, which is why it is a note
here and not a held PR.

## The pre-registration, written before the numbers exist

**The instrument already exists**: `internal/interp/scanbench` drives `burroughs.Instantiate` and
`Instance.Call` through the real decoder and the real dispatch loop, and 0050 used it for the same
kind of question. *A pre-registration forecasts the instruments*, and re-measuring found nothing to
build.

**Most of the board cannot resolve this question, and saying which rows can is the point.** Two atomic
RMWs are single-digit nanoseconds uncontended. Every `Call`-driven row runs 30µs to 2.2ms, so the
mechanism is four to five orders of magnitude below the measurement — a `~` there is *an unasserted
distance*, which is the specific way 0050's forecast 1 passed while saying little. The one row within
three orders of the mechanism is **`Instantiate/funcs=1/openers=1` at 1423 ns/op**, and it is the row
0050 found sensitive enough to attribute +1.57pp to a struct allocation. So:

**The crossings are counted before they are priced, and the count moved the forecast.** *A count is not
a price — decompose by mechanism.* `scanbench`'s `modFuncs` builds functions and nothing else — no
global, no memory, no segment, no start — so `runConst` is never reached and all three Instantiate rows
cross exactly **four** times: `InstantiateLinked`'s pair and `build`'s pair, fixed, independent of
`funcs` and `openers`. That is the mechanism `TestEveryBoundaryCrossingIsPaired` pins per site, not a
count inferred from reading the source.

**This kills the failure mechanism the section was first drafted with, and the correction is recorded
rather than swapped out, because the ordering is the only thing that distinguishes narrowing a forecast
from amending a threshold having seen the number** — and no number exists yet. The draft said the row
would move if instantiation's crossings were "many rather than few", on the strength of `runConst`
running once per global initializer and once per active segment offset. Two things are wrong with it.
The instrument has **no initialiser-bearing row at all**, so that mechanism is unmeasurable here; and
it points the wrong way — a `runConst` crossing is two RMWs against a whole const expression evaluated
through the dispatch loop, so the per-initialiser overhead is proportionally *smaller* than the fixed
cost. The sensitive case is the **cheapest** instantiate, not the busiest.

- **The forecast, and it can fail:** `Instantiate/funcs=1/openers=1` moves less than **2%** at
  `-count=10`, benchstat, arms interleaved. The mechanism that can fail it is named and is the only one
  on that row: **four atomic read-modify-writes against a 1423 ns/op baseline.** At single-digit
  nanoseconds apiece that is 0.6–1.4% of the row — inside the ceiling, and near enough to it that a
  slower atomic than assumed, or a fifth crossing nobody accounted for, comes out as a fail. That is
  deliberately unlike 0050's forecast 1, which passed at 1.32% against 2% through a mechanism that
  could not have reached either number.
- **What this instrument cannot ask.** No `scanbench` row instantiates a module with a global
  initializer or an active segment, so the *per-initialiser* crossing is priced by nothing in the tree.
  Stated rather than left as a gap nobody named; the paragraph above argues it is the cheaper half, and
  an argument is not a measurement.
- **The rollback, stated now so it is not invented later:** if that row exceeds 2%, the crossing
  becomes depth-aware — a nesting count on `thread` (0050's object, which exists for exactly this
  class of per-thread state), so that only the *outermost* entry and the matching exit touch the
  atomic and a `runConst` inside an instantiation is a plain thread-local increment. That is the
  semantically tighter model anyway — an agent already inside the guest is not transitioning — and it
  reopens neither the location choice nor the operation choice.
- **A forecast beaten is a forecast falsified.** If the Instantiate row comes out at `~` I will say
  which mechanism could have moved it and why it did not, rather than banking the pass.

## The result, and the pass is weaker evidence than the ceiling implies

Ten interleaved rounds, whole board, `-count=1` per arm per round so run order is not perfectly
correlated with the arm (grave #552), arms compiled up front and hash-checked distinct. On the forecast
row:

```
Instantiate/funcs=1/openers=1-12     1.369µ ± 2%   1.369µ ± 2%   ~ (p=0.643 n=10)
Instantiate/funcs=200/openers=1-12   186.1µ ± 2%   184.3µ ± 3%   ~ (p=0.315 n=10)
Instantiate/funcs=200/openers=0-12   63.23µ ± 2%   62.80µ ± 9%   ~ (p=0.631 n=10)
geomean                              89.12µ        89.03µ        -0.10%
```

**The forecast passes and the rollback is not triggered.** No row moved beyond the ceiling; the geomean
moved -0.10%.

**Discharging the clause above: which mechanism could have moved this row, and why it did not.** The
named mechanism is four uncontended `atomic.Uint64.Add`s. On arm64 that compiles to `LDADDAL`, and with
one goroutine the line is already exclusive in L1, so the realistic cost is ~1–2 ns each: **4–8 ns
against 1369 ns, i.e. 0.3–0.6%.** The registered estimate was 0.6–1.4%, and its low end sat *under* the
row's own ±2% spread. So a `~` on this row is consistent with the mechanism costing 0.3% and equally
consistent with it costing 1.5%, and **the ceiling was not tight enough to distinguish them.** The
registration was honest about the row being the sensitive one and wrong about it being sensitive
*enough*; what would actually have failed it is a fifth crossing nobody counted (which
`TestEveryBoundaryCrossingIsPaired` independently rules out at 6 per instantiate, 4 of them on this
path) or a contended atomic, and **no `scanbench` row runs two goroutines**, so contention is unpriced
here exactly as the rollback's own note says.

**The noise floor is measured rather than assumed, and the mechanism's sign is what measures it.** Four
rows came out significant and small: `Decoupled/span=5` -0.49%, `PadOnce/distance=0` -0.37%,
`PadOnce/distance=64` -0.60%, `PadOnce/distance=4096` -0.49%, against `Decoupled/span=276` **+1.49%**.
A crossing can only ever *add* work, so **a negative movement cannot be this mechanism** — which puts a
floor of roughly 0.5% on what this instrument can attribute at all, and makes the lone positive row's
+1.49% unattributable in the same breath rather than reported as a cost. That floor is above the
predicted effect, which is the same finding as the paragraph before it arrived at from the other side.

**A level shifted between sessions and the deltas did not.** The pre-registration quoted 1423 ns/op for
this row from an earlier session; this run's base arm reads 1369 ns/op, ~4% lower. Nothing in the engine
explains it and nothing needs to: *correlated errors preserve deltas*, and the two arms here ran
interleaved on one machine within one script, which is why the comparison survives a wandering absolute.
It is also the concrete reason the arms could not have been measured on separate occasions — a 4%
session drift would have swamped a 0.3% mechanism outright.

## Consequences

- **Every new interpreter entry point must cross the boundary**, and this is enforced structurally
  rather than remembered: the control over non-test `stack{…}` literals now asserts that each
  literal's enclosing function crosses. The population is derived by parsing, so the *next* entry
  point is in the domain the day it is written.
- **#554's `runEntry` is that next entry point**, and it is named here so the requirement is not
  discovered during the merge: a spawned thread's first entry into the guest is a host→guest
  transition and needs the crossing like any other.
- **The word is process-global and never reset**, so every assertion about it is a **delta**. An
  absolute is meaningless and a test asserting one would pass or fail on what ran before it. No test
  in this tree calls `t.Parallel()`, which is what makes a delta deterministic; if one ever does, the
  deltas in `boundary_test.go` are among the things it breaks.
- **Contention is the accepted cost and it is not yet measurable.** One cache line for every crossing
  in the process is a scalability question that needs concurrent agents to ask, so it arrives with
  #554 and is filed rather than pre-solved. The escape hatch, if it ever comes to that, is the same
  depth-aware crossing the rollback describes — it reduces crossings, which is the numerator.
- **#10 gains its subject.** The battery is no longer a test of a claim; it is a test of five named
  sites and one named word.

## Postscript — the control cited in the "no second agent" derivation was renamed

Appended, not edited: the derivation above is what was measured when this decision was taken, and it
still holds. Only the name has moved.

The measurement *"no `go` statement in any non-test file, which is what
`TestPlainAccessesAreUnsynchronisedWhileTheInterpreterIsSingleThreaded` keeps true"* is unchanged in
substance; that control is now `TestNoEngineGoroutineLandsWithoutAPrincipalsRuling`, same scan and
same vacuity floor. It was renamed because both remedies its failure message named — **#557**,
discharged by [ADR
0054](0054-every-aligned-guest-access-becomes-atomic-on-the-address-already-resolved-because-a-scoped-gate-is-unavailable-rather-than-unwritten.md),
and **#516**, discharged by this decision — were done, so its name asserted a property the code no
longer had (grave **#576**). It now names **#10**, **#543**, **#573** and **#575**, and this ADR is
one of the two discharges that made the old name stale.
