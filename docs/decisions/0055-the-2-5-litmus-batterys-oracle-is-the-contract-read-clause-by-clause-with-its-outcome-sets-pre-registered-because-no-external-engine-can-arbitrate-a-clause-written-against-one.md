# 0055 — The §§2–5 litmus battery's oracle is the contract read clause-by-clause with its outcome sets pre-registered, because no external engine can arbitrate a clause written against one

Date: 2026-08-31 · Status: **accepted** — Scott's ruling on
[#399](https://github.com/scttfrdmn/burroughs/issues/399), comment
[5332223717](https://github.com/scttfrdmn/burroughs/issues/399#issuecomment-5332223717), 2026-08-18,
which is the stamp this field cites. The scheduling half — that the tables land *now*, ahead of the
mechanism — is Scott's order on the #569 review, recorded by the actor who was ordered, so **the
commits in this slice are `Ratio-Class: carried`**: a durable record written by the actor cites the
actor, and *durability is not independence*.

Filed against **#10**. One ADR, one implementation: the implementation is
[`docs/litmus-battery-preregistration.md`](../litmus-battery-preregistration.md) plus the single
control that checks it against the contract.

## Context

Contract §4's B-MM-5 requires the conformance suite to carry a litmus battery for the boundary edges,
run on both a TSO and a weakly-ordered platform. #10 is that battery. It could not begin, because a
litmus battery needs an oracle and every other suite in this tree gets one for free: the upstream spec
suite is material we did not author, which is what contract §0's neutrality guarantee *is*. §§2–5 has
no upstream. It is Burroughs' own contract, and it exists precisely because the mainstream engines
disagree with it.

Three candidate oracles were put to Scott on #399:

- **(a) The contract itself**, read clause by clause, with allowed-outcome sets authored by hand.
- **(b) A differential** against V8 or wasmtime: run the same litmus test on another engine and compare.
- **(c) The architectures**, running each case on amd64 and arm64 and treating a disagreement as the
  signal.

## Decision

**(a) with (c) promoted from a recommendation to a requirement, and (b) refused.**

1. **The specification is contract §§2–5, cited by clause.** Every case in the battery names the clause
   it discharges, and quotes it verbatim beside its outcome set, so a reader can check the reading rather
   than trusting it.

2. **Allowed-outcome sets are authored and pre-registered before the implementation exists**, and **the
   PR that lands a mechanism may not edit them.** Changing an outcome set is its own PR, stating what was
   wrong about the clause reading. A forecast rewritten by the run it forecasts is not a forecast — the
   same shape as amending a threshold having seen the number, which this project has already paid for.

3. **arm64 versus amd64 is the external arbiter**, not merely a nice-to-have. This is a promotion over
   what was recommended, and Scott's grounds are the promotion's whole content: *a reordering the model
   forbids but the machine performs shows on one architecture and not the other.* x86-TSO structurally
   cannot exhibit most of the store-visibility reorderings §4 forbids, so a case that passes on amd64 has
   in many instances tested nothing at all. The battery therefore records, per case, **which
   architecture is expected to discriminate** — and where the honest answer is *neither, this is a
   scheduling claim*, it says that instead of inventing an arbiter.

4. **`-race` is the host half.** The guest-side outcome tuple cannot see a host-side data race in the
   engine's own state, and the engine is the thing under test as much as the guest is.

5. **(b) is refused.** §4's entire provenance is D20: the browser host establishes happens-before for the
   notified word only, and a sibling field's store can lag the woken agent's resume. That engine is the
   contract's known non-conformer. A differential oracle would ratify the defect §4 exists to forbid,
   which is the wrong-oracle shape in its most expensive form — the disagreement would be scored as this
   engine's bug.

6. **The caveat goes at the top of the battery, not the bottom.** In these words: *a green here means
   agreement with an oracle this project wrote, which is weaker than every green before it.* A reader who
   reaches the caveat after the tables has already formed the belief it corrects.

## The structural constraint on how the tables are represented

Scott's order on the #569 review, which is what this slice implements:

> The tables must not be readable as a passing suite. If they're data, there's no green to misread. If
> they're tests, they fail or skip loudly naming what's missing — never pass vacuously. That's the
> structural-zero family, and it's the only way the "not a control" claim stays true in the tree rather
> than just in the file's preamble.

So the tables are **markdown data** at `docs/litmus-battery-preregistration.md`. Nothing in them runs.
The one green in their neighbourhood belongs to
`TestEveryClauseInSectionsTwoThroughFiveIsPreregistered`, whose subject is this document's agreement
with the contract — a coverage-and-citation claim — and never the engine's conformance. That control:

- checks **both directions** against `contractClauses` restricted to §§2–5, so a clause with no entry
  fails and an entry naming no clause fails;
- checks each entry's quotation is a contiguous substring of the clause's own contract text, so an
  amendment breaks this file rather than letting it drift from what it claims to quote;
- requires the per-case keys, so a case cannot omit its forbidden set or its floor; and
- carries an **inverse tripwire**: a case whose status becomes `implemented — TestName` must name a test
  that resolves in `citationInventory`. Discharging #554 or #543 therefore turns into a failing test
  naming the cases to write, rather than into a document that quietly still reads `blocked`.

The control is watched die by injection before it is trusted, because *a re-pointed control has not been
watched die*.

## Why the tables land before the mechanism, and not after

The worry raised against landing them now was `controls.md`'s stillborn-control family: an instrument
that can never fire. Scott ruled it a category error, and the distinction is the reason this ADR exists
at this point in the ladder rather than later:

> `controls.md`'s failure mode is a *control* that can never fire — a green that means nothing. A
> pre-registration asserts nothing about today's engine; its entire value is being un-editable by the PR
> that satisfies it. Landing after #554 destroys precisely that, since the tables would then be written
> by someone who has seen the mechanism.

## Consequences

- **Every case in the battery is blocked, and the sequencing is stated in the document.** Four steps:
  the tables (this slice), then spawn ([#554](https://github.com/scttfrdmn/burroughs/pull/554)), then
  suspend and wake ([#543](https://github.com/scttfrdmn/burroughs/issues/543)), then the battery. That
  **#543 is a second blocker distinct from #554** is flagged now rather than discovered later: two agents
  can race over plain memory without either suspending, so spawn unblocks B-MM-1 and H-1 while B-MM-2 —
  the sibling-field-after-wake case §4 names by hand — stays blocked behind wait's suspend path.

- **A caseless clause is visible rather than absent.** All seventeen clauses of §§2–5 get an entry,
  including the four that are structural (SP-3, B-MM-3, B-MM-4, H-2), the one that is a suite property
  (B-MM-5), the cost claim (T-4), and the two deferred to §10 open questions (T-5, H-3's MAY half). Each
  says why no outcome tuple can reach it and what discharges it instead. A clause with no case and no
  entry would read as covered.

- **Coverage is fixed at aligned accesses, and unaligned is named as uncovered** — the population ADR
  0054's mechanism makes atomic. Written into the document rather than left for a reader to infer from a
  green.

- **The Go-runtime torture set is not in this battery.** Its oracle is Go's runtime behaviour rather than
  this contract, which is the split-at-the-oracle-seam shape, so it is
  [#406](https://github.com/scttfrdmn/burroughs/issues/406).

- **A verdict from this battery is a falsifier, never a certificate.** An observed forbidden outcome
  refutes conformance; a clean run bounds nothing about the interleavings that did not occur. Every
  verdict is spelled *"not observed in N runs on both architectures"* and never *"conforms"* — which is
  also why every case carries a **floor**: a case that cannot say how often it reached its interleaving
  cannot distinguish *conforming* from *never raced*.

## Alternatives considered

- **Author the tables after #554 lands**, when the mechanism is known and the cases are easier to write
  correctly. Refused above: the ease is the defect.
- **A differential against wasmtime only** (not V8), on the theory that wasmtime is not the known
  non-conformer. Refused with (b): the argument would have to be that wasmtime conforms to a contract it
  has never read, and any disagreement would still be unattributable — nothing in the comparison says
  which engine is wrong.
- **One issue for the whole battery.** Already split: #406 took the Go-runtime torture set out on the
  oracle seam, and this ADR takes the four-step sequencing out of the issue body and into the tree.
