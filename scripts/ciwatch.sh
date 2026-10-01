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
# **Two classes, derived from the workflow rather than named here.** `ci.yml` says it in as many words:
# *"every job whose subject is the TREE carries `if: github.event.action != 'edited'` … `citations` is the only
# job without it, because it is the only job whose subject is the BODY."* So the guard IS the classifier, and a
# hand-kept list of body-subject jobs would be a second copy of a fact the workflow already states.
#
# Why it matters: a body edit fires a run in which every tree job is **skipped** and only the body job runs —
# and that run is the one holding the **current body**. Applying one rule to both classes picks the wrong run
# for one of them, which is how #839's `citations` job came to fail on citation tokens that no longer existed.
# **Comments are skipped and the guard must be a YAML `if:` KEY, for a reason worth stating.** This tree
# documents itself heavily, and the body-subject job's own block contains a comment that QUOTES the guard --
# "the one job with no if: github.event.action != edited". A plain substring match read that comment as the
# guard, classified every job as tree-subject, left the body class empty, and so silently stopped checking the
# half this whole function exists for. Fourth time in this work that prose quoting code was counted as code,
# so the fix is in the matcher: drop comment lines, then require the key rather than the substring.
#
# **And the explanation lives out here rather than inside the awk program**, because the first draft put it in
# there and its apostrophes closed the surrounding single-quoted string -- the same family of defect one layer
# out. The awk body below is deliberately apostrophe-free.
classify() {
	awk -v want="$1" '
		/^jobs:/ { inj = 1; next }
		inj && /^[a-z]/ { inj = 0 }
		inj && match($0, /^  [A-Za-z][A-Za-z0-9_-]*:[[:space:]]*$/) {
			if (key != "") { emit() }
			key = $0; sub(/^  /, "", key); sub(/:[[:space:]]*$/, "", key); guarded = 0; next
		}
		inj && /^[[:space:]]*#/ { next }
		inj && /^[[:space:]]*if:[[:space:]]*github\.event\.action != .edited./ { guarded = 1 }
		END { if (key != "") emit() }
		function emit() {
			if ((want == "tree" && guarded) || (want == "body" && !guarded)) print key
		}
	' "$wf" | sort -u
}
treekeys=$(classify tree)
bodykeys=$(classify body)
keys=$(printf '%s\n%s\n' "$treekeys" "$bodykeys" | grep -c . >/dev/null; printf '%s\n%s\n' "$treekeys" "$bodykeys" | grep . | sort -u)
nkeys=$(printf '%s\n' "$keys" | grep -c .)
if [ "$nkeys" -lt 3 ]; then
	echo "ciwatch: FAIL derived only $nkeys job key(s) from $wf; the extractor is reading the workflow wrong" >&2
	echo "         and a required-job check with an empty domain passes by asking nothing." >&2
	exit 2
fi
ntree=$(printf '%s\n' "$treekeys" | grep -c .)
nbody=$(printf '%s\n' "$bodykeys" | grep -c .)
if [ "$ntree" -lt 2 ] || [ "$nbody" -lt 1 ]; then
	echo "ciwatch: FAIL classified $ntree tree-subject and $nbody body-subject job(s); the guard pattern and" >&2
	echo "         the workflow disagree, and a class with no members silently stops being checked." >&2
	exit 2
fi
echo "ciwatch: $nkeys required job key(s) from $wf — $ntree tree-subject ($(printf '%s ' $treekeys)), \
$nbody body-subject ($(printf '%s ' $bodykeys))" >&2

# --- fetching, behind a seam so the selection rule has a repeatable witness --------------------------------
#
# Two real CI runs are not a repeatable witness, so with CIWATCH_FIXTURE set to a directory the run list comes
# from <dir>/runs.json and each run from <dir>/<id>.json. Nothing else changes, which is the point: the witness
# drives the real selection code rather than a reimplementation of it.
fetch_runs() {
	if [ -n "${CIWATCH_FIXTURE:-}" ]; then
		python3 -c 'import json,sys; print(" ".join(str(r["databaseId"]) for r in json.load(open(sys.argv[1]))))' \
			"$CIWATCH_FIXTURE/runs.json"
		return
	fi
	gh run list --repo "$repo" --commit "$sha" --json databaseId --limit 20 |
		python3 -c 'import json,sys; print(" ".join(str(r["databaseId"]) for r in json.load(sys.stdin)))'
}

fetch_run() {
	if [ -n "${CIWATCH_FIXTURE:-}" ]; then
		cat "$CIWATCH_FIXTURE/$1.json"
		return
	fi
	gh run view "$1" --repo "$repo" --json status,conclusion,headSha,jobs
}

runs=$(fetch_runs)
if [ -z "${runs// /}" ]; then
	echo "ciwatch: no run exists for $sha yet" >&2
	exit 3
fi
echo "ciwatch: runs for this SHA: $runs" >&2

# --- TWO classes, TWO runs, because one rule cannot serve both ---------------------------------------------
#
# Tree-subject jobs take their verdict from the newest run in which they actually RAN. A run where they are
# skipped is by construction an edit-triggered one -- that is the only way the guard excludes them -- so "the
# tree jobs ran" is a sufficient discriminator and no knowledge of the event action is needed.
#
# Body-subject jobs take theirs from the newest run of ANY trigger, because that is the run which read the
# CURRENT body. This is the half #839 got wrong: the push run's citations job had read the body as it was
# before a rewrite, and one rule for everything reported that stale reading as the verdict.
#
# Green requires both, and a class with no run to read from is a failure: silence is not a pass.
# **Coverage is decided in python, not by shell globs.** The first version matched tab-separated job names
# with `case` patterns and could not express "key, then a tab" -- so a mid-list `lint` never matched and the
# tree class looked uncovered in a run that had run it. A matrix job's API name carries its axis
# (`build (ubuntu-24.04)`), so the test is prefix-based: a key matches a job named exactly it, or named it
# followed by a space.
run_covers() {
	python3 - "$1" "$2" <<'PYCOV'
import json, sys
v = json.load(open(sys.argv[1]))
want = sys.argv[2].split()
ran = [j["name"] for j in v.get("jobs", []) if j.get("conclusion") not in (None, "", "skipped")]
for key in want:
    if not any(n == key or n.startswith(key + " ") for n in ran):
        raise SystemExit(1)
raise SystemExit(0)
PYCOV
}

treerun=""
bodyrun=""
for id in $runs; do
	fetch_run "$id" > "$prefix.$id.json" 2>&1 || continue
	if [ -z "$bodyrun" ] && run_covers "$prefix.$id.json" "$bodykeys"; then
		bodyrun=$id
	fi
	if [ -z "$treerun" ] && run_covers "$prefix.$id.json" "$treekeys"; then
		treerun=$id
	fi
	if [ -n "$bodyrun" ] && [ -n "$treerun" ]; then
		break
	fi
done
if [ -z "$treerun" ]; then
	echo "ciwatch: FAIL no run for $sha ran its tree-subject jobs ($(printf '%s ' $treekeys))." >&2
	echo "         A run whose required jobs were skipped is not a verdict, whatever its conclusion says." >&2
	exit 1
fi
if [ -z "$bodyrun" ]; then
	echo "ciwatch: FAIL no run for $sha ran its body-subject jobs ($(printf '%s ' $bodykeys))." >&2
	exit 1
fi
if [ "$treerun" = "$bodyrun" ]; then
	echo "ciwatch: one run carries both classes: $treerun" >&2
else
	echo "ciwatch: tree-subject verdict from run $treerun; body-subject from run $bodyrun -- a body edit fired" >&2
	echo "         a second run that skipped the tree jobs and read the CURRENT body (#839's sequence)" >&2
fi

# --- wait on each, then assert each class against its own run ----------------------------------------------
#
# Waiting on both is what makes this a verdict rather than a snapshot: the tree run may still be building while
# the body run has long finished, and the reverse after an edit.
for id in $treerun $bodyrun; do
	if [ -z "${CIWATCH_FIXTURE:-}" ]; then
		gh run watch "$id" --repo "$repo" > /dev/null 2>&1
		fetch_run "$id" > "$prefix.$id.json" 2>&1
	fi
done
cp "$prefix.$treerun.json" "$prefix.verdict"

assert_class() {
	python3 - "$1" "$2" "$3" <<'PYCLS'
import json, sys
path, label, want = sys.argv[1], sys.argv[2], sys.argv[3].split()
v = json.load(open(path))
jobs = v.get("jobs", [])
by = {j["name"]: j.get("conclusion") for j in jobs}
bad, missing, unfinished = [], [], []
for key in want:
    hits = [n for n in by if n == key or n.startswith(key + " ")]
    if not hits:
        missing.append(key)
        continue
    for n in hits:
        c = by[n]
        if c in (None, ""):
            unfinished.append(n)
        elif c == "skipped":
            bad.append(n + " (skipped)")
        elif c != "success":
            bad.append(f"{n} ({c})")
print(f"ciwatch: {label}: {len(want)} key(s) over {len(jobs)} job(s)", file=sys.stderr)
for kind, names in (("missing", missing), ("unfinished", unfinished), ("failed", bad)):
    if names:
        print(f"ciwatch: FAIL {label}: {kind}: " + ", ".join(sorted(names)), file=sys.stderr)
if missing or unfinished or bad:
    raise SystemExit(1)
PYCLS
}

fail=0
assert_class "$prefix.$treerun.json" "tree-subject (run $treerun)" "$treekeys" || fail=1
assert_class "$prefix.$bodyrun.json" "body-subject (run $bodyrun)" "$bodykeys" || fail=1
if [ "$fail" -ne 0 ]; then
	echo "ciwatch: FAIL not green" >&2
	exit 1
fi
echo "ciwatch: GREEN, and every required job ran" >&2
