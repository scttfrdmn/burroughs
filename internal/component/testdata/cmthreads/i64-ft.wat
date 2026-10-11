(component
  (core type $bodyt (func (param i64)))   ;; ft declares the i64 (memory64) form
  (core module $bodymod
    (table (export "t") 1 1 funcref)
    (func $body (param i32))              ;; but the table holds an (i32) -> () entry
    (elem (i32.const 0) $body)
  )
  (core instance $bodyi (instantiate $bodymod))
  (core func $new (canon thread.new-indirect (core type $bodyt) (core table $bodyi "t")))
  (core func $taskret (canon task.return (result u32)))
  (core module $runmod
    (type $p1 (func (param i32)))
    (type $p2r (func (param i32 i64) (result i32)))
    (type $cbt (func (param i32 i32 i32) (result i32)))
    (import "" "new"     (func $new     (type $p2r)))
    (import "" "taskret" (func $taskret (type $p1)))
    (func (export "callee") (param i32) (result i32)
      (call $taskret (call $new (i32.const 0) (i64.const 0)))
      (i32.const 0))
    (func (export "cb") (type $cbt) (param i32 i32 i32) (result i32) unreachable)
  )
  (core instance $runi (instantiate $runmod
    (with "" (instance (export "new" (func $new)) (export "taskret" (func $taskret))))))
  (core func $calleef (alias core export $runi "callee"))
  (core func $cbf (alias core export $runi "cb"))
  (type $rt (func async (param "id" u32) (result u32)))
  (func $run (type $rt) (canon lift (core func $calleef) async (callback $cbf)))
  (export "run" (func $run))
)
