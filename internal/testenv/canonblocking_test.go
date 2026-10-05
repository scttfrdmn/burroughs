// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package testenv_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestEveryCanonFunctionsWaitIsInsideABlockingExcursion is contract §5 **H-1** as a structural control:
// a canon function that blocks must do it inside `c.Blocking(...)`.
//
// # The defect that minted it
//
// A `interp.CanonFunc` runs with the **calling agent inside guest execution**. Grave #892's sync
// `subtask.cancel` wait landed as a bare `select` with a 30-second bound, so for up to that long the agent
// ran host code **without being marked blocked**: it reaches no safepoint and is not excused as one. Phase
// 4 clause 3 drives garbage collection's stop-the-world through cooperative safepoints, so a GC beginning
// during a cancellation stalled for the whole wait. `waitableSetWait` already did it correctly.
//
// **What made the omission invisible is the thing a reviewer cannot see:** the closure's parameter was
// `_ *interp.CanonCaller`. There was nothing to misuse, so nothing looked wrong. A control is the only
// reader that notices an absence.
//
// # Its domain is DERIVED, not listed
//
// Every closure whose first parameter is a `*interp.CanonCaller` is in scope, found by parsing. So a canon
// function added tomorrow is judged without anyone adding it here — which is the difference between a
// control and a census, and the reason the population below is a *floor* rather than an equality.
//
// # The limit, stated because a control that hides its blind spots is worse than none
//
// **This is the LEXICAL half only.** It sees a wait written inside the closure. It does not see:
//
//   - **a wait reached through a CALL.** This is not hypothetical: the audit that accompanied the repair
//     found a second candidate — `invokeWith` in the sync cross-component arm — which has no `select` of
//     its own and can reach `awaitEvent`'s park transitively. A call-graph pass finds it; this does not.
//   - **`go` statements.** `liftAsAsyncImpl` calls a parking function inside `go func() { … }`, where no
//     agent is in guest execution. A transitive pass flags it and is *wrong* to; this one never sees it.
//   - **guards that make a path unreachable.** The same arm is preceded by a `callee.async` check that
//     makes its parking case impossible. An analysis that followed calls would need to model that guard
//     or report a false positive.
//
// The transitive half is **deliberately not built**: its two false-positive classes above are the work,
// not the walk, and the next real instance is what should justify paying for them. (Chair's scoping on the
// #897 review.) Until then, a wait reached through a call is caught by review and by the audit recipe in
// the H-1 memory, not by this.
//
// # Watched die, executably
//
// `TestEveryCanonFunctionsWaitIsInsideABlockingExcursion` would pass on a tree with no canon functions at
// all, so the floors below guard that. The *analysis* is falsified directly by
// `TestCanonBlockingAnalysisTripsOnASyntheticBareWait`, which feeds it synthetic sources — a bare `select`
// that must be flagged and a wrapped one that must not. Prose claiming "it would catch X" is a forecast;
// those two sub-tests are the measurement.
func TestEveryCanonFunctionsWaitIsInsideABlockingExcursion(t *testing.T) {
	fset := token.NewFileSet()
	var offenders, closures []string
	scanned := 0

	err := filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipWalkDir(d, "third_party") {
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		rel, rerr := filepath.Rel(repoRoot, path)
		if rerr != nil {
			return rerr
		}
		scanned++
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return fmt.Errorf("parsing %s: %w", rel, perr)
		}
		for _, c := range canonClosures(fset, f) {
			closures = append(closures, fmt.Sprintf("%s:%d", rel, c.line))
			for _, w := range c.waits {
				if w.guarded {
					continue
				}
				offenders = append(offenders, fmt.Sprintf("%s:%d %s (canon closure at %s:%d)",
					rel, w.line, w.kind, rel, c.line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", repoRoot, err)
	}
	sort.Strings(offenders)
	sort.Strings(closures)

	// Two vacuity floors, because the walk and the match fail for unrelated reasons — the shape
	// `TestNoEngineLockIsHeldAcrossAChannelOperation` arrived at for the same analysis style. The file
	// count catches a walk that stopped reading the tree; the closure count catches a `CanonCaller` match
	// that stopped resolving, which would make every assertion below a property of nothing.
	//
	// Both are floors rather than equalities, and both keep headroom: a canon function is exactly what
	// this control should *judge* rather than refuse to look at, so adding one must not fail it.
	const canonFilesWhenWritten = 100
	if scanned < canonFilesWhenWritten {
		t.Fatalf("scanned %d non-test .go file(s) under %s, want at least %d — the walk is not reading "+
			"the tree, so the assertion below asserted its property of nothing and passed",
			scanned, repoRoot, canonFilesWhenWritten)
	}
	// **Measured at 29 when written** (printed by this message, not counted by eye), floored at 15. A
	// floor far below what it bounds catches a match that resolves *nothing* and cannot catch one that
	// stops resolving most of the way — *an unasserted distance is the vacuum* — so 15 is a bit over half,
	// which still leaves room for a package's worth of built-ins to be refactored.
	const canonClosuresWhenWritten = 15
	if len(closures) < canonClosuresWhenWritten {
		t.Fatalf("found %d canon closure(s) across %d file(s), and the floor is %d. Below it means the "+
			"`*interp.CanonCaller` parameter match has stopped resolving, so this control is judging an "+
			"empty set and passing.\nfound: %v", len(closures), scanned, canonClosuresWhenWritten, closures)
	}

	if len(offenders) != 0 {
		t.Errorf("these canon functions block OUTSIDE a `c.Blocking(...)` excursion, and contract §5 H-1 "+
			"requires the excursion:\n  %s\n"+
			"A canon function runs with the calling agent INSIDE guest execution. A wait without the "+
			"excursion leaves that agent running host code unmarked: it reaches no safepoint and is not "+
			"excused as blocked, so a stop-the-world stalls for the whole wait — and Phase 4 clause 3 "+
			"drives GC's STW through cooperative safepoints, so this is a GC that hangs on whatever is "+
			"being waited for.\n"+
			"The repair is `c.Blocking(func() error { … })` around the wait, which marks the agent blocked "+
			"for its duration (H-1: only this agent waits, siblings run) and is what `waitableSetWait` "+
			"does. If the closure's caller parameter is `_`, name it — an ignored parameter is what hides "+
			"this, because there is nothing to misuse and so nothing looks wrong.\n"+
			"If the wait genuinely must not be an excursion, the reason is a decision doc's and not an "+
			"entry in a list here: an exemption inherits none of this control's lessons. Grave #892's "+
			"slice has the one case where `Blocking` would have been WRONG — a sync cross-component call "+
			"runs guest code, so marking it blocked would let a stop round report a stopped world over a "+
			"running guest — and that arm refuses by name instead of blocking.",
			strings.Join(offenders, "\n  "))
	}
	t.Logf("%d canon closure(s) across %d file(s); %d with a wait, all inside c.Blocking",
		len(closures), scanned, countClosuresWithWaits(fset))
}

// canonClosure is one closure whose first parameter is a `*interp.CanonCaller`, with the waits found in it.
type canonClosure struct {
	line  int
	waits []canonWait
}

// canonWait is one blocking construct, and whether it is lexically inside a `Blocking(...)` call.
type canonWait struct {
	line    int
	kind    string
	guarded bool
}

// canonClosures finds every canon-function-shaped closure in f and the blocking constructs in each.
//
// **Shaped, not typed**: the match is on the first parameter's syntax mentioning `CanonCaller`, because
// this package parses without type information. The cost is that a closure taking some other
// `…CanonCaller` would be in scope; there is no such type, and the alternative — loading types for the
// whole module — buys nothing a `grep` for the identifier does not already bound.
func canonClosures(fset *token.FileSet, f *ast.File) []canonClosure {
	var out []canonClosure
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.FuncLit)
		if !ok || lit.Type.Params == nil || len(lit.Type.Params.List) == 0 {
			return true
		}
		if !strings.Contains(exprString(lit.Type.Params.List[0].Type), "CanonCaller") {
			return true
		}
		out = append(out, canonClosure{
			line:  fset.Position(lit.Pos()).Line,
			waits: waitsIn(fset, lit),
		})
		return true
	})
	return out
}

// waitsIn reports every blocking construct in lit, with whether it sits inside a `Blocking(...)` call.
//
// Containment is by source range rather than by walking into the call's argument, so a wait inside a
// closure passed to `Blocking` counts as guarded however that closure is spelled — which is the shape
// `c.Blocking(func() error { select { … } })` actually has.
func waitsIn(fset *token.FileSet, lit *ast.FuncLit) []canonWait {
	var guards [][2]token.Pos
	ast.Inspect(lit, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Blocking" {
			guards = append(guards, [2]token.Pos{call.Pos(), call.End()})
		}
		return true
	})
	guarded := func(p token.Pos) bool {
		for _, g := range guards {
			if p >= g[0] && p <= g[1] {
				return true
			}
		}
		return false
	}

	var out []canonWait
	add := func(p token.Pos, kind string) {
		out = append(out, canonWait{fset.Position(p).Line, kind, guarded(p)})
	}
	ast.Inspect(lit, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.SelectStmt:
			add(x.Pos(), "a select")
		case *ast.SendStmt:
			// A send blocks too, on an unbuffered or full channel. Included because "it is buffered" is
			// an argument about a capacity a later change falsifies silently — the same reasoning
			// B-MM-3's control gives for refusing a send inside a critical section.
			add(x.Pos(), "a channel send")
		case *ast.UnaryExpr:
			if x.Op == token.ARROW {
				add(x.Pos(), "a channel receive")
			}
		case *ast.CallExpr:
			sel, ok := x.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch sel.Sel.Name {
			case "After", "Sleep", "Tick":
				if exprString(sel.X) == "time" {
					add(x.Pos(), "time."+sel.Sel.Name)
				}
			case "Wait":
				// `sync.WaitGroup.Wait` / `sync.Cond.Wait`. Matched by method name because the receiver's
				// type is unavailable here; a `Wait` on something else is a false positive this control
				// would rather have than the miss, and there are none in the tree today.
				add(x.Pos(), "a "+sel.Sel.Name+"() call")
			}
		}
		return true
	})
	return out
}

// countClosuresWithWaits recounts for the success log, so the figure a green prints comes from the
// instrument rather than from a comment that can go stale.
func countClosuresWithWaits(fset *token.FileSet) int {
	n := 0
	_ = filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipWalkDir(d, "third_party") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return nil
		}
		for _, c := range canonClosures(fset, f) {
			if len(c.waits) > 0 {
				n++
			}
		}
		return nil
	})
	return n
}

// exprString renders the small subset of type/selector expressions this analysis needs.
func exprString(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return "*" + exprString(t.X)
	case *ast.SelectorExpr:
		return exprString(t.X) + "." + t.Sel.Name
	case *ast.Ident:
		return t.Name
	}
	return ""
}

// TestCanonBlockingAnalysisTripsOnASyntheticBareWait is the control above watched die, executably rather
// than in prose.
//
// A structural control's own failure mode is passing while checking nothing, and the two floors catch only
// the *empty-domain* version of that. This catches the other one: an analysis that resolves a domain and
// then fails to notice a violation in it. Each case is a synthetic source fed to the same `canonClosures`
// the walk uses, so the thing verified is the thing that runs.
//
// **The negative cases matter as much as the positives.** A matcher that flagged everything would pass the
// must-trip rows and be useless; `wrapped_select_is_guarded` and `no_wait_at_all` are what make the
// positives mean something.
func TestCanonBlockingAnalysisTripsOnASyntheticBareWait(t *testing.T) {
	for _, tc := range []struct {
		name        string
		src         string
		wantWaits   int
		wantUnguard int
	}{
		{
			name: "bare_select_is_flagged",
			src: `package p
func f() interp.CanonFunc {
	return func(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		select {
		case <-ch:
		case <-time.After(d):
		}
		return nil, nil
	}
}`,
			// select + two receives + time.After
			wantWaits: 4, wantUnguard: 4,
		},
		{
			name: "wrapped_select_is_guarded",
			src: `package p
func f() interp.CanonFunc {
	return func(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		if err := c.Blocking(func() error {
			select {
			case <-ch:
				return nil
			case <-time.After(d):
				return errBound
			}
		}); err != nil {
			return nil, err
		}
		return nil, nil
	}
}`,
			wantWaits: 4, wantUnguard: 0,
		},
		{
			name: "an_ignored_caller_does_not_exempt_it",
			// The exact shape of the defect: the parameter is `_`, so there is nothing to misuse. The
			// control must judge it anyway — keying on the parameter's NAME rather than its type would
			// have let the one real instance through.
			src: `package p
func f() interp.CanonFunc {
	return func(_ *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		<-ch
		return nil, nil
	}
}`,
			wantWaits: 1, wantUnguard: 1,
		},
		{
			name: "a_bare_sleep_is_flagged",
			src: `package p
func f() interp.CanonFunc {
	return func(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		time.Sleep(d)
		return nil, nil
	}
}`,
			wantWaits: 1, wantUnguard: 1,
		},
		{
			name: "a_blocking_send_is_flagged",
			src: `package p
func f() interp.CanonFunc {
	return func(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		ch <- v
		return nil, nil
	}
}`,
			wantWaits: 1, wantUnguard: 1,
		},
		{
			name: "no_wait_at_all",
			src: `package p
func f() interp.CanonFunc {
	return func(c *interp.CanonCaller, args []interp.Value) ([]interp.Value, error) {
		return []interp.Value{}, nil
	}
}`,
			wantWaits: 0, wantUnguard: 0,
		},
		{
			name: "a_non_canon_closure_is_out_of_scope",
			// The domain is derived from the parameter type. A closure that blocks but is not a canon
			// function is not this control's business, and flagging it would make the control unusable.
			src: `package p
func f() func() {
	return func() {
		select {
		case <-ch:
		}
	}
}`,
			wantWaits: 0, wantUnguard: 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, tc.name+".go", tc.src, 0)
			if err != nil {
				t.Fatalf("parsing the synthetic source: %v", err)
			}
			waits, unguarded := 0, 0
			for _, c := range canonClosures(fset, f) {
				for _, w := range c.waits {
					waits++
					if !w.guarded {
						unguarded++
					}
				}
			}
			if waits != tc.wantWaits {
				t.Errorf("found %d wait(s), want %d — the analysis does not see what this case contains",
					waits, tc.wantWaits)
			}
			if unguarded != tc.wantUnguard {
				t.Errorf("found %d UNGUARDED wait(s), want %d — the containment test is wrong, which is "+
					"the half that decides whether a real violation is reported",
					unguarded, tc.wantUnguard)
			}
		})
	}
}
