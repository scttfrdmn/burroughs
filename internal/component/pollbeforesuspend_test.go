package component

import (
	"os"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
)

// TestACallerPollingWithoutSuspendingSeesNoEventFromItsCallee is the shape outside #1020's class, kept as a
// guard (the chair's ruling of 2026-10-17, item 2).
//
// `poll-before-suspend.wasm`: a parent's async callback `run` async-lowers a provider's `op`, which YIELDs
// once and then returns. It joins the subtask to a set and polls it 200,000 times without suspending.
//
// The readings (`testdata/poll-before-suspend.reading`):
//   - wasmtime 49.0.2 reads 1, 10 of 10;
//   - the reference reads {1, 9002} on the pin (2e7895c) and on the previous pin (2bed77e);
//   - 101, a poll seeing the event while STARTED, is the outcome the reference forbids.
//
// Burroughs reads 1, because a callee's result store needs the caller's run slot (#983) and the caller holds
// it throughout. So the caller cannot observe its callee early here. #1020's class is reached through the
// two mechanisms ADR 0095's amendment names: a store inside the caller's `inHost` window, and a cancel judged
// against the callee's live state.
//
// **Neuter, run:** applying a result store at once instead of under the slot (`onResolve` skipping
// `acquireRun`) lets the poll see the event, and this fails on 101.
func TestACallerPollingWithoutSuspendingSeesNoEventFromItsCallee(t *testing.T) {
	for _, gate := range []string{"0", "1"} {
		t.Run("gate="+gate, func(t *testing.T) {
			t.Setenv(asyncGateEnv, "1")
			t.Setenv(cmThreadsGateEnv, gate)
			b, err := os.ReadFile("testdata/poll-before-suspend.wasm")
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
			case <-time.After(20 * time.Second):
				t.Fatal("run() did not return within 20s")
			}
			if o.err != nil {
				t.Fatalf("run(): %v", o.err)
			}
			v, ok := o.res[0].U32()
			if !ok || (v != 1 && v != 9002) {
				t.Fatalf("run() = %v; the reference's set is {1, 9002}. 101 is a poll seeing the callee's event "+
					"while the caller had not suspended", o.res)
			}
		})
	}
}
