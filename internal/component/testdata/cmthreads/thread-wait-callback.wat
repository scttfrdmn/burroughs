;; thread-wait-callback: a created Thread parks in waitable-set.wait on a call into a NESTED child, and the
;; child services it by calling back into a lift the parent passed it (#977). wasmtime 49.0.2: 8 (7 from the
;; parent's f, +1 in the child's g), 10 of 10.
(component
  ;; P1: the parent's own func `f`, lifted and passed to the child.
  (core module $p1 (func (export "f") (result i32) i32.const 7))
  (core instance $p1i (instantiate $p1))
  (alias core export $p1i "f" (core func $ff))
  (type $ft (func (result u32)))
  (func $F (type $ft) (canon lift (core func $ff)))

  ;; The child: its async `g` calls the parent's `f` synchronously and returns f() + 1.
  (component $child
    (import "f" (func $f (result u32)))
    (core func $fl (canon lower (func $f)))
    (core func $ret (canon task.return (result u32)))
    (core module $cm
      (import "" "f" (func $f (result i32)))
      (import "" "ret" (func $ret (param i32)))
      (func (export "g") (result i32)
        (call $ret (i32.add (call $f) (i32.const 1)))
        (i32.const 0)) ;; EXIT
      (func (export "cb") (param i32 i32 i32) (result i32) unreachable))
    (core instance $ci (instantiate $cm
      (with "" (instance (export "f" (func $fl)) (export "ret" (func $ret))))))
    (alias core export $ci "g" (core func $gf))
    (alias core export $ci "cb" (core func $cbf))
    (type $gt (func async (result u32)))
    (func (export "g") (type $gt) (canon lift (core func $gf) async (callback $cbf))))
  (instance $c (instantiate $child (with "f" (func $F))))
  (alias export $c "g" (func $G))

  ;; P2: the parent's async `run`. It spawns Thread T and YIELDs until T is done. T async-lowers the
  ;; child's `g`, parks in waitable-set.wait for it, and records the result.
  (core module $memmod (memory (export "m") 1 1))
  (core instance $memi (instantiate $memmod))
  (alias core export $memi "m" (core memory $cm))
  (core type $bodyt (func (param i32)))
  (core func $glow (canon lower (func $G) async (memory $cm)))
  (core func $wsnew (canon waitable-set.new))
  (core func $wsjoin (canon waitable.join))
  (core func $wswait (canon waitable-set.wait (memory $cm)))
  (core module $bodymod
    (import "" "mem" (memory 1))
    (import "" "g" (func $g (param i32) (result i32)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "wsjoin" (func $wsjoin (param i32 i32)))
    (import "" "wswait" (func $wswait (param i32 i32) (result i32)))
    (table (export "t") 1 1 funcref)
    ;; T: call g into retptr 16; if it did not resolve at once, join a set and wait on it. mem[8] = done.
    (func $body (param i32)
      (local $packed i32) (local $si i32)
      (local.set $packed (call $g (i32.const 16)))
      (if (i32.ne (i32.and (local.get $packed) (i32.const 15)) (i32.const 2))
        (then
          (local.set $si (call $wsnew))
          (call $wsjoin (i32.shr_u (local.get $packed) (i32.const 4)) (local.get $si))
          (drop (call $wswait (local.get $si) (i32.const 32)))))
      (i32.store (i32.const 8) (i32.const 1)))
    (elem (i32.const 0) $body))
  (core instance $bodyi (instantiate $bodymod
    (with "" (instance (export "mem" (memory $cm)) (export "g" (func $glow))
                       (export "wsnew" (func $wsnew)) (export "wsjoin" (func $wsjoin))
                       (export "wswait" (func $wswait))))))
  (core func $new (canon thread.new-indirect (core type $bodyt) (core table $bodyi "t")))
  (core func $later (canon thread.resume-later))
  (core func $taskret (canon task.return (result u32)))
  (core module $runmod
    (import "" "mem" (memory 1))
    (import "" "new" (func $new (param i32 i32) (result i32)))
    (import "" "later" (func $later (param i32)))
    (import "" "ret" (func $ret (param i32)))
    (func (export "callee") (result i32)
      (call $later (call $new (i32.const 0) (i32.const 0)))
      (i32.const 1)) ;; YIELD
    (func (export "cb") (param i32 i32 i32) (result i32)
      (if (i32.load (i32.const 8))
        (then (call $ret (i32.load (i32.const 16))) (return (i32.const 0)))) ;; EXIT with T's result
      (i32.const 1))) ;; YIELD until T is done
  (core instance $runi (instantiate $runmod
    (with "" (instance (export "mem" (memory $cm)) (export "new" (func $new))
                       (export "later" (func $later)) (export "ret" (func $taskret))))))
  (alias core export $runi "callee" (core func $calleef))
  (alias core export $runi "cb" (core func $runcbf))
  (type $rt (func async (result u32)))
  (func (export "run") (type $rt) (canon lift (core func $calleef) async (callback $runcbf)))
)
