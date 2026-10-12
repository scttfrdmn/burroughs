;; suspend-parked: a created Thread that SUSPENDS PARTWAY and is never resumed.
;;
;; The witness for the second parked shape ADR 0100's handoff amendment names: a Thread parked inside a
;; handoff, mid-guest, which the second build slice's wake did not reach — it ended only Threads that
;; never started. The body ORs bit 1 into mem[0] and calls `thread.suspend`; the callback re-YIELDs until
;; bit 1 is set, then resolves with mem[0] and EXITs, leaving the body suspended and still registered
;; (legal: Threads outlive resolution, ADR 0100). The reading is 2, and bit 2 — set only if the body were
;; resumed — is clear.
(component
  (core module $memmod (memory (export "m") 1))
  (core instance $memi (instantiate $memmod))
  (alias core export $memi "m" (core memory $cm))
  (core type $bodyt (func (param i32)))
  (core func $susp (canon thread.suspend))
  (core module $bodymod
    (type $b (func (param i32)))
    (type $r (func (result i32)))
    (import "" "susp" (func $susp (type $r)))
    (import "" "mem" (memory 1))
    (table (export "t") 1 1 funcref)
    (func $body (type $b) (param i32)
      (i32.store (i32.const 0) (i32.or (i32.load (i32.const 0)) (i32.const 2)))
      (drop (call $susp))
      (i32.store (i32.const 0) (i32.or (i32.load (i32.const 0)) (i32.const 4))))
    (elem (i32.const 0) $body)
  )
  (core instance $bodyi (instantiate $bodymod
    (with "" (instance (export "susp" (func $susp)) (export "mem" (memory $cm))))))
  (core func $new (canon thread.new-indirect (core type $bodyt) (core table $bodyi "t")))
  (core func $later (canon thread.resume-later))
  (core func $taskret (canon task.return (result u32)))
  (core module $runmod
    (type $p1 (func (param i32)))
    (type $p2r (func (param i32 i32) (result i32)))
    (type $cbt (func (param i32 i32 i32) (result i32)))
    (import "" "new"     (func $new     (type $p2r)))
    (import "" "later"   (func $later   (type $p1)))
    (import "" "taskret" (func $taskret (type $p1)))
    (import "" "mem"     (memory 1))
    (func (export "callee") (param i32) (result i32)
      (i32.store (i32.const 72) (call $new (i32.const 0) (i32.const 0)))
      (call $later (i32.load (i32.const 72)))
      (i32.const 1)) ;; YIELD
    (func (export "cb") (type $cbt) (param i32 i32 i32) (result i32)
      (if (i32.and (i32.load (i32.const 0)) (i32.const 2))
        (then (call $taskret (i32.load (i32.const 0))) (return (i32.const 0)))) ;; EXIT
      (i32.const 1)) ;; YIELD
  )
  (core instance $runi (instantiate $runmod
    (with "" (instance (export "new" (func $new)) (export "later" (func $later))
                       (export "taskret" (func $taskret)) (export "mem" (memory $cm))))))
  (core func $calleef (alias core export $runi "callee"))
  (core func $cbf (alias core export $runi "cb"))
  (type $rt (func async (param "id" u32) (result u32)))
  (func $run (type $rt) (canon lift (core func $calleef) async (callback $cbf)))
  (export "run" (func $run))
)
