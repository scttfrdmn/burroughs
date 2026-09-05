// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import "sync/atomic"

// boundaryCrossings is contract §4 B-MM-1's edge, and the count of the transitions that
// established it — decision [0052]'s chosen mechanism.
//
// B-MM-1: every host→guest transition is *"an **acquire edge over the entire shared address space**
// for the resuming agent"*, every guest→host transition the release edge, *"equivalently: a
// sequentially-consistent fence at the boundary, both directions."* Go's `sync/atomic` operations
// are sequentially consistent, and a read-modify-write is both halves at once, so one `Add` per
// direction is the whole mechanism.
//
// # Why one word for the whole package, and not one per instance
//
// A per-`Instance` word is cheaper and has a hole that opens under exactly the concurrency v1 is
// building: **a shared memory is imported**, so two instances can hold the same `*memory`, and two
// agents over one address space releasing and acquiring on two different words have no edge between
// them. Per-*object* words — one per memory, per table, per global — look closer to "the entire
// shared address space" and are wrong on the memory model rather than on cost: a release publishes
// every prior write of that goroutine, not the writes near the word, so the location decides only
// *who can observe whom* and never *which memory is covered*. One word therefore covers everything
// per-object words would, at one atomic instead of one per import. 0052's options A and B.
//
// The accepted cost is a single contended cache line once agents cross concurrently. That is a
// scalability question that now *has* its second agent — T-1's `Spawn` landed under [ADR 0068][0068] —
// and it is still filed rather than pre-solved, because a contended line is a measurement and no
// benchmark in this tree yet has two guest threads crossing concurrently. 0052 records the escape hatch
// (a depth-aware crossing, which reduces the numerator) as the same change its performance rollback
// would make. What changed is that the question is answerable, not that it is answered.
//
// # What the count witnesses, and what it does not
//
// **A presence oracle, not an ordering oracle.** Delete a crossing and a delta comes out short, which
// is what `TestEveryBoundaryCrossingIsPaired` reads. Nothing here witnesses that a write before a
// release is *visible* after an acquire; no single-agent run on one architecture can. That is B-MM-5's
// job and **#10**'s battery, on a TSO *and* a weakly-ordered platform. The mechanism is testable today
// only in the sense that its sites are countable, and overreading the count is the specific mistake
// this paragraph exists to prevent.
//
// **Process-global and never reset, so every assertion about it is a delta.** An absolute reading is a
// fact about whatever ran earlier in the binary. No test in this tree calls `t.Parallel()`, which is
// what makes a delta deterministic.
//
// # B-MM-4's default lives here
//
// B-MM-4: *"Each host call's memory-publication semantics MUST be documented in its signature. The
// default, absent annotation, is sequentially consistent."*
//
// **So this is the annotation's home, and there is deliberately no control demanding one.** The
// default makes an unannotated boundary call *conforming*, so a test requiring an annotation at every
// site would be stricter than the contract it cites. The convention instead: a boundary call whose
// publication semantics are anything other than sequentially consistent says so in its doc comment on
// a line beginning `// Publication:`, and silence means SC. The form is fixed now so that the first
// such call has a spelling to use rather than inventing one under pressure — 0052's reason for doing
// B-MM-4 in the slice where it is cheap.
//
// Every site below is unannotated, and that is the annotation: they are sequentially consistent.
//
// [0052]: ../../docs/decisions/0052-the-4-boundary-edge-is-one-package-level-sequentially-consistent-counter-because-a-shared-memory-spans-instances.md
// [0068]: ../../docs/decisions/0068-spawn-drops-0056s-walk-and-refuses-the-two-cases-a-per-instance-world-cannot-express-because-a-thread-belongs-to-exactly-one-stop.md
var boundaryCrossings atomic.Uint64

// enterGuest establishes B-MM-1's acquire edge: the host is about to run guest code, or to read guest
// state, and must observe everything every other agent released before now.
//
// # The nine sites, and why they are not the four §4 names
//
// **The heading read "six" while seven sites existed, and the sentence that caused it is still below.**
// It says the number *"is prose and is the only thing that needed a hand"* — written in the slice that
// added `runEntry`, then not given that hand in the very next slice, which added `callHost`'s pair. This
// is my own drift and not the population's, so it is repaired here rather than filed: the count is now
// nine, and the two arriving with it are the accessors named at the end of this comment. A number in a
// heading is the one figure in this file no control reads, which is exactly why it is the one that rots.
//
// §4's B-MM-1 enumerates *"host-call return, trap resume, async wake, stack-switch resume"* and **the
// engine has none of them**: no host function exists in either direction (`Extern`'s func arm is an
// owning instance plus an index, so an import is satisfied only by another wasm instance), there is no
// async wake until **#512**, and stack switching is v2. The clause is written over "every host→guest
// transition" rather than over its own examples, and this engine's transitions are the same boundary
// at a smaller radius — a host Go caller entering `internal/interp` and returning.
//
// Derived rather than listed, which is what makes the *next* one covered: **entering the interpreter
// is the same event as creating a stack**, so the four non-test `stack{…}` literals are four of the
// sites, and `TestEveryStackCreationSiteCrossesTheBoundary` asserts the pairing over that parsed
// population. `InstantiateLinked` and `Global` are the two that touch guest state without running any
// — segment copies and a direct read of a global's storage.
//
// **The derivation is what paid off, and this is the instance to point at.** The count went from five to
// six when T-1's spawn added a `stack` literal in `runEntry`, and no list here had to be edited for the
// new site to be covered: `TestEveryStackCreationSiteCrossesTheBoundary` parses the population, so the
// pairing was asserted of `runEntry` before anyone thought to check. The heading's number is prose and
// is the only thing that needed a hand.
//
// **The crossings nest, and that is granularity rather than redundancy.** `build` calling the start
// function is an entry into the interpreter distinct from `InstantiateLinked`'s, and `runConst` is
// called once per global initializer and once per active segment offset. Under the model that every
// entry is a transition, each of those is one; 0052's pre-registered rollback is the tighter model
// (only the outermost pair touches the atomic, on a nesting count on `thread`) and it exists precisely
// because the count of nested crossings is what the Instantiate row can fail on.
//
// **`thread.go`'s `runEntry` is the fourth site, and it landed** — named here before the merge so it
// would not be discovered during it: a spawned thread's first entry into the guest is a host→guest
// transition like any other. It is also the first site where the edge stops being bookkeeping, since it
// is the only one whose two ends are on different threads; the argument is at the site.
//
// **And a host call's return is the site §4 named first, and it has landed** — **#602**, [ADR 0069][0069]'s
// `callHost`. This comment predicted it as **grave #645**'s second site: the enumeration above quotes
// B-MM-1's *"host-call return"* and then records that the engine has none, so the one absent site with a
// clause of its own was the one missing from the list of sites to come. It arrived exactly as forecast,
// two crossings rather than one, because a host call leaves the guest and re-enters it: `leaveGuest` out,
// `enterGuest` back, the same pairing every site here has and the first one where the *guest* is what
// continues afterwards. Unannotated, so B-MM-4's default holds and the call is sequentially consistent.
//
// **The enumeration's "the engine has none of them" is now false of one of the four, and is left standing
// as written with this sentence beside it** rather than edited into agreement. The clause it explains is
// still that §4's four names are not this engine's sites; what changed is that one of §4's names finally
// *is* one, and a reader who finds the old sentence needs to know which one and when — not to find a
// tidied paragraph that no longer records that the site was predicted a merge before it existed.
//
// **It is the second site whose crossing is not a `stack` literal's**, so it is outside
// `TestEveryStackCreationSiteCrossesTheBoundary`'s parsed domain — `callHost` runs on the caller's stack
// and creates none, which is the whole of option A. Its pairing is a hand-written row in
// `TestEveryBoundaryCrossingIsPaired` instead, beside `Global`'s, for the reason that test's own comment
// gives about the two sites that create no stack.
//
// **`Caller.Read` and `Caller.Write` join the family that creates no stack** — Scott's ruling on the #651
// review, which gave `Caller` guest-memory access as copying accessors. They are `Global`'s shape exactly:
// host code reading and writing guest storage without running any guest instruction, so the parsed
// population cannot see them and their pairing is enumerated in `TestEveryBoundaryCrossingIsPaired` with
// `Global`'s and `callHost`'s. **The family, in full, is the enumeration that matters**:
// `InstantiateLinked`, `Global`, `callHost`, `Caller.Read`, `Caller.Write`.
//
// Named as a set and not by ordinal on purpose. The ordinals in this comment family have now gone stale
// twice in two slices — the heading's count above, and #602 calling `callHost` *"the second site whose
// crossing is not a `stack` literal's"* when this file's own text already made `InstantiateLinked` and
// `Global` two such sites, so it was the third. Both were mine. A membership claim is checkable against a
// grep; a position in an unwritten list is checkable against nothing.
//
// **One pair per access, not one per host call, and the count is where that claim is checkable.** A host
// call that reads once costs six crossings and one that reads then writes costs eight — asserted, because
// it is what an embedder is *given in place of atomicity*: [ADR 0064][0064]'s amendment records that these
// two sites are plain, since an atomic accessor would promise a tear-free read of bytes the guest may write
// plainly, and one side of a race cannot supply an atomicity the other lacks. §4's edge per access is the
// thing that is actually on offer, so a slice that collapsed the two pairs into one would be quietly
// withdrawing it.
//
// **A refusal buys no edge**: both accessors answer `ErrNoMemory` before `enterGuest`, so an access that
// reaches no guest storage establishes nothing over it. That is `Invoke`'s placement rather than
// `Global`'s, and the asymmetry between those two is already asserted one file over.
//
// [0064]: ../../docs/decisions/0064-the-bulk-and-simd-region-stays-plain-and-is-confined-by-an-enumeration-a-control-asserts-because-the-guest-model-permits-the-tear.md
// [0069]: ../../docs/decisions/0069-a-host-function-is-a-caller-and-a-value-slice-a-host-call-marks-its-thread-blocked-and-shutdown-is-its-own-terminal-method.md
func enterGuest() { boundaryCrossings.Add(1) }

// leaveGuest establishes B-MM-1's release edge: everything the host wrote while inside becomes visible
// to whichever agent acquires next.
//
// **A separate function from `enterGuest` with an identical body, on purpose.** The direction is the
// only thing a reader at the call site needs and the only thing that can be got wrong there, so it is
// in the name — `enterGuest(); defer leaveGuest()` says which edge is which without a comment at every
// one of the nine sites. Collapsing them into one `crossBoundary` would save a line here and cost that
// everywhere, and the operation being symmetric is a fact about the RMW rather than about the boundary.
func leaveGuest() { boundaryCrossings.Add(1) }
