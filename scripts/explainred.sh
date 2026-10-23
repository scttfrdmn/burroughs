#!/usr/bin/env bash
# explainred.sh <main-sha> <issue> — record that main's red verdict at <main-sha> is explained by an OPEN issue.
#
# The merge route (`editroute.py`, route 8; the chair's ruling of 2026-10-23) refuses every merge while main's tip
# has a red `ciwatch` record. A red main stops merging until it is repaired or explained. This is the explaining:
# an open issue that owns the failure, written into the record as `explained=<issue>`, so the next merge is
# admitted and the record says why.
#
# It refuses a record that is missing or already green, and an issue that is not open. An explanation that
# points at a closed issue explains nothing.
set -euo pipefail
sha=${1:?usage: explainred.sh <main-sha> <issue>}
issue=${2:?usage: explainred.sh <main-sha> <issue>}
issue=${issue#\#}
root=$(cd "$(dirname "$0")/.." && pwd)
rec="$root/.ci-verdict.d/ci-$sha"
[ -f "$rec" ] || { echo "explainred: no ciwatch record for $sha ($rec); watch main first" >&2; exit 1; }
grep -q '^ciwatch=green$' "$rec" && { echo "explainred: $sha is green; there is nothing to explain" >&2; exit 1; }
state=$(gh api "repos/{owner}/{repo}/issues/$issue" -q .state 2>/dev/null || true)
[ "$state" = "open" ] || { echo "explainred: issue #$issue is not open (state: ${state:-unknown})" >&2; exit 1; }
printf 'explained=%s\n' "$issue" >>"$rec"
echo "explainred: main's red at ${sha:0:12} is explained by #$issue" >&2
