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

  **Specimen — read a control's specification before writing the control** (finding 2
  of the PR #281 review, filed here on Scott's ruling): decision 0028 d3 asked for a
  positive control over *"a triple where the composite differs from the **unfused**
  f32 answer"*, and the draft compared the bare expression against the correctly-rounded
  oracle instead. On arm64 that is **0** — the compiler fuses the bare `x*y+z` into a
  genuine `FMADD` which lands on the correctly-rounded answer — so the substituted
  control was a stillborn zero, where d3's actual specification is 55 on both arches.
  d3's own next-but-one sentence warns about precisely that compiler fusion. So the
  record already held the answer, one paragraph past the line that was read, and the
  cost of not reading it was a control that would have passed while measuring nothing.
  This is the law pointed at **specifications** rather than at sibling packages: an
  accepted ADR that commissions a control is the nearest prior payment there is, and
  re-deriving the control from its title re-earns the grave the ADR was written to
  prevent. Both pairings are asserted now. (Ruling: Scott, on the PR #281 relay.)
