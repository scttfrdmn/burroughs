;; #1314's neighbour: a canon lower with the `async` option of an import, `f`, whose function type is not async. The
;; same canonopt rule as the lift's ("only allowed when used with an `async` function type in canon lift or canon
;; lower"). wasm-tools 1.258.0 `validate --features all` and wasmtime 49.0.3 `run` both refuse it at parse: "the
;; `async` canonical option requires an async function type". The control is canonopt-async-lower-async-type.wat.
;; Assembled with `wasm-tools parse`, which does not validate.
(component
  (import "f" (func $f (param "x" u32)))
  (core module $mem (memory (export "mem") 1))
  (core instance $mi (instantiate $mem))
  (core func $lf (canon lower (func $f) async (memory $mi "mem")))
  (core module $m
    (import "" "f" (func (param i32) (result i32)))
    (func (export "run")))
  (core instance $i (instantiate $m (with "" (instance (export "f" (func $lf))))))
  (func (export "run") (canon lift (core func $i "run"))))
