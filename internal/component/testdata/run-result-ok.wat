;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; run-result-ok (ADR 0120): the smallest `wasi:cli/run` export, cli-run-trap.wat's shape, whose `run` returns
;; `ok`: its core func returns the result's discriminant, 0. No imports, so it runs on the stub host.
;; run-result-err.wat and run-result-ok.wat differ in that one constant, so the err fixture's exit 1 is the
;; result's and nothing else's.
;;
;; Assembled with `wasm-tools parse run-result-ok.wat -o run-result-ok.wasm` (wasm-tools 1.258.0).
;; run-result.reading is wasmtime 49.0.3's exit code for each.
(component
  (core module $m
    (func (export "run") (result i32) i32.const 0))
  (core instance $i (instantiate $m))
  (alias core export $i "run" (core func $runf))
  (type $rt (func (result (result))))
  (func $run (type $rt) (canon lift (core func $runf)))
  (instance $r (export "run" (func $run)))
  (export "wasi:cli/run@0.2.0" (instance $r))
)
