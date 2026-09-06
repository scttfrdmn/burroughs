<!-- Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0 -->

# 0074 — `Stop` waits on SP-1's own predicate over the caller marks, because an arrival is a caller and the protocol named neither end of it

Date: 2026-09-06 · Status: **proposed** — no stamp exists to cite, and *a `Status:` field is a citation to
an approval*, so it stays open until one does. Nothing here needs one to proceed: this is mechanism, which
is product work and self-merges on a bound green. It changes no gate's default, adds no public signature,
and needs no §3 amendment — SP-1's own sentence is what it starts asserting.

Filed against **[#656](https://github.com/scttfrdmn/burroughs/issues/656)**, whose body proposes two
repairs. Both are refuted below by a witness measured after it was filed, and the refutation is the reason
this ADR exists rather than a one-field patch.

## Context

`internal/interp/safepoint.go:Instance.Stop` implements §3 SP-1 by counting tokens. It walks the live set
under `world.mu`, decides how many threads will *send* an arrival, and then receives that many times:

```go
arrived, want, atSafepoint := w.arrived, 0, 0
for _, t := range w.live {
    t.stopReq.Store(true)
    t.reported = false
    if t.blocked == t.callers { atSafepoint++; continue }
    want++
}
...
for i := range want {
    select {
    case <-arrived:
    case <-timer.C: return fmt.Errorf("%w: %d of %d arrived within %s", ...)
    }
}
```

`world.arrived` is a `chan ThreadID` and the id is dropped on the floor. `thread.reported` makes each
*thread* send at most once per round — grave #593's repair for a hang, where three callers sharing one
`thread` overflowed a buffer sized from the membership.

**So `want` counts threads, the senders are callers, and the receive counts neither.** Four reachable
shapes follow, and all four are the same sentence: *an arrival is a caller, and this protocol names
neither the caller that must arrive nor the caller that did.*

| # | Shape | Measured |
|---|---|---|
| A | An awaited thread **exits** inside the round. Nothing sends, and `world.retire` deliberately does not. | `ErrStopDeadline: 1 of 2 arrived within 2s` after 2.0011575s, for a thread already exited (#656) |
| B | A thread counted as arrived by SP-2 **wakes** inside the round, parks, and its send fills a *running* thread's slot. | `Stop(2s) -> nil` after 911µs with the runner at `callers=1, reported=false` (#656) |
| C1 | Two callers on **one** thread: the blocked one wakes and its arrival fills its **sibling's** slot. | `Stop(5s) -> nil` after 200.9ms while the running caller's last guest instruction had not run — 6 of 6 |
| C2 | One caller **returns** from the guest. `leaveCall` is not an arrival either. | `ErrStopDeadline: 0 of 1 arrived within 3s` after 3.0s, with the caller's last instruction *already run* — 6 of 6 |

C1 and C2 are new here, and each says something the filed pair does not.

**C1 refutes both of #656's proposed repairs, and it refutes them by identity rather than by degree.** Its
two callers are on the same `thread`: one id, one `reported` flag, one row in `live`. Waiting on *thread*
identities cannot tell the sibling's arrival from the runner's, and an `awaited` *mark* set per thread is
true for both of them at once. The state is `blocked=1 callers=2` — exactly the mixed shape
[ADR 0067](0067-a-caller-count-joins-the-blocked-mark-because-sp-2s-predicate-is-about-callers-and-a-thread-is-not-one.md)
was written for, one level further in: 0067 fixed *which threads to await* and left *what an arrival is*
alone.

**C2 is reachable with no gate at all**, which moves #656's blast radius. It is one `Invoke` of a function
whose tail has no back-edge, one `Stop`, an **unshared** memory, no atomics and no `Spawn` — so the false
expiry is available on the engine as it ships today, not only behind `gate:threads`. The issue's own
sentence *"it blocks the `gate:threads` flip"* is true and is not the whole bound.

**The window is written, not raced.** `poll` runs at back-edges, `enterFrame` and tail calls, so a guest
body with none of those is an unpolled stretch of whatever length it is written to. Every measurement above
uses the same lever: 40–60 × `memory.fill` of 4MiB, ≈0.6–1.0s of un-pollable guest code under `-race`. That
is why these are witnesses rather than flakes, and it is 0067's own stillbirth lesson reused — a
three-instruction loop body passes on the broken engine because the guest has already parked.

## Options

**A — a per-round `awaited` mark, gating the send.** #656's second proposal, and the cheap one: `Stop` sets
`t.awaited` for the threads it counts, `parkAtSafepoint` sends only when the mark is set. Fixes B. Needs a
send from `retire` for A. **Refuted by C1**: the mark is per thread and C1's two callers share one.

**B — wait on the identities awaited.** #656's first proposal: drain until every awaited `ThreadID` has
been seen, ignoring the rest, making the payload the channel already carries load-bearing. Fixes B, still
needs a `retire` send for A, and **refuted by C1** for the same reason — the sibling sends the id the
round is waiting for.

**C — give every caller an identity and wait on the awaited *caller* set.** Sound, and the direct
generalisation. It needs a per-`Invoke` identity where the engine has deliberately chosen counts twice:
0067 rejected one `thread` per `Invoke` (option A there) and
[ADR 0069](0069-a-host-function-is-a-caller-and-a-value-slice-a-host-call-marks-its-thread-blocked-and-shutdown-is-its-own-terminal-method.md)
made `Caller` a small struct rather than an allocated object. It would also put a map insert and delete on
`enterCall`/`leaveCall`, which is the `Invoke` path, to serve a round that happens rarely — paying on the
common case for the rare one.

**D — stop counting arrivals. Wait on SP-1's own predicate over the marks.** *(chosen)* The quantity SP-1
promises is not a number of messages; it is a state: **no agent of this world is executing guest code.**
The marks that express it are `thread.callers` (agents admitted to the guest) and `thread.blocked` (agents
suspended, SP-2), and the one missing term is *agents parked at a safepoint*. Add it, and the promise is a
predicate the engine can read:

```go
func (w *world) atSafepointLocked() bool {
    for _, t := range w.live {
        if t.parked+t.blocked < t.callers { return false }
    }
    return true
}
```

## Decision

**D.** `world.arrived` and `thread.reported` are deleted. `thread.parked` is added, `world.stopped` is the
round's completion channel, and every site that can make the predicate true claims and closes it.

**`thread.parked` is round-scoped and zeroed by the round's *start*, not by the parked caller.** Increment
under `world.mu` in `parkAtSafepoint` before the receive; `Stop`'s own opening walk clears it for every live
thread, beside the `stopReq` store it already does there. A decrement by the woken caller instead would
leave a window where `Resume` has released it, a fresh `Stop` has begun, and the stale `parked` still reads
as a caller at a safepoint — which is C1's false success with a new cause. The counter's lifetime *is* the
round's, so a round boundary is where it is cleared.

**Which boundary, was decided in the implementation and against this ADR's first draft.** That draft said
the round's *end* — `Resume` and `Instance.Close` zeroing it under the same mutex. Clearing at the start
instead is strictly weaker in what it obliges: `releaseIfAtSafepoint` returns early on `world.stopped ==
nil`, so `parked` is never *read* outside a round, and a stale value between rounds is therefore
unobservable. `Resume` and `Close` then need not touch the field at all, which removes two sites that would
each have had to remember to. The recorded reasoning is on `Stop` itself; this paragraph is amended rather
than left standing, because an ADR describing a mechanism the tree does not have is the foreclosing-words
shape aimed at the project's own record.

**`parkAtSafepoint` takes a `counted bool`, and the parameter is what makes the predicate exact.**
`enterBlocked` increments `blocked` and then parks if a round is in flight; if that park also incremented
`parked`, one caller would hold two of its thread's terms. On a thread with two callers — one entering a
wait, one running guest code — the sum would reach `callers` and the predicate would be satisfied while
the second caller ran, which is C1 rebuilt out of the repair. So `enterBlocked` passes `counted=true` and
the walk stays one term per caller.

The comparison is written `>=` rather than `==` even though the invariant is `parked+blocked <= callers`.
That is not a hedge against the case above — the parameter is what handles it — but the same choice 0067's
panic comment argues for one field over: an unsatisfiable equality makes *every* `Stop` wait out its whole
deadline, which is worse and silent, so a violated invariant degrades to an early return instead. No new
panic joins 0067's, because `>=` cannot be unsatisfiable.

**Four signal sites, one per way the predicate can newly hold**, each claiming the channel under `mu` and
closing it outside — §4 B-MM-3, and the shape `world.releaseIfQuiescent` already has:

- `parkAtSafepoint` — a caller reaches a safepoint. Covers `enterBlocked`'s path too, since that park runs
  immediately after the `blocked` increment.
- `leaveCall` — a caller leaves the guest. **C2's repair**, and it is the same function T-5.4 already
  releases quiescence from, for the same reason: a caller inside `Invoke` is one of the ways guest code is
  in flight.
- `world.retire` — a thread leaves the live set. **A's second repair**, and it needs no arrival on the exit
  path: the walk simply stops including a thread that has gone. `retire`'s comment saying an arrival here
  would break the other arm was correct about the protocol it was written against, and is answered by
  removing the protocol rather than by sending after all. *"A's repair"* is what this bullet said before the
  falsification battery was run; the measurement below shows `leaveCall` alone already closes A, so this
  site is a backstop whose necessity is argued and not measured.
- `Stop` itself, before it waits. A world with nothing running completes with no channel and no timer
  allocated at all — the common case for an embedder that stops an idle instance.

**`Instance.Close` and `Resume` nil `world.stopped` without closing it.** A round absorbed by a teardown
or ended by a resume must not have a later site close its channel: a `Stop` woken that way would return
nil for a world that is torn down or already running, which is the outcome `Stop`'s own closed-world
refusal calls *"worse than refusing"*. The waiting `Stop` reaches its deadline and reports an expiry,
which is what it does today for the same two cases. Ending a round is not completing one, and
`stopExpired`'s empty-`running` arm is the message for exactly that: every caller accounted for and the
round still not complete means it was ended by a `Resume` or a `Close`, so the expiry says so instead of
naming a thread.

**The timer is not the verdict, and this is a false red the repair would otherwise have introduced.** Go's
`select` picks arbitrarily among ready cases, so a round completed in the same instant the interval ran out
would be reported as an expiry roughly half the time. After the timer fires, `Stop` re-reads the **local**
`stopped` once, non-blocking, and returns nil if it is closed — the local copy, because `world.stopped` is
nil'd by whichever site claims it and a nil channel is never ready, while a closed one always is. The old
protocol had the same hazard and got away with it by accident: its receives were counted, so a late arrival
was still a receive.

**`ErrStopDeadline`'s message becomes a walk of who is still running.** The old form counted receives
against membership; the new one names the threads whose callers have not arrived, with their three marks.
C2's expiry printed *"0 of 1 arrived within 3s"* about a caller that had finished — a report with no
subject — and the walk is what makes the same red say which agent the engine is waiting for.

**`ErrStopInProgress` keeps refusing a mid-round `Spawn`, and its stated reason is replaced.** That
sentence justifies the refusal by `arrived`'s sizing — *"an (N+1)th potential sender into N slots"* — and
this decision deletes the channel, so the reason evaporates while the need does not: `Stop` broadcasts
`stopReq` once, over the membership it observed, and a thread admitted afterwards would never see it, never
park, and run guest code inside a round the predicate reports complete. The refusal is re-argued in those
terms at both sites. Left as a refusal rather than repaired by setting `stopReq` in `world.addLocked`,
because that would widen [ADR 0068](0068-spawn-drops-0056s-walk-and-refuses-the-two-cases-a-per-instance-world-cannot-express-because-a-thread-belongs-to-exactly-one-stop.md)'s
named limit — a public error's meaning — on the back of a bug fix.

## Consequences

**Grave #593's hazard is dissolved rather than re-guarded.** It was *"the one deadlock this protocol can
have"*: a caller blocked on a send while at a safepoint and unable to say so. There is no send. The
`reported` dedup that closed it goes with it, and
`TestThreeConcurrentCallersAndAStopDoNotHang` — whose `expiringWaits` arm exists precisely because the
dedup was load-bearing — keeps both arms and stops being a test about a buffer. Its `spinningCallers`
prose concedes in writing that *"`Stop` returns as soon as one arrival lands"*; that sentence is C1
described as a test's window rather than as the engine's defect, and it is replaced.

**Three of the four shapes are fixed by state that already existed**, which is the argument for D over C:
A and C2 need no arrival at all once the wait is a predicate, because `retire` and `leaveCall` already
maintain the terms. Only `parked` is new.

**The four shapes become `internal/interp/stoppredicate_test.go`, and each was watched to die against a
mutation of *this* mechanism rather than of the protocol it replaces.** C1 and C2 were measured against the
arrival protocol while it still existed (the context table above), but that engine is gone, so a green here
proves nothing about the four sites on its own — *a re-pointed control has not been watched die*. Five
mutations were applied one at a time and reverted, all four tests run under `-race`: a first-satisfied-thread
`atSafepointLocked` kills B, a thread-granular predicate kills C1, removing `leaveCall`'s site kills C2, and
removing **both** departure sites kills A. The table with the collateral kills is in the test file's header.

**One of the five killed nothing, and the enumeration is what that costs.** With only `retire`'s site
removed all four tests pass, because [ADR 0068](0068-spawn-drops-0056s-walk-and-refuses-the-two-cases-a-per-instance-world-cannot-express-because-a-thread-belongs-to-exactly-one-stop.md)
wraps `runEntry` in `enterCall`/`leaveCall`, so a spawned thread's `leaveCall` always precedes its `retire`
and the predicate holds before the reaper runs. That is the honest state of the fourth site: kept because the
predicate should not depend on a thread never leaving `live` without a matching `leaveCall`, and covered by
no witness this decision produced. Recorded rather than filed, because the missing witness would have to
create a state the engine has no path to.

**The hot path is untouched.** `poll`'s fast path is one atomic load on `stopReq` and does not reach any of
this; no file on the per-instruction path changes. What gains work is `leaveCall` (one nil compare per
`Invoke` return, on a function that already calls `releaseIfQuiescent`) and `parkAtSafepoint` (one extra
critical section, on a path that only runs while the world is stopping). `Stop` on an idle instance stops
allocating a buffered channel and a timer.

**Pre-registration.** Forecast: no benchmark row moves outside its null band, because no file on the
per-instruction path is edited. The falsifier is the diff's own file list, and it is stated as the weaker
instrument it is: a file-list argument cannot see a cost inside a file it says is unchanged. If any of
`exec.go`, `memop.go`, `atomic.go` or `interp.go` is touched by the implementation, `make ab` runs before
the merge and the measured deltas land here. **Rollback**: the predicate walk is `O(live)` under a mutex
already held; if a future N-thread world makes that walk the cost, the repair is a cached count of
non-arrived agents maintained by the same four sites, not a return to counting tokens.

**What this does not do.** It does not widen the world past one `Instance` (SP-4's cross-instance case,
[#515](https://github.com/scttfrdmn/burroughs/issues/515)), it does not settle what a `thread` is
([#514](https://github.com/scttfrdmn/burroughs/issues/514), which carries `decision-needed:scott`), and it
does not amend §3: SP-1's sentence is unchanged and this is the first implementation that asserts it for
every agent rather than for a count of them.
