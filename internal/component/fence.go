// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

// The cross-component fence (ADR 0095's amendment, F1).
//
// The reference has one runner. A callee past its first suspension is resumed only by `Store.tick`, at nesting
// depth zero, so it never runs while its caller's instance executes, and its caller never resumes while it is
// mid-segment. Burroughs runs a cross-component callee on its own goroutine, so that has to be built: #1020's
// mechanisms (a) and (b) are the outcomes its absence allowed.
//
// The fence is per CALLER instance, and it has two sides:
//   - **writers** are the instance's own guest execution: every take of its run slot, from the take to the
//     free. A writer waits until no callee segment is in flight, so a caller's resumption waits for its
//     callees' segments (the symmetric half, without which (b) recurs);
//   - **readers** are its callees' segments past their first suspension. A reader waits until no writer
//     holds or awaits the slot, and segments run in parallel with one another.
//
// **The fence is taken before the run slot**, on both sides, so a writer waiting on it holds no slot a
// reader needs. A reader's own result store and its calls back into the caller's instance run on the
// segment's chain, and a writer whose chain is an in-flight segment's is admitted (the re-entry rule): in
// the reference that is the same thread.
//
// All of it is under `asyncHandles.mu`, and `wake` is closed and replaced on every change that could
// admit someone, the waitable set's `wake` idiom.
type fence struct {
	segs    int               // in-flight callee segments whose caller is this instance
	chains  map[*runChain]int // their chains, for the re-entry rule
	writers int               // slot takes in flight: held, or awaiting the fence to take it
	wake    chan struct{}     // closed on every change that could admit a waiter
	kicks   []*asyncHandles   // callee instances whose scheduler skipped a Thread on this fence
}

// fenceWakeLocked is the channel to wait on for the next change. The caller holds h.mu.
func (h *asyncHandles) fenceWakeLocked() chan struct{} {
	if h.fence.wake == nil {
		h.fence.wake = make(chan struct{})
	}
	return h.fence.wake
}

// fenceSignalLocked wakes every waiter. The caller holds h.mu.
func (h *asyncHandles) fenceSignalLocked() {
	if h.fence.wake != nil {
		close(h.fence.wake)
		h.fence.wake = nil
	}
}

// writerAdmittedLocked reports whether a writer on chain ch may take the slot now: no segment is in flight,
// or ch is one of theirs. The caller holds h.mu.
func (h *asyncHandles) writerAdmittedLocked(ch *runChain) bool {
	return h.fence.segs == 0 || (ch != nil && h.fence.chains[ch] > 0)
}

// fenceWriterEnter waits until a writer on chain ch is admitted, then counts it. False, and nothing counted,
// when abort fires first. A nil abort never fires.
func (h *asyncHandles) fenceWriterEnter(ch *runChain, abort <-chan struct{}) bool {
	for {
		h.mu.Lock()
		if h.writerAdmittedLocked(ch) {
			h.fence.writers++
			h.mu.Unlock()
			return true
		}
		w := h.fenceWakeLocked()
		h.mu.Unlock()
		// Waited on with no lock held (§4 B-MM-3): each pass locks, checks and unlocks before any channel
		// operation.
		select {
		case <-w:
		case <-abort:
			return false
		}
	}
}

// fenceWriterTry counts a writer on chain ch if it is admitted now. When it is not, it returns the channel
// to wait on for the next change, for a caller that waits inside a `select`.
func (h *asyncHandles) fenceWriterTry(ch *runChain) (admitted bool, wake <-chan struct{}) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.writerAdmittedLocked(ch) {
		return false, h.fenceWakeLocked()
	}
	h.fence.writers++
	return true, nil
}

// fenceWriterLeave uncounts one writer. The last one out wakes waiting readers and kicks every callee
// scheduler that skipped a Thread on this fence.
func (h *asyncHandles) fenceWriterLeave() {
	h.mu.Lock()
	h.fence.writers--
	var kicks []*asyncHandles
	if h.fence.writers == 0 {
		h.fenceSignalLocked()
		kicks, h.fence.kicks = h.fence.kicks, nil
	}
	h.mu.Unlock()
	for _, k := range kicks {
		k.kick()
	}
}

// fenceReaderEnter waits until no writer is in flight, then counts a segment on chain ch and reports true.
//
// **It gives up, counting nothing, when `exempt` turns true** while it waits: the task has entered upstream
// #726's hand-back window, where its caller's `subtask.cancel` waits for it inside the caller's own call.
// An async canceller waits there holding its slot, so a reader still waiting would wait for its own caller.
// Arming the transfer signals this fence (`liftAsAsyncImpl`'s cancel), so the check below sees it. `exempt`
// runs with this lock released, because it takes the task's own instance's lock.
func (h *asyncHandles) fenceReaderEnter(ch *runChain, exempt func() bool) bool {
	for {
		h.mu.Lock()
		if h.fence.writers == 0 {
			h.readerCountLocked(ch)
			h.mu.Unlock()
			return true
		}
		w := h.fenceWakeLocked()
		h.mu.Unlock()
		if exempt != nil && exempt() {
			return false
		}
		<-w
	}
}

// fenceSignal wakes every waiter on this fence: a reader that should re-check its exemption.
func (h *asyncHandles) fenceSignal() {
	h.mu.Lock()
	h.fenceSignalLocked()
	h.mu.Unlock()
}

// fenceReaderTry counts a segment on chain ch if no writer is in flight. When one is, it registers `kick`
// to be kicked when the last writer leaves: a scheduler, which must not wait here while it holds its own
// instance's slot.
func (h *asyncHandles) fenceReaderTry(ch *runChain, kick *asyncHandles) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.fence.writers > 0 {
		h.fence.kicks = append(h.fence.kicks, kick)
		return false
	}
	h.readerCountLocked(ch)
	return true
}

func (h *asyncHandles) readerCountLocked(ch *runChain) {
	h.fence.segs++
	if h.fence.chains == nil {
		h.fence.chains = make(map[*runChain]int)
	}
	h.fence.chains[ch]++
}

// fenceReaderLeave uncounts a segment on chain ch. The last one out wakes waiting writers.
func (h *asyncHandles) fenceReaderLeave(ch *runChain) {
	h.mu.Lock()
	h.fence.segs--
	h.fence.chains[ch]--
	if h.fence.chains[ch] <= 0 {
		delete(h.fence.chains, ch)
	}
	if h.fence.segs == 0 {
		h.fenceSignalLocked()
	}
	h.mu.Unlock()
}
