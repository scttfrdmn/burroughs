// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package interp

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"
	"testing"
)

// crossings reads the boundary counter. A helper rather than the field, because **every assertion
// here is a delta** and naming that at the read site is what stops an absolute from being written by
// accident: the word is package-level and never reset, so its value is a fact about whatever ran
// earlier in the binary (decision 0052's consequences).
func crossings() uint64 { return boundaryCrossings.Load() }

// TestEveryBoundaryCrossingIsPaired is contract §4 B-MM-1's **presence** oracle, and the first
// paragraph of this comment exists to stop it being read as more than that.
//
// It witnesses that the crossing ran at every site: delete one and a delta comes out short. It does
// **not** witness that a write before a release is visible after an acquire — nothing running one
// agent on one architecture can, and claiming otherwise would be the whole point of §4 asserted by a
// counter. That is B-MM-5's job and **#10**'s battery, on a TSO *and* a weakly-ordered platform.
//
// # The numbers are predicted from the mechanism, not read off a run
//
// *A count is not a price — decompose by mechanism.* Each expectation below is derived from which
// functions the operation passes through, and every one of them was written before this test was
// first run. Two of them are the interesting ones:
//
//   - **A start function adds nothing.** `build` calls `in.call(m.Start, …)` directly rather than
//     going through `invokeIndex`, so the start function runs inside `build`'s already-open
//     crossing. A reader expecting instantiation to cost more for a module with a start would be
//     wrong, and the reason is worth pinning: crossings nest, and a nested entry is not a second
//     transition through a second site.
//   - **A trap still releases.** The edges are `defer`red, so a guest→host transition through a trap
//     crosses out exactly like a normal return. That is the *early return skips its own guard* shape
//     and it is the one failure here that would be silent: an odd counter is not an error anybody
//     sees, and the next agent to acquire would be doing so against a release that never happened.
func TestEveryBoundaryCrossingIsPaired(t *testing.T) {
	// One global initializer, one defined function, no segments and no start. `InstantiateLinked`
	// (2) + `build` (2) + `runConst` for the initializer (2).
	const mod = `(module
		(global (export "g") i32 (i32.const 7))
		(func (export "f") (result i32) (global.get 0))
		(func (export "boom") (result i32) (i32.div_s (i32.const 1) (i32.const 0))))`

	before := crossings()
	in, trap := instantiate1(t, mod)
	if trap != nil {
		t.Fatalf("instantiate: %v", trap)
	}
	if got := crossings() - before; got != 6 {
		t.Errorf("instantiating one global and two functions crossed the boundary %d times, want 6: "+
			"InstantiateLinked's pair, build's pair, and runConst's pair for the one global "+
			"initializer. A number below this is a site that stopped crossing; above it is a site "+
			"nobody accounted for, and either way decision 0052's Instantiate forecast is measuring "+
			"something other than what it says", got)
	}

	// Cases run against one instance, each measuring its own delta. Ordered so that the two that
	// must cost the same sit next to each other: a returning call and a trapping call are the same
	// transition, and the `defer` is the only reason.
	for _, tc := range []struct {
		name string
		want uint64
		run  func()
	}{
		{"a returning invoke", 2, func() { _, _ = in.Invoke("f") }},
		{"a trapping invoke", 2, func() { _, _ = in.Invoke("boom") }},
		{"an invoke of a name that does not exist", 0, func() { _, _ = in.Invoke("nope") }},
		{"reading an exported global", 2, func() { _, _ = in.Global("g") }},
		{"reading a global that does not exist", 2, func() { _, _ = in.Global("nope") }},
	} {
		before := crossings()
		tc.run()
		if got := crossings() - before; got != tc.want {
			t.Errorf("%s crossed the boundary %d times, want %d", tc.name, got, tc.want)
		}
	}

	// The name lookup in `Invoke` happens before `invokeIndex`, so a miss never enters the guest —
	// the one row above that wants zero. `Global`'s lookup is *inside* the crossing, so its miss
	// wants two. The asymmetry is not a defect and it is asserted rather than tidied: `Global`
	// reads guest storage and `Invoke` resolves a name, so the safe placement differs, and a later
	// author making them agree should have to change a test that says why.
	// Parity, and **what it cannot see is worth more than what it can.** It catches an *odd* number
	// of missed releases — one path out of a function that returns before its `defer` is installed,
	// which is the shape that would otherwise be silent. It is blind to an even number: deleting
	// `invokeIndex`'s release entirely and running the two invoke rows above leaves the count short
	// by two and the parity clean, which is measured rather than assumed (the deletion was run, and
	// the per-case deltas above are what caught it). So this is a second, weaker net under the exact
	// assertions above, and not a substitute for them.
	if got := crossings() % 2; got != 0 {
		t.Errorf("the boundary counter is odd (%d) after every case above, so some site entered the "+
			"guest and never left it. Every crossing is `enterGuest()` with a `defer leaveGuest()`, "+
			"so an odd count means a site paired them by hand and returned early", crossings())
	}
}

// TestEveryStackCreationSiteCrossesTheBoundary is B-MM-1's **structural** half, and the domain is
// derived for the reason its sibling one file over derives the same one: *the failure this exists to
// catch is a site added later.*
//
// **Entering the interpreter is the same event as creating a stack**, which is what makes the
// population parseable at all — and it is already parsed, by
// `TestEveryStackCreationSiteCarriesAThread`, for decision 0050's per-thread context. This asks the
// second question of the same nodes: the enclosing function of every non-test `stack{…}` literal
// must establish §4's edges. `InstantiateLinked` and `Global` are the two boundary sites that create
// no stack, so they are outside this control's reach and inside
// `TestEveryBoundaryCrossingIsPaired`'s — which is why both exist.
//
// **#554's `runEntry` is the site this is aimed at.** T-1's spawn creates a stack for a new thread,
// which is a host→guest transition, and it is parked in a PR whose merge would otherwise be the
// moment nobody thought about §4.
//
// Per enclosing function rather than per literal, following `TestEveryTreeWalkStopsAtTheRepoBoundary`:
// the crossing is at the top of the function and the literal is wherever it is, so the two are in one
// body and never in one expression.
//
// Watched die three ways: dropping either call at any of the three sites fails naming that site;
// blinding the literal match fails the floor at `found 0`; and a scratch non-test file containing a
// crossing-free function with a `stack{…}` literal in it fails naming the scratch file — the
// injection method grave **#561** paid for, because *a claim about what an instrument will permit is
// a forecast about a machine sitting in the tree.*
func TestEveryStackCreationSiteCrossesTheBoundary(t *testing.T) {
	// Three sites today — `constexpr.go`'s `runConst`, `interp.go`'s `build` (the start function)
	// and `invokeIndex`. A floor rather than an equality because a *new* site is exactly what this
	// should judge; the exact number is stated beside it because a floor alone catches a moved file
	// and never a silent partial loss.
	const sitesWhenWritten = 3

	ents, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading internal/interp: %v", err)
	}
	fset := token.NewFileSet()
	var crossed, bare []string
	files, sites := 0, 0
	for _, ent := range ents {
		name := ent.Name()
		if ent.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, perr := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if perr != nil {
			t.Fatalf("parsing %s: %v", name, perr)
		}
		files++
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			stacks, enters, leaves := 0, false, false
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch node := n.(type) {
				case *ast.CompositeLit:
					// `&stack{…}` and `stack{…}` alike: the address-of is a separate node, so
					// matching the literal's own type name covers both spellings.
					if id, ok := node.Type.(*ast.Ident); ok && id.Name == "stack" {
						stacks++
					}
				case *ast.CallExpr:
					if id, ok := node.Fun.(*ast.Ident); ok {
						switch id.Name {
						case "enterGuest":
							enters = true
						case "leaveGuest":
							leaves = true
						}
					}
				}
				return true
			})
			if stacks == 0 {
				continue
			}
			sites++
			site := fmt.Sprintf("%s:%d %s", name, fset.Position(fn.Pos()).Line, fn.Name.Name)
			if enters && leaves {
				crossed = append(crossed, site)
			} else {
				bare = append(bare, fmt.Sprintf("%s (enterGuest=%t leaveGuest=%t)", site, enters, leaves))
			}
		}
	}
	sort.Strings(crossed)
	sort.Strings(bare)

	if files == 0 {
		t.Fatalf("parsed 0 non-test .go files in internal/interp, so every assertion below is "+
			"vacuous: %d directory entries were considered", len(ents))
	}
	if sites < sitesWhenWritten {
		t.Fatalf("found %d function(s) creating a `stack` across %d non-test files (crossed %v, "+
			"bare %v), and there were %d when this control was written. A count below the floor "+
			"means this parser has stopped seeing the sites, so the assertion below would pass by "+
			"asking nothing", sites, files, crossed, bare, sitesWhenWritten)
	}
	if len(bare) != 0 {
		t.Errorf("these functions create a `stack` — they enter the interpreter — without contract "+
			"§4 B-MM-1's edges: %v\n"+
			"Every host→guest transition is an acquire edge over the whole shared address space and "+
			"every guest→host transition the release edge (decision 0052, #516). The shape is "+
			"`enterGuest()` then `defer leaveGuest()` at the top of the function; see `boundary.go`. "+
			"Sites that do cross: %v", bare, crossed)
	}
}
