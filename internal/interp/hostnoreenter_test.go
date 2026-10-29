// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"reflect"
	"sort"
	"testing"
)

// callerSurface is every exported method a host function can reach through what it is handed, and whether it enters
// guest code (#1136). An embedder's host function gets a `*Caller`; the canonical-ABI adapter's gets a
// `*CanonCaller`, which embeds it.
//
// **This is contract §5 H-2 made checkable**: *"Host calls MUST NOT re-enter the guest on the caller's stack."* It
// has held by construction, since neither type has an `Invoke` or an instance accessor, and that was asserted
// nowhere. The one method that enters guest code is the adapter's `Realloc`, H-2's own carve-out as amended on
// #694: it invokes exactly the realloc named in the canon options.
var callerSurface = map[string]map[string]bool{ // type -> method -> enters guest code
	"*interp.Caller": {
		"CheckRange": false, "Context": false, "Read": false, "Thread": false, "Write": false,
	},
	"*interp.CanonCaller": {
		"CheckRange": false, "Context": false, "Read": false, "Thread": false, "Write": false,
		"Blocking": false, "CMThreadIndex": false, "ContextGet": false, "ContextSet": false, "MayLeave": false,
		"Poll":    false,
		"Realloc": true, // the canon options' realloc, and only it (H-2's carve-out)
	},
}

// TestAHostFunctionHasNoPathBackIntoTheGuest pins both types' exported methods and exported fields. A new method
// fails until it is classified here, and one that enters guest code, other than `Realloc`, is a contract question.
// An exported field would hand the host something with methods of its own, so the only one allowed is
// CanonCaller's embedded `*Caller`, whose methods are pinned above.
//
// NEUTER, run (2026-10-29, darwin/arm64): with `Thread` removed from `*interp.Caller`'s row, the method is reported
// as unclassified.
func TestAHostFunctionHasNoPathBackIntoTheGuest(t *testing.T) {
	for _, typ := range []reflect.Type{reflect.TypeOf(&Caller{}), reflect.TypeOf(&CanonCaller{})} {
		want, ok := callerSurface[typ.String()]
		if !ok {
			t.Fatalf("%s has no row in callerSurface", typ)
		}
		var got []string
		for i := range typ.NumMethod() {
			got = append(got, typ.Method(i).Name)
		}
		sort.Strings(got)
		seen := map[string]bool{}
		for _, m := range got {
			seen[m] = true
			if _, ok := want[m]; !ok {
				t.Errorf("%s.%s is reachable from a host function and not classified in callerSurface. Say whether "+
					"it enters guest code. If it does, and it is not the canon options' realloc, it breaks "+
					"contract §5 H-2", typ, m)
			}
		}
		for m := range want {
			if !seen[m] {
				t.Errorf("%s.%s is in callerSurface but not on the type: remove the row", typ, m)
			}
		}
		st := typ.Elem()
		for i := range st.NumField() {
			f := st.Field(i)
			if !f.IsExported() {
				continue
			}
			if typ.String() != "*interp.CanonCaller" || f.Name != "Caller" || !f.Anonymous {
				t.Errorf("%s has an exported field %s, a path for a host function that this control cannot see "+
					"into", typ, f.Name)
			}
		}
	}
}
