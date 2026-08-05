package interp

import (
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/text"
)

// encodeDecodeInvoke is run1's error-returning twin, for the rows whose subject *is* the error.
//
// Same path deliberately — `EncodeModule` → `DecodeModule` → `Invoke`, for run1's reason — but a
// `Fatalf` helper cannot serve a test asserting that something is refused, and a reject-direction
// row written against run1 would have to assert by *absence*. It returns the first error from any
// stage: which stage refused is the caller's to check, and the callers do, because "the encoder
// refused it" and "the interpreter refused it" are different findings.
func encodeDecodeInvoke(src string) ([]Value, error) {
	img, err := text.EncodeModule([]byte(src))
	if err != nil {
		return nil, err
	}
	m, err := binary.DecodeModule(img)
	if err != nil {
		return nil, err
	}
	in, trap := Instantiate(m)
	if trap != nil {
		return nil, trap
	}
	return in.Invoke("c")
}

// TestControlFlowSemantics pins the block family through the encode→decode→invoke path.
//
// **Every row is a case where a plausible implementation gives a different answer**, which is the
// selection rule rather than coverage for its own sake: a `block` whose continuation is computed
// like a `loop`'s, an `if` matching a nested ELSE, a `br` that forgets to truncate. Rows whose
// value would be the same under the wrong implementation are not evidence and are not here.
//
// The answers are the reference's, and the ones that are not obvious say why in the row.
func TestControlFlowSemantics(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want int32
	}{
		// ---- block: falls through, and a branch to it exits ----------------------------
		{
			// A block that simply ends. If END popped nothing the control stack would grow
			// per block and a later `br` would count the wrong labels.
			name: "block falls through",
			src:  `(block) (i32.const 7)`,
			want: 7,
		},
		{
			// `br 0` inside a block jumps *past* its END. If it re-entered (a loop's
			// continuation) this would not terminate.
			name: "br 0 exits its block",
			src:  `(block (br 0) (unreachable)) (i32.const 7)`,
			want: 7,
		},
		{
			// The instruction after the `br` is skipped, not merely reached-and-ignored:
			// `unreachable` would trap if executed. This is what separates a real jump from
			// a fall-through that happens to produce the same value.
			name: "br skips the rest of the block",
			src:  `(block (br 0) (unreachable))(i32.const 1)`,
			want: 1,
		},
		{
			// A block with a result type yields it. Arity 1 rather than 0, so a branch has
			// to keep one value — the `arity` field earning its existence.
			name: "block yields its result",
			src:  `(block (result i32) (i32.const 42))`,
			want: 42,
		},
		{
			// **The truncation witness.** Three values are pushed inside the block and the
			// branch keeps exactly one. An implementation that jumps without doing the stack
			// surgery leaves 8 and 9 behind, and `Invoke`'s arity check then fails — so this
			// row fails loudly rather than returning a wrong number.
			name: "br truncates to the label's arity",
			src:  `(block (result i32) (i32.const 7) (i32.const 8) (i32.const 9) (br 0))`,
			want: 9,
		},
		{
			// `br 1` from inside two blocks exits *both*. The control stack must unwind two
			// levels, not one; unwinding one leaves a stale label and the outer END pops
			// something that is already gone.
			name: "br 1 exits two blocks",
			src:  `(block (block (br 1) (unreachable)) (unreachable)) (i32.const 3)`,
			want: 3,
		},

		// ---- br_if: the operand is consumed either way ---------------------------------
		{
			name: "br_if not taken falls through",
			src:  `(block (br_if 0 (i32.const 0)) (nop)) (i32.const 5)`,
			want: 5,
		},
		{
			name: "br_if taken exits",
			src:  `(block (br_if 0 (i32.const 1)) (unreachable)) (i32.const 5)`,
			want: 5,
		},

		// ---- if/else -------------------------------------------------------------------
		{
			name: "if true runs the then-arm",
			src:  `(if (i32.const 1) (then (nop)) (else (unreachable))) (i32.const 1)`,
			want: 1,
		},
		{
			name: "if false runs the else-arm",
			src:  `(if (i32.const 0) (then (unreachable)) (else (nop))) (i32.const 2)`,
			want: 2,
		},
		{
			// **The then-arm must not fall into the else-arm.** An implementation that pops
			// the label at ELSE and continues executes both arms, and `unreachable` traps —
			// so a broken engine fails this row rather than returning 11.
			name: "then-arm skips the else-arm",
			src:  `(if (result i32) (i32.const 1) (then (i32.const 11)) (else (unreachable)))`,
			want: 11,
		},
		{
			name: "else-arm yields its own value",
			src:  `(if (result i32) (i32.const 0) (then (i32.const 11)) (else (i32.const 22)))`,
			want: 22,
		},
		{
			// An `if` with no else and a false condition skips the whole construct and
			// yields nothing. Its label must be popped by the skip, since the END that
			// would pop it is jumped over.
			name: "if false with no else yields nothing",
			src:  `(if (i32.const 0) (then (unreachable))) (i32.const 4)`,
			want: 4,
		},
		{
			// **The nested-ELSE witness, and the reason elseOf matches at depth 1 only.**
			// The outer `if` is false, so it must jump to *its own* else-arm — the inner
			// `if`'s ELSE comes first in the instruction stream. Matching that one lands
			// inside the then-arm and executes `unreachable`.
			name: "nested if's else is not the outer if's",
			src: `(if (result i32) (i32.const 0)
			        (then (if (result i32) (i32.const 1)
			                (then (unreachable))
			                (else (unreachable))))
			        (else (i32.const 99)))`,
			want: 99,
		},
		{
			// `br 0` inside a then-arm exits the whole `if`, not just the arm — the label is
			// pushed for the construct, so this lands past the END.
			name: "br 0 in a then-arm exits the if",
			src:  `(if (i32.const 1) (then (br 0) (unreachable)) (else (unreachable))) (i32.const 6)`,
			want: 6,
		},

		// ---- loop: a branch re-enters --------------------------------------------------
		{
			// A loop with no branch runs its body once and falls out. If a loop's
			// continuation were used as a fall-through target this would spin.
			name: "loop without a branch runs once",
			src:  `(loop (nop)) (i32.const 8)`,
			want: 8,
		},
		{
			// **The counting loop, and the row that separates loop from block.** `br 0`
			// targets the loop and re-enters it; if its continuation were computed as a
			// block's (past the END) this would run one iteration and return 1.
			name: "loop counts to 10",
			src: `(local i32)
			      (loop
			        (local.set 0 (i32.add (local.get 0) (i32.const 1)))
			        (br_if 0 (i32.lt_s (local.get 0) (i32.const 10))))
			      (local.get 0)`,
			want: 10,
		},
		{
			// A `br 1` from inside a loop inside a block exits the *block* — so it leaves
			// the loop entirely rather than re-entering. Distinguishes the two
			// continuations in one body, where either alone could be coincidentally right.
			name: "br 1 from a loop exits the enclosing block",
			src: `(local i32)
			      (block
			        (loop
			          (local.set 0 (i32.add (local.get 0) (i32.const 1)))
			          (br_if 1 (i32.ge_s (local.get 0) (i32.const 3)))
			          (br 0)))
			      (local.get 0)`,
			want: 3,
		},

		// ---- return, and the implicit function-body label ------------------------------
		{
			name: "return yields the value on the stack",
			src:  `(i32.const 13) (return) (unreachable)`,
			want: 13,
		},
		{
			// **`br 0` with no enclosing block is a return**, because a function body is an
			// implicit labelled block. Legal, and an engine that only looks up explicit
			// labels reports `unknown label` on a valid module — the accept-direction defect
			// this row exists for. `assert_invalid` vectors cannot catch it.
			name: "br 0 in a bare body returns",
			src:  `(i32.const 14) (br 0) (unreachable)`,
			want: 14,
		},
		{
			// **A return truncates to the function's arity** (grave #135). One value is left
			// below the result, so an arm that returns without the stack surgery leaves two on
			// a `(result i32)` function and `Invoke`'s arity check rejects a *valid* module as
			// unvalidated. `eval.ml:1069` is `take n vs0`; the arm this row falsifies asserted
			// in a comment that no surgery was needed, which is why review could not see it.
			name: "return truncates the stack to the result arity",
			src:  `(i32.const 1) (return (i32.const 2))`,
			want: 2,
		},
		{
			// The same for the *implicit-label* spelling, which reaches a different line: `br 0`
			// in a bare body is a return, and it has to truncate for the same reason.
			name: "br to the implicit function label truncates too",
			src:  `(i32.const 1) (i32.const 2) (br 0)`,
			want: 2,
		},
		{
			// And for `br_if`, the third site that resolves to the function label. Taken, with
			// a value stranded below the result.
			name: "br_if to the implicit function label truncates too",
			src:  `(i32.const 1) (i32.const 2) (br_if 0 (i32.const 1)) (unreachable)`,
			want: 2,
		},
		{
			// Depth beyond zero, so the arity comes from the *function* and not from a block
			// that happens to share it: two values stranded, one inside a block.
			name: "return from inside a block truncates the whole stack",
			src:  `(i32.const 1) (block (i32.const 9) (return (i32.const 2))) (unreachable)`,
			want: 2,
		},
		{
			// The same reading one level in: inside one block, label 1 is the function.
			name: "br 1 from one block returns",
			src:  `(block (i32.const 15) (br 1) (unreachable)) (unreachable)`,
			want: 15,
		},
		{
			name: "return from inside two blocks",
			src:  `(block (block (i32.const 16) (return))) (unreachable)`,
			want: 16,
		},

		// ---- select: the operand order, which is the only way to get it wrong ------------
		//
		// Not control flow — here because the arm is in the same switch and the wrong answer is
		// the same *kind* of wrong: a module that runs and computes something else. The two
		// values differ and the condition is asserted in both directions, which is the whole
		// content: an engine that returned `v2` for a true condition passes any row where the
		// operands are equal, and passes a one-direction row outright.
		{
			// `eval.ml:193-197`: the condition is on top, `v1` is the value written *first*,
			// and a nonzero condition selects it. Popped in reverse, so the arm's `a` is `v1`.
			name: "select true takes the first operand",
			src:  `(select (i32.const 11) (i32.const 22) (i32.const 1))`,
			want: 11,
		},
		{
			name: "select false takes the second operand",
			src:  `(select (i32.const 11) (i32.const 22) (i32.const 0))`,
			want: 22,
		},
		{
			// Any nonzero condition is true, not just 1 — `if i = 0l then v2 else v1` tests
			// against zero rather than for one, so an implementation comparing to 1 gets this
			// row wrong and the row above right.
			name: "select treats any nonzero condition as true",
			src:  `(select (i32.const 11) (i32.const 22) (i32.const -7))`,
			want: 11,
		},
		{
			// The **annotated** encoding, which must behave identically: the `vec valtype` is a
			// validation-time annotation the reference's `Select _` discards. Same answer as the
			// bare row, which is the assertion — an arm keying on the opcode would differ here.
			name: "select with a result annotation behaves the same",
			src:  `(select (result i32) (i32.const 11) (i32.const 22) (i32.const 1))`,
			want: 11,
		},
		{
			// `select (result)` — a written but *empty* annotation, which encodes as `0x1c` with
			// a zero-length vector (`selectOpByte`'s reason for keying on the spelling). The
			// third distinct byte sequence for one behaviour.
			name: "select with an empty result annotation behaves the same",
			src:  `(select (result) (i32.const 11) (i32.const 22) (i32.const 0))`,
			want: 22,
		},
		{
			// Select consumes all three operands and pushes exactly one. Two selects in
			// sequence over a stack deep enough to notice: an arm leaving the unselected value
			// behind returns 33 here, or fails `Invoke`'s arity check.
			name: "select consumes three and pushes one",
			src: `(i32.const 33)
			      (select (i32.const 11) (i32.const 22) (i32.const 0))
			      (i32.add)`,
			want: 55,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			src := "(module (func (export \"c\") (result i32) " + tc.src + "))"
			out := run1(t, src)
			if len(out) != 1 {
				t.Fatalf("got %d results, want 1", len(out))
			}
			if got := int32(uint32(out[0].Bits)); got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

// TestSelectOpcodesAgreeWithTheDecoder is the check `opSelect`/`opSelectT` cite, and it exists
// because this package now holds a *second* copy of a fact `internal/text` already holds.
//
// Two packages knowing one fact is the drift shape, and the honest options were to share the
// constants or to control the duplication. Sharing would mean exporting an opcode set from
// `binary` for one pair of bytes, which puts a table in the load-bearing spot for a two-line
// consumer; so the duplication stands and this is the tripwire. Not a copy of `internal/text`'s
// control either — that one asks the *generated table* and the decoder about the text package's
// constants, and a control comparing this package's constants to that package's unexported ones
// cannot be written at all. It asks the decoder directly, which is the authority both copies are
// derived from.
//
// The discriminator is the immediate, for internal/text's reason: `0x1b` takes none, `0x1c` is
// followed by `vec valtype`, so a body of the bare byte decodes for one and is truncated for the
// other. **Both directions**, since either alone is satisfied by a swapped pair.
func TestSelectOpcodesAgreeWithTheDecoder(t *testing.T) {
	// `(module (func) )` with a hand-written body, assembled here rather than encoded from wat:
	// the question is what the *decoder* says about a byte, and going through the text encoder
	// would ask this package's sibling instead.
	build := func(body ...byte) []byte {
		img := []byte{
			0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
			0x01, 0x04, 0x01, 0x60, 0x00, 0x00, // type: [] -> []
			0x03, 0x02, 0x01, 0x00, // function: one func, type 0
		}
		fb := append([]byte{0x00}, body...) // no locals, then the body
		fb = append(fb, 0x0b)               // the function's END
		code := append([]byte{0x01, byte(len(fb))}, fb...)
		return append(img, append([]byte{0x0a, byte(len(code))}, code...)...)
	}

	if _, err := binary.DecodeModule(build(opSelect)); err != nil {
		t.Errorf("opSelect (%#x) alone does not decode, so it is not the immediate-less form: %v",
			opSelect, err)
	}
	if _, err := binary.DecodeModule(build(opSelectT)); err == nil {
		t.Errorf("opSelectT (%#x) alone decodes, so it takes no result vector — the two constants "+
			"are assigned the wrong way round", opSelectT)
	}
	if _, err := binary.DecodeModule(build(opSelectT, 0x01, 0x7f)); err != nil {
		t.Errorf("opSelectT with a one-type vector does not decode: %v", err)
	}
}

// TestBranchToMissingLabelIsTheLayeringDebt pins the direction a validator would own.
//
// A branch past the outermost label is `unknown label` — #9's verdict — and this package must
// report it as `ErrNotValidated` rather than under the spec string, for `needNum`'s reason. What
// makes the row worth having is that the *boundary* is off by one from the naive reading: with two
// explicit labels in scope, `br 2` is the implicit function label and is **legal**, so the first
// illegal depth is 3.
func TestBranchToMissingLabelIsTheLayeringDebt(t *testing.T) {
	src := `(module (func (export "c") (result i32) (block (block (br 3)))))`
	out, err := encodeDecodeInvoke(src)
	if err == nil {
		t.Fatalf("br 3 with two labels in scope was accepted, returning %v", out)
	}
	if !strings.Contains(err.Error(), "reached the interpreter unvalidated") {
		t.Errorf("got %v, want the layering debt", err)
	}
}

// TestBranchToTheImplicitFunctionLabelIsLegal is the accept-direction half of the row above, and
// it is here as its own test because the two directions have different oracles.
//
// The suite cannot fail this: `br 2` inside two blocks is a valid module, so every vector using it
// is one the harness expects to *pass*, and an engine rejecting it scores exactly the same as one
// that never saw it. Only a positive assertion catches it — the shape §9 G-3 names.
func TestBranchToTheImplicitFunctionLabelIsLegal(t *testing.T) {
	src := `(module (func (export "c") (result i32) (block (block (i32.const 21) (br 2))) (unreachable)))`
	out, err := encodeDecodeInvoke(src)
	if err != nil {
		t.Fatalf("br 2 with two labels in scope was rejected: %v", err)
	}
	if len(out) != 1 || int32(uint32(out[0].Bits)) != 21 {
		t.Errorf("got %v, want [21]", out)
	}
}
