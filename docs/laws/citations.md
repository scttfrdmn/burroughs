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
  a ceiling's comment called it "not ours"); different oracle, still open. [*Split
  issues at the oracle
  seam*](evidence-and-instruments.md#an-issue-whose-oracle-does-not-exist-yet-stays-open-on-its-own-account)
  — this half's oracle is local, so this half lands first.
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
  is why this one is stated over the claim rather than over the tool.

  **What it adds is the population — and the population is partly instrumented, which the first
  draft of this entry denied.** That draft said no sweep in this tree could check a discharge
  claim, since there is no artifact named to resolve. `citecheck.sh`'s check 7 resolves a
  discharge claim in a **PR body** against that PR's own changed-file list, and it fired on the
  draft in the sharpest way available: the sentence asserting that nothing here checks such a
  claim was itself resolved `ok` by the check, against this file. A false negative claim about
  the instruments, inside a law about verifying the wrong object, on its first run — and it erred
  in the flattering direction, which is the direction to look in. Two limits survive, and they
  are what the rule carries. Check 7 is `--pr` only, so the channel all four instances actually
  used — a tracker comment, a report to a principal — is outside every sweep's domain. And what
  it verifies is that the **file** is in the diff, which is weaker than the obligation being
  paid: a PR can change `README.md` and still not repair the sentence in it. So a citation is
  what makes the claim checkable by a machine where one reaches and by a reader where none does,
  and a description is checkable by neither.

  **The live specimen for the uncovered channel, and it is a small population with the break in
  the worst place.** #136's body carried `[0002]: …/docs/decisions/0002-internal-form.md` from the
  moment it was filed, 2026-08-05. That file has never existed — the ADR is
  `0002-interpreter-strategy.md` — and the link is the one every reader follows to check whether
  0002 says what the issue claims it says, since the issue's whole case is 0002's letter. Eighteen
  days, in an **issue** body: check 7 is `--pr` only, and `internal/testenv`'s three
  markdown-link controls have every file in the tree in their domain and no tracker body in
  anyone's. Measured across 500 issue and PR bodies, both citation channels resolved against
  `git ls-files`: 657 unique citations, of which 599 are exact tracked paths, 36 resolve by unique
  suffix, 19 name an external or fetched tree, and **1 dangles** — that one. *The channel nothing
  watches is nearly clean, and the single break in it is the load-bearing citation of an open
  issue*, which is the argument for the reader-side half of this rule rather than against it.
  **Two wrong counts came first, and the middle one flattered**: one channel only gave 1-of-13,
  understating the population by 644; counting every unresolved path as dangling gave 51, which
  makes the specimen look systemic. Only the decomposition by mechanism gives 1 of 657. (Mint:
  Scott, on the #499 reconciliation; specimen ordered recorded on the same review. The draft's own
  falsification, above, is recorded rather than repaired silently, on the same rule.)

### An asserted deferral is a citation with no target, and it reads as tracked.

- **An asserted deferral is a citation with no target.** *"Worth pursuing", "left for later", "the
  obvious candidate, not benchmarked here" — each names future work and names no place it lives, so
  the sentence carries a citation's force with nothing on the other end.* This is the family's
  standing shape read in the direction where no pointer was ever written. The rest of this file is
  about pointers that stop resolving; an asserted deferral is the case where **the pointer is absent
  rather than broken**, which is strictly worse for a sweep,
  because a dangling `#N` is a token an instrument can resolve and fail on, and *"worth pursuing"*
  is prose. Nothing in `citecheck.sh` can fire on it, and nothing should be built that tries: the
  words are unbounded. What is checkable is the other half — **once a subject is filed, the sentence
  cites it**, and then every sweep in this file reaches it.

  **The specimen is ADR 0023's narrowing question.** Finding 2 reads *"so a u8-or-narrower design is
  worth pursuing if its wraparound can be made sound (a per-call-frame generation reset is the
  obvious candidate, not benchmarked here)"*, in
  [ADR 0023](../decisions/0023-drop-tags-each-stack-slot-with-a-lazily-activated-push-sequence-number.md)'s
  finding 2 — quoted rather than line-cited, since the quotation survives an insertion above it.
  Two deferrals in one parenthesis, a named candidate mechanism, and no issue anywhere. It sat from
  the ADR's acceptance until the #612 audit read the decisions directory for exactly this, and it
  had been read past repeatedly in between: an ADR is a tombstone, so a reader arriving at a
  Consequences-tier sentence about future work assumes the record is complete, which is what
  "tombstone" means. The tell is that **its own next finding argues against it** — finding 4 says
  that once gated, width stops being the clear lever finding 2 made it — so the deferral was not
  merely unfiled but had accumulated evidence its filing would have to answer.

  **The repair is the form, and it is a title rather than a paragraph.**
  [#617](https://github.com/scttfrdmn/burroughs/issues/617) is *"Gated u8 sequence numbers: ADR
  0023's narrowing question, deferred and never filed"* — the defect named in the title, so the
  issue's own subject line records that the deferral was asserted before it was tracked, and the
  ADR's sentence now has something to point at. **What the audit could not do is make the ADR's
  prose true retroactively**: the sentence was untracked for the whole interval, and a reader in
  that interval had no way to know, since an assertion of future work is indistinguishable from a
  tracked one until you go looking for the number that is not there.

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

### A `Status:` written in the PR that files its blocker is written before the blocker has a body.

- **A `Status:` written in the PR that files its blocker is written before the blocker has a body.**
  `blocked — #631` went into `docs/litmus-battery-preregistration.md` in the same PR that opened #631, and
  at the moment those characters were typed #631 was a title and nothing else. The citation resolved — the
  issue existed, so every sweep here was satisfied — and it was still the strongest possible form of an
  unchecked claim: **the blocker's body is written after the sentence that rests on it**, so the sentence is
  a forecast of what the issue will say, dressed as a reference to what it does say. It came out wrong in the
  ordinary way a forecast does. #631 was expected to be about a gate, so the row said the gate was the
  blocker; what #631 turned out to be was a `Features` literal in a test helper, and the row had to be
  rewritten. (Grave [#630](https://github.com/scttfrdmn/burroughs/issues/630).)

  **The repair is an ordering, not more care.** File the blocker, write its body, *then* write the field
  that cites it — the same shape as the grave label above, and the same reason: a claim is checked against
  the world as it stands when the claim is written, and an issue with no body is not yet a world to check
  against. Where the ordering genuinely cannot be had, the field says what is actually known — a file and a
  line, or *"blocked, cause not yet stated"* — because **a `blocked — #N` reads as though someone has
  already looked**, and that is the value it borrows from the number.

### A control catching its own author at the moment of authorship is the strongest evidence it aims right.

- **A control catching its own author at the moment of authorship is the strongest evidence it aims
  right.** Recorded because the opposite result is the one that looks like success: an instrument that
  only ever fires on someone else's work is indistinguishable from an instrument tuned to its author's
  habits, and it will keep reporting green while the author writes around it without noticing. Three
  firings in this family, two of them in consecutive turns:
  - **The law falsified its own draft.** The paragraph in this file establishing the resolving form was
    written with a citation in the very form it was banning.
  - **The census pin fired on the changelog entry about re-pointing markdown line citations** — the
    entry had added three positional citations in order to describe the three it removed.
  - **The census pin fired again on ADR 0024's amendment**, whose subject is four drifted citations and
    which therefore has to write all four down.
  The third is the instructive one, because it was the first firing that was **not** a mistake: those
  citations belonged there. An exact pin refusing a change its author believed was exempt is how the
  *positional by construction* class was found on the markdown side at all — the alternative path,
  where the author quietly decides their own case is different, produces an exemption instead of a
  measurement, and *an exemption inherits none of the trigger's lessons*. So the habit is to treat a
  self-firing as a result to publish rather than an inconvenience to route around, and to say which of
  the two kinds it was. (Ruling: Scott, on the #502 review — *"that's the strongest evidence available
  that they're aimed correctly, and it's worth a sentence in their own documentation."*)

### When an issue splits, every message naming it is re-derived, not re-numbered.

- **When an issue splits, every message naming it is re-derived, not re-numbered.** Scott, on the
  #637 review: *"When an issue splits, every control message naming it must be re-derived, not
  re-numbered. The #573 clause stayed pointed at the half that kept the number, so nothing in the
  tree names the header-publication class at all. That's a live message one class short of its
  subject — a different failure from a stale name, and harder to see because everything resolves."*
  The last clause is why this is a law rather than a reminder: **no instrument here can see it.**
  `citecheck.sh` asks whether `#573` names a live issue and it does; the sentence wrapped around the
  number is the part that went wrong, and that is *[a `file:N` resolves to a line, not to the thing it
  names](#a-filen-resolves-to-a-line-not-to-the-thing-it-names--and-the-miss-is-systematic-not-careless)*
  with an issue number in place of the line.

  **Specimen.** Three sites carried the clause *"`Spawn` shares the instance's globals, so
  `global.set`'s plain writes are data races (#573)"* — `internal/interp/thread.go`'s parking notice
  and both halves of `TestNoEngineGoroutineLandsWithoutAPrincipalsRuling`, its doc comment and its
  failure message. Two events then moved underneath that sentence without touching it. #573's
  slice-header class split out to [#622](https://github.com/scttfrdmn/burroughs/issues/622), and
  [ADR 0063](../decisions/0063-a-numeric-globals-single-word-goes-atomic-and-a-v128s-pair-goes-under-the-globals-own-mutex.md)
  synchronised two of the three arms that stayed — so the clause was simultaneously **too broad**
  (only the reference slot is still a plain write) and **silent** about the class that had left.
  Every pointer in it resolved throughout.

  **The repair is a narrowing, and the absent addition is the instructive half.** The sites now say a
  *reference* global's `global.set` is still a plain write; no clause was added for #622, because
  #622's defect was fixed in the slice that found this — a precondition that is discharged does not
  need naming, and adding it would have been a message written to be immediately false in the other
  direction.

  **The practice: at split time, grep the parent number.** The quantifier is unreadable by any sweep,
  but a *split* is an enumerable event, so the population to re-read is derivable — every occurrence
  of the parent number, read as a sentence rather than as a pointer. Running it that way is what
  found the label class in the entry below. Scott, same review: *"Running the addition as a sweep
  rather than filing it as a lesson is what turned one known instance into five — and found a class
  nobody was looking for."* (Both quotations are from an in-session review; PR #637 carries no
  comment to cite, and this is recorded by the actor the ruling was given to, so it is durable and
  not independent — commits in the slice stay `Ratio-Class: carried`.)

### The tree may cite an issue; it may not claim that issue's labels.

- **The tree may cite an issue; it may not claim that issue's labels.** Scott's rule, given on the
  #622 slice: *"don't assert tracker state in the tree. A sentence about a label is a predicate about
  a world no control here can see — everything in `internal/testenv` reads the tree, and a label
  lives in the tracker. It rots on a mutation nothing observes. The tree may cite an issue; it may
  not claim that issue's labels. Same shape as the quantifier problem, one notch further out."*

  **Specimen: five sites claiming `decision-needed:scott`** — `internal/interp/global.go`,
  `internal/interp/globalbench/bench_test.go`, two sentences in ADR 0063, and one in ADR 0042. **All
  five were false when they were read**: #573 does not carry the label (all three of its arms are
  ruled), and #452, which ADR 0042 says is waiting on a decision, is **closed** carrying `phase:v0`
  and `gate:gc`. Nothing failed, in exactly the way *[an asserted
  deferral](#an-asserted-deferral-is-a-citation-with-no-target-and-it-reads-as-tracked)* does not fail —
  and the sentence reads to the next agent as though someone had looked.

  **The mechanism is not that a label is unreachable, and the first draft of this entry got that
  wrong.** Every control in `internal/testenv` reads the tree, so those five sites are out of their
  domain for Scott's reason. But `citecheck.sh` **already fetches the labels** — one request per
  citation returning kind, labels, state and title — and prints them on every `ok` line. State is
  compared (a sentence saying an issue is open is checked against the tracker) and *one* label claim is
  compared: `grave #N` must resolve to an issue carrying `type:grave`. So the gap is a **missing
  comparison over data already in hand**, which is a much smaller thing than an unreachable fact, and
  worth saying because *a predicate over already-fetched data is not a new instrument* — a later slice
  can close it cheaply, and the reason this one did not is that the principal chose the ban over the
  checker. **The ban is what makes the class empty rather than checked**, and `grave #N` is the
  standing exception that proves the shape: a label claim the tree is allowed to make is one a gate
  compares.

  **What to write instead is what the tree can hold**: the issue's number and what it is *for*, with
  the state named only where the code itself is the evidence — `#573, open and unimplemented` is
  carried by the missing mechanism beside the sentence; `still decision-needed:scott` is carried by
  nothing. The repair form follows the `Status:` rule: a `proposed` ADR is edited, an **accepted** one
  takes a postscript, because amending a stamped record makes the stamp cite a sentence its signer
  never read. Four of the five were repaired in the slice that found them; ADR 0042's was **filed**
  instead, because nothing blocked that slice on it and *a repair the verdict did not compel is its
  own work* — an accepted ADR's postscript is a slice, not a rider.

  **The attribution needed a fact from outside the tree, and the capability split supplied it.** The
  `scttfrdmn` account performs both principals' tracker mutations, and the API's fields are identical
  for both, so a timeline cannot say who removed a label. Scott closed that: *"I can supply the fact
  you can't. The account is shared but the capability isn't. I act only in this session and never
  touch the tracker. So every tracker mutation under that account is mine"* — the agent's, that is;
  the removal was the agent's and was correct. Recorded because it generalises: **where the artifact
  cannot attribute, a principal's statement about capability can**, and it is worth asking for before
  a provenance question is written down as unanswerable. (In-session ruling on the #622 slice,
  recorded by the actor it was given to; `Ratio-Class: carried`.)
