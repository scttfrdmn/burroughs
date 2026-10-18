// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package testenv_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestTriageCheckFlagsAMapRowWhoseIssueHasClosed is the control for `scripts/triagecheck.sh`, which the
// chair's order of 2026-10-17 asked for when the Windows triage map moved into the tree as data
// (`.github/windows-triage.tsv`): any row whose issue has closed is flagged.
//
// The tracker is replaced by a `gh` shim on PATH, as in `TestPRFetchFailureIsNeverAPass` and for its
// reasons: it is deterministic, and it keeps the binary present, which is the case a presence guard
// cannot see. The shim answers `closed` for one issue number and `open` for every other.
//
// **The pass arm is what makes the fail arms mean anything.** The same shim with no closed number must
// pass and report the row count, so a fail arm cannot be passing because the shim broke the script for
// some other reason. The committed map is run through that arm too, so its rows are parsed by the
// script on every `make test` and not only by CI's live call.
//
// **Neuter, run:** a script that skips the `open` comparison fails the closed-issue arm and nothing else.
func TestTriageCheckFlagsAMapRowWhoseIssueHasClosed(t *testing.T) {
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	run := func(t *testing.T, shim, mapPath string) (string, int) {
		t.Helper()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(shim), 0o755); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("sh", filepath.Join(repo, "scripts", "triagecheck.sh"), mapPath)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
		out, err := cmd.CombinedOutput()
		code := 0
		if err != nil {
			var ee *exec.ExitError
			if !errors.As(err, &ee) {
				t.Fatalf("triagecheck.sh could not be run at all (%v), which is not a non-zero exit:\n%s", err, out)
			}
			code = ee.ExitCode()
		}
		return string(out), code
	}
	// closedShim answers `closed` for the issue number n and `open` for every other. The number is
	// matched as the last path element of the `issues/N` argument, so 7 does not match 70.
	closedShim := func(n string) string {
		return "#!/bin/sh\nfor a in \"$@\"; do case $a in */issues/" + n + ") echo closed; exit 0;; esac; done\necho open\n"
	}
	const allOpen = "#!/bin/sh\necho open\n"
	const fetchFails = "#!/bin/sh\necho 'shim: forced transport failure' >&2\nexit 1\n"

	fixture := func(t *testing.T, body string) string {
		t.Helper()
		p := filepath.Join(t.TempDir(), "triage.tsv")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	const twoRows = "# a comment row\nTestAlpha\t70\tfirst\nTestBeta\t71,7\tsecond\n"

	t.Run("all_open_passes_and_counts", func(t *testing.T) {
		out, code := run(t, allOpen, fixture(t, twoRows))
		if code != 0 || !strings.Contains(out, "2 row(s)") || !strings.Contains(out, "3 issue(s) asked, all open") {
			t.Fatalf("exit %d; want 0 with 2 rows and 3 issues asked, all open:\n%s", code, out)
		}
	})

	t.Run("a_closed_issue_fails_naming_its_row", func(t *testing.T) {
		out, code := run(t, closedShim("7"), fixture(t, twoRows))
		if code == 0 {
			t.Fatalf("exit 0 with issue 7 closed, so the map can send a failure to a closed issue unseen:\n%s", out)
		}
		if !strings.Contains(out, "issue 7 is closed") || !strings.Contains(out, "TestBeta") {
			t.Errorf("the failure does not name the closed issue and the row that cites it:\n%s", out)
		}
		if strings.Contains(out, "TestAlpha") {
			t.Errorf("the failure names TestAlpha, whose issue is open (the shim's 7 must not match 70):\n%s", out)
		}
	})

	t.Run("a_failed_fetch_is_not_a_pass", func(t *testing.T) {
		out, code := run(t, fetchFails, fixture(t, twoRows))
		if code == 0 || !strings.Contains(out, "could not be fetched") || strings.Contains(out, "all open") {
			t.Fatalf("exit %d; want non-zero, saying the state could not be fetched, with no all-open line:\n%s",
				code, out)
		}
	})

	for name, body := range map[string]string{
		"a_malformed_row_fails": "TestAlpha 70 spaces, not tabs\n",
		"a_hashed_issue_fails":  "TestAlpha\t#70\tthe hash is not part of the number\n",
		"a_repeated_name_fails": "TestAlpha\t70\tone\nTestAlpha\t71\ttwo\n",
		"an_empty_map_fails":    "# only a comment\n",
	} {
		t.Run(name, func(t *testing.T) {
			out, code := run(t, allOpen, fixture(t, body))
			if code == 0 {
				t.Fatalf("exit 0 on a map that should be refused:\n%s", out)
			}
		})
	}

	t.Run("the_committed_map_parses", func(t *testing.T) {
		const committed = ".github/windows-triage.tsv"
		raw, err := os.ReadFile(filepath.Join(repo, committed))
		if err != nil {
			t.Fatalf("the committed map is missing: %v", err)
		}
		rows := 0
		for line := range strings.SplitSeq(string(raw), "\n") {
			if line != "" && !strings.HasPrefix(line, "#") {
				rows++
			}
		}
		out, code := run(t, allOpen, committed)
		if code != 0 || !strings.Contains(out, strconv.Itoa(rows)+" row(s)") {
			t.Fatalf("exit %d; want 0 with %d rows read from %s:\n%s", code, rows, committed, out)
		}
	})
}
