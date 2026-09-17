// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"encoding/binary"
	"io"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// TestBMM1AsyncWakeIsAnAcquireEdgeOverTheAddressSpace is the litmus battery case
// `b-mm-1-async-wake-is-an-acquire-edge` (contract §4 B-MM-1, the async-wake crossing): every host→guest
// transition — the amendment on #743 names "async wake" among them — MUST constitute an acquire edge over
// the ENTIRE shared address space for the resuming agent. This witnesses the async subtask-completion path,
// the engine-carried A→B edge the #742 recon identified (Q4 ruled: a host publisher is the only publisher
// B-MM-1's clause admits, its four crossings all being host→guest). SP-6 (#779) witnesses the sibling
// Resume-after-Stop crossing the amendment cross-references; this one is the async wake.
//
// B-MM-1 closes as a STRUCTURAL guarantee, not a discriminated weak-memory one (the #742 recon: the arm64
// value window is marginal and every boundary crossing's acquire edge is inherent to its mechanism). The
// structural argument here is STRONGER than SP-6's redundant-edge one: at the async-wake crossing the
// acquire edge is not merely carried redundantly — it is IDENTICAL to the wake delivery. The parked guest
// resumes by receiving on the set's `wake` channel (async_waitset.go: waitable-set.wait selects on it inside
// CanonCaller.Blocking); a Go channel close happens-before the receive that observes it, so the very act of
// waking the guest establishes happens-before over everything the waker wrote before signalLocked's close.
// There is no way to deliver the wake WITHOUT the acquire edge, because delivering the wake IS a close→receive.
//
// Positive (below): the host writes four words spread low/mid/high across the page while agent A is parked
// in waitable-set.wait, then resolves the subtask (the async wake). A resumes, reads the four spread words
// with guest typed loads, and returns a match mask against their published sentinels — 0xF means all four
// were seen across the wake. It passes under `-race`: the stop-time host writes are ordered before the
// resumed guest loads by the wake edge, so the detector reports nothing.
//
// Second witness — the named-edge-removal control (SP-6's form): the ordering-carrying edges at this
// crossing are the `wake` channel (close→receive) and asyncHandles' mutex (which onResolve/async_waitset.go
// name). Both are load-bearing for the wake ITSELF — the channel delivers the resume, the mutex guards the
// readiness state a waiter reads — so neither can be removed to leave the other carrying it: removing the
// channel means the guest never wakes, removing the mutex corrupts the handle table (a Go-struct race, not a
// guest-memory one). This crossing therefore does not admit SP-6's redundant-edge control, and that is
// itself the finding (recorded in the registration): the async-wake acquire edge is not redundant, it is the
// wake. The run confirming both removals break the mechanism rather than testing it is documented in the PR;
// per the case discipline the broken injections are not landed.
//
// Shape (#742 tagging finding, condition 1): the clause is "the ENTIRE shared address space", so the guest
// reads SPREAD words — low/mid/high across the page — not one location. That samples the address space; no
// finite test proves every address, so this is sampled coverage, stated as such, not full. The falsifying
// instrument is a wake path that delivers WITHOUT a happens-before edge — a plain flag polled without
// synchronization, i.e. a different engine. Arbiter: `-race`, both arches. Run under -race per the discipline.
func TestBMM1AsyncWakeIsAnAcquireEdgeOverTheAddressSpace(t *testing.T) {
	t.Setenv("BURROUGHS_ASYNC", "1")
	const deadline = 10 * time.Second
	// Spread low/mid/high across the 64KiB page — sampling the whole address space, not one word. Disjoint
	// sentinels so a stale read (the pre-write 0) clears its own bit in the mask and nothing else's.
	type word struct {
		addr uint32
		val  uint32
		bit  int32
	}
	words := []word{
		{0x100, 0x11111111, 1},
		{0x4000, 0x22222222, 2},
		{0x8000, 0x33333333, 4},
		{0xF000, 0x44444444, 8},
	}
	const allSeen = int32(0xF)

	b, err := os.ReadFile("testdata/async-wake-bmm1-synth.wasm")
	if err != nil {
		t.Fatalf("synth fixture: %v", err)
	}

	// The op impl STARTS but defers resolution, capturing the caller and its resolver so the test resolves
	// (the async wake) after publishing the spread words — while agent A is parked in waitable-set.wait.
	type resolveReq struct {
		c       *interp.CanonCaller
		resolve func(canon.Value)
	}
	reqs := make(chan resolveReq, 1)
	var entered int32
	h := NewHost(io.Discard, io.Discard, nil)
	h.asyncImpls = map[string]asyncLowerImpl{
		"test:async/ops::op": func(c *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
			onStart()
			atomic.AddInt32(&entered, 1)
			reqs <- resolveReq{c: c, resolve: onResolve}
			return func() {}, nil
		},
	}

	in, err := InstantiateWithHost(b, h)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()
	ci := coreInstanceWithExport(in, "run")
	if ci == nil {
		t.Fatal("no core instance exports run")
	}

	// Publisher: write the four spread words host-side while A is parked, THEN resolve (the async wake). The
	// writes happen-before the wake by program order on this goroutine; the wake carries them to A. A write
	// error is reported on writeErr rather than via t.Fatal (this is not the test goroutine).
	writeErr := make(chan error, 1)
	go func() {
		r := <-reqs
		for _, w := range words {
			var buf [4]byte
			binary.LittleEndian.PutUint32(buf[:], w.val)
			if werr := r.c.Write(uint64(w.addr), buf[:]); werr != nil {
				writeErr <- werr
				return
			}
		}
		writeErr <- nil
		r.resolve(canon.U32(107)) // the async wake: signalLocked closes the set's wake channel
	}()

	res, err := invokeWithTimeout(t, ci, "run", deadline)
	if err != nil {
		t.Fatalf("Invoke(run): %v", err)
	}
	if werr := <-writeErr; werr != nil {
		t.Fatalf("publishing a spread word host-side: %v — premise, not assertion", werr)
	}
	if len(res) != 1 {
		t.Fatalf("run returned %d values, want 1", len(res))
	}
	if got := res[0].Int32(); got != allSeen {
		// Which bit is clear names which spread word the resumed agent did NOT see across the async wake.
		for _, w := range words {
			if got&w.bit == 0 {
				t.Errorf("spread word at %#x (sentinel %#x) not seen across the async wake — B-MM-1 forbidden: "+
					"the resuming agent did not acquire a host write ordered before the wake", w.addr, w.val)
			}
		}
		t.Fatalf("run = %#x, want %#x (all four spread words seen)", got, allSeen)
	}
	if n := atomic.LoadInt32(&entered); n != 1 {
		t.Errorf("op impl entered %d times, want 1", n)
	}
}
