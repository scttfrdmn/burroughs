// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package interp

import (
	"fmt"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// One constant-expression evaluator, four call sites (#241).
//
// # What it replaces, and why the replacement is a deletion rather than a widening
//
// Before this file there were two: `constExprValue`, which ran an offset through the interpreter
// and hard-coded its result to *one numeric slot*, and `constExprRef`, which pattern-matched an
// element expression against exactly `[ref.null, END]` and `[ref.func x, END]` and reported
// everything else unsupported. The second was deleted rather than extended, because *a two-pattern
// matcher is not a subset of an evaluator — it is a different thing that happened to agree on two
// inputs*, and keeping it as a fast path would leave a second opinion about a question the
// interpreter now answers (grave #105's shape: two places knowing the same fact).
//
// Its retirement condition was pre-registered at its own definition site — "retired by #7's
// reference opcodes, and the tell that the time has come is `ref` values appearing on the stack at
// all" — and #172's rung 1 met it: `opRefNull` and `opRefFunc` have had arms in `exec.go` since
// #196/#197, so running `ref.func 0` now leaves a reference to pop instead of a stack shortfall.
//
// # The arity is derived from the declared type, never assumed (#239)
//
// `constExprValue`'s `1` was correct for every type it had ever been handed and wrong for the one
// it had not: a `v128` occupies **two** adjacent `st.num` slots (decision 0024), so a module with a
// `(global v128 (v128.const …))` failed the arity check at *instantiation* and took every vector in
// its file with it. That was grave #239, and it shipped in v0.3.0's default lane, SIMD having gone
// default-on in #233. The fix is not "also allow 2" — it is to ask `countByArray`, which is the
// function every other arity site in this package already asks, so the domain grows with
// `binary.ValType` rather than with this file's imagination. *Derive the domain, never enumerate
// it.*
//
// # The site string is the caller's, because the error is about the caller's construct (#240)
//
// `constExprValue`'s message said "a data segment's offset" from all three of its callers, so an
// element segment's offset and a global initializer both reported themselves as data segments —
// the wrong-layer tell (grave #36's class: an error naming a field the input never contained),
// except manufactured here rather than leaked from below. Every entry point below takes a
// pre-rendered `site`, `funcRefTarget`'s pattern in `call.go` and for its reason: the string is the
// caller's knowledge and the callee has no way to recover it.
//
// The four sites, which is one more than #241 counted: a data segment's offset, an element
// segment's offset, an element *expression* (a different construct on a different line of the
// user's module than the offset above it), and a global initializer.

// constVal is a constant expression's result in whichever of the three shapes a Wasm value has: one
// numeric slot, two numeric slots, or a reference.
//
// **Not a tagged union, and not `any`.** Which field is live is decided by the `binary.ValType` the
// caller asked for — the same rule `global` follows for its own storage, for the same reason a
// global cannot inspect its slot to find out: a null reference and the integer zero are the same
// bits. A caller that asked for an `i32` reads `lo`; one that asked for a `v128` reads `lo` and
// `hi`; one that asked for a reftype reads `ref`. Reading the wrong field yields a zero, which is
// why `runConst` refuses to return a stack whose shape disagrees with the request instead of
// leaving the mismatch for a caller to notice.
type constVal struct {
	lo, hi uint64
	ref    ref
}

// constExpr evaluates a constant expression whose result has the declared type `want`.
//
// **Evaluated against the instance as it stands**, which is what makes `newGlobal`'s caller
// ordering load-bearing: `eval.ml:1206`'s `init_global` folds over the globals in index order and
// evaluates each against the *partially built* instance.
//
// The expression runs through the full interpreter rather than being matched, because the
// reference's const production *is* the instruction grammar (`decode.ml:983`). That is why
// `ErrConstExprRequired` is a declared layering debt (#9) rather than a grammar rule enforced here:
// this engine evaluates `(global i32 (i32.add (i32.const 1) (i32.const 2)))` and gets 3, where the
// spec would have rejected the module. Accepting more than the grammar allows is #9's absence
// showing, and it is reported as such at the one place that can see it — never papered over with a
// pattern match, which would make the same module *silently wrong* instead of honestly permissive.
func (in *Instance) constExpr(expr []binary.Instr, want binary.ValType, site string) (constVal, error) {
	numWant, refWant := countByArray([]binary.ValType{want})
	st, err := in.runConst(expr, numWant, refWant, site)
	if err != nil {
		return constVal{}, err
	}
	switch {
	case refWant == 1:
		return constVal{ref: st.popRef()}, nil
	case numWant == 2:
		// `popV128` returns (hi, lo) — `pushV128`'s own order is hi then lo, so lo is the
		// stack's true top. Named rather than positional here because a silent transposition
		// of a v128's halves is a wrong answer no arity check can see.
		hi, lo := st.popV128()
		return constVal{lo: lo, hi: hi}, nil
	default:
		return constVal{lo: st.popNum()}, nil
	}
}

// constAddr evaluates an active segment's offset expression to an address.
//
// Separate from constExpr because an offset has **no declared `binary.ValType` in the internal
// form**: the wire carries the expression and the memory's or table's index type decides whether it
// is `i32` or `i64`, and both are one numeric slot, so this asks for one slot without naming a type
// it has not resolved. Inventing an `i32` here would be an assertion about memory64 that this
// function is not in a position to make; the honest shape is to ask for the arity, which is what
// both answers share.
func (in *Instance) constAddr(expr []binary.Instr, site string) (uint64, error) {
	st, err := in.runConst(expr, 1, 0, site)
	if err != nil {
		return 0, err
	}
	return st.popNum(), nil
}

// runConst runs a constant expression and returns its stack, having checked that the stack's shape
// is the one `numWant`/`refWant` asked for.
//
// **Both arrays are checked, not just the numeric one.** `constExprValue` tested `len(st.num) != 1`
// and said nothing about `st.refs`, which was harmless while nothing could push a reference and is
// not harmless now: `(global i32 (ref.null func))` would have left one reference and zero numbers,
// failed the numeric check by luck, and reported a count rather than the mismatch. A shape check
// that reads one half of a two-array stack is the vacuity failure with a partner to hide behind.
//
// The arity is also passed to `in.run` as the implicit label's, which is what a `return` inside a
// const-expr truncates to — legal, `decode.ml:983`'s const production being the full grammar.
//
// `in.run`'s own error is returned **unwrapped**. The site belongs on the message this function
// renders and not on one it merely relays: a `*Trap` from a const-expr must stay a `*Trap` for the
// instantiation path to read it, and appending a parenthetical would change an oracle-covered trap
// string into a near-miss (`refop.go`'s note on which trap texts the suite matches verbatim).
func (in *Instance) runConst(expr []binary.Instr, numWant, refWant int, site string) (*stack, error) {
	fn := &binary.Func{Body: expr}
	st := &stack{num: make([]uint64, 0, max(numWant, len(expr)))}
	if err := in.run(fn, nil, st, numWant, refWant); err != nil {
		return nil, err
	}
	if len(st.num) != numWant || len(st.refs) != refWant {
		return nil, fmt.Errorf("%w: %s left %d numeric and %d reference values on the stack, "+
			"but its declared type wants %d and %d",
			ErrNotValidated, site, len(st.num), len(st.refs), numWant, refWant)
	}
	return st, nil
}
