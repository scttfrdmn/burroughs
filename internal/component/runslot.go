// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"context"

	"github.com/scttfrdmn/burroughs/internal/interp"
)

// The run slot: one task of a component instance executes at a time (ADR 0101, #975).
//
// The reference runs one thread at a time — `Store.tick` resumes one, and `Thread.resume` runs it until it
// suspends — so two tasks of one instance interleave only at suspension points. Burroughs runs tasks on
// goroutines, so that property has to be built: `asyncHandles.runSlot` is held by the agent executing the
// instance's guest code, taken at every entry and released only where the reference suspends.
//
// **Until #975 the sync-lift path took nothing**, and two `CallValues` on one instance's sync export
// executed guest code in parallel over one unshared memory — measured, and witnessed by
// `TestTwoCallsIntoOneInstanceNeverExecuteAtOnce`.

// runChain is one call chain: the reference's thread, seen as the stack of synchronous calls it is making.
// Its only property is identity. A chain may hold several instances' slots at once — parent and child, in
// a synchronous cross-component call — and may re-enter one it already holds.
//
// # Why a chain and not an agent index
//
// #976 recorded the holder by component-model Thread index. That self-deadlocked a valid chain: parent
// core instance P2 calls a child synchronously, and the child calls back into the parent through a lift
// of the parent's other core instance P1 (`walker.compArgRef` hands a child any parent func). P1 and P2
// share the parent's slot. The inner entry waited for a slot its own call chain held, on its own
// goroutine. wasmtime 49.0.2 returns 8 for `reenter.wasm`; Burroughs returned 8 before #976 and hung after
// it. The reference has no reentrance rule at this pin (`canon_lower` checks only `may_leave`), and the
// chain is one thread there, so nothing waits.
type runChain struct{ _ byte }

// runChainKey carries a chain into a nested synchronous call through the call's context.
type runChainKey struct{}

// withRunChain returns ctx carrying ch, so the callee's entry recognises its caller's chain.
func withRunChain(ctx context.Context, ch *runChain) context.Context {
	if ch == nil {
		return ctx
	}
	return context.WithValue(ctx, runChainKey{}, ch)
}

// runChainFrom is the chain ctx carries, or a new one: an entry that arrives without a chain starts one.
func runChainFrom(ctx context.Context) *runChain {
	if ch, ok := ctx.Value(runChainKey{}).(*runChain); ok && ch != nil {
		return ch
	}
	return &runChain{}
}

// acquireRun takes the run slot for chain `ch`, recording on `rec` (nil for none) that its agent executes
// under `ch`. If `ch` already holds the slot this is a nested entry, and it re-enters rather than waiting.
// A non-nil `abort` ends the wait without the slot — `false`, and nothing to release — and is passed only
// where an entry may still be abandoned, which is before the agent has ever entered (enterTask's rule).
func (h *asyncHandles) acquireRun(ch *runChain, rec *cmThread, abort <-chan struct{}) bool {
	h.mu.Lock()
	if ch != nil && h.runChain == ch { // non-nil: a free slot's nil chain must not read as re-entry
		h.runDepth++
		if rec != nil {
			rec.chain = ch
		}
		h.mu.Unlock()
		return true
	}
	h.mu.Unlock()
	select {
	case h.runSlot <- struct{}{}:
	case <-abort:
		return false
	}
	h.mu.Lock()
	h.runChain, h.runDepth = ch, 1
	if rec != nil {
		rec.chain = ch
	}
	h.mu.Unlock()
	return true
}

// enterHost and leaveHost bracket host code the run slot's holder runs on its own goroutine — an async
// lower's impl, a subtask's onCancel — during which a result store needs no slot (`asyncHandles.inHost`).
func (h *asyncHandles) enterHost() {
	h.mu.Lock()
	h.inHost++
	h.mu.Unlock()
}

// leaveHost lowers the count, and on the way out of the outermost host call applies every result store
// queued meanwhile (`asyncHandles.hostStores`), here, on the holder's goroutine, before its guest resumes.
// That is the reference's order: another thread's resolution cannot run during this thread's host call,
// and does run before this thread executes guest code again.
//
// **Each store runs with `h.mu` RELEASED.** A store's lowering may call the caller's `cabi_realloc`, which is
// guest code, and guest code must not run under an engine lock (`asyncLowerFunc`'s `apply`). So the queue
// is popped under the lock and each store applied outside it. A store that arrives meanwhile finds
// `inHost` at zero and takes the run slot, which this holder still has, so it waits its turn rather than
// joining this queue.
func (h *asyncHandles) leaveHost() {
	h.mu.Lock()
	h.inHost--
	for h.inHost == 0 && len(h.hostStores) > 0 {
		apply := h.hostStores[0]
		h.hostStores = h.hostStores[1:]
		h.mu.Unlock()
		apply() // takes h.mu for its own bookkeeping, never across its guest-memory work
		h.mu.Lock()
	}
	h.mu.Unlock()
}

// releaseRunFor undoes one acquireRun by chain `ch`: a nested entry only drops the depth, and the last one
// gives the slot up. The state is cleared under the lock and the channel operation is after it (§4
// B-MM-3: no engine lock across a channel operation).
//
// **Only if `ch` holds the slot** (ADR 0101's Option A). Under A a Thread parked in a handoff or waiting
// holds nothing, and one the wake ENDS there unwinds without taking the slot back, so its entry's release
// must not drain a token that another Thread or the scheduler holds. Until A every entry held the slot to
// its end, and the release was unconditional.
//
// **Freeing the slot kicks the scheduler when Threads are ready**: the `Store.tick` equivalent (point 5).
func (h *asyncHandles) releaseRunFor(ch *runChain) {
	h.mu.Lock()
	if ch == nil || h.runChain != ch {
		h.mu.Unlock()
		return
	}
	h.runDepth--
	if h.runDepth > 0 {
		h.mu.Unlock()
		return
	}
	h.runChain = nil
	h.mu.Unlock()
	<-h.runSlot
	h.kick()
}

// kick asks the scheduler to run the ready queue, after the slot has been freed with Threads ready: the
// reference's `Store.tick`, which resumes a ready Thread whenever none is running (ADR 0101's Option A,
// point 5). A non-blocking send on a capacity-1 channel, so kicks coalesce.
//
// **No wakeup is lost**: every append to the ready queue is made under the slot, so a queue that is
// non-empty when the slot is freed is seen here, and one appended to later is seen by that holder's own
// free. The scheduler's own free does not kick (`cmScheduler.free`), so a stale kick cannot re-arm itself.
func (h *asyncHandles) kick() {
	tt := h.threads
	if tt == nil {
		return
	}
	tt.mu.Lock()
	s, n := tt.sched, len(tt.ready)
	tt.mu.Unlock()
	if s == nil || n == 0 {
		return
	}
	select {
	case s.tick <- struct{}{}:
	default:
	}
}

// suspending wraps the body of a `c.Blocking` excursion at a point where the reference SUSPENDS the calling
// thread: `waitable-set.wait` and a sync `subtask.cancel`, both `wait_until`, which is a `block()`. The run
// slot is given up for `fn`'s duration and taken back before the excursion ends. Used as
// `c.Blocking(h.suspending(c, fn))`, and `fn` receives an `abort` channel, closed when a trap or the wake
// ends the wait.
//
// **A wrapper of the excursion's body, not a replacement for `Blocking`**, so every call site still reads
// `c.Blocking(` — the shape `TestEveryCanonFunctionsWaitIsInsideABlockingExcursion` checks for H-1. The
// first draft was a `blockSuspending` that called `Blocking` itself, which the control refused: the waits
// were no longer lexically inside a `Blocking(...)` call. Hiding `Blocking` behind another name is what the
// control exists to catch, so the shape moved rather than the control.
//
// **Only a slot the caller's chain holds is given up**, found through the caller's Thread record (by the
// index its stack carries). A core-level `Invoke` that bypasses the component has no record (ADR 0073's
// population), and for it this returns `fn` as is.
//
// **Under ADR 0101's Option A the wait is a handoff, not a bare release** (point 4). In the reference the
// chain ends at the `block()`, and `Store.tick` runs another ready Thread.
//   - Where the instance has a scheduler, the slot goes to it with a `detach`, and it runs the ready queue
//     while this Thread waits. That is #977's residual, witnessed by
//     `TestAnotherThreadOfTheChainRunsWhileOneWaits`.
//   - Where there is no scheduler, nothing can be ready, and the slot is freed.
//
// Before A the wait freed the slot (#977) but the scheduler still waited on the Thread's handoff, so no other
// Thread of the chain ran.
//
// **The whole slot is given up, whatever the re-entry depth**, and the depth is restored on the re-take.
// A nested entry suspending means the chain suspends: the outer entries of the same chain are blocked in
// host code (the synchronous cross-component arm), not executing.
//
// **The re-take is by contention**, as the reference's waiting Thread is picked by a later `tick`. It is
// inside the excursion, so the agent is counted blocked (at a safepoint, SP-2) while it waits.
//
// **A wait ended by a trap or the wake is NOT followed by a re-take** (point 7). The Thread unwinds holding
// nothing, and its entry's `releaseRunFor` finds another holder and releases nothing. ADR 0101's text first
// said the slot is taken back "whatever fn returned". Under A that would have the ended Thread contend for
// a slot it will not use, and at `Close` wait on a Thread the close has not yet ended. The build amends the
// sentence.
func (h *asyncHandles) suspending(c *interp.CanonCaller, ready func() bool, fn func(abort <-chan struct{}) error) func() error {
	var rec *cmThread
	tt := h.threads
	if tt != nil {
		rec = tt.current(c)
	}
	return func() error {
		h.mu.Lock()
		held := rec != nil && rec.chain != nil && h.runChain == rec.chain
		if held {
			rec.saved.chain, rec.saved.depth, rec.saved.lift = h.runChain, h.runDepth, h.lift
			h.runChain, h.runDepth = nil, 0
		}
		h.mu.Unlock()
		if !held {
			return fn(nil)
		}
		// **ADR 0101's Option A, point 4: the wait is a `block()`, so it is a handoff.** Where the instance
		// has a scheduler, the slot goes to it with a `detach`, and it runs the ready queue while this Thread
		// waits (#977's residual). Where it has none, nothing can be ready, and the slot is simply freed.
		tt.mu.Lock()
		rec.state = threadWaiting
		rec.waitAbort = make(chan struct{})
		rec.abortErr = nil
		rec.waitReady = ready
		abort, s := rec.waitAbort, tt.sched
		tt.mu.Unlock()
		// **The sync tail with no scheduler (#1005):** no Thread exists, so nothing can be a candidate except
		// the waiter's own event or another task of the instance; `syncTail` decides, and a trapped waiter
		// never gave the slot up, so it is restored as it was.
		if s == nil {
			for _, w := range tt.syncTail(h) {
				if w != rec {
					continue
				}
				h.mu.Lock()
				h.runChain, h.runDepth, h.lift = rec.saved.chain, rec.saved.depth, rec.saved.lift
				rec.saved.chain, rec.saved.depth, rec.saved.lift = nil, 0, nil
				h.mu.Unlock()
				tt.mu.Lock()
				terr := rec.abortErr
				rec.state = threadRunning
				tt.mu.Unlock()
				return terr
			}
		}
		if s != nil {
			h.mu.Lock()
			h.schedHolds = true
			h.mu.Unlock()
			select {
			case s.back <- handoff{from: rec, detach: true}:
			case <-s.stop:
			}
		} else {
			<-h.runSlot
		}
		err := fn(abort)
		tt.mu.Lock()
		aborted := rec.state != threadWaiting
		if !aborted {
			rec.state = threadRunning
		}
		abortErr := rec.abortErr
		tt.mu.Unlock()
		if aborted {
			// **Ended while waiting: the slot is NOT taken back.** The Thread unwinds holding nothing, and
			// its entry's `releaseRunFor` finds another holder and releases nothing.
			return abortErr
		}
		// Taken back by contention, as the reference's waiting Thread is picked by a later `tick`.
		h.runSlot <- struct{}{}
		h.mu.Lock()
		h.runChain, h.runDepth, h.lift = rec.saved.chain, rec.saved.depth, rec.saved.lift
		rec.saved.chain, rec.saved.depth, rec.saved.lift = nil, 0, nil
		h.mu.Unlock()
		return err
	}
}
