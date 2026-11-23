// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"encoding/binary"
	"fmt"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// gate:async increment 3, first slice: the readable end of a `future<T>` — the second waitable kind.
//
// `future<T>` is the degenerate `stream<T>` in the CABI (a single value then closed), so it lands the
// shared copy substrate on the single-value case. A guest reads a host-provided future by calling
// `future.read`, which — on the async path — returns BLOCKED and registers the readable end as a waitable;
// the copy's completion arms a `(FUTURE_READ, index, CopyResult)` event that the SAME waitable-set loop
// (2a-i-B-2) delivers. This reuses that park-and-wake rather than growing a second one: readableFutureEnd
// is a second `waitable` kind, not a parallel wait mechanism (#739). The copy protocol's completion states
// are pinned in the oracle (fixtures.json future_reads) with their differing end state.

// copyResult mirrors definitions.py CopyResult (def:CopyResult): how a copy ended, the value the guest
// branches on. A future READ is only ever COMPLETED or CANCELLED — DROPPED is a future.write outcome (a
// read can never be dropped). DROPPED is pinned in the oracle (fixture-only, category 3); future.write is
// now built (#785) and CANCELLED has a running producer (the cancellation guest), but **DROPPED still has
// no running producer** — its expiry is a guest that drops a future read end while a write is in flight
// (checked explicitly, not changed by implication: TestFutureWriteDroppedStaysFixtureOnly).
type copyResult uint32

const (
	copyCompleted copyResult = 0
	copyDropped   copyResult = 1
	copyCancelled copyResult = 2
)

// asyncBlocked is the packed return of an async copy that did not resolve inline (definitions.py BLOCKED =
// 0xffff_ffff, def:BLOCKED): future.read returns it and the guest awaits the (FUTURE_READ, …) event via
// waitable-set.wait.
const asyncBlocked = 0xffffffff

// readableFutureEnd is the readable half of a future<T>: a waitable whose copy, once resolved, has a
// (FUTURE_READ, index, result) pending event. Its end state after resolution differs by outcome — DONE for
// COMPLETED, IDLE for CANCELLED (definitions.py future_event, def:FutureEnd.notify.future_event+, in `FutureEnd.notify`) — which the next operation's
// trap-legality reads, so it is tracked here, not derived at delivery. `value` is the payload the host will
// deliver; `readPtr`/`caller` are captured by a pending future.read so the completion can copy `value`
// into guest memory (the copy runs where the host resolves, possibly another goroutine — synchronized by
// the owning asyncHandles' mutex, which guards all fields).
type readableFutureEnd struct {
	index     int
	set       *waitableSet // the set it is joined to, if any
	resolved  bool         // the copy has ended (COMPLETED or CANCELLED)
	delivered bool         // the FUTURE_READ event has been taken by a waitable-set.wait
	result    copyResult
	value     uint32 // the value this future delivers on COMPLETED (future<u32>, first slice)
	// payload, when set, is the value this future delivers **at its declared payload type**, lowered
	// through the codec on COMPLETED instead of `value`'s 4-byte write (#1246, ADR 0116). A future<u32>
	// leaves it nil, since 4 bytes is that type's size; any other payload type must set it.
	payload *canon.Value
	readPtr uint32              // where a pending future.read wants the value written
	hasRead bool                // a future.read is pending on this end
	caller  *interp.CanonCaller // the reading agent's caller, for the completion's memory write
	// completeErr holds a deferred completion failure — when a host consumer resolves this future off the
	// stream-copy path (write-via-stream's onHostComplete, which cannot return an error), a guest-memory
	// write failure lands here and is surfaced when the guest next touches the end (futureDrop).
	completeErr error
	// writer is the paired writable end, when future.new minted both (the pin's `other`); nil for a future a
	// host resolves. A read that finds its write parked drives the copy through it (#1325).
	writer *writableFutureEnd
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

// hasPendingEventLocked is pendingEventLocked's test without the take (#1005).
func (e *readableFutureEnd) hasPendingEventLocked() bool { return e.resolved && !e.delivered }

// joinTo records the set this end belongs to, so a completed copy can wake its waiters.
func (e *readableFutureEnd) joinTo(s *waitableSet) { e.set = s }

// currentSet reports the set this end is joined to, so `waitable.join` can remove it before re-joining.
func (e *readableFutureEnd) currentSet() *waitableSet { return e.set }

// resolveLocked ends the read with a result and wakes any set the end is joined to. Reached where the kind
// is known (the copy completion), so the signal path is not on the waitable interface. The caller holds mu.
func (e *readableFutureEnd) resolveLocked(r copyResult) {
	e.resolved = true
	e.result = r
	if e.set != nil {
		e.set.signalLocked()
	}
}

// completeLocked ends a pending future.read with result r: on COMPLETED it copies the future's value into
// the guest memory the read named (mirroring the model's copy from the write buffer to the read buffer),
// then resolves + wakes the set. The value write precedes resolveLocked so a waiter that observes the
// resolution sees the lowered value. The caller holds asyncHandles.mu; the write may run on the host's
// goroutine while the reading agent is parked (the mutex is the happens-before). CANCELLED writes nothing.
func (e *readableFutureEnd) completeLocked(r copyResult) error {
	if r == copyCompleted && e.hasRead {
		if err := e.storeValueLocked(); err != nil {
			return err
		}
	}
	e.resolveLocked(r)
	return nil
}

// storeValueLocked writes the future's value at the pending read's pointer: completeLocked's write when the
// read was first, and futureRead's when the host's completion was (ADR 0120). The caller holds mu.
func (e *readableFutureEnd) storeValueLocked() error {
	if e.payload != nil {
		// **The codec lowers the payload, so at most `size(type)` bytes are written** (#1246). This was the
		// 4-byte write below for every future, and write-via-stream's payload is `result<_, error-code>`,
		// which is 2 bytes at alignment 1: each completion zeroed 2 guest bytes past the result. A
		// payload-less case writes only its discriminant, as the model's `store_variant` does.
		//
		// NEUTER: drop this arm so write-via-stream falls through to the 4-byte write, and
		// `TestWriteViaStreamCompletionWritesOnlyItsPayloadsBytes` fails on the bytes past the result.
		if err := canon.StoreVia(guestHeap{e.caller}, *e.payload, int(e.readPtr)); err != nil {
			return fmt.Errorf("component: future.read completion: lowering a %s at ptr: %w", e.payload.Type, err)
		}
		return nil
	}
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], e.value)
	if err := e.caller.Write(uint64(e.readPtr), buf[:]); err != nil {
		return fmt.Errorf("component: future.read completion: writing value at ptr: %w", err)
	}
	return nil
}

// futureRead implements `canon future.read` (definitions.py:canon_future_read) on the async path: register the pending
// read (the dst ptr + the reading agent's caller) and return BLOCKED. The value is not present yet; the
// host completes the future later (completeLocked), which arms the (FUTURE_READ, index, result) event that
// the existing waitable-set.wait delivers — no second wait mechanism. A read of an already-resolved end
// traps (a future is single-shot). This slice binds only the async read; a synchronous read would park
// here directly, which no #734 stdio import needs.
func futureRead(h *asyncHandles) interp.CanonFunc {
	return func(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("component: future.read: got %d args, want (i, ptr)", len(args))
		}
		i, ptr := uint32(args[0].Bits), uint32(args[1].Bits)
		h.mu.Lock()
		defer h.mu.Unlock()
		// def:copy:"trap_if(not isinstance(end, EndT))" (#1282).
		//
		// NEUTER: return a Go error, and TestAWrongKindHandleTraps's future-read-writable and future-read-stream
		// rows fail.
		fe, ok := handleAt[*readableFutureEnd](h, i)
		if !ok {
			return nil, wrongKindTrap("future.read", i, "a readable future end", "def:copy")
		}
		// **A read of a future the host has already completed is the second arriver, and completes inline**
		// (ADR 0120). The host's completion parked its value on the future first, as the reference's writer
		// parks its buffer, and the read that arrives after it copies the value and takes its own event in the
		// same call (def:End.copy:"elif buffer.remain() > 0 and self.other.buffer.remain() > 0:"). A guest that
		// drops its stream's writer before it reads write-via-stream's future has the host resolve the future
		// first, and this refused that read as single-shot.
		//
		// NEUTER: drop this arm, and TestWriteViaStreamResolvesAtTheWritersDrop's read after the drop is
		// refused. p3std does not reach it: Rust std never closes stdout, so its writer is never dropped.
		if fe.resolved && !fe.hasRead && !fe.delivered && fe.result == copyCompleted {
			fe.readPtr = ptr
			fe.hasRead = true
			fe.caller = c
			if err := fe.storeValueLocked(); err != nil {
				return nil, err
			}
			ev, _ := fe.pendingEventLocked()
			return []interp.Value{interp.I32(int32(ev.p2))}, nil
		}
		if fe.resolved || fe.hasRead {
			return nil, fmt.Errorf("component: future.read: end %d already read (a future is single-shot)", i)
		}
		fe.readPtr = ptr
		fe.hasRead = true
		fe.caller = c
		// **A read that finds the guest's write parked is the second arriver, and drives the copy** (#1325).
		// The pin has one rendezvous for both orders: whichever end's `copy` finds the other's buffer moves the
		// value and notifies both, def:End.copy:"elif buffer.remain() > 0 and self.other.buffer.remain() > 0:"
		// then def:End.copy:"self.notify(buffer.progress)" and def:End.copy:"self.other.notify(self.other.buffer.progress)".
		// So the read takes its own COMPLETED inline (def:copy:"code,index,payload = end.get_pending_event()")
		// and the write's FUTURE_WRITE COMPLETED is left pending for its wait. Only futureWrite's direction was
		// built, so writing first and then reading left both ends parked. wasmtime 49.0.3 and the pin both read
		// future-write-then-read as the read COMPLETED and the write's event COMPLETED.
		//
		// NEUTER: drop this arm, and TestAFutureReadCompletesAParkedWriteInItsOwnInstance's write-then-read and
		// write-then-read-cancel rows read the read as BLOCKED, the fixture's 0xdead place, and its
		// write-then-read-bool row does not trap.
		if w := fe.writer; w != nil && w.hasWrite && !w.resolved {
			if err := copyFutureValueLocked(w.elem, w.caller, w.srcPtr, c, ptr, "future.read"); err != nil {
				return nil, err
			}
			w.completeWriteLocked(1, copyCompleted)
			fe.resolved, fe.result = true, copyCompleted
			ev, _ := fe.pendingEventLocked()
			return []interp.Value{interp.I32(int32(ev.p2))}, nil
		}
		bits := uint32(asyncBlocked) // 0xffffffff; as a signed i32 core value this is -1 (same 32 bits)
		return []interp.Value{interp.I32(int32(bits))}, nil
	}
}

// copyFutureValueLocked is the rendezvous's copy, the second arriver's, whichever end it is: one value of `elem`
// from the writer's buffer at srcPtr to the reader's at dstPtr, each through its own end's caller
// (def:End.copy). An element that is not a number traps at the copy, as both ends are this instance's
// (def:End.copy:"trap_if(self.owner and self.owner is self.other.owner and not none_or_number_type(self.t))"),
// and a float's NaN takes the canonical one. `op` names the built-in that arrived second. The caller holds mu.
func copyFutureValueLocked(elem elemType, src *interp.CanonCaller, srcPtr uint32, dst *interp.CanonCaller, dstPtr uint32, op string) error {
	if !elem.known {
		return fmt.Errorf("%w: %s's copy: the future's element type did not resolve (#1006)", ErrUnsupportedForm, op)
	}
	if !elem.numeric {
		return &interp.Trap{Reason: op + ": cannot read from and write to an intra-instance future " +
			"with a non-numeric payload (the pin's End.copy: trap_if(... not none_or_number_type))"}
	}
	if elem.size == 0 {
		return nil
	}
	buf, err := src.Read(uint64(srcPtr), uint64(elem.size))
	if err != nil {
		return &interp.Trap{Reason: fmt.Sprintf("%s: reading the value at the write's %d: %v", op, srcPtr, err)}
	}
	canonicalizeFloats(buf, elem.float)
	if err := dst.Write(uint64(dstPtr), buf); err != nil {
		return &interp.Trap{Reason: fmt.Sprintf("%s: storing the value at the read's %d: %v", op, dstPtr, err)}
	}
	return nil
}

// futureDrop implements `canon future.drop-readable` (definitions.py:canon_future_drop_readable): remove the readable end from the
// table. The guest drops after its read has delivered; slice-1 removes the entry (the end's lifetime is the
// instance's, freed at Close) without the model's writable-side DONE trap, which is a future.write concern.
func futureDrop(h *asyncHandles) interp.CanonFunc {
	return func(_ *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("component: future.drop-readable: got %d args, want (i)", len(args))
		}
		i := uint32(args[0].Bits)
		h.mu.Lock()
		defer h.mu.Unlock()
		// def:drop:"trap_if(not isinstance(end, EndT))" (#1282).
		//
		// NEUTER: return a Go error, and TestAWrongKindHandleTraps's future-drop-readable-writable row fails.
		fe, ok := handleAt[*readableFutureEnd](h, i)
		if !ok {
			return nil, wrongKindTrap("future.drop-readable", i, "a readable future end", "def:drop")
		}
		if fe.completeErr != nil { // a deferred host-completion failure surfaces here rather than being lost
			return nil, fmt.Errorf("component: future.drop-readable: end %d had a failed completion: %w", i, fe.completeErr)
		}
		// **A dropped end leaves its waitable set** (grave #1296): every drop is def:Waitable.drop, which is
		// `self.join(None)`. Without it the set kept the end as a member, and a later waitable-set.drop trapped
		// "still has N joined waitable(s)" where the pin and wasmtime 49.0.3 succeed.
		//
		// NEUTER: drop this line, and TestADroppedWaitableLeavesItsSet's future row traps at the set's drop.
		joinWaitableLocked(fe, nil)
		h.entries[i] = nil
		return nil, nil
	}
}

// writableFutureEnd is the writable half of a future<T> (2nd async guest, #785, the cancellation witness) —
// the future analog of writableStreamEnd, through the SAME copy/cancel protocol. A future is single-value:
// a pending future.write with no reader parks (BLOCKED); a future.cancel-write resolves it to CANCELLED
// inline, leaving the end IDLE (open) — only DROPPED goes DONE. All fields guarded by asyncHandles' mutex.
type writableFutureEnd struct {
	index     int
	set       *waitableSet // the set it is joined to, if any
	resolved  bool         // this write has an event to deliver
	delivered bool         // the FUTURE_WRITE event has been taken by a waitable-set.wait
	done      bool         // DONE (dropped, or COMPLETED: a future is one value); a CANCELLED write leaves it IDLE
	result    copyResult
	progress  uint32             // 0 or 1 (a future is one value); NOT in the event, which is the bare result
	hasWrite  bool               // a future.write is pending on this end
	peer      *readableFutureEnd // the paired readable end (future.new)
	// The parked write's buffer, for the read that arrives second to copy from (#1325): where the value is, the
	// writing agent's caller (whose memory it is in), and the element future.write named.
	srcPtr uint32
	caller *interp.CanonCaller
	elem   elemType
}

// pendingEventLocked delivers the write's (FUTURE_WRITE, index, result) event once. The caller holds
// asyncHandles.mu.
//
// **A future's event payload is the bare CopyResult** (#1325): def:FutureEnd.notify.future_event+ returns
// `(self.event_code, self.index, result)`, where a stream's packs `result | (progress << 4)`. This packed the
// progress, which no path reached while every write event was a cancel's, of progress 0. The read that completes
// a parked write is a progress of 1. wasmtime 49.0.3 reads that event's payload as 0.
//
// NEUTER: pack `e.progress << 4` again, and TestAFutureReadCompletesAParkedWriteInItsOwnInstance's write-then-read
// row reads the event payload as 16, and its write-then-read-cancel row reads the cancel's return as 16.
func (e *writableFutureEnd) pendingEventLocked() (event, bool) {
	if e.resolved && !e.delivered {
		e.delivered = true
		return event{code: eventFutureWrite, p1: uint32(e.index), p2: uint32(e.result)}, true
	}
	return event{}, false
}

// hasPendingEventLocked is pendingEventLocked's test without the take (#1005).
func (e *writableFutureEnd) hasPendingEventLocked() bool { return e.resolved && !e.delivered }

func (e *writableFutureEnd) joinTo(s *waitableSet) { e.set = s }

// currentSet reports the set this end is joined to, so `waitable.join` can remove it before re-joining.
func (e *writableFutureEnd) currentSet() *waitableSet { return e.set }

// completeWriteLocked ends a pending future.write with result r (COMPLETED when a read copied its value, #1325;
// CANCELLED for a cancel; DROPPED if the read end drops), arms the event, and wakes the set. DROPPED and a
// COMPLETED of progress 1 go DONE, as def:FutureEnd.notify.future_event+ sets `End.State.DONE` for both (a future
// is one value); CANCELLED stays IDLE. Caller holds mu.
func (e *writableFutureEnd) completeWriteLocked(progress uint32, r copyResult) {
	e.progress = progress
	e.result = r
	e.resolved = true
	if r == copyDropped || (r == copyCompleted && progress == 1) {
		e.done = true
	}
	if e.set != nil {
		e.set.signalLocked()
	}
}

// futureNew implements `canon future.new` (0x15, definitions.py:canon_future_new): mint a connected readable+writable
// future end pair, return `ri | (wi<<32)` — the same packed shape as stream.new. The cancellation path uses
// the writable end (write then cancel); the readable end is the pair the model always mints.
func futureNew(h *asyncHandles) interp.CanonFunc {
	return func(_ *interp.CanonCaller, _ []interp.Value) ([]interp.Value, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		re := &readableFutureEnd{}
		we := &writableFutureEnd{peer: re} // the pin's one `SharedFutureImpl` behind both ends (#1006)
		re.writer = we                     // and the way back, for a read that arrives second (#1325)
		re.index = h.addLocked(re)
		we.index = h.addLocked(we)
		packed := uint64(uint32(re.index)) | uint64(uint32(we.index))<<32
		return []interp.Value{interp.I64(int64(packed))}, nil
	}
}

// futureWrite implements `canon future.write` (0x17, definitions.py:canon_future_write) on the async path: a single-value
// write. With no reader on the paired end (the cancellation guest's shape), the write parks and returns
// BLOCKED; the guest then cancels it.
//
// # A write to a pending read copies inline (#1006)
//
// The pin's `future_copy` finds the reader on the shared future and completes the copy in the writer's call:
// the value moves from the write buffer to the read buffer, the reader's end gets its (FUTURE_READ, i,
// COMPLETED) event, and the write returns COMPLETED. Burroughs left both ends pending forever.
//
// Measured before the fix, on what was then the default path (`gate:cm-threads` off, the default until the
// 2026-10-17 flip), with `future-self-copy.wasm`: one
// callback task reads its own future (BLOCKED), writes 7, then WAITs. wasmtime 49.0.2, the previous pin
// (2bed77e) and the pin (2e7895c) all read 262151 (event 4, write status 0, byte 7). Burroughs did not return.
//
// # Which elements copy, and which trap (the chair's item 3, read on both oracles first)
//
// `elem` classifies the element as the pin's `End.copy` does (`futureElemOf`):
//
//	element           wasmtime 49.0.2                      pin 2e7895c                  Burroughs
//	integers          the value                            the value                    byte copy
//	f32 1.5           1069809664                           1069809664                   byte copy
//	f32 NaN payload   bits preserved (2143551489)          {canonical, any NaN}         canonical NaN (2143551488)
//	f64 NaN payload   bits preserved (262145)              {canonical, any NaN}         canonical NaN (262144)
//	bool, char        trap: non-numeric payload            trap at the copy             trap at the copy
//
// The floats take the pin's deterministic-profile outcome, which is the codec's own rule
// (`canon.CanonicalizeNaN32`/`64`). wasmtime's preserved payload is also inside the pin's set.
//
// **The other direction is futureRead's** (#1325): a READ arriving at a parked write drives the same copy
// (copyFutureValueLocked). This paragraph read *"only this direction is built"* until it was.
//
// `elem` is the element of the type future.write names, which the decoder keeps since #1271 (ADR 0124); it classified
// type 0's for every future.
func futureWrite(h *asyncHandles, elem elemType) interp.CanonFunc {
	return func(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("component: future.write: got %d args, want (i, ptr)", len(args))
		}
		i, ptr := uint32(args[0].Bits), uint32(args[1].Bits)
		h.mu.Lock()
		defer h.mu.Unlock()
		// def:copy:"trap_if(not isinstance(end, EndT))" (#1282).
		//
		// NEUTER: return a Go error, and TestAWrongKindHandleTraps's future-write-readable row fails.
		e, ok := handleAt[*writableFutureEnd](h, i)
		if !ok {
			return nil, wrongKindTrap("future.write", i, "a writable future end", "def:copy")
		}
		if e.done || e.hasWrite || e.resolved {
			return nil, &interp.Trap{Reason: fmt.Sprintf("future.write: end %d is not writable (done, write pending, or resolved)", i)}
		}
		if r := e.peer; r != nil && r.hasRead && !r.resolved {
			if err := copyFutureValueLocked(elem, c, ptr, r.caller, r.readPtr, "future.write"); err != nil {
				return nil, err
			}
			r.resolveLocked(copyCompleted)
			e.done = true // a future is one value: the writable end is DONE after COMPLETED
			return []interp.Value{interp.I32(int32(copyCompleted))}, nil
		}
		// The write parks with its buffer, which the read that arrives second copies from (futureRead, #1325).
		e.srcPtr, e.caller, e.elem = ptr, c, elem
		e.hasWrite = true
		bits := uint32(asyncBlocked) // 0xffffffff; as a signed i32 core value this is -1 (same 32 bits)
		return []interp.Value{interp.I32(int32(bits))}, nil
	}
}

// futureCancelWrite implements `canon future.cancel-write` (0x19, definitions.py:canon_future_cancel_write -> cancel_copy): cancel
// a write in flight, resolving it to CANCELLED inline and returning the packed `result|progress<<4`. The
// future analog of stream.cancel-write, through the same cancel_copy substrate. Pinned by fixtures.json
// future_cancel_write (the future WRITE arm's running CANCELLED; progress 0; the end left IDLE).
func futureCancelWrite(h *asyncHandles) interp.CanonFunc {
	return func(_ *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("component: future.cancel-write: got %d args, want (i)", len(args))
		}
		i := uint32(args[0].Bits)
		h.mu.Lock()
		defer h.mu.Unlock()
		// def:cancel_copy:"trap_if(not isinstance(end, EndT))" (#1282).
		//
		// NEUTER: return a Go error, and TestAWrongKindHandleTraps's future-cancel-write-readable row fails.
		e, ok := handleAt[*writableFutureEnd](h, i)
		if !ok {
			return nil, wrongKindTrap("future.cancel-write", i, "a writable future end", "def:cancel_copy")
		}
		// **A write a read has completed is still COPYING until its event is taken, so it is cancellable, and the
		// cancel takes the COMPLETED** (#1325). The pin's cancel finds the event pending and notifies nothing
		// (def:End.cancel:"if (not self.has_pending_event()"), and the event it takes is the progress-1 arm of
		// def:FutureEnd.notify.future_event+, COMPLETED, ahead of the CANCELLING_COPY arm. wasmtime 49.0.3 reads 0.
		// Before the read drove the copy, no write could be resolved with its event untaken except by this cancel.
		//
		// NEUTER: trap on `e.resolved` again, and TestAFutureReadCompletesAParkedWriteInItsOwnInstance's
		// write-then-read-cancel row traps "no write in flight".
		if !e.hasWrite || e.delivered {
			return nil, &interp.Trap{Reason: fmt.Sprintf("future.cancel-write: end %d has no write in flight (COPYING)", i)}
		}
		if !e.resolved {
			e.completeWriteLocked(e.progress, copyCancelled) // progress 0 for an undriven pending write
		}
		e.hasWrite = false
		ev, ok := e.pendingEventLocked() // consume inline -> IDLE (CANCELLED)
		if !ok {
			return nil, &interp.Trap{Reason: fmt.Sprintf("future.cancel-write: end %d had no deliverable event after cancel", i)}
		}
		return []interp.Value{interp.I32(int32(ev.p2))}, nil
	}
}
