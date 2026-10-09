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
#
# ## The floor is now a SANITY BOUND, not a calibrated check (chair's ruling, 2026-10-09)
#
# The floor outlived its purpose and then cost something. It was re-pinned to 20s/29s, and the race arm
# **failed twice in six runs** at about 2s under 29s — #931 and #934 — with every clause test passing,
# `collected=8`, `mutations=600`, and non-zero deltas on both clause-2 arms. A ~1-in-3 red over a 2-second
# margin, on a condition the log showed was satisfied.
#
# A third observation arrived inside the margin while this very change was being gated: the `make ci` run
# that greened it recorded the race arm at **28s**, one second under the floor being removed here, on the
# machine that floor was calibrated below. The run certifying the fix would have been failed by the defect.
#
# That is not a calibration error to re-pin; it is a proxy competing with the direct measurement that
# replaced it, and losing. **The primary check is the per-test `run` and `pass` events below.** The floor now
# guards one thing those cannot: a run that finished in about a second because *nothing executed* — a cache
# hit the `(cached)` grep somehow misses, or an event parse that silently reads nothing and so finds no
# failures either. At 5s it is an assertion about zero rather than a measurement of work.
#
# **What was deliberately NOT added**, both refused on the same review and worth recording so they are not
# proposed again:
#
#   - *Scraping the counts from `t.Logf` output* (`collected=8`, `mutations=600`, the clause-2 deltas). The
#     tests already assert those exactly — `clause3_test.go` compares against `c3WantCollected` and
#     `c3WantMutations` — so a `pass` event already means they held. Re-asserting them from log text trusts
#     the **line** over the verdict: it would pass with the assertion behind the line deleted. And it would
#     have got an arm backwards — "a non-zero delta on every clause-2 arm" contradicts `mechanism_absent`,
#     whose registered reading *requires* sibling B's delta to be **zero**; what is asserted in both arms is
#     that sibling **A** advanced, without which B's zero is vacuous rather than discriminating.
#   - *A source-level check that `c3WantCollected` is still 8.* That is a second copy of the number.
#     Weakening a witness's constant is a change to a test, visible in the diff and owned by its reviewer.
#
# ## Why one script rather than a pipeline
#
# `go test -json | check` would put the verdict behind a pipe, and **a command's exit status belongs to
# whatever ran last**. So the JSON goes to a file, `go test`'s own status is captured on its own, and the
# assertions read the file. Two verdicts, neither hiding the other.
#
# Usage: witnessrun.sh <label> <budget-seconds> <sanity-bound-seconds> [--race]
#
# A sanity bound of **0 means no bound**: `elapsed -lt 0` is never true, so the check is skipped. That falls
# out of the arithmetic rather than being written, and it is documented here because a test now relies on
# it — `TestWitnessRunRefusesARunThatDidNotDoTheWork` passes 0 for every case whose subject is the event
# stream rather than the clock, so a stub can answer instantly and no content assertion depends on timing.
# An undocumented behaviour something depends on is one refactor away from being removed as dead.
set -eu

label=${1:?usage: witnessrun.sh <label> <budget> <floor> [--race]}
budget=${2:?usage: witnessrun.sh <label> <budget> <floor> [--race]}
# A bound of 0 means no bound -- see the usage note in the header.
floor=${3:?usage: witnessrun.sh <label> <budget> <sanity-bound> [--race]}
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
	    elif [ -n "${CI:-}" ]; then echo ' [CI: sanity bound binds]'; else echo ' [local: sanity bound advisory]'; fi)" >&2
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
		note "FAIL finished in ${elapsed}s, under the ${floor}s SANITY BOUND. This is not a calibration"
		note "     figure and is not about hardware speed: a real run is tens of seconds and a run that"
		note "     executed nothing is sub-second. Under the bound means the primary checks above found"
		note "     nothing to object to in a run that cannot have done the work — so suspect the event"
		note "     PARSING rather than the tests: a \`-json\` format change, or an empty file, makes every"
		note "     grep above succeed by having nothing to match."
		fail=1
	else
		note "note: finished in ${elapsed}s, under the ${floor}s sanity bound, which is ADVISORY locally."
		note "      The bound exists to catch a run that executed nothing; the primary checks above are"
		note "      what decide whether the tests ran."
	fi
fi

[ "$fail" -eq 0 ] || exit 1
exit "$testrc"
