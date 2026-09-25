# 0090 — `proc_exit` is instance-scoped, and its teardown is a *request* routed into the shutdown mechanism that already exists

Date: 2026-09-25 · Status: **proposed** · [#819](https://github.com/scttfrdmn/burroughs/issues/819) (the finding) · Occasioned by Phase 4's stdlib sweep ([ADR 0087](0087-the-threaded-p3-fork-charter-a-geoexperiment-over-an-async-base-whose-threaded-tier-has-no-independent-engine.md))
Ratio-Class: carried

**`Status` is held open, which is behaviour 3.** The *direction* is ruled — Scott, on the #819 report: *"I'm ruling the direction now, conditional on context not producing a counterexample: `proc_exit` is instance-scoped"* — and the *mechanism* is what this document recommends for ratification. There is no stamp to cite for the mechanism yet, so `Status` does not claim one. Recorded by the actor the ruling was given to, so no independent provenance; commits resting on it stay `Ratio-Class: carried`.

## Context

A Go guest built by the Phase 4 fork runs its test binary to completion — every verdict printed, `proc_exit` reached — and then **`Invoke("_start")` never returns**. Measured: verdict counts are *invariant* across hanging and finishing runs of the same package, so nothing about the guest's work differs. What differs is which agent calls `proc_exit`.

A prediction was registered before it was measured, and held in both directions with no exceptions:

> hung ⇔ `proc_exit` called on a spawned agent; finished ⇔ called on the invoking agent.

| package | outcome | n | `exitTID` | caller |
|---|---|---|---|---|
| `sync` | HUNG | 4 | 3 | spawned |
| `sync` | FINISHED | 1 | 1 | invoking |
| `context` | HUNG | 5 | 3 | spawned |
| `context` | FINISHED | 0 | — | — |

The converse arm rests on **n=1** at the Go level, and is stated as such rather than summarized into
the rest. It is **not left there**: a finishing run is rare, so waiting for more of them is a poor
instrument, while at the engine level the same arm is *deterministic* — see `invoker_exits` below,
n=10.

**The mechanism was then observed directly rather than inferred from the correlation.** A host goroutine dump on a hung run shows the `Invoke` goroutine parked in `interp.(*memory).wait(..., 0xffffffffffffffff)` — an infinite `memory.atomic.wait32` inside guest code — reached through `atomicWait` ← `execFE` ← `Invoke`.

**And the defect is three lines of existing code, read together:**

1. `host.procExit` returns `exitError` (`internal/wasi/preview1.go`).
2. `interp`'s contract is that an error returned from a host function is a **trap**.
3. A trap ends **only its own agent** — which is correct §2 T-5 behaviour, not a bug in the trap path.

So on a spawned agent the trap unwinds that agent, the invoking agent stays in its infinite wait with no waker, and `runModule`'s `defer in.Close()` — the teardown that would fix this — is deferred on an `Invoke` that never returns. The repair already exists in the tree and is simply unreachable from the state that needs it.

**A Go-free probe confirms this is an engine property, not a fork one.** 178 bytes of hand-written WAT — a spawned agent that calls `proc_exit`, and separately one that executes `unreachable`, while the invoking agent parks in an infinite `atomic.wait32`:

```
WAT-proc_exit HUNG t=15s spawns=1 procExit=1 exitTID=2
WAT-trap      HUNG t=15s spawns=1 procExit=0 exitTID=-1
```

Both arms hang, with no Go runtime, no scheduler, and no futex lock layer in the picture.

**A third arm mirrors them and supplies the converse deterministically.** The spawned agent parks
forever and the *invoking* agent exits — the exact inverse of arm 1:

```
WAT-invoker_exits RETURNED t=~200ms err=host function trapped: wasi: proc_exit(7)
                  spawns=1 procExit=1 exitTID=1        [10 of 10]
```

`Invoke` returns with code 7 every time, and `Close` afterwards returns `nil` even though a sibling was
parked in an infinite wait. So the registered shape holds in both directions with the weak side
measured at n=10 rather than n=1, and **the engine-level arms are the load-bearing evidence** — the
Go-level rates are consistent with them but are a worse instrument for the converse.

## The contract says nothing about either, and that is checked rather than assumed

**The contract never names `proc_exit`, and has no instance-lifetime clause at all.** Both verified by grep over the normative text. So §5 is silent on guest-initiated exit, and this is an ADR rather than contract text.

Two §2 clauses nevertheless constrain the answer:

- **T-5**: *"exit is the only termination directed at an **individual thread**: there is no cancel and no kill aimed at one thread."* An instance-scoped exit is not aimed at one thread, so T-5 is not contradicted — but it forecloses "kill each sibling" as the repair's shape.
- **T-5.4** specifies, for *engine shutdown*, exactly the three states and their mechanisms: guest code → its next safepoint (§3 SP-1's interval); **suspended in `memory.atomic.wait` → "trapping it out of the wait rather than by returning one of that instruction's defined results"**; parked in a blocking host call → §5 H-3's cancellation. Plus: wait for the unwinds, and *"a case that provably cannot be ended MUST be named in the engine's documentation and reported as shutdown's error."*

**T-5.4's population is `Close` — the host calling shutdown — not a guest calling exit.** This ADR therefore reuses T-5.4's *mechanism* and does not claim its *obligation*: a resolving citation that served the wrong population would be the defect this project has already paid for once.

## Decision

**`proc_exit` on any agent ends the instance.** WASI's threads semantics already say so, and an agent-scoped exit has no consumer — nothing wants half an instance left running.

**Its teardown is a *request* that does not wait, routed into the mechanism `Close` already drives.** `proc_exit` records the exit code, marks every live thread, cancels every thread context, and returns its `exitError` so the calling agent unwinds normally. The **wait** stays exactly where it already is: `runModule`'s deferred `Close`.

### Why not the two mechanisms that were registered as the candidates

Both were registered before the probe, and the probe refuted both:

- **"Wake every futex waiter" is unnecessary.** `memory.wait`'s `select` already has a third arm — `<-t.context().Done()` → `abandon` then `terminate` — whose comment cites T-5.4 by name. A cancel unparks an infinite wait today; nothing needs waking.
- **"`Stop` then discard" is not the working path, so SP-1 does not gain a consumer here.** `Close`'s own teardown is what reaches the observed state, and the recommendation had claimed SP-1 would finally get one. It does not.

Measured against the exact observed state — an agent parked in an infinite wait — `Close` reaches it and `Invoke` returns:

```
close=<nil>  invokeReturned=yes
err=burroughs: thread terminated by shutdown: thread 1 ended at a safepoint
    after `Close`, so "_start" did not complete (contract §2 T-5.4)
```

### Why the code must travel out of band

That error message is why the routing is only half the repair. If `proc_exit` merely triggered the teardown, `Invoke` would return **the shutdown error**, `runModule`'s `errors.As(err, &ee)` would not find an `exitError`, and exit code 7 would still be lost — just faster. So the code is recorded on the instance and the runner prefers the recorded code over the shutdown error.

### Why the request must not wait

`quiescentLocked`'s first condition is `hostCalls == 0`. Calling `Close` from *inside* `proc_exit` — itself an in-flight host call — would wait on a quiescence predicate that counts the waiter, stalling `closeQuiesceInterval` (10s) before returning `unquiesced()`. Derived from the predicate, not guessed, and it is the reason termination is requested rather than performed here.

### Races

**First exit wins**, recorded once. `Invoke` returns that agent's code. A second `proc_exit` arriving during teardown is not an error and changes nothing.

### Agents parked in a blocking host call

**The answer is inherited, not invented.** An OS-level `fd_read` cannot be interrupted from inside the engine, so `Close` bounds its wait at `closeQuiesceInterval` and returns `unquiesced()`, which names the still-live threads and the in-flight host-call count and cites T-5.4's *"reported as shutdown's error"*. `Invoke` returns without them; the goroutine stays stranded and **named**. This ADR adopts that answer rather than giving `proc_exit` a second one — one of the two cases `Close` already documents as unendable.

### Traps are the same class

The probe's arm 1 hangs identically, so a trap in a spawned agent joins [#819](https://github.com/scttfrdmn/burroughs/issues/819)'s scope rather than becoming a second finding. Under wasi-threads a trap also terminates the instance, and T-5.3 already requires a thread-ending trap to be *retained and reported* — what it does not do is end the siblings. Same routing, same recorded-cause discipline, with the trap rather than an exit code as the cause.

## Consequences

- **No new public API surface.** The termination request lives on `internal/interp`, which is not the embedder's boundary; the root package is unchanged. So this does not reach behaviour 2's escalation set on that ground.
- **No contract text.** Verified by the two greps above rather than asserted.
- **A guest can now end an instance from any agent**, which is new guest-visible power and is the point of the decision rather than a side effect.
- **`Invoke` on the initial agent returns the exit code even though a sibling exited**, which is what makes a Go test binary under the fork terminate at all.
- **The stranded-host-call case remains unendable and remains named.** Nothing here improves it.

## Registered witness, before the repair is built

- **Engine-level, Go-free (the negative arm, and it terminates on its bound):** the WAT guests above, all three arms, arms 1–2 currently hanging on `main` — watched hanging *first*, which is what makes it a watched death rather than a re-pointed control. After the repair, `Invoke` returns the right result within a stated bound: code 7 for the exit arm, the trap as the cause for the trap arm. Committed as the oracle's reading rather than gated on a `wat2wasm` lookup, per the wabt precedent.
  - **`invoker_exits` is the arm that must not move**, and it is registered as such: it passes *today*, so it is the control that catches a repair which fixes the sibling case by breaking the case that already worked. A repair witnessed only on arms 1–2 cannot see that.
- **Fork-level:** amendment 5's pooled row set, **18 of 18** finishing, with exact verdict counts.
- **Gate:** clauses 1, 2 and 3's witnesses re-run. Teardown semantics touch all three.
