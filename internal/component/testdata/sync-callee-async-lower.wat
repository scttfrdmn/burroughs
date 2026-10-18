;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; sync-callee-async-lower: #1050. A parent's inner component ASYNC-lowers a sibling's `op`, and the sibling
;; lifts `op` SYNCHRONOUSLY (no async canon option, no callback), so its core func returns the result. The
;; callee returns at once, so the lower returns RETURNED (2) with the result stored, and `run()` returns
;; status*100 + the stored result: 242. Burroughs read 200 (RETURNED, nothing stored), because it drove the
;; sync callee through the callback loop and read its return value, 42, as a packed callback code.
;; Authored with `wasm-tools parse`; the reading is in sync-callee-async-lower.reading.
(component
  (component $impl
    (core module $m (func (export "op") (param i32) (result i32) (i32.add (local.get 0) (i32.const 35))))
    (core instance $mi (instantiate $m))
    (alias core export $mi "op" (core func $opf))
    (type $opt (func async (param "x" u32) (result u32)))
    (func (export "op") (type $opt) (canon lift (core func $opf))))
  (component $inner
    (core module $memmod (memory (export "m") 1))
    (core instance $memi (instantiate $memmod))
    (alias core export $memi "m" (core memory $cm))
    (import "ops" (instance $ops (type $ift (func async (param "x" u32) (result u32))) (export "op" (func (type $ift)))))
    (alias export $ops "op" (func $impf))
    (core func $lowered (canon lower (func $impf) async (memory $cm)))
    (core module $runmod
      (import "" "op" (func $op (param i32 i32) (result i32)))
      (import "" "m" (memory 1))
      (func (export "run") (result i32)
        (local $st i32)
        (local.set $st (call $op (i32.const 7) (i32.const 32)))
        (i32.add (i32.mul (local.get $st) (i32.const 100)) (i32.load (i32.const 32)))))
    (core instance $runi (instantiate $runmod (with "" (instance (export "op" (func $lowered)) (export "m" (memory $cm))))))
    (alias core export $runi "run" (core func $runf))
    (type $runt (func (result u32)))
    (func (export "run") (type $runt) (canon lift (core func $runf))))
  (instance $impli (instantiate $impl))
  (instance $inneri (instantiate $inner (with "ops" (instance $impli))))
  (alias export $inneri "run" (func $run))
  (export "run" (func $run)))
