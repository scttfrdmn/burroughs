# 0059 — The safepoint poll is guarded at the `pc` assignment, because a back-edge is a runtime comparison and straight-line code pays nothing

Date: 2026-09-02 · Status: **proposed** — no stamp exists to cite for the mechanism, and *a `Status:`
field is a citation to an approval*, so it stays open until one does. What Scott did rule on, in
session and therefore unciteably, is the **narrowing** that put this document's pre-registration in
scope: *"The rule was never 'no forecasts in correctness work'; it was that pre-registration attaches
to performance claims. A poll on every loop back-edge inside `runFrame`'s hot loop makes a performance
claim by existing."* That is an order about where a forecast attaches, not a choice among the options
below, and conflating the two would be exactly the forged provenance this project treats as worse than
a wrong option. Recorded by the agent that was ordered, which is durable but not independent.

Filed against **[#515](https://github.com/scttfrdmn/burroughs/issues/515)**, which scopes contract §3.
This document covers **SP-1's placement question only**. SP-2, SP-3 and SP-4 are correctness clauses
and take tests rather than a priced mechanism; SP-4 additionally needs `Spawn`, which is parked.

## Context

Contract §3 SP-1 is prescriptive about *where*, not merely *that*:

> Preemption is **engine-native**. The engine MUST implement epoch/safepoint checks (loop back-edges
> and call sites) such that a host request `stop(deadline)` brings every guest thread to a safepoint
> within a bounded, configurable interval. The guest runtime MUST NOT need to self-instrument its own
> code generation to be stoppable.

The site is `runFrame`'s walk over a function body (`internal/interp/exec.go`), which is
`for pc := 0; pc < len(body); pc++` with control-flow arms assigning `pc` directly and letting the
loop's `pc++` carry it forward.

**A back-edge is a property of an execution, not of a site.** `exec.go` has **14** `pc = …`
assignments, and the same one goes backwards or forwards depending on which label was resolved:
`pc = target - 1` out of `branch` is a back-edge when the label is a `loop` and a forward jump when it
is a `block`. Three of the fourteen (`pc = els`, `pc = end`, `pc = ctrl[len(ctrl)-1].cont - 1`) are
*structurally* forward, and five (`pc = c.PC - 1`, the `try_table` handler continuations) inherit
whatever the caught label was.

That leaves two ways to know, and only one of them is self-checking. Reasoning about which labels can
be loops is a claim about the grammar, needing its own authority and able to be wrong in the direction
no vector sees. **Comparing the two numbers reads the actual execution** — the same property that
makes `wordAligned` unable to answer wrongly about an address it dereferences. So the poll's condition
is a comparison, and this ADR is about where the comparison goes.

The per-thread state the poll reads already exists: [ADR
0050](0050-the-per-thread-context-is-its-own-object-reached-by-one-pointer-on-stack-because-3-and-5-need-more-per-thread-state-than-a-slot.md)
landed `thread` with `id` and `slot`, reached by one `*thread` on `stack`, and that field's own comment
names *"#515's safepoint check"* as its first reader. Another goroutine requests the stop, so the read
is an atomic load rather than a plain one — a non-atomic read of a word another goroutine writes is a
data race, which is undefined behaviour and therefore not a performance option.

## Options

### A — One compare at the loop head, against a remembered previous `pc`

`prev := pc` at the bottom of each iteration, `if pc <= prev { poll() }` at the top.

One site, trivially complete, and no assignment arm changes. It costs **one compare and one register
on every instruction executed**, to save a compare on each redirect. That is the wrong side of the
trade for the bodies this engine actually runs: a body that never branches pays the whole tax and
takes the whole benefit of nothing. It is also the option whose cost is hardest to attribute later —
a per-instruction compare is spread across every benchmark row rather than concentrated where the
mechanism is.

### B — Guarded at each assignment, through a returning helper *(chosen)*

Every arm that assigns `pc` routes through

```go
func (t *thread) jumpTo(target, pc int) (int, error)
```

which polls when `target < pc` and returns `target`. Call form is two lines, `pc` stays an ordinary
local assigned from a return value rather than through a `*int` — which matters, because a pointer
parameter would take `pc` out of a register and pay option A's tax by another route while claiming to
avoid it.

**Straight-line code pays nothing at all**: no compare, no load, no register held, because
straight-line code never assigns `pc`. Forward branches pay one compare. Only back-edges pay the
atomic load.

Its cost is verbosity across fourteen sites and, more seriously, a **completeness** obligation: a
fifteenth arm added later that assigns `pc` raw is a hole no test would notice. That is answered by a
control rather than by care — see below.

### C — A countdown, polling every *n*th instruction

A per-thread counter decremented per instruction, polling at zero. Bounded arrival interval by
construction, and no reasoning about control flow at all.

It pays a decrement on every instruction (option A's shape, slightly worse) **and** it does not satisfy
the clause as written: SP-1 names *loop back-edges and call sites*, and a guest in a tight loop is
exactly the case a decrement handles no better than a compare. Kept as this document's **rollback**
rather than its choice, because it is the mechanism that trades interval tightness for cost if B's bar
fails — *n* is a term in SP-1's "bounded, configurable interval", not a violation of it.

### D — Guest self-instrumentation

Refused by the clause itself: *"The guest runtime MUST NOT need to self-instrument its own code
generation to be stoppable."* Listed because a rejected option a later reader can see was considered
is worth a line, and because this is the option every host that ships cooperative preemption picks by
default.

### E — Call sites only, no back-edge poll

Cheapest of all, and it fails SP-1's bound rather than its letter: a guest looping on arithmetic with
no call in the body never reaches a safepoint, so *"within a bounded interval"* becomes unbounded for
the one program shape the clause exists to preempt.

## Choice

**Option B**, with unconditional polls at the call sites (`invoke`, `enterFrame`) that SP-1 names
alongside back-edges — there is no cheaper predicate at a call, and a call is already expensive enough
that a load is not the term that matters.

**Completeness is asserted, not maintained.** A control derives the population from the source rather
than from a list: every `pc` assignment in `runFrame` must be either the loop header's own or the
result of `jumpTo`. That is a check over the syntax tree and not a grep, because a grep measures text —
and its own falsification is part of the work: a fifteenth raw `pc = …` is injected, the control is
watched fail on it, and the injection is reverted. *A control isn't born until it's watched die*, and a
control over a population **derived** from the function is what keeps this from being scoped to
today's fourteen.

## Consequences

- Fourteen arms in `exec.go` grow two lines each and one new error path apiece. The diff is wide and
  shallow, which is the shape most likely to hide an inconsistency — hence the control above rather
  than a reviewer's eye over fourteen near-identical hunks.
- `thread` gains an atomically-read stop/epoch field. Its `slot` field's `nolint:unused` directive is
  **not** retired by this ADR: `slot` is T-4's guest-visible slot and this poll does not read it, so
  the directive's subject is unchanged. Retiring it here would be a suppression deleted for the wrong
  reason.
- The poll can return an error, so every one of the fourteen arms acquires an error path. Whether a
  stop surfaces to the guest as an error at all is SP-2/SP-4's question, not this one; what this
  document fixes is that the *return path exists*, because retrofitting one into fourteen arms later is
  the change this ADR's own diff shape argues against.
- **The measurement is pre-registered on the issue, before the mechanism existed** —
  [#515 comment](https://github.com/scttfrdmn/burroughs/issues/515#issuecomment-5513256405), and it is
  cited rather than restated so the two cannot drift. Its structure, briefly: two arms with opposite
  predicted directions, `membench` (straight-line bodies) predicted to **not move** and `dropbench`
  (a real guest loop) predicted to move under a stated bar; `membench`'s zero is a **scope control and
  an analytic zero**, not evidence of cheapness, since a straight-line body executes no back-edge and
  the zero could not have come out otherwise; and identical movement in both arms is the finding rather
  than a pass, because it would mean the instrument is not measuring back-edges.
