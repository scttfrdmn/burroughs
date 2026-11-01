// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package burroughs

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// TestAComponentsMistypedReallocIsRefusedAsInvalid is #1134 class C's witness: a canon lower's `realloc` option
// must be a core function of type `(i32 i32 i32 i32) -> i32`, the component model's validation rule, and a
// component that breaks it is refused as ErrInvalid at all three doors, before anything runs.
//
// Until this rule, the mistyped fixture instantiated, and only a lower that actually called its realloc reached
// `CanonCaller.Realloc`'s arity check, which blamed the module through `ErrNotValidated` at run time.
//
// The host-backed fixture is the boundary. Its realloc is a lowered component function, which has the right
// type and which wasm-tools accepts, so it must load. That it is unsupported when called is
// `internal/interp`'s `TestACanonReallocThatIsAHostFunctionIsUnsupported`.
//
// **Neuter, run:** see the PR that landed this. With the type check in `lowerRealloc` removed, the mistyped
// fixture loads at all three doors and each row reports it.
func TestAComponentsMistypedReallocIsRefusedAsInvalid(t *testing.T) {
	for _, c := range []struct {
		fixture string
		invalid bool
	}{
		{"internal/component/testdata/canon-realloc-mistyped.wasm", true},
		{"internal/component/testdata/canon-realloc-host.wasm", false},
	} {
		wasm, err := os.ReadFile(c.fixture)
		if err != nil {
			t.Fatal(err)
		}
		check := func(door string, err error) {
			t.Helper()
			switch {
			case !c.invalid && errors.Is(err, ErrInvalid):
				t.Errorf("%s: %s refused as ErrInvalid, but its realloc has the right type and the spec "+
					"accepts it: %v", door, c.fixture, err)
			case !c.invalid:
			case err == nil:
				t.Errorf("%s: %s loaded; its realloc is (i32 i32) -> i32, which the component model refuses",
					door, c.fixture)
			case !errors.Is(err, ErrInvalid):
				t.Errorf("%s: %s refused, but not as ErrInvalid: %v", door, c.fixture, err)
			case !strings.Contains(err.Error(), "(i32 i32 i32 i32) -> i32"):
				t.Errorf("%s: %s refused without naming the type the rule wants: %v", door, c.fixture, err)
			}
		}

		comp, err := LoadComponent(wasm)
		if comp != nil {
			comp.Close()
		}
		check("LoadComponent", err)

		comp, err = ComponentConfig{}.LoadComponent(wasm)
		if comp != nil {
			comp.Close()
		}
		check("ComponentConfig.LoadComponent", err)

		// Run asks for a run export the fixtures do not have, so the host fixture's error is a different one.
		// What is asserted for it is only that it is not ErrInvalid.
		_, err = ComponentConfig{}.Run(wasm)
		check("ComponentConfig.Run", err)
	}
}
