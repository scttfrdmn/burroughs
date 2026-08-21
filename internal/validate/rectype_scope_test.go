// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package validate

import (
	"errors"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
)

// requireSelfConsistentExtents is what keeps `recGroupExtent`'s fallback a fixture affordance rather
// than a hole in the rule.
//
// `recGroupExtent` degrades to a singleton group over the flat type space when the retained extent
// violates `RecStart <= own index < RecStart+RecLen`, which is what a hand-built `binary.Module` with
// zero-valued fields presents — and that degraded reading is *looser* than the prefix rule, so if the
// decoder ever produced one, the rule would silently stop firing and no board column would move. This
// asserts the decoder does not.
//
// **Stated as the invariant rather than as the fallback's negation on purpose.** A check written as
// "the fallback was not taken" would pass on a module whose extents are inconsistent in some *other*
// way the predicate happens to accept; the invariant is what `binary.CompType`'s own field comment
// declares, so this checks the declaration and then checks that `recGroupExtent` reads it.
func requireSelfConsistentExtents(t *testing.T, name string, m *binary.Module) {
	t.Helper()
	if len(m.Types) == 0 {
		t.Fatalf("%s decoded with an empty type section, so the rule under test has no subject", name)
	}
	for i, ct := range m.Types {
		start, end := uint64(ct.RecStart), uint64(ct.RecStart)+uint64(ct.RecLen)
		if ct.RecLen == 0 || start > uint64(i) || uint64(i) >= end || end > uint64(len(m.Types)) {
			t.Fatalf("%s: type %d decoded with extent RecStart=%d RecLen=%d over %d type(s), which "+
				"violates `RecStart <= own index < RecStart+RecLen` — so checkTypes falls back to the "+
				"flat type space and the prefix rule under test does not fire",
				name, i, ct.RecStart, ct.RecLen, len(m.Types))
		}
		n, scope := recGroupExtent(m, i)
		if n < 1 {
			t.Fatalf("%s: recGroupExtent returned n=%d at type %d, which cannot terminate "+
				"checkTypes' loop", name, n, i)
		}
		if scope != int(end) {
			t.Errorf("%s: recGroupExtent scoped type %d to %d, want %d (RecStart+RecLen) — the "+
				"fallback was taken on a module whose extents are consistent",
				name, i, scope, end)
		}
	}
}

// TestRecGroupPrefixIsTheScope is `check_rectype`'s context discipline, scored on a pair of modules
// that differ **only** in where a rec-group boundary falls.
//
// # Why a pair
//
// A type reference inside rec group *k* resolves against groups `0..k`, so the same two type
// definitions are invalid split across two groups and valid inside one. No single module witnesses
// that: whichever verdict one module gets is consistent with "the scope is the whole type space" *or*
// "the scope is the prefix" depending on which module it is, and a fixture showing only the reject
// direction is satisfied by a validator that rejects both. The pair is what makes the two readings
// disagree — and the accept half is the one an over-tight prefix fails, which is the half no
// `assert_invalid` vector can host (contract §9 G-3).
//
// # The retention path is in the answer, not assumed
//
// Both rows go through the wat encoder and the decoder before reaching this package, so
// `RecStart`/`RecLen` are the ones `labelRecGroup` wrote. A hand-built `binary.Module` would set them
// itself, which tests `checkTypes` against the fixture's own idea of the grouping and never asks
// whether the decoder agrees — and `recGroupExtent`'s fallback exists precisely for hand-built
// modules, so a hand-built pair could pass with the prefix rule never running.
// `requireSelfConsistentExtents` is the assertion that says which of those two happened.
//
// # Falsification, both directions, measured
//
// With `checkSubtype`'s scope replaced by `len(m.Types)` — the whole type space, which is what this
// package did before the prefix rule — the cross-group row is accepted and the all-on spec lane
// returns to 31 fail, with `type-rec.wast:21,28`, `type-equivalence.wast:76`, `array.wast:27,48,52`,
// `ref.wast:27,31,46,51,55,59` and `struct.wast:36,40` back in the admitted bucket. With it replaced
// by `x` — the group's *start* rather than its end, which is the plausible off-by-a-group — the
// same-group row is refused `unknown type 1 (0 in scope)`. Both rows are live and they die for
// opposite reasons, which is what makes this a discriminating pair rather than two rows that happen
// to agree today.
func TestRecGroupPrefixIsTheScope(t *testing.T) {
	// Index 1 names the *second* group, which is not in the context when the first is checked. The
	// reference forces `unknown type 1` out of `subst_of` (types.ml:149-152), whose `Idx` arm looks the
	// index up in the pre-append `c.types`; `roll_rectype` (types.ml:255-263) has already turned the
	// references that *are* in the group into `Rec` and left this one alone.
	const crossGroup = "(module (rec (type (func (param (ref 1))))) (rec (type (func))))"
	// The same two definitions in one group. Index 1 is a member of the group being checked, so it is
	// in scope even though it points forward, and the module is valid — which is what recursive types
	// are for.
	const sameGroup = "(module (rec (type (func (param (ref 1)))) (type (func))))"

	cross := decodedModule(t, crossGroup, gcOn)
	same := decodedModule(t, sameGroup, gcOn)
	requireSelfConsistentExtents(t, "cross-group", cross)
	requireSelfConsistentExtents(t, "same-group", same)
	requireSameTypeContent(t, cross, same)

	if _, err := Module(cross); !errors.Is(err, ErrUnknownType) {
		t.Errorf("a `(ref 1)` in rec group 0 of a two-group module was not refused as an unknown "+
			"type (%v) — `check_rectype` appends one group at a time, so group 1 is not in group 0's "+
			"context", err)
	}
	if _, err := Module(same); err != nil {
		t.Errorf("a `(ref 1)` naming the second member of its *own* rec group was refused (%v) — a "+
			"forward reference inside one `rec` is legal, and this is the direction an over-tight "+
			"prefix fails", err)
	}
}

// requireSameTypeContent asserts the pair above differs only in its grouping.
//
// Without it the pair proves less than it looks like it does: a typo that changed the *content* of one
// module would produce the same two verdicts for an unrelated reason and both rows would still pass.
// So the comparison covers what the two are supposed to share — the count, each entry's kind, and each
// function signature — and explicitly not `RecStart`/`RecLen`, which is the one thing they must
// disagree about and which the two verdicts are the consequence of. The disagreement is asserted too,
// for the reason a comparison needs a vacuity check: two modules that decoded to the same grouping
// would satisfy every line above.
func requireSameTypeContent(t *testing.T, a, b *binary.Module) {
	t.Helper()
	if len(a.Types) != len(b.Types) {
		t.Fatalf("the pair declares %d and %d type(s), so its two verdicts differ for a reason other "+
			"than the rec-group boundary", len(a.Types), len(b.Types))
	}
	for i := range a.Types {
		x, y := a.Types[i], b.Types[i]
		if x.Kind != y.Kind || !sameFuncSig(x.Func, y.Func) {
			t.Fatalf("type %d differs in content between the pair (%v vs %v), so the pair is not "+
				"discriminating the grouping", i, x, y)
		}
	}
	if a.Types[0].RecLen == b.Types[0].RecLen {
		t.Fatalf("both modules decoded to a first group of length %d, so the pair does not differ in "+
			"the grouping at all and its two rows are one row", a.Types[0].RecLen)
	}
}

func sameFuncSig(a, b binary.FuncType) bool {
	if len(a.Params) != len(b.Params) || len(a.Results) != len(b.Results) {
		return false
	}
	for i := range a.Params {
		if a.Params[i] != b.Params[i] {
			return false
		}
	}
	for i := range a.Results {
		if a.Results[i] != b.Results[i] {
			return false
		}
	}
	return true
}

// TestSupertypeIndexResolvesBeforeTheForwardRule is the message half of the same rule, and it is what
// closes the divergence #358 recorded as a never-fix.
//
// `check_subtype` resolves every declared supertype index against the group's scope (valid.ml:160-163)
// before `check_subtype_sub`'s `require (xi < x)` runs (valid.ml:165-176), so the two failures are
// distinguishable and each has its own message. Before the prefix rule this package had only the
// second, so an out-of-range supertype index reported `forward use of type` — the right verdict under
// the wrong name.
//
// **The zero population is why this test exists in this form.** `grep "forward use"` over the vendored
// corpus returns two hits, both in a `custom-descriptors` proposal file and both for a different rule
// (`forward use of described type`), so `ErrForwardTypeUse` has **no board witness at all** and a
// change that made it unreachable would move no column. It is still reachable, and only from inside a
// rec group — which is the second row here and is a consequence worth writing down: an index that is
// out of *scope* is now `unknown type`, and the only indices both in scope and forward are later
// members of the group being checked. So a bare `(type (sub 1 (func)))` at index 0 is `unknown type 1`
// and not a forward use, in this engine and in the reference both.
func TestSupertypeIndexResolvesBeforeTheForwardRule(t *testing.T) {
	// Index 5 names nothing in a two-type module. The second entry is a bare subtype, so its group is a
	// singleton and its scope is 2 — out of scope, and `check_subtype`'s typeuse check speaks first.
	const outOfScope = "(module (type (func)) (type (sub 5 (func))))"
	// Index 1 is in scope, being the group's own second member, and it is still forward of index 0 —
	// the one shape `require (xi < x)` can fire on.
	const forwardInGroup = "(module (rec (type (sub 1 (func))) (type (func))))"

	if _, err := validated(t, outOfScope, gcOn); !errors.Is(err, ErrUnknownType) {
		t.Errorf("a supertype index past the end of the type space was not refused as an unknown "+
			"type (%v) — `check_subtype` resolves the typeuse before `check_subtype_sub` compares it "+
			"to x, which is the message #358 recorded this engine as diverging on", err)
	}
	if _, err := validated(t, forwardInGroup, gcOn); !errors.Is(err, ErrForwardTypeUse) {
		t.Errorf("a supertype naming a later member of its own rec group was not refused as a "+
			"forward use (%v) — that index *is* in scope, so the second pass is the only rule that "+
			"can refuse it, and this is the one shape in which it is reachable at all", err)
	}
}

// TestCompTypeValTypesResolveInGroupScope covers `check_comptype`'s three arms, because the prefix
// rule reaches a type reference through all of them and a rule written for one arm is not the rule.
//
// The three rows are the same cross-group defect — index 1 naming the next group — carried by a
// function parameter, a struct field, and an array element. All three were admitted before this
// slice, and the corpus partitions them the same way: `ref.wast:27,31,46,51,55,59` are the functype
// rows, `struct.wast:36,40` the field rows, `array.wast:27,48,52` the element rows. So the partition
// is the corpus's rather than one invented here, which is what makes it a domain instead of a sample.
//
// The packed-storage arm is not a fourth row and cannot be one: `check_storagetype`'s `PackStorageT`
// case is `()` because an i8/i16 field names no type, so there is nothing for a scope to bound. Said
// rather than covered, because a row asserting a packed field is accepted would pass whether or not
// the arm was skipped — it has no reference to get wrong.
func TestCompTypeValTypesResolveInGroupScope(t *testing.T) {
	for _, tc := range []struct{ arm, wat string }{
		{"FuncT", "(module (rec (type (func (param (ref 1))))) (rec (type (func))))"},
		{"StructT", "(module (rec (type (struct (field (ref 1))))) (rec (type (func))))"},
		{"ArrayT", "(module (rec (type (array (ref 1)))) (rec (type (func))))"},
	} {
		t.Run(tc.arm, func(t *testing.T) {
			m := decodedModule(t, tc.wat, gcOn)
			requireSelfConsistentExtents(t, tc.arm, m)
			if _, err := Module(m); !errors.Is(err, ErrUnknownType) {
				t.Errorf("a `(ref 1)` reached through `check_comptype`'s %s arm in rec group 0 of a "+
					"two-group module was not refused as an unknown type (%v) — every arm walks into "+
					"`check_valtype`, so the scope has to bound all three", tc.arm, err)
			}
		})
	}
}
