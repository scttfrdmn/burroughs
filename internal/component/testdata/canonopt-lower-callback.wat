;; #1314's neighbour: a canon lower naming a `callback`. CanonicalABI.md's canonopt validation: `callback` "is only
;; allowed with canon lift". wasm-tools 1.258.0 `validate --features all` and wasmtime 49.0.3 `run` both refuse it at
;; parse: "canonical option `callback` cannot be specified for lowerings".
;; Assembled with `wasm-tools parse`, which does not validate.
(component
  (import "f" (func $f async (param "x" u32)))
  (core module $mem
    (memory (export "mem") 1)
    (func (export "cb") (param i32 i32 i32) (result i32) (i32.const 0)))
  (core instance $mi (instantiate $mem))
  (core func $lf (canon lower (func $f) async (memory $mi "mem") (callback (func $mi "cb"))))
  (core module $m
    (import "" "f" (func (param i32) (result i32)))
    (func (export "run")))
  (core instance $i (instantiate $m (with "" (instance (export "f" (func $lf))))))
  (func (export "run") (canon lift (core func $i "run"))))
