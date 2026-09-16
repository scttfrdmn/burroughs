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
	set       *waitableSet       // the set it is joined to, if any
	conn      *readableStreamEnd // the paired readable end of the same stream (set by stream.new); nil for a lowered end
	resolved  bool               // this write has an event to deliver
	delivered bool               // the STREAM_WRITE event has been taken by a waitable-set.wait
	done      bool               // the end is DONE (dropped); a COMPLETED write leaves it not-done (IDLE, open)
	result    copyResult
	progress  uint32              // elements the consumer took (packed into the event as progress<<4)
	srcPtr    uint32              // where a pending stream.write offered its elements
	n         uint32              // how many elements the guest offered
	hasWrite  bool                // a stream.write is pending on this end
	caller    *interp.CanonCaller // the writing agent's caller (used by the inline copy to read src)
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

// streamWrite implements `canon stream.write` (0x10, definitions.py:2473) on the async path. Two orderings,
// pinned by the oracle (fixtures.json stream_writes and stream_hostfirst):
//
//   - Guest-first: the writable end's reader has not yet arrived. Register the pending write (src ptr +
//     count) and return BLOCKED; the reader that arrives second (a host consumer, or a synthetic completion)
//     drives the copy, arming the STREAM_WRITE event the existing waitable-set.wait delivers.
//   - Host-first: a read is already pending on the paired readable end (the real p3async-hello flow, where
//     the host reads the readable end via a lowered import before the guest issues its write). The write is
//     the SECOND arriver: it drives the copy inline and completes INLINE — returning the packed payload
//     `result | (progress<<4)` directly, NOT BLOCKED, and arming no waitable event for the write. `progress`
//     is min(read_n, write_n), the smaller side (fixtures.json stream_hostfirst: under-read and over-read
//     both land min).
//
// `stream.read` (0x0f) is NOT built as a guest built-in (the guest does not bind it); the host reads the
// readable end through hostReadLocked, an internal path. It refuses by name.
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
		e.caller = c
		// Host-first: the paired readable end already has a pending read. Drive the copy now and complete
		// inline — the write is the second arriver (SharedStreamImpl.write, def:997–1005).
		if e.conn != nil && e.conn.hasRead && !e.conn.resolved {
			progress, err := driveStreamCopyLocked(e.conn, e)
			if err != nil {
				return nil, err
			}
			// Arm the READ end's event (so the host consumer that issued the read learns the copy completed
			// and can wake a waiter joined to it); it stays COPYING until that wait consumes it. The WRITE end
			// consumes its own event inline and returns the packed payload — the guest gets no waitable event.
			e.conn.armLocked(progress, copyCompleted)
			e.completeWriteLocked(progress, copyCompleted)
			ev, _ := e.pendingEventLocked()
			return []interp.Value{interp.I32(int32(ev.p2))}, nil
		}
		// Guest-first: no reader yet. Park the write and return BLOCKED.
		e.hasWrite = true
		bits := uint32(asyncBlocked) // 0xffffffff; as a signed i32 core value this is -1 (same 32 bits)
		return []interp.Value{interp.I32(int32(bits))}, nil
	}
}

// copyState mirrors definitions.py CopyState (def:1017–1021): a stream/future end's lifecycle. Only the
// three a stream end reaches in this slice are named — IDLE (open, no copy in flight), COPYING (a copy is
// registered, its event not yet consumed), DONE (dropped). The values match the model's so a name maps
// straight across. The transition COPYING→IDLE/DONE runs when the pending event is CONSUMED (get_pending_
// event → stream_event, def:2491–2497), not when it is armed — which is why the pending side of a copy
// stays COPYING with an armed-but-untaken event (fixtures.json stream_hostfirst: ri stays COPYING).
type copyState uint32

const (
	copyStateIdle    copyState = 1
	copyStateCopying copyState = 2
	copyStateDone    copyState = 4
)

func (s copyState) name() string {
	switch s {
	case copyStateCopying:
		return "COPYING"
	case copyStateDone:
		return "DONE"
	default:
		return "IDLE"
	}
}

// readableStreamEnd is the readable half of a stream<T> — the FOURTH waitable kind. It is minted by
// stream.new paired with a writable end over one shared stream (the `conn` back-pointers), and read by the
// host consumer (hostReadLocked), NOT by a guest built-in: the guest does not bind stream.read (0x0f), so
// that opcode stays refused; the host reads this end through an internal path when a lowered import hands
// it the readable end (the p3async-hello write-via-stream flow). A pending read parks in COPYING; the write
// that arrives second drives the copy and arms this end's (STREAM_READ, index, result|progress<<4) event,
// which stays COPYING until a waitable-set.wait consumes it. All fields are guarded by asyncHandles.mu.
type readableStreamEnd struct {
	index     int
	set       *waitableSet       // the set it is joined to, if any
	conn      *writableStreamEnd // the paired writable end of the same stream (set by stream.new)
	state     copyState
	resolved  bool // the copy has ended (COMPLETED or DROPPED)
	delivered bool // the STREAM_READ event has been taken by a waitable-set.wait
	result    copyResult
	progress  uint32              // elements copied into the read buffer (min(read_n, write_n))
	dstPtr    uint32              // where a pending read wants elements written
	readN     uint32              // how many elements the read requested
	hasRead   bool                // a read is pending on this end
	caller    *interp.CanonCaller // the reader's caller, for the completion's memory write
}

// pendingEventLocked delivers the read's (STREAM_READ, index, result|progress<<4) event once and, on
// consumption, transitions the end out of COPYING (COMPLETED→IDLE, DROPPED→DONE) — the model's stream_event
// runs at get_pending_event, not at arm. readableStreamEnd satisfies the waitable interface. Caller holds mu.
func (e *readableStreamEnd) pendingEventLocked() (event, bool) {
	if e.resolved && !e.delivered {
		e.delivered = true
		if e.result == copyDropped {
			e.state = copyStateDone
		} else {
			e.state = copyStateIdle
		}
		return event{code: eventStreamRead, p1: uint32(e.index), p2: uint32(e.result) | (e.progress << 4)}, true
	}
	return event{}, false
}

// joinTo records the set this end belongs to, so a completed copy can wake its waiters.
func (e *readableStreamEnd) joinTo(s *waitableSet) { e.set = s }

// endStateName is the read end's lifecycle state — COPYING while a read is registered and its event untaken,
// IDLE/DONE once consumed. Tracked here (not derived at delivery) because the next op's trap-legality reads it.
func (e *readableStreamEnd) endStateName() string { return e.state.name() }

// armLocked ends a pending read with result r and progress: mark resolved and wake any joined set. The end
// stays COPYING until the event is consumed (pendingEventLocked) — matching the model, where only the
// consume transitions the state. Caller holds mu; may run on the writing agent's goroutine.
func (e *readableStreamEnd) armLocked(progress uint32, r copyResult) {
	e.progress = progress
	e.result = r
	e.resolved = true
	if e.set != nil {
		e.set.signalLocked()
	}
}

// driveStreamCopyLocked copies min(read_n, write_n) elements from the writable end's source buffer into the
// readable end's destination buffer, returning the count (the shared progress both ends report). This is
// SharedStreamImpl's copy (def:982–987 / 1000–1005): the second arriver moves the bytes; each side supplies
// its own buffer at its own ptr in its own memory. Caller holds asyncHandles.mu.
func driveStreamCopyLocked(r *readableStreamEnd, w *writableStreamEnd) (uint32, error) {
	n := w.n
	if r.readN < n {
		n = r.readN
	}
	if n > 0 {
		src, err := w.caller.Read(uint64(w.srcPtr), uint64(n))
		if err != nil {
			return 0, fmt.Errorf("component: stream copy: reading %d elements from writer at %#x: %w", n, w.srcPtr, err)
		}
		if err := r.caller.Write(uint64(r.dstPtr), src); err != nil {
			return 0, fmt.Errorf("component: stream copy: writing %d elements to reader at %#x: %w", n, r.dstPtr, err)
		}
	}
	return n, nil
}

// hostReadLocked is the host consumer's read of a readable stream end — the internal counterpart to the
// guest's stream.write, NOT the guest built-in stream.read (0x0f stays refused). Two orderings mirror
// stream.write's: if the paired writable end already has a pending write, drive the copy now (the read is
// the second arriver) and complete both ends; otherwise park the read (state COPYING) for the write to
// drive. Returns (progress, completed): completed=false means the read parked (the host-first case — the
// guest's write completes it later). Caller holds asyncHandles.mu.
// completed=false means the read parked (host-first — the guest's write completes it later). On the
// read-first path the copy's count lands in e.progress for a consumer that wants it.
func (e *readableStreamEnd) hostReadLocked(c *interp.CanonCaller, ptr, n uint32) (completed bool, err error) {
	if e.state != copyStateIdle {
		return false, fmt.Errorf("component: stream read: end %d is not readable (state %s)", e.index, e.state.name())
	}
	e.dstPtr = ptr
	e.readN = n
	e.caller = c
	e.state = copyStateCopying
	// Read-first (guest-first for the write): a write is already parked — drive the copy now and complete
	// both ends. The read consumes its own event inline (→ IDLE); the write's event is armed for its wait.
	if e.conn != nil && e.conn.hasWrite && !e.conn.resolved {
		progress, derr := driveStreamCopyLocked(e, e.conn)
		if derr != nil {
			return false, derr
		}
		e.conn.completeWriteLocked(progress, copyCompleted)
		e.armLocked(progress, copyCompleted)
		e.pendingEventLocked() // consume inline → IDLE
		return true, nil
	}
	// Host-first: no writer yet. Park the read; the guest's write drives it (streamWrite's inline arm).
	e.hasRead = true
	return false, nil
}

// streamCancelWrite implements `canon stream.cancel-write` (0x12, definitions.py:2574 → cancel_copy) — the
// symmetric twin of streamCancelRead on the writable end. The end must be mid-copy (a pending write:
// hasWrite && !resolved) or it traps; if the copy already completed (an event is armed) the cancel delivers
// that event, otherwise it resolves the write to CANCELLED with the progress made (0 for an undriven pending
// write). The event is consumed INLINE and its packed payload returned. A CANCELLED write leaves the end
// IDLE (open) — only DROPPED is DONE. Built as its own op (0x12), not folded into cancel-read's shared
// cancel_copy: the guest binds both, and each refuses/permits by name.
func streamCancelWrite(h *asyncHandles) interp.CanonFunc {
	return func(_ *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("component: stream.cancel-write: got %d args, want (i)", len(args))
		}
		i := uint32(args[0].Bits)
		h.mu.Lock()
		defer h.mu.Unlock()
		e, ok := handleAt[*writableStreamEnd](h, i)
		if !ok {
			return nil, fmt.Errorf("component: stream.cancel-write: handle %d is not a writable stream end", i)
		}
		if !e.hasWrite || e.resolved {
			return nil, &interp.Trap{Reason: fmt.Sprintf("stream.cancel-write: end %d has no write in flight (COPYING)", i)}
		}
		e.completeWriteLocked(e.progress, copyCancelled) // progress is 0 for an undriven pending write
		e.hasWrite = false                               // the pending write is cancelled, not outstanding
		ev, ok := e.pendingEventLocked()                 // consume inline -> IDLE (CANCELLED)
		if !ok {
			return nil, &interp.Trap{Reason: fmt.Sprintf("stream.cancel-write: end %d had no deliverable event after cancel", i)}
		}
		return []interp.Value{interp.I32(int32(ev.p2))}, nil
	}
}

// streamDropReadable implements `canon stream.drop-readable` (0x13, definitions.py:2605). Per CopyEnd.drop
// (def:1040–1043): TRAP if the end is mid-copy (COPYING) — an end with an in-flight or armed-but-unconsumed
// copy cannot be dropped — then drop the shared stream and remove the handle. Dropping notifies a pending
// write on the paired writable end with DROPPED (SharedStreamImpl.drop, def:968–972): a dropped reader wakes
// a waiting writer with DROPPED rather than orphaning it. The COPYING guard is the end-state audit — a
// readable end stays COPYING from the read until its event is consumed, so a drop before the host consumer
// takes the STREAM_READ event traps rather than silently discarding a completed copy.
func streamDropReadable(h *asyncHandles) interp.CanonFunc {
	return func(_ *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("component: stream.drop-readable: got %d args, want (i)", len(args))
		}
		i := uint32(args[0].Bits)
		h.mu.Lock()
		defer h.mu.Unlock()
		e, ok := handleAt[*readableStreamEnd](h, i)
		if !ok {
			return nil, fmt.Errorf("component: stream.drop-readable: handle %d is not a readable stream end", i)
		}
		if e.state == copyStateCopying {
			return nil, &interp.Trap{Reason: fmt.Sprintf("stream.drop-readable: end %d is mid-copy (COPYING); consume its event before dropping", i)}
		}
		if e.conn != nil && e.conn.hasWrite && !e.conn.resolved {
			e.conn.completeWriteLocked(0, copyDropped) // notify the pending writer: DROPPED, progress 0
		}
		h.entries[i] = nil
		return nil, nil
	}
}

// streamCancelRead implements `canon stream.cancel-read` (0x11, definitions.py:2571 → cancel_copy). It
// cancels a pending read: the end must be mid-copy (COPYING) or it traps; if the copy already completed (an
// event is armed), the cancel delivers that event (the copy won); otherwise it resolves the read to
// CANCELLED with whatever progress was made (0 for an undriven pending read). Either way the event is
// consumed INLINE and its packed payload returned — not BLOCKED. A CANCELLED read leaves the end IDLE (open,
// reusable); only DROPPED is DONE. This is the FIRST running production of CANCELLED: it has been pinned in
// the codec since the future oracle (#752) but only ever injected synthetically (future.cancel-read is
// refused); the test asserts this running path delivers the SAME encoding the fixture asserted.
func streamCancelRead(h *asyncHandles) interp.CanonFunc {
	return func(_ *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("component: stream.cancel-read: got %d args, want (i)", len(args))
		}
		i := uint32(args[0].Bits)
		h.mu.Lock()
		defer h.mu.Unlock()
		e, ok := handleAt[*readableStreamEnd](h, i)
		if !ok {
			return nil, fmt.Errorf("component: stream.cancel-read: handle %d is not a readable stream end", i)
		}
		if e.state != copyStateCopying {
			return nil, &interp.Trap{Reason: fmt.Sprintf("stream.cancel-read: end %d has no copy in flight (state %s)", i, e.state.name())}
		}
		if !e.resolved {
			e.armLocked(e.progress, copyCancelled) // progress is 0 for an undriven pending read
			e.hasRead = false                      // the pending read is cancelled, not outstanding
		}
		ev, ok := e.pendingEventLocked() // consume inline -> IDLE (CANCELLED) or DONE (a prior DROPPED)
		if !ok {
			return nil, &interp.Trap{Reason: fmt.Sprintf("stream.cancel-read: end %d had no deliverable event after cancel", i)}
		}
		return []interp.Value{interp.I32(int32(ev.p2))}, nil
	}
}

// streamDropWritable implements `canon stream.drop-writable` (0x14, definitions.py:2608). Same guard as the
// readable drop (a stream end drops from IDLE or DONE, traps mid-copy) — NOT the writable FUTURE end's rule,
// which traps unless DONE (def:1125); a writable stream end may be dropped while IDLE. A pending write is the
// writable end's COPYING (hasWrite && !resolved). Dropping notifies a pending read on the paired readable
// end with DROPPED — the anti-hang guarantee in the other direction.
func streamDropWritable(h *asyncHandles) interp.CanonFunc {
	return func(_ *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("component: stream.drop-writable: got %d args, want (i)", len(args))
		}
		i := uint32(args[0].Bits)
		h.mu.Lock()
		defer h.mu.Unlock()
		e, ok := handleAt[*writableStreamEnd](h, i)
		if !ok {
			return nil, fmt.Errorf("component: stream.drop-writable: handle %d is not a writable stream end", i)
		}
		if e.hasWrite && !e.resolved {
			return nil, &interp.Trap{Reason: fmt.Sprintf("stream.drop-writable: end %d is mid-copy (COPYING); a pending write must resolve before dropping", i)}
		}
		if e.conn != nil && e.conn.hasRead && !e.conn.resolved {
			e.conn.armLocked(0, copyDropped) // notify the pending reader: DROPPED, progress 0, -> DONE on consume
		}
		h.entries[i] = nil
		return nil, nil
	}
}

// streamNew implements `canon stream.new` (0x0e, definitions.py:2451): mint a readable and a writable end
// over ONE shared stream, connect them (the `conn` back-pointers), and return the packed handle
// `ri | (wi << 32)` as an i64. The two ends are added in that order (ri before wi), so ri is the lower index
// — matching the oracle (fixtures.json stream_new: ri=1, wi=2, both IDLE). This is the guest→host inversion:
// the guest keeps wi (stream.write) and hands ri to the host, whose read drives the guest's write to done.
func streamNew(h *asyncHandles) interp.CanonFunc {
	return func(_ *interp.CanonCaller, _ []interp.Value) ([]interp.Value, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		re := &readableStreamEnd{state: copyStateIdle}
		we := &writableStreamEnd{}
		re.index = h.addLocked(re)
		we.index = h.addLocked(we)
		re.conn = we
		we.conn = re
		packed := uint64(uint32(re.index)) | uint64(uint32(we.index))<<32
		return []interp.Value{interp.I64(int64(packed))}, nil
	}
}
