// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package testenv_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// litmusNamedCaseRE is how a test claims a litmus case: its doc comment's first paragraph says it *is* that case,
// as in "… is the litmus battery case `slug`" or "… is case `slug`". The claim has to be in the first
// paragraph, and phrased with `case` before the slug. A test that only mentions a case further down its comment
// (`memregime_test.go` does, for `b-mm-2-sibling-field-after-wake`) is not claiming to implement it.
var litmusNamedCaseRE = regexp.MustCompile("\\bcase `([a-z0-9-]+)`")

// litmusCasesClaimedByTests maps each litmus case slug to the tests whose doc comment claims to be it. It parses
// the tree rather than grepping it, so a slug quoted in a string or in a helper's comment is not a claim.
func litmusCasesClaimedByTests(tb testing.TB) map[string][]string {
	tb.Helper()
	claimed := map[string][]string{}
	err := filepath.WalkDir("../..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipWalkDir(d, "third_party") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments)
		if perr != nil {
			return perr
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Doc == nil || !strings.HasPrefix(fn.Name.Name, "Test") {
				continue
			}
			first, _, _ := strings.Cut(fn.Doc.Text(), "\n\n")
			for _, m := range litmusNamedCaseRE.FindAllStringSubmatch(normalizeSpace(first), -1) {
				claimed[m[1]] = append(claimed[m[1]], fn.Name.Name)
			}
		}
		return nil
	})
	if err != nil {
		tb.Fatalf("reading the tree's tests: %v", err)
	}
	return claimed
}

// blockedCasesWithATest returns one finding per case whose registered status is `blocked — #N` while a test
// claims to be that case.
func blockedCasesWithATest(entries map[string]*litmusEntry, claimed map[string][]string) []string {
	var out []string
	for _, clause := range sortedKeys(entries) {
		for _, c := range entries[clause].cases {
			if !litmusBlockRE.MatchString(c.keys["Status"]) {
				continue
			}
			if tests := claimed[c.name]; len(tests) > 0 {
				sort.Strings(tests)
				out = append(out, c.name+" ("+strings.Join(tests, ", ")+")")
			}
		}
	}
	return out
}

// TestALitmusCaseMarkedBlockedHasNoTestClaimingIt is the inverse of `make blockercheck`, ordered as item A of the
// §4 plan on #10 (the chair's third ruling of 2026-10-24).
//
// `blockercheck` asks whether a case's blocker is still open. It cannot see a case that was *built* while its
// blocker stayed open: `sp1-stop-brings-every-agent-to-a-safepoint-by-the-deadline` landed in #1061 as
// `TestSP1StopBringsEveryAgentToASafepointByTheDeadline`, and its row went on reading `blocked — #10` because
// #10 is the battery's own tracking issue and never closes before the battery does. This control reads the
// other side: a test whose doc comment claims a case fails the control while that case's row reads blocked.
//
// Hermetic, unlike `blockercheck`: the evidence is the tree's own tests, not the tracker.
//
// WITNESS, run: on the tree before the SP-1 row was corrected, the registration arm failed naming `sp1-…` and
// `TestSP1StopBringsEveryAgentToASafepointByTheDeadline`, and no other case, of the 13 that tests claimed.
// NEUTER, run: the test-existence lookup in `blockedCasesWithATest` (`claimed[c.name]`) replaced by nil. The
// registration arm then passed the stale SP-1 row, and the synthetic arm failed with `got []`.
func TestALitmusCaseMarkedBlockedHasNoTestClaimingIt(t *testing.T) {
	t.Run("a_blocked_row_whose_test_resolves_fails", func(t *testing.T) {
		entries := map[string]*litmusEntry{"X-1": {clause: "X-1", cases: []litmusCase{
			{name: "x1-a-case", keys: map[string]string{"Status": "blocked — #10"}},
			{name: "x1-another", keys: map[string]string{"Status": "implemented — TestY"}},
		}}}
		claimed := map[string][]string{"x1-a-case": {"TestX"}, "x1-another": {"TestY"}}
		got := blockedCasesWithATest(entries, claimed)
		if len(got) != 1 || got[0] != "x1-a-case (TestX)" {
			t.Fatalf("got %q; want exactly the blocked row with a claiming test, x1-a-case (TestX)", got)
		}
	})

	t.Run("the_registration", func(t *testing.T) {
		claimed := litmusCasesClaimedByTests(t)
		if len(claimed) == 0 {
			t.Fatal("no test in the tree claims a litmus case, so this check would pass over nothing; the " +
				"battery's tests say `is the litmus battery case `slug`` in their first paragraph")
		}
		for _, f := range blockedCasesWithATest(litmusEntries(t), claimed) {
			t.Errorf("%s: case %s reads `blocked — #N`, and a test claims to be it. Either the case is "+
				"built and its row should read `implemented — TestName`, or the test's comment claims a "+
				"case it does not implement", litmusDoc, f)
		}
		t.Logf("%d litmus case(s) claimed by a test", len(claimed))
	})
}
