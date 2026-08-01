#!/usr/bin/env sh
# Vendor the reference interpreter (the *authority* — decision 0007).
#
# This is not a new upstream authority: WebAssembly/spec is the repository the
# vendored suite's expected strings were minted by, and interpreter/binary/decode.ml
# is where "integer too large" is a string literal. The suite samples the spec; this
# is the spec's other representation, and it is the only one that can falsify an
# accept-direction fact (contract §9 G-3).
#
# Gitignored, never committed — same posture as the suite.
#
# Pinned by SHA, and the contrast with fetch-spec-tests.sh is deliberate rather than
# an inconsistency. That script floats on the upstream tip because the suite is the
# thing being *reported*: when it drifts, the board moves and CI says so, loudly. This
# reference is an *input* to a generated table, so a silent drift here arrives as a
# diff nobody ordered. An input to a report gets pinned; a report does not have to be.
# (Whether the suite fetch should be pinned anyway is its own question — #42.
# Decision 0007 declines to fix it in passing.)
set -e

repo="https://github.com/WebAssembly/spec"
rev="bdd7164bfe18cf0bd5c3d90ef8cc3b8919fb9c0a" # 2026-07-28
dest="third_party/spec"

if [ -d "$dest/.git" ]; then
  if [ "$(git -C "$dest" rev-parse HEAD)" != "$rev" ]; then
    git -C "$dest" fetch --depth 1 origin "$rev"
    git -C "$dest" checkout --detach FETCH_HEAD
  fi
else
  # No --depth 1 clone here: a shallow clone of a default branch cannot be checked
  # out at an arbitrary rev. Fetch exactly the one commit wanted instead.
  mkdir -p "$dest"
  git -C "$dest" init -q
  git -C "$dest" remote add origin "$repo"
  git -C "$dest" fetch -q --depth 1 origin "$rev"
  git -C "$dest" checkout -q --detach FETCH_HEAD
fi

# Assert what was asked for, rather than trusting that the fetch did it: a pin that
# is never verified is a comment. (CLAUDE.md — a verdict without an identity check is
# hearsay, pointed at a vendored input.)
#
# Both assertions run on *every* path, including the already-at-the-right-rev one.
# The first draft returned early there and so skipped them, which meant a checkout at
# the correct SHA with decode.ml deleted reported success — the precondition excusing
# the check that exists to police it. Found by deleting the file and re-running
# (CLAUDE.md — the way to know a control's green is falsifiable is to break it).
got=$(git -C "$dest" rev-parse HEAD)
if [ "$got" != "$rev" ]; then
  echo "reference pin failed: wanted $rev, got $got" >&2
  exit 1
fi

# Every file testenv licenses as an authority, not just the first one.
#
# It checked decode.ml alone while decode.ml was the only authority, and stayed that way
# after lexer.mll became the second (0009) — a presence check that had silently narrowed
# to a third of its subject, which is the same shape as the early-return defect above one
# scope out. parser.mly is the third (#62's stratum). The list is here rather than derived
# because a shell script cannot read Go constants; TestFetchScriptAssertsEveryAuthority
# is what keeps the two agreeing.
for f in interpreter/binary/decode.ml interpreter/text/lexer.mll interpreter/text/parser.mly; do
  if [ ! -f "$dest/$f" ]; then
    echo "reference vendored at $got but $dest/$f is missing" >&2
    exit 1
  fi
  echo "  $f $(wc -c <"$dest/$f" | tr -d ' ') bytes"
done
echo "reference vendored at $dest ($got)"
