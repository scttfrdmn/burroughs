#!/usr/bin/env sh
# Vendor the upstream Wasm spec test suite (the oracle — contract §9).
# Gitignored; never committed.
#
# Pinned by SHA (#42, Scott's call). The pin trades drift for staleness, and both are
# honest — the question was which the project wants as its default, and the answer is
# staleness, because a stale corpus is *visible in a diff* while drift is visible only as a
# number that moved. The suite stops picking up upstream additions until someone bumps
# `rev` here, which is then a deliberate act with a reviewable diff and a changelog line:
# the same posture as the toolchain-currency rule (0005).
#
# # Why pinning the oracle is not the same as pinning an input, and why it happens anyway
#
# Decision 0007 pinned the *reference* and deliberately declined to pin the suite in the
# same motion, on a real distinction: the reference is an **input** to a generated table, so
# drift there arrives as a diff nobody ordered, whereas the suite is the thing being
# **reported**, so drift moves the board and CI says so loudly.
#
# That reasoning stands and is not repealed here. What #42 added is that "drift is visible"
# is weaker than it sounds: a board that moves says *something* changed, not *what*, and two
# developers on different fetch dates can legitimately disagree about a count. That collides
# with *never quote a suite count that wasn't run* by making it ambiguous which corpus a run
# quoted. **A count is a claim about a corpus, and an unpinned corpus has no identity** —
# the identity-check law (*a verdict without an identity check is hearsay*) pointed at the
# oracle's inputs rather than at a CI run.
#
# The timing is the column-drain era: `unsupportedCeiling` is a monotonic bound over this
# corpus, so an unpinned corpus lets the bound's own subject float. A ceiling that cannot say
# which corpus it bounds is a number, not a control.
set -e

repo="https://github.com/WebAssembly/testsuite"
rev="de54fd27ecf3e68dfd16b6199c548df77b6a2cc1" # 2026-07-29, 257 .wast files
dest="testdata/spec"

# `rev=` deliberately, and in this exact shape: `gen.PinnedRev` already reads
# `^rev="<40 hex>"` out of a shell script for the reference pin, so naming the field the
# same way makes this pin readable by the existing reader with no second parser. *One
# concept, one trigger* (#82) — a duplicated regexp is how a file comes to be registered
# with a mechanism that cannot read it (#78).

if [ -d "$dest/.git" ]; then
  if [ "$(git -C "$dest" rev-parse HEAD)" != "$rev" ]; then
    git -C "$dest" fetch --depth 1 origin "$rev"
    git -C "$dest" checkout --detach FETCH_HEAD
  fi
else
  # No `--depth 1` clone: a shallow clone of a default branch cannot be checked out at an
  # arbitrary rev. Fetch exactly the one commit wanted instead. (Same as fetch-spec-ref.sh,
  # which is the authority this script is copied from rather than re-derived against.)
  mkdir -p "$dest"
  git -C "$dest" init -q
  git -C "$dest" remote add origin "$repo"
  git -C "$dest" fetch -q --depth 1 origin "$rev"
  git -C "$dest" checkout -q --detach FETCH_HEAD
fi

# Assert what was asked for rather than trusting the fetch did it: a pin that is never
# verified is a comment.
#
# Both assertions run on *every* path, including the already-at-the-right-rev one. That is
# not a precaution, it is fetch-spec-ref.sh's grave inherited on purpose: its first draft
# returned early on that path and so skipped its own post-conditions, meaning a checkout at
# the correct SHA with the corpus deleted reported success. *An early return can skip its own
# guard*, and copying a script means copying the bug it already paid for.
got=$(git -C "$dest" rev-parse HEAD)
if [ "$got" != "$rev" ]; then
  echo "suite pin failed: wanted $rev, got $got" >&2
  exit 1
fi

# A file count with a floor, not a presence check: the vacuity law (*a comparison against an
# empty set succeeds*) says a checkout producing one .wast file passes any `> 0` test while
# making every board count meaningless. The floor is `testenv.MinSuiteFiles`, duplicated here
# because a shell script cannot read a Go constant — and `TestSuitePinIsAssertedByTheFetchScript`
# is what keeps the two agreeing, the same arrangement as `TestFetchScriptAssertsEveryAuthority`.
min=250
n=$(ls "$dest"/*.wast 2>/dev/null | wc -l | tr -d ' ')
if [ "$n" -lt "$min" ]; then
  echo "suite vendored at $got but only $n .wast files (want >= $min)" >&2
  exit 1
fi
echo "spec suite vendored at $dest ($got, $n .wast files)"
