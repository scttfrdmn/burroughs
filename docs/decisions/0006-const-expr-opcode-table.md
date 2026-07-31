# 0006 — Where the const-expression opcode table lives

Date: 2026-07-30 · Status: **accepted** (Scott, 2026-07-31)
Issues: #25 (const-expr grammars), #7 (interpreter core)
Contract refs: §9 (G-2, G-3)

## Decision

**Option B — a `constexpr` reader in `internal/binary`, no shared table yet.**

chat-Claude's shared-table steer is withdrawn: the drift risk was right, the
remedy wrong. Shaping #7's central structure from the decoder's requirements
before a second consumer exists is speculative design in the load-bearing spot,
and today `internal/interp` holds only a benchmark — so "shared from the start"
would be shared with nobody.

**The condition that makes the yield safe, and it is normative here, not a
follow-up:** the agreement test below is **part of #7's definition of done.** The
drift risk is only "convertible into a failing test" if the conversion is a
recorded obligation with a tripwire rather than an intention. Declared now so it
cannot read as a surprise later — the `ErrDataCountRequired` manoeuvre applied to
a design debt.

### The agreement test (pre-registered, §9 obligation on #7)

When the interpreter's opcode table exists, a test asserts that **the table's
const-legal subset and the decoder's `constexpr` reader answer identically over
the full opcode space** — not over the eight opcodes this ADR needs, and not over
the vectors the suite happens to carry.

Three properties, because "identically" has to be said precisely:

1. **Same membership.** For every one of the 256 single-byte opcodes and every
   tracked multi-byte prefix, the two agree on whether it is const-legal. Over the
   *whole* space, so an opcode either side adds later is covered by construction
   rather than by someone remembering to extend a list — a table walked
   exhaustively cannot drift silently in its uncovered region, because it has none.
2. **Same immediate extent.** For every const-legal opcode, both consume the same
   number of bytes from the same input. This is the fact that goes wrong quietly
   when duplicated: a `memarg` read as one LEB instead of two shifts every
   subsequent byte, and the error surfaces far from its cause.
3. **Same rejection.** For every opcode the decoder rejects as not const-legal,
   the interpreter's table agrees it is absent from the const subset — the
   accept-direction half, which is the direction the suite cannot catch, since it
   has no valid-const-expr-that-must-be-rejected vectors.

Failure of any of the three is a **fail, not a skip**: if #7's table is absent the
test asserts nothing and must say so through `internal/testenv`, per *a skip is
not a verdict*. And it is written to fail first — the falsifiability discipline
applies to this control as much as any other, so it is checked against a
deliberately mismatched extent before being trusted.

Filed as **#33**, attached to the `v0 interpreter` milestone, so the obligation is
queryable and not resident in this file alone.

## Question

The decoder must read constant expressions to decode the global, element, and
data sections (#25). A const-expr is *not* length-prefixed: its extent is
discovered by reading instructions until `END`. So the decoder needs to know
opcodes — encodings, and how many immediate bytes each carries — and that is the
first place the decoder and the interpreter (#7) want the same table.

Decide where that knowledge lives before writing it, per decision-before-code.

---

*Deliberation as written before the decision follows, unedited.*

## Measured facts

Everything below was checked against the vendored suite, not inferred.

### The blocked buckets are smaller than #25 claims

#25 attributes 12 accepted `binary.wast` lines to the const-expr grammars. Read
individually, **three of the twelve are function-body vectors** and belong to #7
or to code-section decoding, not to this work:

| Line | Needs | Expected string |
|---|---|---|
| 76 | code section: body missing `END` | unexpected end of section or function |
| 92 | code section: `\0b` of a following data section eaten as `END` | unexpected end of section or function |
| 922 | code section: `br_table` with an under-declared target vector | unexpected end of section or function |

The nine that genuinely belong to #25:

| Line | Section | Shape |
|---|---|---|
| 112 | global | init expr missing its `END` |
| 703 | global | count 2 declared, 1 given |
| 714 | global | count 1 declared, 2 given (the grammar-long size sign) |
| 373 | element | passive segment with a non-funcref reftype |
| 825 | element | count 1 declared, 2 given |
| 851 | data | count 2 declared, 1 given |
| 864 | data | count 1 declared, 2 given |
| 877 | data | segment byte-vec length past the section |
| 891 | data | truncated segment |

**Consequence for the board:** #25 lands 9 of the 12, not 12, and the two
buckets it is said to close do *not* reach zero — lines 76, 92, and 922 keep
*unexpected end of section or function* at 3. That is worth writing down now,
because "a bucket reaching zero is a PR's measure of done" and this issue's
measure of done was overstated. Correcting it here rather than discovering it at
merge.

### The opcode set the decoder needs is small and closed

Across all nine vectors, every const-expr uses only:

```
0x41 i32.const     0x23 global.get
0x42 i64.const     0xD0 ref.null
0x43 f32.const     0xD2 ref.func
0x44 f64.const     0x0B END
```

Eight opcodes. The spec's `constexpr` production is exactly this set for the
tracked MVP (plus WasmGC and extended-const additions behind gates). It is
closed by the grammar, not by what the suite happens to exercise — which matters,
because a set that is closed only empirically would be overfitting to the oracle.

### `internal/interp` has no opcode table to share

`internal/interp/` contains only `dispatchbench/`, which is benchmark-only and
deliberately duplicates its own instruction shapes (a documented exception under
the spirit clause). #7 is blocked on harness phase 2 for acceptance evidence, so
**there is no table to depend on today.** Option A is not "share the table," it
is "write #7's table now, early, to be shared."

---

## Options

### A — one opcode table now, in a package both will import

Write the full MVP opcode table (encoding, immediate shapes, names) as the
decoder's first need, positioned for #7 to consume.

- **For:** one definition of an opcode's immediate shape, ever. The immediate
  shapes are exactly the kind of fact that goes wrong quietly when duplicated —
  a `memarg` read as one LEB instead of two shifts every subsequent byte, and the
  error surfaces far from the cause.
- **Against:** it designs #7's central data structure *from the decoder's
  requirements*, while #7's own ADR (0002) commits to an internal-form rewrite
  with pre-decoded immediates and resolved branch targets. The decoder wants a
  byte-shape table; the interpreter wants `[]ins`. Building the shared thing
  before the second consumer exists is how the shared thing ends up shaped for
  the first consumer only, and 0002 already pinned that the interpreter's form is
  not the wire form.
- **Cost if wrong:** #7 inherits a table it must reshape, with the decoder
  depending on it.

### B — a const-expr-only reader in `internal/binary`, merged later if it pays

Read the eight opcodes where they are needed. When #7 lands its table, revisit.

- **For:** the eight opcodes are a *closed grammatical set*, not a sample — so
  this is not a partial copy of the MVP table that will drift toward it. It is
  the `constexpr` production, which is its own production in the spec.
  Duplication that mirrors a real distinction in the spec is not duplication.
  Also: the decoder validates *shape*, the interpreter *executes*; `f32.const`'s
  four bytes are all the decoder cares about and none of what the interpreter
  cares about.
- **Against:** two places will know that `0x41` takes a signed LEB. If #7's table
  later disagrees, the disagreement is silent until a vector catches it — and
  const-exprs are thinly covered, so the vector may not exist.
- **Mitigation, and it is the point:** the disagreement is *testable without
  either being canonical*. A test that reads every const-expr opcode both ways
  and asserts identical extents converts "two places might drift" into a control
  that fails when they do. Under B, that test is written when #7's table appears.

### C — B, plus a shared immediates *table* but not a shared instruction type

Duplicate nothing factual: put the opcode→immediate-shape mapping in one small
place, and let each consumer build its own representation on top.

- **For:** splits the two options' concerns along the line the objection
  actually falls on. The thing that goes wrong when duplicated is the *immediate
  shape*; the thing that wants to differ is the *representation*. A and B each
  bundle both.
- **Against:** a third package with one map in it, before there is a second
  consumer, is speculative structure — the cost A pays, smaller.

---

## Recommendation

**B**, with the drift control written as a *precondition* of #7's table rather
than a follow-up.

The reason is the one the project keeps rediscovering: the risk in B is
duplication drifting silently, and that risk is convertible into a failing test,
whereas A's risk — a shared structure shaped by its only consumer — is not
convertible into anything, because there is no second consumer to disagree with
it yet. Prefer the risk that a control can catch. It also keeps #25 unblocked
without touching #7's design surface while #7 is still blocked on harness phase 2.

What B commits to, so it is not a deferral wearing a disguise:

1. The reader handles the **`constexpr` production**, named as that, with a
   comment stating the set is closed by the grammar and not by the vectors.
2. Gated additions (WasmGC's `ref.i31`, extended-const's arithmetic) route
   through the features set, never widen the base set — gates partition
   acceptance, they do not redraw the grammar (§9 G-2).
3. When #7 lands an opcode table, a cross-check test asserting identical
   immediate extents for the eight shared opcodes is part of *that* PR, not a
   TODO. Filed as an issue at this ADR's acceptance so it is tracked, not
   remembered.

## Consequences

**The accepted condition is wider than the deliberation's item 3 above,** which
proposed a cross-check over "the eight shared opcodes." That is superseded: the
agreement test walks the **full opcode space**. Recorded here rather than edited
into the deliberation, which stands as written — and the widening matters, because
a cross-check scoped to the eight opcodes this ADR happens to need would be the
overfitting failure applied to a control: it would pass while saying nothing about
the opcode either side adds next.

Accepted:

- #25 is unblocked and goes to work on **nine** vectors, not twelve (see the
  measured facts above). *unexpected end of section or function* goes 6 → 3;
  *section size mismatch* goes 5 → 0.
- Two places will know that `0x41` takes a signed LEB, for as long as it takes #7
  to land. That is a real debt, held with a tripwire rather than a promise.
- #7's definition of done grows a test it must write. Deliberately: that is the
  price of not designing its central structure early, and it is cheaper than the
  reshaping cost option A would have imposed.

Declined, with the reason recorded so it is not relitigated silently:

- **Option A** — no second consumer exists to disagree with a shared table, so it
  would be shaped by the decoder alone, against ADR 0002's pin that the
  interpreter's form (`[]ins`, pre-decoded immediates, resolved branch targets) is
  not the wire form.
- **Option C** — a third package holding one map, before a second consumer, is
  option A's speculative-structure cost in miniature. Revisit only if the
  agreement test starts failing for reasons that are not bugs.

---

## Postscript: two corrections to the measured facts (added after #35)

An ADR is an accepted record, so the text above stands as written. These are the
places the implementation measured something different, appended rather than
edited — the same rule the issue tracker follows, for the same reason: a record
that can be rewritten cannot be cited.

**1. The nine-vector table omits `binary.wast:345`.** That line is a passive
element segment containing `\f3`, expecting `illegal opcode` — squarely a #25
grammar and reached by the element-segment reader. The count in *Accepted* should
read **ten** vectors, and #35's measured board is `binary.wast` 104/127 → 114/127,
exactly +10. The omission happened because the table was assembled from the two
buckets #25's body named, and :345's bucket (`illegal opcode`) was not one of them —
which is the enumeration failure this ADR's own §33 widening warns about, committed
in the ADR that argued against it.

**2. `section size mismatch` goes 5 → 1, not 5 → 0.** The remaining vector is
`binary.wast:92`, correctly identified above as a code-section vector; it is in this
bucket rather than the *unexpected end* bucket the table places it in. So both
buckets #25 was said to close stay open, at 1 and 3, and the honest statement is
that #25 drains them to their code-section residue.

**3. `:345` stays failing after #35, deliberately.** Not a shortfall against this
ADR — a consequence of it. The reader cannot distinguish "no such opcode"
(malformed, `illegal opcode`) from "real opcode, not constant" (invalid, `constant
expression required`, which the suite asserts 22 times and always as
`assert_invalid`) without the existence question answered over the whole opcode
space. That is #7's table, which is precisely what this ADR declined to build early.
So `ErrNonConstantExpr` matches no spec string, :345 keeps failing, and #33 is what
will let it pass. The ADR's debt and this board line are the same fact seen twice.

**4. `binary.wast:714` is not the grammar-long size sign.** The table above labels it
so, following #25's body. Measured, it is `declared 11, grammar consumed 6` — the
same sign as the type-section vector at :469. No suite vector exercises the
grammar-long direction, which is grave #34.
