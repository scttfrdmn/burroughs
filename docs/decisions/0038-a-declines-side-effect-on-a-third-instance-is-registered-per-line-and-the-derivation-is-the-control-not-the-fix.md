# 0038 — A decline's side effect on a *third* instance is registered per line, and the derivation is the control rather than the fix

Date: 2026-08-19 · Status: **accepted** for the design — the shape of a harness-internal registry
and its control, self-decided on Scott's instruction of 2026-08-19 ("Nothing needs me. Start #414")
and no token claimed for it. **The board-shape consequence carries no stamp and claims none**: it is
pre-registered below and in
[#414](https://github.com/scttfrdmn/burroughs/issues/414#issuecomment-5351936399) before the code
exists, flagged in the PR that lands it, and reversible by deleting one branch. The halves are
separated for the reason [0037](0037-the-registry-carries-its-gated-names-and-a-downstream-import-fails-as-gated-not-as-missing.md)
separated them, and the second half is larger here than there.

**What the second half is.** This moves **5 rows out of the fail column**, and they are every fail
the default lane has. The board reaches **0 fail**, which
[a zero-fail board is a lost instrument](../laws/boards-and-buckets.md) names as a hazard rather
than an achievement: at 0 the work plan loses its subject and the gradient inverts toward
instruments. So the ADR states which instrument the ladder is steered by afterwards — the all-on
lane's 38 fails — and states it now, not after the column empties.

## Context

[0037](0037-the-registry-carries-its-gated-names-and-a-downstream-import-fails-as-gated-not-as-missing.md)
gave the run loop gate state in the three slots a declined module's *own identity* lives in:
`cur`/`curGated`, `named`/`namedGated`, `registry`/`Registry.Gated`. Every one is keyed by the
declined module, so every one answers a question *about* it.

`load1.wast` is the case where the decline's consequence lands on a **third party**. Four commands:
`(module $M)` exporting a memory and a reader; `(register "M")`; a second module importing `"M"
"mem"` and defining a memory of its own — two memories, declined; and then five assertions that read
back, **through `$M`'s own exported function**, bytes only the declined module's active data
segments would have written. `$M` instantiated, is not gated, and answers honestly that its memory
holds zero. Five rows red, every one of them correct behaviour under a gate that is off by design,
and 0 fails in the all-on lane.

The row carries a true sentence, which is why nothing on the board looks wrong. It is
[#366](https://github.com/scttfrdmn/burroughs/issues/366)/[#408](https://github.com/scttfrdmn/burroughs/issues/408)
one step further out.

### The seam, and the blocker inside it

The natural rule — read the declined module's import list, see which allocation its data segments
write — is not available. `MultiMemory` declines at **memarg flags bit 6 in `decodeMemop`**
(`internal/binary/instr.go:1570`), in the *decoder*. `load1.wast:10` never produces a
`*binary.Module`, so there is no `Imports` slice to walk, the way `declinedImport` walks one for
0037. Any import-list analysis requires decoding the declined image a second time under a different
feature set — the default lane consulting the all-on lane to compute a default-lane verdict.

That is an instance of the general mechanism this decision is organized around, named by Scott on
the #426 report and restated because it decides three of the four options below:

> **A gate's location in the pipeline decides which forms reach it, so "gated" is a fact about the
> instantiation path, not about the text.**

Proximity is not aboutness, and at forecast scale it is the same defect as in prose. Nothing about
`load1.wast:25-29` sitting *after* a declined module makes them its rows; they are `$M`'s rows,
mis-scored because of a fact about the instantiation path — that the declined module was the only
writer of the bytes `$M` reports.

### Domain, derived rather than enumerated

An issue's list is a registry of where someone noticed. Census over every module command in the
corpus: decode under `binary.DefaultFeatures()`, keep those declining with
`binary.ErrFeatureDisabled`, decode again with all gates on, and keep those with an
**instantiation-time write channel** — an active data segment with `MemIndex < ImportedMems`, an
active elem segment with `TableIndex < ImportedTables`, or a start function alongside an allocation
import. Nothing else writes before user code runs.

Nine declined modules import an allocation or carry a start function; five drop out, and the reasons
are the load-bearing part:

| dropped | why |
|---|---|
| `instance.wast:15`, `:62`, `:128`, `linking.wast:112` | the import is a mutable **global**. Only running code writes a global; instantiation does not. |
| `start0.wast:1` | a start function, but no allocation import — nothing shared to write. |

Four remain — `imports1.wast:1`, `imports2.wast:9`, `load1.wast:10`, `store2.wast:6` — and **only
`load1.wast` mis-scores.** `store2.wast` (2 pass, 0 fail, 22 gated) and `imports1.wast` (1 pass, 0
fail, 4 gated) are already covered by 0037: every action in them targets `cur`, which *is* the
declined module. Four declined writers, one mis-scored file, which is the first place proximity
would have been wrong.

## Options

1. **Per-file slack-0 registry** (#414's option 1), keyed by file, the way `gatedDeclinedRegistration`
   polices #366's four `imports.wast` vectors. Cheap and exact, and *scoped to today's cases* — a new
   corpus file with this shape scores wrong and nothing says so.
2. **Derived by allocation identity** (#414's option 2): track which allocations a declined module's
   imports name, gate any later action on an instance sharing one. Correct-sounding, and **priced at
   4 correct passes.** `imports2.wast`'s declined module at `:9` writes offset 10 of
   `spectest.memory`; a *second, undeclined* module at `:22-27` imports the same `spectest.memory`,
   writes the same offset itself, and `:28-31` read it back. Those four rows pass in both lanes and
   their answer does not depend on the declined write at all. Under this option they become the third
   verdict. A fix that converts 4 correct passes into third verdicts to repair 5 mis-scored fails is
   not obviously better than the defect. Sharing an allocation is proximity; being the instance whose
   reported value the declined write determined is aboutness.
3. **Derived by exporter identity**: gate actions on the instance that *exports* the written
   allocation, reached through `cur`/`named`. This is 0037's registry edge traversed the other way —
   0037 gates the importer when the exporter's registration was declined, this would gate the
   exporter when the importer was declined — and on today's corpus it is exactly right: `load1`'s
   `:25-29` target `$M`, and `imports2`'s `:28-31` target `cur`, so the four passes survive. It still
   needs the declined module's import list, so it inherits the second decode and the lane bleed.
4. **Derived from the lane diff**: gate every default-lane exec fail that is not a fail in the all-on
   lane. No decode analysis at all, since both lanes already run. **And it hides a real defect:** a
   construct the decoder refuses that it should *not* refuse produces exactly that signature, so
   over-gating would be absorbed into the third verdict silently. This is the failure
   `gatedAssertInvalid`'s "if not, the decoder is over-gating and hiding a failure" exists to prevent.

## Choice

**Option 1 for the verdict, option 4 for the control** — the split is the decision, and it is the
idiom `gatedAssertInvalid` and `gatedDeclinedRegistration` already use:

- **The verdict change is a slack-0 registry keyed `file:line`**, each entry naming the declined
  module, its write channel, and the instance whose reported value it determined. The verdict it
  produces is the third one, for those lines only. Prose cause, entered by hand once.
- **The control derives the population from the two lanes the harness already runs** and errors on
  any default-fail/all-on-pass row without an entry. So the fix is not scoped to today's cases — a
  new corpus file with this shape is *named*, which is the blind spot option 1 was charged with. It is
  a predicate over data both lanes already fetch, not a new instrument.
- **The derivation is the control and not the fix** for the reason option 4 fails: a machine that can
  gate a row cannot be allowed to gate one it cannot explain. Membership is machine-checked;
  causation is written down.

### The control's own vacuity, which is not hypothetical

Once the fix lands, the derived population is **0** — the rows are gated, not failed — and a
comparison against an empty set agrees perfectly. So the control runs each registered file twice,
once with the registry consulted and once **neutered**, checks the neutered run's exec fails against
the registry at slack 0, and asserts a non-empty floor. A table drained to empty dies the way a
stillborn control dies, and this one is watched dying.

### Declared blind spot

Aliasing stays out of scope: an undeclined instance reading a declined module's write through a
shared *host* allocation scores today's way. `imports2.wast:28-31` is both the witness that the gap
is currently benign — those rows pass for their own reason — and the site to look at first if it
stops being. Stated here rather than discovered later.

## Consequences

**Forecast, pre-registered** (also in the issue, before the code):

- **Rows that move: exactly 5**, `load1.wast:25-29`, `fail` → `gated`. No other row changes verdict.
- **Files whose per-file line changes: 1.** `load1.wast: 2/7 pass, 5 fail, 10 gated` → `2/2 pass, 0
  fail, 15 gated`.
- **Board:** `60928 pass, 5 fail, 57 unsupported, 4154 gated, 0 unimplemented` → `60928 pass, 0 fail,
  57 unsupported, 4159 gated, 0 unimplemented`.
- **Rows that must not move:** `imports2.wast:28-31`, `imports1.wast`'s pass, `store2.wast`'s two, and
  the all-on lane at `65014 pass, 38 fail, 0 gated`.
- **Derived population before the fix: 5.** Anything else means the census missed a stratum and the
  forecast is wrong before the code is.
- **`unsupported` delta: 0, and structural.** This changes how a row is scored, never what the harness
  can ask.

**The reward figure is a reclassification, not a repair.** Five rows stop saying "the interpreter
computed a wrong answer" and start saying "a gate is off". No engine got anything right, and the PR
says so.

**Which instrument steers the ladder afterwards.** The default lane's fail column reaches 0, so the
work plan is read off the **all-on lane's 38 fails across 9 files** — `local_init` 8, `ref` 6,
`array` 5, `type-subtyping` 5, `type-equivalence` 4, `type-rec` 4, `try_table` 3, `struct` 2, `func`
1. GC in seven, EH in one, one row in `func`. That is product work, so the gradient does not invert
toward instruments — but it inverts if this is left unsaid, which is why it is here.

**Reversal.** Deleting the registry's consult restores the 5 fails and changes nothing else.
