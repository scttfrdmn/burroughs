<!-- Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0 -->

# 0079 — the boundary refuses a reference argument by its own payload kind rather than by the parameter's spelling, and the register splits on whether a widening could lift it

Date: 2026-09-07 · Status: **proposed** — no stamp exists to cite, and *a `Status:` field is a citation to
an approval*, so it stays open until one does. Nothing here needs one to proceed: this is mechanism, which
is product work and self-merges on a bound green, it changes no gate's default, adds no exported symbol,
and reverses no stamped decision. Picked by the actor from the open backlog with no order behind it, so
every commit in this slice is `Ratio-Class: carried`.

Filed against **[#677](https://github.com/scttfrdmn/burroughs/issues/677)**. It changes one internal
signature and two call sites in `internal/interp`, widens one sentinel's doc comment to say what its five
landed call sites already do, and repairs one row of an existing control. It adds **no exported symbol**,
so it is outside the escalation set and is decided here under decide-and-proceed. The one part of this
subject that *is* Scott's is factored out to
[#680](https://github.com/scttfrdmn/burroughs/issues/680) — see *What this deliberately does not do*.

## Context

`internal/interp/interp.go:invokeIndex`'s parameter loop refuses a non-null funcref argument one line
before it converts, and #677's finding is that the *other* unrepresentable references are not refused at
all: `internal/interp/value.go:Value.toRef` turns a `PayloadStruct`, `PayloadArray`, `PayloadExn` or
`PayloadNone` argument into a non-null `ref` with no discriminator set, and the failure surfaces from
`internal/interp/castop.go:typeOfRef`'s default arm — a message about the engine's own consistency, for a
fault that is the caller's.

That is the defect, and it is the milder half of it. **Measured before writing this**, with a throwaway
probe over every payload kind at three parameter spellings, on `main` at the corpus pin `de54fd27`:

| argument `Value` | declared parameter | what happens today |
|---|---|---|
| `{externref, PayloadStruct}` | `externref` | **admitted**, no error at any point |
| `{externref, PayloadArray/PayloadExn/no kind/PastEnd}` | `externref` | **admitted**, no error at any point |
| `{externref, PayloadFunc}` | `externref` | **admitted**; `any.convert_extern` internalizes it |
| `{anyref, PayloadStruct}` | `anyref` | `ErrEngineInvariant` at the parameter — #677's case |
| `{funcref, no kind}` | `(ref func)` | `ErrEngineInvariant` at the parameter |
| `ExternRef(7)` | `funcref` | `ErrUnsupportedOp`, *"is a non-null funcref"* — it is not one |
| `{funcref, PayloadFunc, Bits: 0}` | `(ref func)` | **admitted**: a funcref to the callee's own function 0 |
| `{funcref, PayloadFunc, Bits: ≥ N}` | `(ref func)` | `ErrNotValidated`, several frames later |
| `{funcref, PayloadFunc}` | `funcref` | `ErrUnsupportedOp` — the existing guard, correct |

Four things in that table are not in #677's body, and each changes what the repair has to be.

### The `externref` case is not misreported, it is silently admitted

`toRef` reads externalization off the **static type** — `ext := v.Type == binary.ExternRef` — and
`typeOfRef` dispatches on `case r.Externalized:` **first**, before any payload discriminator. So for an
`externref` parameter the discriminator-less reference the two "cannot honour" arms deliberately produce is
answered `extern`, `matchRefType` agrees, and the value is admitted. What the guest then does with it
decides what the embedder sees:

- returned unchanged, it reaches the embedder as `RefKind: none, RefID: 0` — that is `(ref.extern 0)`, a
  host identity the value does not have and that `extern.wast:37` uses for a *different* reference. This is
  the fabrication `Value.RefKind`'s own doc comment says the discriminator exists to prevent, arriving from
  the inward direction the comment does not cover;
- internalized by `any.convert_extern`, it becomes a non-null `(ref null any)` naming no kind;
- reaching `ref.test`, it produces `ErrEngineInvariant` **inside the guest**.

So the honest statement of the defect is *silent corruption or a late engine-invariant error, depending on
the guest's instructions* — and the corrupting arm is the one that runs for the reference type the corpus
uses most. **`typeOfRef`'s arm order is not the bug and must not be touched**: its own comment argues that
ordering `Externalized` after `IsI31` would make `ref.test i31` answer 1 on a value whose static type is
`externref`, which is grave #36's misreported-payload class. The arm order is right for the question
`typeOfRef` asks. The refusal therefore has to happen *before* `toRef`, where the caller's own `Value` is
still the subject.

### The population is two call sites, and the second one's own comment says so

`toRef` has exactly two callers: `internal/interp/interp.go:invokeIndex`'s parameter loop and
`internal/interp/host.go:pushHostResults`. The second is structurally identical — the same
`w == binary.FuncRef && !got[i].Null` guard, the same `toRef`, the same `typeOfRef`/`matchRefType` pair —
because it is deliberately a copy: its doc comment states that a host result travels **inward**, so *"this
borrows that loop's discipline rather than the outward one's"*. Measured, it has every row of the table
above. #677's Scope names `interp.go`'s loop only, which is *an issue's list read as an inventory* for the
second consecutive slice ([#663](https://github.com/scttfrdmn/burroughs/issues/663) named one of two
`limits.Min` copies). A repair in one and not the other is the drift that comment is already guarding
against.

### The existing funcref guard is wrong in both directions, because it keys on the parameter

`p == binary.FuncRef` compares against `binary.FuncRef`, which is the value `ValType{kind: 0x70, null:
true}` — the **nullable abstract** spelling and nothing else. So:

- **it under-refuses.** A `(ref func)` parameter is a different `ValType`, so the guard does not fire, and a
  `PayloadFunc` argument reaches `toRef`, which resolves the bare index against the callee's own instance.
  With `Bits: 0` that is admitted: the caller fabricated a funcref to the callee's function 0. An
  out-of-range index is caught, but downstream and in the wrong register — `funcRefTarget` reports
  `ErrNotValidated`, blaming a module that is well-formed.
- **it over-refuses, with a false message.** An `externref` argument at a `funcref` parameter is caught by
  this guard and told it *"is a non-null funcref"*. It is not one. `matchRefType` two lines later would
  have said `is funcref, got externref` correctly — the guard preempts the check that had the right
  answer.

Both follow from the same thing: the shape being refused is a property of the **argument**, and the guard
reads the **parameter**. `Value.RefID`'s scope statement is about what an externally-supplied reference
can carry, and it says nothing about how the callee spelled its parameter.

### `ErrUnsupportedOp`'s doc comment is narrower than its five landed call sites

The sentinel reads *"the engine saying it has no arm for an instruction"*, and its doc argues its value is
that *"the board's failure bucket [is] a work plan keyed by opcode"*. Five call sites already use it for
something with no opcode at all: the funcref refusals at `internal/interp/interp.go:invokeIndex` and
`internal/interp/host.go:pushHostResults`, the host-function-has-no-reference-identity limit at
`internal/interp/call.go:funcRefTarget`, the re-exported-host-function refusal also at
`internal/interp/interp.go:invokeIndex`, and the unaligned-atomic-base limit at
`internal/interp/memory.go:checkBaseAlignment` — each containing symbol resolved with a parser rather
than read off a nearby line. The third of those *documents the broader reading as the rule* — *"which is
the register for **this engine cannot**, never `ErrNotValidated`, which would blame a module that is
well-formed"*. So the practice is settled and the prose is stale; the repair is to the sentence, not to
five call sites, and it is in this slice because this document cites that sentinel's meaning as the
authority for its own register choice.

## Options

1. **Refuse at each of the two call sites, keyed on the argument's `RefKind`.** Correct behaviour, two
   copies of one predicate. #663's own finding was two copies of one fact and the lesson was that the
   copy nobody is looking at is the one that goes wrong; a third call site added later inherits nothing.
2. **Refuse inside `toRef`, by giving it an error return.** The knowledge — which payload kinds can cross
   inward — lives in exactly the function that already enumerates them, and both call sites are forced by
   the compiler to handle it. A third caller cannot forget. Costs one internal signature change.
3. **Read the discriminator before `Externalized` in `typeOfRef`.** Rejected above: that order is
   load-bearing for `typeOfRef`'s own question, and inverting it reintroduces grave #36's class.
4. **Export a `Value.Validate()` and ask callers to call it.** A new exported symbol, so escalation; and it
   puts the check where a caller has to remember it, which is the property that made the funcref guard's
   two failure modes invisible.
5. **Carry the payload, so nothing needs refusing.** The right long-run answer for the aggregates and the
   subject of [#680](https://github.com/scttfrdmn/burroughs/issues/680). It changes the public `Value`'s
   vocabulary, so it is public API surface and Scott's, and it is not available to this slice.

## Decision

**Option 2, with the register split.**

`toRef` becomes `func (v Value) toRef(site string) (ref, error)` — **and losing the `*Instance` was not
planned.** It took one before this, and the only arm that read it was `PayloadFunc`'s, which built
`ref{Inst: in, Addr: index}`: a bare index the caller supplied, resolved against the *callee's* index
space, which is exactly the mechanism of the under-refusal below. With that arm refusing, `unparam`
reported the parameter unused, and the choice was to delete it rather than keep it for symmetry —
**a conversion that cannot reach an instance's index space cannot fabricate a reference into one.** The
lint finding is the defect's own shape arriving from the other direction, and it is recorded here because
it is a stronger closure than the refusal alone: the refusal can be deleted by a future edit, and the
missing parameter cannot be un-missed without someone re-adding it on purpose.

The `site` parameter is a
**pre-rendered string, not a format argument** — `internal/interp/call.go:funcRefTarget`'s own arrangement
and for its stated reason: the existing funcref messages at both call sites survive byte for byte, which
matters because their text is what the board's bucket keys were measured against.

The switch over `RefKind` is the whole refusal, and it names every member of the domain from `PayloadNone`
to `PayloadPastEnd` so that `exhaustive` fails the build when a kind is added without an arm — the
derived-domain property `PayloadPastEnd` is exported to provide:

| kind | crosses inward? | why |
|---|---|---|
| a null, at any type | yes | `RefKind` is not read; there is one heaptype-free null (grave #266) |
| `PayloadHost` | yes | `RefID` **is** the payload |
| `PayloadI31` | yes | `I31` **is** the payload |
| `PayloadFunc` | no | a bare module-local index names no instance — `Value.RefID`'s scope statement |
| `PayloadStruct`, `PayloadArray`, `PayloadExn` | no | the payload is guest-allocated; 0002's precision pin |
| no kind on a non-null reference | no | a `Value` naming no constructor is malformed |
| `PayloadPastEnd`, and above | no | the domain's bound is not a kind |

The complement is exactly what this package's own constructors build — `NullRef`, `ExternRef`, `HostRef` —
which is a checkable property and is what the new control checks rather than a hand-written expectation
list.

**Two registers, which is the split #677's Scope paragraph leaves open, decided on whether a widening
could lift the refusal:**

- `PayloadFunc`, `PayloadStruct`, `PayloadArray`, `PayloadExn` → **`ErrUnsupportedOp`**, the register for
  *this engine cannot*. Each of these is a real reference the engine declines to carry inward today and
  #680 could carry tomorrow. This preserves the funcref refusal's existing sentinel exactly, so its public
  class through `publicError` — `ErrUnsupported` — is unchanged for the shape that has it today, and the
  three sibling kinds join it rather than acquiring a class of their own.
- **no kind on a non-null reference**, and `PayloadPastEnd` → a **plain error**, no sentinel. There is no
  widening that makes a reference naming no constructor meaningful; it is a malformed argument, and
  `burroughs.go:publicError`'s own doc puts *"a bad argument type"* in the travels-unchanged class. Giving
  it `ErrUnsupportedOp` would tell an embedder a feature is missing when their `Value` is wrong.

The `p == binary.FuncRef` guards at both call sites are **deleted**, subsumed by `PayloadFunc`'s arm. That
is what fixes the over- and the under-refusal at once: the arm fires for a `(ref func)` parameter because
it never looks at the parameter, and it does not fire for an externref argument, which then falls through
to `matchRefType` and gets the type mismatch it deserves.

## Consequences

- **Two controls, each watched die**, and they fail for unrelated reasons.
  `TestAnUnexpressibleReferenceArgumentIsRefusedAtTheBoundary` walks the derived domain
  `PayloadNone..PayloadPastEnd` at an `externref` and an `anyref` parameter and at a host-function result,
  with the expectation computed as *"crosses iff Host or I31"* rather than tabulated;
  `TestTheFuncrefRefusalKeysOnTheArgumentAndNotOnTheParameterSpelling` is the over/under pair, and it is
  the only one that can see the guard come back.
- **An existing control's row stops testing its own subject, and is repaired.**
  `TestAHostFunctionThatDoesNotHonourItsDeclaredTypeIsRefused`'s *"a non-null funcref"* row passes
  `{Type: binary.FuncRef, RefID: 1}`, whose `RefKind` is `PayloadNone` — so the specimen is a *no-kind*
  reference, not a funcref, and under the new switch it would be refused by the wrong arm while still
  asserting `ErrUnsupportedOp` and still going green. A refusal ordered ahead of the one under test steals
  its row silently. The row gets `RefKind: PayloadFunc` and the no-kind case gets a row of its own.
- **The board is expected unmoved**, and the reason is a measurement rather than a hope: every reference
  *argument* the corpus supplies is a null, a `(ref.extern N)` or a `(ref.host N)` — `PayloadHost` and
  nulls — and the kinds this refuses were already measured at 0 corpus vectors in `Value.RefID`'s and
  `toRef`'s own comments. `extern.wast`'s externalized i31/struct/array values are **results**, which
  travel through `fromRef` and are untouched. Pre-registered here so the post-change board is a check and
  not a report: **60957 pass, 0 fail, 0 unsupported, 4187 gated, 0 unimplemented** over 256 files at pin
  `de54fd27`.
- **`unsupported` is structurally unmoved.** This changes what the boundary accepts from a Go caller; it
  changes nothing about what the harness can ask, which is the only thing that column moves for.
- **`toRef`'s two "cannot honour" arms lose their subject.** Their comments describe producing a
  discriminator-less reference for `typeOfRef` to report; after this they are the refusal itself, and the
  comments are rewritten rather than left to describe a path that no longer exists — *a sentence written
  before a change and left standing after it tells the next reader the tree is in a state it is not*.
- **A malformed reference argument now fails the call instead of corrupting it.** An embedder who was
  round-tripping an externalized aggregate — the reachable case, since the engine hands those out — gets a
  named decline at the parameter where they can act on it, in place of `(ref.extern 0)` or an engine
  invariant several frames into the guest. Nobody can be relying on the previous behaviour: the two
  outcomes it produced were a wrong value and a report blaming the engine.

## What this deliberately does not do

- **It does not make the round trip work**, which is [#680](https://github.com/scttfrdmn/burroughs/issues/680).
  Refusing is the honest interim and not the answer: a `Value` that carried a reference the engine handed
  out would need a new public member with a lifetime relationship no other member has, which is public API
  surface and Scott's.
- **It does not touch `typeOfRef`'s arm order**, for the reason its own comment gives.
- **It does not change any sentinel's identity.** `ErrUnsupportedOp` keeps every call site it has; what
  changes is one paragraph of its doc comment, which claimed *instruction* while five landed sites read it
  as *this engine cannot*. The narrower sentence was the stale one.
- **It does not add a validation pass over `Value`.** The check runs where the value is consumed, at the
  two places that convert one, for the reason `ErrUnsupportedOp`'s own doc gives about pre-scanning: a
  check ahead of use refuses shapes on paths that never execute.
