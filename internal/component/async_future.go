// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"encoding/binary"
	"fmt"

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

// copyResult mirrors definitions.py CopyResult (def:919–922): how a copy ended, the value the guest
// branches on. A future READ is only ever COMPLETED or CANCELLED — DROPPED is a future.write outcome (a
// read can never be dropped), pinned when write lands.
type copyResult uint32

const (
	copyCompleted copyResult = 0
	copyDropped   copyResult = 1
	copyCancelled copyResult = 2
)

// asyncBlocked is the packed return of an async copy that did not resolve inline (definitions.py BLOCKED =
// 0xffff_ffff, def:2412): future.read returns it and the guest awaits the (FUTURE_READ, …) event via
// waitable-set.wait.
const asyncBlocked = 0xffffffff

// readableFutureEnd is the readable half of a future<T>: a waitable whose copy, once resolved, has a
// (FUTURE_READ, index, result) pending event. Its end state after resolution differs by outcome — DONE for
// COMPLETED, IDLE for CANCELLED (definitions.py future_event, def:2531+) — which the next operation's
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
	value     uint32              // the value this future delivers on COMPLETED (future<u32>, first slice)
	readPtr   uint32              // where a pending future.read wants the value written
	hasRead   bool                // a future.read is pending on this end
	caller    *interp.CanonCaller // the reading agent's caller, for the completion's memory write
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

// completeLocked ends a pending future.read with result r: on COMPLETED it copies the future's value into
// the guest memory the read named (mirroring the model's copy from the write buffer to the read buffer),
// then resolves + wakes the set. The value write precedes resolveLocked so a waiter that observes the
// resolution sees the lowered value. The caller holds asyncHandles.mu; the write may run on the host's
// goroutine while the reading agent is parked (the mutex is the happens-before). CANCELLED writes nothing.
func (e *readableFutureEnd) completeLocked(r copyResult) error {
	if r == copyCompleted && e.hasRead {
		var buf [4]byte
		binary.LittleEndian.PutUint32(buf[:], e.value)
		if err := e.caller.Write(uint64(e.readPtr), buf[:]); err != nil {
			return fmt.Errorf("component: future.read completion: writing value at ptr: %w", err)
		}
	}
	e.resolveLocked(r)
	return nil
}

// futureRead implements `canon future.read` (definitions.py:2523) on the async path: register the pending
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
		fe, ok := handleAt[*readableFutureEnd](h, i)
		if !ok {
			return nil, fmt.Errorf("component: future.read: handle %d is not a readable future end", i)
		}
		if fe.resolved || fe.hasRead {
			return nil, fmt.Errorf("component: future.read: end %d already read (a future is single-shot)", i)
		}
		fe.readPtr = ptr
		fe.hasRead = true
		fe.caller = c
		bits := uint32(asyncBlocked) // 0xffffffff; as a signed i32 core value this is -1 (same 32 bits)
		return []interp.Value{interp.I32(int32(bits))}, nil
	}
}

// futureDrop implements `canon future.drop-readable` (definitions.py:2611): remove the readable end from the
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
		if _, ok := handleAt[*readableFutureEnd](h, i); !ok {
			return nil, fmt.Errorf("component: future.drop-readable: handle %d is not a readable future end", i)
		}
		h.entries[i] = nil
		return nil, nil
	}
}
