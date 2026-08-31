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

// TestEveryStackCreationSiteCarriesAThread is a **structural** control, and saying so is the point of
// this comment.
//
// Nothing on the interpreter's hot path *reads* `stack.t` in this slice — the first reader is #515's
// safepoint check — so a behavioural test of propagation is not available: deleting `t: &in.host` from
// a creation site changes no observable answer, and a test that passed either way would be an
// analytic zero wearing a control's clothes. What *is* checkable today is the invariant decision 0050
// actually rests on: **every stack the engine creates is given a thread**. So this parses the
// package's own non-test sources and asserts it of every `stack{…}` literal in them.
//
// **The domain is derived, not enumerated.** Listing today's three sites would inherit today's blind
// spot — the failure this exists to catch is a *fourth* site added later, and T-1's `runEntry` is
// exactly that fourth site, parked in #554 behind #557 and #516 rather than behind the ruling it was
// once waiting on (see `thread`'s own doc comment, and grave **#561** for why the two differ). Test
// files are
// excluded because a bare `&stack{}` is the right thing there: several tests drive a single opcode arm
// and have no thread to speak of, which is also the second reason `stack.t`'s nil is legal.
//
// `os.ReadDir` plus `ParseFile` rather than `parser.ParseDir`, which is deprecated *and* wrong for the
// job in a way that matters: it does not consider build tags when grouping files into packages, so a
// tagged file could fall outside the domain. Walking the directory takes every `.go` file regardless
// of tag, which is the safe direction — a stack created behind a build tag is still a stack.
// `TestPlainAccessesAreUnsynchronisedWhileTheInterpreterIsSingleThreaded` reaches the same conclusion for the same
// reason one file over, which is why this reads the same way rather than differently.
//
// Watched die, four ways: dropping `t:` at any one of the three sites fails naming that site, and
// blinding the type match fails the floor at `found 0`, which is the failure mode that would
// otherwise make the whole test vacuous.
func TestEveryStackCreationSiteCarriesAThread(t *testing.T) {
	// Three sites today — `constexpr.go`'s const-expr stack, `interp.go`'s start function and
	// `invokeIndex`. A count below this means the parser has stopped seeing the literals rather than
	// that the sites went away, and the assertion beneath it would then pass by asking nothing. A
	// floor rather than an equality on purpose: a *new* site is exactly what this control should
	// judge, not refuse to look at. The exact number is stated because a floor alone catches a moved
	// file and never a silent partial loss.
	const sitesWhenWritten = 3

	ents, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading internal/interp: %v", err)
	}
	fset := token.NewFileSet()
	var withThread, without []string
	files := 0
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
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			// `&stack{…}` and `stack{…}` alike: the address-of is a separate node, so matching the
			// literal's own type name covers both spellings without knowing which is used.
			id, ok := lit.Type.(*ast.Ident)
			if !ok || id.Name != "stack" {
				return true
			}
			site := fmt.Sprintf("%s:%d", name, fset.Position(lit.Pos()).Line)
			for _, el := range lit.Elts {
				kv, ok := el.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "t" {
					withThread = append(withThread, site)
					return true
				}
			}
			without = append(without, site)
			return true
		})
	}
	sort.Strings(withThread)
	sort.Strings(without)

	if files == 0 {
		t.Fatalf("parsed 0 non-test .go files in internal/interp, so every assertion below is "+
			"vacuous: %d directory entries were considered", len(ents))
	}
	if total := len(withThread) + len(without); total < sitesWhenWritten {
		t.Fatalf("found %d `stack` literals across %d non-test files (%v), and there were %d when "+
			"this control was written. A count below the floor means this parser has stopped seeing "+
			"the sites, so the assertion below would pass by asking nothing",
			total, files, append(withThread, without...), sitesWhenWritten)
	}
	if len(without) != 0 {
		t.Errorf("these `stack` literals set no thread: %v\n"+
			"Every stack the engine creates carries the thread it runs on (decision 0050): all "+
			"three sites pass `&in.host`. A stack with no thread reaches #515's safepoint check as "+
			"a nil dereference, and until #515 lands it is silent — which is why this is checked "+
			"structurally rather than by running anything.\nSites that do carry one: %v",
			without, withThread)
	}
}

// TestTheHostThreadTakesTheFirstIDAndIsNotSpecial is the behavioural half available in this slice: the
// thread exists, it is reachable from a running stack, and its id is 1.
//
// **Why an id assertion is worth anything here.** T-4's slot has no reader until #515, so the only
// property of the context that is observable today is its identity — and identity is where the
// main-thread special case T-2 forbids would show up first. A host thread with id 0 would be
// indistinguishable from an unset field; a host thread privileged in any other way would need a
// second field, and there is none.
//
// **The propagation half is observed through `runConst`, which is the one creation site that hands
// its stack back.** That matters: a test that built `&stack{t: &in.host}` and then checked `st.t ==
// &in.host` would be reading its own construction back — asserting a thing against itself, with the
// engine's code nowhere in the loop. `runConst` is propagation site 1 of 3 and it returns the stack it
// made, so this is the one place a *behavioural* check of the invariant is available at all. The other
// two sites are covered structurally above, and this test is why that coverage is not the whole story.
//
// The instance comes from the real constructor rather than a bare `&Instance{}` — grave #163's reason
// at a second site: a hand-assembled instance would assert against a struct the test filled in itself,
// and `host` is precisely the field `InstantiateLinked` sets.
func TestTheHostThreadTakesTheFirstIDAndIsNotSpecial(t *testing.T) {
	in, trap := instantiate1(t, `(module
		(global i32 (i32.const 7))
		(func (export "f") (result i32) (global.get 0)))`)
	if trap != nil {
		t.Fatalf("instantiate: %v", trap)
	}
	if in.host.id != 1 {
		t.Errorf("the host thread's id is %d, want 1: ids are monotonic from 1 so that 0 can mean "+
			"nothing but failure, and instantiation takes the first one", in.host.id)
	}
	if got := in.host.String(); got != "thread 1" {
		t.Errorf("the host thread renders as %q, want %q", got, "thread 1")
	}

	// The engine's own const-expression stack, from the engine's own code path.
	st, err := in.runConst(in.mod.Globals[0].Init, 1, 0, "this test's const expression")
	if err != nil {
		t.Fatalf("runConst: %v", err)
	}
	if st.t != &in.host {
		t.Errorf("a stack created by runConst carries %v, want the instance's own thread at %p: "+
			"propagation site 1 of 3 is not handing the thread over, and no board figure would "+
			"move if it never did", st.t, &in.host)
	}

	if got, err := in.Invoke("f"); err != nil || len(got) != 1 || got[0].Bits != 7 {
		t.Fatalf("invoking through the real path after reading the thread: %v, %v", got, err)
	}
}
