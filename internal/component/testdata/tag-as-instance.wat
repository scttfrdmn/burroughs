;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; tag-as-instance: #1258's core tag space, invalid on purpose. A core instance exports a tag, which is aliased, and
;; a second core instance is instantiated with `(instance 1)`. Only one core instance precedes it, so the reference
;; is out of range, and wasmtime 49.0.3 refuses it at parse. A loader that counts the tag alias as a core instance
;; finds an instance 1 and admits it.
;; Assembled with `wasm-tools parse tag-as-instance.wat -o tag-as-instance.wasm` (wasm-tools 1.258.0), which does not
;; validate; `wasm-tools validate --features all` refuses it.
(component
  (core module $m (tag (export "t")))
  (core instance $i (instantiate $m))
  (alias core export $i "t" (core tag $t))
  (core module $n (import "x" "t" (tag)))
  (core instance $j (instantiate $n (with "x" (instance 1)))))
