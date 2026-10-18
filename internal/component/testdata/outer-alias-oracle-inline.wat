;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; #756's oracle, inline-typed control: `outer-alias-oracle.wat` with one change, the func type declared
;; INSIDE the instance type (`(type $ift …)`), so no outer alias is involved. It reads the same 242
;; (`outer-alias-oracle.reading`).
(component
  (component $impl
    (core func $ret (canon task.return (result u32)))
    (core module $m
      (import "" "ret" (func $ret (param i32)))
      (func (export "op") (param i32) (result i32) (call $ret (i32.add (local.get 0) (i32.const 35))) (i32.const 0))
      (func (export "cb") (param i32 i32 i32) (result i32) unreachable))
    (core instance $mi (instantiate $m (with "" (instance (export "ret" (func $ret))))))
    (alias core export $mi "op" (core func $opf))
    (alias core export $mi "cb" (core func $cbf))
    (type $opt (func async (param "x" u32) (result u32)))
    (func (export "op") (type $opt) (canon lift (core func $opf) async (callback $cbf))))
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
