;; #1314's neighbour: an async canon lift whose `callback` is a core function of the wrong type, (i32 i32) -> i32.
;; CanonicalABI.md's canonopt validation: `callback` "the function has type `(func (param i32 i32 i32) (result
;; i32))`". wasm-tools 1.258.0 `validate --features all` and wasmtime 49.0.3 `run` both refuse it at parse:
;; "canonical option `callback` uses a core function with an incorrect signature".
;; Assembled with `wasm-tools parse`, which does not validate.
(component
  (core module $m
    (func (export "run") (result i32) (i32.const 0))
    (func (export "cb") (param i32 i32) (result i32) (i32.const 0)))
  (core instance $i (instantiate $m))
  (func (export "run") async (canon lift (core func $i "run") async (callback (func $i "cb")))))
