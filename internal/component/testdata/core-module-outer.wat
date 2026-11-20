;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; core-module-outer: #1258 on the core side. A nested component aliases its parent's core module with
;; `alias outer 1`, after a module of its own, so the alias is the child's core module 1 and names the parent's
;; core module 0, whose `g` returns 7. The child's own module returns 1. A walk that reads the child's instance's
;; module as a position among the child's section modules names module 1 of one.
;; Assembled with `wasm-tools parse core-module-outer.wat -o core-module-outer.wasm` (wasm-tools 1.258.0).
(component
  (core module $m (func (export "g") (result i32) i32.const 7))
  (component $child
    (core module $z (func (export "g") (result i32) i32.const 1))
    (alias outer 1 $m (core module $om))
    (core instance $i (instantiate $om))
    (func (export "g") (result u32) (canon lift (core func $i "g"))))
  (instance $c (instantiate $child))
  (alias export $c "g" (func $g))
  (export "g" (func $g)))
