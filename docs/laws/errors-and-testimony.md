<!-- Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0 -->

# Laws — Errors and testimony

An error message is a witness, and it can lie while the verdict is right.

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

  **Extension, same organ: a rendered artifact is a third channel, and it is the most
  persuasive and least accountable of the three.** Verdict and mechanism are already named as
  different instruments; the specimen adds the thing a human actually looks at. *A pipeline's
  exit status is its last stage's opinion, and a formatter's opinion is always that the
  formatting went fine.* `make bench` piped `go test` into `tee`, `tee` succeeded on
  `[build failed]`, make read 0, and **benchstat printed a table with a geomean** over a file
  whose contents were a build error (grave #289). Three channels disagreed and the quietest
  won, because a table formats successfully whatever it was handed. `benchstat or it didn't
  happen` was satisfied in letter by a run that measured nothing.

  **This project's largest instance of the third channel is the board.** Every board figure is
  cross-checked from an independent path precisely because the rendered table is the most
  believable and least accountable object in the repo — it will lay out four columns over any
  denominator at all, including one that silently narrowed. That is why the census, the
  per-bucket bounds, and the derived selector each ask the question a different way: not
  redundancy, but refusal to let the artifact be its own witness.

  A second-order note the fix earned, because it separates two axes that get conflated. The
  reasoned-toward repair was to keep the pipe and let `pipefail` fail it; with `-e` also in
  force that aborts the step **before its own `::error::` line prints** — the failure becomes
  *louder and less legible at the same time*. So the counters became glob loops with no failure
  to propagate. **Loudness and legibility are different axes, and a verdict that fires before
  its own testimony is counted but not printed.** (Correction: chat-Claude, on the #290 relay,
  against a mechanism he had reasoned toward and then withdrew — the instruction survived its
  bad premise only because the premise was carried as *unverified* and resolved rather than
  reconciled.)

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

  **And the rank holds for *our own* executable, not only the reference's:** where a
  document and an executable disagree about what the executable does, **the executable is
  the record and the document is the claim.** The heading says "the reference's" because
  that is the specimen that minted it, but nothing in the argument depends on whose
  implementation it is — a document describing behaviour is testimony about an artifact
  that can be run, and the artifact does not have opinions. Specimen: `publicpath_test.go`'s
  own header listed vector agreement under "counted and logged" while the assertion sat in
  the code fifty lines below, so the file carried two accounts of what it checked and the
  softer one was the one a reader met first. That is *the defect stated as the rule* pointed
  inward — the discipline's own output — and the remedy is the same, correct the document and
  say that it was the document that moved. (Sharpening: chat-Claude, PR #302.)

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

### A deferral's stated cost is part of the record's content, and it is the one class of claim nothing ever audits.

- **A deferral's stated cost is part of the record's content, and it is the one class of
  claim nothing ever audits.** The same organ as the hedge law above — what a record says
  about its own grounds is content, not framing — pointed at the sentence a scope note uses
  to *decline* work. Specimen: `internal/text/typetable.go`'s note deferred binding a
  typeuse's params into the local space on a named price, *"honouring that here would mean
  carrying a live local space per pending operation."* Having named a mechanism, the note
  made the deferral read as structural. It was false: only one number is unknown, so one
  thunk supplies it and nothing is carried per operation (#77).

  **The asymmetry is the law, and it generalizes past this file.** An estimate that argues
  *for* doing the work is audited the moment someone does it: the bill arrives and the
  figure is checkable against it, which is the mechanism
  [bucket size estimates the reward, not the job](boards-and-buckets.md#bucket-size-estimates-the-reward-not-the-job)
  relies on — that law's specimens are all forecasts someone paid out and could therefore
  compare. An estimate that argues *against* gets no audit ever, because **the bill only
  arrives with the work the estimate prevented.** The only event that can falsify it is
  someone doing the work anyway, which the estimate exists to discourage. So deferral costs
  are structurally the least-checked claims in this repo, and this one had sat for slices
  while the gap it protected produced a wrong index byte in shipped output — a well-formed
  image denoting a different function, which no suite vector can see.

  Note what that does to the symmetry the bucket law claims for itself. It says the estimate
  errs in *both* directions and prescribes a symmetric census — true of the estimates it can
  see, which are the ones attached to scheduled work. A deferral's cost is an estimate with
  **no scheduled work to attach to**, so it is outside that census by construction rather
  than by oversight. Two laws, one subject, and the seam between them is who pays.

  **How to apply.** When declining work on cost grounds, price the *alternative*
  implementation too, not only the one being rejected. The sentence to distrust is the one
  that names a mechanism — "a live X per Y", "this would need a second pass" — without
  having looked for the cheaper mechanism the deferral makes it comfortable not to look
  for; a cost sentence with a mechanism in it is doing the work of a measurement while
  being an assertion. And when one is falsified, **append the correction with the body
  intact**, on the same precedent 0003's LEB section set above: a scope note can be right
  about where work does not belong and wrong about what it would cost, and only one of
  those two halves was ever checkable from the file it sits in. Deleting it destroys the
  record of which half failed. (Ruling: Scott, PR #424.)

### A message is not its rendering, and a term about the rendering cannot be discharged by the message's author.

- **A message is not its rendering, and a term about the rendering cannot be discharged by
  the message's author.** An error in this engine is built at a leaf and *rendered* somewhere
  else — a wrapper up the call chain composes the text a reader, or a suite's expected string,
  actually sees. Those are two different artifacts with two different owners, and a
  pre-registered term stated over one of them is satisfiable only by the site that owns it.

  Specimen: #452's fifth approval term, *"the message born spec-phrase-first."* The sentinel
  was born that way — `ErrUninitializedLocal` is `errors.New("uninitialized local")` with its
  detail appended after — and the term was still unsatisfiable in that slice, because
  `internal/validate`'s `instrs` wrapped every instruction error as `instr %d (%s): %w`
  (`instr.go`, the wrapper now reading `%w (instr %d: %s)`). The rendered text was
  `instr 3 (local.get): uninitialized local: local 2`. **No leaf in the package could be born
  spec-phrase-first while that wrapper stood**, so the term named work in a different slice's
  file and nothing the approved slice did could move it. It was reported as owed rather than
  quietly reinterpreted, and it was discharged here, at the wrapper — `local_init.wast` awards
  `10/10` in the all-on lane under prefix matching, which is only possible if the rendering
  begins with the phrase.

  **The approval inherits the error, and the ruling says who carries it.** Scott gave the term
  on my description of the tree, and his ruling on the #490 review is the reason this is a law
  rather than a note: *"the approval conflated the two, and since I gave the term on your
  description, I carry it."* That is the same organ as
  [a status field is a citation to an approval](decisions-and-thesis.md#a-status-field-is-a-citation-to-an-approval-and-approvals-are-artifacts-with-provenance)
  read from the other end — a stamp is only as good as the description it was given on, so a
  term the describer could not have delivered is a defect in the description, upstream of the
  approval.

  **How to apply.** Before pre-registering a term about an error's *text*, name the site that
  composes the text and check it is in the slice. Grep the wrappers on the path (`%w` with
  anything before it is a rendering site) rather than reading the constructor and inferring.
  When the owning site is outside the slice, say so in the pre-registration and route the term
  to the slice that owns it — the term is not declined, it is **owed**, and an owed term with a
  named site is trackable where a reinterpreted one is not. The general form runs past error
  strings: any property of a *composed* artifact — a rendered message, a printed board line, a
  formatted citation — belongs to the composer, and a term placed on a contributor is a term
  nobody can meet. (Ruling: Scott, PR #490 review; discharged in ADR
  [0045](../decisions/0045-the-location-context-is-rendered-after-the-spec-phrase-and-the-harness-takes-the-references-prefix-rule.md),
  #455.)

  One consequence for this file: the *match what the suite's expected string contains* wording
  in the second law above is superseded — the reference matches by **prefix** only
  (`script/runner.ml:498-501`), and 0003's substring rule was amended on 2026-08-22. The
  wording stands where it is by this family's own convention of amending rather than
  rewriting, and it is exactly the kind of claim this law is about: it was true of the message
  and false of the rendering.
