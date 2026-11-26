# 0137 — A request is a host call of `handle` on an instance that serves one task at a time, reused only after its task exits, and a failure before the answer is a fixed 500

Date: 2026-11-25 · Status: **accepted**, 2026-11-25. The public surface is Scott's: his rulings on E2, given in session and recorded on [#1375](https://github.com/scttfrdmn/burroughs/issues/1375) (*"The serve API: `(*Component).Handler() http.Handler`, so the embedder owns the listener, TLS and timeouts. Plus a CLI `burroughs serve --addr`, mirroring `wasmtime serve`"*, and *"`serve` grants it by default"* of the outgoing grant). The stamp's limit is ADR 0097's form: this slice adds that method and that subcommand, and the subcommand accepts four of `burroughs run`'s grant flags with their syntax and meaning, which the slice's brief permitted in session (so `Ratio-Class: carried`). It adds nothing else public: no configuration field, no option on `Handler()`, and no concurrency knob. Everything else is an engine mechanism inside `internal/component`, which no contract text governs and which reverses no stamped ADR, so by the #647 review (*"Report decisions taken, not rulings requested"*) it is decided here and reported as a decision taken. The design is R2's, from its closing comment on [#1372](https://github.com/scttfrdmn/burroughs/issues/1372) ((e), the incoming half). The contract reading it rests on is Scott's stamp of 2026-11-24 on [#1382](https://github.com/scttfrdmn/burroughs/issues/1382), §6 R-5's append: *"A host-initiated call of a component export, such as `wasi:http/handler#handle` once per request … is a call, not a delivery of readiness"*. The record is the actor's, so commits resting on it are `Ratio-Class: carried`. Filed against [#1385](https://github.com/scttfrdmn/burroughs/issues/1385), E2-in-b of the v0.16.0 plan ([#1370](https://github.com/scttfrdmn/burroughs/issues/1370)).

**Every citation into this tree names a symbol rather than a line** (ADR 0047).

## Context

E2-out (ADR 0135) serves `wasi:http@0.3`'s four resources and `client.send`. E2-in-a (ADR 0136) made a host call of an async export answerable at the callee's `task.return`, with an own crossing each way through a host table of its own (`Instantiated.callReturning`, `newHostTable`). What is left of serving, R2's gap 5 (#1372 (c)), is the serve loop itself: a request minted as an own `request` whose body is the host's, the response's head, body and trailers written to Go's `http.ResponseWriter`, the instances requests run on, and the public surface.

`handle: async func(request: request) -> result<response, error-code>` is the whole interface (`wasi:http/handler@0.3.0`). An incoming request's body is a `stream<u8>` and a trailers future the guest reaches through `request.consume-body`; a response's are the guest's, given to `response.new`. Neither `wasi:cli/run` nor any other export is called: `wasmtime serve` calls only `handle`, so a Go guest's `main` would never run under it (#1372 (b)).

### What the oracle does, measured

`p3serve` (testdata, its source, `Cargo.lock`, `build.sh` and `PROVENANCE` committed beside it) is a Rust `wasm32-wasip3` guest built offline against wit-bindgen 0.61.1, E2-out's pipeline. It exports the handler and nothing else, one row per path, most of them returning their response first and writing its body from a spawned future, the vendored wasip3 crate's `examples/http-proxy.rs` shape. Its reading, `p3serve.reading`, was taken by the witness's own driver (`p3serve_test.go`'s `serveRows`, run with `-update-p3serve`) against wasmtime 49.0.3's `serve --addr 127.0.0.1:0 --no-logging-prefix --max-instance-reuse-count 1`, over raw TCP and HTTP/1.1, 10 of 10 drives identical. It was committed before the engine code. What it reads:

| row | what the client sends | what wasmtime answers |
|---|---|---|
| hello | `GET /hello?x=1` | 200, the guest's two headers, chunked, its body |
| dump | `GET /dump?a=b&c` with eight headers | the guest reads method, path-with-query and headers; **`host`, `connection`, `keep-alive` and `proxy-authorization` are dropped and `te` kept**; the headers are immutable; options none |
| dump-post, dump-chunked | a fixed-length and a chunked body | `content-length` kept, `transfer-encoding` dropped, `trailer` kept as sent; the body read whole; the request's trailers delivered |
| target, target-absolute | an origin-form and an absolute-form target | **scheme and authority none for origin form**; `HTTP` and the target's host for absolute form |
| big, echo | a 4 MiB response; a 300 KiB request body streamed back | both whole; echo's response head is returned before any of its body is read |
| trailers, trailers-te | a request with trailers; the same with `TE: trailers` | the request's trailers reach the guest both times; **the response's trailers are sent only with `TE: trailers`** (hyper's rule) |
| status, fixed | | 418 with its reason; a guest `content-length` passed through |
| err, err-internal, trap | `Err(HTTP-request-denied)`, `Err(internal-error(some …))`, a panic before the return | **one fixed HTML page, 500, `text/html; charset=UTF-8`**, naming `wasmtime`; the error is logged to wasmtime's stderr, not sent |
| after-trap | | the next request is served by a fresh instance |
| trap-after | a panic after the return and a partial body | **200 and the partial body, ended cleanly**: the truncation is silent |
| late | three writes with a `wait-for` between | the whole body: the task runs on, suspending, after its answer |
| concurrent | four `POST /echo` with their bodies held open | **all four response heads arrive while every body is open**, then each echo |
| forever | an endless body the client stops reading | the guest's next write finds its reader gone |
| stall | a chunked body the client never finishes, then goes away | **the task is not cancelled**: its body ends `err(HTTP-protocol-error)`, and it returns a response nobody reads |
| sleep | a handler waiting on a timer, its client gone once it has entered | **the task is not cancelled**: its wait ends and it returns |
| trailers-err | trailers written `err(internal-error)` | the connection closes before the response ends |
| send | the handler sends a GET to a peer on 127.0.0.1 | the peer's status and body: `serve` grants outgoing HTTP |

**The parallel F12-in probe read the same oracle with a hand-written WAT reactor** ([#1387](https://github.com/scttfrdmn/burroughs/issues/1387)'s slice 0, relayed to this slice), and agrees where both read: a trap after the answer ends the body cleanly, a trap before it is the 205-byte page, error trailers cut the connection (body bytes lost in 1 of its 5 Go runs, the head too in some of p3serve's), a `date` header is added, and a guest `content-length` passes through. It read three more facts this record uses: **wasmtime's concurrency count ends at `task.return`, not at the task's exit** (`--max-instance-concurrent-reuse-count 1` let a second request into an instance whose first task had returned and not exited, 5 of 5; `--max-instance-reuse-count 1` kept it out, 5 of 5; 30 tasks were live at once in one instance by default); `serve` puts `INSTANCE_ID=<n>` in the guest's environment; and a task that exits with a live subtask is clean for the client.

### Why the oracle runs with one request per instance

R2 found a wit-bindgen guest panicking under wasmtime's default reuse (#1372 (b)). p3serve reads it again, as a burst of 32 concurrent requests from a scratch client: with the default reuse, 4 of 32 `/hello` and 15 of 32 `/big` failed, and with `--max-instance-concurrent-reuse-count 1`, 10 and 16 of 32. Every failure is the same panic, `assertion failed: task_state::get().is_null()` in wit-bindgen 0.61.1's `start_task`, which reads `context.get 0` at a new task's entry and requires it empty. **The pin starts every Thread's storage empty** (def:canon_context_get:"result = thread.storage[i]", a Thread constructed with `self.storage = [0,0]`), and Burroughs keeps it per task (`callerStorage`), so the assertion holds on both. wasmtime 49.0.3 is the one that fails it, with a second task in an instance whose first has returned and not exited. With `--max-instance-reuse-count 1` the same bursts fail none, which is why the readings use it.

## Options

**For the instances requests run on:**

1. **A fresh instance per request**, wasmtime's `--max-instance-reuse-count 1`. *For:* it is the oracle's setting, and no request can see another's state. *Costs:* every request pays instantiation, and a Go guest's runtime initialisation on its first entry, which is the cost the F12-in report names (#1387, its risks).
2. **One instance, N tasks in flight, capped**, wasmtime's default (16). *For:* one instantiation. *Costs:* every task executes under one run slot (ADR 0101), so requests on one instance never run in parallel. The fork's Go reactor supports one task in flight first, with a second concurrent task **trapped by name** (#1387, 3(e)'s staging): under this option the second concurrent request to a Go guest traps it, which locks the store (ADR 0105) and fails every later request on it. And a guest that is wrong about its context slot, as wit-bindgen is on wasmtime, would be exercised in a shape the oracle cannot read.
3. **A pool of instances, each serving one task at a time, reused only after that task has exited.** *For:* concurrency across instances is real parallelism, each instance having its own run slot; an instance's initialisation is paid once and then amortised; no task ever enters an instance whose earlier task has returned and not exited, which is the shape both failures above need; and it is option 1's isolation between requests in flight. *Costs:* memory for one instance per concurrent request, and state that persists from one request to a later one on the same instance, as under wasmtime's default reuse.

**For what a client's disconnect does to a task:**

- **(a) Cancel it,** delivering `TASK_CANCELLED`. *Costs:* both rows that read it, stall and sleep, show wasmtime does not; a guest would see a cancellation the oracle never sends.
- **(b) Let it run,** and discard its answer. *For:* it is the reading.

## Choice

**Option 3, and (b).**

### The request

`ServeHTTP` takes an instance from the pool and mints the request into a host table of its own (`newHostTable`, ADR 0136) as an `httpRequest` (ADR 0135's object) with an incoming body:

- **method** is the request's, a standard one by its case and anything else `other`;
- **path-with-query** is the target's path and query (`URL.RequestURI`);
- **scheme and authority are none for an origin-form target**, and the target's own for an absolute-form one, as wasmtime reads them (target, target-absolute);
- **headers** are the request's, lowercased and sorted by name, immutable, with the eight names `fields` forbids dropped (`connection`, `keep-alive`, `proxy-connection`, `transfer-encoding`, `upgrade`, `host`, `http2-settings`, `proxy-authorization`), four of them measured dropped (dump, dump-chunked). Go moves a `trailer` header into `Request.Trailer`, and it is put back from the declared keys, as ADR 0135's send puts back a response's;
- **options** are none (dump).

**The body is ADR 0122's producer over `Request.Body`** (`streamProducer`, its Read the producer's goroutine site, counted in the instance's HTTP registry), handed to the guest by `request.consume-body`, which for an incoming request is this producer and not the guest's pass-through. Its future is the trailers: `ok(some(trailers))` at the body's end, the trailers minted as an immutable own at that delivery, or `ok(none)`; `err(HTTP-protocol-error)` for any Read error, the one code stall reads; and `ok(none)` at the reader's drop. The `res` future `consume-body` takes is read at once and its value dropped, as for a send's response. **Full duplex is enabled** (`http.ResponseController.EnableFullDuplex`), so a body is still readable after the response's head has gone, which echo and concurrent need and Go's HTTP/1 server otherwise refuses.

### The call, and the answer

The request's own crosses into the callee on its entry, and the call is `callReturning` of the instance's `wasi:http/handler@0.3.x#handle` export, on a goroutine of its own: **the one engine goroutine per request this ADR authorises in the census** (`TestEveryEngineGoroutineIsAtASiteADecisionAuthorises`). It runs the task's loop, so its guest code runs under the instance's run slot as any call's does, and it ends when the task exits. Its context is the server's, never the request's: Go cancels a request's context when `ServeHTTP` returns, and (b) means a client's disconnect cancels nothing.

**`onReturn` hands the lifted `result` to `ServeHTTP` and returns**, ADR 0136's contract. `ServeHTTP` then:

- **for `ok(response)`** takes the response from the host table (`takeAs`), writes its status and its headers (Go's canonical spelling, in Go's sorted order), and flushes, so the head leaves at the answer whether or not any body follows. It arms the host's read of the guest's body stream and trailers future under ADR 0101's discipline (`deliverHTTP`), as `send` arms its request body's (ADR 0135's `httpReqBody`, reused whole), and copies the body to the writer, **flushing after every Read**. At the body's end it writes the trailers, **only when the request's `TE` names `trailers`** (trailers, trailers-te);
- **for `err(code)`, a task that ends before answering, or an instance that cannot be made, writes the fixed page**: 500, `text/html; charset=UTF-8`, a `Content-Length`, and wasmtime's HTML with `burroughs` where it names `wasmtime` (err, err-internal, trap). The error itself is not sent, as wasmtime does not send it. wasmtime logs it to its stderr; the handler has no log of its own, and adding one would be public surface;
- **for trailers resolved `err(code)`, aborts the response** (`http.ErrAbortHandler`), so the connection closes before the body ends (trailers-err);
- **for a task that ends after its answer and before its body does, ends the body cleanly** with what was written and no trailers, which is wasmtime's silent truncation (trap-after), a named reading rather than an endorsement;
- **for a client gone before the answer, returns**, and the task runs on; its answer, arriving later, is destroyed at once, inside the `task.return` that gives it, so the body the guest goes on to write is told its reader has gone and the task can exit (stall, sleep). Left in the host table instead, that body's write would be parked for ever;
- **for a client gone after the answer** (a write fails, or the request's context ends while the guest writes nothing), drops the host's reader of the body, so the guest's next write is told DROPPED (forever). The context's end reaches the body's Read through `context.AfterFunc`, whose goroutine is the context package's and runs only the reader's abort.

### The pool

- **An instance serves one task at a time, and returns to the pool only once that task has exited and its response is finished, both cleanly.** "Cleanly" is `callReturning` returning nil, the response's body ending, and the store not locked. A trap, a cancellation, a client gone mid-body or an instance that ended with its body unfinished closes the instance instead. **This counts concurrency to the task's exit, where wasmtime counts to `task.return`** (#1387's probe), which is the divergence that keeps a second task out of an instance whose first has returned and not exited.
- **A request takes the most recently returned idle instance, or a new one.** There is no bound on instances in use: concurrency is the embedder's, through the listener and server they own (Scott's ruling 2). **At most 16 instances are kept idle**, wasmtime's per-instance concurrency default reused as a count of warm instances; one returned with 16 idle is closed. A burst wider than that pays instantiation again for the excess, and is the trade named for having no knob.
- **The Component's own instance, the one `Call` reaches, serves no request.** A request's trap locks only the instance it ran on, which is closed, and the next request runs on another (after-trap). `Call` and requests never share an instance's state or its run slot.
- **Each instance is made from the component's bytes and configuration**, with a host of its own: the same argv, grants and preopens, the same `Stdout` and `Stderr`, each behind one lock the Component's instances share, so instances writing at once never interleave an embedder's writer mid-write, and **an empty stdin**, since two instances reading one reader would split it between them.

### Close

`Component.Close` closes the handler's pool after its own calls, in its own order (ADR 0096's), under its own bound:

1. every task in flight is cancelled (`TASK_CANCELLED`, the context the calls carry);
2. **every body is let go**: a response body being copied has its reader dropped by a delivery, so a guest writing it is told DROPPED even while `ServeHTTP` is held in a Write to a client that reads nothing, and a request body's Read in flight is ended by a read deadline in the past, so a guest reading it is told its end. A task that has answered cannot be cancelled, so this is what ends it;
3. a bounded wait for every task's end;
4. every instance is closed, idle or not, which stops the producers and the HTTP registry (ADR 0122, ADR 0135) and waits out their goroutines.

**A Close within its bound leaves no serving goroutine running.** One that reaches the bound reports `ErrCloseIncomplete`, as for a call, and does not wait again: an instance's Close ends a task executing guest code, and does not wake one parked on something no host work will deliver, which ends at its park's bound. A request after Close is answered with the page.

### `wasi:cli/run`, and the CLI

**A component with no `wasi:http/handler@0.3.x#handle` export is not refused by `Handler()`, which has no error to return: every request is answered with the page.** The documentation says so, and that `Exports` answers the question first. `burroughs serve` asks it, and **refuses such a component before it listens**, naming the export it lacks and, for a component exporting `wasi:cli/run`, that serve never calls it, as a usage error. wasmtime exits 1 there (*"no exported instance named `wasi:http/incoming-handler@0.2.12`"*).

`burroughs serve [--addr ADDR] [--dir HOST[:/GUEST]]... [--scratch HOST[:/GUEST]]... [--inherit-network] [--allow-ip-name-lookup] file.wasm`:

- **`--addr`** defaults to `0.0.0.0:8080`, wasmtime's, and a port of 0 binds an ephemeral one;
- **it prints `Serving HTTP on http://ADDR/` on stderr** once it listens, wasmtime's line with the bound address, which is how a caller learns an ephemeral port;
- **it grants outgoing HTTP** (`AllowHTTP`), Scott's ruling, so it takes no `--allow-http`;
- **`--dir`, `--scratch`, `--inherit-network` and `--allow-ip-name-lookup` are `run`'s**, parsed by `run`'s own flag types, so their syntax, refusals and meaning are identical;
- it serves until SIGINT or SIGTERM, then shuts the server down and closes the component. **Its stop watcher is the one goroutine the command starts**, authorised in the census by this ADR: it waits for a stop or for `Serve` to end, shuts the `http.Server` down under a bound on a stop, runs no guest code and touches the engine only through the public `Component`, and is joined before `serve` returns.

## Consequences

- **A component can serve HTTP**, through `Component.Handler()` on any listener an embedder owns and through `burroughs serve`. This is the public API the slice adds.
- **Requests run in parallel, one per instance**, so a slow handler does not hold another back (concurrent).
- **Named divergences from wasmtime 49.0.3, each a reading:**
  - concurrency counted to the task's exit, not to its return;
  - the page names `burroughs`;
  - no log line for an error-code;
  - the response's header names are Go's canonical spelling, in Go's sorted order rather than the guest's (a case-insensitive name, and an order the comparison does not ask about);
  - the guest's environment is empty, where `serve` sets `INSTANCE_ID`; and its stdin is empty;
  - no `date` header is suppressed or added beyond Go's own, which adds one as hyper does.
- **Named limits:**
  - **No host-to-host copy** (E2-in-c, #1386): a response whose body is a stream the host produces, an incoming request's body forwarded as is, is answered with the page, since a handler has no error to name it in.
  - **Instances are made by parsing the component's bytes again**; a loaded component shared by its instances would parse once.
  - **The re-arm of the body's read waits for the run slot, and only Close abandons that wait** (#1398's subject, `acquireRun`), so a guest computing in a callback holds its response's next flush, and a client gone meanwhile is noticed only after it.
  - **A task's answer to a client already gone still runs to completion** and holds its instance until it exits, as on wasmtime.
  - **HTTP/2 is Go's**: full duplex is native there, and nothing here reads it against the oracle.
  - **A task parked on nothing the host delivers outlives a Close that reaches its bound**, as above.

### A repair the readings compelled

**A lower's realloc ran in its caller's task's context under an async lift**, and every p3serve request trapped out of bounds on it before anything else here could be read. The pin invokes a realloc as a call of its own, on a new Thread whose storage starts [0, 0] (def:LiftLowerContext.reallocate:"self.inst.store.invoke(realloc, on_start, on_resolve)"). ADR 0119 (grave #1260) made Burroughs zero and restore a stack's context slots around a realloc, which is a sync lift's context; an async lift's context is its task's, found through the Thread index the realloc's stack still carried, and that was not reached. A `wasm32-wasip3` std guest keeps its shadow stack pointer in context slot 0, and its realloc is wrapped in task hooks that move it to a scratch stack and set it to 0 on the way out, which is right for a fresh context. Run in the handler's, it left the handler's stack pointer at 0, so the handler's next frame stored below address 0. p3http never reached it: its `run` is a sync lift. **The repair:** a caller inside a realloc (`MayLeave` false) has the stack's slots, which ADR 0119 already zeroes and restores, whatever lift it is nested in (`callerStorage`). `realloc-context-async.wasm`, realloc-context's async twin, is its witness: wasmtime 49.0.3 reads 12345 << 16 | 0, 10 of 10, and Burroughs at 13f4960 read 0xdead << 16 | 12345, gate:cm-threads off and on. It reverses no stamped ADR: it completes ADR 0119's rule at a lift it did not reach.

## The build, as measured

On darwin/arm64, in the worktree of #1385's branch. The reading was committed before the engine code; the send row, which reads `serve`'s grant of outgoing HTTP, was added with it, taken from wasmtime 49.0.3 by the same driver, 10 of 10, before Burroughs' arm ran against it.

- **`TestP3ServeMatchesWasmtime`** (`p3serve_test.go`): all 24 rows match wasmtime 49.0.3 serve's reading, with `gate:cm-threads` off and on, plain and under `-race`.
- **`TestReallocRunsWithItsOwnContextUnderAnAsyncLift`**: 809041920, both gate states.
- **The pool, by places** (`TestAServedInstanceIsReusedOnlyAfterItsTaskExits`): two sequential requests leave one instance, idle; a request served while a held echo's task has answered and not exited runs on a second; 17 held requests run on 17 instances and leave 16 idle when they finish.
- **An abandoned request** (`TestServingAnAbandonedRequestLetsItsTaskExit`): sleep's handler answers a client already gone, and its task exits; its instance is closed.
- **Close** (`TestCloseEndsEveryServedTaskAndItsGoroutine`): an answered echo reading a held body, an unanswered stall reading one, and a forever whose client reads nothing are ended within the bound, both gate states, and no goroutine `ServeHTTP` or a request body's Read started is left.
- **The public doors:** `TestComponentHandlerServesARequest`, `TestHandlerInstancesTakeTheComponentsConfiguration` (the send row answers the peer's status with `AllowHTTP` and `HTTP-request-denied` without), `TestComponentCloseClosesItsHandler` and `TestHandlerOfAComponentWithNoHandleExportAnswersThePage` in the root package; `TestServeAComponent` (the address line, hello, the send row granted), `TestServeRefusesAComponentWithNoHandler` and `TestServeTakesRunsGrantFlags` in `cmd/burroughs`.
- **What the neuters showed** (each run and reverted; the full list is at the sites as `NEUTER, run:` comments):
  - **Counting reuse to the answer fails this guest on Burroughs too.** Released at its answer, wasmtime's count, the held echo's instance takes the next request, which is answered with the page, its task ending before an answer, though Burroughs keeps context slots per task. So the panic R2 read on wasmtime is not the only state a second task beside a returned one disturbs.
  - **The realloc's own context is the whole of the trap.** Without it every request traps out of bounds.
  - **Each wasmtime rule the rows read is load-bearing on its own row**: the dropped forbidden headers, the incoming body, full duplex, `TE: trailers`, the abort at error trailers, the clean end at a trap after the answer, and the body read's error code.
  - **Close's cancel is vacuous on these rows**: the one unanswered task, stall, is ended by its body's end first. Dropping the body aborts instead holds Close to its bound.

## Rollback

Remove `Component.Handler()` and `burroughs serve`: nothing else calls the pool, and `callReturning`, the resources and the send are as ADR 0136 and ADR 0135 left them. The incoming arm of `request.consume-body` is reachable only from a request the pool mints.
