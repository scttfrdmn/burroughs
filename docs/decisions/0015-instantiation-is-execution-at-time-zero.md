# 0015 — instantiation is execution at time zero, so it may trap without gaining an opinion

Date: 2026-08-04 · Status: **accepted** (Scott, 2026-08-04, on #7's memory work)

A **refinement record, not a reversal.** `interp.Instance`'s doc comment made a deliberate
commitment — that the constructor *cannot fail* — and a commitment written down on purpose is
amended on purpose rather than silently outgrown. This is that amendment.

## Decision

Instantiation is **execution at time zero**. The interpreter's module constructor gains a
**trapping** path — allocating memories, copying active data segments — and gains **no**
power to judge modules. Concretely:

- Failures that are *verdicts about the module* stay #9's property, forever. A global
  initializer that is not a constant expression, an unlinkable import, a table whose element
  type disagrees: the interpreter never reports these, under any name.
- Failures that are *runtime events* are carried by `Trap`, which already exists for exactly
  this and whose `Reason` is the spec's own text. An active data segment copied out of its
  memory's bounds is `out of bounds memory access` — not a claim about the module.

So the constructor's signature grows an error, and what may travel through it is bounded by
type rather than by intention: `*Trap`, and nothing else.

## Question

`Instance`'s comment said the constructor "cannot fail", on the ground that

> An `Instantiate` returning an error would be a second place judging modules, and the
> judgement it would be making is #9's.

That reasoning is correct and it is not what the memory work violates. Retaining data segments
and copying them into a fresh memory is the first thing this engine does that can go wrong
*without anyone being wrong about the module*. Three readings were available:

- **A — keep the constructor infallible; copy segments lazily at first invoke.** Rejected: it
  relocates a trap the spec places at instantiation into whichever call happens to touch
  memory first, so a module that must trap *before* any function runs would instead run one.
  That is buying an unchanged signature with a wrong answer.
- **B — the constructor returns `error`, unconstrained.** Rejected on the original comment's own
  argument: an open error channel out of the constructor is precisely the second place judging
  modules, and nothing would stop #9's verdicts leaking into it later.
- **C — the constructor returns `(*Instance, *Trap)`.** Chosen. The trap channel is open and
  the verdict channel is not, enforced by the return type rather than by a comment asking
  future authors to be careful.

## Why this is the suite's taxonomy and not ours

The clincher, and it was **verified rather than assumed** before this ADR was accepted:
`data1.wast` is 14 vectors of `assert_trap` wrapping a bare `(module …)`, every one expecting
`"out of bounds memory access"`, with no invoke anywhere in the form. The oracle already
distinguishes a module that is *invalid* from a module that *traps while coming to life*, and it
has a whole file of the latter. A design that could not express that difference would be unable
to answer a question the judge is already asking.

Measured at acceptance: `assert_trap` appears **nowhere** in `internal/spec/wast.go`, so all 14
are `KindUnsupported` today — they are not on the fail board, and adopting this decision is what
makes them askable.

## Consequences

- **`New` becomes `Instantiate`, returning `(*Instance, *Trap)`**, and the doc comment is
  rewritten to state the two-kind distinction (verdicts to the validator, traps to execution)
  and to cite this record. The old comment's reasoning is *preserved* in it, not deleted: it was
  right about what it was defending.
- **`Module` retains data segments** (`Datas []DataSegment`), which is the consumer-forced
  retention pre-registered two PRs ago as the cure for the round-trip witness blindness.
  `decodeDataSegment` stops being recognize-and-discard — it *appends* to `Datas` — while keeping
  its bare-`error` signature; the function whose signature actually grows is
  `decodeDataSegmentMode`, now `(DataSegment, error)`. Stated at that granularity because the
  first draft of this bullet said "stops being error-only", which is false on the return-type
  reading CLAUDE.md uses when it counts 28 of 29 `decode*` functions returning bare `error`.
  That blindness is cured by this work
  *because the work cannot be done without curing it* — no separate PR was ever needed.
- **A fifth Kind, `KindAssertTrapModule`**, becomes available to the harness for the
  `assert_trap`-wrapping-a-module form. Its arrival is a *board* change and is charged to this
  work's PR: 14 vectors move off `unsupported`.
- **The infallible-construction property is not available to lean on elsewhere.** Anything that
  constructed an `Instance` for free must now handle a trap. That is the cost, and it is paid
  once at every call site rather than absorbed by a lazy path.
- **What this decision does not do:** it does not implement contract §3. Real instantiation
  links imports, evaluates global initializers, and runs the start function; none of that is
  here. §3 remains v1's, and this ADR's scope is exactly "the failure taxonomy of the
  instantiation this phase performs".

## Direction

Contract §0's *correctness-neutral* clause is what selects this: the suite asks the
trap-versus-verdict question, so the engine answers it on the suite's terms rather than on a
shape that happened to be convenient. No performance clause is engaged — the trap check is a
bounds comparison per active segment, paid once at load, on the thesis workload (§1: megabyte
Go guests loaded once) where it rounds to nothing.
