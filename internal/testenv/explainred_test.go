// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package testenv

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestExplainRedAcceptsOnlyAnIssueThatOwnsTheFailure witnesses `scripts/explainred.sh`'s checks (the chair's ruling
// of 2026-10-24). An explanation releases a red main through the merge route only if its issue is open, and either
// existed before the red run or names one of the run's failing tests. The ruling's witness is the refused arm: an
// issue created after the red run that names no failing test.
//
// The issue, the run's creation time and the failing tests come in through the script's seams, so no arm needs the
// network.
func TestExplainRedAcceptsOnlyAnIssueThatOwnsTheFailure(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "scripts", "explainred.sh")
	const (
		sha     = "4444444444444444444444444444444444444444"
		created = "2026-10-24T10:00:00Z"
		failing = "TestStopRefusesASecondConcurrentStop"
	)
	run := func(t *testing.T, record, issue string) (bool, string, string) {
		t.Helper()
		vdir := t.TempDir()
		if werr := os.WriteFile(filepath.Join(vdir, "ci-"+sha), []byte(record), 0o600); werr != nil {
			t.Fatal(werr)
		}
		cmd := exec.Command("bash", script, sha, "946")
		cmd.Env = append(os.Environ(), "EXPLAINRED_VERDICT_DIR="+vdir, "EXPLAINRED_ISSUE="+issue,
			"EXPLAINRED_RUN_CREATED="+created, "EXPLAINRED_FAILED="+failing+"\n")
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		ok := cmd.Run() == nil
		rec, _ := os.ReadFile(filepath.Join(vdir, "ci-"+sha))
		return ok, out.String(), string(rec)
	}
	red := "ciwatch=fail\nsha=" + sha + "\nrun=123\n"

	for _, a := range []struct {
		name, record, issue string
		admit               bool
		want                string
	}{
		{
			"an_issue_created_after_the_red_run_naming_no_failing_test_is_refused", red,
			"open\t2026-10-24T11:00:00Z\nsomething unrelated", false, "names none of its failing tests",
		},
		{
			"an_issue_that_existed_before_the_red_run_is_admitted", red,
			"open\t2026-10-01T00:00:00Z\nan older issue", true, "existed before the red run",
		},
		{
			"an_issue_created_after_that_names_the_failing_test_is_admitted", red,
			"open\t2026-10-24T11:00:00Z\nit is " + failing + " again", true, "names the failing test",
		},
		{
			"a_closed_issue_is_refused", red,
			"closed\t2026-10-01T00:00:00Z\nold", false, "not open",
		},
		{
			"a_green_record_is_refused", "ciwatch=green\nsha=" + sha + "\n",
			"open\t2026-10-01T00:00:00Z\nold", false, "nothing to explain",
		},
		// #1107: a body larger than the pipe buffer. The script split the issue with `printf … | head -1`, and
		// under `set -euo pipefail` `printf` took SIGPIPE once `head` had its line, so the script exited with no
		// message. CI hit that twice with a two-line body under load, and this size hits it every time. 80 KB
		// stays under linux's 128 KB limit for one environment variable.
		//
		// NEUTER, run: the `printf | head` split restored. All three large-body arms then fail, 5 of 5 runs each.
		{
			"a_closed_issue_with_a_large_body_still_says_why", red,
			"closed\t2026-10-01T00:00:00Z\n" + strings.Repeat("x\n", 40000), false, "not open",
		},
		{
			"a_large_body_naming_the_failing_test_at_its_end_is_admitted", red,
			"open\t2026-10-24T11:00:00Z\n" + strings.Repeat("x\n", 40000) + failing, true, "names the failing test",
		},
		// The same shape, one block down: `printf "$body" | grep -qF` inside an `if`. If `grep -q` matches early
		// and exits, `printf` takes SIGPIPE, and `pipefail` turns the match into a false condition. So an issue
		// naming the failing test FIRST read as naming none.
		// NEUTER, run: the `printf | grep -qF` test restored refuses this arm.
		{
			"a_large_body_naming_the_failing_test_first_is_admitted", red,
			"open\t2026-10-24T11:00:00Z\n" + failing + "\n" + strings.Repeat("x\n", 40000), true, "names the failing test",
		},
	} {
		t.Run(a.name, func(t *testing.T) {
			ok, out, rec := run(t, a.record, a.issue)
			if ok != a.admit {
				t.Fatalf("admitted=%v, want %v\n%s", ok, a.admit, out)
			}
			if !strings.Contains(out, a.want) {
				t.Errorf("the output does not give the registered ground (want %q):\n%s", a.want, out)
			}
			if wrote := strings.Contains(rec, "explained=946"); wrote != a.admit {
				t.Errorf("explained= written %v, want %v: %q", wrote, a.admit, rec)
			}
		})
	}
}
