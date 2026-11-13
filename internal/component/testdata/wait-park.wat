;; wait-park (#1233, ADR 0121): a pending wait-for, held two ways.
;;   run    callback-lifted: wait-for(300ms), join the subtask to a set, and WAIT. The callback returns
;;          task.return(code * 16 + state) and EXIT, so a wait that completes reads SUBTASK (1) * 16 + RETURNED (2) = 18.
;;   start  sync-lifted: wait-for(1h), returning the lower's packed result with the subtask left pending, so a
;;          Close after it has a timer to stop.
(component
  (import "wasi:clocks/monotonic-clock@0.3.0" (instance $mc
    (export "wait-for" (func async (param "how-long" u64)))))
  (alias export $mc "wait-for" (func $wf))
  (core func $wfl (canon lower (func $wf) async))
  (core func $wsnew (canon waitable-set.new))
  (core func $wsjoin (canon waitable.join))
  (core func $ret (canon task.return (result u32)))
  (core module $gm
    (import "" "wf" (func $wf (param i64) (result i32)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "wsjoin" (func $wsjoin (param i32 i32)))
    (import "" "ret" (func $ret (param i32)))
    (func (export "run") (result i32) (local $set i32)
      (local.set $set (call $wsnew))
      (call $wsjoin (i32.shr_u (call $wf (i64.const 300000000)) (i32.const 4)) (local.get $set))
      (i32.or (i32.const 2) (i32.shl (local.get $set) (i32.const 4)))) ;; WAIT on the set
    (func (export "cb") (param $code i32) (param i32) (param $state i32) (result i32)
      (call $ret (i32.add (i32.mul (local.get $code) (i32.const 16)) (local.get $state)))
      (i32.const 0)) ;; EXIT
    (func (export "start") (result i32) (call $wf (i64.const 3600000000000))))
  (core instance $gi (instantiate $gm (with "" (instance
    (export "wf" (func $wfl)) (export "wsnew" (func $wsnew)) (export "wsjoin" (func $wsjoin)) (export "ret" (func $ret))))))
  (type $at (func async (result u32)))
  (type $st (func (result u32)))
  (func (export "run") (type $at) (canon lift (core func $gi "run") async (callback (func $gi "cb"))))
  (func (export "start") (type $st) (canon lift (core func $gi "start"))))
