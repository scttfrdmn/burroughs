;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; host-resource-synth: #1231 and #1137's R-4, ADR 0117. A guest over a host-defined resource, `thing`, in a
;; test interface the host implements: `make` returns an own<thing>, `[method]thing.poke` borrows one, `consume`
;; takes one, and the guest drops one with `canon resource.drop`. Each export passes a handle through as a plain
;; u32, so a test can carry an index minted in one instance to another.
;; Assembled with `wasm-tools parse host-resource-synth.wat -o host-resource-synth.wasm` (wasm-tools 1.258.0).
(component
  (type $things (instance
    (export "thing" (type (sub resource)))
    (export "make" (func (result (own 0))))
    (export "[method]thing.poke" (func (param "self" (borrow 0)) (result u32)))
    (export "consume" (func (param "t" (own 0)) (result u32)))))
  (import "test:res/things@0.1.0" (instance $t (type $things)))
  (alias export $t "thing" (type $thing))
  (alias export $t "make" (func $make))
  (alias export $t "[method]thing.poke" (func $poke))
  (alias export $t "consume" (func $consume))
  (core func $make-low (canon lower (func $make)))
  (core func $poke-low (canon lower (func $poke)))
  (core func $consume-low (canon lower (func $consume)))
  (core func $drop-low (canon resource.drop $thing))
  (core instance $imports
    (export "make" (func $make-low))
    (export "poke" (func $poke-low))
    (export "consume" (func $consume-low))
    (export "drop" (func $drop-low)))
  (core module $m
    (import "host" "make" (func $make (result i32)))
    (import "host" "poke" (func $poke (param i32) (result i32)))
    (import "host" "consume" (func $consume (param i32) (result i32)))
    (import "host" "drop" (func $drop (param i32)))
    (func (export "make") (result i32) (call $make))
    (func (export "poke") (param i32) (result i32) (call $poke (local.get 0)))
    (func (export "consume") (param i32) (result i32) (call $consume (local.get 0)))
    (func (export "drop") (param i32) (call $drop (local.get 0))))
  (core instance $i (instantiate $m (with "host" (instance $imports))))
  (func (export "make") (result u32) (canon lift (core func $i "make")))
  (func (export "poke") (param "h" u32) (result u32) (canon lift (core func $i "poke")))
  (func (export "consume") (param "h" u32) (result u32) (canon lift (core func $i "consume")))
  (func (export "drop") (param "h" u32) (canon lift (core func $i "drop"))))
