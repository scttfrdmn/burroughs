#!/usr/bin/env sh
# Vendor the **threads proposal's** reference interpreter — the second authority
# (ADR 0007's 2026-08-28 amendment: the pin set is plural).
#
# Gitignored, never committed — same posture as the other two.
#
# # Why a second pin exists at all
#
# ADR 0007's principle is normative: the opcode table is machine-derived from, or
# machine-checked against, the reference interpreter, and *hand-trusted is not on the
# menu*. The reason is contract §9 G-3 — every suite vector bearing on the table is
# `assert_malformed`, so a table that wrongly **accepts** an opcode no vector uses is
# invisible on the board by construction.
#
# The threads proposal was never merged into the core spec that `fetch-spec-ref.sh`
# pins, and the measurement is unambiguous. At bdd7164, across **all nine** files that
# script licenses, `grep -ic atomic` and `grep -ic shared` both return 0 — decode.ml,
# lexer.mll, parser.mly, encode.ml, free.ml, valid.ml, match.ml, mnemonics.ml,
# v128.ml. `third_party/spec/proposals/` holds 17 proposal directories and no
# `threads`. So contract §§2-5 has no authority in the tree at all, and a hand-written
# 0xfe region would reopen exactly the hole 0007 exists to close.
#
# # Why this is a separate script rather than a second `rev=` in the first one
#
# Scott's constraint, on the v1 scoping report: *the two pins are independently dated —
# drift in one must never be silently absorbed by the other.* A single script holding
# two revisions is one edit away from a bump that moves a table generated from the
# other pin, with nothing in the diff saying so. Two scripts make the pins
# structurally independent: separate files, separate `rev=`, separate dates, separate
# `git log` lines. `gen.PinnedRev` reads `^rev="<40 hex>"` out of *any* named script
# and there are already two callers of it (`PinnedRefRev`, `PinnedSuiteRev`), so this
# instantiates the existing pattern rather than inventing one — which is the third of
# Scott's three reasons the second pin is cheap.
#
# # The authority list is NARROWER than the core pin's, on purpose
#
# Two of the nine files the core pin licenses **do not exist at this revision**:
# `interpreter/valid/match.ml` and `interpreter/syntax/mnemonics.ml`. That is not a
# lossy fetch — the proposal is forked from an older core baseline than bdd7164, from
# before either file existed. Stated here because the obvious reading of "a second
# reference" is a mirror of the first, and it is not: the pin set is plural *and the
# file list is per-pin*.
#
# The same baseline gap is why this authority is consulted **clause by clause and
# region by region, never wholesale**, and there are two measured witnesses for how
# badly a wholesale read would go:
#
#   - its `decode.ml` knows prefixes 0xfc, 0xfd and 0xfe but **not 0xfb** — no GC.
#   - its `limits` reads `require (flags land 0xfc = 0)`, so flags 0x04-0x07 are
#     malformed there. Those are memory64's, which this engine accepts and the core
#     pin authorizes.
#
# Taking either file as "the reference" would delete two shipped proposals. What this
# pin is the authority for is the threads-specific clauses and nothing else, and every
# citation to it names the file *and* the clause.
set -e

repo="https://github.com/WebAssembly/threads"
rev="cc535ada1aa21cfaa3cabf3ac73b89acef78a0a0" # 2026-07-30
dest="third_party/spec-threads"

if [ -d "$dest/.git" ]; then
  if [ "$(git -C "$dest" rev-parse HEAD)" != "$rev" ]; then
    git -C "$dest" fetch --depth 1 origin "$rev"
    git -C "$dest" checkout --detach FETCH_HEAD
  fi
else
  # No --depth 1 clone: a shallow clone of a default branch cannot be checked out at
  # an arbitrary rev. Fetch exactly the one commit wanted instead. (Copied from
  # fetch-spec-ref.sh, which is the authority for this script's shape rather than
  # something re-derived against it.)
  mkdir -p "$dest"
  git -C "$dest" init -q
  git -C "$dest" remote add origin "$repo"
  git -C "$dest" fetch -q --depth 1 origin "$rev"
  git -C "$dest" checkout -q --detach FETCH_HEAD
fi

# Assert what was asked for rather than trusting the fetch did it, and assert it on
# **every** path including the already-at-the-right-rev one. Both grave-carried from
# fetch-spec-ref.sh: its first draft returned early there and so skipped its own
# post-conditions, meaning a checkout at the correct SHA with decode.ml deleted
# reported success. *An early return can skip its own guard*, and copying a script
# means copying the bug it already paid for.
got=$(git -C "$dest" rev-parse HEAD)
if [ "$got" != "$rev" ]; then
  echo "threads reference pin failed: wanted $rev, got $got" >&2
  exit 1
fi

# Every file testenv licenses from *this* pin. The list is here rather than derived
# because a shell script cannot read Go constants;
# TestEveryPinsFetchScriptAssertsItsAuthorities is what keeps the two agreeing, and it
# derives the pin set so a third pin is covered on arrival rather than looking like
# this one's hole.
for f in interpreter/binary/decode.ml interpreter/valid/valid.ml \
         interpreter/text/lexer.mll interpreter/text/parser.mly \
         interpreter/binary/encode.ml; do
  if [ ! -f "$dest/$f" ]; then
    echo "threads reference vendored at $got but $dest/$f is missing" >&2
    exit 1
  fi
  echo "  $f $(wc -c <"$dest/$f" | tr -d ' ') bytes"
done

# A positive check on the *content*, which the core pin's script does not need and this
# one does: the whole reason this pin exists is that the other one has no threads
# clauses, so a fetch that landed a threads-free baseline would satisfy every presence
# and size check above while being useless. That is the vacuity shape — the files are
# there, the bytes are there, and the authority is absent. Two greps, one per half of
# what this pin is for.
#
# `|| true` on the grep is load-bearing and was a real failure of this check, found by
# running it: `grep -c` **exits 1 when the count is 0**, which under `set -e` kills the
# script before it can say why. The falsification printed `RC=1` and not one word of
# explanation — *a verdict with no located failure* — so the guard against a
# threads-free baseline reported the same silent 1 as a broken pipe would.
for probe in atomic shared; do
  n=$(grep -ic "$probe" "$dest/interpreter/binary/decode.ml" || true)
  if [ "$n" -eq 0 ]; then
    echo "threads reference vendored at $got but decode.ml has no '$probe' clause." >&2
    echo "  This pin's only purpose is the clauses the core pin lacks; a baseline" >&2
    echo "  without them passes every presence check above and authorizes nothing." >&2
    exit 1
  fi
done

echo "threads reference vendored at $dest ($got)"
