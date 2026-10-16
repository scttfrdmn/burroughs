;; promote-implicit-control: promote-implicit.wat with T promoting a Thread U that was never readied, so the
;; promote falls to a plain yield. Then both T and run's waiting implicit Thread are candidates, and the
;; pin's `random.choice` permits either order. REFERENCE READING: {16, 17}. wasmtime 49.0.2: 16, 10 of 10.
;; So this arm does not discriminate on wasmtime, and its job is to show the promote fixture's 16 is the
;; promote's, not the scheduler's choice: the pin reads {16} only on the promote arm.
(component
  (core module $memmod (memory (export "m") 1 1))
  (core instance $memi (instantiate $memmod))
  (alias core export $memi "m" (core memory $cm))
  (core type $bodyt (func (param i32)))
  (core func $idx (canon thread.index))
  (core func $later (canon thread.resume-later))
  (core func $ypromote (canon thread.yield-then-promote))
  (core module $tmod
    (import "" "mem" (memory 1 1))
    (import "" "yp" (func $yp (param i32) (result i32)))
    (table (export "t") 1 1 funcref)
    (func $t (param i32)
      (i32.store (i32.const 0) (i32.const 1))
      (drop (call $yp (i32.load (i32.const 8))))
      (i32.store (i32.const 4) (i32.const 1)))
    (elem (i32.const 0) $t))
  (core instance $ti (instantiate $tmod (with "" (instance (export "mem" (memory $cm)) (export "yp" (func $ypromote))))))
  (core func $new (canon thread.new-indirect (core type $bodyt) (core table $ti "t")))
  (core func $ret (canon task.return (result u32)))
  (core module $rm
    (import "" "mem" (memory 1 1))
    (import "" "idx" (func $idx (result i32)))
    (import "" "new" (func $new (param i32 i32) (result i32)))
    (import "" "later" (func $later (param i32)))
    (import "" "ret" (func $ret (param i32)))
    (func (export "run") (result i32)
      (i32.store (i32.const 8) (call $new (i32.const 0) (i32.const 1))) ;; control: U, never readied
      (i32.store (i32.const 12) (i32.const -1)) ;; nothing recorded yet
      (call $later (call $new (i32.const 0) (i32.const 0)))
      (i32.const 1)) ;; YIELD
    (func (export "cb") (param i32 i32 i32) (result i32)
      ;; the first entry after T started records whether T had already resumed
      (if (i32.and (i32.load (i32.const 0)) (i32.eq (i32.load (i32.const 12)) (i32.const -1)))
        (then (i32.store (i32.const 12) (i32.load (i32.const 4)))))
      (if (i32.load (i32.const 4))
        (then (if (i32.ne (i32.load (i32.const 12)) (i32.const -1))
          (then (call $ret (i32.add (i32.const 16) (i32.load (i32.const 12)))) (return (i32.const 0))))))
      (i32.const 1)))
  (core instance $ri (instantiate $rm
    (with "" (instance (export "mem" (memory $cm)) (export "idx" (func $idx)) (export "new" (func $new))
                       (export "later" (func $later)) (export "ret" (func $ret))))))
  (alias core export $ri "run" (core func $runf))
  (alias core export $ri "cb" (core func $cbf))
  (type $rt (func async (result u32)))
  (func (export "run") (type $rt) (canon lift (core func $runf) async (callback $cbf)))
)
