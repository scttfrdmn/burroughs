# Changelog

All notable changes to Burroughs are documented here.

The format is [Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning 2.0.0](https://semver.org/spec/v2.0.0.html).

**Two versions, deliberately independent** (decision 0004, resolving contract
§10.7): the engine's SemVer governs code compatibility, while the host
contract versions on its own schedule and governs semantic promises. Every
release below states the contract version it implements.

Minor versions map to milestones, so the number is a conformance statement:
`v0.1.0` is the MVP core suite green, one minor per proposal gate flipped
after that, and `v1.0.0` is reserved for the v1 threads-and-safepoints
milestone landing with the §4 litmus battery passing on both a TSO and a
weakly-ordered platform.

## [Unreleased]

*Implements contract v0.1.*

### Added
- `internal/spec`: the `.wast` harness, phase 1 (decision 0003) — a pure-Go
  s-expression reader, wast string-literal decoding, the `(module binary ...)`
  form, and `assert_malformed` matched by substring. No wabt, no non-Go tool
  in the conformance loop. **First suite numbers: `binary.wast` 49/127 pass,
  78 fail, 0 unsupported.**
- Boards bucket failures by expected spec error string, ordered largest first,
  so the suite schedules the decoder work: the biggest bucket is the next
  issue and a bucket reaching zero is a PR's measure of done. Pinned by a test
  that asserts the ordering property, not just the counts.
- Parser-robustness sweep: all 257 upstream `.wast` files must parse, even the
  ones phase 1 cannot execute. A parse error and an unsupported command are
  different numbers, and conflating them would hide the real unsupported count.
- Semantic Versioning 2.0.0 and Keep a Changelog 1.1.0 adopted, recorded in
  `CLAUDE.md` so the convention survives session boundaries.
- Decision 0004: engine SemVer and contract version are independent, joined
  by a per-release contract-version statement. **Resolves contract §10.7.**
- GitHub as the project tracker: milestones are the phase ladder, issues
  replace the in-repo queues, `label:type:grave` is the graveyard, and PR
  descriptions carry the session-report format. `PROGRESS.md` and
  `docs/reports/` retired (archived in #1).
- CI runs on x86-64 (TSO) and AArch64 (weakly ordered) — build, vet, test,
  `-race`, and `gofmt`. Green on both from the first push.
- Two disciplines ratified into `CLAUDE.md`: *unreachability is a grave only
  when it's silent — declared and tracked, it's a TODO with an audit trail*,
  and *bucketed failures are the work plan*.
- Two disciplines ratified into `CLAUDE.md` from #28: **a stateful instrument
  measures history until its state is controlled** — *fuzzing is stateful, so a
  measurement that doesn't clear the corpus is measuring the last run*, with the
  sibling law that a fuzzer's two halves fail independently and must be certified
  independently (seed-replay by a reintroduced known defect, exploration by a
  mutation-only needle no seed can reach) — and **a design debt is discharged by a
  tripwire, never by an intention**, the declared-and-tracked ruling pointed at
  architecture instead of at a constant.
- Three more disciplines ratified from this PR's review. **A control scoped to the
  current sample inherits the current blind spot; scope controls to the space** —
  the general form of #33's widening past the eight opcodes the reader needs, and
  the overfitting law (§9 G-3) turned on the controls rather than the engine:
  *derive the domain, never enumerate it.* **A ruling retroactively falsifies prose
  written before it**, so accepting a ruling includes sweeping for the sentences it
  orphaned — *truth has a maintenance cost*, and a comment citing a tracking
  location that no longer exists is the drifted-citation defect in other clothes.
  And **second-order honesty: apply the discipline to its own output** — catching a
  figure as fiction earns nothing if its replacement carries the same
  overconfidence; n=1 cannot separate a property of an environment from an accident
  of one scheduling.
- Grave #34's lesson ratified into `CLAUDE.md` as a discipline: **a test named for a
  partition must be checked against the partition, not against its own case labels** —
  the coverage cousin of *a green that survives the bug it names*, and the failure
  mode where the pass count is right and the coverage is wrong. Its corollary is the
  mechanism, and is why the defect was invisible: **when a partition's members share
  an error value, `errors.Is` is not a partition check**, so the discriminating field
  gets asserted or every member scores as every other. The check is to print what the
  code actually returns for each case rather than read the labels.
- `internal/binary`: the `constexpr` production and the three section grammars that
  need it — global, element, and data (#25). A constant expression is not
  length-prefixed; its extent is discovered by reading instructions to the END
  opcode, which is why those sections could not be decoded until the decoder knew
  opcodes at all. **`binary.wast` 104/127 → 114/127; `binary-leb128.wast` 57/91 →
  73/91; phase 1 total 707 → 733 pass.** `section size mismatch` 5→1, `unexpected
  end of section or function` 6→3, `integer too large` 22→12 in leb128.
- A signed LEB128 reader (`sleb`/`s32`/`s64`), which is **not `uleb` with a cast**:
  it sign-extends, and its range check is *two-sided* — the out-of-width bits of the
  last byte must all match the sign rather than all be zero. That is the bulk of the
  `binary-leb128.wast` gain, since the const-expr immediates are where signed values
  first appear.
- Element-segment flags decoded as a **bitfield**, with the type-field presence rule
  (`flags&(passive|explicit) != 0`) derived from every element-segment encoding the
  suite contains rather than patched per failing vector. Two cheaper rules each fit
  all but one row; the table of six encodings and which row kills which rule is in
  the code, and all six are pinned as fixtures.
- `TestFixtureProvenance` now verifies **fragment citations** — a `<file>.wast:N`
  naming one source line inside a `(module binary ...)`, which is what a
  reader-level test needs when the unit under test is a segment grammar rather than
  a whole module. The bytes are compared against the `"\hh"` escapes on that line.
  It caught two of #25's seven fragment citations pointing several lines off the
  moment it was written. The alternative — marking them `synthetic` — would have
  declared a transcription unverifiable when a transcription is precisely the hazard
  the file exists for.
- Decision 0005: tooling gates. Quality is enforced by pinned tools wired into
  CI rather than left to habit, because a convention that depends on
  remembering decays across session boundaries.
- Decision 0006: the const-expression opcode table is **not** shared with the
  interpreter yet — `internal/binary` gets its own `constexpr` reader. Sharing from
  the start would shape #7's central structure from the decoder's requirements
  before a second consumer exists, and `internal/interp` currently holds only a
  benchmark, so "shared" would be shared with nobody. Unblocks #25.
- The accepted form of 0006 carries a **pre-registered agreement test** (#33) as part
  of #7's definition of done: when the interpreter's opcode table lands, a test
  asserts its const-legal subset and the decoder's reader agree over the *full*
  opcode space — membership, immediate extent, and rejection. The design debt 0006
  accepts is only "convertible into a failing test" if the conversion is an
  obligation with a tripwire rather than an intention, so it is filed, milestoned,
  and required to be falsified before it is trusted.
- `tools/go.mod` pins every quality tool via `tool` directives —
  `golangci-lint` v2.12.2, `govulncheck`, `deadcode`, `benchstat` — so the
  versions are repo state and a green board means the same thing on a laptop
  and in CI. The engine's own `go.mod` stays dependency-free.
- `.golangci.yml`: a curated linter set, each enable carrying its rationale,
  with gofumpt as the formatter. Never `enable-all` — lint noise is its own
  kind of dishonest board.
- Four native fuzz targets: `FuzzDecodeModule` (total behaviour — a module or a
  *declared* error, never a panic), `FuzzULEB` (width invariant at 32 and 64
  bits), `FuzzWastLexer`, and `FuzzParseNodeProgress` (a successful parse
  consumes ≥1 byte). Corpora seed from the spec suite at run time — 809 module
  images from 257 files, no transcription step.
- `TestFixtureProvenance` machine-checks the `binary.wast:N` citations in
  hand-written fixtures against the suite: 62 cited vectors verified, 7
  declared synthetic. `TestEveryFixtureFileIsChecked` guards the guard — the
  file list it reads is hand-maintained, so a new fixture file nobody registers
  would be silently unchecked; the set is now derived from disk and compared
  both ways.
- `make check` as the single local gate mirroring CI, plus `make fuzz`,
  `make bench`, `make vuln`, `make tidy`. CI gains lint, vuln, fuzz-smoke, and
  `go mod tidy` jobs; a weekly `nightly.yml` runs 10-minute fuzz per target and
  re-runs `govulncheck` against moving vulnerability data.
- `-shuffle=on` on all test runs: test order is never load-bearing.
- Two disciplines from the section-order work: **the spec is the objective
  function; the suite samples it** — the oracle answers what it is asked and does
  not define correctness, so pass count is never bought with a check that is wrong
  about inputs the suite has no vector for — and **a verdict without an identity
  check is hearsay**, which is why CI results are bound to the SHA they judge.
- Three disciplines added to `CLAUDE.md` from this work: *parsers prove progress,
  they don't assume it*, *fixtures cite the suite, and the citations are
  checked*, and *verdict channel and mechanism channel are different instruments*
  — an exit code can't tell you why, and output can't tell you whether.
- Two rulings recorded in decision 0005: the `deadcode` allowlist becomes a file
  with a reason per entry at its third entry (an unexplained allowlist entry is a
  suppression wearing a disguise), and fuzz crashers are committed — the
  never-commit rule was about provenance, and a crasher is a grave's reproducer
  this project authored.

- Decoder: **section order and uniqueness enforced**, and `ErrTrailingData` is
  reachable at last — it had been declared-and-tracked since the genesis commit.
  Order and duplicates are one predicate: section ranks must strictly increase,
  so a repeated section fails for the same reason a misordered one does. The rank
  table is deliberately **not** section-id order — the data count section is id 12
  but the grammar places it before code (id 10), so ranking by id accepts a module
  `binary.wast:1194` says is malformed. `binary.wast` **49/127 → 84/127**.
- Decoder: malformed section ids rejected. The lookup that ranks a section is the
  lookup that validates it, so ordering and id-legality are one table.
- Decoder: cross-section count agreements — function/code body counts, and the
  data count section against the data section. An absent section counts as zero,
  which is what makes "one present, one absent" fall out of the same comparison
  rather than needing its own case.

- Decoder: **section payload grammars** — the decoder stops taking a section's
  declared size on trust and descends into type, import, function, table, memory,
  export, start, data count, and custom sections. `binary.wast` **84/127 →
  104/127**; phase 1 total **136 → 179 pass**. `binary0.wast` and `custom.wast`
  reach **7/7** and **11/11**.
- The declared-size check is **one comparison with two signs**, and the spec text
  is selected by the direction of the inequality: grammar wanting more than
  declared is *unexpected end of section or function*, grammar finishing with
  declared bytes left over is *section size mismatch*. A sign error would swap the
  two messages while keeping the pass count superficially plausible, so both
  directions are pinned independently.
- Payload grammars are bounded by the **image**, not the section. Over-reading
  past a section boundary is required rather than tolerated: `binary.wast:754`
  expects *length out of bounds* because the grammar reads the next section's id
  byte as a name length, and `binary.wast:92`'s own comment documents the
  reference interpreter consuming a data section's `\0b` as an END instruction.
  The custom section is the sole exception — its tail is opaque bytes, so no later
  grammar step exists to catch an over-read.
- A `Features` config struct on `Decoder` gates per-section acceptance for
  exception handling, SIMD, threads, and memory64. The zero value is v0's
  posture: every 3.0 gate present and off (contract §9). The structural id-range
  check stays gate-blind.
- Harness: a **third verdict, `gated`**, for vectors the engine declined because a
  feature gate is off. Its own board column, checked before the substring match so
  a gate error containing the expected text cannot buy a pass, and pinned by
  `TestGatedVectors` — an enumerated allowlist with a feature named per entry, since
  a third verdict is otherwise a way to make a board look better by moving
  failures into it. `TestVerdictsPartitionCommands` holds the arithmetic: every
  command lands in exactly one verdict.
- Doctrine ratified into `CLAUDE.md`: **gates never manufacture malformedness.**
  *Malformed* belongs to the grammar of the tracked union (contract §9), so the
  tag section (id 13) is well-formed and ≥14 is malformed regardless of any gate.
  A gate-off engine must *reject* a gated construct — accept-and-ignore silently
  changes the module's semantics — but with a feature-named error, never a spoofed
  spec string. Asserted directly, because a gate that impersonated a spec string
  would score itself green for rejecting a module the spec calls well-formed, and
  no pass count can see that.
- Buckets closed by this work: *malformed limits flags* 7 → 0, *malformed import
  kind* 6 → 0, *length out of bounds* 1 → 0, and `custom.wast`'s *unexpected end*
  2 → 0, all added to `TestClosedBuckets`. Two buckets drained without closing —
  *unexpected end of section or function* 9 → 6 and *section size mismatch* 8 → 5 —
  and earn no entry; their remainder needs the code, global, and element grammars.

- **CI `conformance` job — the suite is the oracle, so CI now actually runs it.**
  Two lanes sharing one suite fetch: default features (every 3.0 gate off, v0's
  posture) for the board numbers, regression floor, closed buckets, and the gated
  allowlist; and **all tracked gates on, where the gated count must be zero**. The
  second is the structural control on the third verdict — under full features every
  vector answers on the merits, so a vector parked in `gated` on the default board
  is simultaneously being honestly *failed* in the all-on lane and stays failed
  until its feature actually works. A deferral that cannot become a disappearance.
  `make conformance` is the local mirror.
- `TestAllGatesOnLeavesNothingGated` discovers the gate set by **reflection over
  `Features`**, not from an enumerated literal: adding a fifth gate and forgetting
  to list it would leave the all-on lane running with that gate off, letting a
  vector hide in `gated` in *both* lanes — the exact failure the lane prevents. A
  non-bool field fails loudly, because "I could not turn this on" must never read
  as "it is on". Both failure modes were verified by deliberately breaking them.

- Decoder: **name validation** — a `name` must be well-formed UTF-8. Phase 1 total
  **179 → 707 pass** (57 fail, 0 unsupported, 2 gated), closing all three
  byte-string `utf8-*.wast` files at **176/176** each. The largest single bucket in
  the corpus, and the check is nine lines.
- The rule is `utf8.Valid`, which *is* the spec's side condition (`name ::=
  b*:vec(byte)` with `b* = utf8(name)`) — not a list of rejected byte patterns
  derived from the suite. The suite enumerates 176 violations per file, and a check
  written from that enumeration would pass every vector while being wrong about
  byte sequences the suite has no vector for. The stdlib predicate was measured
  against all 528 executable vectors as *evidence it is implemented correctly*,
  never as the source of the rule. Unit tests are organised by violation **class**
  — overlong forms, unpaired surrogates, past U+10FFFF, truncations, 5- and 6-byte
  sequences — with the accept direction pinned just as hard, because "reject
  everything" would score 528/528 while making the decoder reject valid modules.
- The predicate is on `name()`, not `byteVec()`: a data segment's contents are
  `vec(byte)` with no encoding constraint, so the cheap generalisation would pass
  every vector and reject modules the spec accepts. `utf8-invalid-encoding.wast`
  stays off the board — its 176 forms are `(module quote ...)` text-format modules
  phase 1 cannot execute, and they belong to #8.
- The `//nolint:unparam` on `byteVec` is **removed with its purpose fulfilled**,
  which is what a declared-and-tracked suppression is supposed to look like at the
  end. `name()` returns only an error: the bytes are consumed by the predicate, so
  the same classification question gets the opposite answer on different facts.
- `phase1Files` is one definition instead of four copies. Adding the utf8 files to
  the board list alone would have left the gated allowlist, the verdict partition,
  and the bucket-ordering property scoped to a narrower corpus than the board
  reports — three controls quietly watching less than the number beside them.
  `TestClosedBuckets` keys are pinned as a subset.

- **`internal/testenv` and a skip-forbidden CI mode** — the class behind the
  passing-by-not-running grave, closed rather than just its instance. Every skip
  license in the tree routes through one helper, each names what it licenses
  (local dev on a clone without `make spec-tests`), and `BURROUGHS_NO_SKIP=1`
  revokes them all: `requireSuite` fails instead of skipping, and the two fuzz
  seeders that silently *degraded* to literal seeds — the same shape one step
  quieter, since nothing but an `f.Log` said the corpus was missing — fail too.
  The flag is set **workflow-wide** in CI, not per job: a job added next month
  inherits strictness rather than needing someone to remember, so the `build`
  job now vendors the suite because it must.
- `TestEverySkipSiteIsLicensed` reads the AST for `Skip`/`Skipf`/`SkipNow` across
  the tree and requires an inventory entry per site, both directions. Without it
  the mechanism would have the shape it exists to forbid — a rule enforcing all
  skips route through `testenv` while nothing asserted that they do. The tree has
  exactly one skip site, which is what makes one env var able to revoke them all.
- `make strict` mirrors the CI mode locally, and the harness's own controls are
  pinned from both sides: present corpus, absent corpus, *partial* corpus (three
  files satisfy an `os.Stat` and then yield a board over three files), and the
  flag on and off. Probing the inventory control with a deliberate unlicensed skip
  caught a real defect in the strictness helper — it reported a fail *and* a skip,
  because `Fatalf`-then-`Skip` leans on `runtime.Goexit` to not return.

- **Decision 0007 (proposed) and `make spec-ref`: the reference interpreter as the
  opcode table's authority.** The table #39 needs is ~530 immediate-shape facts, and
  every suite vector bearing on it is `assert_malformed` — so a table that wrongly
  *rejects* a valid opcode is invisible on the board by construction (contract §9
  G-3). The principle is stamped and normative: **the table is machine-derived from,
  or machine-checked against, the reference; hand-trusted is not on the menu.** The
  ADR argues only the mechanism, and recommends a pure-Go extraction of
  `decode.ml`'s arms — measured as tractable (504 arms, 368 with no immediates, a
  16-reader immediate vocabulary, 4 genuinely irregular arms) and preferred because
  no OCaml toolchain exists on the dev box or is assumed on runners.
  Mechanism endorsed with **four conditions**, inherited by #39 as definition-of-done:
  the extractor is born falsified *including a vacuity control* — an extraction
  finding zero arms must fail, since a silently broken parser otherwise emits an empty
  table and a drift check comparing empty to empty agrees perfectly; the four
  irregular arms are cited or derived, being few earning no exemption from provenance;
  the committed table carries a generation header (reference SHA, extractor version);
  and CI asserts table equality against the pinned source, which is affordable
  *because* the mechanism needs no toolchain.
- `scripts/fetch-spec-ref.sh` vendors `WebAssembly/spec` **pinned by SHA**, verifying
  the pin and the presence of `decode.ml` after every path rather than trusting the
  fetch. The contrast with the unpinned suite fetch is stated at the site: the suite
  is the thing being *reported*, so its drift moves the board loudly, while the
  reference is an *input* to a generated table, where drift would arrive as a diff
  nobody ordered. Falsified on all four paths, and the missing-file probe found a
  real defect — the already-at-the-right-rev path returned early and skipped both
  assertions, the precondition excusing the check that polices it.
- `internal/binary/optable.go`: the opcode immediate-shape table, **machine-extracted
  from the reference interpreter** and committed with its provenance header (authority,
  revision, extractor version, arm count). 542 arms — 218 single-byte including the
  three prefix escapes, 31 `0xfb`, 18 `0xfc`, 275 `0xfd`. `make opcodes` regenerates,
  `make opcode-drift` asserts the committed file still agrees with the pinned source
  and refuses to run without it. Every spec vector bearing on this table is
  `assert_malformed`, so a table that wrongly *rejects* a valid opcode is invisible on
  the board by construction (contract §9 G-3); the extractor is the accept direction's
  only witness.
- The extractor errors on any arm it cannot read, never skips one, and carries the
  vacuity control 0007's condition 1 required: per-region arm floors, so an extraction
  finding nothing fails instead of producing an empty table that a drift check would
  find in perfect agreement with an empty committed table (grave #29's shape relocated
  into a code generator). Falsified per mechanism rather than per sentinel — the locate
  check and the floors share `ErrVacuous`, and two of four subtests stayed green until
  the assertion moved to the discriminating message text (#34's lesson).
- The agreement test decision 0006 pre-registered (#33), landing in the same PR as the
  table it cross-checks. Seven controls over **all 256 single-byte opcodes** and every
  prefix region, derived from the table rather than enumerated: immediate-vocabulary
  totality, the const set as a subset of the authority, a differential extent
  comparison, the full rejection partition (38 absent / 3 escape / 21 illegal / 186
  present / 8 const-legal = 256), dispatch coverage both ways, and invariance across
  all 16 tracked feature configurations. Each one falsified by inducing the defect it
  names.
- **`immBytes` is an enrolled witness**, which is the ruling that settles decision
  0006's shape once: *every copy of a fact is either an enrolled witness or a derived
  artifact* — three copies with only some checked is a drift farm. So the seam between
  the authority's vocabulary and this package's readers now testifies. Every entry
  cites the `decode.ml:N` definition it mirrors and quotes it, machine-checked against
  the vendored source (`TestImmBytesCitationsResolve` — the fixture-provenance
  mechanism pointed at a reader table; it caught two drifted citations of its author's
  on its first run). And every flat reader is measured **on its own** against a derived
  vector stating the reference rule that entails its extent
  (`TestEveryReaderAgreesWithItsAuthorityDefinition`), because composition over the
  const set reaches eight opcodes out of a nineteen-entry vocabulary. On disagreement
  the reference-derived table is the presumptive authority.

### Fixed
- Four extractor defects found by printing what the code returned rather than by
  reading it, all invisible to the suite. `i8x16_shuffle` reads `repeat 16 laneidx s`
  and extracted as **one** lane byte instead of sixteen — 15 lost bytes that would
  shift every following instruction in a body. The four structural arms all reported
  the mnemonic `end_`, because "the last `in`" is not "the last statement".
  `0xfb/0x18` reported the OCaml keyword `if`, and that arm needs *two* mnemonics
  (`br_on_cast`/`br_on_cast_fail`) selected by opcode. A multi-line alternation head
  (`decode.ml:601`) was read as an unrecognized arm — which was the extractor working
  as specified, refusing to guess.
- The three prefix escapes (`0xfb`, `0xfc`, `0xfd`) were **absent from the single-byte
  table**, so a walker could not tell "escape to a sub-table" from "no such opcode" —
  the absent-versus-rejected conflation `opInfo.illegal` exists to prevent, in a third
  flavour. Found by #33's agreement test from outside the generator, because the
  generator's own 256-byte partition test *enumerated* `{0xfb, 0xfc, 0xfd}` as a
  literal and so scored the hole as expected. Now recorded as `escape: true` and
  derived on both sides: a hardcoded exception list in a totality check is a hole with
  a comment.
- Two controls in name only, caught by falsification rather than by review. A reader
  check passed with the shuffle fix removed, because deleting the longer pattern lets
  a *shorter* one mask the same text — the check could only see readers surviving
  masking, never one whose territory had been usurped; replaced by an invariant the
  defect actually violates (a matched reader must be *called*, not passed to a
  combinator). And a test asserting the generated table is clean under the repo's
  formatter passed on deliberately mangled input: golangci-lint skips files carrying
  `Code generated ... DO NOT EDIT.`, so the gap it controlled does not exist. Deleted,
  with the measurement recorded at the site — before controlling a gap, check the gap
  exists.
- Two `immBytes` readers were wrong and **no test could reach them** (grave #47).
  `laneidx` read a raw byte; `let laneidx s = u8 s` is `uN 8`, a LEB, so the legal
  two-byte encoding `81 00` consumed one byte instead of two. `laneidx16` read a flat
  `bytes(16)`; `repeat 16 laneidx s` is sixteen LEB reads, 16..32 bytes. Both invisible
  for two compounding reasons: no lane instruction is const-legal, so the extent
  differential never executed either entry, and "a lane index is 0..15, so it is a
  byte" is true about the value and false about the encoding. The general form is
  *a control that only exercises a fact in composition covers the compositions, not the
  fact* — scope controls to the space the **map** spans, not the one its current callers
  reach. Neither is suite-visible: a non-canonical-but-legal LEB is well-formed, which
  is the accept direction 0007 exists to cover.
- Two drifted citations in the same map, found by the citation check on its first run
  (`blocktype` 230→334, `instr_block'` 612→967) — hand-written line numbers, exactly
  the defect `TestFixtureProvenance` was built for, in a new place.
- Decision 0007's stale figures wear a pointer at their point of reading, not just a
  correction three sections away (ruling: Scott, PR #43). *Records are append-corrected;
  stale claims wear a pointer* — the body is preserved per the 0003 precedent, and the
  `counted (not estimated)` heading now forward-references the section that falsifies
  it. The Correction's own single-byte figure was stale too (215, written before the
  escape rows landed in the same PR) and is now 218.
- CI's `conformance` job vendors the reference **before** the board step, not after it.
  The reference-vendoring steps sat below the board on the reasoning that only
  `make opcode-drift` reads decode.ml — then `internal/binary` grew a test that reads it
  too, and the board step failed under `BURROUGHS_NO_SKIP=1` on a corpus the same job
  fetches nine lines later. Reproduced by hiding `third_party/`, not guessed. The
  corollary to the lesson below: a job's corpora are its **preconditions**, satisfied
  before the first step that runs tests rather than next to the step whose name mentions
  them — which package needs which corpus is not a fact a workflow file can track.
- CI's `build` job vendors the reference interpreter too, not only the suite. It runs
  `go test ./...`, which reaches the extractor's tests, which call `RequireSpecRef` —
  and under the workflow-wide `BURROUGHS_NO_SKIP=1` that is a **fail**, not a skip.
  Caught on PR #43's first CI run by the strictness policy doing exactly its job: the
  drift check had been placed in the `conformance` job while the corpus requirement it
  introduced was inherited tree-wide. The general shape, now stated at the site: *a job
  running `go test ./...` inherits every corpus requirement in the tree*, so it must
  vendor all of them rather than the one it was thinking about. Both presence guards
  now run in that job as well, because a truncated fetch passes the Go-level door and
  is a different failure from a missing one.
- Decision 0007's "counted (not estimated)" figures were wrong and are corrected in an
  appended section, body preserved: 201/29/18/256 (504) counted arm *lines*, and
  assumed the SIMD sub-opcode was a byte where the reference runs to `0x113`. The
  reader histogram was a whole-file grep, so it counted occurrences outside the `instr`
  function, and `grep 'idx s'` silently matched the tail of `laneidx s`. Each figure
  had been checked for plausibility rather than against a second method — and the
  extractor, which is that second method, disagreed on its first successful run.
- `binary.wast:112` is settled by asking the authority instead of reasoning about
  it: `decode.ml`'s `sized` runs a section's payload grammar **unbounded** and
  reconciles the declared extent afterwards, which is Burroughs' existing doctrine
  exactly — so the doctrine and the vector never conflicted. `0x0a` is `throw_ref`,
  decoded as a real instruction, and reading continues into the next section until
  EOS yields "unexpected end of section or function". The reference also shows
  `const s` is the *full* instruction grammar with const-ness left to validation,
  which is why that vector currently fails with `ErrNonConstantExpr` — an honest
  fail, not `ErrFeatureDisabled`, so it cannot hide in `gated`.
- `constexpr.go` said `constant expression required` appears 22 times in the suite;
  it appears **24** (global 7, elem 7, data 6, array 2, func_ptrs 2). The
  load-bearing half of the claim was re-checked and holds — 0 occurrences under
  `assert_malformed`, and both cited lines resolve as described.

### Added
- **`derived` accepted as the third provenance category** — cited, derived,
  synthetic. A derived fixture is one the suite *implies* but does not contain:
  `TestLEBWidthIsPerField`'s accept half asserts a wide-but-legal limits minimum
  decodes, which `binary-leb128.wast` cannot state because it only asserts
  malformedness, and which `:529` and `:221` jointly **bracket** — ten bytes wants
  *integer too large*, eleven wants *integer representation too long*, so the only
  width satisfying both is 64. *Entailment from checked facts is legitimate
  provenance; unstated entailment is just synthetic with better manners.* So the
  category carries obligations and `TestDerivedFixturesStateResolvablePremises`
  enforces the half a machine can: a derived row states its premises
  (`derived from <file>.wast:N,M`) and every premise must **resolve** to a suite
  line carrying content. The inference is reviewed by eyes; a premise pointing at
  prose is caught by the same mechanism that catches a drifted transcription.
  Falsified four ways before being trusted — premise pointing at prose, premise
  past end of file, a `derived` marker with no premises at all (the laundering
  channel), and the category going empty, which fails rather than passing
  vacuously. (Ruling: Scott, PR #37.)

### Changed
- **Decision 0003 amended**: its LEB taxonomy prescribed the *wrong test order*,
  and the implementation followed its documentation faithfully — so every reviewer
  who checked the code against its claims found agreement. Appended, not edited: the
  body stands as the record of what was believed and of why it survived review. The
  authority for order-of-tests questions is the reference interpreter's `decode.ml`,
  not a derivation from vectors that cannot distinguish the orderings. Also corrects
  the ADR's `\ff\ff\ff\ff\ff\7f` witness, which is listed under the continuation-bit
  bullet while being sourced from a *signed* field.
- **LEB widths are per field, not one width for the whole decoder.** Limits
  minimum and maximum are read at **64 bits**; indices and counts stay 32. The
  suite brackets it from both sides: `binary-leb128.wast:525`'s ten-byte memory
  minimum wants *integer too large* (ten bytes is legal width for a u64, so the
  fault is the unused payload bits) while `:217`'s eleven-byte field wants
  *integer representation too long*. A u32 read scores the first as "too long"
  and gets the string wrong. The consequence is deliberate: a memory32 minimum
  above 2^32 now **decodes** and is the validator's to reject, which is the
  correct layering — reading the field narrowly to catch it in the decoder would
  be borrowing the validator's job and getting the malformed string wrong to do
  it. Pinned by a bidirectional control the suite supplies for free: the same
  five bytes `80 80 80 80 10` are malformed as a data-segment memory index
  (`:565`) and legal as a limits minimum, so one width being wrong fails the two
  halves in opposite directions.
- **The functype form tag is an `s7`, not a byte.** `0x60` *is* −32 read at
  width 7 — the spec's type constructors live in negative s7 space, as `0x5e`
  (array) is −34 — and `binary-leb128.wast:1067` is the vector that settles it:
  `\e0\7f`, annotated by the suite itself as "−0x20 in signed LEB128 encoding",
  must fail as *integer representation too long* rather than *malformed function
  type*. This is the inverse of the limits-flags rule, where the field really is
  a byte and a redundant encoding of a legal value is malformed limits.
- `reader.u64` has a production caller and its `//nolint:unused` is gone, which
  empties the `deadcode` allowlist to zero entries. This is the placeholder
  discipline's intended ending — a deferral retired by a caller, not by a
  suppression outliving its reason.
  ([#19](https://github.com/scttfrdmn/burroughs/issues/19))

### Fixed
- **Two correct predicates composed in the wrong order — `uleb` and `sleb`
  tested the continuation bit before the range.** The reference interpreter's
  `uN`/`sN` (`interpreter/binary/decode.ml`) check the unused-bits range
  *before* consulting the continuation bit, and **order of tests is itself a
  claim about the spec**: on the last permitted byte, bytes that are both
  over-wide and continued must be reported *too large*, not *too long*.
  Neither predicate was defective; only their composition. Found by a
  **differential port of the reference's own `uN`/`sN`**, exhaustive over the
  derived disagreement space (k all-continuation prefix bytes × all 256 final
  bytes, k from 0 past the width budget): **112 disagreements at 32 bits, 126 at
  64**, identically for both readers — one structural defect with two tenants.
  Now 0 disagreements over 4096 verdicts at 32 bits and 6656 at 64, with a
  vacuity control asserting the ported oracle actually produces all three
  verdicts over that space. Each half was falsified on its own by reverting it
  and recovering exactly its 112/126.
  ([#36](https://github.com/scttfrdmn/burroughs/issues/36))
- **A taxonomy vector was asserted against the wrong reader.**
  `TestLEBTaxonomy`'s `ff ff ff ff ff 7f` row carried the suite's expectation
  from `binary-leb128.wast:497` — an `i32.const` immediate, a *signed* field —
  and read it with the *unsigned* reader. Both verdicts are correct and they
  differ: `sN(32)` says *too long* (a legal sign extension one byte past the
  budget), `uN(32)` says *too large* (the fifth byte's payload exceeds the
  width). The signed vector moved to `TestSlebIsNotUlebWithACast` and the
  unsigned reading of the same bytes is now pinned where it was, so the pair is
  asserted from both sides. `TestLEBTaxonomy` stayed **green throughout** the
  ordering defect above, because every row it held asked about inputs where the
  two conditions do not overlap — and the overlap region *is* the bug. Grave
  0003's width-parameterization lesson, applied to signedness.
  ([#36](https://github.com/scttfrdmn/burroughs/issues/36))
- **`ErrMalformedFuncType`'s message invented bits the input never had.** The
  byte reconstruction or'd a high bit in for every negative form, reporting a
  `0x5e` array tag as `0xde`. Nothing in the suite can see it — that vector's
  expected string is the bare sentinel, and the harness reads exactly as far as the
  expected string does — and it was found by *printing what the expression returns
  for nine tags* rather than reading its shape. Lesson: **an error message is
  testimony, and fabricated evidence is a lying witness even when the verdict is
  right.** Where the oracle stops short, everything past that point is ours alone to
  keep honest — and per #38 it does *not* always stop at the sentinel: a spec string
  such as `illegal opcode ff` embeds the byte, making the rendering oracle-covered
  for exactly those vectors.
  ([#36](https://github.com/scttfrdmn/burroughs/issues/36))
- **CI's `deadcode` allowlist still filtered `reader.u64`** while the Makefile's
  comment already claimed the allowlist was empty — one truth, two authorities,
  disagreeing. Found by the ruling-falsifies-prose sweep. A gate and its local
  mirror disagreeing is each one's reason to exist, and a suppression outliving its
  subject licenses the next regression silently.
- **`TestSectionSizeBothSigns` was named for both signs and pinned one of them
  twice.** Its first case was labelled "grammar consumed MORE than declared" while
  its own prose said "3 bytes are left over", and the decoder reported `declared 7,
  grammar consumed 4` — the *short* sign. Its second case is face 1, a different
  mechanism. So the grammar-long direction, the only reason the test exists, had no
  assertion at all, and the `t.Log` deferral on its third case hid that a *sign* was
  missing rather than just one vector. Both signs now assert on the error **message**
  (`errors.Is` cannot tell them apart — they are the same error value), and a
  synthetic grammar-long vector covers the direction the suite has no vector for.
  Falsified by swapping the two operands in the message, which now fails three
  assertions instead of none. Lesson: **a test named for a partition must be checked
  against the partition, not against its own case labels** — the coverage cousin of
  *a green that survives the bug it names.* Found while discharging the declared gap
  #25 left in that test. ([#34](https://github.com/scttfrdmn/burroughs/issues/34))
- **`fuzz-smoke` was budgeted in the wrong unit** (#28). The job exists to catch a
  target that stopped building or a corpus that regressed — its purpose names
  *executions* — but its budget was wall clock on a shared runner, making it
  timing-sensitive by construction. It failed twice that way, on PR #27 and again
  on PR #31, both times `context deadline exceeded` with no crasher and after real
  progress: the second at ~70k execs/sec against 130k–670k/sec measured locally, a
  ~7x spread that is a property of the runner and not of the code. Now
  `-fuzztime Nx`, sized from the measured CI floor rather than converted from a dev
  box's rate. Cost, measured on the first green run rather than estimated: the
  `fuzz wast lexer` step went 65s nominal → **3m08s–3m26s**, because the runner's real
  floor is ~17–18k execs/sec, not the ~70k the sizing assumed. Measured across two
  independent green runs, which is the point: 46–47 three-second windows reporting
  `0/sec` against recovery bursts of 605k–**1.25M**/sec — long stalls, not a slow
  steady rate, and a peak that doubles between runs is why no single figure was
  trustworthy. Accepted: a job that takes three minutes and answers a fixed question
  beats one that takes one and sometimes answers none. The stalls get no issue by
  ruling — an issue no work can close is a wish with a label — so the finding lives in
  the budget rationale, where it has consequences.
  `make fuzz` and the nightly 10-minute runs stay wall-clock *because
  their purposes are durations* — the units differ because the purposes do, and both
  sites now say so. Budget by the quantity the purpose names.
- **CI board tests had been passing by not running.** The `build` job never
  vendored the spec suite, and `requireSuite` skips when `testdata/spec` is absent
  — so the pass floor, the closed buckets, the fixture-citation checks, and the
  gated allowlist all skipped on every green CI run in the project's history. No CI
  green had ever asserted a suite count. The `conformance` job vendors the suite and
  **asserts ≥250 `.wast` files are present before trusting any number out of it**:
  a skip is not a verdict, and a job that passes by asking nothing is the
  dishonest-board failure wearing CI's clothes.
- `parseString` returned a nil slice for the empty literal `""`, entangling
  "is a string" with "has bytes" — so a reader checking `str != nil` would
  misread `(module binary "")`, the empty image, which is the *unexpected end*
  boundary and the most-exercised vector in `binary.wast`. Emptiness is a
  length, never a nil. Found by `FuzzWastLexer` on its first run.
- Two hand-typed decoder fixtures had drifted from the suite lines they claimed
  to copy: the UTF-8 BOM vector was truncated from 11 bytes to 8, and an
  `"asm\00"` vector was a mutation of nothing in the suite — reintroducing grave
  #2's own short-preamble-versus-wrong-magic distinction inside the test that
  pins it. All citations now machine-checked, and the coverage widened to every
  preamble vector in `binary.wast:5–45`.
- The s-expression reader could not traverse `annotations.wast`: a bare `;`
  inside a custom annotation form is a delimiter, so the atom loop consumed
  zero bytes and errored on its own delimiter. 256/257 files parsed before,
  257/257 after. Regression vector copied verbatim from `annotations.wast:14`.
- CI used deprecated action versions (Node 20) and requested a Go module
  cache with no `go.sum` to key it on.
- `binary_leb128_64.wast` had been scoring 1/2, and that pass was never earned:
  both its vectors carry i64 memory limits flags, and the decoder was reading the
  limits flags field without interpreting it, so it accepted a memory64 module
  with the memory64 gate off. Honest scoring moves both vectors to `gated` and the
  file to 0/0 — a board line that reads worse and means more. *An unearned pass is
  a regression waiting to be misread*: the fix looks like a regression, which is
  precisely why the third verdict had to exist before the grammar landed.
- Limits flags and section ids are read as **single bytes, not LEBs**. Reading
  either field with `u32` is the helpful mistake: `\81\00` genuinely does encode 1,
  so a LEB read accepts all three of `binary.wast:632`, `:677`, and `:686`, whose
  redundant encoding *is* the malformedness.
- `TestMalformedSectionID` asserted that a tag section must be *accepted* with the
  EH gate off — wrong in the accept-and-ignore direction, and written before the
  gate doctrine was ruled. It now asserts only that the id is not reported as
  malformed; both gate states are covered by
  `TestTagSectionIsWellFormedButGated`.

---

*The entries below land with the instruction and function-body grammars
([#43](https://github.com/scttfrdmn/burroughs/issues/43),
[#39](https://github.com/scttfrdmn/burroughs/issues/39)). `[Unreleased]` now carries
duplicate `Added`/`Fixed` headings from successive PRs appending to it; consolidating them
is a formatting pass of its own, not a drive-by inside a decoder PR.*

### Added

- **The instruction grammar, table-driven — `binary.wast` is 127/127 and the phase-1
  corpus is 764 pass / 0 fail / 0 unsupported / 2 gated.** All 26 previously-failing
  vectors drained. `decodeConstExpr`'s eight-entry accept set **dissolved**: the
  generated `opTable` answers existence, illegality, escape, and immediate shape over
  the whole opcode space, leaving `constOps` — seven bytes carrying the const-legality
  predicate the reference does not encode — as the only opcode fact this engine states
  on its own authority.
- Function-body grammar: `locals` (per-group count a u32 LEB, the *sum* checked at 64
  bits, so `integer too large` and `too many locals` are different fields), `memop`
  (flags bound checked *after* the LEB read, a u64 offset), `catch` clauses, blocktypes,
  and `sized` per body.
- **Gate flips closed as buckets, with their base counts:** `illegal opcode ff` (1),
  `illegal opcode` (1), `data count section required` (2 — this is
  [#22](https://github.com/scttfrdmn/burroughs/issues/22), closed inside #39 from
  `syntax/free.ml`'s four data-referencing opcodes rather than from a byte scan),
  `too many locals` (2), `END opcode expected` (1), `unexpected end of section or
  function` (3), `section size mismatch` (1), `integer too large` (2).
- `binary.wast:345` and `:1218` fell out **on contact** with the table, before any
  body-grammar work — 0007's postscript predicted them as a milestone and they were a
  lookup. Recorded because the mis-estimate is the transferable part.
- **The nine-defect falsification pass.** Each mechanism was broken on purpose and the
  board re-read, because *a green that survives the bug it names is a control in name
  only* and a 26-vector drain is the shape that most deserves the suspicion. Six defects
  refilled exactly the buckets they claim. **Three survived the entire suite** and are
  now `internal/binary/instr_probe_test.go`: per-body extent distinguishability, lane
  index width at the production reader, and the blocktype alternation's branch order.
  Each control was itself falsified before being committed.

### Fixed

- **`decodeConstExpr` defers the const verdict rather than aborting on it.**
  `binary.wast:112` is the vector that forces it: a global initialiser ending `\41\00`
  with no END, followed by the code section's id byte `\0a` — which *is* `throw_ref`.
  An aborting reader reports `constant expression required`; the reference reads on and
  the expression runs off the image, so the answer is `unexpected end of section or
  function`. *An invalid verdict that pre-empts a malformed one is reporting the wrong
  layer's answer.* `ErrNonConstantExpr` is gone; `ErrConstExprRequired` is recorded and
  released only if the grammar completed.
- **Grave [#47](https://github.com/scttfrdmn/burroughs/issues/47) reached a second
  site.** The same raw-byte lane-index read, in `instr.go`'s production `imm` switch
  rather than the test's `immBytes`, survives the whole corpus for the same reason
  (`\fd` appears in no phase-1 vector). *A grave whose lesson was applied to one copy
  of a fact and not the other is half-buried.*
- The changelog's own `binary.wast:112` entry above, which described the vector as
  "currently fail[ing] with `ErrNonConstantExpr`". That sentinel no longer exists and
  the vector passes — the ruling-falsifies-prose sweep applied to this file. ADRs 0006
  and 0007 got the same sweep, by append with bodies preserved.

- **`TestEveryFuzzTargetIsGated`** — the AST-reading sibling of
  `TestEverySkipSiteIsLicensed`, and it was written because it had a live subject.
  `FuzzConstExprProgress` landed with the instruction grammar carrying eleven seeds and a
  fourteen-sentinel allowed-error list, and was budgeted in **neither** the Makefile nor
  either workflow — so it ran only as an ordinary seed-corpus test and its *exploration*
  half had never once executed. Three enumerations of the target set (`Makefile`,
  `ci.yml`, `nightly.yml`) with no control over any: *derive the domain, never enumerate
  it*, broken three times in the same tree. The control now derives the set from
  `func FuzzX(f *testing.F)` declarations, requires each to appear at all three run
  sites, checks both directions, and carries a size floor so an empty walk cannot agree
  with an empty inventory. Newly gated at 1.5M execs, it immediately found **129 new
  interesting inputs** — the measure of what "defined but never budgeted" costs. *A
  fuzzer has two halves that fail independently; a target nothing runs under a budget is
  a file, not equipment.*

### Changed

- **`Features.ExceptionHandling` and `Features.SIMD` doc comments now say what the gates
  do *not* yet cover.** Writing out an opcode scope for them is what found
  [#48](https://github.com/scttfrdmn/burroughs/issues/48): the table-driven dispatch
  consults `Features` nowhere, so with every gate off the decoder **accepts**
  `throw_ref`, `try_table`, `v128.const`, and `ref.eq` in a function body — the
  accept-and-ignore half of the gate ruling, unnoticed because every prior gate
  discussion was about not over-*rejecting*. The comment I first wrote asserted check
  sites that do not exist: grave #36's fabricated-evidence shape, moved from a format
  string into a comment, where nothing reads it. *Writing down what a flag governs is a
  check on whether it governs it.*
- `decodeBlockType`'s comment gave the wrong reason for its branch order. `either`
  backtracks, so the order affects neither the accept set nor any extent — measured over
  all 256 first bytes in both orders, 427 of 768 rows differ and **every** difference is
  the error message alone. What the order decides is which branch's error survives, and
  that is load-bearing in exactly one place: the gated branch must be last, or the
  alternation overwrites `ErrFeatureDisabled` with `malformed value type` — a gate
  manufacturing malformedness. The control keeps the wrong reason beside the right one.

## [0.0.1] - 2026-07-30

*Implements contract v0.1. Scaffold state, recorded retroactively at the
genesis commit — no spec-suite test had been run at this point, and no
conformance claim is made for this version.*

### Added
- Host contract v0.1 (`docs/burroughs-contract-v0.1.md`), normative and
  written before any engine code: 1:1 OS threads with no main-thread special
  case, engine-native safepoints, a sequentially-consistent memory model at
  every host boundary, growable continuation stacks, and a netpoller-shaped
  WASI 0.3 event loop.
- `internal/binary`: module preamble check and section-level scan. Payloads
  alias the input buffer — no copying. LEB128 reader is width-parameterized
  (`uleb(bits)`), with `u32` and `u64` on top.
- Decoder error contract: error text tracks the upstream spec suite's
  `assert_malformed` strings verbatim, matched by substring (decision 0003).
- `cmd/burroughs`: `version` and `inspect` subcommands.
- `internal/interp/dispatchbench`: four interpreter strategies measured over
  the same program with correctness controls — the evidence behind decision
  0002. Records closure compilation as a **negative result with a
  reproducer** (no faster than plain in-place, and it allocates: the frame
  escapes to the heap).
- Decision records 0001 (genesis, Apache 2.0), 0002 (interpreter strategy:
  internal-form rewrite, giant-switch dispatch, `uint64` slots beside a
  parallel reference array), 0003 (staged pure-Go `.wast` harness and the
  error contract) — all accepted.
- `make spec-tests` vendors the upstream WebAssembly test suite (257 `.wast`
  files, gitignored — never committed).
- Apache 2.0 license, © 2026 Scott Friedman.

### Fixed
- Malformed-integer taxonomy in the LEB128 decoder conflated the suite's two
  distinct verdicts: a continuation bit set on the last permitted byte is
  *integer representation too long*, while unused high bits set is *integer
  too large*. The rule is width-parameterized — `ff ff ff ff 0f` is a valid
  `u32` and a malformed `i32` constant — so a `u32`-only reader could not
  express it. Grave (#2); regression vectors lifted from
  `binary-leb128.wast`.
- `ErrLEBTooLong` was unreachable for every input: an early return preempted
  the loop's fall-through, verified exhaustively over all 256 fifth-byte
  values. This is why the taxonomy bug went unnoticed — the error that should
  have named the continuation-bit case could never fire. Grave (#3); lesson
  marked at the fix site: *an error constant with no reachable path is a
  missing check wearing a disguise.*
- A truncated module preamble reported a bad-magic error where the spec says
  *unexpected end*; short and full-width-but-wrong preambles are now
  distinguished, pinned by tests drawn from `binary.wast`.
- Section-size overrun reported the wrong spec string. The suite calls this
  *length out of bounds*; *unexpected content after last section* is a
  different condition (duplicate or misordered sections), now named as such
  and tracked as not-yet-enforced.

[Unreleased]: https://github.com/scttfrdmn/burroughs/compare/v0.0.1...HEAD
[0.0.1]: https://github.com/scttfrdmn/burroughs/releases/tag/v0.0.1
