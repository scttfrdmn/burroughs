#!/usr/bin/env bash
# explainred.sh <main-sha> <issue> — record that main's red verdict at <main-sha> is explained by an OPEN issue.
#
# The merge route (`editroute.py`, route 8; the chair's ruling of 2026-10-23) refuses every merge while main's tip
# has a red `ciwatch` record. A red main stops merging until it is repaired or explained. This is the explaining:
# an open issue that owns the failure, written into the record as `explained=<issue>`, so the next merge is
# admitted and the record says why.
#
# **The escape hatch is checked, not trusted** (the chair's ruling of 2026-10-24). An explanation is accepted only
# if the issue is OPEN and it either:
#   - EXISTED before the red run was created, so it could not have been filed to fit the red; or
#   - NAMES one of the run's failing tests in its body, so it is about this failure.
# An issue created after the red run that names none of its failing tests is refused: that is an explanation
# written to get past the gate.
#
# Every use is appended to `.ci-verdict.d/explained.log` (sha, issue, run, the ground it was accepted on), which
# is what the next report to the chair lists.
#
# Seams for the witness: EXPLAINRED_VERDICT_DIR, EXPLAINRED_ISSUE (a "state<TAB>created_at" line followed by the
# body), EXPLAINRED_RUN_CREATED, EXPLAINRED_FAILED (the failing test names, one per line).
set -euo pipefail
sha=${1:?usage: explainred.sh <main-sha> <issue>}
issue=${2:?usage: explainred.sh <main-sha> <issue>}
issue=${issue#\#}
root=$(cd "$(dirname "$0")/.." && pwd)
vdir=${EXPLAINRED_VERDICT_DIR:-$root/.ci-verdict.d}
rec="$vdir/ci-$sha"
[ -f "$rec" ] || { echo "explainred: no ciwatch record for $sha ($rec); watch main first" >&2; exit 1; }
grep -q '^ciwatch=green$' "$rec" && { echo "explainred: $sha is green; there is nothing to explain" >&2; exit 1; }
run=$(sed -n 's/^run=//p' "$rec" | head -1)

if [ -n "${EXPLAINRED_ISSUE:-}" ]; then
	issue_json=$EXPLAINRED_ISSUE
else
	issue_json=$(gh api "repos/{owner}/{repo}/issues/$issue" -q '"\(.state)\t\(.created_at)\n\(.body // "")"' 2>/dev/null || true)
fi
# **Split by parameter expansion, never by `printf … | head -1`** (#1107). Under `set -euo pipefail`, `head`
# exits after one line, and if `printf` is still writing it takes SIGPIPE. `pipefail` then fails the
# substitution, and `set -e` ends the script with no message at all. A loaded CI runner hit that twice with a
# two-line body, and any body larger than the pipe buffer hits it every time. Expansion has no pipe to signal.
first=${issue_json%%$'\n'*}
state=${first%%$'\t'*}
issued=
[ "$first" = "$state" ] || issued=${first#*$'\t'}
issued=${issued%%$'\t'*}
body=
[ "$issue_json" = "$first" ] || body=${issue_json#*$'\n'}
[ "$state" = "open" ] || { echo "explainred: issue #$issue is not open (state: ${state:-unknown})" >&2; exit 1; }

if [ -n "${EXPLAINRED_RUN_CREATED:-}" ]; then
	created=$EXPLAINRED_RUN_CREATED
else
	created=$(gh run view "$run" --json createdAt -q .createdAt 2>/dev/null || true)
fi
[ -n "$created" ] || { echo "explainred: the red run's creation time could not be read (run ${run:-unknown})" >&2; exit 1; }

ground=""
# ISO-8601 UTC timestamps compare correctly as strings.
if [ -n "$issued" ] && [[ "$issued" < "$created" ]]; then
	ground="existed before the red run ($issued < $created)"
else
	if [ -n "${EXPLAINRED_FAILED+x}" ]; then
		failed=$EXPLAINRED_FAILED
	else
		failed=$(gh run view "$run" --log-failed 2>/dev/null | grep -oE -- '--- FAIL: Test[A-Za-z0-9_]+' | sed 's/--- FAIL: //' | sort -u || true)
	fi
	while IFS= read -r t; do
		[ -n "$t" ] || continue
		# A pattern match, not `printf "$body" | grep -qF` (#1107's second instance): `grep -q` exits on its
		# first match, `printf` then takes SIGPIPE, and `pipefail` makes the match a false condition.
		if [[ $body == *"$t"* ]]; then
			ground="names the failing test $t"
			break
		fi
	done <<<"$failed"
fi
if [ -z "$ground" ]; then
	echo "explainred: issue #$issue was created after the red run ($issued >= $created) and names none of its failing tests;" >&2
	echo "            an explanation written after the fact must say which failure it owns" >&2
	exit 1
fi

printf 'explained=%s\n' "$issue" >>"$rec"
printf '%s\tsha=%s\tissue=%s\trun=%s\t%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$sha" "$issue" "${run:-?}" "$ground" >>"$vdir/explained.log"
echo "explainred: main's red at ${sha:0:12} is explained by #$issue: $ground" >&2
