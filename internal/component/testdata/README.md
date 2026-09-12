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
