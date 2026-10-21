// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package burroughs_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/scttfrdmn/burroughs"
)

// TestAComponentCallAfterATrapIsRefusedAtThePublicBoundary is #1040's lockdown seen from outside the module: after
// a call traps, the next Component.Call returns ErrInstanceLocked, wrapping the error the trapping call returned.
// The engine's witnesses (internal/component/lockdown_test.go) carry the oracles and the neuters; this one asserts
// only that the public sentinel is what an embedder matches on.
func TestAComponentCallAfterATrapIsRefusedAtThePublicBoundary(t *testing.T) {
	wasm, err := os.ReadFile("internal/component/testdata/trap-then-call-plain.wasm")
	if err != nil {
		t.Fatalf("the committed fixture is missing: %v", err)
	}
	c, err := burroughs.LoadComponent(wasm)
	if err != nil {
		t.Fatalf("LoadComponent: %v", err)
	}
	defer func() { _ = c.Close() }()

	_, first := c.Call(context.Background(), "run")
	if first == nil || errors.Is(first, burroughs.ErrInstanceLocked) {
		t.Fatalf("call 1: got %v; want its own trap", first)
	}
	res, err := c.Call(context.Background(), "ok")
	if err == nil {
		t.Fatalf("call 2 returned %v after call 1 trapped; want ErrInstanceLocked", res)
	}
	if !errors.Is(err, burroughs.ErrInstanceLocked) {
		t.Fatalf("call 2: got %v; want burroughs.ErrInstanceLocked", err)
	}
	if !errors.Is(err, first) {
		t.Errorf("call 2's refusal does not wrap call 1's error %v", first)
	}
	if errors.Is(err, burroughs.ErrComponentClosed) {
		t.Errorf("call 2 reported the component closed; it is locked, and Close is still owed")
	}

	// #1084: the trap is this package's *Trap, directly and through the lockdown, as a core module's trap is.
	// NEUTER, run: Component.Call without the publicTrap conversion fails both, because the engine's trap type
	// cannot be named from here.
	var trap *burroughs.Trap
	if !errors.As(first, &trap) || trap.Reason != "unreachable" {
		t.Errorf("call 1: errors.As(*burroughs.Trap) did not reach the component's trap: %v", first)
	}
	var locked *burroughs.Trap
	if !errors.As(err, &locked) || locked != trap {
		t.Errorf("call 2: errors.As(*burroughs.Trap) through ErrInstanceLocked did not reach call 1's trap, "+
			"the same value: %v", err)
	}
}
