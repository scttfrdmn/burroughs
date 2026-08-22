# 0043 — G-1's carve-out retires on zero call sites, not on the validator umbrella's closure

Date: 2026-08-21 · Status: **accepted** — stamped by Scott on the #482 review, relayed to
[a durable comment](https://github.com/scttfrdmn/burroughs/issues/483#issuecomment-5377714094).
Deliberation: [#483](https://github.com/scttfrdmn/burroughs/issues/483).

> **Never held at `proposed`, and the reason is that the stamp preceded the ADR.** The order was
> given in the imperative — *"amend G-1 to name a state of the code first, then close [the
> validator issue]"* — so the approval existed before a word of this file did, and the interval
> this would otherwise have spent open is zero rather than unrecorded. §§0–9 of the contract are
> editable only on Scott's explicit sign-off (`CLAUDE.md`); the relay comment is that sign-off with
> a URL, which is the same remedy 0042's `Status:` needed one PR earlier.

## Context

ADR 0025 amended G-1 with a carve-out: a gate's acceptance may set aside vectors whose **sole
attributed blocker** is #9's deferred validator (`ErrNotValidated`), attribution by the engine's
own error taxonomy. The carve-out was written self-retiring, and this is the sentence that says so:

> *The carve-out is named to #9 specifically, not to a category of validator gaps, and retires
> itself when #9 lands — no second amendment repeals it, because `ErrNotValidated`'s call sites
> become unreachable by its own doc comment's own claim and the carve-out has nothing left to
> except.*

Every clause of that is defensible except its subject. **"When #9 lands" is a tracker event.** #9
is an umbrella issue; its closure is a state transition a person performs, and it can be performed
for good reasons that have nothing to do with the sentinel — the umbrella's work has been landing
in slices for months (#291, #389, and others), and closing it is the correct bookkeeping once its
residue is re-pointed at the slices that still hold work. The engine's side of the sentence does
not follow along: `ErrNotValidated` has **68 call sites in 16 non-test files**, all under
`internal/interp` (measured on `3a7de11`). Close the umbrella and the carve-out reads as retired
while the population it excepts is still producible on demand.

The reasoning inside the clause is where the substitution happened. It says the call sites *become
unreachable* — a claim about the code — and then attributes that state to the tracker event, citing
`ErrNotValidated`'s own doc comment, which makes the identical move (*"it says in this comment that
every one of its call sites becomes unreachable when #9 lands"*). Two artifacts, one unexamined
premise: that the umbrella's closure and the sentinel's disappearance are the same event.

The engine had already noticed. `internal/binary/sections.go:134`, re-measured during #464's
reconciliation, records the carve-out as **inert, not retired** — *"its retirement condition is #9
landing, and `ErrNotValidated` still has call sites throughout `internal/interp`."* That is this
ADR's finding, written down and acted on by nothing, which is its own small lesson about where a
diagnosis has to go to become work.

## Options

1. **Zero call sites.** The carve-out retires when `ErrNotValidated` has no reachable call site in
   the engine. This is the state the sentinel's doc comment already promises, and it is checkable
   three independent ways: `grep`, `deadcode` (already in `make check`), and the compiler itself
   once the declaration goes. It is orthogonal to the umbrella in both directions — #9 may close
   with the sentinel in place, and the sentinel may go before the umbrella does.
2. **Empty attributed population.** The carve-out retires when no vector in any gate's suite is
   attributed to `ErrNotValidated`, by the same error taxonomy G-1 already requires for the
   attribution half of the clause. Observable directly on the board, which is its appeal.
   **Its defect is that a zero has two causes.** The attributed count also goes to zero when the
   harness loses the ability to attribute — a classifier arm that stops firing, a lane that stops
   being run, a taxonomy renamed. A retirement condition that a broken instrument can satisfy is
   the shape this whole ADR exists to remove, one level in.
3. **Both, conjunctive.** Sound, because option 1 is sound and a conjunction cannot be weaker than
   its strongest term. It buys nothing: the added term is the one that cannot be trusted alone, so
   the conjunction's whole load is carried by 1 while the text implies 2 is load-bearing.

## Decision

**Option 1.** G-1's retirement condition names a state of the code: the carve-out retires when
`ErrNotValidated` has no reachable call site in the engine. Two further clauses go in the
amendment, because both are things a later reader would otherwise re-derive wrongly:

- **The board emptying is not the discharge.** Option 2's condition is named in the contract text
  as explicitly *not* the retirement condition. The carve-out's subject being empty in some suite
  makes it **inert in that suite**, a fact about a measurement's moment; `sections.go` already
  distinguishes inert from retired and the contract should say the same word.
- **The umbrella's issue state is evidence at best.** #9 closing is consistent with the condition
  and does not establish it. This is the substitution the amendment exists to undo, so leaving the
  tracker artifact unmentioned would invite the next reader to re-supply it.

What the amendment does **not** touch is the excepted population: same vectors, same attribution
rule, same requirement that the gate's own suite carry zero required-engine-execution defects once
the population is set aside. Only the sentence saying when the clause stops is replaced. G-1's
*"no second amendment repeals it"* survives intact and is now true for a better reason — the
carve-out is not being repealed, and the condition that retires it can no longer be satisfied by
anyone's bookkeeping.

## The law this mints

**A retirement condition that names an issue rather than a state of the code retires on a
bookkeeping event.** Minted on Scott's order (*"Mint the law; it will recur"*) into
[`docs/laws/decisions-and-thesis.md`](../laws/decisions-and-thesis.md), as a sibling of *a status
field is a citation to an approval*: both are governance fields whose referent is the thing to
check. A `Status:` points backwards at an approval, and the failure is a citation to a stamp nobody
gave. A retirement condition points forwards at a state, and the failure is a citation to a state
nobody will reach — satisfied instead by the artifact that stood in for it.

## The population, and the two sites deliberately left standing

Swept the tree for conditions predicated on the bookkeeping event
(`grep -rn "#9 lands\|#9 landing"`, `*.go` and `*.md`): **13 hits across 9 files — six re-pointed
in place, five mentions in two accepted ADRs handled by a header pointer, two left standing.** The
two are stated because an unmeasured complement is not an empty one: "two examined and found sound"
is a measurement, while silence about them would read as a sweep that found nothing else.

| site | disposition |
| --- | --- |
| `docs/burroughs-contract-v0.1.md` G-1 | the amendment |
| `internal/interp/interp.go` `ErrNotValidated` doc comment | re-pointed — the authority G-1's clause cited |
| `internal/interp/interp.go` `deferred` field comment | re-pointed |
| `internal/interp/global_test.go` `TestImmutableGlobalIsNotRefusedHere` | re-pointed — a tripwire whose firing condition was the tracker event |
| `internal/testenv/foreclose_test.go` licence reason | repaired — it named "a conditional about a tracked issue" as the *benign* category |
| `internal/binary/sections.go` `DefaultFeatures` doc comment | re-pointed — the diagnosis, which this amendment makes false-of-the-tree |
| `docs/decisions/0025-*.md` (4 mentions) | superseded-in-part pointer at the header; the record is not rewritten |
| `docs/decisions/0023-*.md` | same pointer — its `stack.tracking` retirement inherits the identical shape with a different subject |
| `CHANGELOG.md` | **left** — a dated account of the condition as it stood, and a changelog is history |
| `internal/interp/interp.go` `maxFrameLocals` | **left** — examined and *not* the shape: *"when #9 lands this stays"* asserts survival, and nothing retires on it |

## Consequences

- **The carve-out's discharge becomes a grep.** Anyone can check it, on any revision, without the
  board and without the tracker. No control is added by this ADR: the condition is a code state
  three existing mechanisms already report on, and the honest reason not to build a fourth is that
  a control asserting "this sentinel still has call sites" would fail on the day the work succeeds
  — a tripwire that fires on the good event. What guards the *other* direction is the compiler.
- **Closing the validator umbrella is now safe to do, and is the next step.** That was the ordered
  sequence: amend first, then close. After this amendment, #9's closure carries no claim about
  G-1 at all, and its closing comment says so out loud rather than relying on a reader not to
  connect the two.
- **The two tombstone pointers change no accepted text.** 0025 and 0023 keep their wordings; each
  gains one line at the header pointing here. A superseded clause read through a pointer is a
  record; a rewritten clause is a record with the evidence removed.
- **0023's retirement condition is now stated in the wrong place.** Its own clause (*"when #9
  lands, this ADR's own mechanism is the thing to delete"*) is advice to the author who lands the
  validator, and the pointer added here is the whole of this ADR's remedy for it. Rewriting it
  would mean amending an accepted record about a subject this ADR did not deliberate — flagged
  rather than fixed, and it costs nothing while `stack.tracking` still exists.
- **The re-pointings are not cosmetic in one case.** `global_test.go`'s tripwire is a *test* whose
  documented failure condition was the tracker event; a reader who retired the umbrella and saw that
  test still pass would have had a false confirmation that the check had not moved. Its condition is now
  the code state that actually flips it.
