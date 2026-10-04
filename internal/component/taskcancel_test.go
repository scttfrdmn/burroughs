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

// TestCancellingABeforeStartedTaskIsRecordedNotRefused **replaces**
// `TestCancellingABeforeStartedTaskIsRefusedByName`, which **no longer exists**: it asserted that the
// before-started arm refused with `ErrTaskCancelUnbuilt`, and ADR 0094 amendment 2 reverses that. The old
// name is written here so a reader looking for it finds the reversal rather than a deletion.
//
// # Why the reversal, and why it is not a loosening
//
// ADR 0094 refused the arm because it was believed to produce `subtask.cancel` status 3, which has no
// reference reading (#884). **That reason is true of the model and false of Burroughs**: the model calls
// `on_start` *inside* the lift (`canon_lift`'s `thread_func`, def:2097-2102), so a lift task still INITIAL
// means the parent's subtask is still STARTING, means status 3. Burroughs' cross-component adapter calls
// `onStart()` itself, synchronously, before the child's lift task exists — it has to, because `on_start`
// reads the caller's flat args — so the parent's subtask is already STARTED and the terminal state is 4
// either way. **Honouring the request claims no unmeasured status.**
//
// And the refusal's cost had changed underneath it. ADR 0094 reasoned the parent would learn of it
// through BLOCKED; grave #892 removed BLOCKED from the sync path, so the refusal became a **30s hang** —
// measured as a flaky one, which is worse than a consistent failure. *A gap whose consequence has changed
// needs re-deciding, not re-citing.*
func TestCancellingABeforeStartedTaskIsRecordedNotRefused(t *testing.T) {
	h := newAsyncHandles()
	task := &liftTask{state: liftInitial, cancelWake: make(chan struct{})}
	h.mu.Lock()
	h.cancellable[task] = struct{}{}
	h.mu.Unlock()

	if err := h.requestCancelAll(); err != nil {
		t.Fatalf("cancelling a not-yet-started task: %v, want it recorded", err)
	}
	if task.state != liftPendingCancel {
		t.Errorf("state = %s, want pending-cancel — the request must be RECORDED, because a request that "+
			"is neither honoured nor reported is a cancellation the caller believes happened", task.state)
	}
	// The task's own start must not erase it. An unconditional INITIAL→STARTED write at the top of
	// `runLiftTask` would, and that is the same 30s hang one step later.
	if task.deliverPendingCancelLocked() != true {
		t.Error("the recorded cancel is not deliverable, so the task would start and never learn of it")
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
