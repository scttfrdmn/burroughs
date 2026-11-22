;; #1314's control: canonopt-async-lift-sync-type.wat's lift, the same `async` option and callback, over an async
;; function type, as wasi:cli/run@0.3.0's `run` is. wasm-tools 1.258.0 `validate --features all` accepts it, and
;; wasmtime 49.0.3 `run` runs it to exit 0: the callee returns ok through task.return and then EXIT.
;; Assembled with `wasm-tools parse`.
(component
  (type $r (result))
  (core func $tr (canon task.return (result $r)))
  (core module $m
    (import "" "task.return" (func $tr (param i32)))
    (func (export "run") (result i32) (call $tr (i32.const 0)) (i32.const 0))
    (func (export "cb") (param i32 i32 i32) (result i32) (i32.const 0)))
  (core instance $i (instantiate $m (with "" (instance (export "task.return" (func $tr))))))
  (func $run async (result $r) (canon lift (core func $i "run") async (callback (func $i "cb"))))
  (instance $ri (export "run" (func $run)))
  (export "wasi:cli/run@0.3.0" (instance $ri)))
