;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; task-return-run-ok (#1320): wasi:cli/run@0.3.0's `run: async func() -> result`, lifted `async` with a
;; callback, whose core func calls `task.return` with the ok case (discriminant 0) and then EXITs. Burroughs
;; trapped at the `task.return`, because liftFlatValue had no variant arm, where wasmtime 49.0.3 exits 0.
;;
;; The text is copied from #1314's canonopt-async-lift-async-type.wat (branch fix-1314-async-lift-type),
;; which is the specimen #1320 was found on, with this header in place of that one. task-return-run-err.wat
;; differs from it in the one constant task.return is given.
;;
;; Assembled with `wasm-tools parse task-return-run-ok.wat -o task-return-run-ok.wasm` (wasm-tools 1.258.0;
;; validates with --features all). task-return-run.reading is wasmtime 49.0.3's exit code for each.
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
