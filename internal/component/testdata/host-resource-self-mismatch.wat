;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; host-resource-self-mismatch: #1252, ADR 0118's method rule. host-resource-synth's test interface, whose
;; `thing` the guest declares as another interface's resource: `(export "thing" (type (eq $gadget)))`, with
;; `$gadget` aliased from `test:res/gadgets`. So `[method]thing.poke`'s self is a `borrow<gadget>`. The host
;; implements `thing` and `gadget` as two types and its poke lends a thing, so every call would trap; the
;; lower must be refused at instantiate.
;;
;; The self is reached through an eq bound because wasm-tools refuses the direct spelling: a `[method]R.m`
;; whose self is a borrow of another exported resource is *"not valid … function does not match expected
;; resource name"*. Through the eq bound the exported `thing` is that resource, and wasm-tools validates it.
;; Assembled with `wasm-tools parse host-resource-self-mismatch.wat -o host-resource-self-mismatch.wasm`
;; (wasm-tools 1.258.0).
(component
  (type $gadgets (instance (export "gadget" (type (sub resource)))))
  (import "test:res/gadgets@0.1.0" (instance $g (type $gadgets)))
  (alias export $g "gadget" (type $gadget))
  (type $things (instance
    (alias outer 1 $gadget (type $ga))
    (export "thing" (type $th (eq $ga)))
    (export "[method]thing.poke" (func (param "self" (borrow $th)) (result u32)))))
  (import "test:res/things@0.1.0" (instance $t (type $things)))
  (alias export $t "[method]thing.poke" (func $poke))
  (core func $poke-low (canon lower (func $poke)))
  (core module $m
    (import "host" "poke" (func (param i32) (result i32))))
  (core instance (instantiate $m (with "host" (instance (export "poke" (func $poke-low)))))))
