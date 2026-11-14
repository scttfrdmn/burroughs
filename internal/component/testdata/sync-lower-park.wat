;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; sync-lower-park (#1268, ADR 0126): a pending blocking sync lower beside a parked task, in one instance.
;;   block  async-typed, lifted with no async option: the host's test `op`, an async import, lowered WITHOUT the
;;          async option, so the call blocks until op resolves, and returns op's value (one flat word).
;;   park   callback-lifted: a new, empty waitable set, and WAIT on it. Nothing joins it, so only the park bound,
;;          or Close, ends the wait; the callback, if it ever ran, would return its event code.
;; A test holds op unresolved while `park` waits: the pending sync lower is a waker, so the bound re-arms.
;; Harness-only: the park bound is Burroughs' instrument, and wasmtime has none to read.
;;
;; Assembled with `wasm-tools parse sync-lower-park.wat -o sync-lower-park.wasm` (wasm-tools 1.258.0).
(component
  (import "test:async/ops" (instance $ops
    (export "op" (func async (param "x" u32) (result u32)))))
  (alias export $ops "op" (func $op))
  (core func $opl (canon lower (func $op)))
  (core func $wsnew (canon waitable-set.new))
  (core func $ret (canon task.return (result u32)))
  (core module $gm
    (import "" "op" (func $op (param i32) (result i32)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "ret" (func $ret (param i32)))
    (func (export "block") (result i32) (call $op (i32.const 5)))
    (func (export "park") (result i32) (i32.or (i32.const 2) (i32.shl (call $wsnew) (i32.const 4)))) ;; WAIT
    (func (export "cb") (param $code i32) (param i32) (param i32) (result i32)
      (call $ret (local.get $code))
      (i32.const 0))) ;; EXIT
  (core instance $gi (instantiate $gm (with "" (instance
    (export "op" (func $opl)) (export "wsnew" (func $wsnew)) (export "ret" (func $ret))))))
  (type $at (func async (result u32)))
  (func (export "block") (type $at) (canon lift (core func $gi "block")))
  (func (export "park") (type $at) (canon lift (core func $gi "park") async (callback (func $gi "cb")))))
