// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

// TestEmbeddedLaneTypesDoNotShadowInstanceFields asserts that no name a lane type promotes into [Instance]
// collides with one of `Instance`'s own fields or methods.
//
// # The defect this exists for, and why it was nearly invisible
//
// #136's lane types are **embedded** in `Instance` so that the struct's declaration is identical in every
// build lane — a field present only under a tag would make the two builds of an A/B differ by more than the
// mechanism. Embedding has a cost Go does not warn about: **field promotion silently resolves a name to the
// shallowest match.**
//
// `lazyEnds` was written with a field called `tables`, and `Instance` already has a `tables` field for the
// wasm table space. Inside the lane, `in.tables` therefore meant *the wasm tables*, not the lane's. It
// surfaced only because the shallower field happened to have an incompatible type and the compiler objected;
// had the two types matched, the lane would have read and written the engine's table space instead of its own,
// in a build that compiled and whose spec board was identical. **A silent wrong answer in a mechanism whose
// whole job is to answer faster.**
//
// So this is a tripwire for the *risk*, not for that one name: a future lane adding an overlapping field fails
// here loudly rather than having it shadowed.
//
// # Why reflection, and why the test carries no build tag
//
// The lane types differ per tag — `lazyEnds` is a `sync.Map` holder in one lane and a zero-width struct in
// three — so an enumeration written per lane would be three copies that could disagree. Reflection reads
// whatever this build actually compiled, which makes **one** untagged test cover all four lanes: run the suite
// under each tag and each gets checked against its own `Instance`.
//
// `make ci` builds and tests `burroughs_endtable` (the `test-endtable` gate), so two lanes are covered there
// already; the other two are covered by the A/B runs that produced the flip package.
func TestEmbeddedLaneTypesDoNotShadowInstanceFields(t *testing.T) {
	inst := reflect.TypeOf(Instance{})

	// `Instance`'s own names: the direct fields, which are what a promoted name would shadow.
	own := map[string]bool{}
	var embedded []reflect.StructField
	for i := range inst.NumField() {
		f := inst.Field(i)
		if f.Anonymous {
			embedded = append(embedded, f)
			continue
		}
		own[f.Name] = true
	}
	if len(own) < 5 {
		t.Fatalf("Instance has only %d named field(s); the shadowing check has nothing to compare against "+
			"and would pass by asking nothing", len(own))
	}
	if len(embedded) == 0 {
		// Not a failure: a build where no lane embeds anything has nothing to shadow. Said aloud so a run
		// that silently checked nothing is distinguishable from one that checked and found nothing.
		t.Logf("EMBEDNAMES no embedded field on Instance in this lane; %d own field(s) unshadowable", len(own))
		return
	}

	var collisions []string
	for _, e := range embedded {
		et := e.Type
		if et.Kind() == reflect.Pointer {
			et = et.Elem()
		}
		if et.Kind() != reflect.Struct {
			continue
		}
		// Promoted FIELDS.
		for i := range et.NumField() {
			name := et.Field(i).Name
			if own[name] {
				collisions = append(collisions, e.Name+"."+name+" shadows Instance."+name+" (field)")
			}
		}
		// Promoted METHODS, which shadow just as quietly. A method is only promoted from an embedded type,
		// which is why this walks the embedded set rather than Instance's own method set.
		for i := range reflect.PointerTo(et).NumMethod() {
			name := reflect.PointerTo(et).Method(i).Name
			if own[name] {
				collisions = append(collisions, e.Name+"."+name+" shadows Instance."+name+" (method vs field)")
			}
		}
	}
	sort.Strings(collisions)
	if len(collisions) > 0 {
		t.Errorf("%d promoted name(s) shadow one of Instance's own:\n\t%s\n"+
			"\tGo resolves a promoted name to the SHALLOWEST match and says nothing. `lazyEnds.tables` once "+
			"shadowed Instance.tables (the wasm table space) and was caught only because the types happened "+
			"to be incompatible; matching types would have given a lane that read the engine's tables instead "+
			"of its own, in a build that compiled with an identical spec board. Rename the lane's field.",
			len(collisions), strings.Join(collisions, "\n\t"))
	}
	names := make([]string, 0, len(embedded))
	for _, e := range embedded {
		names = append(names, e.Name)
	}
	sort.Strings(names)
	t.Logf("EMBEDNAMES %d embedded type(s) [%s] promote no name among Instance's %d own field(s)",
		len(embedded), strings.Join(names, " "), len(own))
}
