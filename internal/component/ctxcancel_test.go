// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"context"
	"errors"
	"io"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// TestCancellingTheCallsContextCancelsTheTask is #880's park half and the mechanism ADR 0085 amendment 1's
// `Component.Call(ctx, …)` sits on: cancelling the call's context **cancels the running task**, and the
// call returns `ErrCancelled`.
//
// # It requests a cancellation; it does not abort the park
//
// The distinction is the whole design. Aborting the park would return an error from a task the guest still
// believes is running — destructors unrun, resources held, and the engine's own view of the task
// disagreeing with the guest's. Requesting takes ADR 0094's path: PENDING_CANCEL, then `TASK_CANCELLED`
// delivered at the top of the lift loop, so the guest runs its own cancellation and resolves. The
// `ErrCancelled` the caller sees is therefore from a task that **actually ended**.
//
// The observable difference is the receipt: `receipt/`'s guest calls `note` from a `Drop` guard local to
// its async body, so a receipt arriving proves the guest's own cancellation path ran. An aborted park
// would produce `ErrCancelled` with **no** receipt — which is why this asserts both and not just the error.
//
// # The host's `tick` never resolves, on purpose
//
// There is nothing for the task to wait for except the cancellation, so the only thing that can end this
// call is the context. A `tick` that resolved would let the test pass against an engine that ignored the
// context entirely and simply completed.
func TestCancellingTheCallsContextCancelsTheTask(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	b, err := os.ReadFile("testdata/asynclift/receipt/component.wasm")
	if err != nil {
		t.Fatalf("the receipt guest is missing: %v", err)
	}

	entered := make(chan struct{}, 1)
	var receipts int32
	h := NewHost(io.Discard, io.Discard, nil)
	h.asyncImpls = map[string]asyncLowerImpl{
		"tick": func(_ *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
			onStart()
			select {
			case entered <- struct{}{}:
			default:
			}
			// **Never resolves on its own** — the context is the only thing that can end this call. But
			// it DOES resolve when asked to cancel, which is the well-behaved shape for a host dropping
			// a pending call.
			//
			// A first version returned an inert `func() {}` here and the test failed after 20s. That was
			// the engine being right about a misbehaving host: the guest's cancellation path drops the
			// `tick` future, which issues a sync `subtask.cancel`, which waits for the resolution this
			// host never produced — grave #892's bound, firing at exactly the case it exists for. The
			// test was asserting that the context ends the call while supplying a host that cannot let
			// it.
			return func() { onResolve(canon.U32(0)) }, nil
		},
	}
	h.syncImpls = map[string]interp.CanonFunc{
		"note": func(_ *interp.CanonCaller, _ []interp.Value) ([]interp.Value, error) {
			atomic.AddInt32(&receipts, 1)
			return nil, nil
		},
	}

	in, err := InstantiateWithHost(b, h)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type outcome struct {
		res []canon.Value
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, cErr := in.CallValuesCtx(ctx, "run", canon.U32(1))
		done <- outcome{res, cErr}
	}()

	// Wait until the guest is demonstrably parked, so the cancellation has a park to reach rather than
	// racing the call's start — the same soundness point #887's witness needed.
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the guest never reached `tick` within 5s; there is nothing parked to cancel")
	}
	if !waitForPark(t, in, 5*time.Second) {
		t.Fatal("the guest reached `tick` but never parked within 5s")
	}

	cancel()

	var got outcome
	select {
	case got = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("cancelling the context did not end the call within 10s. The park must select on the " +
			"call's `ctx.Done()` and request the task's cancellation (#880); without it the call runs " +
			"to `liftParkBound` — 30s — on a task nothing will ever resolve")
	}

	if !errors.Is(got.err, ErrCancelled) {
		t.Fatalf("the cancelled call returned (%v, %v); want ErrCancelled", got.res, got.err)
	}
	// **The receipt is what distinguishes a cancelled task from an abandoned park.** Without it the
	// error above would be satisfied by an engine that gave up on the wait and returned, leaving the
	// guest's destructors unrun.
	if atomic.LoadInt32(&receipts) == 0 {
		t.Error("no receipt arrived, so the guest's own cancellation path never ran — the park was " +
			"abandoned rather than the task cancelled, and `ErrCancelled` is then a claim about the " +
			"engine's bookkeeping rather than about the guest")
	}
}

// TestAnAlreadyCancelledContextDoesNotHangTheEntryWait is #880's **entry** half, which is the wait a
// lexical reading of "the park honours the context" misses.
//
// A lift task queued behind a sibling's entry waits on `enterTask`'s capacity-1 semaphore, and that wait
// was unresponsive for as long as the sibling held the slot — up to the sibling's whole execution. With
// one instance and one entry slot, a caller arriving with an already-cancelled context must not join the
// queue at all.
//
// # Why this is driven at unit level and not through a guest
//
// Producing the contended-entry case through two guests means winning a race on purpose: the sibling must
// hold the slot across the second caller's arrival. That is a timing setup, and *a witness whose subject
// depends on scheduling is a sample* (grave #891). Calling `enterTask` directly with a cancelled context
// asserts the same property with no race in it — the semaphore is a real semaphore either way.
func TestAnAlreadyCancelledContextDoesNotHangTheEntryWait(t *testing.T) {
	h := newAsyncHandles()

	// Occupy the single entry slot, so the next entry must wait.
	holder := &liftTask{state: liftStarted, ctx: context.Background(), cancelWake: make(chan struct{})}
	if _, ok := h.enterTask(holder); !ok {
		t.Fatal("the first entry was refused on an idle instance; the slot was never free")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already done before the attempt
	queued := &liftTask{state: liftStarted, ctx: ctx, cancelWake: make(chan struct{})}

	done := make(chan bool, 1)
	go func() {
		_, ok := h.enterTask(queued)
		done <- ok
	}()

	select {
	case ok := <-done:
		if ok {
			t.Error("the entry SUCCEEDED while the slot was held by another task — the semaphore is not " +
				"bounding entry, so this test's subject does not exist")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled context did not end the entry wait. `enterTask` must select on " +
			"`ctx.Done()` alongside the semaphore send (#880); without it a queued task waits for the " +
			"holder regardless of its caller having given up")
	}

	// The slot must still be the holder's: a refused entry acquires nothing, so `leaveTask` must not
	// run for it. An unpaired release here would admit a caller the semaphore never admitted.
	if len(h.entrySem) != 1 {
		t.Errorf("the entry semaphore holds %d, want 1 — a refused entry released a slot it never took",
			len(h.entrySem))
	}
}

// TestCancellingTheContextOfAYieldingGuestEndsIt is #880's **yield** gap, found on the #858 review.
//
// # The gap
//
// The context was consulted only at the two *waits* — the park (`awaitEvent`) and the entry semaphore
// (`enterTask`). A guest that YIELDS reaches neither: the YIELD arm re-enters the callback immediately,
// so nothing read `ctx.Done()` and **a spinning guest could not be cancelled through a context at all.**
// `Component.Call(ctx, …)` would have had no effect on it.
//
// This is the same shape as the top-of-loop check's own missing witness one slice earlier: the guest that
// exercises the yield path is `lift-cancel-yield-synth`, and a `wit-bindgen` guest always parks, so every
// other witness takes the WAIT route and cannot speak for this one.
//
// # What the fixture guarantees
//
// It yields forever and **bounds its own spin** at 200000 re-entries, trapping `unreachable` past that.
// So an engine that ignores the context fails loudly rather than hanging — which is what makes the
// neuter's failure fast and legible instead of a timeout.
//
// It accepts only event codes 0 (NONE) and 6 (TASK_CANCELLED), trapping on anything else, so "the engine
// delivered the right code" is checked inside the guest rather than inferred from the call ending.
func TestCancellingTheContextOfAYieldingGuestEndsIt(t *testing.T) {
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type outcome struct {
		res []canon.Value
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, cErr := in.CallValuesCtx(ctx, "run")
		done <- outcome{res, cErr}
	}()

	// Wait for the task to exist before cancelling, so the cancellation has a running task to reach
	// rather than racing the call's start. `requestCancelAll`'s own refusal is the condition — it answers
	// `ErrCancelNotRunning` until a task is registered — so this waits on engine state, not on a timer.
	deadline := time.Now().Add(5 * time.Second)
	for {
		in.w.async.mu.Lock()
		registered := len(in.w.async.cancellable) > 0
		in.w.async.mu.Unlock()
		if registered {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no lift task appeared within 5s; the yielding guest never got going")
		}
		time.Sleep(time.Millisecond)
	}

	cancel()

	var got outcome
	select {
	case got = <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("cancelling the context did not end a YIELDING guest's call. The top-of-loop check must " +
			"request the task's cancellation when the context is done: this guest never parks and never " +
			"waits for entry, so neither of the two waits the context used to reach is on its path")
	}

	if !errors.Is(got.err, ErrCancelled) {
		t.Fatalf("the cancelled yielding call returned (%v, %v); want ErrCancelled. A trap here means "+
			"either the engine delivered the wrong event code or the fixture's 200000 spin bound tripped "+
			"because the cancellation never arrived", got.res, got.err)
	}
	if len(got.res) != 0 {
		t.Errorf("a cancelled call returned %d value(s); a cancelled task resolves with no result", len(got.res))
	}
}

// TestCancellingTheOuterCallReachesTheComposedChild is the propagation witness, and it asserts propagation
// **by effect** rather than by route.
//
// # Why by effect
//
// The cross-component sites briefly passed `c.Context()` to the child with a comment claiming an embedder
// cancelling the outer call cancelled the chain. The claim was false — searched: `CanonCaller`'s context
// comes from `newCanonCaller(t.context(), …)`, and a thread's context is created in `world.addLocked` as
// `context.WithCancel(context.Background())`, cancelled by `Instance.Close` (ADR 0069). It is the
// thread's **lifetime** context, not the embedder's per-call one, which lives on the parent's `liftTask`
// and has no route to a canon closure.
//
// So the plumbing is gone and the claim is withdrawn. Cancellation still reaches the child, by the
// **model's own path**: the parent receives `TASK_CANCELLED`, its code issues `subtask.cancel`, and that
// reaches the child through ADR 0095's `onCancel`.
//
// This test is written so it holds whichever route carries it: cancel the outer call and require the
// child's **receipt** and **status 4**. A witness phrased in terms of the route would have passed while
// the route was imaginary, because the effect was real all along.
func TestCancellingTheOuterCallReachesTheComposedChild(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	want := readCancelReading(t, "testdata/asynclift/cancel-wat.reading")
	b, err := os.ReadFile("testdata/asynclift/cancel-wat-parent/composed.wasm")
	if err != nil {
		t.Fatalf("the committed composed artefact is missing: %v", err)
	}

	r := &crossCallReports{}
	var receipts int32
	entered := make(chan struct{}, 1)
	h := NewHost(io.Discard, io.Discard, nil)
	h.asyncImpls = map[string]asyncLowerImpl{
		"tick": func(_ *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
			onStart()
			select {
			case entered <- struct{}{}:
			default:
			}
			// Resolves when asked to cancel, which is the well-behaved host shape — a host whose
			// `onCancel` is inert makes the guest's drop glue wait out grave #892's bound instead.
			return func() { onResolve(canon.U32(0)) }, nil
		},
	}
	h.syncImpls = map[string]interp.CanonFunc{
		"note": func(_ *interp.CanonCaller, _ []interp.Value) ([]interp.Value, error) {
			atomic.AddInt32(&receipts, 1)
			return nil, nil
		},
		"report": func(_ *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
			if len(args) >= 2 {
				r.report(uint32(args[0].Bits), uint32(args[1].Bits))
			}
			return nil, nil
		},
	}

	in, err := InstantiateWithHost(b, h)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()

	// `cancel-wat-parent`'s `go` cancels the child itself, so the outer context is not what triggers the
	// cancellation here — what this asserts is that an outer call carrying a context still produces the
	// child's full cancellation, which is the property an embedder's `Component.Call(ctx, …)` needs.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, cErr := in.CallValuesCtx(ctx, "go")
		done <- cErr
	}()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the child never reached `tick` within 5s")
	}
	cancel()

	select {
	case cErr := <-done:
		if cErr != nil {
			t.Fatalf("the composed call failed: %v", cErr)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the composed call never completed")
	}

	reports, _ := r.snapshot()
	t.Logf("reports=%v receipts=%d", reports, atomic.LoadInt32(&receipts))

	// The child's own cancellation path ran.
	if atomic.LoadInt32(&receipts) == 0 {
		t.Error("no receipt arrived, so the child's cancellation path never ran — cancellation did not " +
			"reach the child by ANY route, which is the whole claim")
	}
	// And it reached the terminal state the committed reading records.
	status, ok := r.valueFor(2)
	if !ok {
		t.Fatal("the parent reported no cancel status (kind 2)")
	}
	if status != want.status {
		t.Errorf("cancel status = %d, want %d from the committed cancel-wat.reading", status, want.status)
	}
}
