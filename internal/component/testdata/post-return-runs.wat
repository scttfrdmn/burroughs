;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; post-return-runs: #1058. A parent sync-lowers a child's sync `get() -> u32`, whose `post-return` bumps a
;; counter, then reads the counter through the child's second export. `run()` returns 100 if `get` returned 42,
;; plus the counter: 101 when `post-return` ran once after the call, as the reference's canon_lift runs it.
;; Burroughs read 100, because it decoded the canonopt and never ran it.
;; Authored with `wasm-tools parse`; the reading is in post-return-runs.reading.
(component
  (component $child
    (core module $cm
      (global $n (mut i32) (i32.const 0))
      (func (export "get") (result i32) (i32.const 42))
      ;; post-return: runs after get's result is lifted; counts its runs
      (func (export "get-post") (param i32) (global.set $n (i32.add (global.get $n) (i32.const 1))))
      (func (export "count") (result i32) (global.get $n)))
    (core instance $ci (instantiate $cm))
    (alias core export $ci "get" (core func $getf))
    (alias core export $ci "get-post" (core func $postf))
    (alias core export $ci "count" (core func $countf))
    (type $u (func (result u32)))
    (func (export "get") (type $u) (canon lift (core func $getf) (post-return $postf)))
    (func (export "count") (type $u) (canon lift (core func $countf))))
  (instance $c (instantiate $child))
  (alias export $c "get" (func $get))
  (alias export $c "count" (func $count))
  (core func $getlow (canon lower (func $get)))
  (core func $countlow (canon lower (func $count)))
  (core module $runmod
    (import "" "get" (func $get (result i32)))
    (import "" "count" (func $count (result i32)))
    ;; run: get() once, then return how many times get's post-return ran, plus 100*get's value check
    (func (export "run") (result i32)
      (local $v i32)
      (local.set $v (call $get))
      (i32.add (i32.mul (i32.eq (local.get $v) (i32.const 42)) (i32.const 100)) (call $count))))
  (core instance $ri (instantiate $runmod (with "" (instance (export "get" (func $getlow)) (export "count" (func $countlow))))))
  (alias core export $ri "run" (core func $runf))
  (type $rt (func (result u32)))
  (func (export "run") (type $rt) (canon lift (core func $runf))))
