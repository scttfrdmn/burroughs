// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"errors"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/interp"
)

// TestSubtaskDropTrapsUnlessDelivered pins subtask.drop (0x0d) against the committed oracle (subtask_drop).
// The rule is the scheduler-side mirror of the stream drop-mid-copy trap: a subtask may be dropped only once
// its resolution has been delivered to the guest; dropping an undelivered subtask traps rather than silently
// discarding an unaccounted completion. The two CANCELLED terminal states — new inputs since subtask.cancel
// landed — behave exactly as RETURNED: drop OK once delivered, trap while undelivered. A subtask constructed
// `delivered` stands for one whose event a wait consumed or whose subtask.cancel returned its state inline.
func TestSubtaskDropTrapsUnlessDelivered(t *testing.T) {
	var doc struct {
		SubtaskDrop struct {
			Returned                map[string]string `json:"returned"`
			CancelledBeforeStarted  map[string]string `json:"cancelled_before_started"`
			CancelledBeforeReturned map[string]string `json:"cancelled_before_returned"`
			StartingUndelivered     string            `json:"starting_undelivered"`
			StartedUndelivered      string            `json:"started_undelivered"`
		} `json:"subtask_drop"`
	}
	loadFixtures(t, &doc)
	fx := doc.SubtaskDrop
	if len(fx.Returned) == 0 {
		t.Fatal("no subtask_drop oracle in fixtures.json")
	}

	cc, err := interp.NewCanonCallerForTest(1)
	if err != nil {
		t.Fatalf("harness caller: %v", err)
	}

	// dropOutcome adds a subtask in the given state/delivery to a fresh table and reports "ok" or "trap".
	dropOutcome := func(t *testing.T, state subtaskState, delivered bool) string {
		t.Helper()
		h := newAsyncHandles()
		// delivered implies resolved (both are set together in production); an undelivered resolved terminal
		// is the guest-dropped-too-early case the trap guards.
		st := &subtask{state: state, resolved: state >= subtaskReturned, delivered: delivered}
		st.index = h.addLocked(st)
		_, derr := subtaskDrop(h)(cc, []interp.Value{interp.I32(int32(st.index))})
		var tr *interp.Trap
		if errors.As(derr, &tr) {
			return "trap"
		}
		if derr != nil {
			t.Fatalf("subtask.drop returned a non-trap error: %v", derr)
		}
		return "ok"
	}

	cases := []struct {
		name  string
		state subtaskState
		rows  map[string]string
	}{
		{"returned", subtaskReturned, fx.Returned},
		{"cancelled_before_started", subtaskCancelledBeforeStarted, fx.CancelledBeforeStarted},
		{"cancelled_before_returned", subtaskCancelledBeforeReturned, fx.CancelledBeforeReturned},
	}
	for _, c := range cases {
		if got := dropOutcome(t, c.state, true); got != c.rows["delivered"] {
			t.Errorf("drop %s delivered = %s, want %s", c.name, got, c.rows["delivered"])
		}
		if got := dropOutcome(t, c.state, false); got != c.rows["undelivered"] {
			t.Errorf("drop %s undelivered = %s, want %s", c.name, got, c.rows["undelivered"])
		}
	}
	// Unresolved subtasks are never delivered -> always trap.
	if got := dropOutcome(t, subtaskStarting, false); got != fx.StartingUndelivered {
		t.Errorf("drop STARTING undelivered = %s, want %s", got, fx.StartingUndelivered)
	}
	if got := dropOutcome(t, subtaskStarted, false); got != fx.StartedUndelivered {
		t.Errorf("drop STARTED undelivered = %s, want %s", got, fx.StartedUndelivered)
	}
}
