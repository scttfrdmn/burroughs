// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package validate

import (
	"errors"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// The start section's falsification bill, both lanes, and the two properties the board cannot see.
//
// #413 crossed three packages — `internal/text` learned to parse, retain and emit section 8,
// `internal/validate` gained `check_start`, `internal/interp` gained the start call — so the bill is
// one table here rather than three tables in three files: the mutations are alternatives to *one*
// feature, and splitting them by package would put M1 and M8 in different files while their whole
// interest is that they cost the same number of rows for different reasons.
//
// Baseline: default 60890/56, all-on 64938/108.
//
//	M1  moduleStart never called                default 60887/59   all-on 64935/111   — -3  / -3
//	M2  moduleStart after the exports           default 60890/56   all-on 64938/108   — no change
//	M3  the index lookup's error swallowed      default 60889/57   all-on 64937/109   — -1  / -1
//	M4  the type check drops its results half   default 60889/57   all-on 64937/109   — -1  / -1
//	M5  the type check drops its params half    default 60889/57   all-on 64937/109   — -1  / -1
//	M6  the start call before the data copies   default 60882/64   all-on 64927/119   — -8  / -11
//	M7  section 8 emitted before section 7      default 60877/77   all-on 64917/129   — -13 / -9
//	M8  the emit condition is `startFunc != 0`  default 60887/59   all-on 64935/111   — -3  / -3
//	M9  moduleStart before the function bodies  default 60890/56   all-on 64938/108   — no change
//
// **Every mutation that moves a lane was re-run with its rows named**, because M1 and M8 both cost
// exactly 3 and they are not the same 3:
//
//	M1  start.wast:1, :6, :13         the three `check_start` vectors, whole
//	M3  start.wast:1                  `(module (func) (start 1))` — the lookup, alone
//	M4  start.wast:6                  `(start $main)` where `$main` has a result
//	M5  start.wast:13                 `(start $main)` where `$main` has a param
//	M6  start.wast:45, :47, :49, :74, :76, :78, linking.wast:609, linking3.wast:82
//	M8  start.wast:6, :13, :97
//
// M3, M4 and M5 partition M1 exactly, one vector each, which is the strongest shape a three-condition
// rule can have on a corpus: no condition is carried by another's vector. M8's three overlap M1's in
// two and differ in the third — `start.wast:97` is the `assert_trap (module …)` whose `(start $main)`
// names function 0, which is the case a `!= 0` emit condition drops and a `haveStart` condition
// keeps. So the two −3s are different populations, and a bill that stopped at the totals would have
// invited the reading that M8 is M1 reached from the encoder.
//
// **M6 is the probe #413's definition of done promised, and the board is the instrument for it.**
// That definition asked for "a probe that the start call runs *after* the data copies rather than
// before"; the mutation costs 8 rows in the default lane and 11 with every gate on, six of them the
// `assert_return`s that read back the counter `start.wast:20` and `:51` increment over a `(data "A")`.
// A unit row asserting the same thing would be a second instrument for a property already witnessed
// by name, so the pre-registration is discharged by re-measuring the gap rather than by paying it —
// which is what a pre-registration of a *control* is for. The two `linking` rows in that list are
// worth reading twice: they are the pair `internal/spec/mismatch_test.go` retired this slice, so the
// registry's departure and this mutation's arrival are the same two vectors seen from opposite sides.
//
// **M7's is the only asymmetric pair in the bill, and the asymmetry is the gates', not the rule's.**
// Emitting section 8 before section 7 makes the decoder refuse every module with a start section, so
// the cost is the whole population rather than the rule's vectors — 13 default, 9 all-on. It is
// smaller with every gate on because `start0.wast`'s nine rows are already fails in the default lane
// for a different reason (the multi-memory gate, `passFloor`'s own entry) and cannot be broken twice.
//
// **M2 and M9 move nothing, so they get the unit rows below.** They are the two seams of
// `check_start`'s position — `Option.iter (check_start c) m.it.start` at valid.ml:1166, after every
// function body and before the exports — and no corpus vector carries two defects across either one.
// That is the same standing `moduleExports`' two-loop split has, and it gets the same treatment:
// an ordering the corpus cannot witness is still the rule, and the only way to hold it is to assert
// it directly.
//
// **Both were watched die, each under its own mutation and not the other's.** M2 fails
// `TestStartIsCheckedBeforeTheExports` alone (`unknown function 99` where the start refusal is owed)
// and M9 fails `TestStartIsCheckedAfterEveryFunctionBody` alone (the start refusal where the body's
// is owed). The cross-check matters more than either death: a single row asserting "the start is
// checked in the middle" would die under both and could not say which neighbour moved, and a pair
// that died together would be one assertion spelled twice.

// startFuncType is a module whose function 0 has the given type and whose start section names it.
//
// A helper rather than two literals because the rows below differ in exactly one field each, and a
// hand-copied module is where a fixture stops testing what its name says.
//
// **The body is `end` and not empty, and the difference cost a vacuous pass.** The first draft left
// `Body` nil, which `funcBody` refuses as `1 block(s) still open at the end of the body` — so the
// ordering row below reported the *body's* refusal no matter where `moduleStart` sat, and the M9 row
// passed for a reason that had nothing to do with M9. A fixture that is invalid in a way the test is
// not about answers every ordering question with the same wrong answer.
func startFuncType(params, results []binary.ValType) *binary.Module {
	return &binary.Module{
		Types: []binary.CompType{{
			Kind: binary.CompFunc,
			Func: binary.FuncType{Params: params, Results: results},
		}},
		Funcs:    []binary.Func{{TypeIndex: 0, Body: []binary.Instr{{Op: opEnd}}}},
		Start:    0,
		HasStart: true,
	}
}

// TestStartIsCheckedBeforeTheExports is the M2 row: `check_start` runs before `check_export`
// (valid.ml:1166 against :1168-1169), so on a module carrying both defects the start wins.
//
// Moving `moduleStart` below `moduleExports` moves neither lane, which is why this is a unit row.
func TestStartIsCheckedBeforeTheExports(t *testing.T) {
	m := startFuncType([]binary.ValType{binary.I32}, nil)
	m.Exports = []binary.Export{{Name: "a", Kind: binary.ExternFunc, Index: 99}}
	_, err := Module(m)
	switch {
	case err == nil:
		t.Fatal("Module accepted a module with a parameterized start function and an out-of-scope " +
			"export index — two defects, neither reported")
	case errors.Is(err, ErrUnknownFunc):
		t.Errorf("Module reported %v, want the start refusal: the export phase ran first, which "+
			"is the order valid.ml:1166 puts before :1168-1169", err)
	case !errors.Is(err, ErrStartFunction):
		t.Errorf("Module reported %v, want ErrStartFunction", err)
	}
}

// TestStartIsCheckedAfterEveryFunctionBody is the M9 row, the same seam from the other side: the
// bodies are checked before `check_start`, so on a module with an ill-typed body *and* a bad start
// the body wins.
//
// Both directions are asserted because a single-sided ordering test passes under the mutation that
// moves the phase past *both* neighbours — and the reference's position has two neighbours.
func TestStartIsCheckedAfterEveryFunctionBody(t *testing.T) {
	m := startFuncType([]binary.ValType{binary.I32}, nil)
	// An `i32.const` left on the stack at the `end` of a `[i32] -> []` function: the body is
	// ill-typed on its own, independently of the start section naming the same function.
	m.Funcs[0].Body = []binary.Instr{{Op: 0x41}, {Op: opEnd}}
	_, err := Module(m)
	switch {
	case err == nil:
		t.Fatal("Module accepted a module with an ill-typed body and a parameterized start " +
			"function — two defects, neither reported")
	case errors.Is(err, ErrStartFunction):
		t.Errorf("Module reported %v, want the body's refusal: `check_start` ran before the body "+
			"loop, which is ahead of the position valid.ml:1166 gives it", err)
	case !errors.Is(err, ErrTypeMismatch):
		t.Errorf("Module reported %v, want the body's type mismatch", err)
	}
}
