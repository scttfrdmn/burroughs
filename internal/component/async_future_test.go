// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"encoding/binary"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/interp"
)

type futureReadFixture struct {
	Name      string `json:"name"`
	ValueType string `json:"value_type"`
	Outcome   string `json:"outcome"`
	ReadRet   []int  `json:"read_ret"`
	Subtaski  int    `json:"subtaski"`
	EventCode *int   `json:"event_code"`
	Payload   int    `json:"payload"`
	RiState   string `json:"ri_state"`
	Value     *int   `json:"value"`
	DstVal    *int   `json:"dst_val"`
}

func loadFutureReads(t *testing.T) []futureReadFixture {
	t.Helper()
	var doc struct {
		FutureReads []futureReadFixture `json:"future_reads"`
	}
	loadFixtures(t, &doc)
	if len(doc.FutureReads) == 0 {
		t.Fatal("no future_reads in fixtures.json")
	}
	return doc.FutureReads
}

// endStateName derives the readable end's post-resolution state the model tracks (DONE for COMPLETED,
// IDLE for CANCELLED — definitions.py future_event) from the resolved fields, so the test asserts it
// alongside the payload. The end state is the second guard: a mis-encoded outcome fails twice — wrong
// guest branch now, wrong trap-legality on the next operation, which surfaces far from its cause.
func endStateName(e *readableFutureEnd) string {
	switch {
	case !e.resolved:
		return "IDLE"
	case e.result == copyCancelled:
		return "IDLE"
	default:
		return "DONE"
	}
}

// TestFutureReadDeliversTheOracleOutcomes pins future.read against the committed oracle (future_reads) for
// BOTH guest-reachable read outcomes, WITH their differing end state — not just success. The async read
// returns BLOCKED and registers a pending read; the host completes it (COMPLETED copies the value, CANCELLED
// copies nothing); the SAME waitable-set loop delivers the (FUTURE_READ, index, CopyResult) event. Matches
// read_ret / payload / end state / value byte-for-byte with the model. (CANCELLED is driven here via a
// direct completion because future.cancel-read is refused by name this slice — it is the deliberate
// inclusion the oracle pins so the codec distinguishes it from success rather than hardcoding COMPLETED.)
func TestFutureReadDeliversTheOracleOutcomes(t *testing.T) {
	for _, fx := range loadFutureReads(t) {
		t.Run(fx.Name, func(t *testing.T) {
			h := newAsyncHandles()
			fe := &readableFutureEnd{}
			if fx.Value != nil {
				fe.value = uint32(*fx.Value)
			}
			fe.index = h.addLocked(fe)
			if fe.index != fx.Subtaski {
				t.Fatalf("readable-end index = %d, want %d (oracle)", fe.index, fx.Subtaski)
			}

			cc, err := interp.NewCanonCallerForTest(1)
			if err != nil {
				t.Fatalf("harness caller: %v", err)
			}
			const ptr = 32
			readRet, err := futureRead(h)(cc, []interp.Value{interp.I32(int32(fe.index)), interp.I32(ptr)})
			if err != nil {
				t.Fatalf("future.read: %v", err)
			}
			if uint32(readRet[0].Int32()) != uint32(fx.ReadRet[0]) {
				t.Errorf("future.read = %#x, want %#x (BLOCKED) — the async read must park", uint32(readRet[0].Int32()), uint32(fx.ReadRet[0]))
			}

			// Join the readable end to a set, then complete it per the fixture's outcome.
			siVals, err := waitableSetNew(h)(cc, nil)
			if err != nil {
				t.Fatalf("waitable-set.new: %v", err)
			}
			si := siVals[0].Int32()
			if _, jerr := waitableJoin(h)(cc, []interp.Value{interp.I32(int32(fe.index)), interp.I32(si)}); jerr != nil {
				t.Fatalf("waitable.join: %v", jerr)
			}
			result := copyCompleted
			if fx.Outcome == "cancelled" {
				result = copyCancelled
			}
			h.mu.Lock()
			cerr := fe.completeLocked(result)
			h.mu.Unlock()
			if cerr != nil {
				t.Fatalf("completing the read: %v", cerr)
			}

			// waitable-set.wait delivers the FUTURE_READ event; check code, payload, and the end state.
			const evptr = 48
			codeVals, werr := waitableSetWait(h)(cc, []interp.Value{interp.I32(si), interp.I32(evptr)})
			if werr != nil {
				t.Fatalf("waitable-set.wait: %v", werr)
			}
			if fx.EventCode != nil && codeVals[0].Int32() != int32(*fx.EventCode) {
				t.Errorf("event code = %d, want %d (FUTURE_READ)", codeVals[0].Int32(), *fx.EventCode)
			}
			buf, rerr := cc.Read(evptr, 8)
			if rerr != nil {
				t.Fatalf("reading event: %v", rerr)
			}
			if p1 := binary.LittleEndian.Uint32(buf[0:4]); p1 != uint32(fx.Subtaski) {
				t.Errorf("event p1 = %d, want %d (the end index)", p1, fx.Subtaski)
			}
			if p2 := binary.LittleEndian.Uint32(buf[4:8]); p2 != uint32(fx.Payload) {
				t.Errorf("event payload = %d, want %d (CopyResult)", p2, fx.Payload)
			}
			if got := endStateName(fe); got != fx.RiState {
				t.Errorf("end state = %s, want %s — the end state is part of the outcome, not just the payload", got, fx.RiState)
			}
			// On COMPLETED the value is copied to the read ptr; on CANCELLED nothing is written.
			if fx.DstVal != nil {
				vbuf, verr := cc.Read(ptr, 4)
				if verr != nil {
					t.Fatalf("reading value at ptr: %v", verr)
				}
				if got := binary.LittleEndian.Uint32(vbuf); got != uint32(*fx.DstVal) {
					t.Errorf("value at ptr = %d, want %d (the delivered future value)", got, *fx.DstVal)
				}
			}
		})
	}
}
