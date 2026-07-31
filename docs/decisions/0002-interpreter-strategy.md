# 0002 — Interpreter strategy

Date: 2026-07-30 · Status: **accepted** (Scott, 2026-07-30) · Resolves contract §10.1
Contract refs: §10.1 (open question), §0 (posture), §9 (gates)

## Decision

1. **Internal-form rewrite** (Q1, Option B).
2. **Giant `switch` dispatch** for v0 (Q2, Option A).
3. **Bare `uint64` slots plus a parallel reference array** (Q3, Option A).

chat-Claude conceded Q1 and sharpened the argument past what this doc
originally made: **the overrule condition offered below — "if the workload is
many short-lived modules" — is a workload Burroughs disclaims.** Go guests are
large and long-lived; a Go program compiled to wasm is megabytes that load
once and run for hours. Rewrite's build cost amortizes to nothing on the
thesis workload, and Wizard's startup/memory win serves a serverless-edge use
case that sits beside "browser embedding" in §1's non-goals. The structural
point stands as the decisive one: structured control flow forces resolved
branch targets into existence regardless, so "in-place avoids a second
representation" was never true — the only question was *which* second
representation, and the measurement says the split-across-two-arrays one
costs two cache lines per instruction.

**The side table died on its own terms.** That is the intended way for a
design to die here.

### Pinned consequence — reference slots (not a footnote)

References MUST live in a parallel array from the first line of interpreter
code. A Go pointer stored in a `uint64` is invisible to the garbage
collector, and pure-Go (§ CLAUDE.md, no cgo) leaves no escape hatch. The
value stack is therefore **two parallel arrays** — `[]uint64` for numeric
slots, `[]ref` for references — with the validator deciding which array each
slot uses.

This is pinned in the decision text at Scott's direction because it is
cheap now and GC-precision surgery later: the alternative is discovering it
at WasmGC (§8, M-4), by which time every opcode touching the stack has
assumed one array.

### Recorded negative result — closure compilation

Closure compilation is a **recorded negative with a reproducer**, upgraded
from "measured later gate" (chat-Claude's original caution). Measured no
faster than plain in-place *and* allocating (72 B/op, 2 allocs/op) because
`*frame` escapes to the heap. If Go's escape analysis improves, the
benchmark is sitting in `internal/interp/dispatchbench` to be re-run — the
gate has a documented bar rather than an impression.

---

*Deliberation as written before the decision follows, unedited.*

## Context

Contract §10.1 leaves three coupled choices open: **program representation**
(in-place interpretation vs internal-form rewrite), **dispatch** in Go
without guaranteed TCO, and **value representation**. This doc argues all
three and asks Scott to decide.

chat-Claude's stated lean, recorded so it can be engaged rather than
inherited: in-place with side tables; boring giant-switch dispatch for v0;
bare `uint64` slots leaning on validation-established types. It also said
*measurements outrank the reviewer*. So I measured before arguing, and the
measurement moved me off one of the three.

The measurement is a repo artifact, not a claim:
`internal/interp/dispatchbench` (see its README). Four strategies over the
same toy loop, positive-control tests first — all four compute 500500 before
any timing is quoted.

**Apple M4 Pro, darwin/arm64, Go 1.26.5, `-count=6`, median ns/op:**

| variant | single-byte imms | multi-byte imms | alloc |
|---|---|---|---|
| Rewrite (`[]ins{op,imm}`) | **~11.9 µs** | **~11.9 µs** | 0 B/op |
| In-place (decode LEB every time) | ~17.3 µs | ~19.5 µs | 0 B/op |
| In-place + side table | ~21.9 µs | ~21.9 µs | 0 B/op |
| Closure compilation | ~21.5 µs | — | **72 B/op, 2 allocs** |

Three findings, two of them against expectation:

1. **Rewrite is ~1.5–1.7× faster than in-place** and, unlike in-place, is
   immune to immediate width — its advantage *grows* as real modules use
   larger indices and constants.
2. **The side table is slower than plain in-place** (~21.9 vs ~17.3 µs).
   This is the surprise, and it kills the "in-place with side tables"
   compromise on its own terms. The table is `len(code)`-sized and sparse,
   so the hot loop touches two cache lines per instruction (`code[pc]` and
   `tbl[pc]`) instead of one, and the memory traffic costs more than the LEB
   decode it saves. Pre-decoding immediates only pays if it also *compacts*
   the representation — which is what the rewrite does and the side table
   doesn't.
3. **Closure compilation is not a speedup in Go** — it matched in-place and
   allocated. `*frame` escapes to the heap because the closures capture it,
   so every dispatch is an indirect call through a heap pointer with no
   inlining. This confirms chat-Claude's instinct to treat it as a *measured
   later gate*, not a founding assumption; the measurement says the gate
   would currently fail.

Caveats stated plainly: one toy loop, one architecture, ~13 instructions,
no calls, no memory ops, no traps. It does not predict the real engine. It
is sufficient to rank the four on the *specific* axis that separates them —
immediate handling and dispatch overhead — and to retire two of them.

## Question 1 — program representation

### Option A: in-place interpretation (Wizard-style)
Bytecode is the program; `pc` is a byte offset; side tables carry what
can't be recomputed cheaply.
- **+** No translation layer to maintain per proposal. This is the real
  argument, and it is a *spec-tracking* argument, not a speed one: when a
  new proposal lands upstream, you extend a decoder and a switch, not a
  decoder, an internal form, a lowering, and a switch.
- **+** Nothing to invalidate; module bytes stay authoritative, which suits
  §9's "spec-edge agility is the product".
- **−** Measured slowest-but-one, and the side-table variant that was
  supposed to fix that measured *slowest*.
- **−** Structured control flow (`block`/`loop`/`if`/`br`) needs branch
  targets resolved to byte offsets anyway — so a side table is not optional,
  it is mandatory, and we just measured its cost.

### Option B: internal-form rewrite
Decode once into `[]ins` with pre-decoded immediates and resolved targets.
- **+** Measured fastest, and width-immune.
- **+** Branch resolution, which in-place needs regardless, becomes free —
  it is just an index fixup at build time (the benchmark does exactly this).
- **−** A second representation to extend for every proposal. The
  maintenance cost §10.1 is worried about is real.
- **−** Two places for a bug to hide: decoder-to-internal-form lowering is
  a new surface the spec suite tests only indirectly.

### Option C: in-place now, rewrite behind a gate later
- **−** Rejected. The two differ in their *hot loop's* shape, so this is not
  a gate you flip; it is two interpreters. §9 gates are for proposals, not
  for a fork of the core.

### Recommendation on Q1: **Option B, internal-form rewrite** — with a caveat I want Scott to weigh.

I am arguing against chat-Claude's lean, and the honest version is that the
speed number is *not* what moves me. 1.6× on a toy loop is not worth
contradicting the reviewer, and §0 says correctness and agility are the
product.

What moves me is that **in-place does not actually avoid the second
representation.** Structured control flow forces a side table with resolved
branch targets, plus per-`pc` metadata for block arity during validation.
Once that exists, "in-place" means *bytecode plus a sparse side structure*,
and the maintenance burden per new proposal is paid in both designs — you
extend the decoder and the side-table builder either way. The rewrite pays
the same conceptual cost in a form that is denser, faster, width-immune, and
easier to debug (one array, one index, printable). The side-table measurement
is the evidence that splitting the program across two arrays is the worst of
both: you keep the maintenance cost *and* lose the speed.

The counter-argument, which is legitimate and is Scott's to weigh: Wizard's
in-place design is a published result with real engineering behind it, and
its win is specifically *fast startup and low memory on large modules* —
neither of which this benchmark measures at all. If Burroughs' eventual
workload is many short-lived modules, in-place may win on the axis I did not
test. I would rather be overruled here on that basis than have it unsaid.

## Question 2 — dispatch

### Options
- **A. Giant `switch` on opcode.** Portable, debuggable, zero codegen,
  `go vet`-clean. Go compiles a dense switch to a jump table.
- **B. Closure compilation.** Measured: no faster, and it allocates.
- **C. Generated Go code** (per-opcode functions, `go:generate`). A build
  step and a large surface, for unmeasured benefit.
- **D. Computed goto / tail calls.** Not available: Go has no computed goto
  and no guaranteed TCO. This is the constraint §10.1 names.

### Recommendation on Q2: **Option A, giant switch, for v0.** Agreeing with chat-Claude.

The measurement supports the boring choice: B is not a speedup in Go today
(heap-escaping frame, indirect calls, no inlining), and C is unmeasured
complexity. A stays the baseline until a *measured* proposal beats it behind
its own §9 gate — and this benchmark is the harness that proposal must beat.
Recording the number now means the future gate has a documented bar rather
than an impression.

## Question 3 — value representation

### Options
- **A. Bare `uint64` slots.** Validation establishes types statically, so
  the runtime tag is redundant; `i32` occupies the low 32 bits, floats via
  `math.Float64bits`. `v128` needs two slots or a side array; references
  need a parallel object array (Go pointers cannot live in a `uint64`
  without breaking GC precision — an unsafe cast would hide pointers from
  the collector).
- **B. Tagged struct** (`struct{kind uint8; bits uint64; ref any}`). Wider
  slots, worse cache behavior, and it re-checks at runtime what validation
  already proved.
- **C. Interface-typed values.** Allocation per value. Rejected outright.

### Recommendation on Q3: **Option A, bare `uint64` slots.** Agreeing with chat-Claude — "validation is the type oracle; tags are redundant freight" is exactly right.

One consequence to record now rather than discover later: **references
cannot share the `uint64` stack.** `funcref`/`externref`/GC refs must live in
a parallel `[]ref` (or an index into a side table), because storing a Go
pointer as an integer defeats the garbage collector and pure-Go means we
have no escape hatch. So the stack is *two* parallel arrays with the
validator deciding which one each slot uses. That is a real design
commitment, it lands before WasmGC (§8, M-4), and it is cheap now and
expensive later — hence flagging it in this doc rather than in the one that
adds `ref.null`.

## Consequences if accepted as recommended

1. `internal/interp` gets an `[]ins`-style internal form built from the
   decoder's output; the decoder stays in-place (payloads alias input, as
   today) — **in-place *decoding* and in-place *interpretation* are separate
   choices, and only the latter is being declined.**
2. Giant-switch execution loop; `dispatchbench` becomes the permanent bar
   any future dispatch proposal must clear behind a §9 gate.
3. Value stack: `[]uint64` plus a parallel reference array, types resolved
   by the validator.
4. Blocked on 0003's harness for acceptance evidence: no interpreter claim
   is "green" until `assert_return` runs, which is 0003 phase 2.

## Decisions asked of Scott (all resolved — see Decision, above)

1. **Q1 — the real question.** In-place (chat-Claude's lean, Wizard
   precedent, untested startup/memory advantage) vs internal-form rewrite
   (measured faster and, I argue, does not actually avoid the second
   representation). I recommend rewrite and have argued against the
   reviewer; if the intended workload is many short-lived modules, overrule
   me. → **Rewrite accepted**; the overrule condition was itself a
   disclaimed workload.
2. **Q2 — giant switch for v0.** Recommended; agrees with chat-Claude.
   → **Accepted.**
3. **Q3 — bare `uint64` slots plus a parallel reference array.**
   Recommended; agrees with chat-Claude, with the reference-array
   consequence made explicit. → **Accepted**, consequence pinned above.

## Status

**Accepted** 2026-07-30. Interpreter code may proceed on these three
choices; acceptance evidence remains 0003 phase 2 (`assert_return`), per
"the suite is the oracle".
