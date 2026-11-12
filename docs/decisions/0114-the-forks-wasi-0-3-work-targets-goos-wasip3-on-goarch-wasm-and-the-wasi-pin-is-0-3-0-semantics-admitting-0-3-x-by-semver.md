# 0114 — The fork's WASI 0.3 work targets `GOOS=wasip3` on `GOARCH=wasm`, and the WASI pin is 0.3.0 semantics admitting 0.3.x by semver

Date: 2026-11-11 · Status: **accepted**, 2026-11-11. Scott, in session, answering the v0.14.0 plan's questions: *"GOOS=wasip3"* (D-F1), and *"v0.14.0 is WASI 0.3"*, *"single-threaded first"*, recorded on [#1140](https://github.com/scttfrdmn/burroughs/issues/1140#issuecomment-6814091138). The record is the actor's, so commits resting on it are `Ratio-Class: carried`. Filed against [#1228](https://github.com/scttfrdmn/burroughs/issues/1228).

## Context

v0.14.0 is WASI 0.3, single-threaded first (#1140). Its witness is a fork-built Go `net/http` server whose responses are byte-identical under the pinned wasmtime and under `burroughs run`. The fork needs a target to build that program for, and the engine and the fork both need to know which WASI 0.3 they implement.

**[ADR 0087](0087-the-threaded-p3-fork-charter-a-geoexperiment-over-an-async-base-whose-threaded-tier-has-no-independent-engine.md) chartered the fork as a `GEOEXPERIMENT`:** a real-threads experiment gated over a p3-async base, in the fork's own private tree, and explicitly *"not a stock `GOOS=wasip3` claim"*. Its experiments today ride on `wasip1`: `componentabi` (no preview-1 imports) and `burroughsspawn` (the spawn door). There is no wasip3 work in the fork (`wasmpreempt/phase4` @ 01f3108158).

**The prior art is a real `GOOS` port.** jellevandenhooff's `wasip3-prototype` (`GOOS=wasip3`, `GOARCH=wasm32`, single-threaded) reports passing `go tool dist test`, net and net/http included, under wasmtime (#1140 §2.2). A WASI 0.3 syscall layer, and a netpoller built on the Canonical ABI's waitable sets, are native to a component. They aren't a variant of preview 1's `poll_oneoff`.

**Which WASI 0.3 to implement** (#1140 §0.1–0.2, read 2026-10-26 and re-confirmed 2026-10-27):
- Through 0.3.2, every 0.3.x release has 0.3.0's interfaces. The diff is version strings and one doc comment.
- 0.3.1 *permits* the Component Model's `map<K,V>`, `implements` and `external-id`, and no WIT through 0.3.2 uses them.
- The pinned oracle, wasmtime 49.0.3, implements 0.3.0 and admits 0.3.1+ imports by semver matching, with those three features off by default.

## Options

**The fork's target (D-F1):**
1. **`GOOS=wasip3`**: a real target in the fork, with its own runtime, syscall, os and net files. This is the prior art's shape and #1140's brief.
2. **A `GEOEXPERIMENT` on `wasip1`**: ADR 0087's shape kept as stamped. The wasip3 behaviour sits behind an experiment flag on the preview-1 port, so a component-native syscall layer would be grafted onto a port whose files assume `poll_oneoff`.

**The architecture (D-F2):** stay on the fork's `GOARCH=wasm`, or move to `wasm32` as jvdh does.

**The pin:** WASI 0.3.0 exactly; 0.3.x by semver; or the latest point release, 0.3.2.

## Choice

- **D-F1: option 1, `GOOS=wasip3`.** Scott's ruling. It is the fork's own port in its own tree. It is **not** a claim about stock Go, and it does **not** depend on an upstream wasip3 base landing. Both hold exactly as ADR 0087 states them.
- **D-F2: `GOARCH=wasm`.** The bindings carry explicit 32-bit layouts, as wit-bindgen-go already does for stock `GOARCH=wasm`. `wasm32` is a separate, larger change that nothing in v0.14.0 needs.
- **The pin: WASI 0.3.0 semantics, admitting 0.3.x imports by semver.** That is what the oracle does by default, so the engine and the oracle agree on which guests are in scope. Each later point release is its own re-reading. **The CM 0.3.1 features are out of scope**, since no WIT through 0.3.2 uses them. This also answers the contract's §10 Q7 recommendation (*"pin 0.3.0 semantics and admit 0.3.x by semver"*) for v0.14.0 only. It is not a contract decision.

## Consequences

- **ADR 0087 is amended in one place:** the fork's WASI 0.3 work is a `GOOS=wasip3` target, not a `GEOEXPERIMENT` over `wasip1`.
  - Its charter otherwise stands: the private tree, the single-threaded slice before any threaded one, no certifying of the threaded memory model by the battery, and no dependency on upstream.
  - The existing `wasip1` experiments (`componentabi`, `burroughsspawn`) stay as they are, and Phase 4's v0.12.0 witness is untouched.
- **What this does not decide:**
  - contract §10.6, `GOOS=wasip3` ABI negotiation ([#15](https://github.com/scttfrdmn/burroughs/issues/15)), which stays open;
  - the multi-M design (D-F3, D-F4, D-F5), which is v0.15.0's.
- **The engine side keys imports by semver compatibility class** (E1, #1229, its own ADR). Without that, the 0.3.x admission above would merge with WASI 0.2, whose names collide with 0.3's under different signatures.
- **The fork stays unpublished.** Guest binaries committed to this repository carry a `PROVENANCE` file naming the fork commit, as `internal/wasi/testdata/threads012` does.
