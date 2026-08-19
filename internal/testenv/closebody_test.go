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

// TestCloseBodyFormScansTheFileAndRefusesToGuess asserts the four outcomes of
// `closecheck.sh --body <file>` — the form Scott ordered so a PR body can be scanned *before* the
// push, since `--pr` needs a number that does not exist yet and `make check` therefore cannot mirror
// CI on the body axis.
//
// # Why the two refusal arms are the point
//
// The success and failure arms are the obvious pair, and on their own they would be a weaker control
// than this form deserves. The gap this form closes was itself a green over the wrong population: at
// #398 the PR body opened with a banned construct, `make close` reported green over the *commit*
// half, and the green was read as the sweep's verdict. So a form whose whole purpose is "scan the
// body" must not report green when it did not read a body, and there are exactly two ways that
// happens locally — an unreadable path and an empty file. The second is the likelier: a redirect that
// went somewhere else leaves a perfectly readable file with nothing in it, and an empty file scans
// clean and truthfully reports zero constructs, which is [grave #365]'s shape in a different tool.
//
// # A skip is not a verdict, and neither is a shape
//
// Each arm asserts the exit status **and** a distinguishing sentence, for `prfetchScripts`' reason:
// a red whose only content is a stack of shell noise leaves the reader unable to tell a broken
// instrument from a finding. The clean arm additionally asserts a positive line count, because
// "0 lines scanned, 0 banned constructs" is the failure mode wearing a pass — the same vacuity check
// the `--pr` success arm carries.
//
// # Watched die, one arm at a time
//
// Two mutations, each killing exactly its own arm and no other. Removing the empty-file guard makes
// the empty arm report the sentence the form exists to prevent, verbatim: `0 lines scanned, 0 banned
// constructs`. Removing the unreadable-path guard kills only the unreadable arm. So neither refusal
// is riding on the other's condition, which a single combined guard would have made unfalsifiable
// from here.
//
// [grave #365]: https://github.com/scttfrdmn/burroughs/issues/365
func TestCloseBodyFormScansTheFileAndRefusesToGuess(t *testing.T) {
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	run := func(t *testing.T, arg string) (string, int) {
		t.Helper()
		cmd := exec.Command("sh", filepath.Join(repo, "scripts", "closecheck.sh"), "--body", arg)
		cmd.Dir = repo
		out, err := cmd.CombinedOutput()
		code := 0
		if err != nil {
			// `errors.As` for the distinction `withShim` above spells out: a process that could
			// not start is a different fact from a non-zero exit.
			var ee *exec.ExitError
			if !errors.As(err, &ee) {
				t.Fatalf("closecheck.sh could not be run at all (%v), which is not the same fact "+
					"as a non-zero exit:\n%s", err, out)
			}
			code = ee.ExitCode()
		}
		return string(out), code
	}

	write := func(t *testing.T, name, body string) string {
		t.Helper()
		p := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	// **The banned construct is assembled rather than spelled**, for the reason
	// `TestCitationLookupFailureIsNotAVerdict`'s fixture is: this file is in the diff that adds it,
	// and the scanner reads tokens rather than quotation marks. A literal here would be a real
	// closing directive in the PR body that ships it — the ban reported in the banned form.
	const kw = "Fix"
	const ref = "#123"

	t.Run("a body carrying the construct fails and names it", func(t *testing.T) {
		out, code := run(t, write(t, "body.md", "A line.\n"+kw+"es: "+ref+"\n"))
		if code == 0 {
			t.Errorf("--body exited 0 over a body with a closing keyword adjacent to a "+
				"reference, so the form cannot do the job it was added for. Output:\n%s", out)
		}
		if !strings.Contains(out, "a closing keyword adjacent to a reference") {
			t.Errorf("--body failed without naming the construct, so its red is "+
				"indistinguishable from a broken script. Output:\n%s", out)
		}
	})

	t.Run("a clean body passes over a positive line count", func(t *testing.T) {
		// "Landed in #123" is the recommended phrasing the script's own remedy offers, so the
		// clean arm is also a check that the remedy it prints is one the checker accepts.
		out, code := run(t, write(t, "body.md", "A line.\nLanded in "+ref+", see also GH-7.\n"))
		if code != 0 {
			t.Fatalf("--body exited %d over a body whose only reference is the phrasing the "+
				"script itself recommends. Output:\n%s", code, out)
		}
		if !strings.Contains(out, "2 lines scanned") {
			t.Errorf("--body passed without reporting the 2 lines it was given, so this arm does "+
				"not establish the file reached the scanner — and a scan of nothing is clean. "+
				"Output:\n%s", out)
		}
	})

	t.Run("an unreadable path is not a pass", func(t *testing.T) {
		out, code := run(t, filepath.Join(t.TempDir(), "absent.md"))
		if code == 0 {
			t.Errorf("--body exited 0 for a file it could not read. Output:\n%s", out)
		}
		if !strings.Contains(out, "could not be read") {
			t.Errorf("--body failed on an unreadable path without saying so. Output:\n%s", out)
		}
	})

	t.Run("an empty file is not a pass", func(t *testing.T) {
		out, code := run(t, write(t, "body.md", ""))
		if code == 0 {
			t.Errorf("--body exited 0 over an empty file, which is a clean scan of nothing — the "+
				"exact shape of the green this form exists to prevent. Output:\n%s", out)
		}
		if !strings.Contains(out, "is empty") {
			t.Errorf("--body failed on an empty file without saying it was empty, so the caller "+
				"cannot tell it from a real finding. Output:\n%s", out)
		}
	})
}
