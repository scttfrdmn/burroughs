// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package interp

import (
	"github.com/scttfrdmn/burroughs/internal/binary"
)

// trapNullStruct is `Trapping "null structure reference"` (eval.ml:697, :711) — the trap both
// `struct.get` and `struct.set` raise on a null operand.
//
// **A trap and not #9's debt**, which is the distinction this whole rung turns on: a null struct
// reference is a perfectly well-typed value that validation cannot rule out, so the failure is a
// runtime event with a spec-named string. Its neighbours in `refop.go` (`null reference`, `null
// function reference`) are the same shape, and the string differs from theirs — "structure", spelled
// out — so a copied constant would be a wrong answer the oracle *can* see: `struct.wast:147` and
// `:152` assert this exact text.
var trapNullStruct = &Trap{Reason: "null structure reference"}

// The 0xfb region's struct sub-opcodes, named rather than left as bare bytes for the reason
// `opTableFB` gives them names: `fb 02` and `fb 04` differ by one bit and by the entire question of
// whether the read sign-extends.
const (
	opStructNew        = 0x00
	opStructNewDefault = 0x01
	opStructGet        = 0x02
	opStructGetS       = 0x03
	opStructGetU       = 0x04
	opStructSet        = 0x05
)

// execFB dispatches the 0xfb region — the GC proposal's instructions.
//
// **A region function beside execFC and execFD for execFC's stated reason**: `Op` is the
// sub-opcode, so `fb 00` and `unreachable` are both `Op == 0x00`, and a shared switch would need
// every arm to re-test the prefix. The prefix is a precondition of this whole switch instead.
//
// Counted rather than described: `opTableFB` has **31** entries (max sub-opcode 0x1e) and this
// switch answers **27** of them — the struct family (rung 2), the array family (rung 3), the i31
// trio (rung 4), and the four casts (rung 5's first slice), decision 0020's ladder. Everything else
// falls to `unsupported`, which renders `fb NN` and so keeps the remaining arms visible as the board
// buckets they are; what is left is rung 5's third slice, `fb 1a`/`fb 1b` (the extern conversions).
//
// **Two of the region's arms are not in this switch, and so this function is no longer the region's
// whole index.** `fb 18`/`fb 19` (`br_on_cast`, `br_on_cast_fail`) are answered by `runFrame`
// itself, before it delegates here, because they branch: a control transfer needs `ctrl` and `pc`,
// which are that loop's locals, and this signature returns an `error`. Their semantics still live
// with the family in `castop.go` (`brOnCastTaken`); only the transfer is up there. The sentence this
// paragraph replaces said the switch was "the single authority for which sub-opcode has an arm",
// and leaving it would have made the count above read as 27-of-29-implemented while two arms
// existed elsewhere — a comment asserting the property the code no longer has, which is the
// camouflage review cannot penetrate because review checks code against claims. The count is
// therefore stated as **29 arms across two sites, 27 of them here**, and the split is pinned in
// both directions rather than described: `TestBrOnCastIsNotInTheFBSwitch` asserts this switch still
// declines the pair (so an arm added here later cannot sit dead behind the interception), and
// `TestBrOnCastBranchesOnTheCastResult` asserts `runFrame` answers them.
//
// **`fn` and `pc` rather than a pre-resolved side-table vector**, which is a widening this rung
// forced and the shape it settled on is worth stating. The four cast arms need `Func.Casts`, a
// third instruction-indexed side table (0016's mechanism, 0027's decision 1), and the two candidate
// signatures were "pass the vector" — resolving it at the call site for every `fb` instruction,
// which is a map lookup charged to `struct.get` so that `ref.cast` need not make one — and "pass
// the frame's function and program counter", which charges the lookup to the four arms that use it.
// The second is what `runFrame` already does for `br_table`'s label vector (`fn.LabelVector(pc)` at
// its own arm, not at the dispatch), so this is the existing convention rather than a new one.
//
// **The dispatch lives here rather than moving to a `gcop.go` as the family grew**, which is a
// choice and not inertia: `execFB` is the single authority for "which sub-opcode has an arm", and the
// count in the paragraph above is only checkable because there is one switch to count. The arms
// themselves are one file per rung (`structop.go`, `arrayop.go`, `i31op.go`, `castop.go`), so the
// file this comment is in is the region's index.
func (in *Instance) execFB(ins binary.Instr, st *stack, fn *binary.Func, pc int) error {
	switch ins.Op {
	case opStructNew:
		return in.execStructNew(ins, st)

	case opStructNewDefault:
		return in.execStructNewDefault(ins, st)

	case opStructGet:
		return in.execStructGet(ins, st, extNone)

	case opStructGetS:
		return in.execStructGet(ins, st, extS)

	case opStructGetU:
		return in.execStructGet(ins, st, extU)

	case opStructSet:
		return in.execStructSet(ins, st)

	case opArrayNew:
		return in.execArrayNew(ins, st)

	case opArrayNewDefault:
		return in.execArrayNewDefault(ins, st)

	case opArrayNewFixed:
		return in.execArrayNewFixed(ins, st)

	case opArrayNewData:
		return in.execArrayNewData(ins, st)

	case opArrayNewElem:
		return in.execArrayNewElem(ins, st)

	case opArrayGet:
		return in.execArrayGet(ins, st, extNone)

	case opArrayGetS:
		return in.execArrayGet(ins, st, extS)

	case opArrayGetU:
		return in.execArrayGet(ins, st, extU)

	case opArraySet:
		return in.execArraySet(ins, st)

	case opArrayLen:
		return in.execArrayLen(st)

	case opArrayFill:
		return in.execArrayFill(ins, st)

	case opArrayCopy:
		return in.execArrayCopy(ins, st)

	case opArrayInitData:
		return in.execArrayInitData(ins, st)

	case opArrayInitElem:
		return in.execArrayInitElem(ins, st)

	// The i31 trio (rung 4, #255, `i31op.go`). None takes an immediate and none needs the
	// instance, which is why these three are the region's only arms with a bare `st` — an i31
	// payload resolves against nothing, so there is no type index to look up and no instance to
	// look it up in.
	case opRefI31:
		return execRefI31(st)

	case opI31GetS:
		return execI31GetS(st)

	case opI31GetU:
		return execI31GetU(st)

	// The casts (rung 5 slice 1, `castop.go`). The region's only arms that read a side table, and
	// so the only ones that need `fn`/`pc` — see the signature note in this function's doc.
	case opRefTest, opRefTestNull:
		return in.execRefTest(fn, pc, st)

	case opRefCast, opRefCastNull:
		return in.execRefCast(fn, pc, st)

	default:
		return unsupported(ins)
	}
}

// execStructNew allocates a struct from initializers on the stack — `eval.ml:673-680`.
//
// **The fields are popped last-to-first**, which is `split` plus `List.rev args`: the reference
// takes the top `|fts|` values off the stack (so the *last* field's initializer is on top) and
// reverses them into declaration order. Iterating downward pops in exactly that order. Getting this
// backwards is a wrong answer no arity check can see, and `struct.wast:73`'s
// `(struct.new $vec (f32.const 1) (f32.const 2) (f32.const 3))` is the vector that says so — its
// three fields are distinguishable only by value.
//
// **Each field is popped by its own declared type, so a mixed struct is not one loop over one
// array.** `(struct (field i32) (field anyref) (field i64))` draws from the numeric stack, the
// reference stack, and the numeric stack again; `popField` makes that per-field rather than
// per-instruction, which is the same reason `constExpr` derives arity from `countByArray` instead of
// assuming one slot (grave #239).
//
// **Underflow is checked per field rather than once up front**, and that is a deliberate weakening
// stated rather than hidden: a single `needNum(n)/needRef(m)` pair would need the totals, which means
// walking the fields twice. Per-field checks answer the same question — a short stack still reports
// `type mismatch` from the first field that runs out — and the only difference is that some fields
// have already been popped when it fires. Since an underflowing module is one validation rejects,
// nothing observes the difference; the stack is discarded with the error.
func (in *Instance) execStructNew(ins binary.Instr, st *stack) error {
	ct, err := in.structType("struct.new", ins.Imm0)
	if err != nil {
		return err
	}
	obj := in.alloc(ins.Imm0, make([]gcField, len(ct.Fields)))
	for i := len(ct.Fields) - 1; i >= 0; i-- {
		f, err := popField(ct.Fields[i], st)
		if err != nil {
			return err
		}
		obj.fields[i] = f
	}
	st.pushRef(ref{Obj: obj})
	return nil
}

// execStructNewDefault allocates a struct with every field at its default — `eval.ml:681-687`.
//
// It pops nothing and pushes the new reference, so it takes `st` for the push alone. A non-defaultable
// field — a non-nullable reference — is `defaultField`'s error and therefore #9's, per
// `Crash.error "non-defaultable type"`.
func (in *Instance) execStructNewDefault(ins binary.Instr, st *stack) error {
	ct, err := in.structType("struct.new_default", ins.Imm0)
	if err != nil {
		return err
	}
	obj := in.alloc(ins.Imm0, make([]gcField, len(ct.Fields)))
	for i, ft := range ct.Fields {
		f, err := defaultField(ft)
		if err != nil {
			return err
		}
		obj.fields[i] = f
	}
	st.pushRef(ref{Obj: obj})
	return nil
}

// execStructGet reads one field — `eval.ml:688-700`.
//
// **The null trap outranks every other check**, because OCaml's match tries `Ref (StructRef s)`
// first and falls to `Ref NullRef` second: a `struct.get` on a null reference traps, and the field
// index is never consulted. So the order here is pop, null-check, *then* resolve the field.
//
// `ext` comes from the opcode and not from the field — three opcodes, one arm — which is why
// `pushField` can report the two `failwith` cases (a signed read of an unpacked field, a plain read
// of a packed one) rather than silently picking something plausible.
func (in *Instance) execStructGet(ins binary.Instr, st *stack, ext fieldExt) error {
	ct, err := in.structType("struct.get", ins.Imm0)
	if err != nil {
		return err
	}
	if short := st.needRef(1); short != nil {
		return short
	}
	r := st.popRef()
	if r.Null {
		return trapNullStruct
	}
	if r.Obj == nil {
		return notAggregate("struct.get", "a struct instance", r)
	}
	ft, err := fieldStorage(ct, r.Obj, ins.Imm1)
	if err != nil {
		return err
	}
	return pushField(ft, r.Obj.fields[ins.Imm1], ext, st)
}

// execStructSet writes one field — `eval.ml:701-714`.
//
// **The value is on top and the reference underneath it** — `v :: Ref (StructRef s) :: vs'`, read
// outside-in — and with two stack arrays that ordering matters only when the field is itself a
// reference, in which case both operands are in `refs` and popping them in the wrong order writes
// the target into itself. Named because the numeric case, which is most of the corpus, works either
// way and so cannot catch the mistake.
//
// **The value's array is chosen from the instruction's type immediate, where the reference chooses it
// from the object.** It has to be: the target reference sits *under* the value, so nothing can look
// at the object before the value is popped, and popping the value requires knowing which array it is
// in. Validation guarantees the two agree — a struct subtype's shared fields match their supertype's
// in storage kind exactly — and this engine does **not** check that agreement: the check existed
// briefly and was retired for comparing a thing to itself on all 30 corpus field accesses, every
// separating path being rung 5's. `fieldStorage` carries the measurement; #248 is the tripwire. One consequence, stated rather than discovered later: because the field must be resolved
// before the value can be popped, an out-of-range field index is reported *before* the null trap,
// where the reference traps first. That reordering is observable only in a module that is
// simultaneously invalid and trapping, which no validated module is.
func (in *Instance) execStructSet(ins binary.Instr, st *stack) error {
	ct, err := in.structType("struct.set", ins.Imm0)
	if err != nil {
		return err
	}
	declared, err := fieldStorage(ct, nil, ins.Imm1)
	if err != nil {
		return err
	}
	f, err := popField(declared, st)
	if err != nil {
		return err
	}
	if err := st.needRef(1); err != nil {
		return err
	}
	r := st.popRef()
	if r.Null {
		return trapNullStruct
	}
	if r.Obj == nil {
		return notAggregate("struct.set", "a struct instance", r)
	}
	if _, err := fieldStorage(ct, r.Obj, ins.Imm1); err != nil {
		return err
	}
	r.Obj.fields[ins.Imm1] = f
	return nil
}
