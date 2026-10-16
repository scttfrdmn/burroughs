;; ignore-cancel: a callee that ignores a delivered cancellation and returns a value. The reference resolves
;; it RETURNED with the value: on_resolve(result) is RETURNED whether or not cancellation was requested;
;; only on_resolve(None) (task.cancel) is a cancel resolution. A nested child's async `work` YIELDs once.
;; Its callback ignores the event it is handed, TASK_CANCELLED included, and returns task.return(9). The
;; parent's async `run` cancels work's subtask at once, waits if the cancel returns BLOCKED, and returns
;; state*16 + the value at its retptr. Expected: RETURNED (2) with 9, so 41.
(component
  (component $child
    (core func $ret (canon task.return (result u32)))
    (core module $wm
      (import "" "ret" (func $ret (param i32)))
      (func (export "work") (result i32) (i32.const 1)) ;; YIELD
      (func (export "cb") (param i32 i32 i32) (result i32) (call $ret (i32.const 9)) (i32.const 0)))
    (core instance $wi (instantiate $wm (with "" (instance (export "ret" (func $ret))))))
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
    (func $report (param $state i32)
      (call $ret (i32.add (i32.shl (local.get $state) (i32.const 4)) (i32.load (i32.const 16)))))
    (func (export "run") (result i32) (local $p i32) (local $st i32) (local $r i32) (local $s i32)
      (local.set $p (call $w (i32.const 16)))
      (if (i32.eq (i32.and (local.get $p) (i32.const 15)) (i32.const 2))
        (then (call $report (i32.const 2)) (return (i32.const 0))))
      (local.set $st (i32.shr_u (local.get $p) (i32.const 4)))
      (local.set $r (call $scancel (local.get $st)))
      (if (i32.ne (local.get $r) (i32.const -1))
        (then (call $report (local.get $r)) (return (i32.const 0))))
      (local.set $s (call $wsnew))
      (call $wsjoin (local.get $st) (local.get $s))
      (i32.or (i32.const 2) (i32.shl (local.get $s) (i32.const 4))))
    (func (export "cb") (param i32) (param i32) (param $state i32) (result i32)
      (call $report (local.get $state))
      (i32.const 0)))
  (core instance $pi (instantiate $pm
    (with "" (instance (export "mem" (memory $cm)) (export "w" (func $wlow)) (export "scancel" (func $scancel))
                       (export "wsnew" (func $wsnew)) (export "wsjoin" (func $wsjoin)) (export "ret" (func $ret))))))
  (alias core export $pi "run" (core func $runf))
  (alias core export $pi "cb" (core func $cbf))
  (type $rt (func async (result u32)))
  (func (export "run") (type $rt) (canon lift (core func $runf) async (callback $cbf)))
)
