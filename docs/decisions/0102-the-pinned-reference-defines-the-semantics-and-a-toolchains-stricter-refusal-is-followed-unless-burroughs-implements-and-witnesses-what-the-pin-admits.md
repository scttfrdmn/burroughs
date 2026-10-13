# 0102 — The pinned reference defines the semantics; a toolchain's stricter refusal is followed unless Burroughs implements and witnesses what the pin admits

Date: 2026-10-12 · Status: **proposed** — chair's ruling, in session, 2026-10-12, as an interim rule. The
chair will bring it to Scott decided-unless-he-objects, because it touches what the project conforms to.
Held open until then. `Ratio-Class: carried`.

**Every citation into this tree names a symbol rather than a line**, per ADR 0047. Citations into
`definitions.py` name a function, since that file is fetched material under a gitignored path.

## Context

Two decisions in the 🧵 and async work met the same question and answered it in opposite directions, and
the record did not say why:

- **#969: Burroughs followed the toolchains, which are stricter than the pin, and refused.** The pinned
  reference's `canon_thread_new_indirect` admits an `ft` of `(i32) -> ()` or `(i64) -> ()`, and traps on a
  mismatch with the entry (`trap_if(f.t != ft)`). `wasm-tools validate` and wasmtime 49.0.2 reject any
  `(i64) -> ()` `ft` at validation: *"start function must take a single `i32` argument (currently)"*.
  Burroughs refuses it at load (`component.checkNewIndirectFT`, `ErrUnsupportedForm`).
- **ADR 0101's result-store amendment: Burroughs followed the pin, which is more permissive than wasmtime,
  and admitted.** The pin's `canon_waitable_set_wait` checks only `may_leave`, so a sync task may block in
  it. wasmtime 49.0.2 traps that: *"cannot block a synchronous task before returning"*. Burroughs admits it,
  and the run-slot, result-store and waitable-set witnesses depend on it.

## What the contract says, and where it is silent

- **It names the suites.** G-1 and G-4: a gate's acceptance is its upstream spec test suite, and the
  conformance battery is the upstream suites plus the §4 litmus battery plus the Go-runtime torture set.
- **It names "the reference" without defining it.** G-3: *"Accept-direction correctness is therefore
  established by controls against the reference, never by the board."*
- **It is silent on which artifact the reference is for the component model**, where no upstream suite is
  vendored and the pinned `definitions.py` is what every slice has cited.
- **It is silent on toolchains.** wasm-tools and wasmtime appear only in §0's line on non-Go guests, never
  as an oracle. So the contract does not say what to do when a toolchain is stricter or looser than the
  reference.

That silence is what this record fills. G-3's "the reference" is read as the pinned reference, and that
reading is the rule's first clause.

## Decision

The chair's interim rule:

1. **The pinned reference defines the semantics.** For the component model that is `definitions.py` at
   CANON_PIN. For core wasm it is the upstream spec and its reference interpreter, as the board already
   uses.
2. **Where a toolchain refuses something the pin admits, Burroughs follows the pin only if it implements
   those semantics and has a witness for them; otherwise it refuses.**
3. **Each such divergence is recorded with both readings**: the pin's answer and the toolchain's, with the
   toolchain's version.

The refusal in point 2 is a refusal of an unimplemented form (`ErrUnsupportedForm`), not a claim that the
input is invalid. The pin says the input is valid.

## Application to the two cases

| case | the pin | the toolchain | modelled and witnessed? | Burroughs |
|---|---|---|---|---|
| #969: `(i64) -> ()` `ft` | admits; traps on a mismatch | wasm-tools and wasmtime 49.0.2 reject at validation | **no**: the memory64 Thread entry is not modelled | **refuses**, `ErrUnsupportedForm`, quoting the toolchains' constraint |
| sync `waitable-set.wait` | admits (`may_leave` only) | wasmtime 49.0.2 traps | **yes**: `async-waitset-synth`, `run-slot`, `result-store-heap` and their witnesses | **admits** |

Both decisions stand, and are now consistent under one rule. #969's refusal message already names the
toolchains' constraint; its reason under this rule is *not modelled*, and that is recorded here rather than
in the message, which stays as the chair approved on #973.

**Not a divergence:** `sync-trap.wasm` (#970). The pin traps a sync task that suspends with nothing ready
(`canon_lift`'s `trap_if(not candidates)`), and wasmtime traps too. The two only phrase it differently.

## Consequences

- **A toolchain's refusal is evidence, not the oracle.** When a fixture's toolchain reading and the pin
  disagree, the slice records both and applies point 2. It does not follow whichever it measured first.
- **Point 2 can flip a case.** If the memory64 Thread entry is ever modelled and witnessed, #969's `ft`
  becomes admitted under this rule, with the toolchains' refusal recorded as a divergence. That is a
  slice's own decision, made when it implements the form.
- **Scott may reverse the interim rule.** Each case above names what it rests on, so a reversal knows what
  it changes.
