# 0036 — #9's exception-handling family closes the single-byte space as slice 10, and its criterion is twenty-five declines plus four admissions

Date: 2026-08-17 · Status: **accepted** — ordered by Scott on the #392 relay

> *"Make the scope change. Type the exception-handling family. The out-of-scope entry was written
> when there was no validator to be out of scope of. It's 25 of the 33 remaining declines, against
> #391's 2 — and closing the single-byte space outright turns a count into a statable property of v0,
> which is worth more than either number. Same shape as the subtyping boundary move: short ADR
> recording the move with the 25 rows as its criterion, the entry retired by measured migration
> rather than by assertion, false text kept where it stood. #391 rides it."* — Scott. Deliberation is
> #393; this is its tombstone. The boundary move is ordered; the wording of this record is the
> agent's and is reviewable as any other.

## Context

This is a **boundary move**, and that is the difference between it and slice 9. Slice 9's own
Consequences drew the line:

> *"No boundary moves here, and that is a difference from slice 5. Tail calls were never in
> `validate.go`'s out-of-scope list; they were declined for want of arms. Exception handling *is* in
> that list."*

The list — `validate.go`'s out-of-scope register — reads, in the text this slice retires:

> *"Out of scope by declaration, each with its own expected string in the suite and so its own
> measurable slice: constant expressions (24), limits (16), and **exception handling**."*

That entry was accurate when written and is no longer, for a reason it could not have stated about
itself: **it was written when there was no validator for it to be out of scope of.** The register
dates from a package that decoded exception handling and typed nothing at all; "out of scope" then
distinguished nothing, because everything was. What has changed underneath it is that every other
single-byte opcode has been typed — 0xFD in slice 2, 0xFC in slice 5, 0xFB in slice 7, the reference
instructions across 6 and 8, tail calls in 9 — so the entry now holds, alone, the **last three bytes
of the single-byte space**. A declared boundary whose remaining content is "the part that would make
the sentence *every retained single-byte opcode is typed* true" is not reserving a slice; it is
reserving a caveat.

Retention is already complete, which is why the slice is small and why the boundary is the only thing
in the way: `internal/binary` carries `Catch{Kind, TagIndex, LabelIndex}`, `Catches`,
`CatchVector`, the four `CatchKind` constants, `HeapExn`/`HeapNoExn`, `Tag{TypeIndex}` and a tag
import's type index (`decodeImport` case `0x04`, #204). `internal/interp` already **executes**
exception handling (#199 rung 2), and `ErrUnknownTag` already exists. As with slice 9's pair, nothing
is missing but the typing rules — but unlike slice 9's pair, a decision is required first, because the
package has said in writing that it would not write them.

## Decision

**Type `throw` (0x08), `throw_ref` (0x0a) and `try_table` (0x1f) plus the module-level
`check_tagtype` rule, and retire the register's exception-handling entry by measured migration.**

- `throw` — `valid.ml:572-576`. Tag index → deftype → functype; pop the parameters, then polymorphic.
- `throw_ref` — `valid.ml:578-579`. Pop a nullable `exnref`, then polymorphic.
- `try_table` — `valid.ml:581-586`. Blocktype, frame pushed with the result types, and **catch clauses
  checked in the *enclosing* context, not the block's** — `check_catch c ct ts2` takes `c`, not `c'`,
  so a clause's label depths are numbered outside the `try_table` it is attached to. Four clause
  forms against `label c x`: `catch` matches the tag's parameters, `catch_ref` those plus
  `(ref exn)`, `catch_all` the empty list, `catch_all_ref` just `(ref exn)`.
- `check_tagtype` — `valid.ml:191-195`, `non-empty tag result type`, a **new sentinel**. It is reached
  both by `check_tag` and by `check_externtype`'s `ExternTagT` arm, so it applies to **imported tags
  too**, and it goes at `check_tag`'s position in `modulePre`'s reference order — where the phase
  table currently reads `check_tag  tags  — gated proposal`.

**Not decided here, deliberately:** the shape of the operand-sequence matcher. `throw` needs
`match_stack`'s wording and `pop`'s padding rule (below), and whether that lands as a new helper or a
generalization of `matchLabel` is implementation shape recorded at the site, as every other ported
rule's has been.

**#391 rides**, per Scott's scheduling ruling — its two admissions have a named cause and should not be
their own artifact. It is not an instruction rule and is not part of this criterion; it is counted
separately below because it lands in a different lane.

## Criterion

**Twenty-five declines, pre-registered per file, per direction and per sentinel, measured over the
all-on lane with `RunGated(…).Buckets`** — not from a grep over board text (#161). 25 = the 33 declines
slice 9 left, minus 8 relaxed SIMD.

| direction | population | now | required |
|---|---|---|---|
| reject | 14 `assert_invalid (module)` | `declined` | **pass** |
| accept | 11 `module text` definitions | `declined` | **pass** |
| admission | 2 `tag.wast:18,22` | **accepted** | **pass** |

Per file: `try_table.wast` 15 (6 accept / 9 reject), `throw.wast` 4 (1/3), `throw_ref.wast` 3 (1/2),
`instance.wast` 3 (3/0). Per opcode named in the decline: `try_table` 16, `throw` 7, `throw_ref` 2.
Line coordinates: `instance.wast` 15/62/128 · `throw.wast` 3/51/52/54 · `throw_ref.wast` 3/117/118 ·
`try_table.wast` 3/10/342/376/386/390/395/399/403/407/411/420/470/483/499.

**`instance.wast`'s three are the tell that this family is not confined to its own files.** They are
`module text` definitions that happen to contain a `try_table`, and their decline costs eleven
downstream `no instance` rows in the same file — so the accept side of this criterion is worth more
than eleven ceiling-bound rows suggest, and that surplus is not forecast here because it is not this
family's to claim.

### The reject side's discriminator is a wording pin, not a count

| expected | rows |
|---|---|
| `type mismatch` | 11 |
| `type mismatch: instruction requires [i32] but stack has []` | 1 |
| `type mismatch: instruction requires [i32] but stack has [i64]` | 1 |
| `unknown tag 0` | 1 |

**A rule that refused all 14 with a blanket `ErrTypeMismatch` scores 11 of 14** — a weaker
populational discriminator than slice 9's 23-of-28, so the criterion leans on the three that
disagree, and two of them are unusually load-bearing:
`grep -rn "instruction requires" testdata/spec/*.wast` returns **exactly two rows, both in
`throw.wast`**. They are the corpus's *only* vectors pinning the reference's operand-mismatch wording,
and our `popExpect` says `expected i32, stack empty`, which does not contain it. So passing them
requires reproducing `match_stack`'s sentence *and* `pop`'s padding rule — pad with bottom **only when
the frame is unreachable** — where `peekN` pads unconditionally and would print `bot` where the
reference prints `[]`. Two rows are a thin witness for a rule that will be reused; that thinness is
stated here so the falsification bill is aimed at it rather than at the eleven.

The **accept** direction remains the weaker half, per 0032's reading: a `module text` definition is a
working module, so a decline naming one opcode means only that the validator stopped there. 11 is a
ceiling on that side and 14 is a count on the other.

## Consequences

- **The single-byte opcode space closes, and that is the artifact.** The count is the smaller half of
  what this buys: *every single-byte opcode the decoder retains is typed* becomes a property of v0
  that a control can assert as **emptiness**, rather than a shrinking set that has needed renaming
  once per slice. Scott priced it that way and the ADR records the price: worth more than 25 or than
  #391's 2.
- **The register loses its third-to-last entry, dropping to two: constant expressions (24) and limits
  (16).** Those figures are re-measured rather than carried, since a register's surviving numbers are
  as stale as its retired ones and nothing has been asserting them. The retired text stays where it
  stood, quoted, as 0031's did — *retirement is recorded, not absorbed.*
- **Two controls lose their population, and neither is closed as no-longer-applicable.**
  `TestSingleByteDeclinesAreExactlyExceptionHandling` becomes an emptiness assertion over the same
  derived set, keeping its `namedRowFloor`/`namedRowExact` walk extent as the non-vacuity argument —
  its own comment prescribed exactly this, that the slice draining it *retire* rather than rename it a
  third time. `TestDeclinesAreDeclinesAndNameTheirOpcode`'s single-byte row is deleted on the
  instruction `stillDeclinedSingleByte` already carries for an empty set; the "a decline names what it
  declined" assertion survives in the 0xFE-prefixed row. **The risk outlives the specimens** — a newly
  retained single-byte opcode arriving untyped — so the tripwire is re-pointed at emptiness and not
  retired with its subject.
- **A gate-campaign slice, but the default lane's reward is *not* structurally zero, and that is
  forecast rather than discovered.** `ExceptionHandling` is absent from `DefaultFeatures()`, so all 25
  declines and both tag admissions are all-on-lane only. #391's two rows are not: neither
  `(module (table funcref (elem 0 0)))` contains a gated opcode, so both are admissions in the default
  lane as well, and the default-lane delta is **2**. This is grave #390's transfer error applied
  before the forecast instead of after — *a repair rides the lane its own call site ships on, not the
  lane its discoverer was working in.*
- **`unsupported` cannot move, and the delta is structural.** What the harness can *ask* is unchanged;
  the three `try_table.wast` rows reading *"result 0 has type (ref null 0), which the harness cannot
  represent"* are a harness representation gap and stay exactly where they are. The reward figures with
  a subject are the two lane deltas above.
- **The rollback is the three dispatch arms, the `check_tagtype` phase, and the elem-index resolution**
  — deleting them restores the 25 declines, the 2 tag admissions and #391 exactly.
- **The closing total will be checked three ways.** An exactly-closing total is the one result a
  pre-registration should be least willing to accept on its own, since a miscount plus a compensating
  miscount produces it too: all-on pass delta, per-file decline census, and the `assert_invalid`
  destination ledger's `accepted` column, which do not share a mechanism. Slice 9 closed exactly and
  the split across those three readings was the *content* of its result rather than an arithmetic
  coincidence.

## Measured result

**It closed at exactly 29, and the three mechanisms see three disjoint parts of it** — which is what
makes the exact close a result rather than the coincidence the Consequences warned about. None of the
three can see the whole 29, so a compensating pair of miscounts would have to be arranged across
instruments with different lanes and different subjects:

| mechanism | lane | before | after | what it can see |
|---|---|---|---|---|
| `allOnPassFloor` | all gates on | 64833 | **64862** | all 29 |
| validator decline census | all gates on | 33 | **8** | the 25 declines only |
| `assert_invalid` destination ledger, `accepted` | default | 30 | **28** | #391's 2 only |

The 29 is 25 declines + 2 tag admissions + #391's 2. The decline census cannot see #391 at all — its
rows are admissions, not declines — and the ledger cannot see the 25 or the tag pair, because
`ExceptionHandling` is off on its lane. The default-lane board moves 60838 → **60840** pass,
187 → **185** fail, and the validate stratum 38 → **36**, which is now exactly
`validateDeclineCeiling (8) + validateAdmitCeiling (28)`.

**The property is stronger than the entry's retirement, and it is the artifact Scott priced.** All
eight surviving validator declines in the suite are relaxed SIMD — one gate, one event — so *every
single-byte opcode the decoder retains is typed* and *every remaining decline has a single named
cause* are both now statable. `TestSingleByteDeclinesAreExactlyExceptionHandling` was renamed
`TestTheSingleByteOpcodeSpaceIsFullyTyped` and asserts the derived set is **empty**, keeping its walk
extent as the non-vacuity argument, exactly as its own comment prescribed.

**This record's `instance.wast` mechanism claim was falsified by the measurement it forecast.** The
Criterion said the three declines there "cost eleven downstream `no instance` rows in the same file".
All three converted — they are 3 of the 25 — and the downstream rows did **not**: `instance.wast`
all-on reads 3 pass / 15 fail, and twelve of those fifteen are `no instance: interp: link failed:
unknown import` while the other three are `register: no module named $I…`. The cause is the harness's
inability to build the `(module instance $I1 $M)` form, so the modules are never registered and the
imports cannot resolve; the validator was never what stood between those rows and a pass. The count
was eleven and is twelve, and the mechanism was wrong. The *forecast* was right — the surplus was
hedged as "not this family's to claim" and it was indeed zero — but **a hedge that is right for the
wrong reason is not a successful forecast**, and the ADR is amended rather than left to read as one.

**The register closed rather than shrank, and two of its entries were found already drained.** The
re-measurement Consequences called for turned up limits (16) and constant expressions (24) taken not
by slices but by **riders** — #332's `check_limits` and #342's `checkConstGlobals` — with nothing
recorded, because a departure gets written down when a slice takes an entry and quotes its criterion.
So the register had been naming four rules that were written and two figures that had stopped
describing anything. Its only surviving residue is two GC admissions at `array.wast:302,315`. *A
correct repair makes its own site look settled*: nothing was wrong at either fix site, which is why
only a re-measurement could find it.

**The falsification bill was run, not described** — nine mutations, in `exception.go`'s header. Eight
were caught and each is attributed to the specific row that caught it; two are caught by a *single*
row, named as the file's thinnest points. The ninth, `ft.Params` appended to rather than copied, is
caught by **nothing anywhere** and was pre-registered as vacuous before the run confirmed it: the
guard defends against a decoder that packs parameter lists into one shared array, which this one does
not.

**One finding is out of scope and filed rather than fixed: #395.** With `ExceptionHandling` on and
`GC` off, `exnref` is refused `gc: feature gate disabled`, because `exn` (-0x17) and `noexn` (-0x0C)
are decoded in the same arm as GC's eight abstract heap types. Neither board lane can see it, it is
in another package, it moves no column, and existing `internal/binary` rows are part of its surface —
so fixing it here would be a second slice riding on this one's stamp with no forecast of its own. It
was found by writing the witness battery, which is the second consecutive slice where the witnesses
found a defect the board could not.
