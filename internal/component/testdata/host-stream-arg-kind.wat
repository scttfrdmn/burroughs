;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; host-stream-arg-kind (#1367): a host import whose parameter is a stream, given a handle that is not a readable
;; stream end of its type in a state it may be lifted from. The import is wasi:cli/stdout's write-via-stream, lowered
;; sync, `func(data: stream<u8>) -> future<result<_, error-code>>`. The pin lifts a stream argument through
;; def:lift_async_value, which removes the handle and traps unless it is a readable end of the parameter's type, IDLE
;; and joined to no set:
;;
;;   trap_if(not isinstance(end, ReadableEndT)); trap_if(end.t != t); trap_if(end.state != End.State.IDLE);
;;   trap_if(end.in_waitable_set())
;;
;;   - wvs-readable-end: the control, a readable stream<u8> end, which the host takes: 7.
;;   - wvs-writable-end, wvs-future-end, wvs-no-handle: the kind (a writable stream end, a readable future end, and an
;;     index with no entry, def:Table.remove's trap).
;;   - wvs-u32-stream: a readable stream<u32> end, the type.
;;   - wvs-joined-end: a readable stream<u8> end joined to a set.
;;   - wvs-reading-end: a readable stream<u8> end with a read pending (COPYING), the state.
;;
;; Every export is sync-typed and lifted sync, and returns 7 after the call. Measured on wasmtime 49.0.3 (`--invoke`)
;; and on the pin (refread.py's host-stream-arg-kind rows), committed as host-stream-arg-kind.reading. Assembled with
;; `wasm-tools parse host-stream-arg-kind.wat -o host-stream-arg-kind.wasm` (wasm-tools 1.258.0).
(component
  (type $types-t (instance
    (type $ec (enum "io" "illegal-byte-sequence" "pipe"))
    (export "error-code" (type (eq $ec)))))
  (import "wasi:cli/types@0.3.0" (instance $types (type $types-t)))
  (alias export $types "error-code" (type $error-code))
  (type $stdout-t (instance
    (alias outer 1 $error-code (type $ec))
    (export "error-code" (type $ec2 (eq $ec)))
    (type $s (stream u8))
    (type $r (result (error $ec2)))
    (type $f (future $r))
    (export "write-via-stream" (func (param "data" $s) (result $f)))))
  (import "wasi:cli/stdout@0.3.0" (instance $stdout (type $stdout-t)))
  (alias export $stdout "write-via-stream" (func $wvs))
  (core module $mm (memory (export "m") 1 1))
  (core instance $mi (instantiate $mm))
  (alias core export $mi "m" (core memory $m))
  (type $u8s (stream u8))
  (type $u32s (stream u32))
  (type $u8f (future u8))
  (core func $wvs-low (canon lower (func $wvs)))
  (core func $snew (canon stream.new $u8s))
  (core func $snew32 (canon stream.new $u32s))
  (core func $sread (canon stream.read $u8s async (memory $m)))
  (core func $fnew (canon future.new $u8f))
  (core func $wsnew (canon waitable-set.new))
  (core func $wsjoin (canon waitable.join))
  (core module $gm
    (import "" "mem" (memory 1 1))
    (import "" "wvs" (func $wvs (param i32) (result i32)))
    (import "" "snew" (func $snew (result i64)))
    (import "" "snew32" (func $snew32 (result i64)))
    (import "" "sread" (func $sread (param i32 i32 i32) (result i32)))
    (import "" "fnew" (func $fnew (result i64)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "wsjoin" (func $wsjoin (param i32 i32)))
    (func $ri (param $s i64) (result i32) (i32.wrap_i64 (local.get $s)))
    (func $wi (param $s i64) (result i32) (i32.wrap_i64 (i64.shr_u (local.get $s) (i64.const 32))))
    (func (export "wvs-readable-end") (result i32)
      (drop (call $wvs (call $ri (call $snew))))
      (i32.const 7))
    (func (export "wvs-writable-end") (result i32)
      (drop (call $wvs (call $wi (call $snew))))
      (i32.const 7))
    (func (export "wvs-future-end") (result i32)
      (drop (call $wvs (call $ri (call $fnew))))
      (i32.const 7))
    (func (export "wvs-no-handle") (result i32)
      (drop (call $wvs (i32.const 99)))
      (i32.const 7))
    (func (export "wvs-u32-stream") (result i32)
      (drop (call $wvs (call $ri (call $snew32))))
      (i32.const 7))
    (func (export "wvs-joined-end") (result i32) (local $r i32)
      (local.set $r (call $ri (call $snew)))
      (call $wsjoin (local.get $r) (call $wsnew))
      (drop (call $wvs (local.get $r)))
      (i32.const 7))
    (func (export "wvs-reading-end") (result i32) (local $r i32)
      (local.set $r (call $ri (call $snew)))
      (drop (call $sread (local.get $r) (i32.const 128) (i32.const 4)))
      (drop (call $wvs (local.get $r)))
      (i32.const 7)))
  (core instance $gi (instantiate $gm (with "" (instance (export "mem" (memory $m))
    (export "wvs" (func $wvs-low)) (export "snew" (func $snew)) (export "snew32" (func $snew32))
    (export "sread" (func $sread)) (export "fnew" (func $fnew)) (export "wsnew" (func $wsnew))
    (export "wsjoin" (func $wsjoin))))))
  (type $st (func (result u32)))
  (func (export "wvs-readable-end") (type $st) (canon lift (core func $gi "wvs-readable-end")))
  (func (export "wvs-writable-end") (type $st) (canon lift (core func $gi "wvs-writable-end")))
  (func (export "wvs-future-end") (type $st) (canon lift (core func $gi "wvs-future-end")))
  (func (export "wvs-no-handle") (type $st) (canon lift (core func $gi "wvs-no-handle")))
  (func (export "wvs-u32-stream") (type $st) (canon lift (core func $gi "wvs-u32-stream")))
  (func (export "wvs-joined-end") (type $st) (canon lift (core func $gi "wvs-joined-end")))
  (func (export "wvs-reading-end") (type $st) (canon lift (core func $gi "wvs-reading-end"))))
