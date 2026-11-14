# 0125 — bind listens eagerly, listen returns a host-produced stream of owned sockets that accepts on demand and mints each under the run slot, and the handle and the stream share the listener

Date: 2026-11-14 · Status: **accepted**, 2026-11-14. Scott, in session, approving the plan for v0.14.0's eleventh slice (E7b-2, the second of #1277's two). The approval was given in session and has no comment to cite, so the record is the actor's, and commits resting on it are `Ratio-Class: carried`. It is an engine mechanism inside `internal/component`: no contract text, no public API (no root-package file changes, no exported symbol and no CLI flag; `ComponentConfig.InheritNetwork`, stamped on #1235, already grants the network, and bind and listen are inside that grant), and no stamped ADR is reversed. Filed against [#1277](https://github.com/scttfrdmn/burroughs/issues/1277).

## Context

E7a (ADR 0123) served the client half of `wasi:sockets/types@0.3.0`'s `tcp-socket`. E7b-1 (ADR 0124) gave stream ends typed elements, with an `own` arm in the element descriptor that nothing read. E7b-2 is the server half, read from `sockets.wit` (the `wasip3-0.8.0+wasi-0.3.0` the nightly's std vendors) and from wasi-libc's calls:

- `bind: func(local-address: ip-socket-address) -> result<_, error-code>`, sync. Once a bind succeeds the binding is fixed; a failed one may be retried. Its implementor note asks for SO_REUSEADDR on every platform but Windows.
- `listen: func() -> result<stream<tcp-socket>, error-code>`, sync: *"at most once per socket"*, an implicit bind if unbound, and **a single perpetual stream** that closes only on fatal errors, a transient `accept` error being skipped. Accepted sockets inherit the listener's family.
- `set-listen-backlog-size: func(value: u64) -> result<_, error-code>`: 0 is `invalid-argument`, `connecting` and `connected` are `invalid-state`, and the value may be ignored.
- **Shared ownership:** the handle and the listen stream reference one OS socket, closed when the last goes; an accepted socket does not keep its listener alive.
- **wasi-libc's `listen()`** binds implicitly when unbound, then always calls `set-listen-backlog-size(max(backlog, 1))`, whose error fails `listen()`, then `listen`; an already-listening socket returns 0 after the backlog call. **`accept()`** reads one element from the listen stream, waiting on a waitable set when the read is BLOCKED, then calls `receive`, `send` (over a `stream.new`) and `get-remote-address` on the accepted handle. std's `TcpListener::bind` is setsockopt(SO_REUSEADDR), which wasi-libc treats as a no-op, then `bind`, then `listen(128)`.

On main at 69115be the socket's states were unbound, connecting, connected and closed; `connect` was refused from anything but unbound; the address getters answered only a connected socket; `get-is-listening` was the constant false; `hostNet` held connections and dials but no listener; and the producer (ADR 0122) assumed a future, which `listen` does not return.

### What the oracle does, measured

wasmtime 49.0.3 (`.oracle/wasmtime-49.0.3/wasmtime`, never the 50.0.1 on PATH), darwin/arm64:

- **`p3tcpserve`**, a dependency-free Rust `wasm32-wasip3` std guest (below), `wasmtime run -S inherit-network p3tcpserve.wasm`: **10 of 10 runs byte-identical**, exit 0, empty stderr. It binds, the listener is v4 loopback with a nonzero port, it connects to itself, accepts, the accepted peer is the client's local address, the client's peer is the listener, the accepted socket's local address is the listener's, and 1024 bytes echo each way. With no network flag, 10 of 10: `bind: PermissionDenied`, which is `create`'s `access-denied`. The trace (`WASMTIME_LOG=wasmtime_wasi=trace`) shows `create`, `bind`, `set-listen-backlog-size(128)`, `listen`, `get-local-address`, the client's `create`, `connect`, `receive` and `send`, then the accepted handle's `receive`, `send` and `get-remote-address`.
- **`tcp-bind-listen.wasm`**, a hand-written component whose `run(row, port)` packs four result bytes, `wasmtime run -S inherit-network -W component-model-async=y -S p3=y --invoke 'run(<row>, <port>)'`, **10 of 10 per row identical** (`testdata/tcp-bind-listen.reading`):
  - `get-local-address` **between bind and listen is ok**, a nonzero port on 127.0.0.1;
  - `set-listen-backlog-size(0)` is `invalid-argument`, on a bound socket, a listening one, **and a connected one**, so the zero is refused before the state; `(1)` is ok when bound and `invalid-state` when connected;
  - `listen` twice: ok, then `invalid-state`, still listening; `listen` on a connected socket `invalid-state`; `listen` with no bind ok, with a nonzero local port;
  - `bind` twice: `invalid-state`; a bind to 224.0.0.1 `invalid-argument`; `get-local-address` on an unbound socket `invalid-state`;
  - `connect` from bound ok, **keeping the bound port**; `connect` on a listener `invalid-state`; `get-remote-address` on a listener `invalid-state`;
  - one `stream.read` of the listen stream after a client's connect: COMPLETED with progress 1, the accepted handle not listening and its remote address ok;
  - **a second socket's connect to a bound port before any listen: `timeout`, after about 7.8 s.** That is darwin's answer to a SYN at a socket that is bound and not listening; other kernels were not measured. It is the one row the choice below diverges on.

## Options

For when the OS socket is bound:

1. **An eager `net.Listen` at `bind`.** One portable call: the port is exact at bind, so `get-local-address` between bind and listen is the oracle's, and `address-in-use` is reported by `bind` as the WIT lists it. Go sets SO_REUSEADDR on POSIX listeners, as the implementor note asks. `listen` then flips the state and returns the stream. *Costs:* the kernel listens from `bind`, so a connect between bind and listen completes into the backlog (the last row above); the backlog value cannot be applied, which the WIT permits; and a connect from bound must close the listener and dial from its address.
2. **Defer the OS socket to `listen`** (bind records the address, listen calls `net.Listen`). *Costs:* `get-local-address` between bind and listen cannot report an ephemeral port, the first row above; `address-in-use` moves from `bind` to `listen`, which the WIT does not list there.
3. **Raw sockets through `syscall`** (socket, bind, listen as three calls). *Costs:* not portable to Windows CI without a second implementation, and a hand-rolled SO_REUSEADDR and V6ONLY per platform; it is the only option that matches every row.

For the stream's producer: a new producer type for owns, or **ADR 0122's producer extended** with an accept source, a queue of accepted sockets and a nil future. The second keeps one delivery path, one census site and one stop.

For where an accepted connection is registered: at its delivery (under the run slot) or **at once, on the accept goroutine**, so `Close` closes one the guest never received.

## Choice

1. **`bind` listens eagerly** (option 1), `net.Listen("tcp4"|"tcp6", addr)`. The socket is `bound` with the listener and its exact local address recorded, and the listener is registered in `hostNet`. Refused inline, the socket left unbound so the bind may be retried: a socket that is not unbound is `invalid-state`; a family that is not the socket's, a multicast, IPv4-mapped or IPv4-broadcast address is `invalid-argument`; `EADDRINUSE` is `address-in-use` and `EADDRNOTAVAIL` `address-not-bindable` (both with Windows' WSA numbers); anything else maps as ADR 0123's choice 10 does.
2. **The states widen** to unbound, bound, listening, connecting, connected and closed. `get-local-address` answers a bound, listening or connected socket; `get-remote-address` a connected one; `get-is-listening` is the state; and `connect` is allowed from bound: the handle's reference to the listener is released, which closes it, and the dial is made from its address (`Dialer.LocalAddr`), so the bound port is kept as wasmtime keeps it.
3. **`set-listen-backlog-size`**: 0 is `invalid-argument` first, then `connecting` or `connected` (and `closed`) are `invalid-state`, and otherwise the value is recorded, ignored, and ok. Ignoring it is the WIT's permission; Go fixes the backlog at `Listen`.
4. **`listen`** binds implicitly to the unspecified address of the socket's family, port 0, when unbound (choice 1's path); is `invalid-state` unless the socket is bound; flips it to listening; and returns `ok(stream)`. The result is lowered as `result<u32, error-code>`, whose layout is `result<stream<…>, error-code>`'s, as `receive` hand-lowers its two handles. **Its signature is checked at instantiate by its own rule**: the guest's result must be `result<stream<own<tcp-socket>>, error-code>`, with the own naming the host's `tcp-socket` and the error the host's `error-code` (`checkListenSignature`); `checkConsumerPayload`'s producer arm, which wants `tuple<stream<u8>, future<P>>`, is not reused.
5. **The accept producer is ADR 0122's producer** with an accept source (`sockAccept`) and no future:
   - **Accept happens on demand**: one `Accept` in flight, started by `startReadLocked` when a guest read is pending and nothing is queued, counted in `producerReads` so it is a park waker, and in `hostNet` so `Close` waits it out. It is the census site ADR 0122 authorised, with ADR 0123's extension; this ADR extends it again.
   - **Each accepted connection is adopted into `hostNet` at once**, on the accept goroutine, and built into a connected `tcp-socket` with its local and remote addresses and the listener's family. It is then queued under ADR 0101's discipline (the run slot, then `asyncHandles.mu`), as a Read's bytes are.
   - **Delivery is the copy's `own` arm**: for an element descriptor whose `own` is set, `copyIntoLocked` mints each queued socket as a fresh own in the reading instance's table (def:lower_own, `hostResources.mint`) and stores its i32 at `dstPtr + 4*i`, which is def:WritableGuestBuffer.write's store of an own. `min(read_n, queued)` are delivered and the rest stay queued.
   - **A transient Accept error is skipped**, and the pending read accepts again: the Linux manual's list the WIT quotes (`ENETDOWN`, `EPROTO`, `ENOPROTOOPT`, `EHOSTDOWN`, `EHOSTUNREACH`, `EOPNOTSUPP`, `ENETUNREACH`; not `ENONET`, which only Linux defines), with `ECONNABORTED`, `ECONNRESET` and `EINTR`. `ECONNABORTED`, `ECONNRESET`, `EHOSTUNREACH` and `ENETUNREACH` are read through ADR 0123's table, so Windows' WSA numbers for them count too. Anything else is fatal: the stream finishes, its writer dropping, so the reader sees DROPPED.
   - **The nil-future arms**: `finishLocked` and `readerDroppedLocked` resolve a future only when there is one.
   - **The reader's drop** releases every queued socket (its connection reference) and the stream's reference to the listener; so does the stream's finish.
6. **`hostNet` holds listeners**, reference-counted by `hostNet.mu` as a connection is: the handle holds one from bind, the listen stream one from listen, and the last release closes it. `stop` closes every listener, which ends an `Accept` in flight, so `Close` waits the accept goroutine out with the dials, readers and writers. A connection accepted after the stop is closed by its adoption, and one accepted before it is closed by the stop with every other connection, delivered or not.

**Lock order, stated:** the run slot → `asyncHandles.mu` → { `hostResources.mu`, a leaf; `tcpSocket.mu` → `hostNet.mu` }. A mint during a copy is `asyncHandles.mu → hostResources.mu`, which stays acyclic because `hostResources.mu` is a leaf: nothing is taken under it, and **no destructor runs under it** (`drop` and `closeAll` run a destructor after releasing it, and a destructor takes `tcpSocket.mu` then `hostNet.mu`). `bind` and an implicit bind hold `tcpSocket.mu` across `net.Listen` and the registration, so two concurrent binds of one socket cannot both succeed.

### Divergences from wasmtime 49.0.3, recorded

- **A connect to a bound port before `listen`** completes into the backlog (ok) where wasmtime 49.0.3 on darwin times out after about 7.8 s. It is the eager listen's cost, paid for an exact `get-local-address` at bind, which is wasmtime's reading; std's `TcpListener::bind` reads the address only after `listen`, so std cannot tell the two apart. `tcp-bind-listen`'s `connect-before-listen` row asserts Burroughs' value and names the divergence.
- **The backlog is recorded and not applied.** The WIT permits it; whether wasmtime applies it was not measured.

Unmeasured: IPv6 listeners; a fatal accept error other than a closed listener; `get-local-address` while connecting (still `invalid-state`, ADR 0123's recorded unmeasured case); other kernels' answer to the connect-before-listen row. These, and whether wasmtime applies the backlog, are filed as #1285.

### Out of scope, recorded

- Name lookup (E7c, #1278), UDP, and the keep-alive, hop-limit and buffer-size options; accepted sockets inherit only the family.
- A host-to-host copy of the listen stream (`send` or `write-via-stream` of it is refused by name, as of every produced stream).
- Applying the backlog (choice 3).

## Consequences

- **Witnesses** (`p3tcpserve_test.go`), every one also run 12 times under `-race`:
  - `TestP3TcpServeMatchesWasmtime`, in both gate states: `testdata/p3tcpserve/p3tcpserve.wasm`, a dependency-free, single-threaded Rust `wasm32-wasip3` std program (nightly 1.100.0, built offline by `p3tcpserve/build.sh`, the same sha256 on a second build), binds 127.0.0.1:0, connects to itself, accepts and echoes 1 KiB each way. Granted, its stdout, empty stderr and exit 0 equal `p3tcpserve.reading`; denied, `p3tcpserve-denied.reading`. It prints properties, never a port. After `Close` no socket goroutine remains.
  - `TestTcpBindListenMatchesWasmtime`: `tcp-bind-listen.wasm`'s twelve measured rows against `tcp-bind-listen.reading`, eleven equal to wasmtime's and `connect-before-listen` asserting Burroughs' value, the recorded divergence.
  - `TestCloseEndsAGuestParkedInAccept`: the fixture's `accept-park` row parks in `waitable-set.wait` with the Accept in flight; `Close` returns, the run ends, and no socket goroutine remains.
  - `TestAnAcceptedConnectionNeverDeliveredIsClosedAtClose`: a read cancelled while its Accept is in flight leaves the accepted connection queued; the registry's stop closes it, and the client's read returns at once.
  - `TestTheListenerOutlivesTheHandleWhileItsStreamHoldsIt`: the handle dropped, the stream still accepts the client that connected before the drop, minted as a connected socket whose remote address is the client's.
  - `TestATransientAcceptErrorDoesNotEndTheStream`: an Accept failing with `ECONNABORTED` is skipped and the read completes with the next connection; a fatal error, the control, drops the stream.
  - `TestListenMustBeDeclaredAsTheHostDeclaresIt`: `tcp-listen-mistyped.wasm`, whose listen returns `stream<u32>`, is refused at instantiate, as wasmtime 49.0.3 refuses it at link.
- **Neuters, run**, each named at its site:
  - the own arm storing each socket's queue position rather than its minted index: p3tcpserve traps at the accepted socket's `receive` and `accept-one` at its `get-is-listening`, both *"handle index 0 is not live"*.
  - the accepted connection built unadopted: `TestAnAcceptedConnectionNeverDeliveredIsClosedAtClose`'s client waits out its 2 s deadline. **p3tcpserve passes under it**, which the plan forecast as a leak at `Close`: every connection it accepts is delivered and dropped by the guest, and the drop's release closes it whether or not the registry holds it.
  - the local address answered for a connected socket only, as E7a did: p3tcpserve prints `listener address: NotConnected`, and `local-between`, `listen-unbound`, `connect-bound` and `connect-before-listen` read `invalid-state`; `accept-one` hangs to the guard, its client connecting to the port `get-local-address` did not report, so nobody reaches the listener.
  - `readerDroppedLocked` resolving the future unconditionally: p3tcpserve panics on the nil future at the listener's drop. `finishLocked` the same: the fatal row of the transient test panics.
  - `hostNet.stop` skipping the listeners' close: `TestCloseEndsAGuestParkedInAccept`'s `Close` hangs to the guard.
  - `releaseListener` closing at every release: the read after the handle's drop is DROPPED.
  - `acceptTransient` returning false: the transient row's read is DROPPED.
  - `checkListenSignature` returning nil: the mistyped guest instantiates.
  - bind's state check dropped: `bind-refusals` reads its second bind ok (0x85820000).
- **Tests changed** (pre-existing):
  - `socketGoroutines` (`p3tcp_test.go`), the helper every socket witness's `requireNoSocketGoroutines` reads, also lists a goroutine in `(*streamProducer).acceptOne`, so a leaked Accept is caught.
  - `internal/testenv/observer_test.go`: the producer's census entry names the listener's source under this ADR; the count stays 1, since the Accept runs on the same `go` statement.
  - Every other suite passes unchanged, with and without `-race`: p3tcp, p3cat, p3std, p3sleep, p3sys, p3async-hello, p3hello and p3echo, and the typed-stream, stream, future, cancel and producer suites. No std guest's reading changed.
- **What a guest sees.** A WASI 0.3 std guest's `TcpListener::bind`, `local_addr`, `accept` and the accepted stream's reads and writes work when the embedder grants the network, and `bind` fails `PermissionDenied` when it does not.
- **Public surface:** none added. `Host.listen` is an unexported test seam beside `Host.dial`.

## Rollback

- Deferring the bind to `listen` makes `get-local-address` between bind and listen report no port, which std's `TcpListener::bind` does not read and a guest that binds and reads its address before listening does.
- Dropping the adoption at accept leaves a connection accepted and not yet delivered open past `Close`.
- Dropping the listener close from `hostNet.stop` leaves `Close` waiting on an `Accept` that never returns.
