# 0140 — A `udp-socket` is a host resource whose OS socket is made at its bind, send resolves inline, receive is served by one reader per socket, and UDP rides the network grant

Date: 2026-11-28 · Status: **accepted**, 2026-11-28. The public half is Scott's: his ruling on E3, given in session on 2026-11-27 and recorded on [#1376](https://github.com/scttfrdmn/burroughs/issues/1376) (*"UDP rides the existing network grant. `ComponentConfig.InheritNetwork` and `--inherit-network` (`run` and `serve`) grant wasi:sockets UDP as well as TCP, as wasmtime's `-S inherit-network` does. No new field or flag is added. The field's doc comment and the flags' help text change from *TCP* to *TCP and UDP*"*). The stamp's limit is ADR 0097's form: this slice changes that field's documentation and those two flags' help text, and adds nothing public. The rest is an engine mechanism inside `internal/component`, which no contract text governs and which reverses no stamped ADR, so by the #647 review (*"Report decisions taken, not rulings requested"*) it is decided here and reported as a decision taken. [ADR 0123](0123-a-tcp-socket-is-a-host-resource-over-a-shared-connection-connect-is-an-async-method-whose-spilled-parameters-are-loaded-flat-and-send-and-receive-are-host-stream-goroutines-behind-a-network-grant.md) is the design authority for wasi:sockets, and this ADR records only what 0123 does not decide for UDP. The record is the actor's, so commits resting on it are `Ratio-Class: carried`. Filed against [#1376](https://github.com/scttfrdmn/burroughs/issues/1376), E3 of the v0.16.0 plan ([#1370](https://github.com/scttfrdmn/burroughs/issues/1370)).

**Every citation into this tree names a symbol rather than a line** (ADR 0047).

## Context

`wasi:sockets/types@0.3.0`'s `udp-socket` (the WIT the nightly's std vendors, `wasip3-0.8.0+wasi-0.3.0`, `sockets.wit`) has fifteen functions:

- `[static]create(address-family) -> result<udp-socket, error-code>`;
- `bind`, `connect` (both `func(ip-socket-address) -> result<_, error-code>`) and `disconnect`, all sync;
- `send: async func(data: list<u8>, remote-address: option<ip-socket-address>) -> result<_, error-code>`;
- `receive: async func() -> result<tuple<list<u8>, ip-socket-address>, error-code>`;
- `get-local-address`, `get-remote-address`, `get-address-family`, `get-unicast-hop-limit`, `set-unicast-hop-limit`, and the receive and send buffer sizes' getters and setters, all sync.

On main at 31287c7 none was served: each reached the refusing stub at its call, and `udp-socket` was not a declared host resource, so a guest's drop of one met `dropNoop`.

**How they cross the ABI.** send flattens to 16 words: `self`, the list's `(ptr, len)`, and the option's case and the address's 12. That is past `MAX_FLAT_ASYNC_PARAMS` (4), so an async lower passes one pointer to the parameter tuple, which ADR 0123's spill loads flat. It is not past `MAX_FLAT_PARAMS` (16), so the blocking sync lower (ADR 0126) passes the 16 words flat. receive's one parameter is `self`. Every result is lowered at a return pointer.

**What the consumers lower.** A Rust std guest's `UdpSocket` (wasi-libc, read with `wasm-tools print`) lowers send and receive with the async option and the other thirteen without, and cancels with the sync `subtask.cancel`. wasmtime's trace of it shows wasi-libc **keeps a receive pending across a timed-out or a nonblocking `recv`**, and the next `recv` takes its result: it does not cancel it. Go's `net` in the fork's `GOOS=wasip3` (F13, [#1379](https://github.com/scttfrdmn/burroughs/issues/1379)) is not built. What it will call follows from `ListenUDP`, `DialUDP`, `ReadFrom`, `WriteTo`, `Read`, `Write` and `LocalAddr`: create, bind, connect, send with and without an address, receive, and `get-local-address`, over IPv4 and IPv6 loopback. The fixture's `dial-v4` and `dial-v6` rows make exactly those calls.

### What the oracle does, measured

wasmtime 49.0.3 (`.oracle/wasmtime-49.0.3/wasmtime`, never the 50.0.1 on PATH), darwin/arm64. **The fixture's flags** are `-W component-model-async=y -W component-model-more-async-builtins=y -S p3=y -S inherit-network`. The second is needed because the fixture uses the async `subtask.cancel`. The readings were taken before any engine code, and are committed with their fixtures in the commit after this ADR's: `udp.reading` (`udp.wat`, every row 10 of 10 identical) and `p3udp.reading` and `p3udp-denied.reading` (a Rust std guest, 10 of 10 each).

- **The grant.** With no network flag, `create` is `err(access-denied)` for both families, and p3udp's bind prints `PermissionDenied`. That is ADR 0123's TCP reading. `-S udp=y` alone makes `create` ok and `bind` `access-denied`, and `-S udp=n -S inherit-network` makes `create` `access-denied`. Neither is a state this host has: Scott's ruling mirrors `-S inherit-network` alone.
- **The datagram's size.** A datagram of 65536 or 65537 bytes is `datagram-too-large`, over IPv4 and over IPv6. **darwin's kernel takes the IPv6 two** from a socket with a 1 MiB send buffer, which was measured with Go's `net`, so that refusal is wasmtime's own check at 65535 bytes, not the kernel's. Below it the kernel answers: darwin's default send buffer is `net.inet.udp.maxdgram`, 9216 bytes, so 9217 is `datagram-too-large`. With a 1 MiB send buffer, 65507 bytes is IPv4's most and 65508 is refused, and 65527 is IPv6's most without a jumbogram. A list past the end of memory traps at the lift: *"list pointer/length out of bounds of memory"*. An empty datagram is sent and received as empty.
- **send's address.** An unconnected socket, fresh or bound, that sends with no address gets `invalid-argument`. A connected socket that sends to another address gets `invalid-argument`, and one that sends to its own peer gets ok. The unspecified address, port 0, the other family, and an IPv4-mapped address on an IPv6 socket are each `invalid-argument`, for send and for connect. So is a broadcast from a socket bound to `0.0.0.0`, because the OS's socket has no `SO_BROADCAST`: the answer is `access-denied`.
- **bind.** The other family, and an IPv4-mapped address on an IPv6 socket, are `invalid-argument`. 192.0.2.1 is `address-not-bindable`, a port another socket holds is `address-in-use`, and a second bind is `invalid-state`.
- **receive.** A socket never bound gets `invalid-state`.
  - **A receive returns STARTED even when its datagram has already arrived**, so wasmtime never resolves a receive inline.
  - Datagrams arrive in the order they were sent.
  - **An async cancel of a pending receive returns BLOCKED, and the subtask then resolves CANCELLED_BEFORE_RETURNED.** The next receive takes the datagram sent after the cancel, so the cancelled receive took nothing.
  - A connected socket receives only from its peer what arrives after the connect. **A datagram from another socket that arrived before the connect is still received:** darwin's connect does not purge the queue, and wasmtime does not filter it. The row's first draft read from-the-peer 9 of 10 runs and from-the-other 1 of 10, a race with darwin's loopback queue. The committed row has the loopback drained first, and reads from-the-other 10 of 10. After a disconnect it receives from anyone again.
  - A connected socket that sends to a closed port gets `connection-refused` at its next receive: the ICMP error is surfaced.
- **Implicit binds and the local address.** A fresh socket that sends is bound to `0.0.0.0` with a port, and one that connects is bound to `127.0.0.1`, the address the path to its peer leaves from. A socket bound to `0.0.0.0` that connects reads `127.0.0.1`, with its bound port. **After a disconnect, darwin reads the unspecified address, with the port kept**, even for a socket explicitly bound to `127.0.0.1`. That is darwin's `udp_disconnect`, measured natively too.
- **Options on a socket never bound.**
  - `get-unicast-hop-limit` is 64 for IPv4. For IPv6 it is **255**, which is darwin's `IPV6_UNICAST_HOPS` of −1 read as a `u8`. A `set` of 0 is `invalid-argument`, and 42 reads back as 42.
  - The buffer sizes read nonzero. A `set` of 0 is `invalid-argument`, 65536 reads back as at least 65536, and 2^40 is ok.
  - Values set before a bind survive it.

## Options

**When the OS socket is made.**

1. **At `create`, unbound,** through `syscall.Socket`, with the options applied to it from the start. *Costs:* Go's `net` has no unbound UDP socket. Binding, receiving and the netpoller would need `net.FilePacketConn`, which Windows does not have, or a second implementation of each.
2. **At the bind,** explicit or implicit (the first `connect` or `send`), through `net.ListenUDP`. *Costs:* an option set before the bind has no socket to live on, so it is recorded and applied at the bind, and a get before the bind needs the kernel's answer without the socket.

**What `connect` is.**

1. **The OS's connect on the bound socket** (`syscall.Connect` inside `RawConn.Control`), and a disconnect by `connect` to `AF_UNSPEC`. The kernel filters what arrives, surfaces ICMP errors, and picks the local address, as it does for wasmtime.
2. **An emulation in the host:** a remembered peer, a filter on receive, and `WriteTo` the peer on a send with no address. *Costs:* no ICMP error is ever surfaced, and the local address after a connect stays unspecified. A Go DNS client over a connected UDP socket learns that a server's port is closed from that ICMP error, without waiting out its timeout.
3. **`net.DialUDP`, a new socket for each connect.** *Costs:* the bound port, and the datagrams already queued, are lost at each connect, and disconnect cannot be expressed.

**How receive waits.**

1. **A goroutine per receive,** each in `ReadFrom`. *Costs:* a cancelled receive's `ReadFrom` cannot be ended without a read deadline, and a deadline belongs to the whole socket. Its goroutine would take the next datagram and discard it, which is the loss wasmtime's reading shows it does not have.
2. **One reader per socket, on demand,** as ADR 0125's accept is: at most one `ReadFrom` in flight, serving the pending receives in order. A datagram read when no receive is waiting is held for the next one.
3. **Option 2, plus an inline non-blocking attempt** when nothing is held and no read is in flight. *Costs:* the receive returns RETURNED where wasmtime returns STARTED. The attempt needs `RawConn.Read`, and Windows sockets are blocking under Go's overlapped I/O, so it would be POSIX only. It is not measured to buy anything.

**How a cancel resolves:** inline, at the cancel, which is ADR 0123's connect and ADR 0126's lookup; or after BLOCKED, which is wasmtime's reading.

**Where send runs:** on the guest's goroutine, inline, or on a goroutine per send.

## Choice

**The OS socket is made at the bind (option 2), `connect` is the OS's (option 1), receive has one reader per socket (option 2), and a cancel resolves inline.** In full:

### The resource and its registry

- **`udp-socket` is a host resource** (`udpSocketResource`, `hostudp.go`), declared for 0.3 in `resourceSource()`. `create` mints an own over an unbound `udpSocket`.
- **Its destructor closes the OS socket,** which ends a read in flight. A socket with a receive pending cannot be dropped, since the receive holds a lend of it.
- **One OS socket, owned by the handle.** UDP has no derived streams, so ADR 0123's reference count has nothing to count.
- **The instance's registry (`hostNet`) holds every UDP socket that has an OS socket.** Its `stop`, at Close, closes each one and drops each pending receive, resolving nothing and releasing its lend. `wait` waits out every reader.

### bind, the implicit binds, and the options

- **`bind` is `net.ListenUDP("udp4"|"udp6", addr)`**, which sets `IPV6_V6ONLY` on an IPv6 socket, as the WIT requires. It answers in this order:
  - the grant;
  - the other family, or an IPv4-mapped address on an IPv6 socket, is `invalid-argument`;
  - a bound socket is `invalid-state`;
  - errors map as below.
- **The first `connect` or `send` of an unbound socket binds it** to its family's unspecified address, port 0. That is the WIT's implicit bind, and the kernel's for wasmtime.
- **`SO_BROADCAST` is cleared on every OS socket.** Go's `net` sets it on every UDP socket. wasmtime's socket does not have it, and no WASI option grants it, so a broadcast is the kernel's `access-denied` and no datagram leaves the host.
- **An option set before the bind is recorded,** after its own refusals, and applied to the OS socket when the bind makes it.
- **A get before the bind is read from a probe:** a socket opened for the call on the family's unspecified address, with the recorded options applied, read, and closed. So the kernel's default and the kernel's rounding are the kernel's own: darwin's IPv6 hop limit of −1, read as 255, and the doubled buffer that socket(7) documents for Linux.
- **After the bind, every get and set is the OS socket's `getsockopt` or `setsockopt`.** `IP_TTL` or `IPV6_UNICAST_HOPS` is the hop limit, read as a `u8`, and `SO_RCVBUF` and `SO_SNDBUF` are the buffers.
  - A `set` of 0 is `invalid-argument`, as the WIT says.
  - A buffer size is clamped to `[1, 2^31−1]`, and `ENOBUFS` from the kernel is ok. That is the WIT's *"accepted without error, but may be clamped"*, and wasmtime's reading of 2^40.
  - A hop limit is set as given.

### connect and disconnect

- **`connect`** answers in this order:
  - the grant;
  - the unspecified address, port 0, the other family, or an IPv4-mapped address on an IPv6 socket, is `invalid-argument`;
  - otherwise the socket is bound implicitly if it is not yet bound. A connected socket is disconnected first, as the WIT's implementors' note asks, then `syscall.Connect`s.
  - On success, the peer is recorded. On failure, the socket is left unconnected, with the error mapped.
- **`disconnect`** of a socket that is not connected is `invalid-state`. Otherwise it is a `connect` to `AF_UNSPEC`:
  - **on darwin and Linux, a raw `SYS_CONNECT`** (`udpDisconnect`, the one `unsafe` use in this package), with darwin's `EAFNOSUPPORT` and `EINVAL` read as success, as wasmtime reads them (darwin dissolves the association before it refuses the address, measured natively);
  - **on Windows, a `connect` to the all-zero address**, which Winsock's documentation names as a disconnect;
  - elsewhere, `not-supported`.
- **The local address is the kernel's `getsockname`,** read at the call, because a connect and a disconnect move it. The remote address is the recorded peer, and `invalid-state` when the socket is not connected.

### send

- **send resolves inline, on the guest's goroutine** (`udpSend`), as ADR 0133's filesystem methods do. In order:
  1. the list is lifted through `canon.LoadListU8`, so a list past the end of memory is the guest's trap, at the pin's place;
  2. the grant;
  3. **a datagram of more than 65535 bytes is `datagram-too-large`**, before the kernel is asked. That is wasmtime's reading, and the WIT's *"may trap if the data length exceeds 64 KiB"* answered with an error rather than a trap, as wasmtime answers it. Beneath 65535 bytes the kernel decides;
  4. the given address's refusals, which are connect's;
  5. a connected socket sending to another address is `invalid-argument`, and an unconnected socket sending with no address is `invalid-argument`;
  6. the implicit bind.
- **Then a connected socket `Write`s, and an unconnected one `WriteToUDPAddrPort`s.** On darwin, a `sendto` with an address on a connected socket is `EISCONN`, so the connected arm never passes one.

### receive

**receive is served by one reader per socket** (`udpReceive`, `(*udpSocket).read`). In order:

1. the grant;
2. **a socket that is not bound is `invalid-state`**, inline;
3. **a datagram already held is the result, inline;**
4. **otherwise the receive is pending:**
   - it is added to the socket's queue of receives, holds its lend of `self`, and counts in `sockPending`, as a waker for the park bound;
   - if no reader is running, one is started, counted in the registry;
   - the receive returns STARTED, wasmtime's reading.

The reader is the one receive site, authorised by this ADR in the census:

- **It reads into a 65535-byte buffer** with no lock held.
- **Each datagram goes to the first pending receive.** It is copied out as a `list<u8>` with its sender, and resolved through `onResolve`, so the async lower stores it under ADR 0101's discipline, through the caller's realloc with no engine lock held. The sync lower stores it after its wait (ADR 0126).
- **It reads again while receives are pending, and returns when none is.** A datagram it read when none was waiting is held, and the next receive takes it. That is how a cancelled receive takes nothing.
- **A read error is a result too:** `connection-refused` for an ICMP error, and the rest mapped. A closed socket ends the reader.
- **A connected socket's reader drops a datagram from anyone but the peer, and connect drops a held one from anyone else.** The WIT promises *"receive will only return datagrams sent from the provided remote-address"*, and the kernel's own filter covers only what arrives after the connect. **This departs from wasmtime,** which returns a straggler that was queued before the connect (`connect-after-arrival`). The WIT's text is the reading Burroughs asserts, as ADR 0138 asserted the WIT's `mutate-directory` text over wasmtime's.

**A cancel resolves inline** (`udpRecv.cancelSubtask`), ADR 0123's connect. One claim decides who resolves a pending receive: the reader's delivery, the guest's cancel, or Close. Each removes the receive from the socket's queue under its lock. The cancel releases the lend and resolves CANCELLED_BEFORE_RETURNED at the cancel. **This departs from wasmtime,** whose async cancel returns BLOCKED and resolves afterwards. The pin permits either: after `on_cancel`, a subtask already resolved returns its state (def:canon_subtask_cancel:"return [subtask.state]"), and one that is not returns BLOCKED. Resolving at the cancel spares a Go guest's netpoller a second wait for a cancel it has already decided, and it is the shape the tree's other cancellable socket calls have.

### Errors

**Errors map by the WIT's per-function lists** (`udpErrorLabel`), with Windows' WSA numbers beside the POSIX ones:

- `EMSGSIZE` is `datagram-too-large`;
- `ECONNREFUSED` is `connection-refused`;
- `ECONNRESET`, `ENETRESET`, `EHOSTUNREACH`, `EHOSTDOWN`, `ENETUNREACH` and `ENETDOWN` are `remote-unreachable`;
- `EACCES` and `EPERM` are `access-denied`;
- `EADDRINUSE` is `address-in-use`;
- `ENOBUFS` and `ENOMEM` are `out-of-memory`;
- for bind, `EADDRNOTAVAIL` is `address-not-bindable`;
- for connect and send, `EADDRNOTAVAIL`, `EAFNOSUPPORT`, `EDESTADDRREQ`, `EISCONN` and `EINVAL` are `invalid-argument`;
- anything else is ADR 0123's table, and then `other(none)`.

### The grant

**The grant is `InheritNetwork`, by Scott's ruling on #1376.** Off, `create` is `err(access-denied)`, which is wasmtime 49.0.3's reading with no network flag and ADR 0123's point for TCP. Every operation that reaches the network checks it again: bind, connect, send and receive. `ComponentConfig.InheritNetwork`'s documentation, the type's, and `--inherit-network`'s help for `run` and `serve` now say *TCP and UDP*.

**Lock order:** the run slot → `asyncHandles.mu` → { `hostResources.mu`, a leaf; `tcpSocket.mu` → `hostNet.mu`; `udpSocket.mu` → `hostNet.mu` }. A resolution is applied with no socket lock held, and `sockPending` is raised before the socket's lock is taken.

**Names added below the public boundary:** none exported.

## Consequences

- **Every function and method of `udp-socket` is served.** None meets the refusing stub any more.
- **Two named departures from wasmtime 49.0.3,** each in a fixture row of its own, with both readings recorded beside the row:
  1. an async cancel of a pending receive resolves at the cancel, where wasmtime's returns BLOCKED first (`cancel`);
  2. a connected socket does not return a datagram from another socket that was queued before the connect, where wasmtime does (`connect-after-arrival`). That row is the WIT's text.
- **Rows that read the kernel and not the engine** are darwin's, and each is marked so in the reading:
  - disconnect's local address;
  - a receive buffer too small for a datagram (darwin drops it; Linux, by its source and unmeasured, queues the first);
  - the hop limit's defaults;
  - the 9216-byte send buffer.

  On another kernel Burroughs asks that kernel, as wasmtime would, and the test does not compare those rows.
- **Named limits:**
  - **A send runs on the guest's goroutine** and blocks it if the kernel cannot take the datagram at once, as a filesystem call does (ADR 0133's limit).
  - **A received datagram is lowered element by element,** through the codec's general list path, so a 65507-byte datagram is 65507 values.
  - **On Windows, Go's `net` disables `SIO_UDP_CONNRESET`,** so an ICMP error is never surfaced, and a receive after a send to a closed port waits.
  - **Unmeasured:**
    - Linux's and Windows' readings;
    - whether wasmtime on Linux halves the buffer sizes it reads (Burroughs reports the kernel's);
    - a datagram longer than 65535 bytes, which only an IPv6 jumbogram could carry (the reader's buffer truncates it);
    - multicast;
    - a send that the kernel's queue makes wait.

## Rollback

- Dropping the grant checks lets a guest send datagrams without `InheritNetwork`.
- Dropping the reader's queue of one held datagram makes a cancelled receive lose the next datagram.
- Dropping `udpSocket`s from `hostNet.stop` leaves a reader's `ReadFrom` holding Close.
