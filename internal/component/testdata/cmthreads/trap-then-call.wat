;; trap-then-call (#1040; the chair's second ruling of 2026-10-20): what an ordinary call sees after an
;; earlier call on the same instance trapped in a Thread that is not its task's main thread.
;;
;; `run` (sync) creates Thread T, whose body executes `unreachable`, and switches to it with
;; thread.yield-then-resume, so T traps while run's call is in progress. `ok` (sync) returns 7 and
;; touches no Thread. The reading is what the host's SECOND call, `ok`, does: refused, trapped, or 7.
;;
;; yield-then-resume rather than suspend-then-resume, so run's main Thread stays READY: with
;; suspend-then-resume the control (T returns) left it suspended and the sync task trapped with nothing
;; runnable, which is a different trap from the one under test.
;;
;; No Thread waits (no waitable-set.wait, no subtask), so this fixture stays off #1045's wasmtime path.
(component
  (core module $memmod (memory (export "m") 1 1))
  (core instance $memi (instantiate $memmod))
  (alias core export $memi "m" (core memory $cm))
  (core type $bodyt (func (param i32)))
  (core module $tmod
    (table (export "t") 1 1 funcref)
    (func $t (param i32) unreachable)
    (elem (i32.const 0) $t))
  (core instance $ti (instantiate $tmod))
  (core func $new (canon thread.new-indirect (core type $bodyt) (core table $ti "t")))
  (core func $yresume (canon thread.yield-then-resume))
  (core module $m
    (import "" "mem" (memory 1 1))
    (import "" "new" (func $new (param i32 i32) (result i32)))
    (import "" "yresume" (func $yresume (param i32) (result i32)))
    (func (export "run") (result i32)
      (drop (call $yresume (call $new (i32.const 0) (i32.const 0))))
      (i32.const 1)) ;; not reached: T traps first
    (func (export "ok") (result i32) (i32.const 7)))
  (core instance $mi (instantiate $m
    (with "" (instance (export "mem" (memory $cm)) (export "new" (func $new))
                       (export "yresume" (func $yresume))))))
  (alias core export $mi "run" (core func $runf))
  (alias core export $mi "ok" (core func $okf))
  (type $ft (func (result u32)))
  (func (export "run") (type $ft) (canon lift (core func $runf)))
  (func (export "ok") (type $ft) (canon lift (core func $okf)))
)
