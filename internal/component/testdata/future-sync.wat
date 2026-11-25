;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; future-sync (#1352): future.read and future.write in their SYNC form, on one future<u8> per export minted by
;; future.new. The value written is 7. wasmtime 49.0.3 loads a sync future.read or future.write only with
;; `component-model-more-async-builtins`.
;;
;; def:copy waits for its own event when the form is sync and the copy did not complete inline
;; (def:copy:"end.wait_for_pending_event()"), and traps a sync copy on an end joined to a set
;; (def:copy:"trap_if(end.in_waitable_set() and not opts.async_)").
;;
;;   - sync-write-to-parked-read, sync-read-of-parked-write: the other end's copy is parked (async), so the sync copy
;;     completes inline and returns COMPLETED (0); the read's row adds the byte, 0 | (7 << 8).
;;   - sync-write-after-reader-dropped: the reader's drop left the writer a DROPPED, which the sync write takes
;;     inline: 1.
;;   - sync-write-joined, sync-read-joined: the end is joined to a set, so the sync copy traps.
;;   - sync-write-no-reader, sync-read-no-writer: nothing else can complete the copy, so the sync wait is never woken.
;;     The export is async-typed, so the pin's sync tail does not judge it: both oracles hang.
;;   - sync-write-no-reader-sync-typed: the same in a sync-typed export, which the sync tail judges: both oracles trap.
;;
;; Every export but the last is async-typed and lifted sync. Measured on wasmtime 49.0.3 (`--invoke`, with the feature)
;; and on the pin (refread.py's future-sync rows), committed as future-sync.reading. Assembled with `wasm-tools parse
;; future-sync.wat -o future-sync.wasm` (wasm-tools 1.258.0). The memory is 1 1, so heap-backed.
(component
  (core module $mm (memory (export "m") 1 1))
  (core instance $mi (instantiate $mm))
  (alias core export $mi "m" (core memory $m))
  (type $f (future u8))
  (core func $fnew (canon future.new $f))
  (core func $fread (canon future.read $f async (memory $m)))
  (core func $fwrite (canon future.write $f async (memory $m)))
  (core func $sread (canon future.read $f (memory $m)))
  (core func $swrite (canon future.write $f (memory $m)))
  (core func $fdropr (canon future.drop-readable $f))
  (core func $wsnew (canon waitable-set.new))
  (core func $wsjoin (canon waitable.join))
  (core module $gm
    (import "" "mem" (memory 1 1))
    (import "" "fnew" (func $fnew (result i64)))
    (import "" "fread" (func $fread (param i32 i32) (result i32)))
    (import "" "fwrite" (func $fwrite (param i32 i32) (result i32)))
    (import "" "sread" (func $sread (param i32 i32) (result i32)))
    (import "" "swrite" (func $swrite (param i32 i32) (result i32)))
    (import "" "fdropr" (func $fdropr (param i32)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "wsjoin" (func $wsjoin (param i32 i32)))
    (global $r (mut i32) (i32.const 0))
    (global $w (mut i32) (i32.const 0))
    ;; 64: the write buffer (7)  128: the read buffer
    (func $split
      (local $s i64)
      (local.set $s (call $fnew))
      (global.set $r (i32.wrap_i64 (local.get $s)))
      (global.set $w (i32.wrap_i64 (i64.shr_u (local.get $s) (i64.const 32))))
      (i32.store8 (i32.const 64) (i32.const 7))
      (i32.store8 (i32.const 128) (i32.const 0)))
    (func $join (param $h i32)
      (call $wsjoin (local.get $h) (call $wsnew)))

    (func (export "sync-write-to-parked-read") (result i32)
      (call $split)
      (drop (call $fread (global.get $r) (i32.const 128)))
      (call $swrite (global.get $w) (i32.const 64)))
    (func (export "sync-read-of-parked-write") (result i32)
      (call $split)
      (drop (call $fwrite (global.get $w) (i32.const 64)))
      (i32.or (call $sread (global.get $r) (i32.const 128)) (i32.shl (i32.load8_u (i32.const 128)) (i32.const 8))))
    (func (export "sync-write-after-reader-dropped") (result i32)
      (call $split)
      (call $fdropr (global.get $r))
      (call $swrite (global.get $w) (i32.const 64)))
    (func (export "sync-write-joined") (result i32)
      (call $split)
      (call $join (global.get $w))
      (call $swrite (global.get $w) (i32.const 64)))
    (func (export "sync-read-joined") (result i32)
      (call $split)
      (call $join (global.get $r))
      (call $sread (global.get $r) (i32.const 128)))
    (func (export "sync-write-no-reader") (result i32)
      (call $split)
      (call $swrite (global.get $w) (i32.const 64)))
    (func (export "sync-read-no-writer") (result i32)
      (call $split)
      (call $sread (global.get $r) (i32.const 128))))
  (core instance $gi (instantiate $gm (with "" (instance (export "mem" (memory $m))
    (export "fnew" (func $fnew)) (export "fread" (func $fread)) (export "fwrite" (func $fwrite))
    (export "sread" (func $sread)) (export "swrite" (func $swrite)) (export "fdropr" (func $fdropr))
    (export "wsnew" (func $wsnew)) (export "wsjoin" (func $wsjoin))))))
  (type $at (func async (result u32)))
  (type $st (func (result u32)))
  (func (export "sync-write-to-parked-read") (type $at) (canon lift (core func $gi "sync-write-to-parked-read")))
  (func (export "sync-read-of-parked-write") (type $at) (canon lift (core func $gi "sync-read-of-parked-write")))
  (func (export "sync-write-after-reader-dropped") (type $at)
    (canon lift (core func $gi "sync-write-after-reader-dropped")))
  (func (export "sync-write-joined") (type $at) (canon lift (core func $gi "sync-write-joined")))
  (func (export "sync-read-joined") (type $at) (canon lift (core func $gi "sync-read-joined")))
  (func (export "sync-write-no-reader") (type $at) (canon lift (core func $gi "sync-write-no-reader")))
  (func (export "sync-read-no-writer") (type $at) (canon lift (core func $gi "sync-read-no-writer")))
  (func (export "sync-write-no-reader-sync-typed") (type $st) (canon lift (core func $gi "sync-write-no-reader"))))
