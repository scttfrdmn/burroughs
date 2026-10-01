// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package testenv

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestPrmergeRefusesWithoutAGreenLocalVerdictForThisCommit witnesses the gate-verdict check in
// `scripts/prmerge.sh`.
//
// # Why the verdict is a file and not an exit code
//
// `make ci`'s verdict was lost three ways in one session. Chaining as `make ci > log; echo rc=$?` hands
// the status to whatever ran last. `pipefail` does not reach across a `;`. And a background task's
// completion notification carries the **wrapper's** exit code — which is how a commit got pushed over a
// red gate with `ci rc=2` sitting unread in the output file.
//
// So `make ci` writes `.ci-verdict` itself, and this is the consumer that makes the file matter.
//
// # Why the SHA is the load-bearing field
//
// Without it the file is a mood: a green from three commits ago reads exactly like a green from this one,
// and the failure mode is invisible because the file says `exit=0` either way. The arms below are ordered
// by how plausibly each could pass unnoticed, and the stale-SHA arm is first for that reason.
//
// # Scope
//
// This drives only the verdict block. It stops before the irreversible step by pointing the script at a
// PR number it will never reach — every check here fires before `gh pr merge` is invoked, which is the
// property prmerge.sh is built around and is asserted by the absence of any network call in these arms.
func TestPrmergeRefusesWithoutAGreenLocalVerdictForThisCommit(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "scripts", "prmerge.sh")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("prmerge.sh is missing, so this witness has no subject: %v", err)
	}

	// A dedicated repo, because the subject is the verdict block and the tree's state must not decide the
	// outcome. The first version ran in the real tree and every arm failed on the CLEAN-TREE check — which
	// fires first — so the test passed in CI and failed locally whenever work was in progress. A control
	// whose verdict depends on ambient state is measuring the ambient state.
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "witness@example.invalid"},
		{"config", "user.name", "witness"},
		{"commit", "-q", "--allow-empty", "-m", "base"},
	} {
		c := exec.Command("git", args...)
		c.Dir = repo
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v in the temp repo: %v\n%s", args, err, out)
		}
	}
	tip, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("cannot resolve the temp repo's HEAD: %v", err)
	}
	head := strings.TrimSpace(string(tip))

	// The verdict block runs after the PR lookup, so these arms need the lookup to succeed. Rather than
	// reach the network, the script is driven with a stub `gh` earlier on PATH: the block under test is
	// reached, and nothing can merge because the stub cannot.
	stub := t.TempDir()
	ghStub := `#!/usr/bin/env bash
# A stub `+"`gh`"+`: answers the one query prmerge makes before the verdict block, and REFUSES to merge.
# If a check under test ever stops firing, the stub is what turns that into a loud failure instead of a
# real merge — a witness for an irreversible step must not be able to perform it.
if [ "$2" = "pr" ] || [ "$1" = "pr" ]; then
  case "$*" in
    *view*) echo '{"headRefName":"' "${PRMERGE_TEST_BRANCH:-nosuch}" '","headRefOid":"deadbeef","state":"OPEN","mergeable":"MERGEABLE"}' | tr -d ' ' ;;
    *merge*) echo "STUB-GH: a merge was attempted, which means a check did not fire" >&2; exit 97 ;;
  esac
fi
`
	if err := os.WriteFile(filepath.Join(stub, "gh"), []byte(ghStub), 0o700); err != nil {
		t.Fatal(err)
	}

	run := func(t *testing.T, verdict string) (int, string) {
		t.Helper()
		dir := t.TempDir()
		path := filepath.Join(dir, "verdict")
		if verdict != "" {
			if err := os.WriteFile(path, []byte(verdict), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		cmd := exec.Command("bash", script, "99999")
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"PATH="+stub+string(os.PathListSeparator)+os.Getenv("PATH"),
			"PRMERGE_VERDICT="+path)
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		runErr := cmd.Run()
		code := 0
		if runErr != nil {
			var ee *exec.ExitError
			if !errors.As(runErr, &ee) {
				t.Fatalf("running prmerge: %v\n%s", runErr, out.String())
			}
			code = ee.ExitCode()
		}
		return code, out.String()
	}

	for _, a := range []struct {
		name    string
		verdict string
		want    string
	}{
		{
			// First because it is the arm that could most easily pass unnoticed: the file says exit=0,
			// which is what a reader checks, and says it about a different tree.
			name:    "a_verdict_for_another_commit_is_refused",
			verdict: "exit=0\nsha=0000000000000000000000000000000000000000\ndirty=no\nwhen=2026-10-01T00:00:00Z\n",
			want:    "the local tip",
		},
		{
			name:    "a_red_verdict_is_refused",
			verdict: "exit=2\nsha=" + head + "\ndirty=no\nwhen=2026-10-01T00:00:00Z\n",
			want:    "recorded exit is 2",
		},
		{
			name:    "a_missing_verdict_is_refused",
			verdict: "",
			want:    "run `make ci`",
		},
		{
			// A green that was taken over uncommitted changes is about neither the commit it names nor
			// anything mergeable, and it is the state the working order most easily produces.
			name:    "a_verdict_taken_on_a_dirty_tree_is_refused",
			verdict: "exit=0\nsha=" + head + "\ndirty=yes\nwhen=2026-10-01T00:00:00Z\n",
			want:    "DIRTY",
		},
		{
			// A field the script cannot parse must not read as a pass. `exit=` absent is the realistic
			// shape, since the file is written by a shell that could fail mid-write.
			name:    "a_verdict_with_no_exit_field_is_refused",
			verdict: "sha=" + head + "\ndirty=no\n",
			want:    "not 0",
		},
	} {
		t.Run(a.name, func(t *testing.T) {
			code, out := run(t, a.verdict)
			if code == 0 {
				t.Fatalf("prmerge exited 0 — it would have merged:\n%s", out)
			}
			if code == 97 || strings.Contains(out, "STUB-GH") {
				t.Fatalf("a merge was ATTEMPTED, so the verdict check did not fire before the "+
					"irreversible step:\n%s", out)
			}
			if !strings.Contains(out, a.want) {
				t.Errorf("the refusal does not mention %q, so this arm may be passing on an unrelated\n"+
					"failure — any of these commands exits non-zero for several reasons:\n%s", a.want, out)
			}
		})
	}

	// The complement, without which every arm above could be satisfied by a script that refuses
	// everything: a green verdict naming this exact commit must get PAST the verdict block. It is
	// recognised by the script's own line, not by an exit code, because the stub stops the merge after.
	t.Run("a_green_verdict_for_this_commit_passes_the_check", func(t *testing.T) {
		_, out := run(t, fmt.Sprintf("exit=0\nsha=%s\ndirty=no\nwhen=2026-10-01T00:00:00Z\n", head))
		if !strings.Contains(out, "the local gate is green on this exact commit") {
			t.Errorf("a green verdict for the current tip did not pass the verdict block, so the four\n"+
				"refusals above prove nothing about discrimination:\n%s", out)
		}
	})
}
