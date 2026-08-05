# 0016 — unbounded immediates live beside the body, keyed by instruction index

Date: 2026-08-05 · Status: **proposed** — awaiting Scott's call on the shape

> Scott ordered the `br_table` work and ruled the split (two PRs, `br_table` first). He has **not**
> ruled on the retention *shape* below, so this record is `proposed` and the PR flags it. The one
> sentence he did rule — *retention is forced by consumers, but shaped by the grammar* — is quoted
> where it applies and is the ground option B stands on. An ADR marked accepted on a stamp nobody
> gave is a fabricated citation about the project's own governance, which is worse than a wrong
> option.

`Instr` is two words, deliberately (0002, and grave #100 on why packing rather than growing).
Some immediates are **unbounded**: `br_table`'s label vector, `try_table`'s catch clauses,
`select`'s optional result types. They cannot live in two words, and the decoder discards them
today with a comment naming their future consumer (`instr.go`'s `immVecIdx`). `br_table` now has
that consumer — 1,330 vectors — so the retention has to be decided rather than deferred again.

## Decision

Unbounded immediates are retained in a **side table on `Func`, keyed by instruction index**:

```go
type Func struct {
    TypeIndex uint32
    Locals    []LocalGroup
    Body      []Instr
    Labels    map[int][]uint32   // instruction index → br_table's label vector
}
```

Three properties are load-bearing, and each is a rule rather than a preference:

1. **The key is the instruction's index in `Body`**, which is the identity `Instr` already has
   and the only one that survives a rewrite. Not a pointer, not an ordinal among `br_table`s.
2. **The vector is never preallocated from its declared count.** `decodeVec` allocates nothing
   and each element consumes bytes, so a large count is bounded by the *image*; `append`ing per
   element preserves that. This is grave #138's law applied before the defect: *a count check
   right about the verdict can be wrong about the resources*.
3. **The structure follows the wire form, not the consumer.** The wire is a vector of label
   indices plus a default, so that is what is kept. The default lands in **`Imm0`** — printed,
   not assumed: `immVecIdx` stages no word, so `immIdx` is the *first* staged immediate for this
   opcode rather than the second. The first draft of this record said `Imm1`, reasoning from
   field order in the table row, and the probe said otherwise.

**Retention is forced by consumers, but shaped by the grammar.** (Scott, on this work.) A
consumer is what makes retention *necessary* — the decoder does not keep what nothing reads, and
0011's error-only reader and `Datas`-before-0015 are both that rule working correctly. But once
retention is necessary, its *shape* comes from the wire form, because the first consumer is
rarely the last: `LocalGroup` kept `(count, type)` runs rather than a flat vector and the text
round-trip got sharper as a side effect, since flattening cannot distinguish one run of two from
two runs of one. `br_table`'s label vector has the same property — the interpreter wants
`labels[i]`, and the text encoder wants the vector's *written length*, which a set or a resolved
target array would have destroyed.

## Question

Where does an immediate that does not fit two words go?

- **A — grow `Instr` to hold a slice.** Rejected, and it is the option that looks cheapest. A
  `[]uint32` field is 24 bytes on every instruction in every module to serve **one opcode in
  256**, and `Instr` is the array 0002's whole strategy is built on: the rewrite's win is a
  compact sequence with pre-decoded immediates, and a 24-byte-per-instruction tax on a
  megabyte-scale Go guest is precisely the cost §1's workload cannot amortize. Rejected on the
  thesis, which is where 0002 rejected the side table.
- **B — a side table keyed by instruction index (chosen).** Costs one map per function that has
  a `br_table` and nothing at all for functions that do not, which is nearly all of them.
  `Instr` stays two words. The key is an index into a slice both sides already have.
- **C — resolve labels to targets at decode time and store the resolved array.** Rejected on
  layering, not on cost. A label index becomes a target only once the enclosing block structure
  is known, and *validating* that the index is in range is #9's judgement. Resolving here would
  put a validator's opinion in the decoder under a name that hides it — the same refusal
  `immBlockType` already makes by staging a raw blocktype rather than normalizing it.

A fourth option was considered and is worth recording because it is the one that reads as
elegant: **a single `[]uint32` arena on `Func` with `Imm1` holding an offset and length.** It is
denser than a map and it is what a compiler would do. Rejected for v0 as premature: it makes
every retained vector share one allocation's lifetime, and the measurement that would justify it
(#136's benchmark) has not been run. When it is run, this is the option to reach for, and B's
accessor shape does not prevent it — which is the point of an accessor.

## Consequences

- **`br_table` becomes executable and encodable**, and the earn was **266 passes, not the 1,330
  the bucket held** — corrected here against the board rather than left as the forecast this
  paragraph opened with. The bucket went 1330 → 0 and the residue is accounted for individually: a
  bucket-set diff shows **+1006** re-keyed to `cannot yet encode the call_indirect instruction`,
  **+8** to `interp: no arm for opcode 10`, and +50 to `gated`. `br_table.wast`'s own module calls
  `call_indirect`, so the file that names this instruction is still 1/147.

  That is the **dependency closure**, and it is the reason this landed as its own PR under Scott's
  split. *Bucket size estimates the reward, not the job* — and the converse holds too: the bucket
  a capability drains is not the bucket it *passes*. Recorded because an ADR quoting its own
  pre-measurement forecast is the number-you-haven't-run defect wearing a consequence's clothes.
- **`try_table`'s catch clauses and `select`'s valtype vector get the same home when they get
  consumers.** Their `immCatchVec` / `immVecValType` comments are updated to name this decision
  rather than "a side array once there is a consumer", so the deferral cites a shape instead of
  an intention.
- **`Labels` is nil for functions with no `br_table`**, and reading a missing key yields nil,
  which is the correct answer for an instruction that has no label vector. A nil map is not an
  error state.
- **The map's cost is per-function-with-a-br_table**, and #136's benchmark is where the arena
  question gets settled. This ADR does not pre-empt it.
- **#136 is not discharged by this**, and that expectation is corrected here: #136 is
  *branch-target resolution at build time*, which needs the pairing to live beside the body.
  This decision supplies the retention **mechanism** #136 needs and leaves #136's own
  measurement outstanding. One ADR earns one implementation, and this one earns `br_table`.
