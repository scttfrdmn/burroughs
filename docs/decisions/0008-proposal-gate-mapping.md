# 0008 — Where the proposal→opcode mapping lives, and how a gate proves it declines

Date: 2026-07-31 · Status: **accepted** (Scott, 2026-07-31 — two rulings on #48)

## Decision

Two rulings, both Scott's, both on #48.

**1. The mapping is a separate hand-authored artifact — one file per authority.**

> The generated table stays purely generated; a proposal→opcode mapping with no
> extraction source is authored testimony and must not cohabit with machine output, or
> the table becomes a file with two authorities — the `tools/go.mod` lesson in a new
> costume. It lives beside `constOps` in kind: **cited** where citable (each proposal's
> overview document enumerates its opcodes — human-checked citations, per the provenance
> taxonomy), and **machine-checked for consistency** where the machine can reach: every
> opcode it names exists in the table, every tracked gate maps at least one construct.

**2. The inverse gate control lands with this change, pre-registered as its
definition-of-done.**

> Red controls don't idle in main: a control born red in a PR that can't green it either
> gets parked (training alarm-blindness, violating the no-skip spirit) or blocks
> unrelated merges. The project already owns the right pattern — *a debt is discharged by
> a tripwire, never an intention* — and the tripwire is #48 carrying the control in its
> done-when, exactly the `ErrDataCountRequired` maneuver.

## Question

#48: the instruction dispatch reads the generated table and consults `d.Features`
**nowhere**, so with every gate off a function body using `throw_ref`, `try_table`,
`v128.const`, or `ref.eq` decodes clean. That is the accept-and-ignore half of the #5
ruling, and neither board can see it — the default lane is `assert_malformed`-only, and
`TestAllGatesOnLeavesNothingGated` requires `Gated == 0` under full features, which a
gate that never fires passes trivially. The lane bounds over-gating; nothing bounded
under-gating.

The mapping is the design question, because it is a `constOps`-shaped fact: the reference
interpreter does not record which proposal an opcode belongs to, so unlike the immediate
shapes (0007), it cannot be extracted.

## Why not a `feature` field on the generated row

The tempting shape — teach `opcodegen` a mapping table and have it stamp each row — was
rejected by ruling 1, and the reason generalizes past this file. `optable.go`'s header
says `Code generated ... DO NOT EDIT` and names its authority: `decode.ml` at `bdd7164`.
Every row in it is answerable by re-running the extractor. A `feature` column would be
the one column that is *not*, and the header would then be making a claim about the file
that is true of 542 rows and false of 542 cells — a single artifact under two
authorities, where a reader cannot tell by looking which half they are reading.

This is the `tools/go.mod` decision (0005) in different clothes: tool versions are pinned
in a file whose job is pinning tool versions, not scattered into CI YAML whose job is
something else. One file, one authority, one answer to "who says so".

So the mapping is its own file, its own package-level declaration, with its own
provenance rows — and the generated table remains a thing that can be deleted and
regenerated without losing a hand-authored fact.

## Measured facts

Measured 2026-07-31 against the vendored `WebAssembly/spec` at `bdd7164` (the same
revision `optable.go` was extracted from, so the citations and the table are the same
snapshot).

### The gap is four proposals, not one

#48's fix sketch said "GC has no gate at all" and listed it as an in-scope-or-deferred
question. Scott's ruling folded it in — *a tracked proposal with no `Features` bool is
the forgotten-fifth-gate scenario existing in the wild.* Dumping the table's mnemonics
and reading them against contract §9 G-2's tracked set shows the same scenario **four
times**:

| Tracked proposal (G-2) | Constructs in the table | `Features` bool before this change |
|---|---|---|
| exception handling | `0x08`, `0x0a`, `0x1f` | present (sections only) |
| SIMD | `0xfd` region, 275 arms | present (valtype only) |
| GC | `0xfb` region (31 arms), `ref.eq` | **absent** |
| tail calls | `0x12`, `0x13` | **absent** |
| relaxed SIMD | `fd 0x100`–`fd 0x113` | **absent** |
| multi-memory | memarg flags bit 6 | **absent** |
| memory64 | limits flags 4..7 | present |
| threads | limits flags 2, 3 | present |

Stopping at GC because GC is what the issue named would have been the
scope-to-the-sample failure the ruling itself invokes to justify folding GC in. The
domain is G-2's tracked set, and the gap is *every member of it with no bool*.

Two G-2 members are deliberately **not** given a bool here, and the mapping file says so
at each site rather than omitting them silently:

- **function references** (`call_ref` `0x14`, `return_call_ref` `0x15`, `ref.as_non_null`
  `0xd4`, `br_on_null` `0xd5`, `br_on_non_null` `0xd6`) — folded into Wasm 3.0's core
  alongside GC, which is also how the reference treats it; the proposal has an overview
  with a per-opcode table, so it is *citable*, and it is mapped to the GC gate with that
  stated. A separate bool would be a fifth gate whose scope is a subset of GC's.
- **stack switching / component model** — no constructs in the table at `bdd7164`, so a
  bool would be a gate that governs nothing, and the inverse control below would fail it
  correctly. They arrive with their phases (contract §§6–7).

### The proposal overviews carry per-opcode tables, so the citations are real

Each mapped construct cites a line in the vendored proposal document:

- `proposals/gc/MVP.md:809`–`839` — a markdown table, one row per `0xfb` sub-opcode.
- `proposals/tail-call/Overview.md:139,140` — "`return_call` is 0x12", "…0x13".
- `proposals/exception-handling/Exceptions.md:460`–`462` — `try_table` `0x1f`, `throw`
  `0x08`, `throw_ref` `0x0a`.
- `proposals/function-references/Overview.md:323`–`327` — the five above.
- `proposals/relaxed-simd/Overview.md`, `proposals/simd/BinarySIMD.md`,
  `proposals/multi-memory/Overview.md` — likewise.

These are **cited** rows in the provenance taxonomy's sense (#37): a human read the
document and wrote down what it said. What the machine checks is not the inference but
the resolvability — the same split as `TestDerivedFixturesStateResolvablePremises`.

### A gate decline must be *deferred*, exactly like the const verdict

The most consequential measured fact, and it is the one that would have made a naive
implementation wrong on a green board.

`binary.wast:112`'s global initialiser ends `\41\00` with no END, and the byte that
follows is the code section's id `\0a` — which *is* `throw_ref`, an exception-handling
opcode. The reference reads on and the expression runs off the image, so the suite wants
`unexpected end of section or function`. An implementation that returns
`ErrFeatureDisabled` the moment it meets a gated opcode reports a **gate decline** for a
module the spec calls malformed — the wrong layer's answer, and worse than the const case
because it also parks the vector in `gated`, where `TestGatedVectors` would demand an
allowlist entry for a decline that is pure artifact.

So the gate verdict defers on exactly the same mechanism the const verdict already uses:
`instrCtx` records the first gated construct and releases it only if the grammar
completed. The two deferrals then need an order between them, and the reference does not
have an opinion because it has neither — so it is decided here and stated: **malformed
wins over both, then the feature decline, then the const verdict.** A gated construct in
a const expression is declined for the feature, because *the engine's configuration is a
more fundamental "no" than a validation rule about a construct it does not implement*.

## Consequences

- `Features` grows four bools: `GC`, `TailCall`, `RelaxedSIMD`, `MultiMemory`. The
  reflection-derived lanes (`allFeaturesOn`,
  `TestAgreementHoldsUnderEveryFeatureConfiguration`) pick them up with no edit, which is
  the dividend of having derived the domain — and the second of those walks `2^N`
  configurations, so N going 4 → 8 costs 16 → 256 iterations of a cheap loop.
- The zero value is still v0's posture: every gate present and off (contract §9).
  *Amended 2026-08-21 on Scott's order (the #468 report): "an ADR records a decision at a
  time. A clause falsified by a later flip gets an amendment note citing the flip that
  falsified it, and the original text stays legible — otherwise the record starts agreeing
  with the present, which is the one thing it exists not to do."*
  *The clause above was true when written and is **falsified in its second half**. Two
  flips have since diverged v0's policy from the struct's zero value, each its own
  stamped event: **SIMD** (#227, [ADR 0025](0025-g-1-carves-out-vectors-whose-sole-blocker-is-9s-deferred-validator.md))
  and **relaxed SIMD** ([ADR 0028](0028-relaxed-simd-lowerings-are-deterministic-and-architecture-uniform-the-references-choice-taken-once.md)).
  `DefaultFeatures()` now returns `Features{SIMD: true, RelaxedSIMD: true}`. So the zero
  value **is** still every gate off — that half stands, and `Features`'s own doc comment
  keeps it as an invariant — but it is **no longer v0's posture**, which is what a caller
  who configures nothing gets. The two facts were accidentally identical when this ADR was
  written, which is why one sentence could carry both; `sections.go` now names them
  separately for that reason.*
  *The same divergence overtakes this list's last bullet — "Every gate remains off and
  every mapped construct remains rejected when off". Read as the scope of **this change**
  it is still true: gate nine landed without flipping anything. Read as a standing claim
  about the tree it is false, and it is flagged here rather than rewritten because its
  subject is a PR, not a posture.*
- **The inverse control is this change's definition-of-done**: for every bool in
  `Features`, turn everything on, turn *that one* off, and require at least one mapped
  construct declined with `ErrFeatureDisabled`. A gate that declines nothing fails
  loudly. Derived by reflection, never enumerated — so gate nine is covered on arrival.
- The same control **pins the blocktype branch ordering** as a second obligation. `0x7b`
  with SIMD off must be `ErrFeatureDisabled`; if the valtype branch ever stops being
  `either`'s last, the alternation overwrites that decline with `malformed value type`
  and the control goes red. One control, two obligations — which is what decided ruling 2
  over parking a red test.
- **Board cost, stated before it is measured:** turning gated opcodes into rejections can
  only move vectors *into* `gated` on the default lane, and every such vector needs a
  `TestGatedVectors` entry naming its feature. Any vector that lands there is a real
  decline, honestly parked, and is simultaneously **failed** in the all-on lane until the
  feature works. If a vector's expected string is an `assert_malformed` that the engine
  was previously producing for the wrong reason, that shows up as a fail, not a silent
  pass.
- What this does **not** do: implement any gated feature. Every gate remains off and
  every mapped construct remains rejected when off. This closes the accept-and-ignore
  hole; it does not flip a gate, so no minor version moves (0004).

## Alternatives rejected

- **A `feature` column on the generated row.** Ruling 1. Two authorities in one file.
- **Region-level checks only** (`0xfb` is wholly GC, `0xfd` wholly SIMD). Correct for
  those two and insufficient: EH is three single-byte opcodes, tail calls two, relaxed
  SIMD a *sub-range* of a region another gate owns. The mapping models both granularities
  and the file states which each entry is, per #48's "mixed granularity is fine but
  should be stated".
- **Deferring the four missing bools to their own PR.** This is the scope the ruling
  explicitly widened; a tracked proposal with no bool is invisible to the very lane that
  would police it.
