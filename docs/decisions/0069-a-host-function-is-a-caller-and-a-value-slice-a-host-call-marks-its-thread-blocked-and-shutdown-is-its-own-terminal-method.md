<!-- Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0 -->

# 0069 — A host function is a `Caller` and a `[]Value`, a host call marks its thread blocked, and shutdown is its own terminal method

Date: 2026-09-05 · Status: **accepted**, **stamped by relay** — Scott, on the
[#651](https://github.com/scttfrdmn/burroughs/pull/651) review: *"ADR 0069 is stamped by relay, on the
same terms as 0050/0052/0060 — recorded citing the report that carried it, saying what the citation is
not."* The independence mechanism is his, stated on the #646 review: *"I reviewed each in the report that
landed it."* For this ADR that report is **PR 651**, which carried the option-A implementation and this
document to him together.

**What this citation is, and what it is not.** It resolves to a principal's approval and to the PR whose
report carried this document to him, which is what the `Status:` rule asks for. It does **not** resolve to
a GitHub review artifact: the review was a session turn and the recording is made by the actor who was
reviewed, so *durability is not independence* and every commit resting on it is `Ratio-Class: carried`.
Stated here rather than left for a reader to discover, because a forged provenance about the project's own
governance is worse than a wrong option.

**The line this replaces held the status open for exactly this event**, and it is worth recording that the
mechanism worked as written rather than being waived: *"A `Status:` field is a citation to an approval, and
this one holds open for a stamp on the report that carries it."* The report carried it, the stamp came back
on that report, and the citation now has a target. The implementation landed *before* the stamp, which is
the order the mechanism intends — the ADR held a status it could not yet cite while the code it describes
was reviewable.

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

## Amended by the #651 ruling — `Caller` reaches guest memory, by copy

This ADR's Consequences said `Caller` *"has no entry point"* and the implementation's own comment said there
was no guest-memory accessor, naming it as escalated public surface. Scott ruled it on the
[#651](https://github.com/scttfrdmn/burroughs/pull/651) review, and the ruling is additive to option A
rather than a change to it: *"`Caller` gets guest-memory access — but as copying accessors, not a view.
`Caller.Read(offset, n) ([]byte, error)` and `Caller.Write(offset, buf) error`."*

**H-2 is untouched, and the distinction is what makes the widening safe.** H-2 forbids *re-entering the
guest*; these accessors read and write bytes and call nothing. `Caller` still holds no `*Instance` — the new
field is a `*memory`, chosen for exactly the containment reason `tid` is a `ThreadID` — so the method that
would violate H-2 still does not exist.

**Why copying, in his words:** *"A retained slice would alias a memory that can grow and relocate — #575
and #622's exact subject — which is the soundness burden option B was rejected for. Exposing a view reopens
the story A was chosen to close."* The engine's own mechanism agrees: `memory.grow`'s second arm reallocates
and blits, so a slice handed out before it names an abandoned array afterwards, and a *write* through such a
slice lands in an image nothing will load again — a store silently lost, with no channel reporting it.
`noMove` exists because ADR 0051's atomics hold a raw pointer for one access; a slice an embedder holds for
as long as it likes is that hazard unbounded.

**The cost, and the escape hatch, both his:** *"Copying costs an allocation per access, the same trade A
already makes with boxed `[]Value`, so it's consistent rather than a new tax"* and *"A view stays addable
later as an additive fast path, exactly like C's identity and B's stack form."*

Three things the implementation decided under this ruling, recorded here because none of them is in its text:

- **Memory 0 of the *declaring* instance, not the running thread's.** The neighbouring world question has
  the opposite answer — cancellation follows the running agent — and a reader transferring one to the other
  gets a defect either way. An index space belongs to the module that wrote the import; a thread's world
  belongs to whoever may cancel it.
- **`ErrNoMemory` is not the only reason there is no memory**, so `memoryFor`'s two other reasons (an
  unsupplied import, §3; a declared memory that failed to allocate) are carried on the `Caller` and reported
  instead of being flattened. An error telling an embedder their module "defines and imports no memory" when
  it imports one nothing supplied is grave #36's shape.
- **The two accessors join ADR 0064's plain region**, which
  `TestNoGuestMemoryAccessSiteJoinsWithoutAClassification` demanded before it would go green again, and
  [0064's own amendment](0064-the-bulk-and-simd-region-stays-plain-and-is-confined-by-an-enumeration-a-control-asserts-because-the-guest-model-permits-the-tear.md)
  records why plain is right there — an atomic accessor would promise an embedder an atomicity the guest side
  cannot supply.

## Pre-registration

- **No measurable change on any `make bench` arm.** Every arm in the tree runs guest code with no host
  import, and the mark, the context and the `Close` bookkeeping are all off those paths. The rollback if an
  arm moves: the `blocked` mark is the only new work on a *guest* path and it is one mutex acquisition per
  host call, so a regression on a no-host-call arm means the seam leaked into `resolveCall`'s fast path,
  which is a bug in this slice rather than a cost to accept.
- **The cost of a host call itself is not pre-registered** and no figure is offered for it, for ADR 0068's
  reason: there is no baseline to compare against, since the operation did not exist. The first
  measurement will be a `hostbench` arm's own registration.

## Amendment 2026-09-10 — Option C lands: a host function is a first-class funcref

**A guest pulled the deferred capability forward, which is the trigger this decision named.** The p3
track (contract §6, component model + WASI) reached its first `wasi:cli/run` component — a Rust program
built by cargo-component, the third-party toolchain the product line targets. Its fused preview-1→2
adapter routes the wasi imports through an `$imports` **funcref trampoline table**: the lowered imports
are placed into a table with an `elem` segment and reached with `call_indirect`, not by name. So the
guest calls an embedder host function *indirectly*, and `funcRefTarget` refused it — the exact limit
this ADR deferred against ("call it by name instead"). A guest demanding a deferred capability is the
trigger 0069 was deferred *against*, not a reason to keep deferring; verified by running the component
(the write path traps at the trampoline, `stdout` still empty), not hypothesized. Ruled Option 1 by
Scott on the p3 C.2 stop-condition report: Option C comes forward **as its own Phase-1 engine
increment**, ahead of the component marshaling, so a defect in funcref identity is localized to the
engine and not entangled with the first live value-marshaling diff.

**What changes, and that it is additive.** Option C is the identity-carrying form C named: a host
function is a `funcref` value like any other. The representation needs no new field — a funcref is
already the pair `(ref.Addr = import index, ref.Inst = the instance whose import slot holds it)`, and
`funcRefTarget` already resolves that pair to the host extern via `importedFunc`; it merely stopped
there and refused. The widening is at the *dispatch* seam: `funcRefTarget` returns a `funcTarget`
(host-aware, the type `call` already dispatches), and `call_indirect`/`call_ref` dispatch its host arm
through `callHost` exactly as `call` does. `ref.func` of an imported host function already produces the
resolving pair, and a host funcref round-trips through `table.set`/`table.get` as an ordinary reference.
Nothing on `HostFunc`/`Extern`'s **signatures** changes — `HostExtern` is untouched; what changes is
that the reference a host function was always addressable by is now callable and storable. **p1 tests
unchanged**: the spec suite has no embedder host functions (its "imported functions" are other wasm
modules, resolved through `ext.owner`, which always worked), so no vector's verdict moves and no skip is
retired — this is why the exit is a new positive assertion rather than a board delta.

**The `call_indirect` type check for a host callee is structural equality, which is `match_deftype` for
it.** A host function's `binary.FuncType` may not name type indices (`hostTypeIsLinkable` refuses those
at link time), so it carries no GC subtyping and `match_deftype` reduces to structural equality of value
types — compared directly rather than through `validate.MatchDefType`, which needs a module and a type
index a host function has neither of.

**Named limits kept, stated not silent.** A GC `ref.cast`/`br_on_cast` to a concrete function type
still refuses a host callee (`castop.go`): the cast lattice names a type by a module type-index, which a
host function has none of. A `return_call_indirect`/`return_call_ref` **tail call** to a host function
refuses (`ErrUnsupportedOp`): a tail call replaces the current frame with the callee's, and a host
function builds no frame — the shape is real but no p3 path or spec vector reaches it, so it is a named
limit rather than modeled speculatively. Both are `gate:gc`/tail-call edges, off the p3 exit's path.

### Pre-registration (this increment)

- **Board unchanged on every lane**, for the reason above (no host functions in the suite). A moved
  vector is a bug in this slice: the host arm must not leak into the wasm-import resolution path.
- **No `make bench` arm moves** — every arm runs guest code with no host import, and the host dispatch
  is off those paths; the `funcRefTarget` change adds one branch on the indirect path only.
- **Exit is a positive assertion** (`internal/interp`): a host function placed in a table via
  `table.set` and read back via `table.get` is the same reference; called through `call_indirect` with a
  matching type it runs, and with a mismatched type it traps `indirect call type mismatch`; `ref.func`
  of an imported host function is callable via `call_ref`. `BURROUGHS_NO_SKIP=1` stays green (no skip
  was retired, so nothing new is forced into CI).
