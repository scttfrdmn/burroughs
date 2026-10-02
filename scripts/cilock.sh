#!/usr/bin/env bash
# cilock — one gate at a time, enforced rather than remembered.
#
# ## Why
#
# Two `make ci` runs overlapped **three times** in one campaign. Each time the damage was different and none of
# it was the lock's absence being noticed:
#
#  * two linters collided and the gate died on `parallel golangci-lint is running` — a red about nothing in the
#    tree, which cost a diagnosis;
#  * a stale run finished **after** the new run's `rm -f .ci-verdict` and wrote a green naming the older
#    commit. Harmless only because the verdict carries the SHA it gated and `prmerge.sh` refuses a mismatch.
#
# The existing mechanisms make an overlap *harmless*; none of them makes it *impossible*. And **removing the
# verdict at gate start cannot protect against a gate that finishes later** — the write happens after the
# removal, so the file comes back. A lock is the thing that stops the second run from starting at all.
#
# ## The four behaviours, and why each is separately needed
#
#  1. **acquire** writes the holder's pid, process group and start SHA. The pgid is recorded because the pid
#     alone is not a handle — `detach.sh --stop` exists for the same reason — and the SHA because a holder a
#     reader cannot identify is a holder they will kill blind.
#  2. **a live holder refuses**, naming the holder and pointing at `detach.sh --stop`. A refusal that does not
#     say how to proceed teaches the reader to delete the lock file, which is the mechanism's own defeat.
#  3. **a dead holder is reclaimed, loudly.** A lock whose process is gone must not wedge the tree — that is
#     how a lock gets removed by hand and never comes back — and reclaiming silently would hide a gate that
#     died without releasing.
#  4. **`holds`** lets the verdict writer check it still owns the lock before writing. Without it, a run that
#     was reclaimed out from under it still writes a verdict, which is the stale-green shape again: this run's
#     exit code against a tree another run is gating.
#
# ## Why a script and not a Make recipe
#
# The same reason as `civerdict.sh`: a recipe carries two escaping layers and cannot be driven from a witness
# with controlled state. The inline version of the verdict writer was wrong in a way worse than absent (`dirty`
# unconditionally `yes`), and the witness could not have caught it.
#
# ## Usage
#
#   cilock.sh acquire <lockfile> <pid> [sha]
#   cilock.sh holds   <lockfile> <pid>
#   cilock.sh release <lockfile> <pid>
set -uo pipefail

mode=${1:?usage: cilock.sh acquire|holds|release <lockfile> <pid> [sha]}
lock=${2:?usage: cilock.sh $mode <lockfile> <pid>}
pid=${3:?usage: cilock.sh $mode <lockfile> <pid>}

field() { sed -n "s/^$1=//p" "$lock" 2>/dev/null | head -1; }

# A DRY RUN must not touch the lock, for the reason documented in scripts/isdryrun.sh: `make -n ci` runs every
# command on the `$(MAKE)` line, so without this an `make -n ci` would write a real lock, and — worse — would
# be REFUSED while a real gate was running, reporting a conflict about a run that is not happening.
if "$(dirname "$0")/isdryrun.sh"; then
	echo "cilock: dry run (MAKEFLAGS=[${MAKEFLAGS:-}]) — not touching $lock" >&2
	exit 0
fi

case $mode in
acquire)
	if [ -f "$lock" ]; then
		holder=$(field pid)
		hpgid=$(field pgid)
		hsha=$(field sha)
		hwhen=$(field when)
		if [ -n "$holder" ] && kill -0 "$holder" 2>/dev/null; then
			echo "cilock: REFUSED — a gate is already running." >&2
			echo "        holder pid=$holder pgid=${hpgid:-?} sha=${hsha:0:12} since=${hwhen:-?}" >&2
			echo "        Two gates at once collide on the linter and can write each other's verdict:" >&2
			echo "        a run finishing LATE overwrites the file a newer run already reset." >&2
			echo "        End it by its process GROUP, not its pid:" >&2
			echo "          scripts/detach.sh --stop <stampfile>   # if it was launched detached" >&2
			echo "          kill -TERM -- -${hpgid:-<pgid>}        # otherwise" >&2
			echo "        Do not delete $lock by hand: that is the mechanism's own defeat." >&2
			exit 1
		fi
		# A dead holder is reclaimed, and said so. Silence here would hide a gate that died without
		# releasing, which is a fact worth one line.
		echo "cilock: reclaiming a stale lock — holder pid=${holder:-none} is gone (sha=${hsha:0:12})" >&2
	fi
	pgid=$(ps -o pgid= -p "$pid" 2>/dev/null | tr -d ' ')
	[ -n "$pgid" ] || pgid=$pid
	sha=${4:-$(git rev-parse HEAD 2>/dev/null || echo unknown)}
	{
		echo "pid=$pid"
		echo "pgid=$pgid"
		echo "sha=$sha"
		echo "when=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
	} >"$lock"
	echo "cilock: acquired $lock pid=$pid pgid=$pgid sha=${sha:0:12}" >&2
	;;
holds)
	# Deliberately quiet on success: this runs on the verdict path and a line per gate run would train the
	# reader to skip the writer's output, where the SHA and the dirty flag live.
	[ -f "$lock" ] || { echo "cilock: $lock is gone, so this run no longer holds it" >&2; exit 1; }
	owner=$(field pid)
	if [ "$owner" != "$pid" ]; then
		echo "cilock: $lock is held by pid=${owner:-none}, not by this run (pid=$pid)" >&2
		exit 1
	fi
	;;
release)
	# Only the holder may release. A run that was reclaimed must not delete the lock of the run that
	# reclaimed it — that would hand the tree to a third gate while the second is still going.
	if [ -f "$lock" ]; then
		owner=$(field pid)
		if [ "$owner" = "$pid" ]; then
			rm -f "$lock"
		else
			echo "cilock: not releasing $lock — held by pid=${owner:-none}, not by this run (pid=$pid)" >&2
		fi
	fi
	;;
*)
	echo "cilock: unknown mode '$mode'" >&2
	exit 2
	;;
esac
