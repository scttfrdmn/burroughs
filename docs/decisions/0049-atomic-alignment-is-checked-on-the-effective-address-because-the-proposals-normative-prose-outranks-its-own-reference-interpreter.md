# 0049 — Atomic alignment is checked on the effective address, because the proposal's normative prose outranks its own reference interpreter

Date: 2026-08-30 · Status: **accepted** — Scott's ruling on the [#547
review](https://github.com/scttfrdmn/burroughs/pull/547), relayed verbatim to [a durable comment on
#546](https://github.com/scttfrdmn/burroughs/issues/546#issuecomment-5472917048): *"don't adjudicate between two artifacts of the
same superseded proposal. `eval.ml` and `Overview.md` are both threads-proposal documents; the merged spec
is a third authority neither of them is. Resolve against its wording — the same principle that settled u32
and the address type: the standard outranks the snapshot."* The ruling supplies the **principle** and
explicitly declines to supply the answer (*"I'm not asserting what the merged text says"*), so what follows
is a measurement against the authority it names.

Filed against **#546**. Implements the alignment half of **#545**'s executor, whose first commit shipped the
other reading.

**One premise of the ruling is falsified by measurement, and the ruling survives it.** There is no merged
spec to resolve against — not at this pin set and, on two checks, not upstream today either. The principle
still decides the question, because the proposal repository is a *fork of the spec repository* and carries
standard-form normative prose of its own, which stands in the same relation to `eval.ml` that a merged
spec would: specification against implementation.

## Context

`internal/interp/atomic.go` executes the 0xFE region's 67 opcodes. Six of them check alignment, and the
question is which address the check applies to:

- the **dynamic** address — the popped operand `i`, alone; or
- the **effective** address — `ea = i + memarg.offset`.

The two coincide whenever the static offset is zero, and **every atomic in both corpora carries a zero
static offset**. Measured over both entire, with the search falsified against a population where the
construct does occur:

```
$ grep -rhoE '(i32|i64|memory)\.atomic\.[a-z0-9_.]+[^)]*offset=[0-9]+' \
      testdata/spec third_party/spec-threads/test | wc -l
0
$ grep -rhoE '(i32|i64)\.load[a-z0-9_]*[^)]*offset=[0-9]+' \
      testdata/spec third_party/spec-threads/test | wc -l
2424
```

So the suite cannot decide this, and **it confirmed that by not moving**: switching all six call sites from
one reading to the other left `atomic.wast` at 297/297, the threads lane at 576 pass / 41 fail, and the core
board at 60957 pass / 0 fail. A behaviour change across six execution sites that moves no vector in 61574 is
the corpus stating, in the only way it can, that it has no witness. *Identical boards are the finding rather
than the licence.*

## The authority question, which is what this ADR is actually about

The first framing was `eval.ml` versus `proposals/threads/Overview.md:344-345`, and it was **ruled out
rather than answered**: both are artifacts of the same proposal, and neither is the standard. That framing
was also *understating the disagreement*, which is a second reason to abandon it — `Overview.md`'s sentence
is about wait and notify only, so read through it the conflict looks like two of seven arms. It is six of
six. A design overview is a summary and its silence is not agreement.

### The merged spec does not exist, so the ruling's own artifact is unavailable

- **At the pin set.** The core pin (`bdd7164`, 2026-07-28) has **no atomics at all**: zero `Atomic`
  constructors in `interpreter/exec/eval.ml`, no `check_align`, and the string `atomic` in exactly one
  prose file, where it means "atomically" in the linking sense. There is no second interpreter to
  cross-check the region against — unusual for this project, and worth stating because most regions have
  two.
- **Upstream today**, on two checks, neither decisive alone: `document/core/exec/` contains the same eight
  files as the pin's, with no atomics file added; and a code search for `ATOMICLOAD` across
  `WebAssembly/spec` returns 0. The second is a **lagging search index** — this project already has a law
  about trusting one for a queue claim — so it is corroboration, not proof.
- A third observation makes the upstream question harder rather than easier: upstream's
  `document/core/exec/instructions.rst` is **1167 lines** where the threads fork's is **4218**. The prose
  is being restructured, so "the same path is missing atomics" is not the same claim as "the spec omits
  atomics", and reading a rewritten file as if it were the old one is how a fetch reports a zero it did not
  measure.

**Whether upstream has merged threads is therefore a question about moving the core pin**, which is its own
gated PR and not this ADR's to answer. Filed as **#548** so the question has a subject rather than a sentence here.

### What the available standard-form text says

The threads proposal repository is a fork of the spec repository, so it carries
`document/core/exec/instructions.rst` — the spec's own normative prose, in the spec's own formal style, with
the numbered execution steps. Not a design overview. It answers the question six times, each site defining
`ea` two lines above the trap that guards it:

```
spec-threads/document/core/exec/instructions.rst
  1737/1743  load with ord         "Let ea be the integer i + memarg.offset" / SEQCST trap
  2285/2291  store with ord        same pair
  3066/3068  atomic.load(n)        "Let ea be i + memarg.offset" / trap
  3205/3207  atomic.rmw(n)         same pair
  3364/3366  memory.atomic.notify  same pair
  3481/3483  memory.atomic.waitN   same pair
```

Every one reads *"If `ea` modulo `N/8` is not equal to `0`, then: Trap."*

`eval.ml` computes something else. All six `check_align` call sites (`exec/eval.ml:381,393,405,422,445,462`)
pass `addr = I64_convert.extend_i32_u i` — the popped operand alone — while the static `offset` travels
separately to `Memory.load_num`/`store_num` and is folded in by `effective_address`
(`runtime/memory.ml:91-94`) only after the check has passed.

**This is an upstream inconsistency, not a reading of ours.** The proposal's specification text and the
proposal's reference interpreter cannot both be satisfied.

## Options

### A — Follow `eval.ml` (what the first commit did)

The reference interpreter is this project's authority for the region's semantics generally, and
[0007](0007-opcode-table-authority.md) makes it a live authority
rather than a one-time influence. Consistency is a real argument: the other 66 rows are transcriptions of
it.

Rejected. The reference is an *implementation of* the prose, so where they diverge the implementation is
the thing with the bug, and transcribing the bug reproduces it. It also loses on its own terms: 0007's
choice of `decode.ml` was scoped to the **byte↔shape mapping**, on the stated ground that *"upstream
publishes nothing machine-readable"* — a finding about opcode tables, not a ranking of prose below the
interpreter for semantics. Citing it here would be *checking a ruling's conclusion instead of its premises*.

### B — Follow the normative prose (chosen)

`ea = i + memarg.offset`, trap on `ea mod N/8 != 0`, at all six sites. The specification outranks the
snapshot, which is the ruling's principle applied to the best artifact that exists.

Cost, stated plainly: this engine now **knowingly disagrees with the reference interpreter** on a
population the suite does not cover, having previously agreed with it. If upstream resolves its own
inconsistency the other way, this inverts.

### C — Defer, keeping `eval.ml`'s reading until a merged spec exists

Rejected because the deferral has no end date that anyone controls, and because "wait for an authority" is
indistinguishable in the tree from "we chose the reference". A decision no artifact records is a decision
that gets re-made by the next reader.

## Consequences

- `checkAlign` takes the offset as a parameter, so the rule is in the signature. Dropping it is a compile
  error at six call sites rather than a silent revert.
- **The reading is observable and pinned**, per the second half of the ruling: *"whichever way it lands,
  add the discriminating vector."*
  `TestAtomicAlignmentIsCheckedOnTheEffectiveAddress` runs 12 hand-built vectors — 6 arms × 2 directions,
  none of them in either corpus — through `text.EncodeModule` → `DecodeModule` → `Instantiate` → `Invoke`,
  not against `checkAlign`. Going through the front end is the point: *a control can test the helper, not
  the path*, and the earlier version of this test called the predicate directly, so it would have kept
  passing if `execFE` stopped calling it or dropped the offset at one site.
  - It does **not** prove the reading correct, and the ruling says so first: *"we'd be asserting our own
    reading against our own decoder."* What it buys is that a future change to the base flips a named test
    instead of silently altering behaviour where no vector looks.
  - Its falsification is measured per direction, because a bidirectional claim should be counted rather
    than asserted: reverting to the dynamic reading fails **12 of 12** rows; trapping unconditionally fails
    **6**, all of them the `addr=3` direction; never trapping fails **6**, all of them `addr=4`. No
    degenerate engine satisfies it.
- The 0xFE region's header now names the prose as its authority for this one rule and `eval.ml` for the
  other 66, and says which is which. *A comment asserting the property the code lacks makes review confirm
  the bug*, so the divergence is stated where the divergence is.
- Nothing about the static `align=` immediate changes. That is a third rule sharing the word — the
  validator's equality check, `1 lsl align = size` (#538, #537) — and it is not touched here.
- **Whether the core pin should move**, giving a genuinely third authority, is #548 rather than settled here.
