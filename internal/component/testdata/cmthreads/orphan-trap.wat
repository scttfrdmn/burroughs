;; orphan-trap (ADR 0101's Option A, point 7; #999 item 2): a Thread traps AFTER its owner's call has
;; returned, so no call can surface the trap and it is kept in the internal `threadTable.orphanTraps`.
;; `run` creates Thread T (which executes `unreachable`), stores its index at mem[0], resolves with 1 and
;; EXITs, leaving T suspended. A later call to the sync `poke` resume-laters T, and T then runs and traps.
;;
;; No wasmtime reading: post-resolution execution is unobservable through `--invoke` (ADR 0100), and a
;; second call needs the same instance. This fixture exists for TestATrapAfterItsOwnerReturnedIsKept,
;; which reads the internal field.
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
  (core func $later (canon thread.resume-later))
  (core func $ret (canon task.return (result u32)))
  (core module $m
    (import "" "mem" (memory 1 1))
    (import "" "new" (func $new (param i32 i32) (result i32)))
    (import "" "later" (func $later (param i32)))
    (import "" "ret" (func $ret (param i32)))
    (func (export "run") (result i32)
      (i32.store (i32.const 0) (call $new (i32.const 0) (i32.const 0)))
      (call $ret (i32.const 1))
      (i32.const 0)) ;; EXIT, with T still suspended
    (func (export "cb") (param i32 i32 i32) (result i32) (i32.const 0))
    (func (export "poke") (result i32)
      (call $later (i32.load (i32.const 0)))
      (i32.const 0)))
  (core instance $mi (instantiate $m
    (with "" (instance (export "mem" (memory $cm)) (export "new" (func $new))
                       (export "later" (func $later)) (export "ret" (func $ret))))))
  (alias core export $mi "run" (core func $runf))
  (alias core export $mi "cb" (core func $cbf))
  (alias core export $mi "poke" (core func $pokef))
  (type $rt (func async (result u32)))
  (type $pt (func (result u32)))
  (func (export "run") (type $rt) (canon lift (core func $runf) async (callback $cbf)))
  (func (export "poke") (type $pt) (canon lift (core func $pokef)))
)
