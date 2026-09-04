# 0064 — The bulk and SIMD region stays plain and is confined by an enumeration a control asserts, because the guest model permits the tear

Date: 2026-09-04 · Status: **accepted** — Scott's two rulings on
[#627](https://github.com/scttfrdmn/burroughs/issues/627), quoted in full in
[comment 5547364368](https://github.com/scttfrdmn/burroughs/issues/627#issuecomment-5547364368):
*"Ruling 1: take A. The guest model permits the tear, so B buys no correctness — it buys report-freedom,
a testability property, at a throughput cost on exactly the workload §1 targets"* and *"Ruling 2:
testimony alone, no — but an enumeration with a control that fails when a new site joins isn't
testimony."* Recorded there and here by the agent that was ruled on, which is durable but **not
independent** — *durability is not independence* — so every commit in this slice is
`Ratio-Class: carried`.

Filed against #627, which is the correction of
[ADR 0054](0054-every-aligned-guest-access-becomes-atomic-on-the-address-already-resolved-because-a-scoped-gate-is-unavailable-rather-than-unwritten.md)'s
account of its own reach rather than of its mechanism. 0054's amendment states the scope; this document
decides what happens to the region the amendment exposed.

## Context

0054 made **typed word** guest accesses sequentially consistent: `atomicLoadWord` / `atomicStoreWord` at
widths 4 and 8, `atomicCell` at 1 and 2. Its title says *every aligned guest access*, and #627 measured
that the partition is not alignment — it is *typed word* versus *bulk and SIMD*. The second group writes
through plain `copy` and plain byte loops at every address, and 0054's mechanism never touches it.

**Seven sites, and all of them ship reachable.** Three are the `0xFC` bulk family (`memory.fill`,
`memory.copy`, `memory.init`), which needs no proposal at all. Three are SIMD (`v128.store`,
`v128.store*_lane`, and the reads that hand a plain slice back), and `DefaultFeatures` returns
`SIMD: true, RelaxedSIMD: true` — ADR 0025's flip and ADR 0028's — so those need no opt-in either. The
seventh is `runData`'s active-data-segment `mem.write` at instantiation, which the six-site list did not
name because that list was derived from *guest-reachable* sites and `write`'s caller set is wider. *An
issue's list is a registry, not an inventory.*

**The property at issue is weaker than the typed path's, and that is what decides this.** A typed aligned
store owes `NOTEARS` to the threads proposal. The bulk family owes nothing of the kind: the proposal
permits a racing `memory.fill` to be **observed torn**. So no option below buys correctness against the
guest's own model. What is at stake is **report-freedom under Go's memory model** — whether `-race` has
something to say about a racy guest program — which is a property of the host and of the instruments, not
of the semantics.

## The two rulings, because they are not the same question

1. **Do the bulk and SIMD plain paths join 0054's atomic regime?** — *No.* Option A.
2. **If not, what says so — may a region reachable with no opt-in rest on testimony alone?** — *No, and
   an enumeration asserted by a control is not testimony.*

Ruling 2 is the load-bearing half, because it is what makes A admissible rather than a gap dressed up as
a decision. Six comments asserting a region's extent are six unchecked claims, and this tree has already
paid for that shape: *a control's failure message is an unscanned claim* is grave #576's lesson, where a
tripwire's own text named landed work for two proposals while the control stayed green. An enumeration
with a control that **fails when a new site joins** is a different object — a bounded, machine-checked
predicate, where the site that would otherwise join silently fails the build instead.

## Choice

**A. The region stays plain, its extent becomes a pinned enumeration, and a control asserts the
enumeration.**

- No mechanism changes. No new predicate, no new gate, no throughput trade.
- `TestNoGuestMemoryAccessSiteJoinsWithoutAClassification` derives the population from
  `internal/interp`'s non-test files — every function that calls `(*memory).view`, `read` or `write` —
  and compares it against a pinned table that records, per site, **which regime it is in**: `atomic`
  (0054's typed word path), `plain` (this ADR's region), or `bounds` (a `view()` taken for `len` only).
  A new site anywhere in the package fails the control, whichever regime it belongs to.
- The seven sites keep their comments, and each now cites this ADR rather than the open issue.

**Why the confinement argument is available here and was not available in #567.** 0054's operative
grounds for refusing a scoped repair were *"with no sound gate available the racy region can't be
confined at all"* — the typed path is every load and store in the ISA, so there was nothing to enumerate
and nothing but a runtime predicate to scope by. Here the region is two instruction families and one
instantiation arm: **statically enumerable, and the enumeration is checkable**. That is a materially
different position from the one that was rejected, and it is the whole of A's case.

## Options considered

### B — bulk and SIMD writes become per-word atomic touches, unconditionally. **Refused.**

Uniform with 0054, one mechanism, no soundness question. Refused on the ruling: *"the guest model permits
the tear, so B buys no correctness — it buys report-freedom, a testability property, at a throughput cost
on exactly the workload §1 targets, and a Go guest's `memmove`/`memclr` lowering to these instructions is
the whole case against it."*

Three things that were part of the pricing and are recorded so a later reader does not re-derive them:

- **The cost is bulk throughput.** A `memory.copy` of a page is one `copy` today — a vectorized `memmove`
  — and becomes a word-at-a-time atomic loop.
- **It would not close the region either.** An extent's unaligned head and tail have no word-granular
  touch available, since the containing-word CAS `atomicCell` uses needs the word in bounds. B is
  report-free over the word-aligned interior *plus a stated residue*, which is the same shape as the
  over-claim #627 exists to correct.
- **It would have carried the carrier obligation**, below.

### C — scoped per *operation* rather than per access. **Refused on governance cost.**

One predicate read once per bulk operation — *is this memory reachable by more than one thread* — with
the plain `copy` on the false arm. 0054's cost argument genuinely does not carry here (*"you cannot dodge
a free instruction with a branch and come out ahead"* is true per access and false for a branch amortized
over up to 4 GiB), and its racy-flag argument is repairable at per-operation frequency. What is not
repairable is the third: `Spawn` is ambient, an exported method on any `*Instance` with no
spawn-capability declared at instantiation, so nothing can soundly conclude a memory will stay
single-observer. The residual narrows to one window — a bulk operation in flight when the *first*
`Spawn` sets the flag — and whether that window exists is a §4 question about whether an embedder may
call `Spawn` concurrently with an in-flight `Invoke`, which is answered nowhere. Scott's words: *"C is
refused on its governance cost — it reopens a stamped closure or rests on an unanswered ruling."* The
stamped closure is 0054's *"declared spawn-capability at instantiation is closed for now … the one change
that would reopen scoped."*

### D — writes only, leaving the plain reads. **Closed on a witness, not an argument.**

The detector reports an atomic access against a non-atomic one whenever *either* is a write, so a plain
bulk read against an atomic typed store still reports. #628's injection produced exactly that pair — an
atomic `LoadUint32` in `memAccess` against `execMemoryFill`'s plain write — in the mirror direction.

### E — exclude the region from `-race`, or suppress. **Closed twice.**

By Scott's #566 ruling (*"that byte loop is the defect, not an inconvenience the battery works
around"*) and by the standing rule that suppression is noticed-and-named or not at all. Listed so a later
reader finds a closed door rather than an absence.

## No figure, and that is a ruling rather than an omission

#627's last section priced the one measurement that separated B from C: two arms in separate binaries with
hashes checked distinct (grave #552's protocol), `memory.fill` and `memory.copy` at three extents so the
per-operation and per-word overheads are separable, on both architectures because 0054's rows differed by
architecture on exactly this axis. Scott: *"No figure is needed, so no measurement spend."* The arms are
not built and no `measured` slot is spent. **The absence is recorded here so that a later slice reaching
for B or C knows the measurement is still owed** rather than concluding from this ADR's silence that
throughput was found acceptable — it was never measured.

## #566's ruling, re-stated with its scope

Scott's #566 ruling was *"the principle is that the guest's data races must not be the host's."* A is
literally a position that some of the guest's data races *are* the host's, in a region reachable without
an opt-in, so leaving the ruling unqualified would leave the corpus carrying a principle the tree
contradicts at seven sites. Re-stated on his order, in his words, and carried into
[`docs/laws/engine.md`](../laws/engine.md#the-guests-data-races-must-not-be-the-hosts-except-where-the-model-permits-the-tear-and-the-region-is-enumerable):

> the guest's data races must not be the host's, except where the guest model itself permits the tear and
> the region is statically enumerable with the enumeration asserted by a control

*"Rather than left to read as narrowed by stealth"* is his phrasing for why this is written down: the
narrowing happens either way, and the choice is only whether the corpus says so.

## Consequences

- **The plain region is now a named population with a machine-checked extent.** Its seven members are in
  the control's pinned table with their regimes, and the failure message tells whoever adds the eighth
  what the classification question is and that widening the `plain` set is this ADR's business rather
  than a diff's.
- **The control asserts a rule, not a property, and that is deliberate.** #576 buried two tripwire names
  one proposal apart, each because the name asserted a code property the next proposal discharged. *No
  guest memory access site joins without a classification* cannot be discharged by a proposal, because a
  proposal is the thing that has to classify its sites.
- **B-MM-2's carrier survives, and that is recorded as a liability on the gap rather than a benefit of
  it.** #10's `b-mm-2-sibling-field-after-wake` — landed as
  `TestAResumedAgentSeesASiblingFieldWrittenBeforeTheNotify` — takes its verdict from the race detector's
  silence over the pair *(A's `memory.fill` plain write, B's post-wake atomic load)*, and a detector needs
  one non-atomic side to have anything to say. **If a later slice ever closes this region, that slice owes
  the case a new plain side, in the same slice.** Scott's order, on the option set and then again on the
  ruling: *"B-MM-2's carrier surviving is a use for the gap, not an argument for it — record that if the
  gap closes the case needs a new plain side, so the carrier never becomes a reason to keep it open."*
  Two things that obligation is known to imply, so they are not discovered inside the diff:
  - **The only replacement in hand is an unaligned typed store**, because 0054's Consequences record that
    the unaligned path has no atomic mechanism at all. That re-points the case's oracle at *that* gap,
    and couples it to that gap staying open.
  - **A complete repair of every plain path leaves the case with no `-race` oracle in the tree.** There is
    no third arbiter: the clause would have to be re-registered against an ordering assertion a passing
    run cannot distinguish from a lucky one, or its `Status:` goes back to blocked.
- **`-race` reports over this region are real and are not bugs in the instruments.** A racy guest program
  racing a plain bulk write reports, correctly, and the report describes the host's race as well as the
  guest's. Bounded is not zero: what bounds it is that these are races on `[]byte` *elements* — no slice
  header, no pointer, no interface word — so the failure mode is a guest-visible byte rather than a broken
  Go runtime invariant. The memory-unsafe class is [#622](https://github.com/scttfrdmn/burroughs/issues/622)'s
  and it is not this.
- **0054's amendment gets one line pointing here**, so a reader who arrives at *"whether the bulk and SIMD
  families join the atomic regime is #627's question and is not decided here"* finds where it was decided.
- **Nothing in the engine changes**, which makes this document plus its control overhead in the sense of
  behaviour 1 — charged to #622, the product slice that follows it and touches the same files.
