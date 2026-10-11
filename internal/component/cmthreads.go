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
}

// cmThread is one component-model Thread's record. Phase 1 slice 1 models `thread.index` only, so the
// record carries its index and nothing else; the goroutine, the stack and the TLS slot a switching form
// needs arrive with the slice that creates a second Thread.
type cmThread struct {
	index uint32
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
func (w *walker) threadBuiltinFunc(op byte) (interp.CanonFunc, bool) {
	// An `if` rather than a one-case `switch`, which the linter is right about: a switch promises a
	// dispatch and there is one arm. It becomes a switch again when a second form is modeled, which is
	// ADR 0100's point 4 — one per slice, each with its own oracle.
	if op == 0x26 { // thread.index
		return threadIndex(w.async, w.threads), true
	}
	return nil, false
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
func threadIndex(h *asyncHandles, _ *threadTable) interp.CanonFunc {
	return func(c *interp.CanonCaller, _ []interp.Value) ([]interp.Value, error) {
		h.mu.Lock()
		lift := h.lift
		h.mu.Unlock()
		if lift != nil && lift.threadIdx != 0 {
			return []interp.Value{interp.I32(int32(lift.threadIdx))}, nil
		}
		if i, ok := c.CMThreadIndex(); ok {
			return []interp.Value{interp.I32(int32(i))}, nil
		}
		return nil, &interp.Trap{Reason: "thread.index: no component-model Thread is registered for " +
			"this agent, so there is no index to return (the reference reserves 0, so it is not an answer)"}
	}
}
