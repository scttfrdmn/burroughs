# 0106 — `gate:threads` defaults on, with `BURROUGHS_THREADS=0` as the rollback, because the §4 battery has no hole and the off state stays witnessed

Date: 2026-10-25 · Status: **proposed** — the chair's ruling of 2026-10-25, in session (*"prepare the `gate:threads` flip, decided unless Scott objects, after G1 is merged and green"*). **Held open until Scott's non-objection exists to cite.** The forecast and the amended claim are on [#10](https://github.com/scttfrdmn/burroughs/issues/10) (comments 6389852491 and 6392164554), and this line is rewritten to cite the comment that records his answer.

**Does not supersede ADR 0088.** [ADR 0088](0088-the-component-path-takes-a-caller-supplied-capability-set-the-engines-first-such-surface.md) fixed the *shape*: a caller-supplied capability set, unknown names refused by name, and a gated capability refused by name when not supplied. It said in its Consequences that *"`gate:threads`' default is untouched … This ADR rules nothing about what the gate should mean"*, and its 2026-09-22 append repeats that for both paths. This ADR is the decision 0088 left open, and the shape survives it. `FeatureThreads` is still accepted, an unknown name is still refused, and with the rollback set, a guest using atomics is still refused by name exactly as before.

## Context

`gate:threads` governs two constructs: the limits flags' shared bit (0x02), and the `0xfe` atomics region (`internal/binary/gatemap.go`). It has been off by default (behaviour 4) while the mechanism landed behind it: spawn, futex wait/notify, safepoints, host functions, and thread exit/join. `CLAUDE.md` quotes Scott's condition for the flip: *"#586 is resolved and the §4 battery covers what the default would promise."* The flip was unscheduled under his #670 ruling.

Both halves of the condition now hold:

- **#586 is resolved**, by ADR 0073.
- **The battery has no registered hole.**
  - 17 of 19 cases are implemented, green on both CI architectures, in both the test and `-race` runs.
  - The remaining two, `sp5` and `h4`, are component-model async, deferred on #771, and the core flip does not depend on them.
  - The battery's first hole, B-MM-1's guest→guest message across a host-call return, closed in #1100.
- **The gap the forecast named first, G1, is closed** (#1102). All 256 core board files read identically with the gate off and on, compared per command. The threads proposal's own files differ, as the floor requires.

## Options

1. **Leave the default off.** Every embedder whose artifact uses atomics or shared memory must supply `FeatureThreads`. That is the state the claim text would then have to describe as opt-in. (A stock Go wasip1 guest is not among them: `TestTheGoGuestNeedsNoThreadsFeature` asserts it decodes with threads off, ADR 0080's finding 2. A Go guest built for real threads, such as the Phase 4 fork's, is.)
2. **Flip the default, with no rollback but a revert.** SIMD's and relaxed SIMD's flips took this form, but they had no runtime behaviour an operator might need to switch off in the field.
3. **Flip the default with an environment rollback**, modelled on `BURROUGHS_CM_THREADS=0` (`internal/component/cmthreads.go`). `BURROUGHS_THREADS=0` restores the old default: a guest using a threads construct is refused by name.

## Choice

**Option 3.** `binary.DefaultFeatures()` returns `Threads: os.Getenv("BURROUGHS_THREADS") != "0"`.

The rollback is read where the default is defined, so every path that starts from `DefaultFeatures()` honours it alike: core `Instantiate`, `IsWASIP1Command`, the component path's core modules, and the CLI.

## Consequences

- **What the default now promises,** re-read on the flipped tree (P1–P7 of the forecast on #10). Each row gives the promise, what covers it, and its state:
  - **P1:** the threads lane at pin de54fd2 reads **576 pass, 41 fail, 0 unsupported, 0 gated, 0 unimplemented, 2 bound**, and **all 41 failures are rows in the superseded-corpus register** (`internal/spec/threads_superseded_test.go`, 7 entries). There are 0 engine defects.
  - **P2:** `t3-…`, implemented.
  - **P3:** the two `b-mm-2-…` cases, implemented under `-race`.
  - **P4:** the two `b-mm-1-…` host-call-return cases, implemented under `-race`.
  - **P5:** `h1-…` and `h3-…`, implemented.
  - **P6** (stop-the-world) is **not promised.** `Instance.Stop` is internal and no public API calls it, so the claim text excludes it.
  - **P7**, that non-threads modules are unchanged, is G1's control.
- **The off state stays witnessed.** The tests that pinned the old default witness the refusal: ADR 0088's caller-supplied surfaces, #813's detection-is-not-permission, and the CLI's `--features` hint. They now run under `BURROUGHS_THREADS=0`, so the rollback is asserted to restore exactly the refusal it promises. Beside each, an on-state arm asserts the default now admits the guest. *A gated rule's off state is its own claim*, and moving these tests under the rollback is how that claim keeps a witness.
- **Remaining gaps, carried from the forecast:**
  - **G2:** threads-on Windows is not a promise. The claim is scoped to linux and darwin, under Scott's 2026-10-10 Windows ruling.
  - **G3:** the 41 superseded rows ride along, cited to their register, and a pin bump re-reads them.
  - **G4:** `sp5` and `h4` are component async (#771).
- **Rollback:** `BURROUGHS_THREADS=0` in the field, and reverting this ADR's one-line change in the tree.
