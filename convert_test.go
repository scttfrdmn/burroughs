// Copyright 2026 Scott Friedman.
// SPDX-License-Identifier: Apache-2.0

package burroughs

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"slices"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/binary"
	"github.com/scttfrdmn/burroughs/internal/interp"
)

// The exhaustiveness guard decision 0029 orders, since Go has no exhaustive `switch`: every type in
// the engine's space converts to a public one and back, and an unmapped kind is an error naming
// itself rather than a silent default.
//
// **The domain is derived from the space, in two directions, because the space has two halves.**
// The reference half is swept: every byte `binary.AbstractRefType` accepts, in both nullabilities,
// so a thirteenth abstract heaptype lands in the domain the day the decoder learns it. The named
// half — the numeric types plus Wasm 2.0's two reference abbreviations — cannot be swept from
// outside `internal/binary` (a `ValType` is a struct with unexported fields and no
// byte-to-numeric constructor is exported), so it is *bound* by name here and the **name set** is
// derived from the authority's own source by an AST walk. That is the same shape
// `declaredValTypes` (`internal/binary/module_test.go`) uses on the same declarations, for the same
// reason: a domain written beside the table it checks is a list, and a list cannot notice an
// addition.

// engineNamedValTypes binds each exported named ValType in `internal/binary` to its value.
//
// The keys are checked against the source by TestConversionDomainMatchesTheDeclaredValTypes, so this
// map is a *binding* of a derived domain rather than an enumeration of one — a named type added
// upstream fails that test with the name it added, instead of quietly staying outside every
// assertion below.
var engineNamedValTypes = map[string]binary.ValType{
	"NoValType": binary.NoValType,
	"I32":       binary.I32,
	"I64":       binary.I64,
	"F32":       binary.F32,
	"F64":       binary.F64,
	"V128":      binary.V128,
	"FuncRef":   binary.FuncRef,
	"ExternRef": binary.ExternRef,
}

// declaredValTypeNames walks `internal/binary/module.go` for package-level `var`s whose value is a
// `ValType{...}` composite literal, which is how the eight named types are spelled there.
//
// Reading the source rather than the package: the values are `var`s of a struct type with unexported
// fields, so there is no reflection or constant enumeration that recovers the set, and the
// declaration itself is the only authority. Parsing is not the fragile part — the *shape* is, so the
// walk asserts a floor on what it found (below) rather than trusting an empty result.
func declaredValTypeNames(t *testing.T) []string {
	t.Helper()

	const path = "internal/binary/module.go"
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}

	var names []string
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Names) != len(vs.Values) {
				continue
			}
			for i, name := range vs.Names {
				lit, ok := vs.Values[i].(*ast.CompositeLit)
				if !ok {
					continue
				}
				if id, ok := lit.Type.(*ast.Ident); ok && id.Name == "ValType" && name.IsExported() {
					names = append(names, name.Name)
				}
			}
		}
	}
	slices.Sort(names)

	// The vacuity check this walk needs: a refactor that moved the declarations, or a rename of the
	// type, yields zero names and would make every comparison below an agreement between empty sets.
	if len(names) < 8 {
		t.Fatalf("found %d named ValTypes in %s (%v); the declaration shape moved, so this "+
			"instrument is measuring nothing", len(names), path, names)
	}
	return names
}

// TestConversionDomainMatchesTheDeclaredValTypes closes the loop between the binding above and the
// authority's source: neither may hold a name the other does not.
//
// Bidirectional, because the two failures differ. A name in the source and not here is a type this
// package converts untested — the drift the guard exists for. A name here and not in the source is a
// binding to something that no longer exists, which compiles only while the identifier survives and
// otherwise reads as coverage of a type nobody declares.
func TestConversionDomainMatchesTheDeclaredValTypes(t *testing.T) {
	declared := declaredValTypeNames(t)
	bound := slices.Sorted(maps.Keys(engineNamedValTypes))

	for _, name := range declared {
		if _, ok := engineNamedValTypes[name]; !ok {
			t.Errorf("internal/binary declares ValType %s and the conversion domain does not bind "+
				"it: add it to engineNamedValTypes so it is converted under test", name)
		}
	}
	for _, name := range bound {
		if !slices.Contains(declared, name) {
			t.Errorf("engineNamedValTypes binds %s, which internal/binary no longer declares", name)
		}
	}
}

// engineTypeSpace is the derived domain: the named types, plus every abstract reference form the
// decoder accepts in both nullabilities, plus the indexed form.
func engineTypeSpace(t *testing.T) []binary.ValType {
	t.Helper()

	space := make([]binary.ValType, 0, 8+24+2)
	for _, name := range declaredValTypeNames(t) {
		vt, ok := engineNamedValTypes[name]
		if !ok {
			continue // reported by TestConversionDomainMatchesTheDeclaredValTypes
		}
		space = append(space, vt)
	}
	// The sweep: 256 bytes asked, twelve accepted. Derived rather than listed, so the day a
	// thirteenth abstract heaptype decodes, it is in this domain without anyone adding it.
	refs := 0
	for b := range 256 {
		for _, null := range []bool{false, true} {
			vt, ok := binary.AbstractRefType(byte(b), null)
			if !ok {
				continue
			}
			space = append(space, vt)
			refs++
		}
	}
	if refs != 12*2 {
		t.Fatalf("the byte sweep found %d abstract reference forms, want 24 (twelve kinds in two "+
			"nullabilities); the reference space moved", refs)
	}
	space = append(space, binary.RefType(0, false), binary.RefType(7, true))
	return space
}

// TestEveryEngineTypeConvertsBothWays is the guard proper: for every type in the derived space,
// either the conversion round-trips exactly, or it fails with an error naming what it was.
//
// The round trip is the assertion that matters. A conversion that succeeded in one direction while
// losing nullability or a type index would satisfy "no unmapped kind" and still hand a host the
// wrong type — `(ref null $3)` and `(ref $3)` differ in one bit that decides whether a null is a
// legal value.
func TestEveryEngineTypeConvertsBothWays(t *testing.T) {
	converted := 0
	for _, vt := range engineTypeSpace(t) {
		pub, err := typeFromInternal(vt)
		if err != nil {
			// The one documented exception, asserted as the only one: the zero ValType is not a
			// value type and converting it would mean inventing one (grave #300).
			if vt != binary.NoValType {
				t.Errorf("typeFromInternal(%v) failed and it is not NoValType: %v", vt, err)
			}
			continue
		}
		if vt == binary.NoValType {
			t.Errorf("typeFromInternal(NoValType) produced %v; a field nothing wrote must not "+
				"convert to a type", pub)
			continue
		}
		back, err := typeToInternal(pub)
		if err != nil {
			t.Errorf("typeToInternal(%v), converted from %v: %v", pub, vt, err)
			continue
		}
		if back != vt {
			t.Errorf("round trip of %v through the public type %v gave %v", vt, pub, back)
			continue
		}
		converted++
	}
	if want := 7 + 24 + 2; converted != want {
		t.Errorf("converted %d engine types, want %d (seven named types with a wire byte, 24 "+
			"abstract reference forms, two indexed forms)", converted, want)
	}
}

// TestEveryPublicKindConverts is the same guard from the other end: every Kind in this package's
// vocabulary maps onto an engine type.
//
// Both directions are needed and neither implies the other. The engine-side sweep catches a decoder
// form this package cannot express; this one catches a public Kind with no engine arm — a constant a
// consumer can name, and pass to Call, and have rejected at the boundary.
func TestEveryPublicKindConverts(t *testing.T) {
	for _, k := range slices.Sorted(maps.Keys(kindNames)) {
		var typ Type
		switch {
		case k == KindTypedRef:
			typ = TypedRefType(3, true)
		case k.IsRef():
			typ, _ = AbstractRefType(k, true)
		default:
			typ, _ = NumberType(k)
		}
		if typ.Kind() != k {
			t.Fatalf("constructing a Type for %v produced kind %v", k, typ.Kind())
		}
		vt, err := typeToInternal(typ)
		if err != nil {
			t.Errorf("typeToInternal(%v): %v", typ, err)
			continue
		}
		back, err := typeFromInternal(vt)
		if err != nil {
			t.Errorf("typeFromInternal(%v), converted from %v: %v", vt, typ, err)
			continue
		}
		if back != typ {
			t.Errorf("round trip of %v through the engine type %v gave %v", typ, vt, back)
		}
	}
}

// TestTheZeroValueConvertsToAnErrorNotAType pins the property KindNone exists for: a Value nobody
// set is refused at the boundary instead of arriving as an i32 zero.
//
// This is the failure mode a `default` arm produces, which is why the conversion has none. A host
// that passes `burroughs.Value{}` by mistake — a slice element never assigned, a struct field
// forgotten — gets a named error at the call instead of a plausible zero in the middle of a
// computation.
func TestTheZeroValueConvertsToAnErrorNotAType(t *testing.T) {
	if _, err := typeToInternal(Type{}); err == nil {
		t.Error("the zero Type converted to an engine type")
	}
	if _, err := valueToInternal(Value{}); err == nil {
		t.Error("the zero Value converted to an engine value")
	}
	if _, err := typeFromInternal(binary.NoValType); err == nil {
		t.Error("NoValType converted to a public type")
	}
}

// TestValueConversionCarriesEveryField is the value-level counterpart to the type-level round trip
// above: a Value crossing to the engine and back is unchanged.
//
// It exists separately because the type conversion cannot see the payload. The v128 high word and
// the host discriminator are exactly the two fields added by the last two widenings this boundary
// exists to absorb (0024, 0027), and a converter that dropped either would pass every assertion in
// this file that only looks at types.
func TestValueConversionCarriesEveryField(t *testing.T) {
	n := 0
	for _, values := range spellableValues(t) {
		for _, want := range values {
			iv, err := valueToInternal(want)
			if err != nil {
				t.Errorf("valueToInternal(%v): %v", want, err)
				continue
			}
			got, err := valueFromInternal(iv)
			if err != nil {
				t.Errorf("valueFromInternal(%v), converted from %v: %v", iv, want, err)
				continue
			}
			if got != want {
				t.Errorf("round trip of %#v gave %#v", want, got)
				continue
			}
			n++
		}
	}
	// 52 since 0039 added four bare-host-reference samples to the shared helper — a pinned count
	// rather than a floor, so a sample set that shrank by one is as visible as one that moved wholesale.
	if n != 52 {
		t.Errorf("round-tripped %d values, want 52 — the sample set moved", n)
	}
}

// TestPayloadConversionCoversTheWholeVocabulary is **the public boundary's half of Scott's condition
// on the 0039 stamp**, quoted here so the condition sits beside the code that answers it: *"the payload
// kind is handled exhaustively at both boundaries, with a test that enumerates the kinds from the
// type's own definition and fails on any unmapped one. No `default` case that silently absorbs a
// future member — an enum whose whole purpose is to grow must fail loudly the first time it does."*
// The harness's half is `TestInterpPayloadsCoverTheEngineVocabulary`.
//
// The domain is counted up from the zero value to `payloadPastEnd`, the sentinel `iota` maintains
// inside `RefPayload`'s own const block, and to `interp.PayloadPastEnd` on the other side — so a new
// member widens this loop in the commit that declares it, and there is no list of members to forget to
// update. That matters more here than a switch's exhaustiveness would: the two enums live in different
// packages, so `exhaustive` cannot check a map at all and could only check a switch over one of them.
//
// **The tables and the converters are both asserted**, because a table can be complete while nothing
// reads it: `valueToInternal`/`valueFromInternal` are the only two functions that cross this boundary,
// and the second half of this test drives a Value carrying each payload through both.
func TestPayloadConversionCoversTheWholeVocabulary(t *testing.T) {
	for p := RefPayload(0); p < payloadPastEnd; p++ {
		in, ok := payloadKinds[p]
		if !ok {
			t.Errorf("RefPayload %v (ordinal %d) has no row in payloadKinds — valueToInternal would "+
				"refuse every Value carrying it, so a public caller could hold a reference this API "+
				"cannot hand to the engine at all", p, p)
			continue
		}
		if back, ok := kindPayloads[in]; !ok || back != p {
			t.Errorf("RefPayload %v maps to engine %v, which maps back to %v (present: %v); the two "+
				"directions must be inverses or a result comes back as a different constructor than "+
				"the argument went in as", p, in, back, ok)
		}
		// The printer is the third table over the same vocabulary, and it is checked here rather than
		// in its own test because a member missing from it is missing from *this* enum's definition in
		// exactly the same way: `String` falls back to `RefPayload(N)`, so the member is nameable but
		// not named, and every error message and `Value.String` about it says an ordinal.
		if _, ok := payloadNames[p]; !ok {
			t.Errorf("RefPayload ordinal %d has a payloadKinds row but none in payloadNames, so it "+
				"prints as %q — a public error message or a Value.String naming it would give a "+
				"caller a number where every other payload gives a word", p, p.String())
		}
	}
	if _, ok := payloadKinds[payloadPastEnd]; ok {
		t.Error("payloadKinds has a row for payloadPastEnd, which is the domain's bound and not a " +
			"payload kind; a row for it makes the loop above pass for a member that does not exist")
	}
	if n := len(payloadKinds); n != int(payloadPastEnd) {
		t.Errorf("payloadKinds has %d rows for %d public payload kinds; the count is asserted as well "+
			"as the membership, so a table carrying rows outside the domain is visible too",
			n, int(payloadPastEnd))
	}

	// The engine's vocabulary, which is the direction that will fire first in practice: a proposal's
	// new reference constructor lands in `interp` and reaches `valueFromInternal` before this package
	// has a member for it.
	for p := interp.RefPayload(0); p < interp.PayloadPastEnd; p++ {
		if _, ok := kindPayloads[p]; !ok {
			t.Errorf("interp.RefPayload %v (ordinal %d) has no row in kindPayloads — valueFromInternal "+
				"would refuse every result carrying it, which turns a reference the engine produced "+
				"correctly into an error at the public boundary", p, p)
		}
	}
	if n := len(kindPayloads); n != int(interp.PayloadPastEnd) {
		t.Errorf("kindPayloads has %d rows for %d engine payload kinds", n, int(interp.PayloadPastEnd))
	}

	// The path, not the table. A Value per payload kind, built here rather than taken from
	// `spellableValues`: the `ref:` family has no `ParseValue` arm and never had one (see Value.String),
	// so these shapes are unspellable by construction and no sample set drawn from the spelling can
	// reach them. `anyref` carries all of them — it is the widest type a payload can sit under, and the
	// one `extern.wast`'s own results arrive at.
	anyref, ok := AbstractRefType(KindAnyRef, true)
	if !ok {
		t.Fatal("AbstractRefType(KindAnyRef, true) failed; this test cannot build its own subjects")
	}
	crossed := 0
	for p := RefPayload(0); p < payloadPastEnd; p++ {
		want := Value{typ: anyref, refKind: p, ref: 7, i31: 9}
		iv, err := valueToInternal(want)
		if err != nil {
			t.Errorf("valueToInternal of a %v reference: %v", p, err)
			continue
		}
		got, err := valueFromInternal(iv)
		if err != nil {
			t.Errorf("valueFromInternal of a %v reference: %v", p, err)
			continue
		}
		if got != want {
			t.Errorf("round trip of a %v reference gave %#v, want %#v", p, got, want)
			continue
		}
		crossed++
	}
	if crossed != int(payloadPastEnd) {
		t.Errorf("crossed the boundary with %d of %d payload kinds; the vacuity check, since a loop "+
			"that continued on every error would report agreement about nothing",
			crossed, int(payloadPastEnd))
	}
}
