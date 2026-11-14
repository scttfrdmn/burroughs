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
//  1. The event packs `result | (progress<<4)` (definitions.py:StreamEnd.notify.stream_event:"packed_result = result | (progress << 4)"), not a bare CopyResult — a consumer
//     that takes fewer elements than offered delivers COMPLETED with partial progress, and a codec reading
//     only the low bits is right on a full copy and silently wrong on a partial one.
//  2. A COMPLETED stream write leaves the end IDLE (open for more writes); only DROPPED is DONE. Future's
//     COMPLETED is DONE (single-shot) — opposite end state, so the write end tracks its own.
//
// The guest bound `stream.write` (stdout) but not `stream.read` (p3async-hello's bytes, #739), so that slice
// built the write side and refused `stream.read` by name. The oracle is fixtures.json's `stream_writes`. A
// guest reading WASI 0.3's stdin binds `stream.read`, which is built in its async form since ADR 0122
// (streamRead below), with the host producing the stream (hostproducer.go).

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
	caller    *interp.CanonCaller // the writing agent's caller
	// readerDropped records that the paired readable end was dropped (def:End.drop:"self.other.other = None").
	// A flag and not `conn == nil`, because a writable end minted by an async lower's stream result has no
	// conn from the start and is not a dropped one.
	readerDropped bool
	// producer, when set, makes this a HOST-held writer (ADR 0122): never in the guest's table, never parking
	// a write, its source the producer's buffer of bytes read and not yet delivered.
	producer *streamProducer
	// elem is the stream's element (ADR 0124), stamped at the mint (stream.new's type, or byteElem for a host's):
	// every stream built-in on this end checks its own element against it, and a copy strides by it.
	elem *elemType
}

// pendingEventLocked delivers the write's (STREAM_WRITE, index, result|progress<<4) event once — progress
// is the load-bearing difference from the future read end. The caller holds asyncHandles.mu.
//
// **Taking the event returns the end to IDLE** (ADR 0120): the write is no longer pending, so `hasWrite` is
// cleared, as the reference's `stream_event` sets `End.State.IDLE` when the event is taken
// (def:StreamEnd.notify.stream_event:"self.state = End.State.IDLE"). It was cleared only by a cancel, so a
// write after a guest-first completion was refused as "not writable".
//
// NEUTER: drop the clear, and TestAStreamWriteEndIsIdleOnceItsEventIsTaken's write after a taken guest-first
// completion is refused.
//
// **A COMPLETED left untaken when the reader dropped is taken as DROPPED, with its progress** (ADR 0122): the
// reference computes the result at the take, from whether `other` is still there
// (def:StreamEnd.notify.stream_event:"if self.other is None:").
//
// NEUTER: drop the conversion, and TestAWritersDropUnlinksItsReader's writer takes its COMPLETED as COMPLETED.
func (e *writableStreamEnd) pendingEventLocked() (event, bool) {
	if e.resolved && !e.delivered {
		e.delivered = true
		e.hasWrite = false
		if e.readerDropped && e.result == copyCompleted {
			e.result, e.done = copyDropped, true
		}
		return event{code: eventStreamWrite, p1: uint32(e.index), p2: uint32(e.result) | (e.progress << 4)}, true
	}
	return event{}, false
}

// hasPendingEventLocked is pendingEventLocked's test without the take (#1005).
func (e *writableStreamEnd) hasPendingEventLocked() bool { return e.resolved && !e.delivered }

// joinTo records the set this end belongs to, so a completed write can wake its waiters.
func (e *writableStreamEnd) joinTo(s *waitableSet) { e.set = s }

// currentSet reports the set this end is joined to, so `waitable.join` can remove it before re-joining.
func (e *writableStreamEnd) currentSet() *waitableSet { return e.set }

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
// the set. Mirrors the model's stream_event (def:StreamEnd.notify.stream_event). The caller holds asyncHandles.mu; may run on
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

// streamWrite implements `canon stream.write` (0x10, definitions.py:canon_stream_write) on the async path. Two orderings,
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
// The reader is the host (write-via-stream, through hostReadLocked's internal path) or, since ADR 0122, a guest
// reading through `stream.read` (streamRead).
//
// **`elem` is the element the built-in names** (ADR 0124). It must be the end's (checkStreamEndType), and the buffer
// is `n` elements of it, checked as def:GuestBuffer checks a buffer (checkGuestBuffer), in def:copy's order: the
// end's type, its state, then the buffer. A write on an end that is not IDLE is a trap
// (def:copy:"trap_if(end.state != End.State.IDLE)"); it was a Go error. Before ADR 0124 the write had no element and
// none of these checks, so a `stream<u32>` write copied a count of bytes (#1275).
func streamWrite(h *asyncHandles, elem *elemType) interp.CanonFunc {
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
		if err := checkStreamEndType("stream.write", i, e.elem, elem); err != nil {
			return nil, err
		}
		if e.done || e.hasWrite {
			return nil, &interp.Trap{Reason: fmt.Sprintf("stream.write: end %d is not IDLE (done or write pending)", i)}
		}
		if err := checkGuestBuffer(c, "stream.write", elem, ptr, n); err != nil {
			return nil, err
		}
		// **A write starts from IDLE, so the last copy's state is cleared here** (ADR 0120). Reaching this line
		// means no write is pending and any completed write's event has been taken, the reference's IDLE
		// (def:copy:"trap_if(end.state != End.State.IDLE)"). `delivered` left set made the next inline write's
		// pendingEventLocked deliver nothing, so it reported progress 0.
		//
		// NEUTER: drop this reset, and TestAStreamWriteEndIsIdleOnceItsEventIsTaken's second host-first write
		// reports progress 0.
		e.resolved, e.delivered, e.progress, e.result = false, false, 0, copyCompleted
		e.srcPtr = ptr
		e.n = n
		e.caller = c
		// **The reader has dropped, so the write completes DROPPED inline** (ADR 0122). The reference's copy
		// finds `other` gone and its drop's notify already pending (def:End.copy:"assert(self.has_pending_event())").
		// The drop left `conn` set, so this write parked for a reader that could never arrive.
		//
		// NEUTER: drop this arm, and TestAWritersDropUnlinksItsReader's write after the reader's drop parks BLOCKED.
		if e.readerDropped {
			e.completeWriteLocked(0, copyDropped)
			ev, _ := e.pendingEventLocked()
			return []interp.Value{interp.I32(int32(ev.p2))}, nil
		}
		// A guest read of 0 is pending: it is notified with progress 0 and this write parks, the reference's
		// def:End.copy:"elif buffer.remain() > 0 or (is_read and self.other.buffer.remain() == 0):" arm seen from
		// the writer (ADR 0122). Driving the copy completed both with 0. Unreachable while every reader was the
		// host's, whose reads are never of 0.
		//
		// NEUTER: drop this arm, and TestAGuestStreamReadFollowsEndCopysArms's write of 3 completes inline.
		if e.conn != nil && e.conn.hasRead && !e.conn.resolved && e.conn.readN == 0 && n > 0 {
			e.conn.armLocked(0, copyCompleted)
			e.hasWrite = true
			return blockedResult(), nil
		}
		// Host-first: the paired readable end already has a pending read. Drive the copy now and complete
		// inline — the write is the second arriver (def:End.copy:"elif buffer.remain() > 0 and self.other.buffer.remain() > 0:").
		//
		// **A write of 0 elements completes only itself** (ADR 0120): the reference's last `End.copy` arm,
		// def:End.copy:"self.notify(progress = 0)", notifies the writer and leaves the reader parked. Driving
		// the copy armed the reader too, which a host consumer reads as a copy that ended.
		//
		// NEUTER: drop this arm, and TestAStreamWriteEndIsIdleOnceItsEventIsTaken's write of 0 arms the parked
		// reader.
		if e.conn != nil && e.conn.hasRead && !e.conn.resolved && n == 0 {
			e.completeWriteLocked(0, copyCompleted)
			ev, _ := e.pendingEventLocked()
			return []interp.Value{interp.I32(int32(ev.p2))}, nil
		}
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

// copyState mirrors definitions.py CopyState (def:End.State): a stream/future end's lifecycle. Only the
// three a stream end reaches in this slice are named — IDLE (open, no copy in flight), COPYING (a copy is
// registered, its event not yet consumed), DONE (dropped). The values match the model's so a name maps
// straight across. The transition COPYING→IDLE/DONE runs when the pending event is CONSUMED (get_pending_
// event → stream_event, def:StreamEnd.notify.stream_event), not when it is armed — which is why the pending side of a copy
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
// stream.new paired with a writable end over one shared stream (the `conn` back-pointers), or by a host
// producer paired with a host-held writer (read-via-stream, ADR 0122). It is read by the host consumer
// (hostReadLocked) when a lowered import hands it the readable end (the p3async-hello write-via-stream flow),
// or by the guest's own `stream.read` (streamRead, ADR 0122). A pending read parks in COPYING; the write
// that arrives second drives the copy and arms this end's (STREAM_READ, index, result|progress<<4) event,
// which stays COPYING until a waitable-set.wait consumes it. All fields are guarded by asyncHandles.mu.
type readableStreamEnd struct {
	index int
	set   *waitableSet // the set it is joined to, if any
	// conn is the paired writable end of the same stream, set at mint and cleared when the writer drops
	// (def:End.drop:"self.other.other = None", #1264).
	conn *writableStreamEnd
	// writerDropped records that drop: the reference's `other is None`. A flag and not `conn == nil`, the
	// writable end's `readerDropped`'s reason: an end built without a writer is not one whose writer dropped.
	writerDropped bool
	state         copyState
	resolved      bool // the copy has ended (COMPLETED or DROPPED)
	delivered     bool // the STREAM_READ event has been taken by a waitable-set.wait
	result        copyResult
	progress      uint32              // elements copied into the read buffer (min(read_n, write_n))
	dstPtr        uint32              // where a pending read wants elements written (guest reader)
	readN         uint32              // how many elements the read requested
	hasRead       bool                // a read is pending on this end
	caller        *interp.CanonCaller // the reader's caller, for the completion's memory write (guest reader)
	// A HOST consumer (write-via-stream) reads into a host sink rather than guest memory — the #763
	// divergence: hostReadLocked's guest-ptr destination was a stand-in for a guest-side reader, and the
	// real consumer's sink is host-side. When hostSink != nil the copy hands the bytes here; onHostComplete
	// fires when the read resolves, so the consumer can finish (write to stdout, resolve its future).
	hostSink       func([]byte) error
	onHostComplete func(result copyResult)
	// elem is the stream's element (ADR 0124), the writable end's: stamped at the mint with it.
	elem *elemType
}

// pendingEventLocked delivers the read's (STREAM_READ, index, result|progress<<4) event once and, on
// consumption, transitions the end out of COPYING (COMPLETED→IDLE, DROPPED→DONE) — the model's stream_event
// runs at get_pending_event, not at arm. readableStreamEnd satisfies the waitable interface. Caller holds mu.
//
// **A COMPLETED left untaken when the writer dropped is taken as DROPPED, with its progress** (#1264, ADR
// 0122): the reference computes the result at the take, from whether `other` is still there
// (def:StreamEnd.notify.stream_event:"if self.other is None:"), and its drop does not notify an end whose event
// is already pending. This delivered the COMPLETED, and the end went back to IDLE for a writer that was gone.
//
// NEUTER: drop the conversion, and TestAWritersDropUnlinksItsReader's untaken COMPLETED is taken as COMPLETED.
func (e *readableStreamEnd) pendingEventLocked() (event, bool) {
	if e.resolved && !e.delivered {
		e.delivered = true
		if e.writerDropped && e.result == copyCompleted {
			e.result = copyDropped
		}
		if e.result == copyDropped {
			e.state = copyStateDone
		} else {
			e.state = copyStateIdle
		}
		return event{code: eventStreamRead, p1: uint32(e.index), p2: uint32(e.result) | (e.progress << 4)}, true
	}
	return event{}, false
}

// hasPendingEventLocked is pendingEventLocked's test without the take (#1005).
func (e *readableStreamEnd) hasPendingEventLocked() bool { return e.resolved && !e.delivered }

// joinTo records the set this end belongs to, so a completed copy can wake its waiters.
func (e *readableStreamEnd) joinTo(s *waitableSet) { e.set = s }

// currentSet reports the set this end is joined to, so `waitable.join` can remove it before re-joining.
func (e *readableStreamEnd) currentSet() *waitableSet { return e.set }

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
	if e.onHostComplete != nil { // a host consumer: let it finish (write to stdout, resolve its future)
		e.onHostComplete(r)
	}
}

// driveStreamCopyLocked copies min(read_n, write_n) elements from the writable end's source buffer into the
// readable end's destination buffer, returning the count (the shared progress both ends report). This is
// SharedStreamImpl's copy (def:End.copy:"elif buffer.remain() > 0 and self.other.buffer.remain() > 0:" / 1000–1005): the second arriver moves the bytes; each side supplies
// its own buffer at its own ptr in its own memory. Caller holds asyncHandles.mu.
//
// **The copy strides by the element** (ADR 0124, #1275): `n` elements are `n * size` bytes
// (def:ReadableGuestBuffer.read, def:load_list_from_valid_range). It moved `n` bytes, a quarter of a `stream<u32>`.
// Both ends' buffers were checked as def:GuestBuffer checks them when their built-ins were called.
//
// NEUTER: move `n` bytes, and TestTypedStreamElementsMatchTheOracle's u32-wf-w0 reads 0x00223344 and u32-wf-w1 0, in
// both orderings.
func driveStreamCopyLocked(r *readableStreamEnd, w *writableStreamEnd) (uint32, error) {
	// A host-held writer's source is the producer's buffer, not guest memory (ADR 0122).
	//
	// NEUTER: drop this arm, and TestP3CatMatchesWasmtime reads no bytes.
	if w.producer != nil {
		return w.producer.copyIntoLocked(r)
	}
	n := min(w.n, r.readN)
	if n == 0 {
		return 0, nil
	}
	el := w.elem
	if r.hostSink != nil {
		// **A host consumer reads bytes** (ADR 0124). Its `data: stream<u8>` parameter is not checked at instantiate,
		// which a producer's result is (checkConsumerPayload), so a guest declaring a stream of anything else
		// reaches here, and its words would reach stdout as bytes.
		//
		// NEUTER: drop this check, and TestAHostConsumerReadsOnlyAStreamOfBytes's stream<u32> write hands its 8 bytes
		// to the sink.
		if el.size != 1 {
			return 0, fmt.Errorf("%w: a host consumer reads a stream<u8>, and this stream's element is %s (ADR 0124)",
				ErrUnsupportedForm, el)
		}
	} else if !el.numeric {
		// **Two guest ends are one instance's** (ADR 0124): stream.new mints both in one instance's table and no
		// built-in or lift moves an end out of it. So a copy between them traps unless the element is a number, at
		// the copy, whichever side arrives second; the write that parks before it does not.
		//
		// NEUTER: drop this arm, and TestTypedStreamElementsMatchTheOracle's string-copy, string-copy-read-first and
		// own-copy complete, progress 1.
		return 0, &interp.Trap{Reason: fmt.Sprintf("stream copy: cannot read from and write to an intra-instance "+
			"stream with a non-numeric payload, %s (the pin's End.copy: trap_if(... not none_or_number_type))", el)}
	}
	nb := uint64(n) * uint64(el.size)
	if nb == 0 { // a stream with no element: n unit values, and no bytes
		return n, nil
	}
	// Both buffers live in the one guest memory, and the src is read through the READER's caller, which is
	// memory-bound in both cases: write-via-stream's lower, or a guest `stream.read`, whose canonopts the decoder
	// keeps (ADR 0122). stream.write's are kept too since grave #1270, and its buffer was checked through them.
	src, err := r.caller.Read(uint64(w.srcPtr), nb)
	if err != nil {
		return 0, fmt.Errorf("component: stream copy: reading %d elements from writer at %#x: %w", n, w.srcPtr, err)
	}
	if r.hostSink != nil { // a host consumer: hand the bytes to its sink (stdout), not guest memory
		if err := r.hostSink(src); err != nil {
			return 0, fmt.Errorf("component: stream copy: host sink: %w", err)
		}
		return n, nil
	}
	// A float's NaN is canonicalised per element, as futureWrite's is: within one instance the copy is a lift then a
	// lower in the pin, and the codec's lift takes the deterministic profile's canonical NaN.
	//
	// NEUTER: skip this, and TestTypedStreamElementsMatchTheOracle's f32-wf-w0, f32-rf-w0 and f64-wf-lo read
	// wasmtime's preserved payload.
	canonicalizeFloats(src, el.float)
	if err := r.caller.Write(uint64(r.dstPtr), src); err != nil {
		return 0, fmt.Errorf("component: stream copy: writing %d elements to reader at %#x: %w", n, r.dstPtr, err)
	}
	return n, nil
}

// hostReadLocked is the host consumer's read of a readable stream end — the internal counterpart to the
// guest's stream.write, NOT the guest built-in stream.read (streamRead, ADR 0122). Two orderings mirror
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
	// From IDLE, so the last copy's state is cleared, as streamWrite clears the writable end's (ADR 0120).
	//
	// NEUTER: drop this reset, and TestAStreamWriteEndIsIdleOnceItsEventIsTaken's second host-first write
	// parks BLOCKED, the re-parked read still reading as resolved.
	e.resolved, e.delivered, e.progress, e.result, e.hasRead = false, false, 0, copyCompleted, false
	e.dstPtr = ptr
	e.readN = n
	e.caller = c
	e.state = copyStateCopying
	// A parked write of 0 elements is completed with progress 0 and the read parks: the reference's
	// def:End.copy:"elif buffer.remain() > 0 or (is_read and self.other.buffer.remain() == 0):" arm (ADR 0120).
	//
	// NEUTER: drop this arm, and TestAStreamWriteEndIsIdleOnceItsEventIsTaken's read at a parked write of 0
	// completes.
	if e.conn != nil && e.conn.hasWrite && !e.conn.resolved && e.conn.n == 0 {
		e.conn.completeWriteLocked(0, copyCompleted)
		e.hasRead = true
		return false, nil
	}
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

// streamCancelWrite implements `canon stream.cancel-write` (0x12, definitions.py:canon_stream_cancel_write → cancel_copy) — the
// symmetric twin of streamCancelRead on the writable end. The end must be mid-copy (a pending write:
// hasWrite && !resolved) or it traps; if the copy already completed (an event is armed) the cancel delivers
// that event, otherwise it resolves the write to CANCELLED with the progress made (0 for an undriven pending
// write). The event is consumed INLINE and its packed payload returned. A CANCELLED write leaves the end
// IDLE (open) — only DROPPED is DONE. Built as its own op (0x12), not folded into cancel-read's shared
// cancel_copy: the guest binds both, and each refuses/permits by name.
//
// `elem` is the element the built-in names, which must be the end's (def:cancel_copy, ADR 0124).
func streamCancelWrite(h *asyncHandles, elem *elemType) interp.CanonFunc {
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
		if err := checkStreamEndType("stream.cancel-write", i, e.elem, elem); err != nil {
			return nil, err
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

// streamDropReadable implements `canon stream.drop-readable` (0x13, definitions.py:canon_stream_drop_readable). Per CopyEnd.drop
// (def:drop:"trap_if(end.copying_or_cancelling())"): TRAP if the end is mid-copy (COPYING) — an end with an in-flight or armed-but-unconsumed
// copy cannot be dropped — then drop the shared stream and remove the handle. Dropping notifies a pending
// write on the paired writable end with DROPPED (SharedStreamImpl.drop, def:End.drop): a dropped reader wakes
// a waiting writer with DROPPED rather than orphaning it. The COPYING guard is the end-state audit — a
// readable end stays COPYING from the read until its event is consumed, so a drop before the host consumer
// takes the STREAM_READ event traps rather than silently discarding a completed copy.
//
// `elem` is the element the built-in names, which must be the end's (def:drop, ADR 0124).
func streamDropReadable(h *asyncHandles, elem *elemType) interp.CanonFunc {
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
		if err := checkStreamEndType("stream.drop-readable", i, e.elem, elem); err != nil {
			return nil, err
		}
		if e.state == copyStateCopying {
			return nil, &interp.Trap{Reason: fmt.Sprintf("stream.drop-readable: end %d is mid-copy (COPYING); consume its event before dropping", i)}
		}
		if e.conn != nil && e.conn.hasWrite && !e.conn.resolved {
			e.conn.completeWriteLocked(0, copyDropped) // notify the pending writer: DROPPED, progress 0
		}
		// **The drop unlinks both ends** (def:End.drop:"self.other.other = None", ADR 0122), so the writer's next
		// write completes DROPPED inline rather than parking for a reader that is gone. A host producer learns
		// here that its reader is gone: it stops, and its future resolves `ok`, as `wasi:cli/stdin` states.
		//
		// NEUTER: drop the producer's notice, and TestAGuestDropOfTheProducedStreamResolvesTheFutureOK's future
		// never resolves.
		if w := e.conn; w != nil {
			if w.producer != nil {
				w.producer.readerDroppedLocked()
			}
			w.readerDropped, w.conn, e.conn = true, nil, nil
		}
		h.entries[i] = nil
		return nil, nil
	}
}

// streamCancelRead implements `canon stream.cancel-read` (0x11, definitions.py:canon_stream_cancel_read → cancel_copy). It
// cancels a pending read: the end must be mid-copy (COPYING) or it traps; if the copy already completed (an
// event is armed), the cancel delivers that event (the copy won); otherwise it resolves the read to
// CANCELLED with whatever progress was made (0 for an undriven pending read). Either way the event is
// consumed INLINE and its packed payload returned — not BLOCKED. A CANCELLED read leaves the end IDLE (open,
// reusable); only DROPPED is DONE. This is the FIRST running production of CANCELLED: it has been pinned in
// the codec since the future oracle (#752) but only ever injected synthetically (future.cancel-read is
// refused); the test asserts this running path delivers the SAME encoding the fixture asserted.
//
// `elem` is the element the built-in names, which must be the end's (def:cancel_copy, ADR 0124).
func streamCancelRead(h *asyncHandles, elem *elemType) interp.CanonFunc {
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
		if err := checkStreamEndType("stream.cancel-read", i, e.elem, elem); err != nil {
			return nil, err
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

// streamDropWritable implements `canon stream.drop-writable` (0x14, definitions.py:canon_stream_drop_writable). Same guard as the
// readable drop (a stream end drops from IDLE or DONE, traps mid-copy) — NOT the writable FUTURE end's rule,
// which traps unless DONE (def:drop:"trap_if(isinstance(end, WritableFutureEnd) and end.state !="); a writable stream end may be dropped while IDLE. A pending write is the
// writable end's COPYING (hasWrite && !resolved). Dropping unlinks the paired readable end and notifies it
// with DROPPED (dropWriterLocked) — the anti-hang guarantee in the other direction.
//
// `elem` is the element the built-in names, which must be the end's (def:drop, ADR 0124).
func streamDropWritable(h *asyncHandles, elem *elemType) interp.CanonFunc {
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
		if err := checkStreamEndType("stream.drop-writable", i, e.elem, elem); err != nil {
			return nil, err
		}
		if e.hasWrite && !e.resolved {
			return nil, &interp.Trap{Reason: fmt.Sprintf("stream.drop-writable: end %d is mid-copy (COPYING); a pending write must resolve before dropping", i)}
		}
		dropWriterLocked(e)
		h.entries[i] = nil
		return nil, nil
	}
}

// dropWriterLocked is a writer's drop, the guest's (stream.drop-writable) or a host producer's at the end of its
// source: the reference's `End.drop` (def:End.drop) seen from the writable end. Caller holds asyncHandles.mu.
//
//   - **The reader is unlinked** (def:End.drop:"self.other.other = None", #1264). The writer's drop left `conn`
//     set, so a read after it parked on a writer that was gone, and an untaken COMPLETED was taken as
//     COMPLETED (readableStreamEnd.pendingEventLocked now computes DROPPED at the take).
//   - **The reader is notified with progress 0 unless it is DONE or already has an event pending**
//     (def:End.drop:"if self.other.state != End.State.DONE and not self.other.has_pending_event():"). That is
//     an IDLE reader too, whose next `stream.read` then takes the DROPPED inline. Only a reader with a read
//     pending was notified.
//
// NEUTER: leave `conn` set and notify only a pending read, as before, and TestAWritersDropUnlinksItsReader's
// untaken COMPLETED is taken as COMPLETED, its read after the drop parks BLOCKED, and its write-via-stream after
// the drop never resolves its future. TestP3CatMatchesWasmtime passes under it: p3cat's stream finishes with a read
// pending, which the old notify reached.
func dropWriterLocked(e *writableStreamEnd) {
	r := e.conn
	if r == nil {
		return
	}
	r.conn, e.conn, r.writerDropped = nil, nil, true
	if r.state != copyStateDone && !r.hasPendingEventLocked() {
		// An IDLE reader may still carry its last copy's taken event (`delivered`), which a read clears as it
		// starts. The notify is a new event, so it clears it here.
		r.delivered = false
		r.armLocked(0, copyDropped) // DROPPED, progress 0, -> DONE on consume
	}
}

// streamNew implements `canon stream.new` (0x0e, definitions.py:canon_stream_new): mint a readable and a writable end
// over ONE shared stream, connect them (the `conn` back-pointers), and return the packed handle
// `ri | (wi << 32)` as an i64. The two ends are added in that order (ri before wi), so ri is the lower index
// — matching the oracle (fixtures.json stream_new: ri=1, wi=2, both IDLE). This is the guest→host inversion:
// the guest keeps wi (stream.write) and hands ri to the host, whose read drives the guest's write to done.
//
// **Both ends carry the element stream.new names** (ADR 0124), as the pin's `new_stream(stream_t.t, owner = inst)`
// gives both its `t`: every later built-in on either end is checked against it.
func streamNew(h *asyncHandles, elem *elemType) interp.CanonFunc {
	return func(_ *interp.CanonCaller, _ []interp.Value) ([]interp.Value, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		re := &readableStreamEnd{state: copyStateIdle, elem: elem}
		we := &writableStreamEnd{elem: elem}
		re.index = h.addLocked(re)
		we.index = h.addLocked(we)
		re.conn = we
		we.conn = re
		packed := uint64(uint32(re.index)) | uint64(uint32(we.index))<<32
		return []interp.Value{interp.I64(int64(packed))}, nil
	}
}

// bufferMaxLength is the reference's `Buffer.MAX_LENGTH` (def:Buffer:"MAX_LENGTH = 2**28 - 1"): a copy longer
// than this traps, and a progress always fits in the 28 bits above an event's 4-bit result.
const bufferMaxLength = 1<<28 - 1

// blockedResult is an async copy's BLOCKED return as its core value: 0xffffffff, which as a signed i32 is -1.
func blockedResult() []interp.Value {
	bits := uint32(asyncBlocked)
	return []interp.Value{interp.I32(int32(bits))}
}

// streamRead implements `canon stream.read` (0x0f, def:canon_stream_read) in its async form (ADR 0122). The
// sync form is refused at instantiate (unbuiltAsyncSurface). It is `copy` (def:copy) with a writable guest
// buffer, so its arms are `End.copy`'s from the reader's side:
//
//   - **It traps unless the end is IDLE**, and on a buffer outside memory or longer than MAX_LENGTH, before any
//     state changes (def:copy:"trap_if(end.state != End.State.IDLE)", def:GuestBuffer).
//   - **The writer has gone** (`writerDropped`): its drop left this end's DROPPED pending, and the read takes it
//     inline (def:End.copy:"assert(self.has_pending_event())").
//   - **A host producer's writer** serves the read from its buffer, or parks it and reads its source on demand
//     (streamProducer.readLocked).
//   - **A guest's parked write of 0** is completed with progress 0 and the read parks; **a read of 0 at a
//     parked write** completes itself with 0 and leaves the write parked; **a read at a parked write** copies
//     `min` of the two inline and completes both; **no write parked**: the read parks, BLOCKED.
//
// The read's caller is bound to the memory its canonopts name (the decoder keeps them), and the copy into the
// reader writes through it, now or when the write that arrives second drives the copy.
//
// NEUTER: refuse 0x0f again (isBuiltAsyncBuiltin), and TestP3CatMatchesWasmtime is refused at instantiate.
//
// **`elem` is the element the built-in names** (ADR 0124): it must be the end's, and the buffer is `n` elements of
// it, so its range is `n * size` bytes and its pointer must be aligned to the element (def:GuestBuffer). The range
// was `n` bytes and the alignment unchecked, while a read of any element but a byte was refused (ADR 0123).
func streamRead(h *asyncHandles, elem *elemType) interp.CanonFunc {
	return func(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		if len(args) < 3 {
			return nil, fmt.Errorf("component: stream.read: got %d args, want (i, ptr, n)", len(args))
		}
		i, ptr, n := uint32(args[0].Bits), uint32(args[1].Bits), uint32(args[2].Bits)
		h.mu.Lock()
		defer h.mu.Unlock()
		e, ok := handleAt[*readableStreamEnd](h, i)
		if !ok {
			return nil, fmt.Errorf("component: stream.read: handle %d is not a readable stream end", i)
		}
		if err := checkStreamEndType("stream.read", i, e.elem, elem); err != nil {
			return nil, err
		}
		if e.state != copyStateIdle {
			return nil, &interp.Trap{Reason: fmt.Sprintf("stream.read: end %d is not IDLE (state %s)", i, e.state.name())}
		}
		if err := checkGuestBuffer(c, "stream.read", elem, ptr, n); err != nil {
			return nil, err
		}
		if e.writerDropped {
			e.state = copyStateCopying
			if !e.hasPendingEventLocked() { // unreachable: a writer's drop notifies every reader not DONE
				e.delivered = false
				e.armLocked(0, copyDropped)
			}
			ev, _ := e.pendingEventLocked()
			return []interp.Value{interp.I32(int32(ev.p2))}, nil
		}
		// From IDLE, so the last copy's state is cleared, as streamWrite clears the writable end's (ADR 0120).
		e.resolved, e.delivered, e.progress, e.result, e.hasRead = false, false, 0, copyCompleted, false
		e.dstPtr, e.readN, e.caller, e.state = ptr, n, c, copyStateCopying
		w := e.conn
		if w != nil && w.producer != nil {
			return w.producer.readLocked(e)
		}
		if w != nil && w.hasWrite && !w.resolved {
			// The two zero-length arms, def:End.copy:"elif buffer.remain() > 0 or (is_read and
			// self.other.buffer.remain() == 0):" and def:End.copy:"self.notify(progress = 0)", in that order.
			//
			// NEUTER: drop the first, and TestAGuestStreamReadFollowsEndCopysArms's read at a parked write of 0
			// completes; drop the second, and its read of 0 completes the parked write too.
			switch {
			case w.n == 0:
				w.completeWriteLocked(0, copyCompleted)
				e.hasRead = true
				return blockedResult(), nil
			case n == 0:
				e.armLocked(0, copyCompleted)
				ev, _ := e.pendingEventLocked()
				return []interp.Value{interp.I32(int32(ev.p2))}, nil
			}
			progress, err := driveStreamCopyLocked(e, w)
			if err != nil {
				return nil, err
			}
			w.completeWriteLocked(progress, copyCompleted)
			e.armLocked(progress, copyCompleted)
			ev, _ := e.pendingEventLocked()
			return []interp.Value{interp.I32(int32(ev.p2))}, nil
		}
		e.hasRead = true
		return blockedResult(), nil
	}
}
