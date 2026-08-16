<!-- Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0 -->

# Laws — Evidence and instruments

Which channel carries which answer, and what a measurement is worth.

Relocated from `CLAUDE.md`'s `## Disciplines` section, **verbatim**, when that file
became an index (see the restructure PR). Each law's one-line compressed form remains in
`CLAUDE.md` as its recall key and points here for the specimen, the minting record, and the
token it was granted on. Nothing was rewritten in the move: the bodies below are the text as
it stood, which is why superseded wordings still appear inside them where a later ruling
amended rather than replaced.

`CLAUDE.md`'s recall key and each heading here are checked equal by
`TestEveryLawIsIndexed` (`internal/testenv`), so the two cannot drift.

### The spec is the objective function; the suite samples it.

- **The spec is the objective function; the suite samples it.** The oracle
  answers the questions it is asked — it does not define correctness. So never
  buy pass count with a check that is wrong about inputs the suite has no vector
  for: that is overfitting to the oracle, and it is invisible on the board by
  construction. A decoder that rejects valid modules is worse than one that
  misses an invalid one. When the cheap check would pass the vectors and be
  wrong in general, leave the bucket open and say why (contract §9 G-3; the
  ruling on `data count section required`, #22).

### A verdict without an identity check is hearsay.

- **A verdict without an identity check is hearsay.** Bind a result to the thing
  it judges: the question is never "is it green," it is "is *this commit*
  green." A mechanism that cannot prove which run it is quoting is not a
  witness. A wait that returns the wrong run's verdict is worse than one that is
  merely slow — see *Waiting on CI* for the shape this took in practice.

### A re-run green doesn't refute a fail — explaining the fail does.

- **A re-run green doesn't refute a fail — explaining the fail does.** The
  temporal cousin of the identity-check law: two runs are two witnesses, and the
  favourable one does not impeach the unfavourable one merely by speaking second.
  A flake is a *diagnosis*, not a default, and it is earned by bounding the cause
  — `fuzz-smoke`'s `context deadline exceeded` was ruled flake only after the
  interesting hypothesis (a pathological parser input) was measured and killed:
  34ms worst case over adversarial shapes, 6× throughput margin, so wall-clock
  starvation. Re-running until green, with nothing explained, is the same reflex
  as scrolling past a warning. (Ruling: Scott, PR #27; the fix is #28.)

### Budget by the quantity the purpose names.

- **Budget by the quantity the purpose names.** A gate whose budget unit differs
  from its purpose unit will eventually fail for reasons that are not findings.
  `fuzz-smoke` exists to catch a target that stopped building or a corpus that
  regressed — its purpose is *executions*, and it was budgeted in *seconds* on
  hardware whose throughput varies 6× from a dev box. Wall-clock budgets on
  shared runners are timing-sensitive by construction. Sharpened by measuring the
  runner: it does not run *slowly*, it **stalls and sprints** — 47 dead
  three-second windows, one 18s unbroken, against 605k/sec bursts faster than the
  dev box. So the ~70k/sec "floor" was an average over a bimodal distribution,
  describing neither mode, and *a wall-clock budget against a bimodal rate is a
  coin flip on where the stalls land*. The unit fix cost 65s → 3m26s and the
  budgets were **not** shrunk to buy it back, because the job's purpose is the
  questions. The sweep's negative space is half the lesson: `make fuzz` and the
  nightly runs stay wall-clock, their purposes being durations — same flag, three
  purposes, two units, stated at each site. *A sweep that knows where the class
  doesn't apply is a sweep that understood the class.* (#28.)

### A stateful instrument measures history until its state is controlled.

- **A stateful instrument measures history until its state is controlled.**
  *Fuzzing is stateful — a measurement that doesn't clear the corpus is measuring
  the last run.* Adopting the executions budget needed proof a real crasher still
  fails under it; the second reading was a `FAIL` in 0.175s that looked like a
  spectacular find and was a **replay of a crasher the previous run had written
  into `testdata/fuzz`** — an instrument contaminated by its own priors. Generalizes
  past fuzzing: any instrument persisting state between measurements is reporting
  history, not the present, until that state is cleared or asserted. The sibling
  law is that **a fuzzer has two halves that fail independently, so certify them
  independently**: seed-replay proven by reintroducing a *known* defect (grave
  #18's zero-progress bug), exploration proven by a mutation-only needle *no seed
  can reach*. A budget that passes the first and never exercised the second has
  tested a corpus, not a fuzzer. (#28.)

### Verdict channel and mechanism channel are different instruments.

- **Verdict channel and mechanism channel are different instruments.** *An exit
  code is not a mechanism* — the verdict channel can't tell you why. *Don't infer
  a verdict from noise* — the output channel can't tell you whether. Read each
  for what it carries and never substitute one for the other: a tool that exits
  non-zero on findings is asked for its status, a tool that reports on stdout and
  exits 0 is asked for its output, and capturing `2>&1` to test for non-empty
  confuses a cold module cache with a defect (grave, PR #21).
  - **Re-scoped from pipes to compounds: the observed status belongs to whichever command
    produced it last, and appending anything to a command replaces its verdict.** Grave #289
    fixed this for *pipes* — `go test | tee` reporting `tee`'s success — and `pipefail-check`
    asserts the fix is live. **The scoping was too narrow, and the proof arrived within days**:
    running `make check > log 2>&1; echo "check exit: $?"` made the harness's reported status the
    **echo's 0** for a run that exited 2. Nothing was piped. The `$?` inside the echo was correct;
    the defect is that the *compound's* status is the last command's, so the wrapper added to
    observe the verdict is the thing that destroyed it. `pipefail` cannot help, because the class
    is not pipes — it is **any construct where the observed status belongs to a different command
    than the one under test**: `cmd; echo`, `cmd || true`, `cmd &`, a `$(…)` whose status is
    discarded by the next link in a `; \` chain. The remedy at the keyboard is one rule with no
    exceptions worth remembering — **run the command under test bare, and read its status from its
    own invocation**; if a wrapper is unavoidable, capture into a variable *before* anything else
    runs (`cmd > log 2>&1; st=$?`) and print the variable. This is the same instrument confusion
    the parent law names, applied to *time order* rather than to channel: the last writer to the
    status wins, and it is not necessarily the witness you called.
    - Sibling project `keel` carries the pipe half of this independently
      (`scripts/l1-bench.sh`: "without it the indent pipe would swallow the one status that says
      whether a comparison happened") and the adjacent rule that *a killed run is `unmeasured`,
      never an exit code*. **The exit-capture checker is `umami`'s — PRs `umami#396`/`umami#397`, merged as
      `umami@0b4ac1c`** (pointer: Scott, PR #298; a search of `~/src` from here had not found it, and it
      was flagged rather than guessed at, because a design invented here and described as grafted
      would be a fabricated provenance). Two things about the port, and the second is the load-
      bearing one:
      - **`umami` reached this place from a different failure**, so what transfers is a **design,
        not evidence about this repo**. A checker that caught a real defect there is a witness to
        `umami`'s codebase and to nothing here; the shapes a Go-plus-Make tree can hide a
        displaced status in are its own question. So the port earns **its own falsification** —
        write a compound whose status belongs to the wrong command, watch the checker go red on
        *this* tree — exactly as if the design had been invented here. Grafting the mechanism does
        not graft the *watched-die*, which is the half a control is not born without.
      - It is therefore a **second instrument, not a replacement for `pipefail-check`**: that
        target asserts a shell *setting* is live and covers pipes; a checker reads *constructs* and
        covers `cmd; echo`, `cmd || true`, `cmd &`, and the `$(…)` whose status the next link in a
        `; \` chain discards. #297 carries the port. (Ruling: Scott, PR #295; pointer supplied on
        PR #298.)

### Second-order honesty: apply the discipline to its own output.

- **Second-order honesty: apply the discipline to its own output.** Catching a
  figure as fiction earns nothing if its replacement is quoted with the same
  overconfidence. The ~70k/sec average was correctly called an artefact of a
  bimodal distribution — and then replaced with one run's numbers, one witness
  dressed as an environmental fact. The re-measurement is what separated the two
  claims: *the shape reproduces, the numbers don't* — dead windows and sprint
  bursts in both runs, peaks differing 2× (605k → 1.25M). So the shape is the
  finding and the numbers are the weather, and a two-run range is the honest
  representation of exactly that much knowledge. The sibling of *benchstat or it
  didn't happen*, pointed at environmental measurement: n=1 cannot separate a
  property from an accident of one scheduling. (#28.)

### Coverage is a claim: an instrument's domain is an assertion it cannot check about itself.

- **Coverage is a claim: an instrument's domain is an assertion it cannot check
  about itself.** Every instrument makes two claims and only one of them is
  falsifiable from the inside. Its **assertion** — this count equals that count,
  this table agrees with the reference — can be broken on purpose and watched to
  fail, which is what *a control isn't born until it has been watched die* buys.
  Its **domain** — the files it reads, the rows it matches, the population it
  ranges over — cannot be, because a domain that is too small produces **no
  finding rather than a wrong one**, and a green from an under-covered instrument
  is indistinguishable at the board from a green earned over everything. So the
  domain is stated, derived from the space rather than enumerated, and floored;
  and an instrument that reports a clean result has to say *over what*.

  **First specimen, and the one that names the shape: a control that names the
  fact it expects cannot notice that fact is missing** (#264, whose closing
  comment is that sentence). `ErrMalformedBrOnCastFlags` landed with its
  sentinel, its `%w` wrap, and **two controls asserting `errors.Is` against it**
  — and was not added to `declaredErrors`, the fuzz target's allowlist of errors
  the decoder is permitted to return. Both controls passed, because the sentinel
  they named was the right one; neither could ask *is this sentinel declared?*,
  since a test written from a sentinel's own name supplies the very fact the
  allowlist is missing. `make check` was green and `fuzz-smoke` went red in 41
  seconds. The instrument that can see it is the one that enumerates the
  decoder's **whole error surface** without knowing what any individual condition
  expects — which is why the repair was a bijection derived from the package
  (`internal/binary/errdecl_test.go`, #283) and not another assertion.

  **Second specimen, the recursion, which is why this is a key and not a
  corollary of the vacuity law: the remedy for a coverage defect can be a
  coverage defect.** `wholeFileGated`'s keys, after #285 zeroed them, were the
  six relaxed-SIMD files that had been *observed* declining — a registry of past
  findings wearing the shape of a domain, so a seventh file that never declined
  had no zero to violate. Scott's question on the flip named it exactly: *that is
  #264's sentence arriving inside #264's own remedy.* The answer was admissible
  only because the domain-side check beside it (`internal/spec/spec_test.go`) is
  derived from the space — all 254 files, all their declines, needing no registry
  — and because the seventh file's absence was then shown to be **structural**: it
  holds one command, which passes with every gate off, so no gate can reach a
  scorable command there and a zero key would be an assertion that cannot fail.
  *Derive the domain from the space, never from the registry* (#264) is the
  operative sentence in both.

  **Third specimen, one layer out, where the domain is chosen by a tool and
  never announced:** `git grep` searches tracked files only, so a sweep run with
  it excluded exactly the new unstaged region the grave came from and returned a
  confident empty set. Full text at *a guard's trigger predicate is itself a
  claim about the space* (`controls.md`), whose third specimen this is — that law
  is this one applied to a *predicate*, and it stays where it is; **a search
  command's default domain is a claim about the space, made silently by the tool
  rather than by the author** is the sentence that generalizes past regexps to
  every instrument with a default scope. `go test ./pkg` says one package, a bare
  `grep` says one directory, a fuzz target says whatever is in its corpus
  directory, and none of them announce the restriction.

  **Fourth specimen, minted in the same PR as the law and by it:**
  `scripts/citecheck.sh`, the check that every `#NNNN` a diff adds resolves,
  matched the `+` lines of a diff one at a time — and missed `ADR 0025` in its own
  PR, because the citation was split by a prose wrap (`... citing ADR` / `0025's
  carve-out ...`). Its assertion was sound and its population was short by
  whatever wraps, which is most prose citations in this repo. It is the
  wrapped-lead defect a fourth time (#78, #105, and the un-graved recurrence on
  #80), in the instrument written to enforce
  the law about it, and it was found the only way this class is ever found: by
  running the instrument over the diff that introduced it, then reading the
  printed citation count instead of the exit status. *Artifacts become oracles*,
  and the sibling that had already paid for it — `internal/testenv`'s law-index
  reader, which splits into bullets and joins each before matching — was two
  directories away.

  **Fifth specimen, and the one the law caught in its own falsification
  procedure: a probe set can exclude the invocation form its consumers use.**
  Every one of the four probes above was run as `sh scripts/citecheck.sh …`, so
  all four passed while the file's **executable bit was never committed** — and
  both binding consumers invoke it as `./scripts/citecheck.sh`, which is the one
  form no probe used. CI said `citations -> failure`, exit 126, `Permission
  denied`: a red gate that had not run its check at all. The falsification was
  sound in its assertions and short in its domain by exactly one dimension —
  *how the thing is called* — and the local mirror could not disagree, because
  `make cite` was never the command that ran it. The repair is the same shape as
  every other repair in this entry: exercise the **path**, not the artifact
  behind it, so `make cite` and the CI job are one invocation with two homes.
  Also the cheapest available reading of *verdict channel and mechanism channel
  are different instruments* — a `citations` job going red says nothing about
  whether a citation failed.

  **Recurrence, in the same file, two PRs later, and counted rather than
  re-minted:** the `project#N` arm added on #298 was falsified against an
  unqualified number and a `umami`-qualified one — two adjacency shapes, and the
  defect lived in the third. (Those probe numbers are described rather than
  quoted, because a fake number written into a law is scanned by the checker the
  law is about; the first draft of this paragraph quoted them and went red.)
  Letting the qualifier end in `-` meant `pre-#298`, ordinary hyphenated
  English, parsed as project `pre-` and a real citation was silently exempted
  from resolution. Same domain-short-by-one-dimension as the executable bit,
  and the dimension is again *the forms a consumer actually writes* rather than
  the forms the author enumerated. A probe set of two is a probe set that has
  chosen its axis; the space of characters that can precede a `#` is derivable,
  and was not derived.

  **Sixth specimen, with the census itself as the subject, and filed as a
  specimen rather than minted as a law** (ruling: Scott, PR #307).
  `TestEveryBoardBoundIsChecked` derives the board's bounds from the package AST
  by matching `*ast.ValueSpec` — the naming convention on a declaration, which is
  the right trigger and was a complete domain for eighteen bounds. #307 made
  `validateAdmitCeiling` **derived** (`142 + <live members of a named set>`), so
  it became a `:=` and therefore an `*ast.AssignStmt`, and the census reported
  **18 bounds** while all nineteen were present, correctly declared, and
  correctly routed through `boardBound`. Nothing was undocumented and nothing
  unbounded; the instrument's claim about *where a bound lives* had gone false
  under it. The specimen's value is which guard caught it: `minBoundPopulation`
  is **8**, so the floor was satisfied by 18 and said nothing — a floor cannot
  notice a missing member of a population of 19 — and the **exact count** fired
  on its own. That is this entry's closing paragraph demonstrated rather than
  restated, which is also why it is a specimen and not a new key: the law already
  said it, and the census was the subject that had not been tested. Its sibling
  reading is *floors bound the catastrophic case; only an exact count sees a
  small silent loss* (`controls.md`), reached in another project from an
  unrelated failure — **two independent derivations, which is corroboration in
  the way a single shape recurring inside one instrument is not**.

  The two failure modes are worth keeping separate because they are found
  differently. An **assertion** defect is found by falsification — break it, watch
  it die. A **coverage** defect is found only by measuring the instrument's
  population against an independently derived one, which is why floors, exact
  counts, and vacuity guards are not redundant with each other: a floor catches a
  domain that collapsed, an exact count catches one that shrank quietly, and a
  vacuity guard catches one that emptied. (Ruling: Scott, PR #285 relay; minted
  from #264, whose closing comment is the first specimen's own wording.)
