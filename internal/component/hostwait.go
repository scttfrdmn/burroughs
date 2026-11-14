// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
)

// A host wait is the pending half of WASI 0.3's `wait-for` and `wait-until` (#1233, ADR 0121): the timer the
// host started for one subtask, kept in its instance's registry (`asyncHandles.hostWaits`) until it fires, is
// cancelled, or is stopped by the instance's Close.
//
// # One claim decides who resolves
//
// Three parties can end a wait: its timer firing, the guest's `subtask.cancel` (`cancel`), and Close
// (`stopHostWaits`). Each removes the wait from the registry under `asyncHandles.mu`, and only the one that
// removed it acts:
//   - the timer resolves the subtask RETURNED;
//   - the cancel stops the timer and resolves it CANCELLED_BEFORE_RETURNED, before it returns, so both forms
//     of `subtask.cancel` read 4 inline (def:canon_subtask_cancel:"if not async_ and not subtask.resolved():");
//   - Close stops the timer and resolves nothing, since no guest of the instance runs again.
//
// A cancel that loses to the timer does nothing, and the subtask is RETURNED, which is #1009's rule: the arm
// is chosen by the resolution, not by the request.
//
// # The timer's callback is a concurrent caller of onResolve
//
// It runs on a goroutine of the runtime's timer, so it is a new caller of the async lower's `onResolve` from
// outside the instance. ADR 0121 authorises it under ADR 0101's result-store amendment: `onResolve` takes the
// caller instance's run slot, or queues the store while the slot's holder is in host code. Both imports return
// no result, so the store writes no guest memory; it marks the subtask RETURNED and wakes its set under
// `asyncHandles.mu`, which is the edge the blocking arm has always used (ADR 0095's census).

// hostWait is one pending wait. timer is set under `asyncHandles.mu` before the wait is shared, and read only
// by whoever has claimed the wait.
type hostWait struct {
	h       *asyncHandles
	timer   *time.Timer
	resolve func(canon.Value) // the async lower's onResolve
}

// startHostWait registers a wait that resolves after d, and returns the subtask's onCancel. A table Close has
// already stopped starts nothing: no guest of the instance runs again, so nothing could observe the wait.
func (h *asyncHandles) startHostWait(d time.Duration, resolve func(canon.Value)) (onCancel func()) {
	w := &hostWait{h: h, resolve: resolve}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.hostWaitsStopped {
		return nil
	}
	h.hostWaits[w] = struct{}{}
	// Started under the lock, so a callback that fires at once waits for the registration to finish.
	w.timer = time.AfterFunc(d, w.fire)
	return w.cancel
}

// claim removes w from the registry and reports whether this caller removed it. A firing claim is counted in
// hostWaitsFiring under the same lock, so Close can wait it out.
func (h *asyncHandles) claim(w *hostWait, firing bool) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.hostWaits[w]; !ok {
		return false
	}
	delete(h.hostWaits, w)
	if firing {
		h.hostWaitsFiring.Add(1)
		h.hostWaitsResolving++
	}
	return true
}

// fire is the timer's callback: the deadline has passed, so the subtask is RETURNED.
func (w *hostWait) fire() {
	h := w.h
	if !h.claim(w, true) {
		return // cancelled, or stopped by Close
	}
	w.resolve(canon.Value{}) // no result: the store writes nothing and marks RETURNED
	h.mu.Lock()
	h.hostWaitsResolving--
	h.mu.Unlock()
	h.hostWaitsFiring.Done()
}

// cancel is the subtask's onCancel. It runs as host code on the canceller's goroutine (`subtaskCancel`), so its
// resolution is queued and applied before the canceller reads the subtask's state.
//
// NEUTER: return without stopping the timer or resolving, and TestWaitCancelMatchesWasmtime's three sync
// cancels wait out subtaskCancelBound (ErrSubtaskCancelExpired), and its async cancel reads BLOCKED and waits on
// the 1h timer until the hang guard ends it. Stop and resolve without the claim, and TestAHostWaitResolvesOnce
// resolves twice after a fire, and once after Close's stop.
func (w *hostWait) cancel() {
	if !w.h.claim(w, false) {
		return // the timer fired first: the subtask is RETURNED (#1009)
	}
	w.timer.Stop()
	w.resolve(canon.NoResolution()) // on_resolve(None): CANCELLED_BEFORE_RETURNED
}

// stopHostWaits is Close's stop: no wait starts or resolves after it, and every pending timer is stopped. A
// callback that claimed its wait before this is waited out by waitHostWaits.
func (h *asyncHandles) stopHostWaits() {
	h.mu.Lock()
	h.hostWaitsStopped = true
	pending := h.hostWaits
	h.hostWaits = map[*hostWait]struct{}{}
	h.mu.Unlock()
	for w := range pending {
		w.timer.Stop()
	}
}

// waitHostWaits returns once every timer callback that claimed its wait before stopHostWaits has finished
// its store.
func (h *asyncHandles) waitHostWaits() { h.hostWaitsFiring.Wait() }

// hostWaitPendingLocked reports a pending host wait, which is a waker for any park in this instance: the park
// bound must not end a wait something is still going to satisfy (ADR 0121). A callback past its claim and
// still resolving counts: it has left the registry and not yet woken anyone. The caller holds h.mu.
//
// **A host stream producer's read in flight counts too** (ADR 0122): its delivery arms the reader's event, so
// it is a waker for the same reason a timer is. A read blocked on a source that never returns keeps the bound
// re-arming, which is the bound's purpose stated the other way round: that park is not one nothing can satisfy.
//
// NEUTER: drop the producer term, and TestAPendingProducerReadOutlivesTheParkBound reads ErrLiftParkExpired.
//
// **So does a socket's pending work** (ADR 0123): a dial, or a send's Write or finish, whose store arms what a
// guest waits on.
//
// NEUTER: drop the socket term, and TestAPendingDialOutlivesTheParkBound reads ErrLiftParkExpired.
func (h *asyncHandles) hostWaitPendingLocked() bool {
	return len(h.hostWaits) > 0 || h.hostWaitsResolving > 0 || h.producerReads > 0 || h.sockPending > 0
}
