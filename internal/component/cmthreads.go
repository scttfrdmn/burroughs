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
	// sched is the instance's scheduler goroutine, started by the first `thread.new-indirect` (cmsched.go).
	sched *cmScheduler
}

// threadState is where a component-model Thread is in its life, mirroring the reference's predicates.
type threadState uint8

const (
	// threadRunning is the reference's `running()`: it holds control. A lift's implicit Thread starts
	// here, and a created Thread enters it when the scheduler resumes it.
	threadRunning threadState = iota
	// threadSuspended is the reference's `suspended()`: `not running() and ready_func is None`. A Thread
	// created by `thread.new-indirect` starts here (`assert(new_thread.suspended())`), with its goroutine
	// parked; any Thread returns here from `thread.suspend` and the `suspend-*` forms.
	threadSuspended
	// threadReady is `resume_later()`'s effect — `start_waiting(ready_func = lambda: True)` — and the
	// `yield-*` forms' state for the giver, so the Thread is in the ready queue and a chain may run it.
	threadReady
	// threadKilled: the wake ended it while it was parked, by a trap in its task or by a `Close`.
	threadKilled
)

// cmThread is one component-model Thread's record.
//
// A lift's implicit Thread (`implicit`) runs on the lift's own goroutine. A Thread created by
// `thread.new-indirect` has a goroutine of its own — ADR 0100's *"a new pairing of the shared core
// `thread` with its own goroutine and stack"* — created parked, which is why `Close` must be able to
// reach it (see `killParked`).
//
// **Both park the same way once they have given control up**, inside [cmScheduler.giveBack], so there are
// two parked shapes and no more: parked inside `giveBack`, or never started. One wake reaches both.
type cmThread struct {
	index    uint32
	state    threadState // guarded by threadTable.mu
	implicit bool        // a lift's implicit Thread; resuming it ends the scheduler's chain (cmsched.go)

	entry interp.TableFunc // resolved at creation, as `canon_thread_new_indirect` reads `ftbl.get(fi)`
	arg   int32            // the `c` operand, passed to the entry
	task  *liftTask        // the task the Thread belongs to — `Thread(task, thread_func)`; nil for a sync lift's
	// owner is the implicit Thread of the lift the Thread belongs to — itself, for an implicit Thread. It
	// is the reference's `Thread.task` as an identity, and it exists because `task` cannot be one: a sync
	// lift has no `liftTask`, so its Threads' `task` is nil, and `killParked(nil)` means every Thread in the
	// instance (#970).
	owner *cmThread

	// resume is sent to by the scheduler alone, to run the Thread: nil to continue, an error to end its
	// handoff with one. Capacity 1. Made at creation for a created Thread and at an implicit Thread's
	// first handoff, so a lift that never uses 🧵 allocates neither channel.
	resume chan error
	kill   chan struct{} // closed by the wake to end it while parked
	done   chan struct{} // a created Thread's: closed when its goroutine has exited, AFTER err is written
	err    error         // the entry's error, if any; read only after done closes
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
// An implicit Thread qualifies exactly when the reference's does: once it has suspended inside its callee
// (`switch.wat`'s bodies resume theirs this way). A running one traps, as the reference's check would.
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

// killParked ends every parked Thread — suspended or ready, in either parked shape — and returns them so
// the caller can wait for each created Thread's goroutine to exit (`done`; an implicit Thread has none,
// and its lift returns the error instead).
//
// **This is the wake a parked Thread needs, and neither existing reclamation path supplies it.** The two
// that end an in-flight call both need the goroutine to be somewhere it is not:
// `internal/interp/host.go:ErrClosed` reaches a goroutine when it RE-ENTERS through `invokeIndex`, and
// `internal/interp/thread.go:ErrTerminated` reaches one at a SAFEPOINT while it runs guest code. A
// Thread is at neither in both of its parked shapes:
//
//   - **never started**: its goroutine's first `select`, with no guest frame;
//   - **parked inside `giveBack`**: inside a canon call, mid-guest, in a Go `select` — the shape the
//     switching forms add, and the one the second build slice's wake did not reach.
//
// Closing `kill` ends both: the first exits without running, the second's `giveBack` returns
// errThreadKilled and the guest unwinds. Without it, `Close` would leave the goroutine blocked for the
// life of the process.
//
// Scoped by `owner` when non-nil — a trap ends its own lift's Threads — and every Thread when nil (`Close`).
// It was scoped by `task`, which a sync lift does not have: its Threads' `task` is nil, and nil already
// meant every Thread in the instance (#970).
//
// **The `close` is after the unlock, and a control put it there.** The first draft closed `kill` inside
// the critical section; `TestNoEngineLockIsHeldAcrossAChannelOperation` refused it under contract §4
// B-MM-3 — *"`close` on a release channel IS the resume"* — so the guarded state is cleared under the lock
// (`threadKilled`, and out of the ready queue) and the channels are closed after it. The state change
// under the lock is what makes the order safe: a killed Thread cannot be popped as ready in between.
func (tt *threadTable) killParked(owner *cmThread) []*cmThread {
	tt.mu.Lock()
	var killed []*cmThread
	for _, t := range tt.array {
		if t == nil || (t.state != threadSuspended && t.state != threadReady) {
			continue
		}
		if owner != nil && t.owner != owner {
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

// threadBuiltinFunc binds a modeled 🧵 built-in to its Go impl — every form with an oracle, which since the
// switching slice is all eight of phase 1 (ADR 0100's point 4). A `false` return is a modeled opcode with
// no impl, which the partition control makes unreachable rather than merely unlikely.
func (w *walker) threadBuiltinFunc(op byte, tbl interp.Extern) (interp.CanonFunc, bool) {
	switch op {
	case 0x26: // thread.index
		return threadIndex(), true
	case 0x27: // thread.new-indirect
		return threadNewIndirect(w.async, w.threads, tbl), true
	case 0x28: // thread.resume-later
		return threadResumeLater(w.threads), true
	case 0x29, 0x2a, 0x2b, 0x2c, 0x2d: // suspend and the four switching forms
		return threadSwitch(op, w.async, w.threads), true
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
// **The goroutine is started here and parks at once**, on `resume` or `kill`. That is what "suspended"
// means for an engine whose Threads are goroutines: it exists, holds an index, and runs nothing.
//
// **From any lift, sync or async (#970).** The new Thread belongs to the CALLER's task, found the way
// `thread.index` finds the caller: through the index the stack carries. A sync lift has no `liftTask`, so
// its Threads carry a nil `task` and are tied to their lift by `owner`. This was refused from a sync lift
// until #970 built the sync branch (`cmScheduler.loop`'s sync origin), because nothing could have run the
// Thread.
//
// **Refused by name only where there is no component-model Thread at all**: a core-level `Invoke` that
// bypasses the component carries no index, and the reference has no `current_thread()` to give the new
// Thread a task.
func threadNewIndirect(h *asyncHandles, tt *threadTable, tbl interp.Extern) interp.CanonFunc {
	return func(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("component: thread.new-indirect: got %d args, want (fi, c)", len(args))
		}
		caller := tt.current(c)
		if caller == nil {
			return nil, fmt.Errorf("%w: thread.new-indirect outside any component-model Thread — the call "+
				"did not enter through a lift, so there is no task for the new Thread to belong to",
				ErrUnsupportedForm)
		}
		entry, err := interp.ResolveTableFunc(tbl, uint32(args[0].Bits))
		if err != nil {
			return nil, err
		}
		t := &cmThread{
			state:  threadSuspended,
			entry:  entry,
			arg:    int32(uint32(args[1].Bits)),
			task:   caller.task,
			owner:  caller.owner,
			resume: make(chan error, 1),
			kill:   make(chan struct{}),
			done:   make(chan struct{}),
		}
		s := tt.scheduler(h) // started here, lazily: an instance that never creates a Thread has none
		idx := tt.add(t)
		go t.run(s)
		return []interp.Value{interp.I32(int32(idx))}, nil
	}
}

// run is a created Thread's goroutine: park until the scheduler resumes it or the wake ends it; when
// resumed, call the entry as this Thread under the chain's hold on the entry slot; then unregister and
// report the exit to the scheduler, whose chain is waiting for it.
//
// **It does not take the entry slot**, which the second build slice's version did. The slot is the
// chain's (cmsched.go): held by the implicit Thread when the chain starts inside its callee, and by the
// YIELD arm when it starts there. Taking it here would deadlock the first case on a slot held by a Thread
// parked mid-callee.
//
// **The error crosses goroutines as a value, written before `done` closes.** That is `thread.err` →
// `retire` → `done`'s two-step at Thread scope, and it is the whole of trap transport here: an entry that
// traps returns its trap as an error from `InvokeCMThread` — `invokeIndex` already converts the terminal
// unwind — so there is no panic to carry. A Go panic that is NOT a trap is an engine bug and is left to
// crash, for ADR 0070's reason: converting an embedder's panic into a value is a promise this engine has
// declined to make.
//
// Unregistering BEFORE the report is the reference's `thread_func` ending in
// `task.unregister_thread(new_thread)` before the chain continues, which frees the index for the next
// Thread the chain creates — the reuse `switch.reading` measures.
func (t *cmThread) run(s *cmScheduler) {
	defer close(t.done)
	select {
	case <-t.resume:
	case <-t.kill:
		s.tt.remove(t.index)
		return
	}
	_, t.err = t.entry.InvokeCMThread(t.index, interp.I32(t.arg))
	s.tt.remove(t.index)
	if errors.Is(t.err, errThreadKilled) {
		return // the wake ended it while parked in a handoff; nothing is waiting for its report
	}
	select {
	case s.back <- handoff{from: t, exited: true, err: t.err}:
	case <-s.stop:
	}
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

// threadSwitch implements `thread.suspend` (0x29) and the four switching forms (0x2a–0x2d): each gives the
// entry slot up through [cmScheduler.giveBack] and returns `[0]` when the calling Thread is resumed.
//
// The reference, per form (`definitions.py`, `canon_thread_*` and `Thread`'s methods of the same names):
//
//	suspend               block()                                         -> suspended
//	suspend-then-resume   trap_if(not other.suspended()); switch_to(other) -> suspended
//	yield-then-resume     trap_if(not other.suspended()); start_waiting;  -> ready
//	                      switch_to(other)
//	suspend-then-promote  trap_if(i == thread.index); if other.ready():   -> suspended
//	                      stop_waiting; switch_to(other) else block()
//	yield-then-promote    as above, with start_waiting / yield_           -> ready
//
// `promote`'s `if other.ready(): other.stop_waiting()` is [threadTable.takeReady], a membership test and a
// removal by identity on the ready queue. `yield_` with nothing to promote is `wait_until(lambda: True)`,
// whose immediate-return branch the reference takes half the time; this takes the blocking branch every
// time, as the YIELD arm does, which the reference permits.
//
// **The calling Thread is the carrier's**, read the way `thread.index` reads it, so a created Thread and
// an implicit one are found the same way.
//
// **From any lift, sync included (#970).** A sync callee that suspends with nothing runnable traps, as the
// reference's sync branch does (`trap_if(not candidates)`); `cmScheduler.loop` delivers that trap. Refused
// by name only where there is no component-model Thread at all, for `new-indirect`'s reason.
//
// **`may_leave` is not checked**: every form's first line is `trap_if(not ...may_leave)`, which no
// committed reading reaches and this engine does not model for the async built-ins either.
func threadSwitch(op byte, h *asyncHandles, tt *threadTable) interp.CanonFunc {
	name := threadBuiltinNames[op]
	return func(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		self := tt.current(c)
		if self == nil {
			return nil, fmt.Errorf("%w: %s outside any component-model Thread — the call did not enter "+
				"through a lift, so there is no Thread to suspend", ErrUnsupportedForm, name)
		}
		var target uint32
		if op != 0x29 {
			if len(args) < 1 {
				return nil, fmt.Errorf("component: %s: got %d args, want (i)", name, len(args))
			}
			target = uint32(args[0].Bits)
		}
		next, state, err := tt.switchTarget(op, name, self, target)
		if err != nil {
			return nil, err
		}
		if err := tt.scheduler(h).giveBack(c, self, next, state); err != nil {
			return nil, err
		}
		return []interp.Value{interp.I32(0)}, nil
	}
}

// switchTarget resolves a switching form's operand to the Thread to run next (nil for none) and the state
// the caller gives itself, trapping where the reference traps.
func (tt *threadTable) switchTarget(op byte, name string, self *cmThread, i uint32) (*cmThread, threadState, error) {
	state := threadSuspended
	if op == 0x2b || op == 0x2d {
		state = threadReady
	}
	if op == 0x29 {
		return nil, state, nil
	}
	if (op == 0x2c || op == 0x2d) && i == self.index {
		return nil, 0, &interp.Trap{Reason: fmt.Sprintf("%s: Thread %d cannot promote itself", name, i)}
	}
	other := tt.get(i)
	if other == nil {
		return nil, 0, &interp.Trap{Reason: fmt.Sprintf("%s: no Thread at index %d", name, i)}
	}
	switch op {
	case 0x2a, 0x2b: // *-then-resume: the target must be suspended
		tt.mu.Lock()
		suspended := other.state == threadSuspended
		tt.mu.Unlock()
		if !suspended {
			return nil, 0, &interp.Trap{Reason: fmt.Sprintf("%s: Thread %d is not suspended", name, i)}
		}
		return other, state, nil
	default: // *-then-promote: switch only to a READY target, else a plain suspend or yield
		if tt.takeReady(other) {
			return other, state, nil
		}
		return nil, state, nil
	}
}

// current is the calling Thread's record, found by the index the stack carries.
func (tt *threadTable) current(c *interp.CanonCaller) *cmThread {
	i, ok := c.CMThreadIndex()
	if !ok {
		return nil
	}
	return tt.get(i)
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
