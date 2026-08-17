// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package testenv_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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
