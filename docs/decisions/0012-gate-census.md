# 0012 — A whole-region gate entry is checked by a census, arm by arm

Date: 2026-08-02 · Status: **accepted** (Scott delegated the call, 2026-08-02 — #91)

## Decision

**Option 2 of #91, widened: a committed census of every accepted arm's gate, checked for
drift, covering the ungated arms as well as the gated ones.**

`internal/binary/testdata/gate-census.txt` records one row per accepted table arm —
`prefix sub gate mnemonic`, with `-` for *no gate* — and
`TestGateCensusIsClassifiedArmByArm` recomputes it from `prefixRegions` and `gatedOpcodes`
and requires exact agreement. An arm arriving upstream inside a whole-region range is then
a **build failure demanding classification** rather than a silent inheritance, which is
0007's condition-4 drift mechanism pointed at the composition of the two tables instead of
at one of them.

Two refinements to the option as filed, both from measurement rather than taste.

**The census covers all 499 accepted arms, not the 298 gated ones.** #91 framed the risk
as an arm inheriting a region's gate, which is the `0xfb` shape. The mirror is an arm
arriving with **no** gate — decoding clean with every gate off — and that is the #48
defect itself, not a cousin of it. A census of only the gated arms would be a control
scoped to the population today's risk lives in, which is the failure mode the
scope-controls-to-the-space law names. Measured: 499 accepted arms, 298 gated (SIMD 236,
GC 37, RelaxedSIMD 20, ExceptionHandling 3, TailCall 2), 201 ungated.

**The census is exact, not slack-tolerant, because it has no unpinned input.** Both its
sources are committed artifacts: `optable.go` is generated from `decode.ml` at the
`bdd7164` pin, and `gatemap.go` is hand-authored testimony. So unlike the board counts of
0013, this number cannot move because upstream moved — it moves only when `make opcodes`
runs or a human edits the mapping, which are exactly the two events that should demand
review. An exact golden file is available here and is the strongest control available, so
it is the one to use.

## Question

#91: `gatedOpcodes` holds whole-region entries — `{prefix: 0xfb, lo: 0x00, hi: 0xff, gate:
gateGC}` — resting on the measured fact that `0xfb` is entirely GC at the pin. That is a
claim about *every arm the region will ever hold*. Both existing controls walk the
**mapping**: `TestEveryMappedOpcodeExistsInTheTable` asks whether each entry covers a real
arm, and `TestEveryGateMapsAtLeastOneConstruct` asks whether each gate maps something.
Neither starts from the table, so an arm arriving inside a region range inherits its gate
with every control still green — the range still covers something, the gate still maps
something.

The gap was found by sweeping cited-versus-defined test names in #88: the comment at the
site cited `TestEveryTableOpcodeIsClassified`, which has never existed, **for precisely
this direction** ("the classification test walks the table rather than this file"). The
missing control was documented as present, which is why nobody looked.

## Why not the other two options

**Option 1, per-arm citations replacing the ranges.** Rejected: it trades the exact defect
for its inverse. A range grows correctly with upstream — a new GC arm at `0xfb 0x20` *is*
GC, and the range says so without an edit — where an enumeration silently under-covers the
same arm. Enumerating also multiplies 0008's hand-authored testimony by 298, and each of
those rows would carry a citation nobody re-derives. The range is good testimony; what was
missing was a check on what it came to cover.

**Option 3, assert region homogeneity against upstream metadata.** Rejected on
availability: there is no such metadata. This is the `constOps` situation from 0007 —
`decode.ml` records what an opcode's immediates are and says nothing about which proposal
introduced it, which is *why* 0008 exists. Option 3 assumes the authority the mapping was
created to substitute for.

## Measured facts

Printed from the code path, not reasoned (`gateFor` over `prefixRegions`):

| region | accepted arms | gated | ungated |
|---|---|---|---|
| `0x00` | (single-byte) | 10 | 201 |
| `0xfb` | 37 | 37 | 0 |
| `0xfc` | — | 0 | — |
| `0xfd` | 256 | 256 | 0 |
| **total** | **499** | **298** | **201** |

`0xfc` is worth naming: it is the bulk-memory/non-trapping-conversion region, entirely
core Wasm 2.0 at the pin, so it holds **no** mapping entry and its arms are all ungated.
That is a correct answer that a gated-arms-only census could not have expressed, and it is
the second reason for widening.

## Consequences

- The census is a **third derived artifact** and its header says so: authority is the
  *composition* of `optable.go` and `gatemap.go`, neither alone. 0008's one-file-one-authority
  rule is about not mixing generated rows with authored cells in one artifact; a file
  explicitly derived from two committed inputs does not have that ambiguity, because
  regenerating it is deterministic and answers "who says so" with "both, composed".
- Regeneration is `make gate-census`, alongside `make opcodes` and `make keywords`. The pin
  moving is now a **two**-target motion, and `make opcodes` without it fails the census —
  which is the intended coupling, not friction.
- The census needs a **vacuity floor**, because it is a comparison and an empty census
  agrees with an empty computation. A per-region floor, not a total: a total floor passes
  while a whole region has dissolved, and `prefixRegions` is exactly where a region can go
  missing.
- `TestEveryMappedOpcodeExistsInTheTable` stays. It is the mapping-side direction and the
  census is the table-side one; the two are not redundant, and deleting either leaves a
  direction unasserted. The comment at each now names the other as its complement.
- The stale citation in `gatemap.go` is replaced by one that resolves. Per #88's lesson,
  **a test name is as checkable as a `.wast:N`** — this ADR's control exists partly because
  a citation claimed it already did.
