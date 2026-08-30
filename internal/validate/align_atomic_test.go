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
// **Zero vectors on the board expect that string**, and the sentence needs both a path and a pin to
// be a measurement, because two different files are called `atomic.wast`:
//
//	testdata/spec/proposals/threads/atomic.wast    48 assert_invalid, all `unknown memory`, 0 alignment
//	third_party/spec-threads/…/threads/atomic.wast 93 assert_invalid, 48 of them + 45 alignment
//
// The first is what the harness runs (`fetch-spec-tests.sh`, `WebAssembly/testsuite` @ `de54fd27`);
// the second is the pin that *defines* these instructions (`fetch-threads-ref.sh`,
// `WebAssembly/threads` @ `cc535ada`). The board's corpus lags the atomics' own authority by exactly
// the 45 vectors that would exercise this rule — filed as **#537**, not fixed here, because bumping a
// corpus pin is its own reviewable diff (#42).
//
// This paragraph previously said those rows were `unknown memory` **and `type mismatch`**. There is no
// `type mismatch` in either copy of the file — `grep -c` is 0 in both — so that half was never
// measured. Corrected in place rather than quietly deleted: what a comment claimed about a corpus is
// the durable part, and a wrong count in a header is read as a survey nobody needs to repeat.
//
// So the whole rule sits in the direction no board can see: an under-aligned atomic access is accepted
// by the ceiling rule and refused by this one, and the difference costs nothing on any column. That is
// the offset bound's situation exactly (`offset_test.go`'s header), and it gets the same answer — the
// discriminating modules do not exist upstream, so they exist here.
//
// # These now reach the rule through a module, which they did not when they were written
//
// The header here used to say they called `checkAlignment` directly, that no path to it existed from
// a module, and that *the rule ships one slice ahead of its own call site* — a control exercising a
// helper while nothing calls it, named honestly and left that way. #524's validation half is the
// slice that was owed: `instr.go` dispatches the 0xFE region, `atomicSignature` calls `checkMemop`,
// and `checkMemop` calls this rule. So the cases below go through `atomicSignature` and `signature`
// — the two production entry points — and the helper is no longer addressed directly by anything.
//
// That re-pointing is not cosmetic. Between `checkAlignment` and the entry point sit two things these
// cases could not previously see: the memory lookup, which must not refuse first, and `atomicAccess`'
// *mode selection*, which reads the mnemonic the entry point happens to pass. The old form supplied
// that mnemonic itself, so it asserted the rule while assuming the selection.

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

	// The width both rows share, read rather than asserted: every exponent below is a position
	// relative to it, and they mean nothing if it moved.
	const natural = 4
	if w, ok := naturalWidth("i32_atomic_load"); !ok || w != natural {
		t.Fatalf("naturalWidth(i32_atomic_load) = %d, %v, want %d", w, ok, natural)
	}
	if w, ok := naturalWidth("i32_load"); !ok || w != natural {
		t.Fatalf("naturalWidth(i32_load) = %d, %v, want %d; the mirror below is only a mirror if "+
			"the two rows have the same width", w, ok, natural)
	}

	tests := []struct {
		name    string
		in      binary.Instr
		atomic  bool
		wantErr error
		why     string
	}{
		{
			name:    "atomic access aligned below natural",
			in:      binary.Instr{Prefix: prefixAtomic, Op: atomicLoad, Imm1: binary.StageMemarg(0, 1)},
			atomic:  true,
			wantErr: ErrAtomicAlignment,
			why: "align=1 is two bytes against a natural four. The ceiling rule accepts it and the " +
				"reference's Atomic branch refuses it, so an accept here is the whole rule missing",
		},
		{
			name:    "atomic access unaligned",
			in:      binary.Instr{Prefix: prefixAtomic, Op: atomicLoad, Imm1: binary.StageMemarg(0, 0)},
			atomic:  true,
			wantErr: ErrAtomicAlignment,
			why: "align=0 is one byte, and it is asserted beside the case above rather than read as " +
				"its consequence: a comparison written `>= size` refuses exponent 1 and accepts " +
				"exponent 0, so one under-aligned case cannot tell an equality from an inverted " +
				"bound. The smallest legal encoding is also the one a hand-written module reaches for",
		},
		{
			name:    "atomic access aligned at natural",
			in:      binary.Instr{Prefix: prefixAtomic, Op: atomicLoad, Imm1: binary.StageMemarg(0, 2)},
			atomic:  true,
			wantErr: nil,
			why:     "align=2 is four bytes, which is natural: the one alignment the rule admits",
		},
		{
			name:    "atomic access aligned above natural",
			in:      binary.Instr{Prefix: prefixAtomic, Op: atomicLoad, Imm1: binary.StageMemarg(0, 3)},
			atomic:  true,
			wantErr: ErrAtomicAlignment,
			why: "align=3 is eight bytes. Refused by both rules, and the sentinel says which one " +
				"refused — the reference reports its own string per branch (0003), so an " +
				"ErrAlignmentTooLarge here would be the right verdict with the wrong words",
		},
		{
			name:    "core access aligned below natural",
			in:      binary.Instr{Op: coreLoad, Imm1: binary.StageMemarg(0, 1)},
			wantErr: nil,
			why: "the same alignment on the same width, non-atomic: legal, and this is the half that " +
				"fails if the equality rule is applied to every memarg row",
		},
		{
			name:    "core access aligned above natural",
			in:      binary.Instr{Op: coreLoad, Imm1: binary.StageMemarg(0, 3)},
			wantErr: ErrAlignmentTooLarge,
			why:     "the ceiling rule still refuses what it always refused",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// One memory, so the lookup inside `checkMemop` cannot refuse before the alignment rule
			// runs — the reference's order puts `memory c x` first and this control is about the
			// second step.
			m := &binary.Module{Memories: []binary.Memory{{}}}

			// Through the entry point that owns the row's region, not through `checkAlignment`: the
			// mnemonic and therefore the *mode* is then whatever production passes, which is the half
			// the old form of this test supplied for itself.
			var err error
			if tc.atomic {
				_, err = atomicSignature(m, tc.in)
			} else {
				_, err = signature(m, tc.in)
			}
			switch {
			case tc.wantErr == nil && err != nil:
				t.Errorf("%v = %v, want no error\n\t%s", mnemonic(tc.in), err, tc.why)
			case tc.wantErr != nil && !errors.Is(err, tc.wantErr):
				t.Errorf("%v = %v, want %v\n\t%s", mnemonic(tc.in), err, tc.wantErr, tc.why)
			}
		})
	}
}
