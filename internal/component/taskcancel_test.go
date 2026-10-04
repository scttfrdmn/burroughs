// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/interp"
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
// # What it establishes
//
// That the opcode is **bound and its uncancelled branch fires by name** — the Canonical ABI's rule, which
// wasmtime spells `TaskCancelNotCancelled`.
//
// This comment used to continue *"It says nothing about cancellation semantics, because nothing can set
// `liftTask.cancelled`"*, and ADR 0094 falsified that: the cancelled branch is built, the state is now
// `liftCancelDelivered`, and `TestTaskCancelResolvesADeliveredCancellation` below is its witness. The
// trap arm is unchanged — what narrowed is its reach, from every reachable call to one of two outcomes.
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

// TestTaskCancelResolvesADeliveredCancellation replaces `TestTaskCancelsCancelledBranchIsRefusedByName`,
// which **no longer exists**: it asserted that this branch was *unimplemented* and refused by name with
// `ErrTaskCancelUnbuilt` pointing at #862. ADR 0094 built the branch, so that test's subject is gone — it
// is replaced rather than deleted, and this note is why a reader looking for the old name finds this.
//
// Its reason is discharged on its own terms: it existed because *an unimplemented branch that returns
// something plausible is worse than one that refuses*, and the branch returning the right thing is what
// retires that.
//
// # The precondition is CANCEL_DELIVERED and not "cancelled somehow", which is the discriminating arm
//
// `Task.cancel` traps unless the state is CANCEL_DELIVERED (definitions.py def:494). So a guest calling
// `task.cancel` on a cancellation that has been *requested* but not *handed to it* is as wrong as one
// calling it with no cancellation at all. The two-state arm below is what makes `liftState`'s separation
// of PENDING_CANCEL from CANCEL_DELIVERED load-bearing rather than decorative: collapse them and the
// pending arm silently succeeds.
func TestTaskCancelResolvesADeliveredCancellation(t *testing.T) {
	t.Run("delivered_resolves", func(t *testing.T) {
		h := newAsyncHandles()
		task := &liftTask{state: liftCancelDelivered}
		h.mu.Lock()
		h.lift = task
		h.mu.Unlock()

		if _, err := taskCancel(h)(nil, nil); err != nil {
			t.Fatalf("task.cancel on a CANCEL_DELIVERED task: %v, want success", err)
		}
		if task.state != liftResolved {
			t.Errorf("state = %s, want resolved — task.cancel resolves the task (def:498)", task.state)
		}
		// The discriminant, not the payload: an empty result is what a guest returning nothing resolves
		// with legitimately (grave #885's artefact), so `result == nil` cannot carry this fact.
		if !task.cancelledResolution {
			t.Error("cancelledResolution is false after task.cancel; the caller would be handed a result " +
				"instead of ErrCancelled, which is the wrong outcome reported as success")
		}
	})

	t.Run("pending_but_undelivered_traps", func(t *testing.T) {
		h := newAsyncHandles()
		task := &liftTask{state: liftPendingCancel}
		h.mu.Lock()
		h.lift = task
		h.mu.Unlock()

		_, err := taskCancel(h)(nil, nil)
		if err == nil {
			t.Fatal("task.cancel succeeded on a PENDING_CANCEL task; the model's precondition is " +
				"CANCEL_DELIVERED specifically (def:494), so a request the guest has not been handed " +
				"must trap")
		}
		if !strings.Contains(err.Error(), "has not been delivered") {
			t.Errorf("trapped, but not with the named precondition: %v", err)
		}
		if task.state == liftResolved {
			t.Error("the task resolved anyway; a trapping task.cancel must not resolve")
		}
	})
}

// TestCancellingABeforeStartedTaskIsRefusedByName is the scope boundary ADR 0094 draws, and it is the one
// branch of the cancellation mechanism that is deliberately NOT built.
//
// `request_cancellation`'s INITIAL arm (definitions.py def:463-466) is the before-started path, and it
// produces `subtask.cancel` status 3 `CANCELLED_BEFORE_STARTED`. **Status 3 has no reference reading** —
// #862 measured 4 and recorded 3 as unmeasured rather than assumed, and #884 registers the child that
// would produce one. Burroughs can reach the state; what it cannot do is claim the behaviour.
//
// So the arm refuses by name. Asserted rather than left to prose because *a negative claim buys a branch
// an exemption* only if something checks the exemption.
func TestCancellingABeforeStartedTaskIsRefusedByName(t *testing.T) {
	h := newAsyncHandles()
	task := &liftTask{state: liftInitial}
	h.mu.Lock()
	h.cancellable[task] = struct{}{}
	h.mu.Unlock()

	err := h.requestCancelAll()
	if err == nil {
		t.Fatal("cancelling a task that has not started returned no error; it would produce status 3, " +
			"which has no reference reading, so it must refuse rather than claim a behaviour")
	}
	if !errors.Is(err, ErrTaskCancelUnbuilt) {
		t.Errorf("refused, but not with ErrTaskCancelUnbuilt: %v", err)
	}
	// The refusal points at the issue that would produce the reading, so a reader meeting it knows what
	// is missing. It used to name #862, which is closed — a refusal citing a discharged issue sends the
	// reader somewhere that answers nothing.
	if !strings.Contains(err.Error(), "#884") {
		t.Errorf("the refusal does not name what would unblock it: %v", err)
	}
	if task.state != liftInitial {
		t.Errorf("state = %s, want initial — a refused request must not move the task", task.state)
	}
}

// TestCancelStateTransitionsFollowTheModel pins the three operations against definitions.py directly,
// away from any guest, because each has a precondition that a guest-driven arm would exercise only on the
// one path that guest happens to take.
func TestCancelStateTransitionsFollowTheModel(t *testing.T) {
	t.Run("request_started_to_pending", func(t *testing.T) {
		task := &liftTask{state: liftStarted}
		if err := task.requestCancelLocked(); err != nil {
			t.Fatalf("requestCancel on a STARTED task: %v", err)
		}
		if task.state != liftPendingCancel {
			t.Errorf("state = %s, want pending-cancel (def:469-470)", task.state)
		}
	})

	t.Run("deliver_is_one_shot", func(t *testing.T) {
		task := &liftTask{state: liftPendingCancel}
		if !task.deliverPendingCancelLocked() {
			t.Fatal("deliverPendingCancel returned false on a PENDING_CANCEL task (def:476)")
		}
		if task.state != liftCancelDelivered {
			t.Fatalf("state = %s, want cancel-delivered (def:477)", task.state)
		}
		// One-shot by construction: the state moved, so a second call cannot find a pending cancel. This
		// is what lets the lift loop park — a `true` every time would deliver TASK_CANCELLED forever and
		// the guest would never wait for anything.
		if task.deliverPendingCancelLocked() {
			t.Error("deliverPendingCancel returned true twice for one request; the lift loop would " +
				"re-deliver TASK_CANCELLED on every iteration and never park")
		}
	})

	t.Run("request_is_not_idempotent", func(t *testing.T) {
		// ADR 0094's own decision, not the model's: the model *asserts* STARTED, specifying nothing for a
		// redundant request, so Burroughs refuses it with the state named.
		task := &liftTask{state: liftPendingCancel}
		err := task.requestCancelLocked()
		if !errors.Is(err, ErrCancelNotRunning) {
			t.Errorf("a second cancel request: %v, want ErrCancelNotRunning", err)
		}
		if !strings.Contains(err.Error(), "pending-cancel") {
			t.Errorf("the refusal does not name the state it found: %v", err)
		}
	})

	t.Run("request_after_resolution_is_refused_not_trapped", func(t *testing.T) {
		// The race the host legitimately loses: the task resolved while the request was in flight. A trap
		// here would blame the guest for the host's timing, and a silent success would make "I cancelled
		// it" and "it finished first" the same observation.
		task := &liftTask{state: liftResolved}
		err := task.requestCancelLocked()
		if !errors.Is(err, ErrCancelNotRunning) {
			t.Errorf("cancelling a resolved task: %v, want ErrCancelNotRunning", err)
		}
		var trap *interp.Trap
		if errors.As(err, &trap) {
			t.Error("cancelling a resolved task TRAPPED; the host raced a legitimate race and nothing " +
				"the guest did is wrong")
		}
	})

	t.Run("no_task_at_all", func(t *testing.T) {
		h := newAsyncHandles()
		if err := h.requestCancelAll(); !errors.Is(err, ErrCancelNotRunning) {
			t.Errorf("cancelling an instance hosting no lift: %v, want ErrCancelNotRunning", err)
		}
	})
}
