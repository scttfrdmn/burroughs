;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; #756's witness fixture, inline-typed control: `outer-alias-lower.wat` with the func type declared INSIDE
;; the imported instance type, so no outer alias is involved. It must store the impl's result exactly as the
;; aliased form does.
(component
  (core module $memmod (memory (export "m") 1))
  (core instance $memi (instantiate $memmod))
  (alias core export $memi "m" (core memory $cm))
  (import "test:alias/ops" (instance $ops (type $ift (func async (param "x" u32) (result u32))) (export "op" (func (type $ift)))))
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
