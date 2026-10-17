package component

import (
	"os"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
)

// TestAFutureWriteCompletesAPendingReadInItsOwnInstance is #1006 on the DEFAULT path: one async callback task
// creates a future, reads it (BLOCKED), writes 7 into it itself, and WAITs for the read's event.
//
// **The readings were taken before the fix** (`testdata/future-self-copy.reading`): wasmtime 49.0.2 (10 of
// 10), the previous pin (2bed77e) and the pin (2e7895c) all read 262151 = 0x40007. That is event FUTURE_READ (4), write status
// COMPLETED (0) and byte 7. Burroughs did not return: `future.write` only parked, so the read stayed pending
// forever. Run in both gate states, since no Thread is involved.
//
// **Neuter, run:** dropping the inline copy (`futureWrite` always parks) makes `run` park, and this fails on
// its bound.
func TestAFutureWriteCompletesAPendingReadInItsOwnInstance(t *testing.T) {
	for _, gate := range []string{"0", "1"} {
		t.Run("gate="+gate, func(t *testing.T) {
			t.Setenv(asyncGateEnv, "1")
			t.Setenv(cmThreadsGateEnv, gate)
			b, err := os.ReadFile("testdata/future-self-copy.wasm")
			if err != nil {
				t.Fatalf("the committed fixture is missing: %v", err)
			}
			in, err := Instantiate(b)
			if err != nil {
				t.Fatalf("instantiate: %v", err)
			}
			t.Cleanup(in.Close)
			type out struct {
				res []canon.Value
				err error
			}
			ch := make(chan out, 1)
			go func() { r, e := in.CallValues("run"); ch <- out{r, e} }()
			var o out
			select {
			case o = <-ch:
			case <-time.After(10 * time.Second):
				t.Fatal("run() did not return within 10s: the write did not complete the pending read, which all " +
					"three oracles read as 262151")
			}
			if o.err != nil {
				t.Fatalf("run(): %v", o.err)
			}
			if v, ok := o.res[0].U32(); !ok || v != 262151 {
				t.Fatalf("run() = %v, want 262151 (0x40007: FUTURE_READ, write COMPLETED, byte 7)", o.res)
			}
		})
	}
}

// TestAFutureCopyWithinAnInstanceFollowsThePinsElementRule is #1006's element types (the chair's item 3),
// read on both oracles first (`future-self-copy-*.wasm`; the value is event code << 16 + the read buffer's
// word):
//
//	fixture   wasmtime 49.0.2                         pin 2e7895c                   asserted
//	bool      trap, non-numeric payload               trap at the copy              trap
//	char      trap, non-numeric payload               trap at the copy              trap
//	f32one    1069809664 (1.5)                        1069809664                    1069809664
//	f32       2143551489 (NaN payload preserved)      {2143551488, any NaN}          2143551488 (canonical)
//	f64       262145 (NaN payload preserved)          {262144, any NaN}              262144 (canonical)
//
// The floats take the pin's deterministic-profile outcome, the codec's own NaN rule.
//
// **Neuters, run:**
//   - without the non-numeric trap, `bool` copies and reads its byte;
//   - without the NaN canonicalization, `f32` reads 2143551489.
func TestAFutureCopyWithinAnInstanceFollowsThePinsElementRule(t *testing.T) {
	for _, gate := range []string{"0", "1"} {
		for _, tc := range []struct {
			fixture string
			want    uint32 // 0: the trap
		}{
			{"bool", 0}, {"char", 0}, {"f32one", 1069809664}, {"f32", 2143551488}, {"f64", 262144},
		} {
			t.Run(tc.fixture+"/gate="+gate, func(t *testing.T) {
				t.Setenv(asyncGateEnv, "1")
				t.Setenv(cmThreadsGateEnv, gate)
				b, err := os.ReadFile("testdata/future-self-copy-" + tc.fixture + ".wasm")
				if err != nil {
					t.Fatalf("the committed fixture is missing: %v", err)
				}
				in, err := Instantiate(b)
				if err != nil {
					t.Fatalf("instantiate: %v", err)
				}
				t.Cleanup(in.Close)
				type out struct {
					res []canon.Value
					err error
				}
				ch := make(chan out, 1)
				go func() { r, e := in.CallValues("run"); ch <- out{r, e} }()
				var o out
				select {
				case o = <-ch:
				case <-time.After(10 * time.Second):
					t.Fatal("run() did not return within 10s")
				}
				if tc.want == 0 {
					wantTrapContaining(t, tc.fixture, o.err, "non-numeric payload")
					return
				}
				if o.err != nil {
					t.Fatalf("run(): %v", o.err)
				}
				if v, ok := o.res[0].U32(); !ok || v != tc.want {
					t.Fatalf("run() = %v, want %d", o.res, tc.want)
				}
			})
		}
	}
}

// TestASyncWaitSatisfiedByItsOwnThreadIsAdmitted is #1005's admit branch, witnessed by its own fixture at
// last. A sync lift waits on a future that a Thread of its own instance writes, so the sync tail has a
// candidate.
//
// Both oracles read 7 (`refread.reading`, `sync-wait-own-thread`; wasmtime 49.0.2, 10 of 10). Until #1006's
// fix, the Thread's write parked and the fixture could not run, so the admit branch was witnessed only by
// `TestASyncTailCountsAWaiterWhoseEventIsPending`, as a unit.
//
// **Neuter, run:** dropping the inline copy makes this trap at the sync tail. The Thread's write parks, so
// once the Thread has run, no candidate is left.
func TestASyncWaitSatisfiedByItsOwnThreadIsAdmitted(t *testing.T) {
	_, res, err := callBounded(t, "sync-wait-own-thread", "run")
	if err != nil {
		t.Fatalf("run(): %v; both oracles return 7", err)
	}
	if v, ok := res[0].U32(); !ok || v != 7 {
		t.Fatalf("run() = %v, want 7", res)
	}
}
