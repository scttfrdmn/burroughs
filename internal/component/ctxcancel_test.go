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
