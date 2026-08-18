// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package validate

import (
	"errors"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// tailCallOn is the gate the two opcodes need at the *decoder*, which is the layer none of these rows
// is about. `TailCall` is absent from `DefaultFeatures()`, so without it every module below is refused
// one layer early and every assertion here would be about `binary`.
func tailCallOn(f *binary.Features) { f.TailCall = true }

// tailCallAndRefsOn adds what the typed-reference spellings need: `(ref func)` as a result type and
// `(ref null $t)` as a table's element type are function-references/GC syntax, and the two rows that
// witness `matchResultType` and `matchRefType` as *relations* rather than as equality cannot be
// written without them.
func tailCallAndRefsOn(f *binary.Features) { f.TailCall, f.GC = true, true }

// TestReturnCallResultsMustSatisfyTheCallersDeclaredResults is `requireTailResults` in both
// directions, and the rows that earn their place are the two that key on the *relation*.
//
// A tail call installs the callee's results as this function's, so `valid.ml:546-549` requires
// `match_resulttype c.types ts2 c.results` — a subtype relation, not equality. The corpus witnesses
// the reject direction thickly (`return_call.wast` and `return_call_indirect.wast` between them carry
// 23 rows expecting `type mismatch`), so the marginal value here is:
//
//   - the **accept** rows, which no `assert_invalid` can score (§9 G-3): a rule written as `slices.Equal`
//     passes every reject row in the corpus and fails `callee's results are a strict subtype`.
//   - the **message**, asserted by substring rather than by sentinel, because `ErrTypeMismatch` is
//     satisfied by 84% of this package's possible refusals and discriminates nothing inside the family.
//
// The direction is stated the way the reference states it and is easy to invert: the *callee's* results
// must satisfy the *caller's* declaration, so returning `(ref func)` where `funcref` was promised is
// valid and the reverse is not. Both are rows below, and a rule with the arguments swapped passes
// neither.
func TestReturnCallResultsMustSatisfyTheCallersDeclaredResults(t *testing.T) {
	for _, c := range []struct {
		name string
		wat  string
		// msg is the substring the refusal must carry, and it defaults to the require's own message
		// for every row whose subject is that require. The one row that overrides it is the argument
		// row, whose refusal comes from the operand pop — stated per row rather than checked as one
		// sentinel, because `ErrTypeMismatch` is satisfied by most of this package's refusals and a
		// row refused for the wrong reason would pass a sentinel check while asserting nothing.
		msg   string
		valid bool
		gate  func(*binary.Features)
	}{
		{
			name:  "equal result types",
			wat:   `(module (func $g (result i32) (i32.const 1)) (func (result i32) (return_call $g)))`,
			valid: true,
			gate:  tailCallOn,
		},
		{
			name:  "callee returns a different numeric type",
			wat:   `(module (func $g (result f32) (f32.const 1)) (func (result i32) (return_call $g)))`,
			valid: false,
			gate:  tailCallOn,
		},
		{
			// The relation, in the direction it holds. `(ref func)` <: `funcref`, so a function
			// promising `funcref` may tail-call one returning the non-nullable form.
			name:  "callee's results are a strict subtype of the caller's",
			wat:   `(module (elem declare func $g) (func $g (result (ref func)) (ref.func $g)) (func (result funcref) (return_call $g)))`,
			valid: true,
			gate:  tailCallAndRefsOn,
		},
		{
			// And in the direction it does not. This row is what a rule with the two arguments
			// transposed fails — a transposition the row above cannot see, because a relation checked
			// backwards still accepts equal types.
			name:  "caller declares the subtype and the callee returns the supertype",
			wat:   `(module (elem declare func $h) (func $h (result funcref) (ref.func $h)) (func (result (ref func)) (return_call $h)))`,
			valid: false,
			gate:  tailCallAndRefsOn,
		},
		{
			// A void function tail-calling one that returns something: the arity half of the same
			// require, which a rule comparing only the types pairwise over the shorter list accepts.
			name:  "void caller, value-returning callee",
			wat:   `(module (func $g (result i32) (i32.const 1)) (func (return_call $g)))`,
			valid: false,
			gate:  tailCallOn,
		},
		{
			// The **arguments**, and this row was added because the falsification bill said it was
			// missing: deleting `popExpectAll` from `returnCall` left every test in this package green
			// and was caught only by the board. A tail call whose parameters are never popped accepts
			// any operand stack, because `setUnreachable` then excuses whatever is left on it — so the
			// two halves of the arm cover for each other's absence and only a row like this one
			// separates them.
			name:  "argument of the wrong type",
			wat:   `(module (func $g (param i32)) (func (f32.const 1) (return_call $g)))`,
			msg:   "instruction requires [i32] but stack has [f32]",
			valid: false,
			gate:  tailCallOn,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			want := c.msg
			if want == "" {
				want = "current function requires result type"
			}
			_, err := validated(t, c.wat, c.gate)
			switch {
			case c.valid && err != nil:
				t.Errorf("valid module refused: %v\n%s\nAn over-rejection, which the board cannot "+
					"see: every `assert_invalid` vector is satisfied by any refusal, so this row is "+
					"the only witness that the rule is a relation and not an equality.", err, c.wat)
			case !c.valid && err == nil:
				t.Errorf("invalid module accepted\n%s\nThe callee's results become this function's "+
					"with no frame in between, so a mismatch here is a function returning a type its "+
					"signature does not declare.", c.wat)
			case !c.valid && !errors.Is(err, ErrTypeMismatch):
				t.Errorf("refused with the wrong sentinel: want %v, got %v", ErrTypeMismatch, err)
			case !c.valid && !strings.Contains(err.Error(), want):
				t.Errorf("refused, but not with %q: %v\nThe rows are keyed on the message because the "+
					"sentinel is shared by most of this package's refusals, and a row failing on a "+
					"different check would pass a sentinel test while asserting nothing about the rule "+
					"it names.", want, err)
			}
		})
	}
}

// TestReturnCallReadsTheFunctionIndexSpace is the one row in this file whose subject is an index space
// rather than a type rule, and it is here because the mistake it catches is invisible to the corpus.
//
// `valid.ml:544-550` reads `func c x` — the **function** index space, imports interleaved. The sibling
// `call_ref`/`return_call_ref` pair takes a *type* index, and the two spaces coincide in almost every
// module anyone writes by hand, including almost every module in the corpus. The row below is a module
// where they disagree: with one imported function of type `$b` and one local of type `$a`, function
// index 1 is the local and type index 1 is `$b`, so a validator reading the wrong space refuses a valid
// module and reports a result-type mismatch that names types the caller never mentioned.
//
// Nothing in `return_call.wast` is that module, which is why the accept direction is asserted here.
func TestReturnCallReadsTheFunctionIndexSpace(t *testing.T) {
	const src = `(module
		(type $a (func (result i32)))
		(type $b (func (result f32)))
		(import "m" "f" (func (type $b)))
		(func $g (type $a) (i32.const 1))
		(func (result i32) (return_call $g)))`

	if _, err := validated(t, src, tailCallOn); err != nil {
		t.Errorf("valid module refused: %v\n%s\nThe two index spaces disagree in this module by "+
			"construction: a refusal here is `funcType` where `funcTypeAt` belongs, and the corpus "+
			"cannot tell the two apart.", err, src)
	}

	// The other half of the same lookup, and the reject sentinel it must produce — two of the 28
	// reject rows expect `unknown function`, so this one *is* corpus-witnessed and is here to hold the
	// sentinel rather than the space.
	if _, err := validated(t, `(module (func (return_call 7)))`, tailCallOn); !errors.Is(err, ErrUnknownFunc) {
		t.Errorf("a return_call to function 7 in a one-function module: want %v, got %v", ErrUnknownFunc, err)
	}
}

// TestReturnCallGoesPolymorphicAfterItsArguments is `-->...` — the polymorphic tail both arms end with.
//
// Control does not return from a tail call, so the reference's stack type is polymorphic and the frame
// is unreachable for whatever follows. The accept row is the witness: `(drop)` after a `return_call`
// pops from an empty operand stack, which is valid **only** in an unreachable frame. Omitting
// `setUnreachable` accepts nothing extra — so no reject row can see it — and refuses this module.
//
// The corpus's `return_call.wast` rows are thickest on the reject side of the same fact, which is why
// this is stated as an accept row here rather than trusted to them.
func TestReturnCallGoesPolymorphicAfterItsArguments(t *testing.T) {
	for _, src := range []string{
		`(module (func $g) (func (return_call $g) (drop)))`,
		`(module (type $t (func)) (table 1 funcref) (func (return_call_indirect (type $t) (i32.const 0)) (drop)))`,
	} {
		if _, err := validated(t, src, tailCallOn); err != nil {
			t.Errorf("valid module refused: %v\n%s\nA `drop` on an empty stack types only in an "+
				"unreachable frame, so this refusal is a missing `setUnreachable` — which over-rejects "+
				"and therefore cannot appear in any `assert_invalid` column.", err, src)
		}
	}
}

// TestIndirectTailCallReadsTheTableItCallsThrough is `indirectTarget`, which is shared with
// `call_indirect` — so this is also grave #390's regression witness.
//
// `valid.ml:560-565` requires the table's element type to match `(ref null func)`. The landed
// `call_indirect` read the table for its address type and never asked what it held, so
// `(table 10 externref)` was accepted through both arms. The corpus has that vector for each opcode —
// `call_indirect.wast:994` and `return_call_indirect.wast:571` — and the first was an admission on the
// board until this slice, which is the honest account of how long the hole was open.
//
// What the corpus does *not* have is the accept direction: a table whose element type is a **subtype**
// of `funcref`. `matchRefType` and not equality is the difference, and a rule written as `==` passes
// both reject vectors and refuses the `(ref null $ft)` row below.
func TestIndirectTailCallReadsTheTableItCallsThrough(t *testing.T) {
	for _, c := range []struct {
		name  string
		wat   string
		valid bool
		gate  func(*binary.Features)
	}{
		{
			name:  "return_call_indirect through a table of externref",
			wat:   `(module (type $t (func)) (table 10 externref) (func (return_call_indirect (type $t) (i32.const 0))))`,
			valid: false,
			gate:  tailCallOn,
		},
		{
			// #390's own row: the same require, at the site that was missing it.
			name:  "call_indirect through a table of externref",
			wat:   `(module (type $t (func)) (table 10 externref) (func (call_indirect (type $t) (i32.const 0))))`,
			valid: false,
			gate:  tailCallOn,
		},
		{
			name:  "return_call_indirect through a table of a concrete funcref subtype",
			wat:   `(module (type $ft (func)) (table 1 (ref null $ft)) (func (return_call_indirect (type $ft) (i32.const 0))))`,
			valid: true,
			gate:  tailCallAndRefsOn,
		},
		{
			name:  "call_indirect through a table of a concrete funcref subtype",
			wat:   `(module (type $ft (func)) (table 1 (ref null $ft)) (func (call_indirect (type $ft) (i32.const 0))))`,
			valid: true,
			gate:  tailCallAndRefsOn,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := validated(t, c.wat, c.gate)
			switch {
			case c.valid && err != nil:
				t.Errorf("valid module refused: %v\n%s\n`match_reftype c.types t (Null, FuncHT)` is a "+
					"relation: a table of `(ref null $ft)` holds functions, and an equality check "+
					"refuses it while passing both reject rows above.", err, c.wat)
			case !c.valid && err == nil:
				t.Errorf("invalid module accepted\n%s\nThis is grave #390 exactly: a call through a "+
					"table that does not hold functions, accepted because the arm read the table's "+
					"address type and never its element type.", c.wat)
			case !c.valid && !strings.Contains(err.Error(), "table has element type"):
				t.Errorf("refused, but not by the element-type require: %v\nA row that fails on the "+
					"index operand or the type lookup instead would keep this test green while the "+
					"require it names was gone.", err)
			}
		})
	}
}

// TestIndirectTailCallImmediatesAndOperandOrder covers the rest of `returnCallIndirect`: the two
// immediates' lookups, the index operand's width, and the order the stack is popped in.
//
// Every reject row here is corpus-witnessed — `unknown type` twice, `unknown table` once, and the
// width and order rows through `type mismatch` — so what this test adds is the *keying*: each row
// names which of the four failures it is, where the board records only that something was refused. The
// i64-table rows are the exception and are the reason the table below has four table-width rows rather
// than one: the corpus has no 64-bit-table vector for this opcode, so `i32 index on a 64-bit table` is
// the only thing anywhere that objects to hardcoding i32 in this arm — the same single-row exposure
// `TestCallIndirectIndexTypeComesFromTheTable` records for the sibling opcode (#343 cause 2).
func TestIndirectTailCallImmediatesAndOperandOrder(t *testing.T) {
	mem64 := func(f *binary.Features) { f.TailCall, f.Memory64 = true, true }
	for _, c := range []struct {
		name string
		wat  string
		// want is the sentinel, or nil for an accept row.
		want error
		gate func(*binary.Features)
	}{
		{
			name: "unknown type index",
			wat:  `(module (table 1 funcref) (func (return_call_indirect (type 7) (i32.const 0))))`,
			want: ErrUnknownType,
			gate: tailCallOn,
		},
		{
			name: "no table at all",
			wat:  `(module (type $t (func)) (func (return_call_indirect (type $t) (i32.const 0))))`,
			want: ErrUnknownTable,
			gate: tailCallOn,
		},
		{
			name: "i32 index on a 32-bit table",
			wat:  `(module (type $t (func)) (table 1 funcref) (func (return_call_indirect (type $t) (i32.const 0))))`,
			gate: tailCallOn,
		},
		{
			name: "i64 index on a 32-bit table",
			wat:  `(module (type $t (func)) (table 1 funcref) (func (return_call_indirect (type $t) (i64.const 0))))`,
			want: ErrTypeMismatch,
			gate: tailCallOn,
		},
		{
			name: "i64 index on a 64-bit table",
			wat:  `(module (type $t (func)) (table i64 1 funcref) (func (return_call_indirect (type $t) (i64.const 0))))`,
			gate: mem64,
		},
		{
			// The row nothing else in the repository asserts for this opcode.
			name: "i32 index on a 64-bit table",
			wat:  `(module (type $t (func)) (table i64 1 funcref) (func (return_call_indirect (type $t) (i32.const 0))))`,
			want: ErrTypeMismatch,
			gate: mem64,
		},
		{
			// The index sits above the arguments, so it pops first. The parameter type is `f32`
			// deliberately: with an `i32` parameter both orders type and the row asserts nothing.
			name: "arguments below the index",
			wat:  `(module (type $t (func (param f32))) (table 1 funcref) (func (f32.const 1) (i32.const 0) (return_call_indirect (type $t))))`,
			gate: tailCallOn,
		},
		{
			name: "index below the arguments",
			wat:  `(module (type $t (func (param f32))) (table 1 funcref) (func (i32.const 0) (f32.const 1) (return_call_indirect (type $t))))`,
			want: ErrTypeMismatch,
			gate: tailCallOn,
		},
		{
			// The result require reached through the indirect arm rather than the direct one, so the
			// shared helper is witnessed at both call sites.
			name: "callee's results do not satisfy the caller's",
			wat:  `(module (type $t (func (result f32))) (table 1 funcref) (func (result i32) (i32.const 0) (return_call_indirect (type $t))))`,
			want: ErrTypeMismatch,
			gate: tailCallOn,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := validated(t, c.wat, c.gate)
			switch {
			case c.want == nil && err != nil:
				t.Errorf("valid module refused: %v\n%s", err, c.wat)
			case c.want != nil && !errors.Is(err, c.want):
				t.Errorf("want %v, got %v\n%s\nThe sentinel is the row's subject: the board records "+
					"that this module was refused and cannot say which of the four lookups did it.",
					c.want, err, c.wat)
			}
		})
	}
}
