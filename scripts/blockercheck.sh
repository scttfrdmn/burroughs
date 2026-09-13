#!/usr/bin/env sh
# blockercheck.sh — a `Status: blocked — #N` in the litmus battery whose #N is CLOSED is a stale blocker.
#
# The litmus pre-registration control (`TestEveryClauseInSectionsTwoThroughFiveIsPreregistered`) checks a
# case's Status *format* — `blocked — #N` or `implemented — TestName` — and, for the implemented form, that
# the named test resolves. It does **not** check whether a `blocked`'s `#N` is still open. So a case whose
# blocker landed months ago keeps a green: `sp1` read `blocked — #554` while #554 (spawn, ADR 0068) was
# closed and its mechanism was on `main`, and nothing noticed. A `Status: blocked — #N` that can go stale
# silently is a status nobody re-reads (recon #742, finding F1; the #264 shape — a control that names the
# fact it expects cannot notice that fact is missing).
#
# **This is a separate sweep, not a phase of `make check`**, for `closecheck.sh`'s reason: it queries issue
# state over the network, and `make check` is hermetic (CI has no guaranteed auth on that path). It runs
# where `closecheck`/`citecheck` run — a manual sweep and a CI citations-style step — and reports, per the
# chair's ruling, rather than editing: correcting a stale case's registration is that case's own PR.
#
# Exit non-zero if any cited blocker is closed. Usage: blockercheck.sh [doc]
set -eu

doc="${1:-docs/litmus-battery-preregistration.md}"
[ -f "$doc" ] || { echo "blockercheck: no such file: $doc" >&2; exit 2; }

# Only the `- **Status:** blocked — #N` lines are blocker claims; a `#N` in prose (a "Blocked by:" bullet,
# the sequencing narrative) is a reference, not a claim that the case is blocked. So the sweep is scoped to
# Status lines, and each is attributed to the `#### Case` header above it.
stale=0
for n in $(grep -oE '^- \*\*Status:\*\* blocked — #[0-9]+' "$doc" | grep -oE '[0-9]+' | sort -un); do
	state=$(gh issue view "$n" --json state -q .state 2>/dev/null || echo UNKNOWN)
	case "$state" in
	CLOSED)
		echo "STALE: blocker #$n is CLOSED but is still a case's Status:"
		awk -v n="$n" '
			/^#### Case /{c=$0}
			$0 ~ ("^- \\*\\*Status:\\*\\* blocked — #" n "([^0-9]|$)"){printf "    %s  (%s:%d)\n", c, FILENAME, NR}
		' "$doc"
		stale=$((stale + 1))
		;;
	UNKNOWN)
		echo "blockercheck: could not resolve #$n's state (no gh auth?) — not counted stale" >&2
		;;
	esac
done

if [ "$stale" -gt 0 ]; then
	echo "blockercheck: $stale stale blocker(s). A case's blocker landed but its Status still reads blocked;"
	echo "re-read the case (it may be ready) and correct its registration in that case's own PR."
	exit 1
fi
echo "blockercheck: no stale blockers ($doc)."
