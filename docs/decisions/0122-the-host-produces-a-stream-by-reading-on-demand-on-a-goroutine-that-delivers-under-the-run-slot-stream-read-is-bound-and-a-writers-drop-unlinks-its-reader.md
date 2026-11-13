# 0122 — The host produces a stream by reading on demand on a goroutine that delivers under the run slot, stream.read is bound, and a writer's drop unlinks its reader

Date: 2026-11-13 · Status: **accepted**, 2026-11-13. Scott, in session, approving the plan for v0.14.0's eighth slice (E6, #1234, with #1264). The approval was given in session and has no comment to cite, so the record is the actor's, and commits resting on it are `Ratio-Class: carried`. It is an engine mechanism inside `internal/component`: no contract text, no public API (no root-package file changes and no exported symbol), and no stamped ADR is reversed. Filed against [#1234](https://github.com/scttfrdmn/burroughs/issues/1234) and [#1264](https://github.com/scttfrdmn/burroughs/issues/1264).

## Context

Under WASI 0.3 a guest reads stdin through `wasi:cli/stdin`'s `read-via-stream: func() -> tuple<stream<u8>, future<result<_, error-code>>>` (the WIT the nightly's std vendors, `wasip3-0.8.0+wasi-0.3.0`). The host produces the stream. Its doc states the future's three outcomes: success when the readable end is dropped, success when stdin was closed, and an `error-code` when reading failed. E7's socket receive is the next producer and reuses this one.

How Rust std reaches it, read from `p3cat.wasm` (below) with `wasm-tools print`:

- wasi-libc lowers `read-via-stream` **sync, with memory**, as `(retptr) -> ()`, and reads the stream handle at +0 and the future at +4. It calls it once, at the first `read()`.
- Each `read()` is one **async** `canon stream.read 21 (memory $memory) async` into an 8 KiB buffer. A read that returns BLOCKED joins a waitable set and waits. On DROPPED, wasi-libc reads the future with `canon future.read 24 (memory $memory) async` to tell end of input from an error, then drops it.
- `run` is async-typed and lifted plainly, as in p3std and p3sleep.

Measured on main at 8b25d47, `p3cat.wasm` is refused at instantiate: *"an async canon built-in (0xf)"*. Exploration found two missing pieces of engine work and one defect behind it.

1. **A guest's `stream.read` (0x0f) is not bound.** `isBuiltAsyncBuiltin` refuses it, and the decoder throws its canonopts away, so even a binding would have had no memory to write into.
2. **No writer end can be held by the host.** A copy reads its source only from guest memory (`driveStreamCopyLocked`). The host's consumer side, `write-via-stream`, runs entirely on the guest's goroutine. A producer cannot: `Read` on `os.Stdin` blocks, and nothing can interrupt it.
3. **#1264: a writer's drop never unlinks its reader.** `streamDropWritable` notifies a pending read and leaves `conn` set. So a read that arrives after the writer dropped parks on a writer that is gone, and a COMPLETED event still untaken at the drop is delivered as COMPLETED, where the pin delivers DROPPED. The host's end of input drops its writer through exactly this path.

The slice found a fourth, once p3cat reached the end of its input:

4. **The decoder drops `future.read`'s, `future.write`'s and `stream.write`'s `(memory …)` option too.** Each one's caller falls back to the memory of the core instance that declared it, and a wit-component guest declares these built-ins in its shim's fixup instance, which has none. So the first `future.read` of a host future with a payload trapped: *"future.read completion: lowering a variant{ok, error(enum{io, illegal-byte-sequence, pipe})} at ptr: the instance that declared this host function has no memory 0"*. No guest had read one before: Rust std never drops stdout, so `write-via-stream`'s future never resolves under it. stdin's does, at the end of input.

### What the oracle does, measured

wasmtime 49.0.3 (`.oracle/wasmtime-49.0.3/wasmtime`, never the 50.0.1 on PATH), darwin/arm64, `wasmtime run p3cat.wasm` with stdout and stderr to files. 10 of 10 runs of each were byte-identical in stdout, stderr and exit code (`p3cat/build.sh` takes and compares them):

- **stdin `p3cat.stdin`**, 20770 bytes, 310 lines (two full 8 KiB reads and a short third): exit 0, empty stderr, and stdout
  - `p3cat: read 20770 bytes, checksum c1c68b27, 310 lines`
  - `p3cat: first line: p3cat stdin line 0001: the quick brown fox jumps over the lazy dog`
- **stdin `/dev/null`:** exit 0, empty stderr, and stdout
  - `p3cat: read 0 bytes, checksum 00000000, 0 lines`
  - `p3cat: first line: ` (the line is empty)

std's reads cannot show when a host's `stream.read` returns BLOCKED, so `testdata/read-park.wat` asks directly: one async `stream.read` of 8 bytes after `read-via-stream`, its result read in a callback (`run`), returned as it comes (`start`), or followed by a second read whose own return is taken as it comes (`twice`, whose first read is of 2). `-W component-model-async=y -W component-model-more-async-builtins=y -S p3=y --invoke '<export>()'`, 10 of 10 runs per row (`testdata/read-park.reading`):

- **The first read always returns BLOCKED,** even with bytes already waiting on the pipe (`start` on "abc": 0xffffffff). Its event is then COMPLETED with the bytes read: 3 of "abc", and 8 of a 12-byte stdin, the read's length.
- **On an empty stdin, a pipe or `/dev/null`, the first read returns BLOCKED and its event is DROPPED,** progress 0. The end of input is not reported inline.
- **A second read returns BLOCKED** after a first read of 2 from "abcdef", so wasmtime serves nothing from bytes read ahead; and after a first read that took "a", the end of input is learned at the second read, not delivered with the last byte.

### What the pin does

- `copy` traps unless the end is IDLE, then hands the end a buffer (def:copy:"trap_if(end.state != End.State.IDLE)"). A guest buffer traps at construction if its range is outside memory (def:GuestBuffer:"trap_if(ptr + length * elem_size(t, cx.opts.memory.ptr_type()) > len(cx.opts.memory))").
- `End.copy` has four arms. With no other end, the event is already pending (def:End.copy:"assert(self.has_pending_event())"). With no buffer on the other end, it parks. With both non-empty, the second arriver moves `min` of the two and notifies both. Otherwise a reader arriving at a writer of 0, or any copy at an empty other buffer, notifies the other with progress 0 and parks, or notifies itself with 0 (def:End.copy:"self.notify(progress = 0)").
- `End.drop` unlinks both sides (def:End.drop:"self.other.other = None"), and notifies the other end with progress 0 **unless it is DONE or already has an event pending** (def:End.drop:"if self.other.state != End.State.DONE and not self.other.has_pending_event():"). An IDLE end is notified too.
- The event is computed when it is taken, not when it is armed. `stream_event` reads `other` at that time, so an end whose writer has gone reports DROPPED, with the progress its notify recorded (def:StreamEnd.notify.stream_event:"if self.other is None:").
- `lower_async_value` adds each end to the instance's table in lowering order (def:lower_async_value:"end.index = cx.inst.handles.add(end)"), so a tuple's stream is minted before its future.
- The pin defines no host producer. How a host fills its writer's buffer, and when, is the host's.

## Options

For the host's writer:

1. **A host-held writable end with a host source:** the bytes the producer has read and not yet delivered. It is kept out of the guest's table, and the copy gains an arm that reads that source.
2. **A synthetic guest-memory buffer** the producer writes into, so the existing copy reads it. *Costs:* it needs guest memory the guest never allocated, and an allocation through `cabi_realloc` from a goroutine that is not the guest's.

For when the host reads:

1. **On demand:** a `Read` starts only when a guest read is pending and the host buffer is empty, sized `min(readN, 64 KiB)`, with at most one in flight per stream. Bytes beyond what the reader took stay in the buffer.
2. **Read ahead** on a long-lived goroutine into a bounded buffer. *Costs:* it consumes stdin bytes no guest asked for. Another reader of the same source, after this instance is closed, would never see them.

For where the producer's result reaches the guest:

1. **Under ADR 0101's result-store discipline**, the path `onResolve` takes: queued in `hostStores` while the slot's holder is in host code, and otherwise through `acquireRun`. The copy into the reader, the arm and the future's resolution then run under the slot.
2. **Lazily, at the event's take**, on the guest's goroutine. *Costs:* the reader's event would be armed before its bytes landed, so `waitable-set.poll` and a cancel would see a COMPLETED whose bytes are not yet in memory.

For `Close`:

1. **Stop delivery, and abandon a goroutine blocked in `Read`.** A per-instance stop flag, set under `asyncHandles.mu`, is checked by every delivery under the same lock, and a stop channel aborts a delivery waiting for the run slot. Nothing waits for a goroutine in `Read`.
2. **Close the reader** to interrupt the `Read`. *Costs:* the stdio objects are the process's, and are deliberately not `io.Closer` (`outputStream`, `inputStream`). Closing stdin would end it for the embedder too.

## Choice

1. **`stream.read` is bound, in its async form** (`streamRead`, `async_stream.go`).
   - The decoder keeps 0x0f's type index and canonopts, so the read's caller is bound to the memory it names.
   - It traps unless the end is IDLE, and traps on a range outside memory, before any state changes. A length over `Buffer.MAX_LENGTH` traps too.
   - **With the writer gone**, the end's pending event is delivered inline: DROPPED.
   - Otherwise it clears the last copy's state, as `streamWrite` does (ADR 0120), and follows `End.copy`'s arms. A parked write of 0 is completed with progress 0 and the read parks. A read of 0 at a parked write completes itself with progress 0. A read at a parked write copies `min` inline, completes both, and returns COMPLETED with its progress. With no write parked, it parks and returns BLOCKED.
   - A guest-to-guest copy reads its source through the **reader's** caller: both ends are in one instance and one memory, and the reader's caller is the one bound to it. A copy into a pending read of 0 from a write of n > 0 notifies the reader with 0 and parks the write (the pin's third arm), where it completed both with 0.
   - **The sync form is refused at instantiate, by name**, as an unbuilt async surface. wasi-libc and Rust std only async-read.
   - **A `stream.read` whose element is not one byte is refused at instantiate, by name.** The copy moves bytes, and the pin's stride is `elem_size`.
   - Its classification moves to `async-built-undemanded`, and `isBuiltAsyncBuiltin` admits 0x0f.
2. **A host-held writer** (option 1). `writableStreamEnd.producer` marks an end whose source is a host producer. It is never in the guest's table, and it never parks a write: the producer's buffer is its source. `driveStreamCopyLocked` gains the arm that copies `buf[:min(readN, len(buf))]` into the reader, and the rest stays buffered for the next read. So no byte read from stdin is lost to a short read or a cancel.
3. **#1264: a drop unlinks both ends** (`End.drop`).
   - `stream.drop-writable` (and a producer's finish, through the same `dropWriterLocked`) clears `conn` on both sides, records `writerDropped` on the reader, and notifies the reader with DROPPED unless it is DONE or has an event pending. An idle reader is notified too.
   - The drop is a **flag** on each side (`writerDropped`, `readerDropped`) and not `conn == nil`, because an end can be built with no partner and not be one whose partner dropped: a writable end an async lower mints for a stream result has none from the start, and the harness tests build readable ends with none.
   - Both ends' `pendingEventLocked` compute DROPPED at the take when the other end has dropped, so an untaken COMPLETED keeps its progress and reports DROPPED (def:StreamEnd.notify.stream_event:"if self.other is None:").
   - `stream.drop-readable` unlinks too, so a later `stream.write` on the writer completes DROPPED inline rather than parking, and a host producer learns that its reader is gone.
   - `write-via-stream` on a readable end whose writer has already dropped resolves its future `ok` inline, which is #1264's own case.
4. **The producer** (`hostproducer.go`).
   - `read-via-stream` is served for WASI 0.3 through `serve(m, wasiP3, …)` as a stream import, sync-lowered with a return pointer. It mints the readable end and then the future, in `lower_async_value`'s order, and writes the two i32 handles.
   - The guest's declared result must be `tuple<stream<u8>, future<P>>` with P the host's `stdioWriteResultType`, checked at instantiate. `checkConsumerPayload` becomes tuple-aware for a stream import marked as producing.
   - **Reads happen on demand** (option 1). At most one `Read` is in flight per stream. A pending read of 0 with an empty buffer starts a `Read` of the cap, since only a byte can answer whether one is available. The bytes it returns are kept for the next read.
   - **One new goroutine site:** `(*streamProducer).startReadLocked`, one goroutine per `Read`, which ends when its `Read` returns and its delivery is applied or discarded. It is authorised in the census by this ADR, on ADR 0095's precedent: what is new is who stores into the reader, not where the bytes land or what orders them.
   - **The goroutine's delivery takes ADR 0101's path** (option 1): queued in `hostStores` if the run slot's holder is in host code, and otherwise through `acquireRun` on a chain of its own, with the instance's stop channel as its abort. Under the slot and `asyncHandles.mu`, it appends to the buffer, copies into a pending read and arms it COMPLETED, and finishes the stream if the source has ended.
   - **The end of input:** a `Read` that returns `io.EOF` sets the stream's final value to `ok`, and any other error sets it to `err(io)`. The stream **finishes** once its buffer is empty: the future resolves to the final value, then the writer drops, so the reader sees DROPPED and wasi-libc's future read finds the value already there. Bytes that came with the EOF are delivered first, and the finish waits for the next read, so a COMPLETED is not turned into DROPPED under the guest.
   - **A read that arrives with the source already ended returns BLOCKED, with its DROPPED already pending.** The read parks and the stream finishes behind it, which is the host's drop ordered after the read: the pin's read parks at a writer with no buffer (def:End.copy:"self.buffer = buffer"), and the host's drop then notifies it. It is also wasmtime's reading on an empty stdin (`run empty`, `run devnull`). The first build finished first and returned DROPPED inline, which the pin admits too, but read-park's callback-lifted guest, which discards the read's return and waits for the event, then waited for an event already taken, until the hang guard. A nil `Host.Stdin` reaches this arm at its first read.
   - **A guest that drops the readable end** stops the producer, and the future resolves `ok`, as the WIT states. A `Read` in flight is discarded when it returns.
   - **A nil `Host.Stdin`** is an immediate end of input: the stream is minted already final, with no goroutine.
   - **Reads of one Host's stdin are serialised** by a per-Host lock, so two streams from two `read-via-stream` calls never call `Read` concurrently on a reader that is not safe for it. The WIT leaves concurrent streams *"implementation-specific"*.
   - `write-via-stream` handed a stream the host itself produces is refused at the call, by name: a host-to-host copy is not built.
5. **Close** (option 1, in E5's pattern).
   - `walker.close` stops the producers of every instance in the tree right after the host waits. The stop sets `producersStopped` under `asyncHandles.mu` and closes the stop channel.
   - **`Close` never waits on a goroutine blocked in `Read`.** That goroutine is abandoned. Whatever it reads after the stop is discarded, never stored, because every delivery checks the flag under the lock it stores under. This is `CanonCaller.Blocking`'s H-3 limit, now stated for streams: a source that never returns holds a goroutine, and no longer holds `Close`.
   - A delivery that passed its check finishes its store before the stop can take the lock, so no byte lands after `Close` has stopped the table. A delivery waiting for the run slot is aborted by the stop channel.
   - The reader is never closed (`inputStream` is deliberately not an `io.Closer`).
   - **A `Read` in flight is a waker** for the callback-lift park bound, as a pending host wait is (`hostWaitPendingLocked`): the bound does not end a wait the producer is still going to satisfy.
6. **The decoder keeps the `(memory …)` of `future.read`, `future.write` and `stream.write`** (finding 4), as it now keeps `stream.read`'s, so each one's caller is bound to the memory its canonopts name. Their type index is still dropped; see *Out of scope*.

### Divergences from wasmtime 49.0.3, recorded

None measured. Both p3cat readings, and every read-park row, match byte for byte. One arm is unmeasured: **a read served from bytes left in the buffer completes inline**, COMPLETED with its progress. Since every `Read` is sized at the guest's request, bytes are left only when a read is cancelled or replaced by a shorter one while its `Read` is in flight, which none of the readings does. wasmtime's second read in `twice` returns BLOCKED, and so does this host's, because nothing was read ahead. The inline arm is the pin's second-arriver copy (def:End.copy:"elif buffer.remain() > 0 and self.other.buffer.remain() > 0:").

### Out of scope, recorded

- **A producer goroutine abandoned in `Read` reads one more chunk** from the source when it returns. For `os.Stdin` that is bytes another reader of the process's stdin will not see. Interrupting it needs a reader that can be cancelled, which `io.Reader` is not.
- **`write-via-stream` of a host-produced stream** (a guest forwarding stdin to stdout without reading it) is refused at the call.
- **The writer side of `End.drop`'s idle notify:** a guest writer whose reader dropped while it was IDLE gets no pending event; its next `stream.write` completes DROPPED inline, which is the observable the pin gives, and a waitable-set wait on the idle writer alone sees nothing.
- **Stream element types other than one byte**, which `stream.read` refuses and `stream.write` does not check.
- **`future.write`'s type index is dropped at decode,** so `futureElemOf(cn.TypeIdx)` classifies type 0 whatever the operand names. Found reading the decoder for finding 4, and not repaired here: it is a defect of the future copy's element rule (#1006), whose fixtures pass because their future is type 0. To be filed.
- **The pin's sync `stream.read`**, which waits in the copy, refused at instantiate.

## Consequences

- **Witnesses:**
  - `TestP3CatMatchesWasmtime`, in both gate states, on three stdins: `p3cat.stdin`, the same bytes through an `io.Pipe` written in 1000-byte pieces (every `Read` short, some reads parked until the next piece), and none. It runs `testdata/p3cat/p3cat.wasm` under `gate:async` through `CallRun`, and compares stdout, stderr and the exit code with wasmtime 49.0.3's readings, `testdata/p3cat.reading` and `testdata/p3cat-empty.reading`, byte for byte. The guest is a dependency-free Rust `wasm32-wasip3` program (nightly 1.100.0, built offline by `p3cat/build.sh`, which also generates the stdin, takes 10 readings of each and refuses unless they are identical; a second build gave the same sha256). It reads stdin to its end with `read_to_end` and prints the byte count, a checksum, the line count and the first line.
  - `TestReadParkMatchesWasmtime`, in both gate states: `read-park.wasm`'s seven rows against `read-park.reading`.
  - `TestAStreamReadCancelledMidReadServesTheBufferedBytesNext`: the producer's `Read` is sized at the read's 8 bytes; the read is cancelled with the `Read` in flight, which returns 6 bytes; a read of 4 and then of 8 are served "abcd" and "ef" inline, with no second `Read`; then a `Read` at the end of input leaves the pending read's event DROPPED.
  - `TestAGuestDropOfTheProducedStreamResolvesTheFutureOK`: the guest cancels and drops the readable end with a `Read` in flight; the future reads `ok` inline, and the late `Read`'s bytes are stored nowhere.
  - `TestAWritersDropUnlinksItsReader` (#1264), at the built-ins, five rows: an untaken COMPLETED at the writer's drop is taken as DROPPED with its progress, and the reader is DONE; a `stream.read` after the drop is DROPPED inline; `write-via-stream` on a stream whose writer already dropped resolves its future; the writer's untaken COMPLETED at the reader's drop is taken as DROPPED with its progress; a `stream.write` after the reader's drop is DROPPED inline.
  - `TestAGuestStreamReadFollowsEndCopysArms`: a guest read at a parked write copies inline; the two zero-length arms from the reader's side; and a write at a pending read of 0 notifies the reader and parks.
  - `TestCloseAbandonsAProducerBlockedInRead`, also run under `-race` (and 20 times under it, with the other producer witnesses): `read-park.wasm`'s `start` leaves a read pending with the producer blocked in `Read` on an `io.Pipe` nobody writes; `Close` returns, and a write to the pipe after `Close`, which the abandoned `Read` takes, neither arms the reader nor writes its buffer.
  - `TestAPendingProducerReadOutlivesTheParkBound`: `read-park.wasm`'s `run` with `liftParkBound` at 5 ms and a source that answers after 60 ms reads 131120, wasmtime's `run abc`.
  - `TestStreamReadIsBoundAndItsSyncFormRefused`: an async 0x0f permits; `stream-read-sync.wasm` is refused, naming the synchronous `stream.read`, and `stream-read-u32.wasm`, naming the element.
  - `TestReadViaStreamMustBeDeclaredAsTheHostDeclaresIt`: `read-via-stream-u32.wasm` is refused, and `read-park.wasm` instantiates.
  - At the CLI, `TestRunAComponentReadsStdin`: `burroughs run p3cat.wasm < p3cat.stdin` exits 0 with the reading's stdout.
- **Neuters, run**, each named at its site:
  - 0x0f out of `isBuiltAsyncBuiltin`: p3cat is refused at instantiate, *"an async canon built-in (0xf)"*.
  - `driveStreamCopyLocked`'s host arm dropped: every read completes with progress 0, which wasi-libc takes as the end, and p3cat prints *"read 0 bytes"*.
  - `io.EOF` mapped to `err(io)`: p3cat prints *"reading stdin failed: I/O error (os error 29)"* and exits 1, on both read stdins.
  - the decoder dropping `future.read`'s memory again: p3cat's future read at the end of input traps, *"… has no memory 0"*.
  - `dropWriterLocked` restored to the old notify (only a pending read, ends left linked): three of `TestAWritersDropUnlinksItsReader`'s rows fail (the untaken COMPLETED read as 0x30 and IDLE, the read after the drop BLOCKED, `write-via-stream`'s future unresolved). **p3cat passes under it**, since its stream finishes with a read pending, which the old notify reached; the plan forecast a hang there, and it was not one.
  - the reader's DROPPED-at-the-take conversion dropped: the untaken COMPLETED is taken as COMPLETED, the reader IDLE. The writer's dropped: its COMPLETED is taken as COMPLETED.
  - `write-via-stream`'s dropped-writer arm dropped: its future is unresolved.
  - `streamWrite`'s reader-dropped arm dropped: the write after the reader's drop is BLOCKED.
  - `streamRead`'s write-of-0 arm dropped: the read at a parked write of 0 completes, 0x0. Its read-of-0 arm dropped: the parked write is completed too. `streamWrite`'s pending-read-of-0 arm dropped: the write of 3 completes inline, 0x0.
  - the sync-form refusal dropped: `stream-read-sync.wasm` instantiates, and `TestGateAsyncNarrowingPermitsLowerRefusesUnbuilt`'s sync row is permitted. The element check dropped: `stream-read-u32.wasm` instantiates.
  - the producer's element test (`!= VU8`) dropped: `read-via-stream-u32.wasm` instantiates.
  - `copyIntoLocked` dropping the rest after a copy: the read after the 4-byte one is BLOCKED, "ef" lost. `apply` discarding a delivery with no read pending: the read after the cancel is BLOCKED, all six bytes lost.
  - `streamDropReadable` not telling the producer: the future read is BLOCKED.
  - `walker.close` without `stopAllProducers`, and, separately, the delivery's two stop checks and its abort channel dropped: under `-race`, the closed instance's reader is armed and its buffer holds `"late"`.
  - `hostWaitPendingLocked` without the producer term: `run()` fails with `ErrLiftParkExpired`.
  - `readLocked` finishing first and taking the DROPPED inline at the end of input: `TestReadParkMatchesWasmtime`'s `none` rows hang until the 10 s guard.
- **Tests changed** (pre-existing):
  - `canonbuiltins_test.go`: `stream.read`'s status moves from `async-refused-by-name` to `async-built-undemanded`, which `TestEngineBuiltSetEqualsTheClassification` requires once 0x0f is built.
  - `async_lower_test.go` (`TestGateAsyncNarrowingPermitsLowerRefusesUnbuilt`): its stream.read row's comment, which said stream.read is unbuilt. The row is unchanged: its `Canon` has `AsyncForm` false, the sync form, which still refuses with `ErrAsyncNotImplemented`.
  - `internal/testenv/observer_test.go`: the census gains the producer's site, naming this ADR.
  - Every other suite passes unchanged, with and without `-race`: the stream, future, cancel, waitable-set, liftpark and runslot suites, p3std, p3sys, p3sleep, p3async-hello, p3hello and p3echo.
- **What a guest sees.** A WASI 0.3 std guest's `std::io::stdin()` reads the host's stdin in order and to its end, and `Close` returns with a read still blocked.
- **No contract text changes, and no public API.**

## Rollback

Each of the following restores its defect, and each is a neuter run against its witness:

- refusing 0x0f again brings back p3cat's refusal at instantiate;
- leaving `conn` set at a writer's drop brings back #1264's parked read and unresolved future;
- dropping `stopAllProducers` from `walker.close` lets a late `Read` store into a closed instance.
