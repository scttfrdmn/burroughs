;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; component-outer-scope: #1258 on the component side, the scope an outer alias names. `$leaf` is defined at the
;; top and outer-aliases the top's core module 1, whose `g` returns 7. A middle component, with core modules of its
;; own whose module 1 returns 5, outer-aliases `$leaf` and instantiates it. `alias outer 1` inside `$leaf` names the
;; component that defines `$leaf`, the top, and not the one instantiating it, so `g` is 7, not 5.
;; Assembled with `wasm-tools parse component-outer-scope.wat -o component-outer-scope.wasm` (wasm-tools 1.258.0).
(component
  (core module $a (func (export "g") (result i32) i32.const 1))
  (core module $b (func (export "g") (result i32) i32.const 7))
  (component $leaf
    (alias outer 1 $b (core module $lb))
    (core instance $i (instantiate $lb))
    (func (export "g") (result u32) (canon lift (core func $i "g"))))
  (component $mid
    (core module $c (func (export "g") (result i32) i32.const 3))
    (core module $d (func (export "g") (result i32) i32.const 5))
    (alias outer 1 $leaf (component $l))
    (instance $li (instantiate $l))
    (alias export $li "g" (func $g))
    (export "g" (func $g)))
  (instance $m (instantiate $mid))
  (alias export $m "g" (func $g))
  (export "g" (func $g)))
