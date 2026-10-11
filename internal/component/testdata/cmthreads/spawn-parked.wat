;; spawn-parked: create a Thread with thread.new-indirect and NEVER make it ready.
;;
;; The witness for ADR 0100's parked-Thread wake. The created Thread is bare-suspended — the reference's
;; `suspended()`, `ready_func is None`, not in `store.waiting` — so nothing in the model can run it, and the
;; task resolves and the call returns while it is still registered (legal: `unregister_thread` constrains
;; only the LAST Thread's exit, and ADR 0100 records that Threads outlive resolution).
;;
;; In this engine that Thread is a goroutine parked in a Go `select`, which is the case neither
;; `interp.ErrClosed` (needs a re-entry) nor `interp.ErrTerminated` (needs a safepoint) can reach.
;;
;; The result is the created Thread's index, so the reading pins it: 2, with the implicit Thread at 1.
(component
  (core type $bodyt (func (param i32)))
  (core module $bodymod
    (type $b (func (param i32)))
    (table (export "t") 1 1 funcref)
    (func $body (type $b) (param i32) unreachable) ;; must never run
    (elem (i32.const 0) $body)
  )
  (core instance $bodyi (instantiate $bodymod))
  (core func $new (canon thread.new-indirect (core type $bodyt) (core table $bodyi "t")))
  (core func $taskret (canon task.return (result u32)))
  (core module $runmod
    (type $p1 (func (param i32)))
    (type $p2r (func (param i32 i32) (result i32)))
    (type $cbt (func (param i32 i32 i32) (result i32)))
    (import "" "new"     (func $new     (type $p2r)))
    (import "" "taskret" (func $taskret (type $p1)))
    (func (export "callee") (param i32) (result i32)
      (call $taskret (call $new (i32.const 0) (i32.const 0)))
      (i32.const 0) ;; EXIT, with the created Thread never readied
    )
    (func (export "cb") (type $cbt) (param i32 i32 i32) (result i32) unreachable)
  )
  (core instance $runi (instantiate $runmod
    (with "" (instance (export "new" (func $new)) (export "taskret" (func $taskret))))
  ))
  (core func $calleef (alias core export $runi "callee"))
  (core func $cbf (alias core export $runi "cb"))
  (type $rt (func async (param "id" u32) (result u32)))
  (func $run (type $rt) (canon lift (core func $calleef) async (callback $cbf)))
  (export "run" (func $run))
)
