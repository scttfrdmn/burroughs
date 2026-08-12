// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package interp

import (
	"errors"
	"strings"
	"testing"
)

// The frame-base controls — grave #251.
//
// # Why this needs a control at all, when the board is green
//
// `returnFrom` truncated the **shared** value stack to the callee's result arity, so a caller with
// operands already on the stack had them destroyed by its callee's return: `(i32.add (i32.const 100)
// (call $f))` where `$f` returns through an explicit `return` refused a **valid** module. That is the
// accept direction (§9 G-3), which the suite scores green by construction — a rejection corpus has no
// vector for "this module should have worked" — and it was measured invisible rather than assumed:
// zero of the all-on lane's 101 bucket keys carried `invoke`'s arity signature. So nothing but a
// control of this shape will ever see it, which is why the fix landed with one.
//
// # Scoped to the paths, not to the two rows that found it
//
// The probe on #251 used an explicit `return` and a `br` to the outermost label. Both are *one* of
// nine sites that reach `returnFrom`, and a control scoped to what the probe happened to try inherits
// the probe's blind spot (the scope-controls-to-the-space rule). The rows below therefore enumerate
// the reaching arms — `return`, `br`/`br_if`/`br_table` to the implicit function-body label,
// `br_on_null`/`br_on_non_null` to it, and a `try_table` catch clause whose label *is* the body — plus
// the fall-off-end path that reaches `invoke`'s arity check without going through `returnFrom` at all.
// That last one is the partition's negative: it was correct before the fix and must stay correct
// after, so a repair that "fixed" it by loosening the arity check instead of by finding the base
// fails here rather than passing quietly.
//
// # The witness is an answer, never an error string
//
// Every row reads a **value** that could only be right if the caller's operands survived — 107, 114,
// 8, 100 — rather than asserting that some particular message is absent. A row keyed on the refusal
// text would pass the day the message is reworded and would say nothing about whether the arithmetic
// is right; a row keyed on the *answer* fails in both directions. This is also what makes the two-
// operand row worth its place beside the one-operand row: under the defect the first reports a
// plausible `left 0 numeric` and the second an impossible `left -1 numeric`, and it is the second that
// convicts the model rather than the measurement.
//
// Every row was watched fail against the pre-fix `returnFrom` (`copy(st.num, st.num[len(st.num)-
// results:]); st.num = st.num[:results]`), which is this file's own falsification: the controls were
// born red on the defect they name and went green on the base being threaded.

// callerOperandsSurvive is the row shape: a callee `$f` that leaves its result by some path, and a
// caller `c` that has operands pending on the stack across the call.
//
// One helper rather than nine hand-written modules because the *only* thing that varies is the
// callee's body — keeping the caller fixed is what makes a broken path read as one row moving rather
// than as nine unrelated numbers, `brTableSwitch`'s own reason in control_test.go.
func callerOperandsSurvive(calleeBody string) string {
	return `(module
	  (func $f (result i32) ` + calleeBody + `)
	  (func (export "c") (result i32) (i32.add (i32.const 100) (call $f))))`
}

// TestCallerOperandsSurviveEveryReturnPath is grave #251's control: the caller's pending operands
// outlive the callee's return, whichever arm performs it.
//
// The want is 107 for every numeric row — `100 + 7` — so a row that reads 7 has had the 100 discarded
// and a row that errors has had the stack truncated below the caller's own base. Both failure modes
// were observed on the pre-fix engine.
func TestCallerOperandsSurviveEveryReturnPath(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			// opReturn (exec.go's `case opReturn`) — the arm the probe found it on.
			name: "explicit return",
			body: `(return (i32.const 7))`,
		},
		{
			// opBr with `depth == len(ctrl)`: a branch to the implicit function-body label is a
			// return, and it reaches the same truncation by a different arm. Not a duplicate of
			// the row above — `opReturn` and `opBr`'s body case are two call sites, and a fix
			// applied to one would leave the other wrong.
			name: "br to the function body",
			body: `(i32.const 7) (br 0)`,
		},
		{
			// opBrIf's taken edge. Its *untaken* edge reaches no truncation at all, which is why
			// the condition is a constant 1: this row is about the taken path only.
			name: "br_if to the function body, taken",
			body: `(i32.const 7) (i32.const 1) (br_if 0)`,
		},
		{
			// opBrTable, table entry. The `br_table.wast` loop row's lesson applies to writing
			// this one rather than to reading it: arrange the *entry* rather than the default so
			// a wrong engine answers wrongly instead of running off somewhere.
			name: "br_table to the function body, table entry",
			body: `(i32.const 7) (i32.const 0) (br_table 0 0)`,
		},
		{
			// opBrTable, default edge — a separate index computation in the same arm, so a
			// default that resolved to a different label would pass the row above and fail here.
			name: "br_table to the function body, default",
			body: `(i32.const 7) (i32.const 9) (br_table 0 0)`,
		},
		{
			// The negative half of the partition: falling off the end reaches `invoke`'s arity
			// check **without** passing through `returnFrom`. It was already correct, and it is
			// here so that a repair which loosened the arity check instead of finding the base
			// fails a row rather than passing quietly.
			name: "falls off the end (no returnFrom on this path)",
			body: `(i32.const 7)`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := run1(t, callerOperandsSurvive(tc.body))
			if len(out) != 1 {
				t.Fatalf("got %d results, want 1", len(out))
			}
			if got := out[0].Int32(); got != 107 {
				t.Errorf("got %d, want 107 — the caller's pending 100 did not survive the callee's return", got)
			}
		})
	}
}

// TestTwoPendingOperandsSurviveAReturn is the row whose *failure mode* is the finding, not merely
// its failure.
//
// With two operands pending across the call the pre-fix engine reported `left -1 numeric` — a stack
// cannot be one value less than empty, and a quantity outside its own domain convicts the model
// rather than the measurement. The one-operand rows above report a *plausible* `left 0 numeric` under
// the same defect, which reads as an ordinary arity disagreement and is the kind of number that gets
// argued about instead of diagnosed. Kept as its own test rather than folded into the table because
// its answer is 114 rather than 107 and because the reason it exists is about the count it produced,
// which a row comment in a table would not carry.
func TestTwoPendingOperandsSurviveAReturn(t *testing.T) {
	src := `(module
	  (func $f (result i32) (return (i32.const 7)))
	  (func (export "c") (result i32)
	    (i32.add (i32.const 100) (i32.mul (i32.const 2) (call $f)))))`
	out := run1(t, src)
	if got := out[0].Int32(); got != 114 {
		t.Errorf("got %d, want 114 (100 + 2*7) — two operands pending across a return", got)
	}
}

// TestPendingReferenceSurvivesAReturn is the reference stack's half, and it is a genuinely separate
// control rather than a symmetry exercise.
//
// `returnFrom` truncates `st.num` and `st.refs` through two independent pieces of arithmetic (0002's
// split stacks, #196/#197's two arities), so a base threaded into one and not the other is exactly
// the defect this file exists to catch, wearing the other array. `invoke`'s own arity check has the
// same shape and its comment records the same lesson from the other direction: one array can be
// exactly right while the other is wrong, and a shared counter cannot tell them apart.
//
// The answer is 8 — `7 + 1`, the 1 being `ref.is_null` on a null funcref pushed *before* the call.
// Under the defect the pending ref is wiped (`$f` declares no reference results, so `st.refs` was
// truncated to zero) and `ref.is_null` then underflows.
func TestPendingReferenceSurvivesAReturn(t *testing.T) {
	src := `(module
	  (func $f (result i32) (return (i32.const 7)))
	  (func (export "c") (result i32)
	    (ref.null func)
	    (call $f)
	    (ref.is_null)
	    (i32.add)))`
	out := run1(t, src)
	if got := out[0].Int32(); got != 8 {
		t.Errorf("got %d, want 8 (7 + ref.is_null of a pending null funcref)", got)
	}
}

// TestReturnBelowTheFrameBaseNamesWhatItIs is the third condition the base makes *speakable*, and it
// is here because the fix added a branch and a branch with no control is a claim.
//
// A callee that pops beneath its own entry height has eaten its caller's operands — `popNum` does not
// check depth by design (its own doc comment: underflow is `type mismatch`, and that verdict is #9's),
// so nothing stops it until the return. Before the base existed this arrived as `left -1 numeric`: a
// quantity outside its own domain, reported as though it were a small arity disagreement. The repair
// could have kept that shape by simply subtracting, which is why the guard is separate and why this
// row asserts the *message* rather than an answer — here the message **is** the behaviour under test,
// the module being invalid (`assert_invalid`'s territory, and this engine has no validator).
//
// The module: two operands pending in the caller, a callee that drops both and pushes one. `2 → 0 → 1`
// against a base of 2, so the return finds the stack one slot *below* the frame it is leaving.
func TestReturnBelowTheFrameBaseNamesWhatItIs(t *testing.T) {
	src := `(module
	  (func $f (result i32) (drop) (drop) (i32.const 7) (return))
	  (func (export "c") (result i32)
	    (i32.const 100)
	    (i32.const 5)
	    (call $f)
	    (i32.add)))`
	_, err := invokeErr(t, src)
	if err == nil {
		t.Fatalf("got no error, want a report: the callee consumed its caller's operands")
	}
	if !errors.Is(err, ErrNotValidated) {
		t.Errorf("got %v, want ErrNotValidated — an invalid module, not a trap and not a spec verdict", err)
	}
	// `errors.Is` cannot tell this apart from every other arity message here (they all wrap the
	// same sentinel — constexpr_test.go's own lesson about a partition sharing an error value), so
	// the discriminating text is asserted: the report must name the *base*, which is exactly what
	// distinguishes it from the ordinary shortfall two guards below it.
	if !strings.Contains(err.Error(), "below the frame's own base") {
		t.Errorf("got %q, want the below-the-base report — an underflow named as itself rather than "+
			"surfacing as a negative count", err)
	}
	// And the negative half, since a guard that reports the right thing for the wrong reason is
	// worth catching: no count in the message may be negative, which was the pre-fix tell.
	if strings.Contains(err.Error(), "-") {
		t.Errorf("got %q — a count outside its own domain is the defect this grave is named for", err)
	}
}

// TestGCBranchesToTheFunctionBodyKeepCallerOperands covers the two `gate:gc` arms that reach
// `returnFrom`, which the default-gate rows above cannot reach at all.
//
// They are in the space by the same rule that put `br_table`'s default beside its table entry:
// `opBrOnNull` and `opBrOnNonNull` are two more call sites, and a base threaded through the arms a
// default-gated test can see would leave these two wrong and green. The `br_on_non_null` row returns a
// **funcref** rather than an i32 because that is what the opcode's label typing requires — it passes
// the non-null reference to the label, so the body label must have a reference in its arity — which is
// also why it is the row that would catch a base applied to `st.num` alone.
func TestGCBranchesToTheFunctionBodyKeepCallerOperands(t *testing.T) {
	t.Run("br_on_null to the function body", func(t *testing.T) {
		src := `(module
		  (func $f (result i32) (i32.const 7) (ref.null func) (br_on_null 0) (drop) (i32.const 9))
		  (func (export "c") (result i32) (i32.add (i32.const 100) (call $f))))`
		out := runGC(t, src)
		if got := out[0].Int32(); got != 107 {
			t.Errorf("got %d, want 107", got)
		}
	})

	t.Run("br_on_non_null to the function body", func(t *testing.T) {
		// `$f` yields a funcref, so the caller's pending operand is numeric and the callee's
		// result is on the *other* stack — the crossed case, where a base applied to one array
		// only is visible.
		src := `(module
		  (func $g)
		  (func $f (result funcref) (ref.func $g) (br_on_non_null 0) (ref.null func))
		  (func (export "c") (result i32)
		    (i32.const 100)
		    (call $f)
		    (ref.is_null)
		    (i32.add)))`
		out := runGC(t, src)
		if got := out[0].Int32(); got != 100 {
			t.Errorf("got %d, want 100 (the pending 100 plus a non-null funcref)", got)
		}
	})
}

// TestCatchClauseReturningKeepsCallerOperands is the `try_table` path: a catch clause whose label
// index names the implicit function-body label, which `branchTo` reports as `IsReturn` and five
// dispatch-loop sites turn into a `returnFrom`.
//
// It is the one reaching arm whose route is an *error* path rather than a branch, so it is the row
// most likely to be missed by a fix that swept the branch arms — and five of the nine call sites are
// this one case reached from five different opcodes, which is why it earns a control of its own rather
// than a table row.
func TestCatchClauseReturningKeepsCallerOperands(t *testing.T) {
	// The tag carries an i32 so the clause's branch to the body label matches its arity: the
	// payload *is* the returned value. A parameterless tag would branch one value short of
	// `(result i32)`, which is #9's arity question and a different row than this one.
	//
	// `catch $t 0` — a catch clause's label index resolves against the scope *outside* the
	// try_table's own label (`valid.ml:581-584`, and `branchTo`'s `ctrl[:handlerIdx]`), so 0 here
	// is the function body rather than the try_table.
	src := `(module
	  (tag $t (param i32))
	  (func $f (result i32)
	    (try_table (result i32) (catch $t 0)
	      (throw $t (i32.const 7))
	      (i32.const 0)))
	  (func (export "c") (result i32) (i32.add (i32.const 100) (call $f))))`
	out, err := run1EH(t, src)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if got := out[0].Int32(); got != 107 {
		t.Errorf("got %d, want 107 — the caller's operand did not survive a catch clause's return", got)
	}
}
