package testenv_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/scttfrdmn/burroughs/internal/gen"
	"github.com/scttfrdmn/burroughs/internal/testenv"
)

// recorder stands in for *testing.T so the strictness policy can be tested from
// both sides. A control whose failure mode has never been observed is a control
// nobody has reason to trust — and this one's whole job is to fail, so its failing
// path is the path that matters.
type recorder struct {
	testing.TB
	skipped bool
	failed  bool
	msg     string
}

func (r *recorder) Helper() {}

func (r *recorder) Skip(args ...any) {
	r.skipped = true
	r.msg = sprint(args)
}

func (r *recorder) Fatalf(format string, args ...any) {
	r.failed = true
	r.msg = format
	_ = args
}

func sprint(args []any) string {
	var b strings.Builder
	for _, a := range args {
		if s, ok := a.(string); ok {
			b.WriteString(s)
		}
	}
	return b.String()
}

// present builds a directory holding MinSuiteFiles .wast files, so the happy path
// is exercised against a real count rather than a mocked one.
func present(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for i := range testenv.MinSuiteFiles {
		p := filepath.Join(dir, "f"+itoa(i)+".wast")
		if err := os.WriteFile(p, []byte("(module)"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	return dir
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [8]byte
	n := len(buf)
	for i > 0 {
		n--
		buf[n] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[n:])
}

func TestRequireSuiteAbsent(t *testing.T) {
	empty := t.TempDir()

	t.Run("default license skips", func(t *testing.T) {
		t.Setenv(testenv.NoSkipEnv, "")
		r := &recorder{TB: t}
		testenv.RequireSuite(r, empty)
		if !r.skipped || r.failed {
			t.Fatalf("absent corpus with license intact: skipped=%v failed=%v, want skipped", r.skipped, r.failed)
		}
		if !strings.Contains(r.msg, "make spec-tests") {
			t.Errorf("skip message must be actionable, got %q", r.msg)
		}
	})

	t.Run("no-skip revokes it", func(t *testing.T) {
		t.Setenv(testenv.NoSkipEnv, "1")
		r := &recorder{TB: t}
		testenv.RequireSuite(r, empty)
		if !r.failed || r.skipped {
			t.Fatalf("absent corpus with %s=1: skipped=%v failed=%v, want failed", testenv.NoSkipEnv, r.skipped, r.failed)
		}
	})
}

// TestRequireSuitePresent pins the other direction. Without it the policy could be
// "always fail under the flag", which would pass the test above and break CI —
// a predicate is only checked when both of its answers are.
func TestRequireSuitePresent(t *testing.T) {
	dir := present(t)
	for _, v := range []string{"", "1"} {
		t.Setenv(testenv.NoSkipEnv, v)
		r := &recorder{TB: t}
		testenv.RequireSuite(r, dir)
		if r.skipped || r.failed {
			t.Fatalf("%s=%q with %d files present: skipped=%v failed=%v, want neither",
				testenv.NoSkipEnv, v, testenv.MinSuiteFiles, r.skipped, r.failed)
		}
	}
}

// TestPartialFetchIsNotPresent is why the check counts instead of calling os.Stat.
// A directory that exists and holds three files satisfies existence and then yields
// a board computed over three files — a number whose input was never asserted.
func TestPartialFetchIsNotPresent(t *testing.T) {
	dir := t.TempDir()
	for i := range 3 {
		if err := os.WriteFile(filepath.Join(dir, "f"+itoa(i)+".wast"), []byte("(module)"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	t.Setenv(testenv.NoSkipEnv, "1")
	r := &recorder{TB: t}
	testenv.RequireSuite(r, dir)
	if !r.failed {
		t.Fatalf("partial fetch (3 files) accepted as a vendored suite; want failure")
	}
}

// proposalTree builds a present corpus with a proposal directory holding n vectors, plus any
// extra file names written into that directory verbatim.
//
// Built rather than pointed at the real corpus, for the reason the doors take a directory
// parameter at all: the falsification arms need a population they can make wrong, and the
// vendored suite is the one population no test may edit.
func proposalTree(t *testing.T, proposal string, n int, extra ...string) string {
	t.Helper()
	root := present(t)
	dir := testenv.ProposalDir(root, proposal)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for i := range n {
		if err := os.WriteFile(filepath.Join(dir, "v"+itoa(i)+".wast"), []byte("(module)"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	for _, name := range extra {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("(module)"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	return root
}

// TestRequireProposal watches the proposal door fail in each of the ways it can, and pass in
// the one way it must.
//
// The two failures it keeps separable are the whole design: *the corpus is not vendored* is
// RequireSuite's answer and carries a skip license, and *the corpus is vendored and this
// directory disagrees* is a hard error under both env values. A single door with one skip
// branch would let a partial checkout excuse itself from the lane whose entire point is that
// this population is no longer excused (#513).
func TestRequireProposal(t *testing.T) {
	const proposal = "threads"

	t.Run("absent corpus is RequireSuite's answer, not an extent failure", func(t *testing.T) {
		empty := t.TempDir()

		t.Setenv(testenv.NoSkipEnv, "")
		r := &recorder{TB: t}
		if got := testenv.RequireProposal(r, empty, proposal, 4); got != nil {
			t.Errorf("paths=%v over an absent corpus; a caller that ranges over this would "+
				"score a board of nothing", got)
		}
		if !r.skipped || r.failed {
			t.Fatalf("absent corpus with license intact: skipped=%v failed=%v", r.skipped, r.failed)
		}
		// The *actionable* message, which is the half a second Fatalf would have overwritten.
		if !strings.Contains(r.msg, "make spec-tests") {
			t.Errorf("message %q does not name the fetch: a partial-corpus diagnosis here would "+
				"send the reader to look for a file the fetch never wrote", r.msg)
		}

		t.Setenv(testenv.NoSkipEnv, "1")
		r = &recorder{TB: t}
		testenv.RequireProposal(r, empty, proposal, 4)
		if !r.failed || r.skipped {
			t.Fatalf("absent corpus with %s=1: skipped=%v failed=%v", testenv.NoSkipEnv, r.skipped, r.failed)
		}
	})

	t.Run("the extent is exact and is never a skip", func(t *testing.T) {
		for _, tc := range []struct{ have, want int }{{3, 4}, {5, 4}} {
			root := proposalTree(t, proposal, tc.have)
			for _, v := range []string{"", "1"} {
				t.Setenv(testenv.NoSkipEnv, v)
				r := &recorder{TB: t}
				if got := testenv.RequireProposal(r, root, proposal, tc.want); got != nil {
					t.Errorf("%d files against a pin of %d returned %v", tc.have, tc.want, got)
				}
				if !r.failed || r.skipped {
					t.Fatalf("%d files against a pin of %d with %s=%q: skipped=%v failed=%v, "+
						"want a hard failure — a corpus that is present and disagrees is not a "+
						"question that could not be asked",
						tc.have, tc.want, testenv.NoSkipEnv, v, r.skipped, r.failed)
				}
			}
		}
	})

	t.Run("a missing proposal directory under a present corpus is a failure", func(t *testing.T) {
		// The arm that says why RequireSuite alone is not the door: the suite's own count is
		// satisfied and the lane's population is gone.
		t.Setenv(testenv.NoSkipEnv, "")
		r := &recorder{TB: t}
		testenv.RequireProposal(r, present(t), proposal, 4)
		if !r.failed || r.skipped {
			t.Fatalf("present corpus with no proposals/%s: skipped=%v failed=%v", proposal, r.skipped, r.failed)
		}
	})

	t.Run("the exact extent passes, and a sidecar does not count", func(t *testing.T) {
		// Both directions of #340's exclusion in one arm: four vectors plus an AppleDouble
		// sidecar is four, where the arm above proves a *fifth vector* is not. Without the
		// second half this would pass over a door that ignored every extra file.
		root := proposalTree(t, proposal, 4, "._v0.wast")
		for _, v := range []string{"", "1"} {
			t.Setenv(testenv.NoSkipEnv, v)
			r := &recorder{TB: t}
			paths := testenv.RequireProposal(r, root, proposal, 4)
			if r.failed || r.skipped {
				t.Fatalf("four vectors and a sidecar with %s=%q: skipped=%v failed=%v (%s)",
					testenv.NoSkipEnv, v, r.skipped, r.failed, r.msg)
			}
			if len(paths) != 4 {
				t.Fatalf("returned %d paths, want 4: %v", len(paths), paths)
			}
			if !slices.IsSorted(paths) {
				t.Errorf("paths %v are not sorted: a lane's board would then be ordered by the "+
					"filesystem, and a per-file log read against a pinned table is read in order", paths)
			}
			for _, p := range paths {
				if strings.HasPrefix(filepath.Base(p), "._") {
					t.Errorf("sidecar %s returned as a vector", p)
				}
			}
		}
	})
}

func TestSuiteFiles(t *testing.T) {
	empty := t.TempDir()

	t.Run("absent degrades under license", func(t *testing.T) {
		t.Setenv(testenv.NoSkipEnv, "")
		r := &recorder{TB: t}
		if got := testenv.SuiteFiles(r, empty); got != nil {
			t.Fatalf("want nil paths, got %d", len(got))
		}
		if r.failed {
			t.Fatal("seeding without a corpus is legitimate locally; must not fail")
		}
	})

	t.Run("absent fails under no-skip", func(t *testing.T) {
		t.Setenv(testenv.NoSkipEnv, "1")
		r := &recorder{TB: t}
		testenv.SuiteFiles(r, empty)
		if !r.failed {
			t.Fatalf("%s=1 must convert a silent seed downgrade into a failure", testenv.NoSkipEnv)
		}
	})

	t.Run("present returns paths", func(t *testing.T) {
		t.Setenv(testenv.NoSkipEnv, "1")
		r := &recorder{TB: t}
		got := testenv.SuiteFiles(r, present(t))
		if r.failed || len(got) != testenv.MinSuiteFiles {
			t.Fatalf("got %d paths, failed=%v; want %d and no failure", len(got), r.failed, testenv.MinSuiteFiles)
		}
	})
}

// poisonedTree writes one file of every kind the shell and Go could disagree about, and
// returns the directory with the three counts that *distinguish the three definitions*.
//
// The fixture is the whole test, so it is built where its discriminating property can be
// asserted rather than assumed. A clean corpus cannot tell the definitions apart — 257
// vectors and no sidecars is 257 under all three — which is precisely why the grave sat
// unlit: the checker and the consumer agreed for as long as nothing dot-leading existed.
func poisonedTree(t *testing.T) (dir string, vectors, dotBlind, unfiltered int) {
	t.Helper()
	dir = t.TempDir()
	tree := []struct {
		name   string
		vector bool // is it a member of the suite population?
	}{
		{"address.wast", true},
		{"i32.wast", true},
		{"._address.wast", false},    // AppleDouble sidecar: dot-leading, .wast-suffixed, not a vector
		{"._i32.wast", false},        // the second half of the 1:1 poisoning macOS tar produces
		{".hidden.wast", true},       // dot-leading and *not* a sidecar — the residual asymmetry
		{"notes.txt", false},         // wrong suffix
		{"wastless", false},          // no suffix at all
		{"trailing.wast.bak", false}, // .wast present but not final
	}
	for _, f := range tree {
		if err := os.WriteFile(filepath.Join(dir, f.name), []byte("(module)"), 0o600); err != nil {
			t.Fatalf("write %s: %v", f.name, err)
		}
		if f.vector {
			vectors++
		}
		if strings.HasSuffix(f.name, ".wast") {
			unfiltered++ // what Go's bare filepath.Glob("*.wast") saw before #340
			if !strings.HasPrefix(f.name, ".") {
				dotBlind++ // what a POSIX shell's `*.wast` sees
			}
		}
	}
	// The fixture discriminates or it proves nothing: three definitions, three different
	// numbers. Without this the test could be handed a directory on which the wrong
	// expression is indistinguishable from the right one and would report agreement.
	if vectors == dotBlind || vectors == unfiltered || dotBlind == unfiltered {
		t.Fatalf("fixture does not separate the three definitions: vectors=%d dot-blind=%d "+
			"unfiltered=%d — a tree they agree on cannot witness the asymmetry this control is "+
			"about", vectors, dotBlind, unfiltered)
	}
	return dir, vectors, dotBlind, unfiltered
}

// TestShellAndGoAgreeOnTheSuitePopulation is the control #340 asked for, and the one the
// arrangement it replaces did not have.
//
// `TestSuitePinIsAssertedByTheFetchScript` certified that the two sides' *thresholds* agreed —
// `min=250` against `testenv.MinSuiteFiles` — while they were applied to different **sets**:
// Go's floor to a dot-inclusive glob, the shell's to a dot-blind one. An agreement about a
// number is not an agreement about the population it measures, which is *a guard's trigger
// predicate is a claim about the space* aimed at a control rather than at a guard.
//
// So: run both definitions over the same poisoned directory and require the same integer.
// `scripts/suite-count.sh` is executed rather than read, because what a shell script *does*
// with two globs and a `case` is not a property a regexp over its text can assert.
func TestShellAndGoAgreeOnTheSuitePopulation(t *testing.T) {
	dir, vectors, dotBlind, unfiltered := poisonedTree(t)

	const script = "../../scripts/suite-count.sh"
	out, err := exec.Command(script, dir).Output()
	if err != nil {
		t.Fatalf("%s %s: %v\n\tIf this is a permission error the executable bit is not committed, "+
			"and the Makefile and both CI floors invoke this script by path — they would fail the "+
			"same way, one push later.", script, dir, err)
	}
	shell, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("%s printed %q, want one integer: %v", script, out, err)
	}

	paths, err := testenv.SuitePaths(dir)
	if err != nil {
		t.Fatalf("SuitePaths: %v", err)
	}

	if len(paths) != vectors {
		t.Errorf("testenv.SuitePaths counted %d of %d vectors in the poisoned tree (%v).\n\tThe Go "+
			"side of the population moved; the sidecar exclusion is what keeps a corpus that is "+
			"50%% junk from being globbed as though it were the corpus.", len(paths), vectors, paths)
	}
	if shell != vectors {
		t.Errorf("%s counted %d, want %d — the shell side of the population moved.\n\tdot-blind "+
			"would say %d and unfiltered would say %d, so this is not a rounding disagreement: it "+
			"names which of the three definitions the script is now implementing.",
			script, shell, vectors, dotBlind, unfiltered)
	}
	if shell != len(paths) {
		t.Errorf("%s says %d and testenv.SuitePaths says %d for the same directory.\n\tEvery "+
			"shell-side floor in the repo is applied to the first number and every board is "+
			"computed over the second: they are one population or the floors bound nothing "+
			"the board measures (#340).", script, shell, len(paths))
	}
}

// TestEveryShellSuiteCountGoesThroughOneScript keeps the shell side at *one* definition, which
// is the part of #340 a passing population control cannot hold on its own: two agreeing
// expressions are still two expressions, and the second one is where the next drift lands.
//
// Derived, not enumerated — the domain is every file in the repo that carries shell (`Makefile`,
// the workflows, `scripts/*.sh`), scanned for a `*.wast` glob outside a comment. The one
// admissible exception is derived too rather than listed by line: a glob on a line that also
// runs `ssh` is asking a different question — what the *far side of a copy* holds, vectors and
// sidecars counted separately, which is the poisoning check in `xcheck-amd64.sh` and not the
// population.
func TestEveryShellSuiteCountGoesThroughOneScript(t *testing.T) {
	const counter = "suite-count.sh"

	files := []string{"../../Makefile"}
	for _, pat := range []string{"../../.github/workflows/*.yml", "../../scripts/*.sh"} {
		got, err := filepath.Glob(pat)
		if err != nil {
			t.Fatalf("glob %s: %v", pat, err)
		}
		files = append(files, got...)
	}
	// Vacuity: a scan that found no shell would report perfect compliance.
	if len(files) < 4 {
		t.Fatalf("found %d shell-bearing files (%v), want at least the Makefile, a workflow and "+
			"two scripts — an empty scan agrees with everything", len(files), files)
	}

	callers := 0
	for _, path := range files {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if strings.HasSuffix(path, counter) {
			continue // the definition itself
		}
		for i, line := range strings.Split(string(b), "\n") {
			if strings.Contains(line, counter) {
				callers++
			}
			if trimmed := strings.TrimLeft(line, " \t"); strings.HasPrefix(trimmed, "#") {
				continue // prose quotes the expressions it replaced, at length
			}
			if !strings.Contains(line, "*.wast") || strings.Contains(line, "ssh") {
				continue
			}
			t.Errorf("%s:%d globs *.wast in shell without going through %s:\n\t%s\n\tA POSIX `*` "+
				"skips a leading dot and Go's filepath.Match does not, so a second expression is "+
				"a second population — 257 vectors beside 257 sidecars counts 257 here and 514 in "+
				"Go (#340).", path, i+1, counter, strings.TrimSpace(line))
		}
	}
	// The negative above is vacuous unless somebody is actually calling the script: zero
	// offenders is also what a repo that had deleted every count would report.
	if callers < 4 {
		t.Errorf("only %d line(s) outside %s invoke it, want at least 4 (Makefile, two CI floors, "+
			"the fetch script).\n\tNo offenders and no callers is not compliance, it is a scan "+
			"whose subject left.", callers, counter)
	}
}

// TestNoSkipIsExactlyOne guards against the sloppiest possible way for this to
// break: an env var that is "set to anything" would treat BURROUGHS_NO_SKIP=0 as
// strict mode, and a CI YAML typo like `false` as strict mode too. Harmless in that
// direction — but the same laxity read the other way is what makes `=1` the only
// contract worth documenting.
func TestNoSkipIsExactlyOne(t *testing.T) {
	for _, tc := range []struct{ val, want string }{
		{"1", "on"}, {"", "off"}, {"0", "off"}, {"true", "off"},
	} {
		t.Setenv(testenv.NoSkipEnv, tc.val)
		got := "off"
		if testenv.NoSkip() {
			got = "on"
		}
		if got != tc.want {
			t.Errorf("%s=%q: NoSkip is %s, want %s", testenv.NoSkipEnv, tc.val, got, tc.want)
		}
	}
}

// TestEveryPinsFetchScriptAssertsItsAuthorities pins each fetch script's presence loop to the
// files testenv licenses *from that pin*, because they are two places that know one fact.
//
// The fact is "these are the reference files the project treats as authorities". `refPins`
// holds it in Go; each `fetch-*-ref.sh` holds its own share in a shell `for` list, and cannot
// read the Go one. When decode.ml was the only authority the two agreed trivially; lexer.mll's
// arrival (0009) made the script's check cover a third of its subject with nothing saying
// so, and it stayed that way until parser.mly was the third. That is the narrowing an
// unpoliced duplicate produces — not a wrong assertion, a shrinking one.
//
// So the control is the drift check 0006 calls for, and its direction matters: each *script*
// must cover everything Go licenses from its pin. A file the script checks and Go does not is
// harmless (an extra assertion); a file Go licenses and the script does not is a fetch that
// reports success with an authority missing, which is precisely the early-return defect the
// scripts' own comments record.
//
// # It was `TestEveryPinsFetchScriptAssertsItsAuthorities`, and the old name's grammar was the defect
//
// One script, every authority: singular subject, universal object. That sentence was true
// while there was one pin and it is *false in the direction that produces green* now there
// are two — the core script cannot assert the threads pin's five files (they are not in its
// checkout) and must not be asked to. A control keyed on "the fetch script" would either
// demand the impossible or, written the way the old body was, quietly check the union of both
// pins' files against one script's loop and report the threads authorities as the core
// script's omission. So the loop is per pin: subject and object move together.
//
// The rename is not cosmetic for a second reason. `LicensedRefPaths()` returns the **union**
// across pins, which is the right domain for "is every authority checked somewhere" and the
// wrong one for this test's question, "does *this* script check its own". The old body called
// it; this one walks `RefPins()` and takes each pin's `Floors`. That distinction is written
// out on `LicensedRefPaths` itself, because it is the sort of widening a caller is wrong about
// silently.
//
// # The Makefile arm, which is not this control's original subject and is here for one reason
//
// `RefPin.Target` is quoted to a human by `RequireSpecRef` as `make <target>`, so it is a
// citation into the Makefile with no resolver — *a negative claim is a citation with no
// target* was avoidable here by making the claim positive. A pin whose target is misspelled,
// or whose Makefile entry was never added, yields a skip reason instructing the reader to run
// a fetch that does not exist. Checked as a rule (`^target:`) and as a `.PHONY` member,
// because a directory named `threads-ref` would shadow the rule and make it a silent no-op.
func TestEveryPinsFetchScriptAssertsItsAuthorities(t *testing.T) {
	mk, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	phony := regexp.MustCompile(`(?m)^\.PHONY:(.*)$`).FindAllStringSubmatch(string(mk), -1)

	pins := testenv.RefPins()

	// Vacuity floor over the *pins*, which is the level this control's own subject moved to.
	// A `refPins` drained to empty runs every loop below zero times and reports green — the
	// empty-set agreement, one level up from where the old body checked for it. Two is the
	// count at this revision (ADR 0007's 2026-08-28 amendment) and a floor rather than an
	// equality, so a third pin fails inside the loop where the real assertions are.
	if len(pins) < 2 {
		t.Fatalf("RefPins returned %d pins, want >=2 (the core spec and the threads proposal) "+
			"— a sweep over an empty pin set asserts nothing about any script", len(pins))
	}

	for _, pin := range pins {
		t.Run(pin.Target, func(t *testing.T) {
			b, err := os.ReadFile(filepath.Join("../..", pin.Script))
			if err != nil {
				t.Fatalf("read %s: %v\n\tPin %q names a script that cannot be read; every "+
					"authority it licenses is then unasserted at the fetch layer.",
					pin.Script, err, pin.Target)
			}
			src := string(b)

			// Per-pin vacuity. The pin-set floor above cannot see a pin that licenses nothing,
			// and a pin with an empty `Floors` is exactly what a half-finished addition looks
			// like: it appears in the list, it has a script, and it asserts nothing.
			if len(pin.Floors) == 0 {
				t.Fatalf("pin %q licenses no files — a containment check over an empty set "+
					"agrees with any script, including one with no presence loop at all",
					pin.Target)
			}

			for p := range pin.Floors {
				// The script's paths are relative to `$dest`, testenv's include it. `Dest` is
				// the pin's own, never a literal: trimming `third_party/spec/` off a threads
				// path leaves it unchanged, and the old body would have reported the failure
				// against the wrong script.
				rel := strings.TrimPrefix(p, pin.Dest)
				if rel == p {
					t.Errorf("licensed path %q does not start with %q — the script's loop is "+
						"written relative to $dest and cannot check it", p, pin.Dest)
					continue
				}
				if !strings.Contains(src, rel) {
					t.Errorf("%s does not assert the presence of %q, which testenv licenses as "+
						"an authority of this pin.\n\tA fetch that reports success with an "+
						"authority missing is the precondition excusing the check that polices "+
						"it — add it to the loop.", pin.Script, rel)
				}
			}

			// The pin's revision, read by the *same* reader the generators use. A pin whose
			// `rev=` cannot be found still fetches something; what it stops doing is stamping
			// provenance, and 0007 condition 3's failure mode is a header that looks stamped.
			rev, err := gen.PinnedRev(filepath.Join("../..", pin.Script))
			if err != nil {
				t.Errorf("gen.PinnedRev(%s): %v\n\tEvery pin is written as `rev=\"<40 hex>\"` so "+
					"one reader serves all of them; a renamed field leaves the fetch working and "+
					"the provenance blank.", pin.Script, err)
			} else if len(rev) != 40 {
				t.Errorf("%s pin %q is %d chars, want 40", pin.Script, rev, len(rev))
			}

			// One `rev=` per script, which is the amendment's structural half rather than a
			// style rule: `gen.PinnedRev` returns the **first** match, so a second pin in the
			// same file is a revision nothing can read beside one that answers for it. That is
			// how drift in one pin gets silently absorbed by the other, and it is the reason
			// these are two files (Scott, on the v1 scoping report).
			if n := len(regexp.MustCompile(`(?m)^rev="[0-9a-f]{40}"`).FindAllString(src, -1)); n != 1 {
				t.Errorf("%s declares %d `rev=` pins, want exactly 1.\n\tPinnedRev reads the "+
					"first; a second is unreadable and the pins stop being independently "+
					"dated.", pin.Script, n)
			}

			// The Makefile arm. Two assertions because the instruction can fail two ways: no
			// rule at all, or a rule shadowed by a same-named file or directory.
			if !regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(pin.Target) + `:`).Match(mk) {
				t.Errorf("RequireSpecRef tells a reader to run `make %s` and the Makefile has no "+
					"such rule.\n\tThe skip reason is then an instruction that resolves to "+
					"nothing, which is the weaker error this field exists to replace.", pin.Target)
			}
			declared := false
			for _, m := range phony {
				if slices.Contains(strings.Fields(m[1]), pin.Target) {
					declared = true
				}
			}
			if !declared {
				t.Errorf("`%s` is not in any .PHONY list.\n\tIt names no output file, so a "+
					"directory of that name would make make consider it up to date and the "+
					"fetch silently a no-op.", pin.Target)
			}
			if !strings.Contains(string(mk), pin.Script) {
				t.Errorf("the Makefile never invokes %s, so `make %s` cannot be the remedy "+
					"RequireSpecRef quotes for this pin.", pin.Script, pin.Target)
			}
		})
	}
}

// TestSuitePinIsAssertedByTheFetchScript pins the suite fetch script's two duplicated facts
// to the Go constants that hold them, because they are two places knowing one thing each.
//
// #42 pinned the suite by SHA, and pinning created two duplications the reference pin did not
// have:
//
//   - the **file-count floor**. `testenv.MinSuiteFiles` holds it in Go; the script holds it as
//     `min=250` and cannot read a Go constant. Its purpose there is the vacuity law — a
//     checkout that yields one .wast file passes any `> 0` test while making every board count
//     meaningless — so a floor that drifted *below* the Go one would let a corpus through that
//     Go then treats as absent, which is the skip-is-not-a-verdict hole re-opened at the fetch
//     layer.
//   - the **pin's shape**. `gen.PinnedRev` reads `^rev="<40 hex>"`, and the suite script was
//     written to that shape deliberately so one reader serves both pins. If the script renamed
//     the field, `PinnedRev` would stop finding it — and per *one concept, one trigger* (#82)
//     the failure would be silent in exactly the way a duplicated regexp is.
//
// Direction, as in TestEveryPinsFetchScriptAssertsItsAuthorities above: the *script's* floor must be at
// least Go's. A script floor higher than Go's is harmless (a stricter fetch); lower is a
// corpus the fetch blesses and the tests disown.
func TestSuitePinIsAssertedByTheFetchScript(t *testing.T) {
	const script = "../../scripts/fetch-spec-tests.sh"
	b, err := os.ReadFile(script)
	if err != nil {
		t.Fatalf("read %s: %v", script, err)
	}
	src := string(b)

	// The pin itself, read by the *same* reader the generators use rather than by a second
	// regexp written here — the duplication this control exists to forbid would otherwise be
	// committed by the control forbidding it.
	rev, err := gen.PinnedRev(script)
	if err != nil {
		t.Fatalf("gen.PinnedRev(%s): %v\n\tThe suite pin is written as `rev=\"<40 hex>\"` so "+
			"one reader serves both pins; if the field was renamed, PinnedRev stops finding "+
			"it and every provenance stamp silently loses its subject.", script, err)
	}
	if len(rev) != 40 {
		t.Errorf("suite pin %q is %d chars, want 40", rev, len(rev))
	}

	// The floor. Matched as an assignment so a `min=` inside prose cannot satisfy it.
	m := regexp.MustCompile(`(?m)^min=([0-9]+)`).FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("%s has no `min=<n>` floor assignment.\n\tWithout it the file-count check is "+
			"a presence check, and a one-file checkout passes it (the vacuity law).", script)
	}
	got, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("floor %q is not a number: %v", m[1], err)
	}
	if got < testenv.MinSuiteFiles {
		t.Errorf("%s floors the corpus at %d but testenv.MinSuiteFiles is %d.\n\tA fetch that "+
			"blesses a corpus the tests treat as absent is the skip-is-not-a-verdict hole at "+
			"the fetch layer: the fetch says success, RequireSuite then skips, and the board "+
			"passes by asking nothing.", script, got, testenv.MinSuiteFiles)
	}

	// The exact count for the pin (#340). `files=` was a *comment* on the `rev=` line until this
	// slice, which is a floor's worth of nothing: a floor cannot see a small silent loss and
	// cannot see an addition at all, so every count between 250 and infinity cleared every check
	// in this file. *Reconcile an extent, never floor it.*
	//
	// Distinct from the sidecar poisoning, which this does **not** catch and is not for: sidecars
	// are excluded on both sides now, so the count does not move and nothing downstream sees the
	// junk. Measured, in the script's own comment. What moves this figure is a lossy fetch, a
	// directory that gained a vector, or a pin bump whose population nobody wrote down.
	//
	// Two assertions, because the field can fail in two directions: absent (the reconciliation
	// is gone and the floor is alone again) and *below the floor* (a `files=` under `min=` is a
	// pin recording a corpus the same script would reject, which is two guards disagreeing about
	// their own subject).
	f := regexp.MustCompile(`(?m)^files="?([0-9]+)"?`).FindStringSubmatch(src)
	if f == nil {
		t.Fatalf("%s has no `files=<n>` assignment beside its pin.\n\tThe pinned rev's own vector "+
			"count is what makes the fetch a reconciliation instead of a floor, and a floor is "+
			"blind to a corpus that grew (#340).", script)
	}
	exact, err := strconv.Atoi(f[1])
	if err != nil {
		t.Fatalf("pinned count %q is not a number: %v", f[1], err)
	}
	if exact < got {
		t.Errorf("%s pins %d vectors but floors at %d.\n\tThe fetch would reject the very corpus "+
			"its pin records; the two fields describe one directory and disagree about it.",
			script, exact, got)
	}
	if !strings.Contains(src, `-ne "$files"`) {
		t.Errorf("%s records `files=%d` and never compares a count against it.\n\tAn exact count "+
			"that nothing reconciles is the comment it used to be, wearing an assignment's "+
			"clothes.", script, exact)
	}

	// The post-conditions must not sit behind the already-at-the-right-rev branch. This is
	// fetch-spec-ref.sh's grave (*an early return can skip its own guard*), and a copied
	// script inherits the bug it already paid for unless something says otherwise. Checked
	// structurally: the pin assertion has to appear *after* the if/else closes.
	ifEnd := strings.Index(src, "\nfi\n")
	pinCheck := strings.Index(src, `if [ "$got" != "$rev" ]`)
	switch {
	case ifEnd < 0:
		t.Errorf("%s has no `fi` closing its fetch branch — the structure this checks is gone", script)
	case pinCheck < 0:
		t.Errorf("%s never compares $got to $rev; the pin is a comment, not an assertion", script)
	case pinCheck < ifEnd:
		t.Errorf("%s asserts the pin *inside* its fetch branch (offset %d < %d).\n\tThe "+
			"already-at-the-right-rev path then skips its own post-conditions, so a correct "+
			"SHA with a deleted corpus reports success — fetch-spec-ref.sh's grave, inherited.",
			script, pinCheck, ifEnd)
	}
}

// TestEveryLicensedAuthorityIsReadableAndAboveItsFloor asks the floor question over the
// *derived set* of authorities rather than over the ones some test happens to cite.
//
// A floor in `refFloors` only bites when something calls `RequireSpecRef` on that path, so an
// authority with no caller has a floor that is declared and unenforced — a presence check
// nothing performs, wearing a constant that looks like a control. That was the state
// valid.ml/match.ml arrived in (#291 commit one): licensed in the map and in the fetch script's
// loop, with nothing in Go ever opening them, so a truncated vendor would have satisfied every
// existing test. **Scoped to the space and not to the sample** — it iterates
// `LicensedRefPaths()`, so authority number eight is covered by arriving rather than by someone
// remembering to add a line here. That is the same derivation `TestEveryPinsFetchScriptAssertsItsAuthorities`
// uses one layer out, pointed at enforcement instead of at presence.
//
// It is deliberately *not* an assertion about sizes at a revision: `RequireSpecRef` already
// compares each file against its own floor and says so, and restating a floor here would be the
// second-site duplication `refFloors`'s own comment forbids. All this adds is a caller.
func TestEveryLicensedAuthorityIsReadableAndAboveItsFloor(t *testing.T) {
	licensed := testenv.LicensedRefPaths()

	// Vacuity floor, as in TestEveryPinsFetchScriptAssertsItsAuthorities: a loop over an empty set
	// enforces nothing and reports green. Five is the count before #291 added the sixth and
	// seventh, and a floor rather than an equality so the next authority fails in the loop.
	if len(licensed) < 5 {
		t.Fatalf("LicensedRefPaths returned %d paths, want >=5 — a floor sweep over an empty "+
			"set enforces nothing and passes", len(licensed))
	}

	for _, p := range licensed {
		t.Run(filepath.Base(p), func(t *testing.T) {
			// RequireSpecRef is the licensed door: it resolves the path, skips honestly on a
			// clone with no `make spec-ref` (revoked by BURROUGHS_NO_SKIP=1), and fails on a
			// file below its floor. Reading the bytes here would bypass all three.
			if got := len(testenv.RequireSpecRef(t, p)); got == 0 {
				t.Errorf("%s read as empty through RequireSpecRef, which should have failed on "+
					"its floor first — the floor lookup found a zero, or the file is a stub", p)
			}
		})
	}
}
