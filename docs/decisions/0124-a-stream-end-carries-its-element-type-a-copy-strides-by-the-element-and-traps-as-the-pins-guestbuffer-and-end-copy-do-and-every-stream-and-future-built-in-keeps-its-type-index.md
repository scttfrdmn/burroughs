# 0124 — A stream end carries its element type, a copy strides by the element and traps as the pin's GuestBuffer and End.copy do, and every stream and future built-in keeps its type index

Date: 2026-11-14 · Status: **accepted**, 2026-11-14. Scott, in session, approving the plan for v0.14.0's tenth slice (E7b-1, the first of #1277's two, absorbing #1275 and #1271). The approval was given in session and has no comment to cite, so the record is the actor's, and commits resting on it are `Ratio-Class: carried`. It is an engine mechanism inside `internal/component`: no contract text, no public API (no root-package file changes and no exported symbol), and no stamped ADR is reversed. It **retires ADR 0123's choice 7**, the refusal of a non-byte `stream.read` at its call, which that choice said E7b would replace (*"E7b builds the read (#1277)"*); ADR 0123 carries a postscript pointing here. Filed against [#1277](https://github.com/scttfrdmn/burroughs/issues/1277), [#1275](https://github.com/scttfrdmn/burroughs/issues/1275) and [#1271](https://github.com/scttfrdmn/burroughs/issues/1271).

## Context

E7b (#1277) is `bind`, `listen` and the accept path, and `listen` returns `stream<own<tcp-socket>>`. On main at 7ffc135 streams were byte streams:

- **A stream end carried no type.** `writableStreamEnd` and `readableStreamEnd` had no element, so the pin's end-type trap (def:copy:"trap_if(end.t != stream_or_future_t.t)", and the same line in def:cancel_copy and def:drop) was not modelled.
- **The copy moved bytes.** `driveStreamCopyLocked` copied `min(read_n, write_n)` bytes, where the pin's guest buffers stride by `elem_size` (def:ReadableGuestBuffer.read, def:WritableGuestBuffer.write, through def:load_list_from_valid_range and def:store_list_into_valid_range).
- **`stream.write` had none of def:GuestBuffer's traps**: a buffer not aligned to its element, one past the end of memory, one longer than def:Buffer:"MAX_LENGTH = 2**28 - 1". `stream.read` checked a range of `n` bytes, not `n` elements, and no alignment.
- **A non-byte `stream.read` was refused by name at its call** (ADR 0123 choice 7), and **`stream.write` did not check its element at all**, so a guest's `stream<u32>` write copied a count of bytes (#1275).
- **The decoder dropped the type index** of `stream.write`, `future.read` and `future.write` (one shared case), and of `stream.new`, the drops and the cancels. So `futureElemOf` always classified type 0's element (#1271): `TestAFutureCopyWithinAnInstanceFollowsThePinsElementRule` passed only because each fixture's future was type 0.

The pin's rule for a copy between two ends of one instance is def:End.copy's:

```
trap_if(self.owner and self.owner is self.other.owner and not none_or_number_type(self.t))
```

so within one instance only numbers copy (`none_or_number_type`: no element, an integer, or a float). A host-produced end has no owner, and its own-handle elements are lowered with def:lower_own, a mint.

### What the oracle does, measured

wasmtime 49.0.3 (`.oracle/wasmtime-49.0.3/wasmtime`, never the 50.0.1 on PATH), darwin/arm64, `wasmtime run -W component-model-async=y --invoke '<export>()'`, **10 of 10 runs identical on every row**, on two new fixtures, each export one sequence returning one u32 (the readings are `testdata/stream-typed-copy.reading` and `testdata/stream-typed-traps.reading`):

- **`stream<u32>`, write 3 then read 4, and read 4 then write 3:** the second arriver returns 48 (COMPLETED, progress 3 elements), and the reader's buffer holds the three words whole, its fourth word 0. A read of 2 returns 32 and leaves the third word untouched. A read through a second type index, also `stream u32`, copies (48).
- **`stream<f32>` and `stream<f64>` NaNs:** wasmtime preserves the payload (0x7fc00001; the f64's low word 1). 1.5 copies unchanged.
- **`stream<u8>`**, the control: 48, bytes in place.
- **A buffer out of range** (two u32 at 65532 of one page): both `stream.write` and `stream.read` trap, *"read pointer out of bounds of memory"*; at 65528 they park.
- **A buffer of 2^28 elements:** *"stream read/write count too large"*.
- **A `stream<u32>` end through a `stream<u8>` built-in**: `stream.write`, `stream.read`, `stream.cancel-write`, `stream.drop-writable` and `stream.drop-readable` each trap, *"handle is a stream of a different type"*.
- **A same-instance copy of `stream<string>`, or of `stream<own<R>>`**: the second arriver traps, *"cannot read from and write to intra-component future/stream with non-numeric payload"*, whichever side it is; the write that parks before it does not.
- **A misaligned buffer** (a `stream<u32>` buffer at 2, one element): **wasmtime does not trap**; the read and the write park.
- **#1271:** `future-self-copy-bool-type1.wasm`, `future-self-copy-bool` with a `future<u8>` declared first, so its `future<bool>` is type 1: the same non-numeric trap as at type 0.

### What the pin does

Read from `definitions.py` (the vendored pin), by constructing each export's buffer with def:WritableGuestBuffer or def:ReadableGuestBuffer over one 64 KiB page (uv-managed Python 3.14.8, the pin's `trap` replaced by a bare raise since a lone buffer has no instance to lock down): **the misaligned read and write of one element trap**, of 0 elements do not (*"if t and length > 0:"*); 65532 by two traps and 65528 does not; 2^28 traps. `U32Type() == U32Type()` is true, so the end-type check compares types, not indices. `none_or_number_type` is false for `string` and `bool`.

## Options

For the end type:

1. **Each end carries an element descriptor**, stamped at `stream.new`, and every stream built-in compares it with its own, structurally (`canon.TypeEqual`), as the pin's dataclass equality does.
2. Compare type indices. *Costs:* two indices naming `stream u32` are one type to the pin and to wasmtime (the equal-type row), so this traps a correct guest.

For the element rule within an instance:

1. **Trap at the copy**, as def:End.copy does, from whichever side arrives second.
2. Refuse a non-numeric element at the call. *Costs:* the write that parks before any reader does not trap in either oracle, and a host reader of a guest's stream is legal.

For floats: the pin's deterministic outcome (canonical NaN) or wasmtime's preserved payload. wasmtime's is inside the pin's set; `futureWrite` already takes the deterministic outcome (`future-self-copy-elems.reading`).

## Choice

1. **The type index survives decode** for every stream and future built-in, 0x0e to 0x1b (`instantiate.go`): `Canon.TypeIdx`, carried to the binding as `asyncBuiltinSlot`. That repairs #1271 at its line.
2. **One element descriptor** for streams and futures, `elemType` (`async_elem.go`), generalising `futureElem`: the element as the codec types it (`canonTypeIn` with the host's resource RTs), its size and alignment (the codec's own `canon.ElemSize` and `canon.Alignment`, now exported so there is one layout), whether it is a float, whether `none_or_number_type` holds, and **an `own` arm**: the host resource an `own<R>` element names, resolved as `resource.drop` resolves its type (`w.hostResourceNamed`). Nothing reads the arm yet; E7b-2's accept stream lowers its elements through it. A stream with no element is numeric with size 0. `byteElem` is `u8`'s, which every host-minted stream end carries.
3. **A stream end carries its descriptor**, stamped at `stream.new` on both ends, by a host producer on its two (`u8`), and by an async lower's stream result (`u8`, the only one a host import has). **Every stream built-in traps unless its descriptor is its end's**: `stream.read`, `stream.write`, both cancels and both drops (def:copy, def:cancel_copy, def:drop), after the handle's kind and before the state, the pin's order.
4. **Guest buffers follow def:GuestBuffer**, as traps, in the pin's order after the end-type and IDLE checks: longer than MAX_LENGTH whatever its length; then, for a length above 0, a pointer not aligned to the element, and a range of `ptr + n * size` past memory. `stream.read` and `stream.write` both. A `stream.write` on an end that is not IDLE is a trap too (def:copy:"trap_if(end.state != End.State.IDLE)"); it was a Go error.
5. **The copy strides by element** (`driveStreamCopyLocked`): `min(read_n, write_n)` elements, `n * size` bytes. **A float element is NaN-canonicalised per element**, as `futureWrite` does, and **a non-numeric element between two guest ends traps at the copy** (*"non-numeric payload"*, wasmtime's phrase and the future copy's). Two guest ends are one instance's, since a stream end never leaves the table it was minted in: no built-in or lift moves one.
6. **ADR 0123's interim refusal is retired.** A non-byte `stream.read` or `stream.write` is no longer refused by name: a numeric element copies, and a non-numeric same-instance copy traps per the pin. **An element the codec does not model** (a `stream<stream<T>>`, a `stream<future<T>>`, a `stream<error-context>`) is still refused at its call, by name, and a type index that names no stream is too: a refusal is the honest answer where no layout exists, and bound is not run.
7. **Host consumers and producers stay `u8`.** A producer's stream is checked at instantiate (`checkConsumerPayload`'s tuple test). **A consumer's is not**: nothing checks `write-via-stream`'s or `send`'s `data: stream<T>` parameter, which the plan forecast as checked. So the host sink refuses, by name, a stream whose element is not one byte (`driveStreamCopyLocked`'s sink arm), rather than handing a `stream<u32>`'s words to stdout as bytes.

### Divergences from wasmtime 49.0.3, recorded

- **A misaligned buffer:** the pin traps (def:GuestBuffer), wasmtime 49.0.3 parks. The pin's answer is taken (ADR 0102); `stream-typed-traps.reading`'s `pin` rows.
- **A float's NaN payload:** wasmtime preserves it, the pin canonicalises it under DETERMINISTIC_PROFILE and randomises it otherwise; Burroughs takes the canonical NaN, as for futures. wasmtime's reading is inside the pin's set.

### Out of scope, recorded

- `bind`, `listen`, the accept producer, the backlog setter and the lock order they need (E7b-2, #1277). The `own` arm is its input.
- **A future end's type.** The index now survives decode for every future built-in, but a future end does not carry its type, so def:copy's end-type trap is not modelled for futures; their host-minted ends (every async lower's and every consumer's future) would each need a type stamped. And `future.write`'s copy has no def:GuestBuffer traps (a one-element buffer's alignment and range). Neither is a stream's, and no witness here reaches them.
- The pin's sync `stream.read` and `stream.write` (#1272), `stream.forward` and `future.forward`.
- `stream.cancel-write` on a write whose copy has completed but whose event is untaken: the pin returns the event (def:cancel_copy, `has_pending_event`), this engine traps "no write in flight". Found, not touched.
- A stream end's kind check (a handle that is not a readable or writable stream end) answers a Go error where the pin traps (def:copy:"trap_if(not isinstance(end, EndT))"). Found, not touched.
- The future end's type, this cancel-write case and the kind check are filed together as #1282. A host consumer's `data: stream<T>` parameter, refused at the copy here rather than at instantiate, is recorded on #1279. Whether a cross-component call passes a handle as a raw index into the wrong instance's table, which was suspected while this slice was built and not confirmed, is #1281.

## Consequences

- **Witnesses:**
  - `TestTypedStreamElementsMatchTheOracle`, in both gate states: every row of `stream-typed-copy.reading` and `stream-typed-traps.reading` (38), wasmtime 49.0.3's, with the pin's answer on its three divergent float rows and its two misaligned rows. Each trap row is held to its own phrase.
  - `TestAFutureCopyWithinAnInstanceFollowsThePinsElementRule` gains `bool-type1` (#1271), against `future-self-copy-bool-type1.wasm` and its row in `future-self-copy-elems.reading`.
  - `TestAHostConsumerReadsOnlyAStreamOfBytes`: a `stream<u32>` handed to `write-via-stream` is refused at the copy by name, and nothing reaches the sink; a `stream<u8>`, the control, writes its 2 bytes.
- **Neuters, run**, each named at its site:
  - the type index dropped at decode again (`keepIdx` leaving `TypeIdx` 0): `bool-type1` reads 262146 (0x40002), type 0's `u8` copied.
  - the copy moving `n` bytes: `u32-wf-w0` and `u32-rf-w0` read 2241348 (0x00223344) and `w1`/`w2` read 0, and the f32 and f64 rows read partial words.
  - no NaN canonicalisation: `f32-wf-w0` and `f32-rf-w0` read 2143289345 and `f64-wf-lo` 1, wasmtime's readings.
  - no alignment check: `misaligned-read` and `misaligned-write` park, BLOCKED, wasmtime's readings.
  - the range checked as `n` bytes: `out-of-range-read` and `out-of-range-write` park.
  - no MAX_LENGTH check: `max-length-write` **still traps**, for its range (2^28 u32 elements pass the end of one page), and fails on its phrase only. A range-free witness needs a memory of a GiB.
  - no end-type check: `mismatch-write` and `mismatch-read` park, `mismatch-cancel-write` returns CANCELLED (2), and both mismatched drops succeed.
  - no same-instance element check: `string-copy`, `string-copy-read-first` and `own-copy` complete with progress 1.
  - no host-sink element check: the `stream<u32>` write hands its 8 bytes to the sink.
- **Tests changed** (pre-existing):
  - `TestStreamReadIsBoundAndItsSyncFormRefused` (`hostproducer_test.go`): `stream-read-u32.wasm`'s read is no longer refused for its element; it is bound, and fails for its handle (1, which the instance does not hold). Its neuter line for ADR 0123's element check goes with the check. The fixture's header comment says so; the reassembled `.wasm` is byte-identical.
  - The stream built-ins take the element they name, so every unit test that calls one directly passes `byteElem`, and every end it builds by hand carries it (`async_stream_test.go`, `host_future_test.go`, `hostproducer_test.go`, `p3tcp_test.go`, `stream_idle_test.go`). No assertion changed.
  - Every other suite passes unchanged, with and without `-race`: p3cat, p3tcp, p3std, p3sleep, p3sys, p3async-hello, p3hello and p3echo, and the stream, future, cancel, waitable-set and producer suites. No std guest's reading changed.
- **What a guest sees.** A guest's own stream of any numeric element copies, at its element's stride, and a stream of anything else traps at the copy, as the pin and wasmtime both do. wasi-libc's `stream<own<tcp-socket>>` read is bound as a read, not as a refusal, and nothing yet produces such a stream.

## Rollback

- Restoring the byte copy copies a quarter of a `stream<u32>` and leaves the GuestBuffer traps behind.
- Dropping the end-type check lets a built-in of one element type read or write an end of another at its own stride.
