;; task-return-early: a cross-component callee resolves its caller at task.return, and runs on to its exit after
;; it (E2-in-a, #1384). The parent's async `go(row)` async-lowers the child's async `g(row)`, reads g's result
;; when its subtask is RETURNED, and then asks the child, through its sync `exited`, whether g's callback has
;; reached its EXIT yet. Default path: gate:async on, no host, no Threads. Readings: task-return-early.reading.
;;
;; The child's memory: [0] released (its sync `release` sets it), [4] exited (g's EXIT sets it), [8] the row,
;; [12] whether row 1's first callback has returned.
;;
;;   row 0  early         g calls task.return(7) in its first call, then YIELDs until released
;;   row 1  later         g YIELDs, its first callback calls task.return(7), then it YIELDs until released
;;   row 2  trap-after    g calls task.return(7) in its first call and YIELDs; its callback traps
;;   row 3  twice         g calls task.return twice in its first call
;;   row 4  no-return     g EXITs without calling task.return
;;   row 5  at-exit       g YIELDs; its callback calls task.return(7) and EXITs (the case that already worked)
;;   row 6  cancel-poll   g YIELDs until its callback is handed TASK_CANCELLED, then calls task.return(7) and EXITs
;;
;; go's result, for rows 0, 1 and 5: g's result | (the lower's state << 8) | (exited when the result was read
;; << 12) | (exited at the end << 16). For row 0 and row 1 the parent releases the child once it has the result
;; and then YIELDs until `exited` reads 1, so bit 16 is the callee running on to its exit after its return.
;; For row 2 the parent WAITs on an empty set once it has the result, so the child's trap is what ends it.
;;
;; Row 6 is the cancellation hand-back (upstream #726): the parent cancels g's subtask with the ASYNC
;; subtask.cancel and then, without suspending, polls the child's sync `exited` until it reads 1. It then takes
;; the result, from the cancel's own RETURNED or, if the cancel returned BLOCKED, from the subtask's event once it
;; WAITs, and returns g's result | (the final state << 8) | (1 << 12). The cancel's own status is not in the value.
;; The pin resumes g inside the cancel, so it reads RETURNED there, and Burroughs returns BLOCKED and runs g
;; beside the parent (#999), so only what both must agree on is packed.
(component
  (component $child
    (core module $cmem (memory (export "m") 1 1))
    (core instance $cmemi (instantiate $cmem))
    (alias core export $cmemi "m" (core memory $chm))
    (core func $ret (canon task.return (result u32)))
    (core module $cm
      (import "" "ret" (func $ret (param i32)))
      (import "" "mem" (memory 1 1))
      (func $exit (result i32) (i32.store (i32.const 4) (i32.const 1)) (i32.const 0)) ;; EXIT, marked
      (func (export "g") (param $row i32) (result i32)
        (i32.store (i32.const 8) (local.get $row))
        (if (i32.eqz (local.get $row)) (then (call $ret (i32.const 7)) (return (i32.const 1))))
        (if (i32.eq (local.get $row) (i32.const 1)) (then (return (i32.const 1))))
        (if (i32.eq (local.get $row) (i32.const 2)) (then (call $ret (i32.const 7)) (return (i32.const 1))))
        (if (i32.eq (local.get $row) (i32.const 3))
          (then (call $ret (i32.const 7)) (call $ret (i32.const 8)) (return (call $exit))))
        (if (i32.eq (local.get $row) (i32.const 4)) (then (return (call $exit))))
        (i32.const 1)) ;; row 5: YIELD
      (func (export "cb") (param i32 i32 i32) (result i32) (local $row i32)
        (local.set $row (i32.load (i32.const 8)))
        (if (i32.eq (local.get $row) (i32.const 2)) (then unreachable))
        (if (i32.eq (local.get $row) (i32.const 5)) (then (call $ret (i32.const 7)) (return (call $exit))))
        (if (i32.eq (local.get $row) (i32.const 6)) ;; TASK_CANCELLED is event code 6
          (then (if (i32.eq (local.get 0) (i32.const 6)) (then (call $ret (i32.const 7)) (return (call $exit))))
                (return (i32.const 1))))
        (if (i32.eq (local.get $row) (i32.const 1))
          (then (if (i32.eqz (i32.load (i32.const 12)))
            (then (i32.store (i32.const 12) (i32.const 1)) (call $ret (i32.const 7)) (return (i32.const 1))))))
        (if (i32.load (i32.const 0)) (then (return (call $exit))))
        (i32.const 1)) ;; YIELD until released
      (func (export "release") (i32.store (i32.const 0) (i32.const 1)))
      (func (export "exited") (result i32) (i32.load (i32.const 4))))
    (core instance $ci (instantiate $cm (with "" (instance (export "ret" (func $ret)) (export "mem" (memory $chm))))))
    (alias core export $ci "g" (core func $gf))
    (alias core export $ci "cb" (core func $cbf))
    (alias core export $ci "release" (core func $relf))
    (alias core export $ci "exited" (core func $exf))
    (type $gt (func async (param "row" u32) (result u32)))
    (type $relt (func))
    (type $ext (func (result u32)))
    (func (export "g") (type $gt) (canon lift (core func $gf) async (callback $cbf)))
    (func (export "release") (type $relt) (canon lift (core func $relf)))
    (func (export "exited") (type $ext) (canon lift (core func $exf))))
  (instance $c (instantiate $child))
  (alias export $c "g" (func $G))
  (alias export $c "release" (func $REL))
  (alias export $c "exited" (func $EXD))
  (core module $pmem (memory (export "m") 1 1))
  (core instance $pmemi (instantiate $pmem))
  (alias core export $pmemi "m" (core memory $pm))
  (core func $gl (canon lower (func $G) async (memory $pm)))
  (core func $rell (canon lower (func $REL)))
  (core func $exl (canon lower (func $EXD)))
  (core func $wsnew (canon waitable-set.new))
  (core func $join (canon waitable.join))
  (core func $acancel (canon subtask.cancel async))
  (core func $pret (canon task.return (result u32)))
  (core module $p2
    (import "" "m" (memory 1 1))
    (import "" "g" (func $g (param i32 i32) (result i32)))
    (import "" "release" (func $release))
    (import "" "exited" (func $exited (result i32)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "join" (func $join (param i32 i32)))
    (import "" "cancel" (func $cancel (param i32) (result i32)))
    (import "" "ret" (func $ret (param i32)))
    ;; [0] g's result (the retptr), [16] the row, [20] the lower's state, [24] exited when the result was read,
    ;; [28] the phase: 0 waiting on g's subtask, 1 waiting for g's exit, 2 row 6's wait on g's subtask
    (func $cancelpoll (param $p i32) (result i32) (local $c i32) (local $s i32) ;; row 6
      (local.set $c (call $cancel (i32.shr_u (local.get $p) (i32.const 4))))
      (block $done (loop $poll (br_if $done (call $exited)) (br $poll))) ;; no suspension while it polls
      (if (i32.eq (local.get $c) (i32.const 2))
        (then (call $ret (i32.or (i32.load (i32.const 0)) (i32.const 0x1200))) (return (i32.const 0))))
      (local.set $s (call $wsnew))
      (call $join (i32.shr_u (local.get $p) (i32.const 4)) (local.get $s))
      (i32.store (i32.const 28) (i32.const 2))
      (i32.or (i32.shl (local.get $s) (i32.const 4)) (i32.const 2))) ;; WAIT on g's subtask
    (func $result (result i32) ;; g's result is in hand
      (i32.store (i32.const 24) (call $exited))
      (if (i32.eq (i32.load (i32.const 16)) (i32.const 2))
        (then (return (i32.or (i32.shl (call $wsnew) (i32.const 4)) (i32.const 2))))) ;; WAIT on an empty set
      (call $release)
      (i32.store (i32.const 28) (i32.const 1))
      (i32.const 1)) ;; YIELD, then poll exited
    (func (export "go") (param $row i32) (result i32) (local $p i32) (local $s i32)
      (i32.store (i32.const 16) (local.get $row))
      (local.set $p (call $g (local.get $row) (i32.const 0)))
      (i32.store (i32.const 20) (i32.and (local.get $p) (i32.const 15)))
      (if (i32.eq (local.get $row) (i32.const 6)) (then (return (call $cancelpoll (local.get $p)))))
      (if (i32.eq (i32.and (local.get $p) (i32.const 15)) (i32.const 2)) (then (return (call $result))))
      (local.set $s (call $wsnew))
      (call $join (i32.shr_u (local.get $p) (i32.const 4)) (local.get $s))
      (i32.or (i32.shl (local.get $s) (i32.const 4)) (i32.const 2))) ;; WAIT on g's subtask
    (func (export "cb") (param $ev i32) (param $p1 i32) (param $p2 i32) (result i32)
      (if (i32.eq (i32.load (i32.const 28)) (i32.const 2)) ;; row 6: g's subtask event
        (then
          (if (i32.ne (local.get $p2) (i32.const 2))
            (then (call $ret (i32.or (i32.const 0xbad00) (local.get $p2))) (return (i32.const 0))))
          (call $ret (i32.or (i32.load (i32.const 0)) (i32.const 0x1200)))
          (return (i32.const 0))))
      (if (i32.eqz (i32.load (i32.const 28)))
        (then
          ;; g's subtask event: anything but RETURNED is reported as 0xbad00 | its state
          (if (i32.ne (local.get $p2) (i32.const 2))
            (then (call $ret (i32.or (i32.const 0xbad00) (local.get $p2))) (return (i32.const 0))))
          (return (call $result))))
      (if (call $exited)
        (then
          (call $ret (i32.or (i32.or (i32.load (i32.const 0)) (i32.shl (i32.load (i32.const 20)) (i32.const 8)))
            (i32.or (i32.shl (i32.load (i32.const 24)) (i32.const 12)) (i32.const 0x10000))))
          (return (i32.const 0))))
      (i32.const 1)))
  (core instance $p2i (instantiate $p2 (with "" (instance (export "m" (memory $pm)) (export "g" (func $gl))
    (export "release" (func $rell)) (export "exited" (func $exl))
    (export "wsnew" (func $wsnew)) (export "join" (func $join)) (export "cancel" (func $acancel))
    (export "ret" (func $pret))))))
  (alias core export $p2i "go" (core func $gof))
  (alias core export $p2i "cb" (core func $pcb))
  (type $got (func async (param "row" u32) (result u32)))
  (func (export "go") (type $got) (canon lift (core func $gof) async (callback $pcb))))
