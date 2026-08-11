# 0025 — G-1 carves out vectors whose sole blocker is #9's deferred validator

Date: 2026-08-11 · Status: **accepted** — stamped by Scott on #230

> Held `proposed` from 2026-08-11 until the stamp existed, per 0016's own ruling: *a status field
> is a citation to an approval, and approvals are artifacts with provenance.* Scott recommended
> this package be sent up (comment on #227, 2026-08-11) and stamped it on #230 ("Scott - proceed
> with 230"). The interval this spent `proposed` is kept in the record rather than overwritten,
> per 0016 and 0017's own demonstration of the same discipline. The deliberation is #230; this is
> its tombstone. The contract text itself is amended in `docs/burroughs-contract-v0.1.md`'s own
> G-1 clause, with an amendment note in the same style as G-2's #109 note.

## Context

Contract §9 G-1 reads: *"A gate's acceptance is its upstream spec test suite green, on both a TSO
and a weakly-ordered platform, before default-on."* The SIMD gate-flip procedure (#227) measured
this literally against SIMD's own suite (`simd_*.wast`, 59 files — the proposal's suite at the
contract's words, narrower than the all-gates-on lane, which answers "is anything hiding
unnoticed" rather than "is this gate's own suite green") and found:

```
pass=25158 fail=161 gated=0
```

Every one of the 161 is `interp: module reached the interpreter unvalidated: ...`, confirmed by
summing the bucket to the fail total, confirmed identical on both architectures. Zero value
mismatches, zero missing arms, zero anything else — the triage across #223 and #229 converted
every genuine engine-execution defect the gate-flip forecast surfaced (four mechanisms across the
project's history: funcref identity #163, NaN min/max #223, `pmin`/`pmax` bit corruption and
`load*_splat` over-read #229), and this is confirmed to be the entire remainder.

`ErrNotValidated`'s own doc comment (`internal/interp/interp.go`) states the mechanism directly:
it is "the interpreter declining to execute something #9 would have rejected" — a declared and
tracked layering debt (per #6's own ruling on that shape), and "every one of its call sites becomes
unreachable when #9 lands." #9 is the validator, open, and is the artifact the v0 phase ladder
(CLAUDE.md) defers: decoder → internal form → validator → interpreter, with v0's own product
being the interpreter arriving *before* the validator does.

## The problem with a literal reading

Reading G-1 as "green, full stop" makes it **unsatisfiable by any gate for the whole of v0**. Any
suite containing a vector whose module reaches the interpreter unvalidated — which #9's absence
guarantees for every gate, not only SIMD — trips the identical wall. A contract clause that no
gate can satisfy by construction is not a gate; it is an accidental prohibition, and it contradicts
the same document's own phase ladder, which explicitly plans for gates to flip *during* v0, before
#9 lands.

Holding the flip serves no correctness purpose in exchange for this cost. Gated-off code does not
validate either — the default lane runs unvalidated today, identically, whenever an unvalidated
module reaches the interpreter through any other path. Declining to flip SIMD does not make any
module more validated; it only moves 161 vectors from `unsupported` (declined for the gate) to
`fail` would-be (declined for #9) once the gate opens, which is a bucket difference, not a
correctness difference.

## Decision

G-1 is amended to add a named, self-retiring carve-out:

> A gate's acceptance is its upstream spec test suite green — **modulo vectors whose sole
> attributed blocker is #9's own deferred validator (`ErrNotValidated`)**, attribution by the
> engine's own error taxonomy rather than by assertion, and only when the gate's own suite carries
> **zero** required-engine-execution defects (no missing arms, no value mismatches, no
> anything-else) once that population is set aside.

This is not a general escape clause. It is scoped three ways, deliberately, against the
rationalization-farm failure this project's own actor-never-classifies-the-actor and
scope-controls-to-the-space disciplines argue against:

1. **Named to one issue, not to a category.** The carve-out excepts vectors blocked by `#9`
   specifically, not "validator-shaped gaps" as a class. When #9 lands, `ErrNotValidated`'s call
   sites become unreachable by its own doc comment's own claim, and the carve-out retires itself
   with nothing left to except — no second amendment is needed to repeal it.
2. **Attribution by the code's own taxonomy, not by claim.** A vector counts toward the carve-out
   only when its fail message is `ErrNotValidated`'s own sentinel — a fact read off the engine's
   error value, the same discipline `TestGatedVectors`' per-vector allowlist and
   `expectedMismatches`' per-row registry already use elsewhere in this codebase (an unexplained
   entry is a suppression wearing a disguise; here the entry is self-explaining because the error
   value names its own cause).
3. **Conditioned on zero engine-execution defects in the remainder.** The carve-out does not
   excuse missing arms or wrong answers — it excuses only the specific, named, structurally
   deferred question #9 has not yet been asked. A gate whose suite has *any* other kind of fail
   does not qualify; G-1 as amended is stricter about everything except this one named exception.

## Precedent

#109 amended G-2 on an identical shape: an enumeration ("Wasm 3.0 core (GC, exception handling,
tail calls, memory64, multi-memory, relaxed SIMD)") had become load-bearing and silently
incomplete (extended-const absent), so the fix was to name the true criterion ("all of Wasm 3.0
core", derived from the spec's own appendix, auditable rather than illustrative) rather than leave
a silent reading or extend the enumeration ad hoc. This amendment follows the identical pattern:
name the true criterion (a specific, numbered, self-retiring blocker), cite the mechanism
(`ErrNotValidated`'s own doc comment), and retire the ambiguity rather than invent a new general
rule.

## Consequences

- G-1's own text in `docs/burroughs-contract-v0.1.md` gains the carve-out clause, with an amendment
  note in the same style as G-2's #109 note (dated, issue-cited, stating what changed and why).
- The SIMD gate now reads as satisfying G-1 under the amended text (161/161 attributed, 0 residual
  engine defects, both architectures) — but the flip itself is #227's own action, gated on this
  ADR's stamp per Scott's own stated intent to cover both in one stamp event.
- Every future gate's own G-1 demonstration inherits this same carve-out automatically (it is
  contract text, not a per-gate exception), and each such demonstration still owes its own
  zero-engine-defect confirmation — the carve-out narrows *what counts as a blocker*, not the
  burden of proving nothing else is one.
- When #9 lands, this ADR's own carve-out clause becomes vacuous by construction (no vector can
  any longer be blocked solely by a validator that exists) and the contract text may be simplified
  in a follow-up decision — not required, since a vacuous clause is inert rather than wrong, but
  worth noting here so a future reader does not mistake "still present" for "still load-bearing".
