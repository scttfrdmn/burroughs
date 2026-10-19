;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; adapter-copy: SP-1's adapter-copy fixture (ADR 0104, reading (a)'s second half). It is string-echo.wat's
;; sync arm with 64 MiB memories, so `run-sync(s)` can carry a string of many MiB through ADR 0103's sync
;; adapter. A child's `echo-sync` returns its argument as a spilled result; the parent's `run-sync(s)` passes
;; `s` to it and returns the echoed string's length, which reads len(s) on wasmtime.
;;
;; Each call copies the string four times in host Go code, on the caller's thread: lifted from the parent's
;; memory, lowered into the child's, lifted back from the child's, and lowered into the parent's. A `Stop`
;; requested during one of those copies waits for it unless the copy polls.
(component
  (component $child
    (core module $cm
      (memory (export "m") 1024 1024)
      (global $bump (mut i32) (i32.const 1024))
      (func (export "realloc") (param i32 i32 i32 i32) (result i32)
        (local $p i32)
        ;; wrap rather than run off the memory: each call's value is consumed before the next
        (if (i32.gt_u (i32.add (global.get $bump) (local.get 3)) (i32.const 62914560)) (then (global.set $bump (i32.const 1024))))
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
    (type $st (func (param "s" string) (result string)))
    (func (export "echo-sync") (type $st) (canon lift (core func $echosf) (memory $chm) (realloc $crealloc))))
  (instance $c (instantiate $child))
  (alias export $c "echo-sync" (func $echos))
  (core module $pm
    (memory (export "m") 1024 1024)
    (global $bump (mut i32) (i32.const 1024))
    (func (export "realloc") (param i32 i32 i32 i32) (result i32)
      (local $p i32)
      (if (i32.gt_u (i32.add (global.get $bump) (local.get 3)) (i32.const 62914560)) (then (global.set $bump (i32.const 1024))))
      (local.set $p (global.get $bump))
      (global.set $bump (i32.add (global.get $bump) (local.get 3))) (local.get $p)))
  (core instance $pmi (instantiate $pm))
  (alias core export $pmi "m" (core memory $pmem))
  (alias core export $pmi "realloc" (core func $prealloc))
  (core func $echoslow (canon lower (func $echos) (memory $pmem) (realloc $prealloc)))
  (core module $rm
    (import "" "echos" (func $echos (param i32 i32 i32)))
    (import "" "m" (memory 1024 1024))
    (func (export "run-sync") (param i32 i32) (result i32)
      (call $echos (local.get 0) (local.get 1) (i32.const 8))
      (i32.load (i32.const 12))))
  (core instance $ri (instantiate $rm (with "" (instance (export "echos" (func $echoslow)) (export "m" (memory $pmem))))))
  (alias core export $ri "run-sync" (core func $runsf))
  (type $rt (func (param "s" string) (result u32)))
  (func (export "run-sync") (type $rt) (canon lift (core func $runsf) (memory $pmem) (realloc $prealloc))))
