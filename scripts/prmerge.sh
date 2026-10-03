#!/usr/bin/env bash
# prmerge — refuse to merge-and-delete while the local branch holds commits the PR does not.
#
# ## The near-miss this replaces
#
# `gh pr merge --squash --delete-branch` deletes the local branch too. A slice had been committed on top of an
# already-pushed branch as a temporary commit; the merge squashed the **pushed** head, deleted the branch, and
# took the extra commit with it. It was recovered from the reflog, and only because a `No stash entries found`
# line in unrelated output was noticed — which is not a mechanism.
#
# The commit was never pushed, so nothing about the PR was wrong. What was wrong is that the merge silently
# disposed of work whose only copy was a local ref about to be deleted.
#
# ## What it asserts, before touching anything
#
#  1. the PR's head ref is the branch being merged;
#  2. **the local branch tip equals the PR's remote head** — if not, it stops and names the local-only commits;
#  3. the working tree is clean, since uncommitted work dies the same way and is not even in the reflog.
#
# Then it merges. The order matters: every check is read-only and happens before the irreversible step.
#
# ## Usage
#
#   scripts/prmerge.sh <pr-number> [--squash|--merge|--rebase]
set -uo pipefail

repo=${PRMERGE_REPO:-scttfrdmn/burroughs}
pr=${1:?usage: prmerge.sh <pr-number> [--squash|--merge|--rebase]}
mode=${2:---squash}

meta=$(gh pr view "$pr" --repo "$repo" --json headRefName,headRefOid,state,mergeable)
branch=$(printf '%s' "$meta" | python3 -c 'import json,sys; print(json.load(sys.stdin)["headRefName"])')
remote_head=$(printf '%s' "$meta" | python3 -c 'import json,sys; print(json.load(sys.stdin)["headRefOid"])')
state=$(printf '%s' "$meta" | python3 -c 'import json,sys; print(json.load(sys.stdin)["state"])')

[ "$state" = "OPEN" ] || { echo "prmerge: FAIL PR #$pr is $state" >&2; exit 1; }
echo "prmerge: PR #$pr branch=$branch remote head=${remote_head:0:12}" >&2

# --- 1. the working tree, because uncommitted work is not even in the reflog -------------------------------
dirty=$(git status --porcelain)
if [ -n "$dirty" ]; then
	echo "prmerge: FAIL the working tree has uncommitted changes, and --delete-branch would leave them" >&2
	echo "         on a branch that no longer exists. Commit, stash, or discard them first:" >&2
	printf '%s\n' "$dirty" | sed 's/^/           /' >&2
	exit 1
fi

# --- 2. the local branch's tip against the PR's remote head -----------------------------------------------
if git rev-parse --verify --quiet "refs/heads/$branch" > /dev/null; then
	local_head=$(git rev-parse "refs/heads/$branch")
	if [ "$local_head" != "$remote_head" ]; then
		echo "prmerge: local $branch is at ${local_head:0:12}, the PR's head is ${remote_head:0:12}" >&2
		extra=$(git log --oneline "$remote_head..$local_head" 2>/dev/null)
		if [ -n "$extra" ]; then
			echo "prmerge: FAIL the local branch holds commit(s) the PR does NOT contain, and" >&2
			echo "         --delete-branch would take them with it:" >&2
			printf '%s\n' "$extra" | sed 's/^/           /' >&2
			echo "         Push them and let CI see them, or move them to another branch first." >&2
			exit 1
		fi
		behind=$(git log --oneline "$local_head..$remote_head" 2>/dev/null)
		if [ -n "$behind" ]; then
			# Behind is safe for the merge — the PR is what merges — but it is worth saying, because a
			# reader who thinks the local branch is the subject will be wrong about what landed.
			echo "prmerge: note the local branch is BEHIND the PR by:" >&2
			printf '%s\n' "$behind" | sed 's/^/           /' >&2
			echo "         That is safe: the PR's head is what merges. Nothing local is at risk." >&2
		fi
	else
		echo "prmerge: local $branch matches the PR's head exactly" >&2
	fi
else
	echo "prmerge: no local $branch; nothing local can be lost" >&2
fi

# --- 3. the local gate's own verdict ------------------------------------------------------------------------
#
# `make ci`'s verdict cannot be carried by a shell's return code. Three ways it was lost in one session:
# chaining with `;` hands the status to whatever ran last; `pipefail` does not reach across a `;`; and a
# background task's notification carries the WRAPPER's exit code, not the gate's. The third put a commit on a
# branch over a red gate, with `ci rc=2` sitting unread in the output file the whole time.
#
# So `make ci` writes `.ci-verdict` itself and this refuses to merge unless that file exists, names the local
# tip, and records exit 0. The SHA is the load-bearing field: without it the file is a mood, and a green from
# three commits ago reads exactly like a green from this one.
#
# `PRMERGE_VERDICT` is overridable only so the witness can point at a fixture; nothing else sets it.
verdict=${PRMERGE_VERDICT:-.ci-verdict}
if [ ! -f "$verdict" ]; then
	echo "prmerge: FAIL no $verdict — run \`make ci\` on this commit first." >&2
	echo "         A merge with no local verdict is a merge on a remembered green, and the gate's" >&2
	echo "         exit code is not something a caller can be trusted to have read." >&2
	exit 1
fi
v_exit=$(sed -n 's/^exit=//p' "$verdict" | head -1)
v_sha=$(sed -n 's/^sha=//p' "$verdict" | head -1)
v_dirty=$(sed -n 's/^dirty=//p' "$verdict" | head -1)
v_when=$(sed -n 's/^when=//p' "$verdict" | head -1)
tip=$(git rev-parse HEAD)
echo "prmerge: $verdict exit=${v_exit:-?} sha=${v_sha:0:12} dirty=${v_dirty:-?} when=${v_when:-?}" >&2

if [ "${v_exit:-1}" != "0" ]; then
	echo "prmerge: FAIL the local gate's recorded exit is ${v_exit:-missing}, not 0." >&2
	echo "         \`make ci\` did not pass on this tree. A red gate is not merged." >&2
	exit 1
fi
if [ "$v_sha" != "$tip" ]; then
	echo "prmerge: FAIL the verdict is for ${v_sha:0:12} but the local tip is ${tip:0:12}." >&2
	echo "         The gate ran on a different tree than the one being merged, so its green says" >&2
	echo "         nothing about this one. Commit first, then \`make ci\`, then merge." >&2
	exit 1
fi
if [ "$v_dirty" = "yes" ]; then
	echo "prmerge: FAIL the gate ran on a DIRTY tree, so its green is about neither the commit it" >&2
	echo "         names nor anything that can be merged. Re-run \`make ci\` on a clean tree." >&2
	exit 1
fi
echo "prmerge: the local gate is green on this exact commit" >&2

# --- 4. CI's own verdict, which this script did not read at all --------------------------------------------
#
# Step 3 above proves `make ci` passed **here**. It says nothing about the nine CI runs, which is the whole
# reason `ciwatch.sh` exists — and until #867 this script never consulted it. So *"merge on a
# ciwatch-verified green"* was **operator discipline, not an enforced precondition**: on #866 a
# `ciwatch_verdict: fail` was in hand and nothing in the tool would have stopped the merge, and nothing would
# stop a caller who never ran `ciwatch.sh` at all.
#
# Five refusals, and **green is the only verdict accepted**:
#
#   fail            — CI said no.
#   wrong SHA       — a green from another commit reads exactly like a green from this one, which is the same
#                     reasoning as step 3's SHA check.
#   no verdict      — absence must not read as permission. A missing file means "no information", and this
#                     script must turn that into a refusal rather than proceeding.
#   unfinished      — the bound expired with the run still going. **Separate from `fail`, and its message
#                     says "still running" rather than "failing"**, which is the entire point of giving the
#                     expiry its own outcome: a reader re-runs the watch, they do not go hunting a breakage.
#   stale body      — the body class was resolved from a run older than the last body edit, so the green is
#                     about a body that no longer exists.
#
# `PRMERGE_CIWATCH` points at the verdict prefix; by default it is the one `scripts/detach.sh` writes.
ciw=${PRMERGE_CIWATCH:-/tmp/ci$pr}.verdict
if [ ! -f "$ciw" ]; then
	echo "prmerge: FAIL no CI verdict at $ciw." >&2
	echo "         The local gate's green is about this tree, not about CI's nine runs. Run" >&2
	echo "         scripts/ciwatch.sh $remote_head $(dirname "$ciw")/ci$pr first." >&2
	echo "         Absence is not permission: no verdict means no information, not a pass." >&2
	exit 1
fi
cv=$(sed -n 's/.*"ciwatch_verdict": *"\([a-z]*\)".*/\1/p' "$ciw" | head -1)
csha=$(sed -n 's/.*"sha": *"\([0-9a-f]*\)".*/\1/p' "$ciw" | head -1)
echo "prmerge: $ciw ciwatch_verdict=${cv:-?} sha=${csha:0:12}" >&2

if [ "$csha" != "$remote_head" ]; then
	echo "prmerge: FAIL the CI verdict names ${csha:0:12} but the PR's head is ${remote_head:0:12}." >&2
	echo "         A green from another commit reads exactly like a green from this one." >&2
	exit 1
fi
case "$cv" in
green) : ;;
unfinished)
	echo "prmerge: FAIL CI is STILL RUNNING for this commit — the watch's bound expired before the run" >&2
	echo "         finished. This is NOT a failing tree: no required job's conclusion has been read." >&2
	echo "         Re-run scripts/ciwatch.sh once the run completes; do not go looking for a breakage." >&2
	exit 1
	;;
*)
	echo "prmerge: FAIL CI's verdict for this commit is '${cv:-missing}', not green." >&2
	echo "         A red CI run is not merged, and only 'green' is accepted here — an unrecognised" >&2
	echo "         verdict is refused rather than assumed benign." >&2
	exit 1
	;;
esac
# The body class must have been read from a run that saw the CURRENT body. A green whose body check predates
# the last body edit is a green about a body that no longer exists (#867's first half).
#
# **`null` is a refusal, not a pass.** The first version refused only on `true`, so a verdict whose body was
# never checked went straight through — *absence is not permission*, in the slice built to remove exactly
# that defect. `ciwatch.sh` reports `null` when it was given no PR number and therefore could not measure
# body currency; this script always knows the PR, so it can insist on a check that was actually made.
#
# Only `false` merges: the body was measured and found current.
bs=$(sed -n 's/.*"body_stale": *\([a-z]*\).*/\1/p' "$ciw" | head -1)
case "$bs" in
false) : ;;
true)
	echo "prmerge: FAIL the CI verdict is green, but its body check was resolved from a run older than" >&2
	echo "         the last body edit — so it is a green about a body that no longer exists." >&2
	echo "         Re-run: CIWATCH_PR=$pr scripts/ciwatch.sh $remote_head $(dirname "$ciw")/ci$pr" >&2
	exit 1
	;;
*)
	echo "prmerge: FAIL the CI verdict's body currency was NEVER CHECKED (body_stale=${bs:-missing})." >&2
	echo "         That is not a pass. ciwatch.sh reports null when it is given no PR number, so it" >&2
	echo "         could not measure whether the body moved after the run that read it." >&2
	echo "         Re-run: CIWATCH_PR=$pr scripts/ciwatch.sh $remote_head $(dirname "$ciw")/ci$pr" >&2
	exit 1
	;;
esac
echo "prmerge: CI is green on this exact commit, body check measured and current" >&2

# --- 5. the irreversible step ------------------------------------------------------------------------------
echo "prmerge: merging #$pr with $mode --delete-branch" >&2
gh pr merge "$pr" --repo "$repo" "$mode" --delete-branch || exit $?

# --- 6. the LOCAL branch, which --delete-branch does not reliably remove ------------------------------------
#
# **Measured, twice.** `gh pr merge --delete-branch` deleted the remote branch on four consecutive merges and
# left the local one every time. A surviving merged local ref is not inert: `git push -u origin <that-branch>`
# — a command copied forward from the previous slice without re-reading it — **resurrected a branch this script
# had just deleted**, at a commit already squashed into main.
#
# So the local ref goes too, and its absence is **verified** rather than assumed.
#
# ## Why `-D` is safe, stated at the point of the force-delete
#
# `-d` would refuse every time: a squash merge leaves the branch tip off `main`'s ancestry, so git cannot see
# that the content landed. `-D` skips that question — which means **this code must answer it itself.**
#
# **The safety is the SHA re-check immediately below, not the fact that `gh pr merge` succeeded.** A successful
# merge says the PR's head is in `main`; it says nothing about whether the local branch is still at that head.
# Step 2 above established the equality, but that check lives three steps away and nothing tied the two
# together — so if step 2 is ever loosened, a bare `-D` here would start destroying local-only commits silently.
# The re-check makes the force-delete's precondition local to the force-delete.
#
# The branch is not deleted while checked out — git refuses, and rightly — so this moves to the default branch
# first. That is where a caller ends up anyway after a merge.
if git rev-parse --verify --quiet "refs/heads/$branch" > /dev/null; then
	# Re-read the tip NOW. Between step 2 and here a merge ran, and anything that commits during a merge —
	# a hook, a concurrent session, a script — moves this ref.
	local_now=$(git rev-parse "refs/heads/$branch")
	if [ "$local_now" != "$remote_head" ]; then
		echo "prmerge: NOT deleting local $branch — it is at ${local_now:0:12}, and the PR head this run" >&2
		echo "         verified was ${remote_head:0:12}. The branch moved after the pre-merge check, so a" >&2
		echo "         force-delete here could destroy work that is not in the squash." >&2
		extra=$(git log --oneline "$remote_head..$local_now" 2>/dev/null)
		if [ -n "$extra" ]; then
			echo "         Commit(s) on the local branch and NOT in the merged head:" >&2
			printf '%s\n' "$extra" | sed 's/^/           /' >&2
		fi
		echo "         The merge itself succeeded. Deal with these, then: git branch -D $branch" >&2
		exit 0
	fi
	default=$(git config --get burroughs.defaultBranch 2>/dev/null || echo "main")
	if [ "$(git symbolic-ref --quiet --short HEAD 2>/dev/null || echo "")" = "$branch" ]; then
		git checkout --quiet "$default" 2>/dev/null ||
			echo "prmerge: could not switch to $default; leaving local $branch in place" >&2
	fi
	if [ "$(git symbolic-ref --quiet --short HEAD 2>/dev/null || echo "")" != "$branch" ]; then
		git branch -D "$branch" > /dev/null 2>&1
	fi
	if git rev-parse --verify --quiet "refs/heads/$branch" > /dev/null; then
		echo "prmerge: WARNING local branch $branch survived deletion." >&2
		echo "         A merged local ref is what a copied-forward \`git push -u origin $branch\` revives," >&2
		echo "         recreating a branch this merge deleted. Remove it: git branch -D $branch" >&2
	else
		echo "prmerge: local branch $branch deleted (verified absent)" >&2
	fi
fi
