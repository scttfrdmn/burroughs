// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package component

import (
	"fmt"

	"github.com/scttfrdmn/burroughs/internal/interp"
)

// gate:async increment 4: context.get/set — the first op the real guest (p3async-hello) hits. Async
// component-model context is per-agent (i32) storage of a fixed number of slots. The model stores it
// per-thread; in Burroughs the per-agent unit is the caller's stack (two concurrent agents are two stacks
// but one shared thread object), so the storage lives there (interp.CanonCaller.ContextGet/Set), keyed by
// nothing — per-agent by construction. An unset slot reads 0 (the stack's zero value, matching the model's
// [0,0]); a slot index past the fixed count traps. The slot index is a STATIC operand of the built-in,
// captured at decode; these wrappers close over it. (Decision 0050: per-invocation and per-thread coincide
// under v1 and split at §7 growable continuations — the placement's live expiry, #739.)

// contextGet implements `canon context.get` (0xa, definitions.py:2293) for a static slot: read the calling
// agent's context slot, returning it as an i32 (slice-1 scope is i32 context).
func contextGet(slot uint32) interp.CanonFunc {
	return func(c *interp.CanonCaller, _ []interp.Value) ([]interp.Value, error) {
		v, err := c.ContextGet(slot)
		if err != nil {
			return nil, err
		}
		return []interp.Value{interp.I32(int32(v))}, nil
	}
}

// contextSet implements `canon context.set` (0xb, definitions.py:2303) for a static slot: write the value
// (a runtime i32 arg) into the calling agent's context slot.
func contextSet(slot uint32) interp.CanonFunc {
	return func(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("component: context.set: got %d args, want (v)", len(args))
		}
		if err := c.ContextSet(slot, uint32(args[0].Bits)); err != nil {
			return nil, err
		}
		return nil, nil
	}
}
