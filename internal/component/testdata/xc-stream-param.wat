;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; xc-stream-param: #1281. A parent SYNC-lowers its child's SYNC lift `take: func(s: stream<u8>) -> u32` and passes
;; the readable end of a stream it made. The child's `setup` makes a stream first, so the child's own readable end
;; is child index 1, and the parent's readable end is parent index 1. `take` drops the end it was given and returns
;; its index. The parent then writes one byte to its writable end. `run` returns
;; own*1000000 + r*10000 + x*100 + status: the child's own readable index, the parent's, the index the child was
;; handed, and the write's status (99 for BLOCKED).
;;
;; The reference moves the end (def:lift_async_value in the parent, def:lower_async_value in the child), so the
;; child is handed a fresh index, drops the parent's end, and the write finds its reader gone: DROPPED, 1.
;; Passing the word through hands the child index 1, its own end, which it drops; the parent's reader is never
;; moved, so the write parks: BLOCKED. Burroughs refuses a cross-component stream by name (#1281).
;; Assembled with `wasm-tools parse xc-stream-param.wat -o xc-stream-param.wasm` (wasm-tools 1.258.0); the
;; readings are in xc-handles.reading.
(component
  (component $child
    (type $u8s (stream u8))
    (core func $new (canon stream.new $u8s))
    (core func $dropr (canon stream.drop-readable $u8s))
    (core module $cm
      (import "" "new" (func $new (result i64)))
      (import "" "dropr" (func $dropr (param i32)))
      (global $own (mut i32) (i32.const 0))
      (func (export "setup") (result i32) (global.set $own (i32.wrap_i64 (call $new))) (global.get $own))
      (func (export "take") (param i32) (result i32) (call $dropr (local.get 0)) (local.get 0)))
    (core instance $ci (instantiate $cm (with "" (instance (export "new" (func $new)) (export "dropr" (func $dropr))))))
    (func (export "setup") (result u32) (canon lift (core func $ci "setup")))
    (type $tt (func (param "s" $u8s) (result u32)))
    (func (export "take") (type $tt) (canon lift (core func $ci "take"))))
  (instance $c (instantiate $child))
  (alias export $c "setup" (func $setup))
  (alias export $c "take" (func $take))
  (core func $setupl (canon lower (func $setup)))
  (core func $takel (canon lower (func $take)))
  (type $u8s (stream u8))
  (core module $mm (memory (export "m") 1))
  (core instance $mi (instantiate $mm))
  (alias core export $mi "m" (core memory $m))
  (core func $new (canon stream.new $u8s))
  (core func $write (canon stream.write $u8s async (memory $m)))
  (core module $pm
    (import "" "mem" (memory 1))
    (import "" "setup" (func $setup (result i32)))
    (import "" "take" (func $take (param i32) (result i32)))
    (import "" "new" (func $new (result i64)))
    (import "" "write" (func $write (param i32 i32 i32) (result i32)))
    (func (export "run") (result i32)
      (local $s i64) (local $r i32) (local $w i32) (local $own i32) (local $x i32) (local $st i32)
      (local.set $own (call $setup))
      (local.set $s (call $new))
      (local.set $r (i32.wrap_i64 (local.get $s)))
      (local.set $w (i32.wrap_i64 (i64.shr_u (local.get $s) (i64.const 32))))
      (local.set $x (call $take (local.get $r)))
      (local.set $st (call $write (local.get $w) (i32.const 0) (i32.const 1)))
      (if (i32.eq (local.get $st) (i32.const -1)) (then (local.set $st (i32.const 99))))
      (i32.add (i32.add (i32.mul (local.get $own) (i32.const 1000000)) (i32.mul (local.get $r) (i32.const 10000)))
        (i32.add (i32.mul (local.get $x) (i32.const 100)) (local.get $st)))))
  (core instance $pi (instantiate $pm (with "" (instance
    (export "mem" (memory $m)) (export "setup" (func $setupl)) (export "take" (func $takel))
    (export "new" (func $new)) (export "write" (func $write))))))
  (type $rt (func (result u32)))
  (func (export "run") (type $rt) (canon lift (core func $pi "run"))))
