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
# instrument is not a lenient one.
#
# Usage: scripts/xcheck-amd64.sh [command...]        (default: go test ./...)
#        XCHECK_HOST=other.local scripts/xcheck-amd64.sh go test ./internal/spec/
set -eu

host=${XCHECK_HOST:-janus.local}
dest=${XCHECK_DEST:-burroughs-xcheck}
image=${XCHECK_IMAGE:-golang:1.26}
if [ "$#" -eq 0 ]; then
	cmd="go test ./..."
else
	cmd="$*"
fi

if [ ! -f go.mod ] || [ ! -d internal ]; then
	echo "xcheck: run from the repository root" >&2
	exit 2
fi

# Dot-aware and sidecar-excluding, so this is the number the *consumer's* globber sees.
localwast=$(find testdata/spec -maxdepth 1 -name '*.wast' ! -name '._*' 2>/dev/null | wc -l | tr -d ' ')

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
	echo "xcheck: verdict from NATIVE x86_64 ($host), exit $st"
	exit "$st"
fi

echo "xcheck: $host unreachable over ssh — falling back to the container." >&2
state=$(docker_state)
case "$state" in
serving)
	st=0
	run_container || st=$?
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
