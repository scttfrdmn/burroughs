<!-- Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0 -->

# Laws — Gates

What a gate may and may not do to the grammar.

Relocated from `CLAUDE.md`'s `## Disciplines` section, **verbatim**, when that file
became an index (see the restructure PR). Each law's one-line compressed form remains in
`CLAUDE.md` as its recall key and points here for the specimen, the minting record, and the
token it was granted on. Nothing was rewritten in the move: the bodies below are the text as
it stood, which is why superseded wordings still appear inside them where a later ruling
amended rather than replaced.

`CLAUDE.md`'s recall key and each heading here are checked equal by
`TestEveryLawIsIndexed` (`internal/testenv`), so the two cannot drift.

### Gates.

- **Gates.** Proposals land behind build tags / config gates; acceptance is
  the proposal's own suite green (contract §9). Nothing defaults on
  without it.
  - **A flip is never in the mechanism's PR — it is its own stamp-tier event.** Mechanism is
    product and self-merges on a bound green; a flip is **governance** and holds for a principal's
    stamp, so they are separate artifacts with separate verdicts. The SIMD flip (#227/#233) is not
    a precedent to cite selectively but **the procedure**: G-1 measured on the proposal's suite
    *after* the mechanism exists, forecast **pre-registered**, rollback stated, one-line diff.
    The practical reason is the one that makes it structural rather than stylistic — *you cannot
    pre-register a forecast inside the PR that creates the numbers*, which is the actor choosing
    the instrument that judges the actor, one level up from the ratio. So a mechanism PR that
    would "also flip while we're here" is two verdicts wearing one green. (Ruling: Scott, PR #252,
    on 0026's flagged scheduling question; the answer was **no**, generalized to every gate.)

### Gates never manufacture malformedness.

- **Gates never manufacture malformedness.** *Malformed* is the spec's word: it
  belongs to the grammar, and the grammar here is the **union of the tracked
  set** (§9 G-2) — section id 13 is defined by Wasm 3.0 and so is well-formed,
  while ≥14 is malformed because nothing tracked defines it. Gates partition
  *acceptance* within that grammar; they do not redraw it. A gate-off engine
  meeting a gated feature must still **reject** the module — accept-and-ignore
  silently breaks semantics — but it reports a **feature-named error**
  (*exception handling: gate disabled*), never a spoofed spec string. Reporting
  "malformed" for a module the spec calls well-formed lies about the module to
  conceal the engine's own configuration. So the structural layer (id range,
  order, uniqueness) stays gate-blind, and the features set governs per-section
  and per-opcode acceptance. (Ruling: Scott, #5; queued as a contract amendment
  in #16.)
