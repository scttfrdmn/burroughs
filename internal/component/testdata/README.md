# component loader test fixtures

`p3async-hello.wasm` is the Phase-3 (`gate:async`, ADR 0086, #734) async guest: a Rust `wasm32-wasip3`
hello-world (`fn main(){ println!("Hello, world!"); }`, dependency-free) whose exported
`wasi:cli/run@0.3.0` is an async functype **sync-lifted** (`opts.async_` false — the host delivers the
return) while its 0.3 stdio imports are **async-lowered**, so it binds the waitable-set / stream / future
event loop. `p3async-hello.wit` is `wasm-tools component wit`'s reading, committed like the others. The
`gate:async` shell refuses it at bind by name (it decodes: 35 canon defs, matching `wasm-tools print`);
there is no committed `.stdout` because `gate:async` on has no mechanism yet (slice 1's), so nothing runs.
Provenance: `rustc 1.100.0-nightly (0fc141305 2026-09-11)` + `rustup target add wasm32-wasip3` (a
precompiled std + wasi-libc; no `-Zbuild-std`); `wasm-tools 1.258.0`; `wasmtime 48.0.1 (7bac2c27)` runs it
async-on-by-default to `Hello, world!\n`. sha1 `06f965f7d392e762b0324d0e0f86aa8762d0ae61`.

`context-synth.wasm` is a hand-authored fixture for the increment-4 context.get/set witness: canon
`context.get`/`set` core funcs over static slots 0 and 1, and a core module exporting `set-then-get` (set
slot 0 to its arg, then get slot 0 -> the arg) and `get1-unset` (get slot 1 -> 0). Exercises the built-in +
decode + binding + the per-agent stack accessor end-to-end via a real Invoke, matched to the `context_ops`
pin. The per-caller isolation (two agents, distinct slots; a fresh stack starts zeroed) is proved directly
in the interp test `TestContextStorageIsPerCallerStack`, since no single-agent guest run can witness it.
Authored via `wasm-tools parse` (1.258.0).

`stream-write-synth.wasm` is a hand-authored fixture for the increment-3 stream write-side binding witness:
an instance import whose `get-stream` async func returns a `stream<u8>` (declared **inline** to avoid the
#753 outer-alias path), an async `canon lower` of it, and a `stream.write` canon built-in over a
component-level `(type $st (stream u8))`. The guest async-lowers get-stream (the wrapper mints a writable end
and writes its handle), then `stream.write`s that handle and returns the `BLOCKED` status. The consumer +
delivery are unit-tested (`TestStreamWriteDeliversTheOracleOutcomes`); this witnesses bind + the `BLOCKED`
return. Authored via `wasm-tools parse` (1.258.0).

`future-read-outer-alias.wasm` is a hand-authored fixture for the **#753** regression witness: an instance
import whose `get-future` async func returns a `future<u32>` declared via an **outer type alias** (a
component-level `(type $fut (future u32))` aliased into the instance type), and an async `canon lower` of it.
Before the fix this stack-overflowed `resolveVal` at `Load` (the self-referential `VRef{0}` placeholder); now
it Loads and the lower refuses by name at instantiate (the unresolved outer alias). Authored via
`wasm-tools parse` (1.258.0). Its inline-typed sibling `future-read-synth.wasm` is the working path.

`p3async-hello.stdout` is the **committed wasmtime reading** — the second oracle for gate:async increment 4
(the end-to-end guest), the way `p3hello`'s output was for slice 2. It is what the real guest
`p3async-hello.wasm` writes to stdout when run on **wasmtime 48.0.1** (async-on-by-default, no flag):
`Hello, world!\n`, exit 0. Committed with its version here so the increment-4 end-to-end test asserts
against a *recorded reading*, not a live wasmtime run. Regenerate only by re-running the pinned wasmtime on
the committed `p3async-hello.wasm` and updating this note's version.

`p3async-cancel.wasm` is the **second async guest** — the Phase-3 async-lift + cancellation producer
(`track:litmus`/#771 discharge slice). Unlike `p3async-hello` (which sync-lifts an async-typed `wasi:cli/run`
export), this guest **async-lifts** its export, verified from the bytes:
`(func $run (canon lift (core func $hook0) async (callback $hook1)))` — the **stackless (callback)** variant,
which is what the Rust p3 toolchain emits (the stackless-vs-stackful choice ADR 0086 deferred, settled
guest-driven; see the slice issue). On its normal path `run` starts a `future<u32>` write with no reader and
**cancels it**, emitting `(canon future.cancel-write)` (the bytes also carry `future.new`, `future.cancel-read`,
`task.return`, `task.cancel`, `subtask.cancel`, `subtask.drop`, `stream.cancel-read` — the async-lift +
cancellation surface). The `mk` export exists only to introduce the `future<u32>` type so `run` can create
and cancel one. `p3async-cancel.reading` is its committed wasmtime reading: `wasmtime run --invoke 'run()'`
returns **1** (the `Cancelled` branch fired — a running producer for CANCELLED), exit 0, on **wasmtime
48.0.2** (the pin bumped 48.0.1→48.0.2 on the chair's ruling; a patch within the minor, each reading names
its binary). Provenance: `rustc 1.100.0-nightly (0fc141305 2026-09-11)`, `rustup target add wasm32-wasip3`
(precompiled std; no `-Zbuild-std`), `wit-bindgen 0.61.1` (`async: true`, per-function async on `run`/`mk`),
`wasm-tools 1.258.0`. **Build-host note:** on a fresh box the nightly's `rust-lld` fails to link with
`dyld: Library not loaded: @rpath/libLLVM.dylib`; the fix is `rustup component add llvm-tools --toolchain
nightly`, which places `libLLVM.dylib` at the `@rpath` location `rust-lld` expects — the next person building
a p3 guest here will hit this.

`async-lift-exit-synth.wasm` is the step-1 oracle for the async (callback) lift's **execution loop** (2nd
async guest, #785): the minimal EXIT-only lift — a callee that calls `task.return(42)` then returns EXIT
(packed code 0), lifted `(canon lift ... async (callback $cb))`, with **no park** and no waitable-set surface.
Its committed reading is **`async-lift-exit-synth.reading`** — `wasmtime run --invoke 'run()'` → **42** (wasmtime 48.0.2), committed as a file rather than stated here so a wasmtime bump surfaces as a changed artifact (#792 item 3). **Built as WAT, not Rust,
on purpose:** the Rust EXIT path is confirmed working (`run()→42`) but a Rust guest drags wit-bindgen's whole
async-runtime import surface (waitable-set new/wait/poll/drop) whether the test needs it or not — noise a
later reader would mistake for scope — so the minimal oracle isolates the loop skeleton rather than the
toolchain's defaults. Authored via `wasm-tools parse` (1.258.0; validates `--features all`):

```wat
(component
  (core module $memmod (memory (export "m") 1))
  (core instance $memi (instantiate $memmod))
  (alias core export $memi "m" (core memory $cm))
  (type $rt (func async (result u32)))
  (core func $taskret (canon task.return (result u32)))
  (core module $runmod
    (import "" "taskret" (func $taskret (param i32)))
    (func (export "callee") (result i32) (call $taskret (i32.const 42)) (i32.const 0))
    (func (export "cb") (param i32 i32 i32) (result i32) (i32.const 0)))
  (core instance $runi (instantiate $runmod (with "" (instance (export "taskret" (func $taskret))))))
  (alias core export $runi "callee" (core func $calleef))
  (alias core export $runi "cb" (core func $cbf))
  (func (export "run") (type $rt) (canon lift (core func $calleef) async (callback $cbf))))
```

`async-future-cancel-synth.wasm` is the **running CANCELLED producer** for the 2nd async guest (#785, step 2
of the async-lift execution): a WAT guest that `future.new`s an end pair, `future.write`s the writable end
(parks, BLOCKED — no reader), `future.cancel-write`s it (→ CANCELLED), `task.return`s the CANCELLED result,
and returns EXIT — lifted `(canon lift ... async (callback ...))`. Its committed reading is **`async-future-cancel-synth.reading`** — `wasmtime run --invoke 'run()'` → **2** (CopyResult.CANCELLED, wasmtime 48.0.2), committed as a file rather than stated here so a wasmtime bump surfaces as a changed artifact (#792 item 3). **Built as WAT, not the Rust
`p3async-cancel`**, for the same reason as the EXIT-only oracle: `run()`'s cancel path executes only
`future.new` + `future.write` + `future.cancel-write`, but the Rust guest's wit-bindgen surface *imports* the
whole future family (cancel-read, drop-writable, task.cancel) it never calls — building four unexercised
built-ins to instantiate one importer is the drag declined one level up. The `async` canonopt is on
`future.write` (a sync future.write needs a wasmtime feature flag); `future.cancel-write` is sync (matching
`p3async-cancel`'s own emission). Authored via `wasm-tools parse` (1.258.0; validates `--features all`):

```wat
(component
  (core module $memmod (memory (export "m") 1))
  (core instance $memi (instantiate $memmod))
  (alias core export $memi "m" (core memory $cm))
  (type $rt (func async (result u32)))
  (type $fut (future u8))
  (core func $taskret (canon task.return (result u32)))
  (core func $fnew (canon future.new $fut))
  (core func $fwrite (canon future.write $fut (memory $cm) async))
  (core func $fcancel (canon future.cancel-write $fut))
  (core module $runmod
    (import "" "taskret" (func $taskret (param i32)))
    (import "" "fnew" (func $fnew (result i64)))
    (import "" "fwrite" (func $fwrite (param i32 i32) (result i32)))
    (import "" "fcancel" (func $fcancel (param i32) (result i32)))
    (func (export "callee") (result i32)
      (local $packed i64) (local $wi i32) (local $cr i32)
      (local.set $packed (call $fnew))
      (local.set $wi (i32.wrap_i64 (i64.shr_u (local.get $packed) (i64.const 32))))
      (drop (call $fwrite (local.get $wi) (i32.const 0)))
      (local.set $cr (call $fcancel (local.get $wi)))
      (call $taskret (i32.and (local.get $cr) (i32.const 0xf)))
      (i32.const 0))
    (func (export "cb") (param i32 i32 i32) (result i32) (i32.const 0)))
  (core instance $runi (instantiate $runmod (with "" (instance
    (export "taskret" (func $taskret)) (export "fnew" (func $fnew))
    (export "fwrite" (func $fwrite)) (export "fcancel" (func $fcancel))))))
  (alias core export $runi "callee" (core func $calleef))
  (alias core export $runi "cb" (core func $cbf))
  (func (export "run") (type $rt) (canon lift (core func $calleef) async (callback $cbf))))
```

`atomics-component-synth.wasm` is the witness for **ADR 0088**'s caller-supplied capability set: a
180-byte component whose core module uses the **0xFE atomics region** (`i32.atomic.rmw.add`), so it
**cannot decode under `DefaultFeatures()`** — which is the whole point. It is refused by name with no
capability supplied, and loads (its lifted `hello` returning 42) with the threads capability supplied.

**Why this fixture and not the Go component that forced the decision:** that artifact is **1.8MB** — four
times this directory's entire contents — because it carries the Go runtime. Committing it would pay a
large repository cost for a claim this 180-byte fixture makes exactly. The Go component's own two-engine
result is recorded on #800: it returns **42 on Wasmtime 48.0.2 and 42 on Burroughs**, matching a reading
committed before the fork's emission code existed. **Stated limitation:** that end-to-end result is
therefore *verified and recorded* but **not re-run in Burroughs' CI**; this fixture is what CI enforces.
Authored via `wasm-tools parse` (1.258.0; validates `--features all`):

```wat
(component
  (core module $m
    (memory (export "memory") 1)
    (func (export "bump") (result i32)
      (i32.atomic.rmw.add (i32.const 0) (i32.const 1)))
    (func (export "hello") (result i32) (i32.const 42)))
  (core instance $i (instantiate $m))
  (alias core export $i "hello" (core func $h))
  (func (export "hello") (result u32) (canon lift (core func $h))))
```

`async-waitset-drop-synth.wasm` is the **firing witness** for the `waitable-set.drop` (0x22) refusal (#792,
Scott's ruling): a guest that creates a waitable set and **drops it** — the call no guest on `main` makes
(`p3async-hello` and `p3async-cancel` both *bind* 0x22 and never call it, which is why the refusal is at the
call and not at bind). It exists because a refusal needs a witness that makes it fire (#732): this component
**instantiates** (bind succeeds, so a guest that merely imports 0x22 still runs) and **refuses by name at the
call** with `ErrAsyncNotImplemented`. The close audit found 0x22 built and permitted but certified by nothing
— no guest execution, no test execution, no fixture pin — and knowingly incomplete (the model traps when a
set is dropped with live members or waiters; the deleted impl had neither trap). Authored via `wasm-tools
parse` (1.258.0; validates `--features all`):

```wat
(component
  (core module $memmod (memory (export "m") 1))
  (core instance $memi (instantiate $memmod))
  (alias core export $memi "m" (core memory $cm))
  (core func $wsnew (canon waitable-set.new))
  (core func $wsdrop (canon waitable-set.drop))
  (core module $runmod
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "wsdrop" (func $wsdrop (param i32)))
    (func (export "run") (result i32)
      (local $si i32)
      (local.set $si (call $wsnew))
      (call $wsdrop (local.get $si))
      (local.get $si)))
  (core instance $runi (instantiate $runmod (with "" (instance
    (export "wsnew" (func $wsnew)) (export "wsdrop" (func $wsdrop))))))
  (alias core export $runi "run" (core func $runf))
  (func (export "run") (result u32) (canon lift (core func $runf))))
```

`future-read-synth.wasm` is a hand-authored **synthesized** component for the `gate:async` increment-3
`future.read` binding witness: an instance import whose `get-future` is an async func returning `future<u32>`
(declared **inline** in the instance type — an outer-aliased future type would hit the placeholder-VRef
recursion in `resolveVal`, #753), an async `canon lower` of it, `future.read`/`future.drop-readable` canon
built-ins over a component-level `(type $fut (future u32))`, and a core module whose `run` async-lowers
get-future (the wrapper mints a readable end and writes its handle), then `future.read`s that handle and
returns the `BLOCKED` status. The completion + delivery is unit-tested (`TestFutureReadDeliversTheOracleOutcomes`);
this witnesses the bind + the `BLOCKED` return. Authored from this WAT via `wasm-tools parse` (wasm-tools
1.258.0; validates `--features all`):

```wat
(component
  (core module $memmod (memory (export "m") 1))
  (core instance $memi (instantiate $memmod))
  (alias core export $memi "m" (core memory $cm))
  (type $fut (future u32))
  (import "test:async/src" (instance $src
    (export "get-future" (func async (result (future u32))))))
  (alias export $src "get-future" (func $getf))
  (core func $lowered (canon lower (func $getf) async (memory $cm)))
  (core func $fread (canon future.read $fut (memory $cm)))
  (core func $fdrop (canon future.drop-readable $fut))
  (core module $runmod
    (import "" "mem" (memory 1))
    (import "" "getf" (func $getf (param i32) (result i32)))
    (import "" "fread" (func $fread (param i32 i32) (result i32)))
    (import "" "fdrop" (func $fdrop (param i32)))
    (func (export "run") (result i32)
      (local $packed i32) (local $fh i32) (local $rr i32)
      (local.set $packed (call $getf (i32.const 0)))
      (local.set $fh (i32.load (i32.const 0)))
      (local.set $rr (call $fread (local.get $fh) (i32.const 8)))
      (call $fdrop (local.get $fh))
      (local.get $rr)))
  (core instance $runi (instantiate $runmod
    (with "" (instance
      (export "mem" (memory $cm)) (export "getf" (func $lowered))
      (export "fread" (func $fread)) (export "fdrop" (func $fdrop))))))
  (alias core export $runi "run" (core func $runf))
  (func (export "run") (result u32) (canon lift (core func $runf))))
```

`async-waitset-synth.wasm` is a hand-authored **synthesized** component for the `gate:async` 2a-i-B-2
waitable-set loop witnesses: an instance import whose `op` is an async func (declared **inline** in the
instance type, so its signature resolves — an outer-aliased type would become a placeholder), an async
`canon lower` of it, the `waitable-set.new`/`waitable.join`/`waitable-set.wait` canon built-ins, and a core
module whose `run` does the full blocking-arm round trip (lower -> blocked packed return -> new -> join ->
wait -> read the lowered result) plus a `sibling` export for the H-4 sibling-progress test. `run`/`sibling`
return their i32 so the tests observe them via the core instance's Invoke. Authored from this WAT via
`wasm-tools parse` (wasm-tools 1.258.0; validates `--features all`):

```wat
(component
  (core module $memmod (memory (export "m") 1))
  (core instance $memi (instantiate $memmod))
  (alias core export $memi "m" (core memory $cm))
  (import "test:async/ops" (instance $ops
    (export "op" (func async (param "x" u32) (result u32)))))
  (alias export $ops "op" (func $impf))
  (core func $lowered (canon lower (func $impf) async (memory $cm)))
  (core func $wsnew (canon waitable-set.new))
  (core func $wsjoin (canon waitable.join))
  (core func $wswait (canon waitable-set.wait (memory $cm)))
  (core module $runmod
    (import "" "mem" (memory 1))
    (import "" "lower" (func $lower (param i32 i32) (result i32)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "wsjoin" (func $wsjoin (param i32 i32)))
    (import "" "wswait" (func $wswait (param i32 i32) (result i32)))
    (func (export "run") (result i32)
      (local $packed i32) (local $subtaski i32) (local $si i32)
      (local.set $packed (call $lower (i32.const 7) (i32.const 0)))
      (if (i32.eq (local.get $packed) (i32.const 2))
        (then (return (i32.load (i32.const 0)))))
      (local.set $subtaski (i32.shr_u (local.get $packed) (i32.const 4)))
      (local.set $si (call $wsnew))
      (call $wsjoin (local.get $subtaski) (local.get $si))
      (drop (call $wswait (local.get $si) (i32.const 8)))
      (i32.load (i32.const 0)))
    (func (export "sibling") (result i32) (i32.const 42)))
  (core instance $runi (instantiate $runmod
    (with "" (instance
      (export "mem" (memory $cm)) (export "lower" (func $lowered))
      (export "wsnew" (func $wsnew)) (export "wsjoin" (func $wsjoin))
      (export "wswait" (func $wswait))))))
  (alias core export $runi "run" (core func $runf))
  (alias core export $runi "sibling" (core func $sibf))
  (func (export "run") (result u32) (canon lift (core func $runf)))
  (func (export "sibling") (result u32) (canon lift (core func $sibf))))
```

`async-wake-bmm1-synth.wasm` is a hand-authored **synthesized** component for the §4 **B-MM-1** litmus
witness (`track:litmus`, #10 — `TestBMM1AsyncWakeIsAnAcquireEdgeOverTheAddressSpace`): the same blocking-arm
round trip as `async-waitset-synth`, but `run`, after `waitable-set.wait` returns (the async wake), reads
**four words spread low/mid/high across the page** (0x100, 0x4000, 0x8000, 0xF000) — written host-side while
the agent is parked — and returns a match mask against their sentinels (0x11111111 / 0x22222222 / 0x33333333
/ 0x44444444), `0xF` iff all four were seen across the wake. It witnesses that the async-wake crossing is an
acquire edge over the *whole* address space (sampled), not one word. Authored from this WAT via `wasm-tools
parse` (wasm-tools 1.258.0; validates `--features all`):

```wat
(component
  (core module $memmod (memory (export "m") 1))
  (core instance $memi (instantiate $memmod))
  (alias core export $memi "m" (core memory $cm))
  (import "test:async/ops" (instance $ops
    (export "op" (func async (param "x" u32) (result u32)))))
  (alias export $ops "op" (func $impf))
  (core func $lowered (canon lower (func $impf) async (memory $cm)))
  (core func $wsnew (canon waitable-set.new))
  (core func $wsjoin (canon waitable.join))
  (core func $wswait (canon waitable-set.wait (memory $cm)))
  (core module $runmod
    (import "" "mem" (memory 1))
    (import "" "lower" (func $lower (param i32 i32) (result i32)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "wsjoin" (func $wsjoin (param i32 i32)))
    (import "" "wswait" (func $wswait (param i32 i32) (result i32)))
    (func (export "run") (result i32)
      (local $packed i32) (local $subtaski i32) (local $si i32) (local $mask i32)
      (local.set $packed (call $lower (i32.const 7) (i32.const 0)))
      (if (i32.ne (local.get $packed) (i32.const 2))
        (then
          (local.set $subtaski (i32.shr_u (local.get $packed) (i32.const 4)))
          (local.set $si (call $wsnew))
          (call $wsjoin (local.get $subtaski) (local.get $si))
          (drop (call $wswait (local.get $si) (i32.const 8)))))
      (if (i32.eq (i32.load (i32.const 0x100))  (i32.const 0x11111111)) (then (local.set $mask (i32.or (local.get $mask) (i32.const 1)))))
      (if (i32.eq (i32.load (i32.const 0x4000)) (i32.const 0x22222222)) (then (local.set $mask (i32.or (local.get $mask) (i32.const 2)))))
      (if (i32.eq (i32.load (i32.const 0x8000)) (i32.const 0x33333333)) (then (local.set $mask (i32.or (local.get $mask) (i32.const 4)))))
      (if (i32.eq (i32.load (i32.const 0xF000)) (i32.const 0x44444444)) (then (local.set $mask (i32.or (local.get $mask) (i32.const 8)))))
      (local.get $mask)))
  (core instance $runi (instantiate $runmod
    (with "" (instance
      (export "mem" (memory $cm)) (export "lower" (func $lowered))
      (export "wsnew" (func $wsnew)) (export "wsjoin" (func $wsjoin))
      (export "wswait" (func $wswait))))))
  (alias core export $runi "run" (core func $runf))
  (func (export "run") (result u32) (canon lift (core func $runf))))
```

`async-lower-synth.wasm` is a hand-authored **synthesized** component for the `gate:async` 2a-i-A
binding-branch witness: an instance import whose `op` is an async func, a `canon lower` of it carrying the
`async` canonopt over a bound memory, and a core module that calls the lowered core func — the smallest
async-**lower**-only component that, gate:async on, the narrowing permits and the walk binds to the
async-lower adapter (`asyncLowerFunc`). A test Host fills the async impl; `async_lower_test.go` drives it.
Authored from this WAT via `wasm-tools parse` (wasm-tools 1.258.0; validates `--features all`):

```wat
(component
  (core module $memmod (memory (export "m") 1))
  (core instance $memi (instantiate $memmod))
  (alias core export $memi "m" (core memory $cm))
  (type $ft (func async (param "x" u32) (result u32)))
  (import "test:async/ops" (instance $ops (export "op" (func (type $ft)))))
  (alias export $ops "op" (func $impf))
  (core func $lowered (canon lower (func $impf) async (memory $cm)))
  (core module $runmod
    (import "" "lower" (func $l (param i32 i32) (result i32)))
    (func (export "run") (drop (call $l (i32.const 7) (i32.const 32)))))
  (core instance $runi (instantiate $runmod (with "" (instance (export "lower" (func $lowered))))))
  (alias core export $runi "run" (core func $runf))
  (func (export "run") (canon lift (core func $runf))))
```

`async-lift-synth.wasm` is a hand-authored **synthesized** component for the `gate:async` lift-arm witness:
a core module exporting a trivial `func`, a core instance, a core-func alias, then a `canon lift` carrying
the `async` canonopt over an async functype (`(func async)`). No guest lifts async yet, so this is the
smallest full, *Loadable* component that reaches the bind refusal on the lift arm (a bare async lift is
refused at Load by the forward-reference check — the core func must exist first). Authored from this WAT via
`wasm-tools parse` (wasm-tools 1.258.0; validates `--features all`), committed like the other fixtures:

```wat
(component
  (core module $m (func (export "f")))
  (core instance $i (instantiate $m))
  (alias core export $i "f" (core func $f))
  (type $t (func async))
  (func (export "run") (type $t) (canon lift (core func $f) async)))
```

`p3hello.wasm` is the Step-0 Rust component from the p3 track (ADR 0084, #694): a `wasi:cli/run`
world compiled to a component. `p3hello.wit` is the oracle's reading of it — verbatim
`wasm-tools component wit p3hello.wasm` — committed rather than derived at test time, on the
project's oracle precedent (external tools run once and their reading is committed; CI does not
install them — see the Makefile `spec-images` target and its note on wabt).

`p3hello.stdout` is the **execution oracle**: the exact stdout `wasmtime run p3hello.wasm` produces,
committed rather than run at test time (CI installs no wasmtime, the same precedent as `p3hello.wit`).
`hello_test.go` diffs Burroughs' own output against it — `Hello, world!\n`, sha1
`09fac8dbfd27bd9b4d23a00eb648aa751789536d`. Regenerate with `wasmtime run p3hello.wasm > p3hello.stdout`.

Provenance (the #694 pinned toolchain):

- `rustc 1.91.1` (rustup), `cargo-component 0.21.1`
- `wasm-tools 1.258.0` produced `p3hello.wit`
- `wasmtime 48.0.1 (7bac2c277 2026-08-24)` produced `p3hello.stdout`

To regenerate the golden after the fixture changes (the drift check the wabt model leaves to
regeneration rather than a CI dependency):

```sh
wasm-tools component wit p3hello.wasm > p3hello.wit
```

`wasm-tools print p3hello.wasm` reports 4 `(core module ...)` definitions — the count
`loader_test.go` asserts.

`p3echo-writefail.txt` is wasmtime 48.0.1's reading of p3echo with stdout to a **closed pipe** (the write
fails): the stream `err` arm fires and Rust std aborts (exit 134, "failed printing to stdout"). It is the
**behavioral** second oracle for the `result<_, stream-error>` err arm (#724) — the ABI lowering is
byte-verified by the canon `definitions.py` fixtures; this records that a real failing write reaches the
err arm and aborts, which Burroughs matches behaviorally (its own error text). Box: darwin, closed pipe;
`/dev/full` on Linux gives the same via ENOSPC (deterministic). Regenerate:
`printf 'hello\n' | wasmtime run p3echo.wasm | true` (capturing stderr and the exit).
