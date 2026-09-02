// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"fmt"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// futexModule instantiates one shared memory with the four entry points these tests need, and it goes
// through the **text front end and the decoder** rather than building a `memory` by literal.
//
// That is not ceremony. A test that calls `mem.wait` directly proves the helper works while nothing
// calls it — and it would also skip `newMemory`'s `checkBaseAlignment`, which is grave #579's exact
// shape: a hand-built `memory` gets whatever alignment escape analysis happens to give its slice. Every
// assertion below is therefore about the *instruction*, executed by the dispatch loop, on a memory the
// engine allocated.
//
// `wait` returns the reference's three results and `notify` returns its wake count, so both are read
// off the guest's own stack rather than from an engine field.
func futexModule(t *testing.T) *Instance {
	t.Helper()

	const src = `(module
	  (memory 1 1 shared)
	  (func (export "wait32") (param $addr i32) (param $expect i32) (param $timeout i64) (result i32)
	    (memory.atomic.wait32 (local.get $addr) (local.get $expect) (local.get $timeout)))
	  (func (export "wait64") (param $addr i32) (param $expect i64) (param $timeout i64) (result i32)
	    (memory.atomic.wait64 (local.get $addr) (local.get $expect) (local.get $timeout)))
	  (func (export "notify") (param $addr i32) (param $count i32) (result i32)
	    (memory.atomic.notify (local.get $addr) (local.get $count)))
	  (func (export "store") (param $addr i32) (param $v i32)
	    (i32.atomic.store (local.get $addr) (local.get $v))))`

	img, err := text.EncodeModule([]byte(src))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	m, err := (&binary.Decoder{Features: binary.Features{Threads: true}}).DecodeModule(img)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	in, trap := Instantiate(m)
	if trap != nil {
		t.Fatalf("instantiate: %v", trap)
	}
	if derr := in.Deferred(); derr != nil {
		t.Fatalf("instantiate fell short: %v", derr)
	}
	return in
}

// call invokes an export from the *test's own* goroutine and returns its single i32 result.
func call(t *testing.T, in *Instance, name string, args ...Value) int32 {
	t.Helper()
	return callOffGoroutine(in, name, args...).get(t)
}

// callVoid invokes an export with no results, on the test's own goroutine. Its own function rather
// than a discarded `call`, because `call`'s arity check is what caught the store's void signature and
// dropping that check to share one helper would remove an assertion to save a line.
func callVoid(t *testing.T, in *Instance, name string, args ...Value) {
	t.Helper()
	if _, err := in.Invoke(name, args...); err != nil {
		t.Fatalf("%s%v: %v", name, args, err)
	}
}

// awaitQueued blocks until `want` waiters are queued at `ea`, and it is the file's one **read of the
// mechanism**.
//
// Used for *sequencing* and never as an assertion. Every test here needs the point at which a waiter
// has reached its `select`, and no guest-visible operation reports it: a `notify` of count 0 wakes
// nobody and returns 0 whether the queue is empty or full, so the only counter is the map. Polling it
// rather than sleeping for a plausible interval is the same rule as everywhere else — *a duration is
// not a completion signal* — and the poll is over an engine field precisely because the signal has no
// guest-visible channel.
//
// A `t.Fatalf` here reports a **premise** that did not hold, not a failed assertion, and says so.
func awaitQueued(t *testing.T, in *Instance, ea uint64, want int) {
	t.Helper()

	mem := in.mems[0]
	queued := 0
	deadline := time.Now().Add(10 * time.Second)
	for queued < want && time.Now().Before(deadline) {
		mem.waitMu.Lock()
		queued = len(mem.waiters[ea])
		mem.waitMu.Unlock()
		if queued < want {
			time.Sleep(time.Millisecond)
		}
	}
	if queued != want {
		t.Fatalf("only %d of %d waiter(s) reached the queue at address %d within 10s — this is the "+
			"test's premise and not its assertion: nothing was measured", queued, want, ea)
	}
}

// outcome is one invocation's result carried off a goroutine.
//
// **It exists because `t.Fatalf` from a spawned goroutine is undefined.** `testing` documents
// `FailNow` as having to be called from the goroutine running the test, and every case in this file
// runs a wait on a second goroutine, so the obvious `call(t, …)` inside the `go func` would be a
// harness defect in the exact tests written to catch engine defects — the failure it produces is a
// test that stops without reporting, which reads as a hang rather than as a failure.
type outcome struct {
	// what names the call, because a `get` on the test's goroutine no longer has the arguments in
	// scope and "invoke failed" without them names neither the export nor the address.
	what string
	res  int32
	err  error
}

// callOffGoroutine invokes an export and returns its single i32 result without touching `*testing.T`,
// so it is safe on any goroutine. `get` is the half that must run on the test's own. The name says
// where it may be called from, because `memory_test.go` already has an `invoke1` that takes a `*T` and
// the two are distinguished by exactly that.
func callOffGoroutine(in *Instance, name string, args ...Value) outcome {
	what := fmt.Sprintf("%s%v", name, args)
	got, err := in.Invoke(name, args...)
	if err != nil {
		return outcome{what: what, err: err}
	}
	if len(got) != 1 {
		return outcome{what: what, err: fmt.Errorf("returned %d values, want 1", len(got))}
	}
	return outcome{what: what, res: got[0].Int32()}
}

// get unwraps an outcome, and must be called from the goroutine running the test.
func (o outcome) get(t *testing.T) int32 {
	t.Helper()
	if o.err != nil {
		t.Fatalf("%s: %v", o.what, o.err)
	}
	return o.res
}

// TestANotifyWakesAWaiterOnAnotherThread is the assertion #543 was filed for: **result 0 exists.**
//
// The issue's title is *"`memory.atomic.wait` cannot return 0 (woken)"* and its body's reason was that
// there was nothing here to suspend. [ADR 0059]'s `world` made that false; this is the test that says so
// on the instruction rather than on the mechanism.
//
// **No corpus vector can witness this.** `atomic.wast` seeds the cell with `0xffffffffffff` and waits on
// 0 twice, taking the not-equal arm both times, and there is no `.wast` directive that starts the second
// agent needed to do the waking. So this test is the oracle, and the same is true of every other case in
// this file.
//
// The notify is retried rather than fired once, because *"the waiter has reached its `select`"* is not
// an event this side can observe: a single notify could arrive before the waiter enqueued and legally
// wake nothing. Retrying converts an unobservable ordering into a bounded loop, and the failure mode it
// removes would have been a flake rather than a wrong answer.
//
// [ADR 0059]: ../../docs/decisions/0059-the-safepoint-poll-is-guarded-at-the-pc-assignment-because-a-back-edge-is-a-runtime-comparison-and-straight-line-code-pays-nothing.md
func TestANotifyWakesAWaiterOnAnotherThread(t *testing.T) {
	in := futexModule(t)

	res := make(chan outcome, 1)
	go func() {
		res <- callOffGoroutine(in, "wait32", I32(0), I32(0), I64(int64(5*time.Second)))
	}()

	woke := int32(0)
	deadline := time.Now().Add(5 * time.Second)
	for woke == 0 && time.Now().Before(deadline) {
		woke = call(t, in, "notify", I32(0), I32(1))
		if woke == 0 {
			time.Sleep(time.Millisecond)
		}
	}
	if woke != 1 {
		t.Fatalf("memory.atomic.notify woke %d waiters, want 1 — no waiter was ever queued, so the "+
			"wait either declined to suspend or queued under a key the notify does not compute. "+
			"Nothing in the corpus can witness this (#543, decision 0060)", woke)
	}
	select {
	case r := <-res:
		if got := r.get(t); got != waitWoken {
			t.Errorf("memory.atomic.wait32 returned %d, want %d (\"ok\"): the notify reported waking "+
				"it, so this thread was detached and then told something else happened",
				got, waitWoken)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("memory.atomic.wait32 did not return after a notify claimed it — the wake was " +
			"counted and not delivered, which is the half of the detach that happens outside the lock")
	}
}

// TestAWaitWhoseCellChangedDoesNotQueue is the not-equal arm, and it is the one the corpus does cover —
// asserted here anyway because it is the arm that must stay total when the other two became reachable.
//
// A queued waiter here would be a hang rather than a wrong number, so the timeout is what makes the
// assertion, and it is infinite deliberately: a finite one would let a queued waiter *time out* and
// return 2, which looks like a plausible answer instead of like the defect.
func TestAWaitWhoseCellChangedDoesNotQueue(t *testing.T) {
	in := futexModule(t)
	callVoid(t, in, "store", I32(0), I32(7))

	res := make(chan outcome, 1)
	go func() { res <- callOffGoroutine(in, "wait32", I32(0), I32(0), I64(-1)) }()

	select {
	case r := <-res:
		if got := r.get(t); got != waitNotEqual {
			t.Errorf("wait32 on a cell holding 7, expecting 0, returned %d, want %d (\"not-equal\")",
				got, waitNotEqual)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("wait32 suspended on a cell that did not hold the expected value, with an infinite " +
			"timeout — the compare is not deciding, or is deciding on the wrong address")
	}
}

// TestAShortTimeoutIsHonouredRatherThanTreatedAsExpired is decision 0060's second choice, and it is a
// **deliberate divergence from the reference** — the only authority this path has.
//
// `eval.ml:45` treats any timeout under `timeout_epsilon` (1e6 ns) as already expired, and this engine
// copied the constant. The comment at the copy site recorded what made it exact: *"with no other agent
// that reading is exact rather than an approximation."* A notifier falsifies that premise, so the
// constant went, and this test is what would fail if it came back: 500 µs is under the epsilon, the
// notify arrives inside it, and the old code returned 2 without ever queuing.
//
// The timeout is short **and the notify is retried until it lands**, so what is asserted is not "a wake
// beat a timer" — a race this test would lose on a loaded machine — but that a wake *is possible at
// all* at this timeout, which the epsilon made impossible. A returned 2 is therefore retried too; only
// the loop expiring is a failure.
func TestAShortTimeoutIsHonouredRatherThanTreatedAsExpired(t *testing.T) {
	in := futexModule(t)

	const belowReferenceEpsilon = int64(500_000) // ns, and 1e6 is the constant that is gone
	deadline := time.Now().Add(10 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatalf("in 10s, no wait32 with a %dns timeout was ever woken. Under the reference's "+
				"`timeout_epsilon` every one of them returns %d immediately without queuing, which "+
				"is a timeout reported for an interval that did not elapse (decision 0060)",
				belowReferenceEpsilon, waitTimedOut)
		}
		res := make(chan outcome, 1)
		go func() {
			res <- callOffGoroutine(in, "wait32", I32(0), I32(0), I64(belowReferenceEpsilon))
		}()
		// One notify, immediately: it either finds the waiter or does not, and the outer loop is the
		// retry. Racing the notify inside the window is the point — it is what the epsilon forbade.
		if call(t, in, "notify", I32(0), I32(1)) == 0 {
			(<-res).get(t)
			continue
		}
		if got := (<-res).get(t); got == waitWoken {
			return
		}
	}
}

// TestAnExpiredWaitReturnsAfterItsTimeoutAndNotBefore is the other half of the same divergence, and it
// is a **one-sided** assertion on purpose.
//
// A `time.Timer` cannot fire early, so `elapsed >= timeout` is a bound the engine controls and the
// scheduler can only overshoot. That direction is what the epsilon violated: with the constant in
// place, a 20 ms wait returned `waitTimedOut` in nanoseconds. The upper side is deliberately not
// asserted — a machine under load can take arbitrarily long to schedule the goroutine, and a ceiling
// there would be measuring the machine.
func TestAnExpiredWaitReturnsAfterItsTimeoutAndNotBefore(t *testing.T) {
	in := futexModule(t)

	const timeout = 20 * time.Millisecond
	start := time.Now()
	got := call(t, in, "wait32", I32(0), I32(0), I64(int64(timeout)))
	elapsed := time.Since(start)

	if got != waitTimedOut {
		t.Fatalf("wait32 with nobody to notify it returned %d, want %d (\"timed-out\")",
			got, waitTimedOut)
	}
	if elapsed < timeout {
		t.Errorf("wait32 with a %s timeout returned %d after %s. A timer cannot fire early, so this "+
			"is the interval not being waited at all — the reference's `timeout_epsilon` reporting a "+
			"timeout that did not elapse (decision 0060)", timeout, got, elapsed)
	}
}

// TestNotifyWakesNoMoreThanItsCount checks the operand that only becomes observable once waiters exist.
// Before this slice `notify` returned 0 at every count, so the count had nothing to bound.
//
// Three waiters, woken two then one: what this catches is a notify that wakes the whole queue and
// *reports* the count, which would pass any test that only read the return value.
func TestNotifyWakesNoMoreThanItsCount(t *testing.T) {
	in := futexModule(t)

	const waiters = 3
	res := make(chan outcome, waiters)
	for range waiters {
		go func() {
			res <- callOffGoroutine(in, "wait32", I32(0), I32(0), I64(int64(10*time.Second)))
		}()
	}
	awaitQueued(t, in, 0, waiters)

	if woke := call(t, in, "notify", I32(0), I32(2)); woke != 2 {
		t.Fatalf("notify with count=2 over a queue of 3 woke %d, want 2", woke)
	}
	if woke := call(t, in, "notify", I32(0), I32(7)); woke != 1 {
		t.Errorf("notify with count=7 over the remaining queue woke %d, want 1 — the first notify "+
			"either woke more than its count or left the queue in a state the second cannot read",
			woke)
	}
	for range waiters {
		select {
		case r := <-res:
			if got := r.get(t); got != waitWoken {
				t.Errorf("a waiter returned %d, want %d: three were notified in total", got, waitWoken)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("a waiter never returned, so a wake was counted and not delivered")
		}
	}
}

// TestANotifyAtAnotherAddressWakesNothing is the key's own assertion, and the one that would pass
// vacuously if the map were keyed by anything constant.
//
// Address 8 rather than 4: at width 4 the two would be different fields of the same 8-byte word, and a
// queue keyed by the containing word rather than by the effective address would still pass. What must
// fail is a key that ignores the address, and what must *not* fail is a key that distinguishes two
// addresses the guest can distinguish.
func TestANotifyAtAnotherAddressWakesNothing(t *testing.T) {
	in := futexModule(t)

	res := make(chan outcome, 1)
	go func() {
		res <- callOffGoroutine(in, "wait32", I32(0), I32(0), I64(int64(2*time.Second)))
	}()
	// Long enough that a wrong key has time to be wrong, short enough that the wait's own timeout is
	// the backstop rather than the test's.
	time.Sleep(50 * time.Millisecond)
	if woke := call(t, in, "notify", I32(8), I32(1)); woke != 0 {
		t.Errorf("notify at address 8 woke %d waiters queued at address 0, want 0 — the queue's key "+
			"does not distinguish the two addresses", woke)
	}
	if got := (<-res).get(t); got != waitTimedOut {
		t.Errorf("the waiter at address 0 returned %d, want %d: nothing notified its address",
			got, waitTimedOut)
	}
}

// TestAWait64AndAWait32ShareOneQueuePerAddress is decision 0060's *one queue per address, not per
// (address, width)* — the choice a reader would most reasonably expect to have gone the other way.
//
// The proposal wakes the waiters *at an address*, and the reference's notify action carries an address
// and no type, so a width-tagged key would decline to wake a correct program. Asserted through the two
// instructions rather than through the map, because the map is where the mistake would be *made* and the
// instruction pair is where it would be *observed*.
func TestAWait64AndAWait32ShareOneQueuePerAddress(t *testing.T) {
	in := futexModule(t)

	res := make(chan outcome, 1)
	go func() {
		res <- callOffGoroutine(in, "wait64", I32(0), I64(0), I64(int64(10*time.Second)))
	}()

	woke := int32(0)
	deadline := time.Now().Add(10 * time.Second)
	for woke == 0 && time.Now().Before(deadline) {
		woke = call(t, in, "notify", I32(0), I32(1))
		if woke == 0 {
			time.Sleep(time.Millisecond)
		}
	}
	if woke != 1 {
		t.Fatalf("a notify at address 0 woke %d of the one wait64 queued there, want 1 — the queue "+
			"is keyed by width as well as address, so a correct program does not get woken", woke)
	}
	if got := (<-res).get(t); got != waitWoken {
		t.Errorf("wait64 returned %d, want %d", got, waitWoken)
	}
}

// TestAStopCompletesWithAThreadSuspendedInAWaitAndDoesNotWakeIt is contract §3 **SP-2 and SP-4 in one
// run**, and it is the pair of clauses this slice is what made checkable.
//
// SP-2: a thread suspended in `memory.atomic.wait` *"counts as at a safepoint"*. SP-4: a stop *"with N
// threads parked in host calls completes without waking them."* Together they forbid the obvious
// implementation — SP-1's protocol has the *thread* announce its arrival, a suspended thread cannot
// announce anything, and SP-4 forbids waking it to ask — so `Stop` counts the mark itself
// (decision 0060's third choice).
//
// **Both halves are asserted, because either one alone passes on a wrong engine.** A `Stop` that woke
// the waiter to collect its arrival would return nil here and satisfy SP-2 while breaking SP-4; an
// engine with no mark at all leaves SP-4 intact and returns `ErrStopDeadline`. The second assertion is
// therefore that the wait has *not* returned while the world is stopped, and the third is that it is
// still queued afterwards — a notify of count 1 waking it is what says the stop passed through without
// disturbing the queue.
//
// The notify comes **after** `Resume` and not during the stop, which is not a preference: a notify is a
// guest invocation, and a guest invocation entered while a stop is in progress parks at its first call
// site. Firing it under the stop would be waiting for a thread this test has itself arranged to be
// parked.
func TestAStopCompletesWithAThreadSuspendedInAWaitAndDoesNotWakeIt(t *testing.T) {
	in := futexModule(t)

	res := make(chan outcome, 1)
	go func() {
		res <- callOffGoroutine(in, "wait32", I32(0), I32(0), I64(int64(30*time.Second)))
	}()
	awaitQueued(t, in, 0, 1)

	if err := in.Stop(2 * time.Second); err != nil {
		t.Fatalf("Stop with one thread suspended in memory.atomic.wait: %v.\n"+
			"Contract §3 SP-2 makes that thread *at a safepoint*, so this stop has nothing left to "+
			"wait for. A deadline expiry means the suspension is not marked — `Stop` is waiting for "+
			"an arrival from a thread that is blocked and cannot send one, and SP-4 forbids waking "+
			"it to ask (decision 0060)", err)
	}

	// SP-4's half. A stop that collected the arrival by waking the waiter would have delivered here.
	select {
	case r := <-res:
		t.Fatalf("the suspended thread returned %d during the stop, want it still suspended — "+
			"contract §3 SP-4 requires a stop with N threads parked to complete *without waking "+
			"them*, and this stop woke one to count it", r.res)
	case <-time.After(100 * time.Millisecond):
	}

	in.Resume()

	// Still queued, and a notify is what says so from the guest's side. Not retried, unlike the other
	// notifies in this file: `awaitQueued` established that this waiter is in the map, and nothing
	// between there and here can have removed it, so a 0 is a finding rather than a lost race.
	if woke := call(t, in, "notify", I32(0), I32(1)); woke != 1 {
		t.Fatalf("after the stop and Resume, a notify woke %d of the one waiter that was queued "+
			"before it, want 1 — the stop disturbed the queue", woke)
	}
	select {
	case r := <-res:
		if got := r.get(t); got != waitWoken {
			t.Errorf("the waiter returned %d, want %d (\"ok\")", got, waitWoken)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the waiter did not return after the notify claimed it")
	}
}

// TestAWaitThatExpiresDuringAStopDoesNotReturnUntilResume is SP-2's **other** half, and the one a
// reader is most likely to think the clause does not cover.
//
// SP-2 does not only say a suspended thread counts as arrived; it requires that such a thread *"cannot
// touch guest memory until it re-enters through a boundary that observes the stop."* A wait that
// expires while the world is stopped is exactly that re-entry, so `leaveBlocked` polls before
// `atomicWait` pushes anything, and this thread parks at the safepoint it would otherwise have walked
// straight past. Without the poll the guest resumes execution inside a stopped world, which is the
// clause's failure and not a timing detail: the host called `Stop` in order to look at guest memory.
//
// **The timer is what the test cannot control, and the premise is separated from the assertion for
// that reason.** `awaitQueued` must observe the waiter before its interval elapses; the window is one
// millisecond of polling against a one-second timeout, so missing it takes a second of starvation of
// this goroutine specifically, and if it ever happens `awaitQueued` reports a premise rather than a
// failure.
func TestAWaitThatExpiresDuringAStopDoesNotReturnUntilResume(t *testing.T) {
	in := futexModule(t)

	const timeout = time.Second
	res := make(chan outcome, 1)
	go func() {
		res <- callOffGoroutine(in, "wait32", I32(0), I32(0), I64(int64(timeout)))
	}()
	awaitQueued(t, in, 0, 1)

	if err := in.Stop(2 * time.Second); err != nil {
		t.Fatalf("Stop with one thread suspended in memory.atomic.wait: %v (see "+
			"TestAStopCompletesWithAThreadSuspendedInAWaitAndDoesNotWakeIt)", err)
	}

	// Twice the interval, so the timer has certainly fired: a `time.Timer` cannot fire early, and
	// this is the direction the engine controls.
	select {
	case r := <-res:
		t.Fatalf("the wait returned %d while the world was stopped, %s after a %s timeout — its "+
			"interval expired and it went straight back to the dispatch loop, so the guest is "+
			"executing inside a stop. Contract §3 SP-2: the wake is a boundary and must observe "+
			"the stop", r.res, 2*timeout, timeout)
	case <-time.After(2 * timeout):
	}

	in.Resume()

	select {
	case r := <-res:
		if got := r.get(t); got != waitTimedOut {
			t.Errorf("the wait returned %d, want %d (\"timed-out\") — parking at the safepoint on "+
				"the way out must not change which of the three results it reports",
				got, waitTimedOut)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the wait did not return after Resume — it parked and the release is not reaching " +
			"it, which is a hang rather than a degraded mode")
	}
}
