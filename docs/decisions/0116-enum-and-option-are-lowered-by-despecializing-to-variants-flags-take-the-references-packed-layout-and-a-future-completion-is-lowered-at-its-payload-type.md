# 0116 — Enum and option are lowered by despecializing to variants, flags take the reference's packed layout, and a future's completion is lowered at its payload type

Date: 2026-11-11 · Status: **accepted**, decided in the slice. It is an engine mechanism inside `internal/component` and `internal/component/canon`: no contract text, no public API (the public `ComponentValue` surface is unchanged and keeps refusing the new kinds by name), and no stamped ADR is reversed, so by the #647 review it is decided here and reported as a decision taken. The record is the actor's, so commits resting on it are `Ratio-Class: carried`. Filed against [#1230](https://github.com/scttfrdmn/burroughs/issues/1230) (E2), and it carries the repairs for [#1245](https://github.com/scttfrdmn/burroughs/issues/1245) and [#1246](https://github.com/scttfrdmn/burroughs/issues/1246).

## Context

WASI 0.3's host functions carry Canonical ABI kinds the codec in `internal/component/canon` had no arm for: `option<string>`, flags, enums, and records at bind. `canon.Kind` had no flags, enum or option kind, the decoded-type bridge (`canontype.go:canonTypeWalk`) refused all three, and `types.go:unmodeledValKind` refused them and `record` at instantiate, for every implemented lower.

The oracle is the pinned reference model, `definitions.py @ 2e7895c` (`CANON_PIN`), driven through `gen/gen.py` into the committed differential fixtures (ADR 0084, ADR 0102). Read at that pin:

- `despecialize` maps `EnumType(labels)` to `VariantType([CaseType(l, None) for l in labels])` and `OptionType(t)` to `VariantType([CaseType("none", None), CaseType("some", t)])`, and `alignment`, `elem_size`, `load`, `store`, `flatten_type`, `lift_flat` and `lower_flat` all `match despecialize(t)`. So an enum and an option have no layout of their own.
- `FlagsType` is **not** despecialized and has its own rules. `alignment_flags`, `elem_size_flags`, `lift_flat_flags` and `lower_flat_flags` each `assert(0 < n <= 32)`; a flags type of 8 labels or fewer is a `u8`, of 16 or fewer a `u16`, and otherwise a `u32`; it flattens to one `i32`. `pack_flags_into_int` sets bit *i* for label *i*, the first label in the least significant bit, and `unpack_flags_from_int` reads only as many bits as there are labels. Binary.md's grammar says the same bound: `(flags l+)` only `if 0 < |l*| <= 32`.

Two graves were found while mapping this work:

- **#1245.** `codec_test.go:valueEqual` had no `record` arm, so two record values compared `a.u == b.u`, which is 0 == 0. Every record lift and lift-flat check in `TestCodecMatchesReferenceModel` passed vacuously.
- **#1246.** `host.go:writeViaStream` completed its future with `readableFutureEnd{value: 0}`, and `async_future.go:completeLocked` wrote `value` as a 4-byte `PutUint32` at the reader's pointer. The payload is `result<_, error-code>`, where `error-code` is `enum { io, illegal-byte-sequence, pipe }`: size 2, alignment 1. Two guest bytes past the result were zeroed on every completion, on a default-on path.

## Options

For enum and option:

1. **Despecialize to variants**, as the reference does, so the variant arms carry them.
   - *Keeps:* one variant framing, already verified by the variant and result fixtures; no layout rule that could disagree with the variant one.
   - *Costs:* a value's kind must survive the round trip, so a load re-specializes the variant it read back to the declared enum or option.
2. **Give each its own arms.**
   - *Costs:* two more copies of the variant layout to keep in step with the first, which is the drift `TupleType`'s despecialization was written to prevent.

For flags:

1. **The reference's packed layout,** one integer of the reference's width holding a bit per label.
2. **Despecialize to a record of bools,** as the Explainer's *semantic* mapping does.
   - *Costs:* that is not the ABI. The reference does not despecialize flags, so the bytes would be wrong.

For #1246's payload type:

1. **The guest's import signature.**
   - *Costs:* it does not carry the enum. Read from `p3async-hello.wasm`, `write-via-stream`'s result decodes as `future<ref 3>`, whose `error-code` is `use`d from `wasi:cli/types`: an `alias export` of an imported instance's type export. This decoder records an instance type's exported types as a resource placeholder (`exportDecl`, the `0x03` arm), so the reference resolves to nothing. Resolving type-export aliases is a resolver slice of its own.
2. **The host interface's declared type.** The consumer implements `wasi:cli/stdout@0.3`'s `write-via-stream`, whose WIT fixes `future<result<_, error-code>>`. The host builds that type with the new codec kinds and lowers the completion through `canon.StoreVia`.
3. **Shrink the hand write to two bytes.**
   - *Costs:* it would be right for this type only, and would still bypass the codec.

## Choice

**Enum and option despecialize (option 1). Flags take the packed layout (option 1). The future's payload type is the host interface's (option 2).**

- **The kinds.** `canon.KindFlags`, `KindEnum` and `KindOption` are appended to `Kind`. `Type.Labels` carries the labels of a flags or an enum; an option's inner type is `Type.Elem`.
- **The type constructors refuse what the reference refuses.** `FlagsType(labels...)` refuses an empty label set, more than 32 labels, and a duplicate label. `EnumType(labels...)` refuses an empty set and a duplicate. Both return an error, because the bounds are the reference's (the model's `assert`s and Binary.md's grammar) and a type with no layout must not be buildable. A duplicate is refused because the model's `match_case` finds a case by its label, which two equal labels make ambiguous. `OptionType(elem)` has nothing to refuse.
- **The value constructors** are `Flags(t, set...)`, `Enum(t, label)`, `Some(t, v)` and `None(t)`, each refusing a label or payload its type does not declare. The accessors are `FlagSet`, `EnumLabel` and `Option`.
- **`despecialize(t)` is applied where the reference applies it**, at the entry to `alignment`, `size` and `flattenType`. A value is despecialized at the entry to `store`, `StoreVia` and `lowerFlat`. A load or flat lift reads at the despecialized type, through the variant arms, and then re-specializes the result to the declared enum or option, so a lifted value has the type the caller asked for.
- **Flags are the only new layout.** The width is `u8`/`u16`/`u32` by label count, the flat form is one `i32`, bit *i* is label *i*, and a load masks to the label count, as `unpack_flags_from_int` does.
- **`LoadVia` gains its variant arm**, which option and enum need, through a new `LoadVariant` that takes an injected element load as `StoreVariant` takes an injected store. So `TestLoadViaIsStoreViasMirrorOverTheSameKinds`' allowance shrinks to `own` alone.
- **The bridge and the predicate.** `canonTypeWalk` maps `VFlags`, `VEnum` and `VOption` through the new constructors. `unmodeledValKind` stops refusing `VRecord`, `VFlags`, `VEnum` and `VOption`; it recurses into a record's fields and an option's element, and refuses a flags or an enum whose labels the codec's constructor refuses, so the two walks still agree. The declared `VRecord` divergence in `TestTheBridgeAndTheUnmodeledPredicateAgree` is retired; only `VRef` remains.
- **#1245's repair.** `valueEqual` gains arms for `record`, flags, enum and option.
- **#1246's repair.** `readableFutureEnd` carries an optional typed payload. `writeViaStream` sets it to `result::ok` at the host's declared `result<_, error-code>`, and `completeLocked` lowers a typed payload through `canon.StoreVia` against the reader's memory, so at most `size(type)` bytes are written. A payload-less case writes only its discriminant, as `store_variant` does. The `future<u32>` the async-lower wrapper mints keeps its 4-byte write, which is that type's size.
- **The public surface is unchanged.** `componentvalue.go` reaches the new kinds only through its existing default arms, which refuse by name with the type's structural rendering.

## Consequences

- **Witness:** new differential cases in `gen/cases.json`, generated from the pin: flags of 1, 8, 9, 16, 17 and 32 labels, each empty, full and mixed; enums of 2, 3, 256 and 257 labels at the first and last label; and `option` of `u8`, `string`, a record and `option<u32>`, each none and some. The pin's flag bound is recorded as its own fixture section (`flags_bounds`): the reference is asked for `elem_size` at 0, 1, 32, 33 and 64 labels, and `FlagsType` must refuse exactly where the model's `assert` fires. Flags of 33 and 64 labels are therefore refusal rows, not layout rows: the pinned model has no layout for them.
- **Neuters, run:** reversing the flags bit order fails the flags cases. Deleting `valueEqual`'s record arm fails #1245's witness while the fixture test stays green. Restoring the 4-byte completion write fails #1246's sentinel witness.
- **The instantiate-time refusal narrows.** An implemented lower whose signature carries a record, flags, an enum or an option now binds. The refusal tests that used those kinds as their specimen move to `error-context`, which is unmodeled for reasons unrelated to this slice.
- **Outside the codec,** the flat argument lowering (`callvalues.go`), the eager flat lift (`async_lift.go`) and the pending lowering (`lowerargs.go`) reach the new kinds only through their default arms, which refuse by name. They gain arms when a call path needs one.
- **What stays open:** resolving a type that an imported instance exports, so the guest's own signature can name `error-code`. That would let a future's payload type be read from the import instead of from the host's declaration.
- **No contract text changes.** An embedder can observe only that a component whose implemented import carries one of these kinds now instantiates, and that a WASI 0.3 guest's stdout future no longer zeroes two bytes past its result.

## Rollback

Each half reverts on its own. Restoring the predicate's refusal list restores instantiate-time behaviour exactly. Restoring the 4-byte write restores #1246's defect, and that is the neuter, run.
