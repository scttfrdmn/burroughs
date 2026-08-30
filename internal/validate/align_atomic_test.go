// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package validate

import (
	"errors"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// The atomic alignment rule's controls, and why they are hand-built like the offset bound's.
//
// The threads pin's `check_memop` takes a mode, and its `Atomic` branch is an **equality**:
//
//	| Atomic ->
//	  require (1 lsl memop.align = size) at
//	    "atomic alignment must be natural"
//
// (`spec-threads/valid/valid.ml:203-209`.)
//
// **Zero corpus vectors expect that string**, measured over the four files the threads suite ships:
// `atomic.wast`'s `assert_invalid` rows are all `unknown memory` and `type mismatch`, and no vector
// in the proposal's suite gives an atomic access a non-natural alignment. So the whole rule sits in
// the direction no board can see — an under-aligned atomic access is accepted by the ceiling rule and
// refused by this one, and the difference costs nothing on any column. That is the offset bound's
// situation exactly (`offset_test.go`'s header), and it gets the same answer: the discriminating
// modules do not exist upstream, so they exist here.
//
// # What these tests reach, and what they do not
//
// They call `checkAlignment` directly. There is no path to it from a module yet: `signature` has no
// arm for an `i32.atomic.load`, so an atomic instruction is refused as unsupported before any memarg
// rule runs, and *the rule ships one slice ahead of its own call site*. Stated because a control that
// exercises a helper while nothing calls it is this project's own named defect — the honest form is
// to say which half is covered. The pair below covers the rule; the arms that reach it are the next
// slice's work, and `TestAtomicModeMatchesTheThreadsReference` is what will hold the *selection*
// right when they arrive.

// atomicMemargRow finds a memarg-carrying row in the 0xfe region by mnemonic, derived from the table
// rather than spelled as an opcode — `i32LoadOpcode`'s posture in `offset_test.go`, for its reason.
func atomicMemargRow(t *testing.T, want string) uint32 {
	t.Helper()
	for op := range uint32(0x400) {
		name, _, ok := binary.PrefixedOp(0xfe, op)
		if !ok || name != want {
			continue
		}
		if !binary.HasMemarg(0xfe, op) {
			t.Fatalf("%s is 0xfe %#02x and the table says it carries no memarg, so the alignment rule "+
				"has no operand to read", want, op)
		}
		return op
	}
	t.Fatalf("no opcode in the 0xfe region is named %q, so these controls have no instruction to "+
		"build — the atomics region is the threads pin's clause and this is what its absence looks "+
		"like", want)
	return 0
}

// TestAtomicAlignmentIsEqualityNotACeiling is the rule's discriminating pair, and the pair is the
// point: one alignment that both rules accept, and one that only the ceiling rule accepts.
//
// `i32.atomic.load` and `i32.load` have the same natural width, 4, so the *only* difference between
// the two rows is which branch of `check_memop` they take. An implementation that applied the ceiling
// to both would pass every under-aligned case; one that applied equality to both would fail the core
// row that 62 corpus vectors depend on. Neither survives both halves.
func TestAtomicAlignmentIsEqualityNotACeiling(t *testing.T) {
	atomicLoad := atomicMemargRow(t, "i32_atomic_load")
	coreLoad := i32LoadOpcode(t)

	tests := []struct {
		name     string
		in       binary.Instr
		mnemonic string
		wantErr  error
		why      string
	}{
		{
			name:     "atomic access aligned below natural",
			in:       binary.Instr{Prefix: 0xfe, Op: atomicLoad, Imm1: binary.StageMemarg(0, 1)},
			mnemonic: "i32_atomic_load",
			wantErr:  ErrAtomicAlignment,
			why: "align=1 is two bytes against a natural four. The ceiling rule accepts it and the " +
				"reference's Atomic branch refuses it, so an accept here is the whole rule missing",
		},
		{
			name:     "atomic access aligned at natural",
			in:       binary.Instr{Prefix: 0xfe, Op: atomicLoad, Imm1: binary.StageMemarg(0, 2)},
			mnemonic: "i32_atomic_load",
			wantErr:  nil,
			why:      "align=2 is four bytes, which is natural: the one alignment the rule admits",
		},
		{
			name:     "atomic access aligned above natural",
			in:       binary.Instr{Prefix: 0xfe, Op: atomicLoad, Imm1: binary.StageMemarg(0, 3)},
			mnemonic: "i32_atomic_load",
			wantErr:  ErrAtomicAlignment,
			why: "align=3 is eight bytes. Refused by both rules, and the sentinel says which one " +
				"refused — the reference reports its own string per branch (0003), so an " +
				"ErrAlignmentTooLarge here would be the right verdict with the wrong words",
		},
		{
			name:     "core access aligned below natural",
			in:       binary.Instr{Op: coreLoad, Imm1: binary.StageMemarg(0, 1)},
			mnemonic: "i32_load",
			wantErr:  nil,
			why: "the same alignment on the same width, non-atomic: legal, and this is the half that " +
				"fails if the equality rule is applied to every memarg row",
		},
		{
			name:     "core access aligned above natural",
			in:       binary.Instr{Op: coreLoad, Imm1: binary.StageMemarg(0, 3)},
			mnemonic: "i32_load",
			wantErr:  ErrAlignmentTooLarge,
			why:      "the ceiling rule still refuses what it always refused",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := checkAlignment(tc.in, tc.mnemonic)
			switch {
			case tc.wantErr == nil && err != nil:
				t.Errorf("checkAlignment(%s) = %v, want no error\n\t%s", tc.mnemonic, err, tc.why)
			case tc.wantErr != nil && !errors.Is(err, tc.wantErr):
				t.Errorf("checkAlignment(%s) = %v, want %v\n\t%s", tc.mnemonic, err, tc.wantErr, tc.why)
			}
		})
	}
}
