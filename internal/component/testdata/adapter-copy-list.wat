;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; adapter-copy-list: SP-1's list-lift fixture (ADR 0104, the chair's third ruling of 2026-10-18, item 2). It is
;; adapter-copy.wat with lists in place of the string. A child echoes its argument back as a spilled result, two
;; ways, and the parent returns the echoed list's length:
;;   - `run-list(l: list<u8>)` crosses a scalar list, which the adapter can move as bytes;
;;   - `run-strs(l: list<string>)` crosses a non-scalar list, which it must build element by element.
;; On wasmtime 49.0.3, `run-list([1, 2, 3, 4, 5])` reads 5 and `run-strs(["a", "bc"])` reads 2.
(component
  (component $child
    (core module $cm
      (memory (export "m") 1024 1024)
      (global $bump (mut i32) (i32.const 1024))
      (func (export "realloc") (param i32 i32 i32 i32) (result i32)
        (local $p i32)
        ;; align up, then wrap rather than run off the memory: each call's value is consumed before the next
        (global.set $bump (i32.and (i32.add (global.get $bump) (i32.const 7)) (i32.const -8)))
        (if (i32.gt_u (i32.add (global.get $bump) (local.get 3)) (i32.const 62914560)) (then (global.set $bump (i32.const 1024))))
        (local.set $p (global.get $bump))
        (global.set $bump (i32.add (global.get $bump) (local.get 3))) (local.get $p))
      ;; both echoes: the argument is already in this memory; return a pointer to (ptr, len) at 16
      (func (export "echo") (param i32 i32) (result i32)
        (i32.store (i32.const 16) (local.get 0))
        (i32.store (i32.const 20) (local.get 1))
        (i32.const 16)))
    (core instance $ci (instantiate $cm))
    (alias core export $ci "m" (core memory $chm))
    (alias core export $ci "realloc" (core func $crealloc))
    (alias core export $ci "echo" (core func $echof))
    (type $lt (func (param "l" (list u8)) (result (list u8))))
    (type $st (func (param "l" (list string)) (result (list string))))
    (func (export "echo-list") (type $lt) (canon lift (core func $echof) (memory $chm) (realloc $crealloc)))
    (func (export "echo-strs") (type $st) (canon lift (core func $echof) (memory $chm) (realloc $crealloc))))
  (instance $c (instantiate $child))
  (alias export $c "echo-list" (func $echol))
  (alias export $c "echo-strs" (func $echos))
  (core module $pm
    (memory (export "m") 1024 1024)
    (global $bump (mut i32) (i32.const 1024))
    (func (export "realloc") (param i32 i32 i32 i32) (result i32)
      (local $p i32)
      (global.set $bump (i32.and (i32.add (global.get $bump) (i32.const 7)) (i32.const -8)))
      (if (i32.gt_u (i32.add (global.get $bump) (local.get 3)) (i32.const 62914560)) (then (global.set $bump (i32.const 1024))))
      (local.set $p (global.get $bump))
      (global.set $bump (i32.add (global.get $bump) (local.get 3))) (local.get $p)))
  (core instance $pmi (instantiate $pm))
  (alias core export $pmi "m" (core memory $pmem))
  (alias core export $pmi "realloc" (core func $prealloc))
  (core func $echollow (canon lower (func $echol) (memory $pmem) (realloc $prealloc)))
  (core func $echoslow (canon lower (func $echos) (memory $pmem) (realloc $prealloc)))
  (core module $rm
    (import "" "echol" (func $echol (param i32 i32 i32)))
    (import "" "echos" (func $echos (param i32 i32 i32)))
    (import "" "m" (memory 1024 1024))
    (func (export "run-list") (param i32 i32) (result i32)
      (call $echol (local.get 0) (local.get 1) (i32.const 8))
      (i32.load (i32.const 12)))
    (func (export "run-strs") (param i32 i32) (result i32)
      (call $echos (local.get 0) (local.get 1) (i32.const 8))
      (i32.load (i32.const 12))))
  (core instance $ri (instantiate $rm (with "" (instance (export "echol" (func $echollow)) (export "echos" (func $echoslow)) (export "m" (memory $pmem))))))
  (alias core export $ri "run-list" (core func $runlf))
  (alias core export $ri "run-strs" (core func $runsf))
  (type $rt (func (param "l" (list u8)) (result u32)))
  (type $rst (func (param "l" (list string)) (result u32)))
  (func (export "run-list") (type $rt) (canon lift (core func $runlf) (memory $pmem) (realloc $prealloc)))
  (func (export "run-strs") (type $rst) (canon lift (core func $runsf) (memory $pmem) (realloc $prealloc))))
