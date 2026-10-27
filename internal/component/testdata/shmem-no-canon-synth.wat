(component
  (core module $mem (memory (export "mem") 1 1 shared))
  (core instance $mi (instantiate $mem))
  (alias core export $mi "mem" (core memory $m))
  (core module $main
    (import "env" "mem" (memory 1 1 shared))
    (func (export "run") (result i32) i32.const 0))
  (core instance $main (instantiate $main (with "env" (instance (export "mem" (memory $m))))))
  (func $run (result (result)) (canon lift (core func $main "run")))
  (instance $inst (export "run" (func $run)))
  (export "wasi:cli/run@0.3.0" (instance $inst))
)
