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

// TestRefusalLogRecordsOneLinePerRefusal witnesses the edit-route hook's refusal log.
//
// # Why the log exists
//
// The hook had refused ten times when this landed and **three were false positives**, all three on its own
// author; it has been narrowed three times as a result. That rate is what decides whether the mechanism
// survives — **an over-refusing check is the kind that gets deleted** — and it was being counted from memory,
// which this project does not accept for a figure that decides something.
//
// **Those figures are recalled and the log could not have produced them**, because it did not exist until the
// slice that counted them — and for that slice's first day the log's dominant writer was the hook's own test
// suite (grave #852: `editroute_test.go` drove the hook without redirecting `EDITROUTE_LOG`, reaching 137
// entries of which 2 were real). `TestMain` in `refusallogguard_test.go` now fails the package if any test
// here modifies the production log, which is the guard this test's own `EDITROUTE_LOG` override was not: the
// override protected the artifact from the test *about* it, not from the test that generates the refusals.
//
// # What these arms assert, and what they cannot
//
// They assert the log's *mechanics*: one line per refusal, nothing on an allowed command, a rule slug and a
// hash. They cannot assert the false-positive **rate**, because whether a refusal was wrong is a judgement
// about what the author meant. That classification is a deliberate step at a tooling decision, and the log
// holds a hash rather than the command precisely so nothing here can pretend otherwise.
func TestRefusalLogRecordsOneLinePerRefusal(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(root, "scripts", "editroute.py")
	if _, statErr := os.Stat(hook); statErr != nil {
		t.Fatalf("the hook is missing, so this witness has no subject: %v", statErr)
	}

	// `EDITROUTE_LOG` redirects the log so the arms never append to the real `.editroute-log` — a witness
	// that writes to the artefact it measures would corrupt the very rate this exists to establish.
	//
	// **Not by redirecting CLAUDE_PROJECT_DIR**, which the first version did: the repo root is what
	// `git ls-files` is asked about, so a temp root made NOTHING tracked and every tracked-file route
	// silently stopped refusing. Four arms failed and two passed — the two that do not consult tracked state
	// — which is exactly the shape of a fixture breaking the thing it is pointed at.
	logPath := filepath.Join(t.TempDir(), "refusals")

	run := func(t *testing.T, cmd string) (int, string) {
		t.Helper()
		payload, mErr := json.Marshal(map[string]any{
			"hook_event_name": "PreToolUse",
			"tool_name":       "Bash",
			"cwd":             root,
			"tool_input":      map[string]any{"command": cmd},
		})
		if mErr != nil {
			t.Fatal(mErr)
		}
		c := exec.Command("python3", hook)
		c.Dir = root
		c.Stdin = bytes.NewReader(payload)
		c.Env = append(os.Environ(), "EDITROUTE_LOG="+logPath)
		var errBuf bytes.Buffer
		c.Stderr = &errBuf
		code := 0
		if runErr := c.Run(); runErr != nil {
			var ee *exec.ExitError
			if errors.As(runErr, &ee) {
				code = ee.ExitCode()
			} else {
				t.Fatalf("running the hook: %v", runErr)
			}
		}
		return code, errBuf.String()
	}

	lines := func() []string {
		b, readErr := os.ReadFile(logPath)
		if readErr != nil {
			return nil
		}
		var out []string
		for _, ln := range strings.Split(string(b), "\n") {
			if strings.TrimSpace(ln) != "" {
				out = append(out, ln)
			}
		}
		return out
	}

	t.Run("an_allowed_command_writes_nothing", func(t *testing.T) {
		// First, because a log that is written on every command cannot measure refusals — and this arm
		// would pass trivially if it ran after one that created the file.
		if code, out := run(t, "go test ./... > /tmp/x.log 2>&1"); code != 0 {
			t.Fatalf("an allowed command was refused: exit %d\n%s", code, out)
		}
		if n := len(lines()); n != 0 {
			t.Errorf("an allowed command wrote %d log line(s); the log must hold refusals only", n)
		}
	})

	t.Run("a_refusal_writes_exactly_one_line", func(t *testing.T) {
		code, out := run(t, "echo hi >> CHANGELOG.md")
		if code != 2 {
			t.Fatalf("expected a refusal (exit 2), got %d:\n%s", code, out)
		}
		got := lines()
		if len(got) != 1 {
			t.Fatalf("a refusal wrote %d line(s), want exactly 1: %v", len(got), got)
		}
		f := strings.Split(got[0], "\t")
		if len(f) != 3 {
			t.Fatalf("the line is not three tab-separated fields: %q", got[0])
		}
		if !strings.HasSuffix(f[0], "Z") || len(f[0]) != 20 {
			t.Errorf("field 1 is not a UTC timestamp: %q", f[0])
		}
		if f[1] != "redirect-write" {
			t.Errorf("rule = %q, want redirect-write — the slug is what the report groups by, so a wrong "+
				"one makes the rate unreadable", f[1])
		}
		if len(f[2]) != 12 {
			t.Errorf("field 3 is not a 12-char digest: %q", f[2])
		}
		// The COMMAND must not be in the log. It can carry a path or a secret, and the log is for counting.
		if strings.Contains(got[0], "CHANGELOG.md") {
			t.Errorf("the log contains the command text; it must hold a hash only: %q", got[0])
		}
	})

	t.Run("a_second_refusal_appends_rather_than_replaces", func(t *testing.T) {
		if code, _ := run(t, "sed -i '' 's/a/b/' scripts/ratio.sh"); code != 2 {
			t.Fatal("expected a refusal")
		}
		got := lines()
		if len(got) != 2 {
			t.Fatalf("after a second refusal the log holds %d line(s), want 2 — it must append: %v",
				len(got), got)
		}
		if f := strings.Split(got[1], "\t"); f[1] != "in-place-edit" {
			t.Errorf("second rule = %q, want in-place-edit", f[1])
		}
	})

	t.Run("the_log_path_is_untracked_and_ignored", func(t *testing.T) {
		// The log must never become a tracked file: the hook writes it on every refusal, so a tracked log
		// would make the hook dirty the tree it is guarding — and would then trip its own tracked-file rule.
		c := exec.Command("git", "-C", root, "ls-files", "--error-unmatch", ".editroute-log")
		if c.Run() == nil {
			t.Error(".editroute-log is TRACKED; the hook would dirty the tree on every refusal")
		}
		c2 := exec.Command("git", "-C", root, "check-ignore", "-q", ".editroute-log")
		if c2.Run() != nil {
			t.Error(".editroute-log is not gitignored, so it will show up in `git status` and get committed")
		}
	})
}

// TestEveryRefusalReasonHasARule is the totality control over the hook's rule table.
//
// The slugs are matched by a distinctive phrase from each reason, so a reason added later with no matching
// phrase would log as `unclassified` — a silent hole in the one figure this log exists to produce. This drives
// every deny arm the hook has and asserts none of them lands there.
//
// It reads the deny commands from the same shapes `TestEditRouteHookRefusesBashEditsOfTrackedFiles` uses, which
// is deliberate duplication of input and not of logic: if a route is added there without a rule here, this
// fails.
func TestEveryRefusalReasonHasARule(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(root, "scripts", "editroute.py")

	logPath := filepath.Join(t.TempDir(), "refusals")

	for _, a := range []struct{ name, cmd, wantRule string }{
		{"redirect", "echo hi >> CHANGELOG.md", "redirect-write"},
		{"tee", "echo hi | tee scripts/ratio.sh", "tee-write"},
		{"sed_in_place", "sed -i '' 's/a/b/' scripts/ratio.sh", "in-place-edit"},
		{"inline_python", "python3 - <<'PY'\np='scripts/ratio.sh'\nopen(p,'w').write('x')\nPY", "inline-write"},
		{"bare_sleep", "sleep 240", "sleep-as-wait"},
		{"subst1_discarded", "python3 scripts/subst1.py CHANGELOG.md /tmp/o /tmp/n > /dev/null", "subst1-discarded"},
		{"subst1_chained", "python3 scripts/subst1.py CHANGELOG.md /tmp/o /tmp/n ; git commit", "subst1-chained"},
	} {
		t.Run(a.name, func(t *testing.T) {
			_ = os.Remove(logPath)
			payload, mErr := json.Marshal(map[string]any{
				"hook_event_name": "PreToolUse",
				"tool_name":       "Bash",
				"cwd":             root,
				"tool_input":      map[string]any{"command": a.cmd},
			})
			if mErr != nil {
				t.Fatal(mErr)
			}
			c := exec.Command("python3", hook)
			c.Dir = root
			c.Stdin = bytes.NewReader(payload)
			c.Env = append(os.Environ(), "EDITROUTE_LOG="+logPath)
			var errBuf bytes.Buffer
			c.Stderr = &errBuf
			_ = c.Run()

			b, readErr := os.ReadFile(logPath)
			if readErr != nil {
				t.Fatalf("no log line for a command the hook refuses:\n%s", errBuf.String())
			}
			line := strings.TrimSpace(string(b))
			if strings.Contains(line, "unclassified") {
				t.Errorf("the reason logged as UNCLASSIFIED — a reason with no rule is a hole in the rate "+
					"this log exists to measure. Add a slug to RULES in scripts/editroute.py.\n"+
					"  line:   %s\n  reason: %s", line, errBuf.String())
			}
			f := strings.Split(line, "\t")
			if len(f) == 3 && !strings.Contains(f[1], a.wantRule) {
				t.Errorf("rule = %q, want %q", f[1], a.wantRule)
			}
		})
	}
}
