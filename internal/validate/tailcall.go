// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package validate

import (
	"fmt"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// Slice 9 (#389, ADR 0035): the tail-call proposal's two opcodes, and the only two instructions in
// the single-byte space that were declined for want of an arm rather than by declaration.
//
// `return_call_ref` (0x15) is not here — it is the third tail-call *shape* and it landed with slice 8
// because it arrived with the function-references proposal. So one file holds two of three siblings
// and `ref.go` holds the other, which is proposal boundaries winning over family resemblance, and is
// stated because the reverse arrangement is the one a reader expects.
//
// **The validator was the sole blocker for both**, which is unusual enough in this campaign to be the
// reason the slice is small: `internal/interp` executes them as real tail calls (`exec.go:502`, ADR
// 0026 / #253), the decoder reads them under `gateTailCall`, and the wat encoder emits them. This is
// the shape ADR 0025's G-1 carve-out names — a vector whose only obstacle is the deferred validator —
// arriving from the direction that retires the carve-out rather than widening it.
const (
	opReturnCall         = 0x12
	opReturnCallIndirect = 0x13
)

// requireTailResults is the condition that makes a tail call a tail call, and it is the whole of what
// separates these arms from `call`/`call_indirect` (`valid.ml:546-549`, `:566-569`):
//
//	require (match_resulttype c.types ts2 c.results) e.at
//	  ("type mismatch: current function requires result type " ^ … ^
//	   " but callee returns " ^ …)
//
// The callee's results become *this* function's results with no frame in between, so they have to
// satisfy what this function promised its own caller. `matchResultType` and not equality: a callee
// returning `(ref $t)` may be tail-called from a function declaring `funcref`, and the relation is
// where that is decided.
//
// **`c.results` is `v.frames[0].labelTypes`**, read from the body frame rather than re-resolved from
// the module, for `opReturn`'s and `returnCallRef`'s reason — the frame was built from the declared
// results, so a second lookup would be a second derivation of a fact one frame already holds.
//
// Shared by both arms rather than written twice, because the reference's two copies are textually
// identical and a message that drifts between two call sites is two claims about one rule.
// `returnCallRef` in `ref.go` keeps its own copy for now: it is a landed slice's arm, and folding it
// in is a refactor of tested behaviour rather than part of this port. Stated so the third copy is a
// noticed duplication instead of an unnoticed one.
func (v *validator) requireTailResults(callee []binary.ValType) error {
	declared := v.frames[0].labelTypes
	if !matchResultType(tctx{gotMod: v.mod, wantMod: v.mod}, callee, declared) {
		return fmt.Errorf("%w: current function requires result type %s but callee returns %s",
			ErrTypeMismatch, typeList(declared), typeList(callee))
	}
	return nil
}

// indirectTarget resolves the two immediates a `call_indirect`/`return_call_indirect` carries and
// performs the require that is about the table rather than about the call.
//
// # The element-type require, which the landed `call_indirect` never had (#390)
//
// `valid.ml:560-565`:
//
//	| ReturnCallIndirect (x, y) ->
//	  let TableT (at, _lim, t) = table c x in
//	  let (ts1, ts2) = func_type c y in
//	  require (match_reftype c.types t (Null, FuncHT)) x.at
//	    ("type mismatch: instruction requires table of function type" ^
//	     " but table has element type " ^ string_of_reftype t);
//
// `callIndirect` read the table for its *address* type — that much is #343 cause 2's repair — and then
// never asked what the table *holds*, so `(table 10 externref)` with a `call_indirect` through it was
// **accepted**: `call_indirect.wast:994`, an admission sitting in the board since the arm landed. The
// shape worth carrying forward is that the same call site was corrected on one axis and left wrong on
// another: *a call site fixed for one field is not thereby right about the others*, and the reason no
// column moved is that this direction over-*accepts*, which only #341's arm can see.
//
// Both arms call this, so the require exists once. Writing it only in the new arm would have left the
// grave standing in the file whose authority proved it exists.
//
// # The lookups are in the reference's order, and the corpus cannot see the difference
//
// `table c x` runs before `func_type c y` above; the landed `callIndirect` resolved the type index
// first. The order is observable only for a module whose *both* immediates are wrong, and no vector in
// the suite is that module — `call_indirect.wast:790` and `return_call_indirect.wast:435` name a valid
// type with no table, `:1004` and `:590` name a valid table with an out-of-range type. So this is an
// unwitnessed ordering, adopted from the authority because the alternative is two arms disagreeing
// about which lookup comes first, and recorded here as unwitnessed rather than asserted as covered.
func (v *validator) indirectTarget(typeIdx, tableIdx uint32) (binary.FuncType, binary.Table, error) {
	tab, err := tableTypeAt(v.mod, tableIdx)
	if err != nil {
		return binary.FuncType{}, binary.Table{}, err
	}
	ft, err := funcType(v.mod, typeIdx)
	if err != nil {
		return binary.FuncType{}, binary.Table{}, err
	}
	if !matchRefType(tctx{gotMod: v.mod, wantMod: v.mod}, tab.ElemType, binary.FuncRef) {
		return binary.FuncType{}, binary.Table{}, fmt.Errorf(
			"%w: instruction requires table of function type but table has element type %s",
			ErrTypeMismatch, typeStr(tab.ElemType))
	}
	return ft, tab, nil
}

// returnCall is `ReturnCall x` (`valid.ml:544-550`):
//
//	| ReturnCall x ->
//	  let (ts1, ts2) = functype_of_comptype (expand_deftype (func c x)) in
//	  require (match_resulttype c.types ts2 c.results) e.at (…);
//	  ts1 -->... [], []
//
// `func c x` is the **function** index space — imports interleaved — which `funcTypeAt` resolves and
// which is the distinction `callRef`'s comment draws from the other side: the `*_ref` pair takes a
// *type* index. Reading `funcType` here would resolve the wrong space and be wrong only for modules
// where the two disagree.
//
// `-->...` is the polymorphic tail: control does not return, so the frame goes unreachable once the
// arguments are popped, exactly as `return` and `br` do. Omitting `setUnreachable` accepts nothing
// extra and rejects every `return_call` that is not the last instruction of a void frame, which is the
// direction `return_call.wast`'s rows are thickest on.
func (v *validator) returnCall(idx uint32) error {
	ft, err := v.funcTypeAt(idx)
	if err != nil {
		return err
	}
	if err := v.requireTailResults(ft.Results); err != nil {
		return err
	}
	if err := v.popExpectAll(ft.Params); err != nil {
		return err
	}
	v.setUnreachable()
	return nil
}

// returnCallIndirect is the `ReturnCallIndirect` arm (`valid.ml:560-570`), over the immediates (x, y):
//
// The arm's payload is written outside the backticks deliberately: the subject extractor in
// `citation_subject_test.go` reads a backticked *identifier*, and its character class admits spaces
// and parentheses but not a comma — so backticking the arm together with its two-immediate payload
// matches nothing at all, and a correct description lands in the excused column. That is the same failure as the unbackticked `check_elem`
// name and the dotted `peek_ref 0 s e.at` call form, in a third spelling, and it is recorded here
// because a comma inside a payload is the least visible of the three.
//
//	(ts1 @ [NumT (numtype_of_addrtype at)]) -->... [], []
//
// Three checks and one operand order, all of them somebody's grave already:
//
//   - The table's element type must be a function type — `indirectTarget`, and #390.
//   - The callee's results must satisfy this function's — `requireTailResults`.
//   - **The table index operand is at the table's address type, not `i32`.** Hardcoding i32 in
//     `callIndirect` refused four valid modules (#343 cause 2); the same field is read here through
//     the same helper, so a table64 is indexed by an i64 in both arms or in neither.
//
// The index sits *above* the arguments on the stack, so it pops first — the reverse of the written
// order, which is `popExpectAll`'s whole subject.
func (v *validator) returnCallIndirect(in binary.Instr) error {
	// Immediates in written order: type index, then table index.
	ft, tab, err := v.indirectTarget(uint32(in.Imm0), uint32(in.Imm1))
	if err != nil {
		return err
	}
	if err := v.requireTailResults(ft.Results); err != nil {
		return err
	}
	if err := v.popExpect(tableAddrType(tab)); err != nil {
		return err
	}
	if err := v.popExpectAll(ft.Params); err != nil {
		return err
	}
	v.setUnreachable()
	return nil
}
