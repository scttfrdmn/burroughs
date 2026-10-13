;; trap-while-waiting-control: trap-while-waiting.wat with T2's `unreachable` replaced by `nop`, so T2 calls
;; `release`, g finishes, T1's wait completes and `run` returns 1. The completing control the chair's
;; ruling requires, so the fixture discriminates in both directions.
;;
;; Its oracle is the REFERENCE READING, `value 1` (testdata/cmthreads/reference/refread.reading), because
;; wasmtime 49.0.2 hits its "BUG: delivering non-present event" on this shape (wasmtime-bug-repro.wat).
(component
  (component $child
    (core module $cmem (memory (export "m") 1 1))
    (core instance $cmemi (instantiate $cmem))
    (alias core export $cmemi "m" (core memory $chm))
    (core func $ret (canon task.return (result u32)))
    (core module $cm
      (import "" "ret" (func $ret (param i32)))
      (import "" "mem" (memory 1 1))
      (func (export "g") (result i32) (i32.const 1)) ;; YIELD
      (func (export "cb") (param i32 i32 i32) (result i32)
        (if (i32.load (i32.const 0)) (then (call $ret (i32.const 5)) (return (i32.const 0))))
        (i32.const 1))
      (func (export "release") (i32.store (i32.const 0) (i32.const 1))))
    (core instance $ci (instantiate $cm (with "" (instance (export "ret" (func $ret)) (export "mem" (memory $chm))))))
    (alias core export $ci "g" (core func $gf))
    (alias core export $ci "cb" (core func $cbf))
    (alias core export $ci "release" (core func $relf))
    (type $gt (func async (result u32)))
    (type $relt (func))
    (func (export "g") (type $gt) (canon lift (core func $gf) async (callback $cbf)))
    (func (export "release") (type $relt) (canon lift (core func $relf)))
  )
  (instance $c (instantiate $child))
  (alias export $c "g" (func $g))
  (alias export $c "release" (func $rel))

  (core module $memmod (memory (export "m") 1 1))
  (core instance $memi (instantiate $memmod))
  (alias core export $memi "m" (core memory $cm))
  (core type $bodyt (func (param i32)))
  (core func $later (canon thread.resume-later))
  (core func $glow (canon lower (func $g) async (memory $cm)))
  (core func $rellow (canon lower (func $rel)))
  (core func $wsnew (canon waitable-set.new))
  (core func $wsjoin (canon waitable.join))
  (core func $wswait (canon waitable-set.wait (memory $cm)))
  (core module $bodymod
    (import "" "mem" (memory 1 1))
    (import "" "g" (func $g (param i32) (result i32)))
    (import "" "rel" (func $rel))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "wsjoin" (func $wsjoin (param i32 i32)))
    (import "" "wswait" (func $wswait (param i32 i32) (result i32)))
    (table (export "t") 2 2 funcref)
    ;; T1: start g into retptr 32; unless it returned at once, wait on a set holding its subtask; mark mem[0]
    (func $t1 (param i32) (local $p i32) (local $s i32)
      (local.set $p (call $g (i32.const 32)))
      (if (i32.ne (i32.and (local.get $p) (i32.const 15)) (i32.const 2))
        (then
          (local.set $s (call $wsnew))
          (call $wsjoin (i32.shr_u (local.get $p) (i32.const 4)) (local.get $s))
          (drop (call $wswait (local.get $s) (i32.const 72)))))
      (i32.store (i32.const 0) (i32.const 1)))
    ;; T2: traps before the call that would have let g finish
    (func $t2 (param i32)
      nop
      (call $rel))
    (elem (i32.const 0) $t1 $t2))
  (core instance $bodyi (instantiate $bodymod
    (with "" (instance (export "mem" (memory $cm)) (export "g" (func $glow)) (export "rel" (func $rellow))
                       (export "wsnew" (func $wsnew)) (export "wsjoin" (func $wsjoin)) (export "wswait" (func $wswait))))))
  (core func $new (canon thread.new-indirect (core type $bodyt) (core table $bodyi "t")))
  (core func $ret (canon task.return (result u32)))
  (core module $runmod
    (import "" "mem" (memory 1 1))
    (import "" "new" (func $new (param i32 i32) (result i32)))
    (import "" "later" (func $later (param i32)))
    (import "" "ret" (func $ret (param i32)))
    (func (export "run") (result i32)
      (call $later (call $new (i32.const 0) (i32.const 0)))
      (call $later (call $new (i32.const 1) (i32.const 0)))
      (i32.const 1)) ;; YIELD
    (func (export "cb") (param i32 i32 i32) (result i32)
      (if (i32.load (i32.const 0)) (then (call $ret (i32.const 1)) (return (i32.const 0))))
      (i32.const 1)))
  (core instance $ri (instantiate $runmod
    (with "" (instance (export "mem" (memory $cm)) (export "new" (func $new))
                       (export "later" (func $later)) (export "ret" (func $ret))))))
  (alias core export $ri "run" (core func $runf))
  (alias core export $ri "cb" (core func $cbf))
  (type $rt (func async (result u32)))
  (func (export "run") (type $rt) (canon lift (core func $runf) async (callback $cbf)))
)
