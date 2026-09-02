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

## What came out

Twenty interleaved rounds per board, both arms compiled to binaries up front and their hashes
checked distinct before any round ran (grave #552's protocol), `-benchtime=20000x` for the reslicing
and bar rows and `-benchtime=100x` for the reallocating one. The arms are `main` at `50f8f54` —
permanent, since it is `main`'s tip today — against `c252214`, this slice's mechanism commit, which
after a squash merge survives on the PR's own `refs/pull/N/head` rather than in `main`'s history.
arm64 is the dev box (Apple M4 Pro); amd64 is native x86-64 on `janus.local` through
`scripts/xcheck-amd64.sh`, which reported `verdict from NATIVE x86_64 (janus.local), exit 0` — not
the QEMU container, and named because *a PR asserting a cross-architecture claim states which
instrument confirmed it*.

The instrument is identical across the arms **by construction and not by care**: the baseline arm is a
worktree at `main` with `internal/interp/growbench/` copied in, `diff -r` clean, and the copy is what both
`go test -c` invocations compiled. `grep -c growMu` reads 0 in the baseline tree and 5 in the effect tree on
both machines, which is the *arms differ* half — the half #552's hash check exists for and the half a null
board would otherwise be free to mean.

```
amd64 (Intel i9-9960X @ 3.10GHz, native)   base = main @ 50f8f54   new = the mechanism commit
Reslice-32                 43.66µ ± 0%   54.00µ ± 0%  +23.68% (p=0.000 n=20)
ResliceNull-32             43.69µ ± 0%   53.92µ ± 0%  +23.42% (p=0.000 n=20)
UncontendedLockUnlock-32   11.32µ ± 0%   11.30µ ± 0%       ~ (p=0.167 n=20)
Reallocate-32              48.53m ± 3%   47.47m ± 3%       ~ (p=0.398 n=20)
geomean                    179.9µ        198.8µ       +10.49%
```

**Registration (1) holds.** `Reallocate` is `~` at p=0.398. The lock is not visible against a 64-page
`copy`, which is what that arm exists to confirm and what a measurable cost on it would have refuted.

**Registration (2) holds, and the percentage is the wrong unit to be alarmed by.** A reslice row is
`grows` = 1000 grows, so +23.68% is **+10.34 ns per grow** on a 43.66 ns/grow operation, against a bar the
same run measured at **11.32 ns per uncontended Lock/Unlock pair**. The increase is one mutex acquisition and
release and nothing else is hiding in it. That the two byte-identical rows moved by +23.68% and +23.42% — a
0.26-point spread — is what licenses reading the figure that finely at all.

**The bar row is `~` on both arms**, which is the machine reporting that it did not drift between them. It
is plain Go touching no engine code, so a move there would have meant the two arms were measured under
different conditions rather than that the change cost anything.

```
arm64 (Apple M4 Pro)       first battery, n=10
Reslice-12                 32.59µ ± 12%   34.73µ ± 14%       ~ (p=0.218 n=10)
ResliceNull-12             33.40µ ± 10%   34.11µ ± 18%       ~ (p=0.190 n=10)
UncontendedLockUnlock-12   1.811µ ±  6%   1.828µ ±  5%       ~ (p=0.796 n=10)
Reallocate-12              11.84m ±  8%   12.24m ± 13%       ~ (p=0.739 n=10)
geomean                    69.50µ         71.75µ        +3.24%

arm64 (Apple M4 Pro)       second battery, n=20, a complete replication and not an extension
Reslice-12                 34.38µ ±  9%   36.62µ ±  7%        ~ (p=0.068 n=20)
ResliceNull-12             32.59µ ±  9%   37.55µ ±  8%  +15.23% (p=0.012 n=20)
UncontendedLockUnlock-12   2.008µ ±  5%   1.917µ ±  7%        ~ (p=0.245 n=20)
Reallocate-12              11.26m ± 10%   11.19m ± 11%        ~ (p=0.989 n=20)
geomean                    70.95µ         73.70µ         +3.87%
```

### arm64 does not adjudicate registration (2), and the null arm is how that is known

Every arm64 row reads `~` except one, and the exception is the row that *cannot* differ from the row above
it: `Reslice` and `ResliceNull` are the same source, so a board where one is `~ (p=0.068)` and the other is
`+15.23% (p=0.012)` is **the instrument disagreeing with itself by nine percentage points**. Nothing about
the mechanism can be read off that.

Put in the units the registration is written in, the two boards' within-arm floors — the same-source pair's
own spread, per arm, on one run — sit at:

| board | floor between the two identical rows | the bar, as a share of the row it bounds |
| --- | --- | --- |
| amd64, n=20 | 0.06% (base), 0.15% (effect) | 25.9% |
| arm64, n=10 | 2.49% (base), 1.81% (effect) | 5.6% |
| arm64, n=20 | 5.51% (base), 2.54% (effect) | 5.8% |

**So the lesson this measurement paid for is about the bar and not about the mutex: a floor is only useful
where it is narrower than the bar it is being read against.** On amd64 the floor is two orders of magnitude
below the bar and the comparison means what it says. On arm64 the floor *is* the bar — 5.51% against 5.8% on
the second battery — so an arm64 board can neither confirm nor refute *"no more than one Lock/Unlock pair"*,
and reporting its `~` rows as a pass would be reading a resolution the instrument does not have. The
registration was written as if a null were adjudicable everywhere it is null; what it needed, and now has
retrospectively, is the floor-versus-bar comparison as an admissibility condition on each board.

The arm64 point estimates are *consistent* with the mutex and nothing else — +6.6% and +6.5% on `Reslice`
across the two batteries, so about 2.2 ns per grow against a bar the same runs measured at 1.81 and 2.01 ns —
but consistency is not adjudication, and it is recorded here as the weaker thing it is. **The ordering
matters and is stated:** the n=10 board was read first, found under-resolved against its bar, and the n=20
run was then made as a *complete second battery into fresh files* rather than as extra rounds appended to
the first, so neither board is a stopping rule applied to a running total.

### The absolute forecast is falsified, favourably, and in both readings of its unit

**15–40 ns/op added to the reslice rows** was pre-registered. Measured: +10,340 ns/op on amd64 and about
+2,200 ns/op on arm64. Read literally, as the ns/op of a row, it is wrong by two orders of magnitude. Read as
this ADR meant it — nanoseconds per grow — it is wrong the other way: the real cost is 10.3 ns per grow on
amd64 and about 2.2 ns on arm64.

Both readings fail, and the mechanism of the failure is the interesting half. *"An uncontended `sync.Mutex`
Lock/Unlock pair is tens of nanoseconds"* was a number recalled rather than measured; the same run measures
it at **1.81–2.01 ns on arm64 and 11.32 ns on amd64**, a spread of six times across two machines and a factor
of up to twenty below the forecast's floor. The forecast is left as written and falsified — *withdraw a
forecast before measurement, never after* — and what saves the registration it sits inside is that the
decisive bar was specified as *a row measured on the same run* instead of as that remembered figure. **A
forecast beaten is a forecast falsified**, and the failure names its own repair: an absolute figure earns its
place in a pre-registration only when something on the same run will produce the figure it is compared to.

**The rollback does not fire.** It was registered for `Reslice` rising by materially more than one
Lock/Unlock pair; on the board that can adjudicate it, the rise is 10.34 ns against a pair measured at 11.32
ns, and on the board that cannot, the point estimate sits within a tenth of a nanosecond of the same
comparison. Folding `Limits` into `memImage` and CAS-ing the descriptor stays available and unused.

**The registered known limitation stands as registered.** `membench` was not run as evidence here and no
claim is made that it is unmoved, because #580's floor is unbounded and a shift there would be unattributable
in either direction.

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
