;; xc-gated: a parent component with two concurrent tasks, each awaiting a NESTED child component's async
;; export; the child's work(2) itself awaits a test-controlled host import, so the test decides when caller
;; 2's result store happens (ADR 0101's result-store amendment). Caller 1 spins over caller 2's retptr while
;; executing and reports whether it changed: (changed << 16) | result. The parent's memory is `(memory 1 1)`,
;; heap-backed (#979).
(component
  (type $opsit (instance
    (type $opt (func async (param "x" u32) (result u32)))
    (export "op" (func (type $opt)))))
  (import "test:async/ops" (instance $ops (type $opsit)))
  (alias export $ops "op" (func $op))
  (component $child
    (type $copt (func async (param "x" u32) (result u32)))
    (import "op" (func $op (type $copt)))
    (core module $cmem (memory (export "m") 1))
    (core instance $cmemi (instantiate $cmem))
    (alias core export $cmemi "m" (core memory $chm))
    (core func $ret (canon task.return (result u32)))
    (core func $oplow (canon lower (func $op) async (memory $chm)))
    (core func $wsnew (canon waitable-set.new))
    (core func $wsjoin (canon waitable.join))
    (core module $cm
      (type $p1 (func (param i32)))
      (type $p2 (func (param i32 i32)))
      (type $p2r (func (param i32 i32) (result i32)))
      (type $r (func (result i32)))
      (type $cbt (func (param i32 i32 i32) (result i32)))
      (import "" "ret" (func $ret (type $p1)))
      (import "" "op" (func $op (type $p2r)))
      (import "" "wsnew" (func $wsnew (type $r)))
      (import "" "wsjoin" (func $wsjoin (type $p2)))
      (import "" "mem" (memory 1))
      ;; work(id): call the host's op(id) into retptr 0; if it resolved at once, return 100+id, else WAIT
      ;; on it and return 100+id from the callback. mem[8] keeps the id.
      (func (export "work") (param $id i32) (result i32)
        (local $packed i32) (local $si i32)
        (local.set $packed (call $op (local.get $id) (i32.const 0)))
        (if (i32.eq (i32.and (local.get $packed) (i32.const 15)) (i32.const 2))
          (then (call $ret (i32.add (i32.const 100) (local.get $id))) (return (i32.const 0))))
        ;; Only the WAIT path keeps state, at mem[12]: a concurrent work(1) resolves at once and never
        ;; writes it. (A first draft kept the id at one word on every path, and work(1) clobbered it.)
        (i32.store (i32.const 12) (local.get $id))
        (local.set $si (call $wsnew))
        (call $wsjoin (i32.shr_u (local.get $packed) (i32.const 4)) (local.get $si))
        (i32.or (i32.const 2) (i32.shl (local.get $si) (i32.const 4)))) ;; WAIT
      (func (export "cb") (type $cbt) (param i32 i32 i32) (result i32)
        (call $ret (i32.add (i32.const 100) (i32.load (i32.const 12))))
        (i32.const 0)))
    (core instance $ci (instantiate $cm
      (with "" (instance (export "ret" (func $ret)) (export "op" (func $oplow))
                         (export "wsnew" (func $wsnew)) (export "wsjoin" (func $wsjoin))
                         (export "mem" (memory $chm))))))
    (alias core export $ci "work" (core func $workf))
    (alias core export $ci "cb" (core func $cbf))
    (type $wt (func async (param "id" u32) (result u32)))
    (func (export "work") (type $wt) (canon lift (core func $workf) async (callback $cbf)))
  )
  (instance $c (instantiate $child (with "op" (func $op))))
  (alias export $c "work" (func $work))

  (core module $memmod (memory (export "m") 1 1))
  (core instance $memi (instantiate $memmod))
  (alias core export $memi "m" (core memory $cm))
  (core func $worklow (canon lower (func $work) async (memory $cm)))
  (core func $wsnew (canon waitable-set.new))
  (core func $wsjoin (canon waitable.join))
  (core func $wswait (canon waitable-set.wait (memory $cm)))
  (core func $ret (canon task.return (result u32)))
  (core module $pm
    (type $p2r (func (param i32 i32) (result i32)))
    (type $r (func (result i32)))
    (type $p1 (func (param i32)))
    (type $p2 (func (param i32 i32)))
    (type $cbt (func (param i32 i32 i32) (result i32)))
    (import "" "mem" (memory 1))
    (import "" "work" (func $work (type $p2r)))
    (import "" "wsnew" (func $wsnew (type $r)))
    (import "" "wsjoin" (func $wsjoin (type $p2)))
    (import "" "wswait" (func $wswait (type $p2r)))
    (import "" "ret" (func $ret (type $p1)))
    ;; Caller 2: start work(2) into retptr 16, join a set, and return WAIT on it: parked with no slot held.
    ;; Caller 1: work(1) into retptr 8 (waiting inside the callee if it did not resolve at once), then SPIN
    ;; over caller 2's retptr while executing, and report whether it changed: (changed << 16) | result.
    (func (export "run") (param $id i32) (result i32)
      (local $packed i32) (local $si i32) (local $i i32) (local $seen i32) (local $changed i32)
      (local.set $packed (call $work (local.get $id) (i32.shl (local.get $id) (i32.const 3))))
      (if (i32.eq (local.get $id) (i32.const 2))
        (then
          (if (i32.eq (i32.and (local.get $packed) (i32.const 15)) (i32.const 2))
            (then (call $ret (i32.load (i32.const 16))) (return (i32.const 0))))
          (local.set $si (call $wsnew))
          (call $wsjoin (i32.shr_u (local.get $packed) (i32.const 4)) (local.get $si))
          (return (i32.or (i32.const 2) (i32.shl (local.get $si) (i32.const 4)))))) ;; WAIT on si
      (if (i32.ne (i32.and (local.get $packed) (i32.const 15)) (i32.const 2))
        (then
          (local.set $si (call $wsnew))
          (call $wsjoin (i32.shr_u (local.get $packed) (i32.const 4)) (local.get $si))
          (drop (call $wswait (local.get $si) (i32.const 64)))))
      (local.set $seen (i32.load (i32.const 16)))
      (block $done
        (loop $again
          (if (i32.ne (i32.load (i32.const 16)) (local.get $seen))
            (then (local.set $changed (i32.const 1)) (br $done)))
          (local.set $i (i32.add (local.get $i) (i32.const 1)))
          (br_if $again (i32.lt_u (local.get $i) (i32.const 3000000)))))
      (call $ret (i32.or (i32.shl (local.get $changed) (i32.const 16)) (i32.load (i32.const 8))))
      (i32.const 0)) ;; EXIT
    ;; Caller 2's callback: its subtask resolved; return its result.
    (func (export "cb") (type $cbt) (param i32 i32 i32) (result i32)
      (call $ret (i32.load (i32.const 16)))
      (i32.const 0)))
  (core instance $pi (instantiate $pm
    (with "" (instance (export "mem" (memory $cm)) (export "work" (func $worklow))
                       (export "wsnew" (func $wsnew)) (export "wsjoin" (func $wsjoin))
                       (export "wswait" (func $wswait)) (export "ret" (func $ret))))))
  (alias core export $pi "run" (core func $runf))
  (alias core export $pi "cb" (core func $cbf))
  (type $rt (func async (param "id" u32) (result u32)))
  (func (export "run") (type $rt) (canon lift (core func $runf) async (callback $cbf)))
)
