;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; read-park (#1234, ADR 0122): a stdin read through read-via-stream, left pending two ways.
;;   start  sync-lifted: read-via-stream, then one async stream.read of 8 bytes into memory at 64, returning
;;          the read's packed result: BLOCKED (0xffffffff) on a fresh stream, with the host's Read in flight,
;;          so a Close after it has a Read to abandon.
;;   run    callback-lifted: the same read, joined to a set, and WAIT. The callback returns
;;          task.return(code << 16 | payload), so a read of "abc" is STREAM_READ (2) << 16 | COMPLETED with
;;          progress 3 (0x30) = 131120.
;;   twice  callback-lifted: a read of 2, waited for, then a read of 8 whose return is taken as it comes, so a
;;          second read served inline and one that blocks read differently.
;; Readings: read-park.reading (wasmtime 49.0.3).
;; Assembled with `wasm-tools parse read-park.wat -o read-park.wasm` (wasm-tools 1.258.0).
(component
  (type $types-t (instance
    (type $ec (enum "io" "illegal-byte-sequence" "pipe"))
    (export "error-code" (type (eq $ec)))))
  (import "wasi:cli/types@0.3.0" (instance $types (type $types-t)))
  (alias export $types "error-code" (type $error-code))
  (type $stdin-t (instance
    (alias outer 1 $error-code (type $ec))
    (export "error-code" (type $ec2 (eq $ec)))
    (type $s (stream u8))
    (type $r (result (error $ec2)))
    (type $f (future $r))
    (type $t (tuple $s $f))
    (export "read-via-stream" (func (result $t)))))
  (import "wasi:cli/stdin@0.3.0" (instance $stdin (type $stdin-t)))
  (alias export $stdin "read-via-stream" (func $rvs))
  (core module $mm (memory (export "m") 1))
  (core instance $mi (instantiate $mm))
  (alias core export $mi "m" (core memory $m))
  (type $bytes (stream u8))
  (core func $rvs-low (canon lower (func $rvs) (memory $m)))
  (core func $sread (canon stream.read $bytes async (memory $m)))
  (core func $wsnew (canon waitable-set.new))
  (core func $wsjoin (canon waitable.join))
  (core func $ret (canon task.return (result u32)))
  (core module $gm
    (import "" "m" (memory 1))
    (import "" "rvs" (func $rvs (param i32)))
    (import "" "sread" (func $sread (param i32 i32 i32) (result i32)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "wsjoin" (func $wsjoin (param i32 i32)))
    (import "" "ret" (func $ret (param i32)))
    ;; read-via-stream's two handles land at 0 (the stream) and 4 (the future); the read asks for 8 bytes at 64.
    (func $open (result i32)
      (call $rvs (i32.const 0))
      (call $sread (i32.load (i32.const 0)) (i32.const 64) (i32.const 8)))
    (func (export "start") (result i32) (call $open))
    (func (export "run") (result i32) (local $set i32)
      (drop (call $open))
      (local.set $set (call $wsnew))
      (call $wsjoin (i32.load (i32.const 0)) (local.get $set))
      (i32.or (i32.const 2) (i32.shl (local.get $set) (i32.const 4)))) ;; WAIT on the set
    (func (export "cb") (param $code i32) (param i32) (param $payload i32) (result i32)
      (call $ret (i32.or (i32.shl (local.get $code) (i32.const 16)) (local.get $payload)))
      (i32.const 0)) ;; EXIT
    ;; twice: a read of 2 bytes at 64, waited for; then, in the callback, a read of 8 at 72, whose return is
    ;; taken as it comes. task.return(first payload << 16 | second return & 0xffff), so a second read that
    ;; completes inline reads its packed payload and one that blocks reads 0xffff.
    (func (export "twice") (result i32) (local $set i32)
      (call $rvs (i32.const 0))
      (drop (call $sread (i32.load (i32.const 0)) (i32.const 64) (i32.const 2)))
      (local.set $set (call $wsnew))
      (call $wsjoin (i32.load (i32.const 0)) (local.get $set))
      (i32.or (i32.const 2) (i32.shl (local.get $set) (i32.const 4))))
    (func (export "cb2") (param i32) (param i32) (param $payload i32) (result i32)
      (call $ret (i32.or (i32.shl (local.get $payload) (i32.const 16))
        (i32.and (call $sread (i32.load (i32.const 0)) (i32.const 72) (i32.const 8)) (i32.const 0xffff))))
      (i32.const 0))) ;; EXIT
  (core instance $gi (instantiate $gm (with "" (instance
    (export "m" (memory $m)) (export "rvs" (func $rvs-low)) (export "sread" (func $sread))
    (export "wsnew" (func $wsnew)) (export "wsjoin" (func $wsjoin)) (export "ret" (func $ret))))))
  (type $at (func async (result u32)))
  (type $st (func (result u32)))
  (func (export "run") (type $at) (canon lift (core func $gi "run") async (callback (func $gi "cb"))))
  (func (export "twice") (type $at) (canon lift (core func $gi "twice") async (callback (func $gi "cb2"))))
  (func (export "start") (type $st) (canon lift (core func $gi "start"))))
