;; #1314's neighbour, the guest-to-guest arm: a canon lower with the `async` option of a function this component
;; lifts itself, `g`, whose function type is not async. Burroughs resolves this lower's callee by another path than
;; an import's, so it is checked apart from canonopt-async-lower-sync-type.wat. wasm-tools 1.258.0 `validate
;; --features all` and wasmtime 49.0.3 `run` both refuse it at parse: "the `async` canonical option requires an
;; async function type".
;; Assembled with `wasm-tools parse`, which does not validate.
(component
  (core module $a
    (memory (export "mem") 1)
    (func (export "g") (param i32)))
  (core instance $ai (instantiate $a))
  (func $g (param "x" u32) (canon lift (core func $ai "g")))
  (core func $lg (canon lower (func $g) async (memory $ai "mem")))
  (core module $m
    (import "" "g" (func (param i32) (result i32)))
    (func (export "run")))
  (core instance $i (instantiate $m (with "" (instance (export "g" (func $lg))))))
  (func (export "run") (canon lift (core func $i "run"))))
