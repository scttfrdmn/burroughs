<!-- Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0 -->

# Laws — Decisions and the thesis

How a design choice is made, and what it must be argued toward.

Relocated from `CLAUDE.md`'s `## Disciplines` section, **verbatim**, when that file
became an index (see the restructure PR). Each law's one-line compressed form remains in
`CLAUDE.md` as its recall key and points here for the specimen, the minting record, and the
token it was granted on. Nothing was rewritten in the move: the bodies below are the text as
it stood, which is why superseded wordings still appear inside them where a later ruling
amended rather than replaced.

`CLAUDE.md`'s recall key and each heading here are checked equal by
`TestEveryLawIsIndexed` (`internal/testenv`), so the two cannot drift.

### Decisions serve the thesis directionally, or they are not this project's decisions to make.

- **Decisions serve the thesis directionally, or they are not this project's decisions
  to make.** The project has a **central core and thesis** — contract §0: a
  *language-directed engine* whose host contract, fast paths, and API surface are
  designed to the specification of the Go runtime, **correctness-neutral and
  performance-partisan**, with §1's non-goals naming what is deliberately given up
  (peak throughput parity, browser embedding, non-Go ergonomics, v0 hardening). Every
  option in an ADR is argued toward that, and the question a decision must answer is
  *"which option moves the engine toward being Go's engine?"* — not "which is more
  elegant", "more general", or "more like other runtimes".
  - **The paradigm is 0002, and it is worth copying exactly.** The side table did not
    lose on aesthetics; it lost because its win served *many short-lived modules*,
    a workload **§1 disclaims** — Go guests are megabytes that load once and run for
    hours, so rewrite's build cost amortizes to nothing on the thesis workload. The
    option died *on the thesis*, and that is the intended way for a design to die here.
  - **Generality without a Go-shaped consumer is a non-goal wearing a virtue's
    clothes.** Burroughs is allowed to be narrow — narrowness is the whole design
    philosophy, the B5000 lesson. So "but another guest language might want X" is not
    an argument in this repo unless X is spec conformance, which is
    non-negotiable for a *different* reason (correctness-neutral, §9 enforces it).
  - **State the direction, not just the choice.** An ADR names which thesis clause or
    non-goal the decision advances, and a decision that cannot cite one is a signal the
    question belongs upstream with Scott — or is not this project's question. Directional
    silence is how a general-purpose runtime gets built by accident, one locally
    reasonable choice at a time.
  - **No host-linking at v0, because the oracle never asks for it.** Doctrine, and it is a
    *measured* negative rather than a deferral by taste: contract §3's host-import
    machinery would answer **521** of the board's 3401 fails, and every one of those is an
    import satisfied by *another module in the same script* or by `spectest` — not by a Go
    host function. The suite has no vector that imports from an embedder, because a
    conformance corpus cannot: it would have to specify the embedder. So the §3 API surface
    that the thesis makes this engine's most Go-shaped feature is the one thing the oracle
    is structurally incapable of scoring, and building it at v0 would be design in the
    load-bearing spot with no witness (0006). What the 521 actually want is a *script-level
    module registry* — `register`, and `(invoke $M …)` — which is harness work with an ADR
    of its own, waiting for its consumer. State the negative when a recon returns one: an
    unrecorded "we looked and there was nothing there" gets re-looked-for. (Ruling: Scott,
    on the linking-frontier recon, #157.)
    - **The 521-of-3401 pair is era-stamped, and the ruling is what survives re-measurement.**
      Re-measured on 0017 against the current board: **605 of 1699**, four mechanisms, residual
      zero — the absolute rose because interpreter arms landed and the denominator fell because
      #158 drained 4876, so *both* figures moved and neither is the one quoted here. What did not
      move is the load-bearing negative: still zero vectors needing a host-supplied import, now
      confirmed by a second instrument (874 import sites, 678 same-file `register` + 174
      `spectest` + 22 `assert_invalid`). A doctrine whose quantities rot while its ruling holds
      gets its quantities dated rather than deleted, per the second-order-honesty rule — quoting
      521 today would be a number nobody re-ran. The ADR is **0017**; its consumer knocked twice
      (#161's frontier and the 105 reclassified §3 slots), which is what took it off "waiting".

### Decision-before-code.

- **Decision-before-code.** Design choices get `docs/decisions/NNNN-*.md`
  (context, options, choice, consequences) *before* implementation.
  Decisions Scott must make are flagged in reports, not made for him.
  Its counterweight is the product rule above: a decision doc is *not* product work
  either, so **one ADR earns one implementation**, and an ADR whose implementation has
  not started is a reason to write code rather than another ADR. Deliberation lives in
  the issue; the ADR is the tombstone.

### A status field is a citation to an approval, and approvals are artifacts with provenance.

- **A status field is a citation to an approval, and approvals are artifacts with provenance.** So
  an ADR's `Status:` is held open until a stamp exists to point at, and *an ADR marked accepted on
  a stamp nobody gave is a fabricated citation about the project's own governance* — worse than a
  wrong option, since a wrong option is arguable and a forged provenance is not. The actor states
  the case and flags it; a principal's stamp is what closes it, and then the record keeps **both**
  the stamp and the interval it spent open. This is the cite-issues-not-PRs discipline pointed at
  the project's own decisions rather than at its code: same defect (a citation nobody can resolve
  to the thing it claims), same remedy (name the artifact, not the intention). Doctrine by
  demonstration — 0016 sat `proposed` through the PR that implemented it. (Ruling: Scott, PR #142.)
