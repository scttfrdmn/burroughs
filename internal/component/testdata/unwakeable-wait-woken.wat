;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; unwakeable-wait-woken (#1303, ADR 0128): unwakeable-wait.wat's control. The same stackful `waitable-set.wait`
;; in an async-typed, plainly lifted `run`, but the set holds an async-lowered wait-for(10ms), so a host timer
;; wakes it. `run` then returns ok, and the process exits 0.
;;
;; It shows that what ADR 0128 adds to a parked wait changes nothing about how a wait that can be woken ends: it
;; ends when it is woken, and the process does not outlive it.
;;
;; wasmtime 49.0.3: exit 0, nothing on stdout or stderr (unwakeable-wait.reading).
;;
;; Assembled with `wasm-tools parse unwakeable-wait-woken.wat -o unwakeable-wait-woken.wasm` (wasm-tools 1.258.0;
;; validates with --features all).
(component
  (import "wasi:clocks/monotonic-clock@0.3.0" (instance $mc
    (export "wait-for" (func async (param "how-long" u64)))))
  (alias export $mc "wait-for" (func $wf))
  (core module $memmod (memory (export "m") 1 1))
  (core instance $memi (instantiate $memmod))
  (alias core export $memi "m" (core memory $cm))
  (core func $wfl (canon lower (func $wf) async))
  (core func $wsnew (canon waitable-set.new))
  (core func $wsjoin (canon waitable.join))
  (core func $wswait (canon waitable-set.wait (memory $cm)))
  (core module $runmod
    (import "" "wf" (func $wf (param i64) (result i32)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "wsjoin" (func $wsjoin (param i32 i32)))
    (import "" "wswait" (func $wswait (param i32 i32) (result i32)))
    (func (export "run") (result i32) (local $packed i32) (local $set i32)
      ;; wait-for(10ms) cannot resolve inline, so the lower returns STARTED with the subtask's index above the
      ;; low four bits.
      (local.set $packed (call $wf (i64.const 10000000)))
      (local.set $set (call $wsnew))
      (call $wsjoin (i32.shr_u (local.get $packed) (i32.const 4)) (local.get $set))
      (drop (call $wswait (local.get $set) (i32.const 8)))
      (i32.const 0))) ;; ok
  (core instance $runi (instantiate $runmod
    (with "" (instance (export "wf" (func $wfl)) (export "wsnew" (func $wsnew)) (export "wsjoin" (func $wsjoin))
                       (export "wswait" (func $wswait))))))
  (alias core export $runi "run" (core func $runf))
  (type $rt (func async (result (result))))
  (func $run (type $rt) (canon lift (core func $runf)))
  (instance $r (export "run" (func $run)))
  (export "wasi:cli/run@0.3.0" (instance $r))
)
