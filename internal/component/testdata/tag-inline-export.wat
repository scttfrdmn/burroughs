;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; tag-inline-export: #1258's core tag space, the valid control. tag-as-instance's component, but the second core
;; instance is given the aliased tag through an inline-export instance, so a tag alias still resolves once tags are
;; their own space.
;; Assembled with `wasm-tools parse tag-inline-export.wat -o tag-inline-export.wasm` (wasm-tools 1.258.0).
(component
  (core module $m (tag (export "t")))
  (core instance $i (instantiate $m))
  (alias core export $i "t" (core tag $t))
  (core module $n (import "x" "t" (tag)))
  (core instance $j (instantiate $n (with "x" (instance (export "t" (tag $t)))))))
