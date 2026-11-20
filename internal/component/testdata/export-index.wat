;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; export-index: #1258. An export introduces a new index in its sort (Binary.md: "All exports (of all `sort`s)
;; introduce a new index that aliases the exported definition"). Here a type is exported and then used, as
;; wit-component emits an exported resource type: `t` is exported as type 1, `$f` uses it and is type 2, and `$g` is
;; type 3. A loader that gives the export no index puts `$f` at 1 and `$g` at 2, so `inc`'s lift, which names type
;; 2, gets `$g`'s `func() -> u32`, and `k`'s, which names type 3, gets nothing. The func sort is the same: `inc` is
;; exported as func 2, and `inc2` re-exports that index, which names past the end of a func space that gave the
;; export no entry.
;; Assembled with `wasm-tools parse export-index.wat -o export-index.wasm` (wasm-tools 1.258.0).
(component
  (type $t u32)
  (export $te "t" (type $t))
  (type $f (func (param "x" $te) (result $te)))
  (type $g (func (result u32)))
  (core module $m
    (func (export "inc") (param i32) (result i32) local.get 0 i32.const 1 i32.add)
    (func (export "k") (result i32) i32.const 42))
  (core instance $i (instantiate $m))
  (func $inc (type $f) (canon lift (core func $i "inc")))
  (func $k (type $g) (canon lift (core func $i "k")))
  (export $inc-e "inc" (func $inc))
  (export "k" (func $k))
  (export "inc2" (func $inc-e)))
