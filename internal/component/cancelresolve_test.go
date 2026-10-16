package component

import (
	"io"
	"os"
	"testing"
)

// TestACalleeThatIgnoresACancelResolvesReturnedWithItsValue is #1009's witness. A subtask whose cancellation
// was requested, and whose callee then returns a value instead of calling `task.cancel`, is RETURNED with
// the value. The reference's `on_resolve` takes the cancel arm only for `result is None`.
//
// `testdata/ignore-cancel.wasm`: the parent returns `state*16 + value`. wasmtime 49.0.2 reads **41**
// (RETURNED, 9), 10 of 10. Burroughs read 64 (CANCELLED_BEFORE_RETURNED, no value) before #1009.
//
// **Neuter, run:** choosing the cancel arm by the request alone (`st.cancellationRequested`, the pre-#1009
// condition), which returns 64.
func TestACalleeThatIgnoresACancelResolvesReturnedWithItsValue(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	b, err := os.ReadFile("testdata/ignore-cancel.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	in, err := InstantiateWithHost(b, NewHost(io.Discard, io.Discard, nil))
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	defer in.Close()
	res, err := in.CallValues("run")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v, _ := res[0].U32(); v != 41 {
		t.Fatalf("run returned %d (state %d, value %d); wasmtime reads 41: RETURNED (2) with 9", v, v>>4, v&15)
	}
}
