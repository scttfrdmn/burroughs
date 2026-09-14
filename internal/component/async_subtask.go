// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

// gate:async slice-1 increment 2a-i-B: the async `canon lower`'s BLOCKING arm — the subtask substrate.
//
// When an async-lowered import's callee does not resolve inline, `canon_lower` registers a subtask in the
// instance handle table and returns `[state | (subtaski<<4)]` (definitions.py:2235–2251), where `state` is
// Subtask.State (STARTING/STARTED at the return) and `subtaski` is the subtask's table index. This file
// introduces the subtask, its per-instance table, and the packing. The waitable-set loop that OBSERVES a
// subtask's resolution — `waitable-set.new`/`join`/`wait`, the park, and the `(SUBTASK, subtaski, state)`
// event — is increment 2a-i-B-2, still refused by name at bind (an async canon built-in). The oracle is
// `canon/testdata/fixtures.json`'s `async_lower_blocking`; this slice matches its return-side pins
// (`packed`/`subtaski`/`state_at_lower`), not the wake-side event, which 2a-i-B-2 delivers.

// subtaskState mirrors definitions.py Subtask.State (def:802–806). STARTING/STARTED/RETURNED are in
// slice-1 scope; the two CANCELLED states (3, 4) are not — cancellation is a later increment.
type subtaskState uint32

const (
	subtaskStarting subtaskState = 0
	subtaskStarted  subtaskState = 1
	subtaskReturned subtaskState = 2
)

// subtask is a pending async-lowered call. The blocking arm registers it in the instance table; the impl
// holds the resolver (onResolve) that later flips it to RETURNED and arms it — `resolved` is the flag the
// 2a-i-B-2 waitable-set loop will observe to deliver a `(SUBTASK, subtaski, state)` event. In this slice a
// registered subtask is only ever STARTING/STARTED at the packed return (its resolution is not yet
// observable in-guest).
type subtask struct {
	state    subtaskState
	resolved bool
}

// subtaskTable is a per-component-instance handle table for subtasks (and, in 2a-i-B-2, waitable-sets).
// Index 0 is a reserved nil sentinel, so the first add returns 1 — matching the reference model's Table
// (which reserves 0) and the oracle's `subtaski=1`. Not concurrency-safe by construction: a lower runs on
// its calling agent's own thread, and slice-1 scope is a single guest agent (the concurrent-task machinery
// is deferred, #739).
type subtaskTable struct {
	entries []*subtask // entries[0] is the reserved nil sentinel
}

func newSubtaskTable() *subtaskTable { return &subtaskTable{entries: []*subtask{nil}} }

// add registers s and returns its index (>= 1). The model asserts 0 < subtaski <= MAX_LENGTH < 2^28.
func (t *subtaskTable) add(s *subtask) int {
	t.entries = append(t.entries, s)
	return len(t.entries) - 1
}

// packSubtaskWait encodes `canon_lower`'s blocking return `[state | (subtaski<<4)]` (def:2251). The model
// asserts 0 <= state < 2^4 and 0 < subtaski < 2^28, so the two fields never overlap.
func packSubtaskWait(state subtaskState, subtaski int) int32 {
	return int32(uint32(state) | uint32(subtaski)<<4)
}
