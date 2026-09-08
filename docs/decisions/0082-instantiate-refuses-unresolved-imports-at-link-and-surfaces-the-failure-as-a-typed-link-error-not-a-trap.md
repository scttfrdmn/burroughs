# 0082 — `Instantiate` refuses unresolved imports at link, and surfaces the failure as a typed link error, not a trap

Date: 2026-09-08 · Status: **proposed** · Resolves [#686](https://github.com/scttfrdmn/burroughs/issues/686) · Ruled by Scott
Ratio-Class: carried

## Context

`interp.Instantiate` (nil resolver, the no-imports-supplied convenience) accepted a module with
**unresolved imports**, left the import slots nil, and let the failure surface only when an import was
*touched* at call time. That is a **deferred failure**: the runtime's law everywhere else is *refuse at
the boundary*, and a nil slot that traps on use reports at the wrong site and names the wrong thing
(#686). The resolver-present path already refused (`link`'s `unknown import`); only the nil-resolver
convenience degraded.

Scott ruled: **refuse at link.** Two riders shaped the landing — measure the degrade's consumers
first, and give the refusal a channel that does not repeat #686's category error one door over.

## Decision

1. **A nil resolver resolves every import to nothing, so it refuses.** `link`'s `if imp == nil {
   continue }` degrade path is removed; a nil resolver leaves `ok = false` and falls into the existing
   `unknown import: %q %q (%w ErrLinkFailed)` refusal — **one mechanism**, naming module and field. An
   import-free module reaches no iteration and instantiates unchanged, so `Instantiate` stays the
   no-resolver convenience for exactly those.

2. **The refusal is a typed link error, not a trap.** `interp.Instantiate` gains a third return,
   `(*Instance, *Trap, error)`. A link failure travels the error channel (`ErrLinkFailed`,
   `assert_unlinkable`); a trap stays the trap channel (`assert_trap`). Surfacing the link failure as a
   `*Trap` — the option the code's fallback already had — was **rejected on 0015's own grounds**: it is
   the same error-vs-trap conflation #686 fixes, moved one channel over, and the suite's own vocabulary
   separates the two. Why this is consistent with [ADR 0015](0015-instantiation-is-execution-at-time-zero.md)'s
   *"never a bare error"* is that ADR's 2026-09-08 append: the split forbade an *unconstrained* error
   (a second verdict-judge), not a *typed* sibling — the argument [ADR 0026](0026-a-tail-call-is-a-fourth-control-transfer-value-the-frame-owners-trampoline-re-enters.md)
   made for `*tailCall`.

3. **The public surface gains `ErrUnlinkable`, wrapping `interp.ErrLinkFailed`.** `Config.Instantiate`
   maps the link failure to it — an import-bearing module refuses at load because this API has no
   linking surface ([ADR 0029](0029-the-public-boundary-run-on-a-validated-path-decline-as-a-third-outcome-and-a-value-that-converts.md)).
   Distinct from `ErrUnsupported` (an engine gap at *use*); this is a link gap at *load*, and the two
   were one smudge only while the degrade deferred an unsupplied import into a call-time failure. `run`
   maps `ErrUnlinkable` to its `refused` exit code.

4. **`Deferred()`'s population shrinks.** Its example — an active data segment whose target memory is
   imported and unsupplied — was an unsupplied *import*, now refused at load before `build`. What
   remains is the import-free shortfall: a *defined* entity that fails to allocate. `Deferred()` stays
   live for that; it is not removed.

5. **The public-path differential's grave #421 branch is retired, its coverage moved to the refusal.**
   `publicpath_test.go` marked an import-bearing module untrusted-and-unlinked and skipped judging it;
   it now refuses at the public path, and the differential's `ErrUnlinkable` case asserts the **raw**
   path refuses the same module by the same `ErrLinkFailed` identity. #421's hazard (judging an
   import-absent module's exports) is subsumed: a refused module has no exports to mis-judge. Coverage
   preserved on the refusal, not routed around.

## Consequences

- **Board delta: zero** (pre-registered). The spec board instantiates through `InstantiateLinked` with
  a resolver, and the nil-path board consumer (`literal_test.go`) feeds import-free modules; the
  behavior change lands in `publicpath_test.go`'s converted case, a test rewrite.
- **Caller ripple: 34 sites, not the ~9 forecast.** `interp.Instantiate`'s signature grew a return, and
  `internal/interp`'s own unit tests call it *unqualified*; the initial sweep counted only the
  `interp.`-qualified form. All are import-free, so the link error is provably nil at each; a single
  test helper `mustInst` absorbs the check for the 31 test-body sites, and two non-test helpers return
  the error directly. Reported per rider 4.
- **Public shape:** `ErrUnlinkable` is new; `ErrUnsupported`'s and `Instance.Deferred`'s docs move the
  unsupplied-import case out to load. Stated as public in the changelog.
- **A trap is never a link failure and a link failure is never a trap**, at every layer — the property
  #686 restored one site down, held here rather than re-broken.
