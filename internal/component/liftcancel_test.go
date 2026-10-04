// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// cancelLog records WHAT happened and IN WHAT ORDER, because one of the four facts under test is an
// order and not an occurrence. #862's reading is explicit that the host call is torn down *before* the
// guest's cancellation code runs, and that *"an implementation with the order reversed would produce the
// same two facts"* — so a pair of booleans would pass against a wrong engine.
type cancelLog struct {
	mu     sync.Mutex
	events []string
}

func (l *cancelLog) add(e string) {
	l.mu.Lock()
	l.events = append(l.events, e)
	l.mu.Unlock()
}

func (l *cancelLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.events...)
}

func (l *cancelLog) indexOf(e string) int {
	for i, got := range l.snapshot() {
		if got == e {
			return i
		}
	}
	return -1
}

// TestHostCancelsAStartedLiftTask is #887's witness: **Burroughs cancels a started async-lift task from
// the host**, checked against #862's committed readings (ADR 0094).
//
// # Why there is no parent component here
//
// #862 measured the reference *through composition* only because wasmtime gives a host no way to cancel a
// started task — `Event::Cancelled` has one producer, the guest-reachable `subtask_cancel`. **That is the
// reference's limitation, not a requirement on Burroughs**, where the host IS the caller. So this drives
// the `receipt/` guest directly, with `tick` and `note` as host imports and no parent at all. I had
// treated composition as a prerequisite; the chair corrected it — *a capability requirement can be a
// mechanism choice*.
//
// The guest is #862's, unmodified, which is the point: the same artefact that produced the reference
// readings produces these, so the guest is not a variable between the two sides.
//
// # The four facts, from the committed readings and not from literals
//
// Read out of `testdata/asynclift/cancel-wat.reading` rather than retyped, so a reading that changes
// upstream cannot leave this test asserting a stale expectation it also claims to be checking.
//
//	TICKDROP    true       the pending host tick is dropped
//	RECEIPT     observed   a cancelled task MAY still call an import
//	EVENTS      ... -> tick-dropped -> receipt(1) -> ...   the ORDER
//	STATUS      4          CANCELLED_BEFORE_RETURNED, which ruling 1 maps to ErrCancelled
//
// # What this does NOT claim
//
// **Nothing about status 3.** The before-started path has no reference reading until #884, and
// `TestCancellingABeforeStartedTaskIsRefusedByName` pins that it refuses by name instead. Fact 4 here is
// status 4's mapping only.
//
// # `task.cancel` is reached BY CONSTRUCTION here, which is better than #862's elimination
//
// #862 had to establish it by elimination (the `stripcall.py`-stripped child as the negative arm),
// because a composed parent cannot see the child's built-ins. In Burroughs the engine is the observer:
// `ErrCancelled` is returned **only** from the EXIT arm's `cancelledResolution`, which **only**
// `task.cancel`'s `cancelLocked` sets. So fact 3 is not a separate assertion — it is what fact 4 being
// true means, and a `task.cancel` that never ran would surface as a trap or a result, not as this error.
func TestHostCancelsAStartedLiftTask(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	b, err := os.ReadFile("testdata/asynclift/receipt/component.wasm")
	if err != nil {
		t.Fatalf("#862's receipt guest is missing: %v", err)
	}
	want := readCancelReading(t, "testdata/asynclift/cancel-wat.reading")

	log := &cancelLog{}
	entered := make(chan struct{}, 1)

	h := NewHost(io.Discard, io.Discard, nil)
	h.asyncImpls = map[string]asyncLowerImpl{
		// `tick` STARTS and never resolves. Without a suspension point there is nothing to cancel — the
		// guest's own world comment says so — and a resolving tick would make this a test of normal
		// completion wearing a cancellation's name.
		"tick": func(_ *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
			onStart()
			log.add("tick-entered")
			select {
			case entered <- struct{}{}:
			default:
			}
			// The onCancel hook is how the host learns its pending call was dropped. Returning a
			// recording closure rather than nil is what makes fact 1 observable at all.
			//
			// **It must RESOLVE, and the first version that did not produced a guest panic.** The engine
			// returns BLOCKED (0xffffffff) from `subtask.cancel` when the impl's cancel handler leaves the
			// subtask unresolved, and `wit-bindgen`'s drop glue is synchronous — it cannot await, so it
			// meets that value in `in_progress_update`'s `other => panic!("unknown code {other:#x}")`
			// (wit-bindgen-rt 0.44.0, `async_support/subtask.rs:204`) and traps `unreachable`. Measured,
			// not reasoned: the first run of this witness failed with a bare `unreachable` and no receipt.
			//
			// Resolving here is what a real host doing "drop the pending call" does, so this is the
			// well-behaved shape rather than a workaround. The value is ignored — `onResolve` under
			// `cancellationRequested` lowers no result and picks the terminal state from whether the
			// subtask had started (async_lower.go), which is STARTED here, hence status 4.
			//
			// **A host that resolves LATER is a separate, real gap**, filed rather than hidden behind this
			// line: the model's `canon_subtask_cancel` *waits* for resolution on a sync-lowered cancel
			// (`thread.wait_until(subtask.resolved)`, def:2427-2428) and returns BLOCKED only on the async
			// form. Burroughs returns BLOCKED unconditionally, so a host resolving asynchronously makes a
			// conforming guest panic. Not in this slice's scope — it is `subtask.cancel`'s arm, the
			// parent's path — and not this witness's subject either. **Filed as #892 with the model
			// citation**, so this line points at a known gap rather than being a workaround nobody
			// wrote down.
			return func() {
				log.add("tick-dropped")
				onResolve(canon.U32(0))
			}, nil
		},
	}
	h.syncImpls = map[string]interp.CanonFunc{
		// The receipt channel. SYNC because the guest calls it from `Drop`, which cannot await.
		"note": func(_ *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
			code := uint32(0)
			if len(args) > 0 {
				code = uint32(args[0].Bits)
			}
			log.add("receipt")
			if code != want.receiptCode {
				t.Errorf("receipt code = %d, want %d (from the committed reading)", code, want.receiptCode)
			}
			return nil, nil
		},
	}

	in, err := InstantiateWithHost(b, h)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()

	type outcome struct {
		res []canon.Value
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, cErr := in.CallValues("run", canon.U32(1))
		done <- outcome{res, cErr}
	}()

	// Wait until the guest has reached `tick`. **Not a sleep**: cancelling before the task starts is a
	// different path entirely (status 3, refused by name), so a test that raced this would sometimes
	// measure the branch it explicitly does not claim.
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the guest never reached the `tick` import within 5s; there is nothing suspended to cancel")
	}

	// **And then wait until it is actually PARKED, which is a second condition and not the same one.**
	//
	// This is the repair for a race in this witness's first version, found by neutering. `entered` fires
	// from inside `tick`'s impl — which is *during* the callee's execution, before it returns WAIT. So the
	// cancel request raced the lift loop's top-of-loop check, and **which mechanism delivered the
	// cancellation was nondeterministic**: sometimes the top-of-loop check (in place of parking),
	// sometimes the park's own check. Both are correct, which is exactly why the test passed either way
	// and said nothing about which.
	//
	// It showed up as a neuter that stopped failing: with the park's check disabled this hung, then — once
	// the yield fixture's work restored the loop check — passed. A witness whose subject depends on
	// scheduling is the parks-assertion defect again (grave #891), and the repair is the same one: wait on
	// the real condition instead of sampling.
	//
	// The real condition is `waiting > 0` on some waitable set, which `awaitEvent` increments on entry.
	// Once it holds, the guest is in the park and the top-of-loop check is behind it, so this witness's
	// subject is the park's check specifically.
	if !waitForPark(t, in, 5*time.Second) {
		t.Fatal("the guest reached `tick` but never parked within 5s; this witness's subject is the " +
			"park's cancellation check, so a cancel delivered before the park would test the wrong one")
	}

	if cErr := in.w.async.requestCancelAll(); cErr != nil {
		t.Fatalf("requestCancelAll on a started task: %v, want success", cErr)
	}

	var got outcome
	select {
	case got = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the cancelled call never returned within 10s — the lift loop is parked somewhere a " +
			"delivered cancellation should have moved it")
	}

	// ## Fact 4 — the caller gets ErrCancelled (ruling 1's mapping for status 4)
	if !errors.Is(got.err, ErrCancelled) {
		t.Fatalf("the cancelled call returned (%v, %v); want ErrCancelled, which #862's ruling 1 maps "+
			"status %d to", got.res, got.err, want.status)
	}
	if len(got.res) != 0 {
		t.Errorf("a cancelled call returned %d value(s); a cancelled task resolves with no result "+
			"(def:497 passes None)", len(got.res))
	}

	events := log.snapshot()
	t.Logf("events: %v", events)

	// ## Fact 1 — the pending host `tick` is dropped
	if want.tickDrop && log.indexOf("tick-dropped") < 0 {
		t.Errorf("the pending host tick was NOT dropped; the committed reading records TICKDROP %v. "+
			"events: %v", want.tickDrop, events)
	}

	// ## Fact 2 — the receipt arrives, so a cancelled task MAY call an import
	//
	// This was pre-registered on #857 with both outcomes fixed in advance, and #862 settled it as "yes".
	// Burroughs agreeing is the parity claim; Burroughs disagreeing would be a finding, not a bug to
	// paper over, which is why the failure message says which reading it contradicts.
	if log.indexOf("receipt") < 0 {
		t.Errorf("no receipt arrived; the committed reading records it observed, so either a cancelled "+
			"task cannot call an import here (a divergence from the reference) or the guest's drop glue "+
			"never ran. events: %v", events)
	}

	// ## Fact 1b — the ORDER: the host call is torn down BEFORE the guest's cancellation code runs
	//
	// The order is part of the finding and not a detail of it, by #862's own words. Checked as a
	// comparison of positions rather than as two separate presence assertions, because presence is
	// exactly what an order-reversed engine would also satisfy.
	drop, receipt := log.indexOf("tick-dropped"), log.indexOf("receipt")
	if drop >= 0 && receipt >= 0 && drop > receipt {
		t.Errorf("the receipt arrived BEFORE the host call was dropped (%v); the committed reading's "+
			"EVENTS order is tick-dropped -> receipt, and the reverse is a different engine producing "+
			"the same two facts", events)
	}
}

// TestHostCancelsAYieldingLiftTask is the witness for the lift loop's **top-of-loop** cancellation check
// (definitions.py def:2129), and it exists because neutering that check left the whole component suite
// green — including `TestHostCancelsAStartedLiftTask` above.
//
// # Why the end-to-end witness could not speak for it
//
// That witness drives a `wit-bindgen` guest, which awaits an import and therefore returns **WAIT**. A
// WAIT's cancellation is delivered by the park's own check (def:789, inside `awaitEvent`), so the
// top-of-loop check is never the thing that delivers it. The engine had a check nothing covered — grave
// #885's shape one level up, and found the same way: by falsifying rather than by reading.
//
// # What this reaches that nothing else does
//
// A guest that **yields repeatedly and never parks**. The YIELD arm re-enters immediately, so
// `awaitEvent` is never entered. The top-of-loop check is then the only path a host cancellation has.
//
// The two witnesses therefore have **different subjects**, exactly as #885's pair does: one guards the
// park's check and one guards the loop's. Confirmed by neutering each separately — see the slice's report.
func TestHostCancelsAYieldingLiftTask(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	b, err := os.ReadFile("testdata/lift-cancel-yield-synth.wasm")
	if err != nil {
		t.Fatalf("the hand-authored yield fixture is missing: %v", err)
	}
	in, err := InstantiateWithHost(b, NewHost(io.Discard, io.Discard, nil))
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()

	type outcome struct {
		res []canon.Value
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, cErr := in.CallValues("run")
		done <- outcome{res, cErr}
	}()

	// **Poll on the real condition, not a timer.** This guest calls no host import, so there is no entry
	// hook to wait on — but `requestCancelAll`'s own refusals ARE the condition: it answers
	// `ErrCancelNotRunning` while no task is registered and `ErrTaskCancelUnbuilt` while the task is still
	// `liftInitial`. Retrying until it succeeds therefore waits for "a started task exists" without
	// asserting anything about how long that takes.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if cErr := in.w.async.requestCancelAll(); cErr == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no started lift task appeared within 5s; the yielding guest never got going")
		}
		time.Sleep(time.Millisecond)
	}

	var got outcome
	select {
	case got = <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("the yielding guest's call never returned — with the top-of-loop check gone it cannot " +
			"be cancelled at all, and it never parks, so nothing else can deliver the cancellation")
	}

	if !errors.Is(got.err, ErrCancelled) {
		// The fixture traps `unreachable` two ways on purpose: an unexpected event code, and the 200000
		// spin bound. Both are more informative than a hang, which is what this would otherwise be.
		t.Fatalf("the cancelled yielding call returned (%v, %v); want ErrCancelled. A trap here means "+
			"either the engine delivered the wrong event code or the spin bound tripped because no "+
			"cancellation arrived", got.res, got.err)
	}
	if len(got.res) != 0 {
		t.Errorf("a cancelled call returned %d value(s); a cancelled task resolves with no result", len(got.res))
	}
}

// waitForPark reports whether some waitable set in this instance has a waiter, which is the observable
// fact "a lift task is parked in `awaitEvent`".
//
// It reads `waiting`, the counter `awaitEvent` increments on entry and decrements on exit — the same
// counter `waitable-set.drop` traps on. Using the engine's own state rather than a timer is what makes
// the caller's claim about WHICH cancellation path it exercises true rather than probable.
func waitForPark(t *testing.T, in *Instantiated, bound time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(bound)
	for time.Now().Before(deadline) {
		in.w.async.mu.Lock()
		parked := false
		for _, e := range in.w.async.entries {
			if s, ok := e.(*waitableSet); ok && s.waiting > 0 {
				parked = true
				break
			}
		}
		in.w.async.mu.Unlock()
		if parked {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
}

// cancelReading is the subset of a committed `.reading` this witness asserts against.
type cancelReading struct {
	tickDrop    bool
	receiptCode uint32
	status      uint32
}

// readCancelReading parses a committed reading rather than letting the test restate its values.
//
// The reason is the one `wantInline` was built for in #870: a literal that duplicates a committed
// artefact's content is correct exactly once, and after that it is a second source of truth that no
// instrument reconciles. Here it is sharper than usual, because the artefact IS the reference side of the
// parity claim — a stale literal would let this test report agreement with a reading it had stopped
// reading.
func readCancelReading(t *testing.T, path string) cancelReading {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the committed reading is missing: %v", err)
	}
	out := cancelReading{receiptCode: 0, status: 0}
	var sawTickDrop, sawReceipt, sawStatus bool
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "TICKDROP":
			out.tickDrop = fields[1] == "true"
			sawTickDrop = true
		case "RECEIPT":
			// `RECEIPT   observed, code=1`
			for _, f := range fields[1:] {
				if after, ok := strings.CutPrefix(strings.TrimSuffix(f, ","), "code="); ok {
					out.receiptCode = parseU32(t, after)
					sawReceipt = true
				}
			}
		case "STATUS":
			out.status = parseU32(t, fields[1])
			sawStatus = true
		}
	}
	// **A parser that silently found nothing is the failure mode this guards.** Every field below is one
	// this witness asserts against; a reading whose format drifted would otherwise hand back a zero value
	// and the assertions would compare against it without complaint — a control passing while checking
	// nothing.
	if !sawTickDrop || !sawReceipt || !sawStatus {
		t.Fatalf("%s did not yield all three fields (tickdrop=%v receipt=%v status=%v); the reading's "+
			"format moved and this witness would otherwise assert against zero values",
			path, sawTickDrop, sawReceipt, sawStatus)
	}
	return out
}

func parseU32(t *testing.T, s string) uint32 {
	t.Helper()
	var v uint32
	for _, r := range s {
		if r < '0' || r > '9' {
			t.Fatalf("not a number: %q", s)
		}
		v = v*10 + uint32(r-'0')
	}
	return v
}
