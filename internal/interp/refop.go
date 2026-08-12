// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package interp

import (
	"fmt"
	"reflect"
)

// The two reference operators that are neither a construction nor a null test, named for
// control.go's reason: a bare 0xd3 in a switch arm is a byte and these are a family.
//
// `opRefNull`/`opRefIsNull`/`opRefFunc` live in table.go because an *element expression* can
// hold two of the three, which is the question that block answers. Neither of these two can
// appear in one, so they get their own block here rather than widening a block whose doc comment
// is about element expressions into one that is about nothing in particular.
const (
	opRefEq        = 0xd3
	opRefAsNonNull = 0xd4
)

// trapNullRef is `ref.as_non_null`'s trap — `eval.ml:642-643`'s
// `Trapping "null reference"`.
//
// **Oracle-covered, unlike most of this engine's trap text**: `ref_as_non_null.wast:27` is
// `(assert_trap (invoke "nullable-null") "null reference")`, and the harness matches the string
// verbatim, so this is one of the cases #38's refinement names — the expected string *is* the
// whole message, not a sentinel with our own tail behind it. Four more vectors in
// `ref_cast.wast` want the identical text from `ref.cast`, which is rung 5's arm and will share
// this var rather than spelling it again.
var trapNullRef = &Trap{Reason: "null reference"}

// trapNullFuncRef is `call_ref`/`return_call_ref`'s trap — `eval.ml:266-267` and `:288-289`,
// both `Trapping "null function reference"`.
//
// A *different* string from trapNullRef, and the difference is the reference's, not a stylistic
// choice: `RefAsNonNull` says "null reference" and `CallRef` says "null function reference".
// Oracle-covered in both files that ask (`call_ref.wast:97`, `return_call_ref.wast:183`), so
// collapsing the two into one message would fail two vectors and pass nothing.
var trapNullFuncRef = &Trap{Reason: "null function reference"}

// refEq is `ref.eq` — `eval.ml:661-662`'s `value_of_bool (eq_ref r1 r2)`.
//
// # The reference's equality is physical, and that is a citation rather than a shortcut
//
// `Value.eq_ref' = ref (==)` (`value.ml:127`) — OCaml *physical* equality, the default every
// reference kind either inherits or overrides. Decision 0020 priced this and chose the same
// thing for the same reason: a struct or array instance is a Go pointer and `ref.eq` compares
// the pointers, because `aggr.ml` registers no `eq_ref'` hook at all. So the cheap
// implementation is the *specified* one, not a cheap implementation that got lucky.
//
// The overrides, all four of them, because which kinds are comparable is the whole content of
// this function:
//
//   - `i31.ml:20` — `I31Ref i1, I31Ref i2 -> i1 = i2`, structural on the boxed int, since two
//     `ref.i31` of the same payload are `eq` however they were produced. Rung 4's arm.
//   - `extern.ml:13` — `ExternRef r1', ExternRef r2' -> eq_ref r1' r2'`, unwrapping to the
//     wrapped reference's own equality.
//   - `instance.ml:42` — `FuncRef _, FuncRef _ -> failwith "eq_ref"`.
//   - `exn.ml:26` — `ExnRef _, ExnRef _ -> failwith "eq_ref"`.
//
// # Why two of those overrides are a `failwith`, and what this engine does with that
//
// **`failwith` is the reference asserting the case is unreachable in a validated module, not a
// verdict.** `ref.eq`'s operands are typed `eqref` (`valid.ml`'s `RefEq` arm), and neither
// `funcref` nor `exnref` is under `eq` in the subtype lattice — so a module presenting one to
// `ref.eq` is rejected before it runs, and the reference interpreter, which validates first,
// can afford to crash there.
//
// This engine has no validator (#9), so the case *is* reachable here, and the project's standing
// answer applies: report the layering debt under `ErrNotValidated` and never invent a verdict of
// this package's own. That is `needNum`'s reading, `branch`'s reading of an out-of-range depth,
// and `declaredFuncType`'s reading of a type index naming a struct. Returning `false` instead
// would be the tempting alternative and is the wrong one twice over — it answers a question #9
// owns, and it answers it in the **accept direction**, where §9 G-3 says the suite scores the
// defect green by construction.
//
// # What is not here yet, and the tripwire that says so
//
// Nothing this engine can construct today is `eq`-comparable except null: `ref` holds a funcref
// (`Addr`+`Inst`) or an exnref (`Exc`), and both are the `failwith` cases above. The i31, struct,
// and array payloads that make `ref.eq` interesting arrive at rungs 2-4 of #172's ladder, each
// adding a field to `ref` (0020's `Obj *gcObj`) that this function must then account for.
//
// A comparison that silently ignores a new payload field is exactly the accept-direction defect
// the paragraph above refuses to commit deliberately, so the obligation is pre-registered as a
// failing test rather than as an intention: `refEqTreatment` below names every field of `ref` and
// what this function does with it, and `TestRefEqAccountsForEveryRefField` fails the moment the
// struct grows a field the map does not mention. *A design debt is discharged by a tripwire,
// never by an intention.*
//
// # `ref_eq.wast` is a board trap: the wrong implementation buys 55 passes
//
// Measured, by falsification M11 of #172's rung-1 battery. Mutate the null clause below to
// `return false` — two nulls no longer equal, which is unambiguously wrong — and `ref_eq.wast`
// goes from **14 pass / 69 fail to 69 pass / 14 fail**. The wrong `ref.eq` is worth **+55**
// vectors on the all-on lane, and it is worth them *today*, in the tree as it stands.
//
// The mechanism is rungs 2-4's absence, not a coincidence: `ref_eq.wast`'s table is populated by an
// `init` function built from `struct.new`, `array.new`, and `ref.i31`, none of which have arms, so
// `init` fails and every slot stays `ref.null`. Correct semantics therefore answers **1** to every
// `eq i j` in the file — passing only the ~14 vectors that expect 1 — while `return false` answers
// **0** everywhere and passes the ~69 that expect 0. The board rewards the defect by a factor of
// five, and it does so *silently*: both readings are green-looking engine arms, and the bucket the
// 55 come out of is `assert_return value mismatch`, which names no opcode.
//
// This is why the arm is written from `value.ml:127` and not from the board, and it is the sharpest
// specimen this package has of the standing law — *the spec is the objective function; the suite
// samples it*. An engine tuned to pass count picks the wrong `ref.eq` here, and no instrument in
// the project would object until rung 2 lands and 55 passes evaporate. The corollary for the
// reader arriving at rung 2: the 68 `value mismatch` fails this file currently contributes are
// **expected and correct**, so do not read them as a defect in this function, and do check that
// they *drain* rather than invert when `init` starts running.
func refEq(a, b ref) (bool, error) {
	// **Null first, and both directions in one expression.** `NullRef` is a constant
	// constructor, so `(==)` makes two nulls physically equal and a null never equal to an
	// allocation — which is why `ref_eq.wast`'s `eq 0 1` expects **1** even though slot 0 is
	// `ref.null eq` and slot 1 is `ref.null i31`: the heaptype a null was spelled with is not
	// part of its identity. Reading it as "same static type and both null" would fail that
	// vector and is the reading `opRefNull`'s own arm warns about (it pushes one value for all
	// thirteen heaptypes).
	if a.Null || b.Null {
		return a.Null && b.Null, nil
	}
	// Non-null on both sides, so the payload kind decides — and today every payload kind this
	// engine can build is one the reference declares unreachable here. Reported per kind rather
	// than under one message, because the two are different modules to go and fix: an exnref
	// reaching `ref.eq` is a `try_table` body the validator would have rejected, a funcref
	// reaching it is a `ref.func` where an `eqref` was wanted.
	if a.Exc != nil || b.Exc != nil {
		return false, fmt.Errorf("%w: ref.eq on an exception reference, which is not under eq "+
			"(exn.ml:26 is `failwith`, the reference asserting validation ruled this out)",
			ErrNotValidated)
	}
	return false, fmt.Errorf("%w: ref.eq on a function reference, which is not under eq "+
		"(instance.ml:42 is `failwith`, the reference asserting validation ruled this out)",
		ErrNotValidated)
}

// refEqTreatment is refEq's claim about `ref`'s fields: one entry per field, saying what the
// comparison does with it. Checked against the struct by reflection in
// TestRefEqAccountsForEveryRefField — see refEq's own doc comment for why the obligation is a
// test rather than a comment.
//
// **The map is the claim and reflection supplies the domain**, which is the shape this project
// requires of a control: scoped to the space (`ref`'s whole field set, whatever it becomes) and
// not to the sample (`ref`'s fields as of today). An enumeration on both sides would agree with
// itself forever.
var refEqTreatment = map[string]string{
	"Null": "compared: two nulls are equal, a null and an allocation are not",
	"Addr": "not compared: reachable only on a funcref, which refEq reports as #9's",
	"Inst": "not compared: reachable only on a funcref, which refEq reports as #9's",
	"Exc":  "not compared: reachable only on an exnref, which refEq reports as #9's",
}

// refFieldTreatments reports refEq's declared treatment of every field `ref` actually has, and
// the fields it has no declaration for. Exported to the test rather than inlined there because
// the reflection is the *mechanism* the claim is checked by and belongs beside the claim.
func refFieldTreatments() (declared, undeclared, stale []string) {
	t := reflect.TypeFor[ref]()
	have := map[string]bool{}
	for i := range t.NumField() {
		name := t.Field(i).Name
		have[name] = true
		if _, ok := refEqTreatment[name]; ok {
			declared = append(declared, name)
		} else {
			undeclared = append(undeclared, name)
		}
	}
	for name := range refEqTreatment {
		if !have[name] {
			stale = append(stale, name)
		}
	}
	return declared, undeclared, stale
}
