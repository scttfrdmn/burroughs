<!-- Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0 -->

# 0077 — a non-null reference with no defining instance is the engine's own broken invariant, so it gets its own sentinel rather than the module's blame

Date: 2026-09-06 · Status: **proposed** — no stamp exists to cite, and *a `Status:` field is a citation to
an approval*, so it stays open until one does. Scott's instruction on the
[#671](https://github.com/scttfrdmn/burroughs/issues/671) review was *"#669 next."*, which schedules this
work and approves none of its option choices; it is in-session with no artifact behind it, so every commit
in this slice is `Ratio-Class: carried`. *Durability is not independence.*

Filed against **[#669](https://github.com/scttfrdmn/burroughs/issues/669)**. It touches two call sites in
`internal/interp` and adds one internal sentinel. It adds **no exported symbol** anywhere — see *What this
deliberately does not do* — so it is outside the escalation set and is decided here under decide-and-proceed.

## Context

`internal/interp/call.go:funcRefTarget` resolves a funcref to an `(instance, function)` pair, and its first
line dereferences the reference's own instance pointer with nothing between:

```go
target := r.Inst
fn, ok := target.mod.DefinedFunc(r.Addr)
```

Go's zero `ref` is `{Null: false, Addr: 0, Inst: nil}` — the value grave #246 named in frame locals — so
such a reference reaching this function is a nil-pointer panic, repanicked out of `invokeIndex`'s recover
and visible to an embedder as an engine crash. #669 measured that rather than deducing it: deleting the
fill loop from `table.grow`'s reslicing arm and running a guest `call_indirect` through a grown slot
produces the panic, with the frame `interp.funcRefTarget({0x0, ...}) call.go:473`.

That frame is the one coordinate in this document that stays a coordinate: it is a **verbatim runtime
artifact**, and the line number is the datum rather than a pointer to be followed — ADR 0024's *"cited as"*
column is the same class, *"counted, not exempted"*. It is already stale against this branch and that is
correct; the trace was taken on an injected tree that no longer exists. Every other location here is written
in ADR 0047's `path/to/file.go:SymbolName` form, so the eleven that name code moved and this one did not.

**Not live on main, which is why #669 is an issue and not a grave.** Every `[]ref` allocation site in the
engine fills: `newTable`'s initializer fill, the reservation arm's fill, `publish`'s fill, and `newFrame`
since #246. No zero `ref` reaches a funcref slot today and no board is wrong. What is absent is the second
line of defence at the point of *use* — and #246's lesson is precisely that the fill sites are where this
property gets re-established by hand, once per site, forever.

### The reachable population is two of the three call sites, not three

`funcRefTarget` has three callers, and they are not equally exposed:

| call site | operand's origin | reaches a nil `Inst`? |
|---|---|---|
| `internal/interp/call.go:resolveCallIndirect` | a table slot, `Null` checked | **yes** |
| `internal/interp/call.go:resolveCallRef` | the operand stack, `Null` and `Exc` checked | **yes** |
| `internal/interp/castop.go:typeOfRef` | reached only from `case r.Inst != nil:` | **no, by construction** |

The third is guarded already, by a discriminator that exists for an unrelated reason — that switch uses
`r.Inst != nil` as *the test for being a funcref at all*. The guard still belongs in `funcRefTarget`, which
is where the property is needed and where a fourth caller would arrive without knowing to check; but the
reachability claim is two, and stating three would have been *an issue's list read as an inventory*.

### The engine already reports this exact value, one file over, and it registers it as the module's fault

The default arm of that same discriminator switch, in `internal/interp/castop.go:typeOfRef`, catches a
non-null reference matching none of `Externalized`, `IsHost`, `IsI31`, `Obj`, `Exc`, `Inst`. **That condition
is wider than the zero `ref`**: it also catches a future payload kind added to `ref` without an arm here,
which is what the switch's own comment is written for (*"a payload with no arm here is not unreported, it is
misreported"*). Today the zero `ref` is the only value satisfying it. Its own comment names what it is:

> a non-null reference with no discriminator set at all, which no construction site produces and which is
> therefore an engine inconsistency rather than a missing feature.

and then reports it as `ErrNotValidated`. So the register question is not open ground: there is a precedent
in the tree, it describes this value in these words, and **it falsifies `ErrNotValidated`'s own documented
promise** — that sentinel's comment says every one of its call sites *"becomes unreachable when a validator
refuses these modules before they reach this package."* A validator cannot make that arm unreachable, because
its condition is a property of an engine construction site and no module. The universal is already false on
main, and #669's site would be the second counterexample rather than the first.

**The clause this document quoted approvingly — *"which no construction site produces"* — is false, and
finding that out is [grave #676][676].** `internal/interp/value.go:Value.toRef` produces this shape at three
of its arms and says so in its own comment: a `*gcObj`/`*excObj` is guest-allocated and inexpressible in a
public `Value` (0002's GC-precision pin), so `PayloadStruct`/`PayloadArray`/`PayloadExn` arrive with nothing
to rebuild from, and `PayloadNone` names no kind at all. All four then reach `typeOfRef`'s default arm
**from an ordinary `Invoke`**, through `invokeIndex`'s parameter loop, measured with no plant and no
mutation:

```text
none    -> interp: module reached the interpreter unvalidated: "g" parameter 0 on a non-null reference with no payload discriminator set
struct  -> (identical)
array   -> (identical)
exn     -> (identical)
```

against `(module (func (export "g") (param anyref)))` on the GC lane, on main before this slice. So the
sentence an embedder gets today for passing an argument this boundary cannot represent is a claim about
their module. That strengthens the case for the re-point and weakens this document's original reason for it:
the argument was *"two sites, one value, two registers"*, a consistency claim, and the measured fact is that
one of the two sites is live on the public boundary. The clause also explains why the arm had no oracle —
nobody writes one for a branch a comment calls unreachable — and this slice writes it.

**What the boundary should say to a host is [#677][677], and this decision does not settle it.** The fault
there is the caller's: they built a `Value` with a payload that cannot cross inward, and the model for
refusing it is the non-null-funcref refusal three lines earlier in the same loop, which names the boundary
and cites `Value.RefID`. The re-point here still moves that message in the right direction — blaming the
engine, where the boundary genuinely should have refused earlier, is a report someone can act on, and
blaming the module is not — but *"engine invariant broken"* is not the final answer for a host's bad
argument and is not claimed to be.

[676]: https://github.com/scttfrdmn/burroughs/issues/676
[677]: https://github.com/scttfrdmn/burroughs/issues/677

## The register is the decision

Three sentinels exist. Measured against this value, each one says something untrue:

- **`ErrUnsupportedOp`** — *"no arm for opcode"*. There is an arm; it ran. False.
- **`ErrUnsupported`** — *"feature not implemented in this phase"*. Nothing was asked for that this phase
  lacks. False.
- **`ErrNotValidated`** — *"module reached the interpreter unvalidated"*. The module is well-formed, the
  slot's element type is right, the index is in range. The engine published an unfilled array. False, and
  false in the direction that costs someone else time: `burroughs.go:publicError` translates only
  `*interp.Trap` and the two unsupported sentinels, so **this text is what an embedder reads**, and it sends
  them to audit a module that has nothing wrong with it. That is *the defect stated as the rule*, one layer
  out — the message asserting the property the situation lacks.

`ErrUnsupported`'s own doc is the precedent for what to do about it: *"The third category, and it exists
because the first two would have lied."* Same argument, one category later.

## Options

1. **`ErrNotValidated` at the new guard** — the issue's option 1. One branch, consistent with
   `internal/interp/castop.go:typeOfRef`'s default arm, and it spreads a sentence that is false about the
   module to a second site while leaving the sentinel's documented retirement promise falsified.
2. **`ErrNotValidated`, and amend its doc's universal** to admit that it also carries engine inconsistencies.
   Honest about the state of the tree, but it widens a sentinel whose entire documented identity is *"a
   declared layering debt, not a validation verdict"* until the identity no longer distinguishes anything,
   and it keeps the wrong sentence in front of the embedder.
3. **A fourth internal sentinel, at both sites.** `ErrEngineInvariant` — *"interp: engine invariant broken"* —
   used by the new guard and by that default arm, whose comment already argues for it. Costs one sentinel and
   one re-point; repairs `ErrNotValidated`'s falsified universal instead of adding to it.
4. **Panic, with a clear message.** Rejected by local precedent inside `funcRefTarget` itself: its last arm
   cites grave 0003 for returning rather than panicking when the condition asserts a property of *sibling*
   code, *"and a future arm could falsify it silently"*. A nil `Inst` asserts exactly that about the fill
   sites.
5. **Trap `uninitialized element`.** Rejected, and named because it is the tempting one — it is what
   `table.grow`'s comment *assumed* would happen before #669 measured it, and it produces a plausible,
   spec-shaped failure that a board would score as a legitimate trap. It would put an engine bug behind a
   verdict the spec reserves for a guest's own mistake, which is the one outcome that makes the defect
   invisible rather than merely misfiled.

## Choice

**Option 3.** `ErrEngineInvariant` is added to `internal/interp`, the guard returns it, and
`internal/interp/castop.go:typeOfRef`'s default arm is re-pointed onto it.

The re-point is in this slice and not a separate one because this slice is what makes it wrong to leave: two
sites, one value, two registers is an inconsistency this PR would be creating. That is the
verdict-compelled test — *was the PR blockable without it?* — answered yes, on a defect of its own making.

**The re-point was pre-registered against the board, and the board held.** `internal/spec` keys failure
buckets by error text, and *"module reached the interpreter unvalidated"* is the head ADR 0025's G-1
carve-out is counted by, so a re-point that moved a bucket would be a finding worth more than the re-point.
Measured before and after, same corpus pin `de54fd27`: **60957 pass, 0 fail, 0 unsupported, 4187 gated, 0
unimplemented** over 256 files, identical both ways, with no failure stratum non-zero to key anything by.

**And the two reachability facts are different facts, which is what the identical board says.** The corpus
did not move because every vector in it is guest-side, and the live path to this arm is host-side —
`invokeIndex`'s parameter loop, reached by an embedder's `Value`. A `.wast` file cannot construct a
malformed host argument, so **no corpus, present or future, could have found this**; it is the same
accept-direction shape as an unfilled slot, one boundary out. The board is the right instrument for *"does
this re-point move a bucket"* and the wrong one for *"is this arm reachable"*, and reading the unchanged
board as the second would have been the analytic zero.

## Consequences

- **One nil compare** ahead of two lookups that already run on this path. No figure is claimed for it and
  none is measured: *cheap is a grammar claim*, and the grammar here is a pointer comparison against a
  branch that already does a map lookup and a slice index. If that needs pricing it needs a benchmark, and
  this ADR does not pretend to one.
- **The oracle reaches the guard through a guest `call_indirect`, not through the helper.** A test writes a
  zero `ref` directly into a live table slot — `table.view()` returns the published `[]ref`, and package-
  internal access is what makes this possible without touching engine source — and then invokes. That is the
  real path from #669's panic trace, and it is available without the source injection the issue used, so the
  regression is covered by a committed test rather than by a mutation someone has to remember to re-run. *A
  control can test the helper, not the path*; this one takes the path.
- **Grave #676 and issue #677 came out of implementing this**, both from the same measurement: the arm's
  *"no construction site produces"* clause is false (the grave, repaired in this slice with the oracle it
  had been going without), and what the boundary ought to say to a host who trips it is unsettled (#677,
  filed and not touched here).
- **The fill sites keep filling.** This is a second line of defence and it is not a licence to stop
  establishing the invariant where the arrays are made. A future `[]ref` allocation that forgets its fill is
  still a bug; it now surfaces as a named error at the point of use instead of as a crash.
- **`publicError` is not taught to translate it**, so it travels to an embedder as its own text. That is the
  point: the message names the engine, and an embedder reading *"engine invariant broken"* files a bug here
  rather than auditing their module.

## What this deliberately does not do

**It adds no public sentinel.** A root-package `ErrEngineInvariant` would be public API surface and therefore
Scott's and chat-Claude's to approve — and it would be surface with no use: there is no recovery an embedder
can perform against a broken engine invariant, so there is nothing to branch on, only something to report.
Declining is a choice rather than an omission, recorded so that a future reader wanting one knows it is an
escalation and not an oversight.

**It does not audit the other discriminator sites.** `internal/interp/gcobj.go:notAggregate` and
`internal/interp/value.go:payloadOf` read `r.Inst != nil` as the funcref test too, and both handle the zero
`ref` by falling through — to a kind message that omits it, and to `PayloadNone`. Neither crashes and neither
lies, so neither is this slice's subject; they are named here so the next reader knows the sweep was done and
where it stopped.
