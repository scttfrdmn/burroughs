package component

import (
	"io"
	"os"
	"testing"
)

// TestASyncTaskDoesNotSeeAWaitingAsyncTasksContext is #1013 with `gate:cm-threads` off, which was the default
// path when #1013 was found and has not been since the 2026-10-17 flip.
// The parent's async `run` sets its context to 5 and waits on a child's `g`, and `g` calls back into the
// parent's sync `peek`, which returns `context.get(0)`. In the pin, context is per Thread, and `peek`'s is a
// new Thread's, so it reads 0. wasmtime 49.0.2 reads 0, 10 of 10.
//
// Burroughs read **5**, deterministically. `context.get` read `asyncHandles.lift`, which still named the
// waiting `run` while `peek` ran. So the defect is in default-on behaviour (`gate:async`, on since
// 2026-09-18).
//
// **Neuter, run:** `callerStorage` returning `h.lift` (the pre-#1013 read), which gives 5.
func TestASyncTaskDoesNotSeeAWaitingAsyncTasksContext(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	t.Setenv(cmThreadsGateEnv, "0")
	b, err := os.ReadFile("testdata/context-clobber.wasm")
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
	if v, _ := res[0].U32(); v != 0 {
		t.Fatalf("run returned %d; both oracles read 0 (peek's own context), not run's 5", v)
	}
}

// TestACreatedThreadHasItsOwnContext is #1013's per-Thread half, behind `gate:cm-threads`. `run` sets its
// context to 5 and creates Thread T, which reads `context.get(0)`. In the pin, a new Thread's storage
// starts at [0,0], so T reads 0. wasmtime 49.0.2 reads 0, 10 of 10. Burroughs shared the task's storage
// with its Threads and read 5.
//
// **Neuter, run:** `callerStorage` giving a created Thread its task's storage, which gives 5.
func TestACreatedThreadHasItsOwnContext(t *testing.T) {
	_, res, err := callBounded(t, "thread-context", "run")
	wantU32(t, "thread-context", res, err, 0)
}
