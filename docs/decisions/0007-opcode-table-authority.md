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
pinning onto the suite fetch is a separate question, filed as #42 — noted here rather
than fixed in passing, since it is not this ADR's subject.

**Provenance of the error, since a correction with no owner teaches nothing.**
chat-Claude claimed it: a reviewer's premise transcribed from *intention* rather than
checked against the tree — the drifted-citation defect committed from the review chair
rather than from the keyboard. Which is the useful part. This project's controls
assume the reviewer is the check on the author, and this is the case where the
review's own factual claim was the defect, invisible to a reviewer verifying code
against claims because the claim was the thing that was wrong. The same camouflage as
0003's *defect stated as the rule*, one seat over.

The correction is also **not** "make the two match", and that distinction is the
substance. Manufacturing the consistency the prose asserted would have satisfied the
sentence and lost the reason; pinning the reference on its own correct ground and
naming the asymmetry keeps both. A subsequent ruling (chat-Claude, on this PR) leans
toward pinning the suite too, on a stronger argument than the original: a floating
suite makes board drift *visible* but not *attributable* — when a count moves,
"upstream added vectors" and "we regressed" are indistinguishable without knowing the
corpus identity, and *measured-not-remembered requires knowing what was measured
against*. That argument belongs to #42, where it is recorded; it is noted here only
because it shows the corrected premise being replaced by a better one rather than by
its negation.

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

### B's four conditions

Endorsed by chat-Claude on the merits rather than as toolchain convenience, subject to
four conditions. They are conditions on the *ADR*, so they are recorded here and
inherited by #39 as definition-of-done rather than restated there.

1. **The extractor is born falsified, including the vacuity control.** Every assertion
   it makes gets the defect introduced and the failure watched, before the table is
   trusted. The vacuity case is the one that would otherwise pass for the wrong
   reason: **an extraction finding zero arms must fail.** A silently broken parser —
   an upstream refactor, a changed indent, a moved file — otherwise emits an *empty*
   table and a drift check comparing empty to empty agrees perfectly. That is a green
   wearing the shape of a control while asserting nothing, which is the
   `requireSuite` grave (#29) relocated into a code generator. A floor on the arm
   count, per region, is the cheap form.
2. **The four irregular arms get the provenance treatment.** `block`, `loop`,
   `if`/`else`, and `try_table` are hand-written under any mechanism, which means they
   are exactly the facts the extractor does not machine-check — so they are **cited**
   to their `decode.ml` line numbers, or **derived** with premises stated, in the same
   scheme `TestFixtureProvenance` already enforces for fixtures. Hand-written and
   uncited is the category this ADR exists to abolish; the four arms do not get an
   exemption for being few.
3. **The committed table carries a generation header:** the reference SHA it was
   extracted from and the extractor version. Stamp-don't-deduce, applied to a
   generated artifact — a table whose provenance has to be reconstructed from git
   archaeology is a table whose authority is hearsay. The header is what lets a reader
   of the *file* answer "which authority, at which revision" without leaving it.
4. **CI re-runs the extractor against the pinned vendored source and asserts table
   equality.** Drift becomes a build failure rather than a diff nobody ordered. This
   is cheap *precisely because* B needs no toolchain — the same property that
   recommends B is what makes its continuous check affordable, where C pays a build
   cost for a check that arrives late.

## Consequences

- `third_party/spec` becomes a **pinned** fetch (`scripts/fetch-spec-ref.sh` +
  `make spec-ref`), SHA recorded in the script, gitignored like the suite.
- The generated table is committed, which is what lets a fresh clone build with no
  fetch.
- **The drift check cannot live in `make check`**, and the reason is the existing
  `conformance` split rather than a new principle. `check` must stay green on a fresh
  clone (Makefile, and it is why `check` deliberately does not set `BURROUGHS_NO_SKIP`),
  but the drift check needs `third_party/spec` vendored. Same shape as the board: it
  gets its own target that **refuses to run without the reference** rather than
  skipping — a skip is not a verdict, and a drift check that passes by asking nothing
  is worse than none, since it reports agreement with an authority it never read. CI
  runs it, so it is not optional; it is the other half of the mirror. If the skip
  routes through Go at all it routes through `internal/testenv`, which is the one
  licensed door.
- The extractor's controls are subject to **condition 1** above, which subsumes what
  this bullet said in an earlier draft (feed it a synthetic arm shape it should
  reject). The unrecognized-arm error is still load-bearing and still gets that test —
  but on its own it is insufficient, because it cannot fire on the failure mode where
  the extractor recognizes *nothing*. The vacuity control is the addition, and the
  narrower version is not the requirement.
- Gated opcodes stay gated. The table says what shape a byte has; `Features` says
  whether it is accepted. A gate-off engine meeting a GC or SIMD opcode still
  reports a feature-named error, never a spoofed spec string.
- **#33's agreement test lands in the same PR as the table (per 0006), and its scope
  must note the dissolution.** The obligation was filed to catch two readers drifting
  — `constExprOps` and #39's full table — but the postscript below changes what the
  first one *is*: the reference defers const-ness to validation, so once the full table
  exists `decodeConstExpr`'s narrow accept set **dissolves** rather than persisting as
  a second opinion to be cross-checked. So the agreement test's subject is not "do the
  two tables agree about the const-legal subset" — that is the version scoped to
  today's sample, and 0006/#33 already widened it once for exactly this reason. It is
  "does the *one* table, now the only opcode authority, agree with the reference across
  all 256 single-byte opcodes and the tracked prefixes." A tripwire whose subject
  disappeared is discharged by re-pointing it, not by closing it: the drift risk it
  named is now the extractor-versus-reference risk, which is condition 4. Record that
  on #33 when the table lands, so the obligation tracks the shape rather than the
  filing.
- **Not fixed here:** the suite fetch's own lack of pinning — #42.

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
