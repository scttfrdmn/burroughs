;; wait-runs-ready: a Thread made ready before the callback returns WAIT runs while the task waits. The parent's
;; async `run` creates T and readies it, starts a nested child's async `g`, and returns WAIT on a set holding
;; g's subtask. g YIELDs until its flag is set, and only T sets it, through the child's sync `release`. So
;; `run` can finish only if T runs during the WAIT. On g's event the callback returns g's result, 5.
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
  (core module $bodymod
    (import "" "rel" (func $rel))
    (table (export "t") 1 1 funcref)
    (func $t (param i32) (call $rel)) ;; T: let g finish
    (elem (i32.const 0) $t))
  (core instance $bodyi (instantiate $bodymod (with "" (instance (export "rel" (func $rellow))))))
  (core func $new (canon thread.new-indirect (core type $bodyt) (core table $bodyi "t")))
  (core func $ret (canon task.return (result u32)))
  (core module $runmod
    (import "" "mem" (memory 1 1))
    (import "" "new" (func $new (param i32 i32) (result i32)))
    (import "" "later" (func $later (param i32)))
    (import "" "g" (func $g (param i32) (result i32)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "wsjoin" (func $wsjoin (param i32 i32)))
    (import "" "ret" (func $ret (param i32)))
    (func (export "run") (result i32) (local $p i32) (local $s i32)
      (call $later (call $new (i32.const 0) (i32.const 0)))
      (local.set $p (call $g (i32.const 32)))
      (local.set $s (call $wsnew))
      (call $wsjoin (i32.shr_u (local.get $p) (i32.const 4)) (local.get $s))
      (i32.or (i32.const 2) (i32.shl (local.get $s) (i32.const 4)))) ;; WAIT on s
    ;; g's subtask event: return its result
    (func (export "cb") (param i32 i32 i32) (result i32)
      (call $ret (i32.load (i32.const 32)))
      (i32.const 0)))
  (core instance $ri (instantiate $runmod
    (with "" (instance (export "mem" (memory $cm)) (export "new" (func $new)) (export "later" (func $later))
                       (export "g" (func $glow)) (export "wsnew" (func $wsnew)) (export "wsjoin" (func $wsjoin))
                       (export "ret" (func $ret))))))
  (alias core export $ri "run" (core func $runf))
  (alias core export $ri "cb" (core func $cbf))
  (type $rt (func async (result u32)))
  (func (export "run") (type $rt) (canon lift (core func $runf) async (callback $cbf)))
)
