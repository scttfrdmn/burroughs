;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; callee-gap: SP-1's callee-execution fixture (ADR 0104's amendment of 2026-10-18; the chair's third ruling of
;; that day, item 3). A caller's core instance $a calls a child component synchronously, in a loop, and counts
;; its own progress in its own memory:
;;   - `run-spin(n)`: each iteration calls the child's `spin(n)`, a tight loop of n trips with no calls, then
;;     increments $a's word at 0. The child counts each finished spin in its own memory's word at 0.
;;   - `run-cb(n)`: each iteration calls the child's `call-back(n)`, which spins and calls back into $a's `cb`
;;     n times; `cb` increments $a's word at 4.
;; $a reaches the child through a table that a later core instance fills, because the child imports `cb` from
;; $a, so $a is instantiated first. The callback re-enters the same core instance the caller's agent is in,
;; which is the instance a `Stop` of the caller covers.
;;
;; On wasmtime 49.0.3: `run-spin(3, 10)` = 3 (three iterations counted), and `run-cb(2, 3)` = 6 (three callbacks
;; in each of two iterations).
(component
  (core module $am
    (memory (export "m") 1)
    (table (export "t") 2 funcref)
    (type $spin (func (param i32) (result i32)))
    ;; run-spin(iters, n): iters calls of spin(n); returns the progress word
    (func (export "run-spin") (param $iters i32) (param $n i32) (result i32)
      (loop $l
        (drop (call_indirect (type $spin) (local.get $n) (i32.const 0)))
        (i32.store (i32.const 0) (i32.add (i32.load (i32.const 0)) (i32.const 1)))
        (local.set $iters (i32.sub (local.get $iters) (i32.const 1)))
        (br_if $l (local.get $iters)))
      (i32.load (i32.const 0)))
    ;; run-cb(iters, n): iters calls of call-back(n); returns the callback word
    (func (export "run-cb") (param $iters i32) (param $n i32) (result i32)
      (loop $l
        (drop (call_indirect (type $spin) (local.get $n) (i32.const 1)))
        (local.set $iters (i32.sub (local.get $iters) (i32.const 1)))
        (br_if $l (local.get $iters)))
      (i32.load (i32.const 4)))
    (func (export "cb")
      (i32.store (i32.const 4) (i32.add (i32.load (i32.const 4)) (i32.const 1)))))
  (core instance $ai (instantiate $am))
  (alias core export $ai "cb" (core func $cbf))
  (alias core export $ai "t" (core table $tbl))
  (type $cbt (func))
  (func $CB (type $cbt) (canon lift (core func $cbf)))
  (component $child
    (import "cb" (func $cb))
    (core func $cbl (canon lower (func $cb)))
    (core module $cm
      (import "" "cb" (func $cb))
      (memory (export "m") 1)
      ;; spin(n): n tight trips, no calls; counts a finished spin at 0
      (func (export "spin") (param $n i32) (result i32)
        (local $acc i32)
        (loop $l
          (local.set $acc (i32.add (local.get $acc) (local.get $n)))
          (local.set $n (i32.sub (local.get $n) (i32.const 1)))
          (br_if $l (local.get $n)))
        (i32.store (i32.const 0) (i32.add (i32.load (i32.const 0)) (i32.const 1)))
        (local.get $acc))
      ;; call-back(n): n rounds of a short spin and one call back into the caller
      (func (export "call-back") (param $n i32) (result i32)
        (local $k i32)
        (loop $l
          (local.set $k (i32.const 10000))
          (loop $s (local.set $k (i32.sub (local.get $k) (i32.const 1))) (br_if $s (local.get $k)))
          (call $cb)
          (local.set $n (i32.sub (local.get $n) (i32.const 1)))
          (br_if $l (local.get $n)))
        (i32.const 0)))
    (core instance $ci (instantiate $cm (with "" (instance (export "cb" (func $cbl))))))
    (alias core export $ci "spin" (core func $spinf))
    (alias core export $ci "call-back" (core func $cbackf))
    (type $st (func (param "n" u32) (result u32)))
    (func (export "spin") (type $st) (canon lift (core func $spinf)))
    (func (export "call-back") (type $st) (canon lift (core func $cbackf))))
  (instance $c (instantiate $child (with "cb" (func $CB))))
  (alias export $c "spin" (func $SPIN))
  (alias export $c "call-back" (func $CBACK))
  (core func $spinl (canon lower (func $SPIN)))
  (core func $cbackl (canon lower (func $CBACK)))
  ;; fill $a's table with the two lowered child functions
  (core module $set
    (import "" "t" (table 2 funcref))
    (import "" "spin" (func $spin (param i32) (result i32)))
    (import "" "call-back" (func $cback (param i32) (result i32)))
    (elem (table 0) (i32.const 0) func $spin $cback))
  (core instance $seti (instantiate $set (with "" (instance
    (export "t" (table $tbl)) (export "spin" (func $spinl)) (export "call-back" (func $cbackl))))))
  (alias core export $ai "run-spin" (core func $runspinf))
  (alias core export $ai "run-cb" (core func $runcbf))
  (type $rt (func (param "iters" u32) (param "n" u32) (result u32)))
  (func (export "run-spin") (type $rt) (canon lift (core func $runspinf)))
  (func (export "run-cb") (type $rt) (canon lift (core func $runcbf)))
  (export "spin" (func $SPIN)))
