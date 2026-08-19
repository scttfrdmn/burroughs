// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package validate

import (
	"errors"
	"fmt"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// Slices **6 and 8** of #9's validator: the register's *reference instructions* entry, which is the
// one entry that could not depart as a unit. Slice 6 (#359/#363) took the `ref.null`/`ref.is_null`/
// `ref.func` rows and the two table accessors; slice 8 (ADR 0034) takes `ref.eq`,
// `ref.as_non_null`, `br_on_null`, `br_on_non_null`, `call_ref` and `return_call_ref`. Both halves
// are here because the register names one entry, and `validate.go`'s register now records the
// two-slice departure rather than a single date.
//
// This header also answers a gap gc.go's names — slice 6 is called 6 in `bulk_test.go`, in
// `validate_test.go` and in ADR 0027 and "never in `ref.go`'s own header", which is how the ordinal
// came to be claimed twice elsewhere. It is claimed here now.
//
// # The boundary slice 8 retires, quoted where it stood
//
// `validate.go`'s slice-2 paragraph declared "**the single-byte opcode space is fully in vocabulary
// as of that slice**, and what remains declined is **0xFE (threads) alone**", and slice 4's
// paragraph called itself "the slice that closes the single-byte space". All three clauses were
// **false when written**: eleven named single-byte opcodes were declined at the time. ADR 0032 swept
// the sentence *immediately after* the first of them for staleness and left it standing, which is
// the third payout of one shape and the reason slice 8's charged overhead is a control rather than a
// corrected sentence.
//
// # Nothing stays declined, and that claim is checked rather than asserted
//
// **Zero** named single-byte opcodes decline. Slice 8 left five in two proposals: `return_call`
// (0x12) and `return_call_indirect` (0x13), typed by **slice 9** (`tailcall.go`, ADR 0035) — the
// validator having been their sole blocker, which is why they were a slice and not a decision — and
// `throw` (0x08), `throw_ref` (0x0a), `try_table` (0x1f), typed by **slice 10** (`exception.go`,
// ADR 0036), which took a scope decision first because exception handling was named in
// `validate.go`'s out-of-scope register and was therefore declined *by declaration* rather than for
// want of arms.
//
// `return_call_ref` (0x15) landed here rather than with the tail-call pair because it arrived with
// function references, so this file holds one tail-call shape and `tailcall.go` holds the other two:
// proposal boundaries winning over family resemblance, stated because the reverse is what a reader
// expects.
//
// The claim is pinned by `TestTheSingleByteOpcodeSpaceIsFullyTyped` — as **emptiness** of the set
// derived by walking `binary.OpMnemonic`'s single-byte rows and asking the real dispatch, over the
// domain of *rows that name an instruction* (the reserved-byte and prefix rows cannot reach `instr`
// with `Prefix == 0`), with the walk's extent bounded both ways because an empty set agrees with a
// walk that stopped walking. It was a *literal set* of the declined mnemonics through slices 8 and 9,
// and the two renames that cost are what earned the emptiness form: a control naming a population
// moves whenever the population does. Whatever next makes a single-byte opcode reachable here without
// an arm gets a failing test rather than a sentence nobody reads — and that is not hypothetical, since
// it is how all three of slice 10's bytes arrived.
//
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

// Slice 8's opcodes (#9, ADR 0034): the rest of the register's "reference instructions" entry — the
// three null-manipulating rows and `ref.eq`, plus the two typed-function-reference calls.
//
// **In this file rather than a new one, and the reason is the register and not line count.** The
// out-of-scope register in `validate.go` names *reference instructions* as one entry; slice 6 took
// part of it and this slice takes the rest, so the two halves of one register entry live in one
// file. `call_ref`/`return_call_ref` are here on the same reading — they are the
// function-references proposal's calls, whose whole content is the reftype their operand carries,
// which is this file's subject and not `instr.go`'s call family. Their *dispatch* still goes in
// `instr.go`'s one switch, per the prefixed-region arm's argument against a second dispatch table.
const (
	opCallRef       = 0x14
	opReturnCallRef = 0x15
	opRefEq         = 0xD3
	opRefAsNonNull  = 0xD4
	opBrOnNull      = 0xD5
	opBrOnNonNull   = 0xD6
)

// refNullEq is `RefT (Null, EqHT)` — `ref.eq`'s operand type, and its whole rule.
//
// A package-level var because the pair is fixed: `RefEq`'s arm names `EqHT` literally, twice, with
// no operand or index feeding it. The discarded second result is `AbstractRefType`'s
// is-this-one-of-the-twelve predicate over a *constant* from the same package that defines the
// table, so it cannot be false — and `TestRefEqOperandIsANullableEqRef` prints what this holds
// rather than leaving that claim to this sentence.
var refNullEq, _ = binary.AbstractRefType(binary.HeapEq, true)

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
	ts, ok := v.castVector(i)
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

// peekRef is `peek_ref`, at depth 0 (`valid.ml:286-293`): read the reference on top of the frame
// without consuming it, and answer with the type whose heaptype the rule then re-emits.
//
// The subject is backticked alone rather than as the call `peek_ref 0 s e.at`, which is how the arms
// spell it, because `citation_subject_test.go`'s extractor reads a backticked *identifier* and the
// call form — with its dotted `e.at` — matches nothing. That file's header names this exact case as
// the fourth thing to be exact about: a description that resolves, describes the right lines, and is
// invisible to the instrument. Written this way so the row is keyed rather than excused.
//
// # It peeks before it pops, and the two steps answer different questions
//
// The four rules that call it have signatures *computed from the operand*, which is why none of
// them is a `popExpect` of some fixed type: there is no fixed type to expect. This reads the top of
// the frame, the requirement is then built as `(ref null ht)` — `WithNull(true)` on what comes back
// — and that requirement is satisfied by construction: a nullable requirement admits a
// non-nullable operand, and `ht` matches itself. So the popped type is never rejected by the
// *match*; what can be rejected is the peeked type being a non-reference at all, and that is this
// function's error.
//
// Writing it as one step would get two cases wrong in opposite directions. Requiring `funcref`
// would reject every `externref` operand; requiring nothing would accept `(ref.is_null)` on an
// `i32`, which is the accept direction. Hence: peek, classify, then consume.
//
// # Bottom answers `(ref bot)` and not `bot`, and the bit it answers with is unobservable
//
// `| BotT -> (NoNull, BotHT)`: a bottom operand yields the *non-nullable reference* bottom, which is
// `botRef(false)` and is a different value from `unknown`. Transcribed as the reference writes it,
// and then **measured, which falsified what the first draft of this paragraph claimed**. The draft
// said returning `unknown` here is the reading that accepts `(unreachable) (ref.as_non_null)
// (f32.abs)`; making that change fails **no row of the suite**, and neither does answering
// `botRef(true)`. Both are the same fact: every caller re-emits the null bit it wants —
// `rt.WithNull(true)` for the pop and `rt.WithNull(false)` for the push — so whatever bit this
// function chose is overwritten before anything compares it, which is why the reference itself
// binds `_nul` at all three call sites.
//
// What the one distinguishing row actually keys on is the **push**, not this answer: replacing
// `v.push(rt.WithNull(false))` with `if isBotHeap(rt) { v.push(unknown) }` — bottom staying the
// *valtype* bottom instead of remaining a reference — accepts that module, and fails exactly one
// row. So the representation decision is real and it is load-bearing one line later than this
// paragraph first said. Recorded rather than quietly re-worded, because a comment asserting the
// wrong site for a property is how the next reader mutates the wrong line and concludes the
// distinction does not matter. See botHeapIdx and ADR 0034's falsification bill.
//
// **It does not fail on an empty stack**, reachable or not — `peek` returns `BotT` out of range
// unconditionally (`valid.ml:283-284`), and `BotT` is a reference as far as these rules care. So a
// bare `ref.is_null` in a reachable frame is reported by the *pop*, as a stack shortage, and not as
// "requires reference type but stack has nothing". `peekN` already implements that padding, and its
// comment records the same reference division for `br_table`.
//
// The message is the reference's verbatim (0003), including its slightly odd "but stack has"
// followed by a single type rather than a list — the corpus matches by substring, and the vectors
// in `ref_is_null.wast`, `ref_as_non_null.wast` and `unreached-invalid.wast` that reach here expect
// the `type mismatch` prefix.
func (v *validator) peekRef() (binary.ValType, error) {
	t := v.peekN(1)[0]
	if t == unknown {
		return botRef(false), nil
	}
	if !t.IsRef() {
		return binary.ValType{}, fmt.Errorf("%w: instruction requires reference type but stack has %s",
			ErrTypeMismatch, typeStr(t))
	}
	return t, nil
}

// refIsNull is `RefIsNull` (`valid.ml:724-726`):
//
//	| RefIsNull ->
//	  let (_nul, ht) = peek_ref 0 s e.at in
//	  [RefT (Null, ht)] --> [NumT I32T], []
//
// The pop is spelled out rather than delegated to `popExpect(ht.WithNull(true))`, and the reason is
// the message and not the check: the match is satisfied by construction (peekRef's second
// paragraph), so the only outcome `popExpect` could add is its own phrasing in place of this one —
// since #394 that is the reference's `instruction requires [(ref null $t)] but stack has []`, which
// names a *type* on a rule whose subject is any reference at all.
// refAsNonNull below does use `popExpect`, because it has to *keep* what it popped.
func (v *validator) refIsNull() error {
	if _, err := v.peekRef(); err != nil {
		return err
	}
	if _, ok := v.pop(); !ok {
		return fmt.Errorf("%w: instruction requires reference type but stack is empty",
			ErrTypeMismatch)
	}
	v.push(binary.I32)
	return nil
}

// refAsNonNull is `RefAsNonNull` (`valid.ml:728-730`):
//
//	| RefAsNonNull ->
//	  let (_nul, ht) = peek_ref 0 s e.at in
//	  [RefT (Null, ht)] --> [RefT (NoNull, ht)], []
//
// **The whole instruction is the null bit**, which is why it is one `peek_ref` and two `WithNull`
// calls with nothing between them: the heaptype is carried through untouched, and a rule that
// pushed `binary.FuncRef` or the peeked type unchanged would be a no-op wearing an opcode.
//
// The peeked *nullability* is discarded — `_nul` in the reference — because `ref.as_non_null` on an
// already-non-nullable operand is valid and yields the same type back. So there is no "already
// non-null" rejection to implement, and looking for one is how the arm acquires a check the
// reference does not have.
func (v *validator) refAsNonNull() error {
	rt, err := v.peekRef()
	if err != nil {
		return err
	}
	if err := v.popExpect(rt.WithNull(true)); err != nil {
		return err
	}
	v.push(rt.WithNull(false))
	return nil
}

// refEq is `RefEq` (`valid.ml:742-743`):
//
//	| RefEq ->
//	  [RefT (Null, EqHT); RefT (Null, EqHT)] --> [NumT I32T], []
//
// The only rule in this file with no index, no side table and no peek: two fixed operands and one
// fixed result, which is why refNullEq is a package var and this is three lines. `popExpectAll` is
// used rather than two `popExpect` calls so the pair goes through the one site that gets the
// rightmost-first order right — here the two are identical and the order cannot be observed, and
// that is exactly the case its comment says passes on coincidence.
func (v *validator) refEq() error {
	if err := v.popExpectAll([]binary.ValType{refNullEq, refNullEq}); err != nil {
		return err
	}
	v.push(binary.I32)
	return nil
}

// brOnNull is `BrOnNull x` (`valid.ml:477-479`):
//
//	| BrOnNull x ->
//	  let (_nul, ht) = peek_ref 0 s e.at in
//	  (label c x @ [RefT (Null, ht)]) --> (label c x @ [RefT (NoNull, ht)]), []
//
// # The label's types and the reference are stacked, not shared
//
// The reference operand sits *above* the label's own types and is not one of them, which is the
// difference from br_on_non_null below and from brOnCast: this rule imposes **no** requirement on
// the label at all. `(block (br_on_null 0))` with a void label is valid. So there is no non-empty
// require here, and adding one by analogy with its sibling would reject the idiomatic use.
//
// What the branch carries is `label c x` — the null is consumed by the taken branch and does not
// reach the label — and what falls through is the label's types with the reference back on top,
// non-nullable now, because the fall-through is the not-null case. That is the whole point of the
// instruction: it is the null test and the unwrap in one, so the fall-through type is strictly
// better than what came in.
func (v *validator) brOnNull(depth uint32) error {
	rt, err := v.peekRef()
	if err != nil {
		return err
	}
	f, err := v.label(depth)
	if err != nil {
		return err
	}
	if err := v.popExpect(rt.WithNull(true)); err != nil {
		return err
	}
	// The label's types are copied before the pushes: `f` points into v.frames and `labelTypes` is
	// read, not written, but popExpectAll and pushAll both run while the slice is live and a future
	// arm that reordered them would be reading a header it had already invalidated.
	ts := f.labelTypes
	if err := v.popExpectAll(ts); err != nil {
		return err
	}
	v.pushAll(ts)
	v.push(rt.WithNull(false))
	return nil
}

// brOnNonNull is `BrOnNonNull x` (`valid.ml:481-490`):
//
//	| BrOnNonNull x ->
//	  require (label c x <> []) e.at ("… but label has " ^ string_of_resulttype (label c x));
//	  let ts0, t1 = Lib.List.split_last (label c x) in
//	  require (is_reftype t1) e.at ("… but label has " ^ string_of_valtype t1);
//	  let ht = match t1 with RefT (_nul, ht) -> ht | _ -> assert false in
//	  (ts0 @ [RefT (Null, ht)]) --> ts0, []
//
// # The heaptype comes off the *label*, not off the stack, and that is the inversion
//
// br_on_null peeks the operand and leaves the label alone; this one reads the label's last type and
// derives the operand requirement from it. Two rules that look like mirror images and take their
// type from opposite ends — which is why they are two functions here rather than one with a flag,
// and why the sibling hazard brOnCast's header names does not apply: there is no swap that turns one
// into the other.
//
// So the reference's two `require`s are about the *label*, and both say "instruction requires
// reference type but label has": an empty label prints the whole (empty) list, a non-reference last
// type prints that type. Kept as two branches with one message shape, because the reference's two
// strings differ only in which of the two it renders.
//
// **Bottom is admitted by the require and then not asserted about**, which is the one place this
// arm reads differently from the reference and reads *weaker* deliberately. `is_reftype` is
// `RefT _ | BotT -> true` (`types.ml:88-90`), so the require lets bottom through — and the next line
// there is `match t1 with RefT … | _ -> assert false`, an assert bottom would fire. It cannot arrive:
// `t1` is a label type, and label types come from declared block types, which have no bottom form.
// Where the reference asserts, this proceeds — `WithNull(true)` on bottom is bottom — which is the
// same claim about reachability made without a panic in a validator (isNumOrVec's posture).
//
// No explicit bottom arm is needed for that, and the first draft had one (`!t1.IsRef() && t1 !=
// unknown`) that was **dead by construction**: both bottoms are spelled as indexed reference types
// here (botHeapIdx), and `IsRef` reports true for the indexed kind, so the extra clause could never
// change the branch. Left in, it would have read as *IsRef excludes bottom* — a condition asserting
// the opposite of what the representation does, which is the shape that makes review confirm a bug.
//
// The branch carries `ts0 @ [t1]` — the non-null reference — and the fall-through is `ts0` alone:
// the operand is *consumed* on the fall-through path, since a null has nothing to unwrap. The
// mirror of br_on_null in both places.
func (v *validator) brOnNonNull(depth uint32) error {
	f, err := v.label(depth)
	if err != nil {
		return err
	}
	if len(f.labelTypes) == 0 {
		return fmt.Errorf("%w: instruction requires reference type but label has %s",
			ErrTypeMismatch, typeList(f.labelTypes))
	}
	ts0, t1 := f.labelTypes[:len(f.labelTypes)-1], f.labelTypes[len(f.labelTypes)-1]
	if !t1.IsRef() {
		return fmt.Errorf("%w: instruction requires reference type but label has %s",
			ErrTypeMismatch, typeStr(t1))
	}
	if err := v.popExpect(t1.WithNull(true)); err != nil {
		return err
	}
	if err := v.popExpectAll(ts0); err != nil {
		return err
	}
	v.pushAll(ts0)
	return nil
}

// callRef is `CallRef x` (`valid.ml:532-534`):
//
//	| CallRef x ->
//	  let (ts1, ts2) = func_type c x in
//	  (ts1 @ [RefT (Null, UseHT (Def (type_ c x)))]) --> ts2, []
//
// # The index is a *type* index, and the operand is a reference to that type
//
// `call` and `return_call` take a function index; these two take a type index, and the callee comes
// off the stack as a `(ref null $t)`. Reading `funcTypeAt` — this package's function-index helper —
// instead of `funcType` would resolve the wrong index space and be wrong only for modules where the
// two spaces disagree, which is a majority of the corpus but not the small modules a slice is first
// tested on.
//
// `func_type` is `call_indirect`'s own resolver, reused rather than re-derived: same reference
// function, same message, and its kind-check disagreement with `struct_type`/`array_type` is ADR
// 0032's open decisions-needed item. Slice 8 doubles that message's call sites and still brings no
// witness — `non-function type` appears in no vector in the suite — so the item stands as flagged
// rather than resolved here.
//
// The `Null` bit on the operand is the reference's, and it is what makes `(call_ref $t)` on a
// `(ref null $t)` legal: the null is a *runtime* trap (`eval.ml`'s `NullFuncRef`), not a typing
// error. Requiring the non-nullable form would reject the common case.
func (v *validator) callRef(idx uint32) error {
	ft, err := funcType(v.mod, idx)
	if err != nil {
		return err
	}
	if err := v.popExpect(binary.RefType(idx, true)); err != nil {
		return err
	}
	if err := v.popExpectAll(ft.Params); err != nil {
		return err
	}
	v.pushAll(ft.Results)
	return nil
}

// returnCallRef is `ReturnCallRef x` (`valid.ml:552-558`):
//
//	| ReturnCallRef x ->
//	  let (ts1, ts2) = func_type c x in
//	  require (match_resulttype c.types ts2 c.results) e.at
//	    ("type mismatch: current function requires result type " ^ … ^
//	     " but callee returns " ^ …);
//	  (ts1 @ [RefT (Null, UseHT (Def (type_ c x)))]) --> ... [], []
//
// # It is callRef plus a result-type require plus `-->...`, and each of the three is load-bearing
//
// The require is the tail call's whole safety condition: the callee's results become *this*
// function's results without a frame in between, so they have to satisfy what this function
// promised. `matchResultType` and not equality — a callee returning `(ref $t)` may tail-call from a
// function declaring `funcref`, and the relation is the one place that is decided.
//
// `-->...` is the polymorphic tail: control does not come back, so the frame goes unreachable after
// the operands are popped, exactly as `return` and `br` do. Omitting `setUnreachable` accepts
// nothing extra and rejects every `(return_call_ref $t)` that is not the last instruction of a
// void frame — the reject direction, and the one the corpus's `return_call_ref.wast` rows are
// thickest on.
//
// **`c.results` is `v.frames[0].labelTypes`**, which is the function-body frame's, and it is read
// from there rather than re-resolved from the module for `opReturn`'s reason: the body frame was
// built from the function's declared results, so a second lookup would be a second derivation of a
// fact one frame already carries. The other two tail-call opcodes — `return_call` 0x12 and
// `return_call_indirect` 0x13 — are the *tail-call proposal* and live in `tailcall.go` (slice 9,
// ADR 0035); this one arrives with function-references, which is the accident of proposal boundaries
// that puts one of three siblings in this slice.
//
// This sentence said they "stay declined" until slice 10 swept it, which is one slice after the one
// that falsified it: slice 9 typed the pair and re-pointed the controls that name them, and left the
// prose beside the *third* sibling — the site a reader would reach from this function, and the one
// place the pair is described rather than implemented. A repair that makes its own site look settled
// is the staleness nobody goes looking for.
func (v *validator) returnCallRef(idx uint32) error {
	ft, err := funcType(v.mod, idx)
	if err != nil {
		return err
	}
	results := v.frames[0].labelTypes
	if !matchResultType(tctx{gotMod: v.mod, wantMod: v.mod}, ft.Results, results) {
		return fmt.Errorf("%w: current function requires result type %s but callee returns %s",
			ErrTypeMismatch, typeList(results), typeList(ft.Results))
	}
	if err := v.popExpect(binary.RefType(idx, true)); err != nil {
		return err
	}
	if err := v.popExpectAll(ft.Params); err != nil {
		return err
	}
	v.setUnreachable()
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
//   - **tables'** initializer expressions (`table` = `tabletype tt ++ const c`), which is the
//     source the section below records arriving.
//
// # The sixth source arrived with #419, and it arrived on the schedule this comment predicted
//
// What stood here was a declared over-rejection: `free.ml`'s `table` contributes a table's own
// initializer, `binary.Table` retained none — the decoder read it and dropped it — so `(table 1
// funcref (ref.func $f))` could not reach this set and its `ref.func` was reported undeclared. The
// comment named the surfacing condition exactly: *"it is queued behind #8, and it will become one
// the moment the encoder learns the field."* #419 is that moment, all four of its layers, and this
// walk is the last of them.
//
// **The prediction was right and the tracking was not, which is the part worth keeping.** The
// deferral's citation was #8 — the *encoder's* issue, not this gap's — so the only thing standing
// between #419 and shipping an over-rejection was an instrument, and one fired: the all-on lane's
// over-rejection table (#341's accept direction) reported `elem.wast:87` and `table.wast:93`, two
// modules whose sole mention of the function outside a body is the table initializer
// (`(table $t 10 (ref func) (ref.func $f))`; `(table $t2 10 funcref (ref.func $dummy))`). Had that
// table not existed, the reject direction's own corpus could not have found this — a rule that
// over-rejects refuses a module no `assert_invalid` is watching. A declared gap whose surfacing
// condition is *another issue's completion* has no tripwire of its own, and nothing here fails when
// the condition is met.
//
// Nothing shipped wrong: `Init` arrived in the same branch as this walk, so main never had a
// retained initializer for this set to miss. That is why the record is a discharge and not a grave.
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
	// `free.ml`'s `table`, and only the defined tables have one: an imported table is a
	// `binary.TableType` in `m.Imports` with no initializer to walk (see `binary.Table.Init`).
	for i := range m.Tables {
		add(m.Tables[i].Init)
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
