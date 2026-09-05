<!-- Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0 -->

# 0069 — A host function is a `Caller` and a `[]Value`, a host call marks its thread blocked, and shutdown is its own terminal method

Date: 2026-09-05 · Status: **proposed** — Scott ruled the surface, and the record of that ruling is a
comment on [#602](https://github.com/scttfrdmn/burroughs/issues/602) *posted by the actor it was given
to*, so it is durable and **not** independent provenance. *A `Status:` field is a citation to an
approval*, and this one holds open for a stamp on the report that carries it. Commits in this slice are
`Ratio-Class: carried` for the same reason.

Filed against **[#602](https://github.com/scttfrdmn/burroughs/issues/602)**. It settles the surface and
the dispatch seam; it does **not** settle
[#12](https://github.com/scttfrdmn/burroughs/issues/12) (T-5 exit/join/detach, contract §10.3),
[#586](https://github.com/scttfrdmn/burroughs/issues/586) (ADR 0058's coherence residual), §10.4's
guest-visible cancel primitive, or §6 readiness. Each is named at the site that would otherwise imply it
was handled.

## Context

Five rows of the §§2–5 pre-registration cannot be built, and none of them is waiting on a merge. They are
waiting on a **blocking host call**, which this engine has no surface for in either direction:
`b-mm-1-message-passing-across-a-host-call-return`, `sp2-a-parked-agent-touches-no-guest-memory-during-the-stop`,
`sp4-stop-completes-without-waking-parked-agents`, `h1-a-parked-agent-does-not-starve-its-siblings`, and
`h3-shutdown-interrupts-a-parked-agent`. T-1's spawn landed under
[ADR 0068](0068-spawn-drops-0056s-walk-and-refuses-the-two-cases-a-per-instance-world-cannot-express-because-a-thread-belongs-to-exactly-one-stop.md)
and moved none of them, which is #602's own finding: the blocker they named was neither necessary nor
sufficient.

Two things in the tree were written to expect this arrival and say so:

- **`boundary.go`'s `enterGuest` names the missing site.** B-MM-1 enumerates *"host-call return"* **first**
  and the engine has none — grave #645's second site — with the shape already stated there: *"it is two
  crossings rather than one, because a host call leaves the guest and re-enters it: `leaveGuest` out,
  `enterGuest` back … and the first one where the *guest* is what continues afterwards."*
- **B-MM-4's annotation convention is already fixed**, unused, for exactly this call shape: a boundary call
  whose publication semantics are not sequentially consistent says so on a `// Publication:` line, and
  silence means SC.

## Options

The four the report in [#647](https://github.com/scttfrdmn/burroughs/pull/647) carried to Scott, on the
signature:

- **A.** `type HostFunc func(c *Caller, args []Value) ([]Value, error)`, with
  `HostExtern(ft binary.FuncType, fn HostFunc) Extern`. Costs a per-call `[]Value` allocation and the
  boxed `Value`; buys an aliasing story closed **at** the boundary.
- **B.** A raw-stack form — the host reads and writes the operand stack in place. Faster and it hands the
  embedder a soundness burden: an embedder who miscounts corrupts the interpreter's stack.
- **C.** An identity-carrying form, where a host function is a first-class object with its own identity for
  `ref.func`/`call_indirect` equality.
- **D.** No host functions; build the five rows some other way. Falsified by #602's own table — there is no
  other way to park an agent.

**Ruled: A, with C additive and B never the default.** Scott, on the #647 review, verbatim: *"Take A, with
C's identity as a later additive widening and B named as a possible fast path, never the default. The
asymmetric-reversibility argument is decisive by itself — a soundness burden pushed onto embedders can't be
taken back once anyone depends on it."* No figure was measured for any option and none was offered as a
reason; **the argument that decided it was reversibility, not speed.**

All four sub-choices confirmed in the same ruling: blocking always permitted with no opt-in flag (H-1 is
unconditional); cancellation through a `context.Context` on `Caller`, created **once per thread** so it is
not a per-call allocation; the shutdown trigger spelled `Instance.Close() error`; and H-2 enforced by
`Caller` having no entry point on it — no control needed, because the method that would violate it will not
exist.

## Choice

Option A as ruled, plus three mechanism choices this slice makes on its own because they are internal and
the ruling does not reach them.

**1. The dispatch seam is `resolveCall`'s callee, and a host function is a fifth thing an `Extern` can
hold.** `resolveCall` returns `(*Instance, *binary.Func, *binary.FuncType, error)` and a host function has
no `*binary.Func` at all, so the seam is where the callee is *named*, not where a frame is built. `Extern`
gains a `host *hostFunc` arm with `Kind == binary.ExternFunc` and a nil `owner`; `importedFunc` already
hands back the `*Extern`, and the host arm is answered there rather than by widening `invoke`. What decides
it against widening `invoke` is that `invoke`'s whole contract is *the arguments are already on the shared
stack and the results come back onto it* — which the host arm honours — while `buildFrame`, the locals
array and the dispatch loop have no host meaning. A `*binary.Func` field left nil through four call sites
is the shape that makes a nil-deref a matter of luck.

**2. A host call marks its thread `blocked` for its duration.** `t.enterBlocked()` before the embedder's
function and `t.leaveBlocked()` after — the same pair `memory.atomic.wait` uses, and this is what makes the
three safepoint rows true rather than hoped for. ADR 0067's predicate is `blocked == callers`: during a
host call the guest frame is still counted as a caller, so without the mark a thread parked in an
embedder's `select` reads as **running guest code** and `Stop` waits out its whole deadline. With it, SP-2
counts the parked thread as arrived *without waking it*, which is SP-4's requirement in the same clause.
`enterBlocked` also parks first when a stop is already in flight, so a host call cannot begin during a
stop — the honest reading of SP-1, and the reason the mark is not merely a counter update.

**3. `Close` is terminal, cancels per thread, and waits on the host calls rather than on a deadline.**
`thread.ctx`/`thread.cancel` are created once per thread, so cancellation is per thread as ruled and
costs no per-call allocation. `Close` marks the world closed, cancels every member, and returns when every
in-flight host call has returned — waited on a signal, never a timer, because *a duration is not a
completion signal*. There is no `Resume` after it: a `Close`d world refuses `Spawn` and refuses to begin a
host call, and the refusals are named errors rather than silence.

**What A pays, stated as a cost and not as a footnote:** one `[]Value` allocation per call, plus boxing
every argument and result through `Value`. `invokeIndex` already pays exactly this at the outer boundary, so
the shape is not new, and no benchmark in this tree yet has a host call to measure. That is a
pre-registration below, not a claim here.

**Named engine limit: an embedder's `binary.FuncType` may not name type indices.** A host function has no
module, so it has no type section, and a `FuncType` whose parameter or result is a type-index-bearing ref
type (`(ref $t)`) has nothing to resolve the index against — `importTypeMismatch`'s func arm compares
through `internal/validate`'s relation over *two* modules' type spaces. So the host arm accepts abstract
heap types only and refuses the rest **at link time with its own error**, rather than resolving an index
against whichever module happens to be at hand. Widening it is the component model's business (§6), and the
refusal is what keeps this from pre-deciding it.

## Consequences

- **Five litmus rows become buildable**, and their `Blocked by` re-points from #554 to nothing. Building
  them is #10's own slice and stays parked past what this needs; this ADR unblocks them without claiming
  them.
- **Two new crossings per host call**, `leaveGuest`/`enterGuest`, unannotated and therefore
  sequentially consistent under B-MM-4's convention. The site is **outside the derived control's domain**:
  `TestEveryStackCreationSiteCrossesTheBoundary` parses non-test `stack{…}` literals, and `callHost`
  creates none — it runs *inside* the caller's stack, which is the whole of option A. So the pairing gets a
  hand-written row in `TestEveryBoundaryCrossingIsPaired`, whose rows are enumerated rather than derived,
  and it sits beside the rows it has to be compared against. Said plainly because a derived population that
  silently excludes the new site is how a control reads as covering it. (This bullet named the pairing
  oracle as the parser in the drafted text; the parsing is the sibling's.)
- **`Caller` has no entry point, and that is H-2's enforcement.** No `Invoke`, no `Instance` accessor that
  reaches one. An embedder who wants reentrancy has to be given it, which is §6's business.
- **`Value` boxing is the ABI**, so C's identity widening stays additive: a host function gaining an
  identity later adds a field to something that does not exist yet at the boundary.
- **A `Close`d instance is a new terminal state** in a type that previously had none, and every public
  method that would touch guest state after it must say what it does. `Stop`/`Resume` are unchanged and
  orthogonal: SP-4 governs the pause and H-3 the teardown, which is Scott's own correction on the #646
  review, recorded at #602.

## Amended by the implementation

Two sentences of this ADR's choice 3 were written from the wrong path and are corrected here rather than
silently, because *an ADR is testimony* and the finding is the useful part.

- **The context is created in `world.addLocked`, not in `newThread`** — choice 3 said `newThread` and this
  file now says "once per thread". `newThread` is `Spawn`'s only path; the *ordinary* host call runs on
  `in.host`, which `link.go` builds by literal and hands to `register`, so a context created in `newThread`
  would be nil on exactly the thread every host call in the tree runs on and `Close` would cancel nothing
  while looking correct. `addLocked` is already documented as the one place membership and `t.w` are
  established, which is the same invariant a cancellable thread needs. **The shape: a creation site named
  from the path you were reading, not from the path the subject travels.**
- **The release channel is closed outside the mutex, not under it.** The first draft closed it inside
  `endHostCall`'s critical section and `TestNoEngineLockIsHeldAcrossAChannelOperation` failed it on sight —
  §4 B-MM-3 forbids holding an engine-internal lock across a channel operation, and a `close` on a release
  channel is exactly the resume that rule is about. The repair is that control's own prescription: claim and
  nil the channel under the lock, close it after. Nil'ing under the lock is what makes the close single, so
  the correctness argument is unchanged and only its *location* moved. Recorded because the rule was
  satisfied by a control rather than by this ADR's reasoning, and the next mechanism that waits on a signal
  will meet the same rule.

## Pre-registration

- **No measurable change on any `make bench` arm.** Every arm in the tree runs guest code with no host
  import, and the mark, the context and the `Close` bookkeeping are all off those paths. The rollback if an
  arm moves: the `blocked` mark is the only new work on a *guest* path and it is one mutex acquisition per
  host call, so a regression on a no-host-call arm means the seam leaked into `resolveCall`'s fast path,
  which is a bug in this slice rather than a cost to accept.
- **The cost of a host call itself is not pre-registered** and no figure is offered for it, for ADR 0068's
  reason: there is no baseline to compare against, since the operation did not exist. The first
  measurement will be a `hostbench` arm's own registration.
