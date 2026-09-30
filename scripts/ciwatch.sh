#!/usr/bin/env bash
# ciwatch — resolve a CI run BY HEAD SHA and refuse a verdict whose required jobs did not run.
#
# ## The near-miss this replaces
#
# A green was read off the wrong run. One push produces **two** runs, and the newer finished in 13 seconds
# with `build`, `lint`, `conformance`, `witnesses`, `vuln` and `fuzz-smoke` all **skipped** — reporting
# `conclusion: success`, because a skipped job contributes success. Resolution by recency picks it.
#
# **The cause is deliberate and documented in `ci.yml` itself**, which is why this script exists rather than a
# workflow fix: the `pull_request:` trigger includes `edited` so that the two body-scanning checks see the body
# they will be merged with (#411), and every job whose subject is the *tree* carries
# `if: github.event.action != 'edited'` because an edit cannot change a byte the compiler sees. So editing a PR
# body — which is the normal flow when pasting a measured figure into it — fires a second run that correctly
# skips almost everything and incorrectly looks like the newest verdict.
#
# ## What it asserts
#
# The **job keys are derived from `ci.yml`'s own `jobs:` block**, the same way
# `TestCIGatesCoverWhatCIInvokes` derives CI's make targets — not from a list in this file, which would be the
# copy that drifts. Each key must appear as the prefix of at least one **non-skipped** job in the run. A run
# with a skipped required job is not a verdict, and this says so with the key that was missing.
#
# A matrix job's API name carries its axis (`build (ubuntu-24.04)`), so the check is prefix-based: deriving the
# expanded names would mean parsing `strategy.matrix`, and the prefix answers the question being asked.
#
# ## Usage
#
#   scripts/ciwatch.sh <sha> [stamp-prefix]
#
# Exits 0 only when a run for that SHA is complete, concluded success, and has every derived key represented by
# a job that actually ran. Writes the chosen run's JSON to <stamp-prefix>.verdict for reading afterwards, and
# prints the rejected runs with their reasons.
set -uo pipefail

repo=${CIWATCH_REPO:-scttfrdmn/burroughs}
sha=${1:?usage: ciwatch.sh <sha> [stamp-prefix]}
prefix=${2:-/tmp/ciwatch-$sha}
wf=${CIWATCH_WORKFLOW:-.github/workflows/ci.yml}

[ ${#sha} -ge 40 ] || {
	# A truncated SHA made `gh run list --commit` return an empty list once, which reads as "no runs yet".
	echo "ciwatch: FAIL the SHA must be the full 40 characters; a truncated one returns an empty run list" >&2
	exit 2
}
[ -r "$wf" ] || { echo "ciwatch: FAIL cannot read $wf, so the required-job set cannot be derived" >&2; exit 2; }

# --- the required job keys, derived from the workflow ------------------------------------------------------
keys=$(awk '
	/^jobs:/ { inj = 1; next }
	inj && /^[a-z]/ { inj = 0 }
	inj && match($0, /^  [A-Za-z][A-Za-z0-9_-]*:[[:space:]]*$/) {
		k = $0; sub(/^  /, "", k); sub(/:[[:space:]]*$/, "", k); print k
	}
' "$wf" | sort -u)
nkeys=$(printf '%s\n' "$keys" | grep -c .)
if [ "$nkeys" -lt 3 ]; then
	echo "ciwatch: FAIL derived only $nkeys job key(s) from $wf; the extractor is reading the workflow wrong" >&2
	echo "         and a required-job check with an empty domain passes by asking nothing." >&2
	exit 2
fi
echo "ciwatch: $nkeys required job key(s) derived from $wf: $(printf '%s ' $keys)" >&2

# --- every run for the SHA, newest first ------------------------------------------------------------------
runs=$(gh run list --repo "$repo" --commit "$sha" --json databaseId --limit 20 |
	python3 -c 'import json,sys; print(" ".join(str(r["databaseId"]) for r in json.load(sys.stdin)))')
if [ -z "${runs// /}" ]; then
	echo "ciwatch: no run exists for $sha yet" >&2
	exit 3
fi
echo "ciwatch: runs for this SHA: $runs" >&2

# --- pick the run whose non-skipped jobs cover every key --------------------------------------------------
chosen=""
for id in $runs; do
	gh run view "$id" --repo "$repo" --json status,conclusion,jobs > "$prefix.$id.json" 2>&1 || continue
	verdict=$(python3 - "$prefix.$id.json" <<'PY'
import json, sys
v = json.load(open(sys.argv[1]))
jobs = v.get("jobs", [])
ran = [j["name"] for j in jobs if j.get("conclusion") not in (None, "skipped")]
skipped = [j["name"] for j in jobs if j.get("conclusion") == "skipped"]
print(v.get("status", "?"), v.get("conclusion", "?"), len(jobs), len(skipped), sep="\t")
print("\t".join(ran))
PY
)
	status=$(printf '%s' "$verdict" | head -1 | cut -f1)
	concl=$(printf '%s' "$verdict" | head -1 | cut -f2)
	njobs=$(printf '%s' "$verdict" | head -1 | cut -f3)
	nskip=$(printf '%s' "$verdict" | head -1 | cut -f4)
	ran=$(printf '%s' "$verdict" | sed -n '2p')

	missing=""
	for k in $keys; do
		case "$ran" in
			"$k"*|*"	$k"*) ;;
			*) missing="$missing $k" ;;
		esac
	done
	if [ -n "${missing// /}" ]; then
		echo "ciwatch: run $id REJECTED — status=$status conclusion=$concl jobs=$njobs skipped=$nskip; no" >&2
		echo "         job ran for:$missing" >&2
		continue
	fi
	echo "ciwatch: run $id accepted as the verdict — status=$status jobs=$njobs skipped=$nskip" >&2
	chosen=$id
	break
done
if [ -z "$chosen" ]; then
	echo "ciwatch: FAIL no run for $sha has a job for every required key. A run whose required jobs were" >&2
	echo "         skipped is not a verdict, whatever its conclusion says." >&2
	exit 1
fi

# --- wait on the chosen run, then re-assert ---------------------------------------------------------------
gh run watch "$chosen" --repo "$repo" > /dev/null 2>&1
gh run view "$chosen" --repo "$repo" --json conclusion,status,headSha,jobs > "$prefix.verdict" 2>&1
python3 - "$prefix.verdict" "$sha" <<'PY'
import json, sys
v = json.load(open(sys.argv[1]))
want_sha = sys.argv[2]
got = v.get("headSha", "")
if got != want_sha:
    print(f"ciwatch: FAIL the chosen run's headSha is {got[:12]}, not {want_sha[:12]}", file=sys.stderr)
    raise SystemExit(1)
jobs = v.get("jobs", [])
skipped = [j["name"] for j in jobs if j.get("conclusion") == "skipped"]
failed = [j["name"] for j in jobs if j.get("conclusion") not in ("success", "skipped")]
print(f"ciwatch: {v.get('status')}/{v.get('conclusion')} over {len(jobs)} job(s), {len(skipped)} skipped",
      file=sys.stderr)
if skipped:
    print("ciwatch: FAIL these jobs were skipped in the run being read as a verdict: "
          + ", ".join(skipped), file=sys.stderr)
    raise SystemExit(1)
if failed:
    print("ciwatch: FAIL " + ", ".join(failed), file=sys.stderr)
    raise SystemExit(1)
if v.get("conclusion") != "success":
    print(f"ciwatch: FAIL conclusion is {v.get('conclusion')}", file=sys.stderr)
    raise SystemExit(1)
print("ciwatch: GREEN, and every required job ran", file=sys.stderr)
PY
