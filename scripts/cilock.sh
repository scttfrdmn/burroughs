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
			if [ "$(field group_owned)" = "yes" ]; then
				# The group was created by detach.sh for this gate, so it holds nothing else.
				echo "        End it by its process GROUP — the group is this gate's own:" >&2
				echo "          scripts/detach.sh --stop <stampfile>   # if it was launched detached" >&2
				echo "          kill -TERM -- -${hpgid:-<pgid>}        # otherwise" >&2
			else
				# **The group is NOT known to be the gate's, so the group kill is not recommended.**
				# A gate started with a plain `&` shares its launcher's process group, and the
				# launcher may be an agent's session or a person's terminal. This advice used to say
				# "End it by its process GROUP, not its pid" unconditionally, and following it killed
				# the launcher — observed, with exit 144.
				leader_cmd=$(ps -o comm= -p "${hpgid:-0}" 2>/dev/null | sed 's|.*/||')
				echo "        End it by its PID. Its process group is NOT known to be this gate's" >&2
				echo "        own (group_owned=no), so a group kill may signal whatever launched it:" >&2
				echo "          kill -TERM $holder" >&2
				echo "        Then check nothing was left behind, because the pid is not the handle" >&2
				echo "        for a gate's children:" >&2
				echo "          pgrep -f golangci-lint" >&2
				echo "        A group kill WOULD end the children, and would also signal the group" >&2
				echo "        leader pid=${hpgid:-?}${leader_cmd:+ ($leader_cmd)}. Do NOT use it if" >&2
				echo "        that is your shell or an agent's session:" >&2
				echo "          kill -TERM -- -${hpgid:-<pgid>}        # reads the warning above first" >&2
			fi
			echo "        Do not delete $lock by hand: that is the mechanism's own defeat." >&2
			exit 1
		fi
		# A dead holder is reclaimed, and said so. Silence here would hide a gate that died without
		# releasing, which is a fact worth one line.
		echo "cilock: reclaiming a stale lock — holder pid=${holder:-none} is gone (sha=${hsha:0:12})" >&2
	fi
	pgid=$(ps -o pgid= -p "$pid" 2>/dev/null | tr -d ' ')
	[ -n "$pgid" ] || pgid=$pid
	# **Is this process group the gate's own, or is it shared with whatever launched it?**
	#
	# It decides which advice the refusal gives, and getting it wrong is not cosmetic: a gate started
	# with a plain `&` from a non-interactive shell stays in the LAUNCHER's group, so
	# `kill -TERM -- -<pgid>` signals the launcher too — an agent's session, or a person's terminal.
	# Observed: a probe of this very mechanism killed its own driver and returned 144.
	#
	# Nothing inside a `make` recipe can work this out; four candidate signals were measured and all
	# fail. `scripts/detach.sh` creates the group, so it exports the pgid it made and this compares
	# against it. **`yes` requires an exact match**, so a missing, stale or forged value gives `no`:
	# being unable to prove ownership is not ownership.
	#
	# A nested gate — one started with `&` from inside a detached run — correctly records `yes`: the
	# pgid matches because the group really is the outer gate's, and that group holds only gate
	# processes, so killing it cannot reach anyone's session.
	group_owned=no
	if [ -n "${BURROUGHS_GATE_PGID:-}" ] && [ "$BURROUGHS_GATE_PGID" = "$pgid" ]; then
		group_owned=yes
	fi
	sha=${4:-$(git rev-parse HEAD 2>/dev/null || echo unknown)}
	{
		echo "pid=$pid"
		echo "pgid=$pgid"
		echo "group_owned=$group_owned"
		echo "sha=$sha"
		echo "when=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
	} >"$lock"
	echo "cilock: acquired $lock pid=$pid pgid=$pgid group_owned=$group_owned sha=${sha:0:12}" >&2
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
