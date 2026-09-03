// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"fmt"
	"runtime"
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

// sawQueued spins until `want` waiters are queued at `ea` and *reports* whether it saw them. It is
// `awaitQueued`'s tight-window twin rather than a duplicate of it, and the two differences are the
// whole reason it exists (grave #608).
//
// **It yields instead of sleeping.** `awaitQueued` sleeps a millisecond between polls, which is right
// for a waiter holding a five- or ten-second timeout and wrong for a **sub-millisecond** one: a waiter
// with a 500 µs timeout is on the queue for at most 500 µs, so a 1 ms poll misses it about as often as
// it catches it. Measured over 8,000 attempts on darwin/arm64 (idle, under 14 spinners, alongside the
// spec suite, and under `-race`), a `Gosched` spin observed the enqueue at p50 ≈ 9 µs, p99 ≈ 20–46 µs,
// max 1.653 ms — the tail being goroutine start latency on a contended machine rather than the
// observation gap, since the spin is already spinning when the enqueue happens.
//
// **It reports rather than calling `t.Fatalf`,** because its caller retries. A helper that fatals has
// decided the miss is a verdict, and for this window it is not one: a missed observation is the harness
// getting no answer, and the engine claim it would print is one the run has not observed.
func sawQueued(in *Instance, ea uint64, want int, within time.Duration) bool {
	mem := in.mems[0]
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		mem.waitMu.Lock()
		queued := len(mem.waiters[ea])
		mem.waitMu.Unlock()
		if queued >= want {
			return true
		}
		runtime.Gosched()
	}
	return false
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

// belowReferenceEpsilon is the timeout the next two tests are about: **under** the reference's
// `timeout_epsilon` of 1e6 ns, which decision 0060 removed rather than copied.
//
// `eval.ml:45` treats any timeout under that constant as already expired, and this engine had copied it.
// The comment at the copy site recorded what made it exact: *"with no other agent that reading is exact
// rather than an approximation."* A notifier falsifies that premise, so the constant went — and what the
// constant did, it did in two separable ways: it returned `waitTimedOut` for an interval that had not
// elapsed, and it never queued the waiter, so no notify could ever reach one. The two tests below
// assert one each, which is the decomposition grave #608 bought.
const belowReferenceEpsilon = int64(500_000) // ns, and 1e6 is the constant that is gone

// TestASubEpsilonTimeoutIsWaitedAndNotReportedExpired is decision 0060's second choice asserted with
// **no race in it**, and it is the case that carries the verdict for the divergence.
//
// Nobody notifies, so the only correct answer is `waitTimedOut` *after* the interval — and a
// `time.Timer` cannot fire early, so `elapsed >= timeout` is a bound the engine controls and a loaded
// machine can only overshoot. One-sided for the same reason
// `TestAnExpiredWaitReturnsAfterItsTimeoutAndNotBefore` is, and a **necessary** sibling of it rather
// than a smaller copy: that test's timeout is 20 ms, which is *above* the removed constant, so the
// epsilon would short-circuit nothing there and it passes with the constant in place. The distance
// between a bound and the thing it bounds decides whether it bounds anything, and 20 ms sat on the
// wrong side of 1 ms. Under the epsilon this case returns in nanoseconds and fails.
func TestASubEpsilonTimeoutIsWaitedAndNotReportedExpired(t *testing.T) {
	in := futexModule(t)

	timeout := time.Duration(belowReferenceEpsilon)
	start := time.Now()
	got := call(t, in, "wait32", I32(0), I32(0), I64(belowReferenceEpsilon))
	elapsed := time.Since(start)

	if got != waitTimedOut {
		t.Fatalf("wait32 with a %s timeout and nobody to notify it returned %d, want %d (\"timed-out\")",
			timeout, got, waitTimedOut)
	}
	if elapsed < timeout {
		t.Errorf("wait32 with a %s timeout returned %d after %s. A timer cannot fire early, so this is "+
			"the interval not being waited at all — the reference's `timeout_epsilon` reporting a "+
			"timeout that did not elapse, at a timeout below the constant (decision 0060)",
			timeout, got, elapsed)
	}
}

// TestASubEpsilonWaiterIsWokenWhenTheNotifyFindsItQueued is the other half — that a sub-epsilon wait
// *queues*, so a notify can still wake it — and it is the case grave #608 rewrote.
//
// **What it used to do and why that was wrong.** It fired the notify blind and retried for 10 s,
// needing the notify to land inside the 500 µs window by luck. Measured, that luck is **59 wins in
// 8,000 attempts — 0.74% per attempt** (darwin/arm64: idle, under 14 spinners, and alongside the spec
// suite), and its `t.Fatalf` on the budget expiring said *"no wait32 was ever woken … which is a
// timeout reported for an interval that did not elapse"* — an engine accusation for what is actually
// the harness never getting an attempt in edgewise. A starved runner cannot produce a wrong answer to
// this question, only no answer, and no-answer spelled `FAIL` reddened `main`.
//
// **Waiting for the enqueue removes the race instead of budgeting for it.** Once the notify finds the
// waiter on the queue it detaches it under `waitMu`, and decision 0060 resolves that tie toward the
// notify: *"a detached waiter whose timer has already fired still returns 0."* So the wake no longer
// has to beat the timer — it has to find a queued waiter, which `sawQueued` observes at p50 ≈ 9 µs.
// Measured over the same 8,000 attempts, including under `-race`: **8,000 wins, zero misses.**
//
// **A miss is retried and exhaustion is not a failure**, because the property is asserted race-free by
// `TestASubEpsilonTimeoutIsWaitedAndNotReportedExpired` above; what is unasserted after an exhausted
// budget is only the conjunction. A notify that *did* detach a waiter and got something other than 0
// back is a different matter and does fail: that is an engine answer, and it is the clause of decision
// 0060 that the detach is supposed to buy.
func TestASubEpsilonWaiterIsWokenWhenTheNotifyFindsItQueued(t *testing.T) {
	in := futexModule(t)

	// Both limits below are set against the measurement in this test's own comment — 8,000 armed
	// attempts, zero misses, enqueue observed at p50 ≈ 9 µs and max 1.653 ms — and neither is measured
	// against the population that produced the red: a loaded x86-64 CI runner, which this tree cannot
	// sample. So they are headroom over what was observed and not over a known requirement.
	const attempts = 32                    // 32x the one attempt every measured case needed
	const observe = 250 * time.Millisecond // 151x the largest observed enqueue latency

	observed := 0
	for i := range attempts {
		// A fresh address per attempt, as the litmus battery does: the previous attempt's waiter has
		// returned before this line, but an address nothing else has used makes that a property of the
		// test rather than of the engine's dequeue ordering.
		ea := int32(8 * (i + 1))

		res := make(chan outcome, 1)
		go func() {
			res <- callOffGoroutine(in, "wait32", I32(ea), I32(0), I64(belowReferenceEpsilon))
		}()
		if !sawQueued(in, uint64(ea), 1, observe) {
			(<-res).get(t) // it timed out unobserved; nothing to conclude, so try again
			continue
		}
		observed++
		woke := call(t, in, "notify", I32(ea), I32(1))
		got := (<-res).get(t)
		if woke == 0 {
			continue // the timer beat the notify to the queue; still nothing to conclude
		}
		if got != waitWoken {
			// Two mechanisms produce this and the test cannot tell them apart, so the message names
			// both rather than picking one: an inverted `w.claimed` arm survives the whole tree's tests
			// (#609), and this line is where its consequence would surface if it ever raced.
			t.Fatalf("memory.atomic.notify detached a waiter at address %d and reported waking it, and "+
				"the wait32 with a %dns timeout returned %d, want %d (\"ok\"). Either the wake path "+
				"answered with the wrong number, or the tie between a claimed waiter and a fired timer "+
				"resolved toward the timer (decision 0060, #609)",
				ea, belowReferenceEpsilon, got, waitWoken)
		}
		return
	}
	// `observed` is in the message because it separates the two ways this can run out, which the count
	// of attempts alone cannot: a queue seen but lost to the timer is the harness being slow, and a
	// queue **never** seen across every attempt is what the epsilon's short-circuit looks like from
	// here. Neither is asserted on — that is the sibling's job — but a reader of a log should not have
	// to guess which one happened.
	t.Logf("no answer: in %d attempts, a wait32 with a %dns timeout was seen on the queue %d time(s) "+
		"and never notified before its timer. Nothing is concluded about the engine — the divergence "+
		"this case shares with TestASubEpsilonTimeoutIsWaitedAndNotReportedExpired is asserted there "+
		"without a race, and only the conjunction goes unasserted (grave #608)",
		attempts, belowReferenceEpsilon, observed)
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
