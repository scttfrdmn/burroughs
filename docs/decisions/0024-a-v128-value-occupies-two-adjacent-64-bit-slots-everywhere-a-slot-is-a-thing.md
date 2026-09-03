# 0024 — A v128 value occupies two adjacent 64-bit slots everywhere a slot is a thing

Date: 2026-08-11 · Status: **accepted** — stamped by Scott on PR #213, on the ground that the
adverse realistic-frequency bench number is printed with its p-value and correctness overrode it
anyway: measurement informing, soundness deciding, in the right order.

Filed against **#212** (the 0xfd interpreter-execution recon) and the #153 chain's own forced
question, on Scott's framing: this is *the value model's widening, not the stack's* — five
64-bit-scalar storage sites moving together, with a sixth arithmetic invariant (arity/height
counts a slot, not a value) the recon's own first pass did not surface and this ADR's own
drafting found by tracing `control.go`. `internal/interp/*bench` (a new package, named below) is
this decision's measurement instrument where the chosen shape touches the hot path, on
`dropbench`'s own precedent (0023).

## Question

A v128 value is 128 bits. Every numeric storage site in this codebase is a single 64-bit
`uint64` slot, and there are five of them:

| site | file | current shape |
| --- | --- | --- |
| value stack | `interp/value.go` `stack.num []uint64` | one slot per numeric push |
| locals | `interp/value.go` `frame.num []uint64` | one slot per local index, flatly indexed |
| globals | `interp/global.go` `global.num uint64` | one scalar field |
| public API | `interp/value.go` `Value.Bits uint64` | one scalar field, the `Invoke` boundary |
| harness value | `spec/value.go` `Val.Bits uint64` | one scalar field, the conformance-suite boundary |

Decide how a v128 value lives at each site, and how every piece of bookkeeping that currently
assumes "one value = one slot" is corrected once the assumption stops holding.

**This is not five independent decisions being asked at once.** The five sites disagree on shape
today (growable slice, flatly-indexed slice, scalar field ×3) precisely because each was sized to
its own consumer's simplest correct answer for a world with no 128-bit value — and a single
uniform rule ("two adjacent slots") that is sound for the growable-slice site is not
automatically sound for the flatly-indexed one or the scalar ones. The question this ADR answers
is whether one rule can be made to work everywhere, with the local, cost, and comparator
differences priced explicitly at each site, rather than assumed away because the growable-slice
site's answer looks obvious.

## What is already decided, by precedent rather than by this ADR

**`binary.ValType`'s `V128` kind byte (`0x7B`) already exists and is already correctly
classified** (`Kind()` returns it; `IsRef()` correctly routes it to the numeric side,
`module.go:127`, `:300-309`) — the *type*-level representation is done. This ADR is about the
*value*-level storage the five sites above hold, never about what `ValType` itself looks like.

**The wire format already packs a v128 into two 64-bit words, and it is already correct.**
`Instr.Imm0`/`Imm1` hold a v128 immediate's low and high 64 bits respectively
(`binary/instr.go:788-798`, the `immV128` decode arm), little-endian half in each, and
`immLane16`'s sixteen-lane shuffle mask packs identically (`:817-839`). This is a real, running,
tested precedent for "two adjacent 64-bit words, low half first" — not a proposal, a fact about
code that has been correct since grave #100 was fixed.

**`stack.num`'s own doc comment named this shape before any SIMD consumer existed.** Written at
0002's own founding: *"num holds every numeric slot — i32, i64, f32, f64, and v128's two halves
when SIMD arrives"* (`interp/value.go:128-130`). This ADR is not choosing a shape from nothing;
it is confirming a shape 0002 already named is sound, pricing what it costs at the four sites
that are not `stack.num`, and stating the corrections those other four sites need that
`stack.num` alone does not.

## Forced design question 1 — the stack, and 0023's kind-shadow

`stack.num` is a growable slice indexed by push order, so pushing a v128 as two consecutive
`pushNum` calls costs nothing structurally: `st.num[len(st.num)-2:]` is the value the moment both
halves are pushed, and popping it is two `popNum` calls in reverse. This is the site the recon's
first pass measured as "free," and it still is, **provided one more invariant holds that the
first pass did not price**: decision 0023 gave every numeric slot a lazily-activated push
sequence number (`numSeq`) so `drop` can tell, at runtime, which array holds the logical top when
no validator exists to say so statically. Two independent pushes for one v128 value get two
independent sequence numbers under 0023's mechanism exactly as written — which means a `drop`
landing between them, or a `branch` whose truncation height falls between them, sees two
different ages for what is logically one value.

**This is grave #206's exact shape, one layer up, and it is named here rather than discovered in
production.** #206 was: no static signal for which array holds the top, silently wrong, found by
a three-instruction reproducer, fixed by a whole ADR and a bench campaign. Here the failure mode
is symmetric — no signal that two adjacent numeric slots are one v128 rather than two independent
numerics — and the fix is architectural rather than a bench decision: **`stack`'s push/pop API
grows a `pushV128`/`popV128` pair that pushes/pops both halves atomically and assigns them the
same sequence number** (or, equivalently, one sequence number covering both slots, read by
`drop`'s comparison as a single unit). No caller may ever push or pop "half a v128" through the
plain `pushNum`/`popNum` path — the two-slot unit must be indivisible at the API boundary, or the
kind-shadow problem is merely deferred to whichever future opcode arm forgets the pairing.

**The control is: falsify before trusting, per the standing law, and this is the specimen
pre-registered before any code exists.** A test asserting "a v128 push/pop is atomic with respect
to `drop`" must be watched fail under a mutation that pushes/pops the two halves independently
(reproducing #206's shape) before it is trusted — the same discipline `TestDropPicksTheCorrectArray`
already applies to the scalar case, extended one dimension. This ADR names the test's shape now
so the implementing PR does not have to re-derive it: push `(i32, v128, i32)` in that order,
`drop` twice, assert the surviving value is the correctly-ordered numeric — mirroring
`TestDropAfterBranchCarriesSequenceNumbersThroughTruncation`'s own seam-finding technique
(a value whose sequence number must be *strictly between* two others to distinguish a correct
carry from a coincidentally-correct one).

**`branch`/`returnFrom`/`catchThrown`'s existing copy+reslice logic needs no new algorithm for the
bytes**, only for the counts — see forced question 4 below, which is the sharper version of the
same worry stated as an arithmetic fact rather than a tracking one.

## Forced design question 2 — locals, and why the stack's answer does not transfer

`frame.num` is **flatly indexed by local index**: `num[i]` is local `i`'s slot, sized once at
`newFrame(total, ...)` to exactly `total` entries, one per declared local
(`interp/value.go:186-196`, the doc comment's own "both arrays are sized `total` and indexed by
the same flat local index" statement). This is not `stack`'s shape — there is no "push a second
slot for this value" operation available, because a local's index is fixed for the function's
entire lifetime and every `local.get`/`local.set`/`local.tee` site reads or writes exactly
`num[idx]`, never `num[idx:idx+2]`.

**So the stack's free lunch does not transfer, and this is the design force the recon's first
pass flagged but did not price.** A v128 local needs two flat slots for one logical local index,
which means one of:

- **(a) A stride table.** `newFrame` computes each local's *slot offset* rather than assuming
  `offset == index`, growing by one extra slot per v128 local ahead of it. Every `local.get`/
  `local.set`/`local.tee` site needs the offset, not the raw index — a second per-local lookup
  table (mirroring `isRef`'s existing per-index bitmap, `frame`'s own established lazy-allocation
  precedent) rather than a new algorithm, but a real new table that must be built once at
  `newFrame` and consulted at every local access, adding one indirection to the hottest path in
  the interpreter for every function that has *any* v128 local, whether or not the instruction
  touching it is itself v128-typed.
- **(b) A parallel `numHi []uint64`, indexed identically to `num`, holding the second 64 bits for
  exactly the local indices that are v128-typed** (nil/empty for everything else, mirroring
  `refs`/`isRef`'s existing lazy-allocation shape one array over). `local.get`/`.set`/`.tee` for a
  v128 local reads/writes `num[idx]` and `numHi[idx]` as a pair; every other local's access is
  untouched. This keeps `num[i]` == local `i`'s low half for every local, numeric or v128, so the
  *existing* flat-indexing invariant survives unmodified, at the cost of a second bitmap-guarded
  array exactly like `refs`.
- **(c) Route v128 locals through a value-representation escape (e.g. a boxed/pointer form) rather
  than through the numeric array at all.** Rejected on 0002's own founding argument before this
  ADR restates it: a Go pointer inside a slot the collector cannot see is the exact hazard the
  parallel-array split exists to avoid, and boxing a v128 (which is not itself a
  garbage-collected object, just wide) would be manufacturing a GC-precision problem 0002 spent
  its first lines avoiding, to solve a width problem that has nothing to do with tracing.

Priced against each other: (a) touches every local access in every function with a v128 local
(one more table lookup, always paid); (b) touches only v128 locals' own access sites at the cost
of one more nil-checked array (mirroring `refs`, whose lazy-allocation cost model `frame`'s own
doc comment already argues is cheap for the common case). **(b) is the shape this ADR proposes**,
on the direct precedent of `refs`/`isRef` already solving an isomorphic problem — "most locals
don't need this second fact, some do, and the common case must pay nothing" — for the reference
case, and a v128 local is exactly that shape of exception once more.

## Forced design question 3 — globals and the two public boundaries

`global.num`, `interp.Value.Bits`, and `spec.Val.Bits` are each **one scalar field on a struct
that is not an array at all** — there is no "push a second word" operation for any of the three,
unlike `stack`. Each needs either a second field or a variant/union shape. 0002's own `ref` type
is the direct precedent for "grow the struct, not the array": *"a struct rather than a bare `any`
or a pointer, because a reference is *two* facts — whether it is null, and what it points at"*
(`interp/value.go`'s own `ref` doc comment) — a v128 value is symmetrically two facts, its low and
high 64 bits, and the same reasoning that rejected a tagged-union reference type argues for a
fixed two-field addition here rather than a variant.

**Proposed: each of the three structs grows a `Hi uint64` field** (or an internally-named
equivalent — `numHi`, matching (b) above's naming), read only when the value's type is `V128`,
exactly as `Extern`/`RefID` are read only when the value's type is a reference kind
(`interp.Value`'s own existing precedent for "a field meaningful only under one type"). This
costs eight bytes on every global, every public `Value`, every harness `Val` — including the
overwhelming majority that are not v128 — which is a real, small, always-paid cost, priced here
rather than left implicit: unlike `stack`/`frame`, which can gate the cost to the population that
needs it (lazy allocation, nil until first use), a fixed-field struct has no lazy-allocation
option without becoming a pointer or an interface, both rejected by 0002's own founding argument
above. Eight bytes per global/Value/Val is judged acceptable without a bench measurement — these
are not hot-loop-per-opcode structures the way `stack.num` is; a global is allocated once per
declared global, a `Value`/`Val` once per call/vector — but the judgement is stated so a future
reader can see it was priced, not skipped.

## Forced design question 4 — arity and height count slots, and v128 breaks the count-is-value identity

**Found while drafting this ADR, not in #212's own first pass — the recon under-priced this one,
and it deserves its own numbered question because it is an arithmetic fact independent of storage
shape.** `control.go`'s `label.arity`/`label.height` and `call.go`'s `wantNum`/`gotNum` are **slot
counts**, derived from `countByArray` (`control.go:154-163`), which counts **values**:

```go
func countByArray(ts []binary.ValType) (numCount, refCount int) {
	for _, t := range ts {
		if t.IsRef() {
			refCount++
		} else {
			numCount++
		}
	}
	return numCount, refCount
}
```

Every numeric `ValType` contributes exactly 1 to `numCount` today, which is correct for i32/i64/
f32/f64 (one value, one slot) and **wrong for v128** (one value, two slots) the moment `numCount`
is used as a slot count — which is everywhere it is used: `branch`'s `src := len(st.num) -
l.arity` and `copy(st.num[l.height:], st.num[src:])` (`control.go:291-308`), `returnFrom`'s
identical shape (`:344-364`), and `call.go`'s `invoke` comparing `len(st.num)-numBase` against
`wantNum` (`call.go:233-240`). Every one of these reads/writes `st.num` at an offset computed from
a *value* count under the assumption that one value costs one slot — an assumption `needNum`
(`exec.go:1141`) makes too, checking `len(s.num) < n` where `n` is a value count from the calling
arm.

**A block whose type includes even one v128 parameter or result breaks this identity today, and
nothing currently notices**, because no v128 value has ever reached these sites (the whole
`0xfd` region has zero interpreter arms). The fix is not a new mechanism, it is a **correction to
`countByArray`'s own arithmetic**: it must return a slot count, not a value count, for the numeric
side — `numCount` incremented by 2 for a v128 `ValType` and by 1 for everything else numeric. Once
that correction is made, every downstream site (`branch`, `returnFrom`, `invoke`, `needNum`)
continues to operate on slot counts exactly as it does today, because they were never actually
computing "value counts" as their own concept — they were computing "how many entries in
`st.num`," and `countByArray` was the one place that got the entries-per-value ratio wrong for
every type it has ever been asked about, silently correct only because every type asked about so
far has had a ratio of exactly 1.

**This generalizes the finding beyond v128 itself, which is worth stating once here rather than
re-deriving later**: `countByArray`'s contract should be read as "numeric slots consumed," not
"numeric values," and any future value type wider than one slot (there is none on the horizon
past v128 in the tracked proposal set, but the *shape* of the bug — a helper silently encoding an
assumption its own name does not state — is exactly grave #105/#106's class, an echo of the
subject rather than an independent check of it) inherits this same correction automatically if
`countByArray` is fixed at the level of "slots per type" rather than patched with a v128-specific
special case alongside the general loop.

## Forced design question 5 — the harness's per-lane NaN-class matcher

`spec.Val.Matches` (`spec/value.go:265-310`) compares one `Bits` field against one `NaN` field,
scalar. The suite's own SIMD vectors require **independent NaN-class expectations per lane**
inside a single `v128.const` result: `simd_f32x4_arith.wast:732` and dozens of neighboring lines
write `(v128.const f32x4 nan:canonical nan:canonical nan:canonical nan:canonical)`, and other
vectors mix exact-value lanes with NaN-class lanes in the same constant. A single scalar `NaN`
field cannot express "lane 2 must be a canonical NaN, lane 3 must equal this exact bit pattern."

**Proposed: decompose a v128 `Val` into N lane-`Val`s at comparison time, each carrying its own
`Bits`/`NaN`/`Kind`, and call the existing `Matches` once per lane.** This reuses `Matches`
unchanged — no new comparator logic, no new NaN-class machinery — because the per-lane
decomposition is exactly what `readConst`'s own literal reader for `v128.const` already has to do
to parse the source text in the first place (the suite writes lane values as a list, tagged by
shape — `i8x16`/`i16x8`/`i32x4`/`i64x2`/`f32x4`/`f64x2` — and each lane is already parsed as an
independent literal, NaN-class or exact, before this ADR's own work begins). The representation
choice this forces on `spec.Val` itself is therefore not "one `NaN` field becomes an array" but
"a v128-typed `Val` is read, at classification time, as N ordinary scalar `Val`s sharing one
result position" — a structural decomposition at the harness's own read boundary, not a widening
of `Matches`'s comparison logic.

**This is a harness-side fact, and it belongs in this ADR rather than in a separate one because
both `interp.Value` and `spec.Val` are answering the identical "how does 128 bits live in a slot
built for 64" question for two different consumers, and the recon's #153 comment named a
standing intent to fold the harness in "properly this time" rather than let it become a sixth
link discovered late** — the fifth link (0xfd execution) was itself found hiding inside the word
"instantiation" in an earlier framing, and the harness's `readConst`/`Matches` gap is exactly the
kind of thing that framing pattern would hide again if it were left for a future PR to discover
mid-implementation rather than priced here, at design time, alongside the interpreter's own five
sites.

## What was tried and rejected before this ADR settled on two-slot packing

- **A tagged union / `any`-typed slot.** Rejected on 0002's own founding argument, restated
  identically to how `ref`'s own doc comment rejects it for references: a slot whose static type
  is not known to the collector is the GC-invisibility hazard 0002's whole parallel-array split
  exists to avoid, and a v128 is not itself GC-relevant (it holds no pointer), so paying an
  interface's dispatch and allocation cost to solve a pure width problem would be strictly worse
  than the two-slot answer on every axis that matters here.
- **A `[2]uint64` fixed-size field replacing every `uint64` slot, everywhere, unconditionally.**
  Rejected: this widens every i32/i64/f32/f64 slot to 128 bits too, doubling the cost of the
  overwhelming majority of stack/local/global traffic that will never hold a v128 — exactly the
  "pay the tax whether or not the feature is used" failure 0023's own gating discipline exists to
  avoid, at a much larger scale (every slot, not one bookkeeping array).
- **A separate `numV128 []v128Value` array, parallel to `num`/`refs`, holding v128 values as a
  third kind alongside numeric and reference.** Considered seriously — it mirrors 0002's own
  two-array split one more time, cleanly separating "wide" from "narrow" the way "traced" is
  separated from "untraced." Rejected in favor of two-adjacent-slots-in-`num` because it would
  require a *third* independent height/arity/sequence-number bookkeeping system alongside the two
  `control.go` and 0023 already maintain, tripling the truncation-copy surface area
  (`branch`/`returnFrom`/`catchThrown` would each need a third array's worth of the identical
  logic) for a separation that buys nothing here: a v128 is not GC-relevant, so it does not need
  the kind of isolation `refs` earns by being traced. Two slots inside the existing numeric array,
  corrected by forced question 4's arithmetic fix, reuses every truncation site's existing logic
  unchanged rather than growing a third copy of it.

## Decision

**A v128 value occupies two adjacent 64-bit slots, low half first, everywhere a slot already
exists as an array position (the stack); a second parallel array, lazily allocated and
bitmap-guarded exactly like `refs`/`isRef`, everywhere a slot is a fixed local-index position
(locals); and a second fixed field, always allocated, everywhere a slot is a scalar struct field
(globals, the two public value types).** Concretely:

1. **`stack`**: `pushV128(hi, lo uint64)` / `popV128() (hi, lo uint64)`, each pushing/popping both
   halves as one atomic operation with **one shared sequence number** covering both slots — never
   two independent `pushNum` calls. `drop`'s comparison treats a v128 unit's sequence number as
   the number of its *low* slot (the one popped last in a two-slot pop), which is sufficient
   because the two slots are never separated by any other operation once pushed atomically.
2. **`frame`**: a new `numHi []uint64` array, nil until the first v128 local is declared, sized
   identically to `num` and read/written only at v128-typed local indices — `isV128`'s own bitmap,
   built once at `newFrame` alongside the existing `isRef` bitmap, for the identical
   "ask once, not per access" reason `isRef` already gives.
3. **`global`**, **`interp.Value`**, **`spec.Val`**: each grows a `Hi uint64` field, read only
   when the value's declared type is `V128`, mirroring `Extern`/`RefID`'s existing
   "meaningful only under one type" precedent on `interp.Value`.
4. **`countByArray`** is corrected to count **slots consumed**, not **values**, for the numeric
   side — a v128 `ValType` contributes 2, every other numeric type contributes 1 as it does
   today. No call site downstream of it (`branch`, `returnFrom`, `invoke`, `needNum`) changes,
   because they were already operating on slot counts; only the one place that computed the
   ratio incorrectly is fixed.
5. **`spec.Val`**'s comparator gains no new logic; a v128 result is decomposed into per-lane
   scalar `Val`s at the harness's own classification boundary (`readConst`'s eventual v128 arm),
   and `Matches` is called once per lane, unchanged.

**Benched, per 0023's own precedent, because slot 1 (the stack) touches the interpreter's hottest
path — and measured before this decision's text was finalized, not merely planned, with every
number below run twice independently before being trusted.** `internal/interp/vecbench` (new
package, `dispatchbench`/`dropbench`'s identical access-pattern discipline) compares the atomic
`pushV128`/`popV128` design against the naive two-independent-`pushNum`-calls alternative this ADR
rejects on correctness grounds (forced question 1). **The finding reverses by workload shape, and
both directions are real, reproduced, and reported — not reconciled into one number:**

| comparison | result (two independent runs each) |
|---|---|
| AtomicV128 vs NaiveV128, every-iteration v128 traffic (`benchtime=2000x`, n=10) | **−25.30%** (p=0.000), **−22.84%** (p=0.002) — Atomic faster |
| AtomicV128 vs NaiveV128, ~1% v128 frequency, realistic (`benchtime=5000x`, n=10) | **+4.68%** (p=0.023), **+5.01%** (p=0.005) — Atomic slower |

> **Protocol note, added by [grave #612](https://github.com/scttfrdmn/burroughs/issues/612). The
> figures above are left as written; this note is the pointer.** Both rows were measured through
> `make bench`, which could not express a two-arm A/B — a hardcoded output file, a comparison printed
> as a suggestion nothing ran, a `benchstat` over one file — so the arms are benchmark rows in one
> binary, run consecutively rather than interleaved. Grave
> [#552](https://github.com/scttfrdmn/burroughs/issues/552)'s protocol controls for that and is now
> executable as `make ab`. **The extreme-traffic row is safe** at −25.30%/−22.84%. **The
> realistic-frequency row is the one landed figure in this tree sitting inside the drift envelope:**
> +4.68% and +5.01% against the 4.1–9.1% that #552 measured on unchanged code fifteen minutes apart
> on this class of machine. Its two agreeing runs do not rescue it — *reproduction under the broken
> protocol is not reproduction* is #552's own third rider, and two sequential-arm runs share the
> confound rather than cancelling it. So the *direction reversal by workload shape*, which this
> section reports as its finding, is the claim that is exposed; the correctness argument in forced
> question 1 that actually decided the ADR does not rest on it. Whether the row is re-measured is
> Scott's call and is not decided here.

**Both rows are resolved (p<0.03 in all four runs) and both are honest — the realistic-frequency
row did *not* resolve at the smaller `benchtime=2000x` first tried (p=0.09–0.35, the environmental
"stalls and sprints" noise this project's own fuzz-smoke lesson already characterized for this
hardware), and the fix was the sharpened lesson's own remedy: budget by the quantity the purpose
names, more executions per sample, not a longer wall-clock guess. At `benchtime=5000x` the
realistic-frequency comparison resolves cleanly in both independent runs, in the *opposite*
direction from the every-iteration extreme.**

**The mechanism for each direction is visible, not mysterious, and the two do not contradict each
other — they measure different things.** At every-iteration frequency, `pushV128`'s one
`append(s.num, hi, lo)` call (a single slice-growth check, amortized over two slots) beats the
naive shape's two independent `append` calls (two growth checks, each capable of triggering its
own reallocation) — batching wins when v128 traffic dominates the loop. At realistic ~1%
frequency, that push-side saving is diluted by 99 non-v128 iterations that cost identically either
way, while `popV128`'s own shape (`lo = s.popNum(); hi = s.popNum()`, `bench_test.go:230-234`) adds
one function-call layer over the naive path's two flat `popNum` calls at the call site — a real,
small, structural cost from the extra indirection that the every-iteration benchmark's higher
operation density evidently amortizes differently (plausibly warmer inlining/branch-prediction
state) than the diluted realistic case does. **Neither number is the "real" one and the other
noise; they are the correct answer to two different questions**, and the ADR states both because
suppressing either to report a single figure would be exactly the second-order-honesty failure
this project's own discipline exists to catch.

**Consequence for the decision: the atomic design is chosen on correctness, and the realistic-case
cost it carries (≈5%, on stack bookkeeping that is itself a small fraction of an opcode's total
work) is accepted rather than argued away, on 0023's own precedent for accepting a real, measured,
small-percentage cost in exchange for a correct answer** (0023 accepted a much larger figure, ~28%
on the zero-reference population, for the identical reason). The two designs' costs are close
enough, and the naive design is definitionally unsound (forced question 1), that no plausible
realistic-case number would flip this decision — but the number is recorded rather than assumed
small, so a future reader sees what was actually measured rather than what would have been
convenient. `popV128`'s own shape can be revisited if a future profile shows the call-layering
cost matters at the population this engine actually runs (real modules, not the synthetic access
pattern above) — inlining `popNum`'s body directly into `popV128` is the obvious next experiment
and is not attempted here, on the standing rule that a plausible optimization not yet needed is
premature generality until a real workload asks for it.

Slots 2–5 are not benched: `frame`'s `numHi` mirrors `refs`' own unbenched lazy-allocation
precedent (accepted without a dedicated bench when it landed, on the argument that it is allocated
only for the population that needs it), and slots 3–5's fixed eight-byte-per-struct cost is judged
acceptable without measurement per forced question 3's own stated reasoning — these are
per-global/per-call costs, not per-opcode ones, and 0002's own `make bench` discipline is reserved
for the hot loop, not every struct that grows a field.

## Options considered

- **A — two adjacent slots everywhere, uniformly (i.e. treat `frame`/`global`/`Value`/`Val` as if
  they were growable arrays too).** Rejected for `frame`: flat indexing has no "push a second
  slot" operation, so "uniformly" would mean re-deriving every local's offset from a stride table
  regardless of whether any local is v128-typed — forced question 2's option (a), rejected there
  in favor of (b) for its always-paid cost on every local access.
- **B — a tagged union / `any` slot everywhere.** Rejected; see "What was tried and rejected."
- **C — universal `[2]uint64` widening of every slot.** Rejected; see "What was tried and
  rejected."
- **D — a third parallel array (`numV128`) alongside `num`/`refs`.** Rejected; see "What was tried
  and rejected."
- **E — the chosen shape (two adjacent slots on the stack, a lazy parallel array on frames, a
  fixed field on scalars, a corrected slot-counting helper, a harness-side lane decomposition).**
  Chosen: each site's answer is the cheapest sound shape *for that site's own existing structure*,
  reusing an established precedent at every site (`Instr.Imm0`/`Imm1`, `refs`/`isRef`,
  `Extern`/`RefID`, `readConst`'s existing per-lane literal parsing) rather than inventing a new
  mechanism, and it is the only option among A–D that leaves every non-v128 access path in every
  one of the five sites completely unchanged in cost.

## What this does not decide

- **Any 0xfd opcode's own arithmetic** — this ADR is the storage the arms will read and write;
  which arm computes what is #212's ladder, starting with the whole-vector-bitwise family once
  this lands.
- **The relaxed-SIMD `(either …)` non-determinism matcher** — a separate, smaller harness widening
  (#212's own recommendation 2's last bullet), unrelated to how a v128 value is stored.
- **The SIMD gate flip's own procedure** — still parked on #153, unrelated to representation.
- **`select`'s `Imm0`-bit dispatch** — has its own static signal already and is untouched by this
  ADR, on the identical carve-out 0023 itself states for the same instruction.
- **Whether a future value type narrower or wider than 128 bits would fit this same two-field/
  lazy-array pattern** — forced question 4's generalization note observes that the *shape* of the
  fix (correct the slot-count helper, not the call sites) would transfer, but this ADR does not
  commit to a mechanism for a type that does not exist in the tracked proposal set.

## Consequences

- **`stack` gains `pushV128`/`popV128`**, and every existing `pushNum`/`popNum` caller is
  unaffected — no numeric arm's code changes, because a v128 is pushed/popped only by the arms
  this ADR's own implementation adds in #212's ladder, none of which exist yet.
- **`frame` gains `numHi []uint64` and an `isV128 []bool` bitmap**, allocated lazily at `newFrame`
  exactly as `refs`/`isRef` already are — a function with no v128 parameter or local pays nothing
  beyond one more nil slice and one more bitmap check per local access, mirroring the existing
  `isRef` cost model precisely.
- **`global`, `interp.Value`, `spec.Val` each grow one `Hi uint64` field** — eight bytes per
  instance, always allocated, judged acceptable per forced question 3's stated reasoning.
- **`countByArray` changes its per-type contribution for `V128` from 1 to 2** — every downstream
  site (`branch`, `returnFrom`, `invoke`, `needNum`) is corrected by this one change alone, with
  no site-specific edit required, because they already treated its output as a slot count.
- **`spec.Val`'s `readConst` gains a `v128.const` arm that decomposes into per-lane scalar `Val`s**
  rather than growing `Matches`'s own comparison logic — the sixth link folded in at design time
  rather than discovered mid-implementation.
- **`internal/interp/vecbench` enters the tree** as the stack-widening's measurement instrument,
  on `dispatchbench`/`dropbench`'s own precedent: the next author touching this mechanism has a
  benchmark already built to re-run rather than a number to trust from a stale comment.
- **A new falsifiable control is pre-registered before any opcode arm is written**: a v128
  push/pop's atomicity with respect to `drop`'s sequence-number comparison, watched fail under a
  mutation that separates the two halves' sequence numbers before being trusted — grave #206's
  shape, caught at design time rather than found in production a second time.

## Amendment, 2026-08-22 — this ADR's four location citations have drifted, and the drift is measured

Appended rather than rewritten, per *a ruling is discharged by appending to the ADR, body preserved*.
The body above is unchanged, including the four citations, because re-pointing a number in a dated
record rewrites the record rather than repairing it — the conversion question is
[#497](https://github.com/scttfrdmn/burroughs/issues/497)'s and Scott's. What this note adds is the
**measurement**, so a reader following one of them knows it is a snapshot and by roughly how much it
has slipped.

| cited as | what the body says it is | declared at | drift |
|---|---|---|---|
| `control.go:154-163` | `countByArray`, whose definition the body quotes inline | 178 | ≈ +24 |
| `control.go:291-308` | `branch`'s `src :=` / `copy(st.num[l.height:], st.num[src:])` | 334–342 | ≈ +43 |
| `:344-364` | `returnFrom`'s identical shape | 430 | ≈ +86 |
| `exec.go:1141` | `needNum`, checking `len(s.num) < n` | 1475 | ≈ +334 |

**All four, not three.** An earlier report on [#502](https://github.com/scttfrdmn/burroughs/pull/502)
called the first one *arguable* on the ground that lines 154–163 contain calls to `countByArray`. That
reading does not survive the body: the citation is immediately followed by a fenced quotation of the
function's **definition**, so the definition is what it points at. The correction runs against my own
interest, which is why it is recorded here — *a favourable reading banked without being asked why* is
how a self-correction turns a true statement into a false one.

The third row is a **bare continuation citation** — no file part at all — resolvable as a `control.go`
reference only through its antecedent in the row above. It is counted and checked here on that basis.
(Described rather than quoted a second time: the table already carries the coordinate, and a repetition
would put another row in the population this note is measuring for no gain in what the sentence says.)

### What this note does **not** do

It does not retract a stability claim, because **this ADR never made one**. The claim that holding
`internal/interp/control.go` at 611 lines protected these citations was made in #502's report, not
here, and the amendment is filed against this file only because this file is where the drifted
citations live. Scott's ruling on the #502 review named the general form — *an unmeasured stability
claim is not a protection; it was a hope with a number attached* — and it is recorded in
[boards-and-buckets.md](../laws/boards-and-buckets.md#an-unmeasured-stability-claim-is-not-a-protection)
with #502's line-count invariant as its specimen. Attributing it to this ADR would be the drifted
citation defect one level up: a correction filed against the wrong author.

Of the drift above, **+6 is #502's** — its six-line insertion into `internal/interp/exec.go` moved
`needNum` from 1469 to 1475. The rest predates that branch. That distinction matters and is the reason
the amendment states offsets rather than a verdict: *a citation you broke this hour is not evidence
about the class the issue was filed for*, so #497's population should not be credited with drift its
own authors did not cause.
