;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; async-spill-params (ADR 0123): an async lower whose parameters flatten past MAX_FLAT_ASYNC_PARAMS (4), so its
;; core function takes one pointer to the parameter tuple (def:flatten_functype). `call(ptr)` stores
;; tuple<u8, u64, s16, u32, u32> at ptr, laid out at 0, 8, 16, 20 and 24 (size 32, alignment 8), then calls the
;; lower with ptr and returns its packed status. The test serves `wide` itself and reads the flat words it is
;; handed. A ptr not aligned to 8, or a tuple past the end of memory, is the guest's trap.
;; Assembled with `wasm-tools parse async-spill-params.wat -o async-spill-params.wasm` (wasm-tools 1.258.0).
(component
  (import "test:spill/wide" (instance $w
    (export "wide" (func async (param "a" u8) (param "b" u64) (param "c" s16) (param "d" u32) (param "e" u32)))))
  (alias export $w "wide" (func $wide))
  (core module $mm (memory (export "m") 1))
  (core instance $mi (instantiate $mm))
  (alias core export $mi "m" (core memory $m))
  (core func $widel (canon lower (func $wide) async (memory $m)))
  (core module $gm
    (import "" "m" (memory 1))
    (import "" "wide" (func $wide (param i32) (result i32)))
    (func (export "call") (param $ptr i32) (result i32)
      (if (i32.lt_u (local.get $ptr) (i32.const 65504))
        (then
          (i32.store8 (local.get $ptr) (i32.const 200))
          (i64.store offset=8 (local.get $ptr) (i64.const 0x0102030405060708))
          (i32.store16 offset=16 (local.get $ptr) (i32.const -3))
          (i32.store offset=20 (local.get $ptr) (i32.const 7))
          (i32.store offset=24 (local.get $ptr) (i32.const 9))))
      (call $wide (local.get $ptr))))
  (core instance $gi (instantiate $gm (with "" (instance (export "m" (memory $m)) (export "wide" (func $widel))))))
  (func (export "call") (param "ptr" u32) (result u32) (canon lift (core func $gi "call"))))
