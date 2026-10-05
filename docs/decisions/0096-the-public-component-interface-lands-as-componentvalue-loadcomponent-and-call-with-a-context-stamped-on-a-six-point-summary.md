# 0096 — The public component interface lands as `ComponentValue`, `LoadComponent` and `Call` with a context, stamped on a six-point summary

Date: 2026-10-05 · Status: **accepted** — stamped by Scott, 2026-10-05 (*"Scott - stamped"*), on the chair's six-point summary of the #858 public-interface review · [#858](https://github.com/scttfrdmn/burroughs/issues/858) · Lands [ADR 0085](0085-the-public-component-api-surface-a-new-component-value-type-resource-handles-first-class-and-wit-typed-constructors.md) and its **amendment 1**; implements [#880](https://github.com/scttfrdmn/burroughs/issues/880)'s context-driven waits
Ratio-Class: ordered Scott's stamp 2026-10-05 on the chair's six-point summary (#858)

**What the stamp covers is the six points as presented, and nothing beyond them.** That bound is part of the ruling and not an inference from it: the chair's words were *"if building the interface needs anything beyond those six points, such as a further exported name, a changed signature, or different error semantics, stop and report it before merging. A change the stamp didn't see goes back to Scott."* Section **"What the stamp did not see"** below is that report.

Recorded by the actor the stamp reached, so no independent provenance — but the `Ratio-Class` is `ordered` rather than `carried`, because a principal's stamp on a stated summary is a citable approval, which is the distinction ADR 0093 and ADR 0085's amendment 1 drew.

## The six points, as stamped

1. **`Component.Call(ctx context.Context, name string, args ...ComponentValue) ([]ComponentValue, error)`.** Cancelling the context cancels the running task, which returns `burroughs.ErrCancelled`. The started/not-started distinction stays an **option** for later, addable without a break.
2. **`ComponentValue`** as the public value type in the root package, converted at the boundary per ADR 0029.
3. **The naming rules**: `interface#function` for an export inside an interface, a bare name for a top-level function and for an import, and **never** resolving a bare name by searching.
4. **Re-entry**: a component that calls back into itself through a host function traps immediately, naming re-entry.
5. **First merge carries `u32` only**, other kinds refused by name and added later without breaking changes.
6. **Merge, do not release.** No version cut until strings, lists and records work. Both the timing and the minor-version number stay Scott's.

## What landed, point by point

**1 and 2 are built as stated.** `Call` wraps the engine's `CallValuesCtx`; the engine's cancellation sentinel is wrapped rather than replaced, so `errors.Is(err, ErrCancelled)` is the embedder's match while the engine's own message — naming the park or the entry wait it ended at — survives for a reader.

`ComponentValue` is a distinct type with unexported fields, so a value always carries the kind it claims (ADR 0085's "constructors carry their WIT type") and a mis-typed read returns `(0, false)` rather than reinterpreting bits. **The zero value names no type** and is refused at the boundary rather than crossing as `u32(0)`, which is the role `KindNone` plays for the core-module `Value`.

**Why a distinct type is not a choice made here:** ADR 0029 decision 2 is Scott's own ruling that an internal representation is not hoisted into the public surface. The record is specific — `interp.Value` widened **four times in four slices**, and the fourth *retyped a field*, which for a published type is a break rather than a minor version. `canon.Value` is in the same position, so the same conversion-at-the-boundary shape applies, with **no silent default in either direction**.

**3 is built, and one half is unwitnessed.** `resolveValueExport` cuts on `#`; a bare name addresses a top-level export, `iface#function` one inside an instance, and naming an instance refuses *and says which form to use*. The embedder's-eye test asserts that a bare name resolves and that an unknown name is **refused rather than found** — which is the half with teeth, since a search would pick one silently when two interfaces share a function name. The `interface#function` form itself has **no committed fixture** that exports an interface-nested function and imports nothing, so it is exercised only through the engine's own tests. Said rather than left to a coverage report.

**5 is built as stated.** `ComponentU32` is the only constructor; both directions of the conversion refuse every other kind **by name**, naming the kind. `ComponentKind` enumerates the WIT kinds beyond what crosses, for exactly that reason — a refusal that could not name what it refused would say only "unsupported".

**6 is recorded and nothing is cut.** The CHANGELOG entry says the interface is merged and that no release is cut.

## Amendment 1 (2026-10-05) — Scott's ruling on the three items the stamp did not see

**Stamped by Scott** on 2026-10-05 (*"I do not object"*), on the chair's three-item summary of what building the interface turned up. Recorded beside the original stamp because it answers the limit that stamp carried, and the limit's whole point was that the answer be his.

The chair split the five reported items: two were the chair's own and are deferred, three were Scott's because they change the public interface or what was stamped.

### Deferred by the chair, not by this ADR's author

**`ComponentConfig.LoadComponent` and `Component.Exports()`.** Both additive later, neither needed by a first user of a `u32`-only interface, and leaving them out costs nothing. Filed against the release-blocking value-kinds work.

### Scott's three, as ruled

**1. Re-entry (point 4) is satisfied because the hazard cannot occur.** §5 **H-2** already guarantees it by giving host code no way back into the instance, and the public API offers no host functions. Detection becomes **required acceptance** on the issue that makes host functions public, and **per-caller identity is built in the same change** — because that change is what brings the hazard into existence, and because the detector cannot be written without the identity.

This is not a weakening of point 4. It is the point read against what exists: the guard and the hazard arrive together, which is stronger than a detector shipped now against a path nothing can take.

**2. Exit code 7 is approved as public CLI surface**, documented as unreachable from the CLI for now.

**3. `Component.Close()` is approved**, with the behaviour stated in the ruling rather than left to the implementation:

- cancel every call in flight, so those callers get `ErrCancelled` from tasks that **actually ended**;
- wait a bounded time for the cancellations to finish, then tear down, with a **named outcome** if the bound is reached;
- calls after `Close` return a **closed** error.

All three are built. `Close` is additionally **idempotent**, which the ruling did not specify and which is not a change to it: an embedder who defers `Close` and also calls it on an error path should not have to track which ran, and a second `Close` returning nil is the only reading under which both are safe.

### What this amendment turned up, reported under the same limit

**Two new public sentinels, not one, and neither could take the exit code the chair suggested.**

`Close` needs `ErrComponentClosed` (a call on a closed component) and `ErrCloseIncomplete` (the bound reached). The chair's instruction was to *"give it the existing code for the invoker's own failure if that fits, or report back if it doesn't."* **It does not fit:** `TestExitCodesCoverEveryPublicSentinel` **forbids** any public sentinel mapping to `exitError`, and it is right to — a sentinel that lands on the catch-all is indistinguishable from one that fell through, which is the failure the taxonomy exists to prevent. Taking the suggestion would have meant weakening the control that asked the question.

So `exitClosed = 8` and `exitCloseIncomplete = 9`. **Two codes rather than one**, because they are facts about different parties: calling a closed component is the **invoker's** mistake, while an incomplete close is the **guest** refusing to stop, and an operator seeing the second learns something about the module that the first does not say. Both unreachable from the CLI today, for code 7's reason, and both documented as such.

**And the sentinel control's domain was one file.** `declaredSentinels` read `burroughs.go` alone, so a sentinel declared in any other file of the package escaped it and would fall through to the catch-all **unnoticed** — the exact failure it exists to prevent. Found by walking into it: `ErrCloseIncomplete` was first declared in `component.go` and the control reported *"this taxonomy binds ErrCloseIncomplete, which the public package no longer declares"*, the right complaint for the wrong reason. The sentinel moved to `burroughs.go` where the others live **and the domain widened to the package**, with its own file-count floor. Same shape as `TestMarkdownLinksResolve`, which was `CLAUDE.md`-scoped until #466: *a control whose domain is a file cannot see the package.*

**One witness could not be written from outside the module.** The ruling's first `Close` witness — an in-flight call parked in a host import that never resolves, with the guest's **receipt** observed — needs a host import, and this release ships no hook for one. So it is witnessed one level down, in `internal/component`, against `Instantiated.CancelAll` — which is *exactly* what `Component.Close` calls before tearing down, so the code path under test is the same one. The public arms assert the error, the closed refusal, the bound's named outcome and the goroutine count. Split by where each claim is observable.

## 4 — re-entry: the hazard is still unreachable, and this is the report the limit asks for

**Point 4 is not implemented, and the reason is that its path does not exist in the surface this slice ships.** This is reported rather than quietly skipped, per the stamp's own limit.

Re-entry needs: *guest's async-lifted entry → host import → embedder's Go code → `Component.Call` on the same instance.* The middle step requires an embedder-supplied host function. **Searched: the root package exports no host-function hook.** `component.Host`'s `asyncImpls` and `syncImpls` are unexported fields set only by tests, and `ComponentConfig` carries `Args`/`Stdin`/`Stdout`/`Stderr`/`Features` and nothing else. Adding one would be a further exported name — exactly what the limit reserves.

So the hazard remains unreachable **by absence**, which is §5 **H-2**'s own shape and Scott's sub-choice there: *"the method that would violate it will not exist."* It is the same reason #869 removed its self-re-entry timer rather than replacing it with a detector, and the chair's ruling on the #882 review applies unchanged: *declining to fabricate a witness for a hazard that cannot be constructed is correct.*

**And a sound detector is not available at present even if the path existed**, which is worth recording because it changes what the owing slice has to do. The detector must distinguish re-entry from ordinary contention — another caller, which must still **wait** (#869, and the model's backpressure at def:424-430). The model distinguishes them by **thread identity**: `assert(inst.exclusive_thread is None)` against `task.implicit_thread`. Burroughs' thread identity is **per instance**, not per caller: `Instance.invokeIndex` runs every call on `&in.host`, and `TestConcurrentHostCallsShareOneThreadID` is the committed witness that two concurrent host callers present the **same** `ThreadID`. So the one signal the model uses is unavailable, and every substitute considered — "the slot is held", "the holder is in a host call", "a bound on the inner wait" — either false-positives on legitimate contention or is the timer point 4 rules out.

**What the owing slice therefore owes**: whichever slice makes host functions public must land per-caller identity *and* the detector together, because the detector cannot be written without it. Recorded on #858 so it is not rediscovered.

## What the stamp did not see

Reported here rather than built, per the limit. None of these blocks points 1–3, 5 or 6.

- **`ComponentConfig.LoadComponent`** — a capability-carrying twin, mirroring `Config.Instantiate` beside `Instantiate`. ADR 0085 commits only the bare `LoadComponent(wasm)`, so `LoadComponent` uses the zero configuration: stdout and stderr discarded, stdin empty, no args. **Consequence an embedder meets**: a component that writes to stdout through `Call` writes nowhere. `ComponentConfig.Run` remains the configured path for `wasi:cli/run`.
- **`Component.Exports()`** — `Instance` has one; a component you cannot enumerate is awkward. Omitted as a further exported name.
- **`Component.Close()`** — and this is the one worth a decision rather than a note. A component instance owns engine threads and async tasks, and without `Close` an embedder cannot release them. The existing `*Instance` has no public `Close` either, so omitting it is *consistent*, but a component with in-flight async tasks is a stronger case than a core module was. The internal `Instantiated.Close` exists and is simply unreachable from outside.

## Consequences

- `LoadComponent` classifies at load exactly as `ComponentConfig.Run` does: `gate:async`'s refusal crosses as `ErrGated` because the component is **well-formed** and reporting it malformed would re-manufacture at the public boundary the malformedness the decoder refused to manufacture (grave #301).
- **A nil context is refused rather than defaulted.** An embedder who passes nil believes the call is cancellable, and substituting `context.Background()` would hand them one that silently is not.
- **The asymmetry with `Instance.Call` is stated and stays** — amendment 1's point 5. `Instance.Call(name, args...)` takes no context and is not changing: it is released surface, and adding a parameter would break every embedder to serve a path it does not have.
- **The embedder's-eye tests are in `package burroughs_test`**, not `package burroughs`. The claim this slice adds is not "the mechanism works" — the engine's tests established that against wasmtime's readings — but that it is **reachable from outside the module**, and only a test that cannot touch unexported identifiers can make it. The compiler is the assertion.
- ADR 0085's **consumer-triggered landing** condition is discharged on its own terms: it refused to land the value type ahead of the lift/lower consuming it, and gate:async's parity work is that consumer.
