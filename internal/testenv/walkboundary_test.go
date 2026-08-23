// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package testenv_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"slices"
	"strings"
	"testing"
)

// walkSkipDirs are the directory names no tree-walking control in this package descends into.
//
// **One list, because three copies is what grave #369 was.** Two sites in inventory_test.go and one
// in citation_test.go each carried their own literal, and the citation copy had a fifth entry the
// other two did not — under a comment claiming they were the same. So the fix is not "add one name
// in three places", it is to leave one place where a name can be added.
//
// `.claude` is the entry the grave is named for. The agent harness places worktrees at
// `.claude/worktrees/agent-*/`, each a **full copy of the repo**, so a walk that descends into one
// parses real Go files at real paths and finds real violations — of a rule keyed to the *unprefixed*
// path, in a tree that is not this one. Measured at the time: 12 findings, all inside
// `.claude/worktrees/`, zero in the real tree, and `make check` red on a pristine checkout.
//
// That is a **false red**, and it costs what a false green costs. A red on a clean tree trains the
// reflex of scrolling past a red, which is the same thing decision 0005 says about lint noise,
// arriving through the gate instead of through the linter.
var walkSkipDirs = map[string]bool{
	"testdata": true,
	"bin":      true,
	".git":     true,
	"tools":    true,
	".claude":  true,
}

// skipWalkDir answers whether a directory is outside the tree these controls walk. `also` carries a
// caller's own documented additions — citation_test.go's `third_party` — so a divergence between two
// walks is visible at the call site rather than as a diff between two literals nobody compares.
func skipWalkDir(d fs.DirEntry, also ...string) bool {
	return walkSkipDirs[d.Name()] || slices.Contains(also, d.Name())
}

// walkBoundaryFloor is the number of walk sites this package is known to contain, and it is a
// vacuity check rather than a census: the scan below asserts a property of *every* site it finds, and
// a scan that finds none asserts it of nothing and passes.
//
// Six — `TestEverySkipSiteIsLicensed`, `TestEveryFuzzTargetIsGated`, citation_test.go's cite walk,
// `TestForeclosingClaimsAboutGatesMatchTheGateTable`'s (#427/#428), laws_test.go's `mdSources`
// walk (#466), which is the first whose domain is markdown rather than Go — and which found four
// upstream files' links reported as this tree's violations on its first run, so the boundary this
// control guards was load-bearing within minutes of the site being added — and clause_test.go's
// `textSources` walk (#442/ADR 0046), whose domain is **neither** Go nor markdown but every text file
// in the tree, decided by content rather than by extension, which makes it the widest domain any
// control here walks and the one with the most to gain from the boundary holding.
//
// **Seven** since #456: citeform_test.go's `treeFiles` walk, whose domain is every file in the tree
// regardless of extension, because it builds the *vocabulary* a citation is resolved against rather
// than a set of files to scan. That makes it the first site where a walk that overran the boundary
// would not report a false violation but a **false pass** — a citation naming a file that exists only
// inside a nested copy of the repo would resolve, and the control would call it good.
//
// A floor and not an equality, because another walk site is a normal thing to add and the check that
// matters is the routing one; if this number ever needs *lowering*, a walk site was deleted and that
// is worth noticing deliberately. It tracks the known count for exactly that reason — left behind
// when a new site lands, the floor stops being able to notice a deletion.
const walkBoundaryFloor = 7

// TestEveryTreeWalkStopsAtTheRepoBoundary is grave #369's control, and it guards the general shape
// rather than the specific name.
//
// # What it asserts, and why the second half is the load-bearing one
//
//   - **`.claude` is excluded.** The specific repair. Cheap, and it would pass forever while a fourth
//     walk site hand-rolled its own list beside it.
//   - **Every `filepath.WalkDir` site in this package routes through `skipWalkDir`.** This is the half
//     that survives the next author. It is derived by parsing every `_test.go` in this directory and
//     asking, per site, whether the enclosing function calls the shared helper — not by checking a
//     list of sites somebody remembered, which is the *control scoped to the current sample* shape
//     that put three copies of one list in the tree to begin with.
//
// # Why the walk root is the interesting thing
//
// *Coverage is a claim*, and a control that walks "the repo" is asserting it knows where the repo
// ends. A nested checkout of the same repo falsifies that assertion **silently** — every file it
// finds is a genuine Go file, every violation it reports is a genuine violation of the rule as
// written, and the only thing wrong is that they belong to a different tree. Nothing in the finding
// itself says so. That is why the boundary gets a control and not a comment.
func TestEveryTreeWalkStopsAtTheRepoBoundary(t *testing.T) {
	if !walkSkipDirs[".claude"] {
		t.Error("`.claude` is not in walkSkipDirs — grave #369 is reopened. The agent harness " +
			"keeps full repo copies under .claude/worktrees/agent-*/, so every tree-walking " +
			"control in this package will report that tree's files as violations of this tree's " +
			"rules, and `make check` goes red on a clean checkout")
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}

	sites := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, e.Name(), nil, 0)
		if err != nil {
			t.Fatalf("%s: %v", e.Name(), err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			// Per enclosing function rather than per call expression: the helper is called from
			// inside the walk's own closure, so the two are in the same body but not in the same
			// expression, and a per-call scan would have nothing to look at.
			walks, routed := 0, false
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch fun := call.Fun.(type) {
				case *ast.SelectorExpr:
					if id, ok := fun.X.(*ast.Ident); ok && id.Name == "filepath" &&
						(fun.Sel.Name == "WalkDir" || fun.Sel.Name == "Walk") {
						walks++
					}
				case *ast.Ident:
					if fun.Name == "skipWalkDir" {
						routed = true
					}
				}
				return true
			})
			if walks == 0 {
				continue
			}
			sites += walks
			if !routed {
				t.Errorf("%s: %s walks the tree with filepath.WalkDir but never calls "+
					"skipWalkDir — it is carrying its own idea of where the repo ends. That is "+
					"the third copy of one list that grave #369 was, and the copy that omits "+
					"`.claude` turns `make check` red on a pristine tree",
					fset.Position(fn.Pos()), fn.Name.Name)
			}
		}
	}

	if sites < walkBoundaryFloor {
		t.Errorf("found %d tree-walk site(s) in this package, want at least %d — the scan is not "+
			"reading the package, so the routing check above asserted its property of nothing and "+
			"passed. A comparison against an empty set succeeds", sites, walkBoundaryFloor)
	}
}
