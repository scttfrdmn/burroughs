# 0048 — The pairing table lives in a per-module arena reached by one `int32` on `Func`, because the per-function field dominates a measured bill

Date: 2026-08-28 · Status: **accepted** — the dense arm is the default arm of Scott's conditional order,
relayed verbatim to [a durable comment on
#136](https://github.com/scttfrdmn/burroughs/issues/136#issuecomment-5458586019): *"If span-scoping saves
substantially it's worth its one extra concept; if not, take the full dense array and say so with the number
attached."* The number attached is **+42.2%** — in the chosen placement span-scoping is a penalty, not a
saving — so the conditional resolves without anyone supplying a threshold for *substantially*. The
**placement** of that dense array is 0016's deferred arena question, delegated to this measurement by name,
and the answer turns on a 4-byte hole in `Func`'s field order.

Filed against **#136**. Implements the block-pairing half of its definition of done; the `br_table` half was
already discharged by [0016](0016-unbounded-immediates-live-beside-the-body.md). Repairs grave **#505**.

**Retracts a figure this project has already published.** An earlier draft of this ADR declined span-scoping
"at 12.1%", and that figure was carried into the durable comment cited above. It was wrong, the instrument that
produced it was wrong in two ways described below, and the correction is delivered by posting to the same
thread rather than by editing it — *a correction travels attached to the quote*.

## Context

[0002](0002-interpreter-strategy.md) specifies an internal form with *"branch targets resolved to indices
in this slice"*. The immediates half shipped; the pairing half did not, and `internal/interp/control.go`
resolves a block's extent by scanning for its `END` on **every dynamic block entry**. Grave #505 is the two
comments in `internal/binary` that told every reader the opposite, one of them using the false claim to
justify retaining `END` at all.

The cost was measured across three PRs. #502 established that the scan's cost exists and scales; #503
measured the distances that actually occur (p50 **5** slots, p99 **67**, max **276**); #504 ran the A/B at
those distances and confirmed materiality **at the median** — `-5.19%` at p50 rising to `-6.59%` at p99, net
of a build-boundary bias the no-effect row exposed. What none of them measured is the other side of the
trade: **the bytes the table retains beside every function body, for the life of the module, whether or not
the function is ever called.**

### Why the bytes needed measuring rather than deciding

Two prior decisions rest on exactly this arithmetic, and a third — the choice here — was about to be made
by judgement between two people who both preferred the same shape.

- **0002** rejects a side table on layering grounds and builds its whole strategy on `Instr` staying compact.
- **0016** chose a sparse `map[int][]uint32` for `br_table`'s label vectors, and it **deferred this exact
  question here by name**: *"#136's benchmark is where the arena question gets settled. This ADR does not
  pre-empt it."*

Reading 0016 corrected the premise this ADR was drafted on. The draft called a dense array "a departure from
a shape the project has already reasoned about"; 0016's rejected option A was growing `Instr` by a **24-byte**
slice field to serve **one opcode in 256**, so the map won against an alternative six times more expensive
per slot, for an instruction an order of magnitude rarer than a structural opener. *A valid citation does not
certify its sentence* — the pointers resolved and the clause between them was invented. 0016 also records a
fourth option it calls the one that *"reads as elegant"*, an arena, with the note that *"when it is run, this
is the option to reach for."* This is that run, so the arena is priced here by 0016's own instruction rather
than as an extra somebody thought of — and it is what this ADR ends up choosing.

### The instrument was wrong twice, in ways that were invisible because they stood in for each other

The first version of the pricing test weighed the candidate structures and reported that as the bill. **A
representation costs what its structures cost plus what the field that reaches them costs**, and for every
dense shape the first version priced, the field is the larger half: a slice field on `binary.Func` is 24 B ×
**9393 functions** = 225432 B, against 54688 B of weighed dense structures. The field is charged on every
function; the structures only on the 13.5% that hold an opener.

It was also *unevenly* incomplete. The harness held each body's structure in a `[]any`, and converting a
`[]int32` to an interface heap-allocates a 24-byte copy of the slice header, while a map, a `*T` and an
`int32` offset are pointer-shaped or small and convert for free. So the harness charged a 24-B-per-body box
to exactly the variants whose real field is 24 B wide and nothing to the variants whose real field is 8 —
**the right width charged to the wrong population**, understating the term it accidentally stood in for by
7.4×. Both errors are the same mistake at different depths: *the pointer that reaches a structure is part of
the structure*, and a harness that boxes some variants and not others has already decided that it isn't.

The 12.1% retracted above decomposes exactly: 1267 boxes × 24 B = 30408 B were added to both dense rows, so
`54688 → 85096` and `44392 → 74800`, diluting a structures-only saving of **18.8%** into 12.1% while hiding
the 75144 B that span-scoping's retained origin costs when it sits inline in `Func`. The dilution and the
omission pointed opposite ways on this one comparison, which is why neither was visible in the result.

## Question

Where does the pairing live, and in what shape? **That is two axes**, and the first instrument priced one of
them, with placement varying accidentally between rows.

Priced by `TestEndTablePairingRepresentationsArePriced` in `internal/interp/endtablesize_test.go`, over the
same 4216-module corpus and with the same helpers as #503's census: **9393 function bodies, 2020 openers,
1267 bodies (13.5%) containing at least one opener**, mean 1.59 openers in the bodies that have any, max 40,
**2.23 functions per module**. `binary.Func` is 88 B and `binary.Module` 280 B before any field is added. The
structures are cumulative allocation over the whole corpus, all retained; the field terms are arithmetic over
the real struct layouts via `reflect.StructOf`, charged at **the cheapest position in the field order** — a
sweep over every position, since the mechanism declares the field and so declares where it goes, and appending
is the worst case by construction. The denominator is what the decoded bodies already cost: `binary.Instr` is
24 B, so the bodies retain **1170864 B**.

- **shapes**: dense over the whole body · dense span-scoped to `[first opener, last matching end]` · sorted
  `(pc, end)` pairs with a binary search.
- **placements**: inline in `Func` · behind one pointer from `Func` · a per-module arena on `Module` with an
  `int32` offset on `Func`.

Nine cells, plus 0016's sparse map, which is off both axes.

| representation | bill | ×cheapest | on bodies | structures | field on `Func` | field on `Module` |
| --- | --- | --- | --- | --- | --- | --- |
| pairs · one pointer from `Func` | 121848 B | 1.00× | +10.4% | 46704 B | 8 B × 9393 | — |
| **dense whole body · per-module arena (chosen)** | **154520 B** | **1.27×** | **+13.2%** | 53336 B | **0 B × 9393** | 24 B × 4216 |
| dense span-scoped · one pointer | 160080 B | 1.31× | +13.7% | 84936 B | 8 B × 9393 | — |
| dense whole body · one pointer | 160240 B | 1.32× | +13.7% | 85096 B | 8 B × 9393 | — |
| pairs · per-module arena | 192832 B | 1.58× | +16.5% | 16504 B | 8 B × 9393 | 24 B × 4216 |
| dense span-scoped · per-module arena | 219688 B | 1.80× | +18.8% | 43360 B | 8 B × 9393 | 24 B × 4216 |
| pairs · inline in `Func` | 241728 B | 1.98× | +20.6% | 16296 B | 24 B × 9393 | — |
| dense span-scoped · inline in `Func` | 269824 B | 2.21× | +23.0% | 44392 B | 24 B × 9393 | — |
| dense whole body · inline in `Func` | 280120 B | 2.30× | +23.9% | 54688 B | 24 B × 9393 | — |
| sparse `map[int]int32` · inline in `Func` (0016's shape) | 324280 B | 2.66× | +27.7% | 249136 B | 8 B × 9393 | — |

**The first finding is the axis, not a row.** The field terms span 0–225432 B and the structures 16296–85096
B, so **placement moves the bill further than shape does** — and the placement this project's two principals
had both settled on, a `[]int32` field on `Func`, is the *most expensive* of the three for every shape.

### The 4-byte hole, which is the single largest term in the comparison

`binary.Func` opens `TypeIndex uint32` followed by a slice header, so it carries **exactly one 4-byte
interior hole**, and an `int32` placed there costs **nothing** — against 8 B if appended, which is **75144 B
over this corpus**. The pricing test measures the hole rather than reading it off the field list, and it
charges every representation at its cheapest field position rather than appended, because the mechanism
declares the field and therefore declares where in the order it sits.

That one fact decides the table, and it is not a trick:

- The **arena's offset is the only candidate field that fits**. A pointer needs 8-byte alignment and the hole
  is 4 wide; a slice header needs 24. So the arena is the one placement whose per-function charge is zero,
  and it leads every dense row on this corpus with no projection involved.
- **The hole holds one `int32` and no more.** Every second `int32` a representation wants costs the full 8 B
  × 9393 = 75144 B, which is what refuses span-scoping below and what makes the pair shape's extent field
  cost more than the arena offset it accompanies.
- It is a fact about `Func`'s *current* layout, so it is computed rather than written down. `Func` gaining or
  losing a field moves this figure, and the next reader gets the moved figure instead of this one.

#### Implementing this decision spent the hole, so the instrument charges against the counterfactual

The field that fills the hole is `Func.EndsOff`, which this ADR's own mechanism adds. So from the commit that
lands it, the live `Func` has no hole left, and a pricing run that charged a hypothetical `int32` against the
live struct would be pricing a **second** one at the full 8 B: the chosen row moves 154520 B → 229664 B, the
table re-orders, and the instrument refutes the record it is the evidence for. That happened — the first run
after the mechanism landed printed the re-ordered table under a sentence reading *"the only interior hole in
`binary.Func` is 0 B wide, and the arena's offset is what fits in it … which is why the arena leads the dense
rows"*, on a board where the arena was fifth. Every number in it was real.

The fix is not a provenance note. `endSizeUncommittedFunc` charges every row against `Func` **less
`EndsOff`**, so the table above re-derives at HEAD and keeps its meaning: each row answers *what would this
representation cost, added to a `Func` that does not already have it*, which is the question the decision
turned on and the only one under which all ten rows share a base. `TestEndsOffsetIsFreeInTheLayout`
(`internal/binary`) is the other half — it asserts the field really is absorbed, in both builds, so a `Func`
that starts paying 8 B for it fails there rather than silently changing what every figure here means.

Two laws are behind that, both already paid for and both recurring inside this measurement: *a hand-derived
relation printed under a machine-derived table is the defect stated as the rule*, and **an instrument whose
subject is changed by its own conclusion stops reproducing it** — which is the artifact-becoming-an-oracle
shape with the artifact and the oracle one commit apart.

### Span-scoping, which is the arm Scott's order turns on

**Declined**, and the answer needs no threshold for *substantially*, because in the chosen placement span
scoping is not a saving at all — **it costs 42.2% more** (219688 B against 154520 B). Its best showing
anywhere in the table is **−3.7%**, and that is in the placement the decision rejects for other reasons.

The arithmetic, which is the same three lines in every placement:

- **What the window saves**: the payload floor drops 49940 → 40652 B, **9288 B**, from narrowing 12485 slots
  to 10163 — 18.6% of the payload. Allocated structures drop 10296 B, slightly more, because smaller slices
  land in smaller size classes.
- **What the origin costs**, and this is the term that decides it. The window's origin must be retained, and
  it is a *second* `int32` — so the 4-byte hole is unavailable, being already spent on the offset the
  representation needs to exist. In the arena that is 8 B × **9393 functions** = **75144 B to save 10296 B, a
  7.3× loss.** Behind a pointer the origin rides inside the per-body cell instead, at 8 B × **1267
  opener-bearing bodies** = 10136 B, which nets **160 B, or 0.10%** — the extra concept paying for itself to
  within 1.6% of its own cost. Inline the origin is absorbed by the 24-byte slice field's own padding and is
  free, delivering the whole 10296 B, **−3.7%** of the most expensive placement in the table.
- **So span-scoping's value is entirely a function of where the origin lands**, and it is worth the most in
  the placement that is worth the least. The order asked whether it saves substantially; over the whole cross
  it ranges from −3.7% to +42.2%, and at the chosen placement it is a 42% penalty.

So the order's default arm is taken: **the full dense array, with the number attached.** The number is not
the 12.1% this ADR first published, the conditional needed no threshold from anybody, and the reason is one
sentence of struct layout rather than a judgement about a word.

### The other options, and why the corpus's cheapest row is not the chosen one

- **A — dense `[]int32` over the whole body, `-1` where no header opens, nil when a body has no opener.**
  The shape `ends_table.go` measured and the mechanism #504's 5–6% win came from: O(1) indexing. Chosen, in
  the arena placement.
- **B — sparse `map[int]int32`, matching `Labels`/`Catches`/`Casts`.** **Rejected on the measurement, and it
  is the one unambiguous result in the table**: 249136 B of structures to carry 2020 entries — **~197 B of
  map per function that has one, for ~19 B of payload**. It is rejected *in the version of the bill most
  favourable to it*: a map header is one word, so the map draws the narrowest field in the table (8 B), and
  it still comes tenth of ten on structures. It is also the only row with no honest analytic form, which is
  why the whole comparison is weighed rather than modelled.
- **C — span-scoped dense.** Rejected above.
- **D — sorted pair slice with a binary search.** **The cheapest row in the table, 121848 B, and not
  chosen.** It replaces an O(1) index with a search, and O(1) indexing is the mechanism #504 measured; taking
  it would trade a measured 5–6% time win for an unmeasured one. At a mean of 1.59 openers the search is ~1
  comparison and the trade is probably free *on this corpus* — but the corpus is not the workload, and on a
  Go guest with tens of openers per body it is ~6 unpredictable branches against one load. Its advantage in
  the chosen placement is **−16%** (192832 vs 229664), which is what a later PR would be buying if it takes
  the search. Recorded as a live alternative rather than rejected, and it is the row to reach for if the
  pairing table ever needs to shrink.
- **E — inline in `Func`, the placement both principals assumed.** Rejected, and it is the most expensive
  placement of the three for every shape. Against the chosen row the decomposition is `+125600 B = structures
  +1352, Func field +225432, Module field −101184`: it pays a 24-byte slice header on all 9393 functions to
  avoid 4216 slice headers on modules, and its structures are no cheaper. **The whole penalty is the field**,
  which is the finding that cost the most to see and the one the first instrument could not report at all.
- **F — one pointer from `Func`.** The **second**-cheapest placement for the chosen shape, `+5720 B =
  structures +31760, Func field +75144, Module field −101184`. It allocates a 24-byte cell per
  opener-bearing body where the arena allocates one run per module, and its 8-byte pointer cannot occupy the
  4-byte hole. The gap is small on this corpus and it is small *for a reason that does not survive*: the
  arena's 101184 B of module headers is 4216 tiny modules, and every one of the three terms moves toward the
  arena as functions per module rises. Recorded as the close second rather than dismissed — it is the
  fallback if the arena's decode-time assembly ever proves awkward, at 3.7% more memory on this corpus.

## Decision

**A dense `[]int32` over the whole body, `-1` where no header opens, built in one pass at decode, living in a
single per-module arena on `binary.Module`, reached by one `int32` offset on `binary.Func` with the extent
taken from `len(Body)`.** An offset of `-1` is "this body opens no block", which is 86.5% of them on this
corpus and needs no allocation anywhere.

Three properties of that sentence are the decision, and each is separable:

1. **Dense and whole-body** — O(1) index, no window, no search. Scott's order, default arm, at 0.10%.
2. **In an arena** — 0016's deferred fourth option, taken on its own instruction, because the field that
   reaches a table costs more than the table and an arena is the placement with the narrowest field and no
   per-body allocation.
3. **`int32` and `len(Body)`** — an offset and nothing else, because a second `int32` is free in `Func`'s
   padding and a third is not, so the shape that needs no length and no origin is the shape that fits.

Populated **only under the `burroughs_endtable` build tag** in this ADR's implementation. Populating it by
default is a default-behaviour change in the memory dimension — `+13.2%` on retained body bytes over this
corpus — and behaviour 4 puts that in its own stamp-tier event with a pre-registered forecast, which is the
flip PR and not this one.

## Consequences

- **The chosen row is the corpus's cheapest O(1) representation, and the second cheapest of the ten
  overall.** The one row below it is the pair search (option D) at 121848 B, **21% cheaper**, declined to
  keep the O(1) index #504's win was measured on. That is the whole gap between this decision and the
  cheapest bill available, it is stated rather than buried, and it is the trade a later PR would be making.
  No projection to a Go guest is doing any work in the choice: the arena wins on the corpus as measured.
- **The interpreter side costs nothing to plumb, which is part of why the arena is affordable.**
  `internal/interp/interp.go` holds `mod *binary.Module` on `Instance`, so `frameEnds` returns
  `in.mod.Ends[off : off+len(body)]` — a subslice, no per-frame allocation, and `endOf`'s signature does not
  change. The `sync.Map` probe in `internal/interp/ends_table.go` goes away with the file.
- **The arena's decode-time transient is a cost this bill does not price, and the mechanism is stated so the
  gap is bounded.** The measurement allocates each module's arena once at its exact size, which a decoder
  cannot do while decoding the module's first body. The mechanism captures pairings during `structural`'s own
  recursion into a scratch buffer reused across functions, appends `len(body)` slots to the module arena at
  end-of-function, and copies once to exact size at end-of-module — so *retained* bytes are as measured and
  the *transient* is bounded by roughly 2× the final extent (49940 B over the whole corpus), reclaimed before
  `DecodeModule` returns. That is a decode-time allocation figure, not a retention figure, and the two are
  different quantities; it is named because an unmeasured term should be named rather than omitted.
- **An `int32` offset bounds a module's arena at 2^31−1 slots**, which requires a code section beyond 2 GiB
  to reach. The mechanism asserts the bound at the point it writes the offset rather than assuming it, since
  a silent `int32` truncation would produce a table that answers the wrong `END` — the failure mode this
  whole ADR exists to remove.
- **Grave #505's two comments become true**, and that is what this ADR's implementation is *for*: `Body`'s
  field doc in `module.go` and `endTerminator`'s justification in `instr.go` both assert 0002's resolution is
  implemented. They are corrected to describe the gate rather than deleted, so the claim names its condition.
  `END` stays retained either way, and the honest reason is stated: the pairing pass reads it too.
- **The instrument-to-engine relationship of this decision is unusual and worth naming.** The measurement
  that chose the representation lives in `internal/interp`, but the representation lives in `internal/binary`
  — so the pricing test builds its own candidates rather than calling the shipped builder. Nine of the ten
  rows are shapes that will never exist in the engine; that is what a comparison costs, and it is charged to
  this mechanism rather than filed as its own instrument PR, per the order.
- **The tagged lane is built but never tested, and this implementation is what makes that a defect rather
  than a gap.** `make check` and CI both run `go build -tags burroughs_endtable ./...` and stop there. Once
  correctness-bearing code sits behind the tag, a lane that only compiles is a lane whose green means
  nothing — and contract §9 makes the flip's acceptance *the proposal's own suite green*, which no instrument
  currently produces. Extended to a test run in the same PR.
- **Two figures from this measurement are the flip's business, not this ADR's.** The bill is the *cost* side
  of a trade whose benefit was measured in #504; the flip PR is where the two are put beside each other, and
  its forecast is already pre-registered on #136 so it cannot be written after the numbers.
- **The guest-scale bill is an open exposure with a named revisit condition, and here it bounds the margins
  rather than the winner.** Two corpus properties move the terms, both artifacts of hand-written conformance
  tests: the **opener-bearing fraction** (13.5% here) is the population the structures are built for against
  the 9393 the field is charged to, and **functions per module** (2.23 here) is the denominator the arena's
  per-module header is spread over. Both move in the arena's favour on a Go guest, so the exposure is to the
  *size* of the win and to the absolute figure, not to its direction. **The pricing test prints a per-shape,
  per-term decomposition rather than a threshold**, after a hand-derived pair of thresholds in its first
  version contradicted the table printed directly above them — *the defect stated as the rule*, in a report
  about a measurement.
  **Revisited when the project builds its first Go guest** — the same condition that governs #503's
  materiality datum, and the same gap, so the two are discharged by one measurement when it exists. Recorded
  rather than estimated, because an extrapolation from 4216 conformance modules to a Go binary would be a
  modelled number wearing a measurement's clothes.
- **A follow-up this measurement found and did not pursue.** The map row prices the shape `Labels`, `Catches`
  and `Casts` already use: ~197 B of map per function that has one, to carry a mean of 1.59 entries. Whether
  those side tables carry enough entries each for that to be the right trade is a question this ADR did not
  ask and has no number for — their entry-count distribution is unmeasured. Filed rather than assumed,
  because *an issue's list is a registry, not an inventory* and this is a lead, not a finding.
- **One ADR earns one implementation**, and this one earns the `binary` pairing table in its arena. It does
  not earn the flip, the pair search, or a re-placement of 0016's own side tables.
