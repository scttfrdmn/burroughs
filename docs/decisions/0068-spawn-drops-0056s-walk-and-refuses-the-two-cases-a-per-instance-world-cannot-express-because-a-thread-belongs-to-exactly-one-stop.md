<!-- Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0 -->

# 0068 — `Spawn` drops ADR 0056's walk and refuses the two cases a per-instance world cannot express, because a thread belongs to exactly one stop

Date: 2026-09-04 · Status: **proposed** — no stamp exists to cite, and *a `Status:` field is a citation to
an approval*, so it stays open until one does. What lets it proceed without one is stated in **Choice**
rather than assumed: the exported shape is contract §2 T-1's, quoted, and the order to land T-1 is
Scott's own.

Filed against **[#554](https://github.com/scttfrdmn/burroughs/pull/554)**, T-1's spawn, parked since
2026-09-01. It implements [ADR 0058](0058-the-memory-image-is-published-through-an-atomic-pointer-because-reachability-is-not-a-spawn-time-property.md)'s
last unbuilt consequence and it does **not** settle
[#12](https://github.com/scttfrdmn/burroughs/issues/12) (T-5 exit/join/detach, contract §10.3),
[#586](https://github.com/scttfrdmn/burroughs/issues/586) (0058's coherence residual) or
[#514](https://github.com/scttfrdmn/burroughs/issues/514) (what a `thread` is). Each is named at the site
that would otherwise imply it was handled.

## Context

T-1 is written and has been parked for three reasons that all resolved somewhere else:

- **Its walk lost its subject.** ADR 0056's second half had `Spawn` walk the entry instance's import
  closure and reserve-and-mark every memory it found, so that no backing array could move under a
  running thread. [#575](https://github.com/scttfrdmn/burroughs/issues/575) showed the closure is not the
  reachable set — a table slot can hold a foreign `funcref` — and ADR 0058 dissolved the question rather
  than widening the walk: `memory.img` is an `atomic.Pointer[memImage]`, so *"moving the array is now
  memory-safe for every memory, marked or not"* and the mark's remaining job is coherence.
- **Two of the four preconditions its tripwire names are closed** — #543 (`memory.atomic.wait`'s
  suspend path) and #573 (all three global arms, the reference arm under ADR 0066's atomic pointer) —
  and #575 is the third.
- **The fourth, #10's §4 litmus battery, is parked by order** past what spawn needs.

What has *not* been examined is what the safepoint machinery landed since the branch was written does
when a second thread exists. ADR 0067's `Stop` asks `blocked == callers`, `world` is per-`Instance`, and
`world`'s own doc says its one-instance extent is right *"today"* only *"because `Spawn` is parked"*. So
the parked branch cannot be replayed: it predates the predicate it has to satisfy.

## The defect the parked branch would land, measured against `Stop` rather than against memory

A spawned thread that runs guest code without being counted as a caller has `blocked == 0` and
`callers == 0`, so `Stop`'s predicate reports it **at a safepoint while it executes guest
instructions**. That is #592 verbatim — the grave ADR 0067 was written to close — reintroduced by a new
site rather than by a changed predicate, and `Invoke` is the only place `enterCall`/`leaveCall` is
paired today. `enterCall`'s own doc comment states the exhaustive claim that makes this a defect and
not a judgement call: instantiate-time execution *"is the only guest code that runs outside `Invoke`"*.

The same asymmetry decides world membership. `thread.w` is one field, so a thread belongs to exactly
one world and there is no representation in which both the spawner's `Stop` and the entry instance's
`Stop` reach it. A thread in no world is worse: `enterCall` and `poll` are both nil-legal, so an
unregistered spawned thread silently opts out of stopping entirely.

## Options

1. **Replay the walk, add the caller wrap, register in the entry instance's world.** Faithful to 0056
   and wrong on three counts. The walk reserves through `allocate`, and the reservation is capped at
   `sharedReservePages`, so *every* memory in a spawned instance — including its unshared ones — becomes
   uncapped-growth-refusing at 128 pages. That is a guest-visible narrowing of which programs run, paid
   for a coherence guarantee the walk cannot deliver (#575), and 0058's *"strictly better for the
   guest"* is falsified by the counter's own excluded-programs paragraph.
2. **Drop the walk; register in the entry instance's world; accept that the spawner's `Stop` misses
   the thread.** Contract-defensible — the thread is a guest thread of the instance whose code it runs
   — and it makes `in.Spawn` followed by `in.Stop` return `nil` while a thread runs. A stop that lies
   in the same signature pair the caller just used is the failure mode this project has paid for most
   recently.
3. **Drop the walk; register in the spawner's world; accept that the entry instance's `Stop` misses
   the thread.** The mirror image, with the hole moved to the instance the embedder did not call.
4. **Drop the walk; refuse the case that makes the two differ, and refuse a spawn during a stop.**
   Chosen.

Option 4 is not a superset of 2 and 3: it declines to answer rather than answering half. A refusal is a
named engine limit a caller can read; options 2 and 3 are a `nil` from `Stop` that means the opposite of
what it says.

## Choice

**The walk is deleted.** `reachableMemories`, `reserveForASecondThread` and the eight-row dynamic test
that exercised the closure go with it. 0058 sanctions this in terms — *"`Spawn` **may** keep it … nothing
depends on the walk being complete"* — so this is implementing 0058's consequence and not reversing
0056's stamped ruling: 0056's mark, its refusal arm and its named engine limit all stay exactly where
they are, and `allocate` remains the only site that reserves. **The safety argument is checkable rather
than inherited**: `allocate` reserves — and therefore marks — every memory whose limits are `Shared`, so
no shared memory ever reaches `grow`'s relocating arm, with or without a walk. `grow`'s own comment
already says so: *"a reserved memory never reaches this arm, so no agent is ever left behind on a shared
memory."*

**`runEntry` wraps its guest execution in `enterCall`/`leaveCall`.** Not optional and not a
convenience: it is the denominator of ADR 0067's predicate, and without it `Stop` reports a running
thread as arrived. The pair goes around `in.invoke` and nothing wider, which is `Invoke`'s own placement
argument (wrap the call that runs guest code, not the one that may delegate).

**A spawned thread joins the spawner's world, and the two cases where that would be a wrong answer are
refused rather than resolved.**

- **`ErrForeignEntry`** — the entry function resolves into a different `Instance` than the one `Spawn`
  was called on. `resolveCall` follows import chains to the defining instance, and `runEntry` must run
  the body with that instance's state, so `target != in` is exactly the case where "the thread's world"
  has two candidate answers and one field to hold them. Refused.
- **`ErrStopInProgress`** — a stop is in flight on this instance. Admitting a member mid-round makes it
  an (N+1)th potential sender into an `arrived` channel `Stop` sized to N, and *"a thread that blocked
  here would be at a safepoint and unable to say so, which is the one deadlock this protocol can
  have."* Refused.

Both are SP-4's dynamic-membership question, which `world`'s doc already assigns to #515's successor
work; refusing is how this ADR avoids pre-deciding it. Neither refusal is reachable from a guest that
spawns its own function while running, which is the shape T-1 describes.

**Registration happens before the `go` statement, under the same critical section as the
stop-in-progress check.** `world.admit` does both, which is what makes the pair indivisible: a thread
that is a member cannot be missed by a stop, and a stop that has begun cannot gain a member. The
window between `admit` returning and the goroutine's first instruction is covered by `poll` and not by
the lock — `Stop` sets `stopReq` on the new member and counts it at a safepoint on `blocked == callers`
(both zero), which is *accurate in effect*: the goroutine reaches `enterFrame`, polls, and parks before
executing one guest instruction. Stated because the reason is `poll`'s nil-and-early-park behaviour
rather than the predicate, and a reader who checks the predicate alone will think it is a hole.

**The exported surface is not a free choice, which is why this proceeds without a stamp.** Contract §2
T-1 fixes it: *"a thread-spawn host primitive of the shape `spawn(entry_func, arg, stack_hint) → tid`,
creating a wasm thread backed 1:1 by an OS thread."* `ThreadID`, `Stop` and `Resume` are already
exported. What is genuinely new and free is the identity of the two refusal errors, and both are
additive — a widening stops returning them and breaks no caller.

## Consequences

**`Spawn` returns a `tid` and no handle, and that is #12 and not an omission.** `spawn` keeps the
`*thread` inside the package so the engine's own tests can observe that a thread ran; anything a caller
could do with the object — join, detach, read the terminal error — is contract §10.3.

**A finished thread stays in `world.members`, and reads as at a safepoint.** `blocked == callers == 0`
after `leaveCall`, so `Stop` counts it arrived and never waits on a dead thread. That is true rather
than lucky — a terminated thread cannot touch guest memory — but the membership itself leaks one entry
per spawn, and reaping is #12's, filed rather than implied.

**#586 stops being a precondition and becomes a named residual with a stated population.** `grow`'s
relocating arm calls it *"a fifth precondition on unparking `Spawn`"*, which this ADR falsifies. What
landing exposes is narrow and is not memory unsafety: an **unshared** memory in an instance that has
spawned, grown by one thread while another holds an older image, loses the writes made through that
image. The spec permits lost plain accesses on a memory that is not shared and does not describe
atomics on one, which is why #586 needs §4 to speak before code can be right about it. No shared memory
is in this population.

**Two tripwires are re-pointed, and one is renamed.** Both `go`-statement scans fire on this slice.
`internal/interp`'s `TestNoEngineGoroutineLandsWithoutAPrincipalsRuling` keeps its name — it asserts the
*rule*, which no proposal can discharge — and its trigger narrows from "any `go`" to "any `go` outside
the ruled site", keyed by enclosing function rather than by line, since *re-key an allow map by content,
not by arithmetic*. `internal/testenv`'s module-wide sibling — formerly
`TestNothingInEngineCodeCreatesASecondObserver` — asserts a *property* that this slice makes flatly
false, so it becomes `TestEveryEngineGoroutineIsAtASiteADecisionAuthorises`, named for the rule it is
really keeping: *name a control after the rule, not the property*, which this tree has now paid for
twice, one proposal apart. Both re-pointed forms are watched die by injection, and both are watched to
*permit* spawn's own `go`, because *a re-pointed control has not been watched die* and *"it now permits
X"* is a forecast to run.

**Each keeps a vacuity arm the narrowing itself needs, which is the failure mode an allow list usually
has.** An entry matching nothing leaves its control green while authorising a site that no longer
exists, so the next `go` written into that function is permitted by a key nothing checked. Both pin the
authorised site's `go` count at exactly one and FAIL in *both* directions — too few means the exemption
has rotted, too many means a second goroutine was added at an authorised site, which is not the same as
being an authorised goroutine. Pinned rather than floored, because *a floor is not a census*.

**Contract §2's T-1 is satisfied and T-2 is not tested by anything here.** T-2 forbids a main-thread
special case; `newThread`'s id counter is atomic so any thread may spawn, but no vector and no test in
this slice has a spawned thread spawn again. Named as untested rather than claimed.

## The pre-registration, written before the mechanism exists

This slice adds no instruction to the interpreter's hot path: `enterCall`/`leaveCall` are two mutex
operations per *thread entry*, not per call site, and the deleted walk removes work from `Spawn`. So the
forecast is **no measurable change on any `make bench` arm**, and the falsifying observation is any arm
moving beyond its null excursion. This is a claim about a path nothing new runs, which is the one shape
where a null result is informative rather than vacuous — the interpreter loop is untouched by
construction, and the check is that the descriptor indirection 0058 already paid for did not acquire a
second reader here.

*Not* pre-registered, because there is no baseline to compare against: the cost of a spawn itself. A
first-of-its-kind operation has no null arm, so a number for it would be a measurement without a
comparator — reported if taken, never weighed against a bar that does not exist.
