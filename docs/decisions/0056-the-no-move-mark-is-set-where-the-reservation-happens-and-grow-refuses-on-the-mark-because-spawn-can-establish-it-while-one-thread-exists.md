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

## Amendment — the walk landed, and the tripwire this ADR cites was re-pointed

Recorded here because the ADR's text above names a control that no longer exists under that name, and a
tombstone whose citations do not resolve is worse than a tombstone that says what happened next.

`TestNothingInEngineCodeCreatesASecondObserver` — the tripwire this ADR was written against, cited twice
above — **is now `TestEveryGoStatementInEngineCodeIsPrecededByTheWalk`**, and the rename marks a real
trigger change rather than a tidier name. Its old trigger was the *presence* of a `go` statement in engine
code, which was the right question only while the remedy was outstanding: once #554 lands the walk, a
control still asserting "there are no goroutines" fails forever on a tree that did exactly what the
control asked for. Retiring it would have been the mistake this project has already paid for — *a
tripwire names a risk, not a code shape* — so the risk was re-pointed instead. It is now "a goroutine in
engine code that starts without the walk in front of it", and the trigger is the pairing: every `go` in a
non-test file must have a call to `reserveForASecondThread` earlier in the same function body.

Being a trigger change, it was watched die on its own terms — the message edit recorded in the #568
changelog entry explicitly had not been. Three falsifications, each run and read back: the walk deleted
(FAIL, unpaired), the walk moved below the `go` (FAIL, on the ordering), and a scratch non-test file with
a bare `go` in a function that marks nothing (FAIL, naming the scratch site).

Two findings from the implementation that this ADR did not anticipate, both now on the code:

- **The walk's domain is the entry's import closure, not the spawning instance's memory index space.**
  `resolveCall` resolves an imported entry to the instance that *defined* the body, and the thread runs
  there, so a walk over `in.mems` marks the wrong set. It is a closure and it is complete only because
  decision 0017 keeps a `funcref` module-local — if that widens (Q2), the walk widens with it. The
  complement is asserted too: a memory only the *spawner* reaches is deliberately left unmarked, since
  marking narrows growth to `sharedReservePages` permanently and §0 says not to pay that where no second
  thread can observe the array move.
- **Half of the "after every refusal" placement is forced by the data flow, not by the policy.** `target`
  does not exist until `resolveCall` has run, so a walk hoisted to the top of `spawn` cannot have the
  right domain at all. Noticed while trying to mutate the position alone: the naive hoist changed the
  domain too and killed a second row.

The condition-1 branch this ADR named as an omission — *"a memory with no max at all"* — is no longer
unreachable, and it is reached by exactly the population predicted: an **unshared** memory the walk marks
is under no obligation to declare a maximum, where a shared one always declares one
(`ErrSharedMemoryNoMax`). `reservation` is the function that answers it, and the consequence is that such
a memory cannot grow past the cap. That is `growthRefusedPastReservation`'s named population arriving.

## Second amendment — this ADR's completeness premise is false, and #575 is what replaces it

Appended rather than edited, on this project's rule for accepted records: the sentence in the amendment
above is wrong, and striking it would hide the fact that the walk shipped with it.

The first amendment says the walk's closure **"is complete only because decision 0017 keeps a `funcref`
module-local — if that widens (Q2), the walk widens with it."** That widening had already happened when
the sentence was written. **Grave #163** made `ref` a `{Addr, Inst}` pair and `funcRefTarget` resolves a
call through `r.Inst`; `call.go`'s comment on that function states the consequence directly — *"a table
slot may hold **another instance's** funcref […] a different instance whenever this table was imported and
someone else's segment wrote into it."* So `call_indirect` leaves the instance that owns the table, along
an edge the walk does not follow.

The sentence came from `link.go`'s `Extern.owner`, where it is accurate as *history* — a report of what
0017 records, immediately followed by "that widening `ref` is its own PR". Read in the present tense it
is false, and no citation sweep can tell the two apart, because the pointer resolves.

**Witnessed, not argued.** Two probes, whose fixtures survive as the last two rows of
`TestSpawnMarksEveryMemoryTheNewThreadCanReach`:

- A spawner exports a table and calls indirectly through slot 0, which it never fills; a second instance
  imports that table, writes its own function into the slot, and holds an unshared memory. The spawned
  thread wrote into that memory, `noMove` was false, and `cap == len == 65536` — so `grow` takes the
  allocate-and-blit arm and replaces the array under a thread running in it. That is #556, on a path this
  ADR does not cover, and it is memory-unsafety rather than a lost update: a torn three-word slice header
  can pair a new length with an old pointer.
- The same fixture with the second instance linked **after** `spawn` returns, the entry spinning on the
  shared memory until the host releases it. The instance did not exist when the walk ran.

The second probe is the one that decides the *shape* of the remedy. **Reachability is not a spawn-time
property**, so following tables and globals as well as import slots is not an incomplete fix but a fix of
the wrong kind — `table.set`, `table.copy`, `table.init` and a later instantiation all put a foreign
funcref where a running thread reads it.

What survives of this ADR: the mark, its site (*reserved ⇒ marked*, one function's invariant), `grow`'s
refusal arm reading it, the ordering (relocate while one thread exists, never after), and the walk itself
as the thing that lets a two-thread-reachable memory still grow to the cap. What does not survive is the
walk as the **soundness argument**. #575 holds the option space and is where the successor decision goes;
it prices a relocation rule keyed on whether any thread is live, reservation at instantiation under the
threads feature, a tear-free published header, and refusing the spawn. Options 1 and 3 there should be
read together with #573's ruling rather than a week apart.

#554 now has two independent blockers, and the count in the section above is stale for that reason.
