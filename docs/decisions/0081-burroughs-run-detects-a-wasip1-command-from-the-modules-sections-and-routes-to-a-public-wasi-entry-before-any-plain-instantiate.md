# 0081 — `burroughs run` detects a `wasip1` command from the module's sections and routes to a public WASI entry, before any plain instantiate

Date: 2026-09-08 · Status: **proposed** · Kickoff: [#683](https://github.com/scttfrdmn/burroughs/issues/683) · Ruled by Scott across the (C)-then-(A) review
Ratio-Class: carried

## Context

[ADR 0080](0080-a-go-wasip1-guest-runs-to-main-on-the-host-surface-and-the-preview1-import-set-is-supplied-whole-because-link-refuses-a-gap.md)
landed an internal WASI preview-1 runner and two guests. The capability is "real only once someone
outside the tree can invoke it, and a CLI is the cheapest such someone" (Scott). This is step **(A)** of
the ruled **(C)-then-(A)** sequence: (C) landed the second program ([#685](https://github.com/scttfrdmn/burroughs/pull/685)),
so `Config` is now shaped against two programs; (A) commits the public entry the CLI needs.

**The measured obstacle that made (A) necessary rather than optional.** [ADR 0029](0029-the-public-boundary-run-on-a-validated-path-decline-as-a-third-outcome-and-a-value-that-converts.md)
(stamped) confines `cmd/burroughs` to the **public** `burroughs` package — never `internal/` — so `run`
is not a second, unprobed path to the interpreter. So the CLI cannot call `internal/wasi` directly;
wiring the CLI and committing the public embedder API are the **same commitment**, and option (B)
(exempting the CLI from 0029) was rejected on 0029's own grounds. The public entry is that commitment.

**Preview 1 is the compatibility on-ramp, not the target (Scott's ruling).** The thesis is "host real
Go, track the spec edge to wasip3", and the contract's §6 ("The event loop and readiness (wasip3)") is
where the design points. Preview 1 exists here for a measured reason: the stock Go toolchain emits
`wasip1` only — `GOOS=wasip2`/`wasip3` are `unsupported GOOS/GOARCH pair` on `go1.27.1`, and a p3 Go
guest is producible today only from the componentize-go family as a *component*, a different artifact
class (recon: [#688](https://github.com/scttfrdmn/burroughs/issues/688)). "Programs people write" is
therefore `wasip1` guests today — the no-consumer logic in the other direction, p1 where the consumers
are and p3 where the design points. **Retirement condition:** a p3-emitting toolchain Go programs can
actually use. Until then p3 stays a readiness constraint on the design, not a track, and no p3 work is
scheduled.

## Decision

**A public WASI run entry and a public command-detector on the `burroughs` package; the CLI reads the
module's sections to detect a `wasip1` command and routes to the entry before any plain instantiate.**

1. **Public run entry.** `WASIP1Config{Args, Env []string; Stdin io.Reader; Stdout, Stderr io.Writer}`
   with `func (WASIP1Config) Run(wasm []byte) (exitCode int, err error)`, mirroring `internal/wasi.Config`.
   The `burroughs` package imports `internal/wasi` (an internal-to-internal dependency; the CLI reaches
   it only through this public method, so 0029 holds). The spelling is `WASIP1Config.Run(wasm)` to parallel
   the existing `Config{Strict}.Instantiate(wasm)`, chosen against the two programs now in hand.

2. **Public command-detector, reading the module's sections.** `func IsWASIP1Command(wasm []byte) (bool,
   error)` decodes and answers true iff the module **imports `wasi_snapshot_preview1`** *and* **exports
   `_start`** (a function). This is the ruled autodetect — *the module's import section is the fact*,
   read through the public surface — and it does **not** instantiate, so it is independent of
   [#686](https://github.com/scttfrdmn/burroughs/issues/686)'s nil-resolver finding (a plain
   `Instantiate` that degrades on unresolved imports). Detection reads; it does not run.

3. **CLI routing (`cmd/burroughs/run.go`), the ruled rule.** After reading the file:
   - **A function name is given → invoke it as today** (`Instantiate` + `Call`), unchanged.
   - **No function + `IsWASIP1Command` → run as WASI** through `WASIP1Config.Run`, with `argv[0]` the file path,
     `Env` the process environment, and the CLI's own stdin/stdout/stderr; the guest's exit status is
     the CLI's exit status.
   - **No function + not a command → list exports as today.** Scott's ruling (i): listing the exports
     *is* naming the runnable forms concretely, so this is the rule's intent, not an exception to it.
   The command branch routes to the run entry **before any plain instantiate**, so the CLI never leans
   on #686's degrade-and-defer behavior.

4. **The guest's exit status propagates; it is not remapped.** A `wasiExit int` error carries the
   guest's `proc_exit` code, and `exitCode` maps it at the **top**, ahead of the CLI's own taxonomy
   (`exitError`…`exitGated`). A WASI guest's exit code is the guest's, and a process runner propagates
   its child's status faithfully — the taxonomy codes are for the CLI's *own* failures, and a genuine
   decode/trap failure of a `Run` still travels them. The unavoidable overlap (a guest exiting 4 and the
   CLI's `exitTrap` = 4) is the standard cost of running arbitrary programs and is resolved in the
   guest's favor: it chose the code.

5. **The public differential test covers the WASI path from the first commit** — 0029's reason for
   funnelling the CLI through the public package. A `burroughs`-package test runs a real guest through
   `WASIP1Config.Run` (and asserts `IsWASIP1Command`), so the public WASI surface is not a second unprobed path.

## Consequences

- **Capability line:** a program compiled by a third-party toolchain is invocable from outside the tree
  — `burroughs run hello.wasm` runs it to `main`, writes its stdout, and exits with its code.
- **`Config`'s shape held across two programs** (hello needed no stdin; echo did), so the public
  `WASIP1Config` is committed against two data points, which is what (C)-then-(A) existed to gather.
- **The public names carry the ABI version — `WASIP1Config`, `IsWASIP1Command` — not a bare `WASI`.**
  Measured: the surface as first drafted (`WASIConfig`, `IsCommand`) read as WASI *tout court* while
  `IsWASIP1Command` detects specifically `wasi_snapshot_preview1` and `Run` runs only preview 1. Naming
  the version means the eventual p3 entry is a *new* symbol, not a rename of one that had claimed to be
  all of WASI. Done at the identifier because the surface was unreleased — a rename now costs nothing
  and makes the lie unrepresentable rather than guarded against.
- **A limitation, named not hidden:** passing `argv` to a `wasip1` command is not yet expressible —
  trailing args are the invoke rule's function-plus-values, so a command runs with `argv = [path]`. A
  grammar extension (e.g. `run cmd.wasm -- args…`) is a later slice, when a guest that reads `argv`
  is in hand.
- **#686 stays independent.** Detection reads sections and routes before instantiate, so the
  nil-resolver decision is not entangled with this one.
- **The remaining stubs** (`fd_close`, `fd_fdstat_set_flags`, `fd_prestat_dir_name`, `poll_oneoff`'s fd
  arm) still grow only when a guest that exercises them is in hand.
