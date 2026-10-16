;; context-clobber (#1013 on the DEFAULT path, gate:cm-threads off): does another task of the instance see a
;; waiting async task's context? The parent's async `run` sets context slot 0 to 5, starts a nested child's
;; async `g`, and blocks in waitable-set.wait for it. `g` calls back into the parent's SYNC `peek`, which
;; returns context.get(0), and `g` returns that. In the pin, `peek` is a new task with its own Thread, whose
;; storage starts at [0,0], so it reads 0 and `run` returns 0. Reading 5 would be `run`'s context leaking
;; into another task.
(component
  (core module $memmod (memory (export "m") 1 1))
  (core instance $memi (instantiate $memmod))
  (alias core export $memi "m" (core memory $cm))
  (core func $cget (canon context.get i32 0))
  (core func $cset (canon context.set i32 0))

  ;; peek: the parent's SYNC export, returning its context slot 0
  (core module $pk
    (import "" "cget" (func $cget (result i32)))
    (func (export "peek") (result i32) (call $cget)))
  (core instance $pki (instantiate $pk (with "" (instance (export "cget" (func $cget))))))
  (alias core export $pki "peek" (core func $peekf))
  (type $pt (func (result u32)))
  (func $PEEK (type $pt) (canon lift (core func $peekf)))

  (component $child
    (import "peek" (func $peek (result u32)))
    (core func $pl (canon lower (func $peek)))
    (core func $ret (canon task.return (result u32)))
    (core module $gm
      (import "" "peek" (func $peek (result i32)))
      (import "" "ret" (func $ret (param i32)))
      (func (export "g") (result i32) (call $ret (call $peek)) (i32.const 0))
      (func (export "cb") (param i32 i32 i32) (result i32) unreachable))
    (core instance $gi (instantiate $gm (with "" (instance (export "peek" (func $pl)) (export "ret" (func $ret))))))
    (alias core export $gi "g" (core func $gf))
    (alias core export $gi "cb" (core func $gcbf))
    (type $gt (func async (result u32)))
    (func (export "g") (type $gt) (canon lift (core func $gf) async (callback $gcbf))))
  (instance $c (instantiate $child (with "peek" (func $PEEK))))
  (alias export $c "g" (func $G))

  (core func $glow (canon lower (func $G) async (memory $cm)))
  (core func $wsnew (canon waitable-set.new))
  (core func $wsjoin (canon waitable.join))
  (core func $wswait (canon waitable-set.wait (memory $cm)))
  (core func $ret (canon task.return (result u32)))
  (core module $rm
    (import "" "mem" (memory 1 1))
    (import "" "cset" (func $cset (param i32)))
    (import "" "g" (func $g (param i32) (result i32)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "wsjoin" (func $wsjoin (param i32 i32)))
    (import "" "wswait" (func $wswait (param i32 i32) (result i32)))
    (import "" "ret" (func $ret (param i32)))
    (func (export "run") (result i32) (local $p i32) (local $s i32)
      (call $cset (i32.const 5))
      (local.set $p (call $g (i32.const 16)))
      (if (i32.ne (i32.and (local.get $p) (i32.const 15)) (i32.const 2))
        (then
          (local.set $s (call $wsnew))
          (call $wsjoin (i32.shr_u (local.get $p) (i32.const 4)) (local.get $s))
          (drop (call $wswait (local.get $s) (i32.const 32)))))
      (call $ret (i32.load (i32.const 16)))
      (i32.const 0))
    (func (export "cb") (param i32 i32 i32) (result i32) unreachable))
  (core instance $ri (instantiate $rm
    (with "" (instance (export "mem" (memory $cm)) (export "cset" (func $cset)) (export "g" (func $glow))
                       (export "wsnew" (func $wsnew)) (export "wsjoin" (func $wsjoin)) (export "wswait" (func $wswait))
                       (export "ret" (func $ret))))))
  (alias core export $ri "run" (core func $runf))
  (alias core export $ri "cb" (core func $cbf))
  (type $rt (func async (result u32)))
  (func (export "run") (type $rt) (canon lift (core func $runf) async (callback $cbf)))
)
