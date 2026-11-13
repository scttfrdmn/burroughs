# 0119 — WASI 0.3's random, clocks and remaining cli functions are served synchronously, a random request is capped at the oracle's 2^26 bytes, and a lower's realloc runs in a context of its own

Date: 2026-11-12 · Status: **accepted**, 2026-11-12. Scott, in session, approving the plan for v0.14.0's fifth slice (E4, #1232). The approval was given in session and has no comment to cite, so the record is the actor's, and commits resting on it are `Ratio-Class: carried`. It is an engine mechanism inside `internal/component`, `internal/component/canon` and `internal/interp`: no contract text, no public API (no root-package file changes), and no stamped ADR is reversed. Filed against [#1232](https://github.com/scttfrdmn/burroughs/issues/1232).

## Context

On main at 32bfa9e, the WASI 0.3 host serves stdout's `write-via-stream`, `exit`, `get-arguments` and an empty `get-environment`, and nothing else. **No `wasi:random` and no `wasi:clocks` exist in the component host at any version.** A Rust `wasm32-wasip3` std guest reaches these on startup: HashMap keys use `get-insecure-seed`, std's random calls `get-random-bytes`, and `Instant` and `SystemTime` call the two clocks' `now`. p3async-hello already lowers the terminal getters, both clocks' `now`, stderr's `write-via-stream` and `exit-with-code`, and none of them was served.

The pinned WIT is the WASI 0.3.0 WIT the nightly's std vendors (`wasip3-0.8.0+wasi-0.3.0`):

- **random:** `get-random-bytes(max-len: u64) -> list<u8>`, `get-random-u64`, `get-insecure-random-bytes(max-len)`, `get-insecure-random-u64`, and `get-insecure-seed -> tuple<u64, u64>`. A bytes function may return fewer than `max-len` bytes, but at least one when it is above 0.
- **monotonic-clock:** `now -> mark` and `get-resolution -> duration`, both sync. `wait-for` and `wait-until` are async and are E5's (#1233).
- **system-clock:** `now -> instant { seconds: s64, nanoseconds: u32 }` and `get-resolution`. `timezone` is `@unstable` and not served.
- **cli:** `get-initial-cwd -> option<string>`; stderr's `write-via-stream`, typed as stdout's; `get-terminal-{stdin,stdout,stderr} -> option<own<terminal-input|terminal-output>>`; and `exit-with-code(u8)`, which is `@since(0.3.0)`. stdin's `read-via-stream` is E6's.

### What the oracle does, measured

wasmtime 49.0.3 (`.oracle/wasmtime-49.0.3/wasmtime`, never the 50.0.1 on PATH), darwin/arm64, on a probe guest built like p3sys:

- **The bytes functions are capped at 2^26.** `max-len` of 2^26-1 returned 2^26-1 bytes. 2^26, 2^26+1, 2^30 and `u64::MAX` each returned 67,108,864 (2^26), for both `get-random-bytes` and `get-insecure-random-bytes`. 0 returned 0 and 16 returned 16.
- **Both resolutions are 1000 ns** on this host, and a system-clock reading's nanoseconds end in 000. That is the host's clock granularity (clock_getres), not a constant of the WIT.
- **The monotonic clock starts near 0**: its first reading in `main` was 193,583 ns.
- **The seed is stable**: two `get-insecure-seed` calls agree, and the pair is nonzero.
- **`exit-with-code` needs no flag.** `exit-with-code(7)` exits 7 under plain `wasmtime run`, and `-S cli-exit-with-code=n` warns *"cannot disable `-Scli-exit-with-code` any more"* and still exits 7. The plan had expected the flag to be the oracle's invocation, and on 49.0.3 it changes nothing, so the invocation is plain `wasmtime run`.
- **The terminals are a fact about the host's streams.** With stdin from `/dev/null` and stdout and stderr to files, all three `get-terminal-*` are none. Under a pty (`script -q /dev/null wasmtime run …`) all three are some.
- **`get-initial-cwd` is none** and the environment is empty, with no `--dir` and no `--env`.

## Options

For the bytes cap:

1. **The oracle's 2^26.** A guest's `max-len` reads the same here as there, and the length a huge request gets is a comparable property.
   - *Costs:* a request can make the guest allocate 64 MiB. That is the guest's memory, through its own realloc, and wasmtime charges the same.
2. **A smaller cap**, one `canon.StringChunk` (64 KiB). The WIT permits it.
   - *Costs:* a guest asking for more gets a different length here than under the oracle, and the witness could compare only a property of it.
3. **No cap.** *Costs:* `u64::MAX` overflows `int` and is refused, and 2^30 allocates a gigabyte of guest memory per call (both measured by neutering the cap).

For where the bytes live: build a `canon.List` of `U8` values, as `blockingRead` does, or write them straight through the lower's realloc.

For the resolutions: the oracle's 1000 ns, or 1 ns, preview 1's figure.

## Choice

1. **Random.**
   - Both bytes functions read `crypto/rand`, and so does `insecure`: the WIT permits an insecure source to be a secure one, and one source is simpler than two.
   - **The cap is option 1, 2^26** (`randomBytesCap`). A request returns `min(max-len, 2^26)` bytes, so 0 gets none and anything above gets at least one.
   - **The bytes are written through the lower's realloc, a chunk at a time** (`canon.StoreListU8`): realloc for the whole count at alignment 1, then `canon.StringChunk` bytes filled and written per step through one buffer, then the (pointer, count) pair at the return pointer. The host holds 64 KiB whatever the request. A `canon.List` of `U8` values would hold a `canon.Value` per byte, about 80 bytes each by `listSegment`'s own figure, so about 5 GiB of host memory for one capped request. `StoreListU8` is `StoreList`'s framing for the one element type whose element loop is a contiguous write, the store side of `LoadListU8`.
   - `get-insecure-seed` returns a pair drawn from `crypto/rand` once, in `NewHost`, since the WIT means it to be stable.
2. **Clocks.**
   - Monotonic `now` is `time.Since(h.start)`, and `NewHost`, the only constructor, sets `start`. Preview 1's #815 applies: a zero start makes `time.Since` saturate at 2^63-1, and the clock stands still.
   - System `now` is `time.Now()`'s `Unix()` and `Nanosecond()`, which split an instant as the WIT does, with nanoseconds in [0, 1e9) even before the epoch.
   - **Both resolutions are 1 ns**, preview 1's figure and the unit Go's time API reads in. The oracle's 1000 ns is its host's clock_getres, which a pure-Go engine cannot ask without cgo. The witness compares the property both satisfy, above 0, and **the figure is a stated divergence**.
3. **cli.**
   - `get-initial-cwd` is none, and `get-environment` stays empty: #1232 grants neither, and `ComponentConfig.Env` would be public API.
   - **stderr's `write-via-stream` is stdout's implementation over `h.Stderr`.** `writeViaStream` takes its writer, read when an instantiate builds the host's tables, and both register `stdioWriteResultType` as their payload, so `checkConsumerPayload` (ADR 0118) applies to both.
   - **The three `get-terminal-*` are none.** The host's streams are `io.Writer`s and an `io.Reader`, which cannot say whether a terminal is behind them. **A stated divergence:** under a pty the oracle answers some.
   - **The 0.3 terminal resource types are declared**, `wasi:cli/terminal-input@0.3::terminal-input` and `wasi:cli/terminal-output@0.3::terminal-output`. That is required, not tidy: once the getters are served, `checkHostResources` (ADR 0118) refuses a served import whose own names no host resource, and p3async-hello lowers all three. Nothing mints one, and a guest's drop of one now has a host type rather than `dropNoop`.
   - **`exit-with-code` is served**, recording the code as `exit` records its status: `Exited`, and `ExitCode` is the u8.
4. **Keying.** Every row is `serve(m, wasiP3, …)` (ADR 0115). There are no 0.2 twins: 0.2's random and clocks are other interfaces with other signatures, and not v0.14.0's.
5. **A repair the witness compelled: a lower's realloc runs with context of its own** (`interp.CanonCaller.Realloc`). The reference does not call realloc inside the caller's thread. `reallocate` lifts it and invokes it as a call of its own (def:LiftLowerContext.reallocate:"self.inst.store.invoke(realloc, on_start, on_resolve)"), so it runs on a new Thread whose context starts [0, 0]. Burroughs ran it on the caller's stack, over the caller's context slots.
   - A `wasm32-wasip3` std guest's realloc is wrapped in wasi-libc task hooks (`__wasm_task_hook` codes 12 and 13). They switch the shadow stack pointer, which is context slot 0, to a scratch stack on entry and set it to 0 on exit. That is right in a fresh context. Run in the caller's, it left the caller's stack pointer at 0, and the guest's next frame stored below address 0 and trapped out of bounds. p3sys trapped at the first stdout write after `get-random-bytes(0)`.
   - So the caller's slots are saved, zeroed for the call, and restored after it, whatever it returns. That is the realloc's own fresh context, and the caller's context as it left it.
   - **Scope: the stack's slots.** A caller whose context lives in a Thread record, an async-lift task or a created Thread (`callerStorage`), still lends that storage to the realloc. No committed guest reaches it, and closing it means routing the realloc's built-ins to a fresh storage of their own.
   - `get-arguments` is a lower with a realloc too, and the probe that read its arguments in `main` and then printed did not trap. Why it escaped was not established; the realloc-context fixture is what isolates the mechanism.

### Out of scope, recorded

- **A guest writes stdout once.** std's wasip3 stdout keeps one stream open across writes, and `write-via-stream` consumes a single write, then resolves its future. A second write has no reader, and a sync task blocks with nothing to run: *"cannot block a synchronous task before returning"*. Measured on a probe that prints two lines. The oracle reads until the writer drops. p3sys therefore writes each channel once, and p3async-hello prints one line. The follow-up is #1261: the consumer re-arming its read after each copy and resolving its future at the writer's drop.
- **`exit` and `exit-with-code` record and return**, so the guest runs on. std's `process::exit` follows the call with `unreachable`, so a guest calling it traps here where the oracle exits. p3sys calls the binding and returns from `main`, which the two engines read alike. The follow-up is #1262: an exit that unwinds the run.
- **The realloc's `thread.index`** is still the caller's. In the reference it is the new Thread's.

## Consequences

- **Witnesses:**
  - `TestP3SysMatchesWasmtime` runs `testdata/p3sys/p3sys.wasm` under `gate:async` and compares stdout, stderr and the exit code with wasmtime 49.0.3's reading, `testdata/p3sys.reading`, byte for byte. The guest is a Rust `wasm32-wasip3` program (nightly 1.100.0, wit-bindgen 0.61.1, built offline by `p3sys/build.sh`). It calls every E4 function through bindings generated from the pinned WIT and prints properties, never values: each bytes function's length against the WIT's bound, the capped length at 2^30 and `u64::MAX`, two draws differing, the seed stable and nonzero, the monotonic clock advancing within a million reads, the system clock's fields in range, both resolutions above 0, an empty environment, no cwd and no terminals. Then it writes one line to stderr and calls `exit-with-code(7)`. The oracle's reading is the same 10 of 10.
  - `TestReallocRunsWithItsOwnContext` (`realloc-context.wasm`): `run` sets context slot 0 to 12345 and calls `get-random-bytes(4)`, whose realloc records the slot it sees and sets it to 0xdead. wasmtime 49.0.3 reads `12345 << 16 | 0`, 10 of 10, and so does this build.
  - `TestStoreListU8IsStoreListsFraming`: `StoreListU8` and `StoreVia` of the same `list<u8>` leave identical model heaps and make identical realloc calls, at counts 0, 5 and two chunks plus three.
- **Neuters, run**, each named at its site:
  - The cap at 0: the two 16-byte lines read false and the three capped lines read `len 0`. The cap removed: `u64::MAX` is refused as *"list count -1 is negative"*.
  - `NewHost` without `start`: *"monotonic-clock now: advances: false"*.
  - The terminal types undeclared: p3async-hello and p3sys are both refused at instantiate, naming `get-terminal-stdin`'s `own<wasi:cli/terminal-input@0.3.0::terminal-input>`.
  - stderr's consumer over `h.Stdout`: the stderr line lands on stdout, and stderr is empty.
  - The realloc over the caller's slots, as before: p3sys traps out of bounds, and realloc-context reads `0xdead << 16 | 12345`. The zeroing alone removed reads `12345 << 16 | 12345`, and the restore alone removed reads `0xdead << 16 | 0` and traps p3sys.
  - `StoreListU8` writing every chunk at the base: the two-chunks-plus-three heap differs.
- **Tests changed:** `TestWriteViaStreamCompletionWritesOnlyItsPayloadsBytes` (`host_future_test.go`) calls `writeViaStream(io.Discard)(async)`, where it called `h.writeViaStream(async)`, since the consumer now takes its writer.
- **What a guest sees.** A WASI 0.3 guest's random, clocks, cwd, terminals, stderr and `exit-with-code` are answered, where each was refused at call. Its realloc no longer clobbers its stack pointer. p3hello, p3echo and p3async-hello run as before.
- **No contract text changes, and no public API.**

## Rollback

Each row is one `serve` line, and removing it restores the refusing stub. The terminal declarations go with the getters, or p3async-hello is refused. Restoring `Realloc`'s call over the caller's slots restores the clobber, and with it the trap after any realloc a wasip3 std guest's hooks wrap. Each is a neuter above, run.
