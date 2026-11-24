// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"encoding/binary"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// TestAFutureReadCompletesAParkedWriteInItsOwnInstance is #1325 item 1: one async callback task meets itself at a
// future, in either order, and the second operation completes the rendezvous. Each u8 fixture returns the tuple
// (write status, read status, byte read, event code, event index, event payload, wi, second poll's code), and each
// row was read on both oracles first (`testdata/future-rendezvous.reading`, `refread.reading`):
//
//	fixture                         wasmtime 49.0.3 (5 of 5)                 pin 2e7895c (16 of 16)
//	write-then-read                 (4294967295, 0, 7, 5, 2, 0, 2, 0)        the same
//	write-then-read-cancel          (4294967295, 0, 7, 51966, 0, 0, 2, 0)    the same
//	write-then-read-bool            trap at the read, non-numeric payload    trap in future.read
//	read-then-write (the control)   (0, 4294967295, 7, 4, 1, 0, 2, 0)        the same
//
// So, writing first: the write parks BLOCKED, the read completes inline with COMPLETED and the byte, and the
// write's event is (FUTURE_WRITE, wi, COMPLETED), the bare result with no progress packed into it. The poll after
// the event reads NONE with both ends joined, so neither end has a second event. A cancel after the read takes
// the COMPLETED, and leaves nothing behind. Burroughs before the fix read write-then-read as
// (4294967295, 4294967295, 0, 0xdead, ...): the read parked too.
//
// **The sync write is not a row**, because both oracles hang on it: a sync `future.write` with no reader waits for
// an event only a later operation of the same task could cause (def:copy:"end.wait_for_pending_event()"). Burroughs
// binds the sync form as the async one, so it does not hang, and that is a separate divergence (the reading file).
//
// **Neuters, run:**
//   - futureRead's #1325 arm dropped: write-then-read and write-then-read-cancel read field 3 as 0xdead, the place
//     the fixture reports a read that parked, and write-then-read-bool returns (4294967295, 0xdead), no trap;
//   - the writable future end's event packing `progress << 4` again: write-then-read reads the payload as 16, and
//     write-then-read-cancel reads the cancel's return as 16;
//   - futureCancelWrite trapping on `e.resolved` again: write-then-read-cancel traps "no write in flight".
func TestAFutureReadCompletesAParkedWriteInItsOwnInstance(t *testing.T) {
	const blocked = 0xffffffff
	for _, gate := range []string{"0", "1"} {
		for _, tc := range []struct {
			fixture string
			want    []uint32 // nil: the trap
		}{
			{"future-write-then-read", []uint32{blocked, 0, 7, 5, 2, 0, 2, 0}},
			{"future-write-then-read-cancel", []uint32{blocked, 0, 7, 0xcafe, 0, 0, 2, 0}},
			{"future-write-then-read-bool", nil},
			{"future-read-then-write", []uint32{0, blocked, 7, 4, 1, 0, 2, 0}},
		} {
			t.Run(tc.fixture+"/gate="+gate, func(t *testing.T) {
				t.Setenv(asyncGateEnv, "1")
				t.Setenv(cmThreadsGateEnv, gate)
				res, err := callFutureFixtureBounded(t, tc.fixture)
				if tc.want == nil {
					wantTrapContaining(t, tc.fixture, err, "future.read: cannot read from and write to an intra-instance "+
						"future with a non-numeric payload")
					return
				}
				if err != nil {
					t.Fatalf("run(): %v", err)
				}
				fields, ok := res[0].Record()
				if !ok || len(fields) != len(tc.want) {
					t.Fatalf("run() = %v, want a tuple of %d u32", res, len(tc.want))
				}
				got := make([]uint32, len(fields))
				for i, f := range fields {
					got[i], _ = f.U32()
				}
				if got[3] == 0xdead {
					t.Fatalf("run() = %v: the second operation did not complete the rendezvous, so both ends are "+
						"parked (the fixture's 0xdead place); both oracles read %v", got, tc.want)
				}
				for i := range got {
					if got[i] != tc.want[i] {
						t.Fatalf("run() = %v, want %v (field %d differs)", got, tc.want, i)
					}
				}
			})
		}
	}
}

// callFutureFixtureBounded instantiates testdata/<fixture>.wasm under the gates the caller set, calls `run`, and
// fails the test, with every goroutine's stack, if the call does not return within the bound.
func callFutureFixtureBounded(t *testing.T, fixture string) ([]canon.Value, error) {
	t.Helper()
	b, err := os.ReadFile("testdata/" + fixture + ".wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	in, err := Instantiate(b)
	if err != nil {
		t.Fatalf("instantiate %s: %v", fixture, err)
	}
	t.Cleanup(in.Close)
	type out struct {
		res []canon.Value
		err error
	}
	ch := make(chan out, 1)
	go func() { r, e := in.CallValues("run"); ch <- out{r, e} }()
	select {
	case o := <-ch:
		return o.res, o.err
	case <-time.After(10 * time.Second):
		buf := make([]byte, 1<<22)
		buf = buf[:runtime.Stack(buf, true)]
		t.Logf("%s: every goroutine at the bound:\n%s", fixture, buf)
		t.Fatalf("%s: run() did not return within 10s", fixture)
		return nil, nil
	}
}

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
			fe := &readableFutureEnd{elem: u32Elem} // the oracle's future<u32>, typed as an async lower mints it
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
			readRet, err := futureRead(h, u32Elem, true)(cc, []interp.Value{interp.I32(int32(fe.index)), interp.I32(ptr)})
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
