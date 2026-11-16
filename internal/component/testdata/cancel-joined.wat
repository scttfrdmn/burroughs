;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; cancel-joined (grave #1301): a sync stream.cancel-read or stream.cancel-write on an end joined to a waitable set
;; traps, as def:cancel_copy's `trap_if(end.in_waitable_set() and not async_)` does. Each export parks one copy on a
;; fresh stream (an async read with no writer, or an async write with no reader), optionally joins that end to a
;; set, then cancels it with the sync form and returns the cancel's result. The `unjoined` rows are the controls that
;; keep the guard from refusing more than the pin does. The async form is not exercised: wasmtime 49.0.3 refuses an
;; async stream.cancel-read at validation unless its "more async builtins" feature is on (measured), so it has no
;; default-oracle reading.
;; Measured on wasmtime 49.0.3 (`--invoke`), committed as cancel-joined.reading.
;; Assembled with `wasm-tools parse cancel-joined.wat -o cancel-joined.wasm` (wasm-tools 1.258.0).
(component
  (core module $mm (memory (export "m") 1))
  (core instance $mi (instantiate $mm))
  (alias core export $mi "m" (core memory $m))
  (type $s (stream u8))
  (core func $snew (canon stream.new $s))
  (core func $sread (canon stream.read $s async (memory $m)))
  (core func $swrite (canon stream.write $s async (memory $m)))
  (core func $crsync (canon stream.cancel-read $s))
  (core func $cwsync (canon stream.cancel-write $s))
  (core func $wsnew (canon waitable-set.new))
  (core func $wsjoin (canon waitable.join))
  (core module $gm
    (import "" "mem" (memory 1))
    (import "" "snew" (func $snew (result i64)))
    (import "" "sread" (func $sread (param i32 i32 i32) (result i32)))
    (import "" "swrite" (func $swrite (param i32 i32 i32) (result i32)))
    (import "" "crsync" (func $crsync (param i32) (result i32)))
    (import "" "cwsync" (func $cwsync (param i32) (result i32)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "wsjoin" (func $wsjoin (param i32 i32)))
    ;; A parked read on the readable end (no writer has written), optionally joined; returns the readable handle.
    (func $parkread (param $join i32) (result i32) (local $p i64) (local $r i32)
      (local.set $p (call $snew))
      (local.set $r (i32.wrap_i64 (local.get $p)))
      (drop (call $sread (local.get $r) (i32.const 64) (i32.const 4)))
      (if (local.get $join) (then (call $wsjoin (local.get $r) (call $wsnew))))
      (local.get $r))
    ;; A parked write on the writable end (no reader has read), optionally joined; returns the writable handle.
    (func $parkwrite (param $join i32) (result i32) (local $p i64) (local $w i32)
      (local.set $p (call $snew))
      (local.set $w (i32.wrap_i64 (i64.shr_u (local.get $p) (i64.const 32))))
      (drop (call $swrite (local.get $w) (i32.const 64) (i32.const 4)))
      (if (local.get $join) (then (call $wsjoin (local.get $w) (call $wsnew))))
      (local.get $w))
    (func (export "read-joined-sync") (result i32) (call $crsync (call $parkread (i32.const 1))))
    (func (export "read-unjoined-sync") (result i32) (call $crsync (call $parkread (i32.const 0))))
    (func (export "write-joined-sync") (result i32) (call $cwsync (call $parkwrite (i32.const 1))))
    (func (export "write-unjoined-sync") (result i32) (call $cwsync (call $parkwrite (i32.const 0)))))
  (core instance $gi (instantiate $gm (with "" (instance (export "mem" (memory $m))
    (export "snew" (func $snew)) (export "sread" (func $sread)) (export "swrite" (func $swrite))
    (export "crsync" (func $crsync)) (export "cwsync" (func $cwsync))
    (export "wsnew" (func $wsnew)) (export "wsjoin" (func $wsjoin))))))
  (type $at (func async (result u32)))
  (func (export "read-joined-sync") (type $at) (canon lift (core func $gi "read-joined-sync")))
  (func (export "read-unjoined-sync") (type $at) (canon lift (core func $gi "read-unjoined-sync")))
  (func (export "write-joined-sync") (type $at) (canon lift (core func $gi "write-joined-sync")))
  (func (export "write-unjoined-sync") (type $at) (canon lift (core func $gi "write-unjoined-sync"))))
