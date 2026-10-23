// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package testenv

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheMergeRouteRefusesWithoutAGreenVerdictForTheHead witnesses editroute's route 8 (the chair's ruling of
// 2026-10-23). #1087 was merged while `ciwatch` printed `FAIL not green`, and main had gone red from #1085 with
// nothing watching it. So a `gh pr merge` is refused unless the PR's current head SHA has a green `make ci`
// record and a green `ciwatch` record, and main's tip has a green (or explained) `ciwatch` record.
//
// The head and main SHAs come from EDITROUTE_PR_HEAD and EDITROUTE_MAIN_SHA here, and the records from
// EDITROUTE_VERDICT_DIR, so no arm needs the network or touches the repository's own `.ci-verdict.d`.
//
// NEUTER, run: `find_record` returning the newest record with the prefix, whatever its `sha=`. The
// `green_for_a_different_sha_is_refused` arm then admits the merge, which is the stale-SHA case.
func TestTheMergeRouteRefusesWithoutAGreenVerdictForTheHead(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(root, "scripts", "editroute.py")
	const (
		head  = "1111111111111111111111111111111111111111"
		other = "2222222222222222222222222222222222222222"
		main  = "3333333333333333333333333333333333333333"
	)
	local := func(sha, exit string) string { return "exit=" + exit + "\nsha=" + sha + "\ndirty=no\n" }
	ci := func(sha, v string) string { return "ciwatch=" + v + "\nsha=" + sha + "\nrun=1\n" }

	run := func(t *testing.T, files map[string]string, command string) (bool, string) {
		t.Helper()
		vdir := t.TempDir()
		for name, body := range files {
			if werr := os.WriteFile(filepath.Join(vdir, name), []byte(body), 0o600); werr != nil {
				t.Fatal(werr)
			}
		}
		payload, merr := json.Marshal(map[string]any{
			"hook_event_name": "PreToolUse", "tool_name": "Bash", "cwd": root,
			"tool_input": map[string]any{"command": command},
		})
		if merr != nil {
			t.Fatal(merr)
		}
		cmd := exec.Command("python3", hook)
		cmd.Dir = root
		cmd.Stdin = bytes.NewReader(payload)
		cmd.Env = append(os.Environ(), "CLAUDE_PROJECT_DIR="+root,
			"EDITROUTE_LOG="+filepath.Join(t.TempDir(), "refusals"),
			"EDITROUTE_VERDICT_DIR="+vdir, "EDITROUTE_PR_HEAD="+head, "EDITROUTE_MAIN_SHA="+main)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		runErr := cmd.Run()
		var ee *exec.ExitError
		if runErr != nil && !errors.As(runErr, &ee) {
			t.Fatalf("running the hook: %v", runErr)
		}
		return runErr != nil && ee.ExitCode() == 2, stderr.String()
	}
	green := map[string]string{head: local(head, "0"), "ci-" + head: ci(head, "green"), "ci-" + main: ci(main, "green")}
	with := func(over map[string]string, drop ...string) map[string]string {
		m := map[string]string{}
		for k, v := range green {
			m[k] = v
		}
		for _, k := range drop {
			delete(m, k)
		}
		for k, v := range over {
			m[k] = v
		}
		return m
	}

	for _, a := range []struct {
		name  string
		files map[string]string
		deny  bool
		want  string
	}{
		{"a_green_verdict_for_the_head_is_admitted", green, false, ""},
		{"a_red_verdict_is_refused", with(map[string]string{head: local(head, "2")}), true, "is red"},
		{"a_missing_verdict_is_refused", map[string]string{}, true, "no `make ci` verdict"},
		{
			"green_for_a_different_sha_is_refused",
			map[string]string{other: local(other, "0"), "ci-" + other: ci(other, "green"), "ci-" + main: ci(main, "green")},
			true, "no `make ci` verdict for the PR's head",
		},
		// #1087's own case: the local gate green, CI red.
		{
			"a_red_ci_verdict_with_a_green_local_gate_is_refused",
			with(map[string]string{"ci-" + head: ci(head, "fail")}), true, "CI's verdict",
		},
		{"an_unwatched_main_is_refused", with(nil, "ci-"+main), true, "has no `ciwatch` verdict"},
		{"a_red_main_is_refused", with(map[string]string{"ci-" + main: ci(main, "fail")}), true, "is red"},
		{
			"a_red_main_that_is_explained_is_admitted",
			with(map[string]string{"ci-" + main: ci(main, "fail") + "explained=1089\n"}), false, "",
		},
	} {
		t.Run(a.name, func(t *testing.T) {
			denied, stderr := run(t, a.files, "gh pr merge 9999 --squash --delete-branch")
			if denied != a.deny {
				t.Fatalf("denied=%v, want %v\n%s", denied, a.deny, stderr)
			}
			if a.deny && !strings.Contains(stderr, a.want) {
				t.Errorf("refused, but not for the registered reason (want %q):\n%s", a.want, stderr)
			}
		})
	}

	t.Run("a_command_that_is_not_a_merge_is_not_judged_by_this_route", func(t *testing.T) {
		if denied, stderr := run(t, map[string]string{}, "gh pr view 9999"); denied {
			t.Fatalf("`gh pr view` was refused by the merge route:\n%s", stderr)
		}
	})
}
