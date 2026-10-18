;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; realloc-wait-late: #994's subject, a cross-component result store that fails AFTER the lower returned.
;; It is realloc-wait.wat's `res()` with one change: the child YIELDs before `task.return`, so its first
;; segment ends unresolved, the parent's async lower returns STARTED, and the store runs later, when the
;; child resolves. The store lowers `get()`'s string into the parent's memory through the parent's
;; realloc, `rwait`, which calls `waitable-set.wait`. A realloc runs with may_leave = False
;; (LiftLowerContext.reallocate), so the reference traps there, inside the store. The parent is WAITing on
;; a set holding the subtask, so a store whose failure has no reader leaves that wait waiting forever.
;;
;; Authored with `wasm-tools parse`; the reading is in realloc-wait-late.reading.
(component
  (component $child
    (core module $cmem (memory (export "m") 1 1) (data (i32.const 100) "abc"))
    (core instance $cmemi (instantiate $cmem))
    (alias core export $cmemi "m" (core memory $chm))
    (core func $ret (canon task.return (result string) (memory $chm)))
    (core module $cm
      (import "" "ret" (func $ret (param i32 i32)))
      ;; get: YIELD first (packed code 1), so the first segment ends unresolved
      (func (export "get") (result i32) (i32.const 1))
      ;; cb: on the re-entry, return "abc" at 100 and EXIT
      (func (export "cb") (param i32 i32 i32) (result i32) (call $ret (i32.const 100) (i32.const 3)) (i32.const 0)))
    (core instance $ci (instantiate $cm (with "" (instance (export "ret" (func $ret))))))
    (alias core export $ci "get" (core func $getf))
    (alias core export $ci "cb" (core func $cbf))
    (type $gt (func async (result string)))
    (func (export "get") (type $gt) (canon lift (core func $getf) async (callback $cbf) (memory $chm)))
  )
  (instance $c (instantiate $child))
  (alias export $c "get" (func $get))

  (core module $memmod (memory (export "m") 1 1))
  (core instance $memi (instantiate $memmod))
  (alias core export $memi "m" (core memory $pm))

  (core func $wswait (canon waitable-set.wait (memory $pm)))
  (core module $allocmod
    (import "" "wswait" (func $wswait (param i32 i32) (result i32)))
    (global $bump (mut i32) (i32.const 1024))
    ;; rwait: waits on set 0 before allocating; the reference traps at the wait's first line
    (func (export "rwait") (param i32 i32 i32 i32) (result i32)
      (local $p i32)
      (drop (call $wswait (i32.const 0) (i32.const 64)))
      (local.set $p (global.get $bump))
      (global.set $bump (i32.add (global.get $bump) (local.get 3)))
      (local.get $p)))
  (core instance $alloci (instantiate $allocmod (with "" (instance (export "wswait" (func $wswait))))))
  (alias core export $alloci "rwait" (core func $rwait))

  (core func $getlow (canon lower (func $get) async (memory $pm) (realloc $rwait)))
  (core func $ret (canon task.return (result u32)))
  (core func $wsnew (canon waitable-set.new))
  (core func $join (canon waitable.join))
  (core module $runmod
    (import "" "get" (func $get (param i32) (result i32)))
    (import "" "ret" (func $ret (param i32)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "join" (func $join (param i32 i32)))
    (import "" "mem" (memory 1 1))
    ;; res: get() into retptr 16. It returns STARTED, so join the subtask to a new set and WAIT on it.
    (func (export "res") (result i32)
      (local $sub i32) (local $set i32)
      (local.set $sub (i32.shr_u (call $get (i32.const 16)) (i32.const 4)))
      (local.set $set (call $wsnew))
      (call $join (local.get $sub) (local.get $set))
      (i32.or (i32.const 2) (i32.shl (local.get $set) (i32.const 4))))
    ;; cb: the subtask's event arrived, so the result is stored; return its length and EXIT
    (func (export "cb") (param i32 i32 i32) (result i32)
      (call $ret (i32.load (i32.const 20)))
      (i32.const 0)))
  (core instance $ri (instantiate $runmod
    (with "" (instance (export "get" (func $getlow)) (export "ret" (func $ret)) (export "wsnew" (func $wsnew))
      (export "join" (func $join)) (export "mem" (memory $pm))))))
  (alias core export $ri "res" (core func $resf))
  (alias core export $ri "cb" (core func $rcbf))
  (type $rt (func async (result u32)))
  (func (export "res") (type $rt) (canon lift (core func $resf) async (callback $rcbf)))
)
