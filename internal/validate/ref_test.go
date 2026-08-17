// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package validate

import (
	"errors"
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
