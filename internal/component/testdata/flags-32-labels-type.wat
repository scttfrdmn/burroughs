;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; flags-32-labels-type (#1247): the bound's accept side, `flags` with exactly 32 labels, which
;; `wasm-tools validate --features all` (1.258.0) accepts. The anti-vacuity half of the flags-bound witness:
;; a decoder that refused every flags type would pass the refusal rows and fail this one.
;; Assembled with `wasm-tools parse flags-32-labels-type.wat -o flags-32-labels-type.wasm`.
(component (type (flags "f1" "f2" "f3" "f4" "f5" "f6" "f7" "f8" "f9" "f10" "f11" "f12" "f13" "f14" "f15" "f16"
  "f17" "f18" "f19" "f20" "f21" "f22" "f23" "f24" "f25" "f26" "f27" "f28" "f29" "f30" "f31" "f32")))
