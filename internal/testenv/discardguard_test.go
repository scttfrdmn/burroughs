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

// TestEditRouteRefusesGitCommandsThatDiscardUncommittedWork is the discard guard's witness.
//
// # What it guards, and why it exists
//
// One compound command was `git add -A && git commit -m "wip: baseline" && python3 <<'PY' … PY`. Its
// heredoc wrote a tracked file, so **the hook refused the whole command and nothing in it ran** —
// including the `git commit` at its front. The baseline therefore never existed, and a later
// `git checkout -- scripts/ciwatch.sh`, issued to undo an injection, reverted real work: a poll loop, a
// default, and a fixture mechanism, all uncommitted.
//
// **A hook refusal kills the entire compound command.** Treating a refusal as "the risky part was
// blocked" rather than "nothing happened" is what made the restore look safe. The project already had the
// rule — *an injection battery needs a committed baseline* — cited in the same session and then applied
// without checking the baseline existed, which is why this is a mechanism rather than another sentence.
//
// # Every arm runs the hook the way PRODUCTION does
//
// `CLAUDE_PROJECT_DIR` set to the fixture repo, and the working directory given explicitly per arm.
// **Twice during development a probe that differed from production in one environment variable looked
// exactly like the guard failing** — an exit 0 from a run that never reached the guard's subject, because
// `repo_root()` had fallen back to `git rev-parse` in the hook's own cwd and resolved a different
// repository.
//
// So each refusing arm **asserts the path the refusal names**, not merely that something refused. That
// ties the result to the repository root the hook actually resolved: a fixture that silently falls back to
// the wrong root produces no match, and the arm fails instead of passing. Same principle as asserting a
// refusal arm reached its block.
//
// # Coverage is a LIMIT, not a guarantee
//
// The guard matches **command text**. It sees a direct invocation and does not see a script that runs git
// internally, a shell function, or an alias. "The hook refuses discards" is false as a general claim; "it
// refuses these spellings, typed directly" is true.
func TestEditRouteRefusesGitCommandsThatDiscardUncommittedWork(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(root, "scripts", "editroute.py")
	if _, statErr := os.Stat(hook); statErr != nil {
		t.Fatalf("the hook is missing, so this witness has no subject: %v", statErr)
	}

	// One fixture repo for the whole test: a tracked file dirtied inside a SUBDIRECTORY (so the
	// root-relative status path differs from the path a caller types), an untracked file for `clean -f`,
	// and a committed-clean file for the allowed arm.
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = repo
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		if out, gErr := c.CombinedOutput(); gErr != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), gErr, out)
		}
	}
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(repo, rel)
		if mkErr := os.MkdirAll(filepath.Dir(p), 0o750); mkErr != nil {
			t.Fatal(mkErr)
		}
		if wErr := os.WriteFile(p, []byte(body), 0o600); wErr != nil {
			t.Fatal(wErr)
		}
	}
	git("init", "-q", "-b", "main")
	write("sub/f.go", "a\n")
	write("clean.go", "c\n")
	git("add", "sub/f.go", "clean.go")
	git("commit", "-q", "-m", "base")
	write("sub/f.go", "b\n")      // modified → dirty
	write("sub/untracked", "u\n") // untracked → `clean -f` territory

	notARepo := t.TempDir()

	run := func(t *testing.T, cwd, cmd string, extraEnv ...string) (int, string) {
		t.Helper()
		logPath := filepath.Join(t.TempDir(), "log")
		payload := `{"hook_event_name":"PreToolUse","tool_name":"Bash","cwd":` +
			quote(cwd) + `,"tool_input":{"command":` + quote(cmd) + `}}`
		c := exec.Command("python3", hook)
		c.Dir = root
		c.Stdin = strings.NewReader(payload)
		// **Production shape:** the project dir is the fixture repo, not whatever the hook's own cwd
		// happens to resolve to.
		c.Env = append(os.Environ(), "CLAUDE_PROJECT_DIR="+repo, "EDITROUTE_LOG="+logPath)
		c.Env = append(c.Env, extraEnv...)
		var out bytes.Buffer
		c.Stdout = &out
		c.Stderr = &out
		runErr := c.Run()
		code := 0
		if runErr != nil {
			var ee *exec.ExitError
			if !errors.As(runErr, &ee) {
				t.Fatalf("running the hook: %v\n%s", runErr, out.String())
			}
			code = ee.ExitCode()
		}
		return code, out.String()
	}

	// **Paths that go into a command string are single-quoted, because an unquoted one is not a
	// command anybody types.** On Windows `repo` is a `D:\…` temp path, and bash removes those
	// backslashes as escapes — so `git -C D:\a\b` arrived as `git -C D:ab`, git could not read that
	// directory, and the guard refused with *"could not be checked"* instead of naming the discard.
	// Two arms failed that way on the Windows job.
	//
	// The guard was behaving correctly: it predicts what bash will do, which is the only useful
	// thing for a guard on a bash command to do. The defect was feeding it input no shell would
	// honour. Single quotes are what `shlex.quote` emits for such a path and they survive the hook's
	// POSIX-mode tokeniser intact (chair's ruling, #942 review).
	sq := func(p string) string { return "'" + p + "'" }

	for _, a := range []struct{ name, cwd, cmd, wantPath string }{
		{"checkout_ddash", repo, "git checkout -- sub/f.go", "sub/f.go"},
		{"restore_worktree", repo, "git restore sub/f.go", "sub/f.go"},
		{"reset_hard", repo, "git reset --hard", "sub/f.go"},
		{"clean_f_deletes_untracked", repo, "git clean -fd", "sub/untracked"},
		// Global options before the subcommand: `a[0]` is the option, so an early version matched nothing
		// and the guard was bypassed by the most ordinary scripted spelling there is.
		{"dash_C_repo", repo, "git -C " + sq(repo) + " checkout -- sub/f.go", "sub/f.go"},
		{"dash_c_config", repo, "git -c user.name=x reset --hard", "sub/f.go"},
		// `-C` at a SUBDIRECTORY: `git status --porcelain` reports root-relative paths whatever `-C`
		// names, so a relative path computed against the subdirectory never matched a status line.
		{"dash_C_subdir", repo, "git -C " + sq(filepath.Join(repo, "sub")) + " checkout -- f.go", "sub/f.go"},
		// A bare path typed FROM a subdirectory: the is-this-a-path test must resolve against the shell's
		// cwd, or it reads as a branch name and is let through.
		{"bare_path_from_subdir", filepath.Join(repo, "sub"), "git checkout f.go", "sub/f.go"},
	} {
		t.Run(a.name, func(t *testing.T) {
			code, out := run(t, a.cwd, a.cmd)
			if code == 0 {
				t.Fatalf("ALLOWED — this discards uncommitted work:\n%s", out)
			}
			if !strings.Contains(out, "would DISCARD uncommitted changes") {
				t.Fatalf("refused, but not as a discard:\n%s", out)
			}
			// The named path ties the refusal to the root the hook resolved. A fixture that fell back to
			// the wrong repository would find nothing dirty and allow, so this is what makes the arm about
			// the guard rather than about the environment.
			if !strings.Contains(out, a.wantPath) {
				t.Errorf("the refusal does not name %q, so it may have measured another repository:\n%s",
					a.wantPath, out)
			}
			// And the guidance must be the DISCARD guidance. An early version let this reason fall into
			// the editing bucket, so a refused `git checkout --` was answered with "use str_replace" —
			// advice for a problem the actor does not have.
			if !strings.Contains(out, "git stash") || strings.Contains(out, "subst1.py") {
				t.Errorf("the refusal gives the wrong remedy:\n%s", out)
			}
		})
	}

	t.Run("stash_drop_discards_the_stash", func(t *testing.T) {
		// `git stash` SAVES, so it is allowed; `stash drop` discards. Exercised against a non-empty stash,
		// because with an empty one there is genuinely nothing to lose.
		git("stash", "-q")
		defer func() { _ = exec.Command("git", "-C", repo, "stash", "pop", "-q").Run() }()
		code, out := run(t, repo, "git stash drop")
		if code == 0 {
			t.Fatalf("ALLOWED — dropping a stash entry discards it:\n%s", out)
		}
		if !strings.Contains(out, "<the stash>") {
			t.Errorf("the refusal does not name the stash as its subject:\n%s", out)
		}
		// The remedy must suit the stash, not tell the actor to stash what they are dropping.
		if !strings.Contains(out, "git stash show -p") {
			t.Errorf("the stash refusal gives the generic remedy, which is nonsense here:\n%s", out)
		}
	})

	t.Run("an_unanswerable_git_fails_CLOSED", func(t *testing.T) {
		// The first version returned "nothing dirty" when git could not be asked, so the discard went
		// through — *absence read as permission*, inside the guard built to stop a data loss.
		// Quoted for the reason the table above records; here the path is meant to be unreadable as
		// a repo, but it must be unreadable for THAT reason rather than because bash mangled it.
		code, out := run(t, repo, "git -C '"+notARepo+"' reset --hard")
		if code == 0 {
			t.Fatalf("ALLOWED — the dirty state could not be determined, so this must refuse:\n%s", out)
		}
		if !strings.Contains(out, "could not be checked") {
			t.Errorf("refused, but not as the unanswerable case — so it is reporting a state it did not "+
				"measure:\n%s", out)
		}
	})

	for _, a := range []struct{ name, cwd, cmd string }{
		{"plain_stash_is_allowed_it_saves", repo, "git stash"},
		{"restore_staged_only_unstages", repo, "git restore --staged sub/f.go"},
		{"a_branch_switch_is_not_a_path", repo, "git checkout main"},
		{"a_clean_path_is_allowed", repo, "git checkout -- clean.go"},
	} {
		t.Run(a.name, func(t *testing.T) {
			code, out := run(t, a.cwd, a.cmd)
			if code != 0 {
				t.Fatalf("REFUSED, but this discards nothing — an over-refusing check is the kind that "+
					"gets worked around:\n%s", out)
			}
		})
	}

	t.Run("the_override_is_allowed_and_logged", func(t *testing.T) {
		logPath := filepath.Join(t.TempDir(), "log")
		code, out := run(t, repo, "git checkout -- sub/f.go",
			"EDITROUTE_ALLOW_DISCARD=1", "EDITROUTE_LOG="+logPath)
		if code != 0 {
			t.Fatalf("the override did not permit a deliberate discard:\n%s", out)
		}
		b, rErr := os.ReadFile(logPath)
		if rErr != nil {
			t.Fatalf("the override was not logged, so a deliberate discard leaves no trace: %v", rErr)
		}
		// Its own slug, because "I chose to discard" and "this would discard" are different entries and a
		// later reader most wants to be able to group the first.
		if !strings.Contains(string(b), "discard-override") {
			t.Errorf("the override logged without its slug:\n%s", b)
		}
	})
}

// quote renders a Go string as a JSON string literal for the hook's stdin payload.
func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
