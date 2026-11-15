;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; flags-33-labels-type (#1247): a component declaring `flags` with 33 labels, one past the bound.
;; `wasm-tools parse` encodes it and `wasm-tools validate --features all` (1.258.0) refuses it: "cannot have
;; more than 32 flags (at offset 0xb)". The pin has no layout for it: alignment_flags and elem_size_flags
;; assert `0 < n <= 32`.
;; Assembled with `wasm-tools parse flags-33-labels-type.wat -o flags-33-labels-type.wasm`.
(component (type (flags "f1" "f2" "f3" "f4" "f5" "f6" "f7" "f8" "f9" "f10" "f11" "f12" "f13" "f14" "f15" "f16"
  "f17" "f18" "f19" "f20" "f21" "f22" "f23" "f24" "f25" "f26" "f27" "f28" "f29" "f30" "f31" "f32" "f33")))
