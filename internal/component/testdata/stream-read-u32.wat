;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; stream-read-u32 (ADR 0122): an async `canon stream.read` of a stream<u32>. The copy moves bytes and the pin
;; strides by the element's size (def:WritableGuestBuffer), so a read of any element but a byte is refused at
;; instantiate by name. It is stream-read-sync's twin but for the option and the element.
;; Assembled with `wasm-tools parse stream-read-u32.wat -o stream-read-u32.wasm` (wasm-tools 1.258.0).
(component
  (core module $mm (memory (export "m") 1))
  (core instance $mi (instantiate $mm))
  (alias core export $mi "m" (core memory $m))
  (type $words (stream u32))
  (core func $sread (canon stream.read $words async (memory $m)))
  (core module $gm (import "" "sread" (func (param i32 i32 i32) (result i32))))
  (core instance (instantiate $gm (with "" (instance (export "sread" (func $sread)))))))
