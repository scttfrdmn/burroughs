;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; string-echo: ADR 0103's cost fixture. A child echoes a string two ways: `echo-sync`, a SYNC lift whose
;; spilled result goes through the adapter, and `echo-async`, a callback lift returning it through
;; `task.return`, the way such a value crossed before the adapter. The parent's `run-sync(s)` and `run-async(s)`
;; each pass `s` to one of them and return the echoed string's length. Both read len(s) on wasmtime.
;; `BenchmarkSyncStringRoundTrip` times the two arms side by side.
(component
  (component $child
    (core module $cm
      (memory (export "m") 1)
      (global $bump (mut i32) (i32.const 1024))
      (func (export "realloc") (param i32 i32 i32 i32) (result i32)
        (local $p i32)
        ;; wrap rather than run off the page: a benchmark makes thousands of calls, and each call's value
        ;; is consumed before the next, so reusing the space is safe
        (if (i32.gt_u (i32.add (global.get $bump) (local.get 3)) (i32.const 60000)) (then (global.set $bump (i32.const 1024))))
        (local.set $p (global.get $bump))
        (global.set $bump (i32.add (global.get $bump) (local.get 3))) (local.get $p))
      ;; echo-sync: the argument is already in this memory; return a pointer to (ptr, len) at 16
      (func (export "echo-sync") (param i32 i32) (result i32)
        (i32.store (i32.const 16) (local.get 0))
        (i32.store (i32.const 20) (local.get 1))
        (i32.const 16)))
    (core instance $ci (instantiate $cm))
    (alias core export $ci "m" (core memory $chm))
    (alias core export $ci "realloc" (core func $crealloc))
    (alias core export $ci "echo-sync" (core func $echosf))
    (core func $ret (canon task.return (result string) (memory $chm)))
    (core module $am
      (import "" "ret" (func $ret (param i32 i32)))
      (func (export "echo-async") (param i32 i32) (result i32) (call $ret (local.get 0) (local.get 1)) (i32.const 0))
      (func (export "cb") (param i32 i32 i32) (result i32) unreachable))
    (core instance $ai (instantiate $am (with "" (instance (export "ret" (func $ret))))))
    (alias core export $ai "echo-async" (core func $echoaf))
    (alias core export $ai "cb" (core func $cbf))
    (type $st (func (param "s" string) (result string)))
    (type $at (func async (param "s" string) (result string)))
    (func (export "echo-sync") (type $st) (canon lift (core func $echosf) (memory $chm) (realloc $crealloc)))
    (func (export "echo-async") (type $at) (canon lift (core func $echoaf) async (callback $cbf) (memory $chm) (realloc $crealloc))))
  (instance $c (instantiate $child))
  (alias export $c "echo-sync" (func $echos))
  (alias export $c "echo-async" (func $echoa))
  (core module $pm
    (memory (export "m") 1)
    (global $bump (mut i32) (i32.const 1024))
    (func (export "realloc") (param i32 i32 i32 i32) (result i32)
      (local $p i32)
      (if (i32.gt_u (i32.add (global.get $bump) (local.get 3)) (i32.const 60000)) (then (global.set $bump (i32.const 1024))))
      (local.set $p (global.get $bump))
      (global.set $bump (i32.add (global.get $bump) (local.get 3))) (local.get $p)))
  (core instance $pmi (instantiate $pm))
  (alias core export $pmi "m" (core memory $pmem))
  (alias core export $pmi "realloc" (core func $prealloc))
  (core func $echoslow (canon lower (func $echos) (memory $pmem) (realloc $prealloc)))
  (core func $echoalow (canon lower (func $echoa) async (memory $pmem) (realloc $prealloc)))
  (core module $rm
    (import "" "echos" (func $echos (param i32 i32 i32)))
    (import "" "echoa" (func $echoa (param i32 i32 i32) (result i32)))
    (import "" "m" (memory 1))
    (func (export "run-sync") (param i32 i32) (result i32)
      (call $echos (local.get 0) (local.get 1) (i32.const 8))
      (i32.load (i32.const 12)))
    (func (export "run-async") (param i32 i32) (result i32)
      (drop (call $echoa (local.get 0) (local.get 1) (i32.const 8)))
      (i32.load (i32.const 12))))
  (core instance $ri (instantiate $rm (with "" (instance (export "echos" (func $echoslow)) (export "echoa" (func $echoalow)) (export "m" (memory $pmem))))))
  (alias core export $ri "run-sync" (core func $runsf))
  (alias core export $ri "run-async" (core func $runaf))
  (type $rt (func (param "s" string) (result u32)))
  (func (export "run-sync") (type $rt) (canon lift (core func $runsf) (memory $pmem) (realloc $prealloc)))
  (func (export "run-async") (type $rt) (canon lift (core func $runaf) (memory $pmem) (realloc $prealloc))))
