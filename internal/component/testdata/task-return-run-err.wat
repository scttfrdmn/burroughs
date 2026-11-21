;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; task-return-run-err (#1320): task-return-run-ok.wat with `task.return` given the err case (discriminant 1),
;; and nothing else changed, so the exit code is the result's and nothing else's. Read that fixture's header
;; for where the text came from.
;;
;; Assembled with `wasm-tools parse task-return-run-err.wat -o task-return-run-err.wasm` (wasm-tools 1.258.0;
;; validates with --features all). task-return-run.reading is wasmtime 49.0.3's exit code for each.
(component
  (type $r (result))
  (core func $tr (canon task.return (result $r)))
  (core module $m
    (import "" "task.return" (func $tr (param i32)))
    (func (export "run") (result i32) (call $tr (i32.const 1)) (i32.const 0))
    (func (export "cb") (param i32 i32 i32) (result i32) (i32.const 0)))
  (core instance $i (instantiate $m (with "" (instance (export "task.return" (func $tr))))))
  (func $run async (result $r) (canon lift (core func $i "run") async (callback (func $i "cb"))))
  (instance $ri (export "run" (func $run)))
  (export "wasi:cli/run@0.3.0" (instance $ri)))
