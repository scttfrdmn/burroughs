// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package testenv_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// prFetchScripts are the two checkers with a `--pr` arm — the arm that fetches a PR's title and
// body from GitHub rather than reading git. Both are run by CI's `citations` job on the
// `pull_request` event, which is where each script's own header says its PR-body half is binding.
var prFetchScripts = []string{"citecheck.sh", "closecheck.sh"}

// TestPRFetchFailureIsNeverAPass asserts that neither checker reports green when it could not read
// the body it was asked to check.
//
// # The grave
//
// Grave #365. `citecheck.sh`'s fetch was written as `diffout="$(gh pr view … | sed 's/^/+/')"`, and **a
// pipeline's exit status belongs to whatever ran last** — `sed`, which succeeds on empty input. So
// `set -eu` never saw a failing `gh`: an API rate limit produced an empty body, the citation scanner
// found nothing in it, and the script exited **0** printing "self-citation check ran against PR #N,
// over 0 prose line(s)" followed by "this diff cites nothing". Both sentences are true of an empty
// string and neither is true of the PR. A positive claim that the check ran, and a benign-sounding
// finding about a diff, standing in for a confession that nothing was read.
//
// It was found by the figure being suspiciously clean: 0 citation-shaped tokens in a PR body that
// cites a dozen issues. The verdict channel said pass and the mechanism channel — `gh`'s own
// "rate limit already exceeded" on stderr, one line above — said the question was never asked.
//
// **The guard that was already there tested the wrong thing.** Both scripts checked
// `command -v gh`, in identical words, and refused to report green when the binary was missing. A
// missing `gh` is the *least* likely way this fetch fails; a rate limit, an unauthenticated gh, and
// a PR number that does not resolve are all far likelier, and all three leave the binary present.
// An under-matching trigger predicate fails silently by construction, which is why this control
// tests the *behaviour on a failing call* rather than the presence of a guard.
//
// # Why a shim rather than a real call
//
// The failure is driven by a `gh` on PATH that exits non-zero, for two reasons. It is
// deterministic, where provoking a real rate limit is not; and it holds the binary *present*, which
// is precisely the case the old guard could not see. A grep for the fixed line would be the other
// available control and a worse one — it would assert the text of a fix rather than the property
// the fix is for, and it would pass against any future rewrite that reintroduced the pipeline
// under a different spelling.
//
// # The success arm is not decoration
//
// A shim that broke either script for an unrelated reason — wrong flags, an unparseable answer —
// would make the failure arm pass while proving nothing. So the same shim mechanism is run with a
// `gh` that *succeeds*, and each script is asserted to exit 0 **and** to report a positive count of
// lines scanned. That is what establishes the shim actually drives the scripts, and it is the
// vacuity check this comparison would otherwise be missing: an assertion that a script fails is
// worth nothing until the same setup is shown to make it pass.
func TestPRFetchFailureIsNeverAPass(t *testing.T) {
	// `../..` is how this package's siblings reach the repo root; made absolute because the shim
	// is invoked with `cmd.Dir` set and a relative script path would then resolve twice.
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	// Two shims, both named `gh` so `command -v gh` finds them: one that fails the way a rate
	// limit does, one that answers with a two-line body citing nothing. The body deliberately
	// contains no `#N` token, so `citecheck.sh`'s success arm resolves no issues and needs no
	// second network call — the arm is about whether the script ran, not about resolution.
	const failShim = "#!/bin/sh\necho 'shim: forced fetch failure' >&2\nexit 1\n"
	const okShim = "#!/bin/sh\nprintf 'A title with no citations in it.\\nA body line, likewise.\\n'\n"

	withShim := func(t *testing.T, body, script string) (string, int) {
		t.Helper()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("sh", filepath.Join(repo, "scripts", script), "--pr", "1")
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
		out, err := cmd.CombinedOutput()
		code := 0
		if err != nil {
			// `errors.As` rather than a type assertion, and the distinction is load-bearing here
			// rather than lint-appeasing: a non-`ExitError` means the process could not be
			// *started* — a missing `sh`, an unreadable script — which is a different fact from a
			// non-zero exit and must not be scored as the failure verdict this test is looking for.
			var ee *exec.ExitError
			if !errors.As(err, &ee) {
				t.Fatalf("%s could not be run at all (%v), which is not the same fact as a "+
					"non-zero exit and must not be read as one:\n%s", script, err, out)
			}
			code = ee.ExitCode()
		}
		return string(out), code
	}

	for _, script := range prFetchScripts {
		t.Run(script+" refuses to report green when the fetch fails", func(t *testing.T) {
			out, code := withShim(t, failShim, script)
			if code == 0 {
				t.Errorf("%s --pr exited 0 with a gh that cannot answer, so a rate limit in CI "+
					"reports this check green having read nothing. Output:\n%s", script, out)
			}
			// The verdict is the load-bearing half; the message is the other one. A red job whose
			// only explanation is gh's own stderr leaves the reader knowing the check failed and
			// not whether the PR is at fault, which is the distinction between a broken instrument
			// and a finding.
			if !strings.Contains(out, "could not be fetched") {
				t.Errorf("%s --pr exits non-zero on a failing fetch but does not say the body "+
					"could not be fetched, so its red is indistinguishable from a real "+
					"violation. Output:\n%s", script, out)
			}
			// The specific sentence the grave produced: a claim that the check ran, printed over
			// an empty body. Asserted by absence because that is the shape the failure took, and
			// a reader of a red log must not find a green-sounding summary line inside it.
			for _, claim := range []string{"lines scanned", "check ran against", "cites nothing"} {
				if strings.Contains(out, claim) {
					t.Errorf("%s --pr printed %q on a failed fetch — a summary of a scan that "+
						"did not happen. Output:\n%s", script, claim, out)
				}
			}
		})

		t.Run(script+" still passes when the fetch succeeds", func(t *testing.T) {
			out, code := withShim(t, okShim, script)
			if code != 0 {
				t.Fatalf("%s --pr exited %d with a gh that answers cleanly — the shim is breaking "+
					"the script for some reason other than the fetch, which would make the "+
					"failure arm above pass while measuring nothing. Output:\n%s",
					script, code, out)
			}
			// Vacuity: a script that exits 0 having scanned zero lines is the grave wearing a
			// green. The shim answers with two lines, so the reported count must be positive —
			// and "2 " is asserted rather than merely "not 0", since both scripts print the count
			// immediately before the word they use for a line.
			if !strings.Contains(out, "2 added lines") && !strings.Contains(out, "2 lines scanned") {
				t.Errorf("%s --pr passed but does not report having scanned the shim's 2 lines, "+
					"so this arm does not establish that the body reached the scanner. "+
					"Output:\n%s", script, out)
			}
		})
	}
}

// TestCitationLookupFailureIsNotAVerdict asserts that `citecheck.sh` tells a citation the tracker
// answered "no" about apart from a citation the tracker was never asked about.
//
// # The grave
//
// #410. The per-citation resolver was `gh api … 2>/dev/null` with *any* nonzero exit becoming
// `#N -> does not resolve: no such issue or PR in this repo`. That sentence is a claim about the
// tracker, and the exit code under it cannot distinguish "the answer is no" from "the question was
// never asked" — discarding stderr is what made the two indistinguishable, since the one channel
// carrying the reason went on the floor.
//
// Observed on a `--pr 409` run: `FAIL  #180 -> does not resolve`, with #180 present and correctly
// labelled when the identical request was made out of band, at 4931/5000 rate limit. So it was
// neither absence nor throttling — one call in ~18 failed for a transport reason.
//
// **The cost is a named wrong remedy, not a confusing message.** The output offers two actions, and
// one of them is "file the missing artifact and cite what comes back" — which mints a duplicate of an
// issue that already exists. On a red CI the line reads as drift in the tree, so the search starts in
// the tree and never reaches the network.
//
// # Why two shims and not one
//
// The two branches are asserted **separately**, which is the whole reason this control costs two
// fixtures. A single `gh` that failed both ways would let either arm's wording cover the other's, and
// the defect being fixed is precisely that one wording covered both cases. So: one shim whose stderr
// carries `HTTP 404` (the verdict arm, which keeps the original wording) and one whose stderr carries
// a transport error (the mechanism arm, which must not claim anything about whether #N exists).
//
// Both are asserted to exit non-zero. A mechanism failure is not a pass either — that is
// `scripts/citecheck.sh:620`'s own rule for the whole phase, and #410 is that rule reaching one
// citation. The number was 479, written without a file and stale before the PR that wrote it merged (grave #418); the
// file name is spelled here because a bare `:NNN` in a *different* file names nothing a reader can
// resolve, which is the half of #418 that needs no convention ruling.
//
// # closecheck.sh is confirmed here rather than changed
//
// #410 named `closecheck.sh --pr`'s single `gh pr view` as worth checking rather than assuming. It was
// already right: its failure arm says the body "could not be fetched" and lists a nonexistent PR as
// one of three *candidate* causes instead of asserting one. So it gets no edit and no arm here beyond
// the fetch-failure coverage `TestPRFetchFailureIsNeverAPass` already gives it — a confirmation whose
// result was "nothing owed" is still a result, and recording it is what keeps the next reader from
// paying for the same look.
func TestCitationLookupFailureIsNotAVerdict(t *testing.T) {
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	// **The fixture's number is assembled at run time and never written as `#` plus digits, and this
	// file is the reason citecheck's header warns about it.** The first draft spelled the literal, and
	// citecheck's own diff scan resolved it and reported `does not resolve: no such issue or PR in this
	// repo` — correctly. The hazard is not the checker's source file; it is *any* file the diff
	// touches, and a test whose whole subject is an unresolvable citation is the sharpest case of it:
	// the fixture has to be citation-shaped to the extractor and must not be citation-shaped to the
	// grep over the diff that adds it. Concatenation is the whole trick, and it is spelled out here
	// because the obvious tidy-up is to inline it.
	const unresolvable = "9876"

	// The shim answers `gh pr view` with a body citing one issue and fails `gh api` — so the fetch
	// half succeeds and the resolver half is what breaks, which is the seam #410 is about. A shim
	// that failed both would be re-testing TestPRFetchFailureIsNeverAPass.
	shim := func(apiStderr string) string {
		return "#!/bin/sh\n" +
			"case \"$1\" in\n" +
			"api) echo '" + apiStderr + "' >&2; exit 1 ;;\n" +
			"*) printf 'A title.\\nA body line citing #" + unresolvable + ".\\n' ;;\n" +
			"esac\n"
	}

	for _, tc := range []struct {
		name     string
		stderr   string
		wantSub  string
		denySubs []string
	}{
		{
			name:    "a 404 is the tracker's answer and keeps the verdict's wording",
			stderr:  "gh: Not Found (HTTP 404)",
			wantSub: "does not resolve: no such issue or PR in this repo",
		},
		{
			name:    "a transport failure says the question was never asked",
			stderr:  "error connecting to api.github.com: dial tcp: lookup failed",
			wantSub: "could not resolve #" + unresolvable + " -- the request failed",
			// The verdict's wording and its remedy, both asserted absent. The remedy is the half
			// that costs something: acting on "file a replacement artifact" when the number was
			// fine is how a duplicate gets minted.
			denySubs: []string{"does not resolve: no such issue", "gh issue create"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(shim(tc.stderr)), 0o755); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("sh", filepath.Join(repo, "scripts", "citecheck.sh"), "--pr", "1")
			cmd.Dir = repo
			cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("citecheck.sh --pr exited 0 with a citation it could not resolve, so a "+
					"citation nobody checked reports green. Output:\n%s", out)
			}
			var ee *exec.ExitError
			if !errors.As(err, &ee) {
				t.Fatalf("citecheck.sh could not be run at all (%v), which is not the same fact "+
					"as a non-zero exit:\n%s", err, out)
			}
			if !strings.Contains(string(out), tc.wantSub) {
				t.Errorf("citecheck.sh did not print %q for a lookup that failed with %q, so the "+
					"two failure causes are reported in the same words — which is the grave. "+
					"Output:\n%s", tc.wantSub, tc.stderr, out)
			}
			for _, deny := range tc.denySubs {
				if strings.Contains(string(out), deny) {
					t.Errorf("citecheck.sh printed %q for a lookup that never reached the "+
						"tracker: a claim, or a remedy, resting on an answer it did not get. "+
						"Output:\n%s", deny, out)
				}
			}
			// **Grave #416, and this is the arm that found it.** The script's header claims "the
			// total counts what the extractor matched, and each category says what it was", and the
			// two category counters used to be incremented inside the *resolution* arms — so a
			// citation that failed to resolve was counted in the total and in no category. The
			// identity held on every green run and broke exactly when something was wrong, which is
			// why the fixture that exposed it is this one: one citation, deliberately unresolvable.
			if got, sum := summaryCounts(t, string(out)); got != sum {
				t.Errorf("citecheck.sh reported %d citation-shaped tokens over categories summing "+
					"to %d — the breakdown drops what the extractor matched, so a reader cannot "+
					"tell a miscount from a failed lookup. Output:\n%s", got, sum, out)
			}
		})
	}
}

// summaryCounts reads `citecheck.sh`'s last summary line and returns its total beside the sum of its
// categories, so a caller can assert the two agree.
//
// Parsed from the printed line rather than recomputed, because the claim under test is about *that
// line* — a second count derived some other way would agree with itself and say nothing about what a
// reader of the log is told. The regexp names all five categories rather than summing whatever
// numbers appear, so a sixth category added later fails to match and is reported as unparseable
// instead of being silently left out of the sum.
func summaryCounts(t *testing.T, out string) (total, sum int) {
	t.Helper()
	re := regexp.MustCompile(`(\d+) citation-shaped tokens \((\d+) issue, (\d+) grave, (\d+) ADR, ` +
		`(\d+) qualified, (\d+) verb\)`)
	m := re.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("citecheck.sh printed no summary line this helper could read, so the identity "+
			"below was never checked — a skip is not a verdict. Output:\n%s", out)
	}
	n := make([]int, len(m)-1)
	for i := range n {
		v, err := strconv.Atoi(m[i+1])
		if err != nil {
			t.Fatalf("unreadable count %q in %q: %v", m[i+1], m[0], err)
		}
		n[i] = v
	}
	for _, v := range n[1:] {
		sum += v
	}
	return n[0], sum
}
