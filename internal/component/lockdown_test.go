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

	bin "github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/component/canon"
	"github.com/scttfrdmn/burroughs/internal/interp"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// The store lockdown's witnesses (#1040; ADR 0105). The oracles, read before this was built and recorded on
// #1040:
//   - wasmtime 49.0.3 refuses a second call after a trap: "cannot enter component instance";
//   - the pin locks the store on every trap (def:trap:"current_instance().store.lock_down()") and refuses the
//     next call (def:Store.invoke:"assert(not self.lockdown)").
//
// NEUTERS, run:
//   - the lock never set (`storeLock.set` returning at once): `trap-then-call`'s call 2 returns 7, and every
//     arm that asserts a refusal fails;
//   - the entry check removed (`CallValuesCtx` and `Call` not consulting `locked()` before the call): call 2
//     returns 7 again, because a call that starts after the lock is mapped by nothing else;
//   - the lock scoped to one instance (`shareLock` not recursing into children): the composed arm fails, because
//     the child's task never sees the lock and its lift stays in flight.

// wantLockedBy asserts err is a lockdown refusal wrapping first, the error the trapping call returned, and that
// the wrapped trap's reason names `reason`.
func wantLockedBy(t *testing.T, what string, err, first error, reason string) {
	t.Helper()
	if !errors.Is(err, ErrInstanceLocked) {
		t.Fatalf("%s: got %v; want ErrInstanceLocked (wasmtime: \"cannot enter component instance\"; the pin: "+
			"Store.invoke asserts not lockdown)", what, err)
	}
	if !errors.Is(err, first) {
		t.Errorf("%s: the refusal does not wrap the trapping call's own error %v", what, first)
	}
	var trap *interp.Trap
	if !errors.As(err, &trap) || !strings.Contains(trap.Reason, reason) {
		t.Errorf("%s: errors.As does not reach a trap naming %q: %v", what, reason, err)
	}
}

func TestACallAfterATrapIsRefusedWithTheTrap(t *testing.T) {
	for _, fx := range []struct{ name, file, reason string }{
		{"in_a_thread", "testdata/cmthreads/trap-then-call.wasm", "unreachable"},
		{"with_no_threads", "testdata/trap-then-call-plain.wasm", "unreachable"},
	} {
		t.Run(fx.name, func(t *testing.T) {
			t.Setenv(cmThreadsGateEnv, "1")
			t.Setenv(asyncGateEnv, "1")
			b, err := os.ReadFile(fx.file)
			if err != nil {
				t.Fatalf("the committed fixture is missing: %v", err)
			}
			in, err := Instantiate(b)
			if err != nil {
				t.Fatalf("instantiate: %v", err)
			}
			defer in.Close()
			_, first := in.CallValues("run")
			var trap *interp.Trap
			if !errors.As(first, &trap) || errors.Is(first, ErrInstanceLocked) {
				t.Fatalf("call 1: got %v; want its own trap, not a lockdown refusal (wasmtime's call 1)", first)
			}
			res, err := in.CallValues("ok")
			if err == nil {
				v, _ := res[0].U32()
				t.Fatalf("call 2 returned %d after call 1 trapped; the pin and wasmtime both refuse it", v)
			}
			wantLockedBy(t, "call 2", err, first, fx.reason)
			// And it stays locked: the lock is not consumed by the first refusal.
			if _, again := in.CallValues("ok"); !errors.Is(again, ErrInstanceLocked) {
				t.Errorf("call 3: got %v; want ErrInstanceLocked still", again)
			}
		})
	}
}

// TestTheLockdownControlRunsBothCalls is trap-then-call with T's `unreachable` removed: no trap, so nothing locks
// (the pin: 1, then 7; wasmtime 49.0.3: the same).
func TestTheLockdownControlRunsBothCalls(t *testing.T) {
	_, res, err := callBounded(t, "trap-then-call-control", "run")
	wantU32(t, "trap-then-call-control", res, err, 1)
	b, _ := os.ReadFile("testdata/cmthreads/trap-then-call-control.wasm")
	in, err := Instantiate(b)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()
	if _, err = in.CallValues("run"); err != nil {
		t.Fatalf("call 1: %v", err)
	}
	res2, err := in.CallValues("ok")
	wantU32(t, "trap-then-call-control call 2", res2, err, 7)
}

// TestALockdownEndsATaskInAComposedInstance: a trap in the parent ends a task running in a child instance at its
// next canon built-in. The child's async `g` calls `context.get` on every turn and never finishes on its own.
func TestALockdownEndsATaskInAComposedInstance(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	b, err := os.ReadFile("testdata/lockdown-composed.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	in, err := Instantiate(b)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()
	if len(in.w.children) != 1 || in.w.children[0].async == nil {
		t.Fatalf("the fixture composes %d child instances, want 1 with async state", len(in.w.children))
	}
	child := in.w.children[0].async
	inFlight := func() int {
		child.mu.Lock()
		defer child.mu.Unlock()
		return child.liftsInFlight
	}

	goDone := make(chan error, 1)
	go func() { _, e := in.CallValues("go"); goDone <- e }()
	// The child's task must be running before the trap, or the arm asserts nothing about ending one.
	deadline := time.Now().Add(10 * time.Second)
	for inFlight() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the child's g never started, so there is no running task for the lockdown to end")
		}
		time.Sleep(time.Millisecond)
	}

	_, first := in.CallValues("boom")
	var trap *interp.Trap
	if !errors.As(first, &trap) {
		t.Fatalf("boom: got %v; want its trap", first)
	}
	select {
	case e := <-goDone:
		wantLockedBy(t, "go", e, first, "unreachable")
	case <-time.After(10 * time.Second):
		t.Fatal("go did not end after boom locked the store")
	}
	// The child's own task, not only the parent's park: with the lock scoped to the parent, `go` ends at its
	// park and `g` yields on forever.
	deadline = time.Now().Add(10 * time.Second)
	for inFlight() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("the child's g is still in flight (%d) after the lockdown: its next context.get was not "+
				"refused, so the lock did not reach the composed instance", inFlight())
		}
		time.Sleep(time.Millisecond)
	}
}

// TestACoreModuleStaysCallableAfterATrap: the lockdown is the component model's, and core wasm keeps the core spec's
// semantics, under which an instance stays usable after a trap (assert_trap followed by further use).
func TestACoreModuleStaysCallableAfterATrap(t *testing.T) {
	img, err := text.EncodeModule([]byte(`(module
		(func (export "boom") unreachable)
		(func (export "ok") (result i32) (i32.const 7)))`))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	m, err := bin.DecodeModule(img)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	in, trap, err := interp.Instantiate(m)
	if err != nil || trap != nil {
		t.Fatalf("instantiate: %v %v", err, trap)
	}
	defer func() { _ = in.Close() }()
	if _, err = in.Invoke("boom"); err == nil {
		t.Fatal("boom did not trap")
	}
	got, err := in.Invoke("ok")
	if err != nil || got[0].Int32() != 7 {
		t.Fatalf("ok after a trap: got %v, %v; want 7 — a core instance stays callable", got, err)
	}
}

// TestAHostCallInFlightEndsWithTheLockdown: a host import blocked on another goroutine when a different call traps
// returns into a locked store, and its call ends with the wrapped error rather than the import's value.
func TestAHostCallInFlightEndsWithTheLockdown(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	b, err := os.ReadFile("testdata/asynclift/suspending/component.wasm")
	if err != nil {
		t.Fatalf("the committed suspending guest is missing: %v", err)
	}
	h := NewHost(io.Discard, io.Discard, nil)
	entered := make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	var resolve func(canon.Value)
	h.asyncImpls = map[string]asyncLowerImpl{
		"tick": func(_ *interp.CanonCaller, onStart func() []interp.Value, onResolve func(canon.Value)) (func(), error) {
			onStart()
			mu.Lock()
			resolve = onResolve
			mu.Unlock()
			once.Do(func() { close(entered) })
			return func() {}, nil
		},
	}
	in, err := InstantiateWithHost(b, h)
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer in.Close()

	done := make(chan error, 1)
	go func() { _, e := in.CallValues("run", canon.U32(3)); done <- e }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("run never reached tick")
	}
	// **The lock is set directly, standing in for a sibling call's trap**, because the suspending guest has no
	// trapping export. What this arm tests is the guard's check AFTER the host call returns. How a real trap sets
	// the lock is the subject of the arms above.
	boom := &interp.Trap{Reason: "unreachable (a sibling call's trap)"}
	in.w.lock.set(boom)
	mu.Lock()
	r := resolve
	mu.Unlock()
	r(canon.U32(3)) // the host call completes, into a locked store
	select {
	case e := <-done:
		wantLockedBy(t, "run", e, boom, "sibling call's trap")
	case <-time.After(10 * time.Second):
		t.Fatal("run did not end after its host call returned into a locked store")
	}
}
