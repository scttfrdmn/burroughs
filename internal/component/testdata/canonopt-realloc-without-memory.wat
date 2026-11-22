;; #1314's neighbour: a canon lift naming a `realloc` and no `memory`. CanonicalABI.md's canonopt validation: "if
;; `realloc` is present then `memory` must be present". wasm-tools 1.258.0 `validate --features all` and wasmtime
;; 49.0.3 `run` both refuse it at parse: "canonical option `realloc` requires `memory` to also be specified".
;; Assembled with `wasm-tools parse`, which does not validate.
(component
  (core module $m
    (func (export "run"))
    (func (export "realloc") (param i32 i32 i32 i32) (result i32) (i32.const 0)))
  (core instance $i (instantiate $m))
  (func (export "run") (canon lift (core func $i "run") (realloc (func $i "realloc")))))
