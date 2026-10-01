#!/usr/bin/env sh
# witnessrun — run the Phase 4 clause witnesses and assert they ACTUALLY EXECUTED, from `go test -json`.
#
# ## Why this replaced a time floor
#
# The first version of this gauge used a **wall-clock floor**: fail if the job finished implausibly fast. It
# was earned honestly — a missing `-count=1` had served a cached pass at `0s of 1800s` — but a floor is a
# **proxy**. It measures hardware speed as much as it measures whether the tests ran, so on a fast enough
# machine it fires with nothing wrong, and on a slow enough one a genuinely cached run could clear it.
#
# **The condition it stands in for is directly observable.** `go test -json` emits a `run` and a `pass` event
# per test, a `skip` action when one skips, and `(cached)` in its output when a package result is reused. So
# this asserts the thing itself: *each named witness ran, passed, was not skipped, and was not cached.*
#
# That is *measure the condition, not its proxy*, applied to an instrument this project built an hour earlier.
# The floor survives as a **secondary backstop in CI only** (see `budget.sh`), where the runners it was
# calibrated against actually run.
#
# ## Why one script rather than a pipeline
#
# `go test -json | check` would put the verdict behind a pipe, and **a command's exit status belongs to
# whatever ran last**. So the JSON goes to a file, `go test`'s own status is captured on its own, and the
# assertions read the file. Two verdicts, neither hiding the other.
#
# Usage: witnessrun.sh <label> <budget-seconds> <floor-seconds> [--race]
set -eu

label=${1:?usage: witnessrun.sh <label> <budget> <floor> [--race]}
budget=${2:?usage: witnessrun.sh <label> <budget> <floor> [--race]}
floor=${3:?usage: witnessrun.sh <label> <budget> <floor> [--race]}
# **A floor prefixed `~` is PROVISIONAL: advisory in CI as well as locally.**
#
# A wall-clock floor is calibrated against an engine, and twice now a change to the engine's default has made
# the job faster than its own floor — the #825 pins, then the #835 flip, which cut the witnesses job by ~2.4x
# and reddened CI on a floor everyone already knew was stale. So the rule, set on the #840 review:
#
#   **A change to the engine default resets every time floor to provisional** until it has been re-pinned from
#   an observation taken on the new default, by this file's own rule (half the lowest observed), and re-armed.
#
# Provisional is not "off": the number is still printed and still compared, and the note says the floor is
# unpinned and why. What it does not do is fail a run over a figure nobody has re-measured yet — which would be
# a gate asserting a property of an engine that no longer exists.
provisional=0
case $floor in
~*) provisional=1; floor=${floor#\~} ;;
esac
raceflag=""
if [ "${4:-}" = "--race" ]; then
	raceflag="-race"
fi

GO=${GO:-go}

# **The expected names live here, in one place.** Each is checked on its own, so a witness that stops existing
# — renamed, deleted, or excluded by a `-run` pattern that quietly stopped matching — fails by name rather
# than being absorbed into a green.
witnesses='TestClause1ForkComponentReturns42
TestClause2BlockedGoroutinesPIsHandedOff
TestClause3GCAcrossTwoAgents'

json=$(mktemp)
trap 'rm -f "$json"' EXIT

start=$(date +%s)
set +e
# `-count=1` is here and not in the caller, so no invocation can leave it out. It is also the thing the
# primary check below independently verifies, which is the point: the flag is the intent, the assertion is
# the evidence.
$GO test -json -count=1 $raceflag -timeout 20m \
	-run 'TestClause1|TestClause2|TestClause3' \
	./internal/wasi/ ./internal/component/ >"$json" 2>&1
testrc=$?
set -e
end=$(date +%s)
elapsed=$((end - start))

# Human-readable rendering, because `-json` is unreadable and a reader of CI logs still needs the outcome.
# `Action` values are one per line, so this needs no JSON parser.
grep -o '"Output":"[^"]*"' "$json" | sed 's/^"Output":"//; s/"$//' |
	sed 's/\\n$//; s/\\t/\t/g' | grep -E '^(ok|FAIL|---|    ---|CLAUSE|\s*clause)' || true

fail=0
note() {
	echo "witnessrun: $label: $1" >&2
}

# --- PRIMARY CHECK 1: no cached package result. -------------------------------------------------------------
if grep -q '(cached)' "$json"; then
	note "FAIL a package result was CACHED, so the tests did not run. This is the exact defect a wall-clock"
	note "     floor was introduced for, now caught directly: \`go test\` reuses a result when its inputs are"
	note "     unchanged, and a reused result is not a verdict this job earned."
	fail=1
fi

# --- PRIMARY CHECK 2: every named witness emitted `run` and `pass`, and none was skipped. -------------------
for w in $witnesses; do
	if ! grep -q "\"Action\":\"run\".*\"Test\":\"$w\"" "$json"; then
		note "FAIL $w never emitted a \`run\` event: it did not execute. A witness that is renamed, deleted,"
		note "     or no longer matched by this script's -run pattern disappears silently otherwise."
		fail=1
		continue
	fi
	if grep -q "\"Action\":\"skip\".*\"Test\":\"$w\"" "$json"; then
		note "FAIL $w was SKIPPED. A skip is not a verdict, and this job exists to be these witnesses'"
		note "     authority."
		fail=1
		continue
	fi
	if ! grep -q "\"Action\":\"pass\".*\"Test\":\"$w\"" "$json"; then
		note "FAIL $w ran but did not pass."
		fail=1
	fi
done

# --- PRIMARY CHECK 3: no subtest of a witness was skipped. -------------------------------------------------
# Scoped to the witnesses' own subtests, so an unrelated skip elsewhere in the package is not this check's
# business — and a witness whose ARM silently stops running is.
if grep -E '"Action":"skip".*"Test":"TestClause[123][^"]*/' "$json" >/dev/null 2>&1; then
	note "FAIL a subtest of a witness was skipped:"
	grep -oE '"Action":"skip","Package":"[^"]*","Test":"[^"]*"' "$json" | sed 's/^/       /' >&2
	fail=1
fi

# --- SECONDARY: the wall-clock reading, and the floor, which only BINDS in CI. -----------------------------
./scripts/budget.sh "$budget" "$label" --floor "$floor" -- true >/dev/null 2>&1 || true
pct=$((elapsed * 100 / budget))
printf 'witnessrun: %s used %ss of %ss (%s%%), floor %ss%s\n' \
	"$label" "$elapsed" "$budget" "$pct" "$floor" \
	"$(if [ "$provisional" -eq 1 ]; then echo ' [PROVISIONAL: advisory everywhere, awaiting a re-pin]';
	    elif [ -n "${CI:-}" ]; then echo ' [CI: floor binds]'; else echo ' [local: floor advisory]'; fi)" >&2
if [ "$pct" -ge 75 ]; then
	printf '::warning title=%s budget::%s used %s%% of its %ss budget. Re-measure before adding to it.\n' \
		"$label" "$label" "$pct" "$budget" >&2
fi
if [ "$elapsed" -lt "$floor" ]; then
	if [ "$provisional" -eq 1 ]; then
		note "note: below the ${floor}s floor, which is PROVISIONAL — the engine default changed and this"
		note "      floor has not been re-pinned from an observation on it. The number above is what the"
		note "      re-pin should be derived from (half the lowest observed). The primary checks are what"
		note "      decide whether the tests ran."
	elif [ -n "${CI:-}" ]; then
		note "FAIL below the ${floor}s floor, on a runner this floor was calibrated for."
		fail=1
	else
		note "note: below the ${floor}s floor, which is ADVISORY locally — the floor is calibrated to CI's"
		note "      runners and a faster machine trips it with nothing wrong. The primary checks above are"
		note "      what decide whether the tests ran."
	fi
fi

[ "$fail" -eq 0 ] || exit 1
exit "$testrc"
