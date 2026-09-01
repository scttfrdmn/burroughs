# The §§2–5 litmus battery — pre-registered allowed-outcome sets

**A green from this battery means agreement with an oracle this project wrote, which is weaker than
every green before it.** Every other suite in this tree is checked against upstream material we did not
author — that is contract §0's neutrality guarantee. This one is checked against allowed-outcome sets
derived by hand from the contract's own clauses, so a misreading of a clause becomes a wrong verdict
with no second opinion. Two further limits belong in the same breath: a litmus battery is a
**falsifier, never a certificate** — an observed forbidden outcome refutes conformance, while a clean
run bounds nothing about the interleavings that did not occur — and no amount of repetition converts the
absence of a witness into proof. Every verdict this battery emits is therefore spelled *"not observed in
N runs on both architectures"* and never *"conforms"*.

This file is **data**. Nothing in it runs, and no test in this tree passes because of it. The one green
in its neighbourhood belongs to `TestEveryClauseInSectionsTwoThroughFiveIsPreregistered`, which checks
this document against the contract and asserts nothing whatever about the engine.

## Provenance, and why the tables precede the mechanism

The oracle question was ruled by Scott on [#399](https://github.com/scttfrdmn/burroughs/issues/399),
2026-08-18: contract §§2–5 as the specification cited by clause, allowed-outcome sets authored and
pre-registered *before* the implementation exists, arm64 versus amd64 as the **external arbiter**,
`-race` as the host half, and a differential against a mainstream engine refused — because §4's whole
provenance is D20, the browser host as the known non-conformer, so a differential oracle would ratify
the defect the contract exists to forbid. ADR 0055 is that ruling's tombstone; this file is its
implementation.

**The PR that lands a mechanism may not edit this file.** Editing an outcome set is permitted only in
its own PR, stating what was wrong about the clause reading — because a forecast rewritten by the run it
forecasts is not a forecast. That is the whole value of landing these tables while the engine cannot yet
run a single one of them: written later, they would be written by someone who had seen the mechanism.

## How to read an entry

One `###` entry per clause in §§2–5 — all seventeen, including the ones no litmus case can reach, so
that a clause with no case is **visible** rather than absent. Each entry carries:

- **Quotation.** The clause as the contract writes it, verbatim, as a blockquote, checked as a
  contiguous substring of the clause's own text — so a contract amendment breaks this file rather than
  letting it drift silently away from what it claims to quote.
- **Shape**, one of: `outcome` (an observation tuple with allowed and forbidden sets), `timing` (a
  verdict on a distribution against a pre-registered ceiling), `structural` (a property of the tree,
  discharged by a control rather than by a race), or `contract-deferred` (the clause's content is a §10
  open question, so no outcome set can exist yet).
- **Blocked by**, naming what must exist before the entry's cases can run at all.

Each `outcome` and `timing` clause then carries one or more `#### Case` blocks with **Discharges**,
**Allowed**, **Forbidden**, **Witness** (the interleaving that must occur, and what makes the harness
capable of producing it — agent count, round count, whether a bounded spin must be removed), **Floor**
(the fraction of runs that must reach the interleaving, below which the case **fails as un-witnessed**
rather than passing — a case that cannot say this cannot distinguish *conforming* from *never raced*),
**Arbiter** (which architecture is expected to discriminate, and where the honest answer is *neither,
this is a scheduling claim*), and **Status**.

**Status** is `blocked — #N` or `implemented — TestName`. When a case is implemented its status names
the test, and the named test must resolve to a real declaration or the control fails. A case whose
blocker has been discharged while its status still reads `blocked` is the way this document is most
likely to rot, which is why the sequencing below says out loud which two issues will make that happen.

## Coverage, fixed in advance

**Aligned accesses only. Unaligned is named as uncovered.** Every case below observes naturally aligned
words, because that is the population ADR 0054's mechanism makes atomic; the unaligned path is byte-wise
and its tear-freedom is asserted by nothing. This is Scott's order on the #567 stamp, written here rather
than discovered by someone reading a green as covering more than it does.

The Go-runtime torture set (STW under load, checkdead soundness in both directions, the sleeper-deadlock
inverse control, and positive controls for the classifiers) is **not here**. Its oracle is Go's runtime
behaviour rather than the Burroughs contract, which is the split-at-the-oracle-seam shape, so it is
[#406](https://github.com/scttfrdmn/burroughs/issues/406) instead.

## Sequencing, and the two blockers that are not the same blocker

Nothing in this file can run today, and the reasons are ordered:

1. **These tables** — this slice. They assert nothing about the engine, and they are cheapest before the
   mechanism exists.
2. **Spawn** — [#554](https://github.com/scttfrdmn/burroughs/pull/554). `Instance.Spawn` is not on
   `main`, so the tree has exactly one agent and every case below needs two.
3. **Suspend and wake** — [#543](https://github.com/scttfrdmn/burroughs/issues/543).
   `memory.atomic.wait`'s blocking path returns `ErrUnsupportedOp` because this engine has no scheduler
   to suspend, and `memory.atomic.notify` wakes nothing — correctly, because nothing can be waiting.
   **Spawn does not fix this.** Two agents can race over plain memory without either ever suspending, so
   #554 unblocks B-MM-1 and H-1 while B-MM-2 — the sibling-field-after-wake case the contract names by
   hand — stays blocked behind #543.
4. **The battery.**

That #543 is a *second* blocker, distinct from #554, is stated here where it is cheap rather than where
it is a surprise.

---

## §2. Threads

### T-1 — the spawn primitive

> The engine MUST provide a thread-spawn host primitive of the shape `spawn(entry_func, arg,
> stack_hint) → tid`, creating a wasm thread backed 1:1 by an OS thread, sharing the module's shared
> linear memory.

- **Shape:** outcome
- **Blocked by:** #554

#### Case `t1-n-agents-block-simultaneously`

- **Discharges:** T-1
- **Allowed:** `blocked == N`, for `N = 8` agents each parked in `memory.atomic.wait32` on a distinct
  naturally-aligned word of one shared memory.
- **Forbidden:** `blocked < N`. A pool, an event loop, or an M:N mapping cannot hold N agents parked at
  once; 1:1 OS threads can. `blocked > N` is an instrument fault, not a verdict.
- **Witness:** all N agents must reach their wait before the observation. The harness spawns N, then
  polls a host-side parked counter until it reads N or a deadline expires; **the deadline expiring is
  the forbidden outcome, not a discard.**
- **Floor:** every run must reach `blocked == N` or report the deadline. There is no interleaving to
  miss here, so a discard would be an instrument fault.
- **Arbiter:** neither — this is a scheduling claim, and both architectures are expected to agree. Run
  on both anyway, because "expected to agree" is a prediction this suite is in a position to check.
- **Status:** blocked — #543

### T-2 — no main-thread special case

> There is **no main-thread special case**. Every thread, including the first, MUST be permitted to
> block in `memory.atomic.wait` and in blocking host calls. No agent is forbidden from sleeping.

- **Shape:** outcome
- **Blocked by:** #543

#### Case `t2-the-first-agent-waits-and-a-child-wakes-it`

- **Discharges:** T-2
- **Allowed:** the instantiating agent's `memory.atomic.wait32` returns `0` (woken), after a spawned
  child stores the expected word and notifies.
- **Forbidden:** a trap; `ErrUnsupportedOp`; a return of `2` (timed out) under an infinite timeout; a
  return of `0` with no notify having been issued.
- **Witness:** the first agent must enter the wait before the child's store, which the harness arranges
  by having the child spin on a host-side flag the first agent sets immediately before waiting. That
  flag is host state, not guest memory, so setting it cannot itself supply the edge under test.
- **Floor:** at least 90% of runs must report `0`; below that the case fails as un-witnessed, since a run
  whose wait returned `1` (not-equal) never tested the clause.
- **Arbiter:** neither — a scheduling claim.
- **Status:** blocked — #543

### T-3 — futex-backed wake latency

> `memory.atomic.wait` / `notify` MUST be futex-backed with OS-native wake latency, not
> event-loop-turn latency.

- **Shape:** timing
- **Blocked by:** #543

#### Case `t3-wake-latency-is-microsecond-scale`

- **Discharges:** T-3
- **Allowed:** median wake latency **below 1ms** over `K = 1000` wake events, measured host-side from the
  notifying agent's return to the woken agent's first executed instruction.
- **Forbidden:** median at or above 1ms.
- **Witness:** one waiter, one waker, K rounds, the waiter re-arming between rounds. The median is
  reported on every run whether or not it is asked to fail — a timing case that prints only on failure
  cannot show a drift toward its own ceiling.
- **Floor:** all K rounds must complete; a round whose wait returned not-equal is re-run rather than
  discarded, and the re-run count is reported.
- **Arbiter:** neither. **This ceiling is a mechanism discriminator, not a performance target**, and it
  is taken from the clause's own contrast: an event-loop turn is milliseconds-scale and a futex wake is
  microseconds-scale, so 1ms separates the two mechanisms by an order of magnitude in either direction
  and is machine-dependent only inside a band it does not care about. A number tuned to this machine's
  actual latency would be a performance gate wearing a conformance hat.
- **Status:** blocked — #543

### T-4 — the per-thread slot

> The engine MUST provide a per-thread slot readable at register-like cost (the `g` register analog),
> stable across host calls and stack switches.

- **Shape:** structural
- **Blocked by:** nothing — the mechanism landed with ADR 0050 (#514).
- **Why no litmus case:** "register-like cost" is a cost claim, and a cost claim's oracle is a benchmark
  rather than an outcome tuple — no interleaving can witness it. The stability half (*"stable across host
  calls and stack switches"*) **is** outcome-shaped, but §7's stack switching does not exist, so the case
  would have half a subject. Registered as **uncovered by this battery**, discharged by ADR 0050's
  measured bill and the single-pointer reach in `internal/interp/thread.go`; when §7 lands, the stability
  half is a case to add in the PR that lands it.

### T-5 — thread exit, join, detach

> Thread exit, join, and detach semantics MUST be defined in this contract (open: §10.3) rather than
> inherited implicitly from the host OS.

- **Shape:** contract-deferred
- **Blocked by:** contract §10.3, which is Scott's and chat-Claude's to close rather than this battery's.
- **Why no outcome set:** the clause's normative content is *that the semantics be written down*, and they
  are not written down yet. An allowed-outcome set authored now would be this project inventing the
  semantics in a test file, which is decision-before-code inverted. When §10.3 closes, T-5 gets its cases
  in the PR that closes it.

## §3. Safepoints and preemption

### SP-1 — engine-native preemption

> Preemption is **engine-native**. The engine MUST implement epoch/safepoint checks (loop back-edges
> and call sites) such that a host request `stop(deadline)` brings every guest thread to a safepoint
> within a bounded, configurable interval. The guest runtime MUST NOT need to self-instrument its own
> code generation to be stoppable.

- **Shape:** outcome
- **Blocked by:** #554, and the epoch mechanism itself, which is unwritten.

#### Case `sp1-stop-brings-every-agent-to-a-safepoint-by-the-deadline`

- **Discharges:** SP-1
- **Allowed:** `stop(deadline)` returns within `deadline`, and every one of `N = 8` agents running an
  unbounded loop is reported at a safepoint.
- **Forbidden:** `stop` returning after `deadline`; any agent's per-agent progress counter advancing after
  `stop` has returned, read twice 10ms apart.
- **Witness:** each agent must be *inside* its loop at the moment of the request, which the harness
  arranges by waiting until every agent's counter has advanced at least once. **The loop body is
  arithmetic only, containing no host call** — a guest whose loop called out would be stopped at the
  boundary rather than by an epoch check, so a case that let a host call in would pass without testing
  the clause.
- **Floor:** every run must observe all N counters advancing before the request; a run that does not is an
  instrument fault.
- **Arbiter:** neither — a scheduling claim.
- **Status:** blocked — #554

### SP-2 — a parked thread is at a safepoint

> A thread blocked in a host call or in `memory.atomic.wait` counts as **at a safepoint** for
> stop-the-world purposes, and the engine MUST guarantee it cannot touch guest memory until it re-enters
> through a boundary that observes the stop

- **Shape:** outcome
- **Blocked by:** #554, and the epoch mechanism.

#### Case `sp2-a-parked-agent-touches-no-guest-memory-during-the-stop`

- **Discharges:** SP-2
- **Allowed:** with `N = 4` agents parked in a blocking host call that writes a known naturally-aligned
  word on return, `stop(deadline)` returns, and each of those words still reads its pre-stop value for as
  long as the stop is held.
- **Forbidden:** any of the N words changing while the stop is held. Also forbidden: `stop` reporting fewer
  than N agents at a safepoint, since a parked agent *counts as* at one.
- **Witness:** the host call must be parked, and its return path must be the thing that writes. The harness
  holds the call in the host until after the stop is taken, releases it, and observes that the write lands
  only after the stop is lifted.
- **Floor:** every run must confirm all N parked before the request.
- **Arbiter:** **arm64 is expected to discriminate.** A too-weak re-entry edge lets the parked agent's
  write become visible early, which is a reordering TSO structurally cannot exhibit.
- **Status:** blocked — #554

### SP-3 — the timer channel is disjoint from guest sync state

> The engine MUST expose a deadline/timer wake facility as a first-class API whose delivery channel is
> **disjoint from guest-visible synchronization state**. Engine timer machinery MUST NOT write to memory
> locations that guest scheduler notes alias.

- **Shape:** structural
- **Blocked by:** the timer facility, which is unwritten.
- **Why no litmus case:** the clause forbids an *aliasing*, and no interleaving can witness the absence of
  one — a race that never fires is what a conforming engine and a lucky non-conforming one both look like.
  The provenance says as much: the browser host's bell cost a full investigation *on suspicion* of
  aliasing. So SP-3's discharge is a control over the timer path's write set — every address the timer
  machinery writes must lie outside every linear memory — registered here so that landing the timer
  facility without that control is a visible omission rather than an oversight. Its shape's sibling is
  `TestNothingInEngineCodeCreatesASecondObserver`: a syntactic total over engine code rather than a
  reachability argument.

### SP-4 — stop composes with parked agents

> `stop()` MUST compose with §2: stopping the world with N threads parked in host calls completes
> without waking them.

- **Shape:** outcome
- **Blocked by:** #554, and the epoch mechanism.

#### Case `sp4-stop-completes-without-waking-parked-agents`

- **Discharges:** SP-4
- **Allowed:** `stop(deadline)` returns within `deadline` with all `N = 4` parked agents still parked — a
  host-side wake counter of `0`.
- **Forbidden:** a wake counter above `0`; `stop` failing to return within `deadline`. **Both halves are
  named**, because an engine that satisfied this clause by waking everybody would otherwise score a pass
  on "completes".
- **Witness:** N agents parked in a blocking host call that records its own wake, plus one agent in a hot
  loop so the stop has something to stop.
- **Floor:** every run must confirm all N parked before the request.
- **Arbiter:** neither — a scheduling claim.
- **Status:** blocked — #554

## §4. The boundary memory model

### B-MM-1 — the boundary is an acquire/release edge over the whole address space

> Every host→guest transition — host-call return, trap resume, async wake, stack-switch resume — MUST
> constitute an **acquire edge over the entire shared address space** for the resuming agent. Every
> guest→host transition MUST constitute the corresponding release edge.

- **Shape:** outcome
- **Blocked by:** #554

#### Case `b-mm-1-message-passing-across-a-host-call-return`

- **Discharges:** B-MM-1
- **Allowed:** observation `(p, d)`, where `p` is what the host `poll` call returned to agent B and `d` is
  what B then read from the data word: `(0,0)`, `(0,1)`, `(1,1)`.
- **Forbidden:** `(1,0)` — B's `poll` returned 1, which the host sets only after A's `publish` call has
  returned, and B nonetheless read the pre-publish value of a word A wrote before calling `publish`. The
  release edge on A's guest→host transition and the acquire edge on B's host→guest return are the only
  things that forbid it.
- **Witness:** A stores `1` to a naturally-aligned data word and calls host `publish`; B spins calling host
  `poll` and, on the first `1`, loads the data word once. Both words live in one shared memory, and
  `poll`'s answer travels through host state rather than guest memory, so the data word is the only channel
  under test. `R = 100_000` rounds, fresh words per round.
- **Floor:** at least 95% of rounds must observe `p == 1` within their spin bound; below that the case fails
  as un-witnessed. A round that exhausts its spin bound is discarded and counted, and the discard count is
  reported on every run.
- **Arbiter:** **arm64 is expected to discriminate.** `(1,0)` requires the data store to become visible
  after the boundary, which x86-TSO's store ordering structurally forbids — B-MM-5's provenance is this
  exact asymmetry, so a case observed on neither architecture is reported as *not observed on either* and
  never merged into one green.
- **Status:** blocked — #554

### B-MM-2 — a wake synchronizes every write, not the futex word

> A wake delivered to a waiting agent MUST synchronize **all** writes that happened-before the wake on
> the waking agent — not only the futex word. "The notified word only" is expressly non-conforming.

- **Shape:** outcome
- **Blocked by:** #543

#### Case `b-mm-2-sibling-field-after-wake`

This is the case the contract names by hand, and D20 is its provenance: on the browser host,
`Atomics.notify` establishes happens-before for the notified word only, and a sibling field's store can lag
the woken agent's resume even when the read occurs under a freshly acquired lock.

- **Discharges:** B-MM-2
- **Allowed:** observation `(r, d)`, where `r` is `memory.atomic.wait32`'s result on agent B and `d` is the
  sibling word B reads immediately after: `(0,1)` — woken, sibling visible; `(1,0)` and `(1,1)` — the wait
  returned not-equal because B arrived after A's store to the futex word, so no wake edge was claimed.
- **Forbidden:** `(0,0)` — B was woken by A's notify and read a stale sibling field. `(2, *)` under an
  infinite timeout is an instrument fault.
- **Witness:** A stores `1` to a naturally-aligned sibling word, then `i32.atomic.store`s `1` to the futex
  word, then notifies. B waits on the futex word for `0`, then loads the sibling word once. B must be inside
  the wait before A's atomic store, which the harness arranges with a host-side arming flag — host state, so
  the arming cannot itself supply the edge under test. `R = 100_000` rounds.
- **Floor:** at least 80% of rounds must report `r == 0`; below that the case fails as un-witnessed, because
  the not-equal rounds test nothing. The `r == 1` share is reported on every run.
- **Arbiter:** **arm64 is expected to discriminate**, for B-MM-1's reason.
- **Status:** blocked — #543

#### Case `b-mm-2-the-sibling-lives-in-a-second-shared-memory`

- **Discharges:** B-MM-2
- **Allowed:** as `b-mm-2-sibling-field-after-wake` — `(0,1)`, `(1,0)`, `(1,1)` — with the sibling word in
  a *different* shared memory from the futex word.
- **Forbidden:** `(0,0)`, for the named case's reason plus the reason this case exists separately: the
  clause says *all* writes, and a per-memory edge would satisfy the named case while publishing nothing
  about a second memory.
- **Witness:** identical, over a module with two shared memories. Registered as its own case because the
  clause says *all* writes while D20's shape is same-memory: an engine could establish a per-memory edge,
  pass the named case, and still publish nothing about a second memory. `R = 100_000` rounds.
- **Floor:** as above, 80% woken.
- **Arbiter:** **arm64 is expected to discriminate.**
- **Status:** blocked — #543, and the multiple-memories gate

### B-MM-3 — no engine lock across a resume

> The engine MUST NOT hold engine-internal locks across a guest resume, and MUST NOT resume a guest
> agent in a state where a previously acquired guest lock is held without the acquire edge of B-MM-1
> having been established.

- **Shape:** structural
- **Blocked by:** nothing.
- **Why no litmus case:** the clause forbids a *state at the moment of resume*, and an interleaving that
  happens not to deadlock is indistinguishable from a resume that never held a lock. It is already
  discharged the way it should be: `TestNoSyncPrimitiveIsUsedInEngineCode` is a total over engine code — no
  `sync` import at all, so there is no lock to hold across anything — and the clause's second half, the
  acquire edge, is what `b-mm-1-message-passing-across-a-host-call-return` observes. The overlap is named
  rather than double-counted: **B-MM-3 adds no outcome tuple of its own**, and if the `sync` prohibition is
  ever relaxed, this entry is where the case it would then need must be written.

### B-MM-4 — publication semantics are documented, default sequentially consistent

> Each host call's memory-publication semantics MUST be documented in its signature. The default, absent
> annotation, is sequentially consistent.

- **Shape:** structural
- **Blocked by:** nothing.
- **Why no litmus case:** the normative content is a documentation requirement plus a default, and no race
  can witness whether a signature carries a comment. The default's *behaviour* is
  `b-mm-1-message-passing-across-a-host-call-return`, which observes exactly the sequentially consistent
  boundary an un-annotated host call promises. The documentation half is discharged at
  `internal/interp/boundary.go`, whose comment states the default and where it is established; the
  per-host-call annotations arrive with the host-call surface, which does not exist yet. Registered so that
  a host call landing without its annotation is a visible omission.

### B-MM-5 — the guarantees are testable

> These guarantees are **testable**: the conformance suite (§9) MUST include a litmus battery for
> boundary edges — including the sibling-field-after-wake case — run on both a TSO and a weakly-ordered
> platform.

- **Shape:** structural
- **Blocked by:** #554 and #543, being the whole battery.
- **Why no litmus case:** the clause's subject is this file and the suite it describes, so a case
  discharging it would be a suite testing its own existence. Its three named requirements are checked where
  each can be: the battery's existence and the sibling-field-after-wake case by
  `b-mm-2-sibling-field-after-wake` above, and *"run on both a TSO and a weakly-ordered platform"* by the
  CI matrix, which already builds and tests on `ubuntu-24.04` (amd64, TSO) and `ubuntu-24.04-arm` (arm64,
  weakly ordered) on every push. **The clause is satisfied by the suite property, not by a row**, and the
  row that would claim otherwise is the vacuous kind.

## §5. Blocking host calls

### H-1 — a blocking host call blocks its thread only

> A blocking host call blocks **its thread only**. No global loop, no starvation of sibling agents, no
> requirement that the guest reach an event-loop turn for siblings to make progress.

- **Shape:** outcome
- **Blocked by:** #554

#### Case `h1-a-parked-agent-does-not-starve-its-siblings`

- **Discharges:** H-1
- **Allowed:** with agent A parked in a blocking host call for a held interval `T = 100ms`, sibling agent
  B's progress counter advances — any nonzero advance.
- **Forbidden:** B's counter advancing zero times over `T`. The clause's content is that A's park does not
  require an event-loop turn for B to run, so **zero is the whole failure and a merely slow B violates
  nothing here.** B's advance count is reported on every run, so a collapse toward zero is visible before it
  arrives.
- **Witness:** A must be parked, confirmed host-side, before B's counter is sampled; B's loop contains no
  host call, so its progress cannot be an artifact of the boundary.
- **Floor:** every run must confirm A parked before sampling.
- **Arbiter:** neither — a scheduling claim.
- **Status:** blocked — #554

### H-2 — no surprise reentrancy

> Host calls MUST NOT re-enter the guest on the caller's stack (no surprise reentrancy). Callbacks, if
> ever offered, are delivered via §6 readiness, never by nested guest entry.

- **Shape:** structural
- **Blocked by:** nothing.
- **Why no litmus case:** the clause forbids a *call shape*, which is a property of the host-call surface
  rather than of an interleaving — a single agent can violate it, so no race is needed to see it and no race
  can prove its absence. `TestEveryBoundaryCrossingIsPaired` and
  `TestEveryStackCreationSiteCrossesTheBoundary` are the existing halves: they make every guest entry go
  through `enterGuest`/`leaveGuest` and count the crossings per call shape, so a nested entry inside a host
  call would show as an unpaired or over-counted crossing. Registered because that is a *presence* oracle —
  it sees a crossing that happened, and the clause forbids one particular caller. The gap is stated rather
  than claimed closed.

### H-3 — cancellation

> Cancellation: a thread parked in a blocking host call MUST be interruptible by engine shutdown and MAY
> be interruptible by a guest-visible cancel primitive (open: §10.4).

- **Shape:** outcome
- **Blocked by:** #554

#### Case `h3-shutdown-interrupts-a-parked-agent`

- **Discharges:** H-3, the MUST half only.
- **Allowed:** with `N = 4` agents parked in a blocking host call, engine shutdown causes every one of the N
  calls to return within `deadline`, each reporting a cancellation error.
- **Forbidden:** any call failing to return within `deadline`; **any call returning *success***, which would
  report a completed operation that never completed and is worse than a hang, because nothing downstream can
  tell.
- **Witness:** all N confirmed parked host-side before shutdown is requested.
- **Floor:** every run must confirm all N parked.
- **Arbiter:** neither — a scheduling claim.
- **Status:** blocked — #554

The MAY half — a guest-visible cancel primitive — is contract-deferred to §10.4 and gets its cases in the PR
that closes it, for T-5's reason: an allowed-outcome set authored now would be this battery inventing the
semantics.
