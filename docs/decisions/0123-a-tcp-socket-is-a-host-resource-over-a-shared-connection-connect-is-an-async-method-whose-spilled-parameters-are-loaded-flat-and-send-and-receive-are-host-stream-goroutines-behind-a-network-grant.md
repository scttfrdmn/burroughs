# 0123 — A tcp-socket is a host resource over a shared connection, connect is an async method whose spilled parameters are loaded flat, and send and receive are host stream goroutines behind a network grant

Date: 2026-11-13 · Status: **accepted**, 2026-11-13. Scott, in session, approving the plan for v0.14.0's ninth slice (E7a, the first of #1235's three), and his public-API stamp on [#1235](https://github.com/scttfrdmn/burroughs/issues/1235) for `ComponentConfig.InheritNetwork` and `burroughs run --inherit-network`. The plan approval was given in session and has no comment to cite, so that half of the record is the actor's, and commits resting on it are `Ratio-Class: carried`. The stamp's limit is ADR 0097's form: this slice adds that one field and that one flag to the public surface and nothing else. No contract text changes, and no stamped ADR is reversed: ADR 0122's refusal of a non-byte `stream.read` is kept and moved from instantiate to the call (choice 7). E7b ([#1277](https://github.com/scttfrdmn/burroughs/issues/1277)) and E7c ([#1278](https://github.com/scttfrdmn/burroughs/issues/1278)) are the other two slices.

## Context

A `wasm32-wasip3` Rust std program reaches TCP through wasi-libc, which lowers `wasi:sockets/types@0.3.0`'s `tcp-socket` (the WIT the nightly's std vendors, `wasip3-0.8.0+wasi-0.3.0`, `sockets.wit`). Read from a std guest that runs `TcpStream::connect` with `wasm-tools print`, and from wasmtime's trace of the same guest (`WASMTIME_LOG=wasmtime_wasi=trace`):

- `[static]tcp-socket.create(address-family) -> result<tcp-socket, error-code>`, sync, with a return pointer.
- `[async-lower][method]tcp-socket.connect(remote-address: ip-socket-address) -> result<_, error-code>`, the only async tcp method. Its flat parameters are 13 (the borrow and a 12-word variant), past `MAX_FLAT_ASYNC_PARAMS = 4`, so the core function takes **one pointer to the parameter tuple** and a return pointer (def:flatten_functype, def:lift_flat_values).
- Straight after a successful connect, wasi-libc calls `receive() -> tuple<stream<u8>, future<result<_, error-code>>>`, stdin's shape, and `send(data: stream<u8>) -> future<result<_, error-code>>`, write-via-stream's shape, with a stream it made with `stream.new`. Both are sync `[method]`s.
- `local_addr()` and `peer_addr()` are `get-local-address` and `get-remote-address`, each `result<ip-socket-address, error-code>` at a return pointer. `shutdown(Write)` drops the send stream's writable end. A dropped `TcpStream` drops the handle.
- `error-code` is a **variant**: 14 payload-less cases and `other(option<string>)`.
- **The tcp object links `listen`'s accept path into every program that uses a socket**, so the guest declares `canon stream.read` of `stream<own<tcp-socket>>` whether or not it listens.

On main at 8036b2f such a guest was refused at instantiate, *"canon stream.read of type 44, whose element is not one byte"* (ADR 0122), and past that, its first `create` reached the stub, *"import wasi:sockets/types@0.3.0::[static]tcp-socket.create is not provided"*.

`sockets.wit` states **shared ownership**: the handle and the streams derived from it reference one OS socket, the streams stay usable after the handle is dropped, and the socket closes only when the last of them goes.

### What the oracle does, measured

wasmtime 49.0.3 (`.oracle/wasmtime-49.0.3/wasmtime`, never the 50.0.1 on PATH), darwin/arm64, `wasmtime run -S p3 … p3tcp.wasm <echo-port> <closed-port>`, against the echo listener `p3tcp/build.sh` starts from `testdata/p3tcp/echo/`:

- **`-S inherit-network`:** 6 of 6 runs byte-identical, exit 0, empty stderr. The connect succeeds, the local address is v4 loopback, the remote is v4 loopback at argv's port, the 102400 bytes come back equal, and the connect to the closed port is `ConnectionRefused`. The trace shows `create` ok, `connect` ok, `receive`, `send`, the two getters, then the second socket's `create` ok and `connect` `Err(ErrorCode::ConnectionRefused)`.
- **No network flag:** exit 0, and both connects print `PermissionDenied`. The trace shows **`create` returning `Err(ErrorCode::AccessDenied)`**, for both sockets, with no connect called.
- `-S tcp=y` without `-S inherit-network`: `create` ok and `connect` `Err(AccessDenied)`. `-S tcp=n -S inherit-network`: `create` `Err(AccessDenied)`. Neither is a state this host has: Scott's stamp mirrors `inherit-network` alone.
- `-S p3` changes nothing in these readings; the 0.3 host is on for a `wasm32-wasip3` component.

The plan forecast the grant-off code at connect, under `-S tcp`. Measured under the switches the witness uses on both engines (none, against `--inherit-network` absent), it is at `create`.

## Options

For the spilled parameters:

1. **Load the tuple's flat words** from memory: the alignment and bounds traps of def:lift_flat_values, then each leaf read at its offset and laid out as its flat form, so the impl gets the 13 words it would get unspilled. A new codec function, `canon.LoadFlat`.
2. **Load the tuple with `canon.LoadVia`**, which gains `own`/`borrow` arms, and hand the impl values. This is what the plan named. *Costs:* every async impl takes flat words today (40 sites), so either the impl signature changes or the values are re-flattened; a re-flattened string would have to be stored again, through the guest's realloc, from host code; and the impl would need value accessors (variant case, `u16`, `u8`) the codec does not export. So `LoadVia` is unchanged, and `own` stays its one documented asymmetry.

For send's sink:

1. **A writer goroutine over a bounded host buffer.** A guest write copies what fits into the buffer under the lock, as write-via-stream's sink does, and never blocks; the goroutine drains the buffer to `conn.Write`.
2. **Write inline**, write-via-stream's sink. *Costs:* `conn.Write` blocks once the kernel's buffer fills, under `asyncHandles.mu` and the run slot, so a peer that echoes while the guest is still writing deadlocks the instance.

For the shared OS socket:

1. **A reference count** on the connection: the handle, the receive stream and the send stream each hold one, and the last release closes it.
2. **The handle owns the socket.** *Costs:* dropping the handle before the streams, which the WIT allows, kills them.

For the grant off: refuse at `create` (wasmtime's reading with no network flag) or at `connect` (its reading under `-S tcp`).

## Choice

1. **`tcp-socket` is a host resource** (`tcpSocketResource`, `hostsock.go`), declared for 0.3 in `resourceSource()`. `create` mints an own over a `tcpSocket` in the unbound state; no OS socket exists until `connect`. Its destructor releases the handle's reference to the connection.
2. **The connection is reference-counted** (option 1, `sockConn`). Connect's success takes the handle's reference; `receive` and `send` take one each and release it when their stream is finished. The last release closes the `net.Conn`. **A per-instance registry** (`hostNet`, in the instance's `hostResources`) holds every live connection and every pending dial, so `Close` can reach them.
3. **An async resource method.** `asyncImplFor` is called with the instance's resource table as well as its async table, so `[async-lower][method]tcp-socket.connect` can lend `self`. The lend is held for the subtask's life and released when the subtask resolves, is cancelled, or is stopped by `Close` (def:Subtask.deliver_resolve releases a subtask's lenders at its resolution).
4. **Spilled async parameters** (option 1). When an async lower's parameters flatten past `MAX_FLAT_ASYNC_PARAMS` (4), the walk binds it with the parameter tuple's type, and the lower loads the tuple's flat words from the one pointer it is passed (`canon.LoadFlat`): it traps on a pointer not aligned to the tuple, or a tuple past the end of memory, and returns the words in the order def:flatten_type lays them out, a variant's payload coerced to the joined types and padded. The impl's `onStart` returns those words, so it is the same impl whether its parameters spilled or not. General, not connect's: any async import past 4 flat parameters takes this path. A string or list leaf yields its `(ptr, len)` pair as it lies in memory, so its bytes are read, and their traps raised, when the impl lifts them rather than at the spill.
5. **connect** dials on a goroutine (`net.Dialer.DialContext`, the one new dial site) and resolves through `onResolve`, ADR 0101's path. One claim decides who resolves, E5's pattern (`hostwait.go`): the dial's return, the guest's `subtask.cancel` (which cancels the context and resolves CANCELLED_BEFORE_RETURNED inline), or `Close` (which cancels it and resolves nothing). A pending dial is a waker for the park bound. On success the socket is connected and its local and remote addresses are recorded; on failure it is closed, as the WIT states (*"After a failed connection attempt, the socket will be in the `closed` state"*). A connect on a socket that is not unbound is `invalid-state`, and a remote address of the wrong family, unspecified, IPv4-mapped, or with port 0, is `invalid-argument`, both inline.
6. **receive is ADR 0122's producer, generalised.** Its source, the future's `ok`, the error mapper and a stop hook become the producer's parameters, and its goroutine is started through the registry so `Close` can wait for it. stdin builds its producer with the values it had, and keeps every reading. For a socket:
   - the source is the connection, and EOF is `ok` (*"after a graceful shutdown from the peer"*); any other error maps to its `error-code`;
   - the guest's drop of the readable end calls `CloseRead` (*"equivalent to calling `shutdown(SHUT_RD)`"*) and releases the stream's reference, as does the stream's finish;
   - **`Close` closes the connection rather than abandoning the read**, so this goroutine, unlike stdin's (#1274), is waited out;
   - a second `receive`, or one on a socket that is not connected, returns a stream that is already finished and a future of `err(invalid-state)`, as the WIT states.
7. **A `stream.read` of a stream whose element is not one byte is refused at its call**, by name, rather than at instantiate. ADR 0122's refusal is kept, since the copy moves bytes and the pin strides by the element's size; it is moved because wasi-libc links `listen`'s `stream<own<tcp-socket>>` read into every socket program, and a refusal at instantiate refused them all. An import the host does not serve is refused at its call for the same reason. E7b builds the read (#1277).
8. **send** (option 1) is a stream consumer whose sink is a writer goroutine (`sockSend`).
   - The host's read of the guest's stream is write-via-stream's host read, with `readN` the buffer's free space (64 KiB in all), so a guest write copies what fits, under the lock, and completes with that progress. With the buffer full, no host read is pending and a guest write parks.
   - The goroutine waits for bytes on a condition over `asyncHandles.mu`, writes them with no lock held, then delivers under ADR 0101's discipline: the freed space re-arms the host read and drives a parked write's copy, which reads guest memory, under the run slot.
   - **The writer's drop** drains the buffer, then `CloseWrite`, then resolves the future `ok` (*"Closing the stream is equivalent to calling `shutdown(SHUT_WR)`"*), then releases the stream's reference.
   - **A write error** resolves the future with the mapped `error-code`, completes a parked write DROPPED and unlinks the stream, so later writes complete DROPPED inline.
   - A second `send`, or one on a socket that is not connected, resolves its future `err(invalid-state)` inline and takes nothing.
9. **Getters:** `get-local-address`, `get-remote-address` (`invalid-state` until connected), `get-address-family` and `get-is-listening` (false: E7a never listens). The other twenty methods are left unserved and reach the stub at their call.
10. **Error mapping:** a Go `net` error maps to `error-code` by its errno: `connection-refused`, `connection-reset`, `connection-aborted`, `connection-broken` (EPIPE), `timeout`, `remote-unreachable`, `address-in-use` and `access-denied`, with Windows' WSA numbers mapped beside the POSIX ones. Anything else is **`other(none)`**: a string payload would be lowered through the guest's realloc from a goroutine's store, and the future payloads stay realloc-free.
11. **The grant.** `Host.InheritNetwork`, set from `ComponentConfig.InheritNetwork` by `Run` and `LoadComponent`, and by `burroughs run --inherit-network`. **Off, `create` is `err(access-denied)`**, wasmtime 49.0.3's reading with no network flag (measured above), and `connect` checks the grant again. A bare `LoadComponent` stays denied. `--inherit-network` on a wasip1 command or a core module is refused by name, as #1128 refuses the other component-only flags.
12. **Close** (`walker.close`) stops the sockets of every instance in the tree right after the producers: under the registry's lock it marks the registry stopped, so no socket goroutine starts after it, claims every pending dial (cancelling its context and releasing its lend), and closes every connection, which unblocks each reader's `Read` and each writer's `Write`. Every socket goroutine — dial, send writer, receive reader — is counted in the registry, and **`Close` waits for all of them at its end**, after the core instances have closed, as it waits for a host wait's callback. A delivery stopped by the stop discards what it carried. A send writer waiting for bytes waits on a condition over `asyncHandles.mu` (`hostIO`), which the producers' stop broadcasts, so no channel operation is made under the engine lock.
13. **`wasi:filesystem/preopens`' `get-directories` is served for 0.3 too**, and `descriptor` is declared for it. It was 0.2 only (ADR 0119), and p3tcp reached it: wasi-libc looks up the preopens when a program that has used a socket closes one, which wasmtime's trace of the same guest shows (`get-directories` between the first socket's getters and the second's `create`). The 0.3 WIT is the 0.2 shape, `list<tuple<descriptor, string>>`, and nothing is granted, so the list is empty, as wasmtime's is with no `--dir`.

**Lock order:** `asyncHandles.mu` may be held while the registry's lock or a socket's is taken, and never the reverse. A resource lend is taken and released with neither held.

**Names added below the public boundary.** `canon.LoadFlat` (internal codec) and `component.Host.InheritNetwork` (internal; `ComponentConfig` sets it, as it sets `Host.Args`). Neither is importable from outside the module. The public surface is the stamped field and flag only.

### Divergences from wasmtime 49.0.3, recorded

None measured on the witness's paths. Unmeasured: an error mapped to `other(none)` that wasmtime maps to a specific code, or carries a string for; `get-local-address` while connecting (this host answers `invalid-state` until connected); the twenty unserved getters and setters; IPv6.

### Out of scope, recorded

- `bind`, `listen` and the accept stream, typed stream elements and the lock order they need (E7b, #1277).
- `ip-name-lookup` and `AllowNameLookup` (E7c, #1278).
- UDP, and the keep-alive, hop-limit and buffer-size options.
- A check that an async import's result type agrees with the host's, as `checkConsumerPayload` checks a stream import's future: connect's `result<_, error-code>` is lowered at the host's declaration, which is the guest's when both come from one WIT, and nothing compares them.

## Consequences

- **Witnesses:**
  - `TestP3TcpMatchesWasmtime`, in both gate states: `testdata/p3tcp/p3tcp.wasm`, a dependency-free Rust `wasm32-wasip3` std program (nightly 1.100.0, built offline by `p3tcp/build.sh`, the same sha256 on a second build), connects to an in-test echo peer, checks its local and remote addresses, writes 100 KiB, shuts its write half, reads the echo to the end, and connects to a closed port. Granted, its stdout, empty stderr and exit 0 equal `p3tcp.reading`; denied, `p3tcp-denied.reading`. Both readings are wasmtime 49.0.3's, 10 of 10 runs byte-identical (also 6 of 6 in the measurement above). The guest prints properties, never a port. After `Close` no socket goroutine remains.
  - `TestTcpConnectParkMatchesWasmtime`: `tcp-connect-park.wasm`, a hand-written callback-lifted task that connects through the spilled async lower and waits for the subtask, reads 66048 against a listening port and 66049 against a closed one, wasmtime's `tcp-connect-park.reading` (10 of 10 per row).
  - `TestCloseEndsASocketsGoroutines`, also under `-race`: p3tcp parked in receive (the peer drains and holds the connection open) and parked in send (a peer that never reads, 4 MiB); `Close` returns, the run ends, and no socket goroutine remains.
  - `TestACancelledConnectResolvesCancelledAndItsDialEnds`, also under `-race`: a held dial, cancelled by the async `subtask.cancel`, reads CANCELLED_BEFORE_RETURNED inline, its context is cancelled, its goroutine ends, and the socket can be dropped.
  - `TestTheConnectionOutlivesTheHandleWhileAStreamHoldsIt`, `TestAReceiveDropShutsTheReadHalf`, `TestTheNetworkIsDeniedAtCreateWithoutTheGrant`, `TestAPendingDialOutlivesTheParkBound` (`tcp-connect-park.wasm` with the bound at 5 ms and a 60 ms dial), `TestAnAsyncLowerLoadsItsSpilledParameters` (`async-spill-params.wasm`, five flat words of four types, and its two traps) and, in the codec, `TestLoadFlatIsLoadThenLowerFlat` (LoadFlat against the model's `lowerFlat`).
  - At the public boundary `TestInheritNetworkReachesBothConfiguredPaths` (Run and LoadComponent); at the CLI `TestRunAComponentWithInheritNetworkConnects` and `TestInheritNetworkIsAComponentsFlag`.
- **Neuters, run**, each named at its site:
  - `tcpCreate`'s grant check dropped: `TestTheNetworkIsDeniedAtCreateWithoutTheGrant`'s create returns ok. **p3tcp's denied rows pass under it**: connect's own check answers access-denied, and std maps both to PermissionDenied.
  - the walk binding connect with no spill: p3tcp fails, *"tcp-socket.connect: got 1 flat parameters, want 13"*, and the spill fixture's impl is handed one word. The lower skipping its load: the same fixture result.
  - the dial's lend never released: p3tcp traps dropping its socket, *"handle has 1 active lends"*.
  - the writer's `CloseWrite` skipped: p3tcp's `read_to_end` waits until the 20 s hang guard.
  - `get-directories` served for 0.2 only: p3tcp fails at the first socket's close, the cross-track refusal, before the closed-port line.
  - **the send sink writing inline: p3tcp passes**, which the plan forecast as a deadlock. 100 KiB fits the loopback's kernel buffers both ways (about 750 KB to a silent peer on this machine, measured), so the inline Write never blocks. `TestCloseEndsASocketsGoroutines`' send row fails: the guest never parks, its `stream.write` blocked in the Write with `asyncHandles.mu` held.
  - `walker.close` without `stopAllSockets`, and `hostNet.stop` returning before it closes the connections: the receive row's `Close` hangs. The send row passes both, because the guest parks once the host's 64 KiB buffer fills, before the kernel's do, so the Write in flight returns by itself.
  - `cancelSubtask` returning without claiming: the cancel reads BLOCKED and the dial is never cancelled.
  - `hostNet.release` closing at every release: the read after the handle's drop is DROPPED with nothing.
  - the receive drop's `CloseRead` dropped: the Read waits out its 2 s deadline.
  - `hostWaitPendingLocked` without the socket term: `ErrLiftParkExpired`. p3tcp cannot witness this one: its `run` is sync-lifted, and the bound is the callback loop's, which is why `tcp-connect-park.wasm` exists.
  - `asyncBuiltinFunc`'s element check dropped: `stream-read-u32.wasm`'s read reaches the byte copy and is refused for its handle, not its element.
  - `LoadFlat`'s coercion skipped: a `u32` case of an i32-and-i64 join reads i32; its padding dropped: the ipv4 address reads 6 words of 12.
  - `runComponent` not passing the flag: the CLI's granted row prints the denied reading; the CLI refusal dropped: both of its rows exit 0; either root path's `h.InheritNetwork` dropped: that row is denied.
- **Tests changed** (pre-existing):
  - `TestStreamReadIsBoundAndItsSyncFormRefused` (`hostproducer_test.go`): `stream-read-u32.wasm` now instantiates, and its new `read` export's call is refused naming the element. The fixture gains that export (`stream-read-u32.wat`, reassembled).
  - `internal/testenv/observer_test.go`: the census gains the dial's and the send writer's sites, and the producer's entry names its socket source.
  - Every other suite passes unchanged, with and without `-race`: p3cat, p3std, p3sleep, p3sys, p3async-hello, p3hello and p3echo, and the stream, future, cancel, waitable-set, liftpark and runslot suites. stdin's readings are unchanged.
- **What a guest sees.** A WASI 0.3 std guest's `TcpStream::connect`, `write_all`, `shutdown` and `read_to_end` work against a reachable peer when the embedder grants the network, and fail `PermissionDenied` when it does not.
- **Public surface:** `ComponentConfig.InheritNetwork` and `--inherit-network`, as stamped, and nothing else.

## Rollback

- Dropping the grant check at `create` and at `connect` lets a guest reach the network without `InheritNetwork`.
- Writing the send sink inline lets a peer that does not read hold the instance's lock, and `Close` with it.
- Dropping `stopAllSockets` from `walker.close` leaves a socket's `Read` holding `Close`.
