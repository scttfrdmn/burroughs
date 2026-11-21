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

// TestEditRouteRefusesABashEditOfAFileAnotherWorktreeTracks is #1315's witness. The hook's tracked set is
// the files of CLAUDE_PROJECT_DIR, the main checkout, so a file tracked only in another worktree of the
// same repository was never "tracked", and the Bash-edit routes refused nothing in one. Every slice agent
// works in such a worktree (~/.xtmp/wt-*). A `sed -i` on a tracked test file in one ran unrefused, and was
// caught only because the agent reported it (#1281's slice, 2026-11-20).
//
// The repository here is a throwaway: a main checkout holding a commit, and a worktree of it holding a
// second file on its own branch, which the main checkout's tree does not have. The hook runs with the
// main checkout as both its project directory and its cwd, as an agent's hook does, and is asked about
// three files:
//   - **the worktree's tracked file:** refused. This is the repair;
//   - **an untracked file in the same worktree:** allowed, since a file nobody tracks is the shell's to write;
//   - **a file another repository tracks:** allowed. The fork is that repository's business (see
//     tracked_in_a_worktree's doc).
//
// NEUTER: tracked_in_a_worktree returning False, and the first row is allowed: the hook as it was.
func TestEditRouteRefusesABashEditOfAFileAnotherWorktreeTracks(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(root, "scripts", "editroute.py")

	base := t.TempDir()
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	mainCO := filepath.Join(base, "main")
	if err := os.MkdirAll(mainCO, 0o755); err != nil {
		t.Fatal(err)
	}
	git(mainCO, "init", "-q", "-b", "main")
	write(filepath.Join(mainCO, "a.txt"), "a\n")
	git(mainCO, "add", "a.txt")
	git(mainCO, "commit", "-q", "-m", "a")

	wt := filepath.Join(base, "wt")
	git(mainCO, "worktree", "add", "-q", "-b", "side", wt)
	write(filepath.Join(wt, "only-here.txt"), "x\n")
	git(wt, "add", "only-here.txt")
	git(wt, "commit", "-q", "-m", "only here")
	write(filepath.Join(wt, "untracked.txt"), "u\n")

	other := filepath.Join(base, "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	git(other, "init", "-q", "-b", "main")
	write(filepath.Join(other, "theirs.txt"), "t\n")
	git(other, "add", "theirs.txt")
	git(other, "commit", "-q", "-m", "t")

	for _, c := range []struct {
		name, target string
		deny         bool
	}{
		{"a file the other worktree tracks", filepath.Join(wt, "only-here.txt"), true},
		{"an untracked file in that worktree", filepath.Join(wt, "untracked.txt"), false},
		{"a file another repository tracks", filepath.Join(other, "theirs.txt"), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			payload, err := json.Marshal(map[string]any{
				"hook_event_name": "PreToolUse",
				"tool_name":       "Bash",
				"cwd":             mainCO,
				"tool_input":      map[string]any{"command": "sed -i.bak 's/x/y/' '" + c.target + "'"},
			})
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("python3", hook)
			cmd.Dir = mainCO
			cmd.Stdin = bytes.NewReader(payload)
			// The log is redirected wherever the hook is invoked (grave #852; editroute_test.go).
			cmd.Env = append(os.Environ(),
				"CLAUDE_PROJECT_DIR="+mainCO,
				"EDITROUTE_LOG="+filepath.Join(t.TempDir(), "refusals"),
			)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			code := 0
			if runErr := cmd.Run(); runErr != nil {
				var ee *exec.ExitError
				if !errors.As(runErr, &ee) {
					t.Fatalf("running the hook failed: %v", runErr)
				}
				code = ee.ExitCode()
			}
			if denied := code == 2; denied != c.deny {
				t.Fatalf("exit %d (denied=%v), want denied=%v\n\tstderr: %s", code, denied, c.deny, stderr.String())
			}
			if c.deny && !strings.Contains(stderr.String(), "REFUSED") {
				t.Errorf("the refusal does not read as one: %s", stderr.String())
			}
		})
	}
}
