;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; sync-spill-string: #995. A parent SYNC-lowers a sibling's SYNC lift `get: func() -> string`. A string
;; flattens to two words, past MAX_FLAT_RESULTS (1), so the result spills. The child's core func returns a
;; pointer to (ptr, len) in the child's memory, and the parent's lowered core func takes a retptr and
;; returns nothing. The adapter must lift from one memory and lower into the other through the parent's
;; realloc. `run()` returns the stored length, 3. Burroughs passed the flat words through, so the call
;; failed on arity. The interim refusal (the chair's ruling of 2026-10-18) names the form instead.
;; Authored with `wasm-tools parse`; the reading is in sync-spill-string.reading.
(component
  (component $child
    (core module $cm
      (memory (export "m") 1)
      (data (i32.const 100) "abc")
      (data (i32.const 200) "\64\00\00\00\03\00\00\00") ;; (ptr 100, len 3) at 200
      (func (export "get") (result i32) (i32.const 200)))
    (core instance $ci (instantiate $cm))
    (alias core export $ci "m" (core memory $chm))
    (alias core export $ci "get" (core func $getf))
    (type $gt (func (result string)))
    (func (export "get") (type $gt) (canon lift (core func $getf) (memory $chm))))
  (instance $c (instantiate $child))
  (alias export $c "get" (func $get))

  (core module $memmod
    (memory (export "m") 1)
    (global $bump (mut i32) (i32.const 1024))
    (func (export "realloc") (param i32 i32 i32 i32) (result i32)
      (local $p i32)
      (local.set $p (global.get $bump))
      (global.set $bump (i32.add (global.get $bump) (local.get 3)))
      (local.get $p)))
  (core instance $memi (instantiate $memmod))
  (alias core export $memi "m" (core memory $pm))
  (alias core export $memi "realloc" (core func $prealloc))

  (core func $getlow (canon lower (func $get) (memory $pm) (realloc $prealloc)))
  (core module $runmod
    (import "" "get" (func $get (param i32)))
    (import "" "mem" (memory 1))
    ;; run: get() into retptr 16, then return the length stored at 20
    (func (export "run") (result i32)
      (call $get (i32.const 16))
      (i32.load (i32.const 20))))
  (core instance $ri (instantiate $runmod (with "" (instance (export "get" (func $getlow)) (export "mem" (memory $pm))))))
  (alias core export $ri "run" (core func $runf))
  (type $rt (func (result u32)))
  (func (export "run") (type $rt) (canon lift (core func $runf))))
