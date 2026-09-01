# 0056 — The no-move mark is set where the reservation happens and `grow` refuses on the mark, because `Spawn` can establish it while exactly one thread exists

Date: 2026-09-01 · Status: **accepted** — stamped by Scott in session on 2026-09-01, relayed to
[a durable comment](https://github.com/scttfrdmn/burroughs/issues/572#issuecomment-5497331174). The relay
pattern is [0042](0042-the-interpreters-second-comparator-is-deleted-rather-than-tuned-and-the-criterion-is-five-rows-in-both-directions.md)'s
and [0054](0054-every-aligned-guest-access-becomes-atomic-on-the-address-already-resolved-because-a-scoped-gate-is-unavailable-rather-than-unwritten.md)'s.

**The relay is weaker than it looks, and this ADR is the first to say so from measurement rather than
from principle.** Checking whether the tracker could distinguish a principal's ruling from the actor's
own report: a comment written by this agent and the #399 comment
[0055](0055-the-2-5-litmus-batterys-oracle-is-the-contract-read-clause-by-clause-with-its-outcome-sets-pre-registered-because-no-external-engine-can-arbitrate-a-clause-written-against-one.md)
cites as its stamp are **identical on every GitHub API field** — same `user`, same
`author_association: OWNER`, and `performed_via_github_app: null` on both, since `gh` posts with a token
that never populates it. So there is no provenance field to appeal to anywhere in this tracker, and a
`Status:` citing an issue comment rests entirely on the review that follows it. Scott ratified this and
made it standing: *"never infer a ruling from the tracker; it comes from the session."* Recorded here
because every ADR above cites a comment, and a reader is entitled to know what that citation can and
cannot carry.

Filed against **#572**. Implements the mechanism `TestNothingInEngineCodeCreatesASecondObserver` demands
before T-1's `Spawn` (**#554**) may land. Does not supersede
[0051](0051-the-atomics-become-sequentially-consistent-word-operations-over-the-backing-array-because-the-proposal-fixes-the-ordering-and-leaves-only-the-mechanism.md);
it supplies the premise 0051 rests on — that the array an atomic holds a pointer into is not replaced
underneath it.

## Context

`allocate` reserves the declared maximum as capacity for a **shared** memory so `grow` reslices rather
than reallocating (#556): a moving array lets a concurrent reader pair a fresh length with a stale
pointer, and under 0051 an atomic holds a raw pointer into that array for the duration of an access, so
a replaced array makes the atomic meaningless. An **unshared** memory does not reserve, on the stated
ground that it *"has no second observer by construction"*.

T-1's `Spawn` falsifies that ground: it refuses an instance with no shared memory and then runs the entry
**in the same instance**, so a spawn-capable instance's unshared memories are reachable from two threads.
`TestNothingInEngineCodeCreatesASecondObserver` is the tripwire, and it offers three ways out.

**Way (1) as the tripwire words it — "reserve for every memory an executing instance can reach" — is not
sufficient, and finding that out is what produced this ADR.** The reservation is **capped**:
`sharedReservePages = 128`, because 0051 pre-registered under 1 ms for the largest declaration the
address width allows and the measurement came back at **855 ms** worst case for `(memory 1 65535
shared)`, so its rollback fired. The cap cannot be removed by fiat, and `grow`'s three arms mean the cap
decides which one a memory lands on:

| arm | condition | the pointer |
|---|---|---|
| 1 | `n <= cap(m.bytes)` | reslices — **never moves** |
| 2 | `m.limits.Shared`, above the reservation | refuses with `-1` — never moves |
| 3 | default | allocate-and-blit — **moves** |

Reserving every memory puts it on arm 1 below the cap and back on **arm 3 above it**, because arm 2's
refusal is gated on `limits.Shared`. So the hole closes only up to 128 pages and reopens above it. What
closes it is **reserve *and* refuse**, and the refusal is the half way (1) does not name.

The refusal cannot be made unconditional either: the spec corpus grows unshared memories constantly,
where no vector grows a shared one at all, so an absolute arm 2 would change conforming answers for
single-threaded programs that have no second observer.

## Options

**(A) Reserve at `allocate` for every memory, and extend the refusal to every reserved memory.** Uniform
and simple. Charges every program up to 8 MiB of capacity per memory and extends a growth refusal to
single-threaded programs — the §0-partisan cost paid by exactly the programs that gain nothing from it.

**(B) Reserve at `Spawn`, and gate the refusal on a per-memory no-move mark. Chosen.** Before starting
the first goroutine, `Spawn` walks the instance's memories, relocates any unreserved one onto a reserved
backing array, and marks it no-move; `grow`'s refusal arm tests that mark rather than `limits.Shared`.

**(C) Track reachability per memory and read it at `grow` time. Rejected as unsound.** `grow` on thread A
can read *not reachable* and, while it reallocates, `Spawn` on thread B sets the flag and starts a child
that reads the abandoned array. A flag is a fact about the past only if it is written before any second
thread exists, which is (B).

**Way (3) of the tripwire's three** — show the goroutine reaches no linear memory — is not available:
`runEntry` runs guest code, which reaches linear memory by definition.

## Decision

**(B), with two conditions that are part of the ruling rather than gloss on it.**

1. **Reserve to the declared max where it is below the cap**, so the refusal bites only where no max is
   declared or the declared max exceeds `sharedReservePages`.
2. **The refusal is a named, documented engine limit**, distinguishable *in the record* from an
   out-of-memory refusal, with the excluded programs stated.

**Soundness is by construction, and that is the whole argument.** The relocation and the mark both happen
while exactly one thread exists, so the mark can never be read racily and the relocation can never move
an array another thread is reading. Single-threaded programs are byte-for-byte unchanged: no reservation,
no refusal, today's behaviour.

**The mark is set where the reservation happens, not beside it.** `allocate` is the only site that
reserves, so it is the site that marks, and *reserved ⇒ marked* is an invariant of one function rather
than an agreement between two. This also means the mark has a live engine producer from its first commit
— shared memories are marked today, exactly as they are reserved today — rather than a setter whose only
caller is a test, which `deadcode` would refuse and which would in any case be
[a control testing the helper rather than the path](../laws/controls.md).

## Consequences

- **`grow` stops asking about `limits.Shared`.** Its refusal arm tests the property the code actually
  needs. The flag remains the *producer's* input at `allocate`; it is no longer the consumer's question.
- **Behaviour is identical today.** Shared ⇒ reserved ⇒ marked, and no vector grows a shared memory at
  all, so the board cannot witness the change. That makes the unit control the only witness, which is
  stated rather than papered over.
- **The implementation splits at the seam where the oracles differ.** The mark, the gating, and the named
  limit land now with `allocate` as producer; **`Spawn`'s walk rides #554**, because its oracle needs a
  second thread to exist. Condition 1's *no max declared* case arrives with that walk — the validator
  refuses a shared memory with no maximum (`ErrSharedMemoryNoMax`), so today no marked memory can lack
  one, and writing that branch now would be writing a branch nothing can reach. **Named as an omission
  rather than left to be discovered.**
- **Which programs are excluded is now a stated fact rather than an emergent one.** A marked memory whose
  declared max exceeds `sharedReservePages` cannot grow past that cap. Today: a shared memory declaring
  more than 128 pages. With #554: any memory in an instance that has spawned.
- **`-1` is the conforming channel and not a convenience.** `memory.grow` does not trap; it reports
  failure in its result, and the reference fails a grow for reasons of its own
  (`memory.ml:60-67`). An engine limit reported through the channel the spec provides for engine limits
  is a true answer, which is the argument arm 2 already makes for shared memories.
- **#554 does not merge on this ADR.** #573 blocks it independently: `Spawn` shares the whole instance,
  so `global.set`'s plain writes to `in.globals` are data races, and a `v128` global is two plain word
  writes that tear in the value sense. That is a separate decision with its own measurement.
