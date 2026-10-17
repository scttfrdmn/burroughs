;; reenter-after-suspend: a callee PAST ITS FIRST SUSPENSION re-enters its caller's instance (the chair's ruling of
;; 2026-10-17 on ADR 0095's amendment: re-entry across the fence). The parent's async `go` async-lowers the
;; child's async `g` and WAITs on it. `g` YIELDs first, then, in its callback, calls the parent's SYNC `f`, a
;; lift the parent passes it, and returns f() + 1. Default path: no Threads, no host.
;;
;; Under the amendment's F1, `g`'s callback is a callee segment past its first suspension, so it holds the fence
;; on the parent edge, and `f` is guest execution of the parent's instance. The re-entry is on `g`'s own call
;; chain and must be admitted, or the call waits on a fence its own chain holds. Readings:
;; reenter-after-suspend.reading.
(component
  (core module $p1 (func (export "f") (result i32) i32.const 7))
  (core instance $p1i (instantiate $p1))
  (alias core export $p1i "f" (core func $ff))
  (type $ft (func (result u32)))
  (func $F (type $ft) (canon lift (core func $ff)))
  (component $child
    (import "f" (func $f (result u32)))
    (core func $fl (canon lower (func $f)))
    (core func $ret (canon task.return (result u32)))
    (core module $cm
      (import "" "f" (func $f (result i32)))
      (import "" "ret" (func $ret (param i32)))
      (func (export "g") (result i32) (i32.const 1)) ;; YIELD: the first suspension
      (func (export "cb") (param i32 i32 i32) (result i32)
        (call $ret (i32.add (call $f) (i32.const 1)))
        (i32.const 0))) ;; EXIT
    (core instance $ci (instantiate $cm (with "" (instance (export "f" (func $fl)) (export "ret" (func $ret))))))
    (alias core export $ci "g" (core func $gf))
    (alias core export $ci "cb" (core func $gcb))
    (type $gt (func async (result u32)))
    (func (export "g") (type $gt) (canon lift (core func $gf) async (callback $gcb))))
  (instance $c (instantiate $child (with "f" (func $F))))
  (alias export $c "g" (func $G))
  (core module $pmem (memory (export "m") 1 1))
  (core instance $pmemi (instantiate $pmem))
  (alias core export $pmemi "m" (core memory $pm))
  (core func $gl (canon lower (func $G) async (memory $pm)))
  (core func $wsnew (canon waitable-set.new))
  (core func $join (canon waitable.join))
  (core func $pret (canon task.return (result u32)))
  (core module $p2
    (import "" "m" (memory 1 1))
    (import "" "g" (func $g (param i32) (result i32)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "join" (func $join (param i32 i32)))
    (import "" "ret" (func $ret (param i32)))
    (global $ws (mut i32) (i32.const 0))
    (func (export "go") (result i32) (local $p i32)
      (local.set $p (call $g (i32.const 0))) ;; result at retptr 0
      (if (i32.eq (i32.and (local.get $p) (i32.const 15)) (i32.const 2))
        (then (call $ret (i32.load (i32.const 0))) (return (i32.const 0))))
      (global.set $ws (call $wsnew))
      (call $join (i32.shr_u (local.get $p) (i32.const 4)) (global.get $ws))
      (i32.or (i32.shl (global.get $ws) (i32.const 4)) (i32.const 2))) ;; WAIT
    (func (export "cb") (param i32 i32 i32) (result i32)
      (call $ret (i32.load (i32.const 0)))
      (i32.const 0)))
  (core instance $p2i (instantiate $p2 (with "" (instance (export "m" (memory $pm)) (export "g" (func $gl))
    (export "wsnew" (func $wsnew)) (export "join" (func $join)) (export "ret" (func $pret))))))
  (alias core export $p2i "go" (core func $gof))
  (alias core export $p2i "cb" (core func $pcb))
  (type $got (func async (result u32)))
  (func (export "go") (type $got) (canon lift (core func $gof) async (callback $pcb))))
