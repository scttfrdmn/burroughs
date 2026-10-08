# 0097 — The public compound value surface: `ComponentType` plus string, list and record, stamped on a four-point summary with three changes

Date: 2026-10-06 · Status: **accepted** — stamped by Scott, 2026-10-06 (*"I agree"*), on the chair's four-point summary of the [#902](https://github.com/scttfrdmn/burroughs/issues/902) signature review, **with three changes** · Continues [ADR 0096](0096-the-public-component-interface-lands-as-componentvalue-loadcomponent-and-call-with-a-context-stamped-on-a-six-point-summary.md) point 5 · Lands the rest of [ADR 0085](0085-the-public-component-api-surface-a-new-component-value-type-resource-handles-first-class-and-wit-typed-constructors.md)'s value type
Ratio-Class: ordered Scott's stamp 2026-10-06 on the chair's four-point summary (#902)

**A new ADR rather than an amendment to 0096**, because 0096's own scope sentence is *"what the stamp covers is the six points as presented, and nothing beyond them"* and its point 5 was *"first merge carries `u32` only"*. This is the question that point deferred, it has its own options and its own ruling, and an amendment carrying thirteen exported names and a new public type would be longer than the ADR it amended.

Recorded by the actor the stamp reached, so no independent provenance for the record. The class is `ordered` rather than `carried` because a principal's stamp on a stated summary is a citable approval — the distinction [ADR 0093](0093-a-speculative-gate-class-for-features-ahead-of-the-spec-never-default-on-off-the-board-and-labelled-where-it-leaks.md) and ADR 0085's amendment 1 drew.

## Context

ADR 0096 shipped `ComponentValue` carrying `u32` and nothing else, with point 5 committing that *"other kinds refused by name and added later without breaking changes."* `string`, `list` and `record` are what a release needs — 0096's point 6 made the release conditional on exactly them — so this is that work.

The question point 5 did not reach: **a `u32` needs no type descriptor and a list or record does.** `ComponentU32(7)` is complete; `ComponentList(…)` cannot be, because an empty list still has to say what it is a list of, and a record has to say what its fields are called.

## Options

1. **A public type descriptor.** Add `ComponentType`, so a compound can be constructed against the type it claims.
2. **Derive the type from the export's signature.** The signature is already in hand at the boundary — `CallValuesCtx` resolves it before lowering anything, and `lowerFlatArgs` already cross-checks each value's kind against the declared parameter. So a value need not carry its own element type.

## Decision

**Option 1.** The chair ruled it on the #902 review and Scott stamped the resulting surface.

**Option 2 was this actor's recommendation and it was wrong, on a ground this tree had available.** It is recorded because the reason is the useful part: ADR 0085's title *is* "…and WIT-typed constructors", and its stated shape requirement is explicit —

> **Constructors carry their WIT type.** A value is constructed against the type it claims, so a mis-typed value refuses **at construction** rather than at the boundary.

Under option 2 constructors become untyped containers checked only at `Call`, which is *"at the boundary"* — the thing that sentence refuses by name. That is a change to Scott's decision, not an implementation choice, and reversing a stamped ADR is the third member of the escalation set. Checked against the ADR rather than taken on the ruling's word, and the premise holds **more strongly than the ruling needed**: 0085's Decision already reads *"Option 1: a new component value type, with Option 3 as a later additive helper layer over it"*, so the ordering was settled there too.

**And option 1 does not conflict with [ADR 0029](0029-the-public-boundary-run-on-a-validated-path-decline-as-a-third-outcome-and-a-value-that-converts.md)**, which was the recommendation's argument against it. A public `ComponentType` with **unexported fields, converted at the boundary**, is exactly the treatment `ComponentValue` already gets — the objection would have applied to `ComponentValue` itself.

**A second, independent reason surfaced while writing the signatures**, and it decides the read direction rather than the write one: under option 1 a value carries its type, so a record that comes **back** from a call knows its own field names and `Field("x")` works on it. Under option 2 it would not, and a returned record could only ever be read **positionally** — the same silent-wrong-value hazard, in the direction nobody was looking.

### The stamped surface

Thirteen new exported names. Nothing existing changes signature.

```go
type ComponentType struct{ /* unexported */ }
type ComponentField struct { Name string; Type ComponentType }

func ComponentTypeU32() ComponentType                                     // total
func ComponentTypeString() ComponentType                                  // total
func ComponentTypeList(elem ComponentType) (ComponentType, error)
func ComponentTypeRecord(fields ...ComponentField) (ComponentType, error)
func (t ComponentType) Kind() ComponentKind                               // total
func (t ComponentType) String() string                                    // fmt.Stringer

func ComponentString(s string) (ComponentValue, error)                    // err: not valid UTF-8
func ComponentList(elem ComponentType, vals ...ComponentValue) (ComponentValue, error)
func ComponentRecord(t ComponentType, fields map[string]ComponentValue) (ComponentValue, error)

func (v ComponentValue) Str() (string, bool)
func (v ComponentValue) List() ([]ComponentValue, bool)
func (v ComponentValue) Field(name string) (ComponentValue, bool)
```

**Records take a map, and that is the answer to named-versus-positional.** A map goes further than checking for a misordered field — it makes misordering **unrepresentable**, because the caller supplies no order at all. The order comes from the descriptor, which is the only order in the system: lowering iterates the descriptor's fields in declared order and looks each name up, so Go's map-iteration nondeterminism never reaches the ABI layout.

### The three changes, which are part of what was stamped

1. **Element and field checks compare whole types structurally, not kinds.** As first drafted, `ComponentList` compared `vals[i].Kind()` against the element kind — so `list<list<u32>>` would have accepted a `list<string>` element, both being lists. Each `ComponentValue` carries its **full type** internally and construction checks structural type equality; the same for record field values. **No new exported accessor is needed**, because the check is internal — which is also why this is a change to the implementation rather than to the stamped name list.
2. **Record field names are validated as WIT labels when the record type is built**, not left to fail later against a signature. ADR 0085's refuse-at-construction rule, applied to the descriptor.
3. **`Str()` stays, and its doc comment must distinguish it from `String()`** — `String()` is the debug rendering, `Str()` returns the content. Confusing the two is the obvious mistake, and the doc comment is where it gets pre-empted. The name itself is not an invention: `canon.Str` is already this tree's name for the WIT `string` value, so `Str` names its WIT type exactly as `U32()` does, and `String()` is unavailable to `fmt.Stringer`.

### Two decisions taken in the proposal and accepted as reasoned

- **`ComponentString` returns an `error`.** The ABI's `string` is UTF-8 and a Go `string` is an arbitrary byte sequence; an invalid one lowered raw puts invalid UTF-8 in guest memory where a Rust guest's `String::from_utf8` panics. The in-tree precedent is `canon.Char`, which already refuses a surrogate or out-of-range code point at construction for the same reason.
- **`ComponentField` keeps exported fields** where everything else is opaque, and the reason is specific rather than convenience: **a per-field constructor cannot check the condition that matters.** Duplicate field names and an empty field set are whole-set properties only `ComponentTypeRecord` can see, so a `ComponentFieldOf` would catch nothing the record constructor does not and would imply a validated field is a safe one.

### The empty-field-set refusal comes from the model, not from API style

Added on the #904 review, because the proposal treated it as a validity condition the public
constructor imposes — *"duplicate field names and an empty field set are whole-set properties only
`ComponentTypeRecord` can see"* — which is true and is not the whole reason.

**The Canonical ABI gives an empty record no size at all.** `elem_size_record` ends with
`assert(s > 0)` (`definitions.py:1256`), so an empty record is not a type with a zero-byte layout; it is
a type the model declines to lay out. Asked of the pinned model directly rather than read off the
source: `elem_size(RecordType([]))` raises `AssertionError`, and so does `elem_size(TupleType([]))`,
because `elem_size` despecializes first (`:1228`). **Alignment, by contrast, is defined and is 1** —
`alignment_record` has no assertion and its loop simply does not run (`:1193-1197`). That asymmetry is
the model's, and this engine reproduces it rather than smoothing it over.

So `ComponentTypeRecord`'s refusal is not this engine being strict about a shape it could have
supported. There is no behaviour to support, and a value of such a type could not be lowered by any
conforming implementation.

**The codec answered 0, and 0 was worse than wrong.** `sizeTuple` returned 0 for an empty field set,
which fed `canon.ListByteLength`: a zero-size element makes the byte length 0 for *any* count, so a list
of a million empty records would have been framed as zero bytes with **no error**. The refusal therefore
belongs where the type is known — `sizeRecord` panics, and `Record`, `StoreVia`, `LoadVia`, `lowerFlat`
and the bridge each refuse before that panic is reachable — rather than in the byte-length guard, which
sees only numbers. `ListByteLength`'s zero-size branch is kept for divide-by-zero safety and now says it
is unreachable for a valid type.

Nothing upstream rejects one: searched, and the tuple decoder reads a count and loops with no `n > 0`
check, the record decoder does the same through `namedValVec`, and no emptiness check exists in
`internal/component` or `internal/validate`. The bridge is therefore the first layer that can decline it
with an error instead of a panic, and that is where it declines. (Chair's ruling: refuse, do not justify
returning 0.)

## The limit this stamp carries, unchanged from 0096's

**If building it needs anything beyond the stamped block plus those three changes — another exported name, a different signature, different error behaviour — the slice stops and reports before merging.**

That limit fired once before and was passed (0096's amendment 2 is the record), so it is restated here rather than assumed inherited. *"Report back if it doesn't" ends the slice at that point*, and a good reason to proceed belongs in the report rather than instead of the answer.

## Consequences

- **Merge only. No version is cut** until string parameters, lists and records work end to end. Scott's ruling, unchanged from 0096 point 6; both the timing and the minor-version number stay his (ADR 0004).
- **Each public piece merges only once its internal direction works end to end.** The chair's ordering: the scalar regression, then the public surface for what already works (`ComponentType`, the string constructor and accessor, with string **results** reachable through `Component.Call`), then string **parameters**, then lists, then records. String parameters through the public API **refuse by name** until their internal mechanism lands.
- **The public surface will spell types the codec cannot lower.** `ComponentTypeRecord` builds a record before [#904](https://github.com/scttfrdmn/burroughs/issues/904) gives the codec one, so a call carrying it refuses **at `Call`**, by name, from the bridge. That is grave #301's distinction: well-formed-but-unimplemented is unsupported, not malformed.
- **`ComponentValue`'s internal storage widens** to carry a string, an element slice and a type. That is precisely the widening ADR 0029 predicted — and it is allowed here because the fields are unexported, which is the whole reason that treatment was chosen. Worth stating so the next reader sees the prediction coming true *inside* the boundary rather than across it.
- **The deferred alternative keeps its trigger.** Signature-derived typing may be added later as a convenience **over** this, never instead of it — ADR 0085's own A-then-D shape. Trigger: spelling types out proving tedious in practice, meaning a real embedder or our own fixtures carrying enough descriptor boilerplate to be worth removing. *A deferral's trigger is a hypothesis about its consumer*, so whoever picks it up says whether the named trigger actually fired.
- **Type introspection stays deferred**: `ComponentValue.Type()`, `ComponentType.Elem()`, `.Fields()`. The three accessors answer every question these kinds raise, because a caller knows the signature it called. Trigger: code that must branch on a returned value's type rather than knowing it in advance.
