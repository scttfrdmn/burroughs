package testenv_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
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
// grows one line per *question*, not one per test — a second corpus (the reference
// interpreter, decision 0007) meant a second door, not a second convention.
//
// **Four doors, and a fifth that was written and withdrawn.** A control pre-registered
// against code not yet written wants to skip until its subject exists, and the obvious
// door for it — condition "the subject is absent", revoked by the subject answering rather
// than by the flag — was drafted here and rejected by CI's *no test declined to answer*
// step, which greps the output channel for SKIP lines under the flag and fails on any of
// them. The rejection was right twice over: it is the ruling this file's policy already
// implies, and the control turned out to have something to assert today after all (the
// accept direction, at the lexer). Recorded because the near-miss is the lesson: **a
// pre-registered control that wants a skip has usually not found the layer where its
// property is already checkable.** Look for that layer before asking for a license.
var licensed = map[string]string{
	"internal/testenv/testenv.go:RequireSuite": "local dev on a clone without `make spec-tests`, revoked by BURROUGHS_NO_SKIP=1",
	// The 0007 authority is a separate corpus from the suite with a separate fetch
	// (`make spec-ref`), so it needs its own door rather than a widened RequireSuite:
	// the two absences have different remedies, and a message naming the wrong one
	// sends a reader to the wrong make target. Belt and suspenders, as with the
	// suite: `make opcode-drift` refuses to run at all without the reference, so
	// this license only ever fires under a bare `go test ./...`.
	"internal/testenv/testenv.go:RequireSpecRef": "local dev on a clone without `make spec-ref`, revoked by BURROUGHS_NO_SKIP=1",
	// The gate mapping's citations (decision 0008) point at proposal *documents*, not
	// at decode.ml, so they are a third input under the same fetch as the second. A
	// third door rather than a widened RequireSpecRef for the reason above — the size
	// floor and the diagnosis differ, and RequireSpecRef's message would send a reader
	// looking for a truncated decode.ml when a proposal overview is what is missing.
	//
	// Worth noting how this line came to exist: the citation check was first written with
	// a bare t.Skipf, and TestEverySkipSiteIsLicensed failed the build — the mechanism
	// catching an unlicensed skip written by the author who knows the rule. That is the
	// case for reading the AST instead of trusting the convention.
	"internal/testenv/testenv.go:RequireProposalDoc": "local dev on a clone without `make spec-ref`, revoked by BURROUGHS_NO_SKIP=1",
	// A fourth door, and the *same* corpus as the first — which is the one case the
	// "one line per corpus" note above did not anticipate. The unit that earns a door
	// is really the *question*: RequireSuite asserts a count over 257 files, and a
	// citation check against one named vector needs that file to exist, which a full
	// corpus with one file missing satisfies and a 249-file corpus does not. Two
	// questions, two floors, two diagnoses.
	//
	// Its arrival repeated RequireProposalDoc's: the skip was first written inline in
	// keywordgen's citation check and TestEverySkipSiteIsLicensed failed the build.
	// Twice now the mechanism has caught an author who knew the rule.
	"internal/testenv/testenv.go:RequireSuiteFile": "local dev on a clone without `make spec-tests`, revoked by BURROUGHS_NO_SKIP=1",
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
			if skipWalkDir(d) {
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
			// Note what this message does *not* offer: a way to add a skip whose condition
			// the flag cannot revoke. That was drafted (see the header) and withdrawn,
			// because CI's no-SKIP step forbids it and because the control that wanted it
			// had a checkable property at a lower layer. The advice stays as it was.
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
	"FuzzLexerProgress":     "the wat lexer's arm-length invariant; sized by its own measured throughput (~9x the per-execution cost of the wast lexer, from large seeds and a full arm sweep per position) rather than by the 2:1 convention, because a ratio inherited from a cheaper target buys duration it cannot pay for",
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
			if skipWalkDir(d) {
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

// rePinnedRev is the shape a *pinned corpus* declares itself in: a fetch script with a
// 40-hex revision on its own line. The same shape `internal/gen.rePin` reads, and named
// here rather than imported because this control's subject is the script's existence
// rather than its value.
var rePinnedRev = regexp.MustCompile(`(?m)^rev="[0-9a-f]{40}"`)

// reMakeTarget matches a Makefile target line. Recipe lines are excluded by the caller
// (they begin with a tab), so `^name:` at column zero is a target and nothing else in
// this file is: variable assignments are upper-case, `.PHONY` begins with a dot.
var reMakeTarget = regexp.MustCompile(`^([a-z][a-z0-9-]*):`)

// reWorkflowJob matches a job header inside a workflow's `jobs:` block. Applied only to
// the slice after the `jobs:` line, because `on:`'s `push:`/`pull_request:` and
// `defaults:`' `run:` have the same indentation and would otherwise read as jobs.
var reWorkflowJob = regexp.MustCompile(`^  ([A-Za-z][A-Za-z0-9_-]*):[[:space:]]*$`)

// TestEveryPinnedCorpusIsFetchedByEveryUnitTestJob requires every pinned corpus in the
// tree to be fetched by every CI job that runs unit tests.
//
// # The defect it was written for
//
// The threads reference pin (ADR 0007's 2026-08-28 amendment) landed with its script, its
// Makefile target, its floors, its licensed paths, and no CI step. `BURROUGHS_NO_SKIP: '1'`
// is workflow-wide, so an absent authority is a *fail* rather than a skip: the `build` and
// `conformance` jobs went red on a pin that was otherwise complete. **`make check` could
// not have seen it** — the local gate deliberately leaves NO_SKIP unset, and a box that has
// run `make threads-ref` once has the corpus forever, so the local mirror was green on a
// machine where the third fetch had already happened. *Text mirrors are not
// failure-behaviour mirrors*: "make check is the local mirror of CI" is intent, and this is
// one of the seams where the two shells differ by construction.
//
// # Why this is the fuzz-inventory shape rather than a new one
//
// `TestEveryFuzzTargetIsGated` above requires each defined fuzz target to appear at each
// of its run sites, in both directions, because a target in one site and not another is
// the surprise the Makefile exists to prevent (decision 0005). A pinned corpus is the same
// object one layer down: defined in one place, required at several, with nothing but a
// reader's memory joining them. So this control shares that one's inputs — the Makefile
// and every workflow, already read here — which is why it is a predicate over data a gate
// already fetches rather than another instrument.
//
// # Every vocabulary is derived
//
// The corpora come from `scripts/*.sh` filtered by the revision pin, not from a list of
// three names and not from a `fetch-*` filename convention: a fourth pin arrives covered,
// and a pin whose script is renamed stays covered. Its Makefile target comes from the
// recipe that invokes the script, so the target is never typed twice. The jobs come from
// each workflow's `jobs:` block. Nothing here enumerates what it could derive — *derive the
// domain, never enumerate it* (0006/#33).
//
// # What "unit-test job" means, and the exemption's own risk
//
// A job runs unit tests if it invokes `go test` without `-fuzz`. The fuzzing jobs pair
// `-fuzz` with `-run XXX`, which matches no test function, so they execute a grammar and no
// corpus-gated test — `fuzz-smoke` and nightly's `fuzz` are the two members of that class
// today, and they vendor only what their seeds need.
//
// **The exemption is where this control can be phrased around**, and an exemption inherits
// none of the trigger's lessons, so it is stated: adding `-fuzz` to a job that also runs
// unit tests would silence this check for that job. The guard is the floor below — at least
// two jobs must classify as unit-test jobs — which fails loudly if the classifier's
// including arm ever drains to one.
//
// # Watched die, five ways
//
// Each neuter was applied, run, and reverted. (1) `main`'s own `ci.yml` — the defect, with
// no `make threads-ref` anywhere: two failures, naming `build` and `conformance` and the
// script whose corpus is missing. (2) `make spec-tests` removed from `conformance` only: one
// failure, the *other* job still green, so the report is per job rather than per tree.
// (3) The pin filter changed to `revision="…"`: the plurality floor, at 0 found. (4) `-fuzz`
// added to the `build` job's `go test` lines — the phrase-around-it move: the classifier
// floor, at 1. (5) The threads fetch recipe removed from the Makefile: the left-hand-side
// Fatal, quoting the pin it found with no target. *A control isn't born until it's watched
// die*, and arm (1) is the only one of the five whose subject was a real committed state.
//
// # The policy this encodes, which is broader than today's need
//
// It requires *every* pinned corpus of *every* unit-test job, not the corpora that job's
// packages happen to need. That is the workflow's own stated policy: which package needs
// which corpus is not a fact a workflow file can track, and `go test ./...` inherits every
// corpus requirement in the tree. A job that genuinely needs one corpus and not another
// therefore fails this control, and the remedy is to state the exception here — not to
// narrow the job's package list until the check stops noticing.
func TestEveryPinnedCorpusIsFetchedByEveryUnitTestJob(t *testing.T) {
	root := "../.."

	// The pinned corpora, derived by content: a shell script that declares a revision.
	shs, err := filepath.Glob(filepath.Join(root, "scripts", "*.sh"))
	if err != nil {
		t.Fatalf("glob scripts: %v", err)
	}
	pinned := map[string]string{} // "scripts/x.sh" -> the pin line, for the message
	for _, sh := range shs {
		b, rerr := os.ReadFile(sh)
		if rerr != nil {
			t.Fatalf("read %s: %v — the control's own input is missing", sh, rerr)
		}
		if m := rePinnedRev.Find(b); m != nil {
			pinned[filepath.ToSlash(filepath.Join("scripts", filepath.Base(sh)))] = string(m)
		}
	}
	// The vacuity floor, and it is a *plurality* floor on purpose: this control is about
	// the join between a pin set and its run sites, and with one pin found it would agree
	// with any workflow that fetched that one. Three at the pin's writing — suite,
	// reference, threads reference.
	if len(pinned) < 3 {
		t.Fatalf("found only %d pinned corpora under scripts/ (want >=3); the content filter is not "+
			"matching the pin declarations, so every assertion below is about a nearly-empty set", len(pinned))
	}

	// Each corpus's Makefile targets — all of them, not the first. *A first-match pick
	// declines to ask*: two targets fetching one corpus is legitimate, and a job invoking
	// either one has vendored it, so the check below accepts any member.
	targets := map[string][]string{}
	cur := ""
	for _, ln := range strings.Split(readFile(t, filepath.Join(root, "Makefile")), "\n") {
		if !strings.HasPrefix(ln, "\t") {
			if m := reMakeTarget.FindStringSubmatch(ln); m != nil {
				cur = m[1]
			}
			continue
		}
		for script := range pinned {
			if cur != "" && strings.Contains(ln, script) {
				if !slices.Contains(targets[script], cur) {
					targets[script] = append(targets[script], cur)
				}
			}
		}
	}
	for script, pin := range pinned {
		if len(targets[script]) == 0 {
			t.Fatalf("%s declares a pin (%s) and no Makefile recipe invokes it\n\t"+
				"a corpus with no target cannot be fetched by CI at all, so the join this control "+
				"checks has no left-hand side. Give it a target, per decision 0005: tooling is "+
				"reached through the Makefile, never spelled into a workflow.", script, pin)
		}
	}

	// The jobs, per workflow.
	wfs, err := filepath.Glob(filepath.Join(root, ".github/workflows/*.yml"))
	if err != nil {
		t.Fatalf("glob workflows: %v", err)
	}
	if len(wfs) < 2 {
		t.Fatalf("found %d workflow files (want >=2: ci and nightly); the glob is not finding them", len(wfs))
	}
	type job struct{ wf, name, body string }
	var jobs []job
	for _, wf := range wfs {
		lines := strings.Split(readFile(t, wf), "\n")
		start := -1
		for i, ln := range lines {
			if ln == "jobs:" {
				start = i + 1
				break
			}
		}
		if start < 0 {
			t.Fatalf("%s has no top-level `jobs:` line; the workflow's shape has changed and this "+
				"control is reading it wrong", wf)
		}
		name, body := "", []string{}
		flush := func() {
			if name != "" {
				jobs = append(jobs, job{filepath.Base(wf), name, strings.Join(body, "\n")})
			}
		}
		for _, ln := range lines[start:] {
			if m := reWorkflowJob.FindStringSubmatch(ln); m != nil {
				flush()
				name, body = m[1], nil
				continue
			}
			body = append(body, ln)
		}
		flush()
	}

	var unit, fuzzOnly []job
	for _, j := range jobs {
		runsUnit, runsAny := false, false
		for _, ln := range strings.Split(j.body, "\n") {
			if !strings.Contains(ln, "go test") {
				continue
			}
			runsAny = true
			if !strings.Contains(ln, "-fuzz") {
				runsUnit = true
			}
		}
		switch {
		case runsUnit:
			unit = append(unit, j)
		case runsAny:
			fuzzOnly = append(fuzzOnly, j)
		}
	}
	// The classifier's floor, which is also the exemption's guard: `build` and
	// `conformance` at the pin's writing. If this ever reads 1, the including arm has
	// drained and the control would pass by asking one job about its corpora.
	if len(unit) < 2 {
		t.Fatalf("classified only %d of the tree's jobs as a unit-test job across %d workflows "+
			"(want >=2); every `go test` line looks like a fuzz invocation, so this control has "+
			"stopped asking", len(unit), len(wfs))
	}

	for _, j := range unit {
		for script, ts := range targets {
			ok := false
			for _, target := range ts {
				if strings.Contains(j.body, "make "+target) {
					ok = true
					break
				}
			}
			if !ok {
				want := make([]string, len(ts))
				for i, target := range ts {
					want[i] = "make " + target
				}
				t.Errorf("%s job %q runs unit tests and never fetches the corpus pinned by %s\n\t"+
					"add one of: %s\n\t"+
					"BURROUGHS_NO_SKIP=1 is workflow-wide, so an absent corpus is a *fail* and not a "+
					"skip: this job goes red on a pin that is otherwise complete. `make check` cannot "+
					"see it — the local gate leaves NO_SKIP unset, and a box that fetched the corpus "+
					"once has it forever.",
					j.wf, j.name, script, strings.Join(want, ", "))
			}
		}
	}

	if !t.Failed() {
		names := make([]string, len(unit))
		for i, j := range unit {
			names[i] = j.wf + ":" + j.name
		}
		t.Logf("%d pinned corpora fetched by all %d unit-test jobs (%s); %d fuzz-only jobs exempt",
			len(pinned), len(unit), strings.Join(names, " "), len(fuzzOnly))
	}
}
