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
// Monotonic from 1, and 0 is deliberately not a valid id: a zero `ThreadID` is the value a caller
// gets back alongside an error, and the one thing it must not be able to mean is "the first thread".
type ThreadID uint64

// thread is one wasm thread's execution context — [0050]'s chosen representation, and the object
// contract §§3–5's per-thread state will hang off.
//
// **Why this exists as its own type rather than as fields on `stack`.** `stack` is threaded through
// every function on the hot path already (`run`, `runFrame`, `enterFrame`, `invoke`), so putting the
// slot straight on it would have cost zero new parameters and three propagation sites — measurably
// the cheapest option, and the one 0050 rejects. `stack` is per-*invocation*; T-4 asks for
// per-*thread*, and §7's growable continuations will make those different by creating stacks inside
// one thread. A slot living on `stack` would then be per-continuation, and a fresh stack that forgot
// to copy it would hold a **zero value indistinguishable from a legitimate first thread** — a
// plausible wrong answer rather than a crash. A `*thread` copies idempotently and a forgotten copy
// is nil, which panics where it is wrong.
//
// The larger reason is that §§3–5 need more per-thread state than a slot: SP-1's stop/epoch flag
// checked at back-edges and call sites, SP-2's "in a host call, therefore at a safepoint" bit, T-3's
// futex park token. Every one is per-thread and every one would be per-continuation on `stack`.
//
// [0050]: ../../docs/decisions/0050-the-per-thread-context-is-its-own-object-reached-by-one-pointer-on-stack-because-3-and-5-need-more-per-thread-state-than-a-slot.md
type thread struct {
	// id is T-1's tid, assigned once at creation and never written again.
	id ThreadID

	// slot is T-4: the per-thread slot, the `g` register analog. Read as `st.t.slot` — two
	// dereferences from a pointer the hot path already holds in a register.
	//
	// **Nothing on the hot path reads it yet, and that is why 0050 does not benchmark the read.**
	// The first reader is #515's safepoint check. Comparing `st.t.slot` against `st.slot` today
	// would compare two fields neither of which is read, which is an analytic zero — it could not
	// have come out any other way. What 0050 *does* pre-register is the cost of carrying the
	// pointer, which is falsifiable on layout alone.
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

// String names a thread in an error or a test failure, so a message about one says which.
//
// **The only method, and there are deliberately no slot accessors.** A `slotOf`/`setSlot` pair would
// be two functions nothing calls — which `deadcode` refuses, and which would in any case be a guess
// at the shape #515's reader wants. The guest-visible surface for T-4, the host function a module
// calls to read its own slot, is not this slice's to invent: that is public API riding a
// representation PR.
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
// to discover that.** The 67 atomics are plain reads and writes (**#542**) and
// `memory.atomic.wait` cannot return 0/woken (**#543**); §4's boundary memory model is **#516** and
// its litmus battery **#10**. Before a second thread existed a lost update was *unobservable*
// rather than absent, and this function is what makes it observable. Guests that share memory across
// threads are racy until those land.
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
func (in *Instance) runEntry(t *thread, fn *binary.Func, ft *binary.FuncType, arg int32, stackHint int) error {
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
