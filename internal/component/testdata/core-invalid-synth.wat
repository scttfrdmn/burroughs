;; #1162: a component whose core module fails validation. Its function declares an i32 result and pushes an
;; i64 (`type mismatch`). wasm-tools 1.258.0 `validate --features all` refuses it; until #1162 Burroughs loaded
;; it and Component.Call("f") returned u32(1). Assembled with `wasm-tools parse`, which does not validate.
(component
  (core module $m
    (func (export "f") (result i32) i64.const 1))
  (core instance $i (instantiate $m))
  (func (export "f") (result u32) (canon lift (core func $i "f"))))
