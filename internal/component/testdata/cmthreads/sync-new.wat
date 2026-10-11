(component
  (core type $bodyt (func (param i32)))
  (core module $bodymod
    (table (export "t") 1 1 funcref)
    (func $body (param i32))
    (elem (i32.const 0) $body)
  )
  (core instance $bodyi (instantiate $bodymod))
  (core func $new (canon thread.new-indirect (core type $bodyt) (core table $bodyi "t")))
  (core module $runmod
    (type $p2r (func (param i32 i32) (result i32)))
    (import "" "new" (func $new (type $p2r)))
    (func (export "callee") (param i32) (result i32)
      (call $new (i32.const 0) (i32.const 0)))
  )
  (core instance $runi (instantiate $runmod
    (with "" (instance (export "new" (func $new))))))
  (core func $calleef (alias core export $runi "callee"))
  (type $rt (func (param "id" u32) (result u32)))
  (func $run (type $rt) (canon lift (core func $calleef)))
  (export "run" (func $run))
)
