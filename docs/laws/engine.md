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

### An enumeration held by construction outranks one held by a scan.

The second conjunct above asks for an enumeration a control asserts. **Where the mechanism can make the
defect unwriteable instead, that is strictly better, and the scan becomes a check on a class that can no
longer be entered.** Scott, on the #637 review: *"the design settled by scoping is the right shape twice
over — moving `slots`/`refs`/`bytes` inside the image structs so a direct field access stops compiling
makes the enumeration hold by construction rather than by a scan, which is the same dissolving move as
#575."*

[ADR 0065](../decisions/0065-the-table-and-segment-headers-move-inside-published-images-because-a-field-that-cannot-be-named-needs-no-enumeration-to-confine-it.md)
is the specimen. #622's defect was three sites publishing a slice header into an already-reachable object
by assigning the owning struct's field — `t.slots = grown`, `s.refs = nil`, `s.bytes = nil`. A comment
naming those three sites is testimony; a control enumerating them is a bounded claim that a fourth site
would break; putting the slice **inside** the published image makes `t.slots` a compile error, so the
fourth site cannot be written. The alternative that was available and rejected — keep the field where it
is and add a scan for assignments to it — would have paid an instrument for a property the type system
gives away.

**The limit is that construction is not always available.** 0064's bulk and SIMD region is plain by
decision, not by an accident of scoping: nothing can be moved to make a plain byte loop unwriteable, so
there the enumeration-plus-control stands and this law does not reach it. The question to ask in that
order — **can the defect be made unnameable? if not, can it be enumerated with a control? if not, it is
not covered by the exception at all** — is what keeps the three answers from being read as
interchangeable. (In-session review; PR #637 carries no comment to cite, and this is recorded by the actor
the ruling was given to — durable, not independent, so `Ratio-Class: carried`.)
