# 0080 — A Go `wasip1` guest runs to `main` on §5's host surface, and the preview-1 import set is supplied whole because `link` refuses a gap

Date: 2026-09-07 · Status: **proposed** · Kickoff: [#683](https://github.com/scttfrdmn/burroughs/issues/683) (Scott, this session)
Ratio-Class: carried

## Context

Burroughs has 60,957 passing spec assertions and has never executed a program anyone wrote outside
the project's own fixtures. The corpus proves compliance; it does not prove the engine is *usable*.
The workload that closes that gap — and the one the compilation conversation flagged as absent —
is a program compiled by a third-party toolchain. Go is the project's own language, `GOOS=wasip1
GOARCH=wasm` is in the stock toolchain, and the resulting binary exercises bulk memory, tables,
indirect calls, `memory.copy`/`fill` on the exact paths ADR 0054 covers, and growth on 0076's mmap
reservation — all at once. §5's host-function surface ([ADR 0069](0069-a-host-function-is-a-caller-and-a-value-slice-a-host-call-marks-its-thread-blocked-and-shutdown-is-its-own-terminal-method.md),
option A) landed three slices ago and has no caller outside its own tests; WASI preview 1 is that
surface's first real consumer, and building it is how the option-A signature is judged in use rather
than in the abstract.

**This is WASI preview 1, and it is deliberately *not* the contract's §6 WASI 0.3.** §6/WASI 0.3 is a
v3 deliverable on the phase ladder. Preview 1 is an older, different interface; running a `wasip1`
guest advances no phase and changes no contract text. It is a usability spike that validates the
existing v1 engine and §5 surface, framed exactly as the kickoff framed it: *prove the engine is
usable*, not *begin v3*.

### The binary is the oracle, and it disagrees with the estimate in two ways

Measured against a real `go1.27.1` `wasip1` hello-world before any design:

1. **The guest declares 15 distinct `wasi_snapshot_preview1` imports, not the ~10 a hello-world's
   surface suggests.** The extras are Go-runtime startup probes: `sched_yield` and `poll_oneoff`
   (the cooperative scheduler), `fd_prestat_get`/`fd_prestat_dir_name` (preopen discovery),
   `fd_fdstat_get`/`fd_fdstat_set_flags` (stdio probing), and `random_get` (hashmap seeding before
   `main`). This matters because `link` (`internal/interp/link.go`) **refuses any unsatisfied
   import** with `unknown import` — the accept-direction repair that converted 15 `assert_unlinkable`
   vectors — so every one of the 15 must be *supplied* at link time whether or not the hello path
   ever *calls* it. Supplied ≠ invoked: the off-path ones may be stubs, but they cannot be absent.

2. **The guest uses no wasm atomics and no shared memory** — measured on the disassembly's mnemonic
   column, after a first regex mistakenly counted 1132 `atomic.` hits that were `runtime/internal/
   atomic.*` symbol text (*measure with the instrument, not a regex*). Go's `wasip1` multiplexes
   goroutines cooperatively on a single wasm thread. So **`gate:threads` is not load-bearing for this
   workload.** The kickoff predicted a Go guest might force the threads flip on evidence; the evidence
   points the other way, which is the more useful result — running a real program and flipping
   `gate:threads` are independent, and this capability does not wait on the flip.

Bulk memory (`memory.copy`/`fill`) *is* used, so the decoder must have bulk memory enabled; threads
stays off.

## Decision

**Build an internal `internal/wasi` preview-1 host module on interp's public surface, supply the
whole declared import set, drive the guest to `main` through a runner, and prove it with a test that
compiles a real Go guest and runs it on Burroughs.**

1. **`internal/wasi`, built on `interp`'s public API** — `HostExtern`, `HostFunc`, `Caller`,
   `InstantiateLinked`, `Instance.Invoke`, and `binary.DecodeModule`. No cgo, no external dependency;
   the implementation uses only the standard library (`os`, `time`, `crypto/rand`,
   `encoding/binary`). The package depends on `interp`; `interp` does not depend on it.

2. **The runner is `DecodeModule` → `InstantiateLinked(m, wasiImports)` → `Invoke("_start")`.** The
   `Imports` resolver maps `("wasi_snapshot_preview1", name)` to a `HostExtern` per name. The guest
   *exports* its memory, so the host functions reach it through `Caller.Read`/`Caller.Write` (the
   copying accessors 0069 chose), never through a retained slice.

3. **All 15 imports are supplied; the startup-path set is implemented correctly and the rest are
   stubbed with a WASI errno.** Correct: `args_sizes_get`/`args_get`, `environ_sizes_get`/
   `environ_get`, `fd_write` (fds 1 and 2), `fd_prestat_get` (returns `EBADF` for non-preopen fds so
   the runtime's discovery loop terminates), `fd_fdstat_get`, `clock_time_get`, `random_get`,
   `sched_yield`, `proc_exit`. Stubbed with a benign errno for the hello path: `poll_oneoff`,
   `fd_close`, `fd_fdstat_set_flags`, `fd_prestat_dir_name`. The stub set is a *floor* that shrinks as
   more programs run, not a claim of completeness.

4. **`proc_exit` is a sentinel error carrying the exit code, and the runner unwraps it.** `HostFunc`'s
   contract is that *a returned error is a trap the guest cannot catch*; `proc_exit` is the one
   "trap" that is a normal termination. A `procExit` error type carries the code; the runner
   distinguishes it from `ErrHostTrap`/`*Trap` and reports `(exitCode, nil)` where a real trap reports
   the trap. One channel, so a normal exit cannot be spelled as a silent success and a trap cannot be
   spelled as exit 0.

5. **The guest source is committed under `testdata` and compiled by the test at run time, targeting
   `wasip1`; a missing toolchain is a *loud* skip that asserts why, never a silent pass.** This proves
   "third-party toolchain" freshly on every run and avoids committing a 2.4 MB `.wasm` whose staleness
   would make it an oracle nobody appointed. CI has Go ≥ 1.26 and the `wasip1` target ships with the
   toolchain (no install, offline build), so the test *runs* rather than skips in CI; the skip guard
   exists only for a developer machine without the target, and it fails loudly if the toolchain is
   present but the build fails (*a skip is not a verdict*).

6. **Decode with bulk memory enabled and threads off, and assert the guest decodes with threads
   off.** The feature set matches what Go emits; the threads-off assertion turns the measured fact
   (2) into a checked one, so a future Go runtime that started emitting atomics would announce itself
   here rather than silently.

7. **Slice 1 is internal and test-driven; there is no public embedder API yet.** A public
   `wasi.Run`-shaped entry point is public API surface and therefore Scott's to approve; it is
   deferred to its own decision rather than folded in. The internal test demonstrating stdout + exit
   0 is the capability's evidence for this slice.

## Consequences

- **Capability line:** the runtime runs a program compiled by a third-party toolchain — a Go guest
  reaching `main`, writing to stdout, and exiting 0.
- **§5 option A is judged in use.** Whether `HostFunc`'s boxed `[]Value` and `Caller`'s copying
  `Read`/`Write` are ergonomic for real WASI functions (`fd_write` reads a `ciovec` array and each
  buffer, writes `nwritten` back, returns an errno) is reported in the implementing PR — the question
  0069 left answerable only in the abstract.
- **`gate:threads` is not forced by this workload**, recorded against the assertion in (6). The flip's
  condition and schedule are untouched (Scott's #670 ruling); this ADR adds evidence that the
  usability milestone does not depend on it.
- **The stub set will grow into real implementations** as programs that exercise `poll_oneoff`,
  `fd_close`, and the filesystem arrive. Each is a later slice with its own guest to run.
- **Explicitly out of scope:** filesystem, sockets, `poll_oneoff` semantics, preopens beyond what
  startup demands, the public embedder API, and any performance number. This slice's number is
  binary.
- **One decision is deferred to Scott, not taken:** the public `internal/wasi` → public `wasi`
  embedder API. Recorded so a later reader does not read the internal-only choice as the permanent
  shape.
