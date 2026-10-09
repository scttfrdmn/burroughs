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
  # Before any checkout, and on this path too — the corpus is a separate repository and inherits
  # the machine's `core.autocrlf`, which Git for Windows sets to `true`. See corpus-lf.sh.
  ./scripts/corpus-lf.sh config "$dest"
  if [ "$(git -C "$dest" rev-parse HEAD)" != "$rev" ]; then
    git -C "$dest" fetch --depth 1 origin "$rev"
    git -C "$dest" checkout --detach FETCH_HEAD
  fi
else
  # No --depth 1 clone here: a shallow clone of a default branch cannot be checked
  # out at an arbitrary rev. Fetch exactly the one commit wanted instead.
  mkdir -p "$dest"
  git -C "$dest" init -q
  # After `init` and before `fetch`/`checkout`, so the first materialisation is already LF.
  ./scripts/corpus-lf.sh config "$dest"
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
# scope out. parser.mly is the third (#62's stratum), encode.ml the fourth (0011's bridge —
# cited 28 times in internal/text before anything licensed it), syntax/free.ml the fifth (the
# data count section's condition, cited by internal/binary since #22 with nothing resolving it).
# valid/valid.ml is the sixth and valid/match.ml the seventh — #9's validator campaign and its
# subtyping companion, licensed *before* the first citation rather than after the twenty-eighth,
# which is encode.ml's lesson taken forward instead of re-earned. They matter more than the others
# for one reason: they are an oracle rather than a cited artifact, so a fetch that dropped them
# would leave the campaign checking itself.
# syntax/mnemonics.ml is the eighth and exec/v128.ml the ninth, both for slice 2's vector typing
# (#305): valid.ml types by *constructor family* and decode.ml names *mnemonics*, so mnemonics.ml
# is the only file that joins them, and v128.ml is where a shape's lane count and lane scalar type
# come from. An oracle reachable only through a join is no more robust than the join's weakest
# link, which is what licensing them together says.
# The list is here rather than derived because a shell script cannot read Go constants;
# TestEveryPinsFetchScriptAssertsItsAuthorities is what keeps the two agreeing.
# **One list, two consumers.** The presence loop below and the LF check after it are the same
# population asked two different questions, so the names are written once. Written twice, an
# authority added to one and not the other would leave the LF check covering a subset — which is
# the narrowing `TestEveryPinsFetchScriptAssertsItsAuthorities` exists to stop, one scope in.
authorities='interpreter/binary/decode.ml
interpreter/text/lexer.mll
interpreter/text/parser.mly
interpreter/binary/encode.ml
interpreter/syntax/free.ml
interpreter/valid/valid.ml
interpreter/valid/match.ml
interpreter/syntax/mnemonics.ml
interpreter/exec/v128.ml'

for f in $authorities; do
  if [ ! -f "$dest/$f" ]; then
    echo "reference vendored at $got but $dest/$f is missing" >&2
    exit 1
  fi
  echo "  $f $(wc -c <"$dest/$f" | tr -d ' ') bytes"
done

# And no carriage returns in any of them, on EVERY path including the already-at-the-right-rev one.
#
# Same reason the assertions above run there: that path is a no-op, so a corpus checked out before
# this config existed keeps its CRLF bytes, and every byte-exact production check then reads a `\r`
# that is not in the reference. The `wc -c` above and this check read the same files for different
# properties — a CRLF checkout inflates that byte count by one per line, which is why a size that
# looks plausible is not evidence of LF.
echo "$authorities" | ./scripts/corpus-lf.sh verify "$dest" "$rev"

echo "reference vendored at $dest ($got)"
