;; #1162: a component whose core module reads a local that does not exist (`local.get 5`, no locals). Until
;; #1162 this reached the interpreter's ErrNotValidated check from a public path (#1134's census finding).
;; Assembled with `wasm-tools parse`, which does not validate.
(component
  (core module $m
    (func (export "f") (result i32) (local.get 5)))
  (core instance $i (instantiate $m))
  (func (export "f") (result u32) (canon lift (core func $i "f"))))
