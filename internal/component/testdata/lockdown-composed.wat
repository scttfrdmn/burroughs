;; lockdown-composed (#1040): a trap in the parent locks the whole store, so a task running in a COMPOSED child
;; instance ends at its next canon built-in.
;;
;; The child's async `g` never finishes on its own: each callback turn calls `context.get` (a canon built-in)
;; and YIELDs. The parent's async `go` starts `g` and waits on its subtask. The parent's sync `boom` traps.
;; The host calls `go` (it parks), then `boom`. With the lock shared across the composition, `g`'s next
;; `context.get` is refused and the child's task ends. With the lock scoped to one instance, `g` yields forever.
(component
  (component $child
    (core func $ret (canon task.return (result u32)))
    (core func $cget (canon context.get i32 0))
    (core module $cm
      (import "" "ret" (func $ret (param i32)))
      (import "" "cget" (func $cget (result i32)))
      (func (export "g") (result i32) (i32.const 1)) ;; YIELD
      (func (export "cb") (param i32 i32 i32) (result i32)
        (drop (call $cget)) ;; a canon built-in on every turn
        (i32.const 1)))     ;; YIELD, forever
    (core instance $ci (instantiate $cm (with "" (instance (export "ret" (func $ret)) (export "cget" (func $cget))))))
    (alias core export $ci "g" (core func $gf))
    (alias core export $ci "cb" (core func $cbf))
    (type $gt (func async (result u32)))
    (func (export "g") (type $gt) (canon lift (core func $gf) async (callback $cbf))))
  (instance $c (instantiate $child))
  (alias export $c "g" (func $g))

  (core module $memmod (memory (export "m") 1 1))
  (core instance $memi (instantiate $memmod))
  (alias core export $memi "m" (core memory $pm))
  (core func $glow (canon lower (func $g) async (memory $pm)))
  (core func $wsnew (canon waitable-set.new))
  (core func $wsjoin (canon waitable.join))
  (core func $ret (canon task.return (result u32)))
  (core module $pmod
    (import "" "mem" (memory 1 1))
    (import "" "g" (func $g (param i32) (result i32)))
    (import "" "wsnew" (func $wsnew (result i32)))
    (import "" "wsjoin" (func $wsjoin (param i32 i32)))
    (import "" "ret" (func $ret (param i32)))
    ;; go: start g into retptr 32, join its subtask to a fresh set, and WAIT on the set.
    (func (export "go") (result i32) (local $p i32) (local $s i32)
      (local.set $p (call $g (i32.const 32)))
      (local.set $s (call $wsnew))
      (call $wsjoin (i32.shr_u (local.get $p) (i32.const 4)) (local.get $s))
      (i32.or (i32.const 2) (i32.shl (local.get $s) (i32.const 4)))) ;; WAIT on s
    (func (export "cb") (param i32 i32 i32) (result i32)
      (call $ret (i32.const 1))
      (i32.const 0)) ;; EXIT once g's subtask reports
    (func (export "boom") (result i32) unreachable))
  (core instance $pi (instantiate $pmod
    (with "" (instance (export "mem" (memory $pm)) (export "g" (func $glow)) (export "wsnew" (func $wsnew))
                       (export "wsjoin" (func $wsjoin)) (export "ret" (func $ret))))))
  (alias core export $pi "go" (core func $gof))
  (alias core export $pi "cb" (core func $pcbf))
  (alias core export $pi "boom" (core func $boomf))
  (type $at (func async (result u32)))
  (type $st (func (result u32)))
  (func (export "go") (type $at) (canon lift (core func $gof) async (callback $pcbf)))
  (func (export "boom") (type $st) (canon lift (core func $boomf)))
)
