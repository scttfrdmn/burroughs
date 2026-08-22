# 0028 — Relaxed SIMD lowerings are deterministic and architecture-uniform: the reference's choice, taken once, as a guarantee exceeding the spec

Date: 2026-08-12 · Status: **accepted in full** — decisions 1, 4 and 5 on Scott's ruling on #275;
decisions 2 and 3 taken by the actor under the license that ruling grants, and **stamped without
veto on the PR #276 relay** (recorded in *Second stamp* at the foot of this record). The interval
those two spent open — from authoring to the relay, one session — is kept rather than erased,
because a status field is a citation to an approval and the citation must name *which* approval and
*when* it arrived.

> **Stamp — decisions 1, 4 and 5: Scott, ruling on #275's recon (relayed in session 2026-08-12,
> recorded as a comment on #275 so this citation resolves to an artifact).** Verbatim in the issue;
> the operative clauses are that Burroughs' relaxed lowerings are *"deterministic and
> architecture-uniform, as a stated guarantee exceeding the spec"*, that *"the widening rides the
> first arm family's PR"*, and that *"the four zero-vector opcodes land with author-supplied
> witnesses pinned to the reference, because arms the suite cannot watch don't get to land
> unwatched"*.
>
> **The license, and why decisions 2 and 3 are the actor's rather than flagged back:** the same
> ruling says *"Where the spec permits a set, we pick once, uniformly, and write down why."* That
> is an instruction to choose per opcode and record the grounds, so choosing is the work rather
> than a decision taken on a principal's behalf — and per *the actor never chooses the instrument
> that judges the actor*, the distinction holds because these are judgements about the work and
> not about the actor. They are named here, in the PR body, and challengeable; Scott's veto
> stands. Decision 3 in particular is a *measured* rather than argued choice, and the measurement
> is below.
>
> **This document was authored `accepted` rather than `proposed`, which is a departure from 0026
> and 0027 and is deliberate:** those two were authored *before* their stamps and the interval is
> kept in their records. Here the stamp preceded the document — the ruling shaped the core decision
> and then ordered the ADR — so there is no interval to keep, and marking it `proposed` would be a
> fabricated citation in the opposite direction: a status claiming an approval is pending when it
> has been given.

Filed against **#275** (the relaxed-SIMD recon) and milestone **v0.x gates**, label
**`gate:relaxed-simd`**. Downstream of **0024** (a v128 occupies two adjacent 64-bit slots) and of
grave **#223**, which is this decision's standing precedent rather than merely a citation.

## Question

Relaxed SIMD is the first family this engine has met where **conformance does not determine
behaviour**. The proposal permits a *set* of results per instruction, and the suite says so out
loud in two ways that #275 measured:

- **32 of the 37 readable all-on failures assert only that the engine agrees with itself.** The
  corpus is built around `*_cmp` exports that apply the instruction twice to the same operands and
  compare the two results (`relaxed_min_max.wast:9-12`). Exactly **4** vectors pin an actual value,
  all in `relaxed_dot_product.wast` (`:18`, `:24`, `:41`, `:49`), on 2 of the 16 opcodes that have
  vectors at all.
- **The value content is 32 `(either …)` vectors the harness cannot read** — 32 occurrences
  corpus-wide, matching the `unsupported` count one for one per file. `internal/spec/wast.go:1253`
  records the form as **0** answerable.

Contract §0 is *correctness-neutral*, which says **conform**; here conforming leaves a choice, so
§0 is silent on the thing that has to be decided. §1's non-goals do not name it either. The
question is therefore not "what is the right answer" — several answers are right — but **who
chooses, on what grounds, and how uniformly**.

**And the proposal has already answered a weaker version of the question, which is what makes
"exceeding the spec" a measurable claim rather than a flourish.** From the spec text the proposal
quotes for itself (`third_party/spec/proposals/relaxed-simd/Overview.md:64-69`):

> Some operators are host-dependent, because the set of possible results may depend on properties of
> the host environment (such as hardware). Technically, each such operator produces a fixed-size list
> of sets of allowed values. For each execution of the operator in the same environment, only values
> from the set at the same position in the list are returned, i.e., **each environment globally
> chooses a fixed projection for each operator.**

That is exactly what the 32 `*_cmp` vectors test — a fixed projection is what makes two applications
of one instruction to one pair of operands agree — and it is *precisely* the guarantee decision 1
strengthens. The spec requires the projection to be fixed **per environment**; Burroughs promises the
same projection in **every** environment. So the delta between conformance and this ADR is one word,
and the vectors that would catch its violation do not and cannot exist: no conformance suite can
compare two hosts.

Four sub-questions, each with a real fork:

- **Q1 — what determines the per-opcode choice?**
- **Q2 — is the choice a guarantee, or an implementation detail free to vary?**
- **Q3 — do the arms land before the `(either …)` widening, or with it?**
- **Q4 — what watches the four opcodes the suite does not score at all?**

### The measurement that shaped Q2, and it is not a hypothetical

Go's own specification licenses the compiler to fuse a floating-point multiply-add, and **the two
architectures this project targets differ on whether they take the licence.** Measured with one
source file, the same input bit patterns, `//go:noinline` on each form, and a positive control
requiring a *discriminating* triple to be found before anything is reported (three found at each
width):

| form | arm64 (dev box, native) | amd64 (`--platform linux/amd64`, Go 1.26) |
| --- | --- | --- |
| `x*y + z` | **fused** — equals `math.FMA` | **unfused** — equals `float64(x*y)+z` |
| `float64(x*y) + z` | unfused | unfused |
| `math.FMA(x, y, z)` | fused | fused |

Specimen, f64, all three samples agreeing: `x=0xbff3bd7936ff4ab8 y=0xbfc02c27bd32e7e4
z=0xbfe0abfcce8fe1e6` gives `math.FMA` = `0xbfd75dfff752495b` and `float64(x*y)+z` =
`0xbfd75dfff752495c`; the bare `x*y+z` produces the **first** on arm64 and the **second** on amd64.
The same split holds for `float32`, which is the width three of the four madd/nmadd opcodes use.

This is **grave #223's shape one layer deeper**. There, Go's `math.Min`/`math.Max` carried
architecture-specific NaN handling in stdlib assembly and cost 80 accidental fails, invisible on the
arm64 dev box. Here it is not a library to route around but the *language* permitting fusion —
so no amount of care about which functions to call would catch it, and the remedy has to be the
spec's own idiom. Go's specification names both directions explicitly: fusion is allowed for
`r = x*y + z` and **disallowed** for `r = float64(x*y) + z`, because an explicit conversion rounds
to the target's precision.

**Negative, recorded so it is not re-looked-for:** the engine has no floating-point multiply-add
today. Float multiplications exist (`internal/interp/simd.go:558`, `:575` — `f32x4.mul`,
`f64x2.mul`) and none is summed in the same expression, so this decision creates a forward-looking
rule with **zero existing violations** rather than repairing a live defect. The positive control on
that negative is the pair of `mul` sites just cited: the search that found no multiply-add did find
the multiplications, so the channel was open — the zero is a fact about the code and not about a
filter that excluded everything.

### What the reference already settled, read rather than recalled

`third_party/spec` at the pinned `bdd7164`. All twenty relaxed opcodes are executed, and **each one
is mapped to a named deterministic choice** in `interpreter/exec/eval_vec.ml`:

| opcode | mnemonic | `eval_vec.ml` | the choice |
| --- | --- | --- | --- |
| `0x100` | `i8x16.relaxed_swizzle` | `:64` → `V128.V8x16.swizzle` | the non-relaxed swizzle (out-of-range index → 0) |
| `0x101`–`0x104` | `i32x4.relaxed_trunc_*` | `:211-214` → `I32x4_convert.trunc_sat_*` | the `_sat` family — saturating |
| `0x105`, `0x107` | `f32x4`/`f64x2`.`relaxed_madd` | `:130`, `:132` → `fma` | **fused** |
| `0x106`, `0x108` | `f32x4`/`f64x2`.`relaxed_nmadd` | `:131`, `:133` → `fnma` | fused, first operand negated (`v128.ml:238`) |
| `0x109`–`0x10c` | `*.relaxed_laneselect` | `:134-137` → `V1x128.bitselect` | bitwise, not top-bit-only |
| `0x10d`–`0x110` | `f32x4`/`f64x2`.`relaxed_min`/`max` | `:113-114`, `:123-124` → `min`/`max` | the non-relaxed min/max |
| `0x111` | `i16x8.relaxed_q15mulr_s` | `:84` → `I16x8.q15mulr_sat_s` | saturating |
| `0x112` | `i16x8.relaxed_dot_i8x16_i7x16_s` | `:85` → `I16x8_convert.dot_s` | both operands sign-extended as **i8**, wrapping i16 sums |
| `0x113` | `i32x4.relaxed_dot_i8x16_i7x16_add_s` | `:138` → `I32x4_convert.dot_add_s` | sign-extended i8 → i32, four products per lane, wrapping add of `z` |

Two facts fall out of that table and both are load-bearing below. **Fourteen of the twenty are the
non-relaxed counterpart**, which this engine already implements. And the reference's `fma`
(`interpreter/exec/fxx.ml:152-157`) is OCaml's `Float.fma` — one rounding — **through the shared
functor**, so for `f32` it widens to double, fuses there, and narrows back. It also canonicalizes a
NaN result through `determine_binary_nan x y`, consulting the two multiplicands and **not** the
addend.

### The permitted sets, and the reference's choice checked against them

The reference is an *executable* authority — it says what one implementation does. The proposal
document is a *normative* one: it states the permitted set per instruction as
`IMPLEMENTATION_DEFINED_ONE_OF(…)` pseudocode (`Overview.md:71-307`, the whole `## Instructions`
section, whose opening line defines the notation). Having two authorities means the
adoption in decision 2 can be checked rather than assumed, so it was — by reading the sets against the
mappings, opcode by opcode:

| instruction | permitted set | reference's choice | inside? |
| --- | --- | --- | --- |
| swizzle | index 16–127 → `ONE_OF(0, a[s%16])`; ≥128 → `0` | `0` throughout | yes |
| trunc, signed | NaN → `ONE_OF(0, INT32_MIN)`; overflow high → `ONE_OF(INT32_MIN, INT32_MAX)` | `trunc_sat`: `0`, then `INT32_MAX` | yes |
| trunc, unsigned | NaN → `ONE_OF(0, UINT32_MAX)`; negative → `ONE_OF(0, UINT32_MAX)` | `trunc_sat`: `0`, then `0` | yes |
| madd / nmadd | two roundings, **or** one (fused) — a two-member set | `fma` — fused | yes, and see below |
| laneselect | partial mask → `ONE_OF(bitselect(…), a\|b by top bit)` | `bitselect` throughout | yes |
| min / max | NaN or a ±0 pair → `ONE_OF(a[i], b[i])` | the non-relaxed `min`/`max` | yes on ±0; NaN qualified below |
| q15mulr_s | both `INT16_MIN` → `ONE_OF(INT16_MIN, INT16_MAX)` | `q15mulr_sat_s` — `INT16_MAX` | yes |
| dot_s | high-bit lane → `ONE_OF(signed, unsigned)`; pair sum → `ONE_OF(wrap, saturate)` | signed, wrapping | yes |
| dot_add_s | as `dot_s`, plus the accumulate | signed, wrapping | yes |

**Every one is inside its set, with two qualifications the reading could not settle** — and naming them
is the point of doing the check at all.

**The smaller one, min/max on a NaN operand.** The permitted set is `ONE_OF(a[i], b[i])`, and the
non-relaxed `min`/`max` return a NaN that the reference *canonicalizes* rather than one of the two
operands bit for bit. Whether a canonical NaN counts as "one of `a[i]`, `b[i]`" is a question the
pseudocode does not ask, and in practice it does not arise on the board: `assert_return` matches NaN
results through `nan:canonical`/`nan:arithmetic` patterns rather than exact payloads, so the corpus
cannot distinguish the readings. Recorded as a qualification rather than a clean "yes", because a
check reported as cleaner than it was is the defect this whole cross-check exists to avoid.

**The larger one, `f32` madd.** The permitted set has exactly two members:
`a*b` rounded to f32 then the add rounded to f32, or the whole expression rounded **once** to f32. The
reference does neither *by construction*: it widens to f64 (where `a*b` on f32 inputs is exact), fuses
and rounds once to f64, then rounds again to f32.

**The engine is already committed to that route, which is what makes the question precise rather than
alarming.** `vecBinaryFloat` (`internal/interp/simd.go:1431-1454`) computes *every* f32x4 float
operation by widening both lanes to `float64`, applying the operation there, and narrowing with
`float32(…)`. For the binary operations that is not a shortcut but a theorem: double rounding through a
53-bit intermediate is innocuous for a 24-bit target when the intermediate carries at least `2p+2` = 50
bits, so `f32x4.mul` and friends are exactly right by that route. **The FMA is ternary, and the
classical theorem is stated for the basic operations rather than for a fused multiply-add.** So the
narrow open question is whether the innocuousness extends to this one case, and *I have not verified it
and do not assert it either way.*

What it is not is a reason to deviate. Go has no `float32` FMA, so the widen route is the only
implementation available without hand-rolling one; it reproduces the reference bit for bit; and it is
the convention every other f32 arm in this file already follows. The question is filed with a tripwire
rather than resolved in prose — see decision 3.

## Options

### Q1 — what determines the per-opcode choice

**(a) Cheapest pure Go.** Thesis-shaped on its face: performance-partisan, no CPU-feature
dependency. Against it — "cheapest" is not well-defined across two arches for exactly the ops in
question (a fused form is one instruction on arm64 and, at baseline `GOAMD64=v1`, a software routine
on amd64), so the criterion silently invites a per-arch answer, which is the thing being ruled out.

**(b) The reference interpreter's choice.** It is an *authority* rather than an invention, which is
the standing preference wherever prose and an executable disagree. It makes 14 of 20 arms aliases to
arms this engine already has, so uniformity is largely **inherited** rather than re-argued — and
inherited from arms that were already made uniform, `floatMin`/`floatMax` being #223's own repair.
And it is the conservative reading of the `(either …)` lists: those enumerate the answers real
implementations give, so a *third* answer — a true f32 FMA where the reference double-rounds — risks
failing a vector that permits two, which would be the worst available outcome.

**(c) The host hardware's natural answer, per architecture.** What the proposal exists *for*, and
what a future compiler backend would want. Rejected under decision 1; see there.

### Q2 — guarantee or implementation detail

**(a) A stated guarantee.** Costs the freedom to take the fast per-arch path later without a
governance event. **(b) An implementation detail.** Cheaper now, and it is what an engine gets by
saying nothing — which is precisely how #223 happened.

### Q3 — order

**(a) Arms first.** Buys 37 fails immediately, of which 32 is green a wrong lowering earns equally.
**(b) Widening first.** Moves no board figure until an arm exists. **(c) Together.**

### Q4 — the four unwatched opcodes

**(a) Land them, note the gap.** **(b) Author witnesses pinned to the reference.** **(c) Defer the
four arms until the suite grows a vector** — which would leave a decoded opcode with no arm and is
not really available.

## Decision

### 1. Relaxed lowerings are deterministic and architecture-uniform, as a stated guarantee exceeding the spec — Q2(a), Q1 rejecting (c)

*Scott's ruling.* The engine picks one answer per relaxed instruction and produces **that** answer
on every platform, and this is promised rather than merely true today.

The grounds §0 cannot supply, §1's thesis does: **correctness-neutral, performance-partisan extends
to the engine itself — same answer everywhere.** And the decisive argument is about the
*instrument* rather than about the semantics: the dual-platform board is only honest because both
arches answer alike, so per-arch relaxed results would make the project's own conformance
measurement architecture-dependent by design. #223's eighty accidental fails are the standing
precedent for what that costs — and they were *accidental*, which is the point: the alternative
here is to do deliberately the thing that has already gone wrong by omission once.

Note what this gives up, so it is not discovered later as a surprise: relaxed SIMD's entire purpose
is to let an implementation take the hardware's cheap path, and this decision declines that for the
engine's own uniformity. §1 disclaims peak throughput parity, so the trade is on-thesis; but a
future compiler backend that wanted the fused-where-fast behaviour would be **changing a stated
guarantee**, which is a governance event and not an optimization. That consequence is the price of
the promise and it is accepted knowingly.

### 2. The per-opcode choice is the reference interpreter's, opcode by opcode — Q1(b) · *actor's decision under the ruling's license*

The twenty mappings in the table above are adopted verbatim, each arm citing its `eval_vec.ml` line.
Three grounds, in order of weight:

1. **It is an authority, not a preference.** Every other accept-direction fact in this engine is
   settled by reading the reference; a choice the suite cannot score is the *purest* case for that
   discipline, not an exception to it.
2. **It makes the guarantee mostly free.** Fourteen arms alias to existing ones, so their
   uniformity is a property already established rather than a new claim — and `f64x2.relaxed_min`
   lands on the very code #223 was filed to fix.
3. **It is checkable against a second authority, and it checks out.** The table above reads the
   proposal's `IMPLEMENTATION_DEFINED_ONE_OF` sets against the reference's twenty mappings; all
   twenty land inside their set. A choice invented here would have to be argued against those sets
   one at a time, and the `(either …)` vectors — the corpus half that scores this at all — enumerate
   what real implementations produce, so a *third* answer risks failing a vector that permits two.

   **The one place this ground does not hold is stated rather than smoothed over.** If the f32
   widen-fuse-narrow composite is *not* double-rounding-innocuous, then matching the reference puts
   this engine outside the proposal's two-member set in rare cases — which is a finding about the
   reference rather than a reason to diverge from it, and one to report upstream rather than paper
   over locally. Decision 2 is adopted with that risk visible, on the grounds that a deliberate
   deviation from the only executable authority is worse than an inherited one, and that the
   discriminating input either exists (in which case the tripwire finds it and this becomes a real
   question) or does not (in which case there was never a defect).

### 3. The bare expression `a*b + c` is forbidden in this engine's floating-point paths — *actor's decision, measured*

Every floating-point multiply-add is written in one of the two forms Go's specification pins:

- **`math.FMA(a, b, c)`** — fused, one rounding, and **arch-uniform by specification**: a single
  correctly-rounded result is uniquely determined by IEEE 754 whatever hardware computes it.
- **`float64(a*b) + c`** — unfused, arch-uniform by Go's own fusion-forbidding idiom.

The bare form is neither, as measured above. Relaxed madd/nmadd take the **fused** form under
decision 2 — spelled `float32(math.FMA(float64(a), float64(b), float64(c)))` at f32 and
`math.FMA(a, b, c)` at f64, which reproduces `fxx.ml:152-157` including its widen-fuse-narrow shape
and its `determine_binary_nan x y` canonicalization, reading the two multiplicands and not the addend.

**And the reason this needs deciding is that the bare form is *conformant* — which is what makes it
invisible.** The proposal's permitted set for madd has two members, two roundings or one, and the two
architectures pick **one member each**. So a bare `a*b + c` passes every vector on both arches, passes
the `_cmp` self-consistency vectors on both arches (one binary makes one choice consistently), and
would sail through the all-on lane on both CI runners. It is not a conformance defect at all; it is a
**uniformity** defect, and decision 1 is the only thing in the project that forbids it. That is the
sharpest available statement of why the guarantee had to be written down: the spec licenses exactly the
divergence the guarantee prohibits, so no oracle will ever mention it.

**The control is therefore stillborn on arm64 by construction, and that is stated rather than worked
around.** A madd arm written with the bare expression is *indistinguishable* from a correct fused one
on arm64 — bare equals fused there — so any assertion pinning the fused answer passes on the dev box
whether the arm is right or wrong. The falsification has to be run under `--platform linux/amd64`, and
the implementing PR runs it there and says so. This is *a control isn't born until it has been watched
die* meeting an architecture asymmetry: the mutation exists and simply cannot fire on the machine the
author is sitting at.

**The f32 double-rounding question gets a tripwire, not a paragraph.** The implementing PR carries a
control that searches for an f32 triple where the widen-fuse-narrow composite differs from a
correctly-rounded single-precision FMA, computed independently with `math/big`. Either it finds one —
and the open question above becomes a concrete report against the reference — or it exhausts its
budget, which is a bound and is recorded as one. A search that finds nothing needs its channel proven
open, so it is paired with a positive control on the same mechanism: a triple where the composite
differs from the *unfused* f32 answer, which must be found.

### 4. The `(either …)` widening rides the first arm family's PR — Q3(c)

*Scott's ruling*, on the slice-3 both-halves precedent. The forecast in #275 already separates the
two deltas, so the account stays legible with both in one PR — and no arm lands with its only
possible value oracle still unbuilt.

### 5. The four zero-vector trunc opcodes land with author-supplied witnesses pinned to the reference — Q4(b)

*Scott's ruling*: arms the suite cannot watch do not get to land unwatched, and the falsification bar
does not waive for corpus gaps. `i32x4_relaxed_trunc.wast` is eight lines with **no assertions**, so
`0x101`–`0x104` are scored by nothing in either lane; their witnesses assert the saturating results
`I32x4_convert.trunc_sat_*` gives, including the out-of-range and NaN inputs that are the whole
reason the opcode is relaxed.

## What this does not decide, named rather than left implicit

- **The gate flip.** `gateRelaxedSIMD` stays off. A flip is its own stamp-tier event (#252) with its
  own pre-registered forecast, and it cannot ride the PR that creates the numbers.
- **Whether the guarantee is contract text.** It is engine doctrine here. Promoting it to
  `docs/burroughs-contract-v0.1.md` would be a contract amendment needing Scott's sign-off, and
  there is no consumer forcing it yet; filing that observation is what discharges it for now.
- **What a compiler backend does.** Decision 1 names the cost; it does not pre-commit v1+ to
  anything except that changing the answer is a governance event.
- **Nothing about `gatemap.go`'s citation, and the reason that bullet is *gone* rather than answered
  is worth one sentence of history.** It stood here claiming `proposals/relaxed-simd/Overview.md:312`
  was in no vendored tree and might be an unresolvable citation. It resolves: `third_party/spec`
  vendors the whole `proposals/` directory, and `Overview.md:312` says *"Opcodes `0x100` to
  `0x12F` (32 opcodes) are reserved for this proposal"* — exactly what `gatemap.go:184-185` claims of
  it. The bullet was wrong because it was written from recall of where the trees end rather than from
  a listing, and resolving it is what turned up the permitted-set authority this ADR's decision 2 is
  now checked against. Kept as a note because the near-miss is the useful part: a "we looked and it
  was not there" that was never actually looked for is worse than no note at all, being a negative
  with a citation's authority.

## Consequences

- **Sixteen arms, of which fourteen are aliases and six are new code.** Aliases: swizzle → the
  `0x0e` body, the four truncs → `vecTruncSatF32x4`/`vecTruncSatF64x2Zero`, the four laneselects →
  the `0x52` bitselect body, the four min/max → the existing float min/max through
  `floatMin`/`floatMax`, q15mulr_s → `q15mulrSatSLane`. New: the four madd/nmadd, plus
  `i16x8.relaxed_dot_i8x16_i7x16_s` and `i32x4.relaxed_dot_i8x16_i7x16_add_s`, neither of which has
  a non-relaxed counterpart in the instruction set.
- **A `vecTernaryFloat` helper, because `vecBinaryFloat` is binary and madd takes three operands.** It
  follows its sibling's widen-apply-narrow shape at width 4 for the reasons above, which is also what
  makes the f32 tripwire a test of *one* helper rather than of four arms.
- **`internal/spec/wast.go:813` says the `(either …)` form's vectors are "all of them in bulk and
  relaxed-SIMD files", and the bulk half is wrong** — measured 32 occurrences corpus-wide, every one in
  the six relaxed files and **zero** in bulk. A one-line attribution fix, and it rides the implementing
  PR rather than this one: the comment sits in the code the widening changes, so correcting it here
  would split one edit across two PRs for no gain.
- **The forecast stands as #275 pre-registered it** and is repeated here so the ADR can be held to
  it: all-on fail **127 → 90**, default `gated` **3625 → 3588**, default `unsupported` **unmoved at
  2689** and structurally so. Separately and marked derived-not-measured, the widening moves the 32
  `(either …)` vectors out of `unsupported` in both lanes.
- **A new arch-uniformity obligation on every future float arm**, not just relaxed ones. Decision 3
  is written over the engine's floating-point paths generally, because the hazard is the language's
  and not the family's.
- **Grave #223's repair becomes load-bearing for a second family.** `floatMin`/`floatMax` were
  hand-written to be arch-uniform; four relaxed arms now alias them, so a regression to
  `math.Min`/`math.Max` would break both families at once. The tripwire is that those arms share the
  helper rather than copying it.
- **A stated guarantee needs somewhere to be stated.** The implementing PR records it in the arms'
  own doc comments beside the `eval_vec.ml` citations, which is where a reader of the code will look.

## Second stamp, appended 2026-08-12 — the whole record, decisions 2 and 3 included

**Stamp: Scott, on the PR #276 relay (session 2026-08-12).** Verbatim: *"0028 is stamped — the
decision is the one this chair shaped and the ADR embodies it: relaxed lowerings deterministic and
architecture-uniform, a Burroughs guarantee exceeding the spec, grounded in the thesis and #223's
paid precedent. Merge #276 with the stamp cited."*

What this adds to the #275 stamp recorded in the header: that one ruled decisions 1, 4 and 5 and
granted the license under which 2 and 3 were taken; **this one covers the record as a whole**, so
the two actor-taken decisions are no longer merely unvetoed-so-far — they are stamped. The
distinction is the point rather than a formality: *"open to veto and nobody vetoed"* is an absence
of objection, and an absence cannot be cited. A `Status:` resting on one would be the fabricated-
provenance failure in its quietest form, since nothing about it reads as false.

Both decision sections keep their `— actor's decision` marking exactly as authored. The marking is
a true statement about **who took it**, which no later approval changes; what changed is the
approval's presence, and that is what this section records. Appended rather than edited in place on
the standing rule for accepted records — *records append-corrected, stale claims wear pointers* —
with the header's `Status:` line the one deliberate exception, because a status field is not
narrative: it *is* the citation, and one pointing at a superseded state of the approval is the
defect the rule exists to prevent.

## Provenance pointer, appended 2026-08-12 — this record landed under another PR's commit message

`git log --follow` on this file resolves to **`6a36e97`**, *"docs: CLAUDE.md becomes an index,
docs/laws/ becomes the corpus — 46 laws relocated verbatim, with a size tripwire (#277)"*. That
message is about a different piece of work and says nothing about relaxed SIMD.

The cause is mechanical and is recorded as a grave in
[#279](https://github.com/scttfrdmn/burroughs/issues/279): #277's branch was cut from #276's branch
while #276 was still open, and a squash merge flattens everything reachable from the head that is
not already upstream — so this ADR, authored and stamped as #276's deliverable, became part of
#277's single squashed commit. The content is byte-identical to what was stamped; only the
attribution is wrong.

**#276 still merged as its own artifact**, carrying the `Status:` amendment above, so this record's
stamp has its own commit and its own verdict even though its body arrived early under someone
else's headline. The lesson #279 records: *a branch cut from an unmerged branch merges its parent's
content under the child's message, and two stamps arrive wearing one green.*

## The f32 double-rounding question, appended 2026-08-12 — answered *no* by the tripwire this record filed

Decision 3 filed one open question with a tripwire instead of a paragraph, and pre-registered both
outcomes: *"Either it finds one — and the open question above becomes a concrete report against the
reference — or it exhausts its budget, which is a bound and is recorded as one."* **It found one.**
This section is that report; it resolves the question and changes no decision.

**The measurement.** Over 1000 f32 triples the reference's widen-fuse-narrow composite differs from
a correctly-rounded single-precision fma on **4**, identically on arm64 and amd64. The first:

```
fma(3, 34.275555, 0x1p-149)   composite 0x42cda740   correctly-rounded 0x42cda741
```

**Why the `2p+2` bound does not reach this case** — the part this record named as the gap and did not
work out. The bound's hypothesis is that the wide format holds the *exact* result before the second
rounding. For `x*y` that hypothesis is satisfied: a 24×24 significand product needs 48 bits and
float64 supplies 53, which is why every *binary* f32 arm in `vecBinaryFloat` is exactly right by the
widen route. For `x*y + z` it fails, and not marginally: with a large product and a subnormal addend
the exact sum spans most of f32's ~277-bit exponent range, so the float64 rounding discards the
addend's contribution and the narrowing then rounds a value that has already lost the bit deciding
which way it should go. Every one of the four differences involves the subnormal addend `0x1p-149`.
So the observation recorded at the time — that the classical theorem is stated for the basic
operations and an fma is ternary — was the right thing to notice, and the answer is that the
extension does not hold.

**Nothing follows for the engine, which is why this is an appended resolution and not an amendment.**
Decision 2 binds this arm to the reference's answer, and the reference *is* this composite: `fxx.ml`
is a functor over `to_float`/`of_float`, so F32's `fma` widens, calls `Float.fma`, and narrows,
double rounding included. The permitted set contains both answers, so a correctly-rounded f32 fma
would conform too — it is simply not the member chosen. Decision 1 also survives intact, and this is
where it is hardest to see: 4 on both architectures, because `math.FMA` and the `float32` narrowing
are both architecture-independent.

**Decision 3's own numbers, since the same sweep produces them.** d3 was an argument when written and
is now a measurement, and it understated the case:

|  | arm64 | amd64 |
| --- | --- | --- |
| `float32(x*y) + z` vs the composite — *the control d3 specified* | 55 | 55 |
| bare `x*y + z` vs the composite | 4 | 55 |
| bare `x*y + z` vs single-rounded | 0 | 59 |

arm64 fuses `x*y + z` into a genuine f32 `FMADD` and therefore lands on the correctly-rounded answer
on every triple; amd64 emits a multiply and an add. So a bare expression in a madd arm would make
Burroughs' answer depend on **which runner built it** for 55 of 1000 triples, while passing every
vector in the suite on both — `relaxed_madd_nmadd.wast`'s expectations are `(either …)` and admit
fused and unfused alike. The uniformity-versus-conformance distinction, with a magnitude.

The first row is the positive control this record specified, and the reason it is the specified one
rather than a substitute is visible in the second: the bare form's count is whatever the compiler
chose, so a control built on it fires for a different reason on each architecture. The implementing
PR's first draft made exactly that substitution — comparing bare against the oracle, which is **0**
on arm64 — and the control was therefore stillborn on the author's machine, precisely as d3 warned
in the sentence above it. The lesson is recorded on
[#280](https://github.com/scttfrdmn/burroughs/issues/280).

**A note on where this record was misread, because the misreading is the finding worth keeping.** The
implementing PR's draft prose — the doc comment on `vecRelaxedFma` and the tripwire's original name,
`TestF32FmaWidenNarrowIsDoubleRoundingInnocuous` — asserted the innocuousness *as reasoning recorded
in this ADR*, quoting `53 >= 2*24+2` and concluding the composite must equal a single-rounded fma.
This record says the opposite: it names the ternary gap and states *"I have not verified it and do not
assert it either way."* A hedge is part of a record's content, so prose that resolves an ADR's open
question in passing — in the confident direction, days later, on the strength of a theorem's name —
is drift with a citation's authority. That is what #280 records, and it is the case *for* filing
questions with tripwires rather than paragraphs: the tripwire outranked the prose that contradicted
its premise.

## Pointer amendment, appended 2026-08-22 — one `wast.go` line citation re-pointed by content

The Question section's `(either …)` bullet cited `internal/spec/wast.go:813` for the harness recording
that form as **0** answerable. That line holds a comment about a shadowed variable in the
`assert_malformed` quote arm and has for some time; the sentence's true target is the `(either …)`
bullet in `classifyAssertReturn`'s declined-shape list, now `internal/spec/wast.go:1253`. Re-pointed
there and nothing else changed.

**The claim was never in question and the historical reading is left exactly as written.** What the
true target holds is *this ADR's own wrong sentence*, quoted and marked as quoted-not-current — the
harness's declined-shape list records that the "0 answerable" count came from a scan of the answerable
population and was wrong in two ways. So the bullet above is a record of what the tree said when this
decision was taken, the pointer now resolves to where the tree keeps that record, and neither is
edited into agreement with the other. Re-pointing a citation is not revising a decision; the
precedent for repairing a drifted pointer in place is ADR 0042's Implementation clause.

Found by the sweep on #455's probe PR and filed as
[#485](https://github.com/scttfrdmn/burroughs/issues/485), which measured three such pointers, none of
them caused by the insertion that found them. Ruled by Scott on the #486 review: the two ADR pointers
take dated amendment notes. `make cite` was green over all three throughout — a `<file>:<line>`
pointer is not in `citecheck`'s domain, which is [#456](https://github.com/scttfrdmn/burroughs/issues/456)'s
population and the reason this was found by a hand sweep rather than by a gate.
