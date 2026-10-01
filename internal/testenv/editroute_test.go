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

// TestEditRouteHookRefusesBashEditsOfTrackedFiles is the witness for `scripts/editroute.py`, the
// PreToolUse hook that closes the unsafe edit path.
//
// # Why a hook rather than another reminder
//
// `scripts/subst1.py` already existed, and its docstring already named this exact defect: a scripted
// `s.replace(OLD, NEW)` that misses rewrites the file identically and reports success. The rule that
// followed — edit through the editor tool, or through `subst1.py` when scripted — was then broken in
// **all 8** writes to one script in one campaign. A rule ignored eight times in a row is being
// remembered, not enforced.
//
// # Why this test exists beside the hook
//
// A hook lives in `.claude/settings.json`, which no Go test loads and no `make` target runs. Without
// this file the hook's behaviour would be asserted by nothing: it could stop denying — a regex edited,
// an exemption widened, `git ls-files` failing open — and the only symptom would be the silent return
// of the defect it was built for. The arms below are the hook's own falsification kept alive, which is
// the difference between a witness and a memory.
//
// The `allow` arms carry as much weight as the `deny` arms, for the reason a refusal-only witness
// cannot check its aim: a hook that denied everything would pass every deny arm and be unusable.
// TestSubst1ShowsWhereTheEditLanded covers the other half of the edit route: the permitted scripted path
// prints the hunk it wrote, with context, so the AIM is visible at the moment of the edit.
//
// This is deliberately NOT a safeguard, and the test says so because the script does. `subst1.py` already
// refuses 0 matches, >1 matches, and a no-op replacement — all of which are *missed* anchors. None of them
// can see an anchor that is unique, present, and in the wrong place, which happened twice in one slice. The
// real check on a wrong aim is a structural oracle over the destination: the second of those two was caught
// by `TestChangelogGroupsAreCanonical`, which knows Keep a Changelog's group order — a thing no
// general-purpose edit helper can know. So this asserts visibility, not protection.
func TestSubst1ShowsWhereTheEditLanded(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	target := write("t.txt", "line1\nline2\nANCHOR\nline4\nline5\n")
	oldText := write("o.txt", "ANCHOR\n")
	newText := write("n.txt", "NEW-A\nNEW-B\n")

	cmd := exec.Command("python3", filepath.Join(root, "scripts", "subst1.py"), target, oldText, newText)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("subst1 failed: %v\n%s", err, stderr.String())
	}
	got := stderr.String()

	// The line numbers must be REAL, not a count of the replacement: the anchor is on line 3 and the
	// replacement is two lines, so it occupies 3-4 of the file as written.
	if !strings.Contains(got, "landed at lines 3-4") {
		t.Errorf("the output does not name the lines the edit occupies:\n%s", got)
	}
	// Both written lines marked, and context on BOTH sides — a hunk shown with only what follows it cannot
	// tell you that you landed after the wrong heading.
	for _, want := range []string{"> 3 | NEW-A", "> 4 | NEW-B", "  1 | line1", "  5 | line4"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q from the landing display:\n%s", want, got)
		}
	}
	// And the marker must DISCRIMINATE: an unmarked context line proves the `>` is not on everything.
	if strings.Contains(got, "> 1 | line1") {
		t.Errorf("a context line is marked as written, so the marker says nothing:\n%s", got)
	}
}

func TestEditRouteHookRefusesBashEditsOfTrackedFiles(t *testing.T) {
	// Absolute, because the hook is handed a `cwd` and a `CLAUDE_PROJECT_DIR` and must resolve a
	// command's relative paths against them — `../..` would be resolved against the wrong base.
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(root, "scripts", "editroute.py")
	if _, err := os.Stat(hook); err != nil {
		t.Fatalf("the hook is missing: %v", err)
	}

	// Tracked files named below are asserted to be tracked, so an arm cannot pass because a path
	// went stale and stopped being the subject — the vacuity check this table would otherwise lack.
	for _, p := range []string{"scripts/ciwatch.sh", "CHANGELOG.md", "scripts/ratio.sh"} {
		out, err := exec.Command("git", "-C", root, "ls-files", "--error-unmatch", p).CombinedOutput()
		if err != nil {
			t.Fatalf("%s is not tracked, so every arm naming it is vacuous: %v: %s", p, err, out)
		}
	}

	type arm struct {
		name string
		tool string
		cmd  string
		deny bool
		// want is a phrase the refusal must contain, so an arm cannot pass on the wrong reason.
		want string
		// routes names what the refusal must offer instead. Per-arm rather than global, because the two
		// subjects need different guidance and **a refusal that names the wrong route is worse than one
		// that names none**: it sends the reader to a tool that cannot help. Defaults to the edit routes.
		routes []string
	}
	editRoutes := []string{"editor tool", "subst1.py"}
	arms := []arm{
		{
			// The campaign's own defect, verbatim in shape.
			name: "inline_python_writing_a_tracked_file",
			cmd: "cd ~/src/burroughs && python3 - <<'PY'\n" +
				"p='scripts/ciwatch.sh'\ns=open(p).read()\n" +
				"s=s.replace('a','b')\nopen(p,'w').write(s)\nPY",
			deny: true,
			want: "scripts/ciwatch.sh",
		},
		{
			name: "subst1_is_a_permitted_route",
			cmd:  "python3 scripts/subst1.py scripts/ciwatch.sh /tmp/old /tmp/new",
			deny: false,
		},
		{
			// The exemption is conditional on the route still BEING the route. The scripted path's one
			// defence against a wrong aim is the landing display; discard it and the exemption buys a
			// silent write that carries the authority of a permitted route — strictly worse than the
			// inline interpreter it replaces. This exact command put the third misplaced anchor of one
			// slice into CHANGELOG.md, minutes after the display was built to prevent it.
			name:   "subst1_with_its_output_discarded_is_refused",
			cmd:    "python3 scripts/subst1.py CHANGELOG.md /tmp/old /tmp/new > /dev/null 2>&1 && make ci",
			deny:   true,
			want:   "landing display",
			routes: []string{"subst1.py"},
		},
		{
			// The allow arm that makes the discard test load-bearing: same route, same target, output
			// left visible. Without it, "refuse subst1.py near /dev/null" could decay into refusing the
			// route altogether and still pass the deny arm.
			name: "subst1_with_its_output_visible_is_still_permitted",
			cmd:  "python3 scripts/subst1.py CHANGELOG.md /tmp/old /tmp/new && make ci",
			deny: false,
		},
		{
			// stderr alone, since that is where the display actually goes.
			name:   "subst1_with_stderr_discarded_is_refused",
			cmd:    "python3 scripts/subst1.py CHANGELOG.md /tmp/old /tmp/new 2>/dev/null",
			deny:   true,
			want:   "landing display",
			routes: []string{"subst1.py"},
		},
		{
			// Reading a tracked file inline is most of what inline python is for.
			name: "inline_python_only_reading_is_allowed",
			cmd:  "python3 - <<'PY'\ns=open('scripts/ciwatch.sh').read()\nprint(len(s))\nPY",
			deny: false,
		},
		{
			name: "redirection_over_a_tracked_file",
			cmd:  "cat > CHANGELOG.md <<'EOF'\nnew text\nEOF",
			deny: true,
			want: "CHANGELOG.md",
		},
		{
			name: "append_to_a_tracked_file",
			cmd:  "echo hi >> CHANGELOG.md",
			deny: true,
			want: "redirection",
		},
		{
			name: "sed_in_place_on_a_tracked_file",
			cmd:  "sed -i '' 's/a/b/' scripts/ratio.sh",
			deny: true,
			want: "in place",
		},
		{
			name: "tee_over_a_tracked_file",
			cmd:  "echo hi | tee scripts/ratio.sh",
			deny: true,
			want: "tee",
		},
		{
			// /tmp needs no exemption clause: it is untracked, so it is not the subject.
			name: "writing_tmp_is_allowed_because_tmp_is_untracked",
			cmd:  "cat > /tmp/pr.md <<'EOF'\nbody\nEOF",
			deny: false,
		},
		{
			name: "redirecting_a_log_is_allowed",
			cmd:  "go test ./... > /tmp/test.log 2>&1",
			deny: false,
		},
		{
			// The defect is a silent no-op on a MISSED ANCHOR, which requires an existing file.
			// Creating a new one has no anchor to miss, so there is nothing to protect.
			name: "creating_a_new_untracked_file_is_allowed",
			cmd:  "cat > internal/testenv/brand_new_file.go <<'EOF'\npackage testenv\nEOF",
			deny: false,
		},
		{
			// A quoted '>' is a word, not a redirection. This is the parse-not-grep arm: a grep for
			// '>' followed by a tracked path would refuse this.
			name: "a_quoted_redirection_character_is_not_a_redirection",
			cmd:  "echo 'write with > CHANGELOG.md in the text'",
			deny: false,
		},
		{
			name: "a_non_bash_tool_is_never_the_subject",
			tool: "Edit",
			cmd:  "",
			deny: false,
		},
		{
			// Behaviour 5: a duration is not a signal. This slipped more than once in the session that
			// built the hook, including inside the slice itself.
			name:   "a_bare_sleep_is_the_wait_and_is_refused",
			cmd:    "sleep 240; tail -3 /tmp/ci.log",
			deny:   true,
			want:   "a duration is not a signal",
			routes: []string{"detach.sh", "ciwatch.sh", "run_in_background"},
		},
		{
			// The route the ruling names as allowed must SURVIVE the refusal, and this arm is what makes
			// the loop-depth discriminator load-bearing rather than incidental: the word `sleep` is present
			// in both arms, so anything matching the word refuses the correct pattern too.
			name: "a_sleep_polling_a_live_process_is_a_wait_on_a_signal",
			cmd:  "while kill -0 53907 2>/dev/null; do sleep 20; done; tail -8 /tmp/w.log",
			deny: false,
		},
		{
			// `sleep` inside a committed script is untouched, because the hook never sees past the command
			// line. Stated as an arm so the boundary is asserted rather than merely true today.
			name: "a_script_that_sleeps_internally_is_not_the_subject",
			cmd:  "bash scripts/detach.sh /tmp/x.stamp 60 -- echo hi",
			deny: false,
		},
		{
			// The known gap, pinned as a deliberate arm rather than left to be discovered: a command
			// that cannot be tokenised is allowed, with a warning. Failing closed selectively would
			// mean grepping the raw text, which is the defect property 21 names.
			name: "an_untokenisable_command_is_allowed_with_a_warning",
			cmd:  "echo 'unbalanced",
			deny: false,
		},
	}

	for _, a := range arms {
		t.Run(a.name, func(t *testing.T) {
			tool := a.tool
			if tool == "" {
				tool = "Bash"
			}
			payload, err := json.Marshal(map[string]any{
				"hook_event_name": "PreToolUse",
				"tool_name":       tool,
				"cwd":             root,
				"tool_input":      map[string]any{"command": a.cmd},
			})
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("python3", hook)
			cmd.Dir = root
			cmd.Stdin = bytes.NewReader(payload)
			cmd.Env = append(os.Environ(), "CLAUDE_PROJECT_DIR="+root)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			runErr := cmd.Run()

			code := 0
			var ee *exec.ExitError
			if runErr != nil {
				if !errors.As(runErr, &ee) {
					t.Fatalf("running the hook failed: %v", runErr)
				}
				code = ee.ExitCode()
			}

			denied := code == 2
			if denied != a.deny {
				t.Fatalf("exit %d (denied=%v), want denied=%v\n\tcommand: %s\n\tstderr: %s",
					code, denied, a.deny, a.cmd, stderr.String())
			}
			if a.deny && a.want != "" && !strings.Contains(stderr.String(), a.want) {
				t.Errorf("the refusal does not mention %q, so this arm may be passing on the wrong\n"+
					"reason — a deny is only evidence for the mechanism it names.\n\tstderr: %s",
					a.want, stderr.String())
			}
			if a.deny {
				// A refusal that does not say what to do instead teaches working around the check
				// rather than through it.
				want := a.routes
				if want == nil {
					want = editRoutes
				}
				for _, route := range want {
					if !strings.Contains(stderr.String(), route) {
						t.Errorf("the refusal does not name the %q route: %s", route, stderr.String())
					}
				}
			}
		})
	}
}
