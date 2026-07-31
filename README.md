# Burroughs

**The Wasm engine for Go.** · burroughs.run

> The B5000 favored ALGOL. Burroughs favors Go.

Burroughs is a language-directed WebAssembly runtime, written in pure Go,
whose host contract is designed to the specification of the Go runtime:
real OS threads, engine-native preemption and stop-the-world, a defined
memory model at every host boundary, growable continuation stacks, and a
netpoller-shaped WASI 0.3 event loop. Any spec-conforming guest runs
correctly here — the upstream test suite is the neutrality guarantee — but
where a design tradeoff exists, Go wins. Other guests have alternatives.

The name is the lineage: the Burroughs Large Systems (B5000, 1961) were
stack machines built so that one high-level language would win —
language-directed design, sixty-five years before this repo. Wasm is a
stack machine. The gopher gets its burrows back, one letter askew.

- **`docs/burroughs-contract-v0.1.md`** — the normative host contract.
  Start there.
- **`docs/decisions/`** — accepted decision records (ADRs).
- **`CLAUDE.md`** — implementation agent brief, phase ladder, disciplines,
  reporting protocol.

Project state lives in **GitHub issues and milestones**, not in files:
milestones are the phase ladder, and `label:type:grave` is the graveyard of
bugs found and lessons learned.

Status: v0 (interpreter phase). `make all` builds the CLI and runs the
tests; `make spec-tests` vendors the upstream spec suite (the oracle).

## License

Apache 2.0 — see `LICENSE` and `NOTICE`. © 2026 Scott Friedman.
