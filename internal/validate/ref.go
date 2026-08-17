// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package validate

import (
	"errors"
	"fmt"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// The reference-type slice's opcodes (#359): the three `ref.*` rows in the single-byte space and
// the two table accessors, which are here rather than in bulk.go because bulk.go is the 0xFC
// region and these are 0x25/0x26 — and because what both of them turn on is the *reftype* a
// table carries, which is this slice's subject and not the bulk operands'.
//
// Checked against the generated table by `TestStructuralOpcodesMatchTheTable` like every other
// constant in this package's opcode blocks; see instr.go's block comment for why that check is
// the reason these are named at all.
const (
	opTableGet  = 0x25
	opTableSet  = 0x26
	opRefNull   = 0xD0
	opRefIsNull = 0xD1
	opRefFunc   = 0xD2
)

// ErrUndeclaredFunc is `refer_func`'s rejection (`valid.ml:81-86`), and it is a category of its
// own rather than a spelling of `ErrUnknownFunc`.
//
// The distinction is the whole content of the rule: the index *resolves* — `func c x` has already
// succeeded by the time `refer_func` runs — and the module is still invalid, because a
// `ref.func x` may only name a function some *other* part of the module already mentions. So
// "unknown" would be a false statement about a function that is perfectly well known, and the
// corpus tells them apart: `elem.wast`'s `undeclared function reference` vectors and its
// `unknown function` vectors are different rows with different expectations.
//
// Deliberately outside `ErrUnknown*`. `TestUnknownCategoriesMatchTheReference` derives its domain
// from this package's `ErrUnknown*` declarations and from messages beginning `unknown `, so this
// sentinel is outside that control's extent by construction rather than by an exemption — which is
// correct, since the reference's own `refer` is a separate function from `lookup` and produces a
// message with a separate shape.
var ErrUndeclaredFunc = errors.New("undeclared function reference")

// errNoRefNullHeapType is opcode `0xD0` reaching this rule with no retained heaptype.
//
// Undeclared and unreachable by construction, on `errNoSelectAnnotation`'s posture and for its
// reason: it is not a decline and not a verdict about the module, but the decoder and this arm
// disagreeing about what `0xD0` files. `binary.TestEveryHeapTypeRowFilesACastType` is the control
// that makes it unreachable — every table row reading an `immHeapType` files a cast-type vector —
// rather than this comment asserting it.
var errNoRefNullHeapType = errors.New("internal: ref.null 0xD0 with no retained heaptype")

// refNull is `RefNull ht` (`valid.ml:714-716`):
//
//	| RefNull ht ->
//	  check_heaptype c ht e.at;
//	  [] --> [RefT (Null, ht)], []
//
// **The type comes off the side table, and that is what #359 is.** Before it, all thirteen
// heaptypes decoded to one indistinguishable `Instr` — 0027's declared gap, closed on 0027's own
// named condition once a consumer arrived, and this rule is that consumer. The alternative
// available to a validator without the retention is to invent a type, which for `funcref` would
// even pass the majority of the corpus: `(ref null func)` is what most `ref.null` vectors spell.
// It would then accept `(global externref (ref.null func))`, an accept-direction defect no
// `assert_invalid` vector can score (§9 G-3).
//
// `check_heaptype` is `()` for every abstract form and `type_ c x` for the indexed one, which is
// exactly `checkValType`'s one non-trivial case, so the message for `(ref.null 99)` is
// `unknown type 99` and not a paraphrase.
//
// The `Null` bit is not read from anywhere: the decoder files the type already nullable, because
// nullability here is the instruction's *meaning* rather than an encoded or opcode bit
// (`decode.ml:604`, and `binary.TestRefNullRetainsTheSpelledHeapType` asserts it per heaptype).
// Re-deriving it here would be a second place knowing one fact.
func (v *validator) refNull(i int) error {
	ts, ok := v.curFunc.CastTypes(i)
	if !ok {
		return fmt.Errorf("%w: instruction %d", errNoRefNullHeapType, i)
	}
	if len(ts) != 1 {
		return fmt.Errorf("%w: instruction %d filed %d types", errNoRefNullHeapType, i, len(ts))
	}
	if err := v.checkValType(ts[0]); err != nil {
		return err
	}
	v.push(ts[0])
	return nil
}

// refIsNull is `RefIsNull` (`valid.ml:724-726`):
//
//	| RefIsNull ->
//	  let (_nul, ht) = peek_ref 0 s e.at in
//	  [RefT (Null, ht)] --> [NumT I32T], []
//
// # It peeks before it pops, and the two steps answer different questions
//
// The signature is *computed from the operand*, which is why this is not `popExpect` of some fixed
// type: there is no fixed type to expect. `peek_ref` reads the top of the frame without consuming
// it and yields the heaptype, the requirement is then built as `(ref null ht)`, and that
// requirement is satisfied by construction — a nullable requirement admits a non-nullable operand,
// and `ht` matches itself. So the popped type is never rejected by the *match*; what can be
// rejected is the peeked type being a non-reference at all, and that is `peek_ref`'s own error.
//
// Writing it as one step would get two cases wrong in opposite directions. Requiring `funcref`
// would reject every `externref` operand; requiring nothing would accept `(ref.is_null)` on an
// `i32`, which is the accept direction. Hence: peek, classify, then consume.
//
// **`peek_ref` does not fail on an empty stack**, reachable or not — `peek` returns `BotT` out of
// range unconditionally (`valid.ml:283-284`), and `BotT` is a reference as far as this rule cares.
// So a bare `ref.is_null` in a reachable frame is reported by the *pop*, as a stack shortage, and
// not as "requires reference type but stack has nothing". `peekN` already implements that padding,
// and its comment records the same reference division for `br_table`.
//
// The message is the reference's verbatim (0003), including its slightly odd "but stack has"
// followed by a single type rather than a list — the corpus matches by substring, and the vectors
// in `ref_is_null.wast` and `unreached-invalid.wast` that reach here expect the `type mismatch`
// prefix.
func (v *validator) refIsNull() error {
	if t := v.peekN(1)[0]; t != unknown && !t.IsRef() {
		return fmt.Errorf("%w: instruction requires reference type but stack has %s",
			ErrTypeMismatch, t)
	}
	if _, ok := v.pop(); !ok {
		return fmt.Errorf("%w: instruction requires reference type but stack is empty",
			ErrTypeMismatch)
	}
	v.push(binary.I32)
	return nil
}

// refFunc is `RefFunc x` (`valid.ml:718-721`):
//
//	| RefFunc x ->
//	  let dt = func c x in
//	  refer_func c x;
//	  [] --> [RefT (NoNull, UseHT (Def dt))], []
//
// # The result is a concrete `(ref $t)`, not `funcref`
//
// `dt` is the function's *deftype*, so the pushed type names the function's type index and is
// non-nullable. Pushing `binary.FuncRef` instead is the obvious approximation and it is wrong in
// both directions at once: it is too weak for `(global (ref 0) (ref.func 0))`, which it would
// reject because `(ref null func)` does not match `(ref 0)`, and too strong nowhere the corpus
// can see, which is why the reject direction is the one that catches it.
//
// The concrete type still satisfies a `funcref` requirement, and it does so through the relation
// rather than by accident: `matchHeap`'s expand arm resolves the index through `compTypeAt`,
// `abstractOfCompType` maps a functype to `HeapFunc`, and `matchNull(false, true)` admits a
// non-nullable operand where a nullable one is wanted. So `(table funcref (elem $f))` and
// `(local.set $r (ref.func $f))` both work without this rule weakening its answer.
//
// # Two checks, in the reference's order, and they are different rules
//
// `func c x` is the index-space lookup and answers `unknown function`. `refer_func` is the
// *declaration* rule and answers `undeclared function reference` — see ErrUndeclaredFunc on why
// collapsing them would make a true statement false. Order matters for a module that gets both
// wrong: `(ref.func 99)` in a module with one function reports the unknown index, which is what
// `elem.wast`'s vectors expect.
func (v *validator) refFunc(in binary.Instr) error {
	idx := uint32(in.Imm0)
	ti, err := v.funcTypeIndexAt(idx)
	if err != nil {
		return err
	}
	if !v.refs[idx] {
		return fmt.Errorf("%w %d", ErrUndeclaredFunc, idx)
	}
	v.push(binary.RefType(ti, false))
	return nil
}

// tableOp is `TableGet x` and `TableSet x` (`valid.ml:610-616`):
//
//	| TableGet x ->
//	  let TableT (at, _lim, rt) = table c x in
//	  [NumT (numtype_of_addrtype at)] --> [RefT rt], []
//
//	| TableSet x ->
//	  let TableT (at, _lim, rt) = table c x in
//	  [NumT (numtype_of_addrtype at); RefT rt] --> [], []
//
// **Both operands come off the table and neither is hardcoded**, which is the shape #343 cause 2
// is about. The index is at the table's address type, so a table64 is indexed by an i64; the
// element is the table's own reference type, so a `(table externref)` refuses a `funcref`. Writing
// `binary.I32` and `binary.FuncRef` here would pass every vector in the default lane and
// over-reject in the all-on one, invisible until #341 gave the accept direction a witness.
//
// `tableTypeAt` and `tableAddrType` already exist next door for the bulk operands and are reused
// rather than re-derived, which is `callIndirect`'s comment's whole point: the same grave was
// re-earned once already by a call site reading a different field. *Lessons are indexed by shape,
// not by file.*
//
// `table.set`'s two operands are popped rightmost-first — the element, then the index — because
// the signature reads in stack order. Reversed, it passes nothing and fails everything, which is
// the harmless direction; `popExpectAll`'s comment records the version of this mistake that is
// not harmless.
func (v *validator) tableOp(in binary.Instr) error {
	tab, err := tableTypeAt(v.mod, uint32(in.Imm0))
	if err != nil {
		return err
	}
	if in.Op == opTableGet {
		if err := v.popExpect(tableAddrType(tab)); err != nil {
			return err
		}
		v.push(tab.ElemType)
		return nil
	}
	if err := v.popExpect(tab.ElemType); err != nil {
		return err
	}
	return v.popExpect(tableAddrType(tab))
}

// declaredFuncs is `check_module`'s first line (`valid.ml:1152`), restricted to the function
// index space:
//
//	let refs = Free.module_ ({m.it with funcs = []; start = None} @@ m.at) in
//
// # It is the whole module minus two things, and the two exclusions are the rule
//
// `Free.module_` unions every index a module mentions (`free.ml:235-245`); this call hands it a
// module with the **function bodies emptied and the start section removed**, so the set is "every
// function this module refers to from somewhere other than code or start". That is what makes
// `(func (ref.func 0))` invalid in a module whose only mention of function 0 is that instruction:
// a body cannot declare its own references, or the rule would be vacuous.
//
// So the contributing sources, from `free.ml` directly rather than from a summary of it:
//
//   - **exports** of kind func (`externidx`'s `FuncX x -> funcs (idx x)`), which is why
//     `(export "f" (func $f))` declares `$f`;
//   - **globals'** initializers (`global` = `globaltype gt ++ const c`);
//   - **element segments'** element expressions, in **every** mode (`elem` = `reftype rt ++ list
//     const cs ++ segmentmode tables mode`) — declarative segments are the *idiomatic* declaration
//     and were this function's first draft's whole content, but passive and active ones count
//     identically, and the index form counts too: the front end desugars `(elem func $f)` into a
//     `ref.func $f` const expr (`decode.ml`'s `elem_index`), so `ElemSegment.Funcs` is that same
//     contribution in the shape this decoder keeps it;
//   - **active-mode offsets** for both element and data segments (`segmentmode`'s `++ const c`),
//     which contribute nothing in practice — an offset must be i32-typed — and are walked anyway,
//     because whether they *can* contain a `ref.func` is a fact about a rule this function does not
//     implement.
//
// # One source is missing and it is a known over-rejection
//
// `free.ml`'s `table` is `tabletype tt ++ const c`: a table's own initializer expression
// contributes, and `binary.Table` does not retain one — the decoder reads it and drops it
// (`sections.go`'s `decodeTableForm`). So `(table 1 funcref (ref.func $f))` cannot reach this set
// and its `ref.func` is reported undeclared. That is **reject-direction**, which is the reason it is
// declared here rather than fixed here: it is a retention gap in `binary` of exactly 0027's shape,
// gated behind GC, and its failure mode is a refusal rather than a module accepted unchecked.
//
// **Where it surfaces is one layer earlier than that, and the distinction matters for what "declared"
// buys.** The suite reaches a table-with-initializer module as *module text*, and the wat encoder has
// no such `(table …)` field yet (#8) — so today the vector is declined by the encoder and this rule
// is never asked. The omission is therefore not currently visible as a red validator row either; it
// is queued behind #8, and it will become one the moment the encoder learns the field. Stated in that
// order because the weaker claim is the true one: this is declared prose plus a retention gap with a
// named cause, not a failure some board is already reporting.
//
// Computed once per module and carried on the validator for the reference's reason — `refs` is a
// field of `context`, computed before the first `check_func` — and not per body, which would make
// it O(bodies × module).
func declaredFuncs(m *binary.Module) map[uint32]bool {
	refs := map[uint32]bool{}
	add := func(body []binary.Instr) {
		for _, in := range body {
			if in.Prefix == 0 && in.Op == opRefFunc {
				refs[uint32(in.Imm0)] = true
			}
		}
	}
	for i := range m.Exports {
		if m.Exports[i].Kind == binary.ExternFunc {
			refs[m.Exports[i].Index] = true
		}
	}
	for i := range m.Globals {
		add(m.Globals[i].Init)
	}
	for i := range m.Elems {
		add(m.Elems[i].Offset)
		for _, e := range m.Elems[i].Exprs {
			add(e)
		}
		for _, x := range m.Elems[i].Funcs {
			refs[x] = true
		}
	}
	for i := range m.Datas {
		add(m.Datas[i].Offset)
	}
	return refs
}
