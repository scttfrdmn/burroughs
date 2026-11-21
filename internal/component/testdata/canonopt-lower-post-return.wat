;; #1314's neighbour: a canon lower naming a `post-return`. CanonicalABI.md's canonopt validation: `post-return` is
;; "only allowed on canon lift". wasm-tools 1.258.0 `validate --features all` and wasmtime 49.0.3 `run` both refuse it
;; at parse: "canonical option `post-return` cannot be specified for lowerings".
;; Assembled with `wasm-tools parse`, which does not validate.
(component
  (import "f" (func $f (param "x" u32)))
  (core module $mem
    (func (export "pr")))
  (core instance $mi (instantiate $mem))
  (core func $lf (canon lower (func $f) (post-return (func $mi "pr"))))
  (core module $m
    (import "" "f" (func (param i32)))
    (func (export "run")))
  (core instance $i (instantiate $m (with "" (instance (export "f" (func $lf))))))
  (func (export "run") (canon lift (core func $i "run"))))
