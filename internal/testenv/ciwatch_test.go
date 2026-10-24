// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package testenv_test

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
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

	// **Body currency is read from the body's last edit, not from the PR's `updatedAt`.** A comment moves
	// `updatedAt`. On #1030 one did, a never-edited body read as stale, and prmerge refused a green. The run
	// that answered for the body (run 2) was created at T1. Each arm gives the PR record the fixture's live
	// mode would fetch, so the arm drives the script's field choice.
	//
	// Neuter, run: reading `updatedAt` fails the first and third arms.
	bodyArm := func(t *testing.T, pr string) map[string]any {
		t.Helper()
		dir := t.TempDir()
		write(dir, "runs.json", runsJSON)
		write(dir, "1.json", pushRun)
		write(dir, "2.json", editRun)
		write(dir, "2.created_at", "2026-10-17T16:47:27Z")
		write(dir, "pr.json", pr)
		if code, out := run(t, dir, "CIWATCH_PR=1"); code != 0 {
			t.Fatalf("exit %d, want 0 (both classes green):\n%s", code, out)
		}
		raw, err := os.ReadFile(filepath.Join(dir, "out.verdict"))
		if err != nil {
			t.Fatalf("no verdict file was written: %v", err)
		}
		var v map[string]any
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatalf("the verdict file is not JSON: %v\n%s", err, raw)
		}
		return v
	}
	t.Run("a_comment_after_the_run_does_not_make_an_unedited_body_stale", func(t *testing.T) {
		v := bodyArm(t, `{"lastEditedAt":null,"createdAt":"2026-10-17T16:47:24Z","updatedAt":"2026-10-17T17:15:13Z"}`)
		if v["body_stale"] != false {
			t.Errorf("body_stale = %v, want false: the body was never edited, and only a comment moved "+
				"updatedAt (%v)", v["body_stale"], v["body_stale_note"])
		}
	})
	t.Run("an_edit_after_the_run_is_stale", func(t *testing.T) {
		v := bodyArm(t, `{"lastEditedAt":"2026-10-17T17:00:00Z","createdAt":"2026-10-17T16:47:24Z","updatedAt":"2026-10-17T17:00:00Z"}`)
		if v["body_stale"] != true {
			t.Errorf("body_stale = %v, want true: the body was edited after the run that answered for it "+
				"(%v)", v["body_stale"], v["body_stale_note"])
		}
	})
	t.Run("an_edit_before_the_run_is_current", func(t *testing.T) {
		v := bodyArm(t, `{"lastEditedAt":"2026-10-17T16:47:26Z","createdAt":"2026-10-17T16:47:24Z","updatedAt":"2026-10-17T17:15:13Z"}`)
		if v["body_stale"] != false {
			t.Errorf("body_stale = %v, want false (%v)", v["body_stale"], v["body_stale_note"])
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
		// This arm exercises the coverage predicate directly — the half that has to be right **even after
		// the wait**, because a fetch can race a job's own transition.
		//
		// **The run says `completed` while a job's conclusion is still null, and that is the point** (#867).
		// It used to say `in_progress`, which worked only because a fixture skipped the wait entirely. Now
		// that the wait polls for `status == completed`, an `in_progress` fixture is caught by the wait and
		// never reaches `assert_class` — so the state this arm is about would have been tested by the wrong
		// mechanism, and the coverage predicate would have gone unexercised while the arm still looked green.
		//
		// A completed run carrying a null-conclusion job is exactly the race the paragraph above names, and
		// it is the only shape that reaches the predicate now. The arm's CLAIM is unchanged; its fixture
		// stopped being a valid way to reach it.
		dir := t.TempDir()
		pending := strings.ReplaceAll(pushRun, `"conclusion":"success"`, `"conclusion":null`)
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

	// --- #867: the wait POLLS, and "unfinished" is never a verdict -----------------------------------------
	//
	// `ciwatch.sh` used to wait with `gh run watch`, which can exit 0 **without the run having completed**.
	// When it returned early the script walked into evaluation, found jobs with no conclusion, and reported
	// a FAILURE. Measured on #868: `FAIL tree-subject: unfinished: build, build` while both were in
	// progress; re-running it unchanged gave GREEN and all nine jobs finished `success`.
	//
	// A false fail on the one path every merge goes through, whose only guard was an operator noticing.

	t.Run("an_unfinished_run_is_waited_for_not_failed", func(t *testing.T) {
		// The fixture reports run 1 as in_progress for its first two reads, then completed — the `runs.delay`
		// idiom one function over. Without a mechanism like this the fixture's run is complete from the first
		// read and the poll has no arm at all, which is how the defect lived in the one step nothing could
		// exercise.
		dir := t.TempDir()
		write(dir, "runs.json", `[{"databaseId": 1}]`)
		write(dir, "1.json", strings.ReplaceAll(pushRun,
			`{"name":"citations","conclusion":"failure"}`, `{"name":"citations","conclusion":"success"}`))
		inprog := strings.ReplaceAll(pushRun, `"status":"completed"`, `"status":"in_progress"`)
		inprog = strings.ReplaceAll(inprog, `"conclusion":"success"`, `"conclusion":null`)
		write(dir, "1.unfinished.json", inprog)
		write(dir, "1.unfinished", "2")

		code, out := run(t, dir)
		if code != 0 {
			t.Fatalf("exit %d — an unfinished run that then completes must be WAITED for, not failed:\n%s",
				code, out)
		}
		if !strings.Contains(out, "GREEN") {
			t.Errorf("the completed run was not read as green after the wait:\n%s", out)
		}
		// The wait must be visible, or a reader cannot tell a poll from a lucky first read.
		if !strings.Contains(out, "polling again") {
			t.Errorf("no evidence the run was polled; the wait may have been skipped:\n%s", out)
		}
	})

	t.Run("a_spent_job_bound_is_unfinished_not_fail", func(t *testing.T) {
		// The bound's expiry is its OWN outcome. A reader acts differently on "still running" than on "the
		// tree is broken", so collapsing them into `fail` would re-create the defect with a bound on it.
		dir := t.TempDir()
		write(dir, "runs.json", `[{"databaseId": 1}]`)
		write(dir, "1.json", pushRun)
		inprog := strings.ReplaceAll(pushRun, `"status":"completed"`, `"status":"in_progress"`)
		write(dir, "1.unfinished.json", inprog)
		write(dir, "1.unfinished", "999") // never completes within any bound

		code, out := run(t, dir, "CIWATCH_JOB_WAIT=1", "CIWATCH_JOB_POLL=1")
		if code != 4 {
			t.Fatalf("exit %d, want 4 — a spent job bound is its own outcome, not fail (1) and not green:\n%s",
				code, out)
		}
		if !strings.Contains(out, "UNFINISHED") || !strings.Contains(out, "NOT a failure of the tree") {
			t.Errorf("the message does not distinguish a spent bound from a tree failure:\n%s", out)
		}
		v, err := os.ReadFile(filepath.Join(dir, "out.verdict"))
		if err != nil {
			t.Fatalf("no verdict file: a spent bound must still record what it concluded: %v", err)
		}
		if !strings.Contains(string(v), `"ciwatch_verdict": "unfinished"`) {
			t.Errorf("the verdict file does not say unfinished, so a reader cannot tell it from fail:\n%s", v)
		}
	})

	t.Run("the_fixture_bound_defaults_to_fixture_scale", func(t *testing.T) {
		// The mechanism that replaces "every fixture test must remember to set a bound". The first version
		// of the poll defaulted to 2400s/15s unconditionally and hung `internal/testenv` until `go test`
		// timed out at 600s. A reminder would have worked until the next test forgot.
		dir := t.TempDir()
		write(dir, "runs.json", `[{"databaseId": 1}]`)
		write(dir, "1.json", pushRun)
		write(dir, "1.unfinished.json", strings.ReplaceAll(pushRun, `"status":"completed"`, `"status":"in_progress"`))
		write(dir, "1.unfinished", "999")

		start := time.Now()
		code, out := run(t, dir) // deliberately NO bound set
		elapsed := time.Since(start)
		if code != 4 {
			t.Fatalf("exit %d, want 4 — with no bound set a fixture must still reach unfinished:\n%s", code, out)
		}
		// The production default is 2400s. Anything near it means the fixture scale did not apply.
		if elapsed > 30*time.Second {
			t.Errorf("took %s with no bound set; the fixture-scale default did not apply, so a test that "+
				"forgets to set one hangs the package", elapsed)
		}
	})

	// #1037: the Windows run is read against the triage map. The log is RECORDED, from run 39458535651 (the
	// windows run for #1072's head), so the parser meets the real `gh run view --log-failed` line shape:
	// job, step and timestamp before `--- FAIL:`, and subtests indented under their parent.
	recorded, err := os.ReadFile(filepath.Join("testdata", "ciwatch", "windows-39458535651.log"))
	if err != nil {
		t.Fatalf("the recorded Windows log is missing: %v", err)
	}
	// The map: the seven top-level names that run failed, under a comment header in the real file's form.
	const triage = "# a comment line, which is not a row\n#\n" +
		"TestCILockAllowsOneGateAtATime\t1000\tnote\n" +
		"TestCIWatchTakesEachJobClassFromItsOwnRun\t1000\tnote\n" +
		"TestEditRouteHookRefusesBashEditsOfTrackedFiles\t1000\tnote\n" +
		"TestEditRouteRefusesGitCommandsThatDiscardUncommittedWork\t1000\tnote\n" +
		"TestPreCommitRefusesCommitsOnTheDefaultBranch\t1000\tnote\n" +
		"TestShellAndGoAgreeOnTheSuitePopulation\t1000\tnote\n" +
		"TestStopBringsATailCallLoopToASafepoint\t1000\tnote\n"
	const winFailed = `{"status":"completed","conclusion":"failure","headSha":"SHA","workflowName":"windows","jobs":[
		{"name":"windows (measured, not a gate)","conclusion":"failure"}]}`
	const winPassed = `{"status":"completed","conclusion":"success","headSha":"SHA","workflowName":"windows","jobs":[
		{"name":"windows (measured, not a gate)","conclusion":"success"}]}`
	winRun := func(t *testing.T, runJSON, log string) (int, string, map[string]any) {
		t.Helper()
		dir := t.TempDir()
		write(dir, "runs.json", `[{"databaseId": 3}, {"databaseId": 1}]`)
		write(dir, "1.json", strings.ReplaceAll(pushRun, `"conclusion":"failure"}]}`, `"conclusion":"success"}]}`))
		write(dir, "3.json", runJSON)
		write(dir, "3.log", log)
		write(dir, "triage.tsv", triage)
		code, out := run(t, dir, "CIWATCH_TRIAGE="+filepath.Join(dir, "triage.tsv"))
		raw, err := os.ReadFile(filepath.Join(dir, "out.verdict"))
		if err != nil {
			t.Fatalf("no verdict file: %v\n%s", err, out)
		}
		var v map[string]any
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatalf("verdict is not JSON: %v\n%s", err, raw)
		}
		return code, out, v
	}

	t.Run("windows_failures_all_in_the_map_are_green", func(t *testing.T) {
		code, out, v := winRun(t, winFailed, string(recorded))
		if code != 0 || v["ciwatch_verdict"] != "green" {
			t.Fatalf("exit %d, verdict %v: every failing name in the recorded log is mapped, so this is green:\n%s",
				code, v["ciwatch_verdict"], out)
		}
		if !strings.Contains(out, "7 failing test(s), all in the triage map") {
			t.Errorf("the log does not say how many failures were read and that all were mapped; a parser that "+
				"read none would also be green:\n%s", out)
		}
	})

	// NEUTER, as the chair ruled it: an unmapped failure injected into the recorded log must turn the verdict red.
	t.Run("an_unmapped_windows_failure_is_red", func(t *testing.T) {
		injected := string(recorded) + "windows (measured, not a gate)\tgo test -race\t2026-10-20T17:00:00.0000000Z" +
			" --- FAIL: TestNotInTheTriageMap (0.01s)\n"
		code, out, v := winRun(t, winFailed, injected)
		if code == 0 || v["ciwatch_verdict"] != "fail" {
			t.Fatalf("exit %d, verdict %v: TestNotInTheTriageMap is in no row, so the verdict must fail:\n%s",
				code, v["ciwatch_verdict"], out)
		}
		if !strings.Contains(out, "TestNotInTheTriageMap") {
			t.Errorf("the failure does not name the unmapped test:\n%s", out)
		}
	})

	// The chair's fourth ruling of 2026-10-24: an empty `--log-failed` falls back to the run's full log. The
	// specimen is RECORDED from run 40113864759, attempt 1 (#1096's first head), as this script kept it at the time:
	// - the run JSON, whose failed job's `go test -race` step has an empty conclusion;
	// - the 0-byte failed-step log;
	// - the attempt's full log, which names seven failures, all mapped.
	// GitHub later filled in the conclusion, so a re-fetch would not reproduce it.
	// NEUTER, run: `fetch_failed_log`'s full-log branch removed reads the witness red, "names no failing test".
	nullRun, nerr := os.ReadFile(filepath.Join("testdata", "ciwatch", "windows-40113864759.json"))
	if nerr != nil {
		t.Fatalf("the recorded null-step run is missing: %v", nerr)
	}
	// The specimen's property, read rather than assumed: a FAILED job whose `go test -race` step has no
	// conclusion. GitHub left the step `in_progress`, inside a job it had completed as a failure.
	var rec struct {
		Jobs []struct {
			Conclusion string `json:"conclusion"`
			Steps      []struct {
				Name       string `json:"name"`
				Conclusion string `json:"conclusion"`
			} `json:"steps"`
		} `json:"jobs"`
	}
	if jerr := json.Unmarshal(nullRun, &rec); jerr != nil {
		t.Fatalf("the recorded run is not JSON: %v", jerr)
	}
	nullStep := false
	for _, j := range rec.Jobs {
		for _, s := range j.Steps {
			if j.Conclusion == "failure" && s.Name == "go test -race" && s.Conclusion == "" {
				nullStep = true
			}
		}
	}
	if !nullStep {
		t.Fatal("the recorded run has no failed job whose `go test -race` step lacks a conclusion, so it is not the specimen")
	}
	nullFull, ferr := os.ReadFile(filepath.Join("testdata", "ciwatch", "windows-40113864759.full.log"))
	if ferr != nil {
		t.Fatalf("the recorded full log is missing: %v", ferr)
	}
	// The recorded run names its head SHA. The fixture's push run uses "SHA", so the record is re-keyed to match.
	nullRunJSON := regexp.MustCompile(`"headSha":"[0-9a-f]{40}"`).ReplaceAllString(string(nullRun), `"headSha":"SHA"`)
	fullRun := func(t *testing.T, full string) (int, string, map[string]any) {
		t.Helper()
		dir := t.TempDir()
		write(dir, "runs.json", `[{"databaseId": 3}, {"databaseId": 1}]`)
		write(dir, "1.json", strings.ReplaceAll(pushRun, `"conclusion":"failure"}]}`, `"conclusion":"success"}]}`))
		write(dir, "3.json", nullRunJSON)
		write(dir, "3.log", "")
		if full != "" {
			write(dir, "3.full.log", full)
		}
		write(dir, "triage.tsv", triage)
		code, out := run(t, dir, "CIWATCH_TRIAGE="+filepath.Join(dir, "triage.tsv"))
		raw, rerr := os.ReadFile(filepath.Join(dir, "out.verdict"))
		if rerr != nil {
			t.Fatalf("no verdict file: %v\n%s", rerr, out)
		}
		var v map[string]any
		if jerr := json.Unmarshal(raw, &v); jerr != nil {
			t.Fatalf("verdict is not JSON: %v\n%s", jerr, raw)
		}
		return code, out, v
	}
	t.Run("an_empty_failed_log_with_a_null_step_is_classified_from_the_full_log", func(t *testing.T) {
		code, out, v := fullRun(t, string(nullFull))
		if code != 0 || v["ciwatch_verdict"] != "green" {
			t.Fatalf("exit %d, verdict %v: the full log names only mapped tests, so this is green:\n%s",
				code, v["ciwatch_verdict"], out)
		}
		if !strings.Contains(out, "7 failing test(s), all in the triage map") {
			t.Errorf("the verdict does not say it read seven mapped failures from the full log:\n%s", out)
		}
	})
	t.Run("an_empty_failed_log_whose_full_log_names_no_test_is_still_red", func(t *testing.T) {
		code, out, v := fullRun(t, "windows (measured, not a gate)\tSet up Go\terror: setup failed\n")
		if code == 0 || v["ciwatch_verdict"] != "fail" || !strings.Contains(out, "names no failing test") {
			t.Fatalf("exit %d, verdict %v: the full log names no test, so the refusal stands:\n%s",
				code, v["ciwatch_verdict"], out)
		}
	})

	t.Run("a_windows_failure_that_names_no_test_is_red", func(t *testing.T) {
		code, out, v := winRun(t, winFailed, "windows (measured, not a gate)\tSet up Go\terror: setup failed\n")
		if code == 0 || v["ciwatch_verdict"] != "fail" {
			t.Fatalf("exit %d, verdict %v: a failed job with no test named cannot be classified by the map:\n%s",
				code, v["ciwatch_verdict"], out)
		}
	})

	// A package can fail with no `--- FAIL:` name, and every other failing name being mapped must not hide it.
	// Both shapes are appended after the recorded log, whose seven names are all mapped.
	const pfx = "windows (measured, not a gate)\tgo test -race\t2026-10-20T17:00:00.0000000Z "
	for _, c := range []struct{ name, tail, pkg string }{
		{
			"a_package_build_failure_is_red", pfx + "FAIL\tgithub.com/scttfrdmn/burroughs/internal/x [build failed]\n",
			"internal/x",
		},
		{
			"a_package_timeout_is_red", pfx + "panic: test timed out after 30m0s\n" + pfx + "\trunning tests:\n" +
				pfx + "\t\tTestSlow (30m0s)\n" + pfx + "FAIL\tgithub.com/scttfrdmn/burroughs/internal/y\t1800.012s\n",
			"internal/y",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			code, out, v := winRun(t, winFailed, string(recorded)+c.tail)
			if code == 0 || v["ciwatch_verdict"] != "fail" {
				t.Fatalf("exit %d, verdict %v: a package failed with no test named, which the map cannot "+
					"classify:\n%s", code, v["ciwatch_verdict"], out)
			}
			if !strings.Contains(out, c.pkg) {
				t.Errorf("the failure does not name the package %s:\n%s", c.pkg, out)
			}
		})
	}

	// The merge route's record (the chair's ruling of 2026-10-23): ciwatch writes `ci-<sha>` where editroute's
	// route 8 reads it, green or fail, and a fixture run writes it nowhere unless told where.
	t.Run("the_verdict_is_recorded_for_the_merge_route", func(t *testing.T) {
		for _, c := range []struct {
			name, run, want string
		}{
			{"green", strings.ReplaceAll(pushRun, `"conclusion":"failure"}]}`, `"conclusion":"success"}]}`), "ciwatch=green"},
			{"red", pushRun, "ciwatch=fail"},
		} {
			dir := t.TempDir()
			vdir := filepath.Join(dir, "verdicts")
			write(dir, "runs.json", `[{"databaseId": 1}]`)
			write(dir, "1.json", c.run)
			_, out := run(t, dir, "CIWATCH_VERDICT_DIR="+vdir)
			rec, err := os.ReadFile(filepath.Join(vdir, "ci-"+sha))
			if err != nil {
				t.Fatalf("%s: no record for the merge route: %v\n%s", c.name, err, out)
			}
			if !strings.Contains(string(rec), c.want+"\n") || !strings.Contains(string(rec), "sha="+sha+"\n") {
				t.Errorf("%s: record %q, want %s and the SHA", c.name, rec, c.want)
			}
		}
	})

	t.Run("a_passing_windows_run_is_green", func(t *testing.T) {
		code, out, v := winRun(t, winPassed, "")
		if code != 0 || v["ciwatch_verdict"] != "green" {
			t.Fatalf("exit %d, verdict %v:\n%s", code, v["ciwatch_verdict"], out)
		}
	})
}
