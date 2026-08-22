# 0046 — The accept-direction blind spot is ratified as a rider on G-3, the clause 243 citations already mean

Date: 2026-08-22 · Status: **accepted** — §§0–9 sign-off given by Scott on the #486 review, relayed to
[a durable comment](https://github.com/scttfrdmn/burroughs/issues/442#issuecomment-5381768900), with
two terms on the text: it is **dated**, and it **ratifies rather than introduces**.

Filed against **#442**. Amends contract §9 G-3.

## Context

**§9 G-3 is cited 243 times in this tree and not once for what it says.** Its text is:

> **G-3.** **The neutrality guarantee is G-1.** Partisanship lives only in §§2–8's API surface and in
> optimization priorities — never in conformance. No guest may be broken to make Go faster.

What the citations use it for is a different proposition: *the suite is a corpus of rejections, so a
defect in the accept direction scores green by construction.* True, load-bearing, and **§9 does not
state it anywhere** — not in G-1 (gate acceptance), G-2 (the tracked set), or G-4 (the battery, whose
rider *"the judge needs a judge"* is the nearest neighbour and still a different sentence).

**Not drift from an edited contract.** `git show 017af60:docs/burroughs-contract-v0.1.md` — genesis —
already reads *"The neutrality guarantee is G-1"*. G-3's text has never said anything else, and the
only contract commit since is `846db5c`, which amended G-1. So the mis-citation dates to the first
sites and was copied, each new author taking the convention from its neighbours.

### The measurement that decides the mechanism

Every `G-3` occurrence in the tree, classified by the proposition its surrounding ±12 lines assert.
Derived from `git grep -l G-3` rather than from a list, and the cells sum:

| what the site uses G-3 for | occurrences |
| --- | --- |
| the accept-direction blind spot | 233 |
| accept-direction, with neutrality vocabulary also in the window | 5 |
| unclassified by the window, accept-direction on reading the line | 3 |
| a bare `Contract refs:` header asserting no proposition | 2 |
| **neutrality or partisanship — G-3's actual text** | **0** |
| the contract's own statement of G-3 | 1 |
| **total** | **244** |

**Zero.** Not one site in the tree cites G-3 for neutrality, partisanship, or *"no guest may be broken
to make Go faster"*. The clause's entire citation traffic is the blind spot.

The by-clause distribution is the same fact from the other side. Over the whole clause namespace —
every `T-`, `SP-`, `B-MM-`, `H-`, `R-`, `S-`, `M-`, `G-` token in the tree at `30377aa`, counted with a
word boundary on both ends and split on whether it sits inside the contract or cites it from outside:

| clause | citations from outside the contract |
| --- | --- |
| **G-3** | **243** |
| G-1 | 57 |
| G-2 | 25 |
| S-3 | 5 |
| G-4 | 4 |
| M-4 | 3 |
| **total** | **337** |
| *the other 27 defined clauses* | *0* |

**G-3 alone is 72% of every contract-clause citation this project has ever written; §9 is 329 of the
337; and 27 of the 33 clauses the contract defines are cited nowhere outside it.** The single
most-cited clause in the contract is the one clause whose stated content nothing cites.

*The narrower spelling would have said 92%, and naming that is the point.* Counting only `§9 G-M`
gives 222 against 11 / 8 / 1 — 92% — because the bare form is not distributed like the prefixed one:
G-1 is written bare 51 times against G-3's 22, so the prefixed spelling over-represents G-3
specifically. Both figures are true of their populations and the wide one is the one that supports the
sentence, which is *a valid citation does not certify its sentence* applied to a percentage: the
citation here is the measurement, and it was resolvable while the quantifier over it was drawn from a
sample.

**The count moved, and the direction matters.** #442's body says 237, measured at an earlier revision;
it is 244 occurrences / 243 citations now. Re-measured rather than carried, because *a count told to a
principal comes back as a premise in their order* — and the population is also **wider** than the
issue's spelling: the issue counted `§9 G-3`, and 21 further sites write bare `G-3` or `G-3's`. A
mechanism priced against the issue's 237 would have missed 6 sites by revision and 21 by spelling.

## Decision

**A dated rider on G-3, ratifying the reading 243 citations already gave it.** Not a new G-5.

The rider states the blind spot as a property of a negative-vector corpus, says it held from genesis,
and leaves G-3's existing two sentences untouched and normative.

**Why G-3 and not a new clause, in one line: it is the only mechanism whose site cost is zero.** A new
G-5 makes 243 citations resolve by requiring 243 edits; a G-3 rider makes them resolve by requiring none.
That is the entire mechanism cost of #442, and the two options are otherwise indistinguishable in what
the contract ends up guaranteeing.

**Why it is coherent rather than merely cheap.** G-3's job is to name *where the neutrality guarantee
lives* — it points at G-1, the suite. The rider states that guarantee's **ceiling**: what the suite
cannot witness. A clause that names a guarantee is the natural home of that guarantee's limit, and the
243 authors who reached for G-3 rather than G-1 were not making 243 independent errors — they were
looking where a reader looks for the scope of the neutrality claim. **A convention 243 sites deep is
evidence about where the sentence belongs**, not merely a cost to be minimized.

**Why "ratifies" is not a courtesy.** The blind spot is a property of a corpus of rejections, so it was
true on the day the first vector was scored. Writing the clause as *new* would assert that verdicts
before this date were unbounded, which is false, and would make every one of the 243 pre-amendment
citations read as anachronistic — citing a rule that did not yet exist. Scott's term, and the reason
for it is the foreclosing-words shape: a sentence that misdates its own subject tells the next reader
the tree was in a state it never was.

### The three alternatives, and why they lose

- **A new G-5.** The clean single-proposition clause, and the one the issue named first. It costs 243
  edits that buy nothing the rider does not, and it introduces an interval — between the amendment and
  the completed sweep — in which 243 sites cite a clause that exists and is *about something else*,
  which is the pre-amendment state with a false sense of resolution added. A partial sweep here is
  worse than none.
- **A rider on G-1.** Logically the tightest: G-1 is the clause that says "suite green", and the blind
  spot bounds what green *means*. It loses on two counts. Zero sites cite G-1 for it, so the site cost
  returns. And G-1 already carries two long amendment riders about #9's carve-out and its retirement
  condition; a third rider on an unrelated subject would be read as part of that argument, which is
  the burial the #464/#466 reconciliation paid for once already.
- **A rider on G-4.** G-4 defines the battery's **composition** — upstream suites plus the §4 litmus
  battery plus the torture set. The blind spot is not a component; it is a limit on what any component
  can report. Its existing rider is about controls for classifiers, which is the *response* to the
  blind spot rather than the blind spot, and merging the two would blur a distinction the tree relies
  on.

### What the amendment does not do

It does not make the 243 sites' **reasoning** newly correct — that was never in question. A
rejection-only corpus genuinely cannot witness a wrongly-accepted module, which is why this repo has
accept-direction controls and why they have paid for themselves repeatedly. The defect was that a true
proposition was attributed to a clause that did not contain it, so a reader who followed the citation
found a different rule and could not tell whether the code or the contract had moved.

## The instrument, and the honest bound on it

**A contract-clause resolver: every clause token in the tree names a clause the contract defines.**

**It goes in `internal/testenv`, not in `citecheck.sh` as #442's body proposed, and the home moved for a
measured reason.** The defect's population is tree-wide — 337 citations across 95 `.go` files, 16
markdown files, `scripts/`, and the CI workflow — while `citecheck.sh --pr` sees a diff and a PR body.
A diff-scoped resolver would have declared the channel checked while 337 standing citations, every one
of them written before the check existed, stayed outside its domain. This is exactly the widening #466
performed on `TestMarkdownLinksResolve`, for the same reason in the same words: *the corpus cites
itself, and a heading rename breaks incoming citations no control could see.* A clause renumbering is
that rename with a different token shape. `internal/testenv` is also where the domain can be **derived
from a tree walk** and the clause vocabulary **derived from the contract's own headers**, rather than
enumerated — which is what makes the resolver's own blind spot bounded rather than unknown.

**It would not have caught this defect, and that is stated rather than left for a reader to discover.**
G-3 exists. All 378 clause tokens at `30377aa` resolve, before this amendment and after it — the 337
counted above plus the 41 the contract writes itself. The resolver catches a typo or a
renumbering — a clause number outside the range its family defines, or a section coordinate written
beside a clause that lives in a different section — and nothing about *aboutness*, because whether a
clause supports a proposition is not mechanizable from the clause's text.

**The word boundary is load-bearing, and that is a measurement rather than a caution.** The first census
run reported a §5 clause the contract does not define, which would have been the resolver's first FAIL
and its first false one. The site is `internal/testenv/closebody_test.go:103`, where the token is `GH-7`
inside a fixture — three characters shaped like a clause reference, at the end of an issue reference in
another project's convention. A resolver whose trigger lacks a left boundary fires on every `GH-N` in
the tree and teaches its author to phrase around it, which is *an exemption inherits none of the
trigger's lessons* arriving before the exemption is even written.

**The resolver's first run found 12 references in 2 files, all of them in prose about the resolver** —
six in this document and six in the control itself, every one an illustration of what a dangling clause
reference looks like, written by writing one. `citation_test.go` bought this shape already and its
ruling governs: *when a control fires on its own explanation, fix the explanation*, because exempting
"prose about the class" buys a green by blinding the control to the population most likely to hold a
stale reference. Ten were repaired here in prose. The remaining two are this document's
rejected-alternatives section naming the clause it declined, which is a decision record doing its job
and not a typo wearing prose — so the control carries exactly one exemption, keyed to the grammar that
says the clause does not exist (a determiner and *new* immediately before the reference), scoped by
adjacency rather than by sentence, and printed with its size on every run.

So the resolver ships with a second half that is a **tell rather than a verdict**: the per-clause
citation census, printed whether or not anything fails. What it prints is the table above, and the two
things a human needs are both in the shape rather than in any single cell — one clause holding 72% of
the traffic, and **27 of 33 clauses holding none**. A clause nothing cites is not necessarily wrong, but
a clause that is cited 243 times is a clause worth reading, and nobody had. That is the same shape as
every other census here — *coverage is a claim*, so a checker that reports OK without saying over what
has made a silent claim about its own population — and it is the honest version of "without it, the same
channel refills": the resolver stops a dangling clause reference, the census is what makes a
*misdirected* one visible.

**The census's numbers will not match this document's table, and the reason is in the census.** The
table above is stamped at `30377aa`, before this slice; the resolver counts the tree it is run on, and
this slice's own text — the amendment, this ADR, and the control's prose and fixtures — is in that tree.
The control's own file is a double-digit percentage of every citation counted from outside the contract,
and landing it took three clauses from cited-nowhere to cited. The handling is disclosure rather than
exclusion: the census prints a per-clause `self` column, that footprint as a percentage, and a count of
clauses cited nowhere but the control, because dropping the control's file from its own sample would be
an exemption written by the party it flatters. The verdict half has no exemption for it at all.

**This paragraph gave its own figures twice and was wrong twice, which is why it now gives none.** The
first draft said *a quarter*, from subtracting a pre-slice total from a post-slice one and charging the
whole slice's delta to this one file — *an unmeasured complement is not an empty one*. The repair said
14%, and *"all three of those clauses are cited nowhere but there"*. Then the `CHANGELOG.md` entry
written to describe this footprint cited `B-MM-1` while describing it, which moved the row: the
percentage fell to 13% and one of the three clauses became cited outside the control. So a sentence
about an instrument's effect on its own sample was falsified by the sentence describing it — the same
shape one level up, and the reason [CLAUDE.md](../../CLAUDE.md)'s rider says a measured figure is
generated or deleted rather than written down. **Ask the instrument:**
`go test ./internal/testenv/ -run TestEveryContractClauseCitationResolves -v`. What is stable here is
the *shape* — one clause holding most of the traffic, most clauses holding none, and an apparatus large
enough in its own sample that the sample says so — and the shape is what the argument rests on.

## Consequences

- **243 citations resolve, with zero site edits.** The clause they name now contains the proposition
  they attribute to it.
- **G-3 states two things**, and the rider says why they are one subject: where the neutrality
  guarantee lives, and what it cannot see. A later reader tempted to split them should read this
  section rather than re-deriving the split.
- **The contract version does not move.** Amendments are riders under the clause they amend, which is
  the mechanism G-1 and G-2 already use (`#230`/ADR 0025, `#483`/ADR 0043, `#109`). The engine's SemVer
  and the contract's version are independent by
  [0004](0004-versioning-and-contract-independence.md), and a rider is not a new contract version.
- **`docs/decisions/0039`'s deliberate non-citation is now stale in the good direction.** It stated the
  proposition in its own words *"rather than with this tree's usual `§9 G-3` citation, which #442 found
  does not resolve"*. It resolves now. Left standing with a dated note rather than rewritten, because
  it is a record of a state the tree was in.
- **The clause-citation channel had no instrument for the project's whole life**, and the reason is
  that every existing citation control is keyed to a *shape* this one does not have: `citecheck.sh`
  reads `#N`, `PR #N`, and ADR numbers; `TestMarkdownLinksResolve` reads bracketed markdown link
  destinations; `TestEveryCitedTestNameResolves` reads Go identifiers in backticks. A clause token is
  none of those, so it was in no control's domain — not by exemption, by never having been enumerated.
  337 is what an unchecked citation channel accumulates in one project's lifetime, which is the general
  lesson and is why the resolver ships in this PR rather than being filed.
  - **Naming that shape cost this document a red `make check`, which is the second instrument in this
    slice to fire on prose about itself.** The sentence above originally spelled the link form out as a
    metasyntactic example inside backticks, and `TestMarkdownLinksResolve` reported a destination that
    resolves to nothing — correctly: **a code span is not an exemption**, because the extractor reads
    the destination and a reader following it lands in the same nowhere either way. So the repair is the
    same one `citation_test.go` already ruled for: describe the shape in words rather than exhibit it.
    The clause resolver had already taken this repair once in this slice, for its own token; a second
    channel taking it independently is what *lessons are indexed by shape, not by file* looks like when
    the shape arrives twice in one diff.
