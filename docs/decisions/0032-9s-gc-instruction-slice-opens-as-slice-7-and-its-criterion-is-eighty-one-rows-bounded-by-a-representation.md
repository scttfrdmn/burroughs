# 0032 — #9's GC-instruction slice opens as slice 7, and its criterion is eighty-one rows bounded by a representation

Date: 2026-08-17 · Status: **accepted** — stamped by Scott, relayed on
[the PR #382 stamp comment](https://github.com/scttfrdmn/burroughs/pull/382#issuecomment-5321968922)

> *"Already answered: short ADR recording the boundary move and the thirty rows as its criterion,
> then the port. Go."* — Scott, on the pre-clear relay. The disposition authorizes the slice; the
> criterion below is **measured, and it is not thirty**. "The thirty rows" was 0031's phrase for the
> subtype slice's own 21+9, and repeating a number across two slices because it was the last one
> spoken is how a forecast becomes a habit. What this record owes the disposition is the boundary
> move and *a* criterion; the figure is derived here and is the agent's, reviewable as any other.

## Context

`internal/validate` types the single-byte space, 0xFD (slice 2), and 0xFC (slice 5). **The 0xFB
region is declared declined in two places**, which is why opening it is a decision:

- `instr.go:65-68`, the region dispatch: *"The prefixed regions are 0xFB (GC), 0xFC (bulk
  memory/table), 0xFD (SIMD), 0xFE (threads). Slice 2 (#305) types 0xFD and slice 5 types 0xFC;
  **0xFB and 0xFE stay declined** — which is what keeps an unchecked module from being reported
  valid, and keeps the decline census a work plan for the slices that own them rather than a
  silence."*
- `validate.go:28-29`, slice 2's paragraph: *"**The single-byte opcode space is fully in vocabulary
  as of that slice**, and what remains declined is the three prefixed regions: 0xFB (GC), 0xFC (bulk
  memory/table), 0xFE (threads)."*

The second sentence is **stale on 0xFC** — slice 5 typed that region and this sentence was not swept
— so the boundary is declared twice and the two declarations disagree about the present. Both are
amended in the implementing PR, with the prior text quoted where it stood, on 0031's and 0025's
reasoning that a retired boundary is recorded rather than absorbed.

Unlike 0031's move, this one is **not** holding false accepts in reserve: every one of the 81 rows
below is a **decline**, measured `declined=81 of 81`. The region is honestly refused. What makes it
a decision anyway is *order* — 0xFB is not next in the register, and taking it ahead of constant
expressions (24), limits (16) and exception handling is a claim about which slice pays most.

## Decision

**Open the GC-instruction slice now, as slice 7, and retire both boundary statements.**

**It is slice 7 and not slice 6, and the reason is a collision worth recording rather than
papering over.** The ordinal 5 is claimed **twice**: `bulk.go`'s header opens *"Slice 5 of #9's
validator: the instructions whose type reads a module index space"*, and `validate.go:153` opens
*"# Slice 5: the subtype relation"*. Slice 6 is the reference-type slice (#359/#363) — named as 6 in
`bulk_test.go:28`, `validate_test.go:528` and ADR 0027, though never in `ref.go`'s own header. So 6
is taken by a slice that does not claim its own number, 5 is taken by two slices that both do, and
**7 is the first ordinal no slice claims.** The collision is left as-is: renumbering a landed slice
would falsify every citation pointing at it, which is the same trade 0031's specimens settled.

*(Swept 2026-08-21. `validate.go:70` is re-pointed above — it had drifted 83 lines. Two other claims
in that sentence decayed differently and are left standing with their corrections here, because the
two failure modes are the point. **`validate_test.go:528` is dangling, not drifted**: no line in that
file names slice 6 at all, and the reason is recorded in the file itself — the paragraph that did was
**deleted rather than re-pointed** when slice 10 drained its population. A pointer whose subject was
deliberately removed reads exactly like one that merely moved. **And "never in `ref.go`'s own header"
is now false**: `ref.go:13` names slice 6, and `ref.go:19` says so on purpose — *"This header also
answers a gap gc.go's names"*. That correction is the harder one, because a negative claim is a
citation with no target, so no pointer sweep can ever check it — the code that falsified it announced
itself in a comment and nothing carried the news back here.)*

**Not decided here, deliberately:** the design of the arms. Porting `valid.ml:492-855` is normative
reference behaviour, and where the field/element accessors resolve their `deftype`, whether the
packed-field check shares a helper with the array one, and what file it lands in are implementation
shape recorded in comments at the site.

## Criterion

**81 rows, pre-registered in both directions, every one of them currently a decline.** Measured over
the 256 board files in the all-gates-on lane, keyed on bucket keys whose *every* newline term names
`prefixed opcode 0xfb` — sole-blocked, so clearing the region clears the row: `sole=81 co=0 keys=40
files=16`.

| direction | population | now | required |
|---|---|---|---|
| reject | 27 `assert_invalid (module)` | `declined` | **pass** |
| accept | 54 `module text` definitions | `declined` | **pass** |

**The reject side's target is `pass`, not `refused`, and here that is a five-string claim rather than
0031's one.** The 27 expect:

| expected string | rows |
|---|---|
| `type mismatch` | 17 |
| `immutable array` | 5 |
| `array types do not match` | 3 |
| `array type is not numeric or vector` | 1 |
| `immutable field` | 1 |

Four of those five strings are **not in this package's declared error set** — only `type mismatch`
is (`ErrTypeMismatch`). `immutable field` appears in `match.go:610` as prose in a comment about
field-mutability invariance — *"an immutable field never matches a mutable one"* — which is not a
sentinel and is worth naming precisely because it reads like one. (Re-pointed 2026-08-21 from
`match.go:584`, a drift of 26 lines onto a bare `return false`.) The nearest existing neighbour is `ErrGlobalImmutable` (`immutable global`,
`valid.ml:607`), whose own text was wrong until a probe caught it — the grave at `validate.go:320`,
the `Its text was global is immutable until the probe caught it` note. (Re-pointed 2026-08-21: this
read `validate.go:222` and had drifted ~98 lines, landing on a `# The authority` heading. The pointer
carries its target's text now, so the next drift is re-findable rather than merely wrong.)
A relation that refuses all 27 with
`ErrTypeMismatch` moves 10 of them from `declined` into the **wrong-message** bucket, which is a
lateral move scored as an improvement — Scott's objection on the 0031 relay, and it applies to ten
rows here instead of twenty-one. So the criterion is four new sentinels as much as it is a count.

The accept side spreads over 16 files, `type-subtyping.wast` heaviest at 11: `array.wast` 5/1,
`array_copy.wast` 1/4, `array_fill.wast` 1/3, `array_init_data.wast` 2/2, `array_init_elem.wast`
3/3, `array_new_data.wast` 5/0, `array_new_elem.wast` 5/0, `br_on_cast.wast` 3/6,
`br_on_cast_fail.wast` 3/6, `extern.wast` 1/0, `i31.wast` 5/0, `ref_cast.wast` 2/0, `ref_eq.wast`
1/0, `ref_test.wast` 2/0, `struct.wast` 4/2, `type-subtyping.wast` 11/0 (accept/reject).

### The bound the 81 rows cannot supply, stated before the fact

0031's criterion was falsified by its own implementation, and the lesson was that *a representation
that cannot express the wrong relation is a stronger bound than a population.* Applying it here,
**before** the port rather than after:

**The region is 31 opcodes and 21 typing arms**, and the arithmetic closes exactly. `opTableFB` has
31 contiguous entries `0x00`-`0x1e`; `valid.ml` has 21 arms for them (`:492 :508 :732 :737 :745
:748 :751 :760 :770 :779 :787 :792 :800 :808 :815 :821 :824 :831 :837 :846 :855`). The ten-entry
difference is eight parameterized constructors: `StructNew`/`ArrayNew` take an `initop` (2→1 each),
`StructGet`/`ArrayGet` take an `exto` (3→1 each), `RefTest`/`RefCast` a nullability (2→1 each),
`I31Get` an `ext` (2→1), `ExternConvert` a direction (2→1). 31 − 10 = 21.

**The 81 rows' bucket keys name 22 distinct opcodes, and that number is a frontier, not a census.**
The decline message names the *first* offending instruction in the module, so every later GC
instruction in the same function is shadowed. Two measurements establish this rather than assume it:
`extern.wast:1`'s key reads `func 1: instr 5 (ref_i31) … 0xfb 0x1c` while the file's modules contain
six `any.convert_extern`/`extern.convert_any` occurrences; and `0x0f` (`array_len`) appears in no key
at all while `array.wast` calls `array.len` at `:90 :135 :191 :265`. So opcode coverage is a **lower
bound** and the 22 is under-reported by construction — which is #249's unshadowing error in the
other direction, and is why the arm-level claim below is not read off the histogram.

What follows is the risk with teeth: **the eight parameterized constructors are where a wrong port
survives the criterion.** `struct.get` on a packed field must be rejected and `struct.get_s`/`_u`
on an unpacked one must be, and those are the *same arm* differing by `exto`. An implementation with
one code path per opcode can get the witnessed sibling right and the shadowed one wrong; the 81
rows will not say so, because the shadowed sibling never reports. The bound is therefore structural:
**each of the eight lands as one arm taking the reference's own parameter**, so a per-opcode
divergence is unrepresentable rather than merely untested. If the port instead grows 31 arms, this
criterion is passing on the same coincidence 0031's did.

## Consequences

- **Both declared boundaries retire, and `validate.go:29`'s stale 0xFC clause is corrected in the
  same motion** — not as a drive-by, but because it is one of the two sentences this decision is
  about, and leaving it would mean the boundary retired in one place and lied in the other.
- **This is a gate-campaign slice, so the default lane's reward is structurally zero.** 0xFB is
  `gate:gc`; all 81 rows are all-gates-on-lane rows. `unsupported` cannot move, the default board
  cannot move, and the reward figure is the **all-on lane's fail delta**, per the product law's own
  substitution clause. A PR claiming a default-lane improvement here would be claiming a number the
  gate makes unreachable.
- **The all-on lane's fail delta is the instrument, and `validateDeclineCeiling` is not.** This bullet
  read "**`validateDeclineCeiling` is the instrument, and it should fall by 81.** The declines are
  exactly what this slice converts, so the ceiling drop *is* the record — no second mechanism," and
  the measurement below falsified it before the stamp: that ceiling is a **default-lane** bound,
  standing at 8, and it did not move by one. It could not have. The bullet directly above this one
  says all 81 rows are all-on-lane rows and the default board cannot move; naming a default-lane
  ceiling as their instrument contradicted it two lines later. Recorded rather than silently fixed
  because the pair is a specimen of *checking a ruling's premises and not just its conclusion* aimed at
  a decision's own consequence list — the conclusion "the declines are what this slice converts" was
  right, and the instrument named for it was in the wrong lane.
- **0xFE stays declined and stays declared**, with `instr.go`'s dispatch keeping its region-dispatch
  shape for exactly the reason its comment gives.
- **The four new sentinels widen this package's error set by more than any slice since alignment**,
  and each is a suite string verbatim per 0003. `immutable field` and `immutable array` are one
  concept over two shapes and the corpus spells them differently; they are two sentinels, because
  the sentinel *is* the expected string.
- **Two arms are named by no key** — `ArrayLen` and `ExternConvert` — and the shadowing measurement
  says that is a fact about the instrument, not about the corpus. They are not a blind spot to
  inherit; they are arms whose vectors exist and whose declines are hidden behind an earlier one, so
  they will surface as the frontier moves. That prediction is checkable: if the 81 do not all pass
  after the port, the residue is where the frontier moved to.

## Measured result — 74 of 81, and the 7 are the criterion's own method failing

Written after the port and before the stamp, so the record is accurate when it is stamped. The
pre-registration above is quoted rather than edited; what follows is what the instruments said.

**All-on lane: 377 fail → 303, a fall of 74.** Measured by removing the one-line dispatch arm
(`case prefixGC: return v.gcInstr(i, in)`) and re-running `TestAllGatesOnLeavesNothingGated`, which is
also the rollback this slice ships with. **Default lane: byte-identical** — `60837 pass, 188 fail, 66
unsupported, 4053 gated`, and `fail by stratum` unchanged term for term, including
`validateDeclineCeiling`'s 8. The structural zero is therefore *measured* rather than argued from the
gate, which is the stronger form of the same claim.

**The 81 was exactly right as a population and exactly wrong as a forecast.** Counting all-on rows
whose bucket key names `prefixed opcode 0xfb` gives **81 before the port, per file: array 6, array_copy
5, array_fill 4, array_init_data 4, array_init_elem 6, array_new_data 5, array_new_elem 5, br_on_cast
9, br_on_cast_fail 9, extern 1, i31 5, ref_cast 2, ref_eq 1, ref_test 2, struct 6, type-subtyping 11.**
Ten of the sixteen files converted their full share; six fell short by one or two, and no file's fail
count rose — so the 74 is pure under-conversion with zero regression.

**The 7 that did not convert all re-declined on a single-byte opcode slice 6 left**: `ref_eq` (0xd3)
×4, `ref_as_non_null` (0xd4) ×1, `br_on_null` (0xd5) ×1, `br_on_non_null` (0xd6) ×1. Not one of them is
a GC arm being wrong.

**Split by direction, the criterion's two halves came out very differently, and that is the result
worth keeping.** Counting `assert_invalid (module)` rows across the whole all-on lane by their arm:

| bucket | before | after | delta |
|---|---|---|---|
| `declined` | 96 | 69 | **−27** |
| `accepted, expected:` (admits) | 71 | 71 | **0** |
| `expected:` (refused, wrong message) | 12 | 12 | **0** |

**The reject side is 27 of 27 with zero lateral movement.** Every row the criterion pre-registered as
`declined → pass` did exactly that; none landed in the admit bucket and none in the wrong-message
bucket. That is the five-sentinel claim discharged in the only form that counts — the objection this
criterion was written against (refuse all 27 with `ErrTypeMismatch` and score ten lateral moves as an
improvement) would have shown up as `expected:` rising by ten, and it stands at 12 before and after.
**So the entire residue is in the accept direction: 47 of 54.**

**And that asymmetry was predictable before the run, which makes it a rule and not an anecdote.** An
`assert_invalid` module is minimal by construction — it exists to isolate one error, so it contains
approximately one interesting instruction and a single-term key really does mean sole-blocked. A
`module text` definition is a full working module written to exercise a feature, so it reaches for
whatever else it needs, and a single-term key means only "the validator stopped early". The probe's
blind spot is therefore **concentrated entirely in the accept direction**, and the next slice's
forecast can say so up front: pre-register the reject count as a number and the accept count as a
number *with a stated upper-bound reading*, because the accept side is measuring a first-refusal and
not a blocker set.

**And the cause is the paragraph two sections up, applied one level higher than it was written.** The
criterion says `sole=81 co=0`, derived from the co-blocking probe's rule that a single-term bucket key
is a sole blocker (`wast.go`'s `Buckets` note, consequence 2). That rule is sound for the `no instance`
form, whose key is an `errors.Join` union of every refusal a module hit. **It is structurally void for
a validator decline**, because the validator stops at the first offending instruction — so a
`module text declined` key has *exactly one* term by construction, and single-term-ness carries no
information about whether anything else blocks the row. `co=0` was not a measurement of the corpus; it
was the probe reporting its own blind spot, and the ADR had already written down the mechanism
(`extern.wast`'s shadowed `any.convert_extern`, `array_len` in no key at all) while drawing the
conclusion only about *opcode coverage* and not about *sole-blockedness*.

`ref_eq.wast` is the cleanest specimen: **7 fail before, 7 fail after**, with one row's key changing
from `ref_i31 … 0xfb 0x1c` to `ref_eq (0xd3)`. A decline moved *within* the column, and the file's
total says nothing happened. That is the third payout of this shape — #249's unshadowing half, #359's
all-on miss of 4, and now 7 — which is what makes it a defect in the instrument's documented rule
rather than three unlucky forecasts. Filed as a grave against `wast.go`'s note, whose remedy ("the
union key answers that directly when it is not truncated at its first term") does not exist on this
stratum: the key is *always* truncated at its first term here.

**The `ArrayLen`/`ExternConvert` prediction paid out, in the direction it did not name.** The bullet
above predicted that a short residue would show where the frontier moved; it moved to slice 6's
leftovers rather than to the two shadowed GC arms, both of which converted silently. So the arms are
still unwitnessed by any bucket key and remain a declared blind spot — the structural bound (one
parameterized arm per reference constructor) is what covers them, exactly as this decision argued it
would have to.
