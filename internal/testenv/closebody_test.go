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

// TestCloseBodyFormScansTheFileAndRefusesToGuess asserts the five outcomes of
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
// Three mutations, each killing exactly its own arm and no other. Removing the empty-file guard makes
// the empty arm report the sentence the form exists to prevent, verbatim: `0 lines scanned, 0 banned
// constructs`. Removing the unreadable-path guard kills only the unreadable arm. So neither refusal
// is riding on the other's condition, which a single combined guard would have made unfalsifiable
// from here. Removing the awk's optional `[` kills the linked arm and **leaves the bare arm green**,
// which is the discrimination grave #595 turned on: a control over one reference form reports on one
// reference form, whatever its name suggests.
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

	// **The banned construct is assembled rather than spelled**, and the reason stated here was
	// borrowed from `TestCitationLookupFailureIsNotAVerdict`'s fixture without checking that it
	// transfers. It does not. That sentence said a literal here "would be a real closing directive in
	// the PR body that ships it", and `closecheck.sh`'s own header says the opposite in as many words:
	// GitHub parses commit messages and pull request bodies, **not the tree**, which is why the script
	// quotes the banned construct verbatim in its first paragraph. The citecheck fixture's reason is
	// load-bearing because *citecheck's domain is files*; closecheck's domain is channels, so a literal
	// in this file could not close anything. Copying inherited the visible property and not the one
	// doing the work.
	//
	// The practice stays, for the reason that survives inspection: a fixture gets quoted — into a
	// commit message, into a PR body — and assembling costs one line and keeps the copy inert. What is
	// corrected is the claim, because a comment asserting a mechanism the tree does not have makes the
	// next reader defend the wrong boundary.
	const kw = "Fix"
	const ref = "#123"

	// The reference forms are one axis of the trigger and the keyword is the other, and grave #595 is
	// what happens when only one of them is ever interrogated: the header argued its *adjacency*
	// boundary in detail, asserted three reference forms were "all three", and a fourth — the
	// reference wrapped in a markdown link — closed #543 on #594's merge while this file reported
	// green over the body that did it.
	//
	// **The wrapped form is the house style, which is why the miss was total rather than partial.**
	// Every reference in every PR body in this repo is a markdown link, because that is the form
	// `citecheck` resolves. So the two arms below are not a symmetry exercise: the bare arm covers the
	// forms GitHub documents, and the linked arm covers the only form this project actually writes.
	//
	// Watched die on the repair's own subject as well as here: with the awk's optional `[` removed,
	// the linked arm fails and the bare arm passes, and `--body` over #594's reconstructed body prints
	// `195 lines scanned, 0 banned constructs` — the exact green that let the specimen through.
	for _, form := range []struct {
		name string
		text string
	}{
		{"bare", kw + "es: " + ref},
		{"wrapped in a markdown link", kw + "es: [" + ref + "](https://example.invalid/" + ref + ")"},
	} {
		t.Run("a reference "+form.name+" is not defused", func(t *testing.T) {
			out, code := run(t, write(t, "body.md", "A line.\n"+form.text+"\n"))
			if code == 0 {
				t.Errorf("--body exited 0 over %q, a closing keyword adjacent to a reference. "+
					"GitHub acts on this and the scanner did not. Output:\n%s", form.text, out)
			}
			// Two assertions on the message, not one, because they fail for different reasons:
			// a red whose only content is shell noise leaves the reader unable to tell a broken
			// instrument from a finding, and a red that names no reference leaves them unable to
			// tell *which* issue is at risk.
			if !strings.Contains(out, "a closing keyword adjacent to a reference") {
				t.Errorf("--body failed over %q without naming the construct, so its red is "+
					"indistinguishable from a broken script. Output:\n%s", form.text, out)
			}
			// The reported reference is normalised to the form GitHub acts on, so both arms name
			// the same one: a report of `[#123` would send a reader grepping for a string the
			// remedy line cannot offer a rephrasing of.
			if !strings.Contains(out, kw+"es "+ref) {
				t.Errorf("--body failed over %q without naming %s, so the caller cannot tell which "+
					"reference is at risk. Output:\n%s", form.text, ref, out)
			}
		})
	}

	t.Run("a clean body passes over a positive line count", func(t *testing.T) {
		// "Landed in #123" is the recommended phrasing the script's own remedy offers, so the
		// clean arm is also a check that the remedy it prints is one the checker accepts.
		//
		// **The second line is the arm that guards the repair from the other side.** The new trigger
		// matches a reference through a markdown link, and the recommended phrasing is *also* written
		// with one here — `Landed in [#123](url)` — so a repair that reached for the bracket instead of
		// the adjacency would fail the exact sentence the script tells the author to write. That is the
		// direction this file's header refuses to guess in: the wrong way to over-match is the one that
		// fails correct prose and teaches the writer to phrase around the instrument.
		out, code := run(t, write(t, "body.md", "A line.\nLanded in "+ref+", see also GH-7.\n"+
			"Landed in ["+ref+"](https://example.invalid/"+ref+"), and see ["+ref+"]("+
			"https://example.invalid/"+ref+") for why.\n"))
		if code != 0 {
			t.Fatalf("--body exited %d over a body whose only reference is the phrasing the "+
				"script itself recommends. Output:\n%s", code, out)
		}
		if !strings.Contains(out, "3 lines scanned") {
			t.Errorf("--body passed without reporting the 3 lines it was given, so this arm does "+
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
