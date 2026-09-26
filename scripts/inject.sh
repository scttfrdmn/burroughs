#!/usr/bin/env sh
# inject — apply a falsification edit, PROVE it applied, run the control, restore, and prove the restore.
#
# ## The three losses that earned this
#
# Three times in one day an edit that changed nothing was reported as a measurement. In each case the run
# looked healthy and the tell was indirect:
#
#   1. `git stash` was used to drop a repair for a "pre-repair" arm — but the repair had been COMMITTED, so
#      the stash took nothing and the arm ran the repaired engine. The tell was one line of output,
#      `No stash entries found`, in a log nobody was reading for that.
#   2. An injection's `perl` substitution targeted the first of three identical call sites and landed in the
#      wrong function. The tell was a compile error, which is the lucky case.
#   3. A `sed` pattern written `\$raceflag` inside single quotes matched nothing, because `\$` is a literal
#      backslash there. The tell was a 79-second runtime where a cached run would have been instant.
#
# **An unapplied edit looks exactly like a measurement.** The subject passes, the log looks ordinary, and the
# conclusion drawn is the opposite of the truth: "the control survived the injection" when the injection never
# happened. That is worse than a missing test, because it is a test that reports a false verdict with
# confidence.
#
# ## What this refuses
#
#   - an edit that produces no diff — the injection did not apply;
#   - a restore that leaves a diff — the tree is dirty and the next measurement inherits it;
#   - with `--expect-fail`, a control that SURVIVED its injection — because *a control isn't born until it's
#     watched die*, and an injection the control shrugs off is a finding rather than a formality.
#
# **The hunk is printed.** No falsification counts unless the log shows what was actually changed, which is
# the part all three losses lacked.
#
# ## Why the edit is Python on stdin and not a sed expression
#
# Two of the three losses were quoting accidents in shell one-liners. The edit arrives as a Python program on
# stdin, receiving the target path as `sys.argv[1]`, so there is no shell quoting layer between the intent and
# the file — and an `assert old in s` inside it fails loudly rather than substituting nothing.
#
# ## Usage
#
#   inject.sh <file> [--expect-fail] -- <command...>   <<'PY'
#   import sys
#   p = sys.argv[1]; s = open(p).read()
#   old = "..."; new = "..."
#   assert old in s, "anchor missing"
#   open(p, "w").write(s.replace(old, new, 1))
#   PY
#
#   inject.sh --worktree <sha> <dir> -- <command...>
#
# The `--worktree` form is for WHOLE-ARM switches — a pre-repair engine against a post-repair one. It never
# uses a stash: it checks the named SHA out into its own worktree, prints `git rev-parse HEAD` from inside it
# so the log carries which tree ran, and removes it afterwards. Loss 1 above is exactly what a stash does to
# an arm switch once the work is committed.
set -eu

fail() {
	echo "inject: FAIL $1" >&2
	exit 1
}

if [ "${1:-}" = "--worktree" ]; then
	sha=${2:?usage: inject.sh --worktree <sha> <dir> -- <command...>}
	dir=${3:?usage: inject.sh --worktree <sha> <dir> -- <command...>}
	shift 3
	[ "${1:-}" = "--" ] && shift
	[ -e "$dir" ] && fail "$dir already exists; refusing to reuse a worktree whose contents are unknown"
	git worktree add -q --detach "$dir" "$sha" || fail "could not create a worktree at $sha"
	# **The log must carry which tree ran.** A whole-arm claim whose arm is not identified in its own output
	# is the stash defect wearing a directory name.
	echo "inject: worktree $dir at $(git -C "$dir" rev-parse HEAD)" >&2
	set +e
	(cd "$dir" && "$@")
	rc=$?
	set -e
	echo "inject: worktree command exit=$rc" >&2
	git worktree remove --force "$dir" || fail "could not remove the worktree $dir"
	echo "inject: worktree removed" >&2
	exit "$rc"
fi

file=${1:?usage: inject.sh <file> [--expect-fail] -- <command...>}
shift
expectfail=0
if [ "${1:-}" = "--expect-fail" ]; then
	expectfail=1
	shift
fi
[ "${1:-}" = "--" ] && shift
[ $# -gt 0 ] || fail "no command given after --"

git ls-files --error-unmatch "$file" >/dev/null 2>&1 ||
	fail "$file is not tracked, so it cannot be diffed or restored"
# A file that is already modified makes "did my edit apply" unanswerable.
[ -z "$(git diff -- "$file")" ] ||
	fail "$file already has uncommitted changes; the injection's own diff would be indistinguishable"

edit=$(mktemp)
trap 'rm -f "$edit"' EXIT
cat >"$edit"
[ -s "$edit" ] || fail "no edit program was supplied on stdin"

python3 "$edit" "$file" || fail "the edit program exited non-zero; nothing was injected"

hunk=$(git diff -- "$file")
[ -n "$hunk" ] || fail "the edit produced NO DIFF — the injection did not apply, and a control that
      'survives' an injection that never happened is a false verdict stated with confidence"

echo "inject: applied to $file:" >&2
printf '%s\n' "$hunk" | sed 's/^/    /' >&2

set +e
"$@"
rc=$?
set -e
echo "inject: command exit=$rc" >&2

git checkout -- "$file" || fail "could not restore $file"
[ -z "$(git diff -- "$file")" ] || fail "$file was NOT restored; the next measurement would inherit it"
echo "inject: $file restored" >&2

if [ "$expectfail" -eq 1 ] && [ "$rc" -eq 0 ]; then
	fail "the control SURVIVED its injection (exit 0). A control isn't born until it's watched die, so
      this is a finding about the control, not a formality: it does not discriminate the case injected."
fi
exit 0
