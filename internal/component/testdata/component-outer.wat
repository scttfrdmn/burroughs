;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; component-outer: #1258 on the component side. A middle component outer-aliases two of its parent's core modules
;; and then its parent's component `$leaf`, and instantiates that alias, which is its component 0. The alias is its
;; third alias, so a walk that reads every component-space entry as a nested component section entry indexes its
;; (empty) nested components at 2. `$leaf`'s `g` returns 7.
;; Assembled with `wasm-tools parse component-outer.wat -o component-outer.wasm` (wasm-tools 1.258.0).
(component
  (core module $a (func (export "g") (result i32) i32.const 1))
  (core module $b (func (export "g") (result i32) i32.const 2))
  (component $leaf
    (core module $m (func (export "g") (result i32) i32.const 7))
    (core instance $i (instantiate $m))
    (func (export "g") (result u32) (canon lift (core func $i "g"))))
  (component $mid
    (alias outer 1 $a (core module $x))
    (alias outer 1 $b (core module $y))
    (alias outer 1 $leaf (component $l))
    (instance $li (instantiate $l))
    (alias export $li "g" (func $g))
    (export "g" (func $g)))
  (instance $c (instantiate $mid))
  (alias export $c "g" (func $g))
  (export "g" (func $g)))
