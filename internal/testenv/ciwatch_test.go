// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package testenv_test

import (
	"encoding/json"
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

	run := func(t *testing.T, dir string, extraEnv ...string) (int, string) {
		t.Helper()
		cmd := exec.Command("bash", "ciwatch.sh", sha, filepath.Join(dir, "out"))
		cmd.Dir = filepath.Join(root, "scripts")
		cmd.Env = append(os.Environ(), "CIWATCH_FIXTURE="+dir,
			"CIWATCH_WORKFLOW=../.github/workflows/ci.yml")
		cmd.Env = append(cmd.Env, extraEnv...)
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

		// The verdict FILE must agree with the verdict. This arm exists because the file used to be a copy
		// of the chosen tree run's JSON, and run 1 here concludes `failure` — its citations job failed
		// against a body that no longer exists. So the file said `"conclusion": "failure"` beside a GREEN,
		// and the standing rule for reading a CI result is *read the verdict file's status field*, which
		// would have returned the opposite of the truth to the one reader the file exists for.
		raw, err := os.ReadFile(filepath.Join(dir, "out.verdict"))
		if err != nil {
			t.Fatalf("no verdict file was written: %v", err)
		}
		var v map[string]any
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatalf("the verdict file is not JSON, so it cannot be read by the tool that needs it: %v\n%s",
				err, raw)
		}
		if v["ciwatch_verdict"] != "green" {
			t.Errorf("verdict file says %v, want green: %s", v["ciwatch_verdict"], raw)
		}
		// And it must not carry a run's conclusion under a name a reader would reach for, because the
		// whole point is that no single run holds one.
		if _, present := v["conclusion"]; present {
			t.Errorf("the verdict file carries a `conclusion` key — a reader will take it for the verdict, "+
				"and since the two-class split no single run's conclusion is one:\n%s", raw)
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

	t.Run("a_run_that_appears_late_is_waited_for", func(t *testing.T) {
		// The gap this closes: the watcher exited 3 the instant no run existed, and the moment it is
		// launched is the moment right after a push — exactly when GitHub may not have created the run yet.
		// It bit once, reporting `no run exists ... yet` having watched nothing.
		//
		// `runs.delay` makes the fixture return an empty list for its first N calls, so the retry is
		// *witnessed* rather than asserted. A static fixture either has runs from the start or never gets
		// them, and neither exercises "appeared on attempt 3".
		dir := t.TempDir()
		write(dir, "runs.json", runsJSON)
		write(dir, "1.json", pushRun)
		write(dir, "2.json", editRun)
		write(dir, "runs.delay", "2")

		code, out := run(t, dir, "CIWATCH_RUN_WAIT=30", "CIWATCH_RUN_POLL=1")
		if code != 0 {
			t.Fatalf("exit %d, want 0 — the run appeared on the third call and the verdict is green:\n%s",
				code, out)
		}
		// The wait must be VISIBLE. An operator watching a watcher needs to tell polling from a stall,
		// and a silent retry loop is indistinguishable from a hang.
		if !strings.Contains(out, "no run yet") {
			t.Errorf("the watcher never said it was waiting, so a poll cannot be told from a stall:\n%s", out)
		}
		if !strings.Contains(out, "a run appeared after") {
			t.Errorf("the watcher did not report that the run eventually appeared:\n%s", out)
		}
	})

	t.Run("a_spent_bound_is_no_run_is_coming_not_no_run_yet", func(t *testing.T) {
		// The other half of the distinction, and the reason the bound exists at all: once it is spent the
		// watcher must conclude, and its message must say what it concluded and on what evidence. Reporting
		// a momentary absence in the same words as an exhausted bound is what made the old behaviour
		// useless — it was never dishonest, it just could not be acted on.
		dir := t.TempDir()
		write(dir, "runs.json", `[]`)

		code, out := run(t, dir, "CIWATCH_RUN_WAIT=0", "CIWATCH_RUN_POLL=1")
		if code == 0 {
			t.Fatalf("exit 0 with no run at all — silence is not a pass:\n%s", out)
		}
		if !strings.Contains(out, "no run appeared") || !strings.Contains(out, "bound") {
			t.Errorf("the failure does not distinguish a spent bound from a momentary absence:\n%s", out)
		}
		// And it must NOT claim the tree is bad: there is no verdict here, only an absent one.
		if strings.Contains(out, "GREEN") {
			t.Errorf("a spent bound reported a green:\n%s", out)
		}
	})

	t.Run("an_in_progress_job_has_run_it_has_not_finished", func(t *testing.T) {
		// The defect #841 shipped and #842's own CI found. `run_covers` read a job with no conclusion as a job
		// that had NOT RUN — the reading that is right for `skipped` and wrong for `in_progress` — and
		// selection happened before the wait. So a freshly pushed SHA, whose jobs are all pending, reported
		// "no run for <sha> ran its tree-subject jobs" about a run that was busy running them.
		//
		// Three states, not two: `skipped` is a claim about whether, a null conclusion is a claim about WHEN,
		// and only the first excludes a run from answering for a class. `assert_class` already drew this
		// distinction; this arm is why drawing it in one of two places was not enough.
		//
		// Under a fixture there is no waiting, so this exercises the coverage predicate directly — which is
		// the half that has to be right even after the wait, because a fetch can race a job's own transition.
		dir := t.TempDir()
		pending := strings.ReplaceAll(pushRun, `"conclusion":"success"`, `"conclusion":null`)
		pending = strings.ReplaceAll(pending, `"status":"completed"`, `"status":"in_progress"`)
		pending = strings.ReplaceAll(pending, `{"name":"citations","conclusion":"failure"}`,
			`{"name":"citations","conclusion":"success"}`)
		write(dir, "runs.json", `[{"databaseId": 1}]`)
		write(dir, "1.json", pending)

		code, out := run(t, dir)
		if code == 0 {
			t.Fatalf("exit 0 — an unfinished run is not green either:\n%s", out)
		}
		// The distinction the whole arm is about: it must fail as UNFINISHED, never as "no run ran them".
		// Both are non-zero, so an exit code alone cannot tell this arm's pass from its failure.
		if strings.Contains(out, "no run for") {
			t.Errorf("an in-progress run was reported as a run that never executed the tree jobs — the\n"+
				"three-state distinction is gone and the exit code cannot see it:\n%s", out)
		}
		if !strings.Contains(out, "unfinished") {
			t.Errorf("the failure does not name the jobs as unfinished, so it is not reporting the state it\n"+
				"found:\n%s", out)
		}
	})
}
