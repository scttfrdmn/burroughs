#!/usr/bin/env bash
# specstress.sh <name> <test-binary> <workers> <target> [VAR=value...]
#
# Runs one test from a compiled test binary as one process per iteration, across <workers> workers, until
# <target> iterations or a failure cap, and records the rate. #1089's reproduction stress and its bisection
# (the chair's ruling of 2026-11-01, item 1) are this script. It runs on a lab host inside a pueue task, and the
# binary is built there (`go test -c [-race] [-tags …] ./internal/spec`), because a `-race` binary cannot be
# cross-compiled without a cgo toolchain.
#
# Each trailing VAR=value is set in the test process's environment only, e.g. GOGC=1 or GOTRACEBACK=system.
#
# Settings, from the environment:
#   SPECSTRESS_RUN      the -test.run pattern (default ^TestThreadsGateLeavesTheCoreBoardUnchanged$, the test
#                       running at #1089's bc75589 fatal)
#   SPECSTRESS_DIR      the directory each iteration runs in (default internal/spec of the tree this script
#                       is in), which is where the test's relative corpus paths resolve
#   SPECSTRESS_OUT      where results go (default ~/specstress); the run's are in <out>/<name>/
#   SPECSTRESS_MAXFAIL  failures before the run stops (default 1, "until the first fatal"); higher measures a rate
#   SPECSTRESS_WHOLE    1 to run the whole binary, every test, as one iteration (#1089's widening: heap corruption
#                       can come from any earlier test in the process). SPECSTRESS_RUN is then ignored.
#
# **What counts as a pass depends on the mode.** One test: exit 0, no SKIP, and that test's `--- PASS:` line.
# Whole binary: exit 0 and a last line of `PASS`, which is what a test binary prints when every test passed. A
# skip is not a failure there, because a full package has licensed skips. A crash or a failed test still exits
# non-zero.
#
# Results in <out>/<name>/:
#   - started and done: counters;
#   - failures: one line per failing iteration;
#   - fail.<n>.log: that iteration's whole output;
#   - STOP: present once the cap is reached;
#   - summary: the closing line.
#
# **An iteration that asks nothing is a failure, not a pass.** A non-zero exit, a SKIP, or no `--- PASS:` line for
# the selected test all count, so a binary that lost its corpus, or a pattern that matches nothing, cannot read
# as 0 in N.
#
# Iterations are claimed under a `mkdir` lock rather than `flock`, which macOS lacks, so the same script runs on
# darwin and linux hosts. Moved from the actor's scratch on the chair's ruling of 2026-11-01, item 5.
# `TestSpecStressCountsWhatItRan` is its witness.
set -u
[ $# -ge 4 ] || { echo "usage: specstress.sh <name> <test-binary> <workers> <target> [VAR=value...]" >&2; exit 2; }
name=$1 bin=$2 workers=$3 target=$4
shift 4
run=${SPECSTRESS_RUN:-^TestThreadsGateLeavesTheCoreBoardUnchanged\$}
want=${run#^}
want=${want%\$}
here=$(cd "$(dirname "$0")/.." && pwd)
out=${SPECSTRESS_OUT:-$HOME/specstress}/$name
mkdir -p "$out"
echo 0 > "$out/started"
echo 0 > "$out/done"
: > "$out/failures"
cd "${SPECSTRESS_DIR:-$here/internal/spec}" || exit 2

lock() { until mkdir "$out/lock.d" 2>/dev/null; do sleep 0.05; done; }
unlock() { rmdir "$out/lock.d"; }

claim() { # prints the claimed iteration number, or -1 when the target is reached or the run is stopped
	local n
	lock
	if [ -e "$out/STOP" ]; then
		n=-1
	else
		n=$(cat "$out/started")
		if [ "$n" -ge "$target" ]; then n=-1; else echo $((n + 1)) > "$out/started"; fi
	fi
	unlock
	echo "$n"
}

passed() { # <rc> <output>: whether the iteration passed, by the mode's rule above
	[ "$1" -eq 0 ] || return 1
	if [ "${SPECSTRESS_WHOLE:-}" = 1 ]; then
		[ "$(tail -n 1 "$2")" = PASS ]
		return
	fi
	! grep -q -- '--- SKIP' "$2" && grep -q -- "--- PASS: $want" "$2"
}

worker() {
	local w=$1 n rc
	shift
	while :; do
		n=$(claim)
		[ "$n" = -1 ] && return
		if [ "${SPECSTRESS_WHOLE:-}" = 1 ]; then
			# -test.v so a failing iteration's log names the test that was running. CI's reproduction at 940e1ad
			# could not say which test took its fault (#1089). The last line is still PASS on a clean run.
			env "$@" "$bin" -test.count=1 -test.v -test.timeout=60m > "$out/cur.$w" 2>&1
		else
			env "$@" "$bin" -test.count=1 -test.v -test.timeout=30m -test.run "$run" > "$out/cur.$w" 2>&1
		fi
		rc=$?
		if ! passed "$rc" "$out/cur.$w"; then
			cp "$out/cur.$w" "$out/fail.$n.log"
			lock
			echo "iteration $n worker $w rc=$rc" >> "$out/failures"
			[ "$(wc -l < "$out/failures")" -ge "${SPECSTRESS_MAXFAIL:-1}" ] && touch "$out/STOP"
			unlock
			[ -e "$out/STOP" ] && return
			continue
		fi
		lock
		echo $(($(cat "$out/done") + 1)) > "$out/done"
		unlock
	done
}

for w in $(seq 1 "$workers"); do
	worker "$w" "$@" &
done
wait
f=$(wc -l < "$out/failures" | tr -d ' ')
echo "$name: $(cat "$out/done") passed, $f failed, target $target, workers $workers" | tee "$out/summary"
