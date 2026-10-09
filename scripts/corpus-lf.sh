#!/usr/bin/env sh
# corpus-lf — keep a vendored corpus's working tree at LF, and REPAIR it when it is not.
#
# ## Why a corpus needs this when the repo's own `.gitattributes` exists
#
# `.gitattributes` governs *this* repository. The corpora are separate git repositories under
# gitignored paths, created by `git init` + `git fetch --depth 1` + `git checkout --detach` — **not
# by `git clone`**, which no script here uses: a shallow clone of a default branch cannot be checked
# out at an arbitrary rev, so all three fetch scripts take the longer route. Being separate
# repositories, they inherit the *machine's* `core.autocrlf`, which Git for Windows' installer sets
# to `true`. So on Windows the corpora arrive CRLF however correct this repository's own attributes
# are.
#
# Measured on the Windows CI job (#934's first run): **7 reference-parser controls failing** —
# `TestExpr1LeadersMatchTheReference`, `TestPlaininstrShapesMatchTheReference`,
# `TestIdxLookupKindsMatchTheReference`, `TestIdxPairLookupKindsMatchTheReference`,
# `TestLabelTakingArmsMatchTheReference`, `TestLabelLookupProductionsAreAllRead`,
# `TestReferenceDecodesUTF8AtNameAndVarOnly` — all of which read the reference OCaml sources and
# compare byte-exact productions. A `\r` in a lexer rule is a wrong answer, not a formatting
# preference.
#
# ## Why it REPAIRS rather than only asserting (chair's ruling, 2026-10-09)
#
# Setting the config fixes the next checkout. It does **nothing** for a corpus already checked out
# at the pinned revision, because the fetch scripts' update path is a no-op in exactly that case —
# the revision already matches, so nothing is re-materialised and the CRLF worktree survives. An
# assertion that only *failed* there would leave every existing Windows checkout broken until
# someone deleted it by hand, and "delete the directory and re-run" is not a repair a script should
# make a human perform when it can do it itself.
#
# So `verify` repairs, then **reads the result back** and fails if the repair did not take. A repair
# whose result is never read back is a claim, not a fix.
#
# ## The repair is two stages, and the first draft had the reason for the second one WRONG
#
# This script was first written to clear the index before resetting, on the reasoning that `git
# reset --hard` decides whether to rewrite a file from cached stat information, that a config change
# touches no file's mtime or size, and that a stat-clean worktree would therefore be left alone.
#
# **Measured, and that is not what happens.** Both scenarios were run against the real corpus on git
# 2.54.0 (Apple Git-157), macOS:
#
#	worktree CRLF, index stat recorded at LF (git reports the file MODIFIED)
#	  → plain `reset --hard` repaired it: 37594 → 36686 bytes
#	worktree CRLF checked out *under* `autocrlf=true`, so the index stat agrees
#	  and `git status` reports the tree CLEAN — the real Windows shape
#	  → plain `reset --hard` STILL repaired it: 37594 → 36686 bytes
#
# So the cheap path is sufficient here and the elaborate justification for the expensive one was
# false. It is kept anyway, as a *second* stage reached only when the readback says the first did not
# work — because the platform where this matters is the one scenario that cannot be measured from
# here: Windows git, with a different filesystem and different stat granularity. Keeping it costs one
# branch that normally does not run; removing it would bet the Windows repair on a measurement taken
# on macOS.
#
# That ordering is the point: the stage that is measured to work runs first, the stage that is
# insurance runs only on evidence, and neither is justified by a sentence nobody checked.
#
# Clearing the index is safe here in a way it would not be in a working repository: a corpus is a
# disposable vendored checkout pinned by SHA, with no local work to lose.
#
# Usage:
#   corpus-lf.sh config <dest>              # set the LF config in that repo, before any checkout
#   corpus-lf.sh verify <dest> <rev>        # read relative paths on stdin, one per line
set -eu

mode=${1:?usage: corpus-lf.sh config <dest> | corpus-lf.sh verify <dest> <rev> (paths on stdin)}
dest=${2:?usage: corpus-lf.sh config <dest> | corpus-lf.sh verify <dest> <rev> (paths on stdin)}

# has_cr <file> — true when the file contains a carriage return.
#
# `tr -d` the \r out and compare to the original: identical means there were none. Done this way
# rather than with `grep $'\r'` because `$'...'` is a bashism and these scripts are `/bin/sh`, and
# rather than with a literal CR in the source because a literal CR in a file about stripping CRs is
# the kind of thing a well-meaning editor silently normalises.
has_cr() {
	! tr -d '\r' <"$1" | cmp -s - "$1"
}

# cr_list <dest> <paths…> — echo the subset of paths that contain a CR. Used for the first scan and
# for both readbacks, so "which files are wrong" is computed one way and cannot drift between the
# question and its re-ask.
cr_list() {
	_d=$1
	shift
	for _p in "$@"; do
		[ -f "$_d/$_p" ] || continue
		if has_cr "$_d/$_p"; then
			printf '%s\n' "$_p"
		fi
	done
}

case $mode in
config)
	# `--local` is explicit: without it a `config` call inside a repo still writes the local file,
	# but saying so means a future reader does not have to know that to trust the line.
	git -C "$dest" config --local core.autocrlf false
	git -C "$dest" config --local core.eol lf
	;;

verify)
	rev=${3:?usage: corpus-lf.sh verify <dest> <rev> (paths on stdin)}

	# **The revision is resolved BEFORE anything is scanned, and that ordering is the whole point.**
	#
	# The scan's verdict does not depend on `$rev` when the tree is already clean: the repair is the
	# only consumer, and a clean tree never reaches it. So a mistyped revision would print
	# `LF-clean over N file(s)` and exit 0 — a green from a check that never used the argument it
	# was handed, which is grave #549's shape exactly. `TestABadRevisionIsNeverAPass` derives its
	# domain from the scripts' own usage strings and caught this on the first gate run.
	if ! git -C "$dest" rev-parse --verify --quiet "$rev^{commit}" >/dev/null 2>&1; then
		echo "corpus-lf: $rev does not resolve to a commit in $dest, so this cannot run." >&2
		echo "           Refused before scanning rather than at the repair, because a clean tree" >&2
		echo "           never reaches the repair -- so the scan would have reported LF-clean and" >&2
		echo "           exited 0 on a revision that does not exist (grave #549)." >&2
		exit 1
	fi

	# The paths come from the caller because each corpus knows its own authorities: nine OCaml
	# sources for the spec reference, two plus a .wast for the threads reference, and every vector
	# for the suite. Sharing the repair while leaving the population local is the seam -- a shared
	# list would have to be a union, and a union is wrong for all three.
	paths=$(cat)
	if [ -z "$paths" ]; then
		echo "corpus-lf: refusing to report LF-clean over an EMPTY path list for $dest." >&2
		echo "           A comparison against an empty set succeeds, so this would be a green that" >&2
		echo "           checked nothing -- the vacuity law, in the one script whose whole output is" >&2
		echo "           a verdict about files." >&2
		exit 1
	fi

	# Word-split on purpose: these are corpus-relative paths from a list in a sibling script, and
	# none contains whitespace. `set --` makes them positional so `cr_list` can take them as "$@".
	# shellcheck disable=SC2086
	set -- $paths

	present=0
	for p in "$@"; do
		[ -f "$dest/$p" ] || continue
		present=$((present + 1))
	done
	if [ "$present" -eq 0 ]; then
		echo "corpus-lf: refusing to report LF-clean over $dest: none of the $# listed path(s) exist." >&2
		echo "           The list named files, so an empty intersection is a wrong list or a lost" >&2
		echo "           checkout, not a clean tree." >&2
		exit 1
	fi

	dirty=$(cr_list "$dest" "$@")
	if [ -z "$dirty" ]; then
		echo "corpus-lf: $dest LF-clean over $present file(s)"
		exit 0
	fi

	# Announced rather than silent: this rewrites a working tree, and a script that rewrites files
	# without saying so is indistinguishable from one that is broken.
	echo "corpus-lf: $dest has CRLF in $(echo "$dirty" | tr '\n' ' ')" >&2
	echo "corpus-lf: repairing — reset to $rev under core.eol=lf" >&2
	git -C "$dest" reset -q --hard "$rev"

	# Readback 1. The repair's own success is measured, not assumed.
	if [ -n "$(cr_list "$dest" "$@")" ]; then
		echo "corpus-lf: the reset left carriage returns behind; clearing the index and retrying" >&2
		echo "           (the stat-cache case — unreachable in the macOS measurements, kept for" >&2
		echo "            Windows git, which is the platform this repair exists for)" >&2
		git -C "$dest" rm -r -q --cached . >/dev/null
		git -C "$dest" reset -q --hard "$rev"
	fi

	# Readback 2, after whichever stage ran last.
	still=$(cr_list "$dest" "$@")
	if [ -n "$still" ]; then
		echo "corpus-lf: FAILED to repair $(echo "$still" | tr '\n' ' ')" >&2
		echo "           The index was cleared and the tree was reset to $rev, and a carriage return is" >&2
		echo "           still there. Three suspects, in the order they are worth checking:" >&2
		echo "             1. \`config\` was never run on this repo, so the reset re-materialised CRLF" >&2
		echo "                under the machine's own autocrlf. Observed while building this script:" >&2
		echo "                with the config call skipped, BOTH repair stages reproduce the CRLF" >&2
		echo "                faithfully and this message is what you get. Check:" >&2
		echo "                  git -C $dest config --get core.autocrlf   # want: false" >&2
		echo "             2. an attribute INSIDE the corpus — '* text eol=crlf' in its own" >&2
		echo "                .gitattributes beats core.eol." >&2
		echo "             3. committed CRLF bytes upstream, which is not a checkout problem at all." >&2
		echo "           Only the first is fixable from this side." >&2
		exit 1
	fi
	echo "corpus-lf: $dest repaired to LF and verified over $present file(s)"
	;;

*)
	echo "corpus-lf: unknown mode $mode" >&2
	exit 2
	;;
esac
