# 0126 — A sync lower of an async host import blocks until it resolves, and ip-name-lookup is served through it behind its own grant

Date: 2026-11-14 · Status: **accepted**, 2026-11-14. Scott, in session, approving the plan for v0.14.0's twelfth slice (E7c, the last of #1235's three), and his public-API stamp on [#1235](https://github.com/scttfrdmn/burroughs/issues/1235) for `ComponentConfig.AllowNameLookup` and `burroughs run --allow-ip-name-lookup`. The plan approval was given in session and has no comment to cite, so that half of the record is the actor's, and commits resting on it are `Ratio-Class: carried`. The stamp's limit is ADR 0097's form: this slice adds that one field and that one flag to the public surface and nothing else, which with E7a's two is the whole of the stamp. No contract text changes. ADR 0121's choice 5, the refusal at instantiate this ADR retires, rests on the same in-session approval as the rest of 0121 and named this as its follow-up, so it is replaced rather than reversed: ADR 0121 gets a postscript, and its text is unchanged. Filed against [#1278](https://github.com/scttfrdmn/burroughs/issues/1278) and [#1268](https://github.com/scttfrdmn/burroughs/issues/1268).

## Context

`wasi:sockets/ip-name-lookup@0.3.0` has one function (the WIT the nightly's std vendors, `wasip3-0.8.0+wasi-0.3.0`, `sockets.wit`):

```wit
resolve-addresses: async func(name: string) -> result<list<ip-address>, error-code>;
```

Its `error-code` is the interface's own variant: `access-denied`, `invalid-argument`, `name-unresolvable`, `temporary-resolver-failure`, `permanent-resolver-failure`, `other(option<string>)`. An `ip-address` is `variant { ipv4(tuple<u8 ×4>), ipv6(tuple<u16 ×8>) }`.

**wasi-libc lowers it without the async option.** A Rust std guest that resolves a name, read with `wasm-tools print`: `(canon lower (func $resolve-addresses) (memory $memory) (realloc …) string-encoding=utf8)`, core type `(i32 i32 i32) -> ()`, the name's `(ptr, len)` and a return pointer. That is the pin's sync lower of an async callee. On main at 78cff79 the walk refused it at instantiate (ADR 0121 choice 5, `ErrUnsupportedForm`), and #1268 had said no toolchain emits the form. wasi-libc's `getaddrinfo` does; #1268's *"Reach"* paragraph was true of `wait-for` and `wait-until` and is false of this import.

### What the pin does

`canon_lower`'s sync arm (def:canon_lower):

```python
subtask.on_cancel = callee(on_start, on_resolve)
...
if not opts.async_:
  if not subtask.resolved():
    thread.wait_until(subtask.resolved)
  subtask.deliver_resolve()
  return subtask.flat_results
```

with `max_flat_params = MAX_FLAT_PARAMS` and `max_flat_results = MAX_FLAT_RESULTS` (16 and 1), so a result that flattens past one word is stored at the return pointer, the last core argument. `wait_until` blocks the calling Thread; nothing can cancel the subtask, because the guest holds no handle to it. The wait meets the sync tail only when the lifted function is sync-typed (def:canon_lift:"if not ft.async_:", `trap_if(not candidates)`), grave #1267's rule.

### What the oracle does, measured

wasmtime 49.0.3 (`.oracle/wasmtime-49.0.3/wasmtime`, never the 50.0.1 on PATH), darwin/arm64. No row needs a DNS server.

- **`sync-lower-wait-for.wasm`** (#1268's fixture), `--invoke`, 10 of 10 each: the sync-typed `go` traps *"cannot block a synchronous task before returning"*, and `go-async-typed` returns 7. Committed as `testdata/sync-lower-wait-for.reading`.
- **`name-lookup.wasm`**, a hand-written component that sync-lowers `resolve-addresses` and returns four result bytes per row (`testdata/name-lookup.reading`), 10 of 10 per line:
  - `localhost`, `127.0.0.1`, `::1`, `[::1]` and the IPv4 shorthand `127.1` are ok, non-empty, hold the row's address, and are all loopback; `::ffff:127.0.0.1` is ok and returned as that IPv6 address, not unmapped.
  - `a b`, the empty name and the zoned literal `fe80::1%1` are `invalid-argument`.
  - **Without `-S allow-ip-name-lookup` every name is `permanent-resolver-failure`**, the literals and the invalid names included, so the grant is checked first. The plan forecast that the code would be measured; it is not `access-denied`.
  - The lookup needs no `-S inherit-network`: it is its own grant, as it is in Scott's stamp.
  - The sync-typed `run-sync` traps at `localhost`'s lookup, *"cannot block a synchronous task before returning"*, and returns every other row, so wasmtime resolves literals and invalid names inline.
- **`p3lookup`**, a dependency-free Rust `wasm32-wasip3` std guest (below), `wasmtime run -S inherit-network -S allow-ip-name-lookup p3lookup.wasm <echo-port>`, 10 of 10 byte-identical, exit 0, empty stderr: localhost resolves, includes 127.0.0.1 and is all loopback; the literal resolves to itself; `"a b"` and the empty name are `InvalidInput: Invalid argument (os error 28)`; `TcpStream::connect(("localhost", port))` connects and the 1 KiB echo matches. With `-S inherit-network` alone, 10 of 10: each name is `Uncategorized: failed to lookup address information: Non-recoverable error` (wasi-libc's EAI_FAIL), the literal still resolves, and the connect is `Uncategorized`.
- **Traces** (`WASMTIME_LOG=wasmtime_wasi=trace`): the literal never reaches `resolve-addresses` (wasi-libc parses it); `localhost` is answered with ::1 first, so std's connect tries `[::1]:port` (refused, since the peer listens on 127.0.0.1) and then 127.0.0.1. A first probe resolved `foo..bar` and `-`; wasmtime handed both to getaddrinfo, which logs a failed DNS resolution, so the guest resolves neither.

## Options

For the blocking sync lower:

1. **A waiting path in the sync lower** (#1268's fix shape): start the impl as the async lower does, and if it has not resolved, wait inside `c.Blocking(h.suspending(…))`, judged by the sync tail, the way the sync `subtask.cancel` waits; then lower the result through the sync lower's own options, on the caller's goroutine.
2. **A sync impl of `resolve-addresses` that blocks in `c.Blocking`** (#1278's alternative). *Costs:* a second implementation of the lookup, one per lowering form; it never meets the sync tail, so a sync-typed caller blocks where the pin and wasmtime trap; and it would have left #1268's form unbuilt for `wait-for`.

For where the sync lower's result is stored: **on the caller's goroutine, after the wait**, or by the resolver, as the async lower's `onResolve` stores. The second needs the run slot from a goroutine and stores through a caller that is blocked; the first stores where a sync impl stores, outside every engine lock, and can return a one-word result as a core value.

For a name that is an IP address: parse it on the host and answer inline, or hand every name to Go's resolver. *The second's cost:* Go's pure resolver sends `127.1` to DNS, which no reading may depend on, and the oracle answers literals inline.

## Choice

1. **The blocking sync lower** (option 1, `syncLowerOfAsync`, `async_lower.go`). `resolveHost` consults the async table for a sync lower that every sync table missed, and the walk binds it to the blocking adapter instead of refusing it:
   - Its core signature is the sync lower's: parameters past `MAX_FLAT_PARAMS` (16) cross as one pointer to their tuple and are loaded flat, ADR 0123's mechanism at the sync threshold; a result past `MAX_FLAT_RESULTS` (1) is stored at the return pointer; a one-word result is returned.
   - **The impl starts as the async lower's does,** with `onStart` and `onResolve`, as host code (`enterHost`). Its `onResolve` records the value and resolves the subtask under `asyncHandles.mu`, which closes the subtask's wake; it writes no guest memory, so it takes no run slot, from whichever goroutine calls it.
   - **Unresolved after the impl returns, the caller waits** inside `c.Blocking(h.suspending(c, ready, true, …))`: `c.Blocking` so a stop-the-world sees the agent blocked (§5 H-1), `suspending` so the run slot is released and another task of the instance may run, and `judged` true so the sync tail judges it. `ready` is `subtask.resolved`, read under `asyncHandles.mu`.
   - **The sync tail judges the wait by the lifted function's type** (grave #1267): a sync-typed function with no candidate traps *"cannot block a synchronous task before returning"*, and an async-typed one waits.
   - **The result is lowered after the wait**, on the caller's goroutine with the run slot held again and no engine lock held, through the lower's own memory and realloc: `canon.StoreVia` at the return pointer, or one flat word returned. A future or stream result is refused by name at the call: no host import returns one through this path.
   - **Nothing cancels a sync-lowered subtask.** It is not in the handle table, and its `onCancel` is never called.
   - **A pending sync-lowered call is a park waker** (`syncLowerWaits`, counted in `hostWaitPendingLocked`): its return resumes a Thread of the instance, which may wake any park. It counts from the impl's start to the lower's return.
   - **Close ends it.** `killAllParked` aborts a waiting Thread, and `stopHostWaits` also closes every pending sync lower's wake, so a waiter Close's Thread kill did not reach returns too, with `errThreadKilled`; no sync lower starts after the stop. **The waiter selects on its own copy of the wake**, taken at the call's start, because the stop can run between the call's start and its wait, and it takes the wake off the subtask so that it is closed once. The call is removed from the registry by a `defer`, because an agent ended at a safepoint after Close unwinds through the wait's `Blocking`, past any statement after it.
   - ADR 0121's choice 5, the refusal at instantiate, is retired, and ADR 0121 gets a postscript saying so.
2. **`resolve-addresses` is an async host import** (`hostlookup.go`), registered with `serve(m, wasiP3, …)` in `asyncSource`, so it is served through both lowers. In order:
   - **The grant first.** With `Host.AllowNameLookup` false, every name is `err(permanent-resolver-failure)`, inline: wasmtime 49.0.3's code, measured. It is independent of `InheritNetwork`, as it is in wasmtime and in the stamp.
   - **A literal is answered inline,** as the oracle answers it: an address `netip.ParseAddr` accepts without a zone, the same in brackets for IPv6, or a WHATWG IPv4 form (`127.1`, `0x7f.1`, the URL standard's host parser, which wasmtime's `url::Host::parse` is). An IPv4-mapped literal is returned as given, as wasmtime returns it.
   - **A syntactically invalid name is `invalid-argument` inline**: the empty name, a URL standard forbidden domain code point (space, control characters, `#%/:<>?@[\]^|`, DEL), a name ending in a number that is not an IPv4 address, or a name that is not ASCII. The last is a recorded divergence (below).
   - **Otherwise the lookup runs on a goroutine,** `net.Resolver.LookupNetIP(ctx, "ip", name)` (the one lookup site, in the census), and resolves through `onResolve`. One claim decides who resolves, E5's pattern (`hostwait.go`): the lookup's return, the guest's `subtask.cancel` of an async-lowered lookup (which cancels the context and resolves CANCELLED_BEFORE_RETURNED inline), or Close (which cancels it and resolves nothing). It is counted in `sockPending`, a waker, until its result is stored.
   - **The answers** are unmapped (the WIT: *"never returns IPv4-mapped IPv6 addresses"*), and an empty answer is `name-unresolvable` (*"never succeeds with 0 results"*).
   - **Error mapping:** a `*net.DNSError` that is not-found is `name-unresolvable`; temporary or a timeout, `temporary-resolver-failure`; any other DNS error, `permanent-resolver-failure`; anything else, `other(none)`, since a string payload would be lowered through the guest's realloc from the lookup's store.
   - **The `list<ip-address>`** is lowered by the codec, through the guest's realloc with no engine lock held: by the sync lower after its wait, or by the async lower's result store (ADR 0101's amendment, its realloc outside `asyncHandles.mu`).
   - **The registry** is the instance's `hostNet`, beside the dials: `stop` cancels every pending lookup, and Close waits every lookup goroutine out with the other socket goroutines.
3. **The grant is threaded as `InheritNetwork` is.** `Host.AllowNameLookup` (internal), set from `ComponentConfig.AllowNameLookup` by `Run` and `LoadComponent`, and by `burroughs run --allow-ip-name-lookup`. A bare `LoadComponent` stays denied. The flag on a wasip1 command or a core module is refused by name, #1128's rule. The exported field's doc is the stamp's words.

**Lock order:** unchanged from ADR 0125's. A lookup takes `hostNet.mu` only for its registry and never with `asyncHandles.mu` held inside it.

**Names added below the public boundary.** `component.Host.AllowNameLookup` (internal; `ComponentConfig` sets it). The public surface is the stamped field and flag only.

### Divergences from wasmtime 49.0.3, recorded

- **A non-ASCII name is `invalid-argument`.** The WIT converts it with IDNA, which wasmtime does through the `url` crate. Go's standard library has no IDNA, and the engine's `go.mod` stays dependency-free, so the name is refused before any lookup rather than sent unconverted. Unmeasured on the oracle, because the converted name would need a DNS server. Filed as #1287, together with the percent-decoding, the sync-lowered stream or future result, and the unmeasured resolver-error codes.
- **A percent sign is `invalid-argument`.** The URL standard's host parser percent-decodes before it validates; this host does not decode. Unmeasured.
- **Error codes from a real resolver are unmeasured**: no reading may need a DNS server. The mapping follows the WIT's POSIX equivalents.

## Consequences

- **Witnesses:**
  - `TestASyncLowerOfAnAsyncImportBlocksOrTrapsByType`, in both gate states: `sync-lower-wait-for.wasm` against `sync-lower-wait-for.reading`, the trap in `go` and 7 in `go-async-typed`. It replaces the test that asserted choice 5's refusal. Its one premise is the fixture's own: `go`'s 10 ms timer is unresolved when the sync tail judges the wait, microseconds after it starts.
  - `TestP3LookupMatchesWasmtime`, in both gate states: `testdata/p3lookup/p3lookup.wasm`, a dependency-free Rust `wasm32-wasip3` std program (nightly 1.100.0, built offline by `p3lookup/build.sh`, the same sha256 on a second build), against an in-test echo peer. Granted, its stdout, empty stderr and exit 0 equal `p3lookup.reading`; with the lookup denied, `p3lookup-denied.reading`. It prints order-free properties of localhost's answers, so neither the resolver's order nor which loopbacks a host has reaches the comparison. After `Close` no socket goroutine remains.
  - `TestNameLookupMatchesWasmtime`, in both gate states: every one of `name-lookup.reading`'s 42 rows, 14 per export, `run-async` lowering the lookup with the async option so localhost's list is stored by the async lower's result store from the lookup's goroutine. **localhost's `run-sync` row is read with its lookup held:** the hosts file can answer before the sync tail judges the wait, which the pin allows and which would make the row read wasmtime's `run` value, so the row is made deterministic rather than rested on that race (E7a's CI lesson).
  - `TestAPendingSyncLoweredCallOutlivesTheParkBound`: `sync-lower-park.wasm`'s `block` held in a sync lower of a test `op`, its callback-lifted `park` waiting on an empty set with the bound at 5 ms. The park does not expire over twenty bounds; once `op` resolves, `block` returns 7 and the park expires, the bound keeping its purpose.
  - `TestCloseEndsAPendingSyncLoweredCall` (also under `-race`), `TestCloseWakesASyncLoweredCallWithNoThread`, `TestAStopBeforeTheWaitStillEndsTheCall`, `TestASyncLowerReturnsAOneWordResultAndStoresAWiderOne`.
  - **Two defects the witnesses found in the first build, each repaired in the slice:** the registry removal after the wait (below, under the stop's neuter), and the waiter reading the subtask's wake at its wait. `TestCloseEndsAPendingSyncLoweredCall` hung 1 run of 12 under `-race`: its Close came after the call registered and before it waited, so the stop had already taken the wake and the waiter selected on nil. `TestAStopBeforeTheWaitStillEndsTheCall` makes that window certain, its impl running the stop itself. The repaired witnesses passed 30 runs under `-race`, and the two Close witnesses 200.
  - `TestACancelledLookupResolvesCancelledAndItsGoroutineEnds` and `TestCloseEndsAPendingLookupAndItsGoroutine`, both also under `-race`, with a held lookup; `TestALookupErrorMapsByTheWITsEquivalents`; `TestANameIsALiteralAnInvalidNameOrALookup`.
  - At the public boundary `TestAllowNameLookupReachesBothConfiguredPaths` (Run, LoadComponent, and the bare LoadComponent denied); at the CLI `TestRunAComponentWithAllowIPNameLookupResolves` and `TestAllowIPNameLookupIsAComponentsFlag`.
- **Neuters, run**, each named at its site:
  - the refusal at instantiate restored: `sync-lower-wait-for`, `p3lookup` and `name-lookup` are refused at instantiate, every row.
  - the sync lower's wait not judged: `go` returns 7, and the held `run-sync(0)` waits until the hang guard.
  - `syncTail` judging by lift form (grave #1267's defect): `go-async-typed`, p3lookup's granted rows and `run(0) granted` trap. p3lookup's lookup-denied rows pass, since every lookup there resolves inline.
  - `hostWaitPendingLocked` without the sync-lower term: the park reads `ErrLiftParkExpired` within its exposure.
  - `stopHostWaits` skipping the wakes: `TestCloseWakesASyncLoweredCallWithNoThread`'s call never returns. **`TestCloseEndsAPendingSyncLoweredCall` passes under it**, because `killAllParked` aborts the Thread's wait first. Run under that neuter, its registry check found the first build's defect: the call left the registry in line after the wait, and an agent ended at a safepoint after Close unwinds past that line. The removal is deferred.
  - the waiter selecting on the subtask's wake read at the wait, as the first build did: `TestAStopBeforeTheWaitStillEndsTheCall`'s call never returns.
  - the grant check dropped: every `off` row and both lookup-denied rows fail.
  - the grant-off code `access-denied`: every `off` row reads 0x80, and the lookup-denied rows print *"PermissionDenied: Permission denied (os error 2)"* where wasmtime's EAI_FAIL prints *"Non-recoverable error"*.
  - the literal parse skipped: rows 1, 2, 5, 6 and 7 read `invalid-argument` in both exports; each literal fails the name check, so none reaches the resolver.
  - not-found mapped to `permanent-resolver-failure`: the mapping test's not-found row.
  - the lookup's `finish` not resolving: `run(0)` and `run-async(0)` granted, localhost through each lower, wait until the hang guard.
  - the lookup's `cancelSubtask` doing nothing: the cancel reads BLOCKED, and the lookup is not cancelled until the guard.
  - `hostNet.stop` skipping the lookups' cancel: Close waits on the held lookup until the hang guard.
  - either root path's `h.AllowNameLookup` dropped: that row is denied. `runComponent` not passing the flag: the CLI's granted row prints the denied reading. The CLI refusal dropped: both of its rows exit 0.
  - **The plan's fourth p3lookup neuter, an unsorted comparison, has no line to change:** the guest sorts, and reads only order-free properties, so no order reaches the comparison.
- **Tests changed** (pre-existing):
  - `TestASyncLowerOfWaitForIsRefusedAtInstantiate` (`hostwait_test.go`) is removed: it asserted choice 5's refusal, which this ADR retires. `TestASyncLowerOfAnAsyncImportBlocksOrTrapsByType` replaces it.
  - `socketGoroutines` (`p3tcp_test.go`), which every socket witness's `requireNoSocketGoroutines` reads, also lists a goroutine in `(*Host).resolveAddresses`, so a leaked lookup is caught.
  - `internal/testenv/observer_test.go`: the census gains the lookup's site.
  - Every other suite passes unchanged, with and without `-race`: p3tcp, p3tcpserve, p3cat, p3std, p3sleep, p3sys, p3async-hello, p3hello and p3echo, and the async lower, cancel, liftpark and runslot suites. No std guest's reading changed.
- **What a guest sees.** A WASI 0.3 std guest's `to_socket_addrs` and `TcpStream::connect` of a host name resolve when the embedder grants name lookup, and fail with wasi-libc's EAI_FAIL when it does not. A guest that sync-lowers any async host import blocks until it resolves, or traps if its function is sync-typed.
- **Public surface:** `ComponentConfig.AllowNameLookup` and `--allow-ip-name-lookup`, as stamped, and nothing else.

## Rollback

- Restoring the refusal at instantiate refuses every std guest that resolves a name, and #1268's fixture.
- Dropping the grant check lets a guest resolve names without `AllowNameLookup`.
- Dropping the lookup from `hostNet.stop` leaves a lookup's goroutine running past Close.
