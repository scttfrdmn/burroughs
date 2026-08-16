// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package validate

import (
	"errors"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// The offset bound's controls, and why they are constructed rather than driven from the corpus.
//
// `checkOffset` is `check_memop`'s third `require` (#310), and it is the first rule in this package
// whose *decision* was ruled on before the code existed: the reference reads the address type for
// this bound from memory 0, hardcoded, while its own callers read the instruction's memory for the
// operand type. Burroughs reads the instruction's memory for both.
//
// **No corpus vector can see the difference**, which is exactly why these tests are hand-built. All
// four vectors expecting `offset out of range` declare a single memory, so index 0 *is* the named
// memory and both readings agree; two `offset=` tokens in the whole suite reach 2^32 and both are in
// that group. A control scoped to the corpus would therefore be a control scoped to the region where
// the decision does not apply — the current-sample blind spot with the sample chosen by the rule's
// own reward. The discriminating modules exist only here.
//
// The pair below is the decision's executable record. Bug-compatibility flips both verdicts, so an
// edit toward the reference's reading fails these rather than passing them quietly.

// dropMemargRow returns an opcode whose row carries a memarg, its mnemonic, and its natural width,
// derived from the generated table rather than named — `align_authority_test.go`'s posture, for its
// reason. `i32.load` is the row wanted and the assertion says so, but it is *found* rather than
// spelled `0x28`, so a table renumbering fails here instead of silently testing another row.
func i32LoadOpcode(t *testing.T) uint32 {
	t.Helper()
	for op := range uint32(0x100) {
		name, ok := binary.OpMnemonic(op)
		if !ok || name != "i32_load" {
			continue
		}
		if !binary.HasMemarg(0, op) {
			t.Fatalf("i32_load is opcode %#02x and the table says it carries no memarg, so the "+
				"offset bound has no operand to read", op)
		}
		return op
	}
	t.Fatal("no core opcode is named i32_load, so these controls have no instruction to build")
	return 0
}

// TestOffsetBoundReadsTheInstructionsMemory is #310's ruling, in both directions.
//
// Two modules, each with two memories of *differing* index type, and in each one the same
// instruction naming memory 1 with an offset of exactly 2^32 — the smallest value the bound refuses.
// The pair is the whole point: one case must refuse and the other must accept, and the reference's
// literal reading gives the opposite answer to both. A single case could be passed by a validator
// that always refuses or always accepts.
func TestOffsetBoundReadsTheInstructionsMemory(t *testing.T) {
	op := i32LoadOpcode(t)

	i32Mem := binary.Memory{}
	i64Mem := binary.Memory{Limits: binary.Limits{Addr64: true}}

	// Offset 2^32 exactly. `align.wast:1004` uses 0xFFFF_FFFF_FFFF_FFFF and the other three use
	// this value; the boundary is the interesting one, since `>=` versus `>` is the plausible slip
	// and only the exact bound can catch it.
	const offset = uint64(1) << 32

	// Memory index 1 in the memarg, alignment exponent 0 (one byte, within i32.load's natural 4 —
	// so an alignment refusal cannot stand in for the verdict under test).
	in := binary.Instr{Op: op, Imm0: offset, Imm1: binary.StageMemarg(1, 0)}

	tests := []struct {
		name    string
		mems    []binary.Memory
		wantErr bool
		why     string
	}{
		{
			name:    "named memory is i32, memory 0 is i64",
			mems:    []binary.Memory{i64Mem, i32Mem},
			wantErr: true,
			why: "memory 1 is the memory the instruction names and it is 32-bit, so the offset " +
				"does not fit its address space. The reference's literal reading consults memory " +
				"0, finds i64, and skips the bound entirely — so an accept here is the " +
				"bug-compatible verdict and #310 ruled against it",
		},
		{
			name:    "named memory is i64, memory 0 is i32",
			mems:    []binary.Memory{i32Mem, i64Mem},
			wantErr: false,
			why: "memory 1 is 64-bit, and a u64 offset cannot leave a 64-bit address space, so " +
				"there is nothing to refuse. The reference's literal reading consults memory 0, " +
				"finds i32, and rejects a module the spec text accepts — the direction that " +
				"costs a valid program, which is why it is worth a test of its own rather than " +
				"being read as the mirror of the case above",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := &binary.Module{Memories: tc.mems}
			_, err := signature(m, in)
			switch {
			case tc.wantErr && !errors.Is(err, ErrOffsetOutOfRange):
				t.Errorf("offset %d against memories %v: got err %v, want ErrOffsetOutOfRange.\n%s",
					offset, tc.mems, err, tc.why)
			case !tc.wantErr && errors.Is(err, ErrOffsetOutOfRange):
				t.Errorf("offset %d against memories %v: got ErrOffsetOutOfRange, want it "+
					"accepted.\n%s", offset, tc.mems, tc.why)
			case !tc.wantErr && err != nil:
				t.Errorf("offset %d against memories %v: got unrelated err %v, want no error — "+
					"the bound is the only rule under test and something else refused first",
					offset, tc.mems, err)
			}
		})
	}
}

// TestOffsetBoundAtTheBoundary pins the comparison itself on the shape the corpus does exercise: a
// single 32-bit memory, where the named memory and memory 0 are the same memory and the ruling has
// no bearing.
//
// Separate from the pair above because it tests a different claim. That pair is about *which memory*;
// this is about *which comparison*, and `>` in place of `>=` would pass every corpus vector — the two
// decimal vectors use exactly 2^32 and would land on the wrong side of it, but they sit in the
// unsupported column, and `align.wast:1004`'s 0xFFFF_FFFF_FFFF_FFFF is far enough past the bound to
// survive an off-by-one. So the one vector that reaches this package cannot see the slip this test
// exists for. *An unasserted distance is the vacuum*: the boundary is asserted here because the
// reward vector is nowhere near it.
func TestOffsetBoundAtTheBoundary(t *testing.T) {
	op := i32LoadOpcode(t)
	m := &binary.Module{Memories: []binary.Memory{{}}}

	tests := []struct {
		offset  uint64
		wantErr bool
	}{
		{offset: 0, wantErr: false},
		{offset: 1<<32 - 1, wantErr: false},
		{offset: 1 << 32, wantErr: true},
		{offset: ^uint64(0), wantErr: true},
	}

	for _, tc := range tests {
		in := binary.Instr{Op: op, Imm0: tc.offset, Imm1: binary.StageMemarg(0, 0)}
		_, err := signature(m, in)
		got := errors.Is(err, ErrOffsetOutOfRange)
		if got != tc.wantErr {
			t.Errorf("offset %d against one i32 memory: refused=%v, want refused=%v (err %v); "+
				"the bound is `offset < 0x1_0000_0000`, so 2^32-1 is the largest legal value and "+
				"2^32 the smallest illegal one", tc.offset, got, tc.wantErr, err)
		}
	}
}

// TestReferenceStillReadsMemoryZeroForTheOffsetBound is the tripwire #310's ruling requires, and it
// watches the *reference* rather than this package.
//
// The divergence recorded in `checkOffset` is a claim about a file this project vendors and does not
// control: that `check_memop` reads `memory c (0l @@ at)` for the offset bound while its callers read
// `memory c x` for the operand type. The day upstream repairs that inconsistency, our divergence
// stops being a divergence — and if the repair goes the other way, ours becomes a real conflict. Both
// outcomes want the decision re-presented, and neither one announces itself.
//
// A comment asserting the reference's intent would rot silently in the accept direction, which is why
// the discharge is a check and not a sentence. *A design debt is discharged by a tripwire, never by
// an intention*, pointed at the oracle's text rather than at our own.
//
// This fails **loudly and by design** when upstream changes: the fix is to re-read `check_memop`, not
// to update the pattern.
func TestReferenceStillReadsMemoryZeroForTheOffsetBound(t *testing.T) {
	src := testenv.RequireSpecRef(t, testenv.RefValidML)

	// The bound's own line, so the test is anchored to the rule and not merely to the file.
	const bound = `require (I64.lt_u memop.offset 0x1_0000_0000L)`
	if !strings.Contains(src, bound) {
		t.Errorf("valid.ml no longer contains %q.\n"+
			"The offset bound this package implements has moved or changed shape upstream. "+
			"Re-read check_memop before touching checkOffset: the rule's text is the thing "+
			"0003 says we quote verbatim, and a bound nobody can find in the reference is a "+
			"citation to a rule that may no longer exist.", bound)
	}

	// The hardcoded index, which is the divergence's subject.
	const literalZero = `let MemoryT (at_, _lim) = memory c (0l @@ at) in`
	if !strings.Contains(src, literalZero) {
		t.Errorf("valid.ml no longer contains %q.\n"+
			"#310 ruled that Burroughs reads the offset bound's address type from the memory the "+
			"instruction names, *diverging* from this line. If upstream has repaired the "+
			"inconsistency the divergence is gone and checkOffset's header is now wrong about the "+
			"reference; if upstream has changed it some other way the ruling needs re-presenting "+
			"to Scott. Either way this is a decision to re-take, not a pattern to update.",
			literalZero)
	}

	// The caller's half, which is what makes the line above an inconsistency rather than a policy.
	// Asserted because the divergence's *argument* rests on the two lines disagreeing: if the
	// callers ever read memory 0 too, the reference is merely consistent and unusual, and #310's
	// reasoning — that a self-contradicting check is an artifact rather than an intent — loses its
	// premise even though its conclusion might survive.
	const callerHalf = `let MemoryT (at, _lim) = memory c x in`
	if !strings.Contains(src, callerHalf) {
		t.Errorf("valid.ml no longer contains %q.\n"+
			"That line is the reason #310 read the memory-0 lookup as an artifact: the same "+
			"function's callers resolve the instruction's own memory. Without it the ruling's "+
			"premise is gone and the divergence rests on the spec text alone.", callerHalf)
	}
}
