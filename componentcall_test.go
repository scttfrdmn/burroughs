// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package burroughs_test

import (
	"context"
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/scttfrdmn/burroughs"
)

// The component surface from an embedder's side (#858, ADR 0085 + its amendment 1, stamped by Scott).
//
// # Why these are in `burroughs_test` and not `burroughs`
//
// Six of this package's test files are the internal `package burroughs`, which can reach unexported
// identifiers. These cannot, and that is the point: the claim this slice adds is not "the mechanism
// works" — the engine's own tests established that against wasmtime's committed readings — but **"the
// mechanism is reachable from outside the module."** Only a test that crosses the public boundary can
// make that claim, because *an instrument's domain is an assertion it cannot check about itself*, which
// is the reasoning ADR 0029 used to put the public path under its own vectors.
//
// So if any of these needed an unexported field or an internal import, the surface would be incomplete —
// the compiler is the assertion.

// TestAnEmbedderCallsAComponentExportAndReadsAU32 is the positive path: load, call, read a value.
//
// `async-lift-exit-synth.wasm` is used because it has **no host imports at all** — a minimal async lift
// whose callee calls `task.return(42)` and returns EXIT. That matters here beyond convenience: this
// release ships no way for an embedder to supply a host function, so a fixture needing one could not be
// driven through this surface at all, and a test that reached into the engine to supply it would stop
// being an embedder's-eye test.
func TestAnEmbedderCallsAComponentExportAndReadsAU32(t *testing.T) {
	t.Setenv("BURROUGHS_ASYNC", "1")
	wasm, err := os.ReadFile("internal/component/testdata/async-lift-exit-synth.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}

	c, err := burroughs.LoadComponent(wasm)
	if err != nil {
		t.Fatalf("LoadComponent: %v", err)
	}

	res, err := c.Call(context.Background(), "run")
	if err != nil {
		t.Fatalf("Call(run): %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("Call(run) returned %d value(s), want 1", len(res))
	}
	got, ok := res[0].U32()
	if !ok {
		t.Fatalf("the result is %v, not a u32 — the accessor refuses a mis-typed read rather than "+
			"reinterpreting bits, so this means the kind crossed wrong", res[0])
	}
	if got != 42 {
		t.Errorf("Call(run) = %d, want 42 — the value the guest's own task.return lowered", got)
	}
}

// TestCancellingAnEmbeddersContextReturnsErrCancelled is **the deliverable** of this slice: the stamped
// cancellation contract, observed with `errors.Is` from outside the module.
//
// # Why the yielding fixture
//
// `lift-cancel-yield-synth.wasm` yields forever and imports nothing, so the only thing that can end this
// call is the context. A fixture that completed on its own would let this pass against a surface that
// ignored ctx entirely, and one that parked on a host import could not be driven here at all — no host
// function hook is public.
//
// It also **bounds its own spin** at 200000 re-entries, trapping past that, so a surface that ignored the
// context fails loudly rather than hanging.
func TestCancellingAnEmbeddersContextReturnsErrCancelled(t *testing.T) {
	t.Setenv("BURROUGHS_ASYNC", "1")
	wasm, err := os.ReadFile("internal/component/testdata/lift-cancel-yield-synth.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}

	c, err := burroughs.LoadComponent(wasm)
	if err != nil {
		t.Fatalf("LoadComponent: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	type outcome struct {
		res []burroughs.ComponentValue
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, cErr := c.Call(ctx, "run")
		done <- outcome{res, cErr}
	}()

	// The guest spins with no host import, so there is no entry hook to wait on. Cancelling after a short
	// delay is sound here in a way it would not be as a *timing* assertion: the fixture cannot finish on
	// its own, so whenever the cancellation lands it is the only thing that can end the call. Too early
	// is also fine — the task's own start honours an already-cancelled context.
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	var got outcome
	select {
	case got = <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("cancelling the context did not end the call. The stamped contract is that cancelling " +
			"ctx cancels the running task and the call returns ErrCancelled")
	}

	if !errors.Is(got.err, burroughs.ErrCancelled) {
		t.Fatalf("the cancelled call returned (%v, %v); want ErrCancelled, matched with errors.Is — "+
			"which is the form the stamp specifies, so a distinct error type later must keep answering it",
			got.res, got.err)
	}
	if len(got.res) != 0 {
		t.Errorf("a cancelled call returned %d value(s); a cancelled task resolves with no result",
			len(got.res))
	}
}

// TestTheBoundaryRefusesAKindThisReleaseDoesNotCarryByName pins the stamped first-merge scope from the
// outside: `u32` crosses, everything else refuses **by name**.
//
// The zero `ComponentValue` is the specimen available to an embedder today, because `ComponentU32` is the
// only constructor this release exports — so the refusal is exercised through the one kind an embedder can
// actually hold without one. That is not a weaker test than passing a string would be: the arm being
// checked is the conversion's refusal, and what makes it meaningful is that it **names** what it refused.
//
// A zero value is also the case most worth refusing. It is what a `var v burroughs.ComponentValue` or a
// missing struct field produces, and crossing it as `u32(0)` would hand the guest a number nobody chose.
func TestTheBoundaryRefusesAKindThisReleaseDoesNotCarryByName(t *testing.T) {
	t.Setenv("BURROUGHS_ASYNC", "1")
	wasm, err := os.ReadFile("internal/component/testdata/async-lift-exit-synth.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	c, err := burroughs.LoadComponent(wasm)
	if err != nil {
		t.Fatalf("LoadComponent: %v", err)
	}

	var unset burroughs.ComponentValue
	if k := unset.Kind(); k != burroughs.KindComponentNone {
		t.Fatalf("the zero ComponentValue has kind %v, want none — a value nobody constructed must not "+
			"claim a type", k)
	}

	_, callErr := c.Call(context.Background(), "run", unset)
	if callErr == nil {
		t.Fatal("a ComponentValue with no kind crossed the boundary; the zero value names no WIT type " +
			"and must be refused rather than lowered as u32(0)")
	}
	if !errors.Is(callErr, burroughs.ErrUnsupported) {
		t.Errorf("refused, but not as ErrUnsupported: %v", callErr)
	}
	// **By name**: the message must say which argument and that the kind was the problem, or an embedder
	// debugging a multi-argument call learns only that something was wrong.
	if msg := callErr.Error(); !strings.Contains(msg, "argument 0") {
		t.Errorf("the refusal does not say which argument: %v", callErr)
	}
}

// TestNamingAnExportFollowsTheStampedGrammar pins the naming rules from outside: a bare name addresses a
// top-level function, and an unknown name refuses rather than being resolved by a search.
//
// The `interface#function` form has no committed fixture that exports an interface-nested function and
// imports nothing, so what is asserted here is the half that is reachable: **a bare name is not searched
// for.** That is the rule with teeth — a search picks one silently when two interfaces share a function
// name — and its observable consequence is that a name which is not a top-level export is refused rather
// than found somewhere.
func TestNamingAnExportFollowsTheStampedGrammar(t *testing.T) {
	t.Setenv("BURROUGHS_ASYNC", "1")
	wasm, err := os.ReadFile("internal/component/testdata/async-lift-exit-synth.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	c, err := burroughs.LoadComponent(wasm)
	if err != nil {
		t.Fatalf("LoadComponent: %v", err)
	}

	// The bare name resolves, which is the baseline this arm's negative needs.
	if _, err := c.Call(context.Background(), "run"); err != nil {
		t.Fatalf("the bare top-level name did not resolve: %v", err)
	}

	// A name that is not a top-level export is refused. If bare names were resolved by searching the
	// exported interfaces, a name appearing inside one would be found here.
	if _, err := c.Call(context.Background(), "definitely-not-an-export"); err == nil {
		t.Error("an unknown export name succeeded, so names are being resolved more loosely than the " +
			"grammar says")
	}
}

// TestCloseReturnsErrClosedToLaterCalls is `Close`'s third stated behaviour, from the embedder's side.
func TestCloseReturnsErrClosedToLaterCalls(t *testing.T) {
	t.Setenv("BURROUGHS_ASYNC", "1")
	wasm, err := os.ReadFile("internal/component/testdata/async-lift-exit-synth.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	c, err := burroughs.LoadComponent(wasm)
	if err != nil {
		t.Fatalf("LoadComponent: %v", err)
	}

	// It works before.
	if _, err := c.Call(context.Background(), "run"); err != nil {
		t.Fatalf("Call before Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close on an idle component: %v, want a clean close", err)
	}

	_, callErr := c.Call(context.Background(), "run")
	if !errors.Is(callErr, burroughs.ErrComponentClosed) {
		t.Fatalf("Call after Close returned %v; want ErrComponentClosed — a refusal from deeper in the "+
			"engine about an instance that no longer exists tells the embedder about the wrong thing",
			callErr)
	}
	// The name is in the message, so a multi-export embedder learns which call did not happen.
	if !strings.Contains(callErr.Error(), "run") {
		t.Errorf("the refusal does not name the call: %v", callErr)
	}

	// Idempotent: an embedder who defers Close and also calls it on an error path should not have to
	// track which ran.
	if err := c.Close(); err != nil {
		t.Errorf("the second Close returned %v; want nil", err)
	}
}

// TestCloseCancelsAnInFlightCall is `Close`'s first stated behaviour: callers of in-flight calls get
// [burroughs.ErrCancelled], from tasks that actually ended.
//
// The yielding fixture is used because it **imports nothing** — this release ships no host-function hook,
// so a fixture parked in a host import cannot be driven from out here at all. The guest's *receipt* — the
// proof that its own cancellation path ran rather than the task being abandoned — is therefore witnessed
// one level down, in `internal/component`, where a host import can be supplied. Split by where each claim
// is observable, not by preference.
func TestCloseCancelsAnInFlightCall(t *testing.T) {
	t.Setenv("BURROUGHS_ASYNC", "1")
	wasm, err := os.ReadFile("internal/component/testdata/lift-cancel-yield-synth.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	c, err := burroughs.LoadComponent(wasm)
	if err != nil {
		t.Fatalf("LoadComponent: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, cErr := c.Call(context.Background(), "run")
		done <- cErr
	}()

	// The guest spins with no host import, so there is no entry hook; a short delay then Close. Sound
	// because the fixture cannot finish on its own — whenever Close lands, it is the only thing that can
	// end the call — and its own 200000 spin bound makes an ignored cancellation fail loudly.
	time.Sleep(20 * time.Millisecond)

	closeErr := c.Close()

	select {
	case cErr := <-done:
		if !errors.Is(cErr, burroughs.ErrCancelled) {
			t.Fatalf("the in-flight call returned %v; want ErrCancelled. Close must CANCEL before it "+
				"tears down, or the caller gets a terminated-call error from a task whose own "+
				"cancellation path never ran", cErr)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the in-flight call never returned after Close")
	}
	if closeErr != nil {
		t.Errorf("Close returned %v; the guest cancels promptly, so this should be a clean close",
			closeErr)
	}
}

// TestCloseReturnsTheGoroutineCountToWhereItStarted is the leak check: a component's threads and tasks are
// released, not merely forgotten.
//
// # Why a settling allowance and not an exact match
//
// `runtime.NumGoroutine()` counts the whole process, and Go's own runtime goroutines come and go. So the
// assertion is that the count returns to its baseline **within a bounded settle**, polled on the real
// condition rather than slept at — which is the honest form: an exact instantaneous match would be a
// sample of the runtime's own scheduling, and *a witness whose subject depends on scheduling is a sample*
// (grave #891).
//
// A leak fails it anyway, because a leaked engine goroutine never goes away: the poll runs out.
func TestCloseReturnsTheGoroutineCountToWhereItStarted(t *testing.T) {
	t.Setenv("BURROUGHS_ASYNC", "1")
	wasm, err := os.ReadFile("internal/component/testdata/lift-cancel-yield-synth.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}

	// Baseline taken after a settle of its own, so a goroutine left by an earlier test in this package is
	// not counted against this one.
	settle(t, runtime.NumGoroutine(), 2*time.Second)
	base := runtime.NumGoroutine()

	c, err := burroughs.LoadComponent(wasm)
	if err != nil {
		t.Fatalf("LoadComponent: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, cErr := c.Call(context.Background(), "run")
		done <- cErr
	}()
	time.Sleep(20 * time.Millisecond)
	if n := runtime.NumGoroutine(); n <= base {
		t.Logf("goroutines did not rise above the baseline (%d vs %d); the fixture may have finished "+
			"before the measurement, which weakens this arm but does not invalidate it", n, base)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	<-done

	if !settle(t, base, 5*time.Second) {
		t.Errorf("goroutines did not return to the baseline %d within 5s (now %d) — Close released the "+
			"instance's state but something it started is still running", base, runtime.NumGoroutine())
	}
}

// settle polls until the goroutine count is at or below want, and reports whether it got there. A poll on
// a real condition rather than a sleep, so a fast machine does not wait and a slow one is not failed.
func settle(t *testing.T, want int, bound time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(bound)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= want {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return runtime.NumGoroutine() <= want
}
