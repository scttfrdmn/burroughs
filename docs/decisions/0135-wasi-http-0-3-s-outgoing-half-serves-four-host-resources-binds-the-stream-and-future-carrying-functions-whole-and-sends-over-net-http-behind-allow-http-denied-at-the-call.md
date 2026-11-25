# 0135 — `wasi:http@0.3`'s outgoing half serves four host resources, binds the stream- and future-carrying functions whole, and sends over `net/http` behind `AllowHTTP`, denied at the call

Date: 2026-11-24 · Status: **accepted**, 2026-11-24. The public half is Scott's: his rulings on E2, given in session and recorded on [#1375](https://github.com/scttfrdmn/burroughs/issues/1375) (*"The outgoing grant: `ComponentConfig.AllowHTTP` and `burroughs run --allow-http`, shaped like ADR 0126. Ungranted, `client.send` answers `HTTP-request-denied` at the call. This is a named divergence from wasmtime's link failure."*). The stamp's limit is ADR 0097's form: this slice adds that one field and that one flag, and nothing else public. The rest is an engine mechanism inside `internal/component`, which no contract text governs and which reverses no stamped ADR, so by the #647 review (*"Report decisions taken, not rulings requested"*) it is decided here and reported as a decision taken. The design is R2's, from its closing comment on [#1372](https://github.com/scttfrdmn/burroughs/issues/1372). The record is the actor's, so commits resting on it are `Ratio-Class: carried`. Filed against [#1383](https://github.com/scttfrdmn/burroughs/issues/1383), E2-out of the v0.16.0 plan ([#1370](https://github.com/scttfrdmn/burroughs/issues/1370)).

**Every citation into this tree names a symbol rather than a line** (ADR 0047).

## Context

`wasi:http@0.3.0` (the WIT the nightly's std vendors, `wasip3-0.8.0+wasi-0.3.0`, `deps/http.wit`) has three interfaces. `types` has four resources and every method of each is sync:

- **`fields`:** a constructor, `from-list`, `get`, `has`, `set`, `delete`, `get-and-delete`, `append`, `copy-all` and `clone`. A `fields` from `request.get-headers` and the like may be immutable, and then `set`, `append` and `delete` fail with `header-error.immutable`.
- **`request`:** the static `new(headers, contents: option<stream<u8>>, trailers: future<result<option<trailers>, error-code>>, options: option<request-options>) -> tuple<request, future<result<_, error-code>>>`, getters and setters for the method, path-with-query, scheme and authority, `get-options`, `get-headers`, and the static `consume-body(this: request, res: future<result<_, error-code>>) -> tuple<stream<u8>, future<result<option<trailers>, error-code>>>`.
- **`request-options`:** a constructor, three timeouts' getters and setters (each setter `result<_, request-options-error>`), and `clone`.
- **`response`:** the static `new`, `consume-body` (request's shapes without options), the status code's getter and setter, and `get-headers`.

`client` has one function, `send: async func(request: request) -> result<response, error-code>`. `error-code` is a variant of 39 cases, several carrying strings.

R2 found three gaps for this half (#1372 (c)): stream- and future-typed signatures are refused generically (`unmodeledInSig`, `unmodeledValKind`), so `new` and `consume-body` cannot bind as resource methods; nothing reads a guest's future from the host side; and there is no `send`.

### What the oracle does, measured

wasmtime 49.0.3 (`.oracle/wasmtime-49.0.3/wasmtime`), darwin/arm64, over `p3http`, a Rust `wasm32-wasip3` guest built offline against wit-bindgen 0.61.1 (testdata/p3http), each row one run against a Go peer on 127.0.0.1. The rows are committed as `p3http.reading` before any engine code.

- **`run` without `-S http` does not link:** *"component imports instance `wasi:http/types@0.3.0`, but a matching implementation was not found in the linker … resource implementation is missing"*. With `-S http` alone, and no network grant, `send` reaches 127.0.0.1.
- **`send`:** a GET answers the status, the peer's headers and its body, trailers `none`, and `new`'s future `ok`. A POST's body is sent chunked; a `content-length` header makes it fixed-length. **A request trailer is sent only if a `trailer` header declares it.** A trailers future resolved `err(HTTP-protocol-error)` fails the send with that code. A port nobody listens on is `connection-refused`, a `.invalid` name `DNS-error` (its `rcode` some string), `https` to a plain peer `TLS-protocol-error`, a scheme of `other("ftp")` `HTTP-protocol-error`, no scheme `https`'s default (so `TLS-protocol-error` against the plain peer), no authority and no path-with-query each `HTTP-request-URI-invalid`, and a first-byte timeout over a peer that never answers `connection-read-timeout`. `new`'s future resolves with the send's error code each time, and `ok` on success. The host adds no `user-agent` and no `accept-encoding`.
- **The response:** its headers are immutable (`set`, `append`, `delete` and `get-and-delete` all `immutable`), and so are its trailers. A response trailer reaches the guest. A body dropped after a partial read resolves the trailers future `ok(none)`. The `res` future `consume-body` takes is read at once: a write to it completes before the body is read.
- **`fields`:** names are lowercased (`append("A")` then `copy-all` reads `a`). Entries are grouped by name in first-insertion order, `set` replaces a name's values in place, and a removal moves the last group into the removed one's place (the `http` crate's `HeaderMap`, which removes by swap). A name must be a token, else `invalid-syntax`; a value must be HTAB, 0x20–0x7E or 0x80–0xFF; a `content-length` value must be digits. `connection`, `keep-alive`, `proxy-connection`, `transfer-encoding`, `upgrade`, `host`, `http2-settings` and `proxy-authorization` are `forbidden`, and `te` is not. The checks come in that order and before immutability. An invalid name in `has` and `get` is false and empty, and in `delete` `invalid-syntax`.
- **`request`:** the defaults are `GET` and none for the rest. `other("GET")` reads back `get`; a method must be a token. A path-with-query must begin with `/`, and `""` reads back `/`; `*` and `a` are refused. A scheme of `other("http")` reads back `HTTP`. An authority must be non-empty and may carry userinfo. `get-headers` is immutable and its `clone` mutable. The status code must be 100–599.
- **`request-options`:** all three timeouts are accepted and read back; `get-options` is immutable (`immutable`), its `clone` mutable, and dropping the request before its options child does not trap.
- **A guest-made message's `consume-body` hands back the guest's own ends:** the body stream reads what the guest wrote into it, and **reading the trailers future traps**, *"cannot read from and write to intra-component future/stream with non-numeric payload"*, since both its ends are the guest's. With `contents` none the body is an empty stream. The value the guest writes to `res` becomes `new`'s future's value. A message dropped unconsumed and unsent resolves its future `ok`.

## Options

**For the stream- and future-carrying functions:**

1. **Widen `unmodeledInSig`** to admit a stream or future and bind these as resource methods. *Costs:* the predicate is the generic refusal for every implemented sync import, and a stream in a sync impl's signature has nothing to lower it: the codec cannot reach the async table.
2. **A carrier binding: the host declares the function's whole type, streams and futures included, and the link checks the guest's against it,** bound with both tables as a stream consumer is. *For:* `checkConsumerPayload` and `checkListenSignature` are each one shape of this already. One check serves the four functions and any later one.
3. **One hand-written check per function.** *Costs:* four more checks of `checkListenSignature`'s kind, each a place to drift.

**For `send`:**

- **(a) `net/http`'s `Transport`,** one per instance. *For:* pooling, HTTP/1.1 and HTTP/2, TLS and proxies' absence are Go's, and a performance-partisan runtime should not hand-roll a client. *Costs:* Go's `Header` is a map, so the response's header order is lost, and it rewrites a few headers (below).
- **(b) A hand-written HTTP/1.1 client over `net.Conn`.** Ruled out: the work is a client, and its readings would be of the client and not of the host.

## Choice

**Option 2, with (a).**

### The four resources

Each is a host resource (`newHostResource`) whose object is a Go value:

- **`httpFields`** holds groups of (lowercased name, values) in the `HeaderMap`'s order, and an immutable flag. Every check is the measured one, in the measured order: name syntax, then forbidden, then value syntax, then immutability. The constructor, `from-list` and `clone` mint a mutable one.
- **`httpRequest`** holds its method, path-with-query, scheme and authority, its headers (immutable from `new`), its options (an immutable copy), and its body: the guest's stream end and trailers future, or nothing. `get-headers` and `get-options` mint a fresh immutable copy each call, which is why dropping the parent first cannot trap: a copy is no child. The validators follow the `http` crate's byte classes, as far as the rows above read them.
- **`httpOptions`** holds three optional durations and an immutable flag. All three are honoured by `send` (below), so none answers `not-supported`, as none does on wasmtime.
- **`httpResponse`** holds its status, its headers, and its body: the guest's ends for one `response.new` made, or the host's `*http.Response` for one `send` made.

Every getter and setter is a sync resource method (`method`), so it is marshalled through the codec at the caller's realloc, under no lock.

### The carrier binding

**`hostResourceSource.sigs`** is the host's declaration of a function's whole type, as `sigType`s: a codec type, a `stream<u8>`, a `future<P>` with a codec payload, or an option or tuple of those (`httpSignatures`). It covers every `wasi:http` import, not only the four, since every one is lowered at a layout the host fixes. At instantiate the guest's declared signature must match it parameter by parameter and in its result (`walker.checkDeclaredSignature`), with handles resolved through the host's RTs and payloads compared with `canon.TypeEqual`. Anything else is `ErrLinkRefused` naming the import. The four functions are stream consumers bound with both tables through `bindRes`, as `tcp-socket.send` is, and stream consumers are not refused by `unmodeledInSig`.

- **A stream argument lifts as write-via-stream's does** (`liftStreamArgLocked`), with the pin's four traps. The registry holds it for its message (`hostHTTP.hold`), so one end handed to two messages traps the second.
- **A future argument lifts by the same rule** (`liftFutureArgLocked`): it must be a readable future end of the parameter's payload type, IDLE and joined to no set, and each is a trap in def:lift_async_value's order. One already handed to the host traps as an index the guest no longer holds, def:Table.get's.
- **Neither handle is removed from the guest's table,** which is the existing divergence `liftStreamArgLocked` names, now named for futures too. That is what lets `consume-body` hand a guest's own end back at its own index.
- **The results are minted under `asyncHandles.mu`** and their i32s written at the return pointer, as receive's are. Every host-minted future is typed by its payload (`elemOfCanon`, `elemOfPayload`'s twin for a type).

### The host reads a guest future

`readableFutureEnd.hostTake`, when set, is the host reading the end. A guest `future.write` that finds it runs it instead of the intra-instance copy (`futureWriteLocked`): it lifts the value at the end's payload type from the writer's memory, an own by def:lift_own (`takeAs`), and the write completes COMPLETED. **A write parked before the host armed its read is taken at the arming.** Either way the lift runs inside a guest call under the run slot, so no goroutine reads guest memory. The host dropping its reader notifies the writer DROPPED (`dropReaderOfWriterLocked`).

### `new`, `consume-body` and the transmission future

- **`new`** takes the headers' own, lifts the stream and the trailers future, takes the options' own as an immutable copy, and mints the message and its transmission future, a host future of `result<_, error-code>`.
- **The transmission future resolves:**
  - `ok` at the message's drop if it was neither sent nor consumed;
  - with `send`'s outcome if it was sent;
  - with the value the guest writes to `res` if it was consumed;
  - **with a string payload made `none`.** A host future's completion is stored under `asyncHandles.mu`, so lowering a string there would run the guest's realloc under an engine lock, which §4's B-MM rules forbid. It is a named divergence, and it reaches only a guest that writes an error carrying a string.
- **`consume-body` of a guest-made message is a pass-through,** as wasmtime's is. It returns the guest's own stream end, or with `contents` none a host-produced empty stream (`streamProducer` with no source and no future). It also returns the guest's own trailers future, so reading it traps as wasmtime's does (`copyFutureValueLocked`'s rule, already the pin's).
- **`consume-body` of a host response is ADR 0122's producer** over the response body:
  - its future is the trailers;
  - at the end of the body it is `ok(some(trailers))`, the trailers minted as an immutable own at that delivery, or `ok(none)` when the peer sent none;
  - at the reader's drop it is `ok(none)`;
  - a read error is its `error-code`.
- **The `res` future is read at once and its value dropped,** as wasmtime reads it. The body's Read is the producer's site (ADR 0122), counted in the instance's HTTP registry.

### `send`

`[async]wasi:http/client#send` is in `Host.asyncSource`. It takes the request's own.

**Ungranted, it resolves `err(HTTP-request-denied)` inline,** before it reads anything else, and the request's transmission future resolves the same, which is Scott's ruling. Two errors are inline too, each RETURNED with no goroutine: no authority or no path-with-query is `HTTP-request-URI-invalid`, and a scheme other than HTTP or HTTPS is `HTTP-protocol-error`. A scheme of none is `https`, wasmtime's default.

Otherwise it builds an `http.Request` and starts one goroutine, the new site this ADR authorises in the census (`TestEveryEngineGoroutineIsAtASiteADecisionAuthorises`). The goroutine runs `RoundTrip` on the instance's `Transport`. It runs no guest code and touches no guest memory, so §4's boundary model and the table twin (#662) are not engaged by it. Its outcome reaches the guest through `onResolve`, ADR 0101's discipline. One claim decides who resolves, E5's pattern: the round trip's return, the guest's cancel, or Close. A response's own is minted only by the claimant, in the instance's resource table under that table's lock, which is a host table and no guest memory.

**The request on the wire:**

- **The headers are copied as the fields hold them,** lowercased, as hyper writes them. No `user-agent` is added, and the `Transport` has `DisableCompression`, so no `accept-encoding` is added either. A `content-length` header becomes the request's `ContentLength`.
- **Request trailers are only the ones a `trailer` header declares,** hyper's rule as measured: they are declared before the send, and filled at the body's end from the trailers the guest wrote.
- **The body is the guest's stream,** read into a bounded host buffer by write-via-stream's consumer shape (`tcp-socket.send`'s, ADR 0123). The `Transport`'s own goroutine Reads it, `httpReqBody.Read`, under the instance's host-I/O condition, and delivers the room it freed under ADR 0101's discipline, which is where a parked guest write's copy runs, under the run slot. That goroutine is `net/http`'s, not an engine site, and it touches no guest memory. At the writer's drop the Read waits for the trailers future's value: `err(code)` fails the request with that code, as wasmtime's does.
- **The timeouts:** `connect-timeout` bounds the dial, and its expiry is `connection-timeout`. `first-byte-timeout` bounds the wait for the response's head. `between-bytes-timeout` bounds each Read of the body. Each of the last two expires as `connection-read-timeout`, wasmtime's code for the first (measured; the second is not).

**The response:** its status, its headers lowercased and immutable, and its body. Go's `Header` is a map, so **the header order is sorted by name rather than the wire's**, a named divergence. The `trailer` header, which Go moves into `Response.Trailer`, is put back from the declared keys.

### The error table

`httpErrorCode` maps a Go error to its case:

| Go error | `error-code` case | Measured? |
|---|---|---|
| `ECONNREFUSED` | `connection-refused` | yes |
| a `*net.DNSError` | `DNS-error`, both fields none | yes (wasmtime's `rcode` carries a string) |
| a TLS record or alert error | `TLS-protocol-error`, or `TLS-alert-received` | yes, for the first |
| an x509 error | `TLS-certificate-error` | no |
| a connect deadline | `connection-timeout` | no |
| the first-byte and between-bytes deadlines | `connection-read-timeout` | yes, for the first |
| a reset or broken pipe | `connection-terminated` | no |
| an unexpected EOF in the body | `HTTP-response-incomplete` | no |
| a malformed response | `HTTP-protocol-error` | no |
| anything else | `internal-error(none)` | no |

**No case carries a string,** so no realloc runs in a result store, including under `gate:cm-shared-memory`, whose off-agent store refuses one. The guest still reads the measured case.

### The grant

**`Host.AllowHTTP`** is set from **`ComponentConfig.AllowHTTP`** and **`burroughs run --allow-http`**, ADR 0126's shape. It is independent of `InheritNetwork` and `AllowNameLookup`, as `-S http` is of `-S inherit-network`. The four resources work ungranted, since they touch no network.

**The divergence:** wasmtime 49.0.3 refuses to link a component that imports `wasi:http/types@0.3.0` without `-S http`. Burroughs links it and answers `HTTP-request-denied` at `send`, so a guest that only builds requests, or that handles the denial, runs.

### Close

The instance's HTTP registry (`hostHTTP`, in `hostResources` beside `net` and `fs`) holds the `Transport`, every send in flight and every body Read. Close:

1. stops it after the producers, so no send goroutine starts after it;
2. cancels every send's context, which ends a round trip and every body Read;
3. closes the `Transport`'s idle connections;
4. waits every send goroutine and body Read out at the end, as the sockets are.

The request body's Read sees the producers' stop on the host-I/O condition and fails the request. **A pending send, and a request body being read, are wakers for the park bound** (`asyncHandles.httpPending`), as a dial is.

## Consequences

- **A component can make outgoing HTTP requests,** through `ComponentConfig.Run`, `ComponentConfig.LoadComponent` and `burroughs run --allow-http`, and read their status, headers, body and trailers. This is the public API the slice adds: one field and one flag.
- **Incoming `handle`, a serve loop and a host-to-host copy are not built** (E2-in-a/b/c). A stream the host produces, passed to `request.new`, is refused by name at `send`, as `write-via-stream` refuses one (ADR 0122).
- **`wasi:http@0.2` is unchanged:** nothing of it is served.
- **Named divergences, each a reading:**
  - the deny-at-call;
  - the `DNS-error` payload, none where wasmtime's `rcode` is a string;
  - a transmission future's forwarded value, with its strings made none;
  - the response header order.
- **Named limits:**
  - **A future argument stays in the guest's table,** as a stream argument does, so a guest may still name an end it has handed to the host.
  - **A trailers own minted at the body's end** and never read is destroyed only at Close.
  - **The `res` future's value is not acted on** for a host response.
  - **Field-size limits are not imposed,** so `size-exceeded` is never answered; wasmtime answered none on the rows measured.
  - **A request's body Read runs on `net/http`'s goroutine.** A guest that never writes or drops its body stream holds that request open until Close, as a socket send's writer does.

## The build, as measured

On darwin/arm64, in the worktree of #1383's branch. The reading was committed before the engine code (`p3http.reading`, its guest's source, build script and peer beside it in `testdata/p3http`).

- **`TestP3HTTPMatchesWasmtime`** (`internal/component/p3http_test.go`): all 34 rows matched wasmtime 49.0.3's stdout and exit code, with `gate:cm-threads` off and on, plain and under `-race`. The two rows wasmtime ends with a trap trapped here, naming the same non-numeric-payload rule. No send or response-body goroutine survived any row's Close.
- **The grant:** `TestASendIsDeniedAtTheCallWithoutTheGrant` reads the deny-at-call and the reading's link failure. `TestAllowHTTPReachesBothConfiguredPaths` and `TestRunAComponentWithAllowHTTPSends` read the field and the flag on each public path.
- **What the neuters showed** (each run, each reverted; the full list is at the sites as `NEUTER, run:` comments):
  - **The host's read of a guest future is the whole of the bodies' end.** With the take dropped from `future.write`, sixteen rows hang: every one whose guest writes a trailers future or `res` the host is reading. With a write parked before the arming left untaken, twelve hang.
  - **Each wasmtime rule the rows read is load-bearing on its own row.** These are the swap-remove, the lowercasing, the value and path validators, `content-length`'s re-rendering, immutability, the declared-trailers filter, the suppressed user-agent and accept-encoding, the restored `trailer` header, and the timeout's code.
  - **One rule p3http cannot see:** a request body's end waiting for its trailers. Its guest writes them before net/http's Read reaches the end, so the fixture passes with the wait neutered, and `TestARequestBodysEndWaitsForItsTrailers` witnesses it by the place the Read parks.
  - **Close and the cancel** each hang their witnesses with their own lines neutered.

## Rollback

`Host.AllowHTTP` ignored, so every `send` answers `HTTP-request-denied`. The resources still work, and nothing reaches the network. Dropping the `wasi:http` entries from the host's tables returns every import to the refusing stub, at its call, which is where the tree stood before E2-out.
