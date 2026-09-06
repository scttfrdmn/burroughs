// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"context"
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

	// exitReq is contract §2 T-5.4's terminal mark: `Instance.Close` has asked this thread to end, and
	// its next safepoint must not resume it. [ADR 0071]'s row 4.
	//
	// **Read only inside `parkAtSafepoint`, never by `poll`, and that placement is the whole cost
	// argument.** A thread reaches `parkAtSafepoint` only because `stopReq` was already set, so the fast
	// path still loads exactly one atomic and this field is read on a path taken once per thread per
	// lifetime. `Close` sets this one *and* `stopReq`, in that order, so the flag that steers is always
	// visible by the time the flag that diverts is observed.
	//
	// **Atomic for `stopReq`'s reason and not a weaker version of it**: the writer is `Close` on another
	// goroutine, and `parkAtSafepoint` reads it outside `world.mu` — before taking the lock, and again
	// after the release, where holding `mu` across the receive is B-MM-3's own hazard.
	//
	// **Terminal, so nothing clears it.** `Resume` clears `stopReq` on every live thread; it cannot clear
	// this, and it cannot revive a closed world either, because `Close` nils `w.resume` and a nil
	// `w.resume` makes `Resume` a no-op. A thread marked here ends.
	//
	// [ADR 0071]: ../../docs/decisions/0071-t-5-is-live-only-membership-a-bounded-status-record-a-fault-in-two-channels-and-a-sentinel-panic-for-the-terminal-unwind.md
	exitReq atomic.Bool

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

	// done closes when the thread has terminated, it has been retired, and its record in `world.exited`
	// is final. Nil for the instantiation thread, which does not terminate — and `quiescentLocked` reads
	// that nil as *"this one is not a spawned thread"*, so it is load-bearing rather than decorative.
	//
	// **This is now join's mechanism, where it used to be the reason there was none.** The sentence here
	// said *"This is not join, and the distinction is contract §10.3's"*, on the ground that T-5's
	// exit/join/detach were undefined and *"a `Join` method would be answering the open question in the
	// channel that gets no review."* §2 T-5 is amended and [ADR 0071] is the review, so the ground is
	// gone: `Instance.Join` waits on this channel. Replaced rather than annotated, because a comment that
	// tells the next reader a settled question is open is the foreclosing-words shape.
	//
	// **The close is still not the record**, which is the part of the old reading that survives. What a
	// joiner reads is `world.exited`, written by `retire` *before* this closes; the channel supplies only
	// the happens-before edge and the wake. That ordering is why a join cannot observe a finished thread
	// with no status.
	done chan struct{}

	// err is the thread's terminal error, written once by the thread itself before `retire` copies it
	// into `world.exited` and `done` closes.
	//
	// **Retained as the writer's own copy, and it is the record in `world.exited` that a `Join` answers
	// from.** The two exist for different lifetimes: this field lives as long as the `thread`, and the
	// record is bounded — *"consumed by a join, or dropped at `Close`"* (T-5.2) — so a joined thread's
	// status leaves the world while the object that carried it is already unreachable.
	//
	// The paragraph here used to say *"How a thread's failure becomes visible to a host *is* exit
	// semantics, so it is #12's to answer and no accessor exists."* It is answered: T-5.3's two channels
	// are `Instance.Join` for this thread's status and `Instance.Fault` for the instance's first trap.
	err error

	// w is the stop-the-world state this thread participates in, set by `world.register` at creation.
	//
	// Nil is legal and means "no world", which is any `thread` this package's own tests build by
	// literal. `poll` reads `stopReq` before it ever reaches this field, so a nil `w` costs the hot
	// path nothing: an unregistered thread can never have `stopReq` set, because the only writers are
	// `Stop` and `Close`, and both walk a world's `live` set. (`Close` joined that sentence with T-5.4's
	// terminal mark, and the set it names was `members` until row 1 of [ADR 0071] made it live-only.)
	w *world

	// ctx/cancel are contract §5 H-3's cancellation channel: what a host function running on this
	// thread sees through `Caller.Context`, and what `Instance.Close` cancels. [ADR 0069][0069]'s
	// third choice.
	//
	// **Per thread and not per call, which is the ruled shape** — Scott's second sub-choice on the
	// #647 review, *"created once per thread so it is not a per-call allocation"* — so a host call
	// costs no `context.WithCancel` and a `Caller` is one small struct.
	//
	// **Established in `world.addLocked` and not in `newThread`, and the difference is load-bearing.**
	// `newThread` is `Spawn`'s alone. The *ordinary* host call — an embedder `Invoke`s and the guest
	// calls a host import — runs on `in.host`, which link.go builds by literal and hands to
	// `register`. A context created in `newThread` would be nil on exactly the thread every host call
	// in the tree today runs on, so `Close` would cancel nothing while looking correct. `addLocked` is
	// documented as *"the one place membership and `t.w` are established, so the invariant … cannot be
	// half-kept by one of two callers"*, and a cancellable thread needs that same invariant.
	//
	// **Nil is legal, for `w`'s reason and with the same consequence.** A `thread` built by literal in
	// this package's tests has neither, so `context()` answers `context.Background()` — a context that
	// is never `Done` — and `cancelCtx` is a no-op. That is the honest reading: a thread no world
	// holds has no `Close` that could cancel it.
	//
	// [0069]: ../../docs/decisions/0069-a-host-function-is-a-caller-and-a-value-slice-a-host-call-marks-its-thread-blocked-and-shutdown-is-its-own-terminal-method.md
	ctx    context.Context
	cancel context.CancelFunc
}

// context is what `Caller.Context` hands an embedder, nil-safe on the receiver and on the field.
//
// **`context.Background()` rather than a nil `context.Context`, because a nil interface is not a
// context and every method on it panics.** An embedder calling `c.Context().Done()` on a thread built
// by literal would take the engine down inside its own host function — a crash in the embedder's frame
// blamed on the embedder. A background context is the truthful answer instead: this thread has no
// shutdown signal, because nothing holds it that could shut it down.
func (t *thread) context() context.Context {
	if t == nil || t.ctx == nil {
		return context.Background()
	}
	return t.ctx
}

// cancelCtx cancels this thread's context, nil-safe both ways. `Close`'s per-member call.
func (t *thread) cancelCtx() {
	if t == nil || t.cancel == nil {
		return
	}
	t.cancel()
}

// world is the world holding this thread, or nil for a thread no world admitted.
//
// # A host call belongs to the running thread's world, not to the instance that declared the import
//
// The two are the same instance for every single-module host call and they come apart the moment a
// module imports a *defined* function whose body calls a host import: the guest entry is instance `A`,
// the host import belongs to instance `B`, and the thread is `A`'s. `callHost` runs as a method on `B`
// — the declared result types are `B`'s, so `B`'s module is what a type check must resolve against —
// and takes its **world** from here instead.
//
// **Because the counter and the cancellation must have the same subject.** `Close` cancels the contexts
// of *its own members* and then waits for its own `hostCalls` to reach zero. Count the call in `B`'s
// world and `B.Close()` waits for a call whose thread is not `B`'s to cancel, which nothing will ever
// do — a teardown that hangs on a thread it has no handle on, and the engine's fault rather than the
// non-cooperating embedder's that `Instance.Close` already documents. Counting it in the thread's world
// makes `A.Close()` the call that both cancels and waits, which is the pairing H-3 describes.
//
// The consequence, stated because it is a real limit and not a detail: **`B.Close()` does not wait for
// a host call of `B`'s that is running on `A`'s thread.** `B` declared the import; `A` owns the agent
// executing it. Closing the instance whose threads are running is what H-3's wait is about, and an
// embedder tearing down a linked graph closes the instances it drove `Invoke` on.
func (t *thread) world() *world {
	if t == nil {
		return nil
	}
	return t.w
}

// threadID is `t.id` for a possibly-nil thread. Zero for a nil one, which is the value `ThreadID`
// documents as *"the one thing it must not be able to mean is 'the first thread'"* — so a host function
// reading 0 out of `Caller.Thread` is being told there is no thread here rather than being told a wrong
// one.
func (t *thread) threadID() ThreadID {
	if t == nil {
		return 0
	}
	return t.id
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

	// ErrTerminated is the terminal status of a thread the engine ended: contract §2 T-5.4's shutdown,
	// reaching a running thread at its next safepoint and a suspended one by trapping it out of the wait.
	//
	// **It is a status and not a fault**, which is the one place T-5.3's two channels carry different
	// contents. `world.retire` records this in the per-`tid` record a `Join` answers from and never in the
	// fault slot `Instance.Fault` reads, because an embedder that calls `Close` and then reads `Fault`
	// must not be told its guest trapped when what happened is that it shut the guest down.
	ErrTerminated = errors.New("burroughs: thread terminated by shutdown")

	// ErrThreadFault wraps the first trap that ended any thread of an instance — contract §2 T-5.3's
	// retained trap, reported once through the next `Invoke` and readable forever through
	// `Instance.Fault`.
	//
	// **Wrapped rather than returned bare, so that a caller can tell where a trap happened.** An `Invoke`
	// that returns a plain trap says *"this call trapped"*; T-5.3's report is about a **different**
	// thread, and an embedder that could not distinguish the two would attribute a spawned thread's
	// out-of-bounds access to the host call that merely happened to be next. The wrapped trap is still
	// matched by `errors.Is`, so a caller testing for a specific trap gets the same answer either way.
	ErrThreadFault = errors.New("burroughs: a thread of this instance ended in a trap")

	// ErrUnknownThread refuses a `Join` on a `tid` this instance has no answer for.
	//
	// **Three causes, one error, and the message names all three because the caller cannot tell them
	// apart and neither can the engine.** T-5.5 forbids `tid` reuse, so an unanswerable id was never
	// spawned here, has already been joined (T-5.2's record is *consumed* by a join), or had its record
	// dropped at `Close`. Distinguishing the second and third from the first needs a record of every id
	// ever issued, which is the unbounded retention T-5.2's bound exists to forbid — so the honest answer
	// is *"nothing here knows"* with the reasons enumerated, rather than a guess dressed as three errors.
	ErrUnknownThread = errors.New("burroughs: no terminal status for this thread")

	// ErrNotSpawned refuses a `Join` on the instantiation thread, which is live, not spawned, and does
	// not terminate (`thread.done`). Distinct from `ErrUnknownThread` because the id *is* known: joining
	// it would block until the instance was closed, which is a hang wearing the shape of a wait.
	ErrNotSpawned = errors.New("burroughs: this thread is the instance's own and does not terminate")
)

// threadTerminated is the sentinel a terminal safepoint panics — [ADR 0071]'s option B, the reason
// `poll` and `jumpTo` keep their no-error signatures, and the *"`recover` above this frame"* [ADR
// 0070] forecast for #12.
//
// **Its own type rather than a sentinel `error` value, so the `recover` can discriminate on a type
// assertion and re-panic everything else.** The alternative — panicking an `error` and testing it with
// `errors.Is` — would recover an embedder's panic that happened to carry an error and convert it into
// this engine's clean termination, which is precisely the promise [ADR 0070] declined to make. An
// unexported type no other package can construct makes the discrimination exact.
//
// [ADR 0070]: ../../docs/decisions/0070-an-embedder-panic-is-repaired-inside-the-defers-that-already-exist.md
// [ADR 0071]: ../../docs/decisions/0071-t-5-is-live-only-membership-a-bounded-status-record-a-fault-in-two-channels-and-a-sentinel-panic-for-the-terminal-unwind.md
type threadTerminated struct{}

// terminate ends this thread from inside its own safepoint. Called from `parkAtSafepoint`, both before
// the park and after the release, and it never returns.
//
// **A panic and not an error return, because the two functions between here and a frame that could
// carry one are `poll` and `jumpTo`** — the fourteen back-edge arms and every frame entry, which is the
// hot path [ADR 0059] withdrew an always-nil error from. T-5.4 supplies the clause that error would
// have carried, and it fires once per thread per lifetime, so paying for it at every back-edge is the
// trade [ADR 0071] declines.
func (t *thread) terminate() {
	panic(threadTerminated{})
}

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
// **The lifecycle is T-5's, and it is settled** — §2 T-5.1–T-5.5, [ADR 0071]. A spawned thread is
// **detached by default**: this returns a tid and no obligation, and a caller that wants to wait calls
// `Instance.Join`. A thread that traps is reported twice, at the next `Invoke` and through
// `Instance.Fault`, and `Instance.Close` ends every thread and waits for the unwinds.
//
// This paragraph said the lifecycle *"stays open"* and that a terminated thread *"stays in
// `world.members` — reaping is #12's too"*. Both are false as written: the reaper is `world.retire`, and
// the field is `world.live`.
//
// [ADR 0071]: ../../docs/decisions/0071-t-5-is-live-only-membership-a-bounded-status-record-a-fault-in-two-channels-and-a-sentinel-panic-for-the-terminal-unwind.md
func (in *Instance) Spawn(entry uint32, arg int32, stackHint int) (ThreadID, error) {
	t, err := in.spawn(entry, arg, stackHint)
	if err != nil {
		return 0, err
	}
	return t.id, nil
}

// spawn is Spawn's mechanism, and the split is where the lifecycle *used* to be undecided.
//
// `Spawn` drops the `*thread` and hands back a bare id, and the reason has changed rather than
// evaporated. It used to be that *"a handle is the lifecycle API"* and T-5 had not been decided, so
// returning the object would have answered an open question in a signature. T-5 is decided, and the
// answer it gives is **still a tid**: `Instance.Join` takes one, T-5.2 asks for a host-side join *"taking
// a `tid`"*, and T-5.5's no-reuse rule is what makes an id a safe key for a thread that no longer exists.
// A `Thread` handle would be a second way to name the same thing, and would tempt a lifetime — an object
// held after its record was consumed — that a bounded record does not have. So the object stays inside
// the package, where the engine's own code and this package's tests use it, and the exported surface is
// the narrowest rendering of the stamped words.
func (in *Instance) spawn(entry uint32, arg int32, stackHint int) (*thread, error) {
	if !in.hasSharedMemory() {
		return nil, fmt.Errorf("%w: this instance reaches no shared memory, so a spawned thread "+
			"would share nothing with its parent", ErrNotShared)
	}
	c, err := in.resolveCall(entry)
	if err != nil {
		return nil, err
	}
	// **A host function is refused as a thread entry, and the reason is not the type check below.** A
	// host entry could satisfy T-1's `(i32) -> ()` shape exactly, so the arity check would pass it; what
	// it cannot satisfy is what a thread *is* here. `runEntry` builds a frame and runs a body, and a host
	// function has neither — so a spawned host entry would be a thread whose whole life is one Go call,
	// with no back-edge to poll, no safepoint it could reach, and therefore a `Stop` that waits out its
	// deadline on a member that will never arrive. Refused rather than special-cased, because the useful
	// version of it is an embedder starting its own goroutine, which needs nothing from this engine.
	if c.host != nil {
		return nil, fmt.Errorf("%w: function %d is a host function, which runs no guest body and could "+
			"reach no safepoint (contract §5, decision 0069)", ErrThreadEntry, entry)
	}
	fn, ft := c.fn, c.ft
	// Decision [0068]'s first named limit. See `ErrForeignEntry` for why this is refused rather than
	// resolved; the check is `c.inst != in` and not a nil test, because `resolveCall` resolves a
	// re-exported import transitively and returns the instance that owns the body.
	if c.inst != in {
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
		// **Retire, then close, in one `defer` and in that order** — T-5.2, [ADR 0071]. `retire` writes
		// the record `Join` answers from, so it must be complete before a joiner can be woken; a close
		// first would let a join observe a finished thread with no status. One `defer` rather than two so
		// the order is a statement rather than a consequence of LIFO, and because the deferred call is
		// where the error is already final: `runEntry` returns before this runs, so the assignment below
		// happens-before both the record and the close.
		defer func() {
			in.world.retire(t, t.err)
			close(t.done)
		}()
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
func (in *Instance) runEntry(t *thread, fn *binary.Func, ft *binary.FuncType, arg int32, stackHint int) (err error) {
	// §4 B-MM-1, at the enclosing function of the `stack` literal below, same as the other three
	// sites (`boundary.go`, decision 0052, #516). **This is the site where the edge stops being
	// bookkeeping.** At the other three the host and the guest are the same thread, so the acquire
	// and release order a thread against itself and the crossing is recorded rather than needed.
	// Here the crossing is the *only* thing ordering what the spawner wrote before `Spawn` against
	// what this thread reads first — B-MM-1's message-passing case is exactly this pair of edges
	// observed from two threads, so the site the control named is also the site the clause is about.
	enterGuest()
	// The third of [ADR 0070][0070]'s sites, and it now carries #12's `recover` as well — the fold 0070
	// forecast, in the words *"#12's exit/join is a `recover` above this frame by construction"*. It is
	// *in* this frame rather than above it, and 0070's forecast is discharged rather than restated: the
	// leak it repaired here (a panic skipping `leaveCall`) is now reachable on a non-test path, because
	// T-5.4's terminal safepoint panics through exactly these frames.
	//
	// # The order inside the defer, and why the re-panic is last
	//
	// The marks are repaired **before** anything is decided about `r`, so an embedder's panic leaves the
	// same state whether it is converted or re-thrown: that is 0070's repair, and putting the re-panic
	// first would undo it for exactly the case 0070 exists for.
	//
	// **Anything that is not the sentinel is re-panicked** ([ADR 0071]). Converting it would turn an
	// embedder's panic into `runEntry`'s error return — a promise 0070 declined to make, and one no test
	// that does not panic on purpose could notice was made. `TestASpawnEntryPanicLeavesNoCallerCounted`
	// is the witness that a foreign panic still escapes, and the sentinel's own conversion is witnessed
	// separately.
	//
	// **A single `defer`, still.** ADR 0067's constraint is that no function here may gain a *second*
	// one (25–29 ns/call once the first stops being open-coded), and a `recover` folded into the existing
	// one adds none.
	//
	// [0070]: ../../docs/decisions/0070-an-embedder-panic-is-repaired-inside-the-defers-that-already-exist.md
	guestRunning := false
	defer func() {
		r := recover()
		if guestRunning {
			t.leaveCall()
		}
		leaveGuest()
		if r == nil {
			return
		}
		if _, ok := r.(threadTerminated); !ok {
			panic(r)
		}
		// T-5.4's terminal exit, spelled as this thread's status rather than as a trap. `retire` reads
		// `ErrTerminated` and keeps it out of the fault slot — see `ErrTerminated`.
		err = fmt.Errorf("%w: %s ended at a safepoint after `Close` (contract §2 T-5.4)",
			ErrTerminated, t)
	}()

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
	// The panic case is the flag in the `defer` above rather than a second `defer` here — ADR 0070.
	t.enterCall()
	guestRunning = true
	// Assigned to the named result rather than declared, which is not a style choice: the `defer` above
	// writes `err` on the sentinel path, and a `:=` here would shadow nothing but would make the two
	// writers two variables to a later reader.
	err = in.invoke(fn, ft, st, 0)
	guestRunning = false
	t.leaveCall()
	return err
}

// Join waits for the thread `tid` to terminate and answers its terminal status: nil for a clean return,
// the trap that ended it otherwise. Contract §2 T-5.2.
//
// **The record is consumed**, which is the stamped half of T-5.2 (*"consumed by a join, or dropped at
// `Close`"* — Scott, on #12). A second `Join` on the same `tid` therefore returns `ErrUnknownThread`, and
// that is the bound rather than a rough edge: retention is what makes join-after-exit answerable at all,
// so the limit on it has to be a rule about who takes the entry out. The measured alternative is fact 3
// of [ADR 0071]'s table — **51 members after 50 completed spawns**, a per-thread record nothing removes.
//
// **Join-after-exit is answerable, and that is the clause this method exists for.** T-5.2 requires it in
// those words, so the ordinary case — spawn, let it finish, then join — must not be the failing one. It
// works because `world.retire` writes the record before `thread.done` closes, so there is no window in
// which a finished thread has no status.
//
// **Not a guest primitive.** T-5.1 makes threads detached by default and T-5.2 says there is *"no
// guest-visible join primitive"*; a guest that wants one builds it over T-3's futex, which is what the
// threads proposal and wasi-threads both assume.
//
// **No value, only an error, and the reason is T-1 rather than parsimony.** A thread entry takes one
// `i32` and returns nothing — checked exactly, in both directions, at `Instance.spawn` — so there is no
// value for a join to answer with. A signature promising one would promise a surface `Spawn` cannot
// produce.
//
// Three refusals, each naming a different thing the caller got wrong:
//
//   - `ErrNotSpawned` for the instantiation thread, which is live and does not terminate. Joining it
//     would block until `Close`, which is a hang wearing a wait's shape.
//   - `ErrUnknownThread` for an id with no record and no live thread: never spawned here, already
//     joined, or dropped at `Close`. See that error for why the three are one.
//   - `ErrClosed` is *not* among them: a closed instance has dropped its records, so a `Join` after
//     `Close` is `ErrUnknownThread` — the honest answer, since the status genuinely is not here.
//
// [ADR 0071]: ../../docs/decisions/0071-t-5-is-live-only-membership-a-bounded-status-record-a-fault-in-two-channels-and-a-sentinel-panic-for-the-terminal-unwind.md
func (in *Instance) Join(tid ThreadID) error {
	w := &in.world

	// The record is checked before the live set, and the order is what makes the common case lock-free of
	// any waiting: a thread that has already exited is not in `live` at all, so a live-set-first reader
	// would fall through to the refusal for exactly the case T-5.2 names as required.
	w.mu.Lock()
	if err, ok := w.exited[tid]; ok {
		delete(w.exited, tid)
		w.mu.Unlock()
		return err
	}
	var found *thread
	for _, t := range w.live {
		if t.id == tid {
			found = t
			break
		}
	}
	w.mu.Unlock()

	if found == nil {
		return fmt.Errorf("%w: thread %d was never spawned by this instance, has already been joined, "+
			"or had its status dropped at `Close` (contract §2 T-5.2, T-5.5)", ErrUnknownThread, tid)
	}
	if found.done == nil {
		return fmt.Errorf("%w: thread %d is the thread this instance was built on (contract §2 T-5.2)",
			ErrNotSpawned, tid)
	}

	// Outside `mu`, necessarily: this blocks until the thread terminates, and `retire` needs the same
	// mutex to record the status this then reads. Holding it across the receive is contract §4 B-MM-3's
	// own hazard and would be a deadlock rather than merely a violation.
	<-found.done

	// Second lookup rather than `found.err`, because the *record* is what T-5.2 bounds and reading the
	// thread's own field would leave the entry in the map forever — the leak, one field over. It is
	// present by construction: `retire` writes it before the close this just observed. Absent only if
	// another `Join` on the same id won the race, which is the double-join refusal arriving by a
	// different route and is answered as such.
	w.mu.Lock()
	err, ok := w.exited[tid]
	delete(w.exited, tid)
	w.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: thread %d terminated, and its status was taken by a concurrent join "+
			"(contract §2 T-5.2 — a record is consumed by a join)", ErrUnknownThread, tid)
	}
	return err
}
