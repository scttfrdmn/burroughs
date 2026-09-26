#!/usr/bin/env sh
# spacecheck — refuse a double space between two word characters, because that is the SIGNATURE of a
# backtick executed away.
#
# ## The defect this exists for
#
# Three times in one session, text was silently deleted by the shell. A commit message or a heredoc written
# with UNQUOTED delimiters treats `` `word` `` as command substitution: the shell runs `word`, the command
# fails, and the backticked span is replaced with **nothing**. What lands is the surrounding prose with a hole
# in it:
#
#     under `-race` the interpreter  ->  under  the interpreter
#     so `main` waited               ->  so  waited
#
# The loss is invisible at a glance — the sentence still scans — but it leaves one detectable trace: **two
# spaces between two word characters**, where the deleted word used to be.
#
# Each of the three losses was caught by reading the file back, which is luck dressed as diligence. So this is
# tooling rather than a resolution to be careful, per the rule that a third instance earns a mechanism.
#
# ## What it does NOT claim
#
# A double space is a **symptom**, not the disease. It catches the case where a backticked word sat between
# other words, which is every instance observed. It does **not** catch a backticked span at the start or end of
# a line, or one adjacent to punctuation, and it cannot see a substitution that produced output instead of
# nothing. Named rather than implied: the check is a tripwire on the common shape, not a proof of integrity.
#
# ## Usage
#
#   spacecheck.sh --msg <file>              one commit message (the commit-msg hook's form)
#   spacecheck.sh <base> <head>             added lines in a range
#   spacecheck.sh --worktree [base]         added lines in the working tree, untracked files included
#   spacecheck.sh --range-msgs <base> <head> every commit MESSAGE in a range
#
# Exemptions are by NAME, never by pattern: a line containing the marker `spacecheck:ok` is permitted, so a
# deliberate double space is a recorded decision rather than a hole in the check.
set -eu

marker='spacecheck:ok'

# scan reads candidate lines on stdin, each prefixed by its own label, and reports every offender.
# The predicate is deliberately narrow: a word character, exactly two spaces, a word character. Three or
# more spaces is alignment (tables, ASCII art) and is not this defect's signature.
scan() {
	found=0
	while IFS= read -r line; do
		case $line in
		*"$marker"*) continue ;;
		esac
		# `grep -E` rather than a shell case, because the predicate needs character classes.
		if printf '%s\n' "$line" | grep -Eq '[[:alnum:]]  [[:alnum:]]'; then
			if [ "$found" -eq 0 ]; then
				echo "FAIL  a double space sits between two word characters, which is the signature of a" >&2
				echo "      backtick executed away by an unquoted shell string. Either the word is missing" >&2
				echo "      (rewrite it, and use \`git commit -F\` with a <<'EOF' heredoc) or the spacing is" >&2
				echo "      deliberate, in which case add the marker '$marker' to that line." >&2
				echo "" >&2
			fi
			found=1
			printf '  %s\n' "$line" >&2
		fi
	done
	return "$found"
}

usage='usage: spacecheck.sh --msg <file> | <base> <head> | --worktree [base] | --range-msgs <base> <head>'

mode=${1:?$usage}
case $mode in
--msg)
	f=${2:?$usage}
	# A commit message's trailing comment block is git's own scissors/template text, not the author's.
	if grep -v '^#' "$f" | sed 's/^/msg: /' | scan; then
		echo "spacecheck: commit message clean ($(grep -cv '^#' "$f") line(s) scanned)" >&2
	else
		exit 1
	fi
	;;
--range-msgs)
	base=${2:?$usage}
	head=${3:?$usage}
	n=$(git log --format=%H "$base..$head" | grep -c '' || true)
	if git log --format=%B "$base..$head" | grep -v '^#' | sed 's/^/msg: /' | scan; then
		echo "spacecheck: $n commit message(s) in $base..$head clean" >&2
	else
		exit 1
	fi
	;;
--worktree)
	base=${2:-HEAD}
	{
		git diff "$base" -- 'PROVENANCE*' '**/PROVENANCE*' 'docs/**' |
			grep '^+' | grep -v '^+++' | sed 's/^+/tree: /'
		# Untracked files are invisible to `git diff`, and a new PROVENANCE is exactly where this defect
		# lands — citecheck learned the same thing about its own worktree mode.
		for f in $(git ls-files --others --exclude-standard -- 'PROVENANCE*' '**/PROVENANCE*' 'docs/**'); do
			[ -s "$f" ] || continue
			grep -qI . "$f" 2>/dev/null || continue
			sed "s|^|new $f: |" "$f"
		done
	} | scan && echo "spacecheck: worktree docs and PROVENANCE clean (base $base)" >&2 || exit 1
	;;
*)
	base=$mode
	head=${2:?$usage}
	if git diff "$base" "$head" -- 'PROVENANCE*' '**/PROVENANCE*' 'docs/**' |
		grep '^+' | grep -v '^+++' | sed 's/^+/added: /' | scan; then
		echo "spacecheck: added doc and PROVENANCE lines in $base..$head clean" >&2
	else
		exit 1
	fi
	;;
esac
