package component

import (
	"testing"
)

// TestASyncWaitWithNoCandidateTrapsAtTheSyncTail is #1005's witness: a sync lift's `waitable-set.wait`
// that no Thread of its own instance can satisfy traps at the reference's sync tail,
// `trap_if(not candidates)`. Matched by trap SITE: the reason names the rule.
//
// Two fixtures, each read by both oracles, 10 of 10 on wasmtime 49.0.2:
//   - **`nested-sync-unwind-control`**: the wait's event can only come from a THIRD instance. wasmtime
//     reads *"cannot block a synchronous task before returning"*, and the reference traps at
//     `canon_lift:2161`. Burroughs returned 5 before #1005.
//   - **`sync-wait-own-thread-control`**: the wait is on the instance's own future, and no Thread writes
//     it. Both oracles trap the same way. Burroughs hung before #1005.
//
// **The admitted twin, `sync-wait-own-thread`** (both oracles: 7), cannot run on Burroughs yet: the
// intra-instance future copy its Thread performs is not built (#1006). The admit branch of the rule is
// witnessed by `TestASyncTailCountsAWaiterWhoseEventIsPending` instead.
//
// **Neuter, run:** making `syncTail` return at once (the candidate check removed). The first fixture then
// returns 5, and the second hangs, failing on its bound.
func TestASyncWaitWithNoCandidateTrapsAtTheSyncTail(t *testing.T) {
	for _, f := range []string{"nested-sync-unwind-control", "sync-wait-own-thread-control"} {
		_, _, err := callBounded(t, f, "run")
		wantTrapContaining(t, f, err, "trap_if(not candidates)")
	}
}

// TestASyncTailCountsAWaiterWhoseEventIsPending is the rule's admit branch, as a unit: a sync waiter whose
// own `ready_func` holds is a candidate, so `syncTail` aborts nothing. With the predicate false, the same
// waiter is aborted with the sync-tail trap.
//
// **Neuter, run:** making `syncTail` ignore `waitReady`, which fails the first arm.
func TestASyncTailCountsAWaiterWhoseEventIsPending(t *testing.T) {
	t.Setenv(cmThreadsGateEnv, "1")
	for _, ready := range []bool{true, false} {
		h := newAsyncHandles()
		tt := newThreadTable()
		tt.h = h
		w := &cmThread{implicit: true, state: threadWaiting, waitAbort: make(chan struct{}), waitJudged: true}
		w.owner = w
		w.waitReady = func() bool { return ready }
		tt.add(w)
		aborted := tt.syncTail(h)
		if ready && len(aborted) != 0 {
			t.Fatalf("a sync waiter whose event is pending was aborted; it is a candidate in the reference")
		}
		if !ready {
			if len(aborted) != 1 || aborted[0] != w {
				t.Fatalf("a sync waiter with no candidate was not aborted (aborted %d)", len(aborted))
			}
			if w.abortErr == nil {
				t.Fatal("the aborted waiter carries no trap")
			}
		}
	}
}
