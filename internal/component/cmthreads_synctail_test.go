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
//     `canon_lift:2123`. Burroughs returned 5 before #1005.
//   - **`sync-wait-own-thread-control`**: the wait is on the instance's own future, and no Thread writes
//     it. Both oracles trap the same way. Burroughs hung before #1005.
//
// **The admitted twin, `sync-wait-own-thread`** (both oracles: 7), could not run on Burroughs until the
// intra-instance future copy its Thread performs was built (#1006). It is now witnessed by
// `TestASyncWaitSatisfiedByItsOwnThreadIsAdmitted`. The unit `TestASyncTailCountsAWaiterWhoseEventIsPending`
// stands beside it.
//
// **Neuter, run:** making `syncTail` return at once (the candidate check removed). The first fixture then
// returns 5, and the second hangs, failing on its bound.
func TestASyncWaitWithNoCandidateTrapsAtTheSyncTail(t *testing.T) {
	for _, f := range []string{"nested-sync-unwind-control", "sync-wait-own-thread-control"} {
		_, _, err := callBounded(t, f, "run")
		wantTrapContaining(t, f, err, "trap_if(not candidates)")
	}
}

// oneEvent is a waitable holding one deliverable event until it is taken.
type oneEvent struct{ pending bool }

func (e *oneEvent) pendingEventLocked() (event, bool) {
	if !e.pending {
		return event{}, false
	}
	e.pending = false
	return event{code: 1}, true
}
func (e *oneEvent) hasPendingEventLocked() bool { return e.pending }
func (e *oneEvent) joinTo(*waitableSet)         {}
func (e *oneEvent) currentSet() *waitableSet    { return nil }

// TestASyncTailCountsAWaiterThatHasTakenItsEvent is #1031's witness, as a unit: a sync waiter that has taken
// its event while still marked waiting is a candidate, so `syncTail` aborts nothing.
//
// That state is real. `waitable-set.wait` takes the event under asyncHandles.mu, and the waiter becomes
// `threadRunning` only afterwards, under the thread table's lock. A chain ending between the two judged the
// waiter with the set's readiness alone, which the taking had made false, and killed it: 17 in 2,000 on
// `sync-wait-own-thread` under `-race -cpu 2`, where both oracles return 7. The state is built here
// directly, so the arm does not depend on scheduling.
//
// The second arm is the control: a wait that has taken nothing, on a set with nothing pending, is still
// aborted. Without it, the first arm would pass against a readiness that is always true.
//
// **Neuter, run:** `waitTake.readyLocked` returning the set's readiness alone fails the first arm.
func TestASyncTailCountsAWaiterThatHasTakenItsEvent(t *testing.T) {
	t.Setenv(cmThreadsGateEnv, "1")
	for _, take := range []bool{true, false} {
		h := newAsyncHandles()
		tt := newThreadTable()
		tt.h = h
		member := &oneEvent{pending: take}
		wt := &waitTake{set: &waitableSet{members: []waitable{member}, wake: make(chan struct{})}}
		if take {
			h.mu.Lock()
			if _, ok := wt.takeLocked(); !ok {
				h.mu.Unlock()
				t.Fatal("the fixture's member had no event to take")
			}
			h.mu.Unlock()
		}
		w := &cmThread{implicit: true, state: threadWaiting, waitAbort: make(chan struct{}), waitJudged: true}
		w.owner = w
		w.waitReady = wt.readyLocked
		tt.add(w)
		aborted := tt.syncTail(h)
		if take && len(aborted) != 0 {
			t.Fatalf("a sync waiter that has taken its event was aborted at the sync tail; it was satisfied, " +
				"and in the reference it would already be running")
		}
		if !take && (len(aborted) != 1 || aborted[0] != w) {
			t.Fatalf("a sync waiter with nothing taken and nothing pending was not aborted (aborted %d)", len(aborted))
		}
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
