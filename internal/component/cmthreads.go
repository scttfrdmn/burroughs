// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/scttfrdmn/burroughs/internal/interp"
)

// `gate:cm-threads` and the per-instance component-model Thread table (ADR 0100, phase 1).
//
// # The gate
//
// ADR 0100's point 2: a new gate, default **off**, which requires `gate:async` to be on. Threads live
// inside tasks, so the dependency is real; and `gate:async` is itself default-**on** since the 2026-09-18
// flip, so a shared gate would have put 🧵 semantics on by default with it. wasmtime separates the two
// the same way (`-W component-model-threading`).
//
// # Why this table is not `asyncHandles`
//
// `asyncHandles.removeLocked` nils a slot and deliberately does **not** reuse it: *"The model's own
// `remove` does the same … and keeps a free list; this engine does not reuse slots yet … a reused index
// would hand a guest a stale handle's identity, which is a decision to take when something needs the
// compaction and not before."* For Threads something does: the committed wasmtime reading
// (`testdata/cmthreads/switch.reading`) measures **every** explicitly created Thread at index **2**,
// because each one returns and unregisters before the next is created and the index is reused. Reuse is
// therefore observable guest behaviour here, where for handles it is not yet needed — two different
// policies, so two tables rather than one with a flag.
const cmThreadsGateEnv = "BURROUGHS_CM_THREADS"

// cmThreadsEnabled reports whether `gate:cm-threads` is on. **Off is the default**, which is the opposite
// of `componentsEnabled` and `asyncEnabled`: both of those read `!= "0"` because their flips happened
// (2026-09-11 and 2026-09-18). This one has not flipped and will not until each modelled form has a
// committed oracle, so it reads `== "1"` — an explicit opt-in.
func cmThreadsEnabled() bool { return os.Getenv(cmThreadsGateEnv) == "1" }

// ErrCMThreadsGated is returned at bind when a component uses a modelled 🧵 built-in and
// `gate:cm-threads` is off. `ComponentConfig.Run` wraps it as `burroughs.ErrGated`, so the CLI
// classifies it exit 6 — a well-formed component this build turns away, which is grave #301's
// distinction and the same shape `ErrAsyncGated` has.
var ErrCMThreadsGated = errors.New("gate:cm-threads is off in this build")

// ErrCMThreadsNeedsAsync is returned at bind when `gate:cm-threads` is on but `gate:async` is off.
//
// **Its own sentinel rather than reusing `ErrAsyncGated`**, because the two say different things to an
// operator: `ErrAsyncGated` means *this component uses async and you turned async off*, while this means
// *you turned 🧵 on and it cannot work without async, which you turned off*. The second names a
// combination the operator chose, and the remedy is different — unset one variable rather than the other.
var ErrCMThreadsNeedsAsync = errors.New("gate:cm-threads requires gate:async, which is off in this build")

// threadTable is the per-instance component-model Thread table, on the reference's `Table` policy.
//
// The reference (`definitions.py`'s `Table`), quoted because this implements it exactly and ADR 0100
// requires that:
//
//	def __init__(self):            def add(self, e):                  def remove(self, i):
//	  self.array = [None]            if self.free:                      e = self.get(i)
//	  self.free = []                   i = self.free.pop()              self.array[i] = None
//	                                 else:                              self.free.append(i)
//	                                   i = len(self.array)              return e
//	                                   self.array.append(e)
//	                                 return i
//
// Two properties, both observable:
//
//   - **Index 0 is reserved.** `array` starts as `[None]`, so the first allocation is 1 — which is where
//     the committed reading's entry-Thread index of 1 comes from.
//   - **The free list is LIFO.** `remove` appends and `add` pops the end, so a reused index is the most
//     recently freed one and not the lowest.
//
// **The committed reading does not discriminate the LIFO order**, and that is recorded rather than
// implied: exactly one slot is free at each creation in that fixture, so LIFO, lowest-free and any other
// order all produce 2. The order is implemented because the reference specifies it, and
// `TestThreadTableFollowsTheReferencesAllocationPolicy` is what holds it — a fixture that keeps two
// Threads live and frees them out of order would be the differential witness, and none exists yet.
type threadTable struct {
	mu    sync.Mutex
	array []*cmThread // array[0] is the reserved nil slot
	free  []uint32    // LIFO: remove appends, add pops the end
	// ready is the instance's ready queue, FIFO, and it is the reference's `store.waiting` filtered to
	// `ready()` Threads. FIFO is a CONFORMING choice rather than the reference's choice: `Store.tick` picks
	// with `random.choice`, so any order is permitted and a deterministic one is easier to reason about.
	ready []*cmThread
}

// threadState is where a component-model Thread is in its life, mirroring the reference's predicates.
type threadState uint8

const (
	// threadImplicit is a lift's implicit Thread. It has no goroutine of its own — it runs on the lift's
	// goroutine — so it is never parked, readied or killed by this table.
	threadImplicit threadState = iota
	// threadSuspended is the reference's `suspended()`: `not running() and ready_func is None`. A Thread
	// created by `thread.new-indirect` starts here (`assert(new_thread.suspended())`), with its goroutine
	// parked on `start`/`kill`.
	threadSuspended
	// threadReady is `resume_later()`'s effect — `start_waiting(ready_func = lambda: True)` — so the
	// Thread is in the ready queue and the next scheduling point may run it.
	threadReady
	// threadRunning: the scheduler has handed it the entry slot.
	threadRunning
	// threadKilled: its goroutine was ended without running, by a trap or a `Close`.
	threadKilled
)

// cmThread is one component-model Thread's record.
//
// An implicit Thread uses only `index`. A Thread created by `thread.new-indirect` uses the rest: it has a
// goroutine — ADR 0100's *"a new pairing of the shared core `thread` with its own goroutine and stack"* —
// created parked, which is why `Close` must be able to reach it (see `killParked`).
type cmThread struct {
	index uint32
	state threadState // guarded by threadTable.mu

	entry interp.TableFunc // resolved at creation, as `canon_thread_new_indirect` reads `ftbl.get(fi)`
	arg   int32            // the `c` operand, passed to the entry
	task  *liftTask        // the task the Thread belongs to — `Thread(task, thread_func)`

	start chan struct{} // closed by the scheduler to run it
	kill  chan struct{} // closed to end it while still parked
	done  chan struct{} // closed when its goroutine has exited, AFTER err is written
	err   error         // the entry's error, if any; read only after done closes
}

func newThreadTable() *threadTable {
	return &threadTable{array: []*cmThread{nil}}
}

// add registers a Thread and returns its index, reusing the most recently freed slot when there is one.
func (tt *threadTable) add(t *cmThread) uint32 {
	tt.mu.Lock()
	defer tt.mu.Unlock()
	if n := len(tt.free); n > 0 {
		i := tt.free[n-1]
		tt.free = tt.free[:n-1]
		tt.array[i] = t
		t.index = i
		return i
	}
	i := uint32(len(tt.array))
	tt.array = append(tt.array, t)
	t.index = i
	return i
}

// remove frees the slot at i, pushing it onto the free list. A no-op for the reserved slot and for an
// index that holds nothing, so a double unregister cannot corrupt the free list into handing one index
// to two Threads.
func (tt *threadTable) remove(i uint32) {
	tt.mu.Lock()
	defer tt.mu.Unlock()
	if i == 0 || int(i) >= len(tt.array) || tt.array[i] == nil {
		return
	}
	tt.array[i] = nil
	tt.free = append(tt.free, i)
}

// get returns the Thread at index i, or nil — the reference's `inst.threads.get(i)`, whose `trap_if` the
// caller renders.
func (tt *threadTable) get(i uint32) *cmThread {
	tt.mu.Lock()
	defer tt.mu.Unlock()
	if i == 0 || int(i) >= len(tt.array) {
		return nil
	}
	return tt.array[i]
}

// markReady is `canon_thread_resume_later`'s state change: `trap_if(not other_thread.suspended())`, then
// `resume_later()`, which puts the Thread in the ready queue.
//
// The implicit Thread is never suspended in this sense — it is running, or it is the lift's own
// goroutine waiting on the scheduler — so naming it traps, as the reference's assertion would.
func (tt *threadTable) markReady(i uint32) error {
	tt.mu.Lock()
	defer tt.mu.Unlock()
	if i == 0 || int(i) >= len(tt.array) || tt.array[i] == nil {
		return &interp.Trap{Reason: fmt.Sprintf("thread.resume-later: no Thread at index %d", i)}
	}
	t := tt.array[i]
	if t.state != threadSuspended {
		return &interp.Trap{Reason: fmt.Sprintf("thread.resume-later: Thread %d is not suspended", i)}
	}
	t.state = threadReady
	tt.ready = append(tt.ready, t)
	return nil
}

// popReady takes the first ready Thread and marks it running, under the lock, so `killParked` cannot race
// it into the killed state between the pop and the start.
func (tt *threadTable) popReady() *cmThread {
	tt.mu.Lock()
	defer tt.mu.Unlock()
	if len(tt.ready) == 0 {
		return nil
	}
	t := tt.ready[0]
	tt.ready = tt.ready[1:]
	t.state = threadRunning
	return t
}

// killParked ends every Thread whose goroutine is still parked — suspended or ready, never started — and
// returns them so the caller can wait for each goroutine to exit.
//
// **This is the wake a parked Thread needs, and neither existing reclamation path supplies it.** The two
// that end an in-flight call both need the goroutine to be somewhere it is not:
// `internal/interp/host.go:ErrClosed` reaches a goroutine when it RE-ENTERS through `invokeIndex`, and
// `internal/interp/thread.go:ErrTerminated` reaches one at a SAFEPOINT while it runs guest code. A
// Thread parked on `start` is at neither — it is in a Go `select` with no guest frame — so without this,
// `Close` would leave its goroutine blocked for the life of the process.
//
// Scoped by `task` when non-nil (a trap ends its own task's Threads), every Thread when nil (`Close`).
//
// **The `close` is after the unlock, and a control put it there.** The first draft closed `kill` inside
// the critical section; `TestNoEngineLockIsHeldAcrossAChannelOperation` refused it under contract §4
// B-MM-3 — *"`close` on a release channel IS the resume"* — so the guarded state is cleared under the lock
// (`threadKilled`, and out of the ready queue) and the channels are closed after it. The state change
// under the lock is what makes the order safe: a killed Thread cannot be popped as ready in between.
func (tt *threadTable) killParked(task *liftTask) []*cmThread {
	tt.mu.Lock()
	var killed []*cmThread
	for _, t := range tt.array {
		if t == nil || (t.state != threadSuspended && t.state != threadReady) {
			continue
		}
		if task != nil && t.task != task {
			continue
		}
		t.state = threadKilled
		killed = append(killed, t)
	}
	if len(killed) > 0 {
		kept := tt.ready[:0]
		for _, t := range tt.ready {
			if t.state != threadKilled {
				kept = append(kept, t)
			}
		}
		tt.ready = kept
	}
	tt.mu.Unlock()
	for _, t := range killed {
		close(t.kill)
	}
	return killed
}

// live reports how many Threads are registered, for teardown accounting and for a test to assert that a
// slice leaks none.
func (tt *threadTable) live() int {
	tt.mu.Lock()
	defer tt.mu.Unlock()
	n := 0
	for _, t := range tt.array {
		if t != nil {
			n++
		}
	}
	return n
}

// gateCMThreads refuses a component that uses a modelled 🧵 built-in when the gate is off, or when the
// gate is on and `gate:async` is not (ADR 0100's point 2).
//
// **Called from `walkComponent`, beside `gateAsync`, for #952's reason**: that is the one place every
// component passes through at every depth, so a nested component cannot slip past the gate the way one
// did before #952's repair.
//
// The dependency arm is checked **first**. With both variables set wrongly — `cm-threads` on and `async`
// off — an operator wants to be told about the combination they chose, not about the gate they turned on.
func gateCMThreads(c *Component) error {
	what, ok := cmThreadSurface(c)
	if !ok {
		return nil
	}
	if cmThreadsEnabled() && !asyncEnabled() {
		// **The remedy names both ways out, and neither is "set the gate you already set."** A first
		// draft said *"unset BURROUGHS_ASYNC or set BURROUGHS_CM_THREADS=1"*, which told an operator who
		// had just set `BURROUGHS_CM_THREADS=1` to set it again — advice that cannot be followed,
		// reached by copying the gated arm's wording into a different condition.
		return fmt.Errorf("%w: this component uses %s. Either unset %s (its default is on) or unset %s",
			ErrCMThreadsNeedsAsync, what, asyncGateEnv, cmThreadsGateEnv)
	}
	if !cmThreadsEnabled() {
		return fmt.Errorf("%w: this component uses %s; set %s=1 to run it (phase 1 of the "+
			"shared-everything-threads proposal, ADR 0100)", ErrCMThreadsGated, what, cmThreadsGateEnv)
	}
	return nil
}

// cmThreadSurface reports the first modelled 🧵 built-in in the component and a name for it.
//
// Only **modelled** forms are reported: an unmodelled one is refused at decode by name
// (`threadBuiltinRefusal`), so it never reaches bind and a gate refusal about it would be unreachable.
// ADR 0100's point 4 is what makes the two sets move together — a form joins the decoder's modelled arm
// only when it has a committed oracle, and joins this surface in the same slice.
func cmThreadSurface(c *Component) (string, bool) {
	for _, cn := range c.Canons {
		if cn.Kind != CanonThreadBuiltin {
			continue
		}
		name, ok := threadBuiltinNames[cn.ThreadOp]
		if !ok {
			name = "an unnamed thread built-in"
		}
		return fmt.Sprintf("canon %s (%#x)", name, cn.ThreadOp), true
	}
	return "", false
}

// threadBuiltinFunc binds a modeled 🧵 built-in to its Go impl. One arm, because one form has an oracle
// (ADR 0100's point 4). A `false` return is a modeled opcode with no impl, which the partition control
// makes unreachable rather than merely unlikely.
func (w *walker) threadBuiltinFunc(op byte, tbl interp.Extern) (interp.CanonFunc, bool) {
	switch op {
	case 0x26: // thread.index
		return threadIndex(), true
	case 0x27: // thread.new-indirect
		return threadNewIndirect(w.async, w.threads, tbl), true
	case 0x28: // thread.resume-later
		return threadResumeLater(w.threads), true
	}
	return nil, false
}

// threadNewIndirect implements `canon thread.new-indirect` (0x27, `canon_thread_new_indirect`): resolve
// the entry from the table NOW, create a suspended Thread registered on the task, and return its index.
//
// The reference, for the order of operations:
//
//	f = ftbl.get(fi)
//	trap_if(f.t != ft)
//	new_thread = Thread(task, thread_func)
//	assert(new_thread.suspended())
//	task.register_thread(new_thread)
//	return [new_thread.index]
//
// **The goroutine is started here and parks at once**, on `start` or `kill`. That is what "suspended"
// means for an engine whose Threads are goroutines: it exists, holds an index, and runs nothing.
//
// **Only from inside an async lift task, in this slice.** The reference reaches `current_task()` from a
// sync lift as well, where `canon_lift`'s sync tail runs ready Threads until the task resolves. This slice
// builds the scheduling point at the callback loop's YIELD arm and not that sync loop, so a
// `new-indirect` with no lift task in flight is refused by name rather than creating a Thread nothing
// could ever run — under ErrUnsupportedForm, since it is a form this engine does not support yet, and not
// ErrAsyncNotImplemented, which says the async tier itself is missing when this call is not async at all.
func threadNewIndirect(h *asyncHandles, tt *threadTable, tbl interp.Extern) interp.CanonFunc {
	return func(_ *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("component: thread.new-indirect: got %d args, want (fi, c)", len(args))
		}
		h.mu.Lock()
		task := h.lift
		h.mu.Unlock()
		if task == nil {
			return nil, fmt.Errorf("%w: thread.new-indirect outside an async lift task — the sync lift's "+
				"scheduling loop is not built in this slice, so the Thread could never run",
				ErrUnsupportedForm)
		}
		entry, err := interp.ResolveTableFunc(tbl, uint32(args[0].Bits))
		if err != nil {
			return nil, err
		}
		t := &cmThread{
			state: threadSuspended,
			entry: entry,
			arg:   int32(uint32(args[1].Bits)),
			task:  task,
			start: make(chan struct{}),
			kill:  make(chan struct{}),
			done:  make(chan struct{}),
		}
		idx := tt.add(t)
		go t.run(h, tt)
		return []interp.Value{interp.I32(int32(idx))}, nil
	}
}

// run is a created Thread's goroutine: park until started or killed; when started, take the entry slot
// for the Thread's task and call the entry as this Thread; then unregister and signal.
//
// **The error crosses goroutines as a value, written before `done` closes.** That is `thread.err` →
// `retire` → `done`'s two-step at Thread scope, and it is the whole of trap transport here: an entry that
// traps returns its trap as an error from `InvokeCMThread` — `invokeIndex` already converts the terminal
// unwind — so there is no panic to carry. A Go panic that is NOT a trap is an engine bug and is left to
// crash, for ADR 0070's reason: converting an embedder's panic into a value is a promise this engine has
// declined to make.
//
// Unregistering on the way out is the reference's `thread_func` ending in
// `task.unregister_thread(new_thread)`, which frees the index for reuse.
func (t *cmThread) run(h *asyncHandles, tt *threadTable) {
	defer close(t.done)
	select {
	case <-t.start:
	case <-t.kill:
		tt.remove(t.index)
		return
	}
	prev, ok := h.enterTask(t.task)
	if !ok {
		t.err = fmt.Errorf("%w: Thread %d could not take the instance's execution slot", ErrCancelled, t.index)
		tt.remove(t.index)
		return
	}
	_, t.err = t.entry.InvokeCMThread(t.index, interp.I32(t.arg))
	h.leaveTask(prev)
	tt.remove(t.index)
}

// threadResumeLater implements `canon thread.resume-later` (0x28): `trap_if(not other_thread.suspended())`,
// then `resume_later()` — the named Thread joins the ready queue and runs at the next scheduling point.
func threadResumeLater(tt *threadTable) interp.CanonFunc {
	return func(_ *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("component: thread.resume-later: got %d args, want (i)", len(args))
		}
		if err := tt.markReady(uint32(args[0].Bits)); err != nil {
			return nil, err
		}
		return nil, nil
	}
}

// runReady is the SCHEDULING POINT: run every ready Thread to completion, in queue order, before the
// caller re-enters its own guest code.
//
// # Where it is called, and the reference's equivalent
//
// From the callback loop's YIELD arm, between leaving the callee's entry and re-entering the callback —
// the one moment the instance's entry slot (`asyncHandles.entrySem`, this engine's `exclusive_thread`) is
// free and the yielding Thread is itself ready. The reference's YIELD arm is
//
//	inst.exclusive_thread = None
//	thread.wait_until(lambda: inst.exclusive_thread is None)
//
// and `Thread.wait_until` either returns at once (half the time, `random.randint(0,1)`) or starts waiting
// and blocks so `Store.tick` can `random.choice` among ready candidates. **This takes the blocking branch
// every time and runs the ready queue FIFO**: both are choices the reference permits, and the corrected
// `cmthreads/spawn.wat` tolerates either branch by re-YIELDing until its body has run.
//
// **The YIELD arm used to re-enter at once with EVENT_NONE and run nothing.** Against `spawn.wat` that
// would loop forever — the callback re-YIELDs until a body runs that this arm never let run — which is
// the defect visible rather than plausible.
//
// # A Thread that traps ends its task's other parked Threads
//
// A trap ends the computation for every Thread of the task (ADR 0100, trap transport), so the remaining
// parked ones are killed and waited for before the error returns. `Close` kills the rest of the instance's.
func (tt *threadTable) runReady() error {
	for {
		t := tt.popReady()
		if t == nil {
			return nil
		}
		close(t.start)
		<-t.done
		if t.err != nil {
			for _, k := range tt.killParked(t.task) {
				<-k.done
			}
			return t.err
		}
	}
}

// threadIndex implements `canon thread.index` (0x26, the reference's `canon_thread_index`): return the
// calling Thread's index.
//
// # Where the index lives, which is `context.get`'s split and not a new one
//
// The reference reads `current_thread().index`. Burroughs has no CM Thread object yet, so the implicit
// Thread's index lives where the async context already does, for the reason that note records: under an
// async (callback) lift it belongs to the **lift task**, because that loop spans several stack-creating
// calls and a per-stack value would change between re-entries; for a sync call the caller **is** a stack,
// so it lives there. Copying a measured placement rather than inventing one.
//
// # This built-in ALLOCATES NOTHING, and that is the repair
//
// The first draft allocated here, on first use, and argued the laziness was harmless because
// registration ORDER only matters once a second Thread exists. The argument was about the wrong thing:
// lazy allocation has no natural free point. The index landed on the per-call stack, the stack died with
// the call, and nothing freed the table entry — so three calls on one instance read **1, 2, 3** where the
// reference gives 1, 1, 1 (`unregister_thread` → `inst.threads.remove`, and *"each call to an export
// logically creates a fresh thread"*). Found because `live()` was written for a leak test, flagged unused
// by the linter, and tested rather than deleted.
//
// Registration is now at the lift's entry (`invokeWith` for a sync lift, `newLiftTask` for an async one)
// and the free is in a `defer` on the way out, which is where the reference puts both. This function only
// reads.
//
// **A zero answer is a refusal rather than an index**, because the reference reserves index 0: reaching
// here with no Thread registered means a `thread.index` from somewhere no lift entered, and answering 0
// would hand the guest a number the model cannot produce.
//
// # It reads ONLY the stack carrier, which is what makes a spawned Thread's answer right
//
// The first slice read `h.lift.threadIdx` first and the carrier second. Once `thread.new-indirect`
// exists that is wrong, not merely redundant: a spawned Thread runs with `h.lift` set to the SAME task —
// it belongs to it (`Thread(task, thread_func)`) — so the lift read would answer the implicit Thread's
// index, **1**, for a body the committed `spawn.reading` measures at **2**. Every entry now carries its
// own Thread's index — the callback loop passes `task.threadIdx`, a spawned Thread passes its own — so
// the carrier is the only source.
//
// That also discharges the unlocked-`threadIdx` argument rather than re-arguing it: `task.threadIdx` is
// written once in `newLiftTask`, before the task is published to any other goroutine, and is now read
// only by the loop's own goroutine to pass into `InvokeCMThread`. No other goroutine reads it.
func threadIndex() interp.CanonFunc {
	return func(c *interp.CanonCaller, _ []interp.Value) ([]interp.Value, error) {
		if i, ok := c.CMThreadIndex(); ok {
			return []interp.Value{interp.I32(int32(i))}, nil
		}
		return nil, &interp.Trap{Reason: "thread.index: no component-model Thread is registered for " +
			"this agent, so there is no index to return (the reference reserves 0, so it is not an answer)"}
	}
}
