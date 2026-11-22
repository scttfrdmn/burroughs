;; #1314's neighbour: an async canon lift, over an async function type, naming `async` twice. CanonicalABI.md's
;; canonopt validation: options "can be specified at most once in any particular list of options". wasm-tools
;; 1.258.0 `validate --features all` and wasmtime 49.0.3 `run` both refuse it at parse: "canonical option `async` is
;; specified more than once".
;; Assembled with `wasm-tools parse`, which does not validate.
(component
  (core module $m
    (func (export "run") (result i32) (i32.const 0))
    (func (export "cb") (param i32 i32 i32) (result i32) (i32.const 0)))
  (core instance $i (instantiate $m))
  (func (export "run") async (canon lift (core func $i "run") async async (callback (func $i "cb")))))
