// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"testing"

	"github.com/scttfrdmn/burroughs/internal/interp"
)

// TestAStreamWriteEndIsIdleOnceItsEventIsTaken is the writable end's idle-on-event (ADR 0120), guest to guest
// in the async-stream tests' harness: two ends from stream.new, with hostReadLocked standing in for a guest's
// stream.read, which is not bound, so no .wat can express the reader. In the reference, taking an end's event
// returns it to IDLE (def:StreamEnd.notify.stream_event), and a copy starts only from IDLE (def:copy). Three
// arms, each a write the end's leftover per-copy state used to get wrong once its reader re-armed:
//
//   - a second host-first write reports its own progress, where `delivered` left set made it report 0;
//   - a write after a guest-first completion whose event was taken is accepted, where `hasWrite` left set
//     refused it as "not writable";
//   - a write of 0 elements completes the writer only, in both orders, where driving the copy armed the
//     reader too (def:End.copy's last two arms).
//
// Neutered, each run: streamWrite's reset dropped, and the second write reports progress 0; hostReadLocked's
// reset dropped, and the second write parks BLOCKED; the hasWrite clear dropped, and the later write is
// refused; streamWrite's zero-length arm dropped, and the parked reader is armed; hostReadLocked's dropped, and
// the read at a parked write of 0 completes.
func TestAStreamWriteEndIsIdleOnceItsEventIsTaken(t *testing.T) {
	type pair struct {
		h      *asyncHandles
		cc     *interp.CanonCaller
		re     *readableStreamEnd
		we     *writableStreamEnd
		wi     uint32
		srcPtr uint32
	}
	const dstPtr = 40
	newPair := func(t *testing.T) pair {
		t.Helper()
		h := newAsyncHandles()
		cc, err := interp.NewCanonCallerForTest(1)
		if err != nil {
			t.Fatalf("harness caller: %v", err)
		}
		ret, err := streamNew(h, byteElem)(cc, nil)
		if err != nil {
			t.Fatalf("stream.new: %v", err)
		}
		packed := uint64(ret[0].Int64())
		ri, wi := uint32(packed&0xffffffff), uint32(packed>>32)
		re, _ := handleAt[*readableStreamEnd](h, ri)
		we, _ := handleAt[*writableStreamEnd](h, wi)
		p := pair{h: h, cc: cc, re: re, we: we, wi: wi, srcPtr: dstPtr + 64}
		if werr := cc.Write(uint64(p.srcPtr), []byte("abcdefgh")); werr != nil {
			t.Fatalf("seeding src: %v", werr)
		}
		return p
	}
	write := func(t *testing.T, p pair, n uint32) uint32 {
		t.Helper()
		ret, err := streamWrite(p.h, byteElem)(p.cc, []interp.Value{interp.I32(int32(p.wi)), interp.I32(int32(p.srcPtr)), interp.I32(int32(n))})
		if err != nil {
			t.Fatalf("stream.write of %d: %v", n, err)
		}
		return uint32(ret[0].Int32())
	}
	read := func(t *testing.T, p pair, n uint32) bool {
		t.Helper()
		p.h.mu.Lock()
		defer p.h.mu.Unlock()
		completed, err := p.re.hostReadLocked(p.cc, dstPtr, n)
		if err != nil {
			t.Fatalf("read of %d: %v", n, err)
		}
		return completed
	}
	take := func(w waitable, h *asyncHandles) (event, bool) { // what a waitable-set.wait does with an end
		h.mu.Lock()
		defer h.mu.Unlock()
		return w.pendingEventLocked()
	}

	t.Run("second host-first write reports its own progress", func(t *testing.T) {
		p := newPair(t)
		read(t, p, 8)
		if got := write(t, p, 3); got != 3<<4 {
			t.Fatalf("first write = %#x, want COMPLETED with progress 3 (%#x)", got, 3<<4)
		}
		if _, ok := take(p.re, p.h); !ok {
			t.Fatal("the reader had no event to take after the first copy")
		}
		read(t, p, 8)
		if got := write(t, p, 2); got != 2<<4 {
			t.Errorf("second write = %#x (progress %d), want COMPLETED with progress 2 (%#x): an end kept its "+
				"last copy's state", got, got>>4, 2<<4)
		}
	})

	t.Run("a write after a taken guest-first completion is accepted", func(t *testing.T) {
		p := newPair(t)
		if got := write(t, p, 3); got != asyncBlocked {
			t.Fatalf("write with no reader = %#x, want BLOCKED", got)
		}
		if !read(t, p, 8) {
			t.Fatal("the read arriving at a parked write did not complete it")
		}
		ev, ok := take(p.we, p.h)
		if !ok || ev.p2 != 3<<4 {
			t.Fatalf("the writer's event = %+v (taken %v), want COMPLETED with progress 3", ev, ok)
		}
		ret, err := streamWrite(p.h, byteElem)(p.cc, []interp.Value{interp.I32(int32(p.wi)), interp.I32(int32(p.srcPtr)), interp.I32(1)})
		if err != nil {
			t.Fatalf("a write after the event was taken was refused: %v", err)
		}
		if got := uint32(ret[0].Int32()); got != asyncBlocked {
			t.Errorf("the next write with no reader = %#x, want BLOCKED", got)
		}
	})

	t.Run("a write of 0 completes the writer only, reader first", func(t *testing.T) {
		p := newPair(t)
		read(t, p, 8)
		if got := write(t, p, 0); got != 0 {
			t.Fatalf("write of 0 = %#x, want COMPLETED with progress 0", got)
		}
		if p.re.resolved {
			t.Fatal("the write of 0 armed the parked reader; the reference notifies only the writer")
		}
		if got := write(t, p, 3); got != 3<<4 {
			t.Errorf("the next write = %#x, want COMPLETED with progress 3 into the still-parked read", got)
		}
	})

	t.Run("a write of 0 completes the writer only, writer first", func(t *testing.T) {
		p := newPair(t)
		if got := write(t, p, 0); got != asyncBlocked {
			t.Fatalf("write of 0 with no reader = %#x, want BLOCKED", got)
		}
		if read(t, p, 8) {
			t.Fatal("the read arriving at a parked write of 0 completed; the reference parks it")
		}
		if ev, ok := take(p.we, p.h); !ok || ev.p2 != 0 {
			t.Fatalf("the writer's event = %+v (taken %v), want COMPLETED with progress 0", ev, ok)
		}
		if p.re.resolved {
			t.Error("the reader was armed by a write of 0")
		}
	})
}
