// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"fmt"
	"sync"

	"github.com/scttfrdmn/burroughs/internal/interp"
)

// gate:async slice-1 increment 2a-i-B: the async `canon lower`'s BLOCKING arm — the subtask substrate and
// the per-instance async handle table.
//
// When an async-lowered import's callee does not resolve inline, `canon_lower` registers a subtask in the
// instance handle table and returns `[state | (subtaski<<4)]` (definitions.py:2235–2251). The guest then
// creates a waitable set (`waitable-set.new`), joins the subtask to it (`waitable.join`), and blocks on
// `waitable-set.wait` until the subtask resolves and delivers a `(SUBTASK, subtaski, state)` event
// (async_waitset.go, increment 2a-i-B-2). Subtasks and waitable-sets share ONE index space per instance —
// the model's `inst.handles` — so a `subtaski` and an `si` never collide.

// subtaskState mirrors definitions.py Subtask.State (def:801–806). STARTING/STARTED/RETURNED are the
// non-cancel lifecycle; the two CANCELLED states (3, 4) land with subtask.cancel (increment 4). Which
// CANCELLED state a cancel produces is chosen by the subtask's state at the time it resolves: STARTING ->
// CANCELLED_BEFORE_STARTED, STARTED -> CANCELLED_BEFORE_RETURNED (async_lower.go's onResolve).
type subtaskState uint32

const (
	subtaskStarting                subtaskState = 0
	subtaskStarted                 subtaskState = 1
	subtaskReturned                subtaskState = 2
	subtaskCancelledBeforeStarted  subtaskState = 3
	subtaskCancelledBeforeReturned subtaskState = 4
)

// subtask is a pending async-lowered call. The blocking arm registers it in the instance table (which
// assigns `index`); the impl holds the resolver (onResolve, in async_lower.go) that later flips it to
// RETURNED, marks `resolved`, and — if the subtask has been joined to a waitable set — wakes the set's
// waiters. `delivered` guards against a resolved subtask's event being taken twice. All fields are guarded
// by the owning asyncHandles' mutex (resolution can fire on the impl's goroutine while a guest agent is
// parked in waitable-set.wait).
type subtask struct {
	state                 subtaskState
	index                 int          // this subtask's handle index (the `subtaski` the event carries)
	resolved              bool         // set by onResolve
	delivered             bool         // set when the SUBTASK event has been taken by a waitable-set.wait
	set                   *waitableSet // the set it is joined to, if any (nil until waitable.join)
	cancellationRequested bool         // set by subtask.cancel before it invokes onCancel
	onCancel              func()       // the impl's request_cancellation, captured at lower (async_lower.go)
}

// asyncHandles is a component instance's async handle table — subtasks and waitable-sets in one index
// space, index 0 a reserved nil sentinel so the first add returns 1 (matching the reference model's Table
// and the oracle's `subtaski=1`). The mutex guards the whole table AND the subtask/waitable-set state it
// holds: resolution runs on the impl's goroutine while a sibling agent is parked in waitable-set.wait, so
// the arming write and the parked read are synchronized here rather than per-object.
type asyncHandles struct {
	mu      sync.Mutex
	entries []any // entries[0] is the reserved nil sentinel; each is *subtask or *waitableSet
}

// pendingEventLocked delivers a resolved subtask's (SUBTASK, subtaski, state) event once, mirroring the
// model's subtask_event closure (def:2243–2246) — the event carries the subtask's live index and state.
// subtask satisfies the waitable interface (async_waitset.go). The caller holds asyncHandles.mu.
func (st *subtask) pendingEventLocked() (event, bool) {
	if st.resolved && !st.delivered {
		st.delivered = true
		return event{code: eventSubtask, p1: uint32(st.index), p2: uint32(st.state)}, true
	}
	return event{}, false
}

// joinTo records the set this subtask belongs to, so onResolve can wake its waiters.
func (st *subtask) joinTo(s *waitableSet) { st.set = s }

func newAsyncHandles() *asyncHandles { return &asyncHandles{entries: []any{nil}} }

// addLocked registers h and returns its index (>= 1). The caller holds mu.
func (t *asyncHandles) addLocked(v any) int {
	t.entries = append(t.entries, v)
	return len(t.entries) - 1
}

// packSubtaskWait encodes `canon_lower`'s blocking return `[state | (subtaski<<4)]` (def:2251). The model
// asserts 0 <= state < 2^4 and 0 < subtaski < 2^28, so the two fields never overlap.
func packSubtaskWait(state subtaskState, subtaski int) int32 {
	return int32(uint32(state) | uint32(subtaski)<<4)
}

// subtaskCancel implements `canon subtask.cancel` (0x06, definitions.py:2414). It requests cancellation of a
// not-yet-resolved subtask and invokes the impl's cancel handler (the onCancel captured at lower, inert
// since 2a-i-B-1 until this increment). If the callee resolves during the cancel, the subtask reaches a
// CANCELLED state — CANCELLED_BEFORE_STARTED if it had not started, CANCELLED_BEFORE_RETURNED if it had —
// and the state is returned; otherwise BLOCKED.
//
// The model yields here (thread.yield_) to let the single-threaded callee run and observe the request;
// Burroughs has no yield — a callee runs on its own goroutine — so an unresolved cancel simply returns
// BLOCKED and the guest awaits the SUBTASK event, as it does for any unresolved subtask (a substrate
// mapping like per-caller context, ADR 0050, not a transliteration). onCancel is invoked WITHOUT the table
// lock held: it may call onResolve, which takes the lock, so holding it here would deadlock — the same
// discipline the blocking arm uses for the impl and its resolver.
func subtaskCancel(h *asyncHandles) interp.CanonFunc {
	return func(_ *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("component: subtask.cancel: got %d args, want (i)", len(args))
		}
		i := uint32(args[0].Bits)
		h.mu.Lock()
		st, ok := handleAt[*subtask](h, i)
		if !ok {
			h.mu.Unlock()
			return nil, fmt.Errorf("component: subtask.cancel: handle %d is not a subtask", i)
		}
		switch {
		case st.delivered:
			h.mu.Unlock()
			return nil, &interp.Trap{Reason: fmt.Sprintf("subtask.cancel: subtask %d already resolve-delivered", i)}
		case st.cancellationRequested:
			h.mu.Unlock()
			return nil, &interp.Trap{Reason: fmt.Sprintf("subtask.cancel: subtask %d already has a cancellation requested", i)}
		case st.set != nil:
			h.mu.Unlock()
			return nil, &interp.Trap{Reason: fmt.Sprintf("subtask.cancel: subtask %d is joined to a waitable set", i)}
		}
		if st.resolved {
			state := st.state // already resolved before the cancel — deliver its state (get_pending_event)
			st.delivered = true
			h.mu.Unlock()
			return []interp.Value{interp.I32(int32(state))}, nil
		}
		st.cancellationRequested = true
		onCancel := st.onCancel
		h.mu.Unlock()

		if onCancel != nil {
			onCancel() // may call onResolve (which locks); must run without the table lock held
		}

		h.mu.Lock()
		defer h.mu.Unlock()
		if !st.resolved {
			bits := uint32(asyncBlocked) // the callee did not resolve inline; the guest awaits the event
			return []interp.Value{interp.I32(int32(bits))}, nil
		}
		st.delivered = true
		return []interp.Value{interp.I32(int32(st.state))}, nil
	}
}
