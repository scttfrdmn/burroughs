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

// TestPrmergeRefusesUnlessCIItselfIsGreen is #867's second half.
//
// # The gap this closes
//
// `prmerge.sh` checked the LOCAL `make ci` verdict and never read CI's. So *"merge on a ciwatch-verified
// green"* was **operator discipline, not an enforced precondition**: on #866 a `ciwatch_verdict: fail` was in
// hand and nothing in the tool would have stopped the merge, and nothing would stop a caller who never ran
// `ciwatch.sh` at all. The local gate's green is about this tree; it says nothing about the nine CI runs.
//
// # Six refusals, and green is the only verdict that merges
//
// The `unfinished` arm is the one the slice exists for: the bound expiring is **its own outcome**, so the
// refusal must say "still running" rather than "failing" — a reader re-runs the watch instead of hunting a
// breakage. And `body_stale: null` is a refusal too: `ciwatch` reports null when it was given no PR and
// could not measure body currency, and letting that through would be *absence read as permission* in the
// slice built to remove it.
//
// # Why a temporary repo
//
// `prmerge.sh` refuses a dirty working tree as its first step, so driving it against this repo would couple
// every arm to whatever is uncommitted here — the arms would pass for the wrong reason while a change was in
// flight. A temp repo with one commit makes the tree state an input.
func TestPrmergeRefusesUnlessCIItselfIsGreen(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "scripts", "prmerge.sh")
	if _, statErr := os.Stat(script); statErr != nil {
		t.Fatalf("prmerge.sh is missing, so this witness has no subject: %v", statErr)
	}

	// A stub `gh` that answers the one pre-verdict query and REFUSES to merge. A witness for an
	// irreversible step must not be able to perform it: if a check under test stops firing, the stub turns
	// that into a loud failure rather than a real merge.
	stub := t.TempDir()
	ghStub := "#!/usr/bin/env bash\n" +
		"case \"$*\" in\n" +
		"  *view*) echo '{\"headRefName\":\"nosuch\",\"headRefOid\":\"HEADSHA\",\"state\":\"OPEN\",\"mergeable\":\"MERGEABLE\"}' ;;\n" +
		"  *merge*) echo 'STUB-GH: a merge was attempted, which means a check did not fire' >&2; exit 97 ;;\n" +
		"esac\n"

	run := func(t *testing.T, ciVerdict string) (int, string) {
		t.Helper()
		repo := t.TempDir()
		git := func(args ...string) {
			t.Helper()
			c := exec.Command("git", args...)
			c.Dir = repo
			c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
				"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
			if out, err := c.CombinedOutput(); err != nil {
				t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
			}
		}
		git("init", "-q")
		if err := os.WriteFile(filepath.Join(repo, "f"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		git("add", "f")
		git("commit", "-q", "-m", "base")
		head, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
		if err != nil {
			t.Fatal(err)
		}
		sha := strings.TrimSpace(string(head))

		// The stub reports the PR's head as this commit, so step 2's comparison passes and execution
		// reaches the CI-verdict block under test.
		if err := os.WriteFile(filepath.Join(stub, "gh"),
			[]byte(strings.ReplaceAll(ghStub, "HEADSHA", sha)), 0o700); err != nil {
			t.Fatal(err)
		}
		// **The verdict files live OUTSIDE the repo.** Written inside it they are untracked files, and
		// prmerge's first step refuses a dirty working tree — so every arm refused for that reason and none
		// reached the block under test. The fixture was failing the script's step 1 and reading it as the
		// arm's own refusal.
		side := t.TempDir()
		// A green LOCAL verdict for this exact commit, so step 3 passes.
		local := filepath.Join(side, "local.verdict")
		if err := os.WriteFile(local,
			[]byte("exit=0\nsha="+sha+"\ndirty=no\nwhen=now\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		ciPath := filepath.Join(side, "ci.verdict")
		if ciVerdict != "" {
			body := strings.ReplaceAll(ciVerdict, "HEADSHA", sha)
			if err := os.WriteFile(ciPath, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}

		cmd := exec.Command("bash", script, "99999")
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"PATH="+stub+string(os.PathListSeparator)+os.Getenv("PATH"),
			"PRMERGE_VERDICT="+local,
			// prmerge appends `.verdict`, so the prefix is the path without it.
			"PRMERGE_CIWATCH="+strings.TrimSuffix(ciPath, ".verdict"))
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

	const greenBody = `{"ciwatch_verdict": "green", "sha": "HEADSHA", "body_stale": false}`

	for _, a := range []struct {
		name, verdict, want string
	}{
		{
			name:    "a_fail_verdict_is_refused",
			verdict: `{"ciwatch_verdict": "fail", "sha": "HEADSHA", "body_stale": false}`,
			want:    "CI's verdict for this commit is",
		},
		{
			name:    "a_verdict_for_another_sha_is_refused",
			verdict: `{"ciwatch_verdict": "green", "sha": "0000000000000000000000000000000000000000", "body_stale": false}`,
			want:    "the CI verdict names",
		},
		{
			name:    "no_verdict_is_refused_absence_is_not_permission",
			verdict: "", // the file is never written
			want:    "no CI verdict at",
		},
		{
			name:    "unfinished_is_refused_as_still_running_not_failing",
			verdict: `{"ciwatch_verdict": "unfinished", "sha": "HEADSHA", "body_stale": false}`,
			want:    "CI is STILL RUNNING for this commit",
		},
		{
			name:    "a_stale_body_check_is_refused",
			verdict: `{"ciwatch_verdict": "green", "sha": "HEADSHA", "body_stale": true}`,
			want:    "body check was resolved from a run older than",
		},
		{
			name:    "an_unchecked_body_is_refused_too",
			verdict: `{"ciwatch_verdict": "green", "sha": "HEADSHA", "body_stale": null}`,
			want:    "body currency was NEVER CHECKED",
		},
	} {
		t.Run(a.name, func(t *testing.T) {
			code, out := run(t, a.verdict)
			if code == 0 {
				t.Fatalf("exit 0 — the merge was NOT refused:\n%s", out)
			}
			if code == 97 {
				t.Fatalf("the stub `gh` was asked to merge, so the check did not fire:\n%s", out)
			}
			// **Execution must have REACHED the block under test**, proved by step 3's success line.
			//
			// This is the repair for a vacuity that hit all seven arms at once: the fixture first wrote its
			// verdict files inside the temp repo, so prmerge's step 1 refused a dirty tree and every arm
			// recorded a refusal without ever reaching step 4. A non-zero exit cannot tell "refused for my
			// reason" from "refused earlier", and **a new precondition makes every test that drives past it
			// refuse for the new reason while still looking like it tested the old thing.**
			//
			// Asserting the predecessor PASSED is what closes that, and it closes it for arms added later
			// too — a message match alone would have to be re-tightened every time an earlier step grows a
			// new refusal.
			if !strings.Contains(out, "the local gate is green on this exact commit") {
				t.Fatalf("prmerge refused BEFORE the CI-verdict block, so this arm proves nothing about it:\n%s", out)
			}
			if !strings.Contains(out, a.want) {
				t.Errorf("refused, but not for the registered reason (want %q):\n%s", a.want, out)
			}
		})
	}

	// The permit path, which is what keeps the six above from being satisfied by a script that refuses
	// everything. It must reach the merge — proved by the stub's exit 97, since the stub is the only thing
	// standing between this test and an irreversible step.
	t.Run("green_with_a_measured_current_body_reaches_the_merge", func(t *testing.T) {
		code, out := run(t, greenBody)
		if code != 97 {
			t.Fatalf("exit %d, want 97 (the stub refusing the merge) — a green CI verdict with a measured "+
				"current body must reach the merge, or the six refusals above prove nothing:\n%s", code, out)
		}
		if !strings.Contains(out, "body check measured and current") {
			t.Errorf("the green path did not report the body check as measured:\n%s", out)
		}
	})
}
