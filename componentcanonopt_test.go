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
		// stream.read's realloc, kept since ADR 0134; its neuter is at the walk's call.
		{"internal/component/testdata/stream-read-realloc-mistyped.wasm", true},
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

// TestACanonOptionTheModelRefusesIsInvalid is #1314's witness: a canon lift or lower whose options break
// CanonicalABI.md's canonopt validation is refused as ErrInvalid at all three doors, before anything runs, with a
// message naming the rule. Each refused fixture's header carries wasm-tools 1.258.0's and wasmtime 49.0.3's
// refusal of the same bytes, every one at parse. The first row is the issue's specimen, wasi:cli/run@0.2.0's
// sync-typed `run` lifted `async` with a callback, which Burroughs loaded and ran.
//
// The two controls are the boundary. Each is its refused neighbour's async option over an async function type,
// which both tools accept, so neither may be refused as invalid. What else they meet when run is not asserted.
//
// **Neuters, run** (each in its site's doc comment): return nil at the top of checkCanonOpts, and the rows its
// rules refuse load; drop canonOpts' repeated-option refusal, and the three `twice` rows load;
// refuseAsyncLowerOfSyncType returning nil lets both async-lower rows load; refuseMistypedLiftOption returning
// nil lets both mistyped rows load. Narrowest of all, drop only checkCanonOpts' type arm, and the specimen
// loads while every other row holds.
func TestACanonOptionTheModelRefusesIsInvalid(t *testing.T) {
	for _, c := range []struct {
		fixture string
		rule    string // a fragment of the refusal, or "" for a control that must not be refused as invalid
	}{
		{"canonopt-async-lift-sync-type", `the canon lift an instance exports as "run": the ` +
			"`async` option requires an async function type"},
		{"canonopt-async-lift-async-type", ""},
		{"canonopt-async-lower-sync-type", "the canon lower of f: the `async` option requires an async function type"},
		{"canonopt-async-lower-sync-lift", "the canon lower of func 0: the `async` option requires an async function type"},
		{"canonopt-async-lower-async-type", ""},
		{"canonopt-callback-without-async", "`callback` cannot be present without `async`"},
		{"canonopt-async-with-post-return", "`async` cannot be present with `post-return`"},
		{"canonopt-lower-callback", "`callback` is only allowed on canon lift"},
		{"canonopt-lower-post-return", "`post-return` is only allowed on canon lift"},
		{"canonopt-realloc-without-memory", "`realloc` requires `memory` to also be present"},
		{"canonopt-memory-twice", "canonical option `memory` is specified more than once"},
		{"canonopt-async-twice", "canonical option `async` is specified more than once"},
		{"canonopt-string-encoding-twice", "canonical option `string-encoding` is specified more than once"},
		{"canonopt-callback-mistyped", "a canon lift's callback is a core function with 2 params and 1 results; " +
			"the component model wants (i32 i32 i32) -> i32"},
		{"canonopt-lift-realloc-mistyped", "a canon lift's realloc is a core function with 2 params and 1 " +
			"results; the component model wants (i32 i32 i32 i32) -> i32"},
	} {
		wasm, err := os.ReadFile("internal/component/testdata/" + c.fixture + ".wasm")
		if err != nil {
			t.Fatal(err)
		}
		check := func(door string, err error) {
			t.Helper()
			switch {
			case c.rule == "" && errors.Is(err, ErrInvalid):
				t.Errorf("%s: %s refused as ErrInvalid, but its async option is over an async function type, "+
					"which the spec accepts: %v", door, c.fixture, err)
			case c.rule == "":
			case err == nil:
				t.Errorf("%s: %s loaded; the component model refuses it (%s)", door, c.fixture, c.rule)
			case !errors.Is(err, ErrInvalid):
				t.Errorf("%s: %s refused, but not as ErrInvalid: %v", door, c.fixture, err)
			case !strings.Contains(err.Error(), c.rule):
				t.Errorf("%s: %s refused without naming the rule %q: %v", door, c.fixture, c.rule, err)
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

		_, err = ComponentConfig{}.Run(wasm)
		check("ComponentConfig.Run", err)
	}
}
