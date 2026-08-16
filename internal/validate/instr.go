// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package validate

import (
	"errors"
	"fmt"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// The single-byte opcodes slice 1 dispatches structurally. Everything else is either derived
// from its mnemonic (see sig.go) or declined.
//
// Named constants rather than bare hex in the switch, because a `case 0x0d:` in a 40-arm switch
// is a claim nobody checks — and `TestStructuralOpcodesMatchTheTable` asserts each of these
// against `binary.OpMnemonic`, so the names here cannot drift from the authority's table.
const (
	opUnreachable  = 0x00
	opNop          = 0x01
	opBlock        = 0x02
	opLoop         = 0x03
	opIf           = 0x04
	opElse         = 0x05
	opEnd          = 0x0B
	opBr           = 0x0C
	opBrIf         = 0x0D
	opBrTable      = 0x0E
	opReturn       = 0x0F
	opCall         = 0x10
	opCallIndirect = 0x11
	opDrop         = 0x1A
	opSelect       = 0x1B
	opSelectT      = 0x1C
	opLocalGet     = 0x20
	opLocalSet     = 0x21
	opLocalTee     = 0x22
	opGlobalGet    = 0x23
	opGlobalSet    = 0x24
)

// instrs walks one instruction sequence.
//
// The sequence is flat: `block`/`loop`/`if` open frames and `end` closes them, exactly as the
// wire has it, because 0002's internal form keeps the linear body rather than building a tree.
// So the control stack here is not an artifact of validation — it is the structure the format
// leaves implicit, and computing it is half of what FuncInfo hands forward.
func (v *validator) instrs(body []binary.Instr) error {
	for i, in := range body {
		if err := v.instr(i, in); err != nil {
			return fmt.Errorf("instr %d (%s): %w", i, mnemonic(in), err)
		}
	}
	return nil
}

func (v *validator) instr(i int, in binary.Instr) error {
	if len(v.frames) == 0 {
		// Everything below reaches for the current frame, so the empty control stack is answered
		// once, here, rather than defended at each of the dozen sites that would otherwise index
		// v.frames[-1]. Reached only by an instruction *after* the body's terminating `end`.
		return fmt.Errorf("%w: instruction after the end of the function body", ErrTypeMismatch)
	}
	if in.Prefix != 0 {
		// The prefixed regions are 0xFB (GC), 0xFC (bulk memory/table), 0xFD (SIMD), 0xFE
		// (threads). Slice 2 (#305) types 0xFD and slice 5 types 0xFC; 0xFB and 0xFE stay
		// declined — which is what keeps an unchecked module from being reported valid, and keeps
		// the decline census a work plan for the slices that own them rather than a silence.
		//
		// A region dispatch rather than one arm per region: the regions that decline decline
		// *identically*, and a copy of that refusal per region is a place to forget when the next
		// slice claims one of them.
		switch in.Prefix {
		case prefixSIMD:
			return v.vecInstr(in)
		case prefixBulk:
			return v.bulkInstr(in)
		}
		return fmt.Errorf("%w: prefixed opcode %#02x %#02x", ErrUnsupported, in.Prefix, in.Op)
	}

	switch in.Op {
	case opUnreachable:
		v.setUnreachable()
		return nil

	case opNop:
		return nil

	case opBlock, opLoop, opIf:
		return v.openBlock(i, in)

	case opElse:
		return v.elseArm()

	case opEnd:
		return v.endBlock()

	case opBr:
		f, err := v.label(uint32(in.Imm0))
		if err != nil {
			return err
		}
		if err := v.popExpectAll(f.labelTypes); err != nil {
			return err
		}
		v.setUnreachable()
		return nil

	case opBrIf:
		f, err := v.label(uint32(in.Imm0))
		if err != nil {
			return err
		}
		// The condition is on top, above the branch's own operands.
		if err := v.popExpect(binary.I32); err != nil {
			return err
		}
		if err := v.popExpectAll(f.labelTypes); err != nil {
			return err
		}
		// Not taken: the operands are still there for whatever follows.
		v.pushAll(f.labelTypes)
		return nil

	case opBrTable:
		return v.brTable(i, in)

	case opReturn:
		// The function's results are the outermost frame's label types, which is what makes
		// `return` and a `br` to the body frame the same check rather than two.
		if err := v.popExpectAll(v.frames[0].labelTypes); err != nil {
			return err
		}
		v.setUnreachable()
		return nil

	case opCall:
		ft, err := v.funcTypeAt(uint32(in.Imm0))
		if err != nil {
			return err
		}
		if err := v.popExpectAll(ft.Params); err != nil {
			return err
		}
		v.pushAll(ft.Results)
		return nil

	case opCallIndirect:
		return v.callIndirect(in)

	case opDrop:
		if _, ok := v.pop(); !ok {
			return fmt.Errorf("%w: drop on an empty stack", ErrTypeMismatch)
		}
		return nil

	case opSelect:
		return v.selectUnannotated()

	case opSelectT:
		return v.selectAnnotated(i)

	case opLocalGet, opLocalSet, opLocalTee:
		return v.localOp(in)

	case opGlobalGet, opGlobalSet:
		return v.globalOp(in)
	}

	// Not structural: the numeric, comparison, conversion and memory-access families, whose
	// signatures come from the mnemonic rather than from a hand-written row here.
	s, err := signature(v.mod, in)
	if err != nil {
		return err
	}
	if err := v.popExpectAll(s.params); err != nil {
		return err
	}
	v.pushAll(s.results)
	return nil
}

// openBlock handles `block`, `loop` and `if`.
func (v *validator) openBlock(i int, in binary.Instr) error {
	params, results, err := v.blockType(in)
	if err != nil {
		return err
	}

	if in.Op == opIf {
		// The condition sits above the block's parameters.
		if err := v.popExpect(binary.I32); err != nil {
			return err
		}
	}
	// The parameters move from the enclosing stack into the new frame.
	if err := v.popExpectAll(params); err != nil {
		return err
	}

	labelTypes := results
	if in.Op == opLoop {
		// A branch to a loop re-enters it, so it carries the loop's *parameters*. Getting this
		// wrong is invisible on every block-shaped vector and on every loop whose params and
		// results coincide, which is most of them.
		labelTypes = params
	}

	v.pushFrame(in.Op, labelTypes, results)
	v.top().params = params
	v.pushAll(params)

	v.blocks[i] = Arity{Label: len(labelTypes), End: len(results)}
	return nil
}

// blockType resolves an opener's block type to its parameters and results.
func (v *validator) blockType(in binary.Instr) (params, results []binary.ValType, err error) {
	idx, vt, empty := binary.BlockType(in.Imm0, in.Imm1)
	switch {
	case empty:
		return nil, nil, nil
	case idx == 0 && vt != binary.ValType{}:
		// `check_blocktype`'s valtype arm (`valid.ml:420`: `ValBlockType (Some t) -> check_valtype
		// c t at`). Without this the annotation is returned unchecked, so `(block (result (ref 1)))`
		// types successfully against a module declaring no type 1 — an accept-direction gap, which
		// is why no default-lane vector could see it (#311).
		//
		// The indexed arm below needs no such call: `funcType` resolves the index and reports
		// `unknown type N` itself.
		// Named `vterr` rather than `err`: this function has a named `err` return, so `err :=`
		// trips govet's shadow and `err =` trips gocritic's sloppyReassign — the two linters want
		// opposite things here, and a distinct name satisfies both without a suppression.
		if vterr := v.checkValType(vt); vterr != nil {
			return nil, nil, vterr
		}
		return nil, []binary.ValType{vt}, nil
	}
	// The indexed form: a full function type, parameters included.
	ft, err := funcType(v.mod, idx)
	if err != nil {
		return nil, nil, err
	}
	return ft.Params, ft.Results, nil
}

// elseArm closes an `if`'s then-arm and opens its else-arm.
func (v *validator) elseArm() error {
	f := v.top()
	if f.kind != opIf {
		return fmt.Errorf("%w: else outside an if", ErrTypeMismatch)
	}
	if f.elseSeen {
		return fmt.Errorf("%w: a second else in one if", ErrTypeMismatch)
	}
	// The then-arm must have produced the block's results, and nothing more.
	if err := v.popExpectAll(f.endTypes); err != nil {
		return err
	}
	if err := v.expectEmptyFrame(); err != nil {
		return err
	}
	// Reset to the arm's entry state: the else-arm starts where the then-arm did, reachable
	// again even if the then-arm ended in `br`.
	f.elseSeen = true
	f.unreachable = false
	v.stack = v.stack[:f.height]
	v.pushAll(f.params)
	return nil
}

// endBlock closes a frame, the function-body frame included.
//
// No special case for the body: it is a frame with the function's results as both its label types
// and its end types, so `end` checks it exactly as it checks a `block`. The previous shape — leave
// the body frame standing and let funcBody check it again — is the double-count grave funcBody's
// comment records.
func (v *validator) endBlock() error {
	if len(v.frames) == 0 {
		// Unreachable through the decoder, which stops at the body's matching `end`, and returned
		// rather than left to panic because "unreachable through the decoder" is a claim about
		// another package. The alternative is an index-out-of-range in the pass whose job is to
		// decide whether a module is safe to run.
		return fmt.Errorf("%w: end with no block open", ErrTypeMismatch)
	}
	f := v.top()

	// An `if` with no `else` has an implicit empty else-arm, which type-checks only when the
	// block's parameters already are its results.
	if f.kind == opIf && !f.elseSeen && !sameTypes(f.params, f.endTypes) {
		return fmt.Errorf("%w: if without else must have matching parameters and results", ErrTypeMismatch)
	}

	if err := v.popExpectAll(f.endTypes); err != nil {
		return err
	}
	if err := v.expectEmptyFrame(); err != nil {
		return err
	}

	end := f.endTypes
	v.frames = v.frames[:len(v.frames)-1]
	if len(v.frames) == 0 {
		// That was the body frame. Its results are the function's return values, which belong to
		// the caller and not to an enclosing frame, so there is nothing to push them onto.
		return nil
	}
	v.pushAll(end)
	return nil
}

// brTable checks a `br_table`: every arm, and the default, against the operand types on the stack.
//
// # Against the stack, and not against each other — which is the whole rule
//
// The obvious reading is that the arms must agree with the default, and it is wrong. The
// reference (`valid/valid.ml:470-475`) peeks the operand types *first* and matches everything
// against them:
//
//	let n = List.length (label c x) in
//	let ts = List.init n (fun i -> peek (n - i) s) in
//	match_stack c ts (label c x) x.at;
//	List.iter (fun x' -> match_stack c ts (label c x') x'.at) xs;
//
// The difference shows up on exactly one vector, and it is a *valid* module:
// `unreached-valid.wast`'s `meet-bottom` branches to an `(result f32)` block and an `(result f64)`
// block from the same table, after `unreachable`. Arm-versus-default rejects it — f32 is not f64 —
// but the operands are bottom, and bottom matches both, so the module is valid and this package
// refused it. That is the one false reject the accept-direction sweep found, and it is the reason
// the sweep exists: the arm-versus-default rule passes all 133 `br_table` reject vectors and would
// never have been questioned by the pass column.
//
// Arity is still enforced, because the match is length-checked: `ts` has the default's arity, so an
// arm of a different arity fails however bottom the stack is.
func (v *validator) brTable(i int, in binary.Instr) error {
	// The index operand, above the branch's own values.
	if err := v.popExpect(binary.I32); err != nil {
		return err
	}

	// **The default is in `Imm0`, and the vector is beside the body.** Not `Imm1`: `immVecIdx`
	// stages no word, so `immIdx` is this opcode's *first* staged immediate. The decoder's own
	// comment records that reasoning from the table row's field order gives the wrong answer and
	// that the right one was obtained by printing the fields — so this reads Imm0 on that
	// authority rather than on the row's shape.
	def, err := v.label(uint32(in.Imm0))
	if err != nil {
		return err
	}

	// The operand types the branch will carry, peeked rather than popped: the default's arity
	// decides how many, and slots below the frame's entry height read as bottom. Enforcing that
	// they are really *there* is popExpectAll's job below, which is where a short reachable stack
	// is caught — the same division the reference makes between `peek` (pads with BotT) and `pop`
	// (pads only for a polymorphic stack, and fails on length otherwise).
	ts := v.peekN(len(def.labelTypes))

	if err := v.matchLabel(ts, def.labelTypes, "default", uint32(in.Imm0)); err != nil {
		return err
	}
	labels, _ := v.curFunc.LabelVector(i)
	for _, l := range labels {
		f, err := v.label(l)
		if err != nil {
			return err
		}
		if err := v.matchLabel(ts, f.labelTypes, "arm", l); err != nil {
			return err
		}
	}

	if err := v.popExpectAll(ts); err != nil {
		return err
	}
	v.setUnreachable()
	return nil
}

// matchLabel requires the peeked operand types to satisfy one br_table target's label types.
//
// **The message names the types, because the version that named the counts was a false witness.**
// It read "arm 0 expects 1 values, default expects 1" — printing the one quantity that agreed,
// about a comparison over types, on the single vector the rule got wrong. The error was right that
// something disagreed and gave evidence that nothing did.
func (v *validator) matchLabel(ts, want []binary.ValType, role string, idx uint32) error {
	if len(ts) != len(want) {
		return fmt.Errorf("%w: br_table %s %d takes %s, stack has %s",
			ErrTypeMismatch, role, idx, typeList(want), typeList(ts))
	}
	for j := range want {
		if !matches(ts[j], want[j]) {
			return fmt.Errorf("%w: br_table %s %d takes %s, stack has %s",
				ErrTypeMismatch, role, idx, typeList(want), typeList(ts))
		}
	}
	return nil
}

// callIndirect checks a call through a table.
func (v *validator) callIndirect(in binary.Instr) error {
	// Immediates in written order: type index, then table index.
	ft, err := funcType(v.mod, uint32(in.Imm0))
	if err != nil {
		return err
	}
	if err := v.requireTable(uint32(in.Imm1)); err != nil {
		return err
	}
	// The table index operand sits above the callee's arguments.
	if err := v.popExpect(binary.I32); err != nil {
		return err
	}
	if err := v.popExpectAll(ft.Params); err != nil {
		return err
	}
	v.pushAll(ft.Results)
	return nil
}

// selectUnannotated is `select` without a result-type vector.
//
// Its rule is narrower than it looks: the two operands must be *numeric or vector* types, never
// references. `valid.ml`'s bare `select` requires a `numtype`/`vectype`, and the annotated form
// exists precisely because reference operands need one. Accepting a reference here would be an
// accept-direction defect that the annotated form's whole existence argues against.
func (v *validator) selectUnannotated() error {
	if err := v.popExpect(binary.I32); err != nil {
		return err
	}
	t2, ok := v.pop()
	if !ok {
		return fmt.Errorf("%w: select on an empty stack", ErrTypeMismatch)
	}
	t1, ok := v.pop()
	if !ok {
		return fmt.Errorf("%w: select with one operand", ErrTypeMismatch)
	}
	if !matches(t1, t2) {
		return fmt.Errorf("%w: select operands are %s and %s", ErrTypeMismatch, t1, t2)
	}
	// The result is whichever operand is concrete; both may be bottom in an unreachable frame.
	res := t1
	if res == unknown {
		res = t2
	}
	if res != unknown && res.IsRef() {
		return fmt.Errorf("%w: select on %s needs a result-type annotation", ErrTypeMismatch, res)
	}
	v.push(res)
	return nil
}

// selectAnnotated is `select` with a result-type annotation — `valid.ml:442-446`, the last
// instruction in the single-byte opcode space slice 1 declined (#294).
//
//	| Select (Some ts) ->
//	  require (List.length ts = 1) e.at
//	    "invalid result arity other than 1 is not (yet) allowed";
//	  check_resulttype c ts e.at;
//	  (ts @ ts @ [NumT I32T]) --> ts, []
//
// **The annotation is the type, and the stack is checked against it — never the reverse.** That
// asymmetry is the whole reason this arm waited for #294's retention rather than being approximated
// from the operands: `select (result i32)` on two `f32`s is invalid, and a validator inferring the
// type from the stack would accept it and hand the interpreter a module whose dispatch bit disagrees
// with its values. Accept-direction, §9 G-3, and no `assert_invalid` vector can score it.
//
// **Order matters and it is the reference's.** Arity is checked before the types are resolved and
// before any operand is popped, so `(select (result i32 i32) …)` on an empty stack reports the arity
// and not a stack shortage. `select.wast:373`'s module is exactly that case — its function declares
// `(result i32 i32)` and its operands are four `i32.const`s, so a validator popping first has three
// other complaints available and the corpus expects this one.
func (v *validator) selectAnnotated(i int) error {
	ts, ok := v.curFunc.SelectTypes(i)
	if !ok {
		return fmt.Errorf("%w: instruction %d", errNoSelectAnnotation, i)
	}
	if len(ts) != 1 {
		// The reference's text verbatim per 0003, with the count after it — the corpus expects
		// the substring `invalid result arity`, and both vectors that reach here are about the
		// count, so naming it is what tells arity 0 from arity 2 in a bucket key.
		return fmt.Errorf("%w (%d annotated)", ErrInvalidResultArity, len(ts))
	}
	// `check_resulttype c ts` — over the whole vector, which is how the reference writes it.
	//
	// **Written as a loop rather than as `ts[0]`, so that the arity require above is the rule and
	// not also this line's bounds guard.** It was `ts[0]` first, and the falsification run that
	// disabled the require to watch the arity rows die found the arity-0 row dying by *panic*
	// instead: with the require gone, indexing an empty annotation is an index-out-of-range in the
	// package whose job is to decide whether a module is safe to run. The row still went red, so
	// the control was never in question — but a rule that is silently load-bearing for memory
	// safety is one lifted restriction away from a crash, and the reference will lift this one
	// ("not (yet) allowed"). Everything below is index-free for the same reason: the arity is the
	// only thing that has to change when it does.
	for _, t := range ts {
		if err := v.checkValType(t); err != nil {
			return err
		}
	}
	// `ts @ ts @ [i32] --> ts` popped from the top down: the condition, then both operands, each
	// against the annotation rather than against each other. `matches` is not consulted here for the
	// same reason — `popExpect` already admits `unknown` in an unreachable frame, and two operands
	// agreeing with a type they were both checked against agree with each other by construction.
	if err := v.popExpect(binary.I32); err != nil {
		return err
	}
	if err := v.popExpectAll(ts); err != nil {
		return err
	}
	if err := v.popExpectAll(ts); err != nil {
		return err
	}
	v.pushAll(ts)
	return nil
}

// checkValType is `check_valtype` (`valid.ml:131-136`), which is a no-op for every form except one:
// a reference type naming a *concrete* type index has to name one that exists.
//
// The numeric and vector cases are `()` in the reference (`check_numtype`, `check_vectype`), and the
// abstract heaptypes are `()` in `check_heaptype`. What is left is `UseHT (Idx x)` reaching
// `check_typeuse`'s `type_ c x` — the same `lookup "type"` every other index-space check in this
// package goes through, so the message is `unknown type N` and not a paraphrase.
//
// **Two callers, and the second one was a gap this comment used to describe as open.** It read
// "`blockType`'s valtype form does not call this and the reference's `check_blocktype` does"
// (`:420`: `ValBlockType (Some t) -> check_valtype c t at`), so `(block (result (ref 99)))` was
// accepted with no such type in the module — an accept-direction gap of exactly the shape #294
// closed for `select`, found while reading the function that closed it. **#311 closed it**, and
// `blockType` now calls this; the sentence is kept in the past tense rather than deleted because the
// reason it was *not* fixed in #294 is the durable part: a different opcode family with its own
// corpus vectors and its own board delta, and folding it in would have put a second reward under the
// first one's forecast.
func (v *validator) checkValType(t binary.ValType) error {
	if !t.IsIndexed() {
		return nil
	}
	if idx := t.Index(); idx >= uint32(len(v.mod.Types)) {
		return fmt.Errorf("%w %d (%d in scope)", ErrUnknownType, idx, len(v.mod.Types))
	}
	return nil
}

// errNoSelectAnnotation is opcode `0x1C` reaching the validator with no retained annotation.
//
// **Undeclared and unreachable by construction**, on `errNoNaturalWidth`'s posture and for its
// reason: this is not a decline (the instruction is plainly in this slice's vocabulary now) and not
// a verdict about the module (nothing in it is wrong) — it is the decoder and this arm disagreeing
// about what `0x1C` files, which is an engine bug and belongs in a channel nobody expects to see.
//
// The decoder files a vector for every `0x1C`, empty annotation included, which is what makes this
// unreachable; `binary.TestSelectRetainsTheAnnotationIncludingItsIllegalArities` is the control that
// says so rather than this comment claiming it.
var errNoSelectAnnotation = errors.New("internal: select 0x1C with no retained result-type annotation")

// localOp handles the three local instructions.
func (v *validator) localOp(in binary.Instr) error {
	idx := in.Imm0
	if idx >= uint64(len(v.locals)) {
		return fmt.Errorf("%w %d (%d in scope)", ErrUnknownLocal, idx, len(v.locals))
	}
	t := v.locals[idx]
	switch in.Op {
	case opLocalGet:
		v.push(t)
	case opLocalSet:
		return v.popExpect(t)
	case opLocalTee:
		if err := v.popExpect(t); err != nil {
			return err
		}
		v.push(t)
	}
	return nil
}

// globalOp handles `global.get` and `global.set`.
func (v *validator) globalOp(in binary.Instr) error {
	t, mutable, err := v.globalAt(uint32(in.Imm0))
	if err != nil {
		return err
	}
	if in.Op == opGlobalGet {
		v.push(t)
		return nil
	}
	if !mutable {
		return fmt.Errorf("%w (global %d)", ErrGlobalImmutable, in.Imm0)
	}
	return v.popExpect(t)
}

// funcTypeAt resolves a *function index* — imports first, then defined — to its type.
//
// The interleaving is the module's, not a convention: an imported function occupies function
// index 0 before any defined function does. `internal/interp` resolves it the same way
// (`call.go:142`), and getting it backwards would type-check every call in a module without
// imports and misresolve every call in a module with them.
func (v *validator) funcTypeAt(idx uint32) (binary.FuncType, error) {
	imported := uint32(v.mod.ImportedFuncs())
	if idx < imported {
		n := 0
		for i := range v.mod.Imports {
			if v.mod.Imports[i].Kind != binary.ExternFunc {
				continue
			}
			if uint32(n) == idx {
				return funcType(v.mod, v.mod.Imports[i].Index)
			}
			n++
		}
		return binary.FuncType{}, fmt.Errorf("%w %d (import scan found no match)", ErrUnknownFunc, idx)
	}
	def := idx - imported
	if def >= uint32(len(v.mod.Funcs)) {
		return binary.FuncType{}, fmt.Errorf("%w %d (%d in scope)",
			ErrUnknownFunc, idx, imported+uint32(len(v.mod.Funcs)))
	}
	return funcType(v.mod, v.mod.Funcs[def].TypeIndex)
}

// globalAt resolves a global index across the same import-then-defined split.
func (v *validator) globalAt(idx uint32) (binary.ValType, bool, error) {
	imported := uint32(v.mod.ImportedGlobals())
	if idx < imported {
		n := 0
		for i := range v.mod.Imports {
			if v.mod.Imports[i].Kind != binary.ExternGlobal {
				continue
			}
			if uint32(n) == idx {
				return v.mod.Imports[i].GlobalType, v.mod.Imports[i].GlobalMutable, nil
			}
			n++
		}
		return binary.ValType{}, false, fmt.Errorf("%w %d (import scan found no match)", ErrUnknownGlobal, idx)
	}
	def := idx - imported
	if def >= uint32(len(v.mod.Globals)) {
		return binary.ValType{}, false, fmt.Errorf("%w %d (%d in scope)",
			ErrUnknownGlobal, idx, imported+uint32(len(v.mod.Globals)))
	}
	g := v.mod.Globals[def]
	return g.Type, g.Mutable, nil
}

// requireTable checks a table index resolves.
//
// A lookup whose result is discarded, delegating to `tableTypeAt` so that the index space is
// walked in one place — see that function on why slice 5 made a second implementation of this
// question a live risk rather than a hypothetical one.
func (v *validator) requireTable(idx uint32) error {
	_, err := tableTypeAt(v.mod, idx)
	return err
}

// sameTypes compares two type sequences by identity.
func sameTypes(a, b []binary.ValType) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// mnemonic renders an instruction for an error message, on the authority's own table.
//
// **`ok` from that table means "there is a row", not "there is a name"**, and the difference is
// load-bearing for exactly the opcodes this package dispatches most: `end` (0x0B) and `else`
// (0x05) have rows with an *empty* mnemonic, because `decode.ml` treats them as terminators of an
// instruction sequence rather than as named instructions. So the first version of this printed
// `instr 1 ()` for every error at a block's `end` — a witness that names nothing, in the one
// message a reader uses to find the instruction. The empty name is checked as well as the
// boolean, and the fallback is the byte, which at least resolves.
func mnemonic(in binary.Instr) string {
	if in.Prefix != 0 {
		if name, _, ok := binary.PrefixedOp(in.Prefix, in.Op); ok && name != "" {
			return name
		}
		return fmt.Sprintf("%#02x %#02x", in.Prefix, in.Op)
	}
	if name, ok := binary.OpMnemonic(in.Op); ok && name != "" {
		return name
	}
	// The four slice 1 dispatches whose rows carry no name. Spelled here rather than left as hex
	// because an error at `end` is the most common one this package produces.
	switch in.Op {
	case opEnd:
		return "end"
	case opElse:
		return "else"
	}
	return fmt.Sprintf("%#02x", in.Op)
}
