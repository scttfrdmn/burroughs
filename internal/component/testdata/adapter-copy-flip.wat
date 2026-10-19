;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; adapter-copy-flip: SP-1's adapter-copy witness fixture (ADR 0104). A child echoes a string or a list<u8> back
;; as a spilled result, but first overwrites the argument's bytes with a fill byte that cycles through 'a', 'b'
;; and 'c', one per call. The parent returns the echoed value's length. On wasmtime 49.0.3,
;; `run-sync("hello")` reads 5 and `run-list([1, 2, 3])` reads 3.
;;
;; **Why the fill.** The adapter writes each result into the parent's memory: ADR 0103's step 4,
;; `canon_lower`'s `on_resolve`. The parent's bump allocator puts successive results of n bytes at 1024 and at
;; 1024+n, alternately, and wraps. So each write overwrites a region that holds a different fill byte. A stop
;; that parks the caller's agent inside that write leaves a region holding two bytes: the new one up to where
;; the copy stopped, the old one after. Only a poll inside the copy can park the agent there. The adapter's
;; other safepoints are frame entries, which are before or after a copy and never inside one.
(component
  (component $child
    (core module $cm
      (memory (export "m") 1024 1024)
      (global $bump (mut i32) (i32.const 1024))
      (global $calls (mut i32) (i32.const 0))
      (func (export "realloc") (param i32 i32 i32 i32) (result i32)
        (local $p i32)
        ;; wrap rather than run off the memory: each call's value is consumed before the next
        (if (i32.gt_u (i32.add (global.get $bump) (local.get 3)) (i32.const 62914560)) (then (global.set $bump (i32.const 1024))))
        (local.set $p (global.get $bump))
        (global.set $bump (i32.add (global.get $bump) (local.get 3))) (local.get $p))
      ;; echo: fill the argument with 'a' + calls%3, then return a pointer to (ptr, len) at 16
      (func (export "echo") (param i32 i32) (result i32)
        (memory.fill (local.get 0)
          (i32.add (i32.const 97) (i32.rem_u (global.get $calls) (i32.const 3)))
          (local.get 1))
        (global.set $calls (i32.add (global.get $calls) (i32.const 1)))
        (i32.store (i32.const 16) (local.get 0))
        (i32.store (i32.const 20) (local.get 1))
        (i32.const 16)))
    (core instance $ci (instantiate $cm))
    (alias core export $ci "m" (core memory $chm))
    (alias core export $ci "realloc" (core func $crealloc))
    (alias core export $ci "echo" (core func $echof))
    (type $st (func (param "s" string) (result string)))
    (type $lt (func (param "l" (list u8)) (result (list u8))))
    (func (export "echo-sync") (type $st) (canon lift (core func $echof) (memory $chm) (realloc $crealloc)))
    (func (export "echo-list") (type $lt) (canon lift (core func $echof) (memory $chm) (realloc $crealloc))))
  (instance $c (instantiate $child))
  (alias export $c "echo-sync" (func $echos))
  (alias export $c "echo-list" (func $echol))
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
  (core func $echollow (canon lower (func $echol) (memory $pmem) (realloc $prealloc)))
  (core module $rm
    (import "" "echos" (func $echos (param i32 i32 i32)))
    (import "" "echol" (func $echol (param i32 i32 i32)))
    (import "" "m" (memory 1024 1024))
    (func (export "run-sync") (param i32 i32) (result i32)
      (call $echos (local.get 0) (local.get 1) (i32.const 8))
      (i32.load (i32.const 12)))
    (func (export "run-list") (param i32 i32) (result i32)
      (call $echol (local.get 0) (local.get 1) (i32.const 8))
      (i32.load (i32.const 12))))
  (core instance $ri (instantiate $rm (with "" (instance (export "echos" (func $echoslow)) (export "echol" (func $echollow)) (export "m" (memory $pmem))))))
  (alias core export $ri "run-sync" (core func $runsf))
  (alias core export $ri "run-list" (core func $runlf))
  (type $rst (func (param "s" string) (result u32)))
  (type $rlt (func (param "l" (list u8)) (result u32)))
  (func (export "run-sync") (type $rst) (canon lift (core func $runsf) (memory $pmem) (realloc $prealloc)))
  (func (export "run-list") (type $rlt) (canon lift (core func $runlf) (memory $pmem) (realloc $prealloc))))
