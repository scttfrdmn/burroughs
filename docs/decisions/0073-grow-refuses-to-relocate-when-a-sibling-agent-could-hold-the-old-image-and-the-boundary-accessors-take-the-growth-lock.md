<!-- Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0 -->

# 0073 — `grow` refuses to relocate when a sibling agent could hold the old image, and the boundary accessors take the growth lock, because the guest path cannot pay for coherence and the boundary can

Date: 2026-09-05 · Status: **proposed** — no stamp exists to cite, and *a `Status:` field is a citation to
an approval*, so it stays open until one does. Scott's order to fix
[#586](https://github.com/scttfrdmn/burroughs/issues/586) is in-session and carries no artifact, so every
commit in this slice is `Ratio-Class: carried` and this document claims his approval for none of its
option choices — *durability is not independence*.

Filed against **[#586](https://github.com/scttfrdmn/burroughs/issues/586)**, part 1: *"a thread left on an
abandoned array."* Part 2 of that issue — `grow` against `grow` — was closed by
[ADR 0061](0061-grow-serialises-on-its-own-mutex-rather-than-a-compare-and-swap-over-the-descriptor-because-the-length-lives-in-two-places-and-only-one-is-in-the-descriptor.md).
It does **not** touch the reservation rule, `noMove`, or anything a *shared* memory reaches.

## Context

[ADR 0058](0058-the-memory-image-is-published-through-an-atomic-pointer-because-reachability-is-not-a-spawn-time-property.md)
publishes a memory's bytes through one `atomic.Pointer[memImage]`, which made relocation
**memory-safe** and left it **incoherent**. `internal/interp/memory.go:memory.grow`'s relocating arm
allocates a new array, copies, and stores a new image; a thread still holding the old descriptor keeps
writing into the array the engine has abandoned. In bounds, alive, and lost.

**The defect is not a forecast.** A unit witness, run before any of this was designed:

```
relocated=true (old img 0x…1b0, new img 0x…1c8, len 65536 -> 131072)
WITNESS: write through the stale image is LOST — new image byte 0 = 0x0
```

**The population, derived rather than assumed.** Three facts bound it:

- `allocate` reserves — and therefore marks `noMove` — exactly the memories whose limits are
  `Shared` with a max, and `validate` requires a max of a shared memory. So **no shared memory reaches
  the relocating arm at all**, with or without ADR 0068's deleted walk.
- `Instance.Spawn` refuses an instance that reaches no shared memory
  (`internal/interp/thread.go:Instance.hasSharedMemory`), so the spawned-thread half of the population
  needs an instance holding *both* a shared memory and an unreserved one — a second memory, or an
  imported one.
- `Invoke` from two goroutines needs neither. Both callers run on `in.host`
  (`internal/interp/link.go` builds it by literal and `register`s it), so the second concurrent `Invoke`
  is a second agent on **one** thread object.

That last fact kills the predicate a reader reaches for first. *"Relocate only if this world has one live
thread"* is **unsound**: two concurrent `Invoke`s are two agents and one `live` entry. The count that
answers the question is `thread.callers`, which is
[ADR 0067](0067-a-caller-count-joins-the-blocked-mark-because-sp-2s-predicate-is-about-callers-and-a-thread-is-not-one.md)'s
subject one level over — *SP-2's predicate is about callers and a thread is not one* — and it is the same
lesson because it is the same mistake available in the same place.

**A fourth agent exists that no count in the engine sees.** `Caller.Read`/`Caller.Write` load one image
and copy against it, and `internal/interp/host.go`'s `Caller` doc says a retained `Caller` is permitted:
*"Nothing here refuses a retained `Caller`, and the accessors are safe on one anyway: they copy."* An
embedder that hands a `Caller` to another goroutine therefore holds an image with `hostCalls` at zero and
every `callers` at zero. Safe under ADR 0058, and in this defect's population: its write is lost the same
way. Any fix that counts only guest agents ships with that hole, and *an unmeasured complement is not an
empty one*.

## The §4 clause, answered — and the answer is that §4 does not need to speak

ADR 0061 and ADR 0068 both defer here: *"it needs §4 to say what is permitted"*, *"#586 needs §4 to speak
before code can be right about it."* Scott scoped this slice to that one clause — *what a racing `grow`
permits a stale reader to observe* — and the answer, read against the text that already exists, is
**nothing, and no new clause is required to forbid it**:

1. **It is not a memory-model question in the first place.** The agent whose store is swallowed observes
   the loss *itself, in its own program order*: `i32.store` at address 0 through the old image, then
   `i32.load` at address 0 on the next instruction, which re-loads the published image and reads the
   pre-store byte. A single agent failing to read its own store back is outside every memory model rather
   than permitted by a weak one, and it is what the witness above prints.
2. **Where a second agent is involved, §4 B-MM-2 already forbids it.** *"A wake delivered to a waiting
   agent MUST synchronize **all** writes that happened-before the wake on the waking agent."* A write into
   an abandoned array is unobservable to every other agent forever, so no wake can synchronize it. The
   clause needs no amendment to reach this; it reaches it as written.
3. **§8 M-1 is the standing gap, and it is not this slice's.** *"`memory.grow` MUST be amortized O(pages
   touched): address space reserved up front, commit on grow, **no full-copy growth path**."* The
   relocating arm is a full-copy growth path, so §8 already says it should not exist. Deleting it means
   reserving address space without committing it, which in pure Go means `syscall.Mmap` and a per-platform
   story. Filed, not smuggled in here.

So the outcome set for a racing `grow` is **empty**: there is no lossy observation the contract permits,
and the engine must make it unreachable rather than describe it. That is a narrowing of what #586 asked
for — it asked §4 to *define* what a stranded agent sees — and the narrowing is the finding: the two ADRs
that deferred to §4 deferred to a clause that was already there. **No normative text changes**, so nothing
here is in the escalation set.

## Options

**(A) Reserve at `allocate` for every memory.** ADR 0056's option (A) and ADR 0058's option 2, rejected
there and rejected here for the same reason plus one new one: it *penalises programs that never spawn*,
and the reservation rule is `min(Max, sharedReservePages)`, so a memory with **no declared max** — the
common unshared shape — cannot be reserved by the current rule at all. It does not reach its own
population.

**(B) Stop the world across the relocation.** Functionally the best option in this list: it preserves
every program's semantics at zero hot-path cost, because threads park between instructions and no view
survives a park. **Blocked, on two counts.** `Stop`'s arrival protocol counts receives rather than
identities and has a witnessed false success ([#656](https://github.com/scttfrdmn/burroughs/issues/656)),
and a mechanism resting on a `Stop` that reports success while a thread is still running would relocate
under exactly the agent it exists to evacuate — the defect, hidden behind a fix. And a memory reachable
from two instances would need two worlds stopped at once, which ADR 0068 is the record of not being
expressible (*a thread belongs to exactly one stop*). **Registered as the functional restoration**: when
#656 lands, option (B) is the successor to (D)'s refusal, and it is a new ADR rather than a silent
widening.

**(C) A lock, or a reader indicator, on every guest access.** Sound and priced wrong: it puts an
acquisition on the path every board vector runs, which is the cost ADR 0058 spent a whole pre-registration
avoiding. Its seqlock spelling — publish, then have the reader re-check the image and retry — is worse than
expensive, it is **unsound**: an atomic RMW is not idempotent, so a retried operation is not the operation
the guest issued.

**(D) Refuse to relocate when a sibling agent could hold the old image.** *Chosen.* Zero cost on the guest
access path, and it mirrors the arm that is already there: a `noMove` memory past its reservation returns
`-1` with a named counter, which is conforming because `memory.grow` reports engine limits in its result
(`memory.ml:60-67`).

**(D′) The refusal on a process-wide count of live threads.** This is the form ADR 0058 *registered* as
its rollback — *"`grow` refuses to relocate whenever any thread is live, on a process-wide counter"* — and
#586's body records it as retained. **Not taken, and the departure is deliberate rather than forgotten.**

- **The rollback did not fire and this is not it firing.** 0058's rollback was conditioned on missing a
  *performance* bar, and the bar cleared on both architectures. Taking the shape now, for a coherence
  reason the registration did not anticipate, is a decision — saying *"the rollback fired"* would be a
  forged provenance about this project's own discipline.
- **A process-wide count couples embedders that share nothing.** Two independent instances in two
  goroutines is the most ordinary embedder shape there is, and under (D′) one instance's grow is refused
  because the *other* is busy. Today's tree would not notice — nothing in `internal/spec` or
  `internal/interp` calls `t.Parallel()`, so no two tests are ever in flight together — and that is
  *protection by coincidence*, which is not protection: one `t.Parallel()` added later silently changes
  what an unrelated memory answers.
- **It misses the concurrent-`Invoke` population entirely**, which needs no spawned thread and is the half
  of #586 reachable on today's default gates.

**(E) Leave it, and document what a stranded agent observes.** #586's other branch. Foreclosed by the
clause above: the observation is a violation of the stranded agent's own program order, so there is nothing
to document but a bug.

## Decision

**1. The relocating arm asks whether any *other* agent could hold the image, and refuses if one could.**
Two arms, and the empty one is what lets every unit fixture reach the subject at all:

| the memory's instances | what `grow` does past capacity |
| --- | --- |
| none (`len(ws) == 0`) — constructed and never installed in an index space | relocates, as today |
| one or more | takes **every** one of their `mu`s, relocates **iff** `self` is the sole agent in all of them, else refuses with `-1` |

**This table had a third row and the board deleted it** — see decision 3, which is the amendment. The row
read *"more than one (`manyWorlds`) → refuses with `-1`, always"*, and it is recorded as struck rather than
edited away because what falsified it is a measurement this ADR did not take before choosing.

**2. The world handle is attached where reach is granted, not by walking to find it.** Every way to touch
a memory goes through some instance's `mems` index space — guest instructions resolve through
`Instance.memoryFor`, SIMD through `Instance.vecMemarg`, the boundary through `Instance.hostMemory` — and
`link` populates that index space for imports *and* definitions. So one pass over `in.mems` at the end of
instantiation attaches `&in.world` to each memory, and an instance created later that imports the same
memory attaches its own on its own pass. **This is not ADR 0056's walk.** The walk traversed *outward*
from a spawning instance to find memories, and #575 falsified its completeness because reachability grows
after the walk runs. This registers *inward*, at the one site that grants reach, so the later instantiation
that broke the walk is the event that maintains this.

**3. Every world the memory is in answers, and a process-wide `relocMu` is what makes holding several
`world.mu`s safe.** `memory.ws` is a slice — one entry per instance that defines or imports the memory,
deduplicated by identity — and `relocate` takes `relocMu`, then each world's `mu`, then requires
`soleAgentLocked(self)` in all of them.

**This decision replaces a refusal, and the thing that replaced it is a board.** As first written, decision
3 said `manyWorlds` refuses rather than checking both worlds, *"because the alternative is inventing a lock
order"*: answering for two worlds means holding two `world.mu`s at once, two grows on two such memories
could take them in opposite orders, and `growMu`'s comment would stop being able to say *"there is no lock
order to get wrong, and that is a property of the call graph rather than a rule anyone is keeping"*. **The
lock premise was right and the cost premise was never measured.** `make check`'s test gate priced the
refusal at **30 default-lane vectors** — `memory_grow.wast` 14, `linking.wast` 7, `imports4.wast` 6,
`imports.wast` 3, plus 14 more in the threads lane — because `memory_grow.wast` exports two memories from
one module and grows them from a second. *The suite's own fixture for growing a memory is the
two-index-space case.* So the arm this ADR called unreachable (*"nothing in either corpus reaches this
arm"*, written into `memory.go` beside the counter) was the ordinary path, and the sentence was an
eyeballed claim about the corpus where a board was available. *An unmeasured stability claim is not a
protection*: the over-refusal had a level, and only the instrument could name it.

**What closes the lock-order hazard is smaller than the total order the first draft rejected.** `relocMu`
admits one relocation at a time process-wide, so a *second* simultaneous holder of any `world.mu` never
exists and no cycle can form. It needs no field on `world` — which is an embedded value in `Instance` with
no constructor to assign a rank in — and nothing for a later `world`-creating path to remember. The order
is `growMu` → `relocMu` → `world.mu`…, and the property it still rests on is the same one as before:
nothing in the engine takes a `world.mu` and then a `growMu`. What it costs is serialisation of an arm that
is already a full-memory `copy` behind a per-memory mutex, reached only when a memory grows past its
allocated capacity, and off every guest access path.

ADR 0058's *"reachability cannot be reasoned about across instances"* was cited for the refusal and does not
support it here: this mechanism does not reason about reachability, it enumerates the index spaces that
grant it (decision 2), which is the same registration the single-world arm uses.

**4. The check is held across the blit and the publication, not taken before them.** Every `w.mu` is
acquired before the `make` and released after the `Store`. Checking and then blitting would leave the arrival
window open: an agent admitted during a multi-megabyte copy loads the *old* image, writes into it after the
copy read it, and the publication swallows that write — the defect, moved rather than fixed. `enterCall`
and `admit` both take `w.mu`, so holding it is what makes "no sibling agent" true *for the duration* rather
than at an instant. **The only agent this can block is one whose arrival would have made the relocation
unsafe**, which is why the O(size) critical section is acceptable here and is not acceptable on `waitMu`.

**5. The predicate counts callers and excludes the grower by identity.** `world.soleAgentLocked(self)`:
every live thread that is not `self` must have `callers == 0`, and `self` itself must have at most one. Not
`len(live) == 1` (unsound — see Context), and not `hostCalls`, which adds nothing: a host call is made
*from* guest code, so its thread already has `callers >= 1` and is refused by the walk. The grower is
excluded by identity rather than by arithmetic on the count, because a grow inside a `(start …)` function
runs on `in.host` with `callers == 0` — `invokeIndex` is where `enterCall` lives, and instantiation does
not go through it — so *"total callers == 1"* would misread instantiation as a second agent, and a
subtraction would misread it as none.

**6. The boundary accessors take the growth lock; the guest path does not.** `growMu` becomes an
`RWMutex`; `Caller.Read` and `Caller.Write` hold `RLock` across their image load and copy. This is the
retained-`Caller` agent from the Context, and it gets a lock rather than a count because it is not an agent
the engine admits — there is no `enterCall` to hook, and a count incremented outside `w.mu` reopens
decision 4's arrival window. **The asymmetry is the design and not an inconsistency**: the guest path
cannot afford an acquisition (option C) and buys coherence by being counted; the boundary path is one
embedder call per host call, can afford one, and buys coherence by excluding the blit. `RWMutex` rather
than `Mutex` so two embedder reads do not serialise against each other. The lock goes in
`Caller.Read`/`Caller.Write` and **not** in `memory.read`/`memory.write`, whose other callers are
`Instance.memAccess`, the SIMD accessors and `atomicNotify` — the hot path this decision exists to keep
free.

**7. The refusal is counted in its own channel.** `growthRefusedWithASiblingAgent`, incremented on this
arm and no other, beside ADR 0056's `growthRefusedPastReservation`. Two counters rather than one because
they answer different questions — *"this memory is past its reservation"* and *"this memory has company"*
— and a single counter would make the board unable to say which limit a guest hit.

## The pre-registration, written before the mechanism exists

**Not optional, and #580 is the reason.** The claim *"nothing here is on the access path"* is true of the
instructions and false as a claim about cost: #580 measured a diff that could not change what a row
executes moving unrelated rows **6–9%** on amd64, with `unsafe.Sizeof` equal on both sides, because a
field landed in existing padding. This change adds a slice field to `memory` and widens `growMu` from
`sync.Mutex` (8 bytes) to `sync.RWMutex` (24), so it is a layout change to the struct every guest access
dereferences. *Cheap is a grammar claim*: the number goes down first.

| | governing |
| --- | --- |
| population | `internal/interp/membench`'s 4 rows — load/store × aligned/unaligned, driving the real interpreter through `Invoke` |
| protocol | ADR 0057's three-arm rotated protocol: `old`, `new`, and a byte-identical `null` copy of `old` asserted equal by hash, arm *i* in slot *(i+r) mod 3* |
| effect | **geomean regression over the 4 rows**, `benchstat`, on `darwin/arm64` and `linux/amd64` (`janus.local`, group `measured`) |
| bar | **2.0%**, ADR 0058's bar on the same population, so the two are comparable |
| null arm | must come back within noise of `old`; where the null's own spread reaches the bar the board does not adjudicate and the run is repeated |

**Estimate, stated separately so that a failed estimate narrows rather than licenses:** **0%** — no
instruction is added to any access path, and the only mechanism a regression could come through is layout.
So a hit above the bar is *not* an argument for tuning the mechanism; it is an argument about field order,
and the registered response is to reorder rather than to reconsider.

**Rollback, if the bar is missed and reordering does not recover it:** keep decisions 1–5 and drop
decision 6, taking the retained-`Caller` hole as a *stated* named limit with its own issue instead — the
`RWMutex` widening is the only part of this that touches the struct's layout by more than a word, and the
guest-agent half of the defect is the half Scott's order names.

### Measured, at the landed mechanism

Both arms are `./scripts/ab.sh --pkg ./internal/interp/membench --base HEAD~1 --head HEAD --rounds 12
--null`, three arms rotated per round, `base` and `null` asserted byte-identical by build hash and `head`
asserted *different* from both — *assert the arms differ, not only that the null matches*.

| | geomean vs base | null vs base | per-row `benchstat` | verdict against the 2.0% bar |
| --- | --- | --- | --- | --- |
| `darwin/arm64`, Apple M4 Pro, not queued (the dev box is nobody's measurement slot) | **+0.75%** | +0.05% | all four rows `~` | **inside the bar** |
| `linux/amd64`, `janus.local` group `measured`, task 18, 0 concurrent at submit, i9-9960X | **−0.87%** | −0.66% | all four rows `~` on `head`; the null's two load rows come back −0.70% / −1.19% at p≈0.03–0.05 | **inside the bar** |

The **0%** estimate stands: no row moves significantly on either architecture, and the sign disagrees
between them, which is what a layout-only change with no added instruction looks like. Decision 6 is kept
and the rollback does not fire.

**An earlier amd64 run on this branch did not adjudicate, and it is recorded rather than dropped.** Taken
before the `relocMu` mechanism existed (`base` `28e87da…`, task 17), it read head −3.39% with the **null arm
at −2.42%** — a null whose own displacement reaches the bar it is supposed to sit inside, so *compare the
floor to the bar*: the board could not distinguish the change from the machine, whichever way it pointed.
The pre-registration's response to that state is that the run is repeated, and the repeat above is that
repeat, at the same host, group and round count. The favourable-looking −3.39% is not banked; it was never
a measurement.

## The witnesses, and the battery they were watched to die under

Two unit tests in `internal/interp/memimage_test.go`, in the pair this ADR's consequences pre-committed:
`TestARelocatingGrowRefusesWhileASiblingAgentCouldHoldTheImage` (the refusal, the counter, the size left
alone, a byte written through the *held* image read back through the memory — the lost write asserted
directly — and then the sole-agent relocation succeeding once the sibling returns) and
`TestAMemoryInTwoIndexSpacesRelocatesOnlyWhenEveryWorldIsIdle` (a supplier that grows once as the floor;
then the same `*memory` in two index spaces relocating from *either* side while both are idle; then an
agent parked in one instance while the other grows, in both directions, refusing).

**The second test is a replacement, and what it replaced asserted the defect as the rule.** It was
`TestAMemoryInTwoIndexSpacesRefusesToRelocate`, and it pinned decision 3's first draft — an unconditional
refusal as soon as a second world appeared — in its own failure messages. The board falsified that draft by
30 vectors (decision 3 records the breakdown), so the test was not merely narrow: a reviewer reading it
would have confirmed the bug.

Six mutations, each certified against **its own `go test -run` invocation** because *a panic hides the
next row's death*, over a committed baseline because *an injection battery needs a committed baseline*.
The table was written down before the run. **A sixth column is the spec board**, added because the board is
the instrument that priced the first draft and no unit witness in the previous battery could have.

| mutation | refusal | two-index | concurrent grow | publishing race | publishes-fresh | spec board |
| --- | --- | --- | --- | --- | --- | --- |
| A — `relocate` publishes and returns true unconditionally | FAIL | FAIL | FAIL | pass | pass | pass |
| B — the `soleAgentLocked` loop deleted | FAIL | FAIL | FAIL | pass | pass | pass |
| C — `soleAgentLocked` returns false | FAIL | FAIL | FAIL | pass | pass | FAIL |
| D — `relocate` consults only `m.ws[0]` | pass | FAIL | pass | pass | pass | pass |
| E — `relocate` refuses whenever `len(m.ws) > 1` | pass | FAIL | pass | pass | pass | FAIL |
| F — `attachWorld` appends without the identity check | pass | pass | pass | pass | pass | pass |

What each column is for. **A** is the whole mechanism gone: it fails the refusal's return value, the
counter delta, the unchanged size, *and* the held-image byte reading back `0x0` instead of `0x5a` — which
is the defect itself, restored and detected. **B** is predicted and measured *identical to A*, and that
identity is the measurement: in the previous battery B diverged from A because the `manyWorlds` arm still
refused the cross-instance case, and with that arm gone the two rows collapse. **C** refuses everything, so
it fails the same three witnesses plus the *floors* inside them — the sole-agent relocation, the two-index
test's own successful grows, and `TestConcurrentGrowLosesNoPages`'s non-empty grant phase, which is the
vacuous pass an over-refusal has to be pinned against — and it fails the board, because `memory_grow.wast`
needs relocation to succeed. **E** is the replaced mechanism, and its board column is the 30 vectors,
reproduced on demand. Columns 4 and 5 ride reserved or worldless fixtures and pass on every row, which is
the permit direction: the battery is not merely breaking the package.

**Row D is where the battery earned its keep, and it did so by missing.** `D` restores a single-world
predicate, so it was pre-registered to fail the two-index test — and it **passed**. 35 of 36 cells matched;
this was the one miss, and *a failed pre-registration narrows, it does not license*. The cause is attach
order: `ws` is append-ordered, the supplier's `build` attaches first, so `m.ws[0]` *is* the supplier's
world — and the witness had parked its agent in the supplier and grown from the importer. The mutation
consulted exactly the world holding the parked agent and refused correctly, for a reason having nothing to
do with being right. *The shape of what survives names the bug*: what no cell could see was the predicate's
**breadth**. The repair is to the fixture and not to `relocate` — part 3 became a table over both
directions, the importer gained its own parked host call, and `m.ws[0] == &sup.world` is asserted so the
attach order the rows reason about is checked rather than assumed. Re-run after the repair: **all 36 cells
match**, with D failing the mirrored row on all three channels (`grow` returns `4` instead of `-1`, the
counter moves `0` instead of `1`, the size goes `4 → 5`) and still passing the original row. That
asymmetry, printed, is the coincidence.

**Row F comes back all-pass as pre-registered, and that is a named gap rather than coverage.** Every
witness module here holds one memory at one index, so `attachWorld`'s identity check never fires and an
unconditional append still produces one entry per instance. Nothing in this battery covers the duplicate —
the shape it guards against is one instance naming the same memory at two indices, and no fixture builds
one. Recorded because *an unmeasured complement is not an empty one*: the check is defensive, its cost is a
linear scan of a slice with one or two entries under a lock the grower already holds, and if it were wrong
the failure would be a self-deadlock rather than a wrong answer (the same `w.mu` twice) — which is why the
battery runs under `-timeout 120s`, so a self-deadlock reports instead of sitting.

**`TestConcurrentGrowLosesNoPages` is on that board as a third witness, and this ADR takes something away
from it.** #600's control ran two arms, and its reallocate arm's exact grant census — 99 grants per round,
the only count a serialised run can produce — is unavailable now, because most of that arm's 120 attempts
are the refusal this ADR introduces. Measured on the landed mechanism: **1141 grants and 1259 refusals**
over 20 rounds (57 grants per round against the 99 a serialised all-permitted run makes), 0 pages lost, 0
`limits.Min` drift — a split that is scheduling-dependent by construction, which is why the assertion is the
partition and not the number. What replaces the census is that **partition**: every attempt is a grant
or a `growthRefusedWithASiblingAgent`, the counter is read per round, and the two must sum to `agents *
attempts` while the memory is short of its declared max. That is still exact, and both phases are asserted
non-empty. What is genuinely lost is *reproduction strength for #600's own mechanism*: with decision 0061's
mutex removed, the reallocate arm used to report bad rounds in 20 of 20 and the reslice arm in 7 of 20, and
the 20-of-20 half is now unreachable rather than merely unrun. Stated here and at the test, because the
next person to remove that mutex will read a weaker green than the one that convicted it.

## Consequences

- **A threaded program's unreserved memory effectively stops growing past its capacity.** Any live sibling
  thread inside an `Invoke` — running, spinning, or parked in a futex wait — has `callers >= 1` and refuses
  the relocation, whether or not it is touching memory at that instant. The refusal is over-broad on
  purpose: the engine cannot tell at grow time whether a sibling is mid-access without option (C)'s
  hot-path indicator, so it **refuses the window rather than the event**. Stated plainly rather than buried
  because it is a guest-visible behaviour change: a grow that succeeds today, in a program with a sibling
  agent, returns `-1` after this.
- **What is *not* changed, and this is most of the tree.** A single-agent program is byte-for-byte
  unchanged. Every shared memory is unchanged (reserved, marked, reslices). The reslicing arm is unchanged
  and needs no predicate: the array does not move, so a stale descriptor names the same bytes at a smaller
  length and writes through it land where they are read. The board cannot witness any of this — no vector
  spawns and none grows past a reservation — so the oracles are unit witnesses, which is what ADR 0056's
  arm already had to do.
- **The over-refusal has a floor a test can pin**: with no sibling agent the relocating arm must still
  relocate and succeed. A fix that refuses everything would satisfy "no lost write" vacuously, so the
  witness comes in a pair — the refusal *and* the still-working relocation — and *comparisons need a
  vacuity check* is the reason the second one exists.
- **The retained-`Caller` lifetime question is answered here only by exclusion.** Decision 6 makes such an
  accessor safe against a concurrent grow; it does not decide whether retaining a `Caller` past its host
  function should be permitted at all. `host.go` already records that invalidation *"was considered and not
  taken"*, and this ADR does not reopen it — it makes the permitted thing correct.
- **Three residuals are filed rather than folded in**, because each has a different oracle and *issues
  split at the oracle seam*:
  1. **[#662](https://github.com/scttfrdmn/burroughs/issues/662)** — `table.grow`
     (`internal/interp/table.go:table.grow`) has **no reslicing arm and no `noMove`**, so it relocates on
     *every* grow: a `table.set` through a stale image is lost, on a wider population than this one. #622
     covered the memory-unsafe half for tables and not this half. The predicate here transfers unchanged;
     the reslicing arm does not, which is why the price is that issue's decision rather than a copy of
     this one.
  2. **[#663](https://github.com/scttfrdmn/burroughs/issues/663)** — `grow`'s write to `m.limits.Min` is a
     plain write, read racily by import matching at `internal/interp/link.go`
     (`matchMemoryType(ext.mem.limits, …)`). The repair is to *delete the second copy* — the published
     image's length is already the authority — not to synchronise it, so it is a different change with a
     `-race` oracle rather than a lost-write one. `memory.go` claims this is *"filed with 0058's coherence
     residual, #586"*; it is not in #586's body, and that sentence is repaired at the site in this slice.
  3. §8 M-1's *"no full-copy growth path"*, per the clause section above.
