#!/usr/bin/env bash
# civerdict — write the local gate's verdict to .ci-verdict, as a file rather than an exit code.
#
# ## Why the verdict is a file
#
# `make ci`'s verdict was lost three ways in one session. Chaining as `make ci > log; echo rc=$?` hands the
# status to whatever ran last. `pipefail` does not reach across a `;`. And a background task's completion
# notification carries the **wrapper's** exit code — which is how a commit was pushed over a red gate with
# `ci rc=2` sitting unread in the output file the whole time.
#
# So the gate records its own verdict, and `prmerge.sh` reads it. The SHA is the load-bearing field: without
# it the file is a mood, and a green from three commits ago reads exactly like a green from this one.
#
# ## Why this is a script and not a Make recipe
#
# It was a recipe first, and the recipe was wrong in a way that would have been worse than having no check.
# Inside a double-quoted recipe line, `test -n \"$$(git status --porcelain)\"` passes *literal* backslash-quote
# characters to `test`, which is a non-empty string, so `dirty` was **always** `yes` — and `prmerge.sh` would
# have refused every merge. A check that always refuses gets removed, not debugged.
#
# Make's recipes carry two escaping layers (`$$` for `$`, plus the shell's own), and a shell script carries
# one. That is the whole reason this file exists: the logic is identical and one class of error is gone.
# It is also now *testable* — a Make recipe cannot be driven from a witness with a controlled tree state,
# and the first version of the consumer's witness passed against hand-written fixtures while the real
# producer was broken. **A control that tests the helper is not testing the path.**
#
# ## Usage
#
#   scripts/civerdict.sh <exit-code> [output-path]
set -uo pipefail

rc=${1:?usage: civerdict.sh <exit-code> [output-path]}
out=${2:-.ci-verdict}

# --- a DRY RUN must never produce a verdict, whoever starts it ---------------------------------------------
#
# `make -n ci` wrote a real verdict. GNU make executes any recipe line containing `$(MAKE)` even under `-n`,
# passing `-n` down, and `ci`'s recipe is one continued line containing `$(MAKE)` — so this script ran, the
# recursive dry run "succeeded", and it recorded `exit=0` with the current SHA and `dirty=no`. That is exactly
# the file `prmerge.sh` accepts: a **forged green**, produced by a command that promises to change nothing.
#
# The test that found it now points its dry run at a temp path, which stops that one caller. It does not stop
# the next: a hand-typed `make -n ci`, a debugging session, a future test. So the refusal belongs here, in the
# writer, where it covers every caller.
#
# **The flag detection is measured, not guessed, because the obvious form is wrong.** Observed on this make:
#
#	make ci              -> MAKEFLAGS=[]
#	make -n ci           -> MAKEFLAGS=[n]
#	make -n -j4 ci       -> MAKEFLAGS=[n --jobserver-fds=3,4 -j]
#	nested under -n      -> MAKEFLAGS=[ --no-print-directory -n]
#	nested, no -n        -> MAKEFLAGS=[ --no-print-directory]
#
# Single-letter options are bundled into the FIRST word with no leading dash; long options follow as separate
# dashed words. So two tests are needed, and one trap avoided: `--no-print-directory` contains an `n` and must
# never match, which is why the word scan compares whole words rather than searching for a letter.
# The detection lives in scripts/isdryrun.sh — one authority, because the gate lock on the same recipe line
# needs the identical test and a second copy of something this subtle would drift. The measured MAKEFLAGS
# shapes and the two traps are documented there.
if "$(dirname "$0")/isdryrun.sh"; then
	flags=${MAKEFLAGS:-}
	echo "civerdict: REFUSING to write $out — this is a make DRY RUN (MAKEFLAGS=[$flags])." >&2
	echo "           A dry run changes nothing, so it must not produce a verdict: prmerge.sh accepts" >&2
	echo "           a green verdict for the current SHA, and -n does not stop a recipe line that" >&2
	echo "           contains \$(MAKE). No file written." >&2
	exit 0
fi

# --- the SHA must be the tree that was GATED, not the tree that exists now --------------------------------
#
# Observed: a gate started on one commit, ran for minutes, and by the time it reached this line another commit
# had moved the tip — so `git rev-parse HEAD` named a tree the gate had never seen. That run was red, so the
# mislabelling cost nothing; a run that PASSES on A and finishes after a commit to B writes `exit=0 sha=B`,
# which is a green for a tree never tested and is exactly what `prmerge.sh` accepts.
#
# So the caller passes the SHA it captured BEFORE the gates. The fallback to the current HEAD is kept for a
# direct invocation, and a mismatch is reported rather than silently preferred either way: a moved tip means
# the verdict is about neither tree cleanly, and saying so is cheaper than picking.
# --- this run must still HOLD the gate lock -----------------------------------------------------------------
#
# A run whose lock was reclaimed is a run another gate has taken over from. Writing a verdict then is the
# stale-green shape once more: this run's exit code against a tree somebody else is gating. The SHA field
# catches the *common* case, where the tip moved; it cannot catch two gates on the SAME commit, and that is
# exactly what a reclaimed lock means.
#
# Optional arguments, so a direct `civerdict.sh <rc> <out>` keeps working — the witness uses that form, and a
# writer that *required* a lock could not be driven without inventing one.
lockfile=${4:-}
lockpid=${5:-}
if [ -n "$lockfile" ] && [ -n "$lockpid" ]; then
	if ! "$(dirname "$0")/cilock.sh" holds "$lockfile" "$lockpid"; then
		echo "civerdict: REFUSING to write $out — this run no longer holds the gate lock." >&2
		echo "           Another gate reclaimed it, so a verdict written now would carry THIS run's" >&2
		echo "           exit code about a tree the OTHER run is gating. No file written." >&2
		exit 0
	fi
fi

sha=${3:-}
if [ -z "$sha" ]; then
	sha=$(git rev-parse HEAD 2>/dev/null || echo unknown)
else
	now=$(git rev-parse HEAD 2>/dev/null || echo unknown)
	if [ "$now" != "$sha" ]; then
		echo "civerdict: the tip MOVED during the run — gated ${sha:0:12}, now ${now:0:12}." >&2
		echo "           Recording the gated SHA, so prmerge.sh will refuse this verdict against the" >&2
		echo "           current tip. A verdict naming a tree the gate never saw is the forged-green" >&2
		echo "           shape arriving by a different route." >&2
	fi
fi

# `dirty` asks whether the tree the gate ran on is the tree the SHA names. Untracked files count: a gate that
# passed with an uncommitted test file present says nothing about the commit without it.
if [ -n "$(git status --porcelain 2>/dev/null)" ]; then
	dirty=yes
else
	dirty=no
fi

{
	echo "exit=$rc"
	echo "sha=$sha"
	echo "dirty=$dirty"
	echo "when=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
} > "$out"

echo "civerdict: $out exit=$rc sha=${sha:0:12} dirty=$dirty" >&2
