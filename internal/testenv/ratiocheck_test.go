// Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0

package testenv_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestRatioCheckRefusesAFigureCarriedFromAnotherPR is the control for `scripts/ratiocheck.sh`, ordered by
// the chair's ruling of 2026-10-17 after the third ratio slip in one cycle: #1039's body carried #1038's
// figure, because a blocked command left an old substitution file for the next run to read.
//
// **The subject is a two-commit repository built here**, not this repo's history, so the figure is known
// in advance and the test does not depend on how deep CI's checkout is. The head adds 5 engine lines, 3
// instrument lines and 2 other lines, so `ratio.sh` reads engine 5 / instrument 3 (other 2). The tracker
// is a `gh` shim that answers the PR's base, head and body.
//
// **The ruling's neuter is the `another_prs_figure_fails` arm**: a body quoting #1039's figure for this
// head must fail. **Neuter of the check itself, run:** a comparison that always matches fails that arm
// and nothing else.
func TestRatioCheckRefusesAFigureCarriedFromAnotherPR(t *testing.T) {
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(repo, "scripts", "ratiocheck.sh")

	subject := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = subject
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t",
			"GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(subject, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q")
	write("README", "base\n")
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	base := git("rev-parse", "HEAD")
	git("update-ref", "refs/remotes/origin/main", base)
	write("internal/x/a.go", "package x\n\n// A is one.\nfunc A() int { return 1 }\n// end\n")
	write("internal/x/a_test.go", "package x\n\n// test\n")
	write("doc.txt", "one\ntwo\n")
	git("add", "-A")
	git("commit", "-q", "-m", "head\n\nRatio-Class: carried")
	head := git("rev-parse", "HEAD")

	run := func(t *testing.T, shim string, env []string, args ...string) (string, int) {
		t.Helper()
		bin := t.TempDir()
		if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(shim), 0o755); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("sh", append([]string{script}, args...)...)
		cmd.Dir = subject
		cmd.Env = append(append(os.Environ(), env...), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		out, err := cmd.CombinedOutput()
		code := 0
		if err != nil {
			var ee *exec.ExitError
			if !errors.As(err, &ee) {
				t.Fatalf("ratiocheck.sh could not be run at all (%v), which is not a non-zero exit:\n%s", err, out)
			}
			code = ee.ExitCode()
		}
		return string(out), code
	}
	// prShim answers the two `gh api …/pulls/N` queries the check makes: the SHAs, then the body.
	const prShim = "#!/bin/sh\ncase \"$*\" in *base.sha*) printf '%s\\n%s\\n' \"$SHIM_BASE\" \"$SHIM_HEAD\";; " +
		"*body*) cat \"$SHIM_BODY\";; *) exit 1;; esac\n"
	const failShim = "#!/bin/sh\necho 'shim: forced transport failure' >&2\nexit 1\n"
	bodyFile := func(t *testing.T, body string) string {
		t.Helper()
		p := filepath.Join(t.TempDir(), "body.md")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	checkPR := func(t *testing.T, body string) (string, int) {
		t.Helper()
		return run(t, prShim, []string{"SHIM_BASE=" + base, "SHIM_HEAD=" + head, "SHIM_BODY=" + bodyFile(t, body)},
			"--pr", "1")
	}

	t.Run("fill_computes_and_substitutes_in_one_step", func(t *testing.T) {
		p := bodyFile(t, "## Board\n\n- Ratio: RATIO\n")
		out, code := run(t, failShim, nil, "fill", p)
		if code != 0 {
			t.Fatalf("fill exited %d:\n%s", code, out)
		}
		got, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"engine 5 / instrument 3", "(other 2)", "all `Ratio-Class: carried`"} {
			if !strings.Contains(string(got), want) {
				t.Errorf("the filled body does not carry %q:\n%s", want, got)
			}
		}
		if strings.Contains(string(got), "RATIO") {
			t.Errorf("the token survived the fill:\n%s", got)
		}
	})

	for name, body := range map[string]string{
		"fill_refuses_no_token":   "- Ratio: nothing to fill\n",
		"fill_refuses_two_tokens": "- Ratio: RATIO\n- Ratio: RATIO\n",
	} {
		t.Run(name, func(t *testing.T) {
			p := bodyFile(t, body)
			if out, code := run(t, failShim, nil, "fill", p); code == 0 {
				t.Fatalf("fill exited 0 on a body it should refuse:\n%s", out)
			}
		})
	}

	t.Run("the_computed_figure_passes", func(t *testing.T) {
		out, code := checkPR(t, "## Board\n\n- Ratio: engine 5 / instrument 3 = 1:0.6 (other 2), all carried.\n")
		if code != 0 || !strings.Contains(out, "equals ratio.sh") {
			t.Fatalf("exit %d; want 0 for the figure ratio.sh computes for this head:\n%s", code, out)
		}
	})

	t.Run("another_prs_figure_fails", func(t *testing.T) {
		// #1039's figure, quoted for a head it does not describe: the slip this control exists for.
		out, code := checkPR(t, "- Ratio: engine 0 / instrument 132 = 1:inf (no engine lines) (other 131)\n")
		if code == 0 || !strings.Contains(out, "gives 5/3/2") {
			t.Fatalf("exit %d; want a failure naming the computed 5/3/2:\n%s", code, out)
		}
	})

	for name, body := range map[string]string{
		"no_ratio_line_fails":    "## Board\n\nnothing quoted\n",
		"two_ratio_lines_fail":   "- Ratio: engine 5 / instrument 3 (other 2)\n- Ratio: engine 5 / instrument 3 (other 2)\n",
		"an_unfilled_line_fails": "- Ratio: RATIO\n",
	} {
		t.Run(name, func(t *testing.T) {
			if out, code := checkPR(t, body); code == 0 {
				t.Fatalf("exit 0 on a body that should be refused:\n%s", out)
			}
		})
	}

	// The no-argument mode, which `make ci` runs. Its first run went red on an unpushed HEAD: GitHub's
	// "No commit found for SHA" is an answer (no PR can have that head), and it was scored as a failed fetch.
	t.Run("no_args_an_unpushed_head_has_no_pr", func(t *testing.T) {
		const unpushed = "#!/bin/sh\necho 'gh: No commit found for SHA: abc (HTTP 422)' >&2\nexit 1\n"
		out, code := run(t, unpushed, nil)
		if code != 0 || !strings.Contains(out, "is not on the remote") {
			t.Fatalf("exit %d; want 0, saying HEAD is not on the remote:\n%s", code, out)
		}
	})
	t.Run("no_args_a_failed_lookup_is_not_a_pass", func(t *testing.T) {
		if out, code := run(t, failShim, nil); code == 0 || !strings.Contains(out, "could not ask the tracker") {
			t.Fatalf("exit %d; want non-zero, saying the tracker could not be asked:\n%s", code, out)
		}
	})
	t.Run("no_args_finds_and_checks_the_open_pr", func(t *testing.T) {
		// The lookup answers PR 1, then the PR queries answer a body carrying another PR's figure, so a
		// found PR is checked rather than merely found.
		shim := "#!/bin/sh\ncase \"$*\" in *commits/*) echo 1;; *base.sha*) printf '%s\\n%s\\n' \"$SHIM_BASE\" \"$SHIM_HEAD\";; " +
			"*body*) cat \"$SHIM_BODY\";; *) exit 1;; esac\n"
		out, code := run(t, shim, []string{
			"SHIM_BASE=" + base, "SHIM_HEAD=" + head,
			"SHIM_BODY=" + bodyFile(t, "- Ratio: engine 0 / instrument 132 (other 131)\n"),
		})
		if code == 0 || !strings.Contains(out, "gives 5/3/2") {
			t.Fatalf("exit %d; want the found PR checked and failed on another PR's figure:\n%s", code, out)
		}
	})

	t.Run("a_failed_fetch_is_not_a_pass", func(t *testing.T) {
		out, code := run(t, failShim, nil, "--pr", "1")
		if code == 0 || !strings.Contains(out, "could not be fetched") || strings.Contains(out, "equals") {
			t.Fatalf("exit %d; want non-zero, saying the body could not be fetched:\n%s", code, out)
		}
	})
}
