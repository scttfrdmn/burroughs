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
dry=no
flags=${MAKEFLAGS:-}
first=${flags%% *}
case "$first" in
	"" | -*) : ;; # no single-letter bundle present
	*n*) dry=yes ;;
esac
for w in $flags; do
	case "$w" in
		-n | --dry-run | --just-print | --recon) dry=yes ;;
	esac
done

if [ "$dry" = "yes" ]; then
	echo "civerdict: REFUSING to write $out — this is a make DRY RUN (MAKEFLAGS=[$flags])." >&2
	echo "           A dry run changes nothing, so it must not produce a verdict: prmerge.sh accepts" >&2
	echo "           a green verdict for the current SHA, and -n does not stop a recipe line that" >&2
	echo "           contains \$(MAKE). No file written." >&2
	exit 0
fi

sha=$(git rev-parse HEAD 2>/dev/null || echo unknown)

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
