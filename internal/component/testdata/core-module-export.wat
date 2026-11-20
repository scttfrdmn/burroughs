;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; core-module-export: #1258 on the core side. A core module is exported, so the export is core module 1 and the
;; module after it is core module 2, which the core instance instantiates. Its `g` returns 7; core module 0's
;; returns 1. A walk that reads the instance's module as a position among the section's modules names module 2 of
;; two.
;; Assembled with `wasm-tools parse core-module-export.wat -o core-module-export.wasm` (wasm-tools 1.258.0).
(component
  (core module $m (func (export "g") (result i32) i32.const 1))
  (export $me "m" (core module $m))
  (core module $n (func (export "g") (result i32) i32.const 7))
  (core instance $i (instantiate 2))
  (func (export "g") (result u32) (canon lift (core func $i "g"))))
