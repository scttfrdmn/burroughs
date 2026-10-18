# 0103 — A sync cross-component call crosses as component values, lifted from one memory and lowered into the other

Date: 2026-10-18 · Status: **accepted in design** — the chair's ruling, in session, 2026-10-18 (second ruling of the day): *"ADR 0103, the sync adapter: accepted in design … mirrors the reference's sync lift and lower."* Drafted under the first ruling of that day, item 4: *"The conformant fix is the full adapter … Write that as an ADR draft held for chair review."* **Not yet built**; it is built next, before SP-1, because it is a default-path defect. The interim named refusal (`syncSpill` in `internal/component/cross_component.go`) stays until the build removes it. The acceptance changed one constraint, `post-return`, and set the build's obligations. Both are in the section at the end, which governs where it differs from the text above.

## Context

A sync `canon lower` whose callee is a sibling's sync `canon lift` is bound by `bindCrossComponent`'s sync arm (`internal/component/cross_component.go`). The arm hands the lower's flat core words to the callee's core func and returns the callee's flat results. That is correct only when every value is a scalar or a handle and nothing spills. Two failures follow, both measured:

| fixture | shape | wasmtime 49.0.3 | Burroughs before the interim | face |
|---|---|---|---|---|
| `sync-spill-string.wasm` | `get: func() -> string` | 3, 10 of 10 | `interp: "get" takes 0 arguments, got 1` | loud: the result spills |
| `sync-string-param.wasm` | `first: func(s: string) -> u32` | 97, 10 of 10 | **120** | **silent**: the string's words point into the caller's memory, and the callee read its own |

**The second row was not in #995.** It was found writing this ADR. A `string` is two flat words, so it does not spill, but it carries a pointer, and no pointer means the same thing in two memories. The interim refusal was widened to cover it, and the case is recorded on #995.

**The reference does this at the value level** (pin 2e7895c), and the two halves are separate functions:

- `def:canon_lower`'s `on_start` lifts the caller's arguments with `def:lift_flat_values`, from the caller's flat args, or from the caller's memory when they exceed `def:MAX_FLAT_PARAMS`.
- `def:canon_lift` lowers them into the callee with `def:lower_flat_values` through the callee's options: its memory, and its realloc for any string or list. After the core call it lifts the results with `def:lift_flat_values` against `def:MAX_FLAT_RESULTS`, then calls `post_return` if the lift has one.
- `def:canon_lower`'s `on_resolve` lowers the results into the caller with `def:lower_flat_values`, through the caller's realloc, at the caller's retptr when they spill.

So a component value is the only thing that crosses. Nothing in either memory is addressed from the other side.

## Options

**A. A value-level adapter, built from the codec Burroughs already has.** The sync arm lifts the caller's arguments to `canon.Value`s with the lower's options, and lowers them into the callee with the lift's options. Strings and lists go through the callee's realloc, which is the path #902 built for a host `Call` with a string argument (`pendingLower`, run on the callee's entry). After the core call, the arm lifts the results from the callee's memory (`canon.LoadVia` for a spilled result), and lowers them into the caller's memory through the caller's realloc at the retptr (`canon.StoreVia`, as the async lower's `apply` does). This mirrors the reference function for function.

**B. A byte-level copy.** Copy the bytes a value reaches from one memory to the other, rewriting pointers as it goes. It needs the same type walk as A to find the pointers, so it saves nothing structural. It also skips the codec's validation, such as a string's UTF-8 check and a char's range, which the reference performs on lift. **Rejected:** it is A without its checks.

**C. Keep the named refusal.** This is honest, but it is a limit on a default-on path (`gate:components`). wasmtime runs both fixtures, and real toolchains emit sync string calls between composed components. **Rejected as the end state.** It stays as the interim until A lands.

## Choice

**A**, with these constraints.

1. **`post-return`.** Burroughs refuses a lift carrying `post-return` at load, and that refusal stands. So a sync callee lifted with `post-return` still does not load, and A frees nothing. A callee with no `post-return` leaves its result buffer allocated, as the reference does. Carrying `post-return` is its own decision, out of scope here.
2. **The order of the reallocs matches the reference.**
   - The callee's realloc runs for the arguments, **on the callee's entry**, as #902 does, so the allocation and the call are one exclusive region.
   - The caller's realloc runs for the result, **inside the lower, before it returns**, under the caller's run slot. That is the async `apply`'s discipline, with no engine lock held across guest code (B-MM-3).
3. **may_leave.** Both reallocs run with `may_leave = False` (`def:LiftLowerContext.reallocate`), and #990 already marks that site.
4. **A trap in either side's realloc, or in the callee, is the lower's trap.** It traps the caller's task, the subset of the reference's whole-store trap that #1040 records.
5. **The refusal is deleted as A lands, shape by shape.** For each shape A carries, `syncSpill` stops refusing it, and its fixture's row moves from refused to a value equal to wasmtime's. A shape A does not yet carry, such as resources crossing as handles, stays refused by name.

## Consequences

- **Witnesses owed**, each with its neuter:
  - `sync-spill-string.wasm` reads 3, and `sync-string-param.wasm` reads 97.
  - A spilled-parameter fixture, with more than 16 flat values.
  - A record with a string field, in both directions.
  - A `list<string>` result, which needs a realloc call per element in the caller.
  - A realloc that traps in each direction, read on wasmtime first.
- **The forecast, to be registered before building:** the whole tree stays green (28 packages), and `switch.wasm` and `BenchmarkFanOut` are unchanged under `make lab-ab`, because neither crosses a sync string.
- **What it does not decide:** resources across the boundary, `post-return`, and the async lower of a sync lift returning a string. That last one currently refuses through `crossComponentValue`'s scalar arm after #1050, and it is the same adapter's other caller, so it is named here to be built with A.

## Accepted, 2026-10-18: the build's obligations, and one constraint corrected

**The chair's ruling, in session, 2026-10-18, accepted this in design.** Where this section differs from the text above, it governs.

**Constraint 1 is corrected, in its premise and its conclusion.**
- **The premise was false.** *"Burroughs refuses a lift carrying `post-return` at load"* is not true. The decoder records the option (`opts.PostReturn` in `internal/component/instantiate.go`), and **no code reads it**. So a lift with `post-return` loads, and its `post-return` is silently never run. `mayleave.go`'s comment made the same claim, and this draft copied it. Found on reading for the build.
- **The conclusion is overruled.** The ruling requires `post_return` to run on the callee after the copy, as `def:canon_lift` does. So the build runs it, and the "out of scope" decision above no longer stands.

**What the build owes**, from the ruling:
- **Both measured faces read the oracle's values:** `sync-spill-string.wasm` reads 3 and `sync-string-param.wasm` reads 97.
- **`post_return` runs on the callee after the copy**, under `may_leave = False` (`def:canon_lift`), measured on wasmtime first with a fixture whose `post-return` has an observable effect.
- **The interim `syncSpill` refusal is removed**, and its witness is converted to assert values.
- **A neuter for each half:**
  - with no lift, the arity error returns;
  - with no lower, the wrong memory returns (120).
- **The encodings and kinds the codec does not model stay refused by name**, listed in this ADR as the build finds them.
- **A cost reading** on a string round-trip, as an A/B on janus (`make lab-ab`).

## As built, 2026-10-18

**Where each step lives:**
- **The caller's half** is `syncAdapter` (`internal/component/cross_component.go`), reached when `syncNeedsAdapter` finds a pointer-carrying parameter or result, or a result past `MAX_FLAT_RESULTS`. Every other sync call keeps the flat passthrough, whose words mean the same in both memories.
  - It lifts the arguments with `liftFlatValue` over the caller's heap. That is `task.return`'s lift, generalised to take a heap and a label: it was `liftResultValue(c, …)`.
  - It lowers them with `lowerFlatArgs`, then lowers a spilled result into the caller's retptr with `canon.StoreVia` through the caller's realloc.
- **The callee's half** is `invokeSync` (`internal/component/link_component.go`). Inside the run slot it runs `lowerPending` through the callee's realloc, then the core func, then `liftSpilledResult` from the callee's memory (`interp.ReadBoundaryMemory`, new beside `WriteBoundaryMemory`, with the same growth-lock discipline), then `post-return` under `InvokeMayNotLeave`.
- **A sync export called by an embedder gains two things.** A `string` argument is now lowered rather than refused (`TestASyncLiftLowersAStringArgumentInsideItsRunSlot`, 97 on wasmtime). A result that spills is now lifted from memory and returned.

**Readings, as forecast on #995:** `sync-spill-string` reads 3, `sync-string-param` 97, and `post-return-runs` (#1058) 101. All three equal wasmtime 49.0.3.

**Still refused by name, as the build found them:**
- **Parameters past `MAX_FLAT_PARAMS`** (`paramSpill`). They cross as one pointer to a tuple in the caller's memory, and `lowerFlatArgs` has no spilled-parameter lowering.
- **A kind the caller-side lift has no arm for.** `liftFlatValue` lifts the scalars, `char`, `string`, `list` and `record`. A variant, `option`, `result`, `enum`, `flags` or handle inside a pointer-carrying signature is refused, naming the kind.
- **A kind the callee-side lowering has no arm for.** `lowerOneArg` lowers `u32`, `string`, `list` and `record`. Any other scalar inside a pointer-carrying signature, such as a `bool` beside a `string`, is refused by name. This is narrower than the lift, and it is the next obvious widening.
- **A spilled result `canon.LoadVia` cannot load.** That refusal is the codec's own.
- **A one-word sync result other than `u32` returned to an embedder** (`liftFlatCoreResult`). It is not a pointer and not a spill, so ADR 0103 does not reach it. The refusal's text, which named `post-return` and a return pointer as the missing pieces, is corrected to say what remains.
