;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; #756's witness fixture, aliased form: the host-impl path, which is the one #756 reaches. A cross-component
;; lower takes its signature from the callee's own lift, so only an import filled by a host impl reads the
;; import's instance type. That is `lowerSignature`, and an outer-aliased func type there used to give a nil
;; signature. `op`'s type is an outer alias inside the imported instance type, as in `async-lower-synth.wasm`.
;; A test Host fills `test:alias/ops::op` with an impl that resolves inline, and `run()` returns
;; status*100 + the result stored at the retptr. wasmtime cannot supply the import, so the oracle is the
;; composed pair `outer-alias-oracle*.wat`, where wasmtime reads the aliased and inline forms alike.
(component
  (core module $memmod (memory (export "m") 1))
  (core instance $memi (instantiate $memmod))
  (alias core export $memi "m" (core memory $cm))
  (type $ft (func async (param "x" u32) (result u32)))
  (import "test:alias/ops" (instance $ops (export "op" (func (type $ft)))))
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
