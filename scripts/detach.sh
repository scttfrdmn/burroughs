#!/usr/bin/env bash
# detach.sh <stampfile> <timeout-seconds> -- <command...>
#
# A launched process is a claim that something will end it. This is the claim, made once, in an
# artifact — so a detached run's bound is a property of a thing rather than of the operator's memory.
# ADR 0072, filed against #659.
#
# Three things end the command, and the stamp file names all three before the command starts:
#
#   1. the command finishing;
#   2. a wall-clock deadline;
#   3. the *session* going away.
#
# (3) is the session and not the parent on a measurement, not a preference. A backgrounded child
# outlives its launching shell by design here: the shell that types the command is gone within about
# two seconds while the child runs on, so `exit when your parent dies` — read against the immediate
# parent — would end a CI watcher having watched nothing. And `$PPID` cannot even see it: in `zsh` a
# subshell inherits the variable rather than recomputing it, so a child reads its *grandparent* and
# never updates on reparenting. What the child must not outlive is the session that wanted the answer.
#
# The pid record is written **first**, before any work, because a pid written at exit is recorded
# exactly in the runs that did not leak — the one population that never needs it. The process *group*
# is recorded beside it: a watcher spawns `gh`, so the killable handle is the group, not the pid.
#
# A terminal line is appended on **every** path, including expiry. Writing nothing on the abnormal
# path is the hole a killed watcher already left once: the verdict file did not exist, and a file that
# does not exist cannot say whether the run was green or whether the watcher died.
#
# Exit status: the command's own, when the command is what ended it. 124 on the deadline (GNU
# timeout's code, for the same meaning), 125 when the session went away, 120 when the child was ended
# from outside and left no status behind, 2 on a usage error. 120 rather than a code in the 126–127
# range because those already mean something about *invoking* a command, and a number that means two
# things is what the `reason=` word in the stamp file exists to avoid.
set -uo pipefail

usage() {
	echo "usage: detach.sh <stampfile> <timeout-seconds> -- <command...>" >&2
	exit 2
}

[ $# -ge 4 ] || usage
stamp=$1
timeout=$2
shift 2
[ "${1:-}" = "--" ] || usage
shift
[ $# -ge 1 ] || usage
case $timeout in
'' | *[!0-9]*) echo "detach.sh: timeout must be whole seconds, got '$timeout'" >&2; exit 2 ;;
esac
[ "$timeout" -gt 0 ] || { echo "detach.sh: timeout must be positive" >&2; exit 2; }

# The session handle, walked rather than assumed. detach.sh's own parent is the shell that launched
# it, which may itself be nested, so climb until a `claude` turns up. Bounded at six links because an
# unbounded walk up a process tree ends at pid 1 on every box and would happily accept it.
#
# DETACH_SESSION_PID overrides the walk. It exists for the injection that points the handle at a dead
# pid: a bound that has never been watched fire has not been shown to exist.
session=${DETACH_SESSION_PID:-}
session_note=env
if [ -z "$session" ]; then
	session_note=unresolved
	p=$PPID
	for _ in 1 2 3 4 5 6; do
		[ -n "$p" ] && [ "$p" -gt 1 ] 2>/dev/null || break
		c=$(ps -o comm= -p "$p" 2>/dev/null | tr -d ' ')
		case ${c##*/} in
		claude)
			session=$p
			session_note=walked
			break
			;;
		esac
		p=$(ps -o ppid= -p "$p" 2>/dev/null | tr -d ' ')
	done
fi
# A handle that might name a recycled pid is worse than no handle: it converts a leak into a
# wrongly-killed run at an unrelated moment. So an unresolved session leaves the deadline standing
# alone, and says so in the file rather than falling back to something that merely resolves.
if [ -n "$session" ] && ! kill -0 "$session" 2>/dev/null; then
	session_note=$session_note-already-dead
fi

started=$(date -u +%Y-%m-%dT%H:%M:%SZ)
deadline=$(($(date +%s) + timeout))
deadline_at=$(date -u -r "$deadline" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null ||
	date -u -d "@$deadline" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || echo "+${timeout}s")

rcfile=$(mktemp "${TMPDIR:-/tmp}/detach-rc.XXXXXX")

# Job control on, so the background job leads its own process group and the whole group is killable
# without the script killing itself. The wrapper subshell records the command's status in a file
# rather than leaving the loop to infer it: a zombie answers `kill -0` until it is reaped, and when
# bash reaps is not a thing to rest a termination condition on.
set -m
(
	set +e
	"$@"
	echo $? >"$rcfile"
) &
child=$!
set +m
cpgid=$(ps -o pgid= -p "$child" 2>/dev/null | tr -d ' ')
[ -n "$cpgid" ] || cpgid=$child

{
	echo "detach: start=$started stamp=$stamp"
	echo "detach: launcher_pid=$$ child_pid=$child child_pgid=$cpgid"
	echo "detach: session_pid=${session:-none} session=$session_note"
	echo "detach: timeout=${timeout}s deadline=$deadline_at"
	echo "detach: kill_handle=\"kill -TERM -- -$cpgid\""
	echo "detach: command=$*"
} >>"$stamp"

reason=
rc=
while :; do
	# The status file is checked first, and that ordering is the whole distinction between the two
	# child conditions: the wrapper writes it *before* exiting, so a child that ended on its own is
	# always reported with its own status, and only a child that died without recording one falls
	# through to `child-gone` below.
	if [ -s "$rcfile" ]; then
		reason=child
		rc=$(tr -dc '0-9' <"$rcfile")
		break
	fi
	# **The launcher must not outlive its own child.** Found by this script's own battery, and by the
	# row that exercised the feature it advertises: killing the group with the `kill_handle` recorded
	# above takes the status-writing wrapper with it, so the file never appears — and with a distant
	# deadline and a live session, the loop then polled for the full remaining timeout over a child
	# that no longer existed. A launcher holding a ten-minute claim on nothing is the leak class this
	# script exists to close, reproduced inside the closure. Anything that ends the child from outside
	# — the harness, an operator, a later session using the handle — must end the launcher too.
	if ! kill -0 "$child" 2>/dev/null; then
		reason=child-gone
		rc=120
		break
	fi
	if [ "$(date +%s)" -ge "$deadline" ]; then
		reason=timeout
		rc=124
		break
	fi
	if [ -n "$session" ] && ! kill -0 "$session" 2>/dev/null; then
		reason=session-gone
		rc=125
		break
	fi
	sleep 1
done

# TERM the group, give it a grace, then KILL. The grace is a duration because a ceiling is one; the
# verdict this wrapper's payload reports is still read from the payload, never from a clock.
killed=none
if [ "$reason" != child ]; then
	if kill -TERM -- "-$cpgid" 2>/dev/null; then
		killed=term
		for _ in 1 2 3; do
			sleep 1
			kill -0 -- "-$cpgid" 2>/dev/null || break
		done
		if kill -0 -- "-$cpgid" 2>/dev/null; then
			kill -KILL -- "-$cpgid" 2>/dev/null && killed=term+kill
		fi
	fi
fi

ended=$(date -u +%Y-%m-%dT%H:%M:%SZ)
echo "detach: end reason=$reason child_exit=${rc:-unknown} killed=$killed end=$ended" >>"$stamp"
rm -f "$rcfile"
exit "${rc:-1}"
