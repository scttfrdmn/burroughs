# component loader test fixtures

`p3hello.wasm` is the Step-0 Rust component from the p3 track (ADR 0084, #694): a `wasi:cli/run`
world compiled to a component. `p3hello.wit` is the oracle's reading of it — verbatim
`wasm-tools component wit p3hello.wasm` — committed rather than derived at test time, on the
project's oracle precedent (external tools run once and their reading is committed; CI does not
install them — see the Makefile `spec-images` target and its note on wabt).

Provenance (the #694 pinned toolchain):

- `rustc 1.91.1` (rustup), `cargo-component 0.21.1`
- `wasm-tools 1.258.0` produced `p3hello.wit`

To regenerate the golden after the fixture changes (the drift check the wabt model leaves to
regeneration rather than a CI dependency):

```sh
wasm-tools component wit p3hello.wasm > p3hello.wit
```

`wasm-tools print p3hello.wasm` reports 4 `(core module ...)` definitions — the count
`loader_test.go` asserts.
