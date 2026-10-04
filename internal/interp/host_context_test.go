// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"context"
	"testing"
)

// TestContextStorageIsPerCallerStack is the placement guard for gate:async context.get/set (increment 4):
// context storage lives on the caller's own stack, so two agents (two stacks) have independent slots, and a
// fresh stack starts zeroed. This is the property no single-agent guest run can witness — a per-instance or
// per-thread-object store would pass a one-agent test and fail here.
func TestContextStorageIsPerCallerStack(t *testing.T) {
	newCaller := func() *CanonCaller {
		return newCanonCaller(context.Background(), 0, nil, nil, nil, nil, &stack{}, 0)
	}

	// Two agents = two stacks: writing one's slot 0 does not touch the other's, and each reads its own.
	a, b := newCaller(), newCaller()
	if err := a.ContextSet(0, 1); err != nil {
		t.Fatalf("a.ContextSet: %v", err)
	}
	if err := b.ContextSet(0, 2); err != nil {
		t.Fatalf("b.ContextSet: %v", err)
	}
	if av, err := a.ContextGet(0); err != nil || av != 1 {
		t.Errorf("a.ContextGet(0) = %d (err %v), want 1 — agent A's slot was clobbered (a shared store)", av, err)
	}
	if bv, err := b.ContextGet(0); err != nil || bv != 2 {
		t.Errorf("b.ContextGet(0) = %d (err %v), want 2 — agent B saw A's value (a shared store)", bv, err)
	}

	// An unset slot reads 0 (the stack's zero value, the model's [0,0]); distinct slots are independent.
	if v, err := a.ContextGet(1); err != nil || v != 0 {
		t.Errorf("a.ContextGet(1) = %d (err %v), want 0 (unset slot)", v, err)
	}

	// A FRESH stack starts zeroed — the guard on the per-Invoke-allocation property (if stacks were ever
	// pooled, this would return A's leftover 1 instead of 0, and this test would fail rather than context
	// leaking silently into a sequential Invoke).
	fresh := newCaller()
	if v, err := fresh.ContextGet(0); err != nil || v != 0 {
		t.Errorf("fresh stack ContextGet(0) = %d (err %v), want 0 — a reused/pooled stack leaked prior context", v, err)
	}

	// Out-of-range slot traps (the model's assert i < len).
	if _, err := a.ContextGet(2); err == nil {
		t.Error("ContextGet(2) did not trap — out-of-range slot must trap")
	}
	if err := a.ContextSet(2, 9); err == nil {
		t.Error("ContextSet(2) did not trap — out-of-range slot must trap")
	}
}
