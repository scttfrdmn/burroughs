(component
  (core module $memmod
    (memory (export "m") 1)
  )
  (core instance $memi (instantiate $memmod))
  (alias core export $memi "m" (core memory $cm))

  (core type $bodyt (func (param i32)))

  (core func $later (canon thread.resume-later))

  (core module $bodymod
    (type $b (func (param i32)))
    (type $u (func (param i32)))
    (import "" "later" (func $later (type $u)))
    (import "" "mem" (memory 1))
    (table (export "t") 2 2 funcref)
    ;; slot 0: mark only — for the yield-* forms, where the switcher stays READY.
    (func $mark (type $b) (param i32)
      (i32.store (i32.const 0) (i32.or (i32.load (i32.const 0)) (local.get 0)))
    )
    ;; slot 1: mark, then make the suspended switcher runnable again.
    (func $markResume (type $b) (param i32)
      (i32.store (i32.const 0) (i32.or (i32.load (i32.const 0)) (local.get 0)))
      (call $later (i32.load (i32.const 64)))
    )
    (elem (i32.const 0) $mark $markResume)
  )
  (core instance $bodyi (instantiate $bodymod
    (with "" (instance (export "later" (func $later)) (export "mem" (memory $cm))))
  ))

  (core func $new (canon thread.new-indirect (core type $bodyt) (core table $bodyi "t")))
  (core func $idx (canon thread.index))
  (core func $susp (canon thread.suspend))
  (core func $sresume (canon thread.suspend-then-resume))
  (core func $yresume (canon thread.yield-then-resume))
  (core func $spromote (canon thread.suspend-then-promote))
  (core func $ypromote (canon thread.yield-then-promote))
  (core func $taskret (canon task.return (result u32)))

  (core module $runmod
    (type $v (func))
    (type $r (func (result i32)))
    (type $p1 (func (param i32)))
    (type $p1r (func (param i32) (result i32)))
    (type $p2r (func (param i32 i32) (result i32)))
    (type $cbt (func (param i32 i32 i32) (result i32)))
    (import "" "new"      (func $new      (type $p2r)))
    (import "" "idx"      (func $idx      (type $r)))
    (import "" "later"    (func $later    (type $p1)))
    (import "" "susp"     (func $susp     (type $r)))
    (import "" "sresume"  (func $sresume  (type $p1r)))
    (import "" "yresume"  (func $yresume  (type $p1r)))
    (import "" "spromote" (func $spromote (type $p1r)))
    (import "" "ypromote" (func $ypromote (type $p1r)))
    (import "" "taskret"  (func $taskret  (type $p1)))
    (import "" "mem"      (memory 1))

    (func (export "callee") (param i32) (result i32)
      (local $t i32)
      ;; 0x26 thread.index — recorded where every body can read it.
      (i32.store (i32.const 64) (call $idx))
      (i32.store (i32.const 0) (i32.const 1))

      ;; 0x2a suspend-then-resume, on a thread that is still suspended from creation.
      (local.set $t (call $new (i32.const 1) (i32.const 2)))
      (drop (call $sresume (local.get $t)))

      ;; 0x2b yield-then-resume — the switcher stays ready, so the body must not resume it.
      (local.set $t (call $new (i32.const 0) (i32.const 4)))
      (drop (call $yresume (local.get $t)))

      ;; 0x28 resume-later then 0x2c suspend-then-promote, which needs a READY target.
      (local.set $t (call $new (i32.const 1) (i32.const 8)))
      (call $later (local.get $t))
      (drop (call $spromote (local.get $t)))

      ;; 0x28 then 0x2d yield-then-promote.
      (local.set $t (call $new (i32.const 0) (i32.const 16)))
      (call $later (local.get $t))
      (drop (call $ypromote (local.get $t)))

      ;; 0x29 plain suspend, with a body that resumes us.
      (local.set $t (call $new (i32.const 1) (i32.const 32)))
      (call $later (local.get $t))
      (drop (call $susp))

      (call $taskret (i32.load (i32.const 0)))
      (i32.const 0)
    )
    (func (export "cb") (type $cbt) (param i32 i32 i32) (result i32)
      unreachable
    )
  )
  (core instance $runi (instantiate $runmod
    (with "" (instance
      (export "new"      (func $new))
      (export "idx"      (func $idx))
      (export "later"    (func $later))
      (export "susp"     (func $susp))
      (export "sresume"  (func $sresume))
      (export "yresume"  (func $yresume))
      (export "spromote" (func $spromote))
      (export "ypromote" (func $ypromote))
      (export "taskret"  (func $taskret))
      (export "mem"      (memory $cm))
    ))
  ))
  (alias core export $runi "callee" (core func $calleef))
  (alias core export $runi "cb" (core func $cbf))
  (type $rt (func async (param "id" u32) (result u32)))
  (func $run (type $rt) (canon lift (core func $calleef) async (callback $cbf)))
  (export "run" (func $run))
)
