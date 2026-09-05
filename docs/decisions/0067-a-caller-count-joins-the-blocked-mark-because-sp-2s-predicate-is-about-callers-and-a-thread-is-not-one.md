<!-- Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0 -->

# 0067 — A caller count joins the blocked mark, because SP-2's predicate is about callers and a thread is not one

Date: 2026-09-04 · Status: **proposed** — no stamp exists to cite, and *a `Status:` field is a citation to
an approval*, so it stays open until one does. Nothing here needs one to proceed: this is mechanism, which
is product work and self-merges on a bound green, and it changes no gate's default and no public
signature.

Filed against **[#592](https://github.com/scttfrdmn/burroughs/issues/592)**. It deliberately does **not**
settle [#514](https://github.com/scttfrdmn/burroughs/issues/514), which carries `decision-needed:scott`
and whose subject is what a `thread` *is*; see option A.

## Context

[ADR 0060](0060-the-futex-queue-hangs-off-memory-keyed-by-effective-address-because-a-pointer-key-would-borrow-its-soundness-from-another-package.md)
gave §3 SP-2 its inverted implementation: a thread suspended in `memory.atomic.wait` cannot announce its
own arrival and SP-4 forbids waking it to ask, so `Stop` counts it as arrived *itself*, reading a
`blocked` mark under the same mutex the transition takes. `Stop`'s arrival loop asks

```go
if t.blocked > 0 {
    atSafepoint++
    continue
}
want++
```

**`blocked` is a count of suspended callers and the predicate is read as a fact about the thread**, and
those are the same proposition only while a thread has at most one caller. It does not. `link.go`
registers exactly one `thread` per instance — `in.host` — while `Invoke` is an exported method on
`*Instance` that nothing gates: no threads proposal, no `shared` flag, no `Spawn`. The engine's own
`TestAtomicRmwIsNotObservablyTornAcrossThreads` drives two concurrent callers through one instance, so the
second caller is not hypothetical and is not gated behind anything a default could turn off.

So the reachable state is **mixed**: caller A suspended in a wait, caller B executing guest code, both on
one `thread`. `blocked` is 1, the predicate answers *"at a safepoint"*, `want` stays 0, and `Stop` returns
`nil` without waiting for anybody — while B runs. The host called `Stop` in order to look at guest memory
and the guest is changing it, which is SP-2 failing on its own terms: a thread reported at a safepoint
*"cannot touch guest memory until it re-enters through a boundary that observes the stop."*

**This is the residual half of #592, and the other half is already closed.** Grave #593 fixed the variant
where the same confusion *hangs*: three callers parked by one `Stop` overflowed an `arrived` buffer sized
`len(w.members)`, and the third blocked on its send forever with `Resume` unable to free it. The repair
was a per-round `reported` flag, one arrival per thread per round. That closed the hang and left the
predicate alone — correctly, since a dedup cannot decide how many callers are running.

**The violation that remains is a race window rather than a steady state**, and that shaped the witness
more than it shaped the mechanism. `Stop` returns early, and B then reaches its next back-edge, polls,
sees a request nobody waited for, and parks. The window is one inter-poll interval. `poll` has three call
sites — a back-edge in `jumpTo`, `enterFrame`, and `tailcall.go` — so a guest body with no branch, no call
and no tail call is an unpolled stretch of whatever length it is written to, which is the lever the
witness uses. Recorded here because the first witness was **stillborn** on exactly this point: with a
three-instruction loop body it passed on the broken engine, since the guest had already parked before the
host's first read.

## Options

**A — one `thread` per `Invoke`.** Make thread identity match caller identity, which is the shape the
defect is measured against: `blocked > 0` becomes true of the right subject and nothing else changes.
*Cost:* it is **#514's subject, not this ADR's.** #514 carries `decision-needed:scott`, and #592's own body
says the principled fix *"changes what `world.members` means"* — membership becomes dynamic per call, which
is the premise SP-4's dynamic-membership hazard is written against and which `Stop`'s buffer sizing,
`Resume`'s member walk and the `arrived` dedup all rest on. Taking it here would decide a parked question
as a side effect of a bug fix, and *decision-before-code* runs the other way.

**B — count the callers on the thread, beside the mark (chosen).** Add `callers int` to `thread`, moved
under `world.mu` by an `enterCall`/`leaveCall` pair wrapping guest execution exactly as
`enterBlocked`/`leaveBlocked` wrap suspension. The predicate becomes `t.blocked == t.callers`: the thread
is at a safepoint when *every* caller on it is suspended, which is SP-2's sentence with its subject
restored. *Cost:* one more field and one more critical section per `Invoke`, on a mutex `Invoke` does not
currently take at all — a real per-call cost that has to be measured rather than asserted, and the
predicate still speaks about a thread rather than about callers, so A remains the eventual answer.

**B′ — the same count, atomic instead of mutexed.** `atomic.Int64`, no lock on the `Invoke` path.
*Cost:* the mark's whole soundness argument is that `blocked++` and the stop check are **one critical
section**, which is what makes 0060's three-way race a two-way one. A count outside the mutex needs its
own ordering argument against a `Stop` reading both fields, and the failure it buys is the one 0060 names
as the outcome that must not exist — *"a `Stop` that neither observes the mark nor receives an arrival."*
Cheaper on a path whose cost is not yet known to matter, in exchange for re-opening a settled proof.

**C — forbid concurrent `Invoke`.** Document one caller per instance and the defect is unreachable.
*Cost:* it breaks the engine's own `TestAtomicRmwIsNotObservablyTornAcrossThreads`, and it would be a
public-API restriction adopted to avoid an internal fix — §2's direction of travel is *more* concurrency,
not less.

## Choice

**B.** It closes the reachable violation without deciding #514, which is the only property that
distinguishes the options that work: A and B fix the same bug, and A does it by ruling on a parked
question. B is also the option that stays true after A lands — a per-call `thread` makes `callers` always
1, so the predicate degenerates to `blocked == 1` and the mechanism retires rather than conflicting.

Soundness, over all four configurations of the two counts, since the predicate is an equality now and an
equality can fail in two directions:

| `callers` | `blocked` | Predicate | Correct? |
|---|---|---|---|
| 0 | 0 | at a safepoint | Yes — no caller is executing. |
| 1 | 0 | `want++` | Yes — the executing caller must arrive. |
| 1 | 1 | at a safepoint | Yes — SP-2's original case, unchanged. |
| 2 | 1 | `want++` | Yes — this is the defect's case, and `Stop` now waits for B. |
| 2 | 2 | at a safepoint | Yes — both suspended, nothing to wait for. |

`blocked <= callers` holds by construction and is what makes the equality safe rather than merely usually
right: `enterBlocked` is reached only from `futex.go`'s `wait`, which is reached only from guest code,
which runs only inside a call that has already incremented `callers`. A `blocked` that exceeded `callers`
would make the predicate unsatisfiable and hang every `Stop`, so the invariant is load-bearing and is
asserted rather than assumed.

**One arrival still suffices per thread per round**, so #593's dedup is untouched: `want` may now count a
thread whose other caller is suspended, and that thread sends once when its executing caller parks.

**Instantiate-time guest execution is not concurrently reachable with `Stop`**, which is why the pair does
not need to wrap it: `build`'s start function and `runConst` run before `InstantiateLinked` returns, so no
external reference to the instance exists and no `Stop` can be in flight. Stated rather than left silent,
because it is the one place guest code runs outside `Invoke`.

## Consequences

- `Stop`'s return becomes a real barrier in the mixed case: no guest write can follow it until `Resume`.
- **`Invoke` takes `world.mu` twice per call**, once on the way in and once on the way out, where it took
  it zero times before. That is a per-call cost on the engine's hottest public entry point and it is
  **pre-registered and measured, not asserted** — *cheap is a grammar claim*. The pre-registration is
  below, and the arm it names is new, for the reason in the next bullet.

- **No arm in the tree could have priced this, and the first draft of this bullet named one that
  structurally cannot.** It said `growbench` prices it most sharply *"because its guest body is a single
  `memory.grow`"*. Its guest body is **1000** of them (`const grows = 1000`), and its own package comment
  says why: *"`Invoke`'s own fixed cost has to be a small share of the row."* Every bench package here is
  built to that principle and states it — `membench` and `rmwbench` at 1000 accesses, `loopbench` and
  `globalbench` at 100,000 trips — so the arm named as the most sensitive was the tree's **most diluted**,
  by three orders of magnitude, and the four others are worse. The forecast was made against a fixture
  that had not been read, which is *name a failure mechanism the instrument can ask about* failing at the
  first step. It is withdrawn **before** any number was taken, which is the only ordering that separates
  withdrawing a forecast from shopping for a threshold; the number that follows was pre-registered after
  this bullet was written and before the run.

- **`internal/interp/invokebench` is the arm, and it is charged to this slice.** One `Invoke` of an
  empty-bodied export per op, so the row is almost entirely boundary crossing and the two new critical
  sections are the largest share of it they will ever be anywhere. **That makes the figure an upper bound
  rather than a typical cost**: every existing arm amortises the same two pairs across ≥1000 guest
  operations, so a reader who takes this row as the cost of the change has overstated it by whatever the
  dilution is on their own workload.

- **Pre-registration, criterion and rollback.** The criterion is **absolute and set against a bar measured
  on the same run**, not a percentage of a baseline — a percentage would need the baseline first, and
  choosing a threshold after seeing it is the thing pre-registration exists to prevent.
  `BenchmarkTwoUncontendedLockUnlock` is that bar: plain Go, two `Lock`/`Unlock` pairs on one uncontended
  mutex, which is exactly what `enterCall` and `leaveCall` add. **Forecast: the `Empty` arm's head-minus-base
  delta is positive, and no larger than that bar plus the `EmptyNull` pair's own spread on the same run.**
  Positive because two mutex pairs are not free and a null here would more likely mean the arm cannot see
  them than that they cost nothing; bounded by the bar because the mechanism adds nothing else to the path.
  *An unasserted distance is the vacuum*, so both halves are asserted. **Rollback if the delta exceeds the
  bar:** something other than the two pairs is being paid, the mechanism does **not** land on that reading,
  and #592 stays open pending #514 — option B′ is not the fallback, since 0060 forbids it, and option A is
  #514's to rule. *Compare the floor to the bar*: if the `EmptyNull` spread is not narrower than the bar,
  the board does not adjudicate and says so rather than reporting a pass.

- **That last clause fired, and it fired before the run.** The floor was measured on the dev box against
  the arm's own identical-source twin — `Empty` and `EmptyNull` at 131.9, 145.5, 168.5 and 312.9 ns/op
  across single rounds, on a row of 5 allocs and 184 B per op, against a bar of 4.249 ns. **The
  instrument's resolution is roughly an order of magnitude wider than the effect it was built to see**,
  because the row is GC-dominated and the effect is two uncontended mutex pairs. So the A/B on the public
  path **cannot** confirm or refute the pre-registered bound; what it can do is exclude a gross regression,
  at whatever its floor turns out to be on the lab host, and that is what it will be reported as.
- **The criterion is unchanged, and this is not a threshold moved.** No head-versus-base figure had been
  taken when the paragraph above was written, and none has been taken now: every number in it is
  identical-source against identical-source, which measures **the instrument** and not the change.
  Distinguishing those two is the entire content of *compare the floor to the bar*, and doing it before the
  board exists is the only time it is worth anything. What follows from it is a change of **instrument
  standing**, not of bound: the bar carries the effect's magnitude, the `Empty` row carries the
  denominator, and the A/B carries a weak ceiling.
- **The effect is therefore reported as a magnitude and a share, not as a resolved delta.** The mechanism
  is straight-line code with no branch on data — two nil checks, two increments, and two `Lock`/`Unlock`
  pairs on one mutex — so `BenchmarkTwoUncontendedLockUnlock` is a tight upper bound on it by construction,
  and `BenchmarkEmpty` is the denominator that turns it into a share of `Invoke`'s fixed cost. **There is
  no amplified arm available and that was checked rather than assumed**: `enterCall` sits at `invokeIndex`
  around `in.run`, so it fires once per `Invoke` — a guest `call` does not reach it, and a re-exported
  import's delegation chain returns through `ext.owner.invokeIndex` to a single `in.run`, so a chain pays
  one pair and not one per link. The arm is as sharp as this mechanism can be measured, which is why the
  floor is reported beside it instead of being tuned away.
- The predicate still says *"this thread is at a safepoint"* while reasoning about callers, so the
  vocabulary remains wrong even though the arithmetic is now right. #514 is where that is repaired, and
  this ADR is a reason to leave it filed rather than to close it.
- `ErrStopDeadline`'s message reports `atSafepoint+i` of `total`, where `total` is `len(w.members)` — a
  count of **threads**. With two callers on one thread it can say *"of 1"* while two callers exist. That is
  the same confusion in the *reporting* channel rather than in the predicate, it changes no verdict, and
  no witness here asserts it. Named so that a reader who finds it knows it was seen and left.
