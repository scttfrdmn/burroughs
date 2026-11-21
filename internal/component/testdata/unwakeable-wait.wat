;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; unwakeable-wait (#1303, ADR 0128): a wasi:cli/run whose `run` is async-typed and lifted plainly, so its wait is
;; the stackful `waitable-set.wait`. It creates a waitable set, joins nothing to it, and waits on it. Nothing can
;; ever deliver an event to an empty set, so the wait never ends.
;;
;; The pin leaves an async-typed waiter blocked (def:Store.tick does nothing with no ready candidate) and traps
;; only a sync-typed one (def:canon_lift's `trap_if(not candidates)`, inside `if not ft.async_`). wasmtime 49.0.3
;; hangs, idle and silent (unwakeable-wait.reading). Burroughs at d8076a1 ended the host with Go's runtime fatal,
;; "all goroutines are asleep - deadlock!", exit 2.
;;
;; Assembled with `wasm-tools parse unwakeable-wait.wat -o unwakeable-wait.wasm` (wasm-tools 1.258.0; validates
;; with --features all).
(component
  (core module $memmod (memory (export "m") 1 1))
  (core instance $memi (instantiate $memmod))
  (alias core export $memi "m" (core memory $cm))
  (core func $wsnew (canon waitable-set.new))
  (core func $wswait (canon waitable-set.wait (memory $cm)))
  (core module $runmod
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "wswait" (func $wswait (param i32 i32) (result i32)))
    (func (export "run") (result i32)
      (drop (call $wswait (call $wsnew) (i32.const 8)))
      unreachable)) ;; the wait never returns, so this is never reached
  (core instance $runi (instantiate $runmod
    (with "" (instance (export "wsnew" (func $wsnew)) (export "wswait" (func $wswait))))))
  (alias core export $runi "run" (core func $runf))
  (type $rt (func async (result (result))))
  (func $run (type $rt) (canon lift (core func $runf)))
  (instance $r (export "run" (func $run)))
  (export "wasi:cli/run@0.3.0" (instance $r))
)
