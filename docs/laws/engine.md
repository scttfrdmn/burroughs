<!-- Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0 -->

# Laws — Engine

The rules about the artifact itself.

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

### The suite is the oracle.

- **The suite is the oracle.** A feature exists when its spec tests pass.
  Claims of correctness cite counts (`N/N green`), not impressions.

### No cgo. Pure Go.

- **No cgo. Pure Go.** `make check` clean at every commit (see Tooling gates).

### Parsers prove progress, they don't assume it.

- **Parsers prove progress, they don't assume it.** A loop whose exit condition
  and error condition are the same predicate is the zero-progress bug; it
  surfaces as an error only when the offending byte happens to be a delimiter,
  and hangs otherwise. Every reader gets a fuzz target asserting the offset
  moved. *A delimiter set is a claim about what cannot start a token, and one
  that's right for the grammar can still be wrong for the corpus* (grave, #18).

### Honest boards.

- **Honest boards.** The PR description and the issue tracker reflect
  reality, including what's red. Never quote a suite count that wasn't run.

### The guest's data races must not be the host's, except where the model permits the tear and the region is enumerable

Scott's ruling on [#566](https://github.com/scttfrdmn/burroughs/issues/566), given as *"the principle is
that the guest's data races must not be the host's"* and re-stated with its scope on the
[#627](https://github.com/scttfrdmn/burroughs/issues/627) rulings, in his words:

> the guest's data races must not be the host's, except where the guest model itself permits the tear and
> the region is statically enumerable with the enumeration asserted by a control

**Why the exception is written down rather than left implicit.** [ADR 0064](../decisions/0064-the-bulk-and-simd-region-stays-plain-and-is-confined-by-an-enumeration-a-control-asserts-because-the-guest-model-permits-the-tear.md)
keeps the bulk and SIMD families' plain accesses plain at seven sites reachable with no opt-in, which is
exactly a position that some of the guest's races are the host's. Leaving the unqualified sentence standing
would leave this page carrying a principle the tree contradicts — the foreclosing-words shape, one level up
from code. Scott's phrasing for the repair: *"#566's ruling is re-stated with its scope rather than left to
read as narrowed by stealth."*

**The exception has two conjuncts and neither is optional.** *The model permits the tear* is a claim about
the proposal being implemented — the threads proposal permits a racing `memory.fill` to be observed torn,
so no correctness is bought by making it atomic, and what is traded away is **report-freedom**, a property
of the instruments rather than of the semantics. *Statically enumerable, with the enumeration asserted by a
control* is the half that does the work: **testimony alone does not carry it.** Comments asserting a
region's extent are unchecked claims that survive the region growing — which is why the second conjunct
names a control and not a doc. Scott, on the same rulings: *"testimony alone, no — but an enumeration with
a control that fails when a new site joins isn't testimony. It's a bounded, checkable claim."*

**Where a region fails the second conjunct, it is not covered by this exception**, and 0054's typed word
path is the worked example: every load and store in the ISA is not an enumeration, which is why #567 bought
the throughput cost there and 0064 does not buy it here.
