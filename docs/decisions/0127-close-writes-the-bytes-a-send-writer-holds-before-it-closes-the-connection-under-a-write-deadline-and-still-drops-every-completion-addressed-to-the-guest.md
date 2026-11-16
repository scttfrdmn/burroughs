# 0127 — Close writes the bytes a send writer holds before it closes the connection, under a write deadline, and still drops every completion addressed to the guest

Date: 2026-11-16 · Status: **accepted**, 2026-11-16. Scott, in session: *"Repair by flushing the send buffer at Close. This amends ADR 0123's choice 12 only"*, recorded on [#1308](https://github.com/scttfrdmn/burroughs/issues/1308#issuecomment-6930948151). The ruling was given in session, so the record is the actor's, and commits resting on it are `Ratio-Class: carried`. It **amends [ADR 0123](0123-a-tcp-socket-is-a-host-resource-over-a-shared-connection-connect-is-an-async-method-whose-spilled-parameters-are-loaded-flat-and-send-and-receive-are-host-stream-goroutines-behind-a-network-grant.md)'s choice 12, and nothing else**: choice 8's write-behind and its pipelining are untouched, as the ruling orders. ADR 0123 rests on Scott's approval, which is why the amendment needed his; it carries a postscript pointing here, and its text is unchanged. It is an engine mechanism inside `internal/component`: no contract text, and no public API (no root-package file changes and no exported symbol). Filed against [#1308](https://github.com/scttfrdmn/burroughs/issues/1308), a `type:grave`, whose specimen is C2's ([#1242](https://github.com/scttfrdmn/burroughs/issues/1242)) tail row.

## Context

**The defect.** At `Close`, Burroughs discarded bytes that a guest's `stream.write` on a `tcp-socket.send` stream had already been told were complete. wasmtime 49.0.3 delivers them.

**The mechanism**, in `internal/component/hostsock.go` on main at 343be3d:

- ADR 0123 choice 8: a guest write completes once its bytes are appended to the send writer's buffer, `s.buf`, which `sendBufCap` (64 KiB) bounds. That can happen while the writer's `Write` of an earlier chunk is still in flight.
- After each `Write` the writer delivers what it freed under ADR 0101's discipline, which waits for the run slot (`acquireRun`) while the guest holds it.
- The guest goes on to close the socket and exit. `walker.close` calls `stopProducers`, which sets `producersStopped`; the delivery gives up, and the writer's loop sees the flag and returns. **`s.buf` is never written.**
- `hostNet.stop` then closes every connection, which also **aborts a `Write` still in flight**.
- Choice 12 says *"a delivery stopped by the stop discards what it carried."* That is right for a completion addressed to the guest, which has gone. It is wrong for bytes the guest had already handed over.

**The specimen** is C2's tail row (#1242, branch `c2-http-witness`): a 96 KiB echo read after the server exits. Burroughs read 69651 bytes where stock Go and wasmtime 49.0.3 read 98405, in about 20% of runs, and 2 runs in 3 under `-race`. The host had taken five writes; the first four reached the connection, and the last 28754 bytes were in `s.buf` while the writer waited for the run slot, read from goroutine dumps in 5 of 5 short runs.

**Kernel capacity is not the cause** (#1308): a loopback writer to an unread peer gets at least 871 KB accepted on darwin and at least 2.2 MB on Linux (janus), each within 1 ms. wasmtime's guest write completes with its bytes in the kernel, which delivers them after the process has exited.

## Options

1. **Flush at Close.** At the stop, a send writer still writes the bytes the guest was told were complete, those in its buffer and any `Write` already in flight, with no engine lock held and no delivery to the guest, under a bounded write deadline; only then is the connection closed. Choice 12 is amended; choice 8 is not.
2. **Complete a write only once its bytes are on the connection.** The guest's write would complete in the writer's delivery after its `Write`, so nothing told complete is ever only in the host. *Costs:* it amends choice 8. The guest's next write cannot be accepted while the last one is on its way to the kernel, which removes the write-behind's pipelining, and every write would wait for the run slot's round trip through the writer. Not taken (#1308's ruling).
3. **Keep the discard, as a known limit.** *Costs:* a guest that writes and exits loses its last bytes in a share of runs that depends on scheduling, where wasmtime loses none, and C2's tail row could not be held to the oracle. Not taken (#1308's ruling).

For the bound: none, a fixed deadline, or a deadline that moves with the peer's progress. *None* lets a peer that has stopped reading hold `Close` for ever. *A moving deadline* bounds the flush only by the peer's rate, so it does not bound `Close` either. A fixed deadline from the stop bounds `Close` absolutely.

For unblocking the receive Read on a connection the stop leaves open: a past read deadline (`SetReadDeadline(time.Now())`) at the stop, or the writer's close after its flush. The second needs no new mechanism. Its only difference is when that Read returns, and a delivery after the stop is dropped either way, so nothing a guest or a peer sees tells them apart.

## Choice

1. **The stop leaves a writer's connection to the writer** (`hostNet.stop`). A connection with a live send writer (`sockConn.writer`, set by `hostNet.trackWriter` when the writer is counted and cleared when it ends) is marked closed in the registry, so no release closes it, and is given to the writer to close (`sockConn.leftToWriter`). Every other connection, every listener, dial and lookup is stopped as choice 12 says. A connection is therefore not closed under a `Write` in flight, which finishes.
2. **The writer flushes, then closes** (`sockSend.run`). On seeing the stop, the writer writes what `s.buf` holds with one `Write`, holding no lock and delivering nothing, and returns. A `Write` that was in flight at the stop has already returned by then, its delivery dropped. The goroutine's end (`trackWriter`'s ended) closes the connection when the stop left it so, and that close ends the connection's receive Read, if one is in flight. `Close` still waits for every socket goroutine at its end, as choice 12 says.
3. **The bound: `sendFlushBound`, 2 s, a write deadline from the stop.** `hostNet.stop` sets it on every connection it leaves to a writer, so it bounds the `Write` in flight and the flush after it, and so it bounds `Close`. It matters only when the peer reads nothing and the kernel's buffers are full; then the bytes it cuts off are lost. The value:
   - the flush carries at most `sendBufCap`, 64 KiB, the buffer and the `Write` in flight together, so **a peer reading at 32 KiB/s or faster receives every byte**;
   - it is what `Close` can cost an embedder whose peer has stopped reading, and it is a fifth of the 10 s hang guard the socket `Close` witnesses use, so a slow runner still has room;
   - it is not wasmtime's: wasmtime's exit costs nothing here, because its kernel already holds the bytes and delivers them after the process has gone. No figure of the oracle's could set it.
4. **Kept from choice 12:** a completion addressed to the guest is still dropped. The writer's delivery after the stop runs nothing; the future is not resolved; no parked write is driven, so a write the guest was not yet told was complete is not written.
5. **The order in `walker.close` is unchanged.** The producers' stop comes first, and it is what the writer reads as its signal to flush; the sockets' stop then sets the deadline and closes what has no writer.

**Lock order:** unchanged from ADR 0125's. `trackWriter` takes `hostNet.mu` under `asyncHandles.mu`, where `track` was, the permitted order. The writer's end takes `hostNet.mu` with no lock held and closes the connection after releasing it. The flush is a `Write` with no lock held. No channel operation is added, under a lock or otherwise (§4 B-MM-3).

### Divergences from wasmtime 49.0.3, recorded

- **A peer that reads nothing, with the kernel's buffers full.** Burroughs' `Close` waits `sendFlushBound` and then loses what the flush could not write. What wasmtime does with a guest's writes in that state is unmeasured.

## Consequences

- **Witnesses** (`p3tcp_test.go`):
  - `TestCloseDeliversTheBytesASendWriterHolds`, also under `-race`, black-box: a guest writes and exits at once, and the peer's count, read to its end after `Close`, equals wasmtime 49.0.3's (`testdata/tcp-send-tail.reading`, 10 of 10 per row). The peer is slower than the guest, 4 KiB per 4 ms, and both ends' buffers are shrunk (`smallSendBufferDial`), so the host holds bytes the guest was told it wrote when the guest exits. Two rows:
    - **`tcp-send-tail.wasm`**, a new hand-written callback-lifted guest: 256 KiB in 64 KiB writes, then two 16 KiB writes, drop and exit. A 64 KiB write fills the buffer, so each completes once the writer has taken the one before; the first 16 KiB completes the same way, and the second completes at once, into the buffer, behind the first's `Write`. So at the exit the writer holds a `Write` in flight **and** bytes in its buffer: the specimen's shape made certain rather than a race. wasmtime: result and peer 294912.
    - **`p3tcp.wasm`**, Rust std's `write_all` of 256 KiB against the same peer: a `Write` in flight only, since std's one write loop leaves the buffer empty. wasmtime: peer 262144.
    - **It is deterministic in practice, not by construction.** It rests on the peer's pace keeping the kernel full for longer than the guest takes from its last write to `Close`'s stop, which is microseconds against tens of milliseconds. Repaired: 50 of 50 runs and 10 of 10 under `-race` (darwin/arm64).
    - The simpler shape, a peer that reads only after `Close` returns, does not discriminate with an existing guest: the kernel must then hold every byte, and with room in it the writer drains before the stop. p3nettail against such a peer read 262144 of 262144 in 30 of 30 runs on the tree before this repair.
  - `TestTheStopsFlushEndsAtItsDeadline`, also under `-race`, white-box at the registry: a writer holding 16 MiB at the stop, to a peer that never reads. `wait` takes at least `sendFlushBound` and returns; the writer closes the connection; no socket goroutine remains. It needs more than the 64 KiB a guest can leave in the buffer, because darwin's loopback keeps taking bytes from a writer whose peer never reads (528384 bytes in 2 s, in 64 KiB writes, both ends shrunk, measured). Repaired: 10 of 10, and 10 of 10 under `-race`.
- **Neuters, run**, each named at its site (darwin/arm64):
  - the flush removed: tcp-send-tail's peer reads 278528, its last 16 KiB lost, 50 of 50 and 10 of 10 under `-race`; the deadline witness's `wait` returns at once, 20 of 20. p3tcp's row passes.
  - `hostNet.stop` closing a writer's connection, as before #1308: tcp-send-tail's peer reads 266240, 20 of 20 and 10 of 10 under `-race`; p3tcp's 200704, 20 of 20, and 200704 to 208896 under `-race`, 10 of 10; the deadline witness's `wait` returns before the bound, 20 of 20 and 10 of 10.
  - both, the tree before #1308: the same figures, 50 of 50 and 10 of 10 per row.
  - the deadline skipped: the deadline witness's `wait` does not return within its 10 s hang guard. `TestP3TcpMatchesWasmtime`, `TestCloseEndsASocketsGoroutines` and `TestCloseDeliversTheBytesASendWriterHolds` pass under it: their peers read, or their writers hold nothing the kernel will not take.
  - **NEUTER claims re-run, unchanged:** `hostNet.stop` returning before it closes the connections, and `walker.close` without `stopAllSockets`, each hang `TestCloseEndsASocketsGoroutines`' receive row (its writer has ended by then, so its connection is one the stop closes), and the send row passes both, as their comments say.
- **C2's tail row**, with this repair applied to a scratch copy of `c2-http-witness` and not committed there: `TestC2BurroughsRunMatchesTheHostReading`, 20 of 20 and 5 of 5 under `-race`, both rows.
- **Tests changed** (pre-existing): `tcpPeer` gains the paced mode and its count. `TestForkWasip3NetTailMatchesWasmtime`'s comment said the sink *"discards at walker.close"*, which this falsifies; it now cites this ADR. No reading changed: `forkp3.reading`'s p3nettail paragraph says the same of `walker.close`, and is left as the dated record of what was measured on 2026-11-16 at a54b95c.
- **What a guest sees.** A WASI 0.3 guest that writes to a socket and exits has every byte it was told was written delivered to the peer, as on wasmtime, unless the peer has stopped reading for `sendFlushBound`. `Close` returns after the flush, or after the bound.

## Rollback

- Dropping the flush, or closing a writer's connection at the stop, discards bytes a guest was told were complete (#1308).
- Dropping the deadline lets a peer that never reads hold `Close` for ever.
