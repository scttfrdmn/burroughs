# 0057 — The native atomic RMW dispatch replaces the CAS loop for the shapes `sync/atomic` can serve in one operation

Date: 2026-09-01 · Status: **accepted** — Scott's ruling on the [#582](https://github.com/scttfrdmn/burroughs/pull/582)
review, which settled both outstanding questions: the acceptance criterion must put **the same statistic on
both sides** (*"Max effect against max noise, or geomean effect against geomean noise with corrected
p-values. Don't mix them in either direction"*), and this PR is **product as constituted**. The criterion
that follows from that ruling was pre-registered on
[#559](https://github.com/scttfrdmn/burroughs/issues/559#issuecomment-5502519729) before its null side was
computed, and the mechanism clears it on both architectures. Recorded here by the agent that was ruled on,
which is durable but not independent.

## Context

[0051](0051-the-atomics-become-sequentially-consistent-word-operations-over-the-backing-array-because-the-proposal-fixes-the-ordering-and-leaves-only-the-mechanism.md)
made every 0xFE read-modify-write a sequentially consistent operation on one host word of the backing
array, and it implemented all six operators the same way: an `atomicCell` and a compare-and-swap loop —
load the containing word, compute the new one, `CompareAndSwap`, retry on failure. That is correct for
every shape and it is two atomic round-trips where some shapes need one.

`sync/atomic` has single-operation primitives for a subset: `AddUint32`/`AddUint64`,
`AndUint32`/`AndUint64`, `OrUint32`/`OrUint64`, `SwapUint32`/`SwapUint64`. It has **no** `Xor`. #559 was
filed against the claim that this distinction is invisible — *"in an interpreter, the difference between one
`LOCK XADD` and a load-plus-CAS is lost in dispatch overhead"* — a **grammar claim about cost with no
instrument in the tree that could confirm or refute it**, since no benchmark here executed an atomic
instruction at all.

## Decision

**Where one `sync/atomic` primitive computes the whole result, take it; otherwise keep 0051's loop.** The
loop stays the authority — `applyRmw` is not deleted, is not duplicated, and every fast path is checked
*against* it rather than trusted beside it.

**Which shapes qualify is derived from `atomicCell`'s own geometry, not from the operator alone.** The
access width decides and the slot width does not: `i64.atomic.rmw32.add_u` is a 4-byte access in a 64-bit
slot and is whole-word exactly as `i32.atomic.rmw.add` is.

| operator | eligible at | why the boundary is there |
| --- | --- | --- |
| `and`, `or` | **every width** | The identity element lies *outside* the field — 1s for `and`, 0s for `or` — so a full-word `AndUint32`/`OrUint32` provably cannot disturb a neighbour, and no retry is needed. This is a regime #559's body did not claim. |
| `xchg` | whole word only | A narrow exchange must preserve the bytes around the field, which a `Swap` of the whole word cannot do. |
| `add`, `sub` | whole word **and** `hostLittleEndian` | Two conditions, and the byte-order one is not incidental: addition does not commute with `guestWord32`'s byte permutation, because carries cross byte boundaries. Masking does — π(g&m) = π(g)&π(m) — which is why `and`/`or` carry no such condition. |
| `xor` | never | `sync/atomic` has no `XorUint32`. The absence of a primitive, not a property of the operation. |
| `cmpxchg` | never | Go's `CompareAndSwapUint32` returns a `bool` where the spec needs the value that was observed. |

23 of the 42 region rmw rows qualify on a little-endian host and 17 on a big-endian one.

## Options considered

1. **Leave 0051's loop everywhere.** Simplest, and it is the status quo the issue disputes. Rejected on
   the measurement below: the eligible rows are 9–12.7% (arm64) and 4.4–9.2% (amd64) faster without it,
   which is not "lost in dispatch overhead".
2. **A second implementation of each operator on the fast path.** Rejected outright: two spellings of
   `and`-with-shift is how the two drift, and no corpus vector distinguishes them — the reference suite
   cannot see a fast path that is subtly wrong only at one in-word position.
3. **This: one primitive where one serves, `applyRmw` retained as the oracle, agreement asserted over the
   whole cross product** — every operator × every width × every in-word position, on both the returned old
   value and the whole memory image byte for byte, with the eligibility set pinned as an exact count.
4. **Hoist the eligibility test to derivation time** so the ineligible rows do not pay a call to be told
   no. Implemented, measured, and **declined by its own pre-registered rule** — the diff is preserved on
   `archive/rmw-hoist-declined` and any revival is a fresh ADR. Recorded here because its failure is what
   makes the price in the next section a *standing* price rather than a transient one.

## Consequences

**The measured effect, on a slot-balanced three-arm instrument with a byte-identical null arm** (12 rotated
rounds, `benchstat`, `darwin/arm64` Apple M4 Pro and `linux/amd64` Intel i9-9960X native):

| population | darwin/arm64 | linux/amd64 |
| --- | --- | --- |
| the 23 rows this ADR makes native | −9.05% … −12.69%, every row p=0.000 | −4.39% … −9.20%, every row p=0.000 |
| the 19 rmw rows it does not | +2.61% … +5.02% | `~` or +1.00% … +2.72% |
| the 7 `cmpxchg` rows | 6 of 7 `~` | 6 of 7 `~` |
| geomean, all 49 | −4.10% | −2.75% |

**The sign of the effect partitions exactly along the eligibility census on both architectures** — 23 rows
faster, 0 of the other 19 faster — which is the specificity that makes this a mechanism rather than a
curve fit.

**The criterion, and why it is a geomean on both sides.** The claim #559 disputes is *"the dispatch is
faster on the shapes it serves"*, which is distributional, so the statistic is a summary over the 23
eligible rows and **the best row is not the figure to compare**. The first criterion written here failed for
mixing the two: an effect selected as the best of a set, judged against a floor derived from a *typical*
row. Its replacement over-corrected the other way — a max-over-49-rows floor against a best-of-selection
effect — and the resolving rule is that the two sides must be the *same* statistic, whichever one is
nominated. Nominated before the null side was computed:

| | `darwin/arm64` | `linux/amd64` |
| --- | --- | --- |
| effect: geomean over the 23 eligible rows | **−10.94%** | **−6.62%** |
| matched null: geomean over the same 23 rows, old vs. a byte-identical copy | +0.55% | −0.44% |
| the bar: 3 × the matched null | 1.65% | 1.32% |
| corrected verdicts: all 23 rows at α/49 = 0.00102 | 23/23 at p ≤ 0.0005 | 23/23 at p ≤ 0.0005 |
| substance floor, carried over from the original registration | −5% | −5% |

**The null arm behaves as the multiplicity arithmetic predicts, which is a second reason to trust it**: 3 of
49 rows carry a verdict on arm64 and 4 of 49 on amd64, against an expectation of 2.45 for a perfectly flat
instrument at α=0.05. A criterion of the form *"every row is `~`"* would therefore have been failed by the
truth.

**The matched max-vs-max pairing is reported and disagrees on one architecture, which is recorded rather
than smoothed.** Largest eligible effect against largest null-arm effect over the same 49-row family:
12.69% against 3 × 1.40% on arm64 (clears), and 9.20% against 3 × 3.08% = 9.24% on amd64 (**misses by 0.04
points**, a ratio of 2.99× where the multiplier asks 3×). The distributional pairing was nominated as
governing before either was computed, and the multiplier is not re-tuned now that a number exists to tune it
against — amending a threshold after measurement is the thing the pre-registration exists to prevent.

**The price is real and it is the consequence to carry forward: the 19 ineligible rows pay 1–5%.** They
run one test to learn they get the loop they were always going to run, and option 4 above is the repair
that failed its own threshold. Any future work on this path inherits that debt rather than a clean win.

**The claim #559 disputed is refuted at the interpreter's own granularity**, and the instrument that
refutes it (`internal/interp/rmwbench`) is the first in the tree to execute an atomic instruction.

**Correctness rests on `TestTheNativeRmwDispatchAgreesWithApplyRmw`, not on this document.** The narrow
`and`/`or` argument above is the kind that reads as obviously right and hides a shift: the test is what
pins it, and it pins the eligibility count exactly rather than as a floor, because every arm silently
falling back to the loop would agree with the loop perfectly.

**What this ADR does not decide.** The contended case is untouched: whether the single-operation form's
advantage survives multiple guest agents on one word needs T-1's `Spawn`, which is parked on
[#554](https://github.com/scttfrdmn/burroughs/issues/554), and whether the narrow loop's retry rate is
ever non-trivial on adjacent bytes is unmeasured. Both stay open on #559.
