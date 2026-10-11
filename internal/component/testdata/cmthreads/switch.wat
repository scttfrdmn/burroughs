(component
  (core module $memmod
    (memory (export "m") 1)
  )
  (core instance $memi (instantiate $memmod))
  (alias core export $memi "m" (core memory $cm))

  (core type $bodyt (func (param i32)))

  (core func $later (canon thread.resume-later))

  (core func $idx (canon thread.index))

  (core module $bodymod
    (type $b (func (param i32)))
    (type $u (func (param i32)))
    (type $r (func (result i32)))
    (import "" "later" (func $later (type $u)))
    (import "" "idx" (func $idx (type $r)))
    (import "" "mem" (memory 1))
    (table (export "t") 2 2 funcref)
    ;; Both slots take k, the body's ordinal. The marker bit is 1 << (k+1), and the body's OWN
    ;; thread.index goes to mem[68 + 4k] — so the reading pins what each production returned, not
    ;; merely that it was reached.
    (func $record (param $k i32)
      (i32.store (i32.const 0)
        (i32.or (i32.load (i32.const 0)) (i32.shl (i32.const 1) (i32.add (local.get $k) (i32.const 1)))))
      (i32.store (i32.add (i32.const 68) (i32.shl (local.get $k) (i32.const 2))) (call $idx))
    )
    ;; slot 0: record only — for the yield-* forms, where the switcher stays READY and
    ;; thread.resume-later would trap on it.
    (func $mark (type $b) (param i32)
      (call $record (local.get 0))
    )
    ;; slot 1: record, then make the suspended switcher runnable again.
    (func $markResume (type $b) (param i32)
      (call $record (local.get 0))
      (call $later (i32.load (i32.const 64)))
    )
    (elem (i32.const 0) $mark $markResume)
  )
  (core instance $bodyi (instantiate $bodymod
    (with "" (instance
      (export "later" (func $later))
      (export "idx" (func $idx))
      (export "mem" (memory $cm))
    ))
  ))

  (core func $new (canon thread.new-indirect (core type $bodyt) (core table $bodyi "t")))
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
      (local.set $t (call $new (i32.const 1) (i32.const 0)))
      (drop (call $sresume (local.get $t)))

      ;; 0x2b yield-then-resume — the switcher stays ready, so the body must not resume it.
      (local.set $t (call $new (i32.const 0) (i32.const 1)))
      (drop (call $yresume (local.get $t)))

      ;; 0x28 resume-later then 0x2c suspend-then-promote, which needs a READY target.
      (local.set $t (call $new (i32.const 1) (i32.const 2)))
      (call $later (local.get $t))
      (drop (call $spromote (local.get $t)))

      ;; 0x28 then 0x2d yield-then-promote.
      (local.set $t (call $new (i32.const 0) (i32.const 3)))
      (call $later (local.get $t))
      (drop (call $ypromote (local.get $t)))

      ;; 0x29 plain suspend, with a body that resumes us.
      (local.set $t (call $new (i32.const 1) (i32.const 4)))
      (call $later (local.get $t))
      (drop (call $susp))

      ;; Pack: bits 0-5 the marker mask, then six 4-bit thread indices — entry at 6, then the five
      ;; bodies in creation order at 10, 14, 18, 22, 26. 30 bits, so it fits the u32 result.
      (call $taskret
        (i32.or (i32.load (i32.const 0))
          (i32.or (i32.shl (i32.load (i32.const 64)) (i32.const 6))
            (i32.or (i32.shl (i32.load (i32.const 68)) (i32.const 10))
              (i32.or (i32.shl (i32.load (i32.const 72)) (i32.const 14))
                (i32.or (i32.shl (i32.load (i32.const 76)) (i32.const 18))
                  (i32.or (i32.shl (i32.load (i32.const 80)) (i32.const 22))
                          (i32.shl (i32.load (i32.const 84)) (i32.const 26)))))))))
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
