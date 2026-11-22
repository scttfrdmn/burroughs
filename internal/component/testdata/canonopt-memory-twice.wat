;; #1314's neighbour: a canon lift naming `memory` twice. CanonicalABI.md's canonopt validation: options "can be
;; specified at most once in any particular list of options". wasm-tools 1.258.0 `validate --features all` and
;; wasmtime 49.0.3 `run` both refuse it at parse: "canonical option `memory` is specified more than once".
;; Assembled with `wasm-tools parse`, which does not validate.
(component
  (core module $m
    (memory (export "mem") 1)
    (func (export "run")))
  (core instance $i (instantiate $m))
  (func (export "run") (canon lift (core func $i "run") (memory $i "mem") (memory $i "mem"))))
