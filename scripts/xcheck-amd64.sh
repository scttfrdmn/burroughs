#!/bin/sh
# xcheck-amd64.sh — run a command on x86-64 from the arm64 dev box, and say which
# instrument answered.
#
# Contract §9 wants both memory models. CI gives both on push; this is the pre-push
# half, for a claim that has to be confirmed before it is written down — a G-1
# demonstration, a redistribution forecast, a flip's own board delta.
#
# Two instruments, in order, and **they are not equivalent**:
#
#   1. `janus.local` (or $XCHECK_HOST) — native x86_64. Real TSO hardware rather than
#      an emulation of it, and fast. Costs a tree copy per run.
#   2. the amd64 container — QEMU on the arm64 host. Slower, needs no copy, exact for
#      this purpose all the same: correctness across memory models, not speed.
#
# The script exists because the copy in (1) has two traps that a prose recipe records
# and an executable can *assert*:
#
#   - **macOS `tar` writes AppleDouble sidecars** (`._address.wast`) unless
#     COPYFILE_DISABLE=1 and --no-mac-metadata are both given. They land in
#     testdata/spec, the harness globs the directory, and twenty instruments redden on
#     a corpus that is 50% junk.
#   - **The obvious verification cannot see that.** `ls *.wast | wc -l` reported 257 on
#     the poisoned tree and 257 on the clean one, because a shell `*` skips a leading
#     dot and Go's `filepath.Glob` does not. So the count below is taken with `find`,
#     dot-aware on both sides, and **reconciled** against the local count rather than
#     floored — an extent has two ends and a floor only watches one (#317).
#
# It also refuses to conflate "the cross-check failed" with "the cross-check did not
# run": every exit path names the instrument, and a no says *which* no. An unavailable
# instrument is not a lenient one. **Command delivery is a third way to not-run** and was
# not in that enumeration until #344: a far-side 126 or 127 is a `NOT RUN`, because a
# command that was never found never judged anything.
#
# **The shape this script preserves, stated because it is the thing a future edit would
# undo (#344): argument boundaries, across one round of far-side re-parsing.** Both
# transports hand a shell a single string, so the words cannot be passed as words — they
# can only be quoted such that the far-side shell reparses them into the same words. The
# invocation `-run 'A|B'` must arrive as one argument containing a `|`, not as a remote
# pipe. `cmd="$*"` collapsed them and made the script's real contract "I run whatever
# this happens to reparse into"; `shquote "$@"` is what keeps it.
#
# Usage: scripts/xcheck-amd64.sh [command...]        (default: go test ./...)
#        XCHECK_HOST=other.local scripts/xcheck-amd64.sh go test ./internal/spec/
set -eu

host=${XCHECK_HOST:-janus.local}
dest=${XCHECK_DEST:-burroughs-xcheck}
image=${XCHECK_IMAGE:-golang:1.26}
# Quote each argument for exactly one round of re-parsing on the far side. See the
# header: this is #344's fix, and the single-quote escape (`'` -> `'\''`) is what makes
# it total rather than merely usually-right.
shquote() {
	for a in "$@"; do
		printf "'%s' " "$(printf '%s' "$a" | sed "s/'/'\\\\''/g")"
	done
}

if [ "$#" -eq 0 ]; then
	cmd="go test ./..."
else
	cmd=$(shquote "$@")
fi

if [ ! -f go.mod ] || [ ! -d internal ]; then
	echo "xcheck: run from the repository root" >&2
	exit 2
fi

# Dot-aware and sidecar-excluding, so this is the number the *consumer's* globber sees.
#
# Through `suite-count.sh` since #340: this script's `find` expression was the first correct
# shell-side count in the repo and, being the only one, it was still a *second* definition of
# the population. It is now the same one the Makefile, CI, and the fetch script use, and the
# one a control holds against `testenv.SuitePaths`. The far side below keeps its own `find`
# pair on purpose — it counts vectors and sidecars *separately*, which is a different question
# and the one that names a poisoned copy.
localwast=$(scripts/suite-count.sh testdata/spec)

native() {
	ssh -o BatchMode=yes -o ConnectTimeout=10 "$host" 'true' 2>/dev/null
}

run_native() {
	echo "xcheck: native x86_64 via $host — copying the working tree (uncommitted state included)"
	ssh -o BatchMode=yes "$host" "rm -rf ~/$dest && mkdir -p ~/$dest"
	COPYFILE_DISABLE=1 tar -c --no-mac-metadata --exclude bin -f - . 2>/dev/null |
		ssh -o BatchMode=yes "$host" "tar -x -C ~/$dest"

	# The two assertions the prose recipe could only ask for politely. Counted with
	# find on the far side too: `*.wast` here means what Go means by it.
	remote=$(ssh -o BatchMode=yes "$host" "cd ~/$dest && find testdata/spec -maxdepth 1 -name '*.wast' | wc -l" | tr -d ' ')
	sidecars=$(ssh -o BatchMode=yes "$host" "cd ~/$dest && find testdata/spec -maxdepth 1 -name '._*' | wc -l" | tr -d ' ')
	if [ "$sidecars" -ne 0 ]; then
		echo "xcheck: FAIL to copy — $sidecars AppleDouble sidecars in testdata/spec on $host." >&2
		echo "  The corpus is poisoned and the board would redden for the copy, not the arch." >&2
		exit 3
	fi
	if [ "$remote" != "$localwast" ]; then
		echo "xcheck: FAIL to copy — testdata/spec holds $localwast .wast here, $remote on $host." >&2
		echo "  Reconciliation, not a floor: too few means a lossy copy, too many means junk." >&2
		exit 3
	fi
	if [ "$localwast" -eq 0 ]; then
		echo "xcheck: testdata/spec is empty — run 'make spec-tests' first; a skip is not a verdict." >&2
		exit 3
	fi
	echo "xcheck: corpus reconciled at $localwast vectors, 0 sidecars; running: $cmd"

	# Bare invocation, status read from its own command. Appending anything to a
	# command replaces its verdict (#289 and its re-scoping).
	ssh -o BatchMode=yes "$host" "cd ~/$dest && $cmd"
}

docker_state() {
	# A hung daemon does not refuse, it hangs, and `docker run` inherits the hang. So
	# the probe is bounded and its three answers are three different facts.
	if ! command -v docker >/dev/null 2>&1; then
		echo "absent"
		return
	fi
	# Status captured from the probe's own invocation, before anything else runs: `$?`
	# read one command later belongs to whatever ran last, which here would be the
	# `[` of the elif.
	probe=0
	timeout 10 docker version --format '{{.Server.Version}}' >/dev/null 2>&1 || probe=$?
	case "$probe" in
	0) echo "serving" ;;
	124) echo "hung" ;;
	*) echo "down" ;;
	esac
}

# A far-side 126 or 127 is the shell reporting that it never ran the command: 127 not
# found, 126 found and not executable. Reporting either as a `verdict` is what #344 was
# filed for — the run said `verdict from NATIVE x86_64, exit 127` and neither channel
# could distinguish "the suite failed on x86-64" from "the suite never started". Note
# what this cannot catch and does not claim to: a command that starts, runs partially,
# and exits 1 is indistinguishable from a real fail, which is why the delivery *shape*
# is fixed above rather than merely detected here.
not_run_if_undelivered() {
	case "$1" in
	126 | 127)
		echo "xcheck: NOT RUN — $2 could not run the command (exit $1: $([ "$1" = 127 ] && echo 'not found' || echo 'not executable'))." >&2
		echo "  Delivered as: $cmd" >&2
		echo "  A mechanism failure, not a verdict. Nothing about the code has been learned." >&2
		exit 4
		;;
	esac
}

run_container() {
	echo "xcheck: emulated amd64 via $image (QEMU) — no copy, mounting the tree; running: $cmd"
	docker run --rm --platform linux/amd64 -v "$PWD":/src -w /src "$image" sh -c "$cmd"
}

if native; then
	# `st=0; cmd || st=$?` and not `cmd; st=$?`: under `set -e` the second form never
	# reaches the assignment on failure, so the verdict line would print only for
	# passes — a report that can only say yes.
	st=0
	run_native || st=$?
	not_run_if_undelivered "$st" "NATIVE x86_64 ($host)"
	echo "xcheck: verdict from NATIVE x86_64 ($host), exit $st"
	exit "$st"
fi

echo "xcheck: $host unreachable over ssh — falling back to the container." >&2
state=$(docker_state)
case "$state" in
serving)
	st=0
	run_container || st=$?
	not_run_if_undelivered "$st" "EMULATED amd64 ($image under QEMU)"
	echo "xcheck: verdict from EMULATED amd64 ($image under QEMU), exit $st"
	exit "$st"
	;;
hung)
	echo "xcheck: NOT RUN — the Docker daemon is up and not answering (probe hit its bound)." >&2
	echo "  Restart it and ask again. This is a mechanism failure, not a verdict: nothing" >&2
	echo "  about the code has been learned, and CI's x86-64 runner is where the answer is." >&2
	exit 4
	;;
down | absent)
	echo "xcheck: NOT RUN — no native host and no Docker daemon ($state)." >&2
	echo "  A mechanism failure, not a verdict. CI's x86-64 runner answers on push." >&2
	exit 4
	;;
esac
