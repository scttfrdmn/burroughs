<!-- Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0 -->

# Laws — Graves and sweeps

A bug becomes an oracle, and a lesson is indexed by shape.

Relocated from `CLAUDE.md`'s `## Disciplines` section, **verbatim**, when that file
became an index (see the restructure PR). Each law's one-line compressed form remains in
`CLAUDE.md` as its recall key and points here for the specimen, the minting record, and the
token it was granted on. Nothing was rewritten in the move: the bodies below are the text as
it stood, which is why superseded wordings still appear inside them where a later ruling
amended rather than replaced.

`CLAUDE.md`'s recall key and each heading here are checked equal by
`TestEveryLawIsIndexed` (`internal/testenv`), so the two cannot drift.

### Artifacts become oracles.

- **Artifacts become oracles.** Bugs found by hand become regression tests
  in the same session. Graves get marked: a comment at the fix site naming
  the lesson and citing the issue.

### Sweep after a grave.

- **Sweep after a grave.** A defined-but-never-returned error, an
  unreachable branch, a constant nothing reads — grep for siblings of the
  same shape in the same session. *An error constant with no reachable path
  is a missing check wearing a disguise* (grave, 0003); its inverse face is
  the predicate-property rule, and disguises come in families.

### Lessons are indexed by shape, not by file, so the sweep runs backwards too.

- **Lessons are indexed by shape, not by file, so the sweep runs backwards too.**
  `keywordgen` had already met and solved the wrapped-arm defect — its lexer arm head ends at
  `->` for exactly that reason — and `opgen` reintroduced it because the regexp shape was
  **re-derived instead of copied**: 411 rows where 436 were measured, silent. The graveyard's
  value is only collected when the next author searches it by *structure* rather than by
  filename, because a grave filed against one file reads as a fact about that file and is
  actually a fact about a shape. #78 → #80 → #105 is one structure in three packages that share
  nothing else. So before writing a reader, trigger, or regexp a sibling package already has,
  **read the sibling's version first** — a same-shaped problem next door is a place to read,
  not a place to invent — and title a grave by its shape, not its site. (Ruling: Scott, PR
  #108; grave #105.)

### Unreachability is a grave only when it's silent.

- **Unreachability is a grave only when it's silent.** Declared and tracked,
  it's a TODO with an audit trail — scaffolding wearing a name tag, not a
  missing check wearing a disguise. The test is whether the deferral was
  *named at its definition site* and carries a tracking issue. A sweep that
  turns up a labelled placeholder has still done its job: it forced the
  classification question. (Ruling on `ErrTrailingData`, #6.)
