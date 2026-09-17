# 0087 — The threaded p3 fork charter: a GEOEXPERIMENT over an async base, whose threaded tier has no independent engine — opens Phase 4

Date: 2026-09-17 · Status: **proposed** · [#782](https://github.com/scttfrdmn/burroughs/issues/782) · Recon [#781](https://github.com/scttfrdmn/burroughs/issues/781) · Would open the real-threads fork (contract §§2–6)

**Held for Scott's stamp.** `Status: proposed` cites no approval yet — behaviour 3: an ADR's `Status` is a citation to an approval, held open until a stamp exists to point at. This draft opens the tier in the same form [#688 → ADR 0084](0084-the-component-loader-and-canonical-abi-lift-lower-for-value-types-sync-only-behind-gate-components.md) opened the component tier and [#735 → ADR 0086](0086-the-async-canonical-abi-behind-gate-async-opens-phase-3.md) opened the async tier. Drafted by the actor from committed sources (see *Sources*); durability is not independence, so this ADR stays `Ratio-Class: carried`.

## What this fork is, and is not

**Is:** a fork of the Go toolchain and runtime that compiles Go to a **WASI 0.3 (p3) component that also uses real threads**, run on Burroughs. It binds the Go runtime's OS-facing seams to Burroughs's mechanisms: `newosproc` → `thread.spawn`, GC stop-the-world → an engine `Stop`, `netpoll`/blocking syscalls → the async readiness built-ins (`waitable-set.wait`) and async-lowered imports. It is a **consumer** of Burroughs, not part of the engine.

**Is not:** a `GOOS=wasip3` claim on stock Go (that is §10 open-Q6, unsupported on go1.27.1); a port of wasmpreempt's JS host runtime (that liveness half does not transfer — see *Where it lives*); or a project that certifies its own threaded memory model against an independent engine (there is none — see *Two tiers*).

## Sources (this charter grounds in committed records, not the external memo)

The formal endgame register (its `D-*` decision items, its `R-*` risk items, and the `GEOEXPERIMENT` shape) is **not committed** — it lives in chat-CC's memo, outside the repo (frozen-markdown-footprint rule). This charter grounds in committed sources instead: **[#742](https://github.com/scttfrdmn/burroughs/issues/742)** (D-1; the two Phase-4 charter items; the battery's measured state), **[#688](https://github.com/scttfrdmn/burroughs/issues/688)** (no stock Go→p3 path; `GOOS=wasip3` is §10 open-Q6), the recon **[#781](https://github.com/scttfrdmn/burroughs/issues/781)** (upstream status, the fork delta, the wasmpreempt survey), and the contract §§2–6.

## Context

- **The mechanisms the fork binds to are on `main`, behind off gates.** `thread.spawn` (§2 T-1, [ADR 0068](0068-spawn-drops-0056s-walk-and-refuses-the-two-cases-a-per-instance-world-cannot-express-because-a-thread-belongs-to-exactly-one-stop.md)), `Stop`/`Resume` at safepoints (§3 SP-1), futex wait/notify (§2), and `waitable-set.wait` as a §5/H-4 blocking excursion are all landed — the last did not exist when the chair last said "nothing binds the fork to Burroughs yet." `gate:threads` and `gate:async` are default-off; each flip is its own stamp-tier event (behaviour 4).
- **Upstream is not moving toward this.** golang/go #77141 (the wasip3 port proposal) is OPEN, unaccepted, dormant since 2026-06; #28631 (threads on wasm) and #71134 (async preemption on wasm) are `NeedsDecision`/`Unplanned`; #76775 (`runtime.wasiOnIdle`) is an open, unmerged PR. The fork cannot ride an upstream base into existence — it owns its base (#781 A).
- **The novel delta exists nowhere.** A p3 *component* that also uses real threads is absent upstream (proposal dormant), in wasmpreempt (core `js/wasm`, no component model), and in componentize-go (p3 components, single-threaded). That is why this is a fork.

## Options and decision

The base, the tree/experiment shape, and the first slice are the chair's and Scott's — deliberated on [#782](https://github.com/scttfrdmn/burroughs/issues/782), not settled here. The recon's leading options: sit on the jellevandenhooff `wasip3-prototype` branch (own it, fall back to a self-built substrate if it rots); a `GEOEXPERIMENT`-gated real-threads experiment over that p3-async base in the fork's own Go tree; the first slice a fork-built Go hello-world emitted as a p3 component, run single-threaded on Burroughs, zero real-threads content.

## The capability line, at its two evidence levels

Following ADR 0084/0086: the claim distinguishes what an independent engine verifies from what only the contract-plus-battery verifies.

- **Standard tier (independently arbitrated):** *"a **fork-built Go p3 component** runs on Burroughs behind `gate:async`, its `wasi:cli/run@0.3.0` export driven to the same output an independent engine produces."* Byte-verified against **Wasmtime** (full p1/p2/p3) and the `definitions.py` model — the same oracle p3async-hello already meets.
- **Threaded tier (no independent engine):** *"the fork's Go guest spawns real threads via `thread.spawn`, its GC stops the world via `Stop`, and its blocking routes through async readiness, with sibling agents making progress (§5 H-1/H-4)."* Verified against the **contract plus the §4 litmus battery only** — stated at that strength per row, never papered over as if an engine had checked it.

## Two tiers, two oracles — at their true strength

- **Standard tier — Wasmtime is a genuine independent arbiter.** Components, the Canonical ABI, single-threaded async emission. This tier has an external oracle and the fork's compile-half is checked against it.
- **Threaded tier — no independent engine (D-1).** Per #742, **D-1 resolved to no `thread.spawn` in the shipped Canonical ABI, so that tier has no independent engine to check against — the §4 litmus battery is its only memory-model oracle** (contract B-MM-5). That battery is now measured: **one discriminating witness — B-MM-2, the guest→guest wake (`-race`, a control that dies)**; the host→guest boundary **certified by construction with a witness, not by discrimination** — B-MM-1 split into async-wake **structural-by-identity** ([#780](https://github.com/scttfrdmn/burroughs/issues/780): the wake *is* the acquire edge), Resume-after-Stop **structural-by-redundancy** (SP-6, [#779](https://github.com/scttfrdmn/burroughs/issues/779)), and **host-call return an open hole with no instrument** (#742). Over half the battery certifies against the contract's own §-text with no external arbiter.

## Residual exposure (the two #742 charter items, carried verbatim in substance)

- **Charter item A — the oracle-sharing limit.** Over half the battery certifies against the contract's own text with no external arbiter, and **engine and fork share an author**; the battery was meant to be the threaded tier's mitigation and it partly is. Mitigated by: the standard tier's Wasmtime differential, the battery run on both a TSO and a weakly-ordered platform (B-MM-5), and `-race` per case. **Not** mitigated: there is no second engine for the threaded tier, so "the battery passed" must never be read as more independence than it is.
- **Charter item B — the honest evidence claim.** *The battery certifies the protocol and the Go-level race freedom, and does not certify the guest-visible ordering at the host-call-return boundary.* Sharpened by the B-MM-1 build (#780): the boundary is certified by construction with a witness, not by discrimination; the one discriminating witness is B-MM-2 at the guest→guest wake edge, not the host→guest boundary. A §4 gap can surface as guest-GC-over-shared-linear-memory heap corruption and coexist with a clean structural/`-race` run. **This is an input to Scott's v1.0.0 gate phrase, not a ruling on it.**

## Entry conditions (met or measured)

- Phase 3's exit at the sync-lift tier: p3async-hello runs end-to-end on Burroughs, byte-identical to a committed Wasmtime reading (ADR 0086).
- The bound mechanisms are on `main`: `thread.spawn` (T-1), `Stop`/`Resume` (SP-1), `waitable-set.wait` (H-4), `enterBlocked`, `Close`.
- The battery has answered what Phase 4 needed — measured, not as expected (see *Two tiers*).
- **Gap (named, not blocking):** both gates are off; the async tier is sync-lift only, with the async-lift half tracked-but-unbuilt ([#771](https://github.com/scttfrdmn/burroughs/issues/771)); whether the fork's guest sync-lifts (reachable, likely) or async-lifts (#771) is the first slice's opening design question.

## Where it lives

A `GEOEXPERIMENT`-gated real-threads experiment over a p3-async base, in the fork's own Go tree — not a stock `GOOS=wasip3` claim. **wasmpreempt is the precedent for the host-agnostic half only** (the back-edge poll codegen, the shared-memory safepoint word, the atomics lowerings, passive segments, the STW broadcast mechanism — all validated on two engines/ISAs). Its scheduler-liveness half is **JS-coupled and does not transfer**: the m0 heartbeat's `Atomics.waitAsync` async-park exists so a single JS event-loop thread never blocks, and has no non-JS analog. **A Burroughs host dissolves that problem** — Burroughs is goroutine-per-agent with real blocking excursions (H-1/H-4), which is the non-JS equivalent wasmpreempt lacks; the fork rebuilds the liveness half on Burroughs's imports rather than porting JS. The reversal trigger for this whole shape is **D-1 flipping to yes** (a `thread.spawn` in the shipped ABI, giving an independent engine), which is on no upstream horizon (#781 A3).

## Consequences

- **Two gates behind it, sync-lift async proven, this is the fork.** The threaded p3 fork is the phase the endgame plan named Phase 4; it is a product line, not scaffolding.
- **The first slice has no threaded-tier content** — it exercises the compile-half against the tier that *has* an external oracle (Wasmtime), before any exposure the battery is the sole oracle for.
- **No `gate:threads`/`gate:async` flip rides this ADR** — the flips are Scott's stamp-tier events with pre-registered forecasts; `gate:threads`'s flip is unscheduled (#670). This charter opens the tier; it does not flip a gate.
- **No engine code lands on this ADR alone.** Its stamp, and `GEOEXPERIMENT`'s creation, are Scott's.
- **What must not start:** a real-threads slice before the single-threaded component slice is green; any claim that the battery certifies the threaded memory model; any dependency on an upstream wasip3 base landing.
