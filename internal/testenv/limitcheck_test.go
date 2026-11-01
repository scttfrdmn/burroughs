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
// The check reads the **Known limits** paragraph of the list being written, and every issue it cites must be open.
// That list is `[Unreleased]`'s when it has entries, and the newest version block's otherwise (the chair's ruling of
// 2026-11-01, item 3: a released block is a record, so its FAIL is noise).
//
// The tracker is a `gh` shim reached through `LIMITCHECK_GH`. It answers `closed`
// for the numbers in CLOSED and `open` for the rest, names a repository, and lists merged PR 9 as naming issue 1.
//
// NEUTERS, run:
//   - `main` without the `st != "open"` arm passes the closed-issue arm;
//   - `list_being_written` with its `[Unreleased]` arm disabled fails exactly the three `--force` rows that read
//     `[Unreleased]`, and on the real CHANGELOG the 0.12.0 block's #1120 FAIL comes back (2026-11-01).
func TestLimitCheckRefusesAClosedIssueCitedAsAKnownLimit(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "scripts", "limitcheck.py")
	// **In Python, and reached through `LIMITCHECK_GH` rather than PATH.** The first shim was a `gh` shell script on
	// PATH, which `subprocess` cannot execute on Windows: every arm there read as an unreadable tracker, so the
	// fail-closed path answered for a shim that never ran (PR #1095's first Windows run).
	const shim = `import os, sys
a = sys.argv[1:]
if a[0] == "repo":
    print("owner/repo"); sys.exit(0)
if os.environ.get("GH_FAIL"):
    print("HTTP 502", file=sys.stderr); sys.exit(1)
if "search/issues" in a:
    if any("#1" in x for x in a):
        print(9)
    sys.exit(0)
n = a[1].rsplit("issues/", 1)[1]
print("closed" if n in os.environ.get("CLOSED", "").split() else "open")
`
	cut := "# Changelog\n\n## [Unreleased]\n*Implements contract v0.1.*\n\n## [1.0.0] - 2026-10-24\n\n" +
		"**Known limits, open on default-on paths:**\n- one ([#1](https://x/issues/1))\n- two (#2)\n\n" +
		"**Then the rest.** It cites #3, which is not a limit.\n\n### Changed\n- a change (#4)\n"
	notCut := strings.Replace(cut, "*Implements contract v0.1.*\n", "*Implements contract v0.1.*\n\n### Changed\n- x\n", 1)
	noLimits := strings.Replace(cut, "**Known limits, open on default-on paths:**", "**Other things:**", 1)
	// Between cuts: [Unreleased] has entries and its own Known limits (#5), above a released block whose list
	// cites #2. With --force, the list being written is [Unreleased]'s, so #2's state is not this check's subject:
	// that is 0.12.0 citing #1120 after #1120 closed (the chair's ruling of 2026-11-01, item 3).
	inProgress := strings.Replace(cut, "*Implements contract v0.1.*\n",
		"*Implements contract v0.1.*\n\n**Known limits, open on default-on paths:**\n- five (#5)\n\n### Changed\n- x\n", 1)
	inProgressNoLimits := strings.Replace(cut, "*Implements contract v0.1.*\n",
		"*Implements contract v0.1.*\n\n### Changed\n- x\n", 1)

	run := func(t *testing.T, changelog string, args []string, env ...string) (int, string) {
		t.Helper()
		dir := t.TempDir()
		gh := filepath.Join(dir, "gh.py")
		if werr := os.WriteFile(gh, []byte(shim), 0o600); werr != nil {
			t.Fatal(werr)
		}
		cl := filepath.Join(dir, "CHANGELOG.md")
		if werr := os.WriteFile(cl, []byte(changelog), 0o600); werr != nil {
			t.Fatal(werr)
		}
		cmd := exec.Command("python3", append(append([]string{script}, args...), cl)...)
		// Slashes, and single-quoted, because the script splits this with `shlex`, which reads a backslash as an
		// escape.
		cmd.Env = append(os.Environ(), "LIMITCHECK_GH=python3 '"+filepath.ToSlash(gh)+"'")
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

	force := []string{"--force"}
	for _, a := range []struct {
		name, changelog string
		args            []string
		env             []string
		code            int
		want            string
	}{
		{"every_cited_limit_open_passes", cut, nil, []string{"CLOSED="}, 0, "2 known limit issue(s) cited, all open"},
		{"a_closed_issue_cited_as_a_limit_fails", cut, nil, []string{"CLOSED=2"}, 1, "#2 is closed"},
		{"an_unreadable_tracker_fails", cut, nil, []string{"GH_FAIL=1"}, 1, "could not be read"},
		{"no_known_limits_paragraph_fails", noLimits, nil, []string{"CLOSED="}, 1, "no **Known limits** paragraph"},
		{"an_issue_outside_the_limits_paragraph_is_not_checked", cut, nil, []string{"CLOSED=3 4"}, 0, "all open"},
		{"no_cut_in_progress_checks_nothing", notCut, nil, []string{"CLOSED=2"}, 0, "nothing checked"},
		{"a_merged_pr_naming_a_limit_is_noted", cut, nil, []string{"CLOSED="}, 0, "note  #1: merged PR #9"},
		// The witness for the block selection (item 3). An [Unreleased] list citing a closed issue fails, and a
		// released block's closed citation is not this check's subject while a list is being written.
		{
			"an_unreleased_list_citing_a_closed_issue_fails", inProgress, force,
			[]string{"CLOSED=5"},
			1,
			"#5 is closed, and Unreleased's Known limits",
		},
		{
			"a_released_blocks_closed_citation_does_not_fail_between_cuts", inProgress, force,
			[]string{"CLOSED=2"},
			0,
			"Unreleased, 1 known limit issue(s) cited, all open",
		},
		{
			"an_unreleased_block_with_no_limits_paragraph_fails", inProgressNoLimits, force,
			[]string{"CLOSED="},
			1,
			"Unreleased's block has no **Known limits** paragraph",
		},
		// At the cut, [Unreleased] is empty and the release's own block is the one being written.
		{"at_the_cut_the_new_version_block_is_read", cut, force, []string{"CLOSED=2"}, 1, "#2 is closed, and 1.0.0's"},
	} {
		t.Run(a.name, func(t *testing.T) {
			code, out := run(t, a.changelog, a.args, a.env...)
			if code != a.code {
				t.Fatalf("exit %d, want %d\n%s", code, a.code, out)
			}
			if !strings.Contains(out, a.want) {
				t.Errorf("output does not contain %q:\n%s", a.want, out)
			}
		})
	}
}
