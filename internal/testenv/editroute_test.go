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

	// A directory that EXISTS and cannot be entered, for the arm that shows why an existence check
	// cannot stand in for "the `cd` succeeded". `0o000` is the whole point: `os.Stat` sees it, bash
	// cannot `cd` into it, and a guard that consults the filesystem calls that a success.
	//
	// Restored to `0o755` on cleanup so `t.TempDir`'s own removal can descend into it — without
	// that the test leaves a directory `RemoveAll` cannot delete, and the failure surfaces as an
	// unrelated cleanup error in whatever test runs next.
	lockedDir := filepath.Join(t.TempDir(), "locked")
	if err := os.MkdirAll(lockedDir, 0o755); err != nil {
		t.Fatalf("creating the unenterable directory: %v", err)
	}
	if err := os.Chmod(lockedDir, 0o000); err != nil {
		t.Fatalf("locking the directory: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(lockedDir, 0o755) })
	// The premise, asserted rather than assumed: if this platform lets the owner enter a 0o000
	// directory then the arm below measures nothing, and that is a finding about the platform
	// rather than a reason to skip.
	if _, err := os.ReadDir(lockedDir); err == nil {
		t.Fatalf("%s is readable at mode 0o000, so 'a directory that exists and cannot be "+
			"entered' cannot be constructed here and its arm has no subject", lockedDir)
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
		// editInput carries a non-Bash payload verbatim. The `Edit` subject has no command to put in
		// `cmd`, and faking one would test a shape the harness never sends.
		editInput map[string]any
	}
	editRoutes := []string{"editor tool", "subst1.py"}
	arms := []arm{
		{
			// The campaign's own defect, verbatim in shape.
			name: "inline_python_writing_a_tracked_file",
			cmd: "cd {ROOT} && python3 - <<'PY'\n" +
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
			// A LATER command's redirect is not subst1's. The first version of the discard check scanned the
			// whole token stream, so this was refused for a `/dev/null` belonging to `make fmt` — a false
			// positive on the hook's author, and the third of them. An over-refusing check is the kind that
			// gets deleted, which is why the arm is here rather than the behaviour being left to drift.
			name: "a_later_commands_dev_null_is_not_subst1s",
			cmd:  "python3 scripts/subst1.py CHANGELOG.md /tmp/o /tmp/n && make fmt > /dev/null 2>&1",
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
			name: "a_plain_edit_is_never_the_subject",
			tool: "Edit",
			cmd:  "",
			deny: false,
		},
		{
			// `replace_all` opts out of the >1-match refusal `subst1.py` enforces. In the slice that built
			// this hook it hit a sixth call site nobody intended and left the hook itself crashing.
			name:      "replace_all_on_a_tracked_file_is_refused",
			tool:      "Edit",
			editInput: map[string]any{"file_path": "{ROOT}/scripts/ciwatch.sh", "replace_all": true},
			deny:      true,
			want:      "replace_all",
			routes:    []string{"subst1.py", "unique"},
		},
		{
			// The allow arm that makes the flag the subject rather than the tool: same tracked file, same
			// editor, one anchored replacement. Without it, "refuse Edit on tracked files" passes the deny
			// arm and makes the hook unusable.
			name:      "an_anchored_edit_on_a_tracked_file_is_permitted",
			tool:      "Edit",
			editInput: map[string]any{"file_path": "{ROOT}/scripts/ciwatch.sh", "replace_all": false},
			deny:      false,
		},
		{
			// And scoped to TRACKED files, like every other route here: a wide edit to an untracked or new
			// file disturbs no history and has no reviewers.
			name:      "replace_all_on_an_untracked_file_is_permitted",
			tool:      "Edit",
			editInput: map[string]any{"file_path": "/tmp/scratch-notes.md", "replace_all": true},
			deny:      false,
		},
		{
			// `MultiEdit` carries the flag PER ENTRY inside `edits`, not at the top level — so a check
			// reading only `tool_input["replace_all"]` refused `Edit` and let the WIDER tool through
			// unchecked. The flag is on the SECOND entry deliberately: a scan that stops at the first
			// passes this arm.
			name: "multiedit_with_replace_all_in_a_later_entry_is_refused",
			tool: "MultiEdit",
			editInput: map[string]any{
				"file_path": "{ROOT}/scripts/ciwatch.sh",
				"edits": []any{
					map[string]any{"old_string": "a", "new_string": "b"},
					map[string]any{"old_string": "c", "new_string": "d", "replace_all": true},
				},
			},
			deny:   true,
			want:   "replace_all",
			routes: []string{"subst1.py", "unique"},
		},
		{
			// The complement: a MultiEdit of anchored replacements is ordinary work and must pass.
			name: "multiedit_without_replace_all_is_permitted",
			tool: "MultiEdit",
			editInput: map[string]any{
				"file_path": "{ROOT}/scripts/ciwatch.sh",
				"edits": []any{
					map[string]any{"old_string": "a", "new_string": "b"},
					map[string]any{"old_string": "c", "new_string": "d"},
				},
			},
			deny: false,
		},
		{
			// The swallowed refusal, in the separator the specimen actually used: a NEWLINE. `subst1.py`
			// refused 7 matches of `### Added` and the commit on the next line ran anyway, so the change
			// landed without its CHANGELOG entry. `shlex` eats newlines, so this arm also holds the
			// newline-to-`;` normalisation in place.
			name: "a_command_after_subst1_on_the_next_line_is_refused",
			cmd: "python3 scripts/subst1.py CHANGELOG.md /tmp/o /tmp/n\n" +
				"git add -A && git commit -q -m x",
			deny:   true,
			want:   "runs whatever its exit status was",
			routes: []string{"&&"},
		},
		{
			name:   "a_command_after_subst1_past_a_semicolon_is_refused",
			cmd:    "python3 scripts/subst1.py CHANGELOG.md /tmp/o /tmp/n ; git commit -q -m x",
			deny:   true,
			want:   "runs whatever its exit status was",
			routes: []string{"&&"},
		},
		{
			// `|| true` is the shape that most looks like care and most reliably discards the refusal.
			name:   "a_command_after_subst1_past_or_is_refused",
			cmd:    "python3 scripts/subst1.py CHANGELOG.md /tmp/o /tmp/n || true",
			deny:   true,
			want:   "runs whatever its exit status was",
			routes: []string{"&&"},
		},
		{
			// The allow arm, and the one that makes this about the SEPARATOR rather than about chaining:
			// `&&` is what the chaining meant — the next step happens only if the edit did.
			name: "a_command_after_subst1_past_and_is_permitted",
			cmd:  "python3 scripts/subst1.py CHANGELOG.md /tmp/o /tmp/n && git commit -q -m x",
			deny: false,
		},
		{
			// A LINE CONTINUATION is one command, and the newline-to-`;` normalisation nearly broke exactly
			// the check it was added beside. Blanket-replacing the newline gives `sed -i '' s/a/b/ \ ; CHANGELOG.md`
			// — the backslash escapes a space and the `;` splits `-i` from its target, so the in-place edit
			// this hook exists to refuse walks through. This arm is the discriminator for the join step.
			name: "an_in_place_edit_split_across_a_continuation_is_still_refused",
			cmd:  "sed -i '' 's/a/b/' \\\n  CHANGELOG.md",
			deny: true,
			want: "in place",
		},
		{
			// Newlines inside quotes are CONTENT, not separators. This is a regression guard rather than a
			// discriminator, and is labelled as one: because `shlex` keeps a quoted string as a single token,
			// a naive replacement injects `;` into the message text without changing the tokenisation, so
			// this arm passes either way. It is here because the *content* is what the inline-interpreter
			// check reads, and a future change to that check would be caught by it rather than by a user.
			name: "a_multi_line_quoted_message_stays_one_command",
			cmd:  "git commit -q -m \"line one\nsleep 300 was the bug\nline three\"",
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
			// A BACKGROUNDED sleep is not a wait — the shell does not block on it. This was a false positive
			// on the hook's own author: `sleep 400 &` was a live process held to occupy a gate lock in a
			// witness. An over-refusing check blocks work it was never aimed at, and a blocked actor
			// proceeds by working around the check.
			name: "a_backgrounded_sleep_is_a_process_not_a_wait",
			cmd:  "sleep 400 & HPID=$!; bash scripts/cilock.sh acquire /tmp/l $HPID",
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
			// The hook's own false positive, found when it refused its author. The bare name `Makefile`
			// was resolved against the repo ROOT — where a tracked Makefile lives — while the command had
			// already `cd`'d to /tmp. An over-refusing check is not the safe direction: it blocks work it
			// was never aimed at, and a blocked actor proceeds by working around the check, which is the
			// failure mode the whole mechanism exists to prevent.
			name: "a_cd_outside_the_repo_makes_a_bare_name_someone_elses_file",
			cmd:  "mkdir -p /tmp/mfprobe && cd /tmp/mfprobe && printf 'x:\\n' > Makefile",
			deny: false,
		},
		{
			// The complement that keeps the `cd` handling from becoming an escape hatch: a `cd` INTO the
			// repo must still resolve bare names to tracked files. Without this arm, "follow the cd" could
			// decay into "ignore relative paths" and still pass the arm above.
			name: "a_cd_into_the_repo_still_resolves_bare_names",
			cmd:  "cd {ROOT} && echo broken > CHANGELOG.md",
			deny: true,
			want: "CHANGELOG.md",
		},
		{
			// **A FAILED `cd` leaves bash where it was, and this hook used to allow the write.**
			//
			// `cd /nonexistent; <write>` runs the write in the directory the command started in —
			// measured: `bash -c 'cd /nonexistent-dir-xyz; pwd'` prints the original directory. The
			// hook resolved the bare name only against the `cd` target, found nothing tracked
			// there, and returned exit 0. A guard failing OPEN on a tracked file, and not a
			// Windows matter: this arm fails on Linux without the fix (chair's ruling, #942
			// review).
			//
			// The repair is in `cd_base`: a target that is not an existing directory keeps the
			// pre-`cd` directory as a candidate, which is what bash does.
			name: "a_failed_cd_then_a_write_still_resolves_to_the_tracked_file",
			cmd:  "cd /nonexistent-dir-xyz; echo broken > CHANGELOG.md",
			deny: true,
			want: "CHANGELOG.md",
		},
		{
			// The `&&` twin, where the write never runs at all — so refusing costs nothing, and
			// over-refusing is the direction this check is allowed to err in. Pinned beside its
			// `;` sibling because the two differ in consequence and not in parse: without both,
			// a future narrowing could fix the harmless case and leave the harmful one.
			// **Allowed, and the `;` twin above is refused — that pair IS the rule.**
			//
			// This arm was written `deny: true` one slice earlier, on the reasoning that the write
			// never runs so over-refusing is free. The operator rule supersedes that: `&&` means
			// the write cannot run unless the `cd` succeeded, so there is no write to refuse and
			// refusing it is a false positive, not a free one. Kept as the complement of the `;`
			// arm because the two differ only in the separator, which is the one thing the rule
			// reads — if both ever agree, the operator test has stopped being consulted.
			name: "a_failed_cd_with_andand_is_allowed_because_the_write_cannot_run",
			cmd:  "cd /nonexistent-dir-xyz && echo broken > CHANGELOG.md",
			deny: false,
		},
		{
			// **The target does not have to be absent for the `cd` to fail**, which is why the rule
			// is the operator and not the filesystem. Here an earlier `mkdir` fails, so the `cd`
			// fails, so the write lands in the original directory — and a draft of this guard that
			// tracked `mkdir` targets as "will exist" allowed it. Measured `rc=0` before the
			// operator rule, `rc=2` after.
			name: "a_cd_after_a_failing_mkdir_keeps_the_previous_directory",
			cmd:  "mkdir /proc/nope; cd /proc/nope; echo broken > CHANGELOG.md",
			deny: true,
			want: "CHANGELOG.md",
		},
		{
			// `{LOCKED}` exists and cannot be entered, so an existence check calls the `cd` a
			// success and the shell stays put. The second of the two unsound cases, and the one
			// that cannot be written with a literal path because it needs a mode the test creates.
			name: "a_cd_into_an_unenterable_directory_keeps_the_previous_directory",
			cmd:  "cd {LOCKED}; echo broken > CHANGELOG.md",
			deny: true,
			want: "CHANGELOG.md",
		},
		{
			// `||` runs the next command precisely WHEN the `cd` failed, so the previous directory
			// is not merely possible but likely. Pinned beside `&&` because the two operators are
			// the whole of the rule: if this arm ever passes while the `&&` arm below still does,
			// the operator test has collapsed into "any separator".
			name: "a_cd_with_orelse_keeps_the_previous_directory",
			cmd:  "cd /tmp || echo broken >> CHANGELOG.md",
			deny: true,
			want: "CHANGELOG.md",
		},
		{
			// The `&&` complement of the arm above, and the one that keeps the rule from becoming
			// "refuse everything after any `cd`": the write cannot run unless the `cd` worked, so
			// the target is the only candidate and a bare name there is someone else's file.
			name: "a_cd_to_an_existing_dir_with_andand_resolves_to_the_target_only",
			cmd:  "cd /tmp && printf x > Makefile",
			deny: false,
		},
		{
			// **`&&` says the `cd` succeeded, not which directory it started from.** The first `cd`
			// fails, so bash is still in the repo root; `cd scripts` then succeeds and the write
			// lands on the tracked `scripts/editroute.py`. Resolving `scripts` against the first
			// candidate alone gave `/nonexistent/scripts`, which `&&` then narrowed to — allowing
			// the write, measured `rc=0`. A relative target now resolves against every candidate.
			name: "a_relative_cd_after_an_uncertain_one_resolves_against_every_candidate",
			cmd:  "cd /nonexistent-dir-xyz; cd scripts && echo broken >> editroute.py",
			deny: true,
			want: "editroute.py",
		},
		{
			// **A `cd` inside a subshell never moves the parent shell**, so this writes the repo's
			// tracked `CHANGELOG.md`. The splitter treats `(` as a separator and discards it, so
			// the inner `cd` looked like any other and `&&` narrowed to `/tmp`: allowed, measured
			// `rc=0`. Now any `(` in the command stops every `cd` from narrowing — the stance
			// recorded in `cd_base`'s docstring, that an unmodelled construct must never narrow.
			name: "a_cd_inside_a_subshell_does_not_move_the_parent",
			cmd:  "(cd /tmp && true) && echo broken >> CHANGELOG.md",
			deny: true,
			want: "CHANGELOG.md",
		},
		{
			// The same subshell, written with no space before `&&`. Pins that the tokeniser splits
			// punctuation rather than handing back `true)&&` as one word — if it ever stops, the
			// arm above keeps passing while this one fails, which is the only way to tell a rule
			// that works from a rule that happens to match the spacing it was written with.
			name: "a_subshell_with_no_space_before_the_operator_is_still_a_subshell",
			cmd:  "(cd /tmp && true)&& echo broken >> CHANGELOG.md",
			deny: true,
			want: "CHANGELOG.md",
		},
		{
			// **Command substitution is a child shell too.** `$( … )` arrives with a `(` token, so
			// the subshell rule already covers it — pinned because that is a property of the
			// tokeniser rather than of this rule, and a tokeniser that stopped emitting `(` for
			// `$(` would reopen the gap silently.
			name: "a_cd_inside_a_command_substitution_does_not_move_the_parent",
			cmd:  "x=$(cd /tmp && true) && echo broken >> CHANGELOG.md",
			deny: true,
			want: "CHANGELOG.md",
		},
		{
			// **The backtick form refused only by accident before this slice**: the token is
			// `` `cd ``, so `basename` never matched "cd" and no `cd` was detected at all. That is
			// fail-closed by a property of the tokeniser, one change away from failing open — so a
			// backtick anywhere now stops narrowing by design. Verified to change behaviour where a
			// `cd` IS detected: `cd /tmp && printf x > Makefile` is allowed, and the same command
			// with a `` `date` `` in it is refused.
			name: "a_cd_inside_backticks_does_not_move_the_parent",
			cmd:  "`cd /tmp` && echo broken >> CHANGELOG.md",
			deny: true,
			want: "CHANGELOG.md",
		},
		{
			// **The arm that keeps the repair from becoming the false positive it replaced.**
			// `cd_base` exists because `cd /tmp/x && printf … > Makefile` was once refused against
			// the repo's tracked `Makefile`. A target that EXISTS still narrows the candidates to
			// it alone, so this stays allowed — if it ever starts being refused, the fix above has
			// widened past a failed `cd` into every `cd`.
			name: "a_cd_to_an_existing_dir_outside_the_repo_is_still_allowed",
			cmd:  "cd /tmp && printf x > Makefile",
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
			// `{ROOT}` is substituted rather than written out, because the two arms that need a `cd` into
			// the repo first said `cd ~/src/burroughs` — one developer's path. CI caught it: on a runner
			// that directory does not exist, so the hook correctly resolved the bare path against a
			// non-repo directory and ALLOWED the write, and both arms inverted. A witness that hard-codes
			// where the tree lives is asserting something about a machine, not about the hook.
			// **Single-quoted, because an unquoted path is not a command a real user would type.**
			//
			// On Windows `root` is `D:\a\burroughs\burroughs`, and bash removes those backslashes as
			// escapes — so the unquoted form gave `cd D:aburroughsburroughs`, the hook resolved bare
			// names against a directory that does not exist, and three arms inverted on the Windows
			// job. The hook was right: it predicted what bash does, which is what a guard on a bash
			// command must do. What was wrong was this substitution, handing it input no shell would
			// honour — the same shape as the CI-lock witness building a holder from Go.
			//
			// Single quotes are what `shlex.quote` emits for such a path, and they survive the
			// hook's own POSIX-mode tokeniser: measured, `shlex.split` returns
			// `D:\a\burroughs\burroughs` intact from the quoted form and `D:aburroughsburroughs`
			// from the bare one. Fixing the tokeniser instead would make the hook disagree with the
			// shell it guards, which is strictly worse (chair's ruling, #942 review).
			cmdText := strings.ReplaceAll(a.cmd, "{ROOT}", "'"+root+"'")
			cmdText = strings.ReplaceAll(cmdText, "{LOCKED}", "'"+lockedDir+"'")
			input := map[string]any{"command": cmdText}
			if a.editInput != nil {
				input = map[string]any{}
				for k, v := range a.editInput {
					if s, ok := v.(string); ok {
						v = strings.ReplaceAll(s, "{ROOT}", root)
					}
					input[k] = v
				}
			}
			payload, err := json.Marshal(map[string]any{
				"hook_event_name": "PreToolUse",
				"tool_name":       tool,
				"cwd":             root,
				"tool_input":      input,
			})
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("python3", hook)
			cmd.Dir = root
			cmd.Stdin = bytes.NewReader(payload)
			// `EDITROUTE_LOG` is redirected because **a test that drives a mechanism is a writer to every
			// artifact that mechanism touches** (grave #852). Without it every refusing arm here appended to
			// the production `.editroute-log` once per `go test` run, and the log that exists to measure the
			// hook's false-positive RATE reached 137 entries of which **2 were real** — its own test suite
			// out-writing real usage 135:2, with nothing in `scripts/refusals.sh`'s output to show it.
			//
			// Redirecting it in `refusallog_test.go` — the test *about* the log — was not enough. The
			// redirection belongs wherever the hook is invoked, which is here.
			cmd.Env = append(os.Environ(),
				"CLAUDE_PROJECT_DIR="+root,
				"EDITROUTE_LOG="+filepath.Join(t.TempDir(), "refusals"),
			)
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
					code, denied, a.deny, cmdText, stderr.String())
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
