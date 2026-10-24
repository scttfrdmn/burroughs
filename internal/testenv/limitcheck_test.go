// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package testenv

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestLimitCheckRefusesAClosedIssueCitedAsAKnownLimit is the control for `scripts/limitcheck.py` (the chair's
// second ruling of 2026-10-24). `v0.8.0` and `v0.9.0` listed #830 as an open known limit after `v0.7.0` had fixed it.
// The check reads the newest version block's **Known limits** paragraph, and every issue it cites must be open.
//
// The tracker is a `gh` shim on PATH, as in `TestTriageCheckFlagsAMapRowWhoseIssueHasClosed`. It answers `closed`
// for the numbers in CLOSED and `open` for the rest, names a repository, and lists merged PR 9 as naming issue 1.
//
// NEUTER, run: `main` without the `st != "open"` arm passes the closed-issue arm.
func TestLimitCheckRefusesAClosedIssueCitedAsAKnownLimit(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "scripts", "limitcheck.py")
	const shim = `#!/bin/sh
if [ "$1" = "repo" ]; then echo owner/repo; exit 0; fi
if [ -n "$GH_FAIL" ]; then echo "HTTP 502" >&2; exit 1; fi
case "$*" in
*search/issues*) case "$*" in *"#1"*) echo 9 ;; esac; exit 0 ;;
esac
n=$(printf '%s' "$2" | sed 's/.*issues\///')
for c in $CLOSED; do [ "$c" = "$n" ] && { echo closed; exit 0; }; done
echo open
`
	cut := "# Changelog\n\n## [Unreleased]\n*Implements contract v0.1.*\n\n## [1.0.0] - 2026-10-24\n\n" +
		"**Known limits, open on default-on paths:**\n- one ([#1](https://x/issues/1))\n- two (#2)\n\n" +
		"**Then the rest.** It cites #3, which is not a limit.\n\n### Changed\n- a change (#4)\n"
	notCut := strings.Replace(cut, "*Implements contract v0.1.*\n", "*Implements contract v0.1.*\n\n### Changed\n- x\n", 1)
	noLimits := strings.Replace(cut, "**Known limits, open on default-on paths:**", "**Other things:**", 1)

	run := func(t *testing.T, changelog string, env ...string) (int, string) {
		t.Helper()
		dir := t.TempDir()
		if werr := os.WriteFile(filepath.Join(dir, "gh"), []byte(shim), 0o755); werr != nil {
			t.Fatal(werr)
		}
		cl := filepath.Join(dir, "CHANGELOG.md")
		if werr := os.WriteFile(cl, []byte(changelog), 0o600); werr != nil {
			t.Fatal(werr)
		}
		cmd := exec.Command("python3", script, cl)
		cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
		cmd.Env = append(cmd.Env, env...)
		out, rerr := cmd.CombinedOutput()
		code := 0
		var ee *exec.ExitError
		if errors.As(rerr, &ee) {
			code = ee.ExitCode()
		} else if rerr != nil {
			t.Fatalf("running limitcheck: %v\n%s", rerr, out)
		}
		return code, string(out)
	}

	for _, a := range []struct {
		name, changelog string
		env             []string
		code            int
		want            string
	}{
		{"every_cited_limit_open_passes", cut, []string{"CLOSED="}, 0, "2 known limit issue(s) cited, all open"},
		{"a_closed_issue_cited_as_a_limit_fails", cut, []string{"CLOSED=2"}, 1, "#2 is closed"},
		{"an_unreadable_tracker_fails", cut, []string{"GH_FAIL=1"}, 1, "could not be read"},
		{"no_known_limits_paragraph_fails", noLimits, []string{"CLOSED="}, 1, "no **Known limits** paragraph"},
		{"an_issue_outside_the_limits_paragraph_is_not_checked", cut, []string{"CLOSED=3 4"}, 0, "all open"},
		{"no_cut_in_progress_checks_nothing", notCut, []string{"CLOSED=2"}, 0, "nothing checked"},
		{"a_merged_pr_naming_a_limit_is_noted", cut, []string{"CLOSED="}, 0, "note  #1: merged PR #9"},
	} {
		t.Run(a.name, func(t *testing.T) {
			code, out := run(t, a.changelog, a.env...)
			if code != a.code {
				t.Fatalf("exit %d, want %d\n%s", code, a.code, out)
			}
			if !strings.Contains(out, a.want) {
				t.Errorf("output does not contain %q:\n%s", a.want, out)
			}
		})
	}
}
