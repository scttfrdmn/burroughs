;; #1314's neighbour: a canon lift with a `callback` and no `async`. CanonicalABI.md's canonopt validation: `callback`
;; "cannot be present without `async`". wasm-tools 1.258.0 `validate --features all` and wasmtime 49.0.3 `run` both
;; refuse it at parse: "cannot specify callback without async".
;; Assembled with `wasm-tools parse`, which does not validate.
(component
  (core module $m
    (func (export "run"))
    (func (export "cb") (param i32 i32 i32) (result i32) (i32.const 0)))
  (core instance $i (instantiate $m))
  (func (export "run") (canon lift (core func $i "run") (callback (func $i "cb")))))
