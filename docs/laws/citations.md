<!-- Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0 -->

# Laws — Citations

A citation nobody resolves is a claim; provenance has categories.

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

### Fixtures cite the suite, and the citations are checked.

- **Fixtures cite the suite, and the citations are checked.** A hand-typed test
  vector carries a `<file>.wast:N` comment that `TestFixtureProvenance`
  verifies, or it is marked `synthetic` with a reason. A citation nobody
  verifies is a claim, not a citation — two vectors claiming to be "verbatim"
  had drifted, one truncated from 11 bytes to 8. Prefer deriving corpora from
  the suite at run time: no transcription step, no drift.

### A doc comment's identifier is a citation, and it gets a resolving check.

- **A doc comment's identifier is a citation, and it gets a resolving check.** The
  class was left as convention on the stated criterion *convention until first
  drift* — and the drift arrived, measured: **`constWalk` was cited in three comments
  across three PRs and has never existed in this package.** Not renamed, not moved;
  fiction from the first keystroke, in prose describing where a gate is read. So the
  criterion now cuts the other way and the fixture-provenance treatment extends to
  prose-in-code: an identifier named in a comment resolves to a definition, or the
  comment is phrased historically. This is `TestEveryCitedTestNameResolves` (#93/#94)
  widened from test names to identifiers generally — same trigger discipline, and its
  three paid-for lessons carry over intact: rejoin hyphenated line wraps, scope the
  historical exemption **per sentence** rather than per block, and exclude
  declaration-shape spans rather than backticked ones. The unchecked sibling remains
  the **issue-number** class from #84 (`#NN` in prose asserting *what a defect is* —
  a diagnosis, and `annotations.wast:1` sat misattributed to #55 for three PRs while
  a ceiling's comment called it "not ours"); different oracle, still open. *Split
  issues at the oracle seam* — this half's oracle is local, so this half lands first.
  (Ruling: Scott, PR #113; graves #114, #115, #116.)
  - **Cite issues at fix sites, never PRs — the mitigation is a better target, not a better
    resolver.** An issue number is a *durable home*: a grave stays a grave, and #8 still names
    the encoder frontier whatever PR moves it. A PR number is a **supersedable container**, and
    the demonstration took one afternoon: a citation to `#127` was wrong *twice in opposite
    directions* — first fiction, typed before any such PR existed; then right by luck, GitHub
    sharing one sequence between issues and PRs; then wrong again in a new and worse way when
    #126's merge deleted the base branch, GitHub auto-closed #127 and refused to reopen it, and
    the work landed as #128. **A citation that resolves perfectly while pointing at the closed,
    superseded half of a pair is invisible to any resolver that only asks existence**, which is
    the drifted-fixture defect with a live target for camouflage. Both errors were caught by a
    human noticing, and *noticing is not a mechanism*. So PRs are cited only as **history**,
    where being superseded is the point — the cascade narrative at `mllex.go` is the licensed
    shape. This shrinks #116's oracle-less half to nearly nothing while building nothing, which
    is why it is a rule and not an issue. (Ruling: Scott, PR #128.)
  - **A range citation's description is written by reading the cited lines, never from what the code
    around them appears to be doing.** Mandatory rather than customary, and the promotion is an
    **error rate** rather than an anecdote: repairing #333 required a description for each newly
    covered range, six were written from inference — inside the repair of a citation defect — and
    **five of the six were wrong**, the sixth right only because it was copied from a description
    someone had written from the reference. Every one of the five *resolved*: well-formed range, inside
    the file, subject's message site contained where keyable. Nothing could see them, because a check
    that asks where a range points cannot ask what the prose beside it claims the range **contains**,
    and the two questions come apart exactly when the author has read the code and not the citation.
    The rule carries a tripwire, a five-in-six rate being the argument for not leaving it a procedure:
    `TestRangeCitationSubjectsAreReadFromTheReference`
    (`internal/validate/citation_subject_test.go`) takes every comment line that cites a range and
    names a reference-defined identifier, and requires one of the two honest relationships — the
    identifier is *inside* the range, or the range is inside the identifier's own definition. Both
    halves of the trigger are derived (globbed files, candidates parsed out of the reference's own
    bindings and arms), which is #333's lesson applied at construction rather than after the grave;
    descriptions naming no reference subject are counted as residue and pinned, so a row leaving the
    checked column is loud. **Naming the subject is therefore the operative form of the rule** — a
    description that names one is a description somebody had to read the reference to write.
    (Ruling: Scott, PR #335 relay.)

### Three provenance categories: cited, derived, synthetic.

- **Three provenance categories: cited, derived, synthetic.** *Entailment from
  checked facts is legitimate provenance; unstated entailment is just synthetic
  with better manners.* A **derived** vector is one the suite implies but does not
  contain — `TestLEBWidthIsPerField`'s accept half asserts a wide-but-legal limits
  minimum is fine, which `binary-leb128.wast` cannot say because it only asserts
  malformedness, and which `:525`/`:217` jointly *bracket* by wanting different
  errors at ten and eleven bytes. What keeps the category from becoming a laundering
  channel is that a derived row **states its premises and its inference**, and
  `TestFixtureProvenance` machine-checks that the premises **resolve** — the
  inference is reviewed by eyes, but a premise citing a line that says something else
  is caught by the same mechanism that catches a drifted transcription. Same shape as
  every other rule here: the human judgement is allowed, the checkable part is
  checked, and silence is not an option. (Ruling: Scott, PR #37.)

### A ruling retroactively falsifies prose written before it, so accepting a ruling includes sweeping for the sentences it orphaned.

- **A ruling retroactively falsifies prose written before it, so accepting a ruling
  includes sweeping for the sentences it orphaned.** *Truth has a maintenance cost.*
  `ci.yml` said the runner stalls were "tracked in #28's thread"; the no-issue
  ruling made that false the moment it was given, and the sentence would have sat
  there citing a tracking location that does not exist — the drifted-fixture-citation
  defect wearing different clothes, since a citation nobody re-checks is a claim.
  So a ruling is not applied when the decision is recorded; it is applied when
  everything the decision contradicts has been found. Grep for the old answer, not
  just for the place you expect it. (Ruling: Scott, #28.)
