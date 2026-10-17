package component

import (
	"io"
	"os"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// TestASyncWaitOnAnotherInstancesEventTraps is ADR 0102's sync-wait row with `gate:cm-threads` OFF: a sync
// lift's `waitable-set.wait` on an event that only another instance (here, the host's `op`) can produce traps
// at the reference's sync tail. Before this, Burroughs admitted it on the default path (#1016), and #1005's
// trap applied only with the gate on.
//
// # The oracle readings, taken before the fix
//
// Each fixture was nested in a provider component whose async `op` YIELDs once and then returns `x`, which is
// what lets wasmtime supply `test:async/ops`:
//   - **wasmtime 49.0.2**, 10 of 10 on each of `async-waitset-synth`, `run-slot` and `result-store-heap`:
//     *"cannot block a synchronous task before returning"*. The provider resolving eagerly instead, so the
//     wait is never reached, reads 7. That is this test's control arm.
//   - **The reference** (`definitions.py` @ CANON_PIN, the fixtures' shared prefix modelled as a scenario):
//     every run that reaches the wait traps at `canon_lift:2161`. That is all 8 seeds under
//     DETERMINISTIC_PROFILE, and 5 of 8 without it. The other 3 never reach the wait, because the early
//     return of `wait_until` lets `op` RETURN inside the lower.
//
// The witnesses that needed two of these callers parked at once moved to callback-lift twins
// (`run-slot-cb`, `result-store-heap-cb`), which both oracles admit.
//
// **Neuter, run:** making `syncTail` judge a waiter only with the gate on (`waitJudged && cmThreadsEnabled()`). Each
// fixture then parks and never returns, and the trap arm fails on its bound.
func TestASyncWaitOnAnotherInstancesEventTraps(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	t.Setenv(cmThreadsGateEnv, "0")
	for _, f := range []string{"async-waitset-synth", "run-slot", "result-store-heap"} {
		for _, inline := range []bool{false, true} {
			b, err := os.ReadFile("testdata/" + f + ".wasm")
			if err != nil {
				t.Fatalf("%s: the committed fixture is missing: %v", f, err)
			}
			h := NewHost(io.Discard, io.Discard, nil)
			h.asyncImpls = map[string]asyncLowerImpl{
				"test:async/ops::op": func(_ *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
					onStart()
					if inline {
						onResolve(canon.U32(7)) // RETURNED inside the lower: the wait is never reached
					}
					return func() {}, nil
				},
			}
			in, err := InstantiateWithHost(b, h)
			if err != nil {
				t.Fatalf("%s: instantiate: %v", f, err)
			}
			type out struct {
				res []canon.Value
				err error
			}
			done := make(chan out, 1)
			go func() { r, e := in.CallValues("run"); done <- out{r, e} }()
			var o out
			select {
			case o = <-done:
			case <-time.After(10 * time.Second):
				in.Close()
				t.Fatalf("%s (inline %v): run() neither returned nor trapped within 10s; it is parked on an "+
					"event only the host can produce, which both oracles trap at the sync tail", f, inline)
			}
			in.Close()
			if inline {
				if o.err != nil {
					t.Errorf("%s control: run() = %v; with op RETURNED inline the wait is never reached, and "+
						"both oracles return 7", f, o.err)
				} else if v, ok := o.res[0].U32(); !ok || v != 7 {
					t.Errorf("%s control: run() = %v, want 7", f, o.res)
				}
				continue
			}
			wantTrapContaining(t, f, o.err, "trap_if(not candidates)")
		}
	}
}
