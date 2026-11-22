;; #1314's neighbour: a canon lift naming two string encodings. CanonicalABI.md's canonopt validation:
;; `string-encoding=N` "can be passed at most once, regardless of `N`". wasm-tools 1.258.0 `validate --features all`
;; and wasmtime 49.0.3 `run` both refuse it at parse: "canonical encoding option `utf8` conflicts with option
;; `utf16`". Burroughs kept the last.
;; Assembled with `wasm-tools parse`, which does not validate.
(component
  (core module $m
    (func (export "run")))
  (core instance $i (instantiate $m))
  (func (export "run") (canon lift (core func $i "run") string-encoding=utf8 string-encoding=utf16)))
