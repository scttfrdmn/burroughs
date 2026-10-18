// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"os"
	"testing"
)

// TestAnAsyncLowerOfASyncLiftStoresItsResult is #1050's witness. An async lower of a sibling whose `op` is
// lifted SYNCHRONOUSLY must return RETURNED with the callee's result stored at the retptr, as the reference's
// `canon_lift` does by running a sync callee to completion inside the lower.
//
// `sync-callee-async-lower.wasm`'s `run()` returns the lower's status*100 + the stored result. wasmtime 49.0.3
// reads 242, 10 of 10 (`sync-callee-async-lower.reading`). Burroughs read 200 before the fix: RETURNED with
// nothing stored, because the callback loop read the sync callee's return value, 42, as a packed code.
//
// **Run several times**, because the defect's answer depended on nothing nondeterministic, but the fix moves the
// callee onto the lower's own goroutine and that should be shown to hold every time, not once.
//
// **Neuter, run:** dropping the `!callee.async` arm in `bindCrossComponent` reads 200, and the test fails on its
// first iteration.
func TestAnAsyncLowerOfASyncLiftStoresItsResult(t *testing.T) {
	t.Setenv(asyncGateEnv, "") // the default, which is on
	b, err := os.ReadFile("testdata/sync-callee-async-lower.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	for i := range 20 {
		in, err := Instantiate(b)
		if err != nil {
			t.Fatalf("instantiate: %v", err)
		}
		res, err := in.CallValues("run")
		in.Close()
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		if v, ok := res[0].U32(); len(res) != 1 || !ok || v != 242 {
			t.Fatalf("run %d: run() = %v, want u32 242 (RETURNED with 42 stored). 200 is #1050: the sync "+
				"callee's return value was read as a callback code and no result was stored", i, res)
		}
	}
}
