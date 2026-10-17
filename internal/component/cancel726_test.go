package component

import (
	"os"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
)

// TestASyncCancelFollowsUpstream726InBothGateStates is the sync `subtask.cancel` row, decided on #999 under
// the chair's ruling of 2026-10-17: the result does not depend on `gate:cm-threads`, and it is upstream
// #726's.
//
// # The fixtures, and the four readings each was taken against
//
// `testdata/cancel726/`: a parent async-lowers a child's callback-lifted `run`, which WAITs on an empty set,
// then cancels it. The child answers TASK_CANCELLED with `task.cancel` and EXIT, or, in `-late`, with YIELD,
// resolving only on its next event. The value is lower-state × 16 + cancel status.
//
//	fixture             old pin 2bed77e   pin 2e7895c   wasmtime 49.0.2   Burroughs before (gate off / on)
//	cancel-sync         trap              20            20                admits / trap
//	cancel-async        20                20            20                20
//	cancel-sync-late    trap              trap          trap              admits / trap
//	cancel-async-late   20                20            20                20
//
// 2e7895c has been the pin since 2026-10-17; it was "head" when this was written. It is upstream #726
// (2f1e56f56), which the old pin predates: a cancellable, ready implicit callback Thread is
// resumed synchronously, and *"control flow deterministically transfers back as soon as the `callback`
// exits or blocks"*. wasmtime 49.0.2's `subtask_cancel` yields to it the same way. Head's late arm traps at
// `canon_lift:2123` in all 8 deterministic seeds (`refread.reading`, the pin since 2026-10-17; the previous
// pin's reading is `refread-2bed77e.reading`).
//
// **Neuters, run:**
//   - no hand-back (`armTransferLocked` returns nil): `cancel-sync` traps in both gate states;
//   - no ack (`handBack` does not wait): `cancel-sync-late` races its judgement and reads 20 in some runs;
//   - the cancel wait unjudged: `cancel-sync-late` reads 20.
func TestASyncCancelFollowsUpstream726InBothGateStates(t *testing.T) {
	for _, gate := range []string{"0", "1"} {
		for _, tc := range []struct {
			fixture string
			want    uint32 // 0: the sync-tail trap
		}{
			{"cancel-sync", 20},
			{"cancel-async", 20},
			{"cancel-sync-late", 0},
			{"cancel-async-late", 20},
		} {
			t.Run(tc.fixture+"/gate="+gate, func(t *testing.T) {
				t.Setenv(asyncGateEnv, "1")
				t.Setenv(cmThreadsGateEnv, gate)
				b, err := os.ReadFile("testdata/cancel726/" + tc.fixture + ".wasm")
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
				go func() { r, e := in.CallValues("go"); ch <- out{r, e} }()
				var o out
				select {
				case o = <-ch:
				case <-time.After(10 * time.Second):
					t.Fatalf("go() did not return within 10s")
				}
				if tc.want == 0 {
					wantTrapContaining(t, tc.fixture, o.err, "trap_if(not candidates)")
					return
				}
				if o.err != nil {
					t.Fatalf("go() = %v, want %d (STARTED, then CANCELLED_BEFORE_RETURNED) — the pin and wasmtime "+
						"both read %d", o.err, tc.want, tc.want)
				}
				if v, ok := o.res[0].U32(); !ok || v != tc.want {
					t.Fatalf("go() = %v, want %d", o.res, tc.want)
				}
			})
		}
	}
}
