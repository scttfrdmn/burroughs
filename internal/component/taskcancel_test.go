// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

// TestTaskCancelTrapsOnAnUncancelledTask is `task.cancel`'s (0x05) witness (#864).
//
// # Why a hand-authored fixture and not a committed guest
//
// `wit-bindgen` emits `task.cancel` only inside a `TaskCancelOnDrop` drop handler, which never runs absent
// a cancellation — so both committed Rust guests IMPORT the intrinsic and never call it. Nothing in this
// engine can cancel a task, and wasmtime cannot cancel one that has started either (dropping a host call
// future abandons it; see `testdata/asynclift/ABANDONMENT.md`). So no guest reachable today exercises the
// opcode, and `testdata/task-cancel-synth.wat` calls it immediately instead.
//
// # What it establishes, and what it deliberately does not
//
// It establishes that the opcode is **bound and its uncancelled branch fires by name** — the Canonical
// ABI's rule, which wasmtime spells `TaskCancelNotCancelled`. It says nothing about cancellation
// semantics, because nothing can set `liftTask.cancelled`; that branch refuses with
// `ErrTaskCancelUnbuilt`, pointing at #862, rather than running semantics nobody has measured.
func TestTaskCancelTrapsOnAnUncancelledTask(t *testing.T) {
	t.Setenv(asyncGateEnv, "1")
	b, err := os.ReadFile("testdata/task-cancel-synth.wasm")
	if err != nil {
		t.Fatalf("the hand-authored fixture is missing: %v", err)
	}
	h := NewHost(io.Discard, io.Discard, nil)
	in, err := InstantiateWithHost(b, h)
	if err != nil {
		// Instantiate must SUCCEED: 0x05 being bound is half of what this witness is for, and a refusal
		// here would hide the trap behind a binding failure.
		t.Fatalf("instantiate refused a component whose only async built-in is task.cancel: %v", err)
	}
	defer in.Close()

	err = in.Call("run")
	if err == nil {
		t.Fatal("run() returned successfully; task.cancel on an uncancelled task must trap (the " +
			"Canonical ABI's rule, wasmtime's TaskCancelNotCancelled)")
	}
	// The trap must NAME the rule. A bare trap would satisfy `err != nil` while leaving a reader unable to
	// tell this from any other failure in the lift loop — and the loop has several.
	if !strings.Contains(err.Error(), "has not been cancelled") {
		t.Errorf("trapped, but not with the named rule: %v", err)
	}
	t.Logf("task.cancel on an uncancelled task: %v", err)
}

// TestTaskCancelsCancelledBranchIsRefusedByName pins the branch that is NOT implemented.
//
// `liftTask.cancelled` has no writer in this engine, so the branch is unreachable from any guest. It is
// still asserted, directly on the task, because **an unimplemented branch that returns something
// plausible is worse than one that refuses**: #862 will set that flag, and if the branch had silently
// fallen through to the trap, a cancelled task would have reported "not cancelled" — the opposite of the
// truth, in the slice that first makes it reachable.
func TestTaskCancelsCancelledBranchIsRefusedByName(t *testing.T) {
	h := newAsyncHandles()
	task := &liftTask{cancelled: true}
	h.mu.Lock()
	h.lift = task
	h.mu.Unlock()

	_, err := taskCancel(h)(nil, nil)
	if err == nil {
		t.Fatal("task.cancel on a CANCELLED task returned no error; the branch is unimplemented and must " +
			"refuse by name rather than fall through to the uncancelled trap")
	}
	if !errors.Is(err, ErrTaskCancelUnbuilt) {
		t.Errorf("refused, but not with ErrTaskCancelUnbuilt: %v", err)
	}
	// The refusal must point at the slice that will build it, so a reader meeting it knows where to look.
	if !strings.Contains(err.Error(), "#862") {
		t.Errorf("the refusal does not name the slice that will implement it: %v", err)
	}
}
