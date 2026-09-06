<!-- Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0 -->

# 0075 — a table reserves to its declared max under a measured ceiling, and refuses to relocate with a sibling agent, because a declared max is the module's own number and a reservation without one would be the engine's guess

Date: 2026-09-06 · Status: **proposed** — no stamp exists to cite, and *a `Status:` field is a citation to
an approval*, so it stays open until one does. Scott's words on
[#662](https://github.com/scttfrdmn/burroughs/issues/662) were *"its price is yours to decide under
decide-and-proceed — I'm not ruling it"*, which is latitude and not approval, and it is in-session with no
artifact behind it: every commit in this slice is `Ratio-Class: carried` and this document claims his
approval for none of its option choices. *Durability is not independence.*

Filed against **[#662](https://github.com/scttfrdmn/burroughs/issues/662)** — residual 1 of
[ADR 0073](0073-grow-refuses-to-relocate-when-a-sibling-agent-could-hold-the-old-image-and-the-boundary-accessors-take-the-growth-lock.md),
the table twin of [#586](https://github.com/scttfrdmn/burroughs/issues/586). It does not touch memories:
`sharedReservePages`, `noMove`, and every arm of `internal/interp/memory.go:memory.grow` stay as 0073 left
them.

## Context

`internal/interp/table.go:table.grow` has one growth arm and it always relocates: `make([]ref, newSize)`,
`copy`, `t.img.Store`. Since [#622](https://github.com/scttfrdmn/burroughs/issues/622) the store is
through an `atomic.Pointer[tabImage]`, so an agent still holding the older image is reading its own live,
in-bounds array rather than freed memory — and every write it makes there (`table.set`, `table.fill`,
`table.copy`, `table.init`, all of which go through `internal/interp/table.go:table.view`) lands in the
array the engine has abandoned and is lost.

**What transfers from 0073 unchanged, and it is more than the code.** The *reading* transfers: an agent
that stores through an abandoned image and reloads the same slot at its next instruction fails to read its
own store back **in its own program order**, which no memory model permits, so the outcome set is empty and
the engine's job is to make the state unreachable rather than to describe it. That reading is about the
shape and not about memories, so — as with #586 — **there is no §4 clause to write and nothing here is in
the escalation set.** The *predicate* transfers too:
`internal/interp/safepoint.go:world.soleAgentLocked` counts callers, not `live` entries, and it is already
correct for a table.

**What does not transfer is the price, which is why this is a decision and not a copy.** A memory reaches
0073's refusal only when it is *unreserved*, because `internal/interp/memory.go:allocate` reserves and
marks every shared memory; a table reserves nothing at all, so `cap == len` on every table this engine
builds and the refusal would reach **every** multi-agent table growth. #662 states that price in its own
words: *"under that mechanism a threaded program's table would stop growing at its initial capacity."*

**The corpus census, measured rather than assumed.** `Instance.newTable` was instrumented to print each
declaration and the Phase-1 board run over it — 694 tables built, 581 declaring a max and 113 declaring
none:

| declared max | tables | note |
| --- | --- | --- |
| `max == min` (0, 1, 2, 3, 5, 7, 8, 16, 28, 30, 32, 64, 128) | 279 | reservation *is* the minimum; costs nothing |
| 20 (min 10) | 262 | the `spectest` host table, in every file |
| 64 (min 32) | 22 | |
| 320 (min 160) | 4 | the largest reservable declaration in the corpus |
| 256, 10, 8, 2, 1 (min below) | 12 | |
| 65536 (min 0) | 1 | `table.wast` |
| 4294967295 (min 0) | 1 | `table.wast`, `maxElems32` — 172 GB of `ref` |
| none | 113 | largest minimum 1000, in `elem.wast` |

Two facts come out of it. **A ceiling is mandatory, not a tuning knob**: without one, `table.wast` — which
instantiates both of those declarations at minimum 0 and never grows either — would attempt a 172 GB
allocation on the board's own lane. And **every other declaration is small**: 320 slots is the largest
reservable max in either corpus, 12.8 KiB of `ref`, so honouring a declared max outright is cheap for all
but two shapes, which is the asymmetry Scott named when he pointed at #572's precedent.

**One asymmetry with memories runs the other way and decides the ceiling's derivation rule.** A memory's
reservation is `[]byte` — pointer-free, so the garbage collector never scans it and the reservation costs
exactly one allocation, once. A table's is `[]ref`, five words with three pointers in them, so a
reservation is *scanned for its whole capacity on every mark cycle* whether or not the guest ever grows
into it. Memory's rule — *the largest rung whose worst case clears the bar* — prices the one-time
allocation and is blind to the recurring scan, so it cannot be transcribed here without being wrong in a
direction no allocation ladder can see.

## Options

1. **Transfer 0073's refusal alone.** Attach the world handles over `in.tables`, guard relocation with
   `soleAgentLocked`, refuse otherwise. Smallest diff, zero cost on every access path, and it excludes
   every multi-agent table growth — a threaded guest's table stops at its declared minimum forever. Its
   *measurable* cost in this tree is zero, since no corpus vector spawns, which is exactly what makes the
   exclusion easy to under-price.
2. **Reserve to the declared max under a ceiling, with the refusal as the fallback.** #572's shape.
   `newTable` allocates `make([]ref, min, min(max, ceiling))`, so a table whose declared max is at or
   below the ceiling grows by reslicing and **never abandons an image**; a table with no declared max, or
   one above the ceiling, falls through to option 1's refusal. Cost: capacity at instantiation, bounded by
   the module's own declaration, plus the mark cost above.
3. **Stop the world at the grow.** SP-1's machinery now exists
   ([ADR 0074](0074-stop-waits-on-sp-1s-own-predicate-over-the-caller-marks-because-an-arrival-is-a-caller-and-the-protocol-named-neither-end-of-it.md)),
   and a relocation performed while every sibling is parked at a safepoint strands nobody — this is the
   *precise* answer that options 1 and 2 approximate conservatively. Rejected on cost and on blast radius:
   it makes one guest instruction's latency a function of every other agent's next safepoint, and it puts
   the engine's stop protocol on a path that a guest can drive in a loop. It becomes the right answer if
   the refusal's level ever proves to matter, and is recorded here so that finding out does not start from
   scratch.
4. **Never move the slots: chunk the table** behind a directory of fixed-size blocks, so growth appends a
   chunk and an agent holding a stale directory still writes into the live block. This *removes* the
   defect rather than bounding it, with no reservation and no refusal. Rejected on the hot path: every
   `call_indirect` dispatch, `table.get` and `table.set` would pay a second load and index arithmetic to
   fix a multi-agent growth corner, and §0 is performance-partisan about exactly that trade.
5. **Lock the writers.** A per-table `RWMutex`, read-held by `store`/`fill`/`blit` and write-held by the
   grow. Precise, no exclusions, no reservation — and it puts a lock acquisition on guest instructions
   forever to buy a guarantee that only two-agent programs need. Same rejection as option 4, one level
   cheaper: correctness is not at stake between these options, since a refused grow is a conforming `-1`,
   so §0 makes the tie-break performance and the guest path loses to the instantiation path.

## Decision

**Option 2, with option 1 as the fallback arm.** Seven parts, each stated because each is a place the
memory twin's shape does *not* simply carry over.

1. **`Instance.newTable` reserves `min(declared max, tableReserveSlots)`, floored at the declared
   minimum, and reserves nothing at all when no max is declared.** The engine reserves what the module
   *declared* and never what the engine would guess: a declared max is a number the module is accountable
   for, and every reservation for a table that declared no ceiling would be the engine picking a growth
   budget on the guest's behalf. The floor keeps a minimum above the ceiling allocated in full and simply
   unable to grow, which is `allocate`'s own arm and reported the same way. The reservation's byte size
   goes through the existing `internal/interp/table.go:refSize` guard, so a ceiling nobody can allocate
   traps at instantiation rather than panicking in `make`.

2. **The reslicing arm fills the new slots explicitly, and memory's argument for why it need not is the
   one thing that must not be copied.** `memory.grow`'s reslice arm publishes `cur[:n]` and relies on
   `make` having zeroed the reservation, because zero *is* the value the spec requires of fresh memory.
   `ref`'s zero value is `{Null: false, Addr: 0}` — **function 0**, not a null reference — which is the
   grave `newTable`'s own comment records for its initializer fill. So this arm writes `r` into
   `cur[old:newSize]` before publishing the longer image, and a witness covers a reslicing grow whose fill
   value is null: without the fill, `table.get` on a grown slot returns a reference to the module's first
   function instead of trapping `uninitialized element`. No lock is needed for that write: every published
   image has length at most `old`, so no reader can see the slots being filled, and `growMu` excludes the
   only other writer of the length.

3. **Relocation is predicate-guarded by 0073's mechanism, transferred whole**: a `ws []*world` on the
   table, an idempotent `attachWorld` called from `build` over the fully populated index space before the
   start function runs, and a `relocate` that publishes freely when no world holds the table and otherwise
   takes every world's mutex *across* the check, the blit and the publication, requiring
   `soleAgentLocked(self)` in all of them. `table.grow` therefore takes `self *thread`, from the same
   `st.t` its memory twin already passes.

4. **`relocMu` is reused, not twinned, and that is load-bearing rather than tidy.** A second mutex for
   tables would restore precisely the cycle the first one exists to prevent: a table relocation holding
   world A's mutex and reaching for world B's, against a memory relocation holding B and reaching for A.
   One admission ticket for every relocation in the process is what makes the multi-lock section safe, so
   its subject widens from *memory* to *image*, and its comment says so.

5. **No `noMove` twin.** A memory carries the mark because a *shared* memory must never leave an agent
   behind: an atomic RMW on an abandoned array is invisible to every agent on the new one, which is not an
   atomic operation in any sense the model recognises, so that case is refused categorically without
   consulting any predicate. **A table has no atomics.** The whole of what a stranded table agent loses is
   plain writes, which is exactly the case the sibling predicate answers, so a categorical refusal would
   exclude strictly more programs for nothing. Two arms, not three.

6. **One counter, `tableGrowthRefusedWithASiblingAgent`, and it is a table's own.** `table.grow` reports
   failure as `-1` and shares that answer with four spec refusals, so the record that makes an engine
   limit distinguishable has to be the engine's — `growthRefusedPastReservation`'s argument. It is a
   *third* counter rather than a wider meaning for memory's second, on that same argument: a test asserting
   a table refusal must not be satisfiable by a memory refusal elsewhere in the process. The excluded
   programs are stated on it.

7. **There is no boundary-accessor half, and that is a checked fact.** 0073's decision 6 put `growMu` on
   `Caller.Read`/`Caller.Write` because a retained `Caller` is an agent no count in the engine sees.
   `internal/interp/host.go`'s `Caller` exposes `Context`, `Thread`, `Read` and `Write` and nothing that
   reaches a table, and the root package exposes no table accessor either, so the population is empty
   today. Because it is empty *today*, the requirement gets a tripwire rather than a sentence: a control
   pins `Caller`'s exported method set, so the next method added to the boundary fails a test that names
   this decision instead of silently shipping an unsynchronised reader.

### Pre-registration

The ceiling is a number that limits which programs run, so it is derived from a measurement and the
measurement is registered first. Two arms, both on `janus.local` in the `measured` group, both
best-and-worst of five per rung — **worst is the column that decides**, because a fresh arena span is
handed out already zeroed while a recycled one is cleared first, and an instantiation pays whichever it
lands on. Rungs: 320, 1024, 4096, 16384, 65536, 2^18, 2^19, 2^20 slots (12.8 KiB … 42 MiB of `ref`).

- **Arm A — `newTable` at the rung.** Bar: worst of five under **1 ms**, the bar ADR 0051 registered for
  the memory twin, taken over rather than re-argued.
- **Arm B — mark cost with reservations live.** Ten tables reserved at the rung, `runtime.GC()` timed,
  best and worst of five.

**Forecasts.** (a) Arm A clears the bar at every rung up to and including 2^18 and crosses it at 2^19 or
2^20 — the risky half, extrapolated from memory's own ladder, where 8 MiB of `[]byte` came in at 618 µs
worst and 16 MiB at 1.161 ms. (b) Arm B rises monotonically with the rung, with 320 and 1024
indistinguishable from each other and 2^18 measurably above both.

**Not benchstat, and the reason is the statistic.** This is a level against a bar, not a delta between
arms, and *the statistic must match on both sides*: the bar is a worst case, and benchstat's job — a
p-value on a central tendency — averages away the needzero spread that is the entire signal. Grave #612's
split says the same thing from the other end: `bench` is one arm and `ab` is the comparison.

**Rollback, stated before the numbers.** The ceiling is **the smallest rung that covers the corpus's
largest reservable declaration**, which on forecast (a) is 1024 — deliberately *not* memory's rule of the
largest rung clearing the bar, because arm B is the cost memory's bar cannot see and generosity is paid
every cycle rather than once. If forecast (b) is **falsified** — mark cost flat across the ladder — then
the `[]ref`-versus-`[]byte` asymmetry above is falsified with it, the derivation rule reverts to memory's,
and the ceiling is re-derived as the largest rung clearing 1 ms. If forecast (a) is falsified *downward* —
1024's worst exceeding 1 ms — the ceiling drops to the largest rung that clears, and if no rung at or
above 320 clears, the reservation is abandoned and option 1 stands alone with its exclusions stated.

## Consequences

- **What the runtime can do afterwards that it cannot now:** grow a table in a multi-agent instance
  without losing a sibling's `table.set`, for every table whose declared max is at or below the ceiling.
  **What it still cannot do:** grow a table past the ceiling, or grow a table that declared no max at all,
  while any agent other than the grower is inside `Invoke` on an instance that holds it — those return the
  spec's `-1` and increment the counter. That set is stated on the counter and is the honest cost of
  option 2 over options 3–5.
- **The refusal is over-broad on purpose**, for 0073's reason exactly: it covers the whole window in which
  a sibling agent is inside a call rather than the instant it touches the table, because narrowing it
  needs a reader indicator on every guest access — option 4's cost with option 5's shape.
- **`tableReserveSlots` is not [#635](https://github.com/scttfrdmn/burroughs/issues/635)'s limit** and
  does not discharge it. #635 is about the `math.MaxInt` guards being vacuous on every i32 path — a bound
  on how large a table may *be*. This is a bound on how much capacity is reserved *ahead* of a growth, and
  it leaves the vacuous guards exactly as vacuous. The two are related only in that both are numbers this
  engine picks, and both are package-level `var`s rather than public API for `sharedReservePages`'
  reason: promoting one is API-surface design, which is Scott's and chat-Claude's.
- **§8 M-1's *"no full-copy growth path"* is written about memories and its table equivalent is still
  unstated** — #662 flags this and this slice does not change it. After this decision a table with a
  declared max within the ceiling has no full-copy growth path either, so the gap is narrower than it was;
  writing the clause is contract text, which is not this actor's to author.
- **The reslicing arm is newly reachable for tables, which makes `growbench`'s missing table twin
  visible.** Nothing in this slice benchmarks `table.grow`, and a reslicing grow is now a materially
  different operation from a relocating one. Named rather than built: the pre-registration above measures
  the reservation, not the grow, and a harness for the grow has no consumer until a claim is made about it.
