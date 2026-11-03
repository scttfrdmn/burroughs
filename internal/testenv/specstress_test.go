// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package testenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSpecStressCountsWhatItRan is `scripts/specstress.sh`'s witness: #1089's reproduction stress is only as good as
// its counting, so each way an iteration can fail must count as a failure, and a clean run must count exactly.
//
// The test binary is a shim, a bash script that behaves as SHIM says. The stress passes trailing VAR=value pairs into
// each iteration's environment, which is how the rows choose the behaviour.
//
// NEUTER, run (2026-11-02, darwin/arm64): with the `--- SKIP` check removed from the failure test, the skip row reads
// as 4 passed and fails here.
func TestSpecStressCountsWhatItRan(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "scripts", "specstress.sh")
	dir := t.TempDir()
	shim := filepath.Join(dir, "spec.test")
	const body = `#!/usr/bin/env bash
case "$SHIM" in
pass) echo "--- PASS: TestThreadsGateLeavesTheCoreBoardUnchanged (0.00s)" ;;
fail) echo "fatal error: found pointer to free object"; exit 2 ;;
skip) echo "--- SKIP: TestThreadsGateLeavesTheCoreBoardUnchanged (0.00s)"; echo "--- PASS: TestThreadsGateLeavesTheCoreBoardUnchanged (0.00s)" ;;
silent) echo "testing: warning: no tests to run" ;;
whole-skip) echo "--- SKIP: TestNeedsACorpus (0.00s)"; echo "PASS" ;;
whole-fail) echo "--- FAIL: TestSomething (0.00s)"; echo "FAIL"; exit 1 ;;
whole-cut) echo "--- PASS: TestSomething (0.00s)" ;;
esac
`
	if werr := os.WriteFile(shim, []byte(body), 0o755); werr != nil { //nolint:gosec // an executable test shim
		t.Fatal(werr)
	}

	// The failure rows run one worker so their counts are exact. With two, a worker can fail while the other is
	// recording the cap, and both failures are real, so the count is right but not fixed. The clean row runs two,
	// so the claim lock is exercised.
	for _, r := range []struct {
		name, shim, maxfail, workers string
		whole                        bool
		passed, failed               string
		stop                         bool
	}{
		{"a_clean_run_counts_every_iteration", "pass", "", "2", false, "4 passed", "0 failed", false},
		{"a_non_zero_exit_stops_at_the_first", "fail", "", "1", false, "0 passed", "1 failed", true},
		{"a_skip_is_a_failure", "skip", "", "1", false, "0 passed", "1 failed", true},
		{"no_pass_line_is_a_failure", "silent", "", "1", false, "0 passed", "1 failed", true},
		{"the_cap_runs_on_to_a_rate", "fail", "3", "1", false, "0 passed", "3 failed", true},
		// The whole-binary mode (#1089's widening). A licensed skip is not a failure there, but a failed test is,
		// and so is an exit 0 whose output does not end in PASS.
		{"whole_a_licensed_skip_still_passes", "whole-skip", "", "2", true, "4 passed", "0 failed", false},
		{"whole_a_failed_test_is_a_failure", "whole-fail", "", "1", true, "0 passed", "1 failed", true},
		{"whole_no_final_pass_is_a_failure", "whole-cut", "", "1", true, "0 passed", "1 failed", true},
	} {
		t.Run(r.name, func(t *testing.T) {
			out := t.TempDir()
			cmd := exec.Command("bash", script, "row", shim, r.workers, "4", "SHIM="+r.shim)
			cmd.Env = append(os.Environ(), "SPECSTRESS_OUT="+out, "SPECSTRESS_DIR="+dir)
			if r.maxfail != "" {
				cmd.Env = append(cmd.Env, "SPECSTRESS_MAXFAIL="+r.maxfail)
			}
			if r.whole {
				cmd.Env = append(cmd.Env, "SPECSTRESS_WHOLE=1")
			}
			got, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("specstress.sh: %v\n%s", err, got)
			}
			s := string(got)
			if !strings.Contains(s, r.passed) || !strings.Contains(s, r.failed) {
				t.Errorf("summary %q, want %q and %q", strings.TrimSpace(s), r.passed, r.failed)
			}
			_, serr := os.Stat(filepath.Join(out, "row", "STOP"))
			if r.stop != (serr == nil) {
				t.Errorf("STOP present = %v, want %v", serr == nil, r.stop)
			}
			if r.stop {
				logs, _ := filepath.Glob(filepath.Join(out, "row", "fail.*.log"))
				if len(logs) == 0 {
					t.Error("a failing iteration left no fail.<n>.log, so its output is lost")
				}
			}
		})
	}
}
