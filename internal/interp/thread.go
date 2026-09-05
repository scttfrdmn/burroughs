// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"errors"
	"fmt"
	"runtime"
	"sync/atomic"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// ThreadID names a wasm thread. Contract §2's T-1 `tid`.
//
// Monotonic from 1, and 0 is deliberately not a valid id: a zero `ThreadID` is what a caller gets
// back alongside an error, and the one thing it must not be able to mean is "the first thread".
type ThreadID uint64

// thread is one wasm thread's execution context — decision [0050]'s chosen representation, and the
// object contract §§3–5's per-thread state will hang off.
//
// **Why this exists as its own type rather than as fields on `stack`.** `stack` is threaded through
// every function on the hot path already (`run`, `runFrame`, `enterFrame`, `invoke`), so putting the
// slot straight on it would have cost zero new parameters and three propagation sites — measurably
// the cheapest option, and the one 0050 rejects. `stack` is per-*invocation*; T-4 asks for
// per-*thread*, and §7's growable continuations will make those different by creating stacks inside
// one thread. A slot living on `stack` would then be per-continuation, and a fresh stack that forgot
// to copy it would hold a **zero value that looks like a legitimate first thread** — a plausible
// wrong answer rather than a crash. A `*thread` copies idempotently and a forgotten copy is nil.
//
// The larger reason is that §§3–5 need more per-thread state than a slot: SP-1's stop/epoch flag
// checked at back-edges and call sites, SP-2's "in a host call, therefore at a safepoint" bit, T-3's
// futex park token. Every one is per-thread and every one would be per-continuation on `stack`.
//
// # T-1's spawn is here, and the four preconditions it waited on are why this section is history
//
// `Spawn` is below, and it landed under [ADR 0068][0068] after being parked as **#554** from
// 2026-09-01. The parking is kept as a record rather than deleted because two graves were dug inside
// it and because *the shape of what survives names the bug*: three of the four preconditions its
// tripwire named were discharged elsewhere, and the fourth was parked by the principal whose queue it
// was.
//
// It was **withheld** because `TestNoEngineGoroutineLandsWithoutAPrincipalsRuling` fired on it. That
// control watches for a `go` statement in this package's non-test files, and it named what unparking
// had to answer: §4's boundary model has its mechanism and no litmus battery (**#10**),
// `memory.atomic.wait` cannot return 0 for woken (**#543**), `Spawn` shares the instance's globals and a
// **reference** global's `global.set` is still a plain write (**#573**), and the spawn walk's closure is
// smaller than the reachable set (**#575**). #543, #573 and #575 are closed — the last by [ADR
// 0058][0058], which dissolved the walk's question instead of widening it — and #10 is parked by
// Scott's order past what spawn needs. Deleting or re-pointing that control was a principal's call and
// not a test author's, and its name is what made the ruling the thing that discharges it: the control
// now asserts that no *further* `go` lands unruled, keyed by enclosing function.
//
// **The blocker changed three times without clearing, and this paragraph is where the first change
// was noticed too late** (grave **#561**; the second is grave **#576**, in the control's own name and
// message). It used to quote the control saying *"discharge #542"*, and to assert that all 67 atomics
// in `atomic.go` were plain read-then-write — true when written, falsified by [ADR 0051][0051], which
// made them sequentially-consistent word operations over the backing array. It then named **#557**,
// the tearing of memop.go's aligned plain accesses, and **#516**, §4's boundary edges; both are
// discharged, #557 by [ADR 0054][0054] and #516 by [ADR 0052][0052]. The watched *event* was unchanged
// through all of it, so `Spawn` was parked further along one chain rather than for a new reason, and
// the way the first of these was settled is the part worth keeping: a `go` statement injected into
// a scratch non-test file and the resulting FAIL read back. **A claim about what an instrument will
// permit is a forecast about a machine sitting in the tree.**
//
// **What the parked branch could not have known, and what therefore is not a rebase.** ADR 0067's
// `Stop` asks `blocked == callers`, and the branch predates it: a spawned thread that runs guest code
// without being counted as a caller reads as *at a safepoint* while it executes, which is #592 with a
// new site instead of a changed predicate. `runEntry` below pairs `enterCall`/`leaveCall` for exactly
// that reason. The branch is re-authored rather than replayed, and it is left intact at
// `refs/pull/554/head` because [ADR 0058][0058] cites that ref for a control that exists only there.
//
// **Scott ruled that order, and the ruling is what this comment records rather than the question it
// used to pose.** Option 1: discharge #542 first — **#542 → #516 → #10** — reversing the in-session
// ordering that had put spawn ahead of §4's model. An override was refused on the grounds that *"once
// a second thread exists, plain Go operations on shared interpreter state are data races — undefined
// behaviour, not merely wrong values."* #542 is discharged. #554 carries the measurement that made
// that refusal concrete — two threads doing 2000 atomic adds each on one cell landing on 3392 rather
// than 4000, with `-race` naming `atomic.go`'s read and `memory.go`'s write — and the repaired engine
// returns 4000, which `TestAtomicRmwIsNotObservablyTornAcrossThreads` asserts and keeps asserting.
//
// **A PR and not a bare commit, deliberately.** A PR number resolves under `citecheck` and GitHub
// retains the diff and `refs/pull/N/head` independently of the branch; the commit SHA this comment
// used to cite could not be checked by anything in the tree, so it would have read as a valid
// citation forever while pointing at a pruned object.
//
// Two things deliberately not done, because both are the shape of arguing with the instrument: the
// file is not added to an exception list, and `Spawn` is not moved to a sibling package where the
// `go` statement would sit outside the control's domain. *An exemption inherits none of the
// trigger's lessons.*
//
// [0050]: ../../docs/decisions/0050-the-per-thread-context-is-its-own-object-reached-by-one-pointer-on-stack-because-3-and-5-need-more-per-thread-state-than-a-slot.md
// [0051]: ../../docs/decisions/0051-the-atomics-become-sequentially-consistent-word-operations-over-the-backing-array-because-the-proposal-fixes-the-ordering-and-leaves-only-the-mechanism.md
// [0058]: ../../docs/decisions/0058-the-memory-image-is-published-through-an-atomic-pointer-because-reachability-is-not-a-spawn-time-property.md
// [0068]: ../../docs/decisions/0068-spawn-drops-0056s-walk-and-refuses-the-two-cases-a-per-instance-world-cannot-express-because-a-thread-belongs-to-exactly-one-stop.md
// [0052]: ../../docs/decisions/0052-the-4-boundary-edge-is-one-package-level-sequentially-consistent-counter-because-a-shared-memory-spans-instances.md
// [ADR 0059]: ../../docs/decisions/0059-the-safepoint-poll-is-guarded-at-the-pc-assignment-because-a-back-edge-is-a-runtime-comparison-and-straight-line-code-pays-nothing.md
// [0054]: ../../docs/decisions/0054-every-aligned-guest-access-becomes-atomic-on-the-address-already-resolved-because-a-scoped-gate-is-unavailable-rather-than-unwritten.md
type thread struct {
	// id is T-1's tid, assigned once at creation and never written again.
	id ThreadID

	// slot is T-4: the per-thread slot, the `g` register analog. Read as `st.t.slot` — two
	// dereferences from a pointer the hot path already holds in a register.
	//
	// **Nothing reads it yet, and 0050 declines to benchmark the read for that reason.** The first
	// reader is #515's safepoint check. Comparing `st.t.slot` against `st.slot` today would compare
	// two fields neither of which is read, which is an analytic zero: it could not have come out any
	// other way. What 0050 pre-registers instead is the cost of *carrying* the pointer, which is
	// falsifiable on layout and allocation alone — and which passed, at a worst row of +0.94%.
	//
	// No `slotOf`/`setSlot` accessors: they would be two functions nothing calls, which `deadcode`
	// refuses and which would in any case guess at the shape #515's reader wants. The guest-visible
	// surface for T-4 — the host function a module calls to read its own slot — is public API and
	// does not ride a representation PR.
	//
	// **The suppression is weaker than the one it copies, and says so.** `stack.refs` carried this
	// exact directive under 0002 and was at least *allocated*; this field is neither read nor
	// written, so the pin is purely about where the slot lives. It is still the right pin: 0050
	// exists to decide that, and landing the object without the slot would hand the placement to
	// #515's PR — *"moving all of them later, in the PR that can least afford a representation
	// change"*, which is option C's own argument against option B. Deleted, not kept, when the
	// reader arrives: a directive must not outlive its subject.
	//
	// **That retirement condition has now been falsified by the work it named, and the directive
	// stays.** This field's forecast was that *"the first reader is #515's safepoint check"*. #515's
	// check landed ([ADR 0059]) and reads `stopReq` below, not `slot` — a stop request is engine
	// state and T-4's slot is guest-visible state, and nothing about polling one requires reading the
	// other. So the suppression's subject is unchanged and deleting it here would be a directive
	// removed on a coincidence of issue numbers. What *is* corrected is the sentence: `slot`'s first
	// reader is the host function a module calls to read its own slot, which is public API and still
	// unwritten. A retirement condition that names a *slice* rather than a *reader* is the kind of
	// citation that reads as satisfied the moment that slice lands, which is why the condition below
	// now names the reader.
	//
	//nolint:unused // pinned by 0050 before its first consumer; retired by T-4's guest-visible slot accessor
	slot uint64

	// stopReq is contract §3 SP-1's epoch/stop flag: set by `Stop` on another goroutine, read by
	// `poll` at every back-edge and call site. [ADR 0059]'s mechanism.
	//
	// **Atomic because of who writes it, not because of contention.** The write happens once per stop
	// round and the read happens per back-edge, so there is no contention to speak of; what makes a
	// plain `bool` wrong is that `Stop` runs on a different goroutine, and a non-atomic read of a word
	// another goroutine writes is a data race — undefined behaviour, not a slightly-stale answer.
	stopReq atomic.Bool

	// blocked is contract §3 SP-2's mark: this thread is suspended and therefore *at a safepoint* —
	// decision 0060's third choice.
	//
	// **The mark names no reason, and the omission is the design.** SP-2 is one clause over two
	// consumers — *"a thread blocked in a host call **or** in `memory.atomic.wait`"* — and
	// `enterBlocked`/`leaveBlocked` (safepoint.go) mention neither: they take a `*thread`, move this
	// count under `world.mu`, and park if a stop is in flight. `memory.atomic.wait` is merely the
	// consumer that exists, not the thing being counted. This sentence said *"suspended in
	// `memory.atomic.wait`"* until #602's scoping — a true description of a population of size one
	// wearing the grammar of a constraint on it, which is **grave #645**. The cost is a reader who
	// wants a blocking host call concluding they must build a second mechanism instead of reusing
	// this one, and that reading happened once before the repair. A blocking host call wraps its call
	// in the same pair and inherits SP-2 and SP-4 whole.
	//
	// **Guarded by `world.mu`, and deliberately not an atomic.** Both writers (`enterBlocked`,
	// `leaveBlocked`) and the only reader (`Stop`) hold that mutex, and the whole point is that the
	// transition and the count cannot interleave: an atomic read would let `Stop` observe "not
	// blocked" from a thread that is one instruction from blocking, and then wait for an arrival that
	// will never come. `stopReq` above is atomic for the opposite reason — its reader is the hot path
	// and must not take a lock.
	//
	// **A count and not a flag, because a `thread` is per *instance* and a caller is per *call*.**
	// `link.go` registers exactly one thread per instance, and an embedder may drive N concurrent
	// `Invoke` calls through it — the engine's own `TestAtomicRmwIsNotObservablyTornAcrossThreads`
	// does, with N=2. A flag would be cleared by the first of those to leave a wait while the others
	// were still in one, which is the mark saying "running" about a thread that is not. The count is
	// exact for that shape; what it cannot fix alone is a *mixed* one, where one caller is suspended
	// and another is executing guest code on the same `thread` — #592, and `callers` below is the
	// other half of that repair.
	blocked int

	// callers is how many callers are currently executing on this thread, moved by
	// `enterCall`/`leaveCall` (safepoint.go) around each `Invoke`'s guest execution. Decision
	// [0067]'s mechanism, and the second half of the mark above.
	//
	// **It exists because `blocked` is a count of callers and `Stop` was reading it as a fact about
	// the thread.** The predicate was `blocked > 0`, which is *"some caller here is suspended"* asked
	// in place of *"no caller here is running"*. Those coincide only while a thread has at most one
	// caller, and one `thread` per instance against an ungated exported `Invoke` means it does not.
	// With A suspended in a wait and B in a loop, `blocked` is 1, the old predicate answered "at a
	// safepoint", and `Stop` returned `nil` while B ran — contract §3 SP-2 failing on its own terms,
	// since a thread reported at a safepoint *"cannot touch guest memory until it re-enters through a
	// boundary that observes the stop."* `Stop` now asks `blocked == callers`.
	//
	// **`blocked <= callers` holds by construction, and the invariant is load-bearing rather than
	// incidental**, which is why `Stop` asserts it rather than trusting it: `enterBlocked` is reached
	// only from `futex.go`'s `wait`, which is reached only from guest code, which runs only inside a
	// call that has already incremented this field. A `blocked` above `callers` would make an equality
	// predicate unsatisfiable and hang every `Stop` — a worse failure than the one being fixed, and
	// silent.
	//
	// **Guarded by `world.mu` for `blocked`'s reason and not for a weaker version of it.** An atomic
	// count would put this field outside the critical section that makes 0060's three-way race a
	// two-way one, and would need its own ordering argument against a `Stop` that reads both fields.
	// 0067 records that as option B′, rejected: the failure it re-opens is the one 0060 names as the
	// outcome that must not exist.
	//
	// **Instantiate-time guest execution is not wrapped, and is not a gap.** `build`'s start function
	// and `runConst` run before `InstantiateLinked` returns, so no external reference to the instance
	// exists and no `Stop` can be in flight against it. That is the only guest code outside `Invoke`.
	//
	// [0067]: ../../docs/decisions/0067-a-caller-count-joins-the-blocked-mark-because-sp-2s-predicate-is-about-callers-and-a-thread-is-not-one.md
	callers int

	// reported records that this thread's arrival has been sent for the round `world.resume` names,
	// so that N callers sharing one thread produce one arrival and not N.
	//
	// **This is a bug fix to #591's arrival protocol and not a new requirement.** `parkAtSafepoint`
	// argued that its send could not block because *"the buffer is `len(w.members)` and each thread
	// sends once per round"* — and each *thread* does, while each *caller* also sends, so three
	// concurrent `Invoke`s and one `Stop` filled a one-slot buffer and left the third caller blocked
	// on a send forever, with `Resume` unable to free it. See `parkAtSafepoint`.
	reported bool

	// done closes when the thread has terminated and `err` is final. Nil for the host thread, which
	// does not terminate.
	//
	// **This is not join, and the distinction is contract §10.3's.** T-5 requires exit, join and
	// detach *defined in the contract* (**#12**, open), so nothing here decides them: there is no
	// ownership, no detach, no reaping, and no ordering promised between two threads' terminations.
	// What the channel buys is the one fact the engine needs internally — that a thread has stopped —
	// plus a happens-before edge that makes `err` readable without a race. A `Join` method would be
	// answering the open question in the channel that gets no review.
	done chan struct{}

	// err is the thread's terminal error, written once by the thread itself before `done` closes.
	//
	// **Stored rather than surfaced, and stored rather than dropped.** How a thread's failure becomes
	// visible to a host *is* exit semantics, so it is #12's to answer and no accessor exists.
	// Swallowing it instead would make a trapping thread indistinguishable from one that returned,
	// which is a wrong answer rather than a deferred question.
	err error

	// w is the stop-the-world state this thread participates in, set by `world.register` at creation.
	//
	// Nil is legal and means "no world", which is any `thread` this package's own tests build by
	// literal. `poll` reads `stopReq` before it ever reaches this field, so a nil `w` costs the hot
	// path nothing: an unregistered thread can never have `stopReq` set, because the only writer is
	// the `Stop` that walks a world's members.
	w *world
}

// String names a thread in an error or a test failure, so a message about one says which.
func (t *thread) String() string { return fmt.Sprintf("thread %d", t.id) }

var (
	// ErrNotShared refuses a spawn on an instance with no shared memory to share.
	//
	// **This is the threads gate, and it is a gate by construction rather than a second flag.** A
	// `shared` limits flag only decodes with `Features.Threads` on (`decodeLimits`), so an instance
	// holding a shared memory *is* proof the threads gate was set when its module was decoded. A
	// duplicate boolean on the instance could disagree with the decoder; this cannot. Behaviour 4 and
	// contract §9 want the capability behind the proposal's gate, and the memory is the gate's own
	// witness.
	ErrNotShared = errors.New("burroughs: spawn needs a shared memory")

	// ErrThreadEntry refuses an entry function whose type is not T-1's `(entry_func, arg)` shape.
	ErrThreadEntry = errors.New("burroughs: thread entry must take one i32 and return nothing")

	// ErrForeignEntry refuses an entry function that resolves into a different instance — decision
	// [0068]'s first named limit.
	//
	// **A `thread` belongs to exactly one world, and this is the case where "which one" has two
	// answers.** `resolveCall` follows import chains to the instance that *defined* the body, and
	// `runEntry` must run it against that instance's state; `thread.w` is one field, so either the
	// spawner's `Stop` or the entry instance's `Stop` would fail to reach the new thread. Both
	// alternatives are a `Stop` returning nil while guest code runs, which is #592's failure with a
	// new cause. Refusing declines to answer instead of answering half, and widening it is SP-4's
	// dynamic-membership work rather than this function's.
	ErrForeignEntry = errors.New("burroughs: thread entry must be defined in the instance it is spawned from")

	// ErrStopInProgress refuses a spawn while a stop is in flight — decision [0068]'s second named
	// limit, and the reason is `Stop`'s arrival channel rather than tidiness.
	//
	// `Stop` sizes `world.arrived` to the membership it observed, and every member may send once. A
	// member admitted mid-round is an (N+1)th potential sender into N slots, and a thread that blocks
	// on that send is *at a safepoint and unable to say so* — the one deadlock this protocol can have
	// (`world.arrived`). Unreachable from a guest that spawns while running, since a running guest
	// means no round is in flight on its own thread; reachable only from an embedder spawning between
	// `Stop` and `Resume`.
	ErrStopInProgress = errors.New("burroughs: spawn refused while a stop is in progress")
)

// newThread makes the instance's next thread **and admits it to the world in the same step**, so that
// *registration is where creation is* stays an invariant of one function rather than an agreement
// between two — link.go's `in.host` obeys the same rule, and a thread registered later than its first
// instruction is a thread a stop can silently fail to reach.
//
// The id counter is atomic because T-2 forbids a main-thread special case: any thread may spawn, so two
// spawns can race for an id. It is bumped *after* `admit` refuses, so a refused spawn consumes no id.
func (in *Instance) newThread() (*thread, error) {
	t := &thread{done: make(chan struct{})}
	if err := in.world.admit(t); err != nil {
		return nil, err
	}
	t.id = ThreadID(in.nextTID.Add(1))
	return t, nil
}

// hasSharedMemory reports whether this instance can reach a shared memory.
//
// It scans `mems` rather than the module's declarations on purpose: the index space reserves a slot
// per import (see `Instance.mems`), so an *imported* shared memory is only reachable once a supplier
// filled it. The question spawn needs answered is "is there a shared memory this instance can
// actually reach", not "did the module mention one" — and those differ for exactly the module that
// imports a memory nobody supplied.
func (in *Instance) hasSharedMemory() bool {
	for _, m := range in.mems {
		if m != nil && m.limits.Shared {
			return true
		}
	}
	return false
}

// Spawn is contract §2's T-1: `spawn(entry_func, arg, stack_hint) → tid`, a wasm thread backed 1:1
// by an OS thread, sharing the module's shared linear memory.
//
// **1:1 with an OS thread, in pure Go, is a goroutine that locks its thread and never unlocks it.**
// `runtime.LockOSThread` binds the goroutine to the thread it is running on, and a goroutine that
// exits while still locked *terminates* that thread — which is the lifetime T-1 asks for
// (*"this is `newosproc`, not a Worker with a message port"*). There is no unlock, deliberately:
// unlocking would return the thread to the pool and make the binding 1:N over the thread's life.
//
// **Spawn does not make everything it reaches coherent, and this comment is not the place a reader
// should have to discover that.** What is closed: the backing array may move under a running thread
// without memory unsafety ([ADR 0058][0058]), the 67 atomics are sequentially consistent ([ADR
// 0051][0051]), aligned plain accesses do not tear ([ADR 0054][0054]), all three global arms are
// atomic (#573), the table and segment headers are published images (#622), and `memory.atomic.wait`
// suspends and wakes (#543). What is **open and reachable from here**: an unshared memory in a
// spawned instance, grown by one thread while another holds an older image, loses the writes made
// through that image — [0058]'s coherence residual, **#586**, which needs §4 (**#10**) to say what is
// permitted before code can be right about it. No *shared* memory is in that population, because
// `allocate` reserves and therefore marks every one of them and a marked memory never reaches
// `grow`'s relocating arm.
//
// The lifecycle stays open: T-5's exit/join/detach are contract §10.3 (**#12**), so a caller gets a
// tid and no way to wait on it. See `thread.done` for why the internal channel is not that API, and
// note that a terminated thread stays in `world.members` — reaping is #12's too.
func (in *Instance) Spawn(entry uint32, arg int32, stackHint int) (ThreadID, error) {
	t, err := in.spawn(entry, arg, stackHint)
	if err != nil {
		return 0, err
	}
	return t.id, nil
}

// spawn is Spawn's mechanism, and the split is where the lifecycle gap lives.
//
// `Spawn` drops the `*thread` and hands back a bare id **because a handle is the lifecycle API**:
// anything a caller could do with the object — wait, join, detach, read the terminal error — is T-5,
// contract §10.3, **#12**. Returning it would answer that in the signature. So the object stays
// inside the package, where the engine's own code and this package's tests can observe that a thread
// ran, and the exported boundary offers nothing #12 has not decided.
func (in *Instance) spawn(entry uint32, arg int32, stackHint int) (*thread, error) {
	if !in.hasSharedMemory() {
		return nil, fmt.Errorf("%w: this instance reaches no shared memory, so a spawned thread "+
			"would share nothing with its parent", ErrNotShared)
	}
	target, fn, ft, err := in.resolveCall(entry)
	if err != nil {
		return nil, err
	}
	// Decision [0068]'s first named limit. See `ErrForeignEntry` for why this is refused rather than
	// resolved; the check is `target != in` and not a nil test, because `resolveCall` resolves a
	// re-exported import transitively and returns the instance that owns the body.
	if target != in {
		return nil, fmt.Errorf("%w: function %d resolves into another instance, whose `Stop` and this "+
			"one cannot both reach the new thread (SP-4)", ErrForeignEntry, entry)
	}
	// T-1 fixes the entry shape at one i32 argument and no results, so the check is exact in both
	// directions rather than a minimum: a function returning a value would leave it on a stack
	// nothing will ever pop, and the arity debt (#9) would report it as a wrong answer later,
	// somewhere with no thread in the message.
	if len(ft.Params) != 1 || ft.Params[0] != binary.I32 || len(ft.Results) != 0 {
		return nil, fmt.Errorf("%w: function %d takes %v and returns %v",
			ErrThreadEntry, entry, ft.Params, ft.Results)
	}

	// **Creation is after every refusal, and admission is inside creation.** A `Spawn` that returns an
	// error leaves the instance exactly as it found it — no id consumed, no member added to a world
	// whose `Stop` would then wait for it. ADR 0056's walk used to sit here on the same reasoning
	// about a mark it could not undo; the walk is deleted (decision [0068]) and the placement rule it
	// was the reason for is now this.
	t, err := in.newThread()
	if err != nil {
		return nil, err
	}
	// **Registration has already happened, and that ordering is the whole soundness argument for the
	// window this `go` opens.** Between `admit` returning and the goroutine's first guest instruction,
	// a `Stop` can begin: it sets `stopReq` on this thread because it is already a member, and counts
	// it as arrived on `blocked == callers` (both zero). That count is *accurate in effect* rather than
	// by the predicate — the goroutine reaches `enterFrame`, polls, and parks before executing one
	// guest instruction — and the distinction is written down because a reader who checks the
	// predicate alone will read it as a hole.
	go func() {
		runtime.LockOSThread()
		// The error is assigned before the deferred close runs, so a reader that has observed the
		// close has observed the write — the channel supplies the happens-before edge, and this is
		// the only cross-thread read of `err`.
		defer close(t.done)
		t.err = in.runEntry(t, fn, ft, arg, stackHint)
	}()
	return t, nil
}

// runEntry runs a thread's entry function on that thread's own stack.
//
// **Through `invoke` rather than building the frame here, and the reason is a grave.** `buildFrame`
// owns the frame ceiling check, the reverse-order parameter pop, decision 0024's v128 two-slot
// conversion and grave #246's null fill for reference locals — *"two callers, one place that knows
// how a frame is built"*, and a third copy here would be grave #105's shape a third time with those
// four facts as the ones to re-derive wrongly. Pushing the argument and calling `invoke` makes a
// thread's entry an ordinary call whose stack happens to be new, which is also what it is.
//
// `depth` starts at 0 like the start function's call and `Invoke`'s, so the entry frame is depth 1:
// a thread gets its own full call budget rather than inheriting its spawner's remaining depth, which
// is the only reading 1:1-with-an-OS-thread supports.
//
// **`stackHint` presizes the value stack, and that is a real use rather than a parameter accepted
// and ignored.** Go's goroutine stacks grow on demand, so T-1's hint has no OS-stack analog to spend
// it on; the closest thing the engine allocates per thread is the operand stack, whose sizing
// `invokeIndex` derives from the body length. The hint becomes a floor on that — a caller who knows
// its guest is deep pays one allocation instead of several regrows, and a caller passing 0 gets
// exactly `invokeIndex`'s behaviour, including its stated v128 imprecision.
//
// This is stack creation site 4 of 4, and the only one that does not hand over `&in.host`: a spawned
// thread runs on its own. `TestEveryStackCreationSiteCarriesAThread` partitions the sites on exactly
// that distinction rather than listing them.
func (in *Instance) runEntry(t *thread, fn *binary.Func, ft *binary.FuncType, arg int32, stackHint int) error {
	// §4 B-MM-1, at the enclosing function of the `stack` literal below, same as the other three
	// sites (`boundary.go`, decision 0052, #516). **This is the site where the edge stops being
	// bookkeeping.** At the other three the host and the guest are the same thread, so the acquire
	// and release order a thread against itself and the crossing is recorded rather than needed.
	// Here the crossing is the *only* thing ordering what the spawner wrote before `Spawn` against
	// what this thread reads first — B-MM-1's message-passing case is exactly this pair of edges
	// observed from two threads, so the site the control named is also the site the clause is about.
	enterGuest()
	defer leaveGuest()

	st := &stack{
		t:   t,
		num: make([]uint64, 0, max(stackHint, len(fn.Body))),
	}
	// T-1's single i32 argument, as the operand `buildFrame` will pop into local 0. Through
	// `pushI32` rather than written into the frame directly, so a negative arg gets the same
	// `uint64(uint32(...))` widening it would through any other boundary.
	st.pushI32(arg)
	// **§3 SP-2's denominator, and the second site to have one** — decision 0067, and without it this
	// thread would run guest code with `blocked == callers == 0`, which `Stop` reads as *at a
	// safepoint*. That is #592's failure arriving through a new site rather than a changed predicate,
	// and it is the single thing the parked branch could not have known: it predates 0067.
	//
	// A plain call rather than a `defer`, on `invokeIndex`'s measurement — a second `defer` in a
	// function that already has an open-coded one takes both off that path, at 25–29 ns/call. The one
	// error path below is `invoke`'s return, which this already covers by uncounting after it.
	t.enterCall()
	err := in.invoke(fn, ft, st, 0)
	t.leaveCall()
	return err
}
