;; #1314's neighbour: a canon lift whose `realloc` is a core function of the wrong type, (i32 i32) -> i32, which
;; #1134 class C refused only on a lower (canon-realloc-mistyped.wat). CanonicalABI.md's canonopt validation:
;; `realloc` "the function has type `(func (param addr addr addr addr) (result addr))`". wasm-tools 1.258.0
;; `validate --features all` and wasmtime 49.0.3 `run` both refuse it at parse: "canonical option `realloc` uses a
;; core function with an incorrect signature".
;; Assembled with `wasm-tools parse`, which does not validate.
(component
  (core module $m
    (memory (export "mem") 1)
    (func (export "run"))
    (func (export "realloc") (param i32 i32) (result i32) (i32.const 0)))
  (core instance $i (instantiate $m))
  (func (export "run") (canon lift (core func $i "run") (memory $i "mem") (realloc (func $i "realloc")))))
