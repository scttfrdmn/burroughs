# 0060 — The futex queue hangs off `memory`, keyed by effective address, because a pointer key would borrow its soundness from another package

Date: 2026-09-02 · Status: **proposed** — no stamp exists to cite, and *a `Status:` field is a citation
to an approval*, so it stays open until one does. Nothing here needs one to proceed: this is mechanism,
which is product work and self-merges on a bound green. What it is **not** is a gate flip — the 67
atomic mnemonics and `shared` are already on their own gate and this document changes none of their
defaults.

Filed against **[#543](https://github.com/scttfrdmn/burroughs/issues/543)**, whose title states the gap
this closes: *"`memory.atomic.wait` cannot return 0 (woken), and its suspend path reports an engine gap
rather than a plausible number."* Deliberated on the issue
([comment 5514746007](https://github.com/scttfrdmn/burroughs/issues/543#issuecomment-5514746007), with a
[correction to §1](https://github.com/scttfrdmn/burroughs/issues/543#issuecomment-5514757560) that this
document adopts). Contract clauses in play: **§2 T-2, T-3** and **§3 SP-2**, with **§4 B-MM-2** and
**B-MM-3** constraining the mechanism.

## Context

`atomicWait` today can produce two of the reference's three results and not the third. Not-equal returns
1; a short timeout returns 2; the *equal with a long or infinite timeout* case returns
`ErrUnsupportedOp` with the reason stated in the message, because both available numbers are lies in the
accept direction — 2 claims a timeout that did not elapse, 0 claims a wake that never happened. The
issue's own diagnosis was *"there is nothing here to suspend"*.

**That premise is now false, and it is the reason this slice is possible rather than a new argument for
it.** [ADR 0059](0059-the-safepoint-poll-is-guarded-at-the-pc-assignment-because-a-back-edge-is-a-runtime-comparison-and-straight-line-code-pays-nothing.md)'s
`world` suspends a guest thread at a safepoint and resumes it with a §4 B-MM-1 edge, landed in
[#591](https://github.com/scttfrdmn/burroughs/pull/591). A futex wait is the second consumer of that
shape, not the first of a new one.

**What no corpus can witness, stated before any of the design.** `atomic.wast` seeds the cell with
`0xffffffffffff` and waits on 0, twice — it takes the not-equal arm both times and never reaches the
equal branch. There is no `.wast` directive that starts a second agent, so **no vector observes a wake,
a queue, a timeout, or a stop composing with a wait**. The threads lane scores `atomic.wast` at 297
pass / 0 fail / 0 unsupported / 0 gated, and every number in that row is already achieved. So the oracle
for this slice is a Go test with two goroutines against one instance, and every clause below that
diverges from the reference is recorded as a decision with its premise stated rather than presented as
conformance.

## Decision

Three, in the order they constrain each other.

### 1 — One mutex per `memory`, a `map[uint64][]*waiter` keyed by **effective address**

`memory` gains a mutex and a map. `wait` locks, compares, enqueues, unlocks, *then* blocks. `notify`
locks, detaches up to N waiters for that key, unlocks, *then* releases them.

**The compare and the enqueue on the same side of one lock is what closes the futex miss.** The hazard
is the classic one — A loads and finds equal, B stores and notifies, A parks, A sleeps through the wake
meant for it — and the reference cannot have it, because a single-agent executor gets `check; suspend`
atomic for free. Here it is not free and the lock is what buys it. A lock-free queue with a per-address
sequence number closes the same race and costs a correctness argument nothing in this tree can check, on
a path entered once per contended wait; declined.

**The release is outside the lock, and that is §4 B-MM-3 rather than a preference.** Releasing a waiter
*is* a guest resume — it is the operation that puts a parked thread back on the dispatch loop — so
doing it under an engine lock is the clause's prohibited shape exactly. This is the correction `Resume`
was put through on #591, and `TestNoEngineLockIsHeldAcrossAChannelOperation` polices it here **without
being extended**: its domain is every non-test file in the tree and its rule is "no channel operation in
a critical section", so the new code is already in scope. That is the re-pointed tripwire doing the job
it was re-pointed for, one slice later.

**The queue hangs off `memory` and not off `memImage`.** `memImage` is what
[ADR 0058](0058-the-memory-image-is-published-through-an-atomic-pointer-because-reachability-is-not-a-spawn-time-property.md)
republishes on a relocating `grow`; a queue living there would be abandoned with the array and every
waiter on it orphaned — [#586](https://github.com/scttfrdmn/burroughs/issues/586)'s first half arriving
in a second place. `memory` also carries the right identity for free: a shared memory spans instances
(ADR 0052) and `memory` is the object they share, so two instances importing one memory wait and notify
on one queue without the queue having to know about instances at all.

**The key is the effective address — a `uint64` — and not the resolved cell pointer.** Both work today;
only one of them keeps its soundness argument inside this package. A pointer key would be valid for the
whole wait *because* `allocate` reserves shared memories and reserving sets `noMove`
(ADR 0056) — but `allocate` returns `noMove = false` when `!lim.Shared || !lim.HasMax`, so the step that
actually excludes the moving arm is `validate.ErrSharedMemoryNoMax` in **another package**, and a
`memory` built by literal bypasses it entirely (grave #579's shape). An integer key makes relocation
*irrelevant* to this arm instead of *excluded from* it: nothing a waiter holds points into the array, a
notify recomputes the same integer from the same operands, and the compare happens once before the
enqueue and is never re-read. `noMove` keeps doing its own job — coherence for plain and RMW accesses,
#586 — and stops being load-bearing here.

**One queue per address, not per (address, width).** `wait32` and `wait64` at the same effective address
share a key, because the proposal wakes *"count waiters waiting on address addr"* and the reference's
notify action carries an address and no type. A width-tagged key would silently decline to wake a
correct program.

Each waiter carries a buffered channel of capacity 1 and the notifier sends on it. Capacity 1 rather
than `close`, because a `close` would panic on a second delivery and the thing that guarantees a single
delivery is the detach under the lock — so the safety would rest on the invariant rather than being
robust to it being wrong. **A detached waiter whose timer has already fired still returns 0.** The tie
between "woken" and "timed out" is resolved by which side won the mutex, and it must resolve toward the
notify, or `notify`'s return count would claim a wake that the waiter reported as a timeout.

### 2 — The reference's `timeout_epsilon` does not survive, and the reason is a premise check

`eval.ml:45` treats any timeout under 1e6 ns as already expired. This engine copied the constant, and
the comment at the copy site states exactly what made it sound: *"with no other agent that reading is
exact rather than an approximation."*

**This slice is what falsifies that premise.** Once a notify can arrive, returning 2 for a 500 µs
timeout reports a timeout that did not elapse and discards a wake that could have arrived inside it. The
constant is an artifact of a non-suspending executor, not a clause of the proposal — keeping it would be
copying a workaround for a constraint we no longer have, which is inheriting a construct's visible
property instead of its load-bearing one. The timeout is honoured for real: negative means never expire
(a nil timer channel, so the `select` has one live case), zero and above run a `time.Timer`.

**This is a divergence from the only authority available on this path, and no vector can arbitrate
it** — see the Context. It is recorded here, and the discriminating case is a Go test, which is the only
oracle there is.

The timer channel satisfies **§3 SP-3**'s disjointness clause incidentally and it is worth naming: *"a
deadline/timer wake facility […] whose delivery channel is disjoint from guest-visible synchronization
state."* A `time.Timer`'s channel aliases no guest memory, so the aliasing SP-3 forbids is impossible by
construction rather than absent by luck — which is that clause's own stated standard.

### 3 — SP-2's `memory.atomic.wait` half rides this slice, and it inverts the arrival protocol

Contract §3 **SP-2**: a thread blocked in `memory.atomic.wait` *counts as at a safepoint*, and the
engine MUST guarantee it cannot touch guest memory until it re-enters through a boundary that observes
the stop. **SP-4**: stopping the world *"with N threads parked in host calls completes without waking
them."*

Together those two forbid the obvious implementation. SP-1's protocol has the *thread* announce its
arrival, but a thread already blocked in a wait when `Stop` runs cannot announce anything, and SP-4
forbids waking it to ask. So the direction inverts: **a blocked thread is counted as arrived by `Stop`
itself.**

- `thread` gains a `blocked` field, guarded by `world.mu` — not an atomic, because both the writer and
  the reader hold that mutex, and the whole point is that the transition and the count cannot interleave.
- Before blocking, the waiter takes `w.mu` and sets `blocked`. If a stop is already in progress it
  parks at the safepoint first, by the existing path, and enters the wait after the world resumes.
- `Stop` counts members with `blocked` set as already arrived and does not wait for them. It still sets
  their `stopReq`, so the wake path parks.
- On wake — from a notify or from the timer — the waiter clears `blocked` under `w.mu` and then polls
  **before pushing its result**. Nothing at all happens between the wake and the safepoint, which is the
  strongest form of SP-2's guarantee and costs nothing to arrange in this order.

Writing this here rather than as a later SP-2 slice is not scope creep: the alternative is a slice whose
whole content is revisiting this function, having first shipped a version of it that makes `Stop` hit its
deadline on any instance with a waiter — `ErrStopDeadline` firing for a thread that is by definition not
running.

## What implementing decision 3 found, and it is two defects in the code it was extending

Both were found by reading `link.go`'s registration site while writing the `blocked` field, not by a
failing test, and neither is in this ADR's design — they are in #591's, which decision 3 extends.

**One `thread` is per *instance*; a caller is per *goroutine* ([#592](https://github.com/scttfrdmn/burroughs/issues/592),
`type:contract`).** `link.go` registers exactly one `thread` per instance, and an embedder may drive N
concurrent `Invoke` calls through it — this engine's own `TestAtomicRmwIsNotObservablyTornAcrossThreads`
does, with N=2. So `blocked` is a **count** rather than a flag: a flag would be cleared by the first
caller to leave a wait while the others were still in one, which is the mark saying *"running"* about a
thread that is not. The count is exact for that shape and does **not** fix the *mixed* one — one caller
suspended and another executing guest code on the same `thread`, where `blocked > 0` lets `Stop` report a
safepoint while guest code runs, SP-2 failing on its own terms. That is not repaired here: the fix is a
per-call execution context, which is #514's subject, and doing a representation change of that size
inside a futex slice is *"moving all of them later, in the PR that can least afford it"* pointed the
other way.

**The arrival buffer was sized by members and filled by callers (grave
[#593](https://github.com/scttfrdmn/burroughs/issues/593)).** `parkAtSafepoint` sent one arrival per
*park* into a channel sized `len(w.members)`, having argued the send could not block because *"the buffer
is `len(w.members)` and each thread sends once per round"* — and deferred the hazard to SP-4's dynamic
membership. Each thread does send once; the **sender is a caller**. Three concurrent `Invoke`s and one
`Stop`: A's send is received, B's fills the one slot, and C blocks on the send forever — before
`<-release`, so `Resume` cannot free it, and `Resume` nils `w.arrived` so nothing ever will. A permanent
hang through the public API, with no gate. Repaired by reporting one arrival per thread per round
(`thread.reported`, set under `world.mu`, cleared by `Stop` when it installs the round). A non-blocking
send is rejected: it cannot hang and it silently *drops* arrivals, which lets `Stop` count two from one
member and report a stopped world with another member running. **A hang is visible; a wrong verdict is
not.**

The reason both belong in this ADR rather than only in their issues is that decision 3 is what made them
reachable in one slice, and a reader who finds `blocked int` and asks why it is not a `bool` needs #592
in front of them.

## Options considered

| | Where the queue lives | Why not |
|---|---|---|
| **A** *(chosen)* | mutex + `map[uint64][]*waiter` on `memory` | — |
| B | lock-free queue, per-address sequence number | Closes the same race for a correctness argument no instrument here can check, on a path entered once per contended wait. |
| C | queue on `memImage` | Abandoned by a relocating `grow`; #586 in a second place. |
| D | queue on `Instance` | Wrong identity. A shared memory spans instances, so two importers would have two queues for one address and a notify would wake half the waiters. |
| E | one global queue keyed by `(*memory, address)` | Works, and puts every memory's waiters behind one mutex — contention across unrelated memories, for no property A lacks. |

And on the wake primitive: a `sync.Cond` per address is the textbook answer and cannot be used, because
it supports neither a timeout nor a `select`, and both are requirements — the timeout from the
instruction's own signature, the `select` from decision 2's timer arm.

## What this does not claim, and the bar that is deliberately not registered

**No performance claim, so no pre-registration.** Scott's narrowing on #515 is the rule being applied:
*"pre-registration attaches to performance claims"*, and the test is whether the mechanism makes one by
existing. SP-1's poll did, on every loop back-edge. This does not: the only *existing* path whose cost
changes is `atomicNotify`, which acquires a mutex it did not before, and which is a synchronization
operation entered once per guest futex call rather than a hot loop. The wait path has no baseline at all
to regress against, because today it returns `ErrUnsupportedOp`.

**§2 T-3's "futex-backed with OS-native wake latency" is not measured here, and the claim is narrower
than the clause.** What this mechanism gives is a Go channel handoff: the waiter parks in the runtime
scheduler, and the notifier's send readies it directly when its M is running and goes through the
runtime's own futex wake when it is not. Neither path is event-loop-turn latency, which is what T-3
forbids. What is *not* yet true is the other half of the same region — T-1's 1:1 OS thread — because
`Spawn` is parked ([#554](https://github.com/scttfrdmn/burroughs/issues/554)), so the agents in this
slice's tests are goroutines against one instance. **A latency number is the claim that would need a
bar, and none is asserted.** Stated because "futex-backed" is exactly the kind of phrase that reads as
discharged once a wait/notify pair works.

## Consequences

- `memory` gains two fields. It already contains an `atomic.Pointer`, so `copylocks` already forbade
  copying the struct and the mutex adds no new constraint on callers.
- `atomicWait`'s `ErrUnsupportedOp` arm is deleted and its doc comment's three-results paragraph is
  rewritten. The `timeoutEpsilon` constant goes with it, and its comment's soundness premise is quoted
  in the ADR above rather than left in the tree as a sentence the code no longer honours.
- `atomicNotify`'s return becomes a real count. Its discarded load stays — it is what raises the
  out-of-bounds trap, which is the reference's own reason for keeping it, and deleting it in the course
  of making the count real would be an accept-direction defect landed inside a correctness fix.
- `notify` on an **unshared** memory still returns 0 without requiring `Shared`, which is the
  reference's behaviour and is unchanged: an unshared memory can have no waiters, so 0 is the true
  answer and not a fast path.
- `Stop`'s arrival count stops being `len(members)`. That is a change to code #591 landed a week's worth
  of reasoning into, so the two blocked-thread orderings get their own tests rather than an argument.
- `thread` gains **two** fields rather than the one this ADR designed: `blocked int` and `reported bool`,
  for the two findings above. Both are guarded by `world.mu`, which is where the transitions they describe
  are already serialized.
- **`world`'s one-instance extent is still the named limit it was in #591**, and this slice does not
  narrow it. A `Stop` on instance A does not park a waiter that entered through instance B on the same
  shared memory, which is SP-4's work behind `Spawn`. Recorded because a wait queue that spans instances
  sitting next to a stop protocol that does not is precisely the asymmetry a reader would otherwise
  assume had been handled.

## Amendment, 2026-09-03 — decision 2's discriminating case was one racy test; it is now two, and the win rate is measured (grave #608)

Decision 2 ends *"the discriminating case is a Go test, which is the only oracle there is."* That was
true, and the test it referred to was `TestAShortTimeoutIsHonouredRatherThanTreatedAsExpired`, which
**had to win a 500 µs race** to render its verdict: fire a notify blind at a sub-epsilon waiter, retry for
up to 10 s, pass if any attempt landed inside the window.

It reddened `main` on `993d883` by losing every attempt on a loaded x86-64 runner, and its `t.Fatalf`
said the engine had reported *"a timeout for an interval that did not elapse"* — an accusation the run
had not observed. A starved runner cannot produce a wrong answer to that question, only no answer.

**The measurement Scott ordered, and it decides the design rather than the budget.** Per-attempt win
rate, darwin/arm64, 2,000 attempts × 4 conditions (idle, under 14 spinners, alongside the spec suite,
and under `-race`):

| form | wins / attempts | rate |
| --- | --- | --- |
| notify fired blind at the waiter (as landed in #594) | 59 / 8,000 | **0.74%** |
| notify fired once the waiter is observed on the queue | 8,000 / 8,000 | **100%** |

The second form is not luckier; it is asking a different question, and the clause that makes it exact is
decision 1's own: **a detached waiter whose timer has already fired still returns 0.** So the notify does
not have to beat the timer — it has to find a queued waiter, which a `Gosched` spin observes at p50 ≈ 9 µs
against a 500 µs window. The 10 s budget was never the number that mattered.

**What replaces it, and why two tests rather than a fixed one.** The epsilon did two separable things —
it returned `waitTimedOut` for an interval that had not elapsed, and it never queued the waiter at all —
so each gets a case:

- `TestASubEpsilonTimeoutIsWaitedAndNotReportedExpired` **carries the verdict** and has no race in it:
  nobody notifies, a timer cannot fire early, and load can only overshoot a one-sided bound. Under the
  epsilon it returns in nanoseconds and fails. This is also the case that shows why
  `TestAnExpiredWaitReturnsAfterItsTimeoutAndNotBefore` never covered the constant: its 20 ms timeout sits
  *above* the 1e6 ns epsilon, so nothing short-circuits there.
- `TestASubEpsilonWaiterIsWokenWhenTheNotifyFindsItQueued` asserts the queue-and-wake half, and **its
  exhaustion is not a failure**: 32 attempts, and if none is observed on the queue in time it logs the
  no-answer with the number of times a waiter *was* seen, because that count is what separates a slow
  harness from the epsilon's signature of never queuing at all.

Both limits in the second case record the population they are set against, and both record the population
they are **not** set against — a loaded x86-64 CI runner, which this tree cannot sample. That is the
standing rule now: *a hard limit is a claim about a distribution, and an uncompared limit is an unasserted
one* (Scott, on the #607 report).

**One clause of decision 1 turns out to have no oracle at all, found by injection while checking that
these two die when they should ([#609](https://github.com/scttfrdmn/burroughs/issues/609)).** Inverting
`resolveExpiry`'s `w.claimed` arm — resolving the tie toward the timer, which is exactly what the sentence
above forbids — survives `go test ./...` over the whole tree. `resolveExpiry` is only reached when the
timer wins the `select`, and every test here arranges for the notify to arrive promptly, so the tie window
is microseconds wide at the far end of an interval no case waits out. The failure message of the second
test names both mechanisms rather than one, because from outside it cannot tell them apart.
