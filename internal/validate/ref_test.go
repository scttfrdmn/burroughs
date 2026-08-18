// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package validate

import (
	"errors"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// gcOn is the gate the GC-only heaptype spellings need at the *decoder*, which is the layer these
// rows are not about.
func gcOn(f *binary.Features) { f.GC = true }

// TestRefNullTypesTheHeapTypeItWasSpelledWith is the accept-direction control for `refNull`, and it
// is the direction contract §9 G-3 says no corpus can score.
//
// Every row is a **valid** module, one per heaptype, whose result type is exactly the type the
// instruction should produce. A validator that invented a type instead of reading the side table
// would pass the `func` row — `(ref null func)` is what most `ref.null` vectors spell, so the
// invention that suggests itself is the one the majority of the corpus agrees with — and reject the
// other twelve. Which is the reject direction and therefore visible; what is *not* visible is the
// mirror case in the row below it, where the wrong type is *accepted* into a wider requirement.
//
// The pairing is the point. Each row asserts the type is right by two mutually exclusive readings:
// the module whose result matches is accepted, and a module whose result is the same type in the
// *other* family is rejected. One assertion alone is satisfiable by a validator that answers
// `unknown` — bottom matches everything, in both directions.
//
// Measured, not asserted: pushing `binary.FuncRef` for every heaptype fails **12 of these 13 rows**,
// and the survivor is `func` — whose right answer *is* `FuncRef`. So a control built from the
// obvious case would have been protected by coincidence on exactly the case it was built from, which
// is why the list is the whole vocabulary rather than a sample of it. That is the same figure
// `binary.TestRefNullRetainsTheSpelledHeapType` records one layer down, and the agreement is not
// redundancy: there it is the *retention* that fails, here it is the *typing*, and a validator that
// re-derived the type would pass the decoder's control and fail this one.
func TestRefNullTypesTheHeapTypeItWasSpelledWith(t *testing.T) {
	// `spell` is the heaptype as written after `ref.null`; `result` is the function result type the
	// instruction's own type satisfies exactly. The two differ wherever the abbreviation exists
	// (`func` → `funcref`), which is grave #180's spelling rule and not a transformation this rule
	// applies.
	cases := []struct{ spell, result string }{
		{"func", "funcref"},
		{"extern", "externref"},
		{"any", "anyref"},
		{"eq", "eqref"},
		{"i31", "i31ref"},
		{"struct", "structref"},
		{"array", "arrayref"},
		{"none", "nullref"},
		{"nofunc", "nullfuncref"},
		{"noextern", "nullexternref"},
		{"exn", "exnref"},
		{"noexn", "nullexnref"},
	}
	// The vocabulary is thirteen forms, twelve abstract plus the indexed one, and the indexed one is
	// a separate row below because its spelling needs a type in the module. Pinned rather than
	// derived: the authority for "how many heaptypes are there" is `binary.decodeHeapType`, which
	// this package cannot read, and `binary.TestRefNullRetainsTheSpelledHeapType` is the control that
	// holds the derived version of this domain. What a pin buys here is that a *deletion* from this
	// list is louder than a passing test with one fewer row in it.
	if len(cases) != 12 {
		t.Errorf("%d abstract heaptype rows, want 12 — the thirteenth form is the indexed one and "+
			"has its own row below; a missing abstract form is a family this rule is untested on",
			len(cases))
	}

	for _, c := range cases {
		t.Run(c.spell, func(t *testing.T) {
			ok := `(module (func (result ` + c.result + `) (ref.null ` + c.spell + `)))`
			if _, err := validated(t, ok, gcOn); err != nil {
				t.Errorf("(ref.null %s) does not satisfy a %s result: %v", c.spell, c.result, err)
			}
			// The other family. `funcref` for everything that is not itself in the func hierarchy,
			// `externref` for the ones that are — chosen so the requirement is genuinely unrelated to
			// the operand rather than merely a different spelling of a supertype.
			other := "funcref"
			switch c.spell {
			case "func", "nofunc":
				other = "externref"
			}
			bad := `(module (func (result ` + other + `) (ref.null ` + c.spell + `)))`
			_, err := validated(t, bad, gcOn)
			if !errors.Is(err, ErrTypeMismatch) {
				t.Errorf("(ref.null %s) was accepted as a %s result (%v) — the retained heaptype is "+
					"being widened or ignored, which is the accept direction", c.spell, other, err)
			}
		})
	}

	// The indexed form, which is the only one `check_heaptype` does any work for.
	t.Run("typeidx", func(t *testing.T) {
		ok := `(module (type $t (func)) (func (result (ref null $t)) (ref.null $t)))`
		if _, err := validated(t, ok, gcOn); err != nil {
			t.Errorf("(ref.null $t) does not satisfy a (ref null $t) result: %v", err)
		}
		// `check_heaptype`'s one non-trivial case, delegated to checkValType — and the message is the
		// index-space category, not `type mismatch`, because the index does not resolve at all.
		_, err := validated(t, `(module (func (result funcref) (ref.null 99)))`, gcOn)
		if !errors.Is(err, ErrUnknownType) {
			t.Errorf("(ref.null 99) in a module with one type reported %v, want ErrUnknownType — "+
				"`check_heaptype c ht` is the half of this rule that is not the pushed type", err)
		}
	})
}

// TestRefIsNullTakesAnyReferenceAndOnlyAReference is `refIsNull`'s control, and its two halves are
// the two ways a one-step version of that rule goes wrong.
//
// A rule that expected a fixed reference type would reject every other family — the reject
// direction, visible. A rule that expected nothing would accept `(i32.const 0) (ref.is_null)`, which
// is the accept direction and is why the numeric rows are here. Neither half alone distinguishes the
// implementation from the one next to it.
//
// The unreachable row is the third case and it is not decoration: `peek_ref` does not fail out of
// range, so a version of this rule that peeked and *required* a concrete reference would reject
// `(unreachable) (ref.is_null)` — a valid body, and `unreached-invalid.wast`'s whole axis.
func TestRefIsNullTakesAnyReferenceAndOnlyAReference(t *testing.T) {
	for _, ref := range []string{
		"funcref", "externref", "anyref", "eqref", "i31ref", "exnref",
		"nullref", "nullfuncref", "structref",
	} {
		t.Run(ref, func(t *testing.T) {
			wat := `(module (func (param ` + ref + `) (result i32) (local.get 0) (ref.is_null)))`
			if _, err := validated(t, wat, gcOn); err != nil {
				t.Errorf("ref.is_null refused a %s operand: %v", ref, err)
			}
		})
	}

	for _, num := range []string{"i32", "i64", "f32", "f64"} {
		t.Run(num, func(t *testing.T) {
			wat := `(module (func (param ` + num + `) (result i32) (local.get 0) (ref.is_null)))`
			_, err := validated(t, wat, gcOn)
			if !errors.Is(err, ErrTypeMismatch) {
				t.Errorf("ref.is_null accepted a %s operand (%v) — a rule that pops without "+
					"classifying is the accept direction, and `peek_ref`'s error is the whole "+
					"difference between the two", num, err)
			}
		})
	}

	t.Run("unreachable", func(t *testing.T) {
		wat := `(module (func (result i32) (unreachable) (ref.is_null)))`
		if _, err := validated(t, wat, nil); err != nil {
			t.Errorf("ref.is_null after `unreachable` was rejected (%v) — `peek` pads out of range "+
				"with bottom rather than failing, and bottom is a reference as far as this rule "+
				"cares", err)
		}
	})

	t.Run("empty", func(t *testing.T) {
		// The reachable empty stack, which `peek_ref` does *not* report: the pop does, as a shortage.
		// Asserted so the two paths above stay distinguishable — a rule that errored in `peek_ref`
		// would pass this row and fail the one before it.
		_, err := validated(t, `(module (func (result i32) (ref.is_null)))`, nil)
		if !errors.Is(err, ErrTypeMismatch) {
			t.Errorf("a bare ref.is_null in a reachable frame reported %v, want a type mismatch", err)
		}
	})
}

// TestRefFuncTypesAsTheFunctionsOwnType is the accept-direction control for `refFunc`'s result, and
// the row that matters is the concrete one.
//
// `[] --> [RefT (NoNull, UseHT (Def dt))]` names the function's type. The approximation available to
// a validator that did not resolve the index is `binary.FuncRef`, and it passes the `funcref` row
// here — which is exactly the row a control would be built from — while rejecting `(ref $a)`. So the
// concrete row is the discriminator and the `funcref` row is the vacuity check for it: it says the
// concrete answer still satisfies the abstract requirement, through `matchHeap`'s expand arm rather
// than by the rule weakening itself.
//
// The mismatched-type row is the other side: `(ref $b)` for a function of type `$a` must be
// rejected, or the resolution is happening and its answer is being ignored.
func TestRefFuncTypesAsTheFunctionsOwnType(t *testing.T) {
	const twoTypes = `(module (type $a (func)) (type $b (func (param i32)))
		(func $f (type $a)) (elem declare func $f) `

	t.Run("concrete", func(t *testing.T) {
		wat := twoTypes + `(func (result (ref $a)) (ref.func $f)))`
		if _, err := validated(t, wat, gcOn); err != nil {
			t.Errorf("(ref.func $f) does not satisfy a (ref $a) result where $f is of type $a: %v — "+
				"the result is the function's own deftype, and `funcref` is the approximation this "+
				"row exists to refuse", err)
		}
	})

	t.Run("satisfies funcref", func(t *testing.T) {
		wat := twoTypes + `(func (result funcref) (ref.func $f)))`
		if _, err := validated(t, wat, gcOn); err != nil {
			t.Errorf("(ref.func $f) does not satisfy a funcref result: %v — the concrete type reaches "+
				"the abstract requirement through the relation (`matchHeap` expands the index and "+
				"`matchNull` admits a non-nullable operand), and if it does not, every `(table "+
				"funcref (elem $f))` module is rejected", err)
		}
	})

	t.Run("wrong type", func(t *testing.T) {
		wat := twoTypes + `(func (result (ref $b)) (ref.func $f)))`
		_, err := validated(t, wat, gcOn)
		if !errors.Is(err, ErrTypeMismatch) {
			t.Errorf("(ref.func $f) was accepted as a (ref $b) result (%v) where $f is of type $a — "+
				"the index is being resolved and the answer discarded", err)
		}
	})

	t.Run("unknown function", func(t *testing.T) {
		// `func c x` before `refer_func`, which is the reference's order and is observable: a module
		// that gets both wrong reports the index and not the declaration.
		_, err := validated(t, `(module (func (result funcref) (ref.func 7)))`, nil)
		if !errors.Is(err, ErrUnknownFunc) {
			t.Errorf("(ref.func 7) in a one-function module reported %v, want ErrUnknownFunc — the "+
				"index-space lookup runs first, so an out-of-range index is never reported as "+
				"undeclared", err)
		}
	})
}

// TestRefFuncDeclarationCountsEverySourceTheReferenceFreeVariablePassDoes is `declaredFuncs`' own
// control, and its subject is the *set*, not the instruction.
//
// The rule is `refer_func` against `Free.module_` of the module with its bodies emptied and its start
// removed. Getting the set too small rejects valid modules; getting it too large accepts invalid
// ones. Both directions are here, and the too-large direction is the one no board sees.
//
// **Every source `declaredFuncs` walks gets a row, and each row's module mentions the function
// exactly once — in that source and nowhere else.** A module that also exported the function would
// pass every row for the wrong reason, which is why none of them do. The first draft of that function
// walked declarative element segments only, on the strength of `(elem declare …)` being the
// idiomatic spelling; the passive, active and index-form rows are what that draft would have failed.
//
// Measured, per source: dropping the export walk fails 1 row, the global walk 1, the element index
// form 1 here and 6 elsewhere in this file. Adding the function bodies to the set fails 2 — the
// bodies row and the start row, which is the tell that those two exclusions are one rule; adding the
// start section fails 1. Every source is therefore load-bearing for at least one row, which is what
// makes the enumeration a domain rather than a list of examples.
func TestRefFuncDeclarationCountsEverySourceTheReferenceFreeVariablePassDoes(t *testing.T) {
	// Each row's module declares `$f` through exactly one source and then reads it from a *body*,
	// which is the one place that cannot declare it.
	sources := []struct{ name, decl string }{
		{"export", `(export "f" (func $f))`},
		{"global initializer", `(global funcref (ref.func $f))`},
		{"elem declarative, index form", `(elem declare func $f)`},
		{"elem declarative, expr form", `(elem declare funcref (item (ref.func $f)))`},
		{"elem passive, expr form", `(elem funcref (item (ref.func $f)))`},
		{"elem passive, index form", `(elem func $f)`},
		{"elem active, index form", `(table 1 funcref) (elem (i32.const 0) func $f)`},
		{"elem active, expr form", `(table 1 funcref) (elem (i32.const 0) funcref (item (ref.func $f)))`},
	}
	for _, s := range sources {
		t.Run(s.name, func(t *testing.T) {
			wat := `(module (func $f) ` + s.decl + ` (func (result funcref) (ref.func $f)))`
			if _, err := validated(t, wat, nil); err != nil {
				t.Errorf("a $f declared only by %s was reported undeclared: %v — `Free.module_` "+
					"unions every mode and every holder, and a set built from the idiomatic "+
					"spelling alone rejects valid modules", s.name, err)
			}
		})
	}

	// The too-large direction, which is what makes every row above non-vacuous: a function mentioned
	// *only* from function bodies is undeclared, however many bodies mention it. `check_module` empties
	// `funcs` before computing the set precisely so a body cannot declare its own references.
	t.Run("bodies do not declare", func(t *testing.T) {
		wat := `(module (func $f)
			(func (result funcref) (ref.func $f))
			(func (result funcref) (ref.func $f)))`
		_, err := validated(t, wat, nil)
		if !errors.Is(err, ErrUndeclaredFunc) {
			t.Errorf("a $f mentioned only from two function bodies was accepted (%v) — if a body "+
				"contributes to the set, the whole rule is vacuous and every accept row above "+
				"passes for the wrong reason", err)
		}
	})

	// The start section, the second of `check_module`'s two exclusions. Hand-assembled rather than
	// written as wat, because `text.EncodeModule` emits no start section yet (#8) and a row the
	// encoder refuses says nothing about this package.
	//
	// type 0 is `[] -> []` for the start function; type 1 is `[] -> [funcref]` for the body that
	// reads the reference. Function 0 is named by `start` and by nothing else.
	t.Run("start does not declare", func(t *testing.T) {
		img := []byte{
			0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
			0x01, 0x08, 0x02, 0x60, 0x00, 0x00, 0x60, 0x00, 0x01, 0x70, // types
			0x03, 0x03, 0x02, 0x00, 0x01, // functions: type 0, type 1
			0x08, 0x01, 0x00, // start: function 0
			0x0a, 0x09, 0x02,
			0x02, 0x00, 0x0b, // body 0: end
			0x04, 0x00, 0xd2, 0x00, 0x0b, // body 1: ref.func 0, end
		}
		m, err := (&binary.Decoder{Features: binary.DefaultFeatures()}).DecodeModule(img)
		if err != nil {
			t.Fatalf("the hand-assembled image does not decode, so this row says nothing about the "+
				"validator: %v", err)
		}
		if !m.HasStart || m.Start != 0 {
			t.Fatalf("the image's start section did not survive decoding (HasStart=%v Start=%d), so "+
				"the exclusion under test is not present in the module", m.HasStart, m.Start)
		}
		if _, err := Module(m); !errors.Is(err, ErrUndeclaredFunc) {
			t.Errorf("a $f named only by the start section was accepted (%v) — `check_module` sets "+
				"`start = None` before computing the set, so the start function is not thereby "+
				"declared", err)
		}
	})

	// The source that is *not* covered, stated rather than omitted. `free.ml`'s `table` contributes a
	// table's own initializer expression and `binary.Table` retains none, so this is a known
	// over-rejection — and it cannot be witnessed here either, because the wat encoder has no
	// `(table … (ref.func $f))` field yet (#8). Two layers short of a witness, which is why it is
	// declared in `declaredFuncs`' comment and asserted nowhere: the honest record of an untestable
	// claim is the claim, not a test that passes for a different reason.
}

// TestSingleByteDeclinesAreExactlyExceptionHandling is slice 8's charged overhead (ADR 0034), and its
// subject is a *sentence*, not an instruction.
//
// **Renamed by slice 9 (ADR 0035), which is the control working.** It was
// `TestSingleByteDeclinesAreExactlyTheTwoDeferredProposals` while the set held two proposals; slice 9
// types the tail-call pair, so the set holds one and the old name asserted a falsehood about its own
// contents. A test name is a checkable citation, and the citation this one carries is a *count of
// proposals* — which means the name moves whenever the set does, and the next slice to drain it (only
// exception handling is left) retires the test rather than renaming it again.
//
// `validate.go` declared the single-byte space "fully in vocabulary" with "0xFE (threads) alone"
// remaining, twice, in two paragraphs, and both clauses were false when written — eleven named
// opcodes were declined at the time. ADR 0032 swept the sentence immediately following one of them
// and left it standing. That is one shape paying out three times, so the boundary is pinned here as a
// **set** and the prose in ref.go's header points at this test rather than the reverse.
//
// # The domain is rows that name an instruction, and the exclusion is not a convenience
//
// `binary.OpMnemonic`'s `ok` means "there is a row", and 24 single-byte rows have an **empty**
// mnemonic: `illegal: true` rows (bytes the reference defines in order to reject) and `escape: true`
// prefix bytes. Both decline here, correctly and permanently — an illegal byte never reaches this
// package with a verdict to give, and a prefix byte reaches it as `Prefix != 0` — so counting them
// would put 24 rows that can never move into a set whose whole purpose is to name what is *left to
// do*. The raw count was 29 when this landed and the honest one 5; slice 9 took two, so it is 27 and 3
// — and the gap is the same 24, because that half of the count never moves. This control states the
// difference rather than reporting the flattering figure.
//
// Both bounds on the walk, for TestEveryNumericOpcodeHasASignature's reason: the floor catches the
// derivation collapsing, and the exact figure catches a handful of rows dropping out of a domain that
// comes from a **committed** table and therefore never moves on upstream's schedule.
func TestSingleByteDeclinesAreExactlyExceptionHandling(t *testing.T) {
	// The set, with the proposal each byte belongs to — which is the fact that makes this a boundary
	// and not a to-do list. All three are exception handling, which is in `validate.go`'s declared
	// out-of-scope list: unlike the tail-call pair slice 9 removed from this set, these are declined
	// *by declaration* rather than for want of an arm, so the next move here is a scope decision and
	// not a slice.
	want := map[uint32]string{
		0x08: "throw",     // exception handling
		0x0a: "throw_ref", // exception handling
		0x1f: "try_table", // exception handling
	}

	// The real dispatch, asked the way `Func` asks it — a hand-written list of "what this package
	// implements" would be the package agreeing with its own notes. The walk lives in
	// `singleByteDeclines` (validate_test.go) because slice 9 gave it a second reader: the specimen
	// row's failure message prints the same set so its re-point is a one-line edit. The set is pinned
	// *here* and only formatted there, so a walk that breaks fails this test.
	got, named := singleByteDeclines()

	const (
		namedRowFloor = 150
		namedRowExact = 192
	)
	if named < namedRowFloor {
		t.Fatalf("walked only %d named single-byte rows, want ≥%d — the domain derivation stopped "+
			"matching, and every set comparison below would be this test reporting its own "+
			"blindness", named, namedRowFloor)
	}
	if named != namedRowExact {
		t.Errorf("walked %d named single-byte rows, want exactly %d — `optable.go` is committed, so "+
			"this moves on a table edit (re-base it in that PR) and never on upstream's clock; a "+
			"floor cannot tell a loss of six rows from a healthy walk", named, namedRowExact)
	}

	for op, name := range want {
		switch mn, ok := got[op]; {
		case !ok:
			t.Errorf("%#02x (%s) is no longer declined — if a slice typed it, delete this row, "+
				"re-name this test for what the set now holds, and say so in that PR's ADR; the "+
				"proposal named in ref.go's header is the claim this test holds", op, name)
		case mn != name:
			t.Errorf("%#02x declines under the mnemonic %q, this set calls it %q — the authority's "+
				"table was re-spelled and the boundary is now stated in two vocabularies",
				op, mn, name)
		}
	}
	for op, name := range got {
		if _, ok := want[op]; !ok {
			t.Errorf("%#02x (%s) is declined and is not in the deferred set — either a slice's "+
				"dispatch arm was lost, or the space grew an instruction nothing has claimed. "+
				"Both are the boundary moving without the sentence moving", op, name)
		}
	}

	t.Logf("%d named single-byte rows; %d declined: %v", named, len(got), got)
}

// TestRefEqOperandIsANullableEqRef prints what `refNullEq` holds, which its own comment declines to
// assert in prose.
//
// The var is built by `binary.AbstractRefType(binary.HeapEq, true)` with the predicate discarded, and
// the argument for discarding it is that the input is a constant from the package that owns the
// table. That argument is checkable, so it is checked here rather than trusted: if the twelve ever
// stop including `eq`, the var silently becomes `NoValType` — a zero ValType, which `IsRef` reports
// **false** for, so `ref.eq` would refuse every operand with a message naming a type that does not
// exist. An engine asserting a type its input does not have is grave #36's class.
func TestRefEqOperandIsANullableEqRef(t *testing.T) {
	rt, ok := binary.AbstractRefType(binary.HeapEq, true)
	if !ok {
		t.Fatalf("binary.AbstractRefType(HeapEq, true) reports HeapEq is not one of the twelve "+
			"abstract forms, so refNullEq is %v — every ref.eq operand would be refused against a "+
			"type this format cannot spell", rt)
	}
	if rt != refNullEq {
		t.Errorf("refNullEq is %v, the same constructor answers %v — the var is not what this test "+
			"checks", refNullEq, rt)
	}
	if !refNullEq.IsRef() {
		t.Errorf("refNullEq (%v) is not a reference type", refNullEq)
	}
	if !refNullEq.Null() {
		t.Errorf("refNullEq (%v) is non-nullable; the reference's operand is `RefT (Null, EqHT)` and a "+
			"non-nullable requirement rejects `(ref.eq (ref.null eq) …)`, which is valid", refNullEq)
	}
	if k, hasKind := refNullEq.Kind(); !hasKind || k != binary.HeapEq {
		t.Errorf("refNullEq's kind byte is (%#02x, %v), want (%#02x, true) — the heaptype is the "+
			"whole content of this rule", k, hasKind, binary.HeapEq)
	}
	t.Logf("refNullEq = %s (kind %#02x, nullable %v)", refNullEq, binary.HeapEq, refNullEq.Null())
}

// TestRefAsNonNullCarriesTheHeapTypeAndClearsOnlyTheNullBit is `refAsNonNull`'s control, and the row
// that earns it is the last one.
//
// The first two families are the ordinary halves: the heaptype must come through untouched (a rule
// pushing `funcref` passes the funcref row and fails every other), and a numeric operand must be
// refused (`peekRef`'s classification, without which the rule is a pop and a push).
//
// **The bottom row is the slice's one structural bound.** `peek_ref` answers `(NoNull, BotHT)` for a
// bottom operand — a *reference* bottom, which satisfies every reference requirement and no numeric
// one — and not `BotT`, which satisfies both. `(unreachable) (ref.as_non_null) (f32.abs)` is
// `unreached-invalid.wast:697`, and it is the **only row of that file's 55 slice-8 rows** that
// separates the two readings.
//
// **Which mutation it separates was measured, and it is not the one this header first named.**
// Making `peekRef` return this package's `unknown` fails nothing — the arm overwrites the null bit
// on both the pop and the push, so the two spellings are the same value by the time anything
// compares them. The reading this row kills is the *push* collapsing a bottom heaptype back to the
// valtype bottom, which fails here and nowhere else. The correction is left visible because the
// first version of this paragraph would have sent a reader to mutate a line that this row cannot
// see, and concluding from that that the distinction is decorative is worse than not having the
// paragraph.
//
// Its accept-direction mirror is here beside it, because a rule that refused *both* would also pass
// the reject row and be wrong about reachability instead.
func TestRefAsNonNullCarriesTheHeapTypeAndClearsOnlyTheNullBit(t *testing.T) {
	// spell is the parameter type; result is what the instruction's own type satisfies exactly.
	for _, c := range []struct{ spell, result string }{
		{"funcref", "(ref func)"},
		{"externref", "(ref extern)"},
		{"anyref", "(ref any)"},
		{"eqref", "(ref eq)"},
		{"i31ref", "(ref i31)"},
		{"structref", "(ref struct)"},
	} {
		t.Run(c.spell, func(t *testing.T) {
			wat := `(module (func (param ` + c.spell + `) (result ` + c.result + `)
				(local.get 0) (ref.as_non_null)))`
			if _, err := validated(t, wat, gcOn); err != nil {
				t.Errorf("(ref.as_non_null) on a %s does not satisfy a %s result: %v — the heaptype "+
					"is carried through and only the null bit changes", c.spell, c.result, err)
			}
			// The other family, so the accept row above is not satisfiable by a rule that pushes one
			// fixed reference type.
			other := "(ref func)"
			if c.spell == "funcref" {
				other = "(ref extern)"
			}
			bad := `(module (func (param ` + c.spell + `) (result ` + other + `)
				(local.get 0) (ref.as_non_null)))`
			if _, err := validated(t, bad, gcOn); !errors.Is(err, ErrTypeMismatch) {
				t.Errorf("(ref.as_non_null) on a %s was accepted as a %s result (%v) — the peeked "+
					"heaptype is being replaced", c.spell, other, err)
			}
		})
	}

	t.Run("already non-null", func(t *testing.T) {
		// Valid, and the rule the reference does *not* have is the one to check for: `_nul` is
		// discarded, so there is no "already non-nullable" rejection to invent.
		wat := `(module (type $t (func)) (func (param (ref $t)) (result (ref $t))
			(local.get 0) (ref.as_non_null)))`
		if _, err := validated(t, wat, gcOn); err != nil {
			t.Errorf("(ref.as_non_null) on an already non-nullable operand was rejected: %v — the "+
				"peeked nullability is discarded, and a check on it is a rule the reference has "+
				"no line for", err)
		}
	})

	t.Run("numeric operand", func(t *testing.T) {
		wat := `(module (func (param i32) (result i32) (local.get 0) (ref.as_non_null)))`
		if _, err := validated(t, wat, gcOn); !errors.Is(err, ErrTypeMismatch) {
			t.Errorf("(ref.as_non_null) accepted an i32 operand (%v) — `peekRef` classifies before "+
				"the pop, and without it this rule is a no-op wearing an opcode", err)
		}
	})

	// The two bottoms, which is the pair this arm exists to keep apart.
	t.Run("bottom satisfies no numeric requirement", func(t *testing.T) {
		wat := `(module (func (result f32) (unreachable) (ref.as_non_null) (f32.abs)))`
		if _, err := validated(t, wat, gcOn); !errors.Is(err, ErrTypeMismatch) {
			t.Errorf("`(unreachable) (ref.as_non_null) (f32.abs)` was accepted (%v) — this is "+
				"unreached-invalid.wast:697, and accepting it is the arm pushing `BotT` where the "+
				"reference pushes `RefT (NoNull, BotHT)`: a *reference* bottom matches no numeric "+
				"requirement, because the mixed-sort pair falls to match_valtype's `_, _ -> false`",
				err)
		}
	})

	t.Run("bottom satisfies every reference requirement", func(t *testing.T) {
		// The row above must fail because the bottom is a *reference*, not because this rule refuses
		// bottom outright, and these are what tell the two apart. The concrete and abstract wants are
		// separate rows because `matchHeap` reaches them through different arms — `matchDefType` and
		// `compTypeAt` respectively — and its bottom arm is the only thing that stops either from
		// resolving `botHeapIdx` as a type index the module is supposed to hold.
		for _, c := range []struct{ name, wat string }{
			{"ref.is_null consumes it", `(module (func (result i32)
				(unreachable) (ref.as_non_null) (ref.is_null)))`},
			{"concrete reference requirement", `(module (type $t (func))
				(func (result (ref $t)) (unreachable) (ref.as_non_null)))`},
			{"abstract reference requirement", `(module (func (result funcref)
				(unreachable) (ref.as_non_null)))`},
		} {
			t.Run(c.name, func(t *testing.T) {
				if _, err := validated(t, c.wat, gcOn); err != nil {
					t.Errorf("a bottom reference did not satisfy a reference requirement (%v) — "+
						"`matchHeap`'s `BotHT, _ -> true` arm has to test the bottom *heaptype*, "+
						"because the value pushed here is `botRef(false)` and only its nullable "+
						"sibling is `unknown`", err)
				}
			})
		}
	})
}

// TestBrOnNullAndBrOnNonNullTakeTheirHeapTypeFromOppositeEnds is the two branch arms' control, and
// the pairing is the whole content of it.
//
// `br_on_null` peeks the *operand* and imposes **no** requirement on the label; `br_on_non_null`
// reads the label's *last type* and derives the operand requirement from it. So the discriminating
// rows are the ones each rule's sibling would get wrong: a void label (fine for `br_on_null`, an
// error for `br_on_non_null`) and a numeric label tail (invisible to `br_on_null`, the other's own
// message). An implementation that shared one code path with a flag fails both.
//
// The fall-through types are the second axis, and they invert too: `br_on_null` leaves the reference
// on the stack non-nullable — the unwrap — and `br_on_non_null` consumes it, because a null has
// nothing to unwrap.
func TestBrOnNullAndBrOnNonNullTakeTheirHeapTypeFromOppositeEnds(t *testing.T) {
	for _, c := range []struct {
		name, wat string
		want      error  // nil for an accept row
		detail    string // a substring of the wrapped message, where the sentinel is not enough
	}{
		{
			// The idiomatic use, and the row a label requirement copied from the sibling rejects.
			name: "br_on_null, void label",
			wat: `(module (func (param (ref null func)) (result i32)
				(block (local.get 0) (br_on_null 0) (drop))
				(i32.const 0)))`,
		},
		{
			// The fall-through is the *unwrapped* form, and the `return` is what asserts it: this
			// function's result is `(ref $t)`, so a rule that left the operand's own `(ref null $t)`
			// on the stack fails here — and it is the only row in this table that would.
			name: "br_on_null, fall-through is non-nullable",
			wat: `(module (type $t (func)) (func (param (ref null $t)) (result (ref $t))
				(block (local.get 0) (br_on_null 0) (return))
				(unreachable)))`,
		},
		{
			name: "br_on_null, label types pass through",
			wat: `(module (func (param (ref null func)) (result i32)
				(block (result i32)
					(i32.const 7)
					(local.get 0)
					(br_on_null 0)
					(drop))))`,
		},
		{
			// The label's types are *required*, not merely passed through: the instruction type is
			// `(ts @ [ref null ht]) --> (ts @ [ref ht])`, so a stack holding the reference alone
			// against an `[i32]` label is invalid. **Accept-direction row** — dropping the
			// `popExpectAll(ts)`/`pushAll(ts)` pair makes exactly this module validate, and the
			// pass-through row above still passes, because there the label's type happens to be on
			// the stack already.
			name: "br_on_null, label types absent from the stack",
			wat: `(module (func (param (ref null func)) (result i32)
				(block (result i32)
					(local.get 0)
					(br_on_null 0)
					(drop)
					(i32.const 0))))`,
			want: ErrTypeMismatch,
		},
		{
			name: "br_on_null, numeric operand",
			wat:  `(module (func (param i32) (block (local.get 0) (br_on_null 0))))`,
			want: ErrTypeMismatch,
		},
		{
			name: "br_on_null, unknown label",
			wat:  `(module (func (param funcref) (local.get 0) (br_on_null 4) (drop)))`,
			want: ErrUnknownLabel,
		},
		{
			// The label's last type is the source of the heaptype, and the operand is consumed.
			name: "br_on_non_null, reference label",
			wat: `(module (func (param (ref null func)) (result funcref)
				(block $l (result funcref)
					(local.get 0)
					(br_on_non_null $l)
					(ref.null func))))`,
		},
		{
			name: "br_on_non_null, label types below the reference pass through",
			wat: `(module (func (param (ref null func)) (result i32) (result funcref)
				(block $l (result i32) (result funcref)
					(i32.const 7)
					(local.get 0)
					(br_on_non_null $l)
					(ref.null func))))`,
		},
		{
			// The reference's first `require`, and the row br_on_null must *not* fail.
			//
			// **The detail is asserted and not the sentinel**, because deleting both requires leaves
			// this module invalid anyway: the operand nothing consumed is still on the stack at the
			// block's `end`. Measured — with the requires dropped, both of these rows still refuse,
			// from `popExpect` and from the block's arity check, which is a row passing while
			// asserting nothing about the arm it is named for.
			name:   "br_on_non_null, void label",
			wat:    `(module (func (param (ref null func)) (block (local.get 0) (br_on_non_null 0))))`,
			want:   ErrTypeMismatch,
			detail: "requires reference type but label has []",
		},
		{
			// The reference's second `require`: the label's last type is not a reference.
			name: "br_on_non_null, numeric label tail",
			wat: `(module (func (param (ref null func)) (result i32)
				(block $l (result i32)
					(local.get 0)
					(br_on_non_null $l)
					(i32.const 0))))`,
			want:   ErrTypeMismatch,
			detail: "requires reference type but label has i32",
		},
		{
			name: "br_on_non_null, operand of the wrong heaptype",
			wat: `(module (func (param externref) (result funcref)
				(block $l (result funcref)
					(local.get 0)
					(br_on_non_null $l)
					(ref.null func))))`,
			want: ErrTypeMismatch,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := validated(t, c.wat, gcOn)
			switch {
			case c.want == nil && err != nil:
				t.Errorf("a valid module was rejected: %v", err)
			case c.want != nil && !errors.Is(err, c.want):
				t.Errorf("reported %v, want %v — the two rules take their heaptype from opposite "+
					"ends, so a shared code path is right about one of them", err, c.want)
			case c.detail != "" && err != nil && !strings.Contains(err.Error(), c.detail):
				t.Errorf("reported %v, which does not contain %q — the refusal came from somewhere "+
					"other than the require this row is named for", err, c.detail)
			}
		})
	}
}

// TestCallRefResolvesATypeIndexAndReturnCallRefChecksTheResults is the two calls' control, and its
// first row is the index space.
//
// `call`/`return_call` take a **function** index; these take a **type** index. Reading this package's
// `funcTypeAt` instead of `funcType` resolves the wrong space, and it is wrong only where the two
// disagree — so the module here has a type the function indices do not line up with, which is the
// case a small hand-written module normally hides.
//
// `return_call_ref`'s own content is the result-type require and the polymorphic tail. The require is
// `matchResultType` and not equality, so a callee returning `(ref $t)` may tail-call from a function
// declaring `funcref`; the tail is why `(return_call_ref $t)` before other instructions is valid.
func TestCallRefResolvesATypeIndexAndReturnCallRefChecksTheResults(t *testing.T) {
	// Two types and one function, arranged so type index 1 and function index 0 name different
	// signatures: a rule resolving the operand's index in the function space would type the callee as
	// `[] -> []` and accept the row below only by discarding the answer.
	const twoSpaces = `(module
		(type $void (func))
		(type $inc (func (param i32) (result i32)))
		(func $unrelated (type $void)) `

	for _, c := range []struct {
		name, wat string
		want      error
	}{
		{
			name: "call_ref, type index not function index",
			wat: twoSpaces + `(func (param (ref null $inc)) (result i32)
				(i32.const 1) (local.get 0) (call_ref $inc)))`,
		},
		{
			name: "call_ref, nullable operand is legal",
			wat: twoSpaces + `(func (result i32)
				(i32.const 1) (ref.null $inc) (call_ref $inc)))`,
		},
		{
			name: "call_ref, operand of another type",
			wat: twoSpaces + `(func (param (ref null $void)) (result i32)
				(i32.const 1) (local.get 0) (call_ref $inc)))`,
			want: ErrTypeMismatch,
		},
		{
			name: "call_ref, missing parameter",
			wat: twoSpaces + `(func (param (ref null $inc)) (result i32)
				(local.get 0) (call_ref $inc)))`,
			want: ErrTypeMismatch,
		},
		{
			name: "call_ref, unknown type index",
			wat: twoSpaces + `(func (result i32)
				(i32.const 1) (ref.null $inc) (call_ref 9)))`,
			want: ErrUnknownType,
		},
		{
			name: "return_call_ref, results match exactly",
			wat: twoSpaces + `(func (param (ref null $inc)) (result i32)
				(i32.const 1) (local.get 0) (return_call_ref $inc)))`,
		},
		{
			name: "return_call_ref, the tail is polymorphic",
			// An instruction after it that produces nothing, in a frame that owes an i32: without
			// `setUnreachable` the `end` demands a result the frame no longer has, and every
			// `(return_call_ref …)` that is not the last instruction of a void frame is rejected.
			wat: twoSpaces + `(func (param (ref null $inc)) (result i32)
				(i32.const 1) (local.get 0) (return_call_ref $inc)
				(nop)))`,
		},
		{
			name: "return_call_ref, callee results do not satisfy this function's",
			wat: twoSpaces + `(func (param (ref null $inc)) (result f32)
				(i32.const 1) (local.get 0) (return_call_ref $inc)))`,
			want: ErrTypeMismatch,
		},
		{
			// `matchResultType` and not equality: `(ref $void)` satisfies a `funcref` result.
			name: "return_call_ref, callee results are a subtype",
			wat: `(module
				(type $void (func))
				(type $mk (func (result (ref $void))))
				(func (param (ref null $mk)) (result funcref)
					(local.get 0) (return_call_ref $mk)))`,
		},
		{
			name: "return_call_ref, this function's results are the subtype",
			wat: `(module
				(type $void (func))
				(type $mk (func (result funcref)))
				(func (param (ref null $mk)) (result (ref $void))
					(local.get 0) (return_call_ref $mk)))`,
			want: ErrTypeMismatch,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := validated(t, c.wat, gcOn)
			switch {
			case c.want == nil && err != nil:
				t.Errorf("a valid module was rejected: %v", err)
			case c.want != nil && !errors.Is(err, c.want):
				t.Errorf("reported %v, want %v", err, c.want)
			}
		})
	}
}

// TestRefEqTakesTwoNullableEqRefsAndOnlyThose is `refEq`'s control: the one rule in this file with no
// index, no side table and no peek, so the only thing to get wrong is the operand type.
//
// A rule expecting `anyref` accepts `(ref.eq (ref.null any) …)`, which is invalid — the accept
// direction, and the reason the `anyref` row is here rather than only the `funcref` one. A rule
// expecting `(ref eq)` rejects the nullable operands every corpus module uses.
func TestRefEqTakesTwoNullableEqRefsAndOnlyThose(t *testing.T) {
	for _, c := range []struct {
		operand string
		want    error
	}{
		{operand: "eqref"},
		{operand: "i31ref"},    // a subtype of eq
		{operand: "structref"}, /* also a subtype */
		{operand: "nullref"},
		{operand: "anyref", want: ErrTypeMismatch},  // a *super*type: the accept-direction row
		{operand: "funcref", want: ErrTypeMismatch}, // the other hierarchy
		{operand: "externref", want: ErrTypeMismatch},
		{operand: "i32", want: ErrTypeMismatch},
	} {
		t.Run(c.operand, func(t *testing.T) {
			wat := `(module (func (param ` + c.operand + `) (param ` + c.operand + `) (result i32)
				(local.get 0) (local.get 1) (ref.eq)))`
			_, err := validated(t, wat, gcOn)
			switch {
			case c.want == nil && err != nil:
				t.Errorf("ref.eq refused two %s operands: %v — the requirement is `(ref null eq)` "+
					"and every subtype of eq satisfies it", c.operand, err)
			case c.want != nil && !errors.Is(err, c.want):
				t.Errorf("ref.eq on two %s operands reported %v, want %v", c.operand, err, c.want)
			}
		})
	}

	t.Run("one operand only", func(t *testing.T) {
		wat := `(module (func (param eqref) (result i32) (local.get 0) (ref.eq)))`
		if _, err := validated(t, wat, gcOn); !errors.Is(err, ErrTypeMismatch) {
			t.Errorf("ref.eq with one operand reported %v, want a type mismatch — the rule pops two", err)
		}
	})
}

// TestTableGetSetReadBothTypesFromTheTable is `tableOp`'s control, and it is #343 cause 2's shape
// checked before it can be re-earned a third time.
//
// Two axes, each with a pair. The **index** operand is the table's address type, so a table64 takes
// an i64 and refuses an i32 — the row that a hardcoded `binary.I32` passes in the default lane and
// fails only with memory64 on. The **element** operand and result are the table's own reference type,
// so an `externref` table refuses a `funcref` — the row a hardcoded `binary.FuncRef` fails.
//
// The two are separated because a single table exercising both would let one right answer cover for
// one wrong one: an i32-indexed `funcref` table is the case both mistakes agree with.
//
// Measured: hardcoding the index at `binary.I32` fails 3 rows and hardcoding the element at
// `binary.FuncRef` fails 5, with no overlap — which is the separation working. Reversing `table.set`'s
// two pops fails 3, one of them the row that exists only for that mistake.
func TestTableGetSetReadBothTypesFromTheTable(t *testing.T) {
	m64 := func(f *binary.Features) { f.Memory64 = true }

	for _, c := range []struct {
		name, wat string
		gate      func(*binary.Features)
		want      error // nil for an accept row
	}{
		{
			name: "get, i32 table",
			wat:  `(module (table 1 funcref) (func (result funcref) (i32.const 0) (table.get 0)))`,
		},
		{
			name: "get, i64 table",
			wat:  `(module (table i64 1 funcref) (func (result funcref) (i64.const 0) (table.get 0)))`,
			gate: m64,
		},
		{
			name: "get, i64 table indexed by i32",
			wat:  `(module (table i64 1 funcref) (func (result funcref) (i32.const 0) (table.get 0)))`,
			gate: m64,
			want: ErrTypeMismatch,
		},
		{
			name: "get, element type is the table's",
			wat:  `(module (table 1 externref) (func (result externref) (i32.const 0) (table.get 0)))`,
		},
		{
			name: "get, element type is not funcref",
			wat:  `(module (table 1 externref) (func (result funcref) (i32.const 0) (table.get 0)))`,
			want: ErrTypeMismatch,
		},
		{
			name: "set, element type is the table's",
			wat:  `(module (table 1 externref) (func (i32.const 0) (ref.null extern) (table.set 0)))`,
		},
		{
			name: "set, wrong element type",
			wat:  `(module (table 1 externref) (func (i32.const 0) (ref.null func) (table.set 0)))`,
			want: ErrTypeMismatch,
		},
		{
			name: "set, i64 table",
			wat:  `(module (table i64 1 externref) (func (i64.const 0) (ref.null extern) (table.set 0)))`,
			gate: m64,
		},
		{
			name: "set, operands the wrong way round",
			// The element pushed below the index, which is the reverse of the signature. This must be
			// rejected, and it is the *accept*-direction witness for the pop order: a rule popping
			// left to right accepts exactly this module and rejects the well-ordered one above it.
			// So the two rows flip together, and neither alone tells a correct implementation from
			// the mirrored one.
			wat:  `(module (table 1 externref) (func (ref.null extern) (i32.const 0) (table.set 0)))`,
			want: ErrTypeMismatch,
		},
		{
			name: "unknown table",
			wat:  `(module (func (result funcref) (i32.const 0) (table.get 3)))`,
			want: ErrUnknownTable,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := validated(t, c.wat, c.gate)
			switch {
			case c.want == nil && err != nil:
				t.Errorf("a valid module was rejected: %v", err)
			case c.want != nil && !errors.Is(err, c.want):
				t.Errorf("reported %v, want %v — both of this rule's operand types come off the "+
					"table, and a hardcoded one passes every row the table happens to agree with",
					err, c.want)
			}
		})
	}
}
