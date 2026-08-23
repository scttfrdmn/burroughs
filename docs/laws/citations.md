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
`TestMarkdownLinksResolve` (`internal/testenv`) that every pointer in every markdown file in the
tree resolves, and
`TestLawFamiliesAreReachable` that every family here is reachable from it — a law nobody can
reach is a law out of context.

### Fixtures cite the suite, and the citations are checked.

- **Fixtures cite the suite, and the citations are checked.** A hand-typed test
  vector carries a `<file>.wast:N` comment that `TestFixtureProvenance`
  verifies, or it is marked `synthetic` with a reason. A citation nobody
  verifies is a claim, not a citation — two vectors claiming to be "verbatim"
  had drifted, one truncated from 11 bytes to 8. Prefer deriving corpora from
  the suite at run time: no transcription step, no drift.

### A `file:N` resolves to a line, not to the thing it names — and the miss is systematic, not careless.

- **A `file:N` resolves to a line, not to the thing it names.** Every number in a
  `somefile.wast:N` is well-formed, so following one lands somewhere and the
  reader confirms whatever is there. Five published citations of the same five
  vectors — ADR 0037's table, the `passFloor` ledger, `CHANGELOG.md`, grave #408,
  PR #409's body, the merged commit message — were **two lines off**: they named
  the `"unknown import"` expectation line, and every instrument here keys on the
  command's *opening* line (`Command.Line`).

  **The bias has a mechanism, which is why "be careful" is not the remedy.**
  Writing the citation and confirming the expectation are the same act, and the
  expectation's line is the one under the eye — so the error is one-directional
  and roughly constant in size, exactly like a mis-calibrated instrument rather
  than like noise. In the same list, `linking3.wast:14` was right, because that
  command happens to open on the line it is read from; *one citation in five
  correct for a reason unrelated to care is the tell*, and an inconsistency
  inside a single list is worth more suspicion than a uniformly wrong one.

  What caught it was re-deriving the set from the mechanism rather than
  re-reading it: printing `Command.Line` for every `KindAssertUnlinkable` in the
  file. **Measure with the instrument, not with the eye** — the same rule that
  applies to a scoping figure applies to a line number, and for the same reason.

  Nothing swept it. `citecheck.sh` resolves issue and ADR tokens only, and the
  one control that does check a `<file>.wast:N` — `TestFixtureProvenance`, above
  — ranges over citations *sharing a line with a byte-slice literal*, which no
  prose citation is. Filed as
  [#412](https://github.com/scttfrdmn/burroughs/issues/412), whose own first
  draft asserted there was no such control at all: **a gap claimed without
  searching for its existing instrument is this family's own defect one level
  up**, and the correction is on the issue rather than folded silently into it.

### A stale citation is a cheap tell for an expired claim, so a repaired pointer gets its sentence read.

- **A stale citation is a cheap tell for an expired claim.** `internal/interp/value.go` said
  `table.go:134` *"already writes `ref{Null: true}` into every fresh table slot."* #419 had
  falsified **both halves** at the referent — `table.go` writes the *initializer's* reference,
  `slots[i] = v.ref`, not a null one — and the record of that change sits directly above the
  write, in the file the sentence was citing. The refutation was written at the target and the
  claim went on standing at the source. Grave
  [#491](https://github.com/scttfrdmn/burroughs/issues/491).

  **It surfaced from a line-shift sweep, not from anyone reviewing the claim.** `:134` was no
  longer the write, and repairing the *number* is what made a reader open the referent and read
  the sentence against it. So the rule is procedural rather than aspirational: **when a citation
  repair moves a pointer, read the sentence at the new target.** Repairing 48 pointers and
  reading none of them passes every gate in this repo — the numbers would all resolve — and the
  read is where the value is.

  **The load-bearing kind is the cross-package citation.** Within a file, a wrong claim about
  nearby code is likely to be noticed by whoever edits it; across a package boundary nobody
  edits both ends, so a claim and its refutation coexist indefinitely, as these did. And no
  instrument here can see it: `citecheck.sh` resolves issue and ADR tokens, the markdown
  controls resolve links, and nothing reads one file's prose against another file's *code*. A
  line sweep is not a claim-checker; it is a **router**, and the human at the far end is the
  oracle.

  The gap this leaves is stated rather than papered over: the tell only fires when the
  referent's line **moves**. A cross-file claim whose target stays put is invisible to
  everything in this tree.

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

### A claim that an obligation was paid cites the artifact that pays it, never a description of it.

- **A claim that an obligation was paid cites the artifact that pays it — a diff hunk, a
  commit, a file and line — never a description.** Scott's standing remedy, on the #499
  reconciliation, and the words are his:

  > That's the fourth self-retraction in four reports, and all four are one shape:
  > verification of the wrong object reported as verification of the right one — the edit
  > made after the amend, the file list written from intent, `grep | head` stopping before
  > the declaration, and now the neighbour restatement read as the README repair. From here:
  > **any claim that an obligation was paid must cite the artifact that pays it** — diff
  > hunk, commit, file and line — never a description. If the citation can't be produced,
  > the claim is "not verified," not "paid."

  **The specimen is a discharge claim about a discharge law.** #464's closing comment told
  Scott that finding 6 was paid — that README's *"Every row now in this column is harness
  debt"*, a universal quantifying over an empty column, was *"gone from the file"* — and said
  in the same sentence that this was **"checked before this transition rather than assumed,
  because a rider is exactly what a closing issue drops silently."** Every clause was false.
  The sentence was intact and present-tense; `git log --since` over README was empty;
  `git log -S` on the phrase returned exactly one commit, the one that added it. So the
  paragraph explaining why riders get dropped silently dropped one, while asserting it had
  checked — which is worse than the unpaid obligation, because it converts an open item into a
  discharged one **on the record**. Retracted by posting
  ([#464](https://github.com/scttfrdmn/burroughs/issues/464#issuecomment-5383999052)).

  **The mechanism is the whole lesson: the wrong object was a plausible neighbour of the right
  one.** The class *is* restated beside `unsupportedCeiling` in `internal/spec/spec_test.go`,
  which the same closing comment cited in its next clause. Reading that and concluding README
  had been repaired is *a check of the wrong file reported as a check of the right one* — and
  the four instances Scott names differ only in which neighbour stood in: the version before
  an amend, the intent behind a file list, the tenth line of a truncated match, the
  restatement next door. Recollection dressed as verification passes review every time,
  because the sentence describing the check is as fluent as the check would have been.

  **Why a citation is the remedy and care is not.** A citation names an object, so producing
  one forces the object to be opened; a description names a belief about an object, and can be
  produced without touching it. That is the asymmetry the rule runs on, and it is mechanical:
  **if the citation cannot be produced, the claim is "not verified", not "paid"** — the third
  outcome, not the flattering one. For a code location the form is
  [ADR 0047](../decisions/0047-a-location-citation-is-path-qualified-and-names-a-symbol-and-the-positional-population-is-pinned-rather-than-banned.md)'s:
  path-qualified and naming a symbol. Two of Scott's four instances are already laws of their
  own — *[a claim about your own diff is sourced from the
  diff](evidence-and-instruments.md#a-claim-about-your-own-diff-is-sourced-from-the-diff--an-edit-made-after-an-amend-is-not-in-the-amend)*
  is instance one, and *[a truncated search proves nothing about
  absence](evidence-and-instruments.md#a-truncated-search-proves-nothing-about-absence-because-a-display-limit-is-not-a-result-set)*
  is instance three — and each was minted as a fact about *its own* wrong object: the amend,
  the pipe. Four objects later the shape is the constant and the object is the variable, which
  is why this one is stated over the claim rather than over the tool. What it adds is the
  population: **discharge claims**,
  every one of which is addressed to a principal, and none of which any sweep in this tree can
  check — there is no artifact named to resolve. (Mint: Scott, on the #499 reconciliation.)

### The word "grave" is a citation to a label, so the label lands before the body that cites it.

- **The word "grave" is a citation to a label, so the label lands before the body that
  cites it.** `citecheck.sh` resolves `grave #N` against `label:type:grave`, and it runs on
  the `pull_request` event — the moment the body is *opened*, not the moment it is merged.
  The closing ritual runs the other way round: comment, then close, then the tombstone is
  complete. So an agent following the ritual and reaching for the label at close time has
  written a body whose claim is false for the whole life of the PR, and the gate says so.
  Specimen: #395's PR opened citing `grave #395` while #395 still carried only `phase:v0`
  and `gate:eh`; the `citations` job failed on exactly that line while every other job was
  still running. **Two orderings, and only one of them is negotiable** — the lesson comment
  must precede the close or the close eats it, but nothing stops the *label* from being
  applied when the defect is diagnosed. So it is: a grave is labeled when it is known to be
  one, and the closing comment is what waits. The general form is that a body's citations are
  checked against the world as it stands when the body is written, and *a plan to make a
  citation true later is a citation that is false now*.
