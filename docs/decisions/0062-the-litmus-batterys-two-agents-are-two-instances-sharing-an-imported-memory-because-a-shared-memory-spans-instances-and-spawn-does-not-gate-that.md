# 0062 — the litmus battery's two agents are two instances sharing an imported memory, because a shared memory spans instances and `Spawn` does not gate that

Date: 2026-09-03 · Status: **proposed** — no stamp exists to cite, and *a `Status:` field is a citation to an
approval*, so it stays open until one does. Nothing here needs one to proceed: it is harness mechanism for a
pre-registered battery, it changes no gate's default, and the outcome sets it runs against were fixed by
[ADR 0055](0055-the-2-5-litmus-batterys-oracle-is-the-contract-read-clause-by-clause-with-its-outcome-sets-pre-registered-because-no-external-engine-can-arbitrate-a-clause-written-against-one.md)
before this document existed.

Filed against **[#10](https://github.com/scttfrdmn/burroughs/issues/10)**, contract §4 B-MM-5's litmus
battery, on Scott's order at the #601 review: *"#10 next."*

## Context

[ADR 0055](0055-the-2-5-litmus-batterys-oracle-is-the-contract-read-clause-by-clause-with-its-outcome-sets-pre-registered-because-no-external-engine-can-arbitrate-a-clause-written-against-one.md)
pre-registered §§2–5 clause by clause in [the tables](../litmus-battery-preregistration.md), and stated its own
sequencing in four numbered steps. Step 2:

> **Spawn** — [#554](https://github.com/scttfrdmn/burroughs/pull/554). `Instance.Spawn` is not on `main`, so
> the tree has exactly one agent and every case below needs two.

Step 3 was `memory.atomic.wait`'s suspend path, [#543](https://github.com/scttfrdmn/burroughs/issues/543),
which landed with
[ADR 0060](0060-the-futex-queue-hangs-off-memory-keyed-by-effective-address-because-a-pointer-key-would-borrow-its-soundness-from-another-package.md)
in [#594](https://github.com/scttfrdmn/burroughs/pull/594). #554 is parked, behind three blockers of its own,
one of which ([#573](https://github.com/scttfrdmn/burroughs/issues/573)) is a decision that is Scott's.

Read literally, the sequencing therefore says the whole battery is blocked and Scott's order has no runnable
slice. **That reading is what got checked, and the premise it rests on is false.**

### The premise, and the experiment that falsified it

A shared memory is **imported**. It is the one thing in this engine whose identity outlives the instance that
declared it, and ADR 0052 already turned on exactly that fact — §4's boundary counter sits at package scope
rather than on `Instance`, because *"a shared memory spans instances."*

So `InstantiateLinked(waiter, exportsOf(waker))` hands the waiter module the **same `*memory`** the waker
declared. What each instance keeps to itself is the rest: its own `thread` (`in.host`) and its own `world`.
Two goroutines driving `Invoke` on the two instances are therefore two agents with distinct thread identities,
over one address space, contending on one futex queue — which is what a litmus case needs and, for most of
these cases, all it needs.

Verified rather than argued, before any case was written on it:

- The two instances' `mems[0]` are the same pointer, asserted in the harness rather than assumed.
- A waiter parked through `memory.atomic.wait32` on one instance is woken by `memory.atomic.notify` on the
  other, and the wake is visible in the real queue: `litmusArmed` reads `m.waiters[ea]` under `waitMu`.
- **2,000,000 rounds of the full arm-and-wake protocol**, `-race` clean, at about **6 µs/round** — so a
  registered `R = 100_000` costs under a second and round count is not a budget question.

The premise was also false in a weaker form already on `main`: `TestAtomicRmwIsNotObservablyTornAcrossThreads`
races two `Invoke` goroutines over one instance's memory. *"The tree has exactly one agent"* described the
spawn primitive's absence and was written as though it described the tree.

## Decision

**1. The battery's two agents are two instances sharing one imported shared memory.** The harness is
`litmusAgents` in `internal/interp/battery_test.go`: a waker module exporting a `shared` memory and a
`notify`, a waiter module importing that memory and exporting the wait alone, both built through
`text.EncodeModule` → the decoder with `Features{Threads: true}` → `InstantiateLinked`.

Both modules go through the real front end for grave #579's reason: a hand-built `memory` skips `newMemory`'s
base-alignment check, and a case built on one would assert the engine against the harness's own idea of a
memory.

**The shared identity is asserted, not assumed.** `litmusAgents` fails if `waker.mems[0] != waiter.mems[0]`,
because that single pointer comparison is the whole vehicle: were the linker ever to copy a memory on import,
every case in this battery would run two agents over two address spaces and pass by never racing. That is the
*comparisons need a vacuity check* shape aimed at the harness's own premise.

**2. The battery lives in `internal/interp`**, not in `internal/spec` and not in a new package. The reason is
one read: knowing that the second agent has **reached** its wait, rather than merely been asked to, requires
`m.waiters` under `waitMu`, and there is no guest-visible substitute — a `notify` of count 0 returns 0 whether
the queue is empty or full. Three properties keep that read honest, and they are stated on `litmusArmed`
rather than here alone:

- It is used for **sequencing only, never as an assertion**; false is a premise that did not hold, and every
  caller reports it as one.
- It **cannot supply the edge a §4 case is testing.** The arming observation happens strictly before the
  waking agent's stores, so the only ordering it establishes runs from the waiter's enqueue to those stores —
  the opposite direction from the one under test.
- It spins with `runtime.Gosched()` rather than sleeping, so a registered `R` stays a statistical budget
  instead of becoming a wall-clock one.

**3. This vehicle must not be used for `t1-n-agents-block-simultaneously`.** T-1's clause is about the spawn
primitive itself — *"a wasm thread backed 1:1 by an OS thread"* — and its case counts N agents parked at once
in order to rule out a pool, an event loop, or an M:N mapping. Run on N instances driven by N goroutines it
would **pass**, because Go parks N goroutines happily, while the clause it claims to discharge stayed
unsatisfied. A vehicle that satisfies a case by supplying exactly the thing the clause forbids is worse than a
blocked row, so T-1 stays blocked on #554 and its row says so.

**4. Every case is watched die against an injected engine before it is trusted** — and where the injection
produces nothing, the phenomenon's **rate** is established with an instrument outside the engine before the
clean board is read as anything. Both halves were exercised in this slice: T-3's injection failed the case on
all three runs, and B-MM-2's did not, which is what #603 is.

## Options considered

**1. Wait for #554.** *Rejected.* It is parked behind a decision that is Scott's, so waiting converts *"#10
next"* into *"#10 after a ruling"* — and the reason it looked necessary is a sentence about the tree that the
tree refutes. Three of the tables' eleven cases are runnable without it.

**2. Two goroutines over one instance**, the shape `TestAtomicRmwIsNotObservablyTornAcrossThreads` already
uses. *Rejected for this battery.* One instance has one `thread`, so both agents share a thread identity and
a `blocked` count: SP-2's *"stop reporting fewer than N agents at a safepoint"* and H-1's per-agent progress
counter would be one agent wearing two hats.
[#592](https://github.com/scttfrdmn/burroughs/issues/592) is that exact gap, filed and open, and a battery
built on it would be asserting the contract through the defect.

**3. A hand-built `*memory` handed to two instances.** *Rejected* — grave #579's reason, above.

**4. A new `internal/litmus` package.** *Rejected.* The arming read needs `waitMu`, so the package would need
an exported accessor for the futex queue: widening the engine's API for the harness's benefit, to buy a
directory. §9's suite boundary is about what the *spec suite* runs, and this battery's oracle is this
project's own contract reading rather than an upstream corpus.

## Which cases the vehicle reaches, and which it does not

Re-derived per case from the **witness text**, because a witness is part of a case's registration and not a
suggestion about how to build it.

| case | runnable on this vehicle | verdict |
| --- | --- | --- |
| `t3-wake-latency-is-microsecond-scale` | yes | **implemented in this slice** |
| `b-mm-2-sibling-field-after-wake` | yes | implementable and **stillborn**, measured — [#603](https://github.com/scttfrdmn/burroughs/issues/603) |
| `b-mm-2-the-sibling-lives-in-a-second-shared-memory` | yes | as above, plus the multiple-memories gate |
| `t1-n-agents-block-simultaneously` | **prohibited** | false green by construction — decision 3 above |
| `t2-the-first-agent-waits-and-a-child-wakes-it` | no | its witness names *"a spawned child"*; two instances would discharge a no-main-thread-special-case clause with an interleaving the registration does not describe |
| `sp1-stop-brings-every-agent-to-a-safepoint-by-the-deadline` | no | `world`'s extent is one instance, so two instances are **two worlds** and one `Stop` cannot reach N agents |
| `b-mm-1`, `sp2`, `sp4`, `h1`, `h3` | no | every one of their witnesses parks in a **blocking host call**, and no host-call surface exists — [#602](https://github.com/scttfrdmn/burroughs/issues/602) |

So the runnable, born, non-prohibited set is **one case**, and this slice lands it. That is a smaller number
than *"three cases are runnable"* looked like at the start of the derivation, and the two that fell away fell
away for reasons the tables could not have stated in advance: one is a measurement (#603) and one is a
prohibition that only appears once you ask what a passing case would have proved.

## T-3, watched die

The case is `TestAWakeArrivesAtFutexLatencyAndNotEventLoopLatency`, and the injection is the mechanism the
clause names: `notify`'s buffered send left in place, and `wait`'s `select` replaced by a loop that
`time.Sleep`s **1 ms** and then polls the channel and the expiry non-blockingly. Three runs per arm, `K = 1000`
wake events each, darwin/arm64.

| arm | median, three runs | run-to-run spread | against the 1 ms bar | verdict |
| --- | --- | --- | --- | --- |
| `main` — futex-backed | 250 ns, 458 ns, 250 ns | 83% | **2 180–4 000× below** | PASS ×3 |
| injected — 1 ms-tick poll | 1.1339 ms, 1.1354 ms, 1.1343 ms | **0.15%** | **13.4% above** | FAIL ×3 |

**The injection is the fastest plausible turn**, which is the hardest case for the ceiling rather than the
easiest: a browser event-loop turn is milliseconds to tens of milliseconds, and a 1 ms tick is the floor of
that range. The ceiling still separates the two mechanisms, and the margin on that side is 13.4%.

**Both arms are admissible, for opposite reasons, and the comparison that says so is the floor against the
bar.** On the injected arm the run-to-run floor is 0.15% and the margin is 13.4% — two orders of magnitude of
room, so the board adjudicates. On the null arm the floor is enormous in relative terms (250 ns against 458 ns
is 83%) and the distance to the bar is three orders of magnitude, so it adjudicates too. A board is readable
when its floor is narrower than its bar; *neither* arm here is readable because its spread is small, and
saying which reason applies to which arm is the whole content of the check.

**The ceiling's resolution limit, registered here rather than discovered later: a 500 µs tick would pass.**
The clause's discriminator is one order of magnitude wide by design — ADR 0055 said so, *"1ms separates the
two mechanisms by an order of magnitude in either direction"* — so this case discriminates *mechanisms* and
nothing finer. A poll loop tuned just under the ceiling is a non-conforming engine this case cannot catch, and
the honest place for that sentence is beside the measurement that shows the margin is 13.4% on one side and
4 000× on the other.

**The minimum is why the pre-registered statistic is the median.** The injected arm's fastest round was
**956 µs** — *below* the ceiling. An engine polling on a 1 ms tick would pass a minimum-based reading of its
own worst mechanism. The registration said median, and this measurement is what that choice bought.

**Negative latencies were observed and are reported signed.** Minima of −3.541 µs and −708 ns appeared on the
`main` arm: the wake is delivered inside `notify`, so the woken agent can be running again before the
notifying agent's own call teardown finishes. The endpoint choice (*"the notifying agent's return"*) predicts
this, and clamping would hide the one number that most decisively separates a futex from a turn.

## Consequences

- **`internal/interp/battery_test.go`** holds the harness — `litmusWakerSrc`, `litmusWaiterSrc`,
  `litmusAgents`, `litmusArmed` — and T-3's case. Its file comment carries ADR 0055's two caveats (a green
  here agrees with an oracle this project wrote; a verdict is a falsifier, never a certificate) and decision
  3's prohibition, so a reader who arrives at the file rather than at this ADR meets both.
- **T-3's row becomes `implemented — TestAWakeArrivesAtFutexLatencyAndNotEventLoopLatency`**, and the
  pre-registration's sequencing section is corrected: step 2's premise is replaced by this vehicle, and the
  five host-call rows and T-1's contradictory status are re-derived per the table above.
- **The proxy for *"the woken agent's first executed instruction"* is named in the test rather than left
  implicit**, with the direction of its error: an `Invoke` return includes the wait's result push and
  teardown, so it can only **over-report**, and an over-reporting instrument cannot manufacture a pass against
  a ceiling.
- **T-3's floor is structurally zero and says so.** The registration re-runs a round whose wait returned
  not-equal; each round here waits on a fresh address, a fresh address holds zero, and zero is what the wait
  expects — so the not-equal arm is unreachable, the re-run count is zero by construction rather than by
  measurement, and the arm it stands in for is covered by `TestAWaitWhoseCellChangedDoesNotQueue`.
- **Three findings leave this slice as filings rather than edits**, because each is a change to something a
  principal or another PR owns: [#602](https://github.com/scttfrdmn/burroughs/issues/602) (the host-call
  surface, a decision), [#603](https://github.com/scttfrdmn/burroughs/issues/603) (B-MM-2's amendment, which
  ADR 0055 requires be its own PR), [#604](https://github.com/scttfrdmn/burroughs/issues/604) (`ThreadID`
  collides across instances, so both agents print as `thread 1`), and
  [#605](https://github.com/scttfrdmn/burroughs/issues/605) (no control asks whether a `blocked — #N` row's
  blocker is still open).
- **B-MM-5 is not discharged by this**, and the row that would claim otherwise is the vacuous kind. Its
  clause asks for a battery *including the sibling-field-after-wake case*, run on both a TSO and a
  weakly-ordered platform; that case is #603's subject and is not landed.
