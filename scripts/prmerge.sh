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

# --- 3. the irreversible step ------------------------------------------------------------------------------
echo "prmerge: merging #$pr with $mode --delete-branch" >&2
gh pr merge "$pr" --repo "$repo" "$mode" --delete-branch
