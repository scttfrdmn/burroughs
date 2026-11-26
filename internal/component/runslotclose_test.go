// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
)

// runSlotCycleRuns is how many fresh instances each witness tries for the cycle. E2-in-a's neuter deadlocked on its
// first run, so the count only covers a schedule in which the child's callback runs after the parent's poll.
const runSlotCycleRuns = 20

// runSlotCycleGuard bounds the wait for the cycle's place, the call's return after its end, and the engine goroutines'
// end. A harness hang guard, not a measurement, scaled under `-race`.
const runSlotCycleGuard = 30 * time.Second * raceGuardScale

// goroutineIDs is the set of goroutines in stacks, by the number on each one's header line.
func goroutineIDs(stacks string) map[string]bool {
	ids := map[string]bool{}
	for _, g := range strings.Split(stacks, "\n\n") {
		if id, ok := goroutineID(g); ok {
			ids[id] = true
		}
	}
	return ids
}

// goroutineID is g's number, from its header line "goroutine N [status]:".
func goroutineID(g string) (string, bool) {
	head, _, _ := strings.Cut(g, "\n")
	rest, ok := strings.CutPrefix(head, "goroutine ")
	if !ok {
		return "", false
	}
	id, _, ok := strings.Cut(rest, " ")
	return id, ok
}

// cycleWaiters reports whether the two halves of #1398's cycle are each parked waiting for a run slot, in the take
// both acquire forms share (takeRunSlot): the child's `task.return`, whose on-return hook stores the parent's result
// (taskReturn → onResolve → acquireRun), and the parent's sync call of the child's `exited` (invokeSync →
// acquireRunFor). Frames are matched by bare name, since the compiler names closures and inlines as it chooses.
//
// **Only goroutines not in `before` are read**, the set taken before this run's instance was made. The first draft
// read every goroutine, and under the neuter the gate-off subtest's cycle, left parked by its failure, was read as
// the gate-on run's: that subtest closed instances that had not reached the cycle yet, and passed 20 of 20.
func cycleWaiters(stacks string, before map[string]bool) (store, call bool) {
	for _, g := range strings.Split(stacks, "\n\n") {
		if id, ok := goroutineID(g); !ok || before[id] {
			continue
		}
		head, _, _ := strings.Cut(g, "\n")
		if !strings.Contains(head, "[select") || !strings.Contains(g, "takeRunSlot") {
			continue
		}
		switch {
		case strings.Contains(g, "taskReturn"):
			store = true
		case strings.Contains(g, "invokeSync"):
			call = true
		}
	}
	return store, call
}

// cyclePlace names where the cycle's goroutines are, for a failure that must name its place.
func cyclePlace(stacks string, before map[string]bool) string {
	store, call := cycleWaiters(stacks, before)
	return fmt.Sprintf("the store's waiter (taskReturn in acquireRun) parked: %v; the call's waiter (invokeSync in "+
		"acquireRunFor) parked: %v; slot waiters: %s; %s", store, call,
		strings.Join(slotWaiters(stacks), ", "), agentsPlace(stacks))
}

// runSlotCycle is one run of task-return-early's cancel-poll row on a fresh instance, driven into #1398's cycle.
type runSlotCycle struct {
	in       *Instantiated
	done     chan error      // the call's error, once it returns
	before   map[string]bool // the goroutines that existed before the instance was made
	baseline int             // the engine goroutines then
}

// startRunSlotCycle makes a fresh instance of b and calls `go(6)` on it until both halves of the cycle are parked,
// and reports whether they were. A false is a schedule that missed the cycle: the call returned, and the instance is
// already closed.
func startRunSlotCycle(t *testing.T, b []byte) (*runSlotCycle, bool) {
	t.Helper()
	r := &runSlotCycle{done: make(chan error, 1), baseline: len(engineGoroutines())}
	r.before = goroutineIDs(goroutineStacks()) // the cycle read is this run's, never a leftover's
	in, err := Instantiate(b)
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	r.in = in
	go func() {
		_, cerr := in.CallValues("go", canon.U32(6))
		r.done <- cerr
	}()
	for deadline := time.Now().Add(runSlotCycleGuard); ; {
		select {
		case <-r.done:
			in.Close()
			return r, false
		default:
		}
		if store, call := cycleWaiters(goroutineStacks(), r.before); store && call {
			return r, true
		}
		if time.Now().After(deadline) {
			t.Fatalf("neither the cycle nor the call's return within %v: %s", runSlotCycleGuard,
				cyclePlace(goroutineStacks(), r.before))
		}
		runtime.Gosched()
	}
}

// finish requires that the call returns and that the engine goroutines fall back to the count before the instance,
// after whatever ended the cycle. It returns the call's error.
func (r *runSlotCycle) finish(t *testing.T, ended string) error {
	t.Helper()
	var callErr error
	select {
	case callErr = <-r.done:
	case <-time.After(runSlotCycleGuard):
		t.Fatalf("%s, and the call did not return: %s", ended, cyclePlace(goroutineStacks(), r.before))
	}
	r.in.Close()
	deadline := time.Now().Add(runSlotCycleGuard) // a harness hang guard for a goroutine between its last act and its end
	for len(engineGoroutines()) > r.baseline && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if left := engineGoroutines(); len(left) > r.baseline {
		t.Fatalf("%s: %d engine goroutine(s) outlived Close, over the %d before the instance:\n%s", ended, len(left),
			r.baseline, strings.Join(left, "\n\n"))
	}
	return callErr
}

// eachRunSlotCycle runs body on runSlotCycleRuns fresh cycles in each gate state, with the seam set, and fails a gate
// state in which no run reached the cycle, since that run witnessed nothing.
func eachRunSlotCycle(t *testing.T, body func(t *testing.T, run int, r *runSlotCycle)) {
	t.Helper()
	answerInsideHandBack = true
	defer func() { answerInsideHandBack = false }()
	b, err := os.ReadFile("testdata/task-return-early.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	for _, gate := range []string{"0", "1"} {
		t.Run("cm-threads="+gate, func(t *testing.T) {
			t.Setenv(asyncGateEnv, "1")
			t.Setenv(cmThreadsGateEnv, gate)
			reached := 0
			for run := range runSlotCycleRuns {
				r, parked := startRunSlotCycle(t, b)
				if !parked {
					continue
				}
				reached++
				body(t, run, r)
			}
			t.Logf("%d of %d runs reached the cycle", reached, runSlotCycleRuns)
			if reached == 0 {
				t.Fatalf("no run of %d reached the cycle, so this run witnessed nothing: the seam no longer drives it",
					runSlotCycleRuns)
			}
		})
	}
}

// TestCloseEndsACrossInstanceRunSlotDeadlock is ADR 0139's witness (#1398): Close ends a deadlock between two
// instances' run slots, and no engine goroutine outlives it.
//
// The cycle is the one E2-in-a's neuter found (ADR 0136), driven through its seam, `answerInsideHandBack`, which
// makes the on-return hook answer inside upstream #726's hand-back window. task-return-early's cancel-poll row
// (row 6) then parks the child's `task.return` in `acquireRun`, waiting for the parent's slot while it holds its
// own, and the parent's sync call of the child's `exited` in `acquireRunFor`, waiting for the child's while it
// holds the parent's. **The witness asserts places, not durations**: it waits until both waiters are parked there,
// then Closes, and requires that Close returns within closeEndsBound, half `interp.closeQuiesceInterval`, so a Close
// that only outwaits its core instances fails it; that the call returns; and that the engine goroutines fall back
// to the count before the instance was made. Close's stop ends the call's half, whose unwind frees the parent's slot,
// and the child's store then lands.
//
// NEUTER, run: takeRunSlot's two selects without the stop and the engine arms. In both gate states the first run
// reaches the cycle, and Close does not return within closeEndsBound, with both waiters still parked. With only the
// stop's arms removed, the same: the engine's arm alone does not end it within the bound.
func TestCloseEndsACrossInstanceRunSlotDeadlock(t *testing.T) {
	eachRunSlotCycle(t, func(t *testing.T, run int, r *runSlotCycle) {
		t.Helper()
		closed := make(chan struct{})
		go func() {
			r.in.Close()
			close(closed)
		}()
		select {
		case <-closed:
		case <-time.After(closeEndsBound):
			t.Fatalf("run %d: Close did not return within %v of the cycle, so it did not end it: %s", run,
				closeEndsBound, cyclePlace(goroutineStacks(), r.before))
		}
		if err := r.finish(t, fmt.Sprintf("run %d: Close returned", run)); run == 0 {
			t.Logf("run %d: Close ended the cycle, and the call returned: %v", run, err)
		}
	})
}

// serveLetGoDeadline is the deadline TestCloseIsBoundedByItsDeadlineWhileAGuestHoldsTheSlot hands Close: an input to
// Close, not a bound the test asserts.
const serveLetGoDeadline = 100 * time.Millisecond

// TestCloseIsBoundedByItsDeadlineWhileAGuestHoldsTheSlot is the let-go's half of ADR 0139's witness (#1398, E2-in-b's
// instance, ADR 0137's postscript). HTTPServer.Close lets a request's response body go by a delivery, which waits for
// the run slot on Close's own goroutine, before Close's bounded wait. Here the slot is held throughout by the test,
// in the place of a guest that answered and then computes without yielding, which no p3serve row does. The request is
// a served one's parts (TestAResponseBodyRecordedAfterCloseIsLetGoAtOnce's), with a task that never ends.
//
// **The places asserted:** Close returns, reporting false, while the slot is still held; and the let-go's store was
// dropped, not applied, since its guest writer was not told its reader had gone, and the pending count is balanced.
// So the let-go ended at Close's deadline and wrote nothing without the slot.
//
// NEUTER, run: HTTPServer.Close passing nil for its let-go's deadline. Close is still in the let-go at p3serveGuard,
// its goroutine waiting for the slot in takeRunSlot under httpReqBody.closeUntil.
func TestCloseIsBoundedByItsDeadlineWhileAGuestHoldsTheSlot(t *testing.T) {
	h := newAsyncHandles()
	h.hostIO = sync.NewCond(&h.mu)
	w := &writableStreamEnd{}
	re := &readableStreamEnd{conn: w}
	w.conn = re
	b := &httpReqBody{h: h, reg: newHostHTTP(), reader: re}
	q := &serveRequest{incoming: &httpIncoming{}, ended: make(chan struct{}), body: b}
	srv := NewHTTPServer(nil, "handle")
	srv.reqs[q] = struct{}{}

	slot := &runChain{} // the computing guest's
	h.acquireRun(slot, nil, nil)
	held := true
	release := func() {
		if held {
			held = false
			h.releaseRunFor(slot)
		}
	}
	defer release()

	closed := make(chan bool, 1)
	go func() { closed <- srv.Close(time.Now().Add(serveLetGoDeadline)) }()
	select {
	case complete := <-closed:
		if complete {
			t.Fatal("Close reported every task ended, and this one's never does")
		}
	case <-time.After(p3serveGuard):
		var place strings.Builder
		for _, g := range strings.Split(goroutineStacks(), "\n\n") {
			if strings.Contains(g, "(*HTTPServer).Close") {
				describeGoroutine(&place, g)
			}
		}
		release()
		<-closed
		t.Fatalf("Close did not return within %v while a guest held the run slot, so its let-go was not bounded by its "+
			"deadline. Close's place:%s", p3serveGuard, place.String())
	}
	h.mu.Lock()
	told, bodyClosed, pending := w.readerDropped, b.closed, h.httpPending
	h.mu.Unlock()
	if told || !bodyClosed || pending != 0 {
		t.Fatalf("after Close, with the slot still held: the guest writer told %v, the body closed %v, %d deliveries "+
			"pending; want false, true and 0, a let-go that ended at the deadline and stored nothing", told, bodyClosed,
			pending)
	}
}

// TestATeardownOfTheCallersCoreInstanceEndsItsWaitForTheCalleesSlot is the engine's half of ADR 0139's witness
// (#1398): a sync cross-component caller's wait for its callee's run slot ends when the caller's own core instance is
// closed, with no component Close. The cycle is TestCloseEndsACrossInstanceRunSlotDeadlock's. Only the core instance
// running `go`, the parent's, is closed, through `interp.Instance.Close`, which cancels its agents' contexts. That is
// the engine's signal `invokeSync` passes (`caller.Context`), so the parent's call unwinds, the parent's slot is freed,
// and the child's store lands. **The place asserted is quiescence**: that core instance's Close returns nil, which it
// does only once no caller is left in it, rather than its bound's ErrCloseIncomplete.
//
// NEUTER, run: invokeSync passing no engine. In both gate states the parent's core instance's Close returns its bound's
// error, with its one caller counted executing and one host call in flight: the sync lower, still waiting.
func TestATeardownOfTheCallersCoreInstanceEndsItsWaitForTheCalleesSlot(t *testing.T) {
	eachRunSlotCycle(t, func(t *testing.T, run int, r *runSlotCycle) {
		t.Helper()
		cd, ok := r.in.export.export("go")
		if !ok || cd.fn == nil || cd.fn.core.inst == nil {
			t.Fatalf("task-return-early exports no core-backed `go`")
		}
		parent := cd.fn.core.inst
		if err := parent.Close(); err != nil {
			t.Fatalf("run %d: the parent's core instance did not fall quiet at its Close, so its agent's wait for the "+
				"child's slot did not end: %v; %s", run, err, cyclePlace(goroutineStacks(), r.before))
		}
		if err := r.finish(t, fmt.Sprintf("run %d: the parent's core instance closed", run)); run == 0 {
			t.Logf("run %d: the parent's core instance closed quiet, and the call returned: %v", run, err)
		}
	})
}

// TestAFreeRunSlotIsTakenAfterTheStop is takeRunSlot's rule, *a wait that finds the slot free still takes it*, asked
// directly. With Close's stop and the engine both already closed, every take of a free slot must hold it, and a take
// of a held slot must end without it. #1398's first form chose among the ready cases at random, so a free slot could
// be refused, and make ci found it through TestCloseReachesItsBoundWithANamedOutcome.
//
// NEUTER, run: drop the first case of takeRunSlot's outer select, and this fails with 209 of 300 takes refused.
func TestAFreeRunSlotIsTakenAfterTheStop(t *testing.T) {
	h := newAsyncHandles()
	h.stopProducers()
	ended := make(chan struct{})
	close(ended)
	const tries = 300
	refused := 0
	for range tries {
		ch := &runChain{}
		if !h.acquireRunFor(ch, nil, nil, ended) {
			refused++
			continue
		}
		h.releaseRunFor(ch)
	}
	if refused != 0 {
		t.Fatalf("%d of %d takes of a free run slot were refused after Close's stop and the engine's end; a free "+
			"slot is taken whatever has ended (takeRunSlot)", refused, tries)
	}
	holder := &runChain{}
	if !h.acquireRun(holder, nil, nil) {
		t.Fatal("the holder's take of a free slot was refused")
	}
	if h.acquireRunFor(&runChain{}, nil, nil, ended) {
		t.Fatal("a take of a held slot did not end at Close's stop and the engine's end")
	}
	h.releaseRunFor(holder)
}
