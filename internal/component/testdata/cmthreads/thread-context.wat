;; thread-context (#1013, behind gate:cm-threads): a created Thread has its OWN context storage. `run` sets
;; context slot 0 to 5, creates and readies Thread T, and YIELDs until T has run. T stores its context.get(0)
;; at mem[4] and marks mem[0]. `run` returns T's reading. In the pin, context is per Thread
;; (`current_thread().storage`), and a new Thread's storage starts at [0,0], so T reads 0. Reading 5 would be
;; the task's storage shared with its Threads.
(component
  (core module $memmod (memory (export "m") 1 1))
  (core instance $memi (instantiate $memmod))
  (alias core export $memi "m" (core memory $cm))
  (core type $bodyt (func (param i32)))
  (core func $cget (canon context.get i32 0))
  (core func $cset (canon context.set i32 0))
  (core func $later (canon thread.resume-later))
  (core module $tmod
    (import "" "mem" (memory 1 1))
    (import "" "cget" (func $cget (result i32)))
    (table (export "t") 1 1 funcref)
    (func $t (param i32)
      (i32.store (i32.const 4) (call $cget))
      (i32.store (i32.const 0) (i32.const 1)))
    (elem (i32.const 0) $t))
  (core instance $ti (instantiate $tmod (with "" (instance (export "mem" (memory $cm)) (export "cget" (func $cget))))))
  (core func $new (canon thread.new-indirect (core type $bodyt) (core table $ti "t")))
  (core func $ret (canon task.return (result u32)))
  (core module $rm
    (import "" "mem" (memory 1 1))
    (import "" "cset" (func $cset (param i32)))
    (import "" "new" (func $new (param i32 i32) (result i32)))
    (import "" "later" (func $later (param i32)))
    (import "" "ret" (func $ret (param i32)))
    (func (export "run") (result i32)
      (call $cset (i32.const 5))
      (call $later (call $new (i32.const 0) (i32.const 0)))
      (i32.const 1)) ;; YIELD
    (func (export "cb") (param i32 i32 i32) (result i32)
      (if (i32.load (i32.const 0)) (then (call $ret (i32.load (i32.const 4))) (return (i32.const 0))))
      (i32.const 1)))
  (core instance $ri (instantiate $rm
    (with "" (instance (export "mem" (memory $cm)) (export "cset" (func $cset)) (export "new" (func $new))
                       (export "later" (func $later)) (export "ret" (func $ret))))))
  (alias core export $ri "run" (core func $runf))
  (alias core export $ri "cb" (core func $cbf))
  (type $rt (func async (result u32)))
  (func (export "run") (type $rt) (canon lift (core func $runf) async (callback $cbf)))
)
