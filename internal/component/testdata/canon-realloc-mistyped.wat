;; #1134 class C: a canon lower whose (realloc) is a core function of the wrong type, (i32 i32) -> i32.
;; wasm-tools 1.258.0 `validate --features all` refuses it: "canonical option `realloc` uses a core function with an
;; incorrect signature". Burroughs refuses it at instantiation as ErrInvalid (component.ErrCanonOptionInvalid).
;; Assembled with `wasm-tools parse`, which does not validate.
(component
  (core module $m
    (memory (export "mem") 1)
    (func (export "realloc") (param i32 i32) (result i32) i32.const 0)
    (func (export "g") (result i32) i32.const 7)
    (func (export "h") (result i32) i32.const 9))
  (core instance $i (instantiate $m))
  (func $g (result u32) (canon lift (core func $i "g")))
  (core func $lg (canon lower (func $g) (memory $i "mem") (realloc (func $i "realloc"))))
  (func (export "h") (result u32) (canon lift (core func $i "h"))))
