// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package interp

import "fmt"

// The two externref conversions of the 0xfb region — rung 5 slice 3 of the GC ladder (#258), and
// decision 0027's Q2/Q3 option A in executable form.
//
// The authority is `eval.ml:929-939`, four arms of `ExternConvert`, and it is worth quoting in full
// because the shape of what is *missing* from it decides both arms below:
//
//	| ExternConvert Internalize, Ref NullRef :: vs'                -> Ref NullRef :: vs'
//	| ExternConvert Internalize, Ref (Extern.ExternRef r) :: vs'   -> Ref r :: vs'
//	| ExternConvert Externalize, Ref NullRef :: vs'                -> Ref NullRef :: vs'
//	| ExternConvert Externalize, Ref r :: vs'                      -> Ref (Extern.ExternRef r) :: vs'
//
// **These two arms are the whole of `extern.wast`'s blocker**, measured: all 11 of the file's all-on
// failures read `no instance:` rather than one bucket per vector, because `extern.wast:6-7` put the
// conversions in *global initializers* — `(global externref (extern.convert_any (ref.null any)))` —
// so nothing in the file instantiates until they exist. One missing arm at the front of an init
// sequence blocks every vector behind it, which is slice 2's own init-sequence lesson arriving from
// the other end: there the question was which write inside the sequence the arm was missing at, here
// it is that the write is a global's and therefore ahead of every function in the file.
//
// **One arm serves both the function path and the const-expression path**, which is worth stating
// because this slice's pre-registration implied two sites and measurement said one: `runConst` builds
// a `binary.Func` from the expression and calls `in.run`, the same interpreter loop with the same
// dispatch, so a const-legal opcode needs no second home. The forecast's conclusion held (globals
// need these arms) and its implied work did not.
const (
	opAnyConvertExtern = 0x1a
	opExternConvertAny = 0x1b
)

// execExternConvertAny is `extern.convert_any` — `ExternConvert Externalize`, `eval.ml:935-939`.
//
// **A null stays a null, and the reference says so with a dedicated arm rather than by implication**
// (`eval.ml:935-936`, matched *before* the general `Ref r` arm). So `ref.null any` externalized is
// still the one null reference value — `value.ml:20`'s nullary `NullRef` — and not a null carrying an
// `Externalized` bit. Wrapping it would manufacture a second null that a cast could distinguish,
// which is grave #266's deviation rebuilt in a new field: that grave was the *harness* asserting a
// distinction between nulls the reference does not have, and this arm is where the **engine** would
// acquire one.
//
// **No vector can score this arm, and the first draft of this comment claimed two do.** It named
// `extern.wast:43` — `(assert_return (invoke "externalize" (ref.null any)) (ref.null extern))` — and
// `:45`, the table slot holding `ref.null any`. Both are answerable and both pass, but what they
// score is that a null *stays a null*, which a mutation setting the bit on a null preserves: it was
// run, and the board did not move. The mechanism is that every reader of `Externalized` guards on
// `Null` first — `typeOfRef` (castop.go, the bottom-heaptype arm), `refEq` (refop.go, the
// two-nulls-are-equal arm) and `fromRef` (value.go) — so a null carrying the bit is unobservable by
// construction, and no vector could distinguish it however the corpus grew. The arm is right for
// canonical-representation reasons alone: one null reference value, which is grave #266's law.
//
// **Externalizing an already-externalized reference is reported, not flattened.** The reference would
// build `ExternRef (ExternRef r)`; a bit cannot express depth two, so silently returning the operand
// unchanged would be the engine answering a question it cannot represent — an accept-direction lie of
// exactly the kind no vector can catch. It is unreachable through a validated module (the operand's
// static type is `anyref`, and `externref` is not under `any`), which makes it the declared-and-tracked
// case rather than a missing check wearing a disguise.
func execExternConvertAny(st *stack) error {
	if short := st.needRef(1); short != nil {
		return short
	}
	r := st.popRef()
	switch {
	case r.Null:
		// Unchanged, per the dedicated arm above.
	case r.Externalized:
		return fmt.Errorf("%w: extern.convert_any on an already-externalized reference, which "+
			"would nest a wrapper the type system permits only one level of (0027 decision 3)",
			ErrNotValidated)
	default:
		r.Externalized = true
	}
	st.pushRef(r)
	return nil
}

// execAnyConvertExtern is `any.convert_extern` — `ExternConvert Internalize`, `eval.ml:929-933`.
//
// **The payload is untouched in both directions**, which is the option's whole dividend: this arm
// clears one bit and `extern.wast:52-57`'s round trip is an identity by construction rather than by
// three arms agreeing with each other. The corpus *would* check the composition and not the halves —
// `externalize-ii` is `any.convert_extern (extern.convert_any (table.get x))`, and its expectations
// at `:53-56` are `(ref.i31)`, `(ref.struct)`, `(ref.array)`, `(ref.host 0)`, i.e. that the wrapper
// preserved the inner runtime constructor across the pair.
//
// **It did not until #270, and this comment's first draft said it did.** All four of those lines were
// scored `unsupported`, not pass or fail: their expectations are `RefTypePat` heaptype patterns
// (`parser.mly:1517-1530`), which `valKind` refused, so the harness declined the assertion before the
// engine was asked. #270 is what made them askable — the widening was a public-surface question (an
// `anyref` named ValType, plus payload-kind discrimination on `interp.Value`) and so had its own ADR
// and stamp (0039), not this slice's.
//
// **The pre-registration this paragraph carried, and its result.** It read: "`IsHost` is scored by
// nothing at all today, and the arithmetic says which vectors will score it: exactly the three that
// mention a host reference, `:39`, `:42` and `:56`, all three unsupported" — against the measured
// six, `{39, 42, 53, 54, 55, 56}`, of `extern.wast`'s 18 commands. #270 landed all six as passes in
// the all-on lane, and the claim was checked the only way a "scored by" claim can be: **mutate the
// boundary and read which vectors go red.**
//
//   - Identity-only mutation (`fromRef`'s `v.RefID = r.Addr` → `r.Addr + 1`, payload kind untouched):
//     **`:39`, `:42`, `:56` and nothing else.** Confirmed to the vector.
//   - Kind mutation (`payloadOf`'s host arm → `PayloadNone`): those three **plus `:49`**. Not a
//     fourth identity vector — `:49`'s expectation is the bare `(ref.extern)` wildcard, and it fails
//     because `RefPat.admits` refuses `PayloadNone` up front as an engine inconsistency. So the second
//     probe is coarser than the claim: it scores payload *determinacy*, which the wildcard does check.
//
// Recorded as two probes rather than one because the first was the one that could falsify the
// sentence, and the second's extra row would have read as the forecast being wrong by one.
//
// The field is `RefKind == PayloadHost` now rather than a boolean `IsHost`, which is #270's retyping.
// Before it, the whole defence was accept-direction: `script.ml:80` (a host reference's dynamic
// heaptype is `any`), `script.ml:93` (its identity is its index), and the unit controls in
// `externop_test.go` — which is §9 G-3's case for such controls being product work rather than
// overhead, since the suite scored a fabricated identity green by construction. Those controls stay:
// three corpus vectors are three, and `TestFromRefDoesNotFabricateAHostIdentity` still separates
// "identity zero" from "no identity" at the boundary rather than one command downstream.
//
// **Internalizing a non-null reference that is not externalized is an error, and that is a
// correction to what this arm was first written as.** The first draft returned the operand unchanged
// on the reasoning that the arm cannot tell how the value reached that slot and #9 owns the question
// statically. Reading `eval.ml` killed it: there are exactly four arms, and a non-null
// non-`ExternRef` operand matches **none** of them, so the reference falls through to its own crash
// case. The draft also mis-cited its evidence — it claimed `extern.wast:39`'s `(ref.extern 1)`
// argument is a bare host reference that carries no bit, and `parser.mly:1502` is
// `LPAR REF_EXTERN NAT RPAR { Extern.ExternRef (Script.HostRef …) }`, i.e. **already externalized**.
// `(ref.host N)` (`parser.mly:1501`) is the unwrapped form, and it appears only as an *expectation*
// at `:39`/`:56` and as an argument to `externalize` at `:42` — never as an operand here. So the
// identity reading was wrong about the reference and wrong about the corpus it cited, in the same
// direction: it made this arm total when the authority makes it partial.
func execAnyConvertExtern(st *stack) error {
	if short := st.needRef(1); short != nil {
		return short
	}
	r := st.popRef()
	switch {
	case r.Null:
		// Unchanged, per `eval.ml:929-930`'s own dedicated arm.
	case r.Externalized:
		r.Externalized = false
	default:
		return fmt.Errorf("%w: any.convert_extern on a non-null reference that is not an "+
			"externref (eval.ml:929-933 has no arm for this operand, the reference asserting "+
			"validation ruled it out)", ErrNotValidated)
	}
	st.pushRef(r)
	return nil
}
