# 0001 — Project genesis

Date: 2026-07-30 · Status: accepted (Scott, via chat)

## Context
Burroughs exists to be the Wasm engine for Go: language-directed
(B5000 precedent), correctness-neutral / performance-partisan, tracking
the spec edge (Wasm 3.0, threads, stack switching, WASI 0.3). Chat
thread with chat-Claude produced the name, the positioning, and the
host contract before any code — decision-before-code applies to the
whole project.

## Decisions recorded
1. **Name: Burroughs.** Domain burroughs.run confirmed available.
2. **Contract v0.1 adopted as law** (docs/burroughs-contract-v0.1.md).
   Normative text changes require Scott's sign-off.
3. **Module path: github.com/scttfrdmn/burroughs** for now; vanity
   `burroughs.run/...` import path deferred until the domain serves
   go-import meta tags.
4. **Pure Go, no cgo.** Go ≥ 1.26.
5. **Genesis commit is Scott's, signed** — the scaffold is handed over
   uncommitted so the history starts with his key.

## Resolved after drafting (Scott, 2026-07-30)
- **License: Apache 2.0**, © 2026 Scott Friedman. `LICENSE` carries the
  verbatim upstream text; the copyright notice lives in `NOTICE` per
  Apache 2.0 §4(d). In the genesis commit, as intended.
- **0002: interpreter strategy** — accepted (internal-form rewrite, giant
  switch, `uint64` slots + parallel reference array).
- **0003: spec harness + error contract** — accepted (staged pure-Go
  harness, substring matching).
- **Tracking moved to GitHub** — milestones as the phase ladder, issues
  replacing queues, PR descriptions as session reports. `PROGRESS.md` and
  `docs/reports/` retired; see CLAUDE.md.
