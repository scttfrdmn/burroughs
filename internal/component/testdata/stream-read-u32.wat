;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; stream-read-u32 (ADR 0122, ADR 0123, ADR 0124): an async `canon stream.read` of a stream<u32>. ADR 0122 refused a
;; read of any element but a byte at instantiate, since the copy moved bytes; ADR 0123 moved the refusal to the call,
;; because wasi-libc links a non-byte stream.read into every socket program; ADR 0124 retired it, since the copy
;; strides by the element. `read` calls it on handle 1, which this instance does not hold, so the read is seen to be
;; bound: it fails for its handle. It is stream-read-sync's twin but for the option and the element.
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
