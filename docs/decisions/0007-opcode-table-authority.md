# 0007 — The opcode table's authority, and how agreement with it is checked

Date: 2026-07-31 · Status: **proposed** (principle stamped by Scott on #38; mechanism open)

## Decision

Two parts, and only the second is open.

**The principle (stamped, normative).** The opcode table is **machine-derived from,
or machine-checked against, the reference interpreter. Hand-trusted is not on the
menu.** (Ruling: chat-Claude on #38, recommendation; stamped by Scott, 2026-07-31.)

**The mechanism (this ADR's question).** Which of the options below discharges that
principle, given that the reference is OCaml and this project has no OCaml.

## Question

#39 needs the immediate shape of every opcode in the tracked union — roughly 530
facts of the form "byte `0x28` takes a memarg", "byte `0x0e` takes a vec of indices
then an index". The decoder cannot find the *end* of an instruction without them,
and a wrong immediate width does not fail loudly: it shifts every subsequent byte,
so the error surfaces elsewhere as a size mismatch or a bogus opcode (0006's
"why the extent, not just the opcodes").

The authority question was escalated rather than answered in code because of an
asymmetry, not a difficulty. Contract §9 G-3: *the spec is the objective function;
the suite samples it.* Every vector bearing on this table is `assert_malformed` —
so a table that wrongly **rejects** a valid opcode is invisible on the board by
construction. The suite cannot falsify the accept direction. This is the first gap
in the project that requires the objective's *other representation* rather than a
bigger sample.

The third argument is local and measured: hand-transcription's error rate in this
repo is not hypothetical. #37 found **seven wrong citations in twelve hand-written
items**, by a careful author, caught within the hour. 530 facts hand-carried with no
machine check is a defect farm with a delivery date.

## Measured facts

All measured on 2026-07-31 against `WebAssembly/spec` at `bdd7164`.

### The reference is the same authority as the suite, not a new one

`WebAssembly/spec` is the repository the vendored suite's expected strings were
*minted by*: `interpreter/binary/decode.ml` is where `"integer too large"` and
`"unexpected end of section or function"` are string literals. The vendored suite is
a different repo (`WebAssembly/testsuite`, currently `de54fd2`) and contains **no**
`interpreter/` — so the reference is a separate fetch, not a subdirectory of
something already present.

### Correction to a premise of the ruling: the suite is not SHA-pinned

The recommendation said the reference would be "gitignored and SHA-pinned
identically" to the suite. Checked: **the suite is not pinned at all.**
`scripts/fetch-spec-tests.sh` does `git clone --depth 1` with no revision, and
`git pull --ff-only` on an existing checkout — a floating tip. So "pinned
identically" would mean "not pinned", and the sentence describes a property the repo
does not have.

This matters more for the reference than for the suite, and the asymmetry is the
reason: a suite that drifts changes *the board*, loudly and visibly — counts move and
CI says so. A reference that drifts changes *a generated table*, and if the table is
regenerated on a drifted reference the change arrives as a diff nobody ordered. The
suite gets away with being unpinned because it is the thing being reported; the
reference cannot, because it is an input to something reported.

Whichever option is chosen, it pins its reference by SHA. Whether to retrofit
pinning onto the suite fetch is a separate question and gets its own issue — noted
here rather than fixed in passing, since it is not this ADR's subject.

### There is no OCaml toolchain, and assuming one is a real cost

    ocaml ocamlfind ocamlbuild dune opam ocamlc  →  all ABSENT

The dev box has none of it. Building the reference means adding `opam` +
a switch + `dune build` to the dev-and-CI setup for a project whose engine `go.mod`
is deliberately dependency-free and whose only build input today is Go. That is not
disqualifying, but it is the cost any "run the reference" mechanism pays, and it is
paid on every runner, every job, forever.

### The table's shape, counted (not estimated)

`decode.ml`, 38042 bytes. The `instr` function:

| region | arms |
|---|---|
| single-byte opcodes | 201 |
| `0xfb` prefix (GC) | 29 |
| `0xfc` prefix (misc: trunc_sat, bulk memory) | 18 |
| `0xfd` prefix (SIMD) | 256 |
| **total** | **504** |

Of these, **368 arms have no immediates at all** — the RHS is a bare mnemonic. The
immediate vocabulary across all arms is 16 readers, and it is heavily skewed:

    78  at idx s        46  memop s       24  laneidx s      13  byte s
    12  u32 s            9  idx s          8  instr_block s   8  end_ s
     5  blocktype s      2  vec valtype s  2  s64/s32/f64/f32/local s (2 each)
     1  vec (at idx) s

24 arms wrap across lines; 20 of those are pure line-wrapping of
`memop`+`laneidx` pairs. The genuinely structural arms are four —
`0x02` block, `0x03` loop, `0x04` if/else, `0x1f` try_table — each recursing through
`instr_block` and `end_`, and each needing a hand-written reader regardless of how
the table is obtained.

So the table is regular. **The immediate-shape facts are extractable from the text of
`decode.ml` without an OCaml compiler**, and the irregular remainder is small enough
to enumerate.

### Upstream publishes nothing machine-readable

Checked for a generated table, JSON, or an opcode appendix in machine form:
`w3c.json` is repo metadata, `document/` is reStructuredText/Bikeshed prose, and
`interpreter/syntax/mnemonics.ml` is smart constructors (`let unreachable =
Unreachable`) with no opcode bytes in it. There is no upstream artifact to consume;
`decode.ml` is the only place the byte↔shape mapping exists.

## Options

### A — Build the reference; generate the table from its behaviour

Add opam/dune to dev and CI, build `wasm`, and derive the table by *executing* the
reference — feed it synthesized one-instruction modules and observe accept/reject.

**For:** derives from behaviour rather than from source text, so it cannot
misread the source. Highest fidelity available.
**Against:** an OCaml toolchain on every runner, forever, for a table that changes
when a proposal lands (rarely). Synthesizing a valid module per opcode is itself a
body of work needing the very knowledge being derived — a module wrapping
`memory.grow` must already know it takes a memarg to build the surrounding bytes.
Circular at the margin.

### B — Extract the table from `decode.ml`'s text, in pure Go; commit the output; drift-check it

A generator under `internal/binary/internal/opcodegen` (or `tools/`) parses the
`| 0xNN ->` arms and their RHS immediate calls, emits a Go table, and the table is
**committed**. A test re-runs the extraction against the vendored reference and fails
on any disagreement with the committed table.

**For:** no OCaml anywhere. The measured regularity (504 arms, 368 immediate-free, a
16-reader vocabulary) says the extraction is a small, testable pure-Go program. The
committed output means a fresh clone builds with no fetch, and the drift check means
the reference stays the authority rather than becoming a historical influence. Same
shape as every other control here: a human wrote it, a machine checks it.
**Against:** parses source text, so it can misread the source — and it reads a file
upstream has no obligation to keep parseable. Mitigated by the extractor being
**required to fail loudly on any arm it does not recognize**, never to skip one: an
unrecognized arm is an error, not an omission. That inverts the failure mode from
silent undercoverage to a loud build break, which is the whole difference between
this and hand-transcription.

### C — CI differential lane against a built reference

Table written by hand; a CI lane builds the reference and compares verdicts on
generated inputs.

**For:** checks behaviour, and the check is continuous.
**Against:** pays A's toolchain cost *and* keeps a hand-written table as the primary
artifact. The check runs late (in CI, on a lane) rather than at authorship, so the
first 530 facts are still hand-carried and the machine only objects afterwards. It
satisfies the letter of "machine-checked" and misses the point of it.

## Recommendation

**B.** The principle demands a machine between the reference and the table; B is the
only option that puts one there without also putting an OCaml toolchain in the build.
The measured shape is what makes it credible rather than optimistic: 368 of 504 arms
carry no immediates, the remaining vocabulary is 16 readers, and the four irregular
arms are hand-written under either option.

B's weakness is real and worth stating plainly: it reads source text, and text can be
misread. Two things bound it. First, the extractor errors on anything it does not
recognize — coverage is asserted, not assumed, so an upstream refactor breaks the
build instead of quietly shrinking the table. Second, the drift check makes the
reference a *live* authority rather than a one-time influence, which is exactly the
property hand-transcription lacks.

## Consequences

- `third_party/spec` becomes a **pinned** fetch (`scripts/fetch-spec-ref.sh` +
  `make spec-ref`), SHA recorded in the script, gitignored like the suite.
- The generated table is committed. `make check` fails when the extraction disagrees
  with it — so regenerating is a deliberate act with a reviewable diff.
- The extractor's unrecognized-arm error is load-bearing and gets its own test:
  feed it a synthetic arm shape it should reject, and confirm it does. Per CLAUDE.md
  — *a control's green must be falsifiable, and the way to know is to break it.*
- Gated opcodes stay gated. The table says what shape a byte has; `Features` says
  whether it is accepted. A gate-off engine meeting a GC or SIMD opcode still
  reports a feature-named error, never a spoofed spec string.
- #33's agreement test lands in the same PR as the table, per 0006.
- **Not fixed here:** the suite fetch's own lack of pinning. Separate issue.

## Postscript: what the authority already settled

Two questions were resolved by *asking* the reference while gathering these facts,
which is the principle working before it was even written down.

**`binary.wast:112` — the doctrine never conflicted with the vector.** The open
worry was that Burroughs' "payload grammar is not bounded by the section" doctrine
disagreed with a vector expecting `unexpected end of section or function`.
`decode.ml:138`'s `sized` runs the payload grammar **unbounded** and reconciles the
declared extent *afterwards* — which is Burroughs' doctrine exactly. For `:112`,
`instr_block'` stops only at `peek ∈ {None, 0x05, 0x0b}`, so `0x0a` (`throw_ref`,
confirmed at `decode.ml:388`) is decoded as a real instruction, reading continues
into the following code section, and EOS reaches `guard` → `"unexpected end of
section or function"`. The third possibility chat-Claude named is the actual one:
there was never a conflict.

**A layering finding that came free.** The reference's `const s` is
`instr_block s; end_ s` (`decode.ml:983`) — the *full* instruction grammar, with
const-ness checked nowhere in the decoder. Burroughs' `decodeConstExpr` restricts
opcodes at decode time and returns `ErrNonConstantExpr`, which is why `:112`
currently reports `constexpr: opcode not in the constant subset: 0x0a` in both
lanes. That error is not `ErrFeatureDisabled`, so the vector scores as an honest
**fail** and never hides in `gated` — the gate tension flagged on #38 does not
arise. `constexpr.go` had already predicted this shape from the suite's assertion
types; the reference confirms it structurally. Fixing it is #39's business: once the
full table exists, the const-legal check moves to the validator and this reader's
narrow accept set dissolves.

While verifying that, one figure in `constexpr.go` was found wrong and corrected:
`constant expression required` appears **24** times (global 7, elem 7, data 6, array
2, func_ptrs 2), not 22. The load-bearing half of the claim held — 0 occurrences
under `assert_malformed`, and both cited lines resolve as described.
