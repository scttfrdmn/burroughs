package binary

import (
	"errors"
	"testing"
)

// TestDelimitersAreRetained asserts END and ELSE appear in the retained sequence.
//
// # The defect this was written for
//
// `expectEnd` read the terminator, judged it, and dropped it — at all three call sites —
// while `structural`'s comment claimed the opposite in so many words: "its own terminator is
// emitted by the recursive `block`/`expectEnd` pair below, which is why END appears in the
// retained sequence at all". Nothing emitted it. The comment was the strongest camouflage
// available, because a reviewer checking the code against the claim finds a `block` call and
// an `expectEnd` call exactly where the sentence says they are.
//
// What found it was an assertion over the accept population — 23 of 27 bound functions
// decoded to a *zero-length body*, which is what `(func)` becomes when its only instruction
// is the terminator and the terminator is discarded. Not findable from the board: every
// affected module is one the suite expects to accept, so this is contract §9 G-3 exactly.
//
// # Why the corpus cannot be the whole control here
//
// `internal/spec`'s retention control now pins END over the real accept population, and that
// is the citation-bearing half. It cannot pin ELSE: **the accept population contains two
// structural instructions and zero ELSE bytes**, measured. So the else-arm retention would be
// an unexercised claim — an emit nobody runs, which is indistinguishable from an emit that is
// wrong. These rows are synthetic and say so, for the reason the blocktype table is: no
// phase-1 vector reaches an `if` with an else arm.
//
// The two halves are deliberate rather than duplicated: the corpus proves the terminator is
// kept on modules whose acceptance is already proven, and this proves the *shape* of what is
// kept for the arms the corpus does not contain.
func TestDelimitersAreRetained(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []byte
		want []Instr
		why  string
	}{
		{
			// synthetic: an empty function body's expr, which is the minimal case the
			// dropped terminator turned into nothing at all.
			"a bare END is one instruction",
			[]byte{0x0B},
			[]Instr{{Op: opEnd}},
			"`expr` is `instr* end_`, so the empty expr is not the empty sequence — a " +
				"consumer handed zero instructions cannot tell it apart from a body the " +
				"retention skipped",
		},
		{
			// synthetic: `i32.const 7` then END.
			"an instruction then its terminator",
			[]byte{0x41, 0x07, 0x0B},
			[]Instr{{Op: 0x41, Imm0: 7}, {Op: opEnd}},
			"the terminator follows the body rather than replacing it",
		},
		{
			// synthetic: `block (result i32) i32.const 1 end end`. The outer END closes
			// the expr, the inner one closes the block.
			"a nested block's END and the expr's are both kept",
			[]byte{0x02, 0x7F, 0x41, 0x01, 0x0B, 0x0B},
			[]Instr{
				{Op: 0x02, Imm0: blockTypeValType | uint64(I32)},
				{Op: 0x41, Imm0: 1},
				{Op: opEnd},
				{Op: opEnd},
			},
			"the header is emitted before the body (branch targets index this slice) and " +
				"each block contributes its own terminator, so extents are readable " +
				"without re-walking the bytes",
		},
		{
			// synthetic: `if (result i32) i32.const 1 else i32.const 2 end end`.
			"an if's ELSE separates its arms",
			[]byte{0x04, 0x7F, 0x41, 0x01, 0x05, 0x41, 0x02, 0x0B, 0x0B},
			[]Instr{
				{Op: 0x04, Imm0: blockTypeValType | uint64(I32)},
				{Op: 0x41, Imm0: 1},
				{Op: opElse},
				{Op: 0x41, Imm0: 2},
				{Op: opEnd},
				{Op: opEnd},
			},
			"**the row this file exists for.** Drop the ELSE and the retained sequence is " +
				"four instructions with no boundary between the arms — and the arms have no " +
				"declared lengths, so nothing downstream can recover the split. An `if` " +
				"whose arms cannot be told apart executes the wrong one on valid input",
		},
		{
			// synthetic: `if 0x40 nop end end` — an if with no else arm, which is the
			// case that must *not* fabricate a delimiter.
			"an if without an else arm retains no ELSE",
			[]byte{0x04, 0x40, 0x01, 0x0B, 0x0B},
			[]Instr{
				{Op: 0x04, Imm0: blockTypeEmpty},
				{Op: 0x01},
				{Op: opEnd},
				{Op: opEnd},
			},
			"the other direction of the same fact: ELSE is emitted only where the image " +
				"held one, so a consumer counting arms is reading the module rather than a " +
				"convention",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []Instr
			d := &Decoder{}
			c := &instrCtx{d: d, nonConst: -1, out: &got}
			r := &reader{b: tc.in, eof: ErrPayloadEnd}
			if err := c.block(r); err != nil {
				t.Fatalf("block: %v (%s)", err, tc.why)
			}
			if err := c.endTerminator(r); err != nil {
				t.Fatalf("endTerminator: %v (%s)", err, tc.why)
			}
			if r.off != len(tc.in) {
				t.Errorf("consumed %d of %d bytes; a delimiter left unread is one the "+
					"next grammar will misread", r.off, len(tc.in))
			}
			if len(got) != len(tc.want) {
				t.Fatalf("retained %d instructions, want %d:\n got %+v\nwant %+v\n%s",
					len(got), len(tc.want), got, tc.want, tc.why)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("instruction %d = %+v, want %+v\n%s",
						i, got[i], tc.want[i], tc.why)
				}
			}
		})
	}
}

// TestDelimiterRetentionIsOnTheAcceptingPathOnly asserts a rejected expr retains nothing.
//
// The timing half of `endTerminator`'s split: the verdict is `expectEnd`'s and the emit is
// this layer's, so an image whose terminator is wrong must produce the error *and* leave
// the sequence without a fabricated END. Merging the retention into `expectEnd` would put
// the append in front of the error return, which is the shape `emit`'s own comment forbids —
// an instruction retained from a module the layer is about to reject.
//
// Both rows are the free function's real vectors, cited: binary.wast:55 has a byte there
// that is not END, binary.wast:76 has no byte there at all. Cited rather than synthetic
// because the *bytes* are derived from those vectors' expr fragments — see each row for the
// mechanism, which is load-bearing in the first one.
func TestDelimiterRetentionIsOnTheAcceptingPathOnly(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []byte
		// binary.wast:55 — "END opcode expected": a non-END byte where the terminator
		// belongs. binary.wast:76 — "unexpected end of section or function": no byte.
		wantErr error
	}{
		{
			// binary.wast:55, mechanism and all. The vector omits function 0's END, so
			// the byte reaching the terminator's position is function 1's *size*, `\05` —
			// which is ELSE. That matters for the row: `block` stops only on ELSE, END or
			// eof, so ELSE is the one non-END byte `expectEnd` can actually be handed, and
			// a row using any other byte would have `block` consume it as an opcode and
			// never reach the assertion. Found by reading the vector rather than by
			// picking a plausible-looking byte, which is how the first draft got 0x40.
			"a non-END byte where the terminator belongs",
			[]byte{0x41, 0x07, opElse},
			ErrEndExpected,
		},
		{
			// binary.wast:76: the terminator's position is past the end of the image.
			"no byte at all",
			[]byte{0x41, 0x07},
			ErrPayloadEnd,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []Instr
			c := &instrCtx{d: &Decoder{}, nonConst: -1, out: &got}
			r := &reader{b: tc.in, eof: ErrPayloadEnd}
			if err := c.block(r); err != nil {
				t.Fatalf("block: %v", err)
			}
			err := c.endTerminator(r)
			if err == nil {
				t.Fatalf("endTerminator accepted %#v", tc.in)
			}
			// The error identity, not merely its presence: these two vectors are the
			// reason `expectEnd` distinguishes a wrong byte from a missing one, and a
			// retention change that collapsed them would be invisible to a nil check.
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("endTerminator: %v, want %v", err, tc.wantErr)
			}
			// One instruction — the i32.const — and no END. The count is the assertion:
			// a retention on the rejecting path would make it two.
			if len(got) != 1 {
				t.Errorf("retained %d instructions past a rejected terminator, want 1 "+
					"(the i32.const alone): %+v", len(got), got)
			}
			for i, in := range got {
				if in.Op == opEnd && in.Prefix == 0x00 {
					t.Errorf("instruction %d is an END retained from an expr that has "+
						"none; the emit must sit past the verdict, not before it", i)
				}
			}
		})
	}
}
