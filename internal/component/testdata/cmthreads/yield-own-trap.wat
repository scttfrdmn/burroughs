;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; yield-own-trap (#1265, behind gate:cm-threads): a created Thread traps while its own task waits at the YIELD
;; arm, so the trap is the call's own and is not reported as a lockdown. `run` (an async callback lift) creates
;; Thread T, readies it, and YIELDs forever. T executes `unreachable`.
;;
;; wasmtime 49.0.3: `wasmtime run -W component-model-threading=y --invoke 'run()' yield-own-trap.wasm` traps at
;; T's `unreachable` (tmod), 10 of 10, asserted by build.sh. The YIELD arm's lock case (#1265) must not answer
;; this call in place of its own trap: ADR 0105 has the trapping call return its own trap unchanged.
;;
;; Assembled with `wasm-tools parse yield-own-trap.wat -o yield-own-trap.wasm` (wasm-tools 1.258.0), by build.sh.
(component
  (core type $bodyt (func (param i32)))
  (core module $tmod
    (table (export "t") 1 1 funcref)
    (func $t (param i32) unreachable)
    (elem (i32.const 0) $t))
  (core instance $ti (instantiate $tmod))
  (core func $new (canon thread.new-indirect (core type $bodyt) (core table $ti "t")))
  (core func $later (canon thread.resume-later))
  (core module $m
    (import "" "new" (func $new (param i32 i32) (result i32)))
    (import "" "later" (func $later (param i32)))
    (func (export "run") (result i32)
      (call $later (call $new (i32.const 0) (i32.const 0)))
      (i32.const 1)) ;; YIELD
    (func (export "cb") (param i32 i32 i32) (result i32)
      (i32.const 1))) ;; YIELD forever: only T's trap ends the call
  (core instance $mi (instantiate $m
    (with "" (instance (export "new" (func $new)) (export "later" (func $later))))))
  (alias core export $mi "run" (core func $runf))
  (alias core export $mi "cb" (core func $cbf))
  (type $rt (func async (result u32)))
  (func (export "run") (type $rt) (canon lift (core func $runf) async (callback $cbf)))
)
