// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

// gate:async increment 3, first slice: the readable end of a `future<T>` — the second waitable kind.
//
// `future<T>` is the degenerate `stream<T>` in the CABI (a single value then closed), so it lands the
// shared copy substrate on the single-value case. A guest reads a host-provided future by calling
// `future.read`, which — on the async path — returns BLOCKED and registers the readable end as a waitable;
// the copy's completion arms a `(FUTURE_READ, index, CopyResult)` event that the SAME waitable-set loop
// (2a-i-B-2) delivers. This reuses that park-and-wake rather than growing a second one: readableFutureEnd
// is a second `waitable` kind, not a parallel wait mechanism (#739). The copy protocol's completion states
// are pinned in the oracle (fixtures.json future_reads) with their differing end state.

// copyResult mirrors definitions.py CopyResult (def:919–922): how a copy ended, the value the guest
// branches on. A future READ is only ever COMPLETED or CANCELLED — DROPPED is a future.write outcome (a
// read can never be dropped), pinned when write lands.
type copyResult uint32

const (
	copyCompleted copyResult = 0
	copyDropped   copyResult = 1
	copyCancelled copyResult = 2
)

// readableFutureEnd is the readable half of a future<T>: a waitable whose copy, once resolved, has a
// (FUTURE_READ, index, result) pending event. Its end state after resolution differs by outcome — DONE for
// COMPLETED, IDLE for CANCELLED (definitions.py future_event, def:2531+) — which the next operation's
// trap-legality reads, so it is tracked here, not derived at delivery. All fields are guarded by the owning
// asyncHandles' mutex.
type readableFutureEnd struct {
	index     int
	set       *waitableSet // the set it is joined to, if any
	resolved  bool         // the copy has ended (COMPLETED or CANCELLED)
	delivered bool         // the FUTURE_READ event has been taken by a waitable-set.wait
	result    copyResult
}

// pendingEventLocked delivers the future read's (FUTURE_READ, index, result) event once. readableFutureEnd
// satisfies the waitable interface (async_waitset.go). The caller holds asyncHandles.mu.
func (e *readableFutureEnd) pendingEventLocked() (event, bool) {
	if e.resolved && !e.delivered {
		e.delivered = true
		return event{code: eventFutureRead, p1: uint32(e.index), p2: uint32(e.result)}, true
	}
	return event{}, false
}

// joinTo records the set this end belongs to, so a completed copy can wake its waiters.
func (e *readableFutureEnd) joinTo(s *waitableSet) { e.set = s }

// resolveLocked ends the read with a result and wakes any set the end is joined to. Reached where the kind
// is known (the copy completion), so the signal path is not on the waitable interface. The caller holds mu.
func (e *readableFutureEnd) resolveLocked(r copyResult) {
	e.resolved = true
	e.result = r
	if e.set != nil {
		e.set.signalLocked()
	}
}
