// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"context"
	"errors"
	"fmt"

	"github.com/scttfrdmn/burroughs/internal/interp"
)

// A component instance's spawned agents (#1336, ADR 0130, on Scott's ruling 1 on #1332: D-F3 = (b)).
//
// # What an agent is here
//
// A spawned agent is an `interp` spawned thread (§2 T-1): OS-locked, with its own T-6 globals, a member of its core
// instance's world, ended by that instance's `Close`. In a component it is also **a thread of its spawner's task**:
// a `cmThread` with `agent` set, whose `task` and `owner` are its spawner's, registered in the instance's Thread
// table, its index carried on its own stack. So every built-in it calls finds its own record, as a lift's implicit
// Thread's calls find theirs.
//
// # Two disciplines, chosen by the instance
//
//   - **A free instance** (`asyncHandles.agentsRunFree`: its canonical memory is shared) runs its agents outside the
//     run slot. They execute guest code in parallel with the slot's holder and with one another, wait without
//     giving a slot up (`threadTable.agentWait`), and open in-host windows of their own (`cmThread.window`).
//   - **Every other instance** keeps ADR 0101 exactly. An agent takes the run slot at entry as a chain of its own,
//     gives it up where the reference suspends, as any slot holder does, and releases it at exit.
//
// # Who starts one
//
// `asyncHandles.spawnAgent` is the mechanism. A guest reaches it through the component spawn door,
// `burroughs:threads/spawn@0.1.0#spawn` (spawndoor.go, #1337, ADR 0131), which starts the importing core instance's
// `mstart`. This said until #1337 that no guest reached it, which was true while E2's witnesses, which call it from a
// test host import, were its only callers.

// freeAgent returns rec when it is a spawned agent of a free instance, which holds no slot and has its own in-host
// window, and nil otherwise: for a slot holder, for no record, and for every agent of an instance that keeps ADR
// 0101.
func (h *asyncHandles) freeAgent(rec *cmThread) *cmThread {
	if rec == nil || !rec.agent || !h.agentsRunFree {
		return nil
	}
	return rec
}

// freeAgentOf is freeAgent for the caller c's record, found by the index its stack carries.
func (h *asyncHandles) freeAgentOf(c *interp.CanonCaller) *cmThread {
	if h.threads == nil {
		return nil
	}
	return h.freeAgent(h.threads.current(c))
}

// noteCanonMemory records one canonical memory option the walk admitted, and re-derives whether the instance is
// free: at least one shared canonical memory, which only `gate:cm-shared-memory` admits (ADR 0129), and no
// unshared one. **An instance with both is not free**, because its agents could lower into the unshared memory in
// parallel, which is a race on a memory no agent shares (ADR 0130's §1).
func (w *walker) noteCanonMemory(shared bool) {
	if w.async == nil {
		return // a walk with no async table (`Component.instantiate`'s) runs no agent
	}
	m := &w.async.canonMemories
	if shared {
		m.shared++
	} else {
		m.unshared++
	}
	w.async.agentsRunFree = m.shared > 0 && m.unshared == 0
}

// spawnAgent starts an agent at the core export `entry` of `core`, with argument `arg`, as a thread of the
// calling Thread's task (ADR 0130's §2). It returned the agent's `interp` thread id until #1337, whose door, its one
// production caller, returns none (`func(arg: u32)`), so nothing read it.
//
// **Refused by name with no caller record**: a core-level call that bypassed the component has no task for the
// agent to join, for `thread.new-indirect`'s reason. **And on a locked store**: no thread of a trapped instance
// starts (ADR 0105).
//
// The record is registered before the agent's goroutine starts, so the agent is a sync-tail candidate from the
// moment it exists, and a spawn the interp refuses leaves the table as it found it.
func (h *asyncHandles) spawnAgent(c *interp.CanonCaller, core *interp.Instance, entry string, arg int32) error {
	tt := h.threads
	var parent *cmThread
	if tt != nil {
		parent = tt.current(c)
	}
	if parent == nil {
		return fmt.Errorf("%w: an agent spawned outside any component-model Thread: the call did not enter "+
			"through a lift, so there is no task for the agent to be a thread of (ADR 0130)", ErrUnsupportedForm)
	}
	if err := h.lock.locked(); err != nil {
		return err
	}
	rec := &cmThread{agent: true, state: threadRunning, task: parent.task, owner: parent.owner}
	rec.chain = &rec.ownChain
	idx := tt.add(rec)
	free := h.agentsRunFree
	hooks := interp.AgentHooks{
		// NEUTER, run: carry 0, and TestAgentsOfASharedMemoryInstanceExecuteAtOnce's agents trap at thread.index
		// ("no component-model Thread is registered for this agent"), so run never joins them.
		CMThread: idx,
		Exit:     func(err error) { tt.agentEnded(h, rec, free, err) },
	}
	if !free {
		// ADR 0101 exactly: the agent is one more chain of the instance, and takes the slot before its first guest
		// instruction. The wait is abandoned on the agent's context, which Close cancels.
		//
		// NEUTER (ADR 0130's rollback): set this arm for a free instance too, and
		// TestAgentsOfASharedMemoryInstanceExecuteAtOnce reads one executing agent, its spawned agents parked here.
		hooks.Enter = func(ctx context.Context) error {
			if !h.acquireRun(rec.chain, rec, ctx.Done()) {
				return fmt.Errorf("%w: Close ended it while it waited for the run slot", errThreadKilled)
			}
			return nil
		}
	}
	if _, err := core.SpawnAgent(entry, arg, hooks); err != nil {
		tt.remove(idx)
		return err
	}
	return nil
}

// agentEnded is an agent's Exit hook, on its own goroutine: release a slot its chain holds (an ADR 0101 instance's),
// unregister it, and route a trap to its owner, as a created Thread's trap is routed (ADR 0101's Option A, point
// 7), which locks the store (ADR 0105).
//
// **An end Close caused is not a trap**: the agent was ended, and an instance being closed has nobody to report
// to.
func (tt *threadTable) agentEnded(h *asyncHandles, rec *cmThread, free bool, err error) {
	if !free {
		h.releaseRunFor(rec.chain) // releases only a slot this chain holds (Option A)
	}
	tt.remove(rec.index)
	if !isTrap(err) || agentEndedByTeardown(err) {
		return
	}
	h.mu.Lock()
	closing := h.hostWaitsStopped
	h.mu.Unlock()
	if closing {
		return
	}
	tt.routeFault(rec.owner, err)
}

// agentEndedByTeardown reports an agent's end that Close, a wake, or another call's trap caused, rather than a trap
// of its own.
func agentEndedByTeardown(err error) bool {
	return errors.Is(err, errThreadKilled) || errors.Is(err, interp.ErrTerminated) ||
		errors.Is(err, ErrInstanceLocked) || errors.Is(err, context.Canceled)
}

// agentWait is a free agent's wait (ADR 0130's §3): the reference's `wait_until`, with no slot to give up.
//
// It is marked as a slot holder's wait is, so the instance's other machinery reads it the same way:
//   - **the sync tail** counts its `ready_func` (`waitReady`) and, while it waits, does not count it as executing.
//     The tail is evaluated here, because this agent may have been a blocked sync task's last candidate, as it is
//     at a slot holder's wait;
//   - **a trap of its owner, and Close,** end it through `waitAbort` (`killParked`, `syncTail`), and it returns the
//     abort's error.
//
// **It never ends its task's first segment** (F0): that is the implicit Thread's segment, and another thread of the
// task blocking does not end it.
func (tt *threadTable) agentWait(h *asyncHandles, a *cmThread, ready func() bool, judged bool, fn func(abort <-chan struct{}) error) error {
	tt.mu.Lock()
	a.state = threadWaiting
	a.waitAbort = make(chan struct{})
	a.abortErr = nil
	a.waitReady = ready
	a.waitJudged = judged
	abort := a.waitAbort
	tt.mu.Unlock()
	tt.syncTail(h)
	err := fn(abort)
	tt.mu.Lock()
	aborted := a.state != threadWaiting
	if !aborted {
		a.state = threadRunning
	}
	abortErr := a.abortErr
	tt.mu.Unlock()
	if aborted {
		return abortErr
	}
	return err
}

// refuseFromAgent refuses a 🧵 form that appends to the ready queue or hands control off, called by a spawned agent
// (ADR 0130's §5, row 4). Those forms are the scheduler's handoffs, which carry the run slot, and an agent is not a
// Thread the scheduler resumes. Refusing them keeps `kick`'s argument whole: every append is made under the slot.
// `thread.index` is not refused.
func refuseFromAgent(rec *cmThread, name string) error {
	if rec == nil || !rec.agent {
		return nil
	}
	return fmt.Errorf("%w: %s called by a spawned agent, which is a thread of its task but not a component-model "+
		"Thread the scheduler resumes (#1336, ADR 0130)", ErrUnsupportedForm, name)
}

// offAgentHeap is the guest heap of a result store applied off its agent's own goroutine, which a free instance
// does (ADR 0130's §3). Its writes are guestHeap's. Its `Realloc` is refused by name: `cabi_realloc` runs on the
// lowering agent's stack, which may be executing guest code on another goroutine at this moment.
//
// NEUTER, run: have Realloc return a pointer (0) instead of the refusal, and
// TestALateReallocStoreInAFreeInstanceIsRefusedByName's late string is stored and its call returns 4.
type offAgentHeap struct{ guestHeap }

func (offAgentHeap) Realloc(_, _, _, _ int) (int, error) {
	return 0, fmt.Errorf("%w: a result store applied off its agent's goroutine would call cabi_realloc on a stack "+
		"that agent may be executing on; in an instance whose canonical memory is shared, a late store that "+
		"lowers a string or a list is refused (#1336, ADR 0130)", ErrUnsupportedForm)
}
