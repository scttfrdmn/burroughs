# 0085 — The public component API surface: a new component value type, resource handles first-class, and WIT-typed constructors

Date: 2026-09-09 · Status: **accepted** · [#697](https://github.com/scttfrdmn/burroughs/issues/697) · Resolves ADR 0084's deferred embedder surface · **Amendment 1 (2026-10-02, stamped by Scott) adds `context.Context` to `Component.Call` and makes cancellation a distinct outcome** — a question this body does not reach
Ratio-Class: carried

## Context

[ADR 0084](0084-the-component-loader-and-canonical-abi-lift-lower-for-value-types-sync-only-behind-gate-components.md)
deferred the embedder-facing API — *"what a loaded component looks like to an embedder is public API
surface … deferred until an embedder operation exists to shape it"*. Slice 2 (calling a component
export) is that operation, so the deferral comes due. An embedder needs two things it does not have: a
handle on a loaded component, and a representation for values crossing the component ABI — strings,
lists, records, variants, results, options, flags, and resource handles.

The existing `burroughs.Value` (root package) is a **core-wasm scalar**: a fixed struct of `bits`/`hi`
(for `v128`), a `ref`/`i31` for reference types, and constructors `I32`/`I64`/…. It was made safe to
export by narrowing to one enumerated payload kind ([ADR 0039](0039-a-references-payload-kind-crosses-the-two-boundaries-as-one-enumerated-kind-and-the-static-type-gate-is-its-own-census.md)).
No field of it can hold a string, a list, or a variant.

This is public API surface, so the decision is Scott's (behaviour 2, the escalation set). It was brought
as proposition/options/costs and ruled this review; the deliberation and the ruling in Scott's words are
recorded on [#697](https://github.com/scttfrdmn/burroughs/issues/697).

## Options

1. **A new component value type** — a `burroughs`-package tagged union over the WIT value types,
   separate from core `Value`.
2. **Extend `burroughs.Value`** — add component kinds (string/list/record/variant/…) to the core value
   struct.
3. **Map to Go types** — exports take and return concrete Go types (`string`↔`string`,
   `list<u8>`↔`[]byte`, record↔struct, variant↔a tagged Go type) through a marshaling layer.

## Decision

**Option 1: a new component value type, with Option 3 as a later *additive* helper layer over it** (the
A-then-D shape ruled on #602 — the explicit total form first, the ergonomic form as a helper on top,
never instead of).

Two shape requirements:

- **Resource handles get a first-class form in the type.** Hello-world already crosses `own`/`borrow`
  handles (stdout is an `output-stream` resource), so a handle is a value kind, not an escape hatch —
  backed this slice by a minimal host handle table, without the full own/borrow lifetime machinery.
- **Constructors carry their WIT type.** A value is constructed against the type it claims, so a
  mis-typed value refuses **at construction** rather than at the boundary — the refuse-early discipline
  the loader already applies to unmodeled sections, moved to the value surface.

`LoadComponent(wasm) (*Component, error)` mirrors `Instantiate`/`Instance`, as proposed.

## Consequences

- **Core `Value` is untouched.** Option 2 was refused on `Value`'s own design: a field that can hold a
  list widens every core call site to pay for a domain it never touches, and spends the narrowing
  (ADR 0039) that justified exporting `Value` at all.
- **Reflection stays off the call path.** Option 3 is what other Go runtimes offer and what embedders
  will eventually ask for, but it needs either reflection on the call path — which contract §0 already
  refused for host functions — or generated bindings, a tool this project does not have; and its
  WIT↔Go mapping is not total (variants have no natural Go form without codegen; results and options
  map awkwardly). Because the ergonomic layer is expressible as helpers over Option 1 later, it need not
  be decided now, and deciding it now would be reflection this project has refused.
- **Verbose but honest.** `burroughs.Variant("closed", nil)` is not what anyone wants to write, but it
  is the honest representation of a typed ABI, and verbose-but-correct is the right first form for a
  surface that is permanent.
- **Guest-driven scope.** The value type covers the WIT types slice 2's guest drives (primitives,
  `string`/`list`, `result`, a non-nested `variant`, `own`/`borrow` handles); records, tuples, flags,
  `enum`, and `option` are added when a guest needs them, and a value outside the modeled set refuses by
  name — the loader's discipline, at the value surface.
- **Consumer-triggered landing.** The type is not landed ahead of the lift/lower that consumes it: it
  arrives with the Canonical ABI codec (the differential against `definitions.py`), not as speculative
  public surface.
- **In-session ruling, no independent provenance.** Durability is not independence; commits resting on
  this ADR are `Ratio-Class: carried`.

## Amendment 1 (2026-10-02) — `Component.Call` takes a `context.Context`, and cancelling it cancels the task

**Stamped by Scott** on the #771 slice-2 cancellation question: *"I agree with the recommendation."* Appended
rather than folded into the body above, because the body is the record of what was decided on #697 and this is
a later decision about a question #697 did not reach — this ADR says nothing about cancellation, context, or
async, and that silence is why the question arose at all.

### The question, and why it was Scott's

Slice 2 of [#771](https://github.com/scttfrdmn/burroughs/issues/771) is checked against wasmtime 49, where
**host-initiated cancellation is dropping the call's future** — there is no public `cancel()`
(`concurrent.rs:2404`, *"Dropping a host `call_async` future which needs to cancel the task"*). Parity
therefore needs an equivalent host action in Burroughs, and **Burroughs had none at any level**: measured, no
exported entry point in the root package takes a `context.Context`, and the lifecycle methods that bound
anything (`Close`/`Join`/`Fault`/`Stop`) are on `interp.Instance` — internal — with `Stop` taking a
`time.Duration`, which cannot carry a cancellation. New public API surface is the escalation set's second
member, so the decision was Scott's rather than the slice's.

### The decision

```go
func (c *Component) Call(ctx context.Context, name string, args ...Value) ([]Value, error)
```

1. **`Component.Call` takes `context.Context` as its first argument.**
2. **Cancelling the context cancels the in-flight task** — the engine delivers the component model's
   cancelled event to the task, which runs its own cancellation path.
3. **The call returns a distinct cancelled outcome, checkable with `errors.Is`**, not a generic failure. **Its
   exact form is set from wasmtime's cancellation reading**, deliberately not chosen here: the reference
   behaviour — including what the canceller observes — is captured and committed first, and the sentinel is
   defined from it. Picking the form before the reading would be inventing the thing the parity witness exists
   to measure.
4. **The handle-based alternative is declined, and remains addable on top.** A `Start`-returning-`*Task` form
   with its own `Cancel`/`Wait` is more surface and a second concurrency idiom permanently beside Go's; it is
   expressible as a helper over (1) later, which is this ADR's own A-then-D shape — the explicit form first,
   the ergonomic one on top, never instead of. Declined **for want of a consumer**, not permanently.
5. **The asymmetry with `Instance.Call` is stated rather than resolved.** `Instance.Call(name, args...)` takes
   no context and is not changing: it is released surface, and adding a parameter would break every embedder
   to serve a path it does not have. So the component call carries a context and the core call does not. That
   is a real inconsistency in the public surface, accepted knowingly, and it is cheap only because
   `Component.Call` **has not shipped** — which is also why this decision had to be taken before the surface
   landed rather than after.

### What this does not decide

- **Versioning.** This lands with the component surface whenever that ships; the version number is Scott's
  (ADR 0004).
- **The sentinel's identifier and wrapping**, per (3) — set from the committed wasmtime reading in slice 2.
- **Nothing about the core-module path.** `gate:threads`' own lifecycle surface is a separate question and no
  part of it is settled here.

### Ordering, so the stamp is not read as a licence to land surface early

The chair's sequencing on the same review splits the work at the public boundary, and the split is the reason
this amendment can be recorded now without any surface appearing:

- **Slice 2 is entirely internal**: `task.cancel` (`0x05`), the parity witness driven through an internal
  embedding, the cancellation plumbing checked against wasmtime's committed reading, and the sentinel defined
  internally from it.
- **Slice 3 is the public half**: ADR 0085's surface — `LoadComponent`, `*Component`, the value type — landing
  together with this amendment's `Call(ctx, …)` signature and a test written from an embedder's point of view
  (cancel through the context, observe the sentinel). That is the first point at which cancellation is
  reachable from outside the module, and it is also this ADR's **consumer-triggered landing** condition coming
  due.

Recorded by the actor the ruling reached, so **no independent provenance**; commits resting on this amendment
stay `Ratio-Class: carried`.
