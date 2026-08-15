<!-- Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0 -->

# Laws — Errors and testimony

An error message is a witness, and it can lie while the verdict is right.

Relocated from `CLAUDE.md`'s `## Disciplines` section, **verbatim**, when that file
became an index (see the restructure PR). Each law's one-line compressed form remains in
`CLAUDE.md` as its recall key and points here for the specimen, the minting record, and the
token it was granted on. Nothing was rewritten in the move: the bodies below are the text as
it stood, which is why superseded wordings still appear inside them where a later ruling
amended rather than replaced.

`CLAUDE.md`'s recall key and each heading here are checked equal by
`TestEveryLawIsIndexed` (`internal/testenv`), so the two cannot drift.

### An error from the wrong layer is evidence about where structure was lost.

- **An error from the wrong layer is evidence about where structure was lost.**
  When a lower grammar is missing, its bytes do not vanish — they leak upward and
  get misread by whatever grammar *is* running, so the error names a field the
  input never contained. `malformed section id: 128` on eight LEB vectors was the
  code section's immediates being read as section ids: a diagnosis, not a defect in
  the section-id check. Read the mismatch between the error's layer and the
  vector's layer as a pointer to the missing descent. The same tell has an
  intra-layer form — an error message that reports a byte the image never held (the
  functype tag reconstruction printing `0x5e` as `0xde`) is the engine lying about
  its input, and no suite can see it, because the harness matches the sentinel and
  reads no further than the expected string does. (#36.)

### An error message is testimony, and fabricated evidence is a lying witness even when the verdict is right.

- **An error message is testimony, and fabricated evidence is a lying witness even
  when the verdict is right.** The rule is **match what the suite's expected string
  contains** — and for most vectors that string stops at the sentinel, which is
  exactly why everything past it is ours alone to keep honest.
  `ErrMalformedFuncType`'s reconstruction or'd a high bit in for every negative form
  and reported a `0x5e` array tag as `0xde`: the right verdict, quoting a byte the
  image never held, and green on every board by construction. So a message that names
  a value from the input gets *printed for real inputs* before it is trusted — the
  print-don't-trust check applies to the half of the error the oracle cannot see, and
  that is where it earns the most. Its sibling above is the wrong-layer tell: both are
  the engine being wrong about its own input, one across layers and one inside a
  format string. (Ruling: Scott, PR #37; grave #36.)
  **Refinement, not repeal (#38):** *some expected strings carry data.*
  `binary.wast:1218` expects `illegal opcode ff` — the byte is *inside* the sentinel —
  so for those vectors message rendering is **oracle-covered**, and the invented-bits
  class has suite teeth in the one place vectors exist. The doctrine was never "ignore
  message text"; it was "the oracle reads exactly as far as its expected string
  does." Print-checks cover everywhere it stops short, which is nearly everywhere.
  Read the vector to know which case you are in — and note the shape: the sibling of
  the buried defect is the newly-checked case. (Ruling: chat-Claude, #38.)

  **The class is not confined to error strings, and the specimen that proves it is the
  reviewer committing the substitution he had just ratified.** Scott's own ledger, on the
  PR #285 relay, recorded a directive as *"not delivered"* — a claim about the agent's
  action — when the record behind it supported only *"not received by me"*, a claim about
  his tracking. Same shape as `0xde`: the verdict was right (the sentence in question was
  and remains unminted, and the sweep found nothing to remove), the ground quoted was
  never in the image, and nothing on any board could have contradicted it. He filed the
  correction himself, and the reason it is recorded here rather than absorbed as an
  ordinary erratum is his: *the specimen is the reviewer committing the substitution he
  ratified, not the substitution alone* — a law whose author breaks it while enforcing it
  is evidence that the defect is structural to how records get written and not a property
  of the party writing them. Two operative consequences. **An absence in your own records
  is evidence about your records**, so it is reported as such — the tracker's version of
  *silence is not evidence*. And the payload-verification rule that this project applies
  to `gh issue close` applies to instructions: a directive believed delivered because a
  status somewhere said so is *presence-of-status mistaken for presence-of-content*,
  pointed at the chair rather than at the tool. The outcome of the corrected record was
  unchanged; only its stated reason moved, from "not delivered" to "delivered and never
  minted", which is the whole point — **a right verdict does not launder its grounds.**
  (Correction: Scott, PR #285 relay, on his own ledger.)

### Comments and ADRs are testimony too, and where prose and the reference's executable disagree, the executable outranks.

- **Comments and ADRs are testimony too, and where prose and the reference's
  executable disagree, the executable outranks.** 0003's LEB section said "the order
  matters" and then prescribed the wrong order, so the implementation followed its
  documentation faithfully and every reviewer who checked code against claims found
  agreement — *the defect stated as the rule*, which is the strongest camouflage a bug
  can wear, because review verifies code against claims. The mechanical tell is in the
  same sentence: the order was "derived from the actual vectors", and those vectors
  were precisely the ones where the two conditions do not overlap, so they could not
  distinguish the orderings. An order-of-tests claim needs an *authority*
  (`interpreter/binary/decode.ml`), never a derivation from a sample that cannot
  falsify it — the scope-controls-to-the-space law pointed at documentation. And a
  ruling like this one is discharged by **appending** to the ADR, body preserved: the
  record of what was believed, and of why it survived review, is the part worth
  keeping. (Ruling: Scott, PR #37; the correction is in 0003.)

### When two fields disagree about a value, the suite has handed you a bidirectional control.

- **When two fields disagree about a value, the suite has handed you a
  bidirectional control.** The width-parameterized design's dividend: identical
  bytes `80 80 80 80 10` are *integer too large* as a data-segment memory index and
  perfectly legal as a limits minimum, because one field is 32 bits and the other
  64. Pin **both** directions in one test — a single width being wrong then fails
  the two halves in opposite directions, where either half alone would look like a
  plausible reading. Prefer such a pair over two independent assertions whenever a
  value's verdict depends on the field rather than on the bytes. (#36.)

### A hedge is part of a record's content, so prose that resolves an accepted record's open question in passing has forged an agreement.

- **A hedge is part of a record's content, so prose that resolves an accepted
  record's open question in passing has forged an agreement.** The specimen is this
  agent's own filing of grave #280, which said decision 0028's d2 rationale *"asserted"*
  that a double-rounding gap was innocuous. It did not. 0028 at `:184` names the exact
  gap — *"the classical theorem is stated for the basic operations rather than for a
  fused multiply-add"* — and then states *"I have not verified it and do not assert it
  either way,"* filing the question with a tripwire and pre-registering both outcomes.
  The hedge *was the record's finding*: a careful ADR's "I do not know" is a measurement
  of the project's confidence, and a draft comment that borrowed 0028's authority while
  quietly closing the question drifted in the direction that matters most — **toward
  confidence**. This is worse than an ordinary stale comment, because a reader who
  follows the citation finds an ADR saying the opposite, which is why it belongs beside
  the fabricated-evidence law rather than under mere drift. Two corollaries earned in
  the same correction: the generalized lesson underneath it survives — *a theorem is a
  citation and its hypothesis is the part that resolves* — but as a rule the ADR
  **followed**, so the law is stated about hedges and not about theorems; and a first
  filing that is wrong about its own subject is **corrected by comment and retitled with
  the superseded title kept verbatim**, never deleted, on #143's precedent. Nothing false
  reached a merged artifact here — `git cat-file` confirmed the file did not exist on
  `origin/main` — so what the specimen shows is the mechanism working, caught by the ADR
  it had misquoted. (Ruling: Scott, on the PR #281 relay; minted under the #277 gate,
  which is why this body and its `CLAUDE.md` key landed together.)

  **Second specimen, the mirror image — testimony that is *true* and believed for the
  wrong reason. A carve-out cited where it is not load-bearing is a citation that will be
  believed the next time it is.** The relaxed-SIMD flip (#285) was drafted citing ADR
  0025's carve-out, which excuses vectors whose sole blocker is #9's deferred validator
  from G-1's all-green requirement. Every sentence of that citation was accurate — the
  carve-out exists, it is accepted, it covers exactly that class. It was also *unnecessary*:
  the proposal's own suite measured `pass=77 fail=0 unsupported=0 gated=0`, identical on
  both arches, so the flip satisfies G-1's **literal** reading and the carve-out is not
  reached at all. A reader who follows the citation finds a genuine exemption and concludes
  the flip needed one, which is a wrong belief about how strong the evidence was —
  manufactured by a true statement. The flip landed saying so in `sections.go`'s own prose:
  the carve-out is named as **not invoked**, with the reason, because the next flip that
  does need it must be the first one that appears to. Sited here rather than under drift
  because the defect is in a *record's* accuracy about its own grounds and not in a
  measurement's coverage: an instrument claiming coverage it lacks is
  *coverage-is-a-claim* (`evidence-and-instruments.md`); testimony whose citation
  overstates the difficulty of the case it decided is this. (Ruling: Scott, PR #285 relay —
  filed as a specimen and explicitly **not** minted as a key: "it's a specimen, not a key",
  the index ceiling being real.)
