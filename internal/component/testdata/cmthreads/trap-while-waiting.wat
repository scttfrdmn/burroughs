;; trap-while-waiting: T1 waits on a subtask that only T2 would let finish, and T2 traps first (the chair's
;; item 1 on #997). A nested child's async `g` YIELDs until its flag is set, and its sync `release` sets it.
;; The parent's async `run` readies T1 then T2 and YIELDs until T1 marks mem[0]. T1 async-lowers `g`,
;; joins the subtask to a set and waits. T2 executes `unreachable` before it calls `release`. Whichever
;; order a scheduler picks, the call can only end in T2's trap; Burroughs's FIFO queue runs T1 first, so
;; on Burroughs T1 is waiting when T2 traps.
;;
;; Two shapes that gave no reading, recorded so they are not re-paid for. Each hit wasmtime 49.0.2's
;; "BUG: delivering non-present event" (concurrent.rs:948):
;;   * the control (`unreachable` -> `nop`), where T1's wait would complete;
;;   * T1 readying T2 just before its wait, to force the order on wasmtime too.
;; A probe isolated it: a created Thread's waitable-set.wait receiving the event of a subtask whose
;; callee YIELD-looped. So this fixture has no completing control. The trap arm's backtrace names the
;; frame instead (`bodymod!t2`).
;;
;; Not a future: wasmtime 49.0.2 refuses a future copy whose two ends are in one instance ("resource not
;; present"), with or without Threads, so the waitable comes from a child instead.
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
      unreachable
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
