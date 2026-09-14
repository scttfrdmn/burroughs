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
