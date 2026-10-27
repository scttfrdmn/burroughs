(component
  (core module $mem (memory (export "mem") 1 1 shared))
  (core instance $mi (instantiate $mem))
  (alias core export $mi "mem" (core memory $m))
  (core module $main
    (import "env" "mem" (memory 1 1 shared))
    (func (export "s") (result i32) i32.const 0))
  (core instance $main (instantiate $main (with "env" (instance (export "mem" (memory $m))))))
  (func $s (result string) (canon lift (core func $main "s") (memory $m)))
  (export "s" (func $s))
)
