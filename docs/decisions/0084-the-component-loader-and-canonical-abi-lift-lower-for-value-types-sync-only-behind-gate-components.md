# 0084 — The component loader and Canonical ABI lift/lower for value types, sync only, behind `gate:components`

Date: 2026-09-09 · Status: **proposed** · [#694](https://github.com/scttfrdmn/burroughs/issues/694) · Opens the p3 track (contract §6)
Ratio-Class: carried

## Pinned spec commits

The design reads the WebAssembly component-model spec at a fixed point, because every lift/lower
decision below rests on *which text was read*, and the two-month release train moves HEAD.

- **`WebAssembly/component-model` @ `2bed77e4228841c1d2721996d3ecc169ff96b158`** (committed 2026-09-06).
  The repo carries **no tags**, so this is HEAD at the fetch, per the pinning rule (a tag names a
  spec state; where none exists, HEAD with the date). The six artifacts, all under `design/mvp/`:
  `Explainer.md`, `CanonicalABI.md`, `Concurrency.md`, `Binary.md`, `WIT.md`, and
  `canonical-abi/definitions.py` (the executable lift/lower model).
- **Semantic anchor:** WASI 0.3.0, ratified June 2026. This slice implements the **value-type** tier of
  the Canonical ABI, sync only; `thread.spawn` is absent from 0.3.0's shipped ABI (gated behind 🧵,
  a later track with no independent-engine oracle), and async (🔀) is Phase 3.
- **Toolchain pin** for producing the corpus: recorded on [#694](https://github.com/scttfrdmn/burroughs/issues/694)
  (rustup `rustc 1.91.1`, `cargo-component 0.21.1`, `wasm-tools 1.258.0`, `wit-bindgen-cli 0.61.1`,
  targets `wasm32-wasip1`/`wasip2`), and the oracle is `wasmtime 48.0.1` (above the pinned 47).

## Context

The wasip1 usability arc is complete; #688's recon (verified) opened the p3 track. Step 0 is done — a
Rust component builds under the pinned toolchain and runs on the local Wasmtime, the corpus's first
member and the oracle's first reading. This ADR is the loader's decision-before-code; the mechanism
follows it.

## Decision

**A component binary loader and Canonical ABI lift/lower for the value types, sync only, behind a new
`gate:components` (off by default, behaviour 4).**

1. **The component binary loader** (`Binary.md` @ the pin): parse the component-model binary format —
   a component wrapping one or more **core modules**, its **instantiation** (core and component
   instances), **aliasing** (exports/outer), and the **type sections** (defvaltype, functype,
   componenttype, instancetype). The core modules inside a component decode through the existing
   `internal/binary` core decoder; the component layer wraps it.

2. **Canonical ABI lift/lower for the value types** (`CanonicalABI.md` @ the pin), and only these:
   `bool`/ints/floats/`char`, **strings**, **lists**, **records**, **variants** (and `enum`),
   **results**, **options**, **flags**, and **resources** (`own`/`borrow`), with **`cabi_realloc`**
   for allocation into guest memory and **`post-return`** for cleanup. Sync only — no `async`, no
   futures/streams, no `task.*` (Phase 3).

3. **A differential corpus and harness.** Registered Rust-built components (produced under the pinned
   toolchain) round-trip each in-scope WIT value type. The harness runs each through Burroughs and
   compares against **two independent oracles**: **Wasmtime** (runtime behavior) and
   **`definitions.py`** (the spec's executable lift/lower model). A result matching neither, or the two
   oracles disagreeing, is the finding.

4. **The gate is `gate:components`, off by default.** Mechanism lands behind it and self-merges on a
   bound green; the **flip is its own stamp-tier event later** (behaviour 4), pre-registered with a
   forecast and rollback when it comes — not in a mechanism PR.

## Exit condition

A registered set of Rust-built components **round-trips every in-scope WIT value type** on Burroughs,
with results matching **both** Wasmtime and `definitions.py` on the differential harness. **A
mis-lowered-variant test is in the suite as a positive assertion** — the silent-wrong-branch hazard
(a variant lowered to the wrong case discriminant reads as a plausible value) covered by construction,
hand-built to discriminate the case, not left to hope.

**Pre-registered per WIT type:** the expected pass, and an interpreter **cost class** (the shape of
what lift/lower costs for that type — e.g. a string is a bounded copy through `cabi_realloc`, a list is
that per element, a variant is a tag read plus one arm). Pre-registered before the mechanism produces
numbers, because a forecast cannot be written inside the PR that creates them.

## Consequences

- **Capability line (on exit):** Burroughs loads a component and moves every value type across the
  Canonical ABI boundary, matching the spec's own model and the reference runtime.
- **Two gates, and this is the first:** `gate:components` now; `gate:async` in Phase 3. The
  **single-threaded async tier is a Burroughs product, not scaffolding** — it will ship a capability
  line of its own — which is why the threaded tier's absent oracle does not block the track.
- **p3 WASI worlds are guest-driven:** none implemented until a guest demands one, and filesystem
  semantics are implemented once and bound twice (the wasip1 read-only work is not re-derived for a
  p3 world that has no guest).
- **Scope fences, inherited:** no fork-tree work before Phases 2–3 exist; no §4 battery work until
  Phase 4 opens as its consumer.
- **The pin ages:** when the mechanism reads a spec behavior, it reads it at `2bed77e`; a later slice
  that needs newer text re-pins with its own date, and the difference is a recorded amendment, not a
  silent drift.

## Amendment 2026-09-11 — the `gate:components` flip (default on)

Scott stamped [#720](https://github.com/scttfrdmn/burroughs/issues/720)'s pre-registered forecast
([`#720#issuecomment-5643091558`](https://github.com/scttfrdmn/burroughs/issues/720#issuecomment-5643091558)),
the stamp-tier event behaviour 4 requires for a flip. As of this date `gate:components` is **on by
default**: `componentsEnabled()` reads on unless an explicit `BURROUGHS_COMPONENTS=0` refuses.

**The claim, at its two evidence levels — not collapsed.** A default build instantiates a `wasi:cli/run`
component and marshals, through the single `canon` codec, the value-type set the forecast's table
verifies, refusing by name every kind neither oracle covers. The table carries **two evidence levels,
both verified in the track's sense, and the claim distinguishes them**: (1) types verified against the
`definitions.py @ 2bed77e` model **and** observed on a real guest byte-for-byte against wasmtime 48.0.1 —
`string`, `list<u8>`, `list<string>`, empty `list<tuple<string,string>>`, `result<T,stream-error>`
(both arms), `own<error>`, and the `u64` bytes-count; (2) types verified against the model **only**
(no guest on the two flip paths lowers a bare value) — the scalars, `f32`/`f64`, `list<u32>`, and the
`borrow` i32 encoding (borrow *lifetime* is Phase-3). Level (2) is not weaker verification; it is a
narrower witness, and the forecast states "differential-only" per row rather than inventing a reading.

**Refused by name, witnessed firing.** Every unmodeled kind is refused at the earliest marshal point —
decode, `StoreVia`, binding, call, export — and each refusal has a test that witnesses it *firing* on
real or synthesized bytes naming what it refused, not merely a permit path surviving (the standard the
two silent-no-op bugs on [#732](https://github.com/scttfrdmn/burroughs/issues/732) set).

**Rollback.** The revert is a one-line default change (`componentsEnabled()` back to `== "1"`) plus the
inverted test flipping back; no mechanism is removed, the gate stays present, and the refuse-by-name path
is the same code in both positions. `TestRunRefusesAComponentWhenExplicitlyGatedOff`
(`BURROUGHS_COMPONENTS=0`) witnesses the rollback pre-need.

This is a minor version bump under the milestone↔SemVer convention (0004); the version number is Scott's,
assigned at release. Phase 3's opening recon (`gate:async`) follows the flip, not before it.
Ratio-Class: carried
