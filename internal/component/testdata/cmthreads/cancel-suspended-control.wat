;; cancel-suspended-control: cancel-suspended.wat with T's resume of `work` removed, so work stays suspended.
;; The pin delivers a pending cancel only at the callback loop's delivery points, never to a suspended
;; Thread, so the cancel is never delivered and `run` never returns. wasmtime 49.0.2: no return
;; (`timeout 15`, exit 124).
(component
  (component $child
    (core module $cmem (memory (export "m") 1 1))
    (core instance $cmemi (instantiate $cmem))
    (alias core export $cmemi "m" (core memory $chm))
    (core type $bodyt (func (param i32)))
    (core func $idx (canon thread.index))
    (core func $later (canon thread.resume-later))
    (core func $susp (canon thread.suspend))
    (core func $ret (canon task.return (result u32)))
    (core func $tcancel (canon task.cancel))
    (core module $tmod
      (import "" "mem" (memory 1 1))
      (import "" "later" (func $later (param i32)))
      (table (export "t") 1 1 funcref)
      (func $t (param i32)) ;; control: T does not resume work
      (elem (i32.const 0) $t))
    (core instance $ti (instantiate $tmod (with "" (instance (export "mem" (memory $chm)) (export "later" (func $later))))))
    (core func $new (canon thread.new-indirect (core type $bodyt) (core table $ti "t")))
    (core module $wm
      (import "" "mem" (memory 1 1))
      (import "" "idx" (func $idx (result i32)))
      (import "" "new" (func $new (param i32 i32) (result i32)))
      (import "" "later" (func $later (param i32)))
      (import "" "susp" (func $susp (result i32)))
      (import "" "ret" (func $ret (param i32)))
      (import "" "tcancel" (func $tcancel))
      (func (export "work") (result i32)
        (i32.store (i32.const 4) (call $idx))
        (call $later (call $new (i32.const 0) (i32.const 0)))
        (drop (call $susp))
        (i32.const 1)) ;; YIELD
      (func (export "cb") (param $ev i32) (param i32 i32) (result i32)
        (if (i32.eq (local.get $ev) (i32.const 6)) ;; TASK_CANCELLED
          (then (call $tcancel) (return (i32.const 0))))
        (call $ret (i32.const 9))
        (i32.const 0)))
    (core instance $wi (instantiate $wm
      (with "" (instance (export "mem" (memory $chm)) (export "idx" (func $idx)) (export "new" (func $new))
                         (export "later" (func $later)) (export "susp" (func $susp)) (export "ret" (func $ret))
                         (export "tcancel" (func $tcancel))))))
    (alias core export $wi "work" (core func $workf))
    (alias core export $wi "cb" (core func $wcbf))
    (type $wt (func async (result u32)))
    (func (export "work") (type $wt) (canon lift (core func $workf) async (callback $wcbf))))
  (instance $c (instantiate $child))
  (alias export $c "work" (func $W))

  (core module $memmod (memory (export "m") 1 1))
  (core instance $memi (instantiate $memmod))
  (alias core export $memi "m" (core memory $cm))
  (core func $wlow (canon lower (func $W) async (memory $cm)))
  (core func $scancel (canon subtask.cancel async))
  (core func $wsnew (canon waitable-set.new))
  (core func $wsjoin (canon waitable.join))
  (core func $ret (canon task.return (result u32)))
  (core module $pm
    (import "" "mem" (memory 1 1))
    (import "" "w" (func $w (param i32) (result i32)))
    (import "" "scancel" (func $scancel (param i32) (result i32)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "wsjoin" (func $wsjoin (param i32 i32)))
    (import "" "ret" (func $ret (param i32)))
    (func (export "run") (result i32) (local $p i32) (local $st i32) (local $r i32) (local $s i32)
      (local.set $p (call $w (i32.const 16)))
      (if (i32.eq (i32.and (local.get $p) (i32.const 15)) (i32.const 2)) ;; returned at once: report it
        (then (call $ret (i32.const 2)) (return (i32.const 0))))
      (local.set $st (i32.shr_u (local.get $p) (i32.const 4)))
      (local.set $r (call $scancel (local.get $st)))
      (if (i32.ne (local.get $r) (i32.const -1)) ;; resolved by the cancel itself: its state
        (then (call $ret (local.get $r)) (return (i32.const 0))))
      (local.set $s (call $wsnew))
      (call $wsjoin (local.get $st) (local.get $s))
      (i32.or (i32.const 2) (i32.shl (local.get $s) (i32.const 4)))) ;; WAIT
    ;; the subtask's event: (SUBTASK, index, state), so report the state
    (func (export "cb") (param i32) (param i32) (param $state i32) (result i32)
      (call $ret (local.get $state))
      (i32.const 0)))
  (core instance $pi (instantiate $pm
    (with "" (instance (export "mem" (memory $cm)) (export "w" (func $wlow)) (export "scancel" (func $scancel))
                       (export "wsnew" (func $wsnew)) (export "wsjoin" (func $wsjoin)) (export "ret" (func $ret))))))
  (alias core export $pi "run" (core func $runf))
  (alias core export $pi "cb" (core func $cbf))
  (type $rt (func async (result u32)))
  (func (export "run") (type $rt) (canon lift (core func $runf) async (callback $cbf)))
)
