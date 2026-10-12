// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import "github.com/scttfrdmn/burroughs/internal/interp"

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

// acquireRun takes the run slot for the agent whose component-model Thread index is `holder`. A non-nil
// `abort` ends the wait without the slot — `false`, and nothing to release — and is passed only where an
// entry may still be abandoned, which is before the agent has ever entered (enterTask's rule).
func (h *asyncHandles) acquireRun(holder uint32, abort <-chan struct{}) bool {
	select {
	case h.runSlot <- struct{}{}:
	case <-abort:
		return false
	}
	h.mu.Lock()
	h.runHolder = holder
	h.mu.Unlock()
	return true
}

// releaseRun gives the run slot up. The holder is cleared under the lock and the channel operation is
// after it (§4 B-MM-3: no engine lock across a channel operation).
func (h *asyncHandles) releaseRun() {
	h.mu.Lock()
	h.runHolder = 0
	h.mu.Unlock()
	<-h.runSlot
}

// suspending wraps the body of a `c.Blocking` excursion at a point where the reference SUSPENDS the calling
// thread: the run slot is released for `fn`'s duration, so another task of the instance can execute, and
// taken back before the excursion ends. Used as `c.Blocking(h.suspending(c, fn))`.
//
// **A wrapper of the excursion's body, not a replacement for `Blocking`**, so every call site still reads
// `c.Blocking(` — the shape `TestEveryCanonFunctionsWaitIsInsideABlockingExcursion` checks for H-1. The
// first draft was a `blockSuspending` that called `Blocking` itself, which the control refused: the waits
// were no longer lexically inside a `Blocking(...)` call. Hiding `Blocking` behind another name is what the
// control exists to catch, so the shape moved rather than the control.
//
// **Only a slot the caller holds is released**, found by the component-model Thread index its stack
// carries. A core-level `Invoke` that bypasses the component holds none (ADR 0073's population), and a
// created Thread runs under its chain's hold (ADR 0100's amendment), so for both this returns `fn` as is.
//
// **Both the release and the re-take are inside the excursion**, so the agent is counted blocked — at a
// safepoint, SP-2 — while it waits for its event and while it waits for another task to suspend. If the
// wake's poll then ends the agent, it unwinds holding the slot, and the entry that took it releases it.
func (h *asyncHandles) suspending(c *interp.CanonCaller, fn func() error) func() error {
	idx, ok := c.CMThreadIndex()
	return func() error {
		h.mu.Lock()
		held := ok && idx != 0 && h.runHolder == idx
		if held {
			h.runHolder = 0
		}
		h.mu.Unlock()
		if !held {
			return fn()
		}
		<-h.runSlot
		err := fn()
		h.runSlot <- struct{}{}
		h.mu.Lock()
		h.runHolder = idx
		h.mu.Unlock()
		return err
	}
}
