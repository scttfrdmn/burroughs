# 0053 — Tear-freedom is one aligned word access, chosen from the slice's own host address, because 0051 already asserts the base

Status: **proposed**

Issue: [#557](https://github.com/scttfrdmn/burroughs/issues/557)
Contract: none — and that is a finding, not an omission. See *This requirement is not §4's* below.
Supersedes: nothing. Rests on [ADR 0051](0051-the-atomics-become-sequentially-consistent-word-operations-over-the-backing-array-because-the-proposal-fixes-the-ordering-and-leaves-only-the-mechanism.md)'s base-alignment premise and reuses its endianness machinery.

## Context

The threads proposal requires **non-atomic** accesses not to tear when they are naturally aligned
and no wider than 32 bits. At the revision ADR 0049 pins
(`WebAssembly/threads @ cc535ada1aa21cfaa3cabf3ac73b89acef78a0a0`), `document/core/exec/runtime.rst:742-746`:

```
tearing(iN', N, u32)  =  NOTEARS   (iff u32 mod N/8 = 0  ∧  N ≤ 32)
tearing(iN', N, u32)  =  ε         (otherwise)
tearing(fN', N, u32)  =  ε
```

called from the *ordinary* load and store rules — `instructions.rst:1763` and `:2315`, each
`Let notears? be tearing(t, N, ea)` — not from the atomic ones. So this is a different requirement
from #542/ADR 0051's, on different opcodes, and it exists even with every atomic implemented
perfectly.

**Where the engine tears, verified rather than taken from the issue.** `memory.read` returns
`m.bytes[ea : ea+n]` — a slice of the backing array, no copy — so the only decomposition on the load
side is `loadValue`'s loop:

```go
for i := len(bs) - 1; i >= 0; i-- {
        raw = raw<<8 | uint64(bs[i])
}
```

Four separate byte reads for an `i32.load`. The store side is `copy(m.bytes[ea:], storeBytes(v, width))`
— a `memmove` whose granularity is not a guest-visible guarantee, over a byte slice rendered per store.
Both are single call sites: `storeBytes` has exactly one caller (`memory.go:518`) and the memop load
path exactly one (`memory.go:524`).

The rendered slice is **not** a heap allocation, which this ADR first said it was; see *The
allocation was never there* below.

### The authority for "does not tear" is the Go memory model, and it is a local citation

`$(go env GOROOT)/doc/go_mem.html:245`, shipped with the toolchain rather than fetched:

> A read r of a memory location holding a value that is not larger than a machine word must observe
> some write w such that r does not happen before w and there is no write w' such that w happens
> before w' and w' happens before r. That is, each read must observe a value written by a preceding
> or concurrent write.

That *is* tear-freedom, stated at the language level for locations up to a machine word — so a plain
`*(*uint32)(…)` read is conforming **by the specification**, not by inspecting what the compiler
emitted. The same section is explicit about the other direction:

> Reads of memory locations larger than a single machine word are encouraged but not required to
> meet the same semantics … implementations may instead treat larger operations as a set of
> individual machine-word-sized operations in an unspecified order.

Which lines up with the proposal's own `N ≤ 32`: what Go declines to promise is exactly what the
proposal declines to require.

### The `-race` tension I expected does not exist, and the measurement is why

CI runs `go test -race -shuffle=on ./...`, and the same Go document opens the region above with
*"Any implementation can, upon detecting a data race, report the race and halt execution of the
program. Implementations using ThreadSanitizer (accessed with `go build -race`) do exactly this."*
The obvious worry is that implementing tear-freedom with **plain** accesses makes every racy guest
program a `-race` failure, and that #10's litmus battery is made of deliberately racy guest programs
— which would be an argument for using `sync/atomic` on the plain path too, buying race-detector
cleanliness at the price of `LDAR`/`STLR` on the hottest path in the engine.

Four arms, one goroutine writing an aligned 4-byte word inside a `[]byte` and one reading it,
`go test -race`:

| load side | store side | detector |
| --- | --- | --- |
| plain `*(*uint32)` | plain `*(*uint32)` | **DATA RACE** |
| `atomic.LoadUint32` | `atomic.StoreUint32` | clean |
| plain `*(*uint32)` | `atomic.StoreUint32` | **DATA RACE** |
| **the byte loop as written today** | `atomic.StoreUint32` | **DATA RACE** |

The fourth row is the one that decides it. **The engine's existing byte loop already races the
detector against an atomic store**, so `-race` exposure is a property of the tree as it stands and
not something this mechanism introduces or can avoid: it arrives the moment two goroutines run guest
code, which is #554, and it is #10's problem to state how a deliberately-racy battery is run. The
third row kills the middle option separately — mixing a plain load with an atomic store does not
placate the detector, so "atomics on the store side only" buys nothing.

What that leaves is the honest trade: **all-atomic is the only race-clean option, and it costs
sequential consistency on every `i32.load` in every program to buy it.** §0 is
performance-partisan and the language already gives the property for free, so this decision does not
pay that.

### This requirement is not §4's

§4 is boundary-scoped: it governs host↔guest transitions, which is what
[ADR 0052](0052-the-4-boundary-edge-is-one-package-level-sequentially-consistent-counter-because-a-shared-memory-spans-instances.md)
implemented. Tear-freedom is a **guest-to-guest** property with no §4 clause, so its bindingness comes
from §0's correctness-neutrality plus §9's proposal-suite gate rather than from this project's
contract. That is the third instance of the same discovery and #557 names it as such; recorded here so
the next guest-level memory-model requirement is not looked for in §4 and then presumed absent.

## Options

### Where the alignment test comes from

The condition is `ea mod N/8 = 0` on the *guest* effective address, but what a single-instruction
host access needs is absolute alignment in the host address space. The two coincide exactly when the
backing array's base is 8-byte aligned — which `checkBaseAlignment` (`memory.go:185`) already asserts
once per memory, for ADR 0051's benefit, refusing construction otherwise.

- **A — plumb `ea` to the access site.** Requires threading the effective address into `loadValue`,
  whose four call sites include two in `gcobj.go` that have no effective address at all. Rejected:
  it widens a signature to carry a value that a cheaper and more local fact already implies.
- **B — test the slice's own host address, `uintptr(unsafe.Pointer(&bs[0])) & (width-1) == 0`.**
  Chosen. `bs` *is* `m.bytes[ea:ea+n]`, so its first element's address is the host address of `ea`;
  with the base 8-aligned the test is equivalent to the guest-space condition for every width ≤ 8,
  and where the premise does not hold — `gcobj.go` passes Go-allocated field bytes, not linear memory
  — the test is still *sound*, because a false answer only declines the fast path. The implication
  runs the right way: aligned-in-guest ⟹ aligned-in-host ⟹ fast path ⟹ no tear.

**B makes this decision's conformance rest on ADR 0051's assertion, and that is stated rather than
inherited quietly.** If `checkBaseAlignment` ever failed on some platform the memory would not
construct at all, so the failure mode is one loud refusal and never a silent tear — but the
dependency is real and a reader changing that assertion needs to find this ADR from it.

### What the access is

- **i — `encoding/binary.LittleEndian.Uint32`.** Endianness is guaranteed by the API, and Go's
  compiler intrinsifies it to a single load. Rejected on the *authority*, not the codegen: at the
  source level it is four byte reads, so the memory model's word-sized guarantee does not apply to
  it, and the property would rest on an intrinsic — an implementation detail that no citation covers
  and that a future compiler is free to change. *A comment asserting the property the code lacks
  makes review confirm the bug.*
- **ii — a typed word read through `unsafe`, normalized by `guestWord32`/`guestWord64`.** Chosen.
  This *is* "a read of a memory location holding a value not larger than a machine word", so the
  guarantee applies as written. The machinery already exists and is already tested: ADR 0051 built
  `hostLittleEndian`, `guestWord32` and `guestWord64` precisely because `memop.go`'s endianness
  comment predicted that reading a word through `unsafe` would need it, and
  `TestAtomicCellAgreesWithTheByteLoop` already checks that path against the byte loop with the byte
  loop as the authority.
- **iii — `sync/atomic` on the plain path.** Rejected above: it is the only race-detector-clean
  option and it buys that at sequential consistency on every plain load and store. Recorded rather
  than dismissed, because it is the option a future contributor will re-propose the first time #10's
  battery is awkward to run, and the reason it was declined is a cost measurement rather than a
  taste.
- **iv — leave it, since nothing is observable until #554.** Rejected. This is the shape #557 was
  filed to prevent being absorbed, and *"unobservable today"* is the same argument that would have
  deferred every other item in this family.

## The decision

**An aligned access of width 1, 2, 4 or 8 is one typed host-word access, normalized to guest byte
order; an unaligned access keeps the byte loop.** The predicate is the slice's own host address. On
the store side the rendered byte slice goes away with it, because a whole-word write needs nothing
rendered — a code-shape consequence, not a measured saving (below).

### Three widths are over-conformance, and it is free rather than tolerated

The proposal requires tear-freedom only for integer accesses with `N ≤ 32`. This mechanism also
delivers it for aligned `i64` (`N = 64`, third condition absent) and for aligned `f32`/`f64` (the
float line has no side condition at all, so a float access may always tear). That asymmetry is the
consequence of `tearing` easiest to read past — `i32.load` and `f32.load` at the same aligned address
have *different* obligations — so it is written down here and the rule is **not** stated in the code
as "aligned and ≤ 4 bytes", which would be a width-and-alignment predicate the specification does not
have.

Not tearing where tearing is permitted is unobservable: no vector can assert that a permitted
nondeterminism *occurred*, and single-threaded execution cannot see either. And branching on
`isFloat` to reintroduce a byte loop would be *slower* code written to be less conforming, which is
not a trade anything in §0 asks for. So the partition is by alignment and width alone, and the
float rule is documented where a reader would otherwise reconstruct it wrongly from the code.

## What this does not cover

- **SIMD.** `simd.go` reads and writes through the same `read`/`write` pair at four sites, and a
  `v128` access is 128 bits, so `tearing` gives it `ε`. But the *narrow* SIMD accesses —
  `v128.load32_splat`, `v128.load32_lane` and their siblings — touch 4 bytes, and whether the
  proposal's `t` at `instructions.rst:1763` is the lane type or the vector type decides whether they
  carry `NOTEARS`. **That is a reading of normative text and it is not decided here**; it is filed
  rather than answered by whichever way this implementation happens to fall, and it currently falls
  on the byte-loop side because `simd.go` does not call `loadValue`.
- **Whether a tear can be *observed*.** Nothing here witnesses non-tearing; it witnesses that the
  single-word path was taken. A witness needs two guest threads racing on a weakly-ordered platform,
  which is #554 plus a battery, and it is the same presence-versus-ordering distinction ADR 0052
  drew for the boundary count — *a presence oracle is not an ordering oracle*.
- **The `-race` question.** Measured above and assigned: it belongs to #554 and #10, is already true
  of the tree, and is not made better or worse by this decision.

## The pre-registration, written before the numbers exist

**The instrument does not exist, and that is measured rather than assumed.** No benchmark in the tree
executes a wasm load or store: every `load`/`store` hit across `dispatchbench`, `dropbench`,
`scanbench` and `vecbench` is prose in a comment, checked by grep over all four packages. `scanbench`'s
module builder emits functions and nothing else — no memory at all — which is the same finding that
corrected ADR 0052's forecast one slice earlier, arriving again because *a pre-registration forecasts
the instruments* and the instrument here is absent rather than merely mis-aimed.

So this slice builds the smallest one that can ask the question, `internal/interp/membench`, and it is
**overhead charged to this product work** rather than a deliverable. Its axis is the one the mechanism
turns on:

- **`aligned` rows** take the fast path. **`unaligned` rows cannot**, and they are the within-instrument
  control: if they move, something other than the fast path changed, and *an unmeasured complement is
  not an empty one*.

### The allocation was never there, and this is withdrawn before the instrument exists

The paragraph below originally forecast **`i32.store` aligned faster by at least 5%**, on two named
mechanisms: a 4-byte `make` per store deleted, and a 4-iteration byte loop plus `memmove` replaced by
one store. It also said *"if this row does not move materially the allocation was already being
optimized away and I will say so rather than record a null."*

That condition was checkable without the benchmark, and checking it came out against the forecast.
`storeBytes` inlines into its only call site, and at that site the compiler reports the slice **does not
escape** — on `main`, at the real call site, `go build -gcflags='-m -m'` prints
`memory.go:518:53: make([]byte, width) does not escape`. Restoring the entire pre-change store path into
this branch and measuring allocations through `Invoke` gave **0 per store**, unchanged from the new path.
Two independent mechanisms, agreeing: escape analysis and a counter.

So the first mechanism is void and the forecast loses most of its basis. **It is narrowed to a
direction, and the ordering is the whole of its legitimacy:** no `membench` existed when this was
written, so this is a registration corrected against a *different* measurement, not a threshold moved
having seen the number it judges. Recorded in place rather than quietly restated, because a favourable
5% would otherwise have been banked against a mechanism that was not doing the work.

**A control died with it.** `TestAnAlignedStoreAllocatesNothing` was written to witness the deletion
through `Invoke` as a per-store allocation differential — the one assertion in the set that could tell
which path ran from the public entry point. It passed, and then passed again with the pre-change path
restored: it could not be made to fail by the defect it existed to catch, so it was deleted as
stillborn rather than kept as protection nothing was protecting. *A control isn't born until it's
watched die.*

**The forecast, and it can fail in both directions:**

- **`i32.store` aligned gets faster, direction only.** One mechanism remains — a 4-iteration byte loop
  plus a `memmove` call replaced by a single typed store — and no figure is registered for it, for the
  same reason the load row carries none. A regression falsifies the mechanism.
- **`i32.load` aligned gets faster, and by less** — one load replacing four byte reads, a shift and an
  or, with no allocation on either side. No figure is registered for it because I have no basis for one
  that is not arithmetic dressed up as a prediction; what is registered is the **direction**, and a
  regression falsifies the mechanism.
- **`i32.load` unaligned moves less than 2%**, in either direction. This is the control, and it is the
  row most likely to embarrass the change: the fast path adds a predicate to a path that cannot use it,
  so unaligned access pays the test and gains nothing.
- **The rollback, stated now so it is not invented later:** if the unaligned control regresses beyond
  2%, the predicate moves off the per-access path — the memop table gains a precomputed
  "fast-path-eligible" width flag so the branch is on a field already loaded, and only the address test
  remains. If the *aligned* rows fail to improve, the mechanism stays anyway on conformance grounds and
  the performance claim is withdrawn from the report rather than restated more weakly.
- **A forecast beaten is a forecast falsified**, and here that cuts the way it did not last time: a
  *larger* speedup than registered needs a mechanism too, and "it got much faster" is exactly the
  result that goes unexamined.

## Consequences

- **`loadValue`'s doc comment becomes wrong in the direction it warned about.** It currently says the
  byte loop exists so the engine's answer *"does not depend on the machine it runs on"*, naming a
  big-endian host reading through `unsafe` as the hazard. That warning is now load-bearing for this
  function too, and the comment is corrected in place rather than swapped out, since it is testimony
  that predicted the very machinery this change reuses.
- **The byte loop stays, and stays the authority.** It is the unaligned path, so it cannot rot into
  dead code, and it is what the new path is checked against — the same shape ADR 0051 used, for the
  same reason: the byte loop is the code the whole spec suite has already validated.
- **The suite still validates it, and that was measured rather than assumed.** The obvious risk of
  adding a fast path is that the corpus stops reaching the old one, leaving "the byte loop is the
  authority" true of history and false of the tree. Reversing the *fallback* loop's index alone fails
  both `TestPhase1Files` and `TestThreadsProposalLane`, so the board does exercise unaligned accesses;
  reversing only the *word* path fails both as well. Neither path is carried by the other.
- **A vacuity risk with a name.** The fast path could silently never be taken and every agreement test
  would still pass, because both paths compute the same value. So the fast path is counted and the
  count is asserted, which is the presence-oracle pattern from ADR 0052 — *a control isn't born until
  it's watched die*, and here the death to watch is the fast path never firing.
- **`checkBaseAlignment` acquires a second dependent.** Its comment names ADR 0051; it now also
  guarantees this decision's conformance, and saying so is what makes the coupling findable from the
  assertion rather than only from here.
