;; #1314's neighbour: an async canon lift, over an async function type, that also names a `post-return`.
;; CanonicalABI.md's canonopt validation: `async` "cannot be present with `post-return`". wasm-tools 1.258.0
;; `validate --features all` and wasmtime 49.0.3 `run` both refuse it at parse: "cannot specify post-return function
;; in async".
;; Assembled with `wasm-tools parse`, which does not validate.
(component
  (core module $m
    (func (export "run") (result i32) (i32.const 0))
    (func (export "cb") (param i32 i32 i32) (result i32) (i32.const 0))
    (func (export "pr")))
  (core instance $i (instantiate $m))
  (func (export "run") async
    (canon lift (core func $i "run") async (callback (func $i "cb")) (post-return (func $i "pr")))))
