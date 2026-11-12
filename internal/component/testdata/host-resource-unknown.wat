;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; host-resource-unknown: #1252, ADR 0118's identity rule. host-resource-synth's test interface, where the
;; guest declares `consume`'s argument as an own of `widget`, a resource the host does not implement. The
;; host's consume takes a thing out of the instance's table of host resources, where no widget can be; the
;; lower must be refused at instantiate.
;; Assembled with `wasm-tools parse host-resource-unknown.wat -o host-resource-unknown.wasm`
;; (wasm-tools 1.258.0).
(component
  (type $things (instance
    (export "widget" (type (sub resource)))
    (export "consume" (func (param "t" (own 0)) (result u32)))))
  (import "test:res/things@0.1.0" (instance $t (type $things)))
  (alias export $t "consume" (func $consume))
  (core func $consume-low (canon lower (func $consume)))
  (core module $m
    (import "host" "consume" (func (param i32) (result i32))))
  (core instance (instantiate $m (with "host" (instance (export "consume" (func $consume-low)))))))
