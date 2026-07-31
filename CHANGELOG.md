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
- Decision 0005: tooling gates. Quality is enforced by pinned tools wired into
  CI rather than left to habit, because a convention that depends on
  remembering decays across session boundaries.
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
  hand-written fixtures against the suite: 19 cited vectors verified, 2
  declared synthetic.
- `make check` as the single local gate mirroring CI, plus `make fuzz`,
  `make bench`, `make vuln`, `make tidy`. CI gains lint, vuln, fuzz-smoke, and
  `go mod tidy` jobs; a weekly `nightly.yml` runs 10-minute fuzz per target and
  re-runs `govulncheck` against moving vulnerability data.
- `-shuffle=on` on all test runs: test order is never load-bearing.
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

### Fixed
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
