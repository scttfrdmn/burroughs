# 0011 — the wat parser returns an error, and the text path will emit bytes rather than modules

Date: 2026-08-01 · Status: **accepted** (Scott, 2026-08-01 — option A on #66, with C
explicitly ruled out and the accept-direction bridge named in the same ruling)

## Decision

**Two parts, and the second is the reason the first is not a deferral.**

1. **The wat parser's public surface is error-only.** `text.ReadModule(src []byte) error`,
   matching `spec.ReadTextFunc` exactly. The context carrying index spaces and binding
   state is unexported. Nothing constructs a module value.

2. **When well-formed text modules are needed, the parser emits binary bytes into the
   proven decoder.** It never constructs modules. There is **one module authority in the
   codebase, ever** — `binary.Module` — and the text path reaches it through an encoder,
   buying the binary path's entire conformance record for the price of that encoder.

## Question

Every one of #62's 207 reachable vectors is a *rejection*: 176 UTF-8 at `name`, 1 at `var`
(`id.wast:31`), 16 import-ordering, 13 duplicate-binding, 1 `multiple start sections`. Not
one needs a module handed back. But #8's terminus is `assert_return`/`assert_trap`, which
needs something instantiable — so the question was never *whether* a module form is needed,
only *when it gets designed and from whose requirements*.

## Options

- **A — error-only surface, unexported context.** Chosen.
- **B — design a text-side `Module` now.** Its only consumer would be reject vectors, so
  the shape gets fitted to code that never reads it: the load-bearing-spot manoeuvre 0006
  declined.
- **C — reuse `binary.Module` directly.** Ruled out explicitly.

## Why C was ruled out, and the general form of the reason

`binary.Module` is `{Version, Sections}` with payloads aliasing the input image
(`binary.go:288`, `binary.go:293-295`). Wat has neither sections nor an image to alias.
Forcing the type would mean synthesizing section bytes — an encoder wearing a struct's
clothes — or widening the type for a producer that does not fit it.

Stated one notch more generally, which is the part worth keeping: reuse here would be
**nominal consistency purchased with a structural lie — a type shared for its name rather
than its shape.** 0002 asks for one internal form; it does not ask for one *identifier*
spanning two shapes. And a second module representation, had B been taken, would be the
enrolled-witness-or-derived-artifact rule violated at the type level: two artifacts holding
the same fact, neither derived from the other.

The bridge resolves both objections at once. The text path is a *derived* producer — it
emits the binary format and the existing decoder is the single authority that reads it — so
there is no second representation to keep in agreement, and no shape designed from reject
vectors.

## Consequences

- **#62 builds productions that return errors and nothing else.** The context exists
  because the reference's grammar puts ordering and duplicate checks *inside* it
  (`parser.mly:1321`–`1354` for `import after <kind> definition`, `bind_abs:174` for
  `duplicate <category>`, `:1372` for `multiple start sections`), not because a module is
  being assembled.
- **The encoder is a new artifact, and it does not exist yet.** Its cost is real and is
  paid in #7's era, not this one.
- **The drift risk is re-pointed, not discharged.** The risk option A accepts is normally
  "two places know the same fact and drift". The named bridge *dissolves that subject* —
  there is only one module representation — so under *a tripwire whose subject dissolves is
  re-pointed, never closed*, the obligation survives and moves: the live risk becomes **an
  encoder that is not a faithful bridge**, so that a well-formed text module is rejected,
  or accepted as the wrong module, with the binary path's conformance record vouching for
  neither. That is an *accept-direction* risk, which is the direction the suite is weakest
  in — the same asymmetry that made #62's UTF-8 siting worth pre-registering.
- **The re-pointed tripwire is pre-registered in #8's definition of done**, scoped to the
  space rather than to the fields #63/#64 happen to need: for every suite text module that
  must succeed, the emitted bytes decode clean and to the module the text denotes. Filed at
  this ADR's acceptance as **#67**, per *a design debt is discharged by a tripwire, never by
  an intention*. Its two halves fail independently — bytes that are malformed, and bytes that
  are well-formed for the *wrong* module — and it is closed by the control being falsified in
  both, not by the encoder landing.

## Veto line

Scott's veto stays open on the bridge, as a design call of 0006 shape. Nothing in #62
depends on it: the error-only surface is what #62 builds against, and the bridge governs a
consumer that does not exist yet.
