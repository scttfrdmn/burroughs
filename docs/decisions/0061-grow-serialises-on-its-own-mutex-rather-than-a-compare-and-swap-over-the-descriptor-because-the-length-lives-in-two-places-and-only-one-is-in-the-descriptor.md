# 0061 — `grow` serialises on its own mutex rather than a compare-and-swap over the descriptor, because the length lives in two places and only one of them is in the descriptor

Date: 2026-09-02 · Status: **proposed** — no stamp exists to cite, and *a `Status:` field is a citation to
an approval*, so it stays open until one does. Nothing here needs one to proceed: this is mechanism, which
is product work and self-merges on a bound green, and it changes no gate's default. The choice among the
four options below is this document's, decided by the pre-registration in it, which was written before the
mechanism existed to measure.

Filed against **[#600](https://github.com/scttfrdmn/burroughs/issues/600)**, split from
[#586](https://github.com/scttfrdmn/burroughs/issues/586) at the oracle seam: this half is settled by a
witness test and a benchmark, and #586's other half needs [#10](https://github.com/scttfrdmn/burroughs/issues/10)'s
allowed-outcome tables to say what is permitted before any code can be right.

## Context

The threads proposal's memory model, at the revision
[ADR 0051](0051-the-atomics-become-sequentially-consistent-word-operations-over-the-backing-array-because-the-proposal-fixes-the-ordering-and-leaves-only-the-mechanism.md)
pins — `third_party/spec-threads` at `cc535ada1aa21cfaa3cabf3ac73b89acef78a0a0` — models the length change
as one operation:

> changes to the length (e.g. `memory.grow`) are modelled as atomic read-modify-write accesses.
> — `document/core/exec/relaxed.rst:246`

`memory.grow` here is three steps, and its own doc comment has said so since
[ADR 0058](0058-the-memory-image-is-published-through-an-atomic-pointer-because-reachability-is-not-a-spawn-time-property.md)
published the image: it reads the size, computes a new one, and publishes. Two threads growing the same
memory can both read the same old size, and the second publication can name an array built from a view
taken before the first. Nothing is memory-unsafe — 0058 bought that, and every abandoned array stays alive
— so the whole of the damage is in the value domain:

- **A lost grow.** Both threads read *s*, both publish *s+δ*, and the memory ends at *s+δ* having told two
  callers they succeeded.
- **A published image shorter than one already published**, on the reslicing arm, which a later access
  reports as `out of bounds memory access`. A trap is the one outcome a guest cannot distinguish from its
  own defect.

**The population is the embedder, not `Spawn`.** Two goroutines calling `Invoke` on one instance share
`in.mems[0]`, which is what `TestAtomicRmwIsNotObservablyTornAcrossThreads` already does to witness a lost
RMW. `Spawn` cannot reach it, because no engine code starts a goroutine and
`TestNoEngineGoroutineLandsWithoutAPrincipalsRuling` is the tripwire that keeps that true.

### The fact that decides this ADR: the size is stored twice

`memImage.bytes`' length is the authority on the current size — the reference reads `size` back out of the
array's dimension (`memory.ml:47-50`) rather than keeping a counter, and that field's own comment says why:
*"a second place holding the same fact is how the two drift."*

There is a second place, and it is deliberate. `grow`'s last statement is `m.limits.Min = newSize`, because
`memory.ml:64`'s `grow` sets `mem.ty <- MemoryT (at, lim')` and `type_of` reads that field back at
import-match time (`instance.ml:76`). `imports4.wast:22-37` pins it in its own comment: *"imported memory
limits should match, because external memory size is 2 now."* 0058 already filed that write as a residual —
*"`grow`'s write to `Min` is still a plain write … one word rather than three, so it cannot produce an
out-of-bounds access."*

So `grow` publishes the length to a descriptor **and** to a field outside it. A compare-and-swap over
`m.img` makes the descriptor's copy indivisible and leaves the other copy a plain read-compute-write — the
same three-step defect, relocated to the copy the CAS cannot reach. It would be the drift the descriptor's
comment warns about, arrived at by fixing half of it.

## Decision

**`memory` gains its own `growMu sync.Mutex`, and `grow` holds it across the whole function** — the four
validation arms, the reslice/refuse/reallocate switch, the publication, and the `limits.Min` write. One
critical section covers both copies of the length, which is what makes the length change one operation in
the sense `relaxed.rst:246` models.

`grow` also loads `img` once inside the section instead of calling `m.size()` and then `m.view()`. Two
loads were correct before and are correct now; one is what the section makes *obviously* correct, and the
reason it can be one is the lock rather than an argument about the two loads agreeing.

**Its own mutex and not `waitMu`.** The reallocating arm's `make`+`copy` is O(memory size), and `waitMu` is
[ADR 0060](0060-the-futex-queue-hangs-off-memory-keyed-by-effective-address-because-a-pointer-key-would-borrow-its-soundness-from-another-package.md)'s
futex queue lock: holding it across a multi-megabyte blit would block every `memory.atomic.wait` and
`memory.atomic.notify` on that memory for the duration of the copy, and those paths have nothing to do with
growing. One lock over two unrelated subjects is a correctly synchronised wrong grouping, and the cost
lands on the path that did not ask for it.

**There is no lock order to get wrong, and that is checked rather than assumed.** `grow` takes only
`growMu` and never `waitMu`; `wait` and `notify` take only `waitMu` and never `growMu`. Neither section
nests inside the other, so no ordering rule exists to be violated. This is stated because a second mutex on
one struct is exactly where such a rule usually has to appear, and its absence here is a property of the
call graph that a later edit could remove.

### What this does not fix, said here rather than discovered later

- **A plain store racing the blit.** On the reallocating arm, a thread storing into the old array after
  `copy` has read that region loses the store: it landed in the array the engine abandoned. A mutex on
  `grow` cannot reach it, because the racing party takes no lock and should not — it is a guest store on
  the hot path. That is #586's residual (*what a thread on an abandoned image observes*), it needs §4 to
  say what is permitted, and `noMove` is what excludes it for a shared memory. **`noMove` therefore stays,
  and its reason is unchanged by this ADR.**
- **`limits.Min` against an import-matching reader.** Serialised against other grows now; still a plain
  write against `type_of`. No path today runs import matching concurrently with a running thread, and
  0058's residual for that half stays filed on #586 rather than being quietly counted as closed here.

## Options considered

**1. A compare-and-swap loop over `m.img`.** *Rejected*, and this document's first draft chose it, which is
recorded because the reason it was chosen is a refuted form of argument.

Its stated advantage was *no new field, therefore no layout change on the access path*.
[#580](https://github.com/scttfrdmn/burroughs/issues/580) is the measurement that refutes that form: a diff
that could not change what a row executes moved unrelated rows **6–9%** on amd64 **with
`unsafe.Sizeof(atomicop)` equal at 24 on both sides** — the added field landed in existing padding. So
declining the field does not decline the risk; it substitutes a different unmeasured perturbation for a
measurable one, while claiming the immunity #580 measured the absence of.

It also fails on the merits, in two ways that only appeared once the code was read:

- **It leaves `limits.Min` racing**, per the Context above. A CAS over one of two copies of the length does
  not make the length change indivisible.
- **It makes #586's residual worse while fixing this one.** A lost CAS on the reallocating arm throws away
  an O(size) allocate-and-blit and retries, so the window during which a concurrent store can be lost
  widens from one blit to two. Fixing a value race by lengthening a different one is a bad trade even when
  both are filed.

**2. Fold `Limits` into `memImage` and CAS the whole descriptor.** *Rejected*, and it is the registered
rollback below rather than a bad idea: it is the only option that removes the two-copies problem without a
lock. Rejected now because `Max`, `HasMax` and the address width never change, so every grow would copy
immutable data in order to change one field, and import matching would have to read the declared type
through an atomic pointer — a wider blast radius, in exchange for a lock-free `grow` that no caller needs.
`grow` is a guest instruction issued deliberately, not a hot path: `exec.go`'s two `mem.grow` sites are the
only callers.

**3. Reuse `waitMu`.** *Rejected* under the Decision above — the blit would block the futex paths.

**4. Widen `noMove` to every memory, so nothing ever reallocates.** *Rejected as the wrong tier.* It makes
an unshared grow past its size class return `-1` where it succeeds today, which is a guest-visible default
change, and it would be a change made to avoid a race rather than one the spec asks for. That is a gate and
default question with its own stamp tier, not a mechanism choice, and it would not fix the reslicing arm's
lost grow at all.

## The pre-registration, written before the mechanism exists

The obvious forecast is *"no measurable change, since neither the mutex nor the field is on the access
path."* It is unusable, in two opposite ways, and both were measured rather than assumed:

- **It is analytic on the mechanism.** `internal/interp/membench` never grows — the string `grow` does not
  appear anywhere under that directory. A null result there is a zero that could not have come out
  otherwise, and *an analytic zero is not a measurement*.
- **It is un-adjudicable on the layout half.** #580's floor is real, is 6–9% on amd64, and is unbounded by
  any current protocol. A null at `membench`'s resolution cannot distinguish *no layout effect* from *an
  effect under a floor nothing has measured*.

So this slice builds the instrument the forecast needs: **`internal/interp/growbench`**, driving the real
front end through `Invoke` the way `membench` does, with three arms on one run.

| arm | memory | what `grow` does | why the arm exists |
| --- | --- | --- | --- |
| `Reslice` | `(memory 1 100 shared)`, inside its 100-page reservation (`sharedReservePages` is 128) | bounds checks, one `memImage` allocation, one `Store` | the lock is the **largest fraction** of the operation here, so this is the arm that can embarrass the change |
| `Reallocate` | unshared, `cap == len` | `make` + `copy` of the whole memory | the lock is a rounding error here **by construction**, so a measurable cost on this arm falsifies something other than the lock |
| `Null` | byte-identical to `Reslice` | — | the layout floor gets a number of its own instead of an assumption (#580) |

**The forecast is not zero, and that is the point.** An uncontended `sync.Mutex` Lock/Unlock pair is tens of
nanoseconds; a reslicing grow is a handful of comparisons, one one-word allocation and one atomic store. The
lock is plausibly a double-digit *percentage* of that arm. Predicting "no change" would be a claim I expect
to be false, and amending it afterwards is what *withdraw a forecast before measurement, never after*
forbids.

Pre-registered, same statistic on both sides per ADR 0057's ruling (*"Max effect against max noise, or
geomean effect against geomean noise with corrected p-values. Don't mix them"*):

1. **`Reallocate`: geomean effect within the `Null` arm's geomean.** The lock cannot be visible against a
   ≥64 KiB copy. If it is, the cost is not the lock and this mechanism is not what is being measured.
2. **`Reslice`: geomean effect ≤ the cost of one uncontended Lock/Unlock pair**, which is measured on the
   same run by a separate `BenchmarkUncontendedLockUnlock` in the same package rather than quoted from
   memory. This is the bar that decides the ADR: the mutex may cost what a mutex costs and no more.
   Absolute forecast, so the number is on the record before the run: **15–40 ns/op** added to the reslice
   rows.
3. **Both architectures**, darwin/arm64 and linux/amd64, per the standing protocol, with `benchstat`.

**Rollback**, stated before the numbers exist: if `Reslice` rises by materially more than one Lock/Unlock
pair, or if a contended measurement shows `growMu` serialising something other than grow against grow, take
option 2 — fold `Limits` into `memImage` and CAS the descriptor. That is the option that keeps the property
this ADR is for while changing the cost profile, which is what a rollback has to be.

**Registered as a known limitation rather than a prediction:** because #580's floor is unbounded, a shift in
`membench` after this change is not attributable to this change in either direction. This ADR does not
claim `membench` is unmoved, and it does not get to treat an unmoved `membench` as evidence.

## Consequences

- **The witness.** A test in which two `Invoke` goroutines each grow one page repeatedly, on both arms,
  asserting that the final `memory.size` equals the initial size plus the number of grows that *returned a
  non-negative result*. Counting successes rather than iterations is what makes the oracle independent of
  the reservation: a refusal past the reservation returns `-1` and is not counted, so the assertion is the
  spec's property — each successful grow adds its delta — rather than an arithmetic identity that a
  refusal would break. It must be watched die on `main` in both arms.
- **`grow`'s doc comment stops saying the race is a residual**, because that sentence is what review would
  otherwise confirm the function on. What replaces it is the residual that is actually left: the store
  racing the blit, which is #586's.
- **#586 narrows to one residual and keeps its number**; #600 closes with this.
- **`growthRefusedPastReservation` is now incremented under the lock.** Its meaning is unchanged — it
  already counted an arm no corpus vector reaches — and it is named here only so the next reader of that
  counter knows the section it sits in.
- **`memory` gains a lock-bearing field, and `copylocks` already forbade copying a `memory` by value**
  because `img` is an `atomic.Pointer` and `waitMu` is already a mutex. So no caller constraint changes,
  which is the same observation ADR 0060 recorded when it added `waitMu`.
