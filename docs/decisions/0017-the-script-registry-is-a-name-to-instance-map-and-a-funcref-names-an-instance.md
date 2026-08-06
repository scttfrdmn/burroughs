# 0017 — the script registry is a name→instance map, and a funcref names an instance

Date: 2026-08-06 · Status: **proposed** — no stamp exists yet

> Held open per the ruling on #142: *a status field is a citation to an approval, and approvals are
> artifacts with provenance.* Scott ordered this record authored ("the (B) ADR gets authored now")
> and has not ruled on the option, so the two are different acts and only one of them has happened.
> An ADR marked accepted on a stamp nobody gave is a fabricated citation about the project's own
> governance. The interval this spends `proposed` is part of the record.

The linking frontier is **script-level machinery**, not contract §3's host-linking story — measured
on #157 and now re-measured against the current board, since #158 moved 4876 vectors and a census
quoted from the pre-#158 era would be a number nobody re-ran. What is missing is a map from a
`(register "name")` string to a **module instance**, plus the ability for an action to name a module.
There is no host-function machinery to design.

## The consumer, measured

Every figure below comes from a throwaway probe inside `internal/spec` over `boardFiles`, reading
`run(s).Buckets` and the raw `node` tree — never a grep over printed board lines (grave #129/#150),
and never `suitePaths`, whose 254 files are **not** the board's population: the probe's first run
reported unsupported 27627 against the board's 27501, which is a census of a different corpus than
the counts are quoted from. Scoped to `boardFiles`, the totals reconcile (fail 1699, unsupported
27501). The probe was deleted; the numbers are the product.

**Fail column — 605 vectors carry the §3 sentinel**, over four mechanisms, residual **0**:

| mechanism | fails | site |
| --- | --- | --- |
| `call_indirect` reaching a table slot naming an imported function | 540 | `call.go:250` |
| an imported memory | 44 | `memory.go:300` |
| an imported table | 11 | `table.go:204` |
| an imported global | 7 | `global.go:87` |
| `call` naming an imported function directly | 3 | `call.go:99` |

The 605 is up from #157's 531 and #149's 486 because the interpreter has landed arms since; the
partition is unchanged in shape, and the mechanism sum closing to zero is asserted rather than
eyeballed. By file, it is two files and a tail: `table_copy.wast` 228, `table_copy64.wast` 228,
`table_init.wast` 42, `table_init64.wast` 42, `imports.wast` 31, `memory_grow.wast` 20, then nine
files holding 1–5 each.

**Unsupported column — 278 commands with no harness Kind**: 200 `assert_unlinkable`, 78 `register`.

**Module-named actions — 142**: `(invoke $M …)` ×132, `(get $M …)` ×10, concentrated in
`linking.wast` (83), `elem.wast` (17), `linking2/1/3.wast` (21 together).

**Who supplies the imports — 874 sites over the board's files:**

| supplier | sites |
| --- | --- |
| a same-file `(register "n")` | 678 |
| the harness's `spectest` builtin | 174 |
| nothing any script supplies | 22 |

The 22 are all `assert_invalid`: validation rejects before linking is reached, so they need no
supplier. **No vector in the corpus needs a host-supplied import that a script does not itself
define** — which is the standing doctrine in `CLAUDE.md` (*no host-linking at v0, because the oracle
never asks for it*) confirmed a second time by a second instrument.

`spectest` is asked for **15 distinct item names over 174 sites**, and the distribution is why it is
cheap: `memory` 63, `table` 33, `global_i32` 31, `print_i32` 24, `unknown` 5 (a deliberately absent
name — `assert_unlinkable` fodder), `print` 4, then ten names with 1–3 sites each. Every value in
`interpreter/host/spectest.ml` is a constant: memory 1..2 pages (`:30-32`), `table`/`table64`
10..20 funcref all null (`:22-28`), four `global_*` — `i32`/`i64` holding **666**, `f32`/`f64`
holding **666.6** (`:13-16`) — and seven `print_*` taking params and returning `[]` (`:42-45`).

**`spectest` has 14 exports, not the 15 #157 recorded, and the corpus imports all 14.** Counted off
`lookup`'s arms (`:49-63`, `lookup` at `:48`): four globals, seven prints, `table`, `table64`, `memory`. The corpus's
15th name is `unknown`, which `spectest` deliberately does **not** export — so #157 read the
corpus's distinct-name count as the export count, and its "five `global_*`" is the same error seen
from the other side: one phantom global reconciles both figures at once, which is what says this is
one miscount rather than two. The load-bearing claim survives — every name any vector asks for is a
constant, and there is no host-function machinery to design — and correcting a figure quoted
*beside* the load-bearing one is exactly the class #157's own closing comment named.

### The pre-selection probe, run first per the standing rule

Bucket size × sole-blocker fraction, measured before the bucket is chosen. The cheap variant — the
whole bucket profile of every file holding a §3 fail — says the 605 is **not** a shadow:

```
  table_copy.wast     pass=1499 fail=228 unsup=1  gated=0    other buckets: none
  table_copy64.wast   pass=1477 fail=228 unsup=1  gated=22   other buckets: none
  table_init.wast     pass=681  fail=43  unsup=68 gated=0    other: 1 (a GC element type)
  table_init64.wast   pass=684  fail=43  unsup=68 gated=93   other: 1 (a GC element type)
  imports.wast        pass=85   fail=31  unsup=100 gated=2   other buckets: none
  memory_grow.wast    pass=30   fail=20  unsup=1  gated=0    other buckets: none
```

The four largest files have **no second fail bucket at all** — the §3 decline is the only thing
standing between them and a verdict, which is the *sole-blocker* clause of the census rule rather
than the *shared deeper blocker* clause that priced #161's 609 at zero. That is a forecast, not a
result: it says these vectors have no other *reported* refusal, and the honest residual risk is the
one partition-by-mechanism is structurally blind to — a second refusal invisible until the first is
gone. What bounds it here is that `table_copy.wast` already passes **1499** vectors, so the file's
machinery is demonstrably reachable, and the 228 are that same module's own read-backs: **116
`check_t1` and 112 `check_t0`** — read off each failure's source line, not assumed. (This paragraph
first said "`check_t0` reads", which is half the population; the split is 116/112 across two
tables.) `table_init.wast`'s 42 are all `check`, and `imports.wast`'s 31 spread over `call` 10,
`load` 8, `grow` 5 and eight singletons.

## Decision

**The registry is a name→instance map in the harness, and an import is satisfied by an export of a
previously-instantiated module in the same script.** Three parts, and each is the reference's own
shape rather than an invention:

1. **`Script.run` keeps a `map[string]Instance`**, written by `(register "n")` and read when a later
   module's imports are resolved. This is `runner.ml:314-355`'s `instances` and `registry` maps,
   collapsed to one: the reference separates them because `register_virtual` also serves
   `spectest`, and so does this decision (part 3).
2. **Actions may name a module.** `(invoke $M "f")` and `(get $M "g")` select from a
   `map[string]Instance` keyed by the script-level `$name`, which is `lookup_instance`. This is a
   second map, not the same one: `register` keys by the *export name string*, a `$name` keys by the
   script identifier, and one module can have either, both, or neither.
3. **`spectest` is a synthesized module, defined once in the harness as wat source**, instantiated
   through the same path every other module takes and registered under `"spectest"` before the
   script runs. Not a special case in the import resolver.

**Retention is forced by consumers, but shaped by the grammar** (Scott, on 0016) — and the grammar
here is `runner.ml`'s, so the shape is two maps and a pre-registered builtin.

## Question 1: where does the registry live — harness or engine?

- **A — in `internal/spec`, the harness (chosen).** `register` is a *script* command; it has no
  binary encoding and no presence in a module. An engine-side registry would be contract §3's API
  surface, which the doctrine in `CLAUDE.md` declines at v0 on a measured negative: §3 would answer
  605 of 1699 fails and **every one of them** is satisfied by another module in the same script or
  by `spectest`, never by a Go host function. Building the §3 surface now is design in the
  load-bearing spot with no witness (0006), because the oracle is structurally incapable of scoring
  it — a conformance corpus cannot specify an embedder.
- **B — in `internal/interp`, as a `Linker` or `Store`.** Rejected for v0, and it is the option that
  looks like the real engineering. It is also the one that reads the thesis backwards: §3's host
  contract is this engine's *most* Go-shaped feature, so designing it from the suite's requirements
  would shape Go's linking surface to the specification of a test corpus. The right consumer for B
  is a Go guest, and there isn't one yet.

**Direction (§0):** A keeps the §3 API surface unshaped until its Go-shaped consumer exists, which
is the thesis clause — *host contract designed to the specification of the Go runtime* — declining
to be designed by something else.

## Question 2: what does a funcref hold, once a table slot can name another instance?

This is the question Scott named as the core one, and it is where the harness decision above stops
being sufficient: `interp.ref` is `{Null bool; Addr uint32}`, and `Addr` is a **function index in
the current module**. The moment a table slot may hold a function belonging to *another instance*,
that representation is wrong — not slow, wrong — because index 3 means two different functions.

`table_copy.wast` is exactly this shape: module `a` exports `ef0`..`ef4`, `(register "a")`, then a
second module imports all five and builds 30-slot tables over funcrefs to them. So the 456-then-540
bucket is not "linking" in the abstract; it is one instance's table holding another instance's
functions.

Three options, and the corpus prices the difference:

- **A — `ref{Null, Addr}` gains an `Inst *Instance` field.** A funcref is a *pair*: which instance,
  which index. This is `instance.ml:21`'s `funcinst = moduleinst Lib.Promise.t Func.t` — the
  reference's funcref carries its module instance, and the `Promise` is there because a function
  and its instance are mutually recursive at allocation time.
- **B — a flat `[]*funcInstance` store in the harness, `Addr` an index into it.** Wasmtime's shape.
  Denser and it is what a production engine does; rejected for v0 on the same premature-generality
  ground 0016 rejected its arena option — the store's lifetime rules only pay for themselves with
  many instances, and it makes `Addr` mean something the *module* cannot resolve on its own, which
  moves a decoder-adjacent fact into a runtime table.
- **C — copy the callee's `Func` into the slot.** Rejected on correctness, not cost: a function's
  behaviour depends on its instance's memory, tables and globals, so a copied body executing against
  the *caller's* state is a wrong answer rather than a missing feature. This is the option that
  would pass `table_copy.wast`'s `check_t0` reads (they return constants) and be wrong for anything
  reading memory — an accept-direction defect the suite scores green by construction (§9 G-3), which
  is precisely the class this project refuses to buy pass count with.

**Recommendation: A.** Two words become three, and `Instr` is untouched — `ref` is a *value*, not an
instruction, and 0002's two-word pin is about `Instr`. The GC-precision pin holds: `refs` is already
a parallel array precisely so the collector can see what a reference names, and an `*Instance`
pointer is the first thing that array has ever held that the collector genuinely must trace. So A is
the option the 0002 pin was *for*.

**Direction (§0):** A is the shape a Go guest wants. A Go guest is one instance with a large table;
its funcrefs point into itself, so the `Inst` field is one comparison in the fast path and never a
store lookup. B optimizes for many instances — §1's disclaimed workload, the same clause that killed
0002's side table.

### The observability question, asked because a clean answer would be a tell

Does the corpus ask about a funcref's **identity**, or only about calling it? If nothing compares
two funcrefs, a representation that cannot distinguish them is unobservable and the argument above
is aesthetic. Counted over the board's files: `ref.func` **2149** sites, `ref.test` 121, `ref.cast`
64, `ref.is_null` 21, `ref.eq` 16, `ref.as_non_null` 12. And the cross-instance population
specifically — files with `ref.func` that also `register` or `import` — is fifteen files led by
`table_init64.wast` (602), `table_init.wast` (575), `table_copy.wast` (360), `table_copy64.wast`
(360), `elem.wast` (45), `type-subtyping.wast` (40), `ref_func.wast` (27).

So identity is asked about, at scale, in exactly the files that cross instances. Note what `ref.eq`
at 16 does *not* mean: `eq_ref'` in `instance.ml:38-43` **fails** on two `FuncRef`s — the reference
refuses to compare funcrefs for equality — so the identity that matters is not `ref.eq`'s, it is
*which function actually runs*, which is `call_indirect`'s 540.

## Consequences

- **The 605 fails and the 278 unsupported commands become answerable**, and the honest statement of
  reward is that these are two different jobs with two different unlocks: the harness registry
  (Q1) admits `register`, `assert_unlinkable`, and the 142 module-named actions; the funcref
  representation (Q2) is what makes the 540 `call_indirect` fails *correct* rather than merely
  reached. Either alone leaves the other's column standing.
- **This ADR earns one implementation, and it is not both halves.** *One ADR earns one
  implementation* — so the split is at the same seam Scott ruled on #142: Q1's harness registry and
  Q2's `ref` widening are separate PRs, and the ADR records both because the second decision is
  unmakeable without the first (a funcref needs an instance to name before it can name one).
- **A skip is not a verdict, and `spectest` is where that bites.** Synthesizing it as wat means the
  harness's own module goes through `text.ReadModule` → `binary.DecodeModule` → `Instantiate`, so a
  `spectest` that failed to build would make every importing vector report no-instance rather than
  silently passing. That is the intended direction, and the control is a floor: **15 exports
  present**, asserted, not `err == nil`.
- **#149 is not resolved by this and is not contradicted by it.** #149 asks whether the §3 fails
  should be *re-verdicted* as `unimplemented`, and found the load-bearing premise false (capability
  assignment is static; this gap is dynamic). This ADR proposes to make them **pass** instead, which
  makes #149's question smaller rather than answering it — whatever remains un-drained after Q1 and
  Q2 is what #149 is actually about, and that number is not knowable until they land.
- **No contract text changes**, and one sentence in `CLAUDE.md` needs re-reading rather than
  rewriting: the doctrine says §3's *host-linking* has no suite reward, which this ADR confirms and
  builds around rather than against. What it adds is that the *script-level* machinery beside it has
  605 + 278, and that the two are different mechanisms — which the doctrine already said, in the
  clause naming the script-level registry as "harness work with an ADR of its own, waiting for its
  consumer". This is that ADR; the consumer knocked twice.
- **`interp.Value` stays reference-free.** The boundary type deliberately holds no reference values
  (`value.go:213-233`, 0006's load-bearing-spot rule), and nothing here changes that: the harness
  reads a funcref only as an `assert_return` expectation, which `spec.Val` already models. Widening
  the *host* boundary is §4's work and has no consumer in this ADR.

## What this decision does not do

It does not implement contract §3. There is no host-function surface, no `Linker` type, no way for a
Go embedder to supply an import — and the negative is *stated* rather than left implied, because an
unrecorded "we looked and there was nothing there" gets re-looked-for (the ruling on #157). The
engine will need all of it at v1; the suite will never ask for any of it.

## Correction, appended 2026-08-06 — the census is 624, not 605

**Era stamp: the figures above were measured by *key partition*; this section re-measures the same
board by *two-axis cross-tab*.** The body is preserved, per Scott's ruling on the census law
(*records append-corrected, stale claims wear pointers*): editing 605 into 624 would fabricate a
history in which this ADR always knew the right number, and the real history is worth more — the
gap is exactly the blind spot the body's own residual-risk paragraph predicted, so the record shows
its instrument being upgraded rather than merely asserting an answer.

**The law this applies, minted by Scott on this measurement: *Expected and Got are different facts,
and keying a census on either alone undercounts.***

Re-measured on `main` at `85e1d08`, same corpus pin, by a throwaway probe that cross-tabs each
sentinel-bearing `Failure` on **both** axes — whether the *bucket key* carries the sentinel, and
which engine site the *`Got`* names. Residual 0 on both. The probe was deleted; the numbers are
the product.

| mechanism | key-visible (as published) | key-hidden | **corrected** | site |
| --- | --- | --- | --- | --- |
| `call_indirect` reaching a table slot naming an imported function | 540 | 0 | **540** | `call.go:250` |
| an imported memory | 44 | 11 | **55** | `memory.go:300` |
| an imported table | 11 | 6 | **17** | `table.go:204` |
| an imported global | 7 | 2 | **9** | `global.go:87` |
| `call` naming an imported function directly | 3 | 0 | **3** | `call.go:99` |
| **total** | **605** | **19** | **624** | |

**Why the 19 hid, and it is a mechanism rather than an oversight.** All nineteen are
`assert_trap (module)` commands — a module command whose *expectation* is a trap. `Result.Buckets`
keys such a failure by the **`Expect`** (`assert_trap (module) expected: out of bounds memory
access` ×13, `… table access` ×6), so the §3 sentinel appears only in the `Got`. A census that
partitions on the bucket key therefore cannot see them *by construction*: it is not that the probe
looked and missed, it is that the axis it read does not carry the fact. By file: `data.wast` 4,
`data1.wast` 4, `elem.wast` 4, `linking.wast` 4, `linking1.wast` 2, `linking3.wast` 1.

**Direction of the error, stated because it matters for how the figure was used.** The undercount
is 3.0%, it is *concentrated entirely in the three non-func mechanisms* — memory 44→55 is +25%,
table 11→17 is +55%, global 7→9 is +29% — and the 540 that dominates the total is unaffected. So
every conclusion the body draws is unchanged and two are strengthened: the consumer is larger than
claimed, and the mechanism partition's *shape* survives, which is the property the body asserted.
Nothing here revises the decision; it revises one number and sharpens the instrument that produced
it.

**The body's own caveat predicted this, in these words:** *"the honest residual risk is the one
partition-by-mechanism is structurally blind to — a second refusal invisible until the first is
gone."* That paragraph was written about a *second refusal behind* the §3 decline and the blind spot
turned out to be one axis over — a second *key* in front of it — but the class it named is the class
that fired. Recorded rather than smoothed, because a caveat that half-predicts its own failure is
the most useful sentence in a census.

**A second defect, found by appending rather than by measuring, and recorded for the same reason.**
The body's census prose reads *"over four mechanisms"* above a table with **five** rows. The table is
right and sums to 605; the total is right; only the word is wrong, and no arithmetic depends on it —
which is precisely why it survived review. It is the same shape as the figure above it: a claim whose
checkable half was checked and whose uncheckable half was not, in a document arguing that a census
needs both axes. Left in place, wearing this pointer.

**Where the superseded 605 still stands in the body**, so this section can serve as its index rather
than the body carrying inline edits: the census heading and its table, the up-from-#157 paragraph,
the shadow-check paragraph, the Q1 consumer count, and both Consequences mentions. Every one of them
is sound as an argument — the corrected total moves each figure the same direction by 3.0% and
reverses no conclusion. **Flagged for Scott:** whether those six sites should also carry inline
`(→ 624)` pointers is his call, not the actor's; a pointer preserves the history that editing would
fabricate, but *placing* one is still a hand in the body, and the rule as given was "never edit the
body".
