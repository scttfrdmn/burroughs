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

// TestCIWatchTakesEachJobClassFromItsOwnRun reproduces #839's sequence and asserts the watcher reads each job
// class from the run that can actually answer for it.
//
// # The sequence, and why one rule cannot serve both classes
//
// A push produces a run in which every job executes. **Editing the PR body produces a second run** in which
// every *tree*-subject job is skipped — `ci.yml` guards them with `if: github.event.action != 'edited'`,
// because an edit cannot change a byte the compiler sees — and only the *body*-subject job runs. That second
// run is the only one that has read the **current** body.
//
// On #839 the watcher applied one rule to both classes, picked the push run, and reported its `citations`
// verdict — which had been taken against the body **as it was before a rewrite**. The job had failed on
// citation tokens that no longer existed, and re-running it against the current body passed.
//
// So: tree-subject from the newest run in which those jobs *ran*; body-subject from the newest run of any
// trigger. Green only when both hold.
//
// # Fixtures, because two real CI runs are not a repeatable witness
//
// `ciwatch.sh` reads its run list and run detail through a seam: with `CIWATCH_FIXTURE` set it reads
// `runs.json` and `<id>.json` from a directory. The arms below drive **the real selection code** over canned
// runs rather than a reimplementation of it — which is the difference between testing the rule and testing a
// copy of the rule.
func TestCIWatchTakesEachJobClassFromItsOwnRun(t *testing.T) {
	root := "../.."
	script := filepath.Join(root, "scripts", "ciwatch.sh")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("ciwatch.sh is missing, so this witness has no subject: %v", err)
	}

	// Run 1 is the push: every job ran, and `citations` FAILED — it read the body before the rewrite.
	// Run 2 is the body edit: the tree jobs are skipped and `citations` passed against the current body.
	// Newest first, which is the order `gh run list` returns.
	const runsJSON = `[{"databaseId": 2}, {"databaseId": 1}]`
	const pushRun = `{"status":"completed","conclusion":"failure","headSha":"SHA","jobs":[
		{"name":"build (ubuntu-24.04)","conclusion":"success"},
		{"name":"build (ubuntu-24.04-arm)","conclusion":"success"},
		{"name":"lint","conclusion":"success"},
		{"name":"conformance","conclusion":"success"},
		{"name":"vuln","conclusion":"success"},
		{"name":"fuzz-smoke","conclusion":"success"},
		{"name":"witnesses (clauses 1-3) (ubuntu-24.04)","conclusion":"success"},
		{"name":"citations","conclusion":"failure"}]}`
	const editRun = `{"status":"completed","conclusion":"success","headSha":"SHA","jobs":[
		{"name":"build (ubuntu-24.04)","conclusion":"skipped"},
		{"name":"build (ubuntu-24.04-arm)","conclusion":"skipped"},
		{"name":"lint","conclusion":"skipped"},
		{"name":"conformance","conclusion":"skipped"},
		{"name":"vuln","conclusion":"skipped"},
		{"name":"fuzz-smoke","conclusion":"skipped"},
		{"name":"witnesses (clauses 1-3) (ubuntu-24.04)","conclusion":"skipped"},
		{"name":"citations","conclusion":"success"}]}`

	sha := strings.Repeat("a", 40)
	write := func(dir, name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(strings.ReplaceAll(body, "SHA", sha)), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	run := func(t *testing.T, dir string) (int, string) {
		t.Helper()
		cmd := exec.Command("bash", "ciwatch.sh", sha, filepath.Join(dir, "out"))
		cmd.Dir = filepath.Join(root, "scripts")
		cmd.Env = append(os.Environ(), "CIWATCH_FIXTURE="+dir,
			"CIWATCH_WORKFLOW=../.github/workflows/ci.yml")
		out, err := cmd.CombinedOutput()
		code := 0
		if err != nil {
			var ee *exec.ExitError
			if !errors.As(err, &ee) {
				t.Fatalf("running ciwatch: %v\n%s", err, out)
			}
			code = ee.ExitCode()
		}
		return code, string(out)
	}

	t.Run("each_class_from_its_own_run", func(t *testing.T) {
		dir := t.TempDir()
		write(dir, "runs.json", runsJSON)
		write(dir, "1.json", pushRun)
		write(dir, "2.json", editRun)

		code, out := run(t, dir)
		if code != 0 {
			t.Errorf("exit %d, want 0 — the tree jobs all passed in run 1 and citations passed in run 2, so "+
				"this is green:\n%s", code, out)
		}
		// The split must be VISIBLE, not merely effective: a reader of the log has to be able to see which
		// run answered for which class, or a future wrong pick is invisible.
		if !strings.Contains(out, "tree-subject verdict from run 1") ||
			!strings.Contains(out, "body-subject from run 2") {
			t.Errorf("the log does not name which run answered for which class:\n%s", out)
		}
	})

	t.Run("a_failing_body_job_in_the_newest_run_is_still_a_failure", func(t *testing.T) {
		// The mirror of the first arm: if the CURRENT body is bad, the newest run says so and the watcher
		// must not reach past it to an older run that passed. Without this, "take the newest" could be
		// satisfied by "take whichever passes", which is not a verdict.
		dir := t.TempDir()
		write(dir, "runs.json", runsJSON)
		write(dir, "1.json", strings.ReplaceAll(pushRun, `{"name":"citations","conclusion":"failure"}`,
			`{"name":"citations","conclusion":"success"}`))
		write(dir, "2.json", strings.ReplaceAll(editRun, `{"name":"citations","conclusion":"success"}`,
			`{"name":"citations","conclusion":"failure"}`))

		code, out := run(t, dir)
		if code == 0 {
			t.Errorf("exit 0, want non-zero — the newest run's body job FAILED, and an older run's pass must "+
				"not stand in for it:\n%s", out)
		}
	})

	t.Run("no_run_ran_the_tree_jobs_is_a_failure_not_a_pass", func(t *testing.T) {
		// Only the edit run exists. There is no run that can answer for the tree, and silence is not a pass.
		dir := t.TempDir()
		write(dir, "runs.json", `[{"databaseId": 2}]`)
		write(dir, "2.json", editRun)

		code, out := run(t, dir)
		if code == 0 {
			t.Errorf("exit 0, want non-zero — no run executed the tree-subject jobs:\n%s", out)
		}
		if !strings.Contains(out, "no run for") {
			t.Errorf("the failure does not say that no run could answer for the tree class:\n%s", out)
		}
	})
}
