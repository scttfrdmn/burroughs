;; #1314's control for canonopt-async-lower-sync-type.wat: the same async lower over an async-typed import.
;; wasm-tools 1.258.0 `validate --features all` accepts it and wasmtime 49.0.3 `compile` compiles it (exit 0); `run`
;; gets past parse and stops at instantiation, "function implementation is missing", since nothing provides `f`.
;; Assembled with `wasm-tools parse`.
(component
  (import "f" (func $f async (param "x" u32)))
  (core module $mem (memory (export "mem") 1))
  (core instance $mi (instantiate $mem))
  (core func $lf (canon lower (func $f) async (memory $mi "mem")))
  (core module $m
    (import "" "f" (func (param i32) (result i32)))
    (func (export "run")))
  (core instance $i (instantiate $m (with "" (instance (export "f" (func $lf))))))
  (func (export "run") (canon lift (core func $i "run"))))
