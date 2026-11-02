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

// TestExplainRedReadsTheWindowsRunsFailures is #1207's repair: a red that was only in the windows run is explainable
// by an issue that names its failing test. `ciwatch.sh` records the windows run as `winrun=` beside the tree run's
// `run=`, and explainred reads the failing names from both. Before, it read `run=` alone, the tree run, whose log was
// green. So b72f070's Windows-only red refused #1207, which names the test, as naming none of the run's failures.
//
// The tree runs through `EXPLAINRED_GH`, a `gh` shim that answers `run view <id> --log-failed` per run id, so the
// two-run reading is tested end to end. TestExplainRedAcceptsOnlyAnIssueThatOwnsTheFailure feeds names in through
// EXPLAINRED_FAILED, which skips it.
//
// NEUTER, run (2026-11-02, darwin/arm64): with `$winrun` dropped from the loop, the first row is refused.
func TestExplainRedReadsTheWindowsRunsFailures(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "scripts", "explainred.sh")
	const sha = "5555555555555555555555555555555555555555"
	dir := t.TempDir()
	shim := filepath.Join(dir, "gh.sh")
	// Run 123 is the tree run, with a green log, and 456 is the windows run, with the failure.
	const body = `case "$3" in
456) echo "windows (measured, not a gate)	go test -race	--- FAIL: TestWindowsOnlyFailure (0.10s)" ;;
esac
`
	if werr := os.WriteFile(shim, []byte(body), 0o600); werr != nil {
		t.Fatal(werr)
	}
	for _, a := range []struct {
		name, record string
		admit        bool
		want         string
	}{
		{
			"a_windows_only_failure_named_by_the_issue_is_admitted",
			"ciwatch=fail\nsha=" + sha + "\nrun=123\nwinrun=456\n", true, "names the failing test TestWindowsOnlyFailure",
		},
		{
			"a_record_without_the_windows_run_reads_the_tree_run_alone",
			"ciwatch=fail\nsha=" + sha + "\nrun=123\n", false, "names none of its failing tests",
		},
	} {
		t.Run(a.name, func(t *testing.T) {
			vdir := t.TempDir()
			if werr := os.WriteFile(filepath.Join(vdir, "ci-"+sha), []byte(a.record), 0o600); werr != nil {
				t.Fatal(werr)
			}
			cmd := exec.Command("bash", script, sha, "1207")
			cmd.Env = append(os.Environ(), "EXPLAINRED_VERDICT_DIR="+vdir, "EXPLAINRED_RUN_CREATED=2026-11-02T21:00:00Z",
				"EXPLAINRED_ISSUE=open\t2026-11-02T22:00:00Z\nTestWindowsOnlyFailure counts a scheduling gap",
				"EXPLAINRED_GH=bash "+filepath.ToSlash(shim))
			var out bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &out
			ok := cmd.Run() == nil
			if ok != a.admit || !strings.Contains(out.String(), a.want) {
				t.Errorf("admitted=%v, want %v, and output %q should contain %q", ok, a.admit, out.String(), a.want)
			}
		})
	}
}
