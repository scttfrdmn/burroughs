<!-- Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0 -->

# Laws — Graves and sweeps

A bug becomes an oracle, and a lesson is indexed by shape.

Relocated from `CLAUDE.md`'s `## Disciplines` section, **verbatim**, when that file
became an index (see the restructure PR). Nothing was rewritten in the move: the bodies below
are the text as it stood, which is why superseded wordings still appear inside them where a
later ruling amended rather than replaced. The per-law recall keys `CLAUDE.md` carried were
retired with the index economy when that file became a brief and a pointer page (Scott's
directive, the four-workstream brief of 2026-08-17); the laws themselves were not touched.

`CLAUDE.md` links this family, and the two halves of that link are checked:
`TestClaudeMDLinksResolve` (`internal/testenv`) that every pointer on the page resolves, and
`TestLawFamiliesAreReachable` that every family here is reachable from it — a law nobody can
reach is a law out of context.

### Artifacts become oracles.

- **Artifacts become oracles.** Bugs found by hand become regression tests
  in the same session. Graves get marked: a comment at the fix site naming
  the lesson and citing the issue.

  **Specimen — a closing keyword closes the issue and never writes the lesson** (grave
  #314; demoted here on Scott's order, PR #364). The law above says a grave gets marked
  with a comment naming the lesson. The tracker-side half of that is where it gets lost:
  `Closes #N` or `Fixes #N` anywhere in a PR body or a commit message makes GitHub close
  the issue on merge, and it closes it *correctly* — the state transition is real, the
  timestamp is right, every query asking "is it closed?" gets the reassuring answer, and
  the closing comment carrying the lesson was never written. What is left is a tombstone
  with no inscription, which is worse than an open issue because an open issue still
  reads as work.

  **The mechanism is that the parser reads tokens, not negations**, so no sentence around
  the keyword can disarm it: "this does not close #N" closes #N. That is the whole reason
  the rule is a banned *construct* rather than a banned *intention*. The remedy is two
  commands in order — `gh issue comment` then `gh issue close` — so the lesson lands
  first and the state follows it, and three phrasings that carry the reference without
  the trigger: "Landed in #N", "Filed, deferred: #N", "see #N".

  **This law has no key in `CLAUDE.md` and is not getting one.** `scripts/closecheck.sh`
  scans the PR body and the commit messages, runs in CI's `citations` job, and its failure
  message states both the rule and the remedy — the criterion (#339) under which a law
  needs no index prose, because the control says the whole thing at the moment it matters.
  It has now caught this same author three times, most recently on the report *about* a PR
  whose own Graves section lectured on citations. A control that fires reliably at the
  moment of the mistake is exactly the kind whose index line is redundant, so the order to
  demote it frees no index bytes at all: there was nothing to take out, and this specimen
  is where the lesson lives instead of in a shell script and a closed issue. It is also
  the preventive face of *a completion state can be true while its payload vanished* —
  the keyword is how the payload goes missing, and reading the artifact's own comment
  count is how you find out.

  **Amendment — closing is a state transition on an issue, but a queue label is a claim
  about the world** (Scott's ruling, relayed on the #448 merge). #314 above is the *keyword*
  path; this is the **hand** path, and the harm is identical, because in this repo the label
  **is** the queue: `CLAUDE.md` defines the decisions-needed queue as open issues carrying
  `decision-needed:scott`, assigned to Scott. So `gh issue comment` then `gh issue close`
  discharges #314 in full and still leaves the second failure standing. An issue's state is a
  fact *about the issue*, and both paths get it right. A queue label asserts something
  *outside* the issue — that a person owes a decision — and that claim does not become false
  when the issue closes. It moves, or it evaporates unobserved, and `CLOSED` is the
  reassuring answer either way.

  Caught one command short on #441, which carried the label while none of the three
  successors the PR body listed under *Decisions needed from Scott* (#450, #451, #452) did.
  The remedy is a third command in the order: **before closing, list the labels**, and for
  each one that defines a queue or a work set, either transfer it — label *and* assignee — to
  whatever still carries the claim and verify the count landed where you expect, or say in
  the closing comment that the queue shrank and why. Measure the queue *after*, not before.
  **All the risk is in the transfer direction**, because the convention is `--state open`
  plus the label: ten closed issues carry `decision-needed:scott` today and none of them is a
  false entry, so a stale label on a closed issue costs nothing and a dropped one costs the
  whole ask.

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

### A repair to a defect whose file records a prior instance of the same shape isn't done until it sweeps.

- **A repair to a defect whose file records a prior instance of the same shape isn't done until it
  sweeps.** The stronger form of the rule above, and it names *when* the backwards sweep is owed:
  when the file you are editing already carries a header, a sibling entry, or a paragraph about the
  shape you just fixed. Then the lesson is not being learned, it is being **re-read and not applied** —
  and one fix at the site the checker pointed at leaves the same defect standing everywhere the file
  documents it. Scott issued it on grave #449, from three instances in one session: the stable key in
  #447, `fail=1` in #449 itself, and a `0040` token repaired in one section of a PR body and left
  standing in *Next*. *"In two of the three the lesson was already written down in the file's own
  header or its sibling."*
  It paid immediately, twice, in the PR that recorded it (#448): `publicpath_test.go`'s domain
  paragraph, whose own title is *"every figure this section ever carried has gone stale once"*, was
  carrying its third generation of stale figures with the falsifier 120 lines below it in the same
  file; and `foreclose_test.go`'s licence map turned out to hold three reasons that were false of the
  paragraphs they license — read only because an unrelated edit three packages away forced a re-key.
  The habit is cheap and the direction matters: *a FAIL names a site, not the population*, so after
  fixing the site, re-scan the channel the checker cannot see.

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
