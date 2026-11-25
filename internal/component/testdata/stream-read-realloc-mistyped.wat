;; Copyright 2026 Scott Friedman. SPDX-License-Identifier: Apache-2.0
;;
;; stream-read-realloc-mistyped (#1374 E1b, ADR 0134): a canon stream.read whose (realloc) is a core function of the
;; wrong type, (i32 i32) -> i32, as canon-realloc-mistyped.wat's lower has. wasm-tools 1.258.0 `validate --features all`
;; refuses it: "canonical option `realloc` uses a core function with an incorrect signature". Burroughs refuses it at
;; instantiation as ErrCanonOptionInvalid, now that stream.read's realloc survives the decode.
;; Assembled with `wasm-tools parse`, which does not validate.
(component
  (type $s (stream string))
  (core module $m
    (memory (export "mem") 1)
    (func (export "realloc") (param i32 i32) (result i32) i32.const 0)
    (func (export "h") (result i32) i32.const 9))
  (core instance $i (instantiate $m))
  (core func $sread (canon stream.read $s async (memory $i "mem") (realloc (func $i "realloc"))))
  (func (export "h") (result u32) (canon lift (core func $i "h"))))
