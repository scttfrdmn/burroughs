# 0120 — A host-read stream is re-armed after each copy until its writer drops, taking an end's event returns it to idle, and a WASI exit ends the run

Date: 2026-11-12 · Status: **accepted**, 2026-11-12. Scott, in session, approving the plan for v0.14.0's sixth slice (#1261, #1262). The approval was given in session and has no comment to cite, so the record is the actor's, and commits resting on it are `Ratio-Class: carried`. It is an engine mechanism inside `internal/component`, with one root-package edit that adds no exported symbol: `ComponentConfig.Run` reads a code the engine now returns. No contract text changes and no stamped ADR is reversed. Filed against [#1261](https://github.com/scttfrdmn/burroughs/issues/1261) and [#1262](https://github.com/scttfrdmn/burroughs/issues/1262).

## Context

E4 (#1232, ADR 0119) found two defects that trap an ordinary Rust `wasm32-wasip3` std guest that wasmtime 49.0.3 runs, and p3sys worked around both rather than repairing them.

**#1261: a host-read stream takes one copy.** `write-via-stream` parks a host read on the guest's readable end (`hostSink`), and its `onHostComplete` resolves the future on the first copy. No per-copy state is reset after it:

- the readable end's `resolved` stays true, so a second `stream.write` finds no pending read, parks BLOCKED, and the sync task's wait raises `errSyncTail`: *"cannot block a synchronous task before returning"*;
- `streamDropWritable` notifies only a reader with `hasRead && !resolved`, so the writer's drop is never seen by the host.

Measured on main at 2b1171d, with a std guest that prints three lines: the first line is written and the run traps exit 4 at the second.

Two latent bugs sit on the writable end, behind the first, and surface once the reader re-arms:

- **a second inline write reports progress 0.** `streamWrite` reads the inline payload through `pendingEventLocked`, which delivers once per end because `delivered` is never cleared;
- **a write after a guest-first completion is refused** *"not writable"*, because `hasWrite` is cleared only by a cancel.

The reference returns an end to IDLE when its event is taken: `stream_event` clears the buffer and sets `End.State.IDLE` for COMPLETED and CANCELLED, DONE for DROPPED (def:StreamEnd.notify.stream_event:"self.state = End.State.IDLE"), and `copy` admits a new copy only from IDLE (def:copy:"trap_if(end.state != End.State.IDLE)"). A host consumer is an End with no owner that copies again after each event until DROPPED, which is what wasmtime's stdout consumer does.

A future-read case follows once the drop reaches the host. A guest that drops its writable end and only then reads the future `write-via-stream` returned has the drop resolve the future first, and `futureRead` refuses an already-resolved end. In the reference the host's completion is the first arriver, its value parked on the shared future, and the guest's read is the second, completing inline (def:End.copy:"elif buffer.remain() > 0 and self.other.buffer.remain() > 0:"). The plan expected Rust std to take that order at exit. **Measured, it does not reach it:** with the inline arm removed, p3std still matches the oracle, and a count of the built-ins, run once and removed, saw no `stream.drop-writable` and no `future.read` in p3std, in p3sys, or in a std guest that prints twice and returns from `main`. The guest's wasi-libc drops a stream's writer in its descriptor `free` (`__wasilibc_write_state_close`), which a program that never closes fd 1 does not reach. The arm is the reference's semantics and is witnessed below at the built-ins.

**A zero-length write.** In the reference a write of 0 elements against a pending read takes the last arm of `End.copy` and notifies only the writer (def:End.copy:"self.notify(progress = 0)"). The reader stays parked. Burroughs drove the copy for 0 and armed both ends, which a host consumer reads as a copy that ended.

**#1262: an exit returns to the guest.** `exit` (0.2 and 0.3) and `exit-with-code` record the code and return nil. Rust std's `process::exit` treats the import as one that does not return, so the next instruction is `unreachable`, and the guest traps. A host function's error already unwinds the interpreter, wrapped as `interp.ErrHostTrap`, and preview 1 already uses that channel: `procExit` returns `exitError{code}`, which `wasi.Run` unwraps (`internal/wasi/preview1.go`, `runner.go`).

**An adjacent defect: `run`'s own result is discarded.** `Instantiated.call` ran the export through `invoke`, which drops the lift's results, so a `run` returning `Err` exited 0.

### What the oracle does, measured

wasmtime 49.0.3 (`.oracle/wasmtime-49.0.3/wasmtime`, never the 50.0.1 on PATH), darwin/arm64:

- **p3std** (the witness guest below): exit 7, stdout and stderr byte-identical across 10 of 10 runs, nothing else on either. stdout is the three lines, then the bytes `print!` had buffered with no newline, so std's `process::exit` flushes stdout before it calls `exit-with-code`. On Burroughs that flush is one more write on the open stream.
- **A `wasi:cli/run@0.2.0` component whose `run` returns `err`** (`run-result-err.wat`, below): exit 1, nothing on stdout or stderr, 10 of 10. Its control, the same component returning `ok`, exits 0.
- A std guest whose `main` returns `Err` prints `Error: "…"` to stderr and exits 1 too, but through `exit-with-code`, not through `run`'s result, so it witnesses #1262 rather than the result read.

## Options

For #1261:

1. **Re-arm the host read after each copy** in `onHostComplete`: COMPLETED clears the read end's per-copy state and leaves it COPYING with `hasRead`; DROPPED resolves the future. One read per guest write, as the reference's host consumer does.
2. **A read loop on a goroutine** over the readable end. *Costs:* a second scheduler for one consumer, and it has to take `asyncHandles.mu` in competition with the writer for every copy the inline path already serves under that lock.
3. **Resolve on the first copy and swallow later writes.** *Costs:* every byte after the first write is lost, and the future resolves before the guest stops writing.

For #1262:

1. **The host returns an unexported `exitError`**, preview 1's shape. The interpreter's existing unwind carries it out, and `CallRun` reads it.
2. **A sentinel panic** like ADR 0071's terminal unwind. *Costs:* a second unwind mechanism where an error already reaches every frame.
3. **An exported exit type** an embedder matches. *Costs:* public API, which is Scott's, for a code `Run` already returns.

## Choice

1. **A host-read stream is re-armed after each copy** (option 1, #1261).
   - `writeViaStreamTo`'s `onHostComplete` branches on the copy's result. COMPLETED re-arms: `resolved`, `delivered` and `progress` are cleared, and the end stays COPYING with `hasRead`, so the next write is again the second arriver and completes inline. DROPPED resolves the future with `result::ok`. A host sink's error stays the copy's error, the failing `stream.write`'s, as before.
   - `streamDropWritable` notifies a reader with `hasRead && !resolved`, which a re-armed host read is, so the writer's drop now reaches it.
2. **Taking an end's event returns it to idle**, on the writable end (def:StreamEnd.notify.stream_event).
   - `pendingEventLocked` clears `hasWrite` when it delivers, so a write whose guest-first completion has been taken leaves the end writable.
   - `streamWrite` clears the end's per-copy state (`resolved`, `delivered`, `progress`, `result`) at the start of each write. It is reached only from IDLE: an end with a write pending, or with a completed write whose event is untaken, still has `hasWrite` and is refused, as the reference's `copy` traps on an end not IDLE.
   - This repairs both latent bugs for any reader, the host's or a guest's.
   - **The readable end's guest path gets the same reset.** `hostReadLocked`, which stands in for a guest's `stream.read` (not bound as a built-in), is reached only from IDLE and now clears the last copy's state. Without it a reader re-parked after taking its event still read as resolved, and the next write parked rather than completing inline. The witness below needs a reader that re-parks, so the repair rides with it.
3. **A zero-length write completes the writer only**, against a reader parked either way. `streamWrite` with a pending read returns COMPLETED with progress 0 and leaves the read pending; a read arriving at a parked write of 0 completes that write with progress 0 and parks itself. This is `End.copy`'s last two arms (def:End.copy:"self.notify(progress = 0)", and the arm before it). The host consumer needs no arm of its own: driving a parked write of 0 copies nothing, completes it, and re-arms the read, which is the reference's outcome.
4. **A read of a host-resolved future completes inline.** `futureRead` on an end the host has resolved and nobody has read lowers the payload at the read's pointer and returns COMPLETED, consuming the event, as the reference's second arriver does. A second read of the same end is still refused, since the future is single-shot.
5. **An exit ends the run** (option 1, #1262).
   - `exit` and `exit-with-code` still set `Exited` and `ExitCode`, and now return `exitError{code}`. The interpreter unwinds the guest with it, as it does any host function's error.
   - `Instantiated.CallRun` returns `(code, nil)` for an exit, matched with `errors.As`, and `ComponentConfig.Run` returns that code. The store's lockdown after a terminal outcome is kept: the exit reaches the lock as a host trap, so a later call is refused.
   - `Component.Call` returns the exit as its error, whose text names the exit and its code: *"host function trapped: component: the guest exited with code 7 (wasi:cli/exit)"*, measured on p3std's `run` called through `LoadComponent`. A second call is refused as `ErrInstanceLocked`, wrapping the same text. **No exported symbol is added**: an exported exit type would be public API, and is left for an embedder's request. The one root-package edit is `ComponentConfig.Run` returning `CallRun`'s code, where it read `Host.Exited`.
6. **`run`'s result is read.** `CallRun` reads the export's result: `wasi:cli/run`'s `run` returns a `result` with no payloads, which flattens to one discriminant word, and a nonzero word is `err`, exit 1, as the oracle exits. The word is a sync lift's own return or the one `task.return` received, so both lift kinds are read alike. An exit inside `run` wins over its result, which it never reaches.

### Out of scope, recorded

- **A writer dropped before the host reads.** The reference's drop clears the reader's `other` and arms a DROPPED event the reader's next copy finds. Burroughs's drop keeps `conn` and notifies only a reader already pending, so a `write-via-stream` handed a stream whose writer is already gone would park. No committed guest orders it that way.
- **An exit on a spawned component-model Thread.** `exitError` is not an `interp.InstanceEnder` (ADR 0090's mechanism), so it ends the agent that calls it. No committed guest exits from a created Thread.
- **The guest-to-guest witness is a harness test, not a `.wat`.** The plan asked for a `.wat`. A guest cannot read a stream here, since `stream.read` is not bound as a built-in, so no component can hold both ends' guests; the async-stream tests' harness, which drives the built-ins directly, is the nearest form.

## Consequences

- **Witnesses:**
  - `TestP3StdMatchesWasmtime` runs `testdata/p3std/p3std.wasm` under `gate:async` through `CallRun`, as `Run` does, and compares stdout, stderr and the exit code with wasmtime 49.0.3's reading, `testdata/p3std.reading`, byte for byte. The guest is a dependency-free Rust `wasm32-wasip3` program (nightly 1.100.0, built offline by `p3std/build.sh`): three `println!`s, an `eprintln!`, a `print!("")` and a flush, then a `print!` with no newline and `std::process::exit(7)`.
  - `TestARunReturningErrExitsOne`: `run-result-err.wasm` exits 1 through `CallRun`, and its `ok` control exits 0, each as wasmtime 49.0.3 does (`run-result.reading`).
  - `TestRunAComponentsExitIsTheProcesssExit` (`cmd/burroughs`): `burroughs run p3std.wasm` exits 7 and `run run-result-err.wasm` exits 1, through `ComponentConfig.Run`, with stderr holding only what the guest wrote.
  - `TestWriteViaStreamResolvesAtTheWritersDrop`: the consumer through the guest built-ins, short of a guest. Two writes are taken inline, the future stays pending while the writer is open, `stream.drop-writable` resolves it, and a `future.read` after the drop returns COMPLETED inline with `result::ok` lowered. It is the witness for the drop and the late read, which p3std does not reach.
  - `TestAStreamWriteEndIsIdleOnceItsEventIsTaken`: two ends from `stream.new`, a reader standing in for a guest's, as the async-stream tests do. A second host-first write reports its own progress, a write after a taken guest-first completion is accepted, and a write of 0 completes only the writer, in both orders.
- **Neuters, run**, each named at its site:
  - `onHostComplete` resolving the future on every result: p3std traps at its second line, *"cannot block a synchronous task before returning"*, and the drop witness's future resolves after the first write with the writer still open.
  - `futureRead`'s inline arm removed: the drop witness's read is refused, *"end 3 already read (a future is single-shot)"*. p3std stays green, as recorded above.
  - `exit-with-code` returning nil: p3std traps at `unreachable`, and p3sys's run exits 0 where the reading says 7. `exit` returning nil fails `TestSharedTrackEntriesStillServe` and no guest witness, since std's `process::exit` calls `exit-with-code`.
  - `CallRun` not unwrapping the exit: p3std and p3sys both fail with *"host function trapped: component: the guest exited with code 7 (wasi:cli/exit)"* in place of code 7.
  - `runReturnedErr` returning false: run-result-err exits 0.
  - `ComponentConfig.Run` returning 0 for the code: the CLI witness exits 0 for both guests.
  - `streamWrite`'s per-copy reset dropped: the idle witness's second host-first write reports progress 0, the drop witness's second write returns 0, and p3std traps at `unreachable` (std's `write_all` sees a write of 0 bytes and panics).
  - `hostReadLocked`'s reset dropped: the idle witness's second host-first write parks BLOCKED.
  - The `hasWrite` clear on taking the event dropped: the idle witness's later write is refused, *"end 2 is not writable (done or write pending)"*.
  - `streamWrite`'s zero-length arm dropped: the write of 0 arms the parked reader. `hostReadLocked`'s dropped: the read at a parked write of 0 completes.
- **Tests changed:**
  - `TestSharedTrackEntriesStillServe` (`hosttrack_test.go`) asserted `exit` returned nil; it now asserts the `exitError` for code 1, since an exit ends the run.
  - `TestP3SysMatchesWasmtime` (`p3sys_test.go`) read `h.Exited` after a nil `CallRun`; it now takes the code `CallRun` returns, as `Run` does. Its reading is unchanged.
  - `TestWriteViaStreamCompletionWritesOnlyItsPayloadsBytes` (`host_future_test.go`) completed the future with `onHostComplete(copyCompleted)`; it now drives `copyDropped`, since a COMPLETED copy re-arms the read and only the writer's end resolves the future. Its subject, the bytes the completion writes, is unchanged.
  - `TestP3EchoMatchesWasmtime` and `TestStreamErrArmLowersAnOwnErrorHandle` (`echo_test.go`) take `CallRun`'s two results.
  - No async-stream, future or cancel test asserted one-copy behaviour, and all pass unchanged, with and without `-race`.
- **What a guest sees.** A WASI 0.3 std guest prints as many lines as it writes, and its `process::exit(n)` exits n. A component's `run` returning `err` exits 1.
- **No contract text changes, and no public API.**

## Rollback

Restoring `onHostComplete`'s single completion restores #1261's trap at the second write. Returning nil from `exit` and `exit-with-code` restores #1262's `unreachable`. Each is a neuter above, run.
