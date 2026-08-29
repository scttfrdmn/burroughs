<!-- Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0 -->

# Laws — Decisions and the thesis

How a design choice is made, and what it must be argued toward.

Relocated from `CLAUDE.md`'s `## Disciplines` section, **verbatim**, when that file
became an index (see the restructure PR). Nothing was rewritten in the move: the bodies below
are the text as it stood, which is why superseded wordings still appear inside them where a
later ruling amended rather than replaced. The per-law recall keys `CLAUDE.md` carried were
retired with the index economy when that file became a brief and a pointer page (Scott's
directive, the four-workstream brief of 2026-08-17); the laws themselves were not touched.

`CLAUDE.md` links this family, and the two halves of that link are checked:
`TestMarkdownLinksResolve` (`internal/testenv`) that every pointer in every markdown file in the
tree resolves, and
`TestLawFamiliesAreReachable` that every family here is reachable from it — a law nobody can
reach is a law out of context.

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

  - **The stamp's subject is the capability and the constraint, never the signature.** The law above
    is about not claiming a stamp nobody gave; its mirror is asking for one on a question that was
    already yours. Scott's standing form: *"I'll name the capability and the constraint, and the
    signature is yours."* So an exported name, a parameter order, an error's wording are the actor's
    to settle and to *state* — while whether a capability exists at all, and under what constraint,
    is a principal's. Routing a signature upward reads as diligence and is the same instrument
    misused in the other direction: it spends the one channel whose scarcity is a principal's
    attention on a decision the record can already answer, and it buries the questions that do need
    ruling among ones that do not. Flag what needs ruling; decide what needs deciding.
    (Standing correction: Scott, PR #331.)

  - **A stamp is only as good as the description it was given on**, which is this law read from
    the requester's end. When a principal approves a term the actor could not have delivered, the
    defect is upstream of the approval and sits in the description — and a principal who notices
    it says so in those words: *"since I gave the term on your description, I carry it."* So the
    check before asking for a stamp is not only "is this a principal's question" but "is the term
    I am asking to be held to satisfiable by the slice I am asking about." Full body and its
    specimen — a pre-registered term about an error's *rendered* text, owned by a wrapper in
    another slice's file — at
    [a message is not its rendering](errors-and-testimony.md#a-message-is-not-its-rendering-and-a-term-about-the-rendering-cannot-be-discharged-by-the-messages-author).
    (Ruling: Scott, PR #490 review.)

  - **Durability is not independence, and the mechanism is reviewability.** *"A durable record of an
    order, written by the party acting on it, is durable but not independent provenance no matter how
    good the channel."* **Independence comes from the record being reviewable by someone other than
    its author, not from the record being permanent** — stated in the headline rather than left to the
    exception below it, because the original phrasing left the mechanism implicit and a reader who
    stopped at *durable but not independent* could only conclude that no self-written record is ever
    citable, which is false and would forbid relaying a stamp at all. Permanence is a property of the
    channel; falsifiability is a property of who reads it next. (Sharpening: chat-Claude, on the #519
    review — *"that's the actual mechanism, and my original phrasing left it implicit."*) So an order
    that arrived in session does not become citable by being written down: transcribing it into an issue
    comment produces a permanent, timestamped, public artifact, and the artifact's author is still the
    actor. Pointing a `Ratio-Class: ordered` at it cites yourself. The classification is therefore
    **`carried`**, with the order quoted in prose where a reader takes it as testimony rather than
    counting it as a citation — and the reason to state the rule this way is that it is **decidable
    from who wrote the record**, not case-by-case from how good the channel was. A better channel is
    the tempting wrong answer here: it improves durability, which was never the missing property.
    - **This does not conflict with relaying a stamp into an ADR's `Status:`**, and the difference is
      the sentence above's own test rather than an exception. A relay is *reviewed afterward* — the
      principal reads the report that carries it and can say the stamp was never given — and **that
      review is what supplies the independence** a self-written record lacks. So the two rules have
      one shape: provenance is independent when someone other than the actor can falsify it. A relay
      is falsifiable by its reader; a transcript cited as its own authority is not.
    - **Specimen: PR #502's ratio trailer.** The probe was genuinely ordered (Scott, on the #499
      reconciliation — *"take (c) first, as a measurement"*), and the nearest artifact was
      [#499's rulings comment](https://github.com/scttfrdmn/burroughs/issues/499#issuecomment-5384079699),
      written by the actor to record Scott's words. Classed `carried`. Sibling of *an in-session order
      has no citation*, which said what not to do; this one says why, and generalizes past the
      one-off. (Ruling: Scott, on the #502 review.)

### An amendment that changes what a stamped decision commits to carries its own dated stamp.

- **An amendment that changes what a stamped decision commits to carries its own dated stamp; an
  amendment that records a falsification or repairs a citation does not.** The corollary of the law
  above, pointed at the *second* thing written into an ADR. An accepted ADR is a citation to an
  approval, and appending to it silently widens what that one approval is claimed to cover — so the
  question is not "is this an edit or an append" (0003's precedent settles that: append) but **is the
  approval still about this**. The test, and it is a single sentence because it has to be applied by
  whoever is holding the pen: **would someone acting on the ADR now do something different?**
  (Ruling: Scott, on the #518 review — *"#512: yes, it needs its own stamp."*)

  - **Qualifies — needs its own stamp.** ADR 0007's 2026-08-28 amendment: the pin set goes plural.
    *"Plural pins means maintaining two and drift-checking two revisions — that qualifies."* An
    implementer reading it now maintains two fetch scripts, drift-checks two revisions, and consults
    the authorities **clause-scoped** rather than globally. The 2026-07-31 stamp was given on a
    one-authority world and cannot be stretched over that.
  - **Does not — no second stamp.** ADR 0002's falsification amendment: a prediction the ADR made came
    out false and the amendment says so. Nothing an actor does changes. Same for a premise correction,
    a citation re-point, a scored forecast, and a pointer to a superseding ADR.

  **The test is two-part, because the first part alone over-triggers.** *Would an actor do something
  different* is satisfied by any changed mechanism, including one that was never a principal's to
  change — 0010's `fail`-column partition (`binaryFail` ceiling 1 / `textFail` ceiling 600) changes
  what an actor does and owes no stamp, because the shape of a harness-internal ceiling is a signature
  and not a capability. So both questions must answer yes: **did the commitment change, and was that
  commitment a principal's to make?** The second is *the stamp's subject is the capability and the
  constraint, never the signature* read at amendment time, and routing a self-decided mechanism change
  upward for a stamp is that law's other failure rather than this one's compliance. (Filter proposed by
  the actor on PR #519 and accepted: *"my rule as stated would have demanded stamps on amendments
  changing mechanical facts no principal ever stamped — 0010's fail-column partition is the right
  counterexample."* chat-Claude, on the #519 review.)

  Two mechanics follow, and both are about where the reader looks:

  - **The stamp goes in the `Status:` line with its scope named, not only in the amendment section.**
    A reader who checks `Status:`, sees **accepted**, and stops has been told the whole ADR is covered
    by one stamp. So `Status:` carries both stamps and says which half of the document each one is
    about — 0007's now names *the number of pinned authorities and the consultation rule over them* as
    the amendment's scope, and states in those words that the earlier stamp does not cover it. This is
    the *foreclosing-words* shape from `CLAUDE.md`'s phase ladder: a sentence written before a change,
    left standing after it, telling the next reader the tree is in a state it is not.
  - **The best shape is not an amendment at all: it is a new ADR with its own stamp, and a pointer.**
    ADR 0025's G-1 retirement condition changed, and what carries the change is **0043**, stamped by
    Scott on the #482 review, with 0025's `Status:` pointing at it (*superseded in part*). That needs
    no amendment stamp because the amendment is not where the commitment lives. When a changed
    commitment is large enough to argue about, it wants an ADR; when it is a clarification an actor
    would act on, it wants a stamped amendment; when it is a fact about the past, it wants neither.

  **What this test cannot see, stated because the audit that applied it hit the limit.** The
  population of amendments is derived by heading shape — a `## ` heading carrying a date or an
  amendment word — so **an appended section whose heading carries neither is invisible to the sweep**,
  and it is exactly the section most likely to have skipped the question. *Derive the domain, never
  enumerate it* buys nothing when the domain is derived from a convention the writer can decline to
  follow; the residual risk is named here rather than converted into a control that would read as
  coverage.

### A retirement condition that names an issue rather than a state of the code retires on a bookkeeping event.

- **A retirement condition that names an issue rather than a state of the code retires on a
  bookkeeping event.** The sibling of the law above, pointed the other way down the timeline. A
  `Status:` is a citation *backwards* to an approval, and its failure is a stamp nobody gave. A
  retirement condition — a carve-out that self-retires, a debt discharged by a tripwire, a deferral
  with an end — is a citation *forwards* to a state, and its failure is quieter: the condition is
  satisfied on schedule by an artifact that stood in for the state and was never the state. Nobody
  forged anything. Someone closed an issue.
  - **The specimen is G-1's own carve-out.** ADR 0025's clause read *"retires itself when #9 lands
    — no second amendment repeals it, because `ErrNotValidated`'s call sites become unreachable."*
    Both halves are in that sentence: a claim about the code (*call sites become unreachable*) and a
    tracker event standing in for it (*when #9 lands*). #9 is an umbrella whose work landed in
    slices, so closing it is correct bookkeeping the day its residue is re-pointed — and on that day
    the sentinel had **68 call sites across 16 files**, every one of them able to produce exactly
    the population the carve-out excepts. The contract would have read retired with its subject
    untouched. Amended by Scott's stamp on the #482 review to name the code state (ADR
    [0043](../decisions/0043-g-1s-carve-out-retires-on-zero-call-sites-not-on-the-validator-umbrellas-closure.md)).
  - **The tell is grammatical: the condition's subject is an artifact rather than the tree.** "When
    #N lands", "once the port is done", "after the migration", "when the harness supports it" — all
    name events in a schedule. A state of the code is a predicate someone can evaluate on a checkout
    with no tracker access: *this identifier has no call sites*, *this field is gone*, *this arm
    returns a verdict*. Prefer the condition three mechanisms can check (grep, `deadcode`, the
    compiler) over the one that needs a person to have kept a promise.
  - **A condition an instrument can satisfy by breaking is the same defect one level in.** The
    rejected alternative in 0043 was "retires when no vector is attributed to the sentinel", which
    is observable on the board and reads sharper than a grep. Its zero has two causes: the
    population emptying, or the classifier losing the ability to attribute. A board going quiet must
    never discharge anything, which is why the amendment names the emptied-subject case explicitly
    as **inert, not retired** — inert is a fact about a measurement's moment, retirement a fact
    about the tree.
  - **A diagnosis in a doc comment is not an action.** `sections.go` had recorded the whole finding
    — *"inert, not retired — its retirement condition is #9 landing, and `ErrNotValidated` still has
    call sites throughout `internal/interp`"* — for a full PR cycle before anything moved. Prose that
    names a defect and files nothing leaves the defect standing with a witness beside it. Where the
    condition is normative text the remedy is an amendment and a stamp; where it is a tripwire the
    remedy is re-pointing the tripwire, because a test whose documented failure condition is a
    tracker event stays green on the day its subject changes and reads as a confirmation.
  - **And it is stated over the *convertible* population, not the total.** A condition can name a
    state of the code and still be unachievable, because part of the population cannot reach the
    state at all. [#497](https://github.com/scttfrdmn/burroughs/issues/497) asks whether the
    citation-form census's pins are permanent; any option leaving one owes a retirement condition,
    and the obvious phrasing — *retires when the positional-citation count reaches zero* — is
    arithmetic nonsense the moment a subset is **positional by construction**. An ADR's *"cited as"*
    column is the specimen: there the coordinate **is** the datum, a record of what the ADR said,
    and converting it to a symbol destroys the thing being recorded. A zero over the total is
    therefore not a state the tree can be put into, so the condition would sit in the tree forever
    reading as live. State it over the convertible remainder and write the by-construction floor
    down, because a floor nobody has recorded is indistinguishable from a debt nobody has paid.
    (Ruling: Scott, on the #502 review — *"any option leaving a permanent pin must name a retirement
    condition that names a state of the code — and because of the by-construction class, that
    condition has to be stated over the convertible population, not the total. A criterion defined
    over the total is unachievable by construction, which is exactly what you found."* The class was
    found by the census refusing a correct action, which is the third time that pin has fired on its
    own author and the first time the firing was not a mistake — *[a control catching its own author
    at the moment of authorship is the strongest evidence it aims
    right](citations.md#a-control-catching-its-own-author-at-the-moment-of-authorship-is-the-strongest-evidence-it-aims-right)*.)
  (Ruling and minting: Scott, on the #482 review — *"closing [the validator umbrella] would satisfy
  G-1 by the letter while `ErrNotValidated` still has call sites throughout `internal/interp`. Mint
  the law; it will recur."* The bracket is an editorial substitution for the issue's number:
  `citecheck.sh`'s closure-claim check reads the pairing as this diff asserting it closed the issue,
  and a modal *after* the verb does not reach the conditional exemption. Third time a principal's
  verbatim words have collided with a guard aimed at the actor's — the alteration is bracketed
  rather than silent every time.)
