// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package testenv_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// revFormChecks pairs each revision-taking script with two strings.
//
// `successPhrase` is what the script prints on a run that actually read a diff, and it is used in
// **both** directions: present on a good revision, absent on a bad one. One string for both is what
// stops the two arms from drifting into testing different things.
//
// `flagRefusal` is the sentence the script prints when handed a flag it does not have, and it is
// empty for two of the three deliberately — they refuse such an argument by passing it to git and
// letting git fail, so the sentence a reader gets is git's. `citecheck.sh` has a bespoke one because
// its two real mis-invocations were both *sibling* forms (`--body` is `closecheck.sh`'s; `--range`
// is nobody's), and a message naming that asymmetry is the only thing its `-*` guard adds over the
// revision check that would otherwise catch the same input.
//
// **That last clause is measured, not assumed, and it is why this field exists.** Deleting the `-*`
// guard left this whole test green: `requirerev` catches a flag too, so the flag arm was asserting a
// property the guard was not the cause of — a guard with no witness, which is exactly the
// under-matching predicate grave #365 was repaired for. The arm asserts the message because the
// message is the guard's entire effect.
//
// The table is not the domain — `revFormScripts` derives that from the scripts themselves and the
// test fails if the two disagree, in either direction. A hand-written domain would inherit today's
// list of siblings, and the point of this control is the sibling written next.
var revFormChecks = map[string]struct{ successPhrase, flagRefusal string }{
	"citecheck.sh":  {successPhrase: "added lines", flagRefusal: "is not a form this script has"},
	"closecheck.sh": {successPhrase: "lines scanned"},
	"ratio.sh":      {successPhrase: "engine "},
}

// revFormScripts derives the domain: every script under `scripts/` that advertises a `<rev>` form
// in its usage string. All three spell that usage differently — one assigns a `usage=` variable,
// two inline it into a `${1:?...}` default — so the predicate is the `<rev>` token inside a string
// containing the word "usage", which is the part all three share and the part a fourth sibling
// would have to write too.
func revFormScripts(t *testing.T, repo string) []string {
	t.Helper()
	ents, err := os.ReadDir(filepath.Join(repo, "scripts"))
	if err != nil {
		t.Fatalf("reading scripts/: %v", err)
	}
	var found []string
	for _, ent := range ents {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".sh") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(repo, "scripts", ent.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", ent.Name(), err)
		}
		for _, line := range strings.Split(string(b), "\n") {
			if strings.Contains(line, "usage") && strings.Contains(line, "<rev>") {
				found = append(found, ent.Name())
				break
			}
		}
	}
	return found
}

// citationFreeRepo builds a throwaway two-commit repository and returns its path.
//
// **A temporary repository rather than this one, and the reason is not isolation.** Run against
// Burroughs, `citecheck.sh <rev>` resolves every `#N` the commit adds against the GitHub API, so a
// good-revision arm here would need the network — and an offline `make check` would then see this
// control fail for a reason that has nothing to do with the property. The two commits below cite
// nothing, so the scanners find zero tokens, make zero calls, and still report having read a diff.
// That is the phrase both arms turn on.
//
// The second commit carries a `Ratio-Class` trailer because `ratio.sh` is one of the three scripts
// under test and an untrailered commit is a different code path in it. Nothing here asserts anything
// about attribution; the trailer is present so the good arm exercises the ordinary case.
func citationFreeRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s in the fixture repo: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	write := func(name, body string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	run("init", "-q", "-b", "main")
	run("config", "user.email", "fixture@example.invalid")
	run("config", "user.name", "fixture")
	// Signing off, in case the developer running this has `commit.gpgsign` on globally: a fixture
	// that cannot commit would fail this control for a reason outside its subject.
	run("config", "commit.gpgsign", "false")

	write("main.go", "package main\n\nfunc main() {}\n")
	run("add", "-A")
	run("commit", "-q", "-m", "fixture: first commit, deliberately citing nothing")

	write("main.go", "package main\n\nfunc main() { println(\"two\") }\n")
	write("sub/sub.go", "package sub\n")
	run("add", "-A")
	run("commit", "-q", "-m", "fixture: second commit, also citation free\n\nRatio-Class: carried")

	return dir
}

// TestABadRevisionIsNeverAPass asserts that no revision-taking checker reports green when the
// revision it was handed does not resolve.
//
// # The grave
//
// Grave #549. Both of `citecheck.sh`'s diff captures read `diffout="$(git diff "$base" $head ||
// true)"`. `git diff` is invoked there without `--exit-code` or `--quiet`, so it returns 0 whether
// or not there are differences — **the `|| true` had no legitimate subject.** What it caught was an
// argument that is not a revision: git wrote `fatal:` to stderr, `|| true` turned 128 into 0,
// `diffout` was empty, and all seven checks ran over nothing and exited **0**, signing off with
// "this diff cites nothing" — a finding about a diff, standing in for a confession that no diff was
// read.
//
// It is grave #365's specimen recurring in the same file, fourteen lines below the comment that
// narrates #365. That repair made the `--pr` fetch its own statement and left the diff arm's
// identical hole standing, which is why this control's domain is *the scripts*, not the arm: a
// per-arm fix is what produced a second grave out of the first one.
//
// Reached twice in one session, both times on real work and both times self-caught by a figure
// being implausible rather than by the tool: `--range origin/main HEAD` reported 0 added lines over
// a 1219-line diff, and `--body <file>` reported 0 over a comment carrying 20 citations. `--range`
// is nobody's form; `--body` is `closecheck.sh`'s and not this script's, and that asymmetry is what
// invites it.
//
// # Two of the three arms pass without the repair, and that is the reason for them
//
// Measured before the fix, one bad argument each: `closecheck.sh` and `ratio.sh` exited 128/129 on
// every form, `citecheck.sh` exited 0 on every form. So the siblings were already correct, and their
// arms here are a regression guard against the `|| true` migrating — which is not hypothetical,
// since it is how #549 came out of #365. The arm that was watched failing is `citecheck.sh`'s.
//
// # The good arm is the vacuity check
//
// A runner that broke every script for an unrelated reason — a bad cwd, a missing fixture — would
// make the bad arm pass while measuring nothing. So each script is also run on a revision that
// *does* resolve and asserted to exit 0 **and** to print its count phrase. Absence of that phrase
// is what the bad arm asserts, so the good arm is what establishes the phrase is reachable at all.
func TestABadRevisionIsNeverAPass(t *testing.T) {
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	scripts := revFormScripts(t, repo)
	// Vacuity first: a derived domain that comes back empty makes every loop below pass by asking
	// nothing, and an empty parse is a clean bill of health from an instrument that read nothing.
	if len(scripts) == 0 {
		t.Fatal("no script under scripts/ advertises a <rev> usage form, so every arm below would " +
			"pass vacuously; the derivation predicate has stopped matching")
	}
	t.Logf("derived domain: %d revision-taking script(s): %s", len(scripts), strings.Join(scripts, " "))
	// Both directions against the phrase table, because a derived domain and a hand-written table
	// disagree silently in whichever direction nobody checked: a new sibling with no phrase would
	// be skipped, and a phrase whose script was renamed would assert nothing.
	for _, s := range scripts {
		if _, ok := revFormChecks[s]; !ok {
			t.Errorf("%s takes a <rev> and has no success phrase in revFormChecks, so it is "+
				"in the domain and out of the test. Add its count phrase rather than removing it "+
				"from the derivation (grave #549)", s)
		}
	}
	for s := range revFormChecks {
		if !slices.Contains(scripts, s) {
			t.Errorf("revFormChecks names %s, which the derivation does not find under "+
				"scripts/ — a renamed or deleted script leaves a table row asserting nothing", s)
		}
	}

	fixture := citationFreeRepo(t)

	// Run a script in the fixture repo and return its combined output and exit code.
	run := func(t *testing.T, script string, args ...string) (string, int) {
		t.Helper()
		cmd := exec.Command("sh", append([]string{filepath.Join(repo, "scripts", script)}, args...)...)
		cmd.Dir = fixture
		out, err := cmd.CombinedOutput()
		code := 0
		if err != nil {
			// `errors.As` for the sibling control's reason, which is load-bearing rather than
			// lint-appeasing: a non-`ExitError` means the process could not be *started*, which is
			// a different fact from a non-zero exit and must not be scored as the verdict this test
			// is looking for.
			var ee *exec.ExitError
			if !errors.As(err, &ee) {
				t.Fatalf("%s could not be run at all (%v), which is not the same fact as a "+
					"non-zero exit and must not be read as one:\n%s", script, err, out)
			}
			code = ee.ExitCode()
		}
		return string(out), code
	}

	for _, script := range scripts {
		want := revFormChecks[script]

		// Two shapes of bad argument. They are **not** independent on the verdict, and saying so is
		// the honest version: a flag is not a revision either, so the revision check refuses both
		// and the flag arm's non-zero exit is not evidence about flag handling. It is kept because
		// it is the input the mistake was actually made with, twice, and because the message it
		// asserts *is* independent — that assertion is the flag guard's only witness.
		for _, bad := range []struct {
			what, arg string
			isFlag    bool
		}{
			{what: "a revision that does not resolve", arg: "no-such-revision-grave-549"},
			{what: "a flag no script has", arg: "--no-such-flag-grave-549", isFlag: true},
		} {
			t.Run(script+" refuses to report green on "+bad.what, func(t *testing.T) {
				out, code := run(t, script, bad.arg)
				if code == 0 {
					t.Errorf("%s %s exited 0, so a mistyped argument reports this check green "+
						"having read nothing — grave #549 exactly. Output:\n%s",
						script, bad.arg, out)
				}
				// The verdict is the load-bearing half and this is the other one: the output must
				// not contain the phrase it prints when it *has* read a diff. A red that still
				// carries a scan summary is what let #549 survive two readings.
				if strings.Contains(out, want.successPhrase) {
					t.Errorf("%s %s printed %q — a summary of a scan that cannot have happened, "+
						"since the revision does not exist. Output:\n%s",
						script, bad.arg, want.successPhrase, out)
				}
				// The flag guard's whole effect is its wording, so the wording is what is asserted.
				// Empty for the siblings, which refuse via git and whose message is git's.
				if bad.isFlag && want.flagRefusal != "" && !strings.Contains(out, want.flagRefusal) {
					t.Errorf("%s %s exits non-zero but does not say %q, so its bespoke flag guard "+
						"is gone and the refusal now comes from the revision check — which gives a "+
						"reader who reached for a sibling's form no hint that that is what they "+
						"did. Output:\n%s", script, bad.arg, want.flagRefusal, out)
				}
			})
		}

		t.Run(script+" still reads a diff on a revision that resolves", func(t *testing.T) {
			out, code := run(t, script, "HEAD")
			if code != 0 {
				t.Fatalf("%s HEAD exited %d in the fixture repo — the runner is breaking the "+
					"script for some reason other than the revision, which would make the arms "+
					"above pass while measuring nothing. Output:\n%s", script, code, out)
			}
			if !strings.Contains(out, want.successPhrase) {
				t.Errorf("%s HEAD passed without printing %q, so the phrase the bad arms assert "+
					"the absence of is not reachable here and their absence proves nothing. "+
					"Output:\n%s", script, want.successPhrase, out)
			}
		})
	}
}
