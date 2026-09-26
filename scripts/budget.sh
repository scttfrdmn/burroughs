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
# ## The gauge checks BOTH ends, and the lower end FAILS rather than warns
#
# A job that uses almost none of its budget is as suspect as one that uses almost all of it. The proof is
# this script's own first run in anger: `witnesses-norace used 0s of 1800s (0%)` — `go test` had served a
# CACHED result because the target omitted `-count=1`, so the job whose whole purpose was to be an authority
# reported green having executed nothing.
#
# An upper-end warning cannot see that. A pass at 0s and a pass at 83s print the same word, which is the same
# vacuum the upper end exists for, arrived at from below. So `--floor` is a hard FAILURE, not a warning:
# a run that finishes implausibly fast has not answered the question.
#
# **The floor is a STATED SECONDS VALUE, never a percentage of the budget.** A percentage would move whenever
# the budget moved, and the budget moves for reasons — runner drift, added work — that have nothing to do
# with how fast the work *cannot* be. It is set from the lowest elapsed time CI has actually observed on
# either arch, halved.
#
# Usage: budget.sh <seconds> <label> [--floor <seconds>] -- <command...>
set -eu

budget=${1:?usage: budget.sh <seconds> <label> [--floor <seconds>] -- <command...>}
label=${2:?usage: budget.sh <seconds> <label> [--floor <seconds>] -- <command...>}
shift 2
floor=0
if [ "${1:-}" = "--floor" ]; then
	floor=${2:?--floor needs a value in seconds}
	shift 2
fi
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
if [ "$floor" -gt 0 ]; then
	printf 'budget: %s used %ss of %ss (%s%%), floor %ss\n' "$label" "$elapsed" "$budget" "$pct" "$floor" >&2
else
	printf 'budget: %s used %ss of %ss (%s%%), no floor set\n' "$label" "$elapsed" "$budget" "$pct" >&2
fi

# **The lower end is checked BEFORE the command's own exit code is honoured**, because an implausibly fast
# pass is a failure of the measurement rather than of the work, and reporting the work's success would be
# reporting a verdict the run did not earn.
if [ "$floor" -gt 0 ] && [ "$elapsed" -lt "$floor" ]; then
	printf '\n' >&2
	printf 'FAIL  %s finished in %ss, below its %ss floor. A run that fast has not done the work:\n' \
		"$label" "$elapsed" "$floor" >&2
	printf '      the first time this gauge ran, a missing -count=1 served a CACHED pass at 0s. Check that\n' >&2
	printf '      the tests actually executed before treating this as a speed improvement.\n' >&2
	exit 1
fi

if [ "$pct" -ge 75 ]; then
	printf '\n' >&2
	printf '::warning title=%s budget::%s used %s%% of its %ss budget (%ss). Past 75%%: either the work\n' \
		"$label" "$label" "$pct" "$budget" "$elapsed" >&2
	printf '         grew or the runner drifted, and the two are distinguishable only while there is still\n' >&2
	printf '         room. Re-measure before adding to this job.\n' >&2
fi
exit "$rc"
