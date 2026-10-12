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

func (h *asyncHandles) leaveHost() {
	h.mu.Lock()
	h.inHost--
	h.mu.Unlock()
}

// releaseRun undoes one acquireRun: a nested entry only drops the depth, and the last one gives the slot
// up. The state is cleared under the lock and the channel operation is after it (§4 B-MM-3: no engine
// lock across a channel operation).
func (h *asyncHandles) releaseRun() {
	h.mu.Lock()
	h.runDepth--
	if h.runDepth > 0 {
		h.mu.Unlock()
		return
	}
	h.runChain = nil
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
// **Only a slot the caller's chain holds is released**, found through the caller's Thread record (by the
// index its stack carries), and only for an implicit Thread. A core-level `Invoke` that bypasses the
// component has no record (ADR 0073's population). A created Thread runs under its chain's hold (ADR 0100's
// amendment) and releases nothing, which is #977's open edge and is left for #977. For both this returns
// `fn` as is.
//
// **The whole slot is released, whatever the re-entry depth**, and the depth is restored on the re-take.
// A nested entry suspending means the chain suspends: the outer entries of the same chain are blocked in
// host code (the synchronous cross-component arm), not executing.
//
// **Both the release and the re-take are inside the excursion**, so the agent is counted blocked — at a
// safepoint, SP-2 — while it waits for its event and while it waits for another task to suspend. If the
// wake's poll then ends the agent, it unwinds holding the slot, and the entry that took it releases it.
func (h *asyncHandles) suspending(c *interp.CanonCaller, fn func() error) func() error {
	var rec *cmThread
	if h.threads != nil {
		rec = h.threads.current(c)
	}
	return func() error {
		h.mu.Lock()
		held := rec != nil && rec.implicit && rec.chain != nil && h.runChain == rec.chain
		var ch *runChain
		var depth int
		if held {
			ch, depth = h.runChain, h.runDepth
			h.runChain, h.runDepth = nil, 0
		}
		h.mu.Unlock()
		if !held {
			return fn()
		}
		<-h.runSlot
		err := fn()
		h.runSlot <- struct{}{}
		h.mu.Lock()
		h.runChain, h.runDepth = ch, depth
		h.mu.Unlock()
		return err
	}
}
