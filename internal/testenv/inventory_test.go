package testenv_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// licensed is the inventory of every skip site in the tree, with the license each
// one claims. Keyed by "package/file.go:funcname".
//
// This exists because BURROUGHS_NO_SKIP=1 only revokes the licenses of skips that
// route through testenv. A t.Skip written tomorrow, calling t.Skip directly, would
// be invisible to the flag and to CI — the grave (#29) again, one layer up: the
// mechanism that forbids skips would itself have a precondition nobody asserts,
// namely that all skips go through it.
//
// So this test reads the AST rather than trusting the convention. Adding a skip
// means adding a line here, and adding a line here means writing down why the test
// may decline to answer. A license nobody had to state is a license nobody reviewed.
// The tree currently has exactly one, which is the point: routing every skip
// through one helper is what makes one env var able to revoke them all. Callers
// like internal/spec's requireSuite delegate here and so are not sites themselves.
var licensed = map[string]string{
	"internal/testenv/testenv.go:RequireSuite": "the sole skip in the tree: local dev on a clone without `make spec-tests`, revoked by BURROUGHS_NO_SKIP=1",
}

// skipCalls are the testing.TB methods that end a test without a verdict.
//
// SkipNow and Skipf are here alongside Skip because the class is "declines to
// answer", not "calls a function named Skip" — matching on the convenient name
// would leave two doors open. t.Fatal is deliberately absent: a Fatal is a verdict.
var skipCalls = map[string]bool{"Skip": true, "Skipf": true, "SkipNow": true}

func TestEverySkipSiteIsLicensed(t *testing.T) {
	root := "../.."

	found := map[string]string{} // site -> file:line, for the error message
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "testdata", "bin", ".git", "tools":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		// Walk with the enclosing function tracked, so a site is named by the
		// function that can decline rather than by a line number that moves
		// every time something above it is edited.
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			ast.Inspect(fn, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || !skipCalls[sel.Sel.Name] {
					return true
				}
				// Receiver must plausibly be a testing.TB. Matching on the name
				// is enough here and deliberately over-broad: a false positive
				// costs one inventory line, a false negative costs the control.
				recv, ok := sel.X.(*ast.Ident)
				if !ok || (recv.Name != "t" && recv.Name != "f" && recv.Name != "b" && recv.Name != "tb") {
					return true
				}
				site := rel + ":" + fn.Name.Name
				found[site] = fset.Position(call.Pos()).String()
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	for site, pos := range found {
		if _, ok := licensed[site]; !ok {
			t.Errorf("unlicensed skip at %s (%s)\n\t"+
				"a skip is not a verdict: a test that declines to answer must say why it is allowed to.\n\t"+
				"Add %q to licensed in internal/testenv/inventory_test.go with its reason, and make sure\n\t"+
				"BURROUGHS_NO_SKIP=1 revokes it — otherwise CI will pass by not asking.", pos, site, site)
		}
	}

	// Both directions, per the TestEveryFixtureFileIsChecked lesson: a stale
	// inventory entry makes the list look more thorough than the tree is, and it
	// is also how a license outlives the code that needed it.
	for site := range licensed {
		if _, ok := found[site]; !ok {
			t.Errorf("licensed skip %q no longer exists: remove it from the inventory", site)
		}
	}

	// Conditioned on Failed(): an unconditional "all licensed" printed beside a
	// failure is a dishonest board in miniature, and the log line is the thing a
	// reviewer skims.
	if !t.Failed() {
		t.Logf("%d skip sites, all licensed", len(found))
	}
}
