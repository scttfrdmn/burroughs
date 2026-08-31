# 0050 — The per-thread context is its own object reached by one pointer on `stack`, because §§3–5 need more per-thread state than a slot

Date: 2026-08-30 · Status: **proposed** — no stamp exists to cite yet, and *a `Status:` field is a
citation to an approval*, so it stays open until one does. Scott's order sequencing this work is
in-session and therefore unciteable: *"the real v1 work is next, not after. Spawn, the per-thread
slot, safepoint checks at back-edges and call sites, stop composing with parked threads."*

Filed against **#514**, which scopes it: contract §2 **T-1** (`spawn(entry_func, arg, stack_hint) →
tid`, 1:1 with an OS thread) and **T-4** (a per-thread slot readable at register-like cost, the `g`
register analog, stable across host calls and stack switches). One ADR, implementation attached.

## Context

`internal/interp` has no per-thread execution context at all. The receiver on the hot path is
`*Instance`, which every thread of a shared-memory module shares *by definition*, so it cannot hold
anything per-thread. What it does have is a parameter already threaded through every function on the
path — surveyed on
[#514](https://github.com/scttfrdmn/burroughs/issues/514#issuecomment-5473525632), and the two figures
that matter are these:

```
st *stack is a parameter of all four:  run, runFrame, enterFrame, invoke
runFrame / enterFrame:                 2 call sites each
stacks are created at exactly 3 sites: constexpr.go (instantiation), interp.go (start fn, invokeIndex)
                                       — none of them inside the dispatch loop
```

`invoke` takes the caller's `st` rather than making one, which is why the third figure is 3 and not
one-per-call. So a field on `stack` would cost **zero** new parameters and **three** propagation
sites: by a long way the cheapest option on plumbing, and the one this ADR argues against.

## Options

### A — A `*thread` parameter threaded through `run`/`runFrame`/`enterFrame`/`invoke`

The read is a parameter in a register, which is T-4's wording almost literally, and a function that
forgets it does not compile. Rejected on a smaller ground than it deserves: `runFrame` already takes
six parameters, and every future function on the path inherits a seventh whose only job is to be
passed on. The decisive objection is that it does not help the case that actually breaks — a *new
stack* created inside one thread (§7) has to be given the right thread either way, and a parameter
makes that a call-site obligation rather than a data-structure invariant.

### B — A field on `stack` directly: `st.slot`

Cheapest, and one dereference off a pointer already in a register. Rejected, and the reason is what
this ADR is for: **`stack` is per-invocation, and T-4 asks for per-thread.** Today those coincide
because a wasm call reuses the caller's stack. §7 (v2) adds growable continuations — stacks created
*within* a single thread — and at that point a slot living on `stack` is per-continuation. The failure
mode is the disqualifying part: a fresh stack that forgets to copy the slot holds a **zero value that
looks like a legitimate first thread**, so the bug is a plausible wrong answer rather than a crash.
That is the shape this project has a grave for in three other places.

### C — A `thread` object; `stack` carries a `*thread` (chosen)

The identity and the per-thread state live in one object, and `stack` holds a pointer to it. Three
properties, in the order they mattered:

1. **Propagation becomes idempotent.** Copying a pointer cannot half-happen, and a stack that forgets
   holds `nil` — a visible panic on first use, not a plausible tid. Correctness by construction where
   B offers correctness by convention.
2. **§§3–5 need somewhere to put more than a slot, and it is not `stack`.** SP-1's stop/epoch flag is
   checked at back-edges and call sites; SP-2 needs an "in a host call, therefore at a safepoint" bit;
   T-3 needs a futex parking token. Every one of those is per-*thread*, and every one of them would be
   per-*continuation* on `stack` once §7 lands. Choosing B now means moving all of them later, in the
   PR that can least afford a representation change.
3. It gives #12 a place to be *absent* from. Thread exit/join/detach are contract §10.3, still open,
   and a `thread` object with no lifecycle field states that gap where a reader will meet it.

Cost: one extra indirection on the read, `st.t.slot` against `st.slot`. **Whether that is still
"register-like" is a measurement rather than an argument**, and it is pre-registered below.

### D — A map on `Instance` keyed by OS-thread or goroutine identity

Rejected on T-4 alone: pure Go exposes no cheap goroutine identity, and a map read — with or without a
lock — is not register-like by any reading of the word. Recorded because it is the obvious shape for
anyone coming from a runtime with thread-local storage, and the reason it loses here is the *pure Go*
constraint rather than anything about threads.

## The pre-registration, written before the numbers exist

**The instrument already exists and no new one is built.** `internal/interp/scanbench` drives
`burroughs.Instantiate` and `Instance.Call` — the real decoder and the real dispatch loop, not a
replica — so its rows are the baseline. This is recorded because the first draft of this slice budgeted
a new benchmark harness: *a pre-registration forecasts the instruments*, and re-measuring the gap found
one of the four bench packages already covering it. The other three (`dispatchbench`, `dropbench`,
`vecbench`) measure reimplemented shapes and are the wrong authority for a cost on the real path.

**What is measurable now, and what is not.** Nothing on the hot path *reads* the slot in this slice —
the first reader is #515's safepoint check. So a benchmark comparing `st.t.slot` against `st.slot`
today would compare two fields neither of which is read, which is an analytic zero: it could not have
come out any other way. Stated rather than performed.

So two forecasts, one for now and one for the slice that can falsify it:

- **Now, and it can fail:** carrying a `*thread` on `stack` moves no `scanbench` row by more than
  **2%** at `-count=10`, benchstat. The field is carried and not read, so any movement is layout,
  allocation, or cache — and a real one would mean `stack`'s size matters more to the dispatch loop
  than this ADR assumes, which is a finding either way.
- **At #515, when the read lands on the hot path:** the two-dereference form costs less than **5%**
  against a one-dereference form on the same rows. **Rollback if it exceeds that**, stated now so it
  is not invented later: hoist the *flag being read* into a `stack` field refreshed at thread entry and
  at every safepoint, leaving identity on `thread`. That trades one indirection for a write on a cold
  path and does not reopen this decision.

A forecast beaten is a forecast falsified. If the 2% comes out at 0.0% on every row I will say so and
ask why, rather than bank it.

## The result: forecast 1 passes, and the instrument failed before the forecast could

**Forecast 1 holds.** With the arms interleaved, no `scanbench` row moves by more than **1.32%**
against the registered 2% ceiling:

| arm | worst row | geomean |
| --- | --- | --- |
| `host thread` by value (**landed**) | `PadOnce/distance=64` **+0.94%** (p=0.012) | **−0.05%** |
| `host *thread` by pointer | `PadOnce/distance=64` **+1.32%** (p=0.000) | **+0.45%** |

Every other row in both arms is `~` at n=10.

**The forecast passed, and 0050 committed to asking why rather than banking it.** The answer is that
the ceiling was never within reach of the mechanism it was aimed at. `stack` grows by eight bytes and
is allocated once per `Invoke`; the benchmark bodies run 30µs to 2ms. For a carried-and-never-read
word to move any of them 2% would have required `stack`'s width to dominate the dispatch loop, and
nothing in the design suggested it could. So this is *an unasserted distance*: a bound far enough
from what it bounds that it ran, agreed, and said little. A forecast that could only have failed
through a mechanism it did not name is a weak forecast, and forecast 2 — the two-dereference *read*
at #515, against a 5% ceiling on the same rows — is the one that carries the real risk.

### What actually failed: three rounds of measurement, from run order (grave **#552**)

This is recorded in full because for three rounds the tree said forecast 1 was falsified, and every
figure was an artifact.

The reported sequence was: `Instantiate/funcs=1/openers=1` **+2.73%** (p=0.000), reproduced at
**+3.10%**; decomposed on that row into the `thread` struct's allocation **+1.57pp** and T-1's
`done` channel **+1.53pp**; repaired by embedding `host` by value, which returned the row to `~`.
Then the full board on the repaired tree showed `Decoupled/span=276` **+6.64%**,
`Entries/distance=512` **+6.98%** and `Entries/distance=4096` **+3.86%** — a repair apparently three
times worse than the defect.

**The arms had been run sequentially: all of A, then all of B.** That makes run order a confounder
perfectly correlated with the arm. It was caught by measuring the *baseline* twice, fifteen minutes
apart, with identical code at `5387682`:

| row | first run | second run | drift, same code |
| --- | --- | --- | --- |
| `Entries/distance=4096` | 2.239m | 2.442m | **+9.1%** |
| `Entries/distance=512` | 335.1µ | 362.8µ | **+8.3%** |
| `Entries/distance=64` | 91.01µ | 96.81µ | **+6.4%** |
| `Decoupled/span=276` | 188.8µ | 196.5µ | **+4.1%** |

The baseline drifted further than the effect being attributed to the change, and the same landed tree
reads **+1.16%** geomean against one baseline and **−1.07%** against the other. The sign of the board
was a function of which run was called *old*.

**The repair.** Arms are compiled to binaries up front — so nothing builds during measurement and the
tree is never mutated mid-run — and one round runs every arm at `-count=1`, ten rounds. Thermal drift
then hits all arms alike instead of loading onto whichever ran last. Two checks ride along: the three
binaries are confirmed distinct by hash, because identical arms would agree perfectly and say nothing,
and each round prints its per-arm row count.

Three things worth keeping:

- **A repair validated on one row is not validated.** The `+1.57`/`+1.53pp` decomposition and the
  by-value repair were measured on `Instantiate/funcs=1/openers=1` alone. The other 23 rows were
  *unmeasured, not unmoved* — and when they were finally measured they appeared to have blown up.
- **The high-variance rows cannot resolve this ceiling at all.** On the interleaved board
  `Decoupled/span=276` carries ±21%, `Entries/distance=512` ±17%, `Entries/distance=4096` ±19%. A 2%
  question put to a row with ±20% spread gets an answer about the room. Stating this is the honest
  bound on what forecast 1's pass covers; forecast 2 needs either more rounds or a quieter machine,
  and it should not be checked on these four rows.
- **Nothing decides between by-value and by-pointer on performance,** so the choice is made on design
  and said to be: by-value rides `Instance`'s own allocation, so there is one object fewer, and it
  cannot be nil on any instance the constructor built. Option C is untouched either way — what
  `stack` carries is one pointer in both forms.

## Consequences

**The four bullets below are about T-1's `Spawn`, which this decision's implementation does not
land.** They are stated in the present tense because they are this decision's commitments about
`Spawn` whenever it lands, and a reader must not take them as claims that the code is in the tree —
it is not. `Spawn` exists and lives in **#554**, a PR parked unmerged and deliberately red, withheld
because `TestAtomicsArePlainWhileTheInterpreterIsSingleThreaded` fires on the first `go` statement in
`internal/interp` and instructs the reader to discharge **#542** rather than exempt the file. #542
prices its discharge as §4's litmus battery (**#516**, **#10**), which is work this slice was
sequenced ahead of. That looked like a cycle in the phase's order, so it went to Scott rather than
being resolved here; neither evasion was taken — no exception-list entry, and `Spawn` was not moved to
a sibling package to put the `go` statement outside the control's domain, since *an exemption inherits
none of the trigger's lessons.*

**The ruling was option 1: discharge #542 first, #542 → #516 → #10**, reversing the in-session
ordering that had put spawn ahead of §4's model. It was not in fact a cycle, which is worth recording
because the appearance of one is what escalated it: the atomics repair and §4's boundary edges need no
second thread to *write*, only to test at scale, so the order runs repair → edges → the tripwire's
premise becomes false and it is retired → T-1 merges → #10's battery runs against it. Nothing in that
chain waits on itself. #554 also carries the measurement that decided the refusal of an override: two
threads × 2000 atomic adds on one cell yield 3392 rather than 4000, with the race detector naming the
read in `atomic.go` and the write in `memory.go`.

- **`Spawn` is an engine API and not a wasm instruction.** The threads proposal defines no spawn
  opcode; T-1 says *host primitive*, and *"this is `newosproc`, not a Worker with a message port."* In
  pure Go, 1:1 with an OS thread is a goroutine that calls `runtime.LockOSThread` and never unlocks —
  a goroutine exiting while locked terminates its thread, which is the semantics T-1 asks for.
- **The lifecycle gap is stated, not filled.** T-5 requires exit/join/detach *defined in the contract*
  (§10.3, **#12**). Nothing here answers it, and the `thread` object deliberately has no lifecycle
  field: code that quietly picked a behaviour would be answering an open contract question in the one
  channel that gets no review.
- **Spawn does not make shared memory safe, and the report says so.** The 67 atomics are plain reads
  and writes (**#542**) and `memory.atomic.wait` cannot return 0/woken (**#543**). Both become
  *observable* the moment a second thread exists — before this slice a lost update was unobservable
  rather than absent. §4's memory model is **#516** and its litmus battery **#10**. So the tests here
  are guests that touch per-thread state only, and a shared-memory race is a known gap with three
  filed numbers rather than a surprise.
- Nothing defaults on: `Spawn` refuses unless the instance's features carry `Threads`, so the
  capability rides the same gate as the rest of the proposal (behaviour 4, contract §9).
