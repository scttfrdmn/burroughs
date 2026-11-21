;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; wait-forever (#1303, ADR 0128): memory.atomic.wait on a shared memory with no other agent to notify it.
;;   forever    wait32 at address 0 for the value it holds (0), with timeout -1 ("never expire")
;;   forever64  wait64 the same way
;;   brief      the control: wait32 with a 10 ms timeout, which a timer ends; it returns 2 ("timed-out")
;;
;; The threads proposal leaves an infinite wait with no notifier suspended for ever. wasmtime 49.0.3 hangs on
;; forever and forever64, idle and silent, and prints 2 for brief (wait-forever.reading). Burroughs at d8076a1
;; ended the host at once with Go's runtime fatal, "all goroutines are asleep - deadlock!", exit 2.
;;
;; Assembled with `wasm-tools parse wait-forever.wat -o wait-forever.wasm` (wasm-tools 1.258.0).
(module
  (memory 1 1 shared)
  (func (export "forever") (result i32)
    (memory.atomic.wait32 (i32.const 0) (i32.const 0) (i64.const -1)))
  (func (export "forever64") (result i32)
    (memory.atomic.wait64 (i32.const 0) (i64.const 0) (i64.const -1)))
  (func (export "brief") (result i32)
    (memory.atomic.wait32 (i32.const 0) (i32.const 0) (i64.const 10000000))))
