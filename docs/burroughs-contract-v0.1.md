# The Burroughs Contract

**Host contract for Burroughs — the Wasm engine for Go.**
v0.1 (working draft) · burroughs.run · July 2026

> The B5000 favored ALGOL. Burroughs favors Go.

This document is written before the engine, on purpose. Every clause below is
a promise the engine must keep, and most clauses descend from a specific,
named failure discovered while porting a real Go runtime onto hosts that made
no such promises. Provenance notes cite those graves. The key words MUST,
MUST NOT, SHOULD, and MAY are used as in RFC 2119.

---

## §0. Thesis

Burroughs is a **language-directed engine**: a WebAssembly runtime whose host
contract, fast paths, and API surface are designed to the specification of
the Go runtime. This is the design philosophy of the Burroughs Large Systems
(B5000, 1961) — hardware built so that one language wins — applied to a stack
machine sixty-five years later, for a different language, in the other
direction of abstraction.

**Correctness-neutral, performance-partisan.** Any spec-conforming guest runs
correctly on Burroughs (§9 makes this enforceable). But where a design
tradeoff exists, Go wins. Other guests have alternatives; that is what
wasmtime is for.

## §1. Non-goals

1. Peak throughput parity with Cranelift/V8 optimizing tiers. Correctness,
   contract fidelity, and spec-edge agility are the product.
2. Browser embedding. Burroughs is a native host; the browser constraint set
   (single-threaded event loop, no blocking on the main agent, Worker=thread
   cosplay) is precisely the pathology this engine exists to not have.
3. Guest-language ergonomics for non-Go guests beyond spec conformance.
4. (v0 posture) Production hardening, sandboxing guarantees for hostile
   modules, fuzzing depth. Research engine first; harden when the contract
   is stable.

## §2. Threads

- **T-1.** The engine MUST provide a thread-spawn host primitive of the shape
  `spawn(entry_func, arg, stack_hint) → tid`, creating a wasm thread backed
  1:1 by an OS thread, sharing the module's shared linear memory.
  *This is `newosproc`, not a Worker with a message port.*
- **T-2.** There is **no main-thread special case**. Every thread, including
  the first, MUST be permitted to block in `memory.atomic.wait` and in
  blocking host calls. No agent is forbidden from sleeping.
  *Provenance: the single-M assumption confessed nine-plus faces during the
  wasip1 port — async-m0 notes, m0-heartbeat, bell keep-alive, checkdead
  redrive — all scar tissue from a host where the primary agent could not
  block. Burroughs deletes the entire class.*
- **T-3.** `memory.atomic.wait` / `notify` MUST be futex-backed with
  OS-native wake latency, not event-loop-turn latency.
- **T-4.** The engine MUST provide a per-thread slot readable at
  register-like cost (the `g` register analog), stable across host calls and
  stack switches.
- **T-5.** Thread exit, join, and detach semantics MUST be defined in this
  contract (open: §10.3) rather than inherited implicitly from the host OS.

## §3. Safepoints and preemption

- **SP-1.** Preemption is **engine-native**. The engine MUST implement
  epoch/safepoint checks (loop back-edges and call sites) such that a host
  request `stop(deadline)` brings every guest thread to a safepoint within a
  bounded, configurable interval. The guest runtime MUST NOT need to
  self-instrument its own code generation to be stoppable.
- **SP-2.** A thread blocked in a host call or in `memory.atomic.wait`
  counts as **at a safepoint** for stop-the-world purposes, and the engine
  MUST guarantee it cannot touch guest memory until it re-enters through a
  boundary that observes the stop (this is `entersyscall`/`exitsyscall` as
  an engine guarantee rather than a runtime convention).
- **SP-3.** The engine MUST expose a deadline/timer wake facility as a
  first-class API whose delivery channel is **disjoint from guest-visible
  synchronization state**. Engine timer machinery MUST NOT write to memory
  locations that guest scheduler notes alias.
  *Provenance: the bell. Stage-3 taught bell ≠ event queue; the D20 hunt's
  candidate-(ii) round taught that even suspecting bell-writes-alias-notes
  costs a full investigation. Burroughs makes the aliasing impossible by
  construction rather than absent by luck.*
- **SP-4.** `stop()` MUST compose with §2: stopping the world with N threads
  parked in host calls completes without waking them.

## §4. The boundary memory model

*The heart of the contract. Every clause here descends from the host-wake
visibility gap (D20): on the browser host, `Atomics.notify` establishes
happens-before for the notified word only; a sibling field's store
propagates separately and can lag the woken agent's resume even when the
read occurs under a freshly acquired lock. Native futex wake has no such
gap. The downstream fix in the guest was a bounded spin; Burroughs exists so
that spin is unnecessary.*

- **B-MM-1.** Every host→guest transition — host-call return, trap resume,
  async wake, stack-switch resume — MUST constitute an **acquire edge over
  the entire shared address space** for the resuming agent. Every
  guest→host transition MUST constitute the corresponding release edge.
  Equivalently: a sequentially-consistent fence at the boundary, both
  directions.
- **B-MM-2.** A wake delivered to a waiting agent MUST synchronize **all**
  writes that happened-before the wake on the waking agent — not only the
  futex word. "The notified word only" is expressly non-conforming.
- **B-MM-3.** The engine MUST NOT hold engine-internal locks across a guest
  resume, and MUST NOT resume a guest agent in a state where a previously
  acquired guest lock is held without the acquire edge of B-MM-1 having
  been established. A lock held across an async boundary without a
  synchronization edge is the hazard class; the contract closes it for
  every field, not per-field.
- **B-MM-4.** Each host call's memory-publication semantics MUST be
  documented in its signature. The default, absent annotation, is
  sequentially consistent.
- **B-MM-5.** These guarantees are **testable**: the conformance suite (§9)
  MUST include a litmus battery for boundary edges — including the
  sibling-field-after-wake case — run on both a TSO and a weakly-ordered
  platform.
  *Provenance: mac/ARM exhibited what janus/x86-TSO structurally could not;
  a contract clause without a weak-memory witness is a wish.*

## §5. Blocking host calls

- **H-1.** A blocking host call blocks **its thread only**. No global loop,
  no starvation of sibling agents, no requirement that the guest reach an
  event-loop turn for siblings to make progress.
- **H-2.** Host calls MUST NOT re-enter the guest on the caller's stack
  (no surprise reentrancy). Callbacks, if ever offered, are delivered via
  §6 readiness, never by nested guest entry.
- **H-3.** Cancellation: a thread parked in a blocking host call MUST be
  interruptible by engine shutdown and MAY be interruptible by a
  guest-visible cancel primitive (open: §10.4).

## §6. The event loop and readiness (wasip3)

*WASI 0.3 (ratified June 2026) moves async into the component model:
`future<T>`, `stream<T>`, and a host-owned event loop shared by all
components. A host-owned loop is a netpoller-shaped object; Burroughs
commits to that reading.*

- **R-1.** The engine MUST expose batch readiness:
  `poll_ready(buf, max, deadline_ns) → n`, returning all currently ready
  futures/streams up to `max`; `deadline_ns = 0` is non-blocking; a blocked
  `poll_ready` MUST be wakeable by `spawn`, by wake primitives, and by
  engine shutdown (the netpoller-break analog).
- **R-2.** Readiness is **pulled by the guest scheduler**, never pushed by
  callback into guest code. The integration point is `findRunnable`, not an
  event handler.
- **R-3.** Ordering: readiness delivery observes B-MM-1 — a future reported
  ready MUST have its payload's writes visible to the polling agent without
  further synchronization.
- **R-4.** The loop MUST be shareable across component instances per the
  0.3 model, with per-instance isolation of handle tables.

## §7. Stacks

- **S-1.** The engine implements the stack-switching proposal
  (phase-tracking; §9 gates) with **growable continuation stacks**: a
  continuation is created with an initial size and grows via an engine
  hook — the `morestack` analog — rather than failing or pre-committing
  worst case.
  *A stack-switching implementation with fixed-size continuations forces a
  goroutine model into overallocate-or-die. This clause is the single
  largest Go-partisan divergence from current prototypes.*
- **S-2.** Switch cost SHOULD be O(register save/restore) with no host-call
  round trip on the switch path (the WasmFX libcall→native transition
  measured ~6× on microbenchmarks; Burroughs starts native).
- **S-3.** All continuation stacks MUST be enumerable and walkable at a
  §3 stop, so the guest GC can scan them. The walk interface is part of
  this contract (open: §10.5).
- **S-4.** Stack switches are boundary transitions for §4 purposes
  (B-MM-1 applies).

## §8. Memory

- **M-1.** `memory.grow` MUST be amortized O(pages touched): address space
  reserved up front, commit on grow, no full-copy growth path. Go will
  call it; it is not an exceptional event.
- **M-2.** The engine MUST provide a decommit hook —
  `memory.decommit(offset, len)` or equivalent host call — mapping to
  `madvise(DONTNEED)`-class behavior, so the guest scavenger can return
  memory to the OS and the RSS high-water mark is not a ratchet.
- **M-3.** memory64 and multiple memories are supported (§9 gates).
- **M-4.** WasmGC is implemented for conformance (§9) and deliberately
  unoptimized. Go brings its own collector; that is the point.

## §9. Gates, conformance, and the edge

- **G-1.** Every proposal lives behind a gate. A gate's acceptance is its
  upstream spec test suite green, on both a TSO and a weakly-ordered
  platform, before default-on — modulo vectors whose sole attributed
  blocker is **#9's own deferred validator** (`ErrNotValidated`),
  attribution by the engine's own error taxonomy rather than by assertion,
  and only when the gate's own suite carries zero required-engine-execution
  defects (no missing arms, no value mismatches, no anything-else) once
  that population is set aside.
  *Experiment-gated, decision-doc-then-land — the discipline that carried
  the Go-side work is the engine's release discipline too.*
  *Amended by Scott's stamp on #230 (ADR 0025). A literal "green, full
  stop" reading is unsatisfiable by any gate for the whole of v0: #9's
  absence blocks every proposal's suite identically, since any suite
  containing a vector whose module reaches the interpreter unvalidated
  trips the same wall, and that contradicts the phase ladder the same
  contract establishes. The carve-out is named to #9 specifically, not to
  a category of validator gaps. **It retires when `ErrNotValidated` has no
  reachable call site in the engine** — a state of the code, checkable by
  grep, by `deadcode`, and by the compiler once the sentinel's declaration
  goes. No second amendment repeals it: at that state the carve-out has
  nothing left to except. It does not excuse missing arms or wrong
  answers: it excuses only the one named, structurally deferred question
  #9 has not yet been asked. Precedent: G-2's own #109 amendment, which
  named the true criterion rather than leaving a silent or ad hoc reading.*
  *Retirement condition amended by Scott's stamp on the #482 review (ADR
  0043, deliberation #483). It previously read "retires itself when #9
  lands", which is a tracker event: closing the validator umbrella is a
  state transition a person performs, and it is the correct bookkeeping
  once that issue's residue is re-pointed at the slices still holding
  work, while `ErrNotValidated`'s call sites survive it untouched. So the
  clause was satisfiable by an act that changed no code. Two corollaries,
  stated because a later reader would otherwise re-supply them wrongly:
  the carve-out's subject going empty in a gate's suite makes it **inert
  in that suite** and not retired — that zero has a second cause, a
  harness that has lost the ability to attribute — and #9's issue state is
  evidence about the condition at best, never the condition. What this
  amendment leaves untouched is the excepted population: same vectors,
  same attribution rule, same zero-defect requirement on the residue.*
- **G-2.** Tracked set at v0.1: **all of Wasm 3.0 core**; threads;
  stack switching (pre-phase-4, tracked); component model + WASI 0.3.
  Wasm 3.0 core is the ten features the spec's own release appendix lists
  (`document/core/appendix/changes.rst`, "Release 3.0"): extended
  constant expressions, tail calls, exception handling, multiple memories,
  64-bit address space, typeful references, garbage collection, relaxed
  vector instructions, profiles, custom annotations.
  *Amended by Scott's stamp on #109. The clause previously named six of
  the ten in a parenthetical, and the enumeration was load-bearing:
  extended-const was absent, so a decoder that rejected nine valid suite
  modules read as tracking the set correctly. An enumeration is a sample
  and a sample has a blind spot by construction, so the normative fact is
  now "all of 3.0 core" and the list is a derivation from the appendix,
  auditable rather than illustrative. Two of the ten are not gates and
  say so where gates are declared: profiles is an execution mode, and
  custom annotations is a text-format rule the lexer already implements.*
- **G-3.** **The neutrality guarantee is G-1.** Partisanship lives only in
  §§2–8's API surface and in optimization priorities — never in
  conformance. No guest may be broken to make Go faster.
  **The guarantee has a ceiling, and it is part of the guarantee.** The
  upstream suites are a corpus of *rejections*: a vector asserts that a
  named module is refused, and is satisfied by a refusal for any reason.
  So no vector can witness a defect in the **accept direction** — a rule
  that admits an invalid module, or refuses a valid one, or answers
  correctly for a wrong reason, scores green by construction. A green
  bounds what has been refuted; it does not certify what is correct.
  Accept-direction correctness is therefore established by controls
  against the reference, never by the board, and a claim resting on a
  green states which direction it is in.
  *Ratified, not introduced, by Scott's stamp on the #486 review (ADR
  0046, deliberation #442), dated 2026-08-22. This is a property of a
  negative-vector corpus, so it held from genesis and every verdict this
  project has ever recorded was already bounded by it; the clause is
  written down because 243 sites in the tree cited **G-3** for it while
  §9 stated it nowhere, and a citation whose referent does not exist
  leaves a reader unable to tell whether the code or the contract moved.
  It lands on G-3 rather than as a new clause because G-3 is what names
  the neutrality guarantee, and this is that guarantee's limit — the
  same subject from the other side. Zero of those 243 sites cited G-3
  for its first two sentences, so the reading being ratified is the only
  reading the tree has ever used. The existing sentences are untouched
  and remain normative.*
- **G-4.** The Burroughs conformance battery = upstream suites + the §4
  litmus battery + a Go-runtime torture set (STW under load, checkdead
  soundness both directions, sleeper-deadlock inverse control) promoted
  from the wasip1 port's batteries into permanent CI.
  *The judge needs a judge: positive controls for the classifiers ship in
  the same battery.*

## §10. Open questions

1. **Value representation & dispatch** in a Go-hosted interpreter without
   guaranteed TCO: switch dispatch vs closure-compilation vs generated
   code; in-place interpretation (Wizard-style) vs internal rewrite.
2. **Component model scope**: own canonical-ABI implementation vs
   interoperating through wasm-tools/jco for lifting/lowering while owning
   the async/task machinery.
3. Thread exit/join/detach semantics (T-5).
4. Cancellation of blocked host calls (H-3).
5. The continuation walk interface for GC scanning (S-3) — cooperative
   metadata vs engine-maintained maps.
6. **GOOS=wasip3 ABI negotiation**: which of §§2–8 become the reference
   host requirements in an eventual Go port proposal, and which stay
   Burroughs extensions.
7. Contract versioning: does the contract track WASI point releases or
   version independently?

---

*v0.1 — drafted before the first line of engine code, as required.*
