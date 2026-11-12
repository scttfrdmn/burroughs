;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; host-resource-nested: #1252, ADR 0118's teardown across nested instances. host-resource-synth's `make`,
;; called from a nested component the outer one instantiates with its imported instance. The own `make`
;; returns is minted in the nested instance's own table (ADR 0117), so the outer instance's Close must reach
;; that table to destroy it. The outer component re-exports the nested `make`, which returns the handle as a
;; plain u32.
;; Assembled with `wasm-tools parse host-resource-nested.wat -o host-resource-nested.wasm` (wasm-tools 1.258.0).
(component
  (type $things (instance
    (export "thing" (type (sub resource)))
    (export "make" (func (result (own 0))))))
  (import "test:res/things@0.1.0" (instance $t (type $things)))
  (component $child
    (import "things" (instance $ct
      (export "thing" (type (sub resource)))
      (export "make" (func (result (own 0))))))
    (alias export $ct "make" (func $make))
    (core func $make-low (canon lower (func $make)))
    (core module $m
      (import "host" "make" (func $make (result i32)))
      (func (export "make") (result i32) (call $make)))
    (core instance $i (instantiate $m (with "host" (instance (export "make" (func $make-low))))))
    (func (export "make") (result u32) (canon lift (core func $i "make"))))
  (instance $c (instantiate $child (with "things" (instance $t))))
  (alias export $c "make" (func $cmake))
  (export "make" (func $cmake)))
