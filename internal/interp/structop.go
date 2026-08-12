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
// switch answers **20** of them — the struct family (rung 2) and the array family (rung 3), decision
// 0020's first two rungs. Everything else falls to `unsupported`, which renders `fb NN` and so keeps
// the remaining arms visible as the board buckets they are: rung 4 is `i31` (`fb 1c`–`fb 1e`) and
// rung 5 the casts (`fb 14`–`fb 1b`).
//
// **The dispatch lives here rather than moving to a `gcop.go` as the family grew**, which is a
// choice and not inertia: `execFB` is the single authority for "which sub-opcode has an arm", and the
// count in the paragraph above is only checkable because there is one switch to count. The arms
// themselves are one file per rung (`structop.go`, `arrayop.go`), so the file this comment is in is
// the region's index.
func (in *Instance) execFB(ins binary.Instr, st *stack) error {
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
	obj := &gcObj{typ: ct, fields: make([]gcField, len(ct.Fields))}
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
	obj := &gcObj{typ: ct, fields: make([]gcField, len(ct.Fields))}
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
