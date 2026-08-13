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
//
// **Called, and it came in: rung 4 drained all 68 and inverted none.** `ref_eq.wast` went
// **14 pass / 69 fail → 83 / 0** the moment `ref.i31` got an arm (#255) — `init`'s last missing
// constructor, rungs 2 and 3 having already landed `struct.new` and `array.new` — so the file is
// fully green and the +55 the wrong `ref.eq` was worth has evaporated exactly as predicted. Kept as a
// confirmed prediction rather than deleted, because the *point* of the paragraph above is that the
// board rewarded the defect for four rungs and the only defence was reading `value.ml:127`. Note the
// instrument lesson it also produced: the 68 were forecast **here**, in prose, and #255's
// co-blocking probe measured only the `fb 1c` bucket, so the PR's own forecast under-counted its
// reward by 2.9× while the answer sat in this comment (`spec_test.go`'s floor decomposition records
// it).
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
	// **An externalized reference on either side compares its *payload*, and this clause must come
	// before `IsI31`** — `extern.ml:11-14` is the one override in the family that recurses:
	//
	//	| ExternRef r1', ExternRef r2' -> Value.eq_ref r1' r2'
	//	| _, _ -> eq_ref' r1 r2
	//
	// So two externrefs are equal exactly when what they wrap is equal, and a mixed pair falls to the
	// base `(==)` on two different constructor blocks — `false`, not an error, the same delegation
	// reading the i31 and aggregate clauses already had to be corrected to.
	//
	// **The ordering is load-bearing in a way the other clauses' is not**, because an externalized
	// i31 has `IsI31` set too: reached in declaration order, `ExternRef (I31Ref 5)` versus a bare
	// `I31Ref 5` would answer **true** where the reference answers false (neither pair matches an
	// override, so the base compares two distinct blocks). That is a bit encoding leaking through a
	// clause that was written when no bit existed — the same hazard `typeOfRef`'s arm order carries,
	// and the reason both places state it rather than relying on the reader noticing twice.
	//
	// Clearing the bit and recursing is exactly `Value.eq_ref r1' r2'`: the recursion is on the
	// *unwrapped* references, and depth cannot exceed one (0027 decision 3, and
	// `execExternConvertAny` reports an attempt to nest), so this recurses at most once.
	if a.Externalized || b.Externalized {
		if !a.Externalized || !b.Externalized {
			return false, nil
		}
		a.Externalized, b.Externalized = false, false
		return refEq(a, b)
	}
	// **A host reference compares by identity, and `Addr` is what carries it** — `script.ml:91-95`,
	// `| HostRef n1, HostRef n2 -> n1 = n2`, structurally the same shape as the i31 override below
	// and for a related reason: a host reference's identity *is* its number, the harness having no
	// allocation to point at. A mixed pair is `false` by the same delegation as everywhere else.
	//
	// Placed ahead of the two error returns rather than beside `IsI31` — a host reference sets no
	// pointer field, so without this clause it would reach the funcref error and be reported as
	// something it is not, which is grave #36's class (the engine's testimony naming a kind the value
	// never had). It must also come ahead of them for the *mixed* pairs: host-versus-funcref and
	// host-versus-exnref both answer `false` in the reference, so an error there would be a refusal
	// where an answer exists.
	if a.IsHost || b.IsHost {
		return a.IsHost && b.IsHost && a.Addr == b.Addr, nil
	}
	// **An i31 on either side answers structurally, and the mixed pair answers `false` rather than
	// #9's** — `i31.ml:20`'s `I31Ref i1, I31Ref i2 -> i1 = i2`, the one override in the family that
	// compares a payload instead of installing `failwith`. Two `ref.i31` of the same integer are
	// `eq` however they were produced, which is the whole reason `i31ref` is under `eq` in the
	// lattice where `funcref` is not: there is no allocation to have an identity, so structural
	// *is* the identity.
	//
	// Both sides are already masked to 31 bits at construction (`i31op.go`'s `i31Mask`), so this is
	// a plain `==` and not a comparison of two maskings — the narrow-on-store contract paying for
	// itself at its second reader.
	//
	// **The placement that matters is ahead of the two error returns, not ahead of `Obj`.** Against
	// an aggregate the answer is `false` either way, the kinds being mutually exclusive; against an
	// exnref or a funcref it is `false` here and `ErrNotValidated` below, and `false` is the
	// reference's answer — each override matches only its same-kind pair and delegates the rest to
	// the base `(==)`, so `I31Ref` versus `FuncRef` never reaches `instance.ml:42`'s `failwith`.
	// That is the correction the aggregate clause below already had to make once; it is the same
	// correction, and it is what stops a legal `ref.eq` between an `i31` and a `struct` — which
	// `ref_eq.wast`'s table mixes deliberately — from being refused as unvalidated.
	if a.IsI31 || b.IsI31 {
		return a.IsI31 && b.IsI31 && a.I31 == b.I31, nil
	}
	// **An aggregate on either side answers by pointer identity, and `aggr.ml`'s *silence* is the
	// citation.** Every other runtime module chains an `eq_ref'` override for its own kind —
	// `instance.ml:42` and `exn.ml:26` install `failwith`, `i31.ml:21` compares payloads,
	// `extern.ml:13` unwraps and recurses — and `aggr.ml` installs **nothing**, so a
	// `StructRef, StructRef` pair falls through to the base `let eq_ref' = ref (==)`
	// (`value.ml:127`), OCaml physical equality. A struct or array's identity is its allocation,
	// and `a.Obj == b.Obj` is that, not a convenient approximation of it.
	//
	// **One expression covers the mixed pair too, and mixed is `false` rather than an error** —
	// which is a correction to the reading that looked obvious. Each override matches only its
	// *same-kind* pair and delegates `| _, _ -> eq_ref' r1 r2` to the base, so `StructRef` versus
	// `FuncRef` reaches `(==)` on two different blocks and answers false with no `failwith`. So
	// this must not report #9's for a mixed pair the way the same-kind funcref case below does:
	// the reference answers, and fidelity to it outranks a diagnostic we would prefer to emit.
	// `a.Obj == b.Obj` is false whenever exactly one side is an aggregate, which is that answer.
	//
	// The one place the two models could differ is unreachable here: OCaml compares the
	// *constructor block*, so two distinct `StructRef s` blocks wrapping one `s` would be
	// unequal where two `ref`s sharing an `Obj` are equal. Nothing re-boxes — `eval.ml:679`
	// builds the block once at `struct.new` and every later copy copies the pointer — so the
	// distinction has no witness.
	if a.Obj != nil || b.Obj != nil {
		return a.Obj == b.Obj, nil
	}
	// Non-null non-aggregate on both sides, so the payload kind decides — and each remaining kind
	// is one the reference declares unreachable here. Reported per kind rather than under one
	// message, because the two are different modules to go and fix: an exnref reaching `ref.eq`
	// is a `try_table` body the validator would have rejected, a funcref reaching it is a
	// `ref.func` where an `eqref` was wanted.
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
	// **This entry's reason inverted when `IsHost` landed, and the value it names did not change** —
	// which is #260's whole point about a map of justifications. `Addr` was "not compared: reachable
	// only on a funcref, which refEq reports as #9's", and both halves of that were true when
	// written; a host reference now reaches `refEq` with its identity in this field and `refEq`
	// compares it (`script.ml:93`). A coverage control asking only "is there an entry for `Addr`?"
	// was green across that inversion and had nothing to say about it, because the entry existed
	// throughout. That is what the witness pairs in `TestRefEqTreatmentsHaveWitnesses` are for.
	"Addr":         "compared when IsHost: a host reference's identity is its number (script.ml:93); not compared on a funcref, which refEq reports as #9's",
	"Inst":         "not compared: reachable only on a funcref, which refEq reports as #9's",
	"Exc":          "not compared: reachable only on an exnref, which refEq reports as #9's",
	"Obj":          "compared by pointer identity: an aggregate's identity is its allocation (0020)",
	"IsI31":        "compared: selects the i31 clause, and a mixed pair answers false (i31.ml:20)",
	"I31":          "compared structurally: an i31 has no allocation, so the payload is the identity",
	"Externalized": "compared: both set recurses on the unwrapped payloads, a mixed pair answers false (extern.ml:13)",
	"IsHost":       "compared: selects the host clause, whose identity is Addr, and a mixed pair answers false (script.ml:93)",
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
