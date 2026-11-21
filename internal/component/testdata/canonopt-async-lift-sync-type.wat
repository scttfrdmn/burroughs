;; #1314's specimen: wasi:cli/run@0.2.0's `run`, `func() -> result`, which is not an async function type, lifted
;; with the `async` option and a callback. CanonicalABI.md's canonopt validation: `async` "is only allowed when used
;; with an `async` function type in canon lift or canon lower". wasm-tools 1.258.0 `validate --features all` and
;; wasmtime 49.0.3 `run` both refuse it at parse: "the `async` canonical option requires an async function type".
;; Burroughs loaded and ran it. The control is canonopt-async-lift-async-type.wat, which differs only in the type.
;; Assembled with `wasm-tools parse`, which does not validate.
(component
  (type $r (result))
  (core func $tr (canon task.return (result $r)))
  (core module $m
    (import "" "task.return" (func $tr (param i32)))
    (func (export "run") (result i32) (call $tr (i32.const 0)) (i32.const 0))
    (func (export "cb") (param i32 i32 i32) (result i32) (i32.const 0)))
  (core instance $i (instantiate $m (with "" (instance (export "task.return" (func $tr))))))
  (func $run (result $r) (canon lift (core func $i "run") async (callback (func $i "cb"))))
  (instance $ri (export "run" (func $run)))
  (export "wasi:cli/run@0.2.0" (instance $ri)))
