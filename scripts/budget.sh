#!/usr/bin/env sh
# budget — run a command, print its elapsed time against a stated budget, and warn LOUDLY past 75%.
#
# ## Why a tripwire and not just a timeout
#
# `make race`'s 25-minute timeout reported nothing until the day it fired, and when it fired the cause was
# two things at once: `internal/spec` had drifted from the 430-593s the Makefile documented to **760s**, and
# a new test had been added. A budget nobody watches is an alarm with no gauge — it cannot say "you are at
# 90%", only "you are past 100%", and by then the question of which change spent the budget is already
# confounded.
#
# **An unasserted distance is the vacuum**, applied to wall-clock: a pass at 24 minutes and a pass at 4
# minutes print the same word, so nothing can report that the room is gone.
#
# Usage: budget.sh <seconds> <label> -- <command...>
set -eu

budget=${1:?usage: budget.sh <seconds> <label> -- <command...>}
label=${2:?usage: budget.sh <seconds> <label> -- <command...>}
shift 2
[ "${1:-}" = "--" ] && shift

start=$(date +%s)
set +e
"$@"
rc=$?
set -e
end=$(date +%s)
elapsed=$((end - start))

# Integer percent, so this needs no `bc` on a runner that may not have it.
pct=$((elapsed * 100 / budget))
printf 'budget: %s used %ss of %ss (%s%%)\n' "$label" "$elapsed" "$budget" "$pct" >&2
if [ "$pct" -ge 75 ]; then
	printf '\n' >&2
	printf '::warning title=%s budget::%s used %s%% of its %ss budget (%ss). Past 75%%: either the work\n' \
		"$label" "$label" "$pct" "$budget" "$elapsed" >&2
	printf '         grew or the runner drifted, and the two are distinguishable only while there is still\n' >&2
	printf '         room. Re-measure before adding to this job.\n' >&2
fi
exit "$rc"
