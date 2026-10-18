#!/usr/bin/env sh
# ratiocheck.sh — a PR body's quoted ratio is computed, never carried, and the sweep checks it.
#
# Usage:
#   scripts/ratiocheck.sh fill <body-file>   # compute the figure and substitute it for the body's one RATIO token
#   scripts/ratiocheck.sh --pr <N>           # PR N's quoted figure equals ratio.sh over its merge base..head
#   scripts/ratiocheck.sh                    # the same check, for an open PR whose head is HEAD (none: says so)
#
# **Why it exists: three ratio slips in one cycle, the third through a file left behind.** The PR-body
# figure was substituted by script, but the script read a substitution file that a *previous* run had
# written. A blocked command never rewrote it, and #1039's body carried #1038's figure. A scripted
# substitution is only as fresh as its input, so `fill` computes the figure and substitutes it **in one
# step, with no intermediate file**. The chair's ruling of 2026-10-17 also asked that the body sweep check
# the quoted figure, because a fill that is skipped or run too early leaves the same wrong number.
#
# **The range is the merge base to the head**, not `origin/main` to the head. `ratio.sh <base> <head>`
# diffs its two endpoints, so once main moves past a PR's branch point, `origin/main HEAD` counts main's
# new lines against the PR. The merge base is what `origin/main HEAD` means at the moment of a fill on a
# branch cut from main's tip, and it is the one reading that stays stable afterwards.
#
# **What is checked: the Board's `- Ratio:` line.** It must appear exactly once, and its first
# `engine E / instrument I` and its `(other O)` must equal the figure for the PR's head. A body with no
# Ratio line fails, because CLAUDE.md's behaviour 1 requires the figure in every PR. A fetch that fails
# fails too, saying the body could not be fetched (grave #365's rule).
set -eu

here=$(cd "$(dirname "$0")" && pwd)

# figure <base> <head> prints `E I O R RANGE CLASSES` for merge-base(base, head)..head. CLASSES is either
# `carried` (every line carried) or a short split.
figure() {
	mb=$(git merge-base "$1" "$2") || { echo "ratiocheck: no merge base for $1 and $2" >&2; return 1; }
	# Captured whole rather than piped to `head`: a pipe's reader closing early SIGPIPEs ratio.sh (exit 141).
	out=$("$here/ratio.sh" "$mb" "$2")
	first=$(printf '%s\n' "$out" | sed -n 1p)
	range=${first%% *}
	e=$(printf '%s' "$first" | sed -n 's/.* engine \([0-9][0-9]*\) \/ instrument [0-9][0-9]* = .*/\1/p')
	i=$(printf '%s' "$first" | sed -n 's/.* engine [0-9][0-9]* \/ instrument \([0-9][0-9]*\) = .*/\1/p')
	r=$(printf '%s' "$first" | sed -n 's/.* = \(.*[^ ]\)  *(other [0-9][0-9]*)$/\1/p')
	o=$(printf '%s' "$first" | sed -n 's/.*(other \([0-9][0-9]*\))$/\1/p')
	if [ -z "$e" ] || [ -z "$i" ] || [ -z "$o" ] || [ -z "$r" ]; then
		echo "ratiocheck: could not parse ratio.sh's first line: $first" >&2
		return 1
	fi
	# **All carried means the other two rows are empty**, not that the carried row equals the total. The
	# provenance rows count per-commit added lines, and the total counts the range diff, so a later commit
	# rewriting an earlier one's lines makes them differ (225 against 223 on this script's own PR).
	# `-E` for the alternation: BSD sed's basic regex has no `\|`, so a BRE here matched nothing on macOS.
	others=$(printf '%s\n' "$out" | sed -En 's/^ *(ordered in review|unattributed) +engine +([0-9]+) \/ instrument +([0-9]+) +\(other ([0-9]+)\)$/\2 \3 \4/p' | tr '\n' ' ')
	if [ "$others" = "0 0 0 0 0 0 " ]; then
		classes=carried
	else
		classes=split
	fi
	echo "$e $i $o $range $classes $r"
}

# quoted <body-file> prints `E I O` from the body's single `- Ratio:` line.
quoted() {
	n=$(grep -c '^- Ratio:' "$1" || true)
	if [ "$n" -ne 1 ]; then
		echo "FAIL  the body has $n '- Ratio:' line(s); it must quote the ratio exactly once, in the Board"
		return 1
	fi
	line=$(grep '^- Ratio:' "$1")
	qe=$(printf '%s' "$line" | sed -n 's/.*engine \([0-9][0-9]*\) \/ instrument [0-9][0-9]*.*/\1/p')
	qi=$(printf '%s' "$line" | sed -n 's/.*engine [0-9][0-9]* \/ instrument \([0-9][0-9]*\).*/\1/p')
	qo=$(printf '%s' "$line" | sed -n 's/.*(other \([0-9][0-9]*\)).*/\1/p')
	if [ -z "$qe" ] || [ -z "$qi" ] || [ -z "$qo" ]; then
		echo "FAIL  the Ratio line quotes no 'engine E / instrument I ... (other O)' figure: $line"
		return 1
	fi
	echo "$qe $qi $qo"
}

# check <body-file> <base> <head> compares the quoted figure with the computed one.
check() {
	q=$(quoted "$1") || { echo "$q"; return 1; }
	f=$(figure "$2" "$3") || return 1
	set -- "$1" $f
	if [ "$q" != "$2 $3 $4" ]; then
		echo "FAIL  the body quotes engine/instrument/other = $(echo "$q" | tr ' ' '/'), but ratio.sh over $5"
		echo "      gives $2/$3/$4. The figure was carried from somewhere else, or the head moved after the"
		echo "      fill. Re-run: scripts/ratiocheck.sh fill <body-file>, then gh pr edit <N> --body-file <body-file>."
		return 1
	fi
	echo "ratiocheck: the quoted ratio engine $2 / instrument $3 (other $4) equals ratio.sh over $5"
}

# pr_check <N> fetches PR N's body, base and head, and checks it.
pr_check() {
	tmp=$(mktemp -d)
	trap 'rm -rf "$tmp"' EXIT
	if ! gh api "repos/{owner}/{repo}/pulls/$1" --jq '.base.sha, .head.sha' >"$tmp/shas" 2>"$tmp/err" ||
		! gh api "repos/{owner}/{repo}/pulls/$1" --jq '.body // ""' >"$tmp/body" 2>>"$tmp/err"; then
		echo "FAIL  PR #$1's body could not be fetched: $(tr '\n' ' ' <"$tmp/err" | cut -c1-200)"
		echo "      A mechanism failure, not a verdict: nothing here says whether the quoted ratio is right."
		return 1
	fi
	base=$(sed -n 1p "$tmp/shas")
	head=$(sed -n 2p "$tmp/shas")
	for s in "$base" "$head"; do
		if ! printf '%s' "$s" | grep -Eq '^[0-9a-f]{40}$'; then
			echo "FAIL  PR #$1's base/head did not come back as SHAs: $(tr '\n' ' ' <"$tmp/shas" | cut -c1-120)"
			return 1
		fi
		git cat-file -e "$s^{commit}" 2>/dev/null || git fetch -q origin "$s" ||
			{ echo "FAIL  commit $s is not available locally and could not be fetched"; return 1; }
	done
	check "$tmp/body" "$base" "$head"
}

case ${1:-} in
fill)
	body=${2:?usage: ratiocheck.sh fill <body-file>}
	n=$(grep -o 'RATIO' "$body" | wc -l | tr -d ' ')
	if [ "$n" -ne 1 ]; then
		echo "FAIL  $body has $n RATIO token(s); fill needs exactly one" >&2
		exit 1
	fi
	f=$(figure origin/main HEAD)
	# figure prints `E I O RANGE CLASSES EXPR...`, and EXPR can contain spaces (`1:inf (no engine lines)`).
	set -- $f
	e=$1 i=$2 o=$3 range=$4 classes=$5
	shift 5
	expr="$*"
	if [ "$classes" = carried ]; then
		cls='all `Ratio-Class: carried`'
	else
		cls='provenance split as `scripts/ratio.sh` prints it'
	fi
	text="\`scripts/ratio.sh origin/main HEAD\` ($range): engine $e / instrument $i = $expr (other $o), $cls."
	# Written beside the body and moved over it, so a failure leaves the body as it was.
	awk -v t="$text" '{ k = index($0, "RATIO"); if (k) $0 = substr($0, 1, k - 1) t substr($0, k + 5); print }' \
		"$body" >"$body.ratiocheck" && mv "$body.ratiocheck" "$body"
	# The read-back: the landed line, from the file, not from the variable that was meant to land.
	echo "ratiocheck: filled $body:"
	grep -n -F "$text" "$body" || { echo "FAIL  the figure is not in $body after the write" >&2; exit 1; }
	;;
--pr)
	pr_check "${2:?usage: ratiocheck.sh --pr <N>}"
	exit $?
	;;
'')
	headsha=$(git rev-parse HEAD)
	err=$(mktemp)
	if ! prs=$(gh api "repos/{owner}/{repo}/commits/$headsha/pulls" --jq '.[] | select(.state == "open" and .head.sha == "'"$headsha"'") | .number' 2>"$err"); then
		# **An unpushed HEAD is an answer, not a failed question.** GitHub says "No commit found for SHA" for a
		# commit it has never seen, and no PR can have such a commit as its head. `make ci` runs before the
		# push, so treating this as a transport failure made the gate red on every unpushed commit (its first run).
		if grep -q 'No commit found for SHA' "$err"; then
			rm -f "$err"
			echo "ratiocheck: HEAD ($(git rev-parse --short HEAD)) is not on the remote, so no PR has it as its head"
			exit 0
		fi
		echo "FAIL  could not ask the tracker which PR has HEAD ($headsha) as its head: $(tr '\n' ' ' <"$err" | cut -c1-200)"
		rm -f "$err"
		exit 1
	fi
	rm -f "$err"
	if [ -z "$prs" ]; then
		echo "ratiocheck: no open PR has HEAD ($(git rev-parse --short HEAD)) as its head, so no body to check"
		exit 0
	fi
	rc=0
	for p in $prs; do pr_check "$p" || rc=1; done
	exit $rc
	;;
*)
	echo "usage: ratiocheck.sh fill <body-file> | --pr <N> | (no arguments)" >&2
	exit 2
	;;
esac
