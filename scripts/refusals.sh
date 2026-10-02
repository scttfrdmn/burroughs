#!/usr/bin/env bash
# refusals — report what the edit-route hook has refused, by rule.
#
# ## Why this exists
#
# `scripts/editroute.py` had refused ten times when this was written, and **three of those were false
# positives** — all three on its own author, and the hook was narrowed three times as a result. That ratio is
# the number that decides whether the mechanism survives: **an over-refusing check is the kind that gets
# deleted.** It was being counted from memory, which this project does not accept for a figure that decides
# something.
#
# ## What this does NOT do
#
# It does not say which refusals were *wrong*. Nothing can: whether a refusal was a false positive is a
# judgement about what the author meant, and the log holds a hash rather than the command precisely so it
# cannot pretend otherwise. **The classification is a deliberate step at a tooling decision**, with this report
# as its input — a rate claimed without someone having classified the entries would be the same recalled figure
# wearing a table.
#
# The log is untracked and per-machine, so a count here is about one developer's recent sessions and is not a
# project-wide statistic. Said plainly because a number in a table invites being quoted as more than it is.
#
# ## Usage
#
#   scripts/refusals.sh            # counts by rule, newest-first sample
#   scripts/refusals.sh --since 2026-10-01
set -uo pipefail

log=${EDITROUTE_LOG:-.editroute-log}
since=""
if [ "${1:-}" = "--since" ]; then
	since=${2:?usage: refusals.sh --since YYYY-MM-DD}
fi

if [ ! -f "$log" ]; then
	# Not an error. An empty log means the hook has refused nothing on this machine, which is a reading and
	# not a missing input — and reporting it as a failure would teach the reader to ignore this script.
	echo "refusals: no log at $log — the hook has refused nothing here yet." >&2
	exit 0
fi

rows=$(cat "$log")
if [ -n "$since" ]; then
	rows=$(printf '%s\n' "$rows" | awk -v s="$since" '$1 >= s')
fi

total=$(printf '%s\n' "$rows" | grep -c . || true)
if [ "$total" -eq 0 ]; then
	echo "refusals: 0 entries${since:+ since $since} in $log" >&2
	exit 0
fi

echo "refusals: $total entr$([ "$total" -eq 1 ] && echo y || echo ies)${since:+ since $since} in $log" >&2
echo >&2
printf '%s\n' "$rows" | cut -f2 | tr ',' '\n' | grep . | sort | uniq -c | sort -rn |
	awk '{ printf "  %5d  %s\n", $1, $2 }' >&2
echo >&2
echo "  unclassified entries are a DEFECT in the rule table, not a category:" >&2
echo "  TestEveryRefusalReasonHasARule asserts the classifier is total over the hook's own deny arms." >&2
echo >&2
echo "  Most recent:" >&2
printf '%s\n' "$rows" | tail -5 | awk -F'\t' '{ printf "  %s  %-28s %s\n", $1, $2, $3 }' >&2
echo >&2
echo "  Classifying these true/false is the next step, and it is a judgement nobody can automate:" >&2
echo "  the log holds a HASH, not the command, so this report cannot pretend to make that call." >&2
