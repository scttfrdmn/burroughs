#!/usr/bin/env sh
# triagecheck.sh [map] — every issue in the Windows triage map is still open.
#
# The map (`.github/windows-triage.tsv`) says which issue each recurring Windows failure is, and a reader
# triaging a red `windows` run compares its failing names against it. **A row whose issue has closed is the
# map lying about the tree**: either the failure is fixed and the row should go, or it still recurs and
# nothing tracks it. The map lived in the actor's session memory until the chair's order of 2026-10-17
# moved it into the tree as data, and this check is that order's control. Making `ciwatch.sh` read the map
# is #1037, which is parked, so this script reads the tracker and nothing else.
#
# **A question that could not be asked is not a pass.** A failed fetch (rate limit, no auth, transport) is
# a FAIL that says the state could not be fetched and claims nothing about the issue. That is grave #365's
# rule and #410's distinction: HTTP 404 is a verdict, anything else is a mechanism failure. A map with no
# rows also fails, because "all open" over nothing is a green that checked nothing.
set -eu

map=${1:-.github/windows-triage.tsv}
[ -r "$map" ] || { echo "FAIL  triagecheck: the map $map is not readable"; exit 1; }

tab=$(printf '\t')
fail=0
rows=0
names=""
issues=""
lineno=0
while IFS= read -r line || [ -n "$line" ]; do
	lineno=$((lineno + 1))
	case $line in '' | '#'*) continue ;; esac
	name=${line%%"$tab"*}
	rest=${line#*"$tab"}
	nums=${rest%%"$tab"*}
	if [ "$rest" = "$line" ] || ! printf '%s' "$name" | grep -Eq '^Test[A-Za-z0-9_]+$' ||
		! printf '%s' "$nums" | grep -Eq '^[0-9]+(,[0-9]+)*$'; then
		echo "FAIL  $map:$lineno is not 'TestName<TAB>issue[,issue...]<TAB>note': $line"
		fail=1
		continue
	fi
	case " $names " in *" $name "*)
		echo "FAIL  $map:$lineno names $name a second time; one row per test name"
		fail=1
		continue
		;;
	esac
	names="$names $name"
	rows=$((rows + 1))
	for n in $(printf '%s' "$nums" | tr ',' ' '); do
		case " $issues " in *" $n "*) ;; *) issues="$issues $n" ;; esac
	done
done <"$map"

if [ "$rows" -eq 0 ]; then
	echo "FAIL  triagecheck: $map has no rows, so there is nothing to check"
	exit 1
fi

asked=0
for n in $issues; do
	err=$(mktemp)
	if ! state=$(gh api "repos/{owner}/{repo}/issues/$n" --jq .state 2>"$err"); then
		if grep -q 'HTTP 404' "$err"; then
			echo "FAIL  issue $n in $map does not resolve: no such issue in this repo"
		else
			echo "FAIL  issue $n's state could not be fetched: $(tr '\n' ' ' <"$err" | cut -c1-200)"
			echo "      A mechanism failure, not a verdict: nothing here says whether issue $n is open."
		fi
		rm -f "$err"
		fail=1
		continue
	fi
	rm -f "$err"
	asked=$((asked + 1))
	if [ "$state" != "open" ]; then
		mapped=$(awk -F'\t' -v n="$n" '$0 !~ /^#/ && NF >= 2 { k = split($2, a, ","); for (i = 1; i <= k; i++) if (a[i] == n) print $1 }' "$map" | tr '\n' ' ')
		echo "FAIL  issue $n is $state, and the map still sends $mapped to it."
		echo "      If the failure is fixed, delete the row. If it still recurs, file what tracks it and repoint the row."
		fail=1
	fi
done

if [ "$fail" -ne 0 ]; then
	exit 1
fi
echo "triagecheck: $rows row(s) in $map, $asked issue(s) asked, all open"
