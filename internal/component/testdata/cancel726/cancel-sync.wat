;; cancel-sync: a SYNC lift cancels a child that resolves on TASK_CANCELLED. Readings and the shared shape: cancel726.reading.
(component
  (component $child
    (core func $wsnew (canon waitable-set.new))
    (core func $tcancel (canon task.cancel))
    (core module $cm
      (import "" "wsnew" (func $wsnew (result i32)))
      (import "" "tcancel" (func $tcancel))
      (func (export "run") (param i32) (result i32)
        (i32.or (i32.shl (call $wsnew) (i32.const 4)) (i32.const 2))) ;; WAIT on an empty set
      (func (export "cb") (param i32 i32 i32) (result i32)
        (if (i32.eq (local.get 0) (i32.const 6)) (then (call $tcancel) (return (i32.const 0)))) ;; TASK_CANCELLED
        unreachable))
    (core instance $ci (instantiate $cm (with "" (instance (export "wsnew" (func $wsnew)) (export "tcancel" (func $tcancel))))))
    (alias core export $ci "run" (core func $runf))
    (alias core export $ci "cb" (core func $cbf))
    (type $rt (func async (param "id" u32) (result u32)))
    (func (export "run") (type $rt) (canon lift (core func $runf) async (callback $cbf))))
  (instance $c (instantiate $child))
  (alias export $c "run" (func $RUN))
  (core module $pmem (memory (export "m") 1 1))
  (core instance $pmemi (instantiate $pmem))
  (alias core export $pmemi "m" (core memory $pm))
  (core func $runlow (canon lower (func $RUN) async (memory $pm)))
  (core func $cancel (canon subtask.cancel))
  (core func $ret (canon task.return (result u32)))
  (core module $gm
    (import "" "run" (func $run (param i32 i32) (result i32)))
    (import "" "cancel" (func $cancel (param i32) (result i32)))
    (import "" "ret" (func $ret (param i32)))
    ;; state * 16 + cancel status
    (func $core (result i32) (local $p i32)
      (local.set $p (call $run (i32.const 1) (i32.const 0)))
      (i32.add (i32.mul (i32.and (local.get $p) (i32.const 15)) (i32.const 16))
               (call $cancel (i32.shr_u (local.get $p) (i32.const 4)))))
    (func (export "go") (result i32) (call $core)))
  (core instance $gi (instantiate $gm (with "" (instance (export "run" (func $runlow)) (export "cancel" (func $cancel)) (export "ret" (func $ret))))))
  (alias core export $gi "go" (core func $gof))
  
  (type $gt (func (result u32))) (func (export "go") (type $gt) (canon lift (core func $gof))))
