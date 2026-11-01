;; #1134 class C: a canon lower whose (realloc) is a lowered component function, so a host function in the
;; engine, with the right type, (i32 i32 i32 i32) -> i32. wasm-tools 1.258.0 `validate --features all` accepts it,
;; so Burroughs must instantiate it: a host-function realloc is unsupported only when it is called.
;; Assembled with `wasm-tools parse`.
(component
  (core module $m
    (memory (export "mem") 1)
    (func (export "realloc4") (param i32 i32 i32 i32) (result i32) i32.const 0)
    (func (export "g") (result i32) i32.const 7)
    (func (export "h") (result i32) i32.const 9))
  (core instance $i (instantiate $m))
  (func $rl (param "a" u32) (param "b" u32) (param "c" u32) (param "d" u32) (result u32)
    (canon lift (core func $i "realloc4")))
  (core func $lrl (canon lower (func $rl)))
  (func $g (result u32) (canon lift (core func $i "g")))
  (core func $lg (canon lower (func $g) (memory $i "mem") (realloc $lrl)))
  (func (export "h") (result u32) (canon lift (core func $i "h"))))
