# 0092 — The async-lift ABI is stackless callback, because the first guest that lifts async chose it, and the demand set is read from that guest

Date: 2026-10-02 · Status: **accepted** ([#771](https://github.com/scttfrdmn/burroughs/issues/771), approved by Scott: *"Agree - proceed"*) · **Addendum below: `task.cancel` is built, so the demand table's last row is a dated measurement** · Discharges [ADR 0086](0086-the-async-canonical-abi-behind-gate-async-opens-phase-3.md)'s async-lift deferral
Ratio-Class: carried

**What is approved and what is decided in-slice are different, and the `Status` says which.** Scott approved *starting* #771. The two decisions below — the ABI arm, and the classification of the built-in set — were taken in the slice under the #647 narrowing (*"Escalate only for: contract (§) text, public API surface, reversing a stamped ADR"*), and none of the three applies: no contract clause changes, no embedder-visible surface changes, and ADR 0086 is **discharged on its own terms** rather than reversed — it deferred the choice to a consumer, and a consumer has made it.

Recorded by the actor the work reached, so no independent provenance; commits resting on it stay `Ratio-Class: carried` unless they carry their own citation.

## Context — ADR 0086 deferred a choice to a guest that did not exist

ADR 0086 opened the async tier behind `gate:async` and refused to pick between the two async-lift ABIs:

> the **stackless callback** arm and the **stackful** arm … deferred to the first guest that actually lifts an async export.

That deferral was right and it was also load-bearing: the two arms want different engines. Stackless means the guest returns to the host at every suspension point and is re-entered through a callback; stackful means the engine must hold a suspended guest stack. Picking wrong is not a refactor.

**The deferral's trigger has fired, and it fired as an artefact rather than as an argument.** Two guests now exist, built with `wit-bindgen` 0.62.0 and committed with their sources, their lockfiles, a build script and a reproducibility result ([`internal/component/testdata/asynclift/`](../../internal/component/testdata/asynclift/README.md)):

| guest | what it exercises |
|---|---|
| `single/` | async **lift** only — `compute: async func(x: u32) -> u32` |
| `suspending/` | async lift **and** async **lower** — `run` awaits an imported `tick`, so a task suspends |

## Decision 1 — the async-lift ABI is **stackless callback**, because the guest emits it

Read off `single/component.wasm`:

```
(canon lift (core func $"[async-lift]test:probe/ops@0.1.0#compute") async
            (callback $"[callback][async-lift]test:probe/ops@0.1.0#compute"))
```

The `(callback ...)` immediate **is** the stackless arm. The stackful arm is an async lift with no callback, and no guest in this toolchain produces one.

So the choice is not Burroughs': the consumer made it, which is exactly what ADR 0086 required. The engine's existing shape already matches — `invoke()` runs the callback loop, and an async lift *without* a callback still refuses as unbuilt ([`link_component.go`](../../internal/component/link_component.go)). **That refusal stays**, and this ADR is the reason it is a decision rather than an omission: the stackful arm is declined for want of a consumer, not for want of time, and its trigger is the same shape as ADR 0086's — *a guest that lifts async with no callback.*

## Decision 2 — the demand set is read from the artefact, and it is eleven intrinsics

The committed `suspending` guest's core modules import eleven bracketed canonical intrinsics. Measured with `wasm-tools print` and re-derived in the witness by Burroughs' own loader, so the figure has two independent readings.

The third column is the one that matters: **what this engine does today**, measured against `isBuiltAsyncBuiltin` rather than against its doc comment.

| import | canon built-in | opcode | Burroughs today |
|---|---|---|---|
| `[context-get-0]` | `context.get` | `0x0a` | built |
| `[context-set-0]` | `context.set` | `0x0b` | built |
| `[subtask-cancel]` | `subtask.cancel` | `0x06` | built |
| `[subtask-drop]` | `subtask.drop` | `0x0d` | built |
| `[task-return]run` | `task.return` | `0x09` | built |
| `[waitable-set-drop]` | `waitable-set.drop` | `0x22` | built |
| `[waitable-set-new]` | `waitable-set.new` | `0x1f` | built |
| `[waitable-set-poll]` | `waitable-set.poll` | `0x21 0x00` | built |
| `[waitable-join]` | `waitable.join` | `0x23` | built |
| `[task-cancel]` | `task.cancel` | `0x05` | **absent — refused by name** |
| `[async-lower]tick` | `lower` (with `async`) | `0x01 0x00` | n/a — a lifting operation, not a built-in |

**Slice 2's engine work for this guest is one opcode: `task.cancel` (`0x05`).** It decodes — `0x05` is inside the recognized `0x05–0x25` range, so it reaches bind and refuses there with `ErrAsyncNotImplemented` — which means the refusal is by name and not a decode failure.

### Two expectations this measurement overturned

Recorded because both were reasonable and both were wrong, and because the second one would have put a week into the wrong opcode.

- **`waitable-set.wait` (`0x20`) was expected to be the gap.** It is built, and **this guest never imports it**: the generated code *polls*. So it is in the demand set's complement, not its deficit.
- **`waitable-set.poll` (`0x21`) was expected to be absent.** It is built, and demanded.

The source of both errors was the same, and it is worth naming: **`isBuiltAsyncBuiltin`'s doc comment contradicts its own switch.** The comment says *"Every other async built-in — waitable-set.poll (0x21) … stays refused by name"* while `0x21` is in the `case` list, and the comment does not mention `0x09`, `0x15`, `0x17` or `0x19` among the built at all. The comment is repaired in the slice that lands this ADR; the general lesson is the familiar one in the other direction — *a comment names one constraint, the code embodies another* — and the instrument that now prevents it is below.

## Decision 3 — the built-in set is classified in full, against the binary grammar, with the equality checked both ways

A demand list says what one guest needs. It does not say what the engine's coverage *is*, and the question "which canonical built-ins exist" has an authority: `Binary.md`'s `canon` production.

**The population is 47, not 44, and the difference was measured rather than assumed.** `definitions.py` at the same pin defines 44 `canon_*` functions; the grammar has 47 productions. The three it lacks are exactly `thread.spawn-ref`, `thread.spawn-indirect` and `thread.available-parallelism` — the 🧵② shared-everything-threads forms, which the spec's own footnote says gain their `shared` immediate only *"when shared-everything-threads (🧵②) is added."* **A decoder must be total over the binary grammar**, so the grammar is the authority for this table and the reference implementation is not.

Every production gets **exactly one** status, and nine statuses are needed rather than the five a first pass expected:

| status | n | what it means |
|---|---|---|
| `async-built-undemanded` | 12 | executed by this engine for an earlier increment; this guest does not ask for it |
| `async-demanded-built` | 9 | in the demand set above, and built |
| `threads` | 8 | 🧵 — a **decode** refusal, not a gate refusal; `gate:threads`'s business, not `gate:async`'s |
| `async-refused-by-name` | 6 | decoded into the graph, refused at bind with `ErrAsyncNotImplemented` |
| `resources` | 3 | `resource.new`/`drop`/`rep` — modeled since ADR 0084, behind `gate:components` (on by default) |
| `error-context` | 3 | 📝 — a separate proposal, refused as an unmodeled kind at marshal |
| `threads-phase2` | 3 | 🧵② — decode refusal; the three `definitions.py` lacks |
| `lifting-operation` | 2 | `lift` / `lower` — carry the `async` canonopt but are not members of the built-in set |
| `async-demanded-absent` | 1 | `task.cancel`: slice 2's work |

Two of those nine are corrections to the status set this ADR was asked for, and both are reported rather than quietly absorbed:

1. **`error-context` needs its own status.** It is 📝, a different proposal from 🔀 async, and it refuses at a different place (marshal, as an unmodeled kind). Folding it into either async status would have made a false claim about where the refusal happens.
2. **"demanded" and "refused by name" do not partition the async family.** Twelve built-ins are *built but undemanded* — implemented for earlier increments, not asked for by this guest. Collapsing them into "demanded" would overstate the guest's needs; collapsing them into "refused" would understate the engine. **Implemented and demanded are different questions**, which is the whole reason the demand list has a third column.

`async-demanded-absent` is likewise kept distinct from `async-refused-by-name` even though both refuse: only one of them blocks a guest that exists.

### The witness, and why it cannot be satisfied by agreeing with itself

[`internal/component/testdata/canon-builtins.tsv`](../../internal/component/testdata/canon-builtins.tsv) holds the spec's facts — opcode, name, proposal marker — generated by [`scripts/gen-canon-builtins.py`](../../scripts/gen-canon-builtins.py) from `Binary.md` at `CANON_PIN` and committed as the oracle's reading, so the witness needs no network and passes under `BURROUGHS_NO_SKIP=1`. The generator's network path was run and produced a table identical to one built from a local copy, so it is not a dormant path.

**The status column is authored in Go and deliberately not generated.** A status derived from the engine would make the equality check compare the engine to itself, and would pass whatever the engine did. So `canonStatus` makes a claim, and three independent readings check it **in both directions**:

| arm | catches |
|---|---|
| `TestCanonTablePinMatchesTheMakefile` | a `CANON_PIN` bump that did not regenerate the table |
| `TestEveryCanonProductionHasExactlyOneStatus` | a production with no status; a status naming no production; a status in the wrong proposal family |
| `TestEngineBuiltSetEqualsTheClassification` | an opcode added to `isBuiltAsyncBuiltin` with nothing reclassified; one removed while a status still claims it; a permitted opcode with no production at the pin |
| `TestDemandSetEqualsTheClassification` | a `demanded` status the guest does not import; an import no status claims; an import matching no production at all |
| `TestTheDemandedAbsentSetIsSliceTwosWorkList` | the slice-2 list disagreeing with the engine it describes |

The pin arm is what gives the rest teeth. Without it the table is a file with a date on it: `CANON_PIN` could move, `make canon-fixtures` could regenerate the ABI fixtures against a newer revision, and this table — with the whole classification resting on it — would keep describing the old one with nothing failing.

Each comparison carries a vacuity check, because two empty sets agree perfectly.

**The controls were watched die.** Ten injections against a committed baseline, each run over all five arms rather than only the one it targets: status entry removed; status for a nonexistent production; table pin changed; opcode added to the switch; opcode removed from the switch; a demanded built-in reclassified undemanded; an undemanded one reclassified demanded; a production put in the wrong family; the table truncated; and `task.cancel` reclassified as built. **All ten were caught**, each by the arm that should catch it — and two of them by a second arm as well, which is the measurement that says the arms are not duplicates of each other.

Two facts in this ADR came from running the witness rather than from reading anything: a lifting operation's bracket spelling carries the canonopt in front (`[async-lower]` for `lower`), so a name-derived match needs both spellings; and **`thread.yield` is marked 🔀, not 🧵** — it belongs to the async family despite its name, and only the marker says so.

## Consequences

- **ADR 0086's async-lift deferral is discharged.** The stackless arm is the implemented one; the stackful arm is declined for want of a consumer, with the trigger stated above.
- **Slice 2's engine work for this guest is `task.cancel` (`0x05`)**, read off an instrument rather than off a plan.
- **The classification is now falsifiable.** An engine change that moves the built set without reclassifying fails; a pin bump that adds a built-in fails until it is classified; a regenerated guest whose demand set changes fails until the statuses follow.
- **A reproducibility claim about the committed guests exists and is recorded with its hashes** in the testdata README, including the three ways the check failed before it passed. The relevant one for a future reader: a build that exits before writing anything leaves the committed artefact in place, so the comparison runs each file against itself and reports a clean match.
- **This ADR does not open slice 2.** It is the record of what slice 1 measured. One ADR earns one implementation, and the implementation this one earns is `task.cancel`.

## Addendum (2026-10-03) — `task.cancel` is built, and the demand table above is a dated measurement

**The demand list's last row now reads wrong, and it is corrected here rather than edited in place**, because
that table is a *measurement of the engine on 2026-10-02* and the body is the record of what slice 1 found.
Editing it would leave no trace that the engine moved, which is the whole reason the row was interesting.

`task.cancel` (`0x05`) is **built** (#864). Its status in the classification moved from
`async-demanded-absent` to `async-demanded-built`, and **the demand set is now 10 built, 0 absent** — the
slice-2 work list this ADR named is empty.

**It had to land earlier than this ADR implied.** The body says *"slice 2's engine work for this guest is
`task.cancel`"*, which read as a task to schedule. In fact `wit-bindgen` emits a `TaskCancelOnDrop` guard for
**every** async export, so **both** committed guests import `[task-cancel]` — including `single`, whose
`compute` has nothing to do with cancellation. With `0x05` refused by name, **neither guest could
instantiate**, which blocked the value-carrying export call and the parity readings alike. The opcode was a
prerequisite for running the guests at all, not a feature to add afterwards.

**What was built is the Canonical ABI's rule, not a stub.** `task.cancel` on a task that has not been
cancelled **traps** — wasmtime spells the same rule `TaskCancelNotCancelled`. Burroughs cannot cancel
anything, so every reachable call lands on that branch, and implementing it is implementing the spec. The
*cancelled* branch refuses by name (`ErrTaskCancelUnbuilt`, pointing at
[#862](https://github.com/scttfrdmn/burroughs/issues/862)), because nothing sets that flag yet and semantics
written against no measurement is the shape this campaign keeps correcting.

**The committed guests cannot witness it** — they call `task.cancel` only from drop glue that never runs
absent a cancellation — so `testdata/task-cancel-synth.wat` calls it immediately and traps. Its `.wat` source
is committed beside its `.wasm`, which the other hand-authored fixtures here do not do: regenerating one of
those means re-authoring it from a prose description.

**The classification moved because the engine moved, and the witness is what noticed.** Adding `0x05` to
`isBuiltAsyncBuiltin` turned `TestEngineBuiltSetEqualsTheClassification` red with *"executes these, but no
status claims them built"* — the direction nobody checks by hand, and the reason this ADR's table is
falsifiable rather than decorative.
