// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package validate

import (
	"fmt"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// Slice 10 (#393, ADR 0036): the exception-handling family — `throw`, `throw_ref`, `try_table` and
// the module-level `check_tagtype` — and with it the **last three bytes of the single-byte opcode
// space**.
//
// # This slice moved a declared boundary, which is why it needed an ADR and slice 9 did not
//
// Exception handling was `validate.go`'s out-of-scope register's third-to-last entry, and slice 9's
// consequence list drew the distinction from the other side: tail calls "were never in
// `validate.go`'s out-of-scope list; they were declined for want of arms. Exception handling *is* in
// that list." The entry was accurate when written and could not say the thing that made it stale —
// **it was written when there was no validator for it to be out of scope of**, so "out of scope"
// distinguished nothing. What retired it is the arithmetic of everything around it having landed:
// once 0xFD, 0xFC, 0xFB, the reference instructions and the tail calls are typed, the entry holds
// exactly the caveat on *every single-byte opcode is typed*, and a boundary reserving a caveat is
// not reserving a slice.
//
// # Retention was complete before this slice started, and that is unusual enough to name
//
// `internal/binary` already carries `Catch`, `Catches`/`CatchVector`, the four `CatchKind` bytes,
// `HeapExn`/`HeapNoExn`, `Tag` and a tag import's type index in `Import.Index` (#199 rung 1, #204).
// `internal/interp` already *executes* the family (#199 rung 2). `ErrUnknownTag` already existed,
// declared by the export slice. So nothing was missing but the four typing rules — the same
// "validator is the sole blocker" shape slice 9 had, arriving a second time, and the reason a
// three-opcode proposal is one small file.
//
// # The falsification bill, run rather than described
//
// Nine mutations, each applied to this file or to `module.go`, each built and run against
// `internal/validate` with the board behind it. What caught each one:
//
//	mutation                                          caught by
//	--------------------------------------------------+------------------------------------------
//	clauses checked in `c'` (frame pushed first)        both rows of the enclosing-context battery
//	`popSeqExpect` pads unconditionally                the `[]`-wording row, alone
//	`catch_ref` omits the exnref                       the catch_ref accept/reject pair
//	the `ExternTagT` arm deleted                       the `tag.wast:22` row, alone
//	the index-form elem branch deleted (#391)          two rows of the elem battery
//	`throwInstr` drops `setUnreachable`                the value-returning-function row
//	`throwRef` drops `setUnreachable`                  the polymorphic-frame row
//	`tagTypeAt` ignores imported tags                  all three index-space rows
//	`ft.Params` appended to rather than copied         **nothing, anywhere**
//
// The last row is the one worth reading. It was pre-registered as vacuous at `checkCatch` below and
// the run confirms it: with the copy removed, `go test ./...` fails nothing but three citation-count
// pins unrelated to this slice, and both board lanes stay exactly where they were. Two of the other
// eight are caught by a *single* row, which is where this file is thinnest — delete either row and
// the mutation goes silent.
const (
	opThrow    = 0x08
	opThrowRef = 0x0a
	opTryTable = 0x1f
)

// exnRef is `exnref` — `RefT (Null, ExnHT)`, the operand `throw_ref` consumes.
//
// exnRefNonNull is `(ref exn)`, which is what a `catch_ref`/`catch_all_ref` handler *provides* to
// its label (`valid.ml:983,988`). The two differ in exactly the bit that makes
// `try_table.wast:386,395` invalid — a handler providing a non-null exnref to a label that takes
// none is an arity mismatch, and a handler providing one to a label that takes `exnref` is fine
// because non-null flows into nullable and not the reverse.
//
// The discarded second result is `AbstractRefType`'s "is this one of the twelve", which `HeapExn`
// is by construction; `refNullEq` in `ref.go` is the same idiom for the same reason.
var (
	exnRef, _        = binary.AbstractRefType(binary.HeapExn, true)
	exnRefNonNull, _ = binary.AbstractRefType(binary.HeapExn, false)
)

// tagTypeAt resolves a tag index to the functype its type names — `tag c x` (`valid.ml:572-575`),
// followed by `deftype_of_typeuse` and `functype_of_comptype`, which is the shape all three
// consumers want.
//
// The citation sits beside `tag c x` rather than beside the two helpers below it because the
// subject reader works a line at a time and reads only names `valid.ml` itself defines: the
// helpers are the type algebra's, defined in another file, so a range cited on their line names
// nothing readable. Noted where the wrapping is, since reflowing this paragraph is a semantic act.
//
// Across the imports-then-definitions index space, `tableTypeAt`'s walk exactly: an imported tag
// occupies tag index 0 before any defined tag does, and a tag import keeps its type index in
// `Import.Index` rather than in a descriptor of its own, which is `decodeImport`'s case `0x04`
// (#204). Returning the resolved *functype* rather than the type index is the same choice
// `funcTypeAt` makes over `funcTypeIndexAt`: the kind check — a struct type where a functype is
// wanted — then belongs to `funcType` and is common to every caller instead of being repeated at
// three of them.
func tagTypeAt(m *binary.Module, idx uint32) (binary.FuncType, error) {
	imported := m.ImportedTags()
	if int(idx) < imported {
		n := 0
		for i := range m.Imports {
			if m.Imports[i].Kind != binary.ExternTag {
				continue
			}
			if n == int(idx) {
				return funcType(m, m.Imports[i].Index)
			}
			n++
		}
	}
	if defined := int(idx) - imported; defined >= 0 && defined < len(m.Tags) {
		return funcType(m, m.Tags[defined].TypeIndex)
	}
	// `ErrUnknownTag`'s tail is `indexInScope`'s, and the reason it is spelled here rather than
	// delegated is `tableTypeAt`'s: this function answers with a *type* and `indexInScope` answers
	// with existence, so a delegation would resolve the space twice. `throw.wast:51` —
	// `(module (func (throw 0)))` — is the vector, and it wants the bare `unknown tag 0`, so
	// nothing may sit between the category and the index (0003, and the format control in
	// `authority_test.go`).
	n := uint32(imported) + uint32(len(m.Tags))
	return binary.FuncType{}, fmt.Errorf("%w %d (%d in scope)", ErrUnknownTag, idx, n)
}

// checkTagType is `check_tagtype` (`valid.ml:191-195`), the whole of it:
//
//	let TagT ut = tt in
//	let (ts1, ts2) = func_type c (idx_of_typeuse ut @@ at) in
//	require (ts2 = []) at "non-empty tag result type";
//
// A tag is a *thrown* signature, so it has parameters and no results, and this is the only rule in
// the family that is not an instruction rule.
//
// **It has two call sites and they are two phases apart**, which is the reason it is a function
// rather than three lines inside a loop: `check_tag` runs it over the defined tags (`valid.ml:1049-
// 1052`, folded at `:1157`) and `check_externtype`'s `ExternTagT` arm runs it over the *imported*
// ones (`:222-223`), which `check_import` reaches first. `tag.wast:18` and `tag.wast:22` are the two
// vectors, one per site, and a rule written at the defined site alone passes the first and admits
// the second.
//
// The parameters are not checked here, and that is the reference's own asymmetry rather than an
// omission: `func_type` has already resolved the type index, so an ill-formed parameter type is
// `check_type`'s verdict on the type section and not this phase's.
func checkTagType(m *binary.Module, typeIdx uint32) error {
	ft, err := funcType(m, typeIdx)
	if err != nil {
		return err
	}
	if len(ft.Results) != 0 {
		return fmt.Errorf("%w: type %d returns %s", ErrNonEmptyTagResult, typeIdx, typeList(ft.Results))
	}
	return nil
}

// throwInstr is `Throw x` (`valid.ml:572-576`):
//
//	let TagT ut = tag c x in
//	let dt = deftype_of_typeuse ut in
//	let (ts1, ts2) = functype_of_comptype (expand_deftype dt) in
//	ts1 -->... [], []
//
// The tag's parameters are the payload and `-->...` is the polymorphic tail: a `throw` does not fall
// through, so the frame goes unreachable once the payload is consumed, exactly as `br` and `return`
// do. `ts2` is bound and unused, and it is guaranteed empty by `checkTagType` two phases earlier —
// which is why the instruction arm can ignore it and why a module-level rule that only ran over
// *defined* tags would let an imported tag's results reach here unexamined.
func (v *validator) throwInstr(idx uint32) error {
	ft, err := tagTypeAt(v.mod, idx)
	if err != nil {
		return err
	}
	if err := v.popSeqExpect(ft.Params); err != nil {
		return err
	}
	v.setUnreachable()
	return nil
}

// throwRef is `ThrowRef` (`valid.ml:578-579`), which is the whole rule:
//
//	[RefT (Null, ExnHT)] -->... [], []
//
// One nullable `exnref` in, nothing out, frame unreachable. **`popExpect` here and `popSeqExpect` in
// `throwInstr` was this file's deliberate inconsistency, and #394 dissolved it rather than resolving
// it either way**: `popExpect` is now the one-element case of `popSeqExpect` (`stack.go`), so both
// arms produce the reference's sentence and the choice of spelling is back to being about arity. What
// the paragraph here used to record — that the two vectors pinning the wording are `throw`'s, while
// `throw_ref.wast:117,118` expect the bare `type mismatch` — is still why this arm's own rows could
// not have caught the divergence.
func (v *validator) throwRef() error {
	if err := v.popExpect(exnRef); err != nil {
		return err
	}
	v.setUnreachable()
	return nil
}

// tryTable is `TryTable`, whose arm binds (bt, cs, es) (`valid.ml:581-586`):
//
//	let InstrT (ts1, ts2, xs) as it = check_blocktype c bt e.at in
//	let c' = {c with labels = ts2 :: c.labels} in
//	List.iter (fun ct -> check_catch c ct ts2 e.at) cs;
//	check_block c' es it e.at;
//	ts1 --> ts2, …
//
// # The clauses are checked in the *enclosing* context, and that is the rule's one trap
//
// `check_catch c` takes **`c`, not `c'`**. So a clause's label index is numbered from outside the
// `try_table` it is attached to: `(func (try_table (catch_all 0)))` branches to the *function body*
// frame, not to the try_table's own. Reading it as `c'` would shift every clause's depth by one, and
// would be invisible on the idiomatic vector — a `try_table` inside a `block` whose handler targets
// that block reads plausibly under either numbering — while inverting the seven `try_table.wast`
// vectors whose handler targets the body directly.
//
// This code therefore checks the clauses **before** pushing the frame, which is the only way a
// streaming validator can spell "the enclosing context". The blocktype is resolved first regardless,
// because the reference's `check_blocktype` precedes the iteration and a `try_table` naming both a
// missing type and a bad clause reports the type.
//
// `ts2` is passed to `check_catch` and **never read** by it (`:974`, where the parameter is bound and
// the four arms all match against `label c x2` instead). So nothing about the block's results reaches
// a clause, and this arm passes nothing.
//
// # Otherwise it is a `block`, and `openBlock`'s pieces say so
//
// `ts1 --> ts2` with `labels = ts2 :: …` is exactly `block`'s frame: a `br` out of a `try_table`
// carries the block's *results*, not its parameters. `enterBlock` is shared with `openBlock` rather
// than copied, so the loop-versus-block label rule — the one whose two readings coincide on most
// modules — stays written once.
func (v *validator) tryTable(i int, in binary.Instr) error {
	params, results, err := v.blockType(in)
	if err != nil {
		return err
	}
	// A `try_table` with zero clauses is legal and means every exception falls through uncaught, so
	// the second result is not consulted: an absent vector and an empty one check identically here,
	// and `CatchVector`'s ability to tell them apart is the *interpreter's* need rather than this
	// rule's.
	clauses, _ := v.curFunc.CatchVector(i)
	for k := range clauses {
		if err := v.checkCatch(clauses[k]); err != nil {
			return fmt.Errorf("%w (catch %d)", err, k)
		}
	}
	return v.enterBlock(i, in.Op, params, results)
}

// checkCatch is `check_catch`'s four arms (`valid.ml:974-989`).
//
// Each arm computes the types the handler *hands to* its label and matches them against what that
// label takes: the tag's parameters for `catch`, those plus `(ref exn)` for `catch_ref`, nothing for
// `catch_all`, and just `(ref exn)` for `catch_all_ref`.
//
// The tag resolves **before** the label in the two arms that have both, which is the reference's
// order and decides the message for a clause that is wrong twice.
//
// # The message says which side is which, and the reference's does not
//
// `check_catch` composes it as `match_result_type "label" "catch handler"`, which renders
//
//	type mismatch: catch handler requires <the label's types> but label has <the handler's types>
//
// — the two role names swapped relative to the arguments they label. `match_stack` next door binds
// the same helper correctly (`"stack" "instruction"` over stack-then-instruction), so this is an
// upstream slip rather than a convention to transcribe. **No vector pins it**: all seven
// `try_table.wast` clause vectors expect the bare `type mismatch`, so fidelity costs nothing and
// emitting the reference's text would mean knowingly printing a sentence that misnames its own
// operands. The head `type mismatch:` is kept, since that is what the corpus matches (0003).
func (v *validator) checkCatch(cl binary.Catch) error {
	var handler []binary.ValType
	switch cl.Kind {
	case binary.CatchTag, binary.CatchTagRef:
		ft, err := tagTypeAt(v.mod, cl.TagIndex)
		if err != nil {
			return err
		}
		// **Copied, not appended to** — and the reason is weaker than the draft of this comment
		// claimed, which is why the measurement is here rather than the claim. `ft.Params` does
		// alias the module's type section, and it does carry spare capacity: `decodeCompType`
		// builds it by appending from nil, so a three-parameter tag decodes to `len 3, cap 4` and
		// a five-parameter one to `len 5, cap 8`. `append(ft.Params, exnRefNonNull)` would
		// therefore write *into the module*. But it writes into that parameter list's **own**
		// padding — each `FuncType.Params` owns its array — and the write was measured to change
		// no other type's parameters and not that type's results either. So the copy guards
		// against a decoder that packs parameter lists into one shared array, which this one does
		// not, and **no vector on either lane can witness its absence**. Recorded as vacuous
		// rather than witnessed by a test that cannot fail; the draft asserted the corruption was
		// "silent on the two-clause vectors", which named a harm nothing could observe.
		handler = make([]binary.ValType, 0, len(ft.Params)+1)
		handler = append(handler, ft.Params...)
		if cl.Kind == binary.CatchTagRef {
			handler = append(handler, exnRefNonNull)
		}
	case binary.CatchAny:
		// `match_target c [] (label c x)`: a `catch_all` hands its label nothing, so the label must
		// take nothing. `try_table.wast:399` is the vector — a label taking `exnref` refuses it.
	case binary.CatchAnyRef:
		handler = []binary.ValType{exnRefNonNull}
	default:
		// Unreachable through the decoder, which admits exactly the four wire bytes
		// (`decodeCatch`). Loud rather than a bare `return nil` for the reason `exportExists`'s
		// tail is: a fifth clause form arriving in a future proposal would otherwise read as
		// *checked* the moment the decoder learned to admit it.
		return fmt.Errorf("catch clause kind %#x is not one of check_catch's four arms", byte(cl.Kind))
	}

	f, err := v.label(cl.LabelIndex)
	if err != nil {
		return err
	}
	if len(handler) != len(f.labelTypes) {
		return catchMismatch(handler, f.labelTypes, cl.LabelIndex)
	}
	for j := range handler {
		if !v.matches(handler[j], f.labelTypes[j]) {
			return catchMismatch(handler, f.labelTypes, cl.LabelIndex)
		}
	}
	return nil
}

// catchMismatch is the one message `checkCatch` has, written once because the arity branch and the
// element branch are the same verdict about the same two sequences — `matchLabel`'s reason next
// door, where a message naming the *counts* was a false witness on the single vector the rule got
// wrong.
//
// The relation is `matches`, so `try_table.wast:470,483` are refused by *subtyping* and not by
// identity: a tag declaring `(param (ref null $t))` hands a nullable reference to a label taking
// `(ref $t)`, and nullable does not flow into non-null. Those two rows are the only ones in this
// family that need slice 5's relation, which is why they are the last two invalid vectors in the
// file.
func catchMismatch(handler, label []binary.ValType, idx uint32) error {
	return fmt.Errorf("%w: catch handler provides %s but label %d takes %s",
		ErrTypeMismatch, typeList(handler), idx, typeList(label))
}
