package testenv_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
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
// Callers like internal/spec's requireSuite delegate here and so are not sites
// themselves.
//
// Every entry is in this one file, which is the point: routing all skips through
// testenv is what makes a single env var able to revoke them all. The inventory
// grows one line per *corpus*, not one per test — a second corpus (the reference
// interpreter, decision 0007) meant a second door, not a second convention.
var licensed = map[string]string{
	"internal/testenv/testenv.go:RequireSuite": "local dev on a clone without `make spec-tests`, revoked by BURROUGHS_NO_SKIP=1",
	// The 0007 authority is a separate corpus from the suite with a separate fetch
	// (`make spec-ref`), so it needs its own door rather than a widened RequireSuite:
	// the two absences have different remedies, and a message naming the wrong one
	// sends a reader to the wrong make target. Belt and suspenders, as with the
	// suite: `make opcode-drift` refuses to run at all without the reference, so
	// this license only ever fires under a bare `go test ./...`.
	"internal/testenv/testenv.go:RequireSpecRef": "local dev on a clone without `make spec-ref`, revoked by BURROUGHS_NO_SKIP=1",
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

// gatedFuzzTargets is where each fuzz target is *run under a budget* — the Makefile's
// `fuzz` recipe and the CI `fuzz-smoke` job — with the reason for its budget size.
//
// The sibling of licensed above, and it exists because the same defect was found in the
// same tree: a fuzz target is only equipment if something runs it. FuzzConstExprProgress
// was written with the instruction grammar (#43/#39), landed with eleven seeds and a
// fourteen-sentinel allowed-error list, and was gated in **neither** the Makefile nor
// either workflow. It ran only under `go test` as an ordinary seed-corpus test, so its
// exploration half — the part that finds what no seed reaches — had never once executed.
//
// Three enumerations of the same set (Makefile, ci.yml, nightly.yml) and no control over
// any of them, which is *derive the domain, never enumerate it* broken three times over.
// This test derives the domain from the tree and requires each member to be gated
// somewhere, so writing a target without budgeting it is a build failure rather than a
// target that quietly never runs.
//
// The values are not budgets — a size lives in the Makefile and the workflow, and copying
// it here would be a fourth place to drift. They are *reasons*, which is the part a
// reviewer needs and the part no recipe can hold.
var gatedFuzzTargets = map[string]string{
	"FuzzDecodeModule":      "the whole-module entry point; the largest budget (3M in CI) because every other target is a subset of its surface",
	"FuzzULEB":              "the LEB readers, where the malformed taxonomy is width-parameterized; 2:1 smaller than the module target",
	"FuzzWastLexer":         "the harness's own parser — a lexer bug is a corpus bug, so it is budgeted like a decoder",
	"FuzzParseNodeProgress": "the zero-progress property (grave #18), which needs mutation rather than seeds to falsify",
	"FuzzConstExprProgress": "the instruction grammar's progress property, now over a recursive grammar (block -> instr -> structural -> block); the recursion is what makes a hang plausible rather than theoretical",
}

// TestEveryFuzzTargetIsGated reads the tree for `func FuzzX(f *testing.F)` and requires
// every one to appear in gatedFuzzTargets, in the Makefile's fuzz recipe, and in the CI
// smoke job.
//
// Both directions, as with licensed: a stale entry claims coverage that has moved. And the
// three run-sites are checked *separately* rather than as one "is it gated anywhere",
// because they answer different questions — `make fuzz` is the local mirror, `fuzz-smoke`
// is the per-PR gate, and a target in one but not the other is exactly the surprise the
// Makefile exists to prevent (decision 0005).
func TestEveryFuzzTargetIsGated(t *testing.T) {
	root := "../.."

	found := map[string]string{} // target -> position
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
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Fuzz") {
				continue
			}
			// Signature check rather than name check: `func FuzzyMatch(s string)` is not
			// a fuzz target, and Go's own rule is the parameter type. Matching on the
			// name alone would put a helper in the inventory and send a reader looking
			// for a budget that should not exist.
			if fn.Type.Params == nil || len(fn.Type.Params.List) != 1 {
				continue
			}
			star, ok := fn.Type.Params.List[0].Type.(*ast.StarExpr)
			if !ok {
				continue
			}
			sel, ok := star.X.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "F" {
				continue
			}
			found[fn.Name.Name] = fset.Position(fn.Pos()).String()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	// The vacuity check: an empty walk agrees with an empty inventory, and a moved file
	// or a changed signature convention produces exactly that. A floor rather than a
	// non-nil check, because the failure this guards is "found nothing", not "found nil".
	if len(found) < 4 {
		t.Fatalf("found only %d fuzz targets in the tree; the walk is not finding them, so every "+
			"assertion below is comparing two nearly-empty sets and agreeing", len(found))
	}

	sites := map[string]string{
		"Makefile":                      readFile(t, filepath.Join(root, "Makefile")),
		".github/workflows/ci.yml":      readFile(t, filepath.Join(root, ".github/workflows/ci.yml")),
		".github/workflows/nightly.yml": readFile(t, filepath.Join(root, ".github/workflows/nightly.yml")),
	}

	for target, pos := range found {
		if _, ok := gatedFuzzTargets[target]; !ok {
			t.Errorf("%s has no entry in gatedFuzzTargets (%s)\n\t"+
				"a fuzz target nothing runs under a budget is not equipment — it is a file. Add it to\n\t"+
				"the inventory with the reason for its budget size, and to the Makefile and both workflows.",
				target, pos)
		}
		for name, body := range sites {
			if !strings.Contains(body, target) {
				t.Errorf("%s is not run in %s (%s)\n\t"+
					"defined but never budgeted: its exploration half never executes, so it has tested a\n\t"+
					"corpus rather than a grammar. This is how FuzzConstExprProgress shipped ungated.",
					target, name, pos)
			}
		}
	}

	for target := range gatedFuzzTargets {
		if _, ok := found[target]; !ok {
			t.Errorf("gatedFuzzTargets lists %q, which no longer exists in the tree; remove the entry "+
				"and its budget lines", target)
		}
	}

	if !t.Failed() {
		t.Logf("%d fuzz targets, all budgeted in the Makefile and both workflows", len(found))
	}
}

// readFile is a Fatal-on-error read: a missing Makefile or workflow means the control
// cannot answer, and answering anyway would be a comparison against an empty string —
// which every Contains check below would fail loudly rather than silently, but the
// diagnosis would name the wrong thing.
func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v — the control's own input is missing", path, err)
	}
	return string(b)
}
