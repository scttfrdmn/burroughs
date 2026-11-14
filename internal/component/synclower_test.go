// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// readingRow returns the reading recorded for key in a committed `<key>: <reading>` file, failing rather than
// defaulting when the line is absent: a witness that falls back to a built-in expectation is checking itself.
func readingRow(t *testing.T, path, key string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the committed reading is missing, so this witness has no oracle: %v", err)
	}
	for _, ln := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(ln, key+": "); ok {
			return strings.TrimSpace(rest)
		}
	}
	t.Fatalf("%s records no reading for %q", path, key)
	return ""
}

// TestASyncLowerOfAnAsyncImportBlocksOrTrapsByType is #1268's witness (ADR 0126): `sync-lower-wait-for.wasm`
// lowers wait-for(10ms) WITHOUT the async option, the pin's blocking sync lower
// (def:canon_lower:"thread.wait_until(subtask.resolved)"), against wasmtime 49.0.3's two readings
// (sync-lower-wait-for.reading), in both gate states:
//   - `go`, sync-typed, traps "cannot block a synchronous task before returning": the wait meets the sync tail;
//   - `go-async-typed` blocks until the timer fires and returns 7.
//
// It replaces the test that previously asserted ADR 0121's refusal of the form at instantiate.
//
// **One premise, stated:** `go`'s timer is 10ms, and the sync tail judges the wait microseconds after the timer is
// started, so the wait it judges is unresolved. A resolution that beat the judgement would make `go` return 7,
// which the pin also allows; the margin is the oracle's own fixture's.
//
// Neutered, run: the refusal at instantiate restored, and both rows are refused at instantiate; the wait passed
// `judged` false, and `go` returns 7; `syncTail` without its `!t.asyncTyped` test (the sync tail judged by lift
// form, grave #1267), and `go-async-typed` traps too.
func TestASyncLowerOfAnAsyncImportBlocksOrTrapsByType(t *testing.T) {
	b, err := os.ReadFile("testdata/sync-lower-wait-for.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	for _, gate := range []string{"0", "1"} {
		for _, export := range []string{"go", "go-async-typed"} {
			t.Run(export+"/cm-threads="+gate, func(t *testing.T) {
				t.Setenv(asyncGateEnv, "1")
				t.Setenv(cmThreadsGateEnv, gate)
				reading := readingRow(t, "testdata/sync-lower-wait-for.reading", export)
				in, err := InstantiateWithHost(b, NewHost(io.Discard, io.Discard, nil))
				if err != nil {
					t.Fatalf("instantiate: %v; a sync lower of an async import is the pin's blocking sync lower", err)
				}
				defer in.Close()
				res, err := callWithin(t, in, export)
				if trap, ok := strings.CutPrefix(reading, "trap: "); ok {
					if err == nil || !strings.Contains(err.Error(), trap) {
						t.Fatalf("%s() = %v, %v; want wasmtime's trap %q", export, res, err, trap)
					}
					return
				}
				want, perr := strconv.ParseUint(reading, 10, 32)
				if perr != nil {
					t.Fatalf("sync-lower-wait-for.reading's %s row %q: %v", export, reading, perr)
				}
				if err != nil {
					t.Fatalf("%s() = %v; want %d, wasmtime's reading", export, err, want)
				}
				if got, ok := res[0].U32(); !ok || uint64(got) != want {
					t.Fatalf("%s() = %v, want %d, wasmtime's reading", export, res, want)
				}
			})
		}
	}
}

// heldOp is a test `op` (test:async/ops::op, an async import) whose resolution the test makes: each call's onResolve
// is handed over on started, and nothing resolves it until the test does.
func heldOp(started chan<- func(canon.Value)) asyncLowerImpl {
	return func(_ *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
		onStart()
		started <- onResolve
		return func() {}, nil
	}
}

// awaitCall runs export on a goroutine and returns the channel its outcome arrives on.
func awaitCall(in *Instantiated, export string) <-chan callOutcome {
	done := make(chan callOutcome, 1)
	go func() { r, e := in.CallValues(export); done <- callOutcome{r, e} }()
	return done
}

type callOutcome struct {
	res []canon.Value
	err error
}

// outcomeWithin reads a call's outcome, failing the test at a harness hang guard, not a measurement.
func outcomeWithin(t *testing.T, done <-chan callOutcome, what string) callOutcome {
	t.Helper()
	select {
	case o := <-done:
		return o
	case <-time.After(10 * time.Second):
		t.Fatalf("%s did not return within the 10s hang guard", what)
		return callOutcome{}
	}
}

// TestAPendingSyncLoweredCallOutlivesTheParkBound is the sync lower's waker term (ADR 0126): in one instance,
// `sync-lower-park.wasm`'s `block` is held in a sync lower of the test `op`, and its callback-lifted `park` WAITs
// on an empty set with `liftParkBound` at 5ms. Nothing else in the instance is pending, so the only waker is the
// sync-lowered call: the park must not expire while it is pending, over twenty bounds. Once op resolves, `block`
// returns op's value, the waker is gone, and the park expires, the bound keeping its purpose.
//
// The green outcome does not depend on timing: nothing can end the park while the call is pending. Only the
// neuter's detection does, at twenty bounds of margin.
//
// Neutered, run: hostWaitPendingLocked without the sync-lower term, and the park reads ErrLiftParkExpired within
// the exposure, while `block` is still pending.
func TestAPendingSyncLoweredCallOutlivesTheParkBound(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	prev := liftParkBound
	liftParkBound = 5 * time.Millisecond
	defer func() { liftParkBound = prev }()
	b, err := os.ReadFile("testdata/sync-lower-park.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	started := make(chan func(canon.Value), 1)
	h := NewHost(io.Discard, io.Discard, nil)
	h.asyncImpls = map[string]asyncLowerImpl{"test:async/ops::op": heldOp(started)}
	in, err := InstantiateWithHost(b, h)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()
	blocked := awaitCall(in, "block")
	var resolve func(canon.Value)
	select {
	case resolve = <-started:
	case <-time.After(10 * time.Second): // a harness hang guard, not a measurement
		t.Fatal("op was not called within the 10s hang guard")
	}
	a := in.w.async
	awaitPlace(t, a, "block waiting in its sync lower", func() {}, func() bool { return len(a.syncLowerCalls) == 1 })
	parked := awaitCall(in, "park")
	awaitPlace(t, a, "park waiting on its set", func() {}, func() bool {
		for _, e := range a.entries {
			if s, ok := e.(*waitableSet); ok && s.waiting > 0 {
				return true
			}
		}
		return false
	})
	select {
	case o := <-parked:
		t.Fatalf("park returned %v, %v while block's sync-lowered call was pending: a pending call is a waker", o.res, o.err)
	case <-time.After(20 * liftParkBound): // the exposure: twenty bounds in which an expiry would show
	}
	resolve(canon.U32(7))
	if o := outcomeWithin(t, blocked, "block"); o.err != nil {
		t.Fatalf("block() = %v; want op's 7", o.err)
	} else if v, ok := o.res[0].U32(); !ok || v != 7 {
		t.Fatalf("block() = %v, want op's 7", o.res)
	}
	if o := outcomeWithin(t, parked, "park"); !errors.Is(o.err, ErrLiftParkExpired) {
		t.Fatalf("park() = %v, %v once nothing was pending; want ErrLiftParkExpired, the bound's purpose", o.res, o.err)
	}
}

// TestCloseEndsAPendingSyncLoweredCall is Close's end of a blocking sync lower (ADR 0126), under -race too:
// `block` is held in its sync lower of an `op` that never resolves, and Close returns, the call returns ended by
// Close, and the registry holds no call.
//
// Neutered, run: see TestCloseWakesASyncLoweredCallWithNoThread, which is the stop's own witness; this one passes
// with the stop's wakes skipped, because killAllParked aborts the Thread's wait first. Run under that neuter, its
// registry check found a defect in the first build: the call was removed from the registry in line after the wait,
// and an agent ended at a safepoint after Close unwinds past that line, so the removal is now deferred.
func TestCloseEndsAPendingSyncLoweredCall(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	b, err := os.ReadFile("testdata/sync-lower-park.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	started := make(chan func(canon.Value), 1)
	h := NewHost(io.Discard, io.Discard, nil)
	h.asyncImpls = map[string]asyncLowerImpl{"test:async/ops::op": heldOp(started)}
	in, err := InstantiateWithHost(b, h)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	blocked := awaitCall(in, "block")
	a := in.w.async
	awaitPlace(t, a, "block waiting in its sync lower", func() {}, func() bool { return len(a.syncLowerCalls) == 1 })
	closed := make(chan struct{})
	go func() { in.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(10 * time.Second): // a harness hang guard, not a measurement
		t.Fatal("Close did not return within the 10s hang guard")
	}
	// The call's wait returns errThreadKilled, and the agent then ends at its next safepoint, the core instance being
	// closed, so the caller reads T-5's termination (interp.ErrTerminated) or the kill, whichever reaches it.
	if o := outcomeWithin(t, blocked, "block"); !errors.Is(o.err, errThreadKilled) && !errors.Is(o.err, interp.ErrTerminated) {
		t.Fatalf("block() = %v, %v after Close; want errThreadKilled or interp.ErrTerminated", o.res, o.err)
	}
	a.mu.Lock()
	n := len(a.syncLowerCalls)
	a.mu.Unlock()
	if n != 0 {
		t.Errorf("%d sync-lowered call(s) still registered after Close", n)
	}
}

// TestCloseWakesASyncLoweredCallWithNoThread is stopHostWaits' wake (ADR 0126): the sync lower called from a caller
// with no Thread record, whose wait `suspending` runs with no abort channel, so killAllParked cannot reach it. The
// stop closes its wake, and the call returns errThreadKilled; a sync lower started after the stop returns it at
// once, rather than waiting on an impl the stop may have refused to start.
//
// Neutered, run: stopHostWaits skipping the wakes, and the call never returns, until the hang guard.
func TestCloseWakesASyncLoweredCallWithNoThread(t *testing.T) {
	cc, err := interp.NewCanonCallerForTest(1)
	if err != nil {
		t.Fatalf("harness caller: %v", err)
	}
	a := newAsyncHandles()
	started := make(chan func(canon.Value), 1)
	lower := syncLowerOfAsync(heldOp(started), syncLowerForm{name: "test:async/ops::op", hasResult: true}, a)
	done := make(chan error, 1)
	go func() { _, e := lower(cc, []interp.Value{interp.I32(5)}); done <- e }()
	select {
	case <-started:
	case <-time.After(10 * time.Second): // a harness hang guard, not a measurement
		t.Fatal("op was not called within the 10s hang guard")
	}
	awaitPlace(t, a, "the call registered", func() {}, func() bool { return len(a.syncLowerCalls) == 1 })
	a.stopHostWaits()
	select {
	case e := <-done:
		if !errors.Is(e, errThreadKilled) {
			t.Fatalf("the call returned %v after the stop; want errThreadKilled", e)
		}
	case <-time.After(10 * time.Second): // a harness hang guard, not a measurement
		t.Fatal("the sync-lowered call did not return within 10s of Close's stop")
	}
	if _, e := lower(cc, []interp.Value{interp.I32(5)}); !errors.Is(e, errThreadKilled) {
		t.Fatalf("a sync lower after the stop returned %v; want errThreadKilled at once", e)
	}
}

// TestAStopBeforeTheWaitStillEndsTheCall is the window between a sync lower's start and its wait (ADR 0126): Close's
// stop runs while the impl is still running, so the stop takes the wake before the caller waits on it. The impl here
// runs the stop itself, which makes the window certain rather than a schedule; the call returns errThreadKilled.
//
// TestCloseEndsAPendingSyncLoweredCall found this window, 1 run of 12 under -race: its Close came after the call
// registered and before it waited, and Close hung. The waiter read the subtask's wake, which the stop had taken
// and nil'd, and selected on nil.
//
// Neutered, run: awaitSyncLower selecting on the subtask's `resolveWake` read at the wait, as the first build did,
// and the call never returns, until the hang guard.
func TestAStopBeforeTheWaitStillEndsTheCall(t *testing.T) {
	cc, err := interp.NewCanonCallerForTest(1)
	if err != nil {
		t.Fatalf("harness caller: %v", err)
	}
	a := newAsyncHandles()
	stopsInside := func(_ *interp.CanonCaller, onStart func() []interp.Value, _ func(canon.Value)) (func(), error) {
		onStart()
		a.stopHostWaits() // Close's stop, while the impl runs and before the caller waits
		return func() {}, nil
	}
	done := make(chan error, 1)
	go func() {
		_, e := syncLowerOfAsync(stopsInside, syncLowerForm{name: "op", hasResult: true}, a)(cc, []interp.Value{interp.I32(5)})
		done <- e
	}()
	select {
	case e := <-done:
		if !errors.Is(e, errThreadKilled) {
			t.Fatalf("the call returned %v; want errThreadKilled", e)
		}
	case <-time.After(10 * time.Second): // a harness hang guard, not a measurement
		t.Fatal("a call whose wake the stop took before its wait did not return within the 10s hang guard")
	}
}

// TestASyncLowerReturnsAOneWordResultAndStoresAWiderOne is lowerSyncResult's two arms (ADR 0126): a u32 result
// is returned as one core word, and a result<list<ip-address>, error-code> is stored at the return pointer through
// the codec. The stored value is an error, since the harness caller has no realloc for a list's backing; the list
// arm is name-lookup.wasm's and p3lookup's, whose guests have one.
func TestASyncLowerReturnsAOneWordResultAndStoresAWiderOne(t *testing.T) {
	cc, err := interp.NewCanonCallerForTest(1)
	if err != nil {
		t.Fatalf("harness caller: %v", err)
	}
	a := newAsyncHandles()
	inline := func(v canon.Value) asyncLowerImpl {
		return func(_ *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
			onStart()
			onResolve(v)
			return nil, nil
		}
	}
	ret, err := syncLowerOfAsync(inline(canon.U32(0xdeadbeef)), syncLowerForm{name: "op", hasResult: true}, a)(cc, []interp.Value{interp.I32(5)})
	if err != nil || len(ret) != 1 || uint32(ret[0].Int32()) != 0xdeadbeef {
		t.Fatalf("a one-word result = %v, %v; want the word 0xdeadbeef", ret, err)
	}
	ret, err = syncLowerOfAsync(inline(lookupErr("invalid-argument")), syncLowerForm{name: "op", hasResult: true, retptr: true}, a)(cc, []interp.Value{interp.I32(7), interp.I32(64)})
	if err != nil || len(ret) != 0 {
		t.Fatalf("a stored result = %v, %v; want nothing returned", ret, err)
	}
	b, err := cc.Read(64, 5)
	if err != nil {
		t.Fatal(err)
	}
	if b[0] != 1 || b[4] != 1 {
		t.Errorf("the stored result's bytes are %v; want error (1) at +0 and invalid-argument (1) at +4", b)
	}
}
