;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; stream-read-u32 (ADR 0122, ADR 0123): an async `canon stream.read` of a stream<u32>. The copy moves bytes and the
;; pin strides by the element's size (def:WritableGuestBuffer), so a read of any element but a byte is refused by
;; name. ADR 0122 refused it at instantiate; ADR 0123 moved the refusal to the call, because wasi-libc links a
;; non-byte stream.read into every socket program. `read` calls it, on handle 1, so the refusal can be watched.
;; It is stream-read-sync's twin but for the option and the element.
;; Assembled with `wasm-tools parse stream-read-u32.wat -o stream-read-u32.wasm` (wasm-tools 1.258.0).
(component
  (core module $mm (memory (export "m") 1))
  (core instance $mi (instantiate $mm))
  (alias core export $mi "m" (core memory $m))
  (type $words (stream u32))
  (core func $sread (canon stream.read $words async (memory $m)))
  (core module $gm
    (import "" "sread" (func $sread (param i32 i32 i32) (result i32)))
    (func (export "read") (result i32) (call $sread (i32.const 1) (i32.const 0) (i32.const 4))))
  (core instance $gi (instantiate $gm (with "" (instance (export "sread" (func $sread))))))
  (func (export "read") (result u32) (canon lift (core func $gi "read"))))
