// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"encoding/binary"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/interp"
)

type streamWriteFixture struct {
	Name     string `json:"name"`
	Outcome  string `json:"outcome"`
	N        int    `json:"n"`
	M        *int   `json:"m"`
	WriteRet []int  `json:"write_ret"`
	Wi       int    `json:"wi"`
	Packed   int    `json:"packed"`
	Result   int    `json:"result"`
	Progress int    `json:"progress"`
	WiState  string `json:"wi_state"`
}

func loadStreamWrites(t *testing.T) []streamWriteFixture {
	t.Helper()
	var doc struct {
		StreamWrites []streamWriteFixture `json:"stream_writes"`
	}
	loadFixtures(t, &doc)
	if len(doc.StreamWrites) == 0 {
		t.Fatal("no stream_writes in fixtures.json")
	}
	return doc.StreamWrites
}

// TestStreamWriteDeliversTheOracleOutcomes pins stream.write against the committed oracle (stream_writes)
// for all three outcomes WITH the two things stream adds over future.read: progress packing and the
// IDLE-on-COMPLETED end state. The async write returns BLOCKED and registers the pending write; the host
// consumer takes some elements (COMPLETED, end IDLE — open for more) or the readable end drops (DROPPED,
// end DONE); the SAME waitable-set loop delivers (STREAM_WRITE, index, result|progress<<4). A codec that
// drops the progress field passes the full case and fails the partial one; a Go that inherits future's
// DONE-on-COMPLETED fails the end-state check.
func TestStreamWriteDeliversTheOracleOutcomes(t *testing.T) {
	for _, fx := range loadStreamWrites(t) {
		t.Run(fx.Name, func(t *testing.T) {
			h := newAsyncHandles()
			se := &writableStreamEnd{}
			se.index = h.addLocked(se)

			cc, err := interp.NewCanonCallerForTest(1)
			if err != nil {
				t.Fatalf("harness caller: %v", err)
			}
			const ptr = 32
			writeRet, err := streamWrite(h)(cc, []interp.Value{interp.I32(int32(se.index)), interp.I32(ptr), interp.I32(int32(fx.N))})
			if err != nil {
				t.Fatalf("stream.write: %v", err)
			}
			if uint32(writeRet[0].Int32()) != uint32(fx.WriteRet[0]) {
				t.Errorf("stream.write = %#x, want %#x (BLOCKED)", uint32(writeRet[0].Int32()), uint32(fx.WriteRet[0]))
			}

			siVals, err := waitableSetNew(h)(cc, nil)
			if err != nil {
				t.Fatalf("waitable-set.new: %v", err)
			}
			si := siVals[0].Int32()
			if _, jerr := waitableJoin(h)(cc, []interp.Value{interp.I32(int32(se.index)), interp.I32(si)}); jerr != nil {
				t.Fatalf("waitable.join: %v", jerr)
			}

			// The host consumer takes fx.Progress elements (COMPLETED) or the readable end drops (DROPPED).
			result := copyCompleted
			if fx.Outcome == "dropped" {
				result = copyDropped
			}
			h.mu.Lock()
			se.completeWriteLocked(uint32(fx.Progress), result)
			h.mu.Unlock()

			const evptr = 48
			codeVals, werr := waitableSetWait(h)(cc, []interp.Value{interp.I32(si), interp.I32(evptr)})
			if werr != nil {
				t.Fatalf("waitable-set.wait: %v", werr)
			}
			if codeVals[0].Int32() != int32(eventStreamWrite) {
				t.Errorf("event code = %d, want %d (STREAM_WRITE)", codeVals[0].Int32(), eventStreamWrite)
			}
			buf, rerr := cc.Read(evptr, 8)
			if rerr != nil {
				t.Fatalf("reading event: %v", rerr)
			}
			p2 := binary.LittleEndian.Uint32(buf[4:8])
			// The packed payload carries progress in the high bits — the hazard the partial case exercises.
			wantPacked := uint32(fx.Result) | (uint32(fx.Progress) << 4)
			if p2 != wantPacked || int(p2) != fx.Packed {
				t.Errorf("event packed = %d (result %d, progress %d), want %d (fixture)", p2, p2&0xf, p2>>4, fx.Packed)
			}
			if got := se.endStateName(); got != fx.WiState {
				t.Errorf("end state = %s, want %s — a stream COMPLETED stays IDLE, only DROPPED is DONE (not future's rule)", got, fx.WiState)
			}
		})
	}
}
