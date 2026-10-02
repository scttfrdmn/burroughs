#!/usr/bin/env bash
# isdryrun — exit 0 when the calling make is a DRY RUN, 1 otherwise. One authority, two consumers.
#
# ## Why this is a file and not two copies
#
# `make -n` is not "nothing runs": GNU make executes any recipe line containing `$(MAKE)` even under `-n`,
# passing `-n` down so a dry run can see into sub-makes. `ci`'s recipe is one continued line containing
# `$(MAKE)`, so **every command on it runs** — which is how `make -n ci` once wrote a real verdict file:
# `exit=0` with the current SHA and `dirty=no`, the exact shape `prmerge.sh` accepts. A forged green,
# manufactured by a command that promises to change nothing.
#
# Two scripts on that line must now decline under a dry run — the verdict writer and the gate lock — and a
# second copy of this test is a second place for the truth to live. It is subtle enough that a copy would
# drift: see below.
#
# ## The detection is MEASURED, because the obvious form is half-broken
#
# Observed on this fleet's make:
#
#	make ci         -> MAKEFLAGS=[]
#	make -n ci      -> MAKEFLAGS=[n]
#	make -n -j4 ci  -> MAKEFLAGS=[n --jobserver-fds=3,4 -j]
#	nested under -n -> MAKEFLAGS=[ --no-print-directory -n]
#	nested, no -n   -> MAKEFLAGS=[ --no-print-directory]
#
# Single-letter options bundle into the **first word with no leading dash**; long options follow as separate
# dashed words. So two tests are needed:
#
#  * the first word's letter bundle — catches `make -n ci` and `make -n -j4 ci`;
#  * a **whole-word** scan — catches a nested `-n`, where the first word is empty.
#
# A first-word-only check passes the direct case and misses the nested one. And the word scan must compare
# **whole words**, because `--no-print-directory` contains an `n` and is present on every nested make in this
# Makefile — a substring test would decline during *every real run*, which is a refusal that looks like the
# mechanism working while disabling it, and nothing would report it.
#
# ## Usage
#
#   if scripts/isdryrun.sh; then ...decline... fi
#
# Prints nothing. The caller says what declining means, because only the caller knows what it was about to do.
set -uo pipefail

flags=${MAKEFLAGS:-}
first=${flags%% *}

case $first in
"" | -*) : ;; # no single-letter bundle present
*n*) exit 0 ;;
esac

for w in $flags; do
	case $w in
	-n | --dry-run | --just-print | --recon) exit 0 ;;
	esac
done

exit 1
