(component
  (import "wasi:cli/environment@0.3.0" (instance $env
    (type $l (list string))
    (export "get-arguments" (func (result $l)))))
  (core module $mem (memory (export "mem") 1 1 shared)
     (func (export "realloc") (param i32 i32 i32 i32) (result i32) i32.const 64))
  (core instance $mi (instantiate $mem))
  (alias core export $mi "mem" (core memory $m))
  (core func $ga (canon lower (func $env "get-arguments") (memory $m) (realloc (func $mi "realloc"))))
  (core module $main
    (import "env" "ga" (func $ga (param i32)))
    (import "env" "mem" (memory 1 1 shared))
    (func (export "run") (result i32) (call $ga (i32.const 0)) i32.const 0))
  (core instance $main (instantiate $main (with "env" (instance (export "ga" (func $ga)) (export "mem" (memory $m))))))
  (func $run (result (result)) (canon lift (core func $main "run")))
  (instance $inst (export "run" (func $run)))
  (export "wasi:cli/run@0.3.0" (instance $inst))
)
