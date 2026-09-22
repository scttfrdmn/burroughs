# 0088 — The component path takes a caller-supplied capability set: the engine's first such surface

Date: 2026-09-18 · Status: **accepted** · [#798](https://github.com/scttfrdmn/burroughs/issues/798) (A1 ruled) · [#800](https://github.com/scttfrdmn/burroughs/issues/800) (the slice) · Amends ADR 0085's `ComponentConfig` surface
Ratio-Class: ordered https://github.com/scttfrdmn/burroughs/issues/798

**Stamped 2026-09-18 (Scott, on #798 — "approved", on the *shape*).** The two approvals are distinct and both are cited because they are: A1 — *that* the component path takes a caller-supplied feature set — was ruled first; **the shape** below — an extensible named-capability set with guest-driven contents, unrecognized values refused by name — was stamped separately, because a shape is public API surface, one of the three subjects an actor may not settle alone. `Status: accepted` cites the second. #800's mechanism proceeds on it.

## Context

`internal/component/loader.go:Load` constructs `bin.DefaultFeatures()` for the core modules inside a component. `DefaultFeatures()` is `{SIMD, RelaxedSIMD}`; it does not include the threads proposal's surface (the 0xFE atomics region, shared memories). A component whose core module uses atomics is therefore refused — `the 0xfe region (atomics): feature gate disabled` — and **no configuration lifts it**: `ComponentConfig` carries `Args`, `Stdin`, `Stdout`, `Stderr`, and there is no environment override.

**Surfaced by a real consumer, not anticipated.** Phase 4's fork (ADR 0087) emits a Go program as a p3 component that Wasmtime 48.0.2 runs. Burroughs refuses it, because **Go's compiler emits atomics unconditionally — 1148 operations in a hello-world that starts no goroutines.** The consequence generalizes: *every* Go component this fork emits needs the threads proposal's surface, whether or not the program has threads. The charter's Burroughs-side gap list missed it because it was written from the engine's perspective, where atomics and threads are one gate; in Go's runtime they are not.

### The measurement that shaped this decision, and what it corrected

Before A1 was ruled, the condition attached to the decision — *measure what each path resolves to* — was run. **Every feature-resolution site in the engine, outside tests:**

| site | path | resolves to |
|---|---|---|
| `burroughs.go:Instantiate` | the public **core** path | `binary.DefaultFeatures()` |
| `wasi.go:IsWASIP1Command` | the **WASIP1** classification path | `wasi.GuestFeatures()`, which is `bin.DefaultFeatures()` (`internal/wasi/runner.go:GuestFeatures`) |
| `internal/component/loader.go:Load` | the **component** path | `bin.DefaultFeatures()` |
| `internal/binary/binary.go:DecodeModule` | the decode helper | `DefaultFeatures()` |

Two corrections followed, both recorded here because they changed what was being decided rather than tidying it:

1. **An option was struck as vacuous.** "Let the component path inherit whatever the core path uses" was on the table; it is **already true** — all paths resolve to the identical set. There is no per-path resolution to inherit and no behaviour difference to preserve.
2. **A premise was false.** The finding had twice been characterized as *the component path lacking a capability the core path exposes.* It does not: the public `Config` exposes exactly `Strict`, and **no exported symbol in the module root mentions features at all.** `binary.Decoder.Features` is internal with no public entry.

So this is **not the component path catching up.** It is **the engine's first caller-supplied capability surface**, and that is the decision's real subject. "Every future feature question arrives here" is not a cost to weigh against alternatives — it is the precedent this ADR sets.

## Options and decision

**Why not the alternatives that avoid the precedent.** Both were declined on #798 because they make the same decision somewhere a reader will not find it:

- **Widen the component path's default** to include threads: no API change, and the fork's guest loads — but it silently enables atomics for *every* component and makes `gate:threads` off mean nothing on the component path, a policy change enacted by a default rather than by a ruling.
- **An environment override only:** no public surface, but a library embedder using `ComponentConfig` cannot reach it, and it conflates *gates* (reversible policy, which is what env vars have meant in this tree) with *configuration* (what an artifact requires).

**The shape, proposed.** Four candidates, and the precedent is the deciding criterion:

1. **Export `binary.Features` itself.** Rejected: couples the public API to an internal struct that grows a field per proposal, so every future proposal changes the public surface whether or not an embedder needs it.
2. **A bool per capability on `ComponentConfig`** (`Threads bool`). Narrowest today, and rejected *for the precedent it sets*: the next capability is a second field, and a third a third. An ADR whose subject is the precedent should not choose accretion.
3. **An extensible named-capability set — chosen.** An exported `Feature` type with named constants and a `Features` field on `ComponentConfig`. The **container is extensible; its contents are guest-driven.** Exactly one capability is defined — the threads proposal's surface, because that is what a real consumer needs — and **an unrecognized value is refused by name** rather than ignored, so the set cannot silently accept a capability the engine does not implement.
4. **Functional options** (`WithFeatures(...)`). Rejected: this tree configures with plain structs (`Config{Strict}`, `ComponentConfig{Args, Stdin, …}`); a second configuration style is a cost paid by every reader.

**The refusal is part of the shape, not a consequence of it.** With nothing supplied, the component path behaves exactly as it does today: a module needing a gated capability is refused **by name**. The surface makes the refusal *conditional on what the caller supplied*; it does not remove it. Both halves are registered in #800's forecast and witnessed firing on real bytes (#714's lesson: a gated entry's forecast registers the refusal, not only the enabled exit).

## Consequences

- **`gate:threads`' default is untouched.** It stays off. Supplying a capability is an embedder saying what their artifact requires, not a gate flipping. This ADR rules nothing about what the gate *should* mean for an atomics-only guest — that is [#799](https://github.com/scttfrdmn/burroughs/issues/799) (B2), unscheduled, behind a safety claim registered to be **witnessed rather than asserted**.
- **The same gap exists on the core and WASIP1 paths and is deliberately left there.** `burroughs.go:Instantiate` and `wasi.go:IsWASIP1Command` hardcode the same default, so a *core-module* guest using atomics is equally unloadable. No consumer has asked; the surface appears on the component path first **because that is where a consumer exists.** Recorded with its trigger: a core-path consumer that needs it. Guest-driven, not an oversight.
- **One capability, not a set of them.** Every other proposal stays unexposed and is refused by name if named. A second capability arrives with a second consumer.
- **This is the precedent.** The next feature question arrives at this surface, in this shape. That is the point of settling it in an ADR rather than in the patch that needed it.
- **The stamp is Scott's**, and the mechanism holds for it. No code lands on this ADR alone.

## Append, 2026-09-22 — the trigger fired: the WASIP1 path is this precedent's second consumer (#813)

**This ADR's own deferral named the site.** The Consequences above read: *"The same gap exists on the core and
WASIP1 paths and is deliberately left there. `burroughs.go:Instantiate` and `wasi.go:IsWASIP1Command` hardcode
the same default… Recorded with its trigger: a core-path consumer that needs it."* The consumer arrived, and the
trigger pointed at `wasi.go:IsWASIP1Command` by name — which is a deferral's trigger doing the job the law
family asks of one (*a deferral's trigger is a hypothesis about its consumer*), rather than the two instances
where a trigger of mine named the wrong event and sat until something unrelated walked into it.

**Ruled as execution, not decision (Scott, 2026-09-22):** *"ADR 0088 set the precedent — a caller-supplied
capability set, extensible named capabilities, unrecognized refused by name — and this is that precedent's
second application rather than a new decision… Scott ruled A1 on the principle; applying it to a second path is
execution."* So this is an append rather than a new ADR — and it deliberately does not name the number one
would have had, because the citation sweep is right to resolve every `ADR NNNN` it finds and a number
chosen *not* to exist is a reference with no target. The `Status:` above needs no second stamp: the shape it
cites is the shape being reused, unchanged.

### The consumer, and why the fix widens rather than repairs

Phase 4's fork emits Go `wasip1` guests, and **every one of them carries atomic instructions** — 1025 in a
hello-world that starts no goroutines. `GuestFeatures()` is plain `DefaultFeatures()`, so the whole public
wasip1 path refuses them.

**Attributed by measurement, because a toolchain claim is a hypothesis until run:**

| toolchain | atomic ops, wasip1 hello-world | `IsWASIP1Command` |
|---|---|---|
| released **go1.27.1** | **0** | **true** |
| the fork's base (go1.28-devel) | **1025** | false — refused |

The atomics are `c11c94ec47` *"make wasm atomic Or8/And8/And/Or genuinely atomic (D19c zombie fix)"*, author
`scttfrdmn`, 2026-07-24 — **this project's own earlier campaign, not upstream Go.** Two things follow, and the
second is why this append states the attribution at all: Burroughs' public wasip1 surface is **not** broken for
today's Go and this is not an ecosystem compatibility finding; and the capability is required by *our* guests,
so the change **widens a surface rather than repairing a defect**, which is what keeps it inside 0088's ruling
instead of being a grave.

### What the trigger did NOT anticipate, and it is the substantive half

The deferral assumed the gap's consequence was *"a guest using atomics is equally unloadable"* — a permission
problem. On the wasip1 path it is also an **identity** problem, because detection decodes:

> `IsWASIP1Command` returned `(false, …)` for a module that imports `wasi_snapshot_preview1` and exports
> `_start`. The CLI then printed **"this module is not one"** — a false statement about the module. It *is* a
> wasip1 command; this build is not permitted to decode it.

That is the **face-4 family** — *a call returning false reads as "nothing to do" when it means "cannot be
reached"* — and it is the first instance of that family found in Burroughs' own public surface rather than in
Go's runtime. **Detection answers *what is this*; capability answers *may I run it*.** Conflating them makes the
engine deny a module's identity on the strength of a gate, and the `(bool, error)` signature that was designed
to let a caller distinguish them is exactly what the CLI then discarded by testing `derr == nil && isCmd`.

So the consequence recorded here, beyond the field: **a capability-aware detection form, and a call site that
says which of the two things it means.** Preserving `IsWASIP1Command`'s existing contract is deliberate — its
doc already says an undecodable module is `(false, err)` and that the caller chooses — so the repair is a
capability-aware sibling plus a call site that stops flattening the two channels, not a signature change.

### And the CLI supplied capabilities on NEITHER path

Measured while implementing: `cmd/burroughs/run.go` constructs `ComponentConfig` **without** `Features`, so the
capability this ADR added in 2026-09-18 has never been reachable from `burroughs run` either. Slice 1's
two-engine claim runs through an internal test for that reason (recorded on #802's close). One CLI flag serves
both paths; wiring only the wasip1 half would leave an asymmetry with no reason a reader could recover.

### What is unchanged

- **`gate:threads`' default is untouched**, on both paths. Supplying a capability is still an embedder stating
  what their artifact requires.
- **Unrecognized names are still refused by name**, and the refusal is still witnessed *firing* with the
  capability withheld — not inferred from the permit path working.
- **[#799](https://github.com/scttfrdmn/burroughs/issues/799) (B2) is still open and unscheduled.** Whether
  `gate:threads` off *should* admit atomics when nothing can spawn is not answered by supplying the capability
  on a second path; if anything the second consumer strengthens the case for asking, and it stays a question.
- **One capability still, not a set.** `FeatureThreads` is the only name either path recognizes.
