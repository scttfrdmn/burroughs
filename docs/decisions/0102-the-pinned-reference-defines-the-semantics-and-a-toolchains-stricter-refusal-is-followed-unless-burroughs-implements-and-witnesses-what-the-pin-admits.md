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

  **Corrected by measurement, 2026-10-16** (#1005; the chair's ruling on ADR 0101's Option A build, which
  asked for this correction; the original text above is kept so the correction is visible):
  - **The pin admits the wait only where a Thread of the waiting instance can satisfy it.** `canon_lift`'s
    sync tail runs `trap_if(not candidates)` over `inst.threads` alone, so the pin traps the wait wherever
    its event can only come from elsewhere.
  - **Measured on the reference** (`testdata/cmthreads/reference/refread.reading`):
    `sync-wait-own-thread`, which an own-instance Thread satisfies, completes with 7, and its control traps
    at `canon_lift:2161`. `nested-sync-unwind-control`, which only a third instance can satisfy, traps
    there too.
  - **wasmtime agrees** on all three arms, so this example's *"the pin admits it"* holds only in the first
    shape.
  - **What follows for this record's argument:** where no Thread can satisfy the wait, Burroughs admits
    what both oracles trap. That is not the toolchain-stricter case this rule covers. Under
    `gate:cm-threads`, #1005 makes it trap. The gate-off path (the witnesses named above, which use no
    Threads at all) still admits, and whether it should follow is recorded on #1005, undecided.
  - This changes text Scott has under review. Its substance stands unless he objects (noted on #999).

  **Measured with the gate off, 2026-10-16** (the chair's item 2 before the `gate:cm-threads` flip, oracle
  first; the *"undecided"* above is kept so this change stays visible):
  - **Both oracles trap the gate-off witnesses' shape.** Each fixture was nested in a provider whose async
    `op` YIELDs once (`internal/component/testdata/sync-wait-gate-off.reading`).
    - wasmtime 49.0.2 traps *"cannot block a synchronous task before returning"*, 10 of 10, on
      `async-waitset-synth`, `run-slot` and `result-store-heap`. An eager provider reads 7.
    - The reference (`refread.reading`, `sync-wait-host-import`) traps at the sync tail in every run that
      reaches the wait.
  - **So it was a default-path conformance defect (#1016), and is fixed as #1005 was, without the gate
    condition.**
    The tail judges a sync `waitable-set.wait` with the gate off.
  - **The witnesses that needed two parked callers moved to callback-lift twins**, `run-slot-cb` and
    `result-store-heap-cb`. Both read 7 on wasmtime. `call-wat-parent`'s `go` became a callback lift: its
    sync form trapped on wasmtime and its callback form reads 49.
  - **A sync `subtask.cancel` of another instance's subtask is a separate row, and there the oracles split.**
    - The reference traps at the tail (`refread.reading`, `cancel-wat's shape`). Its async-canceller
      control reads state 1 then status 4.
    - wasmtime 49.0.2 returns status 4 (`asynclift/cancel-wat.reading`).

    That row is left as it was, admitting with the gate off and judged by the tail with it on, and the
    split is recorded on #999.

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
| sync `waitable-set.wait` | admits where a Thread of the waiting instance can satisfy it, else traps at the sync tail (corrected, #1005) | wasmtime 49.0.2 traps where it would block | **yes**: `async-waitset-synth`, `run-slot`, `result-store-heap` and their witnesses | **admits** with `gate:cm-threads` off; **traps** without a candidate under it (#1005). **Since 2026-10-16, traps without a candidate with the gate off too** (amendment below) |

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
