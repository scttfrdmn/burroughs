// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package validate

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEveryOperandPopIsFatalToItsArm pins the property #394's convergence rests on: every call to
// an operand-popping helper returns its error immediately.
//
// `popExpectAll` used to pop as it went, so a failure left the operand stack partially consumed;
// `popSeqExpect` pops nothing on failure. Swapping one for the other is behaviour-preserving **only
// because no arm looks at the stack after a failed pop** — and that is a property of 62 call sites
// in seven files, not of the helper. A doc comment asserting it would be the defect stated as the
// rule: review reads the sentence, the sentence is right, and the one arm that swallows an error is
// two files away.
//
// # What "fatal" is allowed to look like
//
// Two shapes, which are the two the package already uses:
//
//	return v.popExpect(t)                                  // the call is the return value
//	if err := v.popExpectAll(ts); err != nil { return err } // checked and returned
//
// Anything else fails: `_ = v.popExpect(t)`, an assignment whose `err` is checked later, a call
// inside a condition whose body does not return. The check is on the *syntax* rather than on a
// dataflow analysis because the two licensed shapes are syntactic, and a call that needs an
// analysis to prove it fatal is a call worth rewriting into one of them.
//
// # The domain is derived, and its size is pinned rather than floored
//
// The files come from a glob of the package directory, not a list — a control scoped to today's
// files inherits today's blind spot, and this package gains a file per proposal slice. The count is
// pinned **exactly**: a floor catches the domain collapsing and would say nothing about six call
// sites quietly leaving the checked population, which is the failure mode a refactor actually has.
// It moves when an arm is added, and that is the point rather than the cost — the number is one
// line, and the thing it buys is that nobody can remove a call site's error check by deleting the
// call site's whole statement.
//
// # Watched die, arm by arm
//
// Five mutations, each applied and run, because a control with four trigger shapes can have three
// of them stillborn and still report a clean pass. What each one printed:
//
//	mutation (at vec.go:88)                        caught by
//	-----------------------------------------------+------------------------------------------------
//	`_ = v.popExpectAll(s.params)`                   assigned-rather-than-returned, and the arm count
//	`v.popExpectAll(s.params)` bare                  bare-statement, and the arm count
//	`perr := …` then `_ = perr`                      assigned-rather-than-returned, and the arm count
//	error checked, body pushes instead of returning  body-does-not-return, alone
//	`popExpect` stops delegating                     the delegation count, alone
//
// The two "alone" rows are where this control is thinnest, and they are the two the count cannot
// see: a non-returning body and a wrapper growing logic both leave the number of call sites intact.
//
// # Two counts, and the split is on a property rather than on a file name
//
// `popExpect` and `popExpectAll` are `popSeqExpect` wrappers, so their bodies are call sites too —
// and they are *delegations*, which say nothing about whether an arm checks an error. Splitting them
// out by asking "is this `stack.go`?" would be the file-as-rule-owner proxy this project has already
// paid for twice; the property is **the enclosing function is itself one of the helpers**, which is
// true wherever the primitive is later moved to.
func TestEveryOperandPopIsFatalToItsArm(t *testing.T) {
	const (
		wantArms        = 62 // arm sites: seven files, one per typing rule that consumes operands
		wantDelegations = 2  // popExpect and popExpectAll, each one line over popSeqExpect
	)

	helpers := map[string]bool{"popExpect": true, "popExpectAll": true, "popSeqExpect": true}

	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	var files []string
	for _, p := range paths {
		if !strings.HasSuffix(p, "_test.go") {
			files = append(files, p)
		}
	}
	// Vacuity floor on the *domain*, not on the finding: an empty file list agrees with an empty
	// set of call sites and reports a clean run. 10 against the package's current size leaves room
	// for files to merge without leaving room for the glob to stop globbing.
	if len(files) < 10 {
		t.Fatalf("domain collapsed: %d non-test files in the package, want at least 10 — the glob "+
			"is what defines this control's population, so a shrunken one passes by asking nothing",
			len(files))
	}

	fset := token.NewFileSet()
	arms, delegations := 0, 0
	for _, path := range files {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		f, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			t.Fatalf("%s: parse: %v", path, err)
		}

		// The walk is over statements rather than over calls, because the question is what the
		// *enclosing statement* does with the error — a call-first walk has to climb back up to
		// find out, and climbing needs a parent map the AST does not carry.
		//
		// One walk rather than two, and the reason is a bug the two-walk draft had: an `if err :=
		// v.popExpect(…); err != nil` **contains** an AssignStmt, so a second pass looking for
		// stray assignments flagged all ten of the licensed multi-statement arms. `ast.Inspect`
		// visits a parent before its children, so recording the licensed `Init` on the way down is
		// enough — and doing it in one pass means the classification cannot disagree with itself.
		licensedInit := map[ast.Node]bool{}
		inHelper := false
		count := func() {
			if inHelper {
				delegations++
				return
			}
			arms++
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch s := n.(type) {
			case *ast.FuncDecl:
				// `ast.Inspect` visits a declaration before its body and this package declares no
				// nested functions, so a flag is enough where a stack would otherwise be needed.
				inHelper = helpers[s.Name.Name]
			case *ast.ReturnStmt:
				for _, r := range s.Results {
					if _, ok := poppedHelper(r, helpers); ok {
						count()
					}
				}
			case *ast.IfStmt:
				name, ok := ifInitPop(s, helpers)
				if !ok {
					return true
				}
				count()
				licensedInit[s.Init] = true
				if !bodyReturns(s.Body) {
					t.Errorf("%s: %s's error is checked and the body does not return — a pop whose "+
						"failure is not fatal leaves the operand stack in a state no arm reads, "+
						"which is exactly the difference popSeqExpect's pop-on-success-only hides",
						fset.Position(s.Pos()), name)
				}
			// Everything below is a call this control did not license. Without these two arms it
			// would be an under-matching trigger: `_ = v.popExpect(t)` is an AssignStmt and a bare
			// `v.popExpect(t)` is an ExprStmt, and neither would be counted or complained about.
			case *ast.ExprStmt:
				if name, ok := poppedHelper(s.X, helpers); ok {
					t.Errorf("%s: %s is called as a bare statement, so its error is discarded",
						fset.Position(s.Pos()), name)
				}
			case *ast.AssignStmt:
				if licensedInit[n] {
					return true
				}
				for _, r := range s.Rhs {
					if name, ok := poppedHelper(r, helpers); ok {
						t.Errorf("%s: %s's error is assigned rather than returned — the licensed "+
							"shapes are `return v.%s(…)` and `if err := v.%s(…); err != nil`",
							fset.Position(s.Pos()), name, name, name)
					}
				}
			}
			return true
		})
	}

	if arms != wantArms {
		t.Errorf("counted %d arm pop sites, want exactly %d — if an arm was added or removed, "+
			"move this number and say so; if it moved on its own, a call site left the checked "+
			"population without anybody deciding that", arms, wantArms)
	}
	// Pinned in the same breath because a delegation that grows a body stops being a delegation:
	// the whole verdict-identity argument in `popSeqExpect`'s comment assumes `popExpect` adds
	// nothing but an arity, and a third wrapper appearing here is where that assumption would go.
	if delegations != wantDelegations {
		t.Errorf("counted %d pops inside the helpers themselves, want exactly %d — a wrapper was "+
			"added, removed, or grew logic of its own", delegations, wantDelegations)
	}
}

// poppedHelper reports the helper name if e is a call to one of them on the validator receiver.
func poppedHelper(e ast.Expr, helpers map[string]bool) (string, bool) {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return "", false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || !helpers[sel.Sel.Name] {
		return "", false
	}
	return sel.Sel.Name, true
}

// ifInitPop reports the helper name when s is `if err := v.popX(…); err != nil`.
func ifInitPop(s *ast.IfStmt, helpers map[string]bool) (string, bool) {
	assign, ok := s.Init.(*ast.AssignStmt)
	if !ok {
		return "", false
	}
	for _, r := range assign.Rhs {
		if name, ok := poppedHelper(r, helpers); ok {
			return name, true
		}
	}
	return "", false
}

// bodyReturns reports whether the block returns on every path out of it — approximated by "the
// last statement is a return", which is the only shape this package writes and the only one worth
// licensing: a conditional return inside the error branch is a pop whose failure is recoverable,
// and there is no such pop.
func bodyReturns(b *ast.BlockStmt) bool {
	if len(b.List) == 0 {
		return false
	}
	_, ok := b.List[len(b.List)-1].(*ast.ReturnStmt)
	return ok
}
