# 0007 — The opcode table's authority, and how agreement with it is checked

Date: 2026-07-31 · Status: **accepted** (Scott, 2026-07-31 — principle stamped on #38,
mechanism **B** stamped with chat-Claude's four conditions)

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

> **Stale as written — see [Correction (2026-07-31, during #39)](#correction-2026-07-31-during-39-the-counted-shape-was-wrong).**
> Every figure in this section is wrong, and the heading's claim about method is the
> reason why. Body preserved per the 0003 precedent; the pointer is here because a
> claim must not present itself as current while its correction lives three sections
> away. (Ruling: Scott, PR #43.)

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

A generator under `internal/gen/opcodegen` (or `tools/`) parses the
`| 0xNN ->` arms and their RHS immediate calls, emits a Go table, and the table is
**committed**. A test re-runs the extraction against the vendored reference and fails
on any disagreement with the committed table.

**For:** no OCaml anywhere. The measured regularity (504 arms, 368 immediate-free, a
16-reader vocabulary — figures corrected in the Correction section; the regularity
claim survives, more strongly) says the extraction is a small, testable pure-Go program. The
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
arms are hand-written under either option. (Figures corrected below — 411 of 542, 17
readers. The ratio, which is what this argument rests on, is unchanged.)

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

## Correction (2026-07-31, during #39): the counted shape was wrong

Appended rather than edited in place, per the 0003 precedent: the record of what was
believed at the deciding moment, and of why it survived review, is the part worth
keeping. The **decision** is unaffected — B is more strongly supported by the true
figures than by these — but "counted (not estimated)" was a claim about a method, and
the method was flawed in two nameable ways. The measured table, extracted by the very
mechanism this ADR chose:

| region | ADR said | actually | why the ADR was wrong |
|---|---|---|---|
| single-byte opcodes | 201 | **218** | counted arm *lines*, not arms; and omitted the 3 prefix escapes |
| `0xfb` prefix (GC) | 29 | **31** | lines, not arms |
| `0xfc` prefix (misc) | 18 | **18** | — |
| `0xfd` prefix (SIMD) | 256 | **275** | assumed sub-opcodes are bytes |
| **total** | **504** | **542** | |

The single-byte figure moved twice, and the second move is worth naming because it
happened *after* this section was first written, inside the same PR: 215 arms parse out
of the source text, and `0xfb`/`0xfc`/`0xfd` are three further single-byte facts — an
escape is neither absent nor illegal, and the first table omitted all three. Found from
outside by #33, not by the extractor's own partition test, which enumerated the three
prefixes as a literal and so could not miss them. Grave #45.

**Error one — an arm is not a line.** A single `| 0x18l | 0x19l as opcode ->` head is
one line and two opcodes, and a head can wrap across lines (`decode.ml:601` cost an
afternoon before the extractor grew head-continuation joining). Lines are what a grep
counts; arms are what a table has rows for.

**Error two — the `0xfd` sub-opcode is a `u32` LEB, not a byte.** The ADR wrote 256
because a prefix table "obviously" has at most 256 entries. `decode.ml` reads the SIMD
sub-opcode with `match u32 s with`, and the arms run to **`0x113`** with a single hole
at `0xbb`. Assuming the width was the tell that this figure was reasoned rather than
counted, and it is exactly the class of mistake #36's width-parameterized design exists
to catch — *when two fields disagree about a value, the suite has handed you a
bidirectional control*, and here the ADR simply asserted one side.

The prose figures need the same correction. **411** arms carry no immediates (not 368),
there are **17** distinct readers (not 16), **20** distinct immediate shapes counting the
empty one, and **40** arms are explicit `Illegal`. The reader histogram was worse than
imprecise — it was a whole-file grep, so it counted occurrences *outside* `instr`
entirely (`at idx s`: 78 file-wide, 63 within `instr`), and `grep 'idx s'` silently
matched the tail of `laneidx s`. Reconciled per-reader against the real arms:

    68  idx        45  memop      22  laneidx     9  heaptype    5  block
     4  blocktype   2  byte       1 each: f32 f64 laneidx16 s32 s64 u32 v128
                               vec_catch vec_idx vec_valtype

Source occurrences and attributed immediates differ by design and the difference is
itself checkable: `heaptype` appears 7 times and is attributed 9 because the
`0x18l | 0x19l` arm emits two rows from one source line. That is the arithmetic of
error one, running the other way.

**The `laneidx16` entry is a defect the ADR's method could not have found.**
`i8x16_shuffle` reads `repeat 16 laneidx s` — sixteen lane bytes. The first extractor
recorded *one* `laneidx`, losing 15 bytes and shifting every subsequent instruction in
a body. No spec vector can see it: every vector bearing on this table is
`assert_malformed`, which is the whole reason this ADR exists (§9 G-3). It was found by
printing what the extractor actually returned for each arm — *print-don't-trust* — and
it is the strongest available argument for B over hand-transcription, since a human
reading `repeat 16 laneidx s` into a table row is running exactly the same risk with no
machine to object.

Three of the four irregular arms are confirmed as described (`0x02` block, `0x03` loop,
`0x1f` try_table). `0x04` if/else has a wrinkle the ADR did not record: its second
`instr_block` is **conditional** on `peek = 0x05`, so `if` without `else` reads one
block and `if`/`else` reads two. Cited in `TestIrregularArmsHaveCitedShapes` per
condition 2 rather than left in prose here.

**How this was allowed to happen, which is the reusable part.** The figures came from
greps over the file, and each grep was checked for *plausibility* rather than against a
second method. A count that nothing can contradict is an estimate wearing a count's
clothes — the same defect as a derivation from a sample that cannot falsify it (0003's
LEB ordering). The extractor is that second method, and it disagreed with the ADR on
its first successful run. Which is the ADR being right about the mechanism it chose.

## Postscript (2026-07-31, #43/#39): what the implementation settled, and one thing it broke

Appended, body preserved. The **decision** is unaffected — every claim below is the
authority principle working — but two passages above are now false and one condition
turned out to be unenforced.

**The `ErrNonConstantExpr` passage is orphaned.** The layering-finding paragraph says
Burroughs "restricts opcodes at decode time and returns `ErrNonConstantExpr`, which is
why `:112` currently reports `constexpr: opcode not in the constant subset: 0x0a`."
That sentinel no longer exists. `:112` now passes, and the fix is the shape this ADR
predicted: the const check moved out of the reader's dispatch. It did not move to the
validator, though — it moved to a *deferred* position inside the same reader, and the
distinction is the finding.

**The deferred verdict is the implementation's one genuine surprise.** The ADR
correctly read `const s = instr_block s; end_ s` as containing no const check, and
inferred that const-ness belongs to #9's validator. True, and insufficient: reading the
full grammar means a truncation encountered *after* a non-const instruction is still a
truncation, and `:112` is exactly that module. An aborting reader reports `constant
expression required`; the reference reads on and reports `unexpected end of section or
function`. So `instrCtx` records the first non-const opcode and `decodeConstExpr`
releases it **only if the grammar completed**. *An invalid verdict that pre-empts a
malformed one is reporting the wrong layer's answer* — the wrong-layer tell (#36)
pointed the other way. The ADR's own postscript walked `:112` byte by byte and did not
notice this, because it was tracing why the *reference* says what it says rather than
what an implementation must do to agree with it.

**"Gated opcodes stay gated" is a condition, and it is not met — #48.** The bullet above
states it as settled: "A gate-off engine meeting a GC or SIMD opcode still reports a
feature-named error, never a spoofed spec string." The second half holds. The first does
not: the table-driven dispatch consults `Features` nowhere, so with every gate off the
decoder **accepts** `throw_ref`, `try_table`, `v128.const`, and `ref.eq` in a function
body. Measured, all six probes returning `<nil>`; filed as #48 with the fix sketch.

Two things worth extracting, because the failure is structural rather than careless:

1. **The bullet reads like a consequence of the table and is actually a requirement on
   its consumers.** The table's job ends at shape; nothing in it can make a dispatch ask
   about features. Stating that as a property of the design, in a list of design
   properties, is how it acquired the appearance of being implemented by the design.
   *A condition phrased as a consequence has no owner.* This ADR's other conditions
   became named tests; this one became a sentence.
2. **Neither board could catch it, and the all-gates-on lane could not by
   construction.** `TestAllGatesOnLeavesNothingGated` requires `Gated == 0` under full
   features — a control on gates *hiding* vectors. A gate that never fires trivially
   passes it. So the third-verdict machinery bounds over-gating and nothing bounded
   under-gating, which is the asymmetry #48's control closes by reflecting over
   `Features` and requiring each gate, off, to decline *something*.

**And the method held where it was actually used.** Condition 4's agreement test found
grave #47 (`immBytes` reading a lane index as a raw byte) and the three probe survivors
in #43's falsification pass were all found by *printing what the code returns* — the
same instrument that found `laneidx16` above. One of those three, the blocktype branch
order, falsified a claim in this ADR's neighbourhood too: `either` backtracks, so branch
order does not affect the accept set at all. 427 of 768 measured rows differ between the
two orders and every difference is the error message alone. What the order decides is
which branch's error survives — load-bearing for exactly one reason, that a gated
branch's decline must be last or the alternation overwrites it with a spec
malformed-string. Recorded in `TestBlockTypeAlternationIsTheAuthority`, whose doc keeps
the wrong reason alongside the right one.
