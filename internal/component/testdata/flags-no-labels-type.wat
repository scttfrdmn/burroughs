;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; flags-no-labels-type (#1247): a component declaring `flags` with no labels. `wasm-tools parse` encodes it
;; and `wasm-tools validate --features all` (1.258.0) refuses it: "flags must have at least one entry (at
;; offset 0xb)". The pin has no layout for it: alignment_flags and elem_size_flags assert `0 < n <= 32`.
;; Assembled with `wasm-tools parse flags-no-labels-type.wat -o flags-no-labels-type.wasm`.
(component (type (flags)))
