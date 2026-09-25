# 0090 — `proc_exit` is instance-scoped, and its teardown is a *request* routed into the shutdown mechanism that already exists

Date: 2026-09-25 · Status: **accepted** · [#819](https://github.com/scttfrdmn/burroughs/issues/819) (the finding) · Occasioned by Phase 4's stdlib sweep ([ADR 0087](0087-the-threaded-p3-fork-charter-a-geoexperiment-over-an-async-base-whose-threaded-tier-has-no-independent-engine.md))
Ratio-Class: carried

**`Status: accepted` cites chat-Claude, and the first draft of this line cited Scott. That was a fabricated citation and its repair is this ADR's first amendment.** The direction ruling — *"I'm ruling the direction now, conditional on context not producing a counterexample: `proc_exit` is instance-scoped"* — is **chat-Claude's**, relayed through Scott on the #819 report. Scott conveyed it; he did not issue it, and he has said explicitly that technical calls of this kind are not his to make. The ratification of this document, with the three amendments recorded below, is chat-Claude's on the same channel.

**Scott's standing here is the after-the-fact veto**, and that is the whole of it. Naming him as the source of a technical ruling would have credited a stamp he never gave — which this project treats as worse than a wrong option, because it is a forged provenance about the project's own governance (behaviour 3). Relayed and recorded by the actor the relay reached, so no independent provenance; commits resting on it stay `Ratio-Class: carried`.

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

### Why the cause must travel out of band, and why it surfaces at `Invoke` rather than in the runner

That error message is why the routing is only half the repair. If `proc_exit` merely triggered the teardown, `Invoke` would return **the shutdown error**, `errors.As(err, &ee)` would not find an `exitError`, and exit code 7 would still be lost — just faster.

**The first draft put the fix in `runModule`, and that is amendment 2's subject.** Preferring the recorded code inside the WASI runner repairs the runner and leaves **every other embedder** holding this error out of `Invoke`:

> `thread 1 ended at a safepoint after "Close"`

When the guest exited or trapped, **that sentence is false: nobody called `Close`.** A true message for the WASI runner bought by a false one for everyone else is not a repair, it is a narrower blast radius.

**So the rule is at the engine.** When termination was requested *by the guest*, `Invoke` returns the recorded **first cause** — `errors.As` finding the `exitError`, or the trap — and the out-of-band channel lives in `interp`. The runner then needs no special case at all, and `runModule`'s existing `errors.As(err, &ee)` keeps working unchanged.

This changes **the value of an error, not an API surface**, so it stays inside this ADR rather than reaching behaviour 2's escalation set.

### Why the request must not wait

`quiescentLocked`'s first condition is `hostCalls == 0`. Calling `Close` from *inside* `proc_exit` — itself an in-flight host call — would wait on a quiescence predicate that counts the waiter, stalling `closeQuiesceInterval` (10s) before returning `unquiesced()`. Derived from the predicate, not guessed, and it is the reason termination is requested rather than performed here.

### Where the hook goes, and the distinction it must make (amendment 3)

**One hook covers both classes.** A spawned agent whose top-level frame unwinds **with a trap** requests instance termination, and `proc_exit` reaches that path already — its `exitError` *becomes* a trap, which is the defect's own mechanism read forwards.

**The hook MUST NOT fire when a spawned agent's entry function returns normally.** That is §2 T-5's ordinary per-thread exit — *"A thread **exits** when its entry function returns or traps"* — and the instance must carry on. T-5 gives those two the same clause but not the same consequence, and the hook is precisely where that difference is implemented.

**A hook placed at "agent ended" rather than "agent trapped" passes arms 1–3 of the witness and silently breaks this.** That is why the witness has a fourth arm rather than three: the three hanging-or-exiting arms cannot see the over-firing case at all, because in each of them the instance is *supposed* to end.

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

- **Engine-level, Go-free (the negative arm, and it terminates on its bound):** the WAT guests above, **four arms**, with arms 1–2 currently hanging on `main` — watched hanging *first*, which is what makes them watched deaths rather than re-pointed controls. After the repair, `Invoke` returns the right result within a stated bound: code 7 for the exit arm, the trap as the cause for the trap arm. Committed as the oracle's reading rather than gated on a `wat2wasm` lookup, per the wabt precedent, so there is no skip to license.
- **Two of the four are arms that MUST NOT MOVE, and both pass today** — which is what makes them able to catch a repair that fixes the broken cases by breaking the working ones. A repair witnessed only on arms 1–2 can see neither.

  | arm | today | what it catches |
  |---|---|---|
  | 1 `proc_exit` | HUNG 15s, `exitTID=2` | the defect |
  | 2 `trap` | HUNG 15s, `procExit=0` | the same class, folded into #819 |
  | 3 `invoker_exits` | RETURNED ~200ms, code 7, `exitTID=1`, **10/10** | a repair that breaks the exit path that already worked |
  | 4 `sibling_returns` | RETURNED, `err=<nil>`, **5/5** | **a hook at "agent ended" instead of "agent trapped"** — T-5's ordinary per-thread exit, where the instance must carry on |

  Arm 4's *today* column is measured, not assumed: registering an arm as must-not-move on an unverified claim would be the same defect the hook itself is guarded against.
- **Fork-level:** amendment 5's pooled row set, **18 of 18** finishing, with exact verdict counts.
- **Gate:** clauses 1, 2 and 3's witnesses re-run. Teardown semantics touch all three.
- **The committed witness asserts that its own output lines were seen.** During this investigation a
  count of the probe's runs was taken with `go test` but without `-v`, which swallows the probe's
  stdout: the pipeline printed a tally for a pattern that had matched nothing. That is the same family
  as the earlier `grep` block-buffering defect — **an instrument reporting on output it never
  received** — so the witness asserts a positive line count per arm rather than trusting that absence
  of a failure line means absence of a failure.

---

# Amendment 4, 2026-09-25 — amendment 3's trap clause is withdrawn, because it reversed stamped ADR 0071

**Ruled by chat-Claude** (relayed via Scott), on the conflict being found by building the mechanism rather
than by re-reading either document. Appended rather than edited into the body above, so that what was
ratified and what replaced it are both readable.

## What was withdrawn

Amendment 3 said a spawned agent whose top frame unwinds **with a trap** requests instance termination, on
the wasi-threads reading that a trap in any thread ends the instance. **That clause is withdrawn.** It was
never implemented past a local branch and no code carrying it ever landed.

**It would have reversed [ADR 0071](0071-t-5-is-live-only-membership-a-bounded-status-record-a-fault-in-two-channels-and-a-sentinel-panic-for-the-terminal-unwind.md),** whose witness says so in plain words — a host
entry after a spawned thread's trap *"must run"*. 0071 decided what a fault does: record it, report it on
both of §2 T-5.3's channels, and **leave the instance usable**. Building amendment 3 turned exactly one
test in the tree red, and it was that witness.

**The premise is what was wrong, not the conclusion.** T-5.3 governs *reporting*, not survival, so the
contract clause permits either answer — which is why checking the clause alone would not have caught this,
and why the conflict is with a stamped ADR rather than with §2. The chair applied wasi-threads semantics
without asking whether a stamped decision had already answered the question; it had.

## What replaces it

**`proc_exit` ends the instance because it is a declaration. A bare trap does not, because it is a fault.**

- `proc_exit` is the guest saying the process is over.
- a bare trap is a fault, and 0071's considered answer to a fault stands unchanged.

Both reach `world.retire` as a trap, because a host function's returned error *is* a trap — so the engine
cannot separate them by shape, and a **declaration** is what separates them. `internal/wasi`'s `exitError`
implements `interp.InstanceEnder`; the hook's predicate is an `errors.As` over the chain for that
interface. **Fault recording is unchanged for every trap**: only the teardown request is gated.

**The interface is deliberately not the public embedder surface.** Nothing outside the module can
implement it. If an embedder defining host functions through the public API ever needs to declare this,
that is new public surface and Scott's stamp — recorded as out of scope, not as a gap.

## The divergence from wasi-threads is deliberate and known

wasi-threads says a trap in any thread ends the instance. **Burroughs does not, and this records that as a
choice rather than an oversight.** What retires it is a **consumer that needs a bare spawned trap to end
the instance** — a candidate being a fork guest reaching `unreachable` in a spawned agent without going
through `proc_exit`, for instance if the runtime's abort path fires. For the Go fork as it stands, fatal
paths reach `proc_exit` via `runtime.exit`, so Go guests are covered either way.

**That consumer would bring a reversal of 0071, which is Scott's tier.** It does not arrive as another
amendment to this document.

## The witness changed with it

**Arm 2 changed meaning and now pins 0071 in the threaded setting** — where 0071's own witness does not
reach, because nothing there is parked in a futex. A spawned agent traps while the invoker sits in an
infinite wait; `Fault()` reports the trap while `Invoke` is **still in flight**; the embedder then calls
`Close`, and `Invoke` returns the `Close` sentence, which is **true this time because `Close` was called**.
`errors.As` finds no `exitError`. Arm 2 joins arms 3 and 4 as must-not-move.

**Injection D is the watched death for the new condition**: fire the hook on any trap and ignore the
declaration. Arm 2 and T-5.3's witness both die. Injections A–C were re-run against the new predicate, and
the four form a matrix in which no arm is redundant — **arm 4 is the only row where B and D differ**. One
honest note: **arm 3 never fails under any of the four injections**; it is a regression guard, not a
discriminator.
