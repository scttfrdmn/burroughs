# Changelog

All notable changes to Burroughs are documented here.

The format is [Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning 2.0.0](https://semver.org/spec/v2.0.0.html).

While the major version is `0` the public API is unstable by definition; the
phase ladder (see `CLAUDE.md`) maps to minor versions.

## [Unreleased]

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
- Decision records 0001 (genesis, Apache 2.0), 0002 (interpreter strategy),
  0003 (spec harness and error contract), all accepted.
- CI on x86-64 (TSO) and AArch64 (weakly ordered) from the first push:
  build, vet, test, `-race`, and `gofmt`.
- `make spec-tests` vendors the upstream WebAssembly test suite (257 `.wast`
  files, gitignored — never committed).

### Fixed
- Malformed-integer taxonomy in the LEB128 decoder conflated the suite's two
  distinct verdicts: a continuation bit set on the last permitted byte is
  *integer representation too long*, while unused high bits set is *integer
  too large*. The rule is width-parameterized — `ff ff ff ff 0f` is a valid
  `u32` and a malformed `i32` constant — so a `u32`-only reader could not
  express it. Grave; regression tests use vectors lifted from
  `binary-leb128.wast`.
- `ErrLEBTooLong` was unreachable for every input: an early return preempted
  the loop's fall-through, verified exhaustively over all 256 fifth-byte
  values. This is why the taxonomy bug went unnoticed — the error that should
  have named the continuation-bit case could never fire. Grave; lesson marked
  at the fix site: *an error constant with no reachable path is a missing
  check wearing a disguise.*
- A truncated module preamble reported a bad-magic error where the spec says
  *unexpected end*; short and full-width-but-wrong preambles are now
  distinguished, pinned by tests drawn from `binary.wast`.
- Section-size overrun reported the wrong spec string. The suite calls this
  *length out of bounds*; *unexpected content after last section* is a
  different condition (duplicate or misordered sections), now named as such
  and tracked as not-yet-enforced.

[Unreleased]: https://github.com/scttfrdmn/burroughs/commits/main
