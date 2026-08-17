<!-- Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0 -->

# Laws — Gates

What a gate may and may not do to the grammar.

Relocated from `CLAUDE.md`'s `## Disciplines` section, **verbatim**, when that file
became an index (see the restructure PR). Nothing was rewritten in the move: the bodies below
are the text as it stood, which is why superseded wordings still appear inside them where a
later ruling amended rather than replaced. The per-law recall keys `CLAUDE.md` carried were
retired with the index economy when that file became a brief and a pointer page (Scott's
directive, the four-workstream brief of 2026-08-17); the laws themselves were not touched.

`CLAUDE.md` links this family, and the two halves of that link are checked:
`TestClaudeMDLinksResolve` (`internal/testenv`) that every pointer on the page resolves, and
`TestLawFamiliesAreReachable` that every family here is reachable from it — a law nobody can
reach is a law out of context.

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
    - **Every forecast row is marked P or D, a D row carries its derivation inline, and a row
      entered as P that was derivable is a miss recorded like any other.** The instrument note
      belongs to the *forecast*, so it is sited here rather than in `CLAUDE.md`: the governance
      question — may this PR flip — is answered above, and this is how the pre-registration is
      written once the answer is yes. **P** is a prediction: a number that cannot be computed
      before the diff exists, so being wrong about it costs only accuracy. **D** is a
      derivation: a number that *can* be computed from what is already measured, so being wrong
      about it is an arithmetic error, and the derivation is written beside the row for exactly
      that reason. The distinction exists because an unmarked forecast lets a derivable figure
      be quoted with a prediction's tolerance — a P mark on a D row is a hedge bought for free,
      and it is the forecast's own version of *the actor never chooses the instrument that
      judges the actor*. The relaxed-SIMD flip (#285) forecast **every row D**, derivations
      inline, and reconciled exactly; the residual 77-against-69 gap was resolved by
      *deriving* the eight extra passes (module-definition commands, scored on the text
      reader's answer per #124) rather than by subtracting totals, which is the same rule
      applied to a discrepancy instead of to a prediction. (Ruling: Scott, PR #285.)

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
