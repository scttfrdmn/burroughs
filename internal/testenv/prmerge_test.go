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

// TestCIVerdictRecordsTheTreeItActuallyRanOn witnesses the PRODUCER of `.ci-verdict`.
//
// # Why this exists separately from the consumer's witness
//
// The consumer's witness below went green against hand-written fixture files while the real producer was
// broken. The verdict was first written inline in the Makefile, where a double-quoted recipe line passes
// *literal* backslash-quote characters into `test -n` — a non-empty string — so `dirty` was **always** `yes`.
// `prmerge.sh` would have refused every merge, and a check that always refuses gets deleted rather than
// debugged. The defect was found by reading a real `.ci-verdict` that said `dirty=yes` on a clean tree.
//
// **A control that tests the helper is not testing the path.** Fixtures prove the consumer reads fields; only
// running the producer proves the fields are true. That is also why the writing moved out of the Makefile: a
// recipe cannot be driven from a witness with a controlled tree state, and a script can.
func TestCIVerdictRecordsTheTreeItActuallyRanOn(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "scripts", "civerdict.sh")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("civerdict.sh is missing, so this witness has no subject: %v", err)
	}

	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = repo
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("config", "user.email", "witness@example.invalid")
	git("config", "user.name", "witness")
	git("commit", "-q", "--allow-empty", "-m", "base")

	headOut, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(string(headOut))

	write := func(rc string) map[string]string {
		t.Helper()
		out := filepath.Join(t.TempDir(), "v")
		c := exec.Command("bash", script, rc, out)
		c.Dir = repo
		if combined, err := c.CombinedOutput(); err != nil {
			t.Fatalf("civerdict.sh %s: %v\n%s", rc, err, combined)
		}
		raw, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		fields := map[string]string{}
		for _, ln := range strings.Split(string(raw), "\n") {
			if k, v, ok := strings.Cut(ln, "="); ok {
				fields[k] = v
			}
		}
		return fields
	}

	t.Run("a_clean_tree_records_dirty_no", func(t *testing.T) {
		got := write("0")
		// This is the arm the Makefile version failed, and it failed by always reporting the SAFE-SOUNDING
		// value — `yes` — which is why nothing downstream looked wrong until a merge was attempted.
		if got["dirty"] != "no" {
			t.Errorf("dirty=%q on a clean tree, want no: prmerge refuses a dirty verdict, so an always-yes\n"+
				"writer refuses every merge: %v", got["dirty"], got)
		}
		if got["exit"] != "0" {
			t.Errorf("exit=%q, want 0: %v", got["exit"], got)
		}
		if got["sha"] != head {
			t.Errorf("sha=%q, want the repo's HEAD %q — the SHA is what makes the file a verdict about\n"+
				"something rather than a mood", got["sha"], head)
		}
		if got["when"] == "" {
			t.Errorf("no timestamp recorded: %v", got)
		}
	})

	t.Run("a_dirty_tree_records_dirty_yes", func(t *testing.T) {
		// The complement, without which the arm above is satisfied by a writer hard-coding `no` — which is
		// the same defect as hard-coding `yes`, pointed the other way and far more dangerous.
		if err := os.WriteFile(filepath.Join(repo, "untracked.txt"), []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		got := write("0")
		if got["dirty"] != "yes" {
			t.Errorf("dirty=%q with an untracked file present, want yes: a gate that passed with "+
				"uncommitted work says nothing about the commit without it: %v", got["dirty"], got)
		}
	})

	t.Run("a_nonzero_code_is_recorded_as_given", func(t *testing.T) {
		if got := write("2"); got["exit"] != "2" {
			t.Errorf("exit=%q, want 2: %v", got["exit"], got)
		}
	})
}

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
# A stub ` + "`gh`" + `: answers the one query prmerge makes before the verdict block, and REFUSES to merge.
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
