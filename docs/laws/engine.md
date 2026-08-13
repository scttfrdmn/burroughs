<!-- Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0 -->

# Laws — Engine

The rules about the artifact itself.

Relocated from `CLAUDE.md`'s `## Disciplines` section, **verbatim**, when that file
became an index (see the restructure PR). Each law's one-line compressed form remains in
`CLAUDE.md` as its recall key and points here for the specimen, the minting record, and the
token it was granted on. Nothing was rewritten in the move: the bodies below are the text as
it stood, which is why superseded wordings still appear inside them where a later ruling
amended rather than replaced.

`CLAUDE.md`'s recall key and each heading here are checked equal by
`TestEveryLawIsIndexed` (`internal/testenv`), so the two cannot drift.

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
