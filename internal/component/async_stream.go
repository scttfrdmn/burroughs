// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"fmt"

	"github.com/scttfrdmn/burroughs/internal/interp"
)

// gate:async increment 3, stream write-side slice: the writable end of a `stream<T>` — the THIRD waitable
// kind (after subtask and readable future end), delivered through the SAME waitable-set loop (2a-i-B-2),
// not a parallel one. `stream_copy` is `future_copy` generalized to n elements with PROGRESS, so two things
// differ from the future read end and must not be inherited from it (#739):
//
//  1. The event packs `result | (progress<<4)` (definitions.py:2500), not a bare CopyResult — a consumer
//     that takes fewer elements than offered delivers COMPLETED with partial progress, and a codec reading
//     only the low bits is right on a full copy and silently wrong on a partial one.
//  2. A COMPLETED stream write leaves the end IDLE (open for more writes); only DROPPED is DONE. Future's
//     COMPLETED is DONE (single-shot) — opposite end state, so the write end tracks its own.
//
// The guest binds `stream.write` (stdout) but not `stream.read` (p3async-hello's bytes, #739), so this slice
// builds the write side; `stream.read` is refused by name. The oracle is fixtures.json's `stream_writes`.

// writableStreamEnd is the writable half of a stream<T>: a waitable whose write, once a consumer takes some
// elements (COMPLETED) or the readable end drops (DROPPED), has a (STREAM_WRITE, index, result|progress<<4)
// pending event. `srcPtr`/`n`/`caller` are captured by a pending stream.write (the elements the guest
// offered); the host consumer reads up to n and reports how many it took as `progress`. All fields are
// guarded by the owning asyncHandles' mutex.
type writableStreamEnd struct {
	index     int
	set       *waitableSet // the set it is joined to, if any
	resolved  bool         // this write has an event to deliver
	delivered bool         // the STREAM_WRITE event has been taken by a waitable-set.wait
	done      bool         // the end is DONE (dropped); a COMPLETED write leaves it not-done (IDLE, open)
	result    copyResult
	progress  uint32              // elements the consumer took (packed into the event as progress<<4)
	srcPtr    uint32              // where a pending stream.write offered its elements
	n         uint32              // how many elements the guest offered
	hasWrite  bool                // a stream.write is pending on this end
	caller    *interp.CanonCaller // the writing agent's caller (unused this slice; the host consumer reads src)
}

// pendingEventLocked delivers the write's (STREAM_WRITE, index, result|progress<<4) event once — progress
// is the load-bearing difference from the future read end. The caller holds asyncHandles.mu.
func (e *writableStreamEnd) pendingEventLocked() (event, bool) {
	if e.resolved && !e.delivered {
		e.delivered = true
		return event{code: eventStreamWrite, p1: uint32(e.index), p2: uint32(e.result) | (e.progress << 4)}, true
	}
	return event{}, false
}

// joinTo records the set this end belongs to, so a completed write can wake its waiters.
func (e *writableStreamEnd) joinTo(s *waitableSet) { e.set = s }

// endStateName is the write end's lifecycle state after resolution: DONE for DROPPED, IDLE for a COMPLETED
// write (the stream stays open for more — NOT DONE like a future). A subsequent write's trap-legality reads
// this; it is tracked here rather than derived from the future end's rule.
func (e *writableStreamEnd) endStateName() string {
	if e.done {
		return "DONE"
	}
	return "IDLE"
}

// completeWriteLocked ends a pending stream.write: the consumer took `progress` of the offered elements
// (COMPLETED, end stays IDLE) or the readable end dropped (DROPPED, end goes DONE). Arms the event and wakes
// the set. Mirrors the model's stream_event (def:2491–2501). The caller holds asyncHandles.mu; may run on
// the host consumer's goroutine while the writing agent is parked.
func (e *writableStreamEnd) completeWriteLocked(progress uint32, r copyResult) {
	e.progress = progress
	e.result = r
	e.resolved = true
	if r == copyDropped {
		e.done = true
	}
	if e.set != nil {
		e.set.signalLocked()
	}
}

// streamWrite implements `canon stream.write` (0x10, definitions.py:2473) on the async path: register the
// pending write (the src ptr + element count) and return BLOCKED. The host consumer later takes elements
// (completeWriteLocked), which arms the STREAM_WRITE event the existing waitable-set.wait delivers — no
// second wait mechanism. `stream.read` (0x0f) is NOT built (the guest does not bind it); it refuses by name.
func streamWrite(h *asyncHandles) interp.CanonFunc {
	return func(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		if len(args) < 3 {
			return nil, fmt.Errorf("component: stream.write: got %d args, want (i, ptr, n)", len(args))
		}
		i, ptr, n := uint32(args[0].Bits), uint32(args[1].Bits), uint32(args[2].Bits)
		h.mu.Lock()
		defer h.mu.Unlock()
		e, ok := handleAt[*writableStreamEnd](h, i)
		if !ok {
			return nil, fmt.Errorf("component: stream.write: handle %d is not a writable stream end", i)
		}
		if e.done || e.hasWrite {
			return nil, fmt.Errorf("component: stream.write: end %d is not writable (done or write pending)", i)
		}
		e.srcPtr = ptr
		e.n = n
		e.hasWrite = true
		e.caller = c
		bits := uint32(asyncBlocked) // 0xffffffff; as a signed i32 core value this is -1 (same 32 bits)
		return []interp.Value{interp.I32(int32(bits))}, nil
	}
}
