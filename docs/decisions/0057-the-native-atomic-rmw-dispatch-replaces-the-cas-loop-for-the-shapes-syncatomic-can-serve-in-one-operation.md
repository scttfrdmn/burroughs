# 0057 — The native atomic RMW dispatch replaces the CAS loop for the shapes `sync/atomic` can serve in one operation

Date: 2026-09-01 · Status: **proposed** — no stamp exists to cite, and *a `Status:` field is a citation to
an approval*. **Two rulings are outstanding and one of them decides whether this ADR's mechanism ships at
all**; both are on
[#559](https://github.com/scttfrdmn/burroughs/issues/559#issuecomment-5501057158) and neither is this
agent's to make. The mechanism is implemented and correct; its *landing* failed a pre-registered threshold
on `linux/amd64` and is held.

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
