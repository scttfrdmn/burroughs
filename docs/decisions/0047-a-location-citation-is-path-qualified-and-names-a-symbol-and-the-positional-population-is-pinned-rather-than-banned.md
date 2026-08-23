# 0047 — A location citation is path-qualified and names a symbol, and the positional population is pinned rather than banned

Date: 2026-08-22 · Status: **accepted** — option chosen by Scott on the #486 review, relayed verbatim
to [a durable comment on #456](https://github.com/scttfrdmn/burroughs/issues/456#issuecomment-5381768767),
with one term on the implementation: *"exclude the 39 map keys by a stated rule, not by hand."*

Filed against **#456**. Its conversion half is **#497**.

## Context

A citation naming a file and a line number in the **pinned reference** is a permanent address. The pin
does not move, so the number is durable by construction and the form is exactly right. That is where
the habit was learned. Then it was applied to files this repo edits, where the same syntax is a
snapshot of a moment with nothing behind it — *a premise that holds for one mechanism is not a premise
about the other*, and this is that lesson arriving in a citation convention rather than in a gate.

### Two defects, and the second one needs no drift to be a defect

- **Drift.** The number was right and an insertion above it made it wrong. #440 is the proximate
  witness: 29 lines added to one doc block silently invalidated a citation that had been exact. There
  is no diagnostic — the citation still points *somewhere*, the somewhere is usually a comment, and the
  reader lands on plausible English with no signal that they are in the wrong place.
- **Ambiguity.** A citation whose file part is a bare basename names **no file at all** when two
  tracked files share that basename. The instrument found 57 of these, and the issue had not named the
  defect. The worst cases are the ones where both candidates are plausible: a matcher basename cited 22
  times with two matching implementations in the tree, a module basename 11 times against two files, an
  instruction basename 10 times against three. A reader following one of these has to guess, today,
  before anything edits anything.

The second defect is the one that decides the form. A drift-detector could in principle address the
first; nothing addresses the second except naming the file.

### The population, and the fact that it grew while the issue sat open

`go test ./internal/testenv/ -run TestPositionalCitationCensusIsPinned -v` prints it. Measured at this
slice: **303 positional citations**, on two margins that sum to the same total — one saying who reads
each citation, one saying whether it names a file. 181 are markdown, and the great majority of those
are `CHANGELOG.md` entries and accepted ADRs; 92 are Go comments; 23 are data keys; 7 are prose inside
string literals. By resolution: 90 path-qualified, 156 bare-but-unique, 57 ambiguous.

**91 in the issue's title, 263 at `6afbd9c`, 303 here.** The population is a function of how much prose
the project has written, because *adding lines breaks every line citation below* and every landing
slice adds prose. That rate is why Scott ordered this next rather than after a product slice: a defect
whose population grows with ordinary work gets more expensive every slice it waits.

**303 and not the 301 reported to Scott, and the difference is the instrument rather than the tree.**
That figure came from a shell one-liner whose file-part pattern rejected a digit, so a citer with a
digit in its name was invisible to it. *Measure with the instrument, not a regex* — the control is now
the instrument, and 303 is the figure to carry forward.

### Two of this issue's own arguments were wrong, and the corrections are here rather than only in the tracker

- **The dangling-symbol witness does not exist in this tree.** The relay comment on #456 called a
  symbol *"defined nowhere in the tree"* and called it the sharpest evidence the issue had. It is
  declared, in `internal/binary/instr.go`, and the search that produced the claim ended in a pager that
  returned ten matches with the declaration eleventh. A negative claim is the one kind of claim no
  sweep here can check, because there is no target to resolve; *count with a counter, not by eye*. The
  [correction was posted rather than
  edited](https://github.com/scttfrdmn/burroughs/issues/456#issuecomment-5383301564). The case for
  symbols therefore rests on the two *measured* defects above and not on a rename that broke prose.
- **The ruling's "39 map keys" is 23, and 20 of the 39 are prose.** The file holds 43 positional
  citations: 23 composite-literal keys, 3 inside a reason string, 17 in comments. A rule stated as
  "the map keys in that file" would have swept 20 pieces of live prose in with the data. This is why
  the term was *a stated rule* and not a list, and it is also *an issue's list is a registry, not an
  inventory* applied to a principal's estimate: the number was a reading, and the rule's job is to
  derive the population rather than to reproduce the reading.

## Decision

**A citation to a location in a file this repository edits is written `path/to/file.go:SymbolName`.**

The form has two halves and each answers one of the two defects: the **path** resolves the ambiguity,
the **symbol** survives an insertion. A rename or deletion then breaks the citation *loudly* — the
target file declares no such name and a control says so — where a line number resolves silently onto
whatever now occupies the line.

**The spelling is not invented here.** `internal/testenv/inventory_test.go` already keys its skip
licences on this form, and those keys are machine-consumed, so the tree's own precedent supplies both
the syntax and the proof that it is checkable.

Four riders, each of which is a decision rather than a detail:

1. **The pinned reference keeps line numbers.** Its files do not move, and #456's exclusion of
   `.ml`/`.mly` citations carries forward unchanged. The rule is about files this repo edits.
2. **A positional citation in a composite-literal key is data, not prose** — Scott's stated rule,
   stated as the AST's own: the key of a `*ast.KeyValueExpr` inside a `*ast.CompositeLit`, following
   concatenation. `foreclose_test.go`'s allow map is keyed on the line its scan reports, because the
   scan reports lines, and there is no symbol there to name. Derived from the parse and not from a
   filename, so a second control that keys on lines inherits the rule without being added to a list.
3. **The positional population is pinned, not banned.** An exact census on both margins, so a new
   positional citation cannot land without someone editing a number in the control and reading why. A
   ban would have required the conversion in the same PR, and the conversion contains a question that
   is not this document's to answer (below).
4. **A citation with no file part at all is counted, because it cannot be resolved.** Scott's ruling
   folded this population in and said a symbol-based resolver has to decide what to do with them. The
   decision is: count them, bucketed by what they continue, and convert under #497.

### The alternatives, and why they lose

- **Ban line numbers and convert in one PR.** The clean version, and it fails on a question it has no
  standing to answer: 181 of the 303 are in dated records, where re-pointing a number edits the record
  rather than repairing a citation. A ban forces that decision as a side effect of a mechanism PR,
  which is the shape *decision-before-code* exists to prevent. Held for Scott as #497.
- **A drift detector: re-resolve each line citation by remembering what was on the line.** Addresses
  drift and nothing else — the 57 ambiguous citations stay ambiguous, and the detector needs a stored
  snapshot of every cited line, which is a second copy of the tree's prose maintained by hand. It also
  reports drift *after* it happens, where the symbol form makes drift impossible to express.
- **Require path qualification only, keep the line numbers.** Cheap, fixes the measured-worse defect,
  and leaves the growth curve exactly where it is: every future insertion still silently invalidates
  every citation below it, and the tree acquires 200 more of them per phase. It buys the half that has
  no upkeep and declines the half that does.
- **Do nothing; rely on review.** The population went 91 → 303 under review. That is the measurement.

### The question this document does not answer

**Is a line number inside a dated record an address to repair, or a historical fact to leave alone?**
An accepted ADR is a tombstone; a changelog entry says what shipped on a date. The **form** of an
in-record repair is already ruled — #485's ruling says dated amendment notes, not silent repair — but
whether the repair should happen at all in a document whose job is to record a past state is a decision
about the project's own records. It is #497's, and Scott's. The census counts those 181 and judges
none of them.

## The instrument, and the four honest bounds on it

`internal/testenv/citeform_test.go`. Three assertions, and each of the bounds below is stated in the
control itself rather than left for a reader to find.

- **`TestSymbolCitationsResolveToADeclaration`** — the binding half. Every path-qualified citation whose
  location is a symbol names a symbol the cited file declares, checked against the file's own parse.
  **Its domain is small today** (a floor of 4, the precedent keys) because the conversion is #497, and
  it is a floor rather than an equality precisely so this control is not in the way of the thing it
  exists to encourage. The floor is a vacuity guard: the assertion is universally quantified, so a
  domain narrowed to nothing satisfies it while asserting nothing.
- **`TestPositionalCitationCensusIsPinned`** — the ratchet. Exact on both margins, because *a floor
  bounds the catastrophic case only* and here the interesting motion is +1. It prints the per-file
  breakdown and every ambiguous basename with the files it could mean, because the print is #497's work
  plan and a control that only asserts a number tells the next reader nothing about which of 303 to
  convert first.
- **`TestBareContinuationCitationsAreBounded`** — the population the ruling folded in. A bare
  colon-and-number in a code span, continuing a citation that named a file some lines earlier: no file
  part, so nothing in the tree could see it. Bucketed by the extension of its nearest antecedent,
  because which kind of citation it is depends on what it continues — a spec-suite or reference
  antecedent is durable by construction and the majority of the population; a Go antecedent is #456's
  defect with the file part deleted.

Four bounds:

1. **It cannot see aboutness.** A citation that resolves perfectly can still sit under a sentence that
   describes something else. *A valid citation does not certify its sentence*, and the symbol form
   narrows the gap — a symbol name is a claim about *what* the target is, where a line number is a claim
   only about where — without closing it.
2. **The continuation attribution is a heuristic, and its number is an interval rather than a count.**
   The antecedent is the nearest filename-bearing citation within twelve lines; a citation whose file was
   named further up is attributed to nothing. `(unattributed)` is a pinned bucket for that reason, so the
   in-scope population is bounded on both sides — the Go bucket is its floor, Go-plus-unattributed its
   ceiling — and neither side can grow inside the bucket that admits it has not looked.
3. **Its own footprint in its own sample is disclosed, and it carries no exemption.** The control's file
   is scanned like every other, and two of its illustrations are written in a live citation shape: a
   metasyntactic example and, in a failure message, the mandated form itself. Neither is positional, so
   neither touches the pin, and what keeps them out of the binding assertion is the **resolution** axis
   rather than a licence — one names a file by basename, the other names no file. That is also the file's
   own tripwire: write either at a real path and the assertion demands a declaration for a placeholder
   and fails, and the repair then is to de-qualify the example rather than to exempt the file.
4. **An identifier-shaped placeholder is classified as a symbol, not as metasyntax, and this is stated
   because the obvious reading of the three kinds is wrong.** A single `N` standing for "some line"
   matches the identifier pattern and nothing lexical separates it from a real name; a list of
   placeholder spellings is exactly the exemption surface the ruling excluded. What separates a
   placeholder from an address is that metasyntax stands for an *arbitrary* file, so its file part is a
   placeholder too — there is no such thing as metasyntax at a real address.

**Nine mutations, each applied by an asserted single substitution and each restored by digest.** The
renamed symbol, one new positional citation, the data-key rule neutered, the string-literal channel
left unread, the symbolic pattern narrowed to nothing, the walk leaving the repo boundary, one new
continuation citation, the attribution window collapsed, and the continuation pattern narrowed away —
nine reds, each attributable to its own row. Two of them are why the design is what it is: neutering
the data-key rule leaves the **total unchanged at 303** while moving 23 citations from one bucket to
another, which is a defect a single-number pin could not have seen; and collapsing the attribution
window fires the pinned buckets *and* the reference floor together, which is what a floor beside a pin
is for.

The bill's own first draft restored with a command that cannot restore an untracked file, so four rows
silently reported the first row's failure — *a repair is confirmed by the authority*, and a restore is
confirmed by the digest coming back rather than by the fact that a restore ran.

**The walk boundary moved from six sites to seven, and the new site is the first where overrunning it
produces a false pass.** The vocabulary walk's domain is *every* file regardless of extension, because
a citation is written in Go and markdown and *names* anything. So a walk that descended into a nested
copy of the repo would resolve a citation against a file that exists only in that copy and call it
good — where every previous site would have reported a false violation. The boundary was already
load-bearing; this is the first site where its failure is silent.

## Consequences

- **The form is checked, so it is worth writing.** A path-qualified symbol citation now fails loudly
  when its target moves. That is the property the whole decision is for, and before this it was a
  convention with no oracle.
- **The 57 ambiguous citations are named, printed with their candidate files, and are #497's first
  sub-population.** They were broken before this slice and nothing in the tree could say so.
- **A tenth channel joins the citation apparatus, and the reason it was missing is the same one #442
  found.** Every existing control keys on a shape this one does not have — bracketed markdown
  destinations, backticked test names, issue numbers, contract clauses. A `file:line` token was in no
  control's domain, not by exemption but by never having been enumerated. 303 plus 651 continuations is
  what an unchecked citation channel accumulates in one project's lifetime.
- **The positional population can now only go down**, and every decrease passes through an edit to a
  pinned number, which is the moment somebody reads the list.
- **The pin will be edited by unrelated PRs, and that cost is accepted deliberately.** Any PR adding a
  positional citation goes red. That is the mechanism, not a side effect: the red arrives on the author
  of the +1 with the mandated form in the failure message, rather than on whoever eventually takes
  #497.
- **The law this slice mints is about the specification of a control, not about this citation form**:
  [a control specified by its mechanism rather than by the defect it must catch will catch a different
  population](../laws/controls.md#a-control-specified-by-its-mechanism-rather-than-by-the-defect-it-must-catch-will-catch-a-different-population).
  It is upstream of the four failure modes `controls.md` already carries, and it is why the binding
  assertion here is scoped to the form the ADR mandates while the populations that are *not* defects
  are counted and printed instead of asserted against.
