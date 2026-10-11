;; spawn: thread.index + thread.new-indirect + thread.resume-later, and NOTHING ELSE.
;;
;; ADR 0100's point 4 lands one form at a time, so each slice needs a fixture using only the forms it
;; models. `switch.wat` needs all eight; `index-call.wat` needs one. This needs three.
;;
;; The entry Thread records its own index, creates a second Thread, makes it runnable, and the body
;; records ITS index. Both come back packed, so the reading pins two indices rather than a reachability
;; bit. The body must run before the entry returns: an async callback lift, where the entry yields once
;; so the scheduler can pick up the ready Thread, then resolves on the callback.
(component
  (core module $memmod
    (memory (export "m") 1)
  )
  (core instance $memi (instantiate $memmod))
  (alias core export $memi "m" (core memory $cm))

  (core type $bodyt (func (param i32)))
  (core func $idx   (canon thread.index))
  (core func $later (canon thread.resume-later))

  (core module $bodymod
    (type $b (func (param i32)))
    (type $r (func (result i32)))
    (import "" "idx" (func $idx (type $r)))
    (import "" "mem" (memory 1))
    (table (export "t") 1 1 funcref)
    ;; The spawned Thread: record my own index at mem[8] and a marker at mem[0]. No resume-later here —
    ;; the entry yields rather than suspending, so the scheduler returns to it on its own.
    (func $body (type $b) (param i32)
      (i32.store (i32.const 0) (i32.const 1))
      (i32.store (i32.const 8) (call $idx))
    )
    (elem (i32.const 0) $body)
  )
  (core instance $bodyi (instantiate $bodymod
    (with "" (instance (export "idx" (func $idx)) (export "mem" (memory $cm))))
  ))

  (core func $new (canon thread.new-indirect (core type $bodyt) (core table $bodyi "t")))
  (core func $taskret (canon task.return (result u32)))

  (core module $runmod
    (type $r (func (result i32)))
    (type $p1 (func (param i32)))
    (type $p2r (func (param i32 i32) (result i32)))
    (type $cbt (func (param i32 i32 i32) (result i32)))
    (import "" "idx"     (func $idx     (type $r)))
    (import "" "new"     (func $new     (type $p2r)))
    (import "" "later"   (func $later   (type $p1)))
    (import "" "taskret" (func $taskret (type $p1)))
    (import "" "mem"     (memory 1))

    ;; The callee: record my index, spawn, make the spawned Thread ready, then YIELD so it runs.
    (func (export "callee") (param i32) (result i32)
      (i32.store (i32.const 4) (call $idx))
      (call $later (call $new (i32.const 0) (i32.const 0)))
      (i32.const 1) ;; YIELD
    )

    ;; The callback. **It re-YIELDs until the spawned body's marker is set, and only then packs and
    ;; returns** — so the result does not depend on which ready Thread the scheduler picks first.
    ;;
    ;; The first version of this fixture packed on its first re-entry, on the assumption that "by now
    ;; the spawned Thread has run". The reference does not promise that. Its YIELD arm is
    ;;
    ;;     case CallbackCode.YIELD:
    ;;       thread.wait_until(lambda: inst.exclusive_thread is None)
    ;;
    ;; with `exclusive_thread` set to None on the line before, so the predicate is already true, and
    ;; `Thread.wait_until` begins
    ;;
    ;;     if ready_func() and not DETERMINISTIC_PROFILE and random.randint(0,1):
    ;;       return
    ;;
    ;; — half the time it returns at once, re-entering this callback with EVENT_NONE before the body
    ;; has run. wasmtime 49.0.2 happens always to run the body first (30 of 30 runs gave 529 on the
    ;; first version), which is why the first reading looked order-independent: it pinned one
    ;; implementation's fixed choice. With the loop, both orders reach the same packed value.
    ;;
    ;; Pack: marker in bit 0, entry index in bits 4-7, body index in bits 8-11.
    (func (export "cb") (type $cbt) (param i32 i32 i32) (result i32)
      (if (i32.eqz (i32.load (i32.const 0)))
        (then (return (i32.const 1)))) ;; YIELD again: the body has not run yet
      (call $taskret
        (i32.or (i32.load (i32.const 0))
          (i32.or (i32.shl (i32.load (i32.const 4)) (i32.const 4))
                  (i32.shl (i32.load (i32.const 8)) (i32.const 8)))))
      (i32.const 0) ;; EXIT
    )
  )
  (core instance $runi (instantiate $runmod
    (with "" (instance
      (export "idx"     (func $idx))
      (export "new"     (func $new))
      (export "later"   (func $later))
      (export "taskret" (func $taskret))
      (export "mem"     (memory $cm))
    ))
  ))
  (core func $calleef (alias core export $runi "callee"))
  (core func $cbf (alias core export $runi "cb"))
  (type $rt (func async (param "id" u32) (result u32)))
  (func $run (type $rt) (canon lift (core func $calleef) async (callback $cbf)))
  (export "run" (func $run))
)
