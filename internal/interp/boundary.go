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
// scalability question that needs a second agent to ask — T-1's `Spawn` is parked in **#554** — so it
// is filed rather than pre-solved, and 0052 records the escape hatch (a depth-aware crossing, which
// reduces the numerator) as the same change its performance rollback would make.
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
var boundaryCrossings atomic.Uint64

// enterGuest establishes B-MM-1's acquire edge: the host is about to run guest code, or to read guest
// state, and must observe everything every other agent released before now.
//
// # The five sites, and why they are not the four §4 names
//
// §4's B-MM-1 enumerates *"host-call return, trap resume, async wake, stack-switch resume"* and **the
// engine has none of them**: no host function exists in either direction (`Extern`'s func arm is an
// owning instance plus an index, so an import is satisfied only by another wasm instance), there is no
// async wake until **#512**, and stack switching is v2. The clause is written over "every host→guest
// transition" rather than over its own examples, and this engine's transitions are the same boundary
// at a smaller radius — a host Go caller entering `internal/interp` and returning.
//
// Derived rather than listed, which is what makes the *next* one covered: **entering the interpreter
// is the same event as creating a stack**, so the three non-test `stack{…}` literals are three of the
// sites, and `TestEveryStackCreationSiteCrossesTheBoundary` asserts the pairing over that parsed
// population. `InstantiateLinked` and `Global` are the two that touch guest state without running any
// — segment copies and a direct read of a global's storage.
//
// **The crossings nest, and that is granularity rather than redundancy.** `build` calling the start
// function is an entry into the interpreter distinct from `InstantiateLinked`'s, and `runConst` is
// called once per global initializer and once per active segment offset. Under the model that every
// entry is a transition, each of those is one; 0052's pre-registered rollback is the tighter model
// (only the outermost pair touches the atomic, on a nesting count on `thread`) and it exists precisely
// because the count of nested crossings is what the Instantiate row can fail on.
//
// **#554's `runEntry` is the next site**, named here so it is not discovered during that merge: a
// spawned thread's first entry into the guest is a host→guest transition like any other.
//
// **And a host call's return is the site §4 named first**, which this comment has been able to say
// since it was written and did not — **grave #645**'s second site: the enumeration above quotes B-MM-1's
// *"host-call return"* and then records that the engine has none, so the one absent site with a clause of
// its own was the one missing from the list of sites to come. It arrives with the host-function surface —
// a decision, **#602**, not a merge — and it is two crossings rather than one, because a host call leaves
// the guest and re-enters it: `leaveGuest` out, `enterGuest` back, which is the same pairing every site
// here has and the first one where the *guest* is what continues afterwards. B-MM-4's annotation
// convention above is fixed so that call shape has a spelling to use on arrival.
func enterGuest() { boundaryCrossings.Add(1) }

// leaveGuest establishes B-MM-1's release edge: everything the host wrote while inside becomes visible
// to whichever agent acquires next.
//
// **A separate function from `enterGuest` with an identical body, on purpose.** The direction is the
// only thing a reader at the call site needs and the only thing that can be got wrong there, so it is
// in the name — `enterGuest(); defer leaveGuest()` says which edge is which without a comment at every
// one of the five sites. Collapsing them into one `crossBoundary` would save a line here and cost that
// everywhere, and the operation being symmetric is a fact about the RMW rather than about the boundary.
func leaveGuest() { boundaryCrossings.Add(1) }
