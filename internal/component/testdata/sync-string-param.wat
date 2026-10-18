;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; sync-string-param: #995's silent face. A parent SYNC-lowers a sibling's SYNC lift `first: func(s: string) -> u32`,
;; which returns its argument's first byte. A string is two flat words, so nothing spills, but the words are a
;; pointer into the CALLER's memory. The parent passes "abc" at its own 100, and the child's 100 holds "xyz". The
;; reference lowers the string into the child's memory through the child's realloc, so `run()` reads 97 ('a').
;; Burroughs passed the words through, and the child read its own memory at the caller's address: 120 ('x').
;; Authored with `wasm-tools parse`; the reading is in sync-spill-string.reading.
(component
  (component $child
    (core module $cm
      (memory (export "m") 1)
      (data (i32.const 100) "xyz")
      (global $bump (mut i32) (i32.const 1024))
      (func (export "realloc") (param i32 i32 i32 i32) (result i32)
        (local $p i32) (local.set $p (global.get $bump))
        (global.set $bump (i32.add (global.get $bump) (local.get 3))) (local.get $p))
      ;; first: the first byte of its string argument, read from the CHILD's memory
      (func (export "first") (param i32 i32) (result i32) (i32.load8_u (local.get 0))))
    (core instance $ci (instantiate $cm))
    (alias core export $ci "m" (core memory $chm))
    (alias core export $ci "realloc" (core func $crealloc))
    (alias core export $ci "first" (core func $firstf))
    (type $ft (func (param "s" string) (result u32)))
    (func (export "first") (type $ft) (canon lift (core func $firstf) (memory $chm) (realloc $crealloc))))
  (instance $c (instantiate $child))
  (alias export $c "first" (func $first))
  (core module $memmod (memory (export "m") 1) (data (i32.const 100) "abc"))
  (core instance $memi (instantiate $memmod))
  (alias core export $memi "m" (core memory $pm))
  (core func $firstlow (canon lower (func $first) (memory $pm)))
  (core module $runmod
    (import "" "first" (func $first (param i32 i32) (result i32)))
    ;; run: pass "abc" at the PARENT's 100; the child's 100 holds "xyz"
    (func (export "run") (result i32) (call $first (i32.const 100) (i32.const 3))))
  (core instance $ri (instantiate $runmod (with "" (instance (export "first" (func $firstlow))))))
  (alias core export $ri "run" (core func $runf))
  (type $rt (func (result u32)))
  (func (export "run") (type $rt) (canon lift (core func $runf))))
