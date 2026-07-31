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

### Fixed
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
