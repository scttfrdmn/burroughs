// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package testenv

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestPreCommitRefusesCommitsOnTheDefaultBranch witnesses `.githooks/pre-commit`.
//
// # The specimen
//
// After one PR merged, the next slice's two commits went onto `main` directly — not from disagreeing with the
// rule but because the `git checkout -b` step never happened. They were recovered by branching at the commits
// and resetting `main`, so nothing was lost; what made that possible was *noticing*, and noticing was luck.
//
// # Both arms, and why the allow arm is not optional
//
// A hook that refused every commit would pass the refusal arm and be deleted within the hour. The feature-branch
// arm is what makes the *branch* the subject rather than committing itself.
func TestPreCommitRefusesCommitsOnTheDefaultBranch(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(root, ".githooks", "pre-commit")
	info, statErr := os.Stat(hook)
	if statErr != nil {
		t.Fatalf(".githooks/pre-commit is missing, so this witness has no subject: %v", statErr)
	}
	// The hook is useless unmarked-executable, and `git add` of an editor-created file records 100644 — the
	// same defect that left a verdict writer dead with `Permission denied`.
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf(".githooks/pre-commit is not executable (%v); git will not run it", info.Mode().Perm())
	}

	// **Only `pre-commit` is installed, not the whole `.githooks` directory.** Pointing `core.hooksPath` at
	// the real directory also activates `commit-msg`, which runs `./scripts/spacecheck.sh` — a path relative
	// to the working directory, absent in a temp repo — so every commit failed for a reason that had nothing
	// to do with the subject. The refusal arm still passed, because `pre-commit` fires first; the ALLOW arms
	// died on the other hook, which is a confound that makes a passing refusal arm meaningless on its own.
	hooksDir := t.TempDir()
	hookSrc, readErr := os.ReadFile(hook)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if wErr := os.WriteFile(filepath.Join(hooksDir, "pre-commit"), hookSrc, 0o700); wErr != nil {
		t.Fatal(wErr)
	}

	// Its own repo, so the arms do not depend on this checkout's branch or on its hooks being installed.
	repo := t.TempDir()
	git := func(args ...string) (string, error) {
		c := exec.Command("git", args...)
		c.Dir = repo
		out, runErr := c.CombinedOutput()
		return string(out), runErr
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "witness@example.invalid"},
		{"config", "user.name", "witness"},
		{"config", "burroughs.defaultBranch", "main"},
		// The base commit is made BEFORE core.hooksPath is set, deliberately: with the hook already
		// active this very commit is on `main` and the hook refuses it, which is correct behaviour
		// breaking the setup. The first version had the order wrong and the arm died on its own
		// fixture — the hook working, the witness unable to start.
		{"commit", "-q", "--allow-empty", "-m", "base"},
		{"config", "core.hooksPath", hooksDir},
	} {
		if out, gitErr := git(args...); gitErr != nil {
			t.Fatalf("setup `git %v`: %v\n%s", args, gitErr, out)
		}
	}

	write := func(name string) {
		t.Helper()
		if wErr := os.WriteFile(filepath.Join(repo, name), []byte("x\n"), 0o600); wErr != nil {
			t.Fatal(wErr)
		}
		if out, gitErr := git("add", name); gitErr != nil {
			t.Fatalf("git add: %v\n%s", gitErr, out)
		}
	}

	t.Run("a_commit_on_main_is_refused", func(t *testing.T) {
		write("a.txt")
		out, commitErr := git("commit", "-q", "-m", "on main")
		if commitErr == nil {
			t.Fatalf("the commit on main SUCCEEDED; the hook did not fire:\n%s", out)
		}
		// The refusal must say how to proceed AND how to recover, because the realistic reader is someone who
		// has already committed here — which is how the specimen happened.
		for _, want := range []string{"REFUSED", "git checkout -b", "git branch <topic>", "reset --hard"} {
			if !strings.Contains(out, want) {
				t.Errorf("the refusal does not mention %q:\n%s", want, out)
			}
		}
	})

	t.Run("a_commit_on_a_feature_branch_is_allowed", func(t *testing.T) {
		if out, gitErr := git("checkout", "-q", "-b", "topic"); gitErr != nil {
			t.Fatalf("checkout -b: %v\n%s", gitErr, out)
		}
		write("b.txt")
		if out, gitErr := git("commit", "-q", "-m", "on a branch"); gitErr != nil {
			t.Fatalf("a commit on a feature branch was REFUSED, which would make the hook unusable: %v\n%s",
				gitErr, out)
		}
	})

	t.Run("a_detached_head_is_not_the_subject", func(t *testing.T) {
		// A rebase and a bisect both run detached. Refusing there would break them, and an empty branch name
		// compared against "main" would pass by accident rather than by decision — hence the explicit arm.
		head, _ := git("rev-parse", "HEAD")
		if out, gitErr := git("checkout", "-q", "--detach", strings.TrimSpace(head)); gitErr != nil {
			t.Fatalf("detach: %v\n%s", gitErr, out)
		}
		write("c.txt")
		if out, gitErr := git("commit", "-q", "-m", "detached"); gitErr != nil {
			t.Errorf("a commit on a DETACHED HEAD was refused; rebase and bisect run there: %v\n%s",
				gitErr, out)
		}
	})
}

// TestPrmergeDeletesTheLocalBranch witnesses the step `gh pr merge --delete-branch` does not perform.
//
// # Why this exists
//
// Measured on four consecutive merges: `--delete-branch` removed the remote branch and left the local one every
// time. A surviving merged local ref is not inert — `git push -u origin <that-branch>`, copied forward from the
// previous slice, **recreated a branch prmerge had just deleted**, at a commit already squashed into main.
//
// # Scope
//
// This drives the post-merge block with a stub `gh` that reports a successful merge without performing one, so
// the arm is about the local ref and nothing reaches the network.
func TestPrmergeDeletesTheLocalBranch(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "scripts", "prmerge.sh")
	if _, statErr := os.Stat(script); statErr != nil {
		t.Fatalf("prmerge.sh is missing: %v", statErr)
	}

	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = repo
		if out, gitErr := c.CombinedOutput(); gitErr != nil {
			t.Fatalf("git %v: %v\n%s", args, gitErr, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("config", "user.email", "witness@example.invalid")
	git("config", "user.name", "witness")
	git("commit", "-q", "--allow-empty", "-m", "base")
	git("checkout", "-q", "-b", "topic")
	git("commit", "-q", "--allow-empty", "-m", "work")

	head := func(ref string) string {
		c := exec.Command("git", "rev-parse", ref)
		c.Dir = repo
		out, _ := c.Output()
		return strings.TrimSpace(string(out))
	}
	// Existence is read from the EXIT STATUS, never from stdout. `git rev-parse refs/heads/gone` prints the
	// ref NAME to stdout and exits non-zero, so a stdout-only check reads a deleted branch as present — which
	// is how the first version of this arm reported SURVIVED while prmerge's own line said "verified absent".
	// Two channels disagreed and the script's was right.
	refExists := func(ref string) bool {
		c := exec.Command("git", "show-ref", "--verify", "--quiet", ref)
		c.Dir = repo
		return c.Run() == nil
	}
	topicHead := head("refs/heads/topic")

	// A stub `gh`: answers the metadata query with the topic branch at its real head, and reports the merge as
	// successful without doing anything. The subject is what prmerge does AFTER a successful merge.
	stub := t.TempDir()
	ghStub := "#!/usr/bin/env bash\n" +
		"case \"$*\" in\n" +
		"  *\" view \"*) echo '{\"headRefName\":\"topic\",\"headRefOid\":\"" + topicHead +
		"\",\"state\":\"OPEN\",\"mergeable\":\"MERGEABLE\"}' ;;\n" +
		"  *merge*) echo 'stub: merged' ;;\n" +
		"esac\n"
	if wErr := os.WriteFile(filepath.Join(stub, "gh"), []byte(ghStub), 0o700); wErr != nil {
		t.Fatal(wErr)
	}

	// A green verdict for the topic head, so the gate check passes and execution reaches the merge.
	verdict := filepath.Join(t.TempDir(), "v")
	if wErr := os.WriteFile(verdict,
		[]byte("exit=0\nsha="+topicHead+"\ndirty=no\nwhen=2026-10-02T00:00:00Z\n"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}

	cmd := exec.Command("bash", script, "1")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(),
		"PATH="+stub+string(os.PathListSeparator)+os.Getenv("PATH"),
		"PRMERGE_VERDICT="+verdict)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if runErr := cmd.Run(); runErr != nil {
		var ee *exec.ExitError
		if !errors.As(runErr, &ee) {
			t.Fatalf("running prmerge: %v\n%s", runErr, out.String())
		}
		t.Fatalf("prmerge exited %d before reaching the branch deletion:\n%s", ee.ExitCode(), out.String())
	}

	if refExists("refs/heads/topic") {
		t.Errorf("the local branch `topic` SURVIVED the merge — which is the ref a copied-forward "+
			"`git push -u origin topic` revives, recreating a branch the merge deleted:\n%s", out.String())
	}
	// The absence must be REPORTED, not merely true: a silent deletion is indistinguishable from a deletion
	// that did not happen, which is how this went unnoticed for four merges.
	if !strings.Contains(out.String(), "verified absent") {
		t.Errorf("prmerge did not report that it verified the local branch was gone:\n%s", out.String())
	}

	// --- the force-delete's own precondition ---------------------------------------------------------------
	//
	// **`-D` is safe because the SHA is re-checked at the point of deletion, not because `gh pr merge`
	// succeeded.** Step 2 established local-tip == PR-head three steps earlier, and nothing tied that to the
	// delete; if step 2 were ever loosened, a bare `-D` would start destroying local-only commits silently.
	//
	// The branch has to MATCH at step 2 and DIVERGE by the deletion, so the stub commits during the merge.
	// That is the real hazard rather than a contrived one: a hook or a concurrent session does exactly that.
	t.Run("a_branch_that_moved_during_the_merge_is_not_deleted", func(t *testing.T) {
		git("checkout", "-q", "-b", "topic2")
		git("commit", "-q", "--allow-empty", "-m", "work2")
		t2 := head("refs/heads/topic2")

		stub2 := t.TempDir()
		ghStub2 := "#!/usr/bin/env bash\n" +
			"case \"$*\" in\n" +
			"  *\" view \"*) echo '{\"headRefName\":\"topic2\",\"headRefOid\":\"" + t2 +
			"\",\"state\":\"OPEN\",\"mergeable\":\"MERGEABLE\"}' ;;\n" +
			"  *merge*) echo 'stub: merged'; git commit -q --allow-empty -m 'landed during the merge' ;;\n" +
			"esac\n"
		if wErr := os.WriteFile(filepath.Join(stub2, "gh"), []byte(ghStub2), 0o700); wErr != nil {
			t.Fatal(wErr)
		}
		v2 := filepath.Join(t.TempDir(), "v")
		if wErr := os.WriteFile(v2,
			[]byte("exit=0\nsha="+t2+"\ndirty=no\nwhen=2026-10-02T00:00:00Z\n"), 0o600); wErr != nil {
			t.Fatal(wErr)
		}

		c := exec.Command("bash", script, "1")
		c.Dir = repo
		c.Env = append(os.Environ(),
			"PATH="+stub2+string(os.PathListSeparator)+os.Getenv("PATH"),
			"PRMERGE_VERDICT="+v2)
		var o bytes.Buffer
		c.Stdout = &o
		c.Stderr = &o
		_ = c.Run()

		if !refExists("refs/heads/topic2") {
			t.Errorf("the branch was DELETED after moving during the merge — a force-delete whose "+
				"precondition was established three steps earlier and never re-checked:\n%s", o.String())
		}
		for _, want := range []string{"NOT deleting local topic2", "landed during the merge"} {
			if !strings.Contains(o.String(), want) {
				t.Errorf("the refusal does not mention %q — it must name the local-only commits, or a "+
					"reader cannot tell what would have been lost:\n%s", want, o.String())
			}
		}
	})
}
