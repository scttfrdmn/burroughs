// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"errors"
	"fmt"
	"runtime"

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
// # This branch is parked, and it is expected to be red
//
// T-1 is written and lives in **#554**, a PR parked unmerged: `Spawn`, a shared-memory gate, an
// entry-signature check, and `runEntry` launching a goroutine that calls `runtime.LockOSThread` and
// never unlocks. Five tests for T-1 there, and it is deliberately red at one test — see below.
//
// It is **withheld** because `TestPlainAccessesAreUnsynchronisedWhileTheInterpreterIsSingleThreaded`
// fires on it. That control watches for the first `go` statement in this package's non-test files and
// says what to do about it: *"Do not exempt this file; discharge #557, and #516 for §4's boundary
// model."*
//
// **The blocker changed rather than cleared, and this paragraph is where that was noticed too late**
// (grave **#561**). It used to quote the same control saying *"discharge #542"*, and to assert that all
// 67 atomics in `atomic.go` were plain read-then-write — true when written, falsified by [ADR
// 0051][0051], which made them sequentially-consistent word operations over the backing array.
// What the control still watches for is the other half of one risk: the plain accesses in `memop.go`
// are a byte-at-a-time loop and a `copy`, so an aligned `i32.load` can tear where the proposal forbids
// it (**#557**), and §4's boundary model is **#516**. The watched event is unchanged, so `Spawn` is
// parked one link further along the chain than the ruling below predicted, and the way that was
// settled is the part worth keeping: a `go` statement injected into a scratch non-test file and the
// resulting FAIL read back. **A claim about what an instrument will permit is a forecast about a
// machine sitting in the tree.**
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
// trigger's lessons.* Taking neither is what made this a real block rather than a speed bump.
//
// [0050]: ../../docs/decisions/0050-the-per-thread-context-is-its-own-object-reached-by-one-pointer-on-stack-because-3-and-5-need-more-per-thread-state-than-a-slot.md
// [0051]: ../../docs/decisions/0051-the-atomics-become-sequentially-consistent-word-operations-over-the-backing-array-because-the-proposal-fixes-the-ordering-and-leaves-only-the-mechanism.md
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
	//nolint:unused // pinned by 0050 before its first consumer; retired by #515's safepoint check
	slot uint64

	// done closes when the thread has terminated and err is final.
	//
	// **This is not join, and the distinction is contract §10.3's.** T-5 requires exit, join and
	// detach *defined in the contract* (**#12**, open), so nothing here decides them: there is no
	// ownership, no detach, no reaping, and no ordering promised between two threads' terminations.
	// What the channel buys is the one fact the engine needs internally and SP-4 will need for
	// real — that a thread has stopped — plus a happens-before edge that makes `err` readable
	// without a race. A `Join` method would be answering the open question in the channel that gets
	// no review, which is the thing 0050 says it will not do.
	done chan struct{}

	// err is the thread's terminal error, written once by the thread itself before `done` closes.
	//
	// **Stored rather than surfaced, and stored rather than dropped.** How a thread's failure
	// becomes visible to a host *is* exit semantics, so it is #12's to answer and no accessor
	// exists. Swallowing it instead would make a trapping thread indistinguishable from one that
	// returned, which is a wrong answer rather than a deferred question.
	err error
}

// String names a thread in an error or a test failure, so a message about one says which. The only
// method on the type.
func (t *thread) String() string { return fmt.Sprintf("thread %d", t.id) }

var (
	// ErrNotShared refuses a spawn on an instance with no shared memory to share.
	//
	// **This is the gate, and it is a gate by construction rather than a second flag.** A `shared`
	// limits flag only decodes with `Features.Threads` on (`decodeLimits`), so an instance holding a
	// shared memory *is* proof the threads gate was set when its module was decoded. A duplicate
	// boolean on the instance could disagree with the decoder; this cannot. Behaviour 4 and contract
	// §9 want the capability behind the proposal's gate, and the memory is the gate's own witness.
	ErrNotShared = errors.New("interp: spawn needs a shared memory")

	// ErrThreadEntry refuses an entry function whose type is not T-1's `(entry_func, arg)` shape.
	ErrThreadEntry = errors.New("interp: thread entry must take one i32 and return nothing")
)

// newThread makes the instance's next thread. The id counter is the only piece of instance state
// spawn mutates, and it is atomic because T-2 forbids a main-thread special case: any thread may
// spawn, so two spawns can race for an id.
func (in *Instance) newThread() *thread {
	return &thread{id: ThreadID(in.nextTID.Add(1)), done: make(chan struct{})}
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

// reachableMemories is every linear memory reachable from this instance through *import slots*, in a
// deterministic order: this instance's memory index space, then that of every instance it imports a
// function from, transitively.
//
// **It is a closure and not `in.mems`, because a thread's entry can be an imported function.**
// `resolveCall` resolves an import to the instance that *defined* the body and `spawn` hands that
// instance to `runEntry`, so a walk over one index space would mark the wrong set — and the set it
// missed is precisely the one the new thread runs against.
//
// **This closure is not the set the new thread can touch, and the gap is [#575].** The first draft of
// this comment argued it was, on the ground that a `funcref` cannot name another instance's function.
// That is `link.go`'s sentence about what decision 0017 *records*, and it is true as history only:
// grave #163 widened `ref` to a pair, `funcRefTarget` resolves a call through `r.Inst`, and
// `call.go`'s own comment says a table slot may hold another instance's funcref. So `call_indirect`
// does leave the instance that owns the table, along an edge nothing here follows.
//
// Widening the walk is not the repair. A funcref is a value that flows: `table.set`, `table.copy`,
// `table.init` and the instantiation of a module that did not exist yet all put a foreign one into a
// table a running thread reads. `TestSpawnMarksEveryMemoryTheNewThreadCanReach`'s last two rows are
// the witness and the refutation of the cheap fix respectively — the second links its foreign instance
// *after* the spawn returns. #575 holds the option space; until it is ruled, this walk covers the
// import closure and `Spawn` is parked.
//
// Import slots of every kind carry an owner, and only the function slots are followed: a memory,
// table, global or tag import introduces no *code*, and the memory it introduces is already in this
// instance's own `mems` (the same allocation, one pointer, which is why the dedup is by `*memory` and
// not by index). A nil slot is an import nothing supplied, and a nil owner is a host function with no
// instance behind it; both are skipped rather than being errors here, since `resolveCall` is what
// reports an unlinked import to the caller.
func (in *Instance) reachableMemories() []*memory {
	var mems []*memory
	seenInst := make(map[*Instance]bool)
	seenMem := make(map[*memory]bool)
	var walk func(x *Instance)
	walk = func(x *Instance) {
		if x == nil || seenInst[x] {
			return
		}
		seenInst[x] = true
		for _, m := range x.mems {
			if m != nil && !seenMem[m] {
				seenMem[m] = true
				mems = append(mems, m)
			}
		}
		for _, ext := range x.funcs {
			if ext != nil {
				walk(ext.owner)
			}
		}
	}
	walk(in)
	return mems
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
// **Spawn does not make shared memory safe, and this comment is not the place a reader should have
// to discover that.** `memory.atomic.wait` cannot return 0/woken (**#543**); §4's boundary memory
// model is **#516** and its litmus battery **#10**. Before a second thread existed a lost update was
// *unobservable* rather than absent, and this function is what makes it observable. That is why this
// code is parked rather than merged — see the type's doc comment for the ruling.
//
// **One of that list is discharged and two blockers are not on it, which is the state to read this in.**
// The 67 atomics stopped being plain reads and writes with #542/#557. Neither of the others was
// enumerated when the list was written:
//
//   - This function runs the entry in the same instance, so the new thread shares the instance's
//     **globals**, and `global.set` writes them plainly (**#573**). Contract T-1 says a spawned thread
//     shares *"the module's shared linear memory"* and says nothing about the instance, so whether the
//     globals may be shared at all is the open question rather than how to synchronise them.
//   - The moving-backing-array hole this function opened (**#556**) is **not** closed by the walk
//     below. Decision 0056 chose the right shape and rested it on a false premise: the walk's domain is
//     an import closure, and a table slot holding a foreign funcref takes the thread outside it
//     (**#575**, `reachableMemories`). A memory reached that way is unmarked *and* unreserved, so
//     `grow` relocates it under a running thread — which is memory-unsafe rather than a lost update,
//     since a torn slice header can pair a new length with an old pointer.
//
// The lifecycle stays open: T-5's exit/join/detach are contract §10.3 (**#12**), so a caller gets a
// tid and no way to wait on it. See `thread.done` for why the internal channel is not that API.
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
	// T-1 fixes the entry shape at one i32 argument and no results, so the check is exact in both
	// directions rather than a minimum: a function returning a value would leave it on a stack
	// nothing will ever pop, and the arity debt (#9) would report it as a wrong answer later,
	// somewhere with no thread in the message.
	if len(ft.Params) != 1 || ft.Params[0] != binary.I32 || len(ft.Results) != 0 {
		return nil, fmt.Errorf("%w: function %d takes %v and returns %v",
			ErrThreadEntry, entry, ft.Params, ft.Results)
	}

	// **Decision 0056's walk, and its position in this function is load-bearing twice over.**
	//
	// *After every refusal*, because marking is not free to undo: a marked memory can never grow past
	// `sharedReservePages` again (see `reservation`), so a spawn that was going to fail its entry-shape
	// check must not narrow what the instance's memories can do on its way out. A `Spawn` that returns
	// an error leaves the instance exactly as it found it.
	//
	// **Half of that placement is forced by the data flow rather than by the policy, which is worth
	// knowing before someone "simplifies" it.** `target` does not exist until `resolveCall` has run, so
	// a walk hoisted above the refusals cannot have the right domain — it would have to walk the
	// *spawner's* closure and would then mark memories the new thread cannot reach. Measured while
	// trying to mutate the position alone: the naive hoist changed the domain too, and failed a second
	// row than the one it was aimed at.
	//
	// *Before the `go`*, because that is the whole soundness argument — the relocation and the mark
	// happen while one thread exists, so no other agent holds a slice header or an ADR 0051 pointer
	// into the array being replaced. Marking after the goroutine starts is option (C), which 0056
	// rejects.
	//
	// **`target`'s closure and not `in`'s**, for `runEntry`'s reason one step further: the thread runs
	// `target`'s body, so the memories it can reach are `target`'s and those of every instance
	// `target` can call into. Memories only the *spawner* reaches stay single-threaded and keep their
	// cheap allocate-and-blit growth, which is the narrowing §0 asks for — this marks what a second
	// thread can touch, not everything in sight.
	//
	// **And it does not mark all of what a second thread can touch: [#575].** The closure is over import
	// slots, and an indirect call through a table slot another instance filled leaves it
	// (`reachableMemories` carries the falsification and why a bigger walk is the wrong repair). This
	// loop is correct about the memories it reaches and is not the soundness argument it was written as;
	// #575 is what makes it one, and `Spawn` stays parked until that is ruled.
	//
	// A failure refuses the spawn. That direction is forced: the alternative is a thread running
	// against a memory whose array can still move, which is #556 with a thread to observe it.
	for _, m := range target.reachableMemories() {
		if err := m.reserveForASecondThread(); err != nil {
			return nil, fmt.Errorf("%w: preparing this instance's memories for a second thread "+
				"(decision 0056)", err)
		}
	}

	t := in.newThread()
	go func() {
		runtime.LockOSThread()
		// The error is assigned before the deferred close runs, so a reader that has observed the
		// close has observed the write — the channel supplies the happens-before edge, and this is
		// the only cross-thread read of `err`.
		defer close(t.done)
		// `target`, not `in`: `resolveCall` resolves an imported function to the instance that
		// *defined* it, and a thread entering through an import must run with that instance's
		// memories and tables. The same distinction `call` makes (`return target.invoke(...)`) —
		// getting it wrong here would run a foreign body against this instance's state.
		t.err = target.runEntry(t, fn, ft, arg, stackHint)
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
	return in.invoke(fn, ft, st, 0)
}
